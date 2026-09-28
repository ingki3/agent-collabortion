package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
)

// T-CTX0 (plan/research/CONTEXT_MEMORY.md 0단계): the heartbeat and finish
// halves of task_context_metric. The claim writes the row (queue/
// context_metric.go); these only UPDATE it, so an attempt dispatched before
// the migration — or one whose claim-time insert failed — simply has no row
// and nothing here happens.
//
// Measurement never costs the report it rides on: each write runs under its
// own savepoint and an error is rolled back to it and dropped.

// metricSampleCap bounds the per-attempt heartbeat series: 240 samples at the
// 15s heartbeat is an hour of turn, and a sample is only kept when a number
// moved.
const metricSampleCap = 240

func withMetricSavepoint(ctx context.Context, tx pgx.Tx, fn func() error) {
	if _, err := tx.Exec(ctx, `SAVEPOINT context_metric`); err != nil {
		return
	}
	if err := fn(); err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT context_metric`)
		return
	}
	_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT context_metric`)
}

// recordMetricSample appends one heartbeat's running usage to the attempt's
// series: [seconds since claim, cache_read, cache_write, input]. Successive
// cache_read values are what the turn's API calls re-read — the growth
// between two samples is the context the calls in between carried.
func recordMetricSample(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int, u contracts.Usage, now time.Time) {
	withMetricSavepoint(ctx, tx, func() error {
		_, err := tx.Exec(ctx, `
			UPDATE task_context_metric m
			SET samples = m.samples || jsonb_build_array(jsonb_build_array(
			      round(extract(epoch FROM ($3::timestamptz - m.claimed_at))::numeric, 1), $4::bigint, $5::bigint, $6::bigint))
			WHERE m.task_id = $1 AND m.attempt = $2
			  AND jsonb_array_length(m.samples) < $7
			  AND (jsonb_array_length(m.samples) = 0
			       OR (m.samples->-1->>1)::bigint <> $4 OR (m.samples->-1->>2)::bigint <> $5 OR (m.samples->-1->>3)::bigint <> $6)`,
			taskID, attempt, now, u.CacheReadTokens, u.CacheWriteTokens, u.InputTokens, metricSampleCap)
		return err
	})
}

// recordMetricFinish fills the finish half: whether the runtime resumed, how
// many turns of the same runtime session came before this one, the turn's
// usage including cache_write (task_usage has no column for it), and the
// tool calls the attempt's events show, by kind.
//
// session_depth counts earlier metric rows that finished on the same runtime
// session id — a cold start opens a new id, so it reads 0 there by itself.
// Rows written before this table existed are not counted (the depth of a
// session already running at migration time starts low).
func recordMetricFinish(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, attempt int, resumed *bool, f contracts.Finish, now time.Time) {
	// The session the turn ran on: the ref finish reports, else — for a
	// resumed turn that reported none — the one the bundle asked to resume.
	var sessionID *string
	if f.RuntimeSessionRef != nil && f.RuntimeSessionRef.SessionID != "" {
		s := f.RuntimeSessionRef.SessionID
		sessionID = &s
	}
	// An empty finish usage is "no information" (Finish's own rule): the
	// last heartbeat sample stands in for the token columns then.
	var in, out, cr, cw *int64
	if u := f.Usage; u.InputTokens != 0 || u.OutputTokens != 0 || u.CacheReadTokens != 0 || u.CacheWriteTokens != 0 {
		in, out, cr, cw = &u.InputTokens, &u.OutputTokens, &u.CacheReadTokens, &u.CacheWriteTokens
	}
	withMetricSavepoint(ctx, tx, func() error {
		_, err := tx.Exec(ctx, `
			WITH calls AS (
				SELECT DISTINCT ON (e.payload->>'tool_call_id')
				       CASE WHEN e.verb = 'use_tool' THEN COALESCE(NULLIF(split_part(e.payload->>'title', ' ', 1), ''), 'use_tool')
				            ELSE e.verb END AS kind
				FROM task_event e
				WHERE e.task_id = $1 AND e.attempt = $2 AND e.class = 'tool' AND e.verb <> 'permission'
				  AND e.payload ? 'tool_call_id'
				ORDER BY e.payload->>'tool_call_id', e.seq DESC
			),
			kinds AS (SELECT kind, count(*)::int AS n FROM calls GROUP BY kind)
			UPDATE task_context_metric m SET
			  resumed = $3,
			  finish_session_id = COALESCE($4, CASE WHEN $3 THEN m.resume_session_id END),
			  session_depth = CASE WHEN COALESCE($4, CASE WHEN $3 THEN m.resume_session_id END) IS NULL THEN NULL ELSE (
			      SELECT count(*)::int FROM task_context_metric p
			      WHERE p.finish_session_id = COALESCE($4, CASE WHEN $3 THEN m.resume_session_id END) AND NOT (p.task_id = $1 AND p.attempt = $2)
			        AND p.finished_at IS NOT NULL AND p.finished_at <= $9) END,
			  input_tokens = COALESCE($5, (m.samples->-1->>3)::bigint),
			  output_tokens = $6,
			  cache_read = COALESCE($7, (m.samples->-1->>1)::bigint),
			  cache_write = COALESCE($8, (m.samples->-1->>2)::bigint),
			  tool_calls = COALESCE((SELECT sum(n)::int FROM kinds), 0),
			  tool_kinds = COALESCE((SELECT jsonb_object_agg(kind, n) FROM kinds), '{}'::jsonb),
			  finished_at = $9
			WHERE m.task_id = $1 AND m.attempt = $2`,
			taskID, attempt, resumed, sessionID, in, out, cr, cw, now)
		return err
	})
}
