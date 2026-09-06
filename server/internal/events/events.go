// Package events receives the daemon's task_event batches
// (daemon-protocol §4.2): validates each against contracts/task_event.schema.json,
// stores them idempotently on (task_id, attempt, seq), returns
// accepted_seq_max and fans them out to the activity feed (FR-7.2).
package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/eventschema"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// The task_event schema — the closed enums, the closed per-class payload key
// sets and the validator — lives in internal/eventschema so the server's own
// feed writer (tasks.InsertServerEvent) can run the same check. `events`
// imports `tasks`, so it could not live here (S-52).

type Service struct {
	DB    *pgxpool.Pool
	Clock clock.Clock
	Hub   *realtime.Hub
}

func New(pool *pgxpool.Pool, c clock.Clock, h *realtime.Hub) *Service {
	return &Service{DB: pool, Clock: c, Hub: h}
}

// Ingest stores a batch for (task, attempt). Already-seen seqs are ignored;
// partial events are never persisted (PRD §7). Returns accepted_seq_max.
func (s *Service) Ingest(ctx context.Context, taskID uuid.UUID, attempt int, evs []contracts.TaskEvent) (int, error) {
	t, err := tasks.Get(ctx, s.DB, taskID)
	if err != nil {
		return 0, err
	}
	if t.Attempt != attempt {
		return 0, tasks.ErrStaleAttempt
	}
	for i := range evs {
		if evs[i].TaskID != "" && evs[i].TaskID != taskID.String() {
			return 0, apperr.Validation(apperr.Field("events", "task_mismatch", "event task_id differs from path"))
		}
		if evs[i].Attempt == 0 {
			evs[i].Attempt = attempt
		}
		if evs[i].Attempt != attempt {
			return 0, apperr.Validation(apperr.Field("events", "attempt_mismatch", "event attempt differs from path"))
		}
		if err := eventschema.Validate(&evs[i]); err != nil {
			// S-41: 422 AND a feed line. A rejected batch is invisible
			// otherwise — the daemon retries, the operator sees a task with a
			// gap in its activity and no reason for it, and the schema
			// violation that caused it lives only in an HTTP response nobody
			// keeps.
			s.recordRejectedBatch(ctx, taskID, attempt, evs[i].Seq, err)
			return 0, err
		}
	}
	// §9: the workspace may decline to store what the agent read and wrote.
	// Read once per batch — it is one row and the batch is up to 100 events.
	maskOn, err := Masking(ctx, s.DB, t.WorkspaceID)
	if err != nil {
		return 0, err
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for i := range evs {
		e := evs[i]
		if e.Partial {
			continue
		}
		// Masking happens BEFORE the insert. Redacting on read would leave the
		// raw diff on the server's disk, which is exactly what the setting
		// exists to prevent.
		masked := false
		if maskOn {
			masked = Mask(&e)
		}
		// object_ref is stored as a JSON string (task_event.schema.json, openapi v0.4 — N2).
		var objectRef any
		if e.ObjectRef != "" {
			objectRef = e.ObjectRef
		}
		// usage holds only class=usage measurements (N3); the class payload
		// goes to the payload column and out as top-level TaskEvent.payload.
		var usage any
		if e.Class == "usage" && e.Payload != nil {
			usage = e.Payload
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, usage, payload, masked, ts, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $12, $10, $11)
			-- Unlike the server-issued notes (tasks.InsertServerEvent), seq here
			-- comes FROM THE DAEMON and task_id + seq is the protocol's
			-- idempotency key: a conflict means the daemon re-delivered the same
			-- event, so dropping it is the correct answer, not a lost write.
			ON CONFLICT (task_id, attempt, seq) DO NOTHING RETURNING id`,
			taskID, attempt, e.Seq, e.Class, e.Verb, jsonArg(objectRef), e.Outcome, usage, e.Payload, e.TS, now, masked).Scan(&id)
		if errors.Is(err, errNoRows()) {
			continue // duplicate seq — idempotent
		}
		if err != nil {
			return 0, fmt.Errorf("events: insert seq %d: %w", e.Seq, err)
		}
		if s.Hub != nil {
			sid := t.SessionID
			api := toAPI(id, taskID, e, now)
			if masked {
				api.Masked = &masked
			}
			_ = s.Hub.Publish(ctx, tx, t.WorkspaceID, &sid, "task_event.appended", api)
		}
	}
	// Events are liveness: refresh the heartbeat.
	_, _ = tx.Exec(ctx, `UPDATE task SET heartbeat_at = $3 WHERE id = $1 AND attempt = $2 AND status IN ('preparing', 'running')`, taskID, attempt, now)
	var max int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM task_event WHERE task_id = $1 AND attempt = $2 AND seq < $3`, taskID, attempt, router.ServerSeqBase).Scan(&max); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return max, nil
}

func errNoRows() error { return pgxErrNoRows }

// List is listTaskEvents: seq ascending, after_seq, latest revisions only
// unless include_superseded.
func (s *Service) List(ctx context.Context, taskID uuid.UUID, afterSeq int, includeSuperseded bool, limit int) ([]gen.TaskEvent, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.DB.Query(ctx, `
		SELECT id, task_id, attempt, seq, class, verb, object_ref, outcome, tool, input, output, usage, payload, superseded_by, ts, created_at, masked
		FROM task_event WHERE task_id = $1 AND seq > $2 AND ($3 OR superseded_by IS NULL)
		ORDER BY attempt, seq LIMIT $4`, taskID, afterSeq, includeSuperseded, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []gen.TaskEvent{}
	for rows.Next() {
		var (
			id, tid                 uuid.UUID
			attempt, seq            int
			class                   string
			verb, outcome, tool     *string
			objectRef               any
			input, output, usage, p map[string]any
			supersededBy            *uuid.UUID
			ts                      *time.Time
			createdAt               time.Time
			masked                  bool
		)
		if err := rows.Scan(&id, &tid, &attempt, &seq, &class, &verb, &objectRef, &outcome, &tool, &input, &output, &usage, &p, &supersededBy, &ts, &createdAt, &masked); err != nil {
			return nil, false, err
		}
		ref := objectRefString(objectRef)
		a := attempt
		ev := gen.TaskEvent{Id: id, TaskId: tid, Attempt: &a, Seq: seq, Class: class, CreatedAt: createdAt,
			Verb: tasks.NullString(verb), Outcome: tasks.NullString(outcome), Tool: tasks.NullString(tool),
			SupersededBy: tasks.NullUUID(supersededBy)}
		ev.ObjectRef = nullString(ref)
		ev.Input = nullMap(input)
		ev.Output = nullMap(output)
		ev.Usage = nullMap(usage)
		ev.Payload = nullMap(p) // top-level, as stored (openapi v0.4 — R2)
		ev.Sentence = nullable.NewNullableWithValue(sentenceFor(class, deref(verb), ref, deref(outcome)))
		if masked {
			// openapi listTaskEvents: "마스킹이 켜진 워크스페이스에서는 …
			// `masked: true`". The reader has to be able to tell a quiet turn
			// from a redacted one.
			m := true
			ev.Masked = &m
		}
		out = append(out, ev)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, rows.Err()
}

// toAPI is the SSE task_event.appended frame: the same shape as List —
// payload top-level, object_ref a string, usage only for class=usage.
func toAPI(id, taskID uuid.UUID, e contracts.TaskEvent, createdAt time.Time) gen.TaskEvent {
	a := e.Attempt
	ev := gen.TaskEvent{Id: id, TaskId: taskID, Attempt: &a, Seq: e.Seq, Class: e.Class, CreatedAt: createdAt,
		Verb: nullable.NewNullableWithValue(e.Verb), Outcome: nullable.NewNullableWithValue(e.Outcome)}
	ev.ObjectRef = nullString(e.ObjectRef)
	ev.Payload = nullMap(e.Payload)
	if e.Class == "usage" {
		ev.Usage = nullMap(e.Payload)
	}
	ev.Sentence = nullable.NewNullableWithValue(sentenceFor(e.Class, e.Verb, e.ObjectRef, e.Outcome))
	return ev
}

// jsonArg makes pgx encode a Go string as a JSON string value (a bare string
// parameter would be taken as raw JSON text).
func jsonArg(v any) any {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

// objectRefString reads the jsonb object_ref column: a string since 0003;
// the pre-0003 {"ref": …} envelope is normalised by the migration but
// tolerated here.
func objectRefString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case map[string]any:
		if r, ok := x["ref"].(string); ok {
			return r
		}
	}
	return ""
}

func nullString(s string) nullable.Nullable[string] {
	if s == "" {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(s)
}

func nullMap(m map[string]any) nullable.Nullable[map[string]interface{}] {
	if m == nil {
		return nullable.NewNullNullable[map[string]interface{}]()
	}
	return nullable.NewNullableWithValue(m)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// sentenceFor is the FR-7.2 one-line fallback render: "<verb> <object> → <outcome>".
func sentenceFor(class, verb, objectRef, outcome string) string {
	obj := ""
	if objectRef != "" {
		obj = " " + objectRef
	}
	return fmt.Sprintf("%s.%s%s → %s", class, verb, obj, outcome)
}

// recordRejectedBatch is S-41's feed half. It runs outside the ingest
// transaction (there is none yet) and never turns its own failure into the
// caller's: the 422 is the answer, this line is the record.
func (s *Service) recordRejectedBatch(ctx context.Context, taskID uuid.UUID, attempt, seq int, cause error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	detail := fmt.Sprintf("데몬이 보낸 task_event(seq %d)가 계약 스키마를 어겨 배치를 거절했습니다 — %s",
		seq, cause.Error())
	if err := tasks.InsertServerEventOnce(ctx, tx, taskID, attempt, "runtime", "error",
		"task_event.schema_rejected", "failed",
		map[string]any{"detail": detail}, s.Clock.Now()); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
