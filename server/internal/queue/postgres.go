package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Postgres implements Queue on the task table (daemon-protocol §4.1 rules).
type Postgres struct {
	DB       *pgxpool.Pool
	Clock    clock.Clock
	Tasks    *tasks.Service
	Notifier *Notifier
}

var _ Queue = (*Postgres)(nil)

func NewPostgres(pool *pgxpool.Pool, c clock.Clock, t *tasks.Service, n *Notifier) *Postgres {
	return &Postgres{DB: pool, Clock: c, Tasks: t, Notifier: n}
}

// Claim hands out up to capacity queued tasks to runtimeID:
//   - only sessions fixed to this runtime (E11-09); a `none` session with no
//     runtime is fixed to the first claimer (E11-10) — but only a runtime of
//     the session's own workspace is ever a candidate (FR-2.1 M10, FR-1.9):
//     a daemon paired to workspace B must never claim, and thereby pin,
//     workspace A's session
//   - session must be active (paused → nothing, E5-04)
//   - not_before must have passed (rate_limited)
//   - one in-flight task per lane (FR-6.3)
//
// Each claimed task moves queued → dispatched and gets a fresh task token.
func (p *Postgres) Claim(ctx context.Context, runtimeID string, capacity int, now time.Time) ([]contracts.TaskBundle, error) {
	rt, err := uuid.Parse(runtimeID)
	if err != nil {
		return nil, fmt.Errorf("queue: runtime id: %w", err)
	}
	if capacity <= 0 {
		return []contracts.TaskBundle{}, nil
	}
	tx, err := p.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// FR-6.3's four concurrency layers plus the DAG gate, all as guards on the
	// one query so a claim can never overshoot between SELECT and UPDATE.
	//
	// `waiting_human` and `blocked` are absent from every count on purpose
	// (t-1): both processes have already exited, so holding a slot for them
	// stalls the session while nothing runs.
	//
	// `worktree` isolation is the fifth, implicit limit — the same agent's
	// lanes share one checkout (FR-6.1), so they must run one at a time or two
	// turns write the same files.
	rows, err := tx.Query(ctx, `
		WITH busy AS (
			SELECT id, session_id, agent_id, lane_id, runtime_id FROM task
			WHERE status IN ('dispatched', 'preparing', 'running')
		)
		SELECT t.id FROM task t
		  JOIN session s ON s.id = t.session_id
		  JOIN lane l ON l.id = t.lane_id
		  JOIN agent a ON a.id = t.agent_id
		  JOIN runtime r ON r.id = $1
		  JOIN workspace_settings cfg ON cfg.workspace_id = r.workspace_id
		WHERE t.status = 'queued'
		  AND s.status = 'active'
		  AND s.workspace_id = r.workspace_id
		  AND (s.runtime_id = r.id OR (s.runtime_id IS NULL AND s.isolation->>'kind' = 'none'))
		  AND (t.not_before IS NULL OR t.not_before <= $2)
		  AND NOT EXISTS (SELECT 1 FROM busy b WHERE b.lane_id = t.lane_id)
		  -- FR-6.2: a lane waits for every lane it depends on to end.
		  AND NOT EXISTS (
		        SELECT 1 FROM lane d WHERE d.id = ANY (l.depends_on)
		          AND d.status NOT IN ('done', 'failed', 'blocked'))
		  -- 1. session: max_parallel_lanes (default 5)
		  AND (SELECT count(DISTINCT b.lane_id) FROM busy b WHERE b.session_id = t.session_id)
		      < COALESCE((s.limits->>'max_parallel_lanes')::int, 5)
		  -- 2. agent: max_concurrent_tasks
		  AND (SELECT count(*) FROM busy b WHERE b.agent_id = t.agent_id) < a.max_concurrent_tasks
		  -- 3. runtime (daemon) global cap
		  AND (SELECT count(*) FROM busy b WHERE b.runtime_id = r.id)
		      < COALESCE((cfg.runtime_policy->>'max_concurrent_tasks')::int, 10)
		  -- 4. workspace global cap, when the policy sets one
		  AND (cfg.runtime_policy->>'max_concurrent_tasks_workspace' IS NULL
		       OR (SELECT count(*) FROM busy b JOIN session bs ON bs.id = b.session_id
		           WHERE bs.workspace_id = r.workspace_id)
		          < (cfg.runtime_policy->>'max_concurrent_tasks_workspace')::int)
		  -- 5. worktree isolation shares one workdir per agent → sequential
		  AND NOT (s.isolation->>'kind' = 'worktree'
		           AND EXISTS (SELECT 1 FROM busy b WHERE b.session_id = t.session_id AND b.agent_id = t.agent_id))
		ORDER BY t.created_at
		LIMIT $3
		FOR UPDATE OF t SKIP LOCKED`, rt, now, capacity)
	if err != nil {
		return nil, fmt.Errorf("queue: select: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	bundles := []contracts.TaskBundle{}
	for _, id := range ids {
		t, err := tasks.Get(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		// E11-10: fix the session to the first runtime that claims it. The
		// workspace guard mirrors the SELECT so the session can never be pinned
		// to a runtime outside its workspace.
		if _, err := tx.Exec(ctx, `UPDATE session SET runtime_id = $2, updated_at = $3
			WHERE id = $1 AND runtime_id IS NULL
			  AND workspace_id = (SELECT workspace_id FROM runtime WHERE id = $2)`, t.SessionID, rt, now); err != nil {
			return nil, err
		}
		token, err := p.Tasks.MarkDispatched(ctx, tx, t, rt, now)
		if err != nil {
			return nil, err
		}
		b, err := buildBundle(ctx, tx, t, token)
		if err != nil {
			return nil, err
		}
		bundles = append(bundles, *b)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return bundles, nil
}

// ClaimWait is the long-poll form (§4.1 wait_ms ≤ 30s): it returns as soon as
// a claim yields tasks, a queued-task notification arrives (then re-claims),
// or the wait elapses. Wall-clock waiting is real time; task timestamps use
// the injected clock.
func (p *Postgres) ClaimWait(ctx context.Context, runtimeID string, capacity int, wait time.Duration) ([]contracts.TaskBundle, error) {
	if wait > contracts.ClaimMaxWait {
		wait = contracts.ClaimMaxWait
	}
	deadline := time.Now().Add(wait)
	for {
		bundles, err := p.Claim(ctx, runtimeID, capacity, p.Clock.Now())
		if err != nil || len(bundles) > 0 || capacity <= 0 {
			return bundles, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return bundles, nil
		}
		poll := remaining
		if poll > time.Second {
			poll = time.Second
		}
		var wake <-chan struct{}
		if p.Notifier != nil {
			wake = p.Notifier.Wait()
		}
		select {
		case <-ctx.Done():
			return bundles, nil
		case <-wake:
		case <-time.After(poll):
		}
	}
}

func (p *Postgres) Heartbeat(ctx context.Context, taskID string, attempt int, now time.Time) error {
	id, err := uuid.Parse(taskID)
	if err != nil {
		return err
	}
	return p.Tasks.Heartbeat(ctx, id, attempt, now)
}

func (p *Postgres) Requeue(ctx context.Context, taskID string, reason contracts.FailureKind, notBefore *time.Time, now time.Time) error {
	id, err := uuid.Parse(taskID)
	if err != nil {
		return err
	}
	return p.Tasks.Requeue(ctx, id, reason, notBefore, now)
}

func (p *Postgres) ExpireStale(ctx context.Context, now time.Time) (int, error) {
	return p.Tasks.ExpireStale(ctx, now)
}

// errNoBundle is returned when the task's joins are missing (data bug).
var errNoBundle = errors.New("queue: bundle rows missing")

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
