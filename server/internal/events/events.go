// Package events receives the daemon's task_event batches
// (daemon-protocol §4.2): validates each against contracts/task_event.schema.json,
// stores them idempotently on (task_id, attempt, seq), returns
// accepted_seq_max and fans them out to the activity feed (FR-7.2).
package events

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Closed sets from task_event.schema.json v0.1. events_test.go asserts they
// match the contract file so drift is caught in CI.
var (
	Classes  = []string{"message", "tool", "usage", "plan", "runtime", "status"}
	Verbs    = []string{"say", "think", "edit_file", "run_shell", "read", "search", "use_tool", "permission", "report", "update", "start", "resume", "error", "cancel", "turn_end", "post_message", "delegate", "set_status", "submit_artifact", "record_decision", "hitl", "review"}
	Outcomes = []string{"started", "ok", "failed", "allowed", "rejected", "cancelled", "resumed", "cold_start", "report", "update", "info"}

	messageKinds = []string{"text", "thought"}
	toolKinds    = []string{"edit", "execute", "read", "search", "fetch", "think", "other"}
	optionKinds  = []string{"allow_once", "allow_always", "reject_once", "reject_always"}
)

// Validate checks the required fields, enums and the class payload shape.
func Validate(e *contracts.TaskEvent) error {
	var errs []apperr.FieldError
	if e.Attempt < 1 {
		errs = append(errs, apperr.Field("attempt", "minimum", "attempt must be ≥ 1"))
	}
	if e.Seq < 1 {
		errs = append(errs, apperr.Field("seq", "minimum", "seq must be ≥ 1"))
	}
	if e.TS.IsZero() {
		errs = append(errs, apperr.Field("ts", "required", "ts is required"))
	}
	if !slices.Contains(Classes, e.Class) {
		errs = append(errs, apperr.Field("class", "enum", "unknown class "+e.Class))
	}
	if !slices.Contains(Verbs, e.Verb) {
		errs = append(errs, apperr.Field("verb", "enum", "unknown verb "+e.Verb))
	}
	if !slices.Contains(Outcomes, e.Outcome) {
		errs = append(errs, apperr.Field("outcome", "enum", "unknown outcome "+e.Outcome))
	}
	if len(e.ObjectRef) > 512 {
		errs = append(errs, apperr.Field("object_ref", "maxLength", "object_ref longer than 512"))
	}
	if e.Payload != nil {
		str := func(k string) (string, bool) { v, ok := e.Payload[k].(string); return v, ok }
		switch e.Class {
		case "message":
			if k, ok := str("kind"); !ok || !slices.Contains(messageKinds, k) {
				errs = append(errs, apperr.Field("payload.kind", "enum", "message payload needs kind text|thought"))
			}
		case "tool":
			if _, ok := str("tool_call_id"); !ok {
				errs = append(errs, apperr.Field("payload.tool_call_id", "required", "tool payload needs tool_call_id"))
			}
			if e.Verb == "permission" {
				if k, ok := str("option_kind"); !ok || !slices.Contains(optionKinds, k) {
					errs = append(errs, apperr.Field("payload.option_kind", "enum", "permission payload needs option_kind"))
				}
			} else if k, ok := str("kind"); !ok || !slices.Contains(toolKinds, k) {
				errs = append(errs, apperr.Field("payload.kind", "enum", "tool payload needs kind"))
			}
		case "status":
			if _, ok := str("command"); !ok {
				errs = append(errs, apperr.Field("payload.command", "required", "status payload needs command"))
			}
		}
	}
	if len(errs) > 0 {
		return apperr.Validation(errs...)
	}
	return nil
}

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
		if err := Validate(&evs[i]); err != nil {
			return 0, err
		}
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, e := range evs {
		if e.Partial {
			continue
		}
		var objectRef any
		if e.ObjectRef != "" {
			objectRef = map[string]any{"ref": e.ObjectRef}
		}
		var usage any
		if e.Class == "usage" && e.Payload != nil {
			usage = e.Payload
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, usage, payload, ts, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (task_id, attempt, seq) DO NOTHING RETURNING id`,
			taskID, attempt, e.Seq, e.Class, e.Verb, objectRef, e.Outcome, usage, e.Payload, e.TS, now).Scan(&id)
		if errors.Is(err, errNoRows()) {
			continue // duplicate seq — idempotent
		}
		if err != nil {
			return 0, fmt.Errorf("events: insert seq %d: %w", e.Seq, err)
		}
		if s.Hub != nil {
			sid := t.SessionID
			_ = s.Hub.Publish(ctx, tx, t.WorkspaceID, &sid, "task_event.appended", toAPI(id, taskID, e, now))
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
		SELECT id, task_id, attempt, seq, class, verb, object_ref, outcome, tool, input, output, usage, payload, superseded_by, ts, created_at
		FROM task_event WHERE task_id = $1 AND seq > $2 AND ($3 OR superseded_by IS NULL)
		ORDER BY attempt, seq LIMIT $4`, taskID, afterSeq, includeSuperseded, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	out := []gen.TaskEvent{}
	for rows.Next() {
		var (
			id, tid                            uuid.UUID
			attempt, seq                       int
			class                              string
			verb, outcome, tool                *string
			objectRef, input, output, usage, p map[string]any
			supersededBy                       *uuid.UUID
			ts                                 *time.Time
			createdAt                          time.Time
		)
		if err := rows.Scan(&id, &tid, &attempt, &seq, &class, &verb, &objectRef, &outcome, &tool, &input, &output, &usage, &p, &supersededBy, &ts, &createdAt); err != nil {
			return nil, false, err
		}
		ev := gen.TaskEvent{Id: id, TaskId: tid, Seq: seq, Class: class, CreatedAt: createdAt,
			Verb: tasks.NullString(verb), Outcome: tasks.NullString(outcome), Tool: tasks.NullString(tool),
			SupersededBy: tasks.NullUUID(supersededBy)}
		ev.ObjectRef = nullMap(objectRef)
		ev.Input = nullMap(input)
		ev.Output = nullMap(output)
		if usage == nil && p != nil {
			usage = map[string]any{"payload": p, "attempt": attempt}
		} else if p != nil {
			usage["payload"] = p
			usage["attempt"] = attempt
		}
		ev.Usage = nullMap(usage)
		sentence := sentenceFor(class, deref(verb), objectRef, deref(outcome))
		ev.Sentence = nullable.NewNullableWithValue(sentence)
		out = append(out, ev)
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, rows.Err()
}

func toAPI(id, taskID uuid.UUID, e contracts.TaskEvent, createdAt time.Time) gen.TaskEvent {
	ev := gen.TaskEvent{Id: id, TaskId: taskID, Seq: e.Seq, Class: e.Class, CreatedAt: createdAt,
		Verb: nullable.NewNullableWithValue(e.Verb), Outcome: nullable.NewNullableWithValue(e.Outcome)}
	var objectRef map[string]any
	if e.ObjectRef != "" {
		objectRef = map[string]any{"ref": e.ObjectRef}
	}
	ev.ObjectRef = nullMap(objectRef)
	if e.Payload != nil {
		ev.Usage = nullable.NewNullableWithValue(map[string]any{"payload": e.Payload, "attempt": e.Attempt})
	}
	ev.Sentence = nullable.NewNullableWithValue(sentenceFor(e.Class, e.Verb, objectRef, e.Outcome))
	return ev
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
func sentenceFor(class, verb string, objectRef map[string]any, outcome string) string {
	obj := ""
	if objectRef != nil {
		if r, ok := objectRef["ref"].(string); ok {
			obj = " " + r
		}
	}
	return fmt.Sprintf("%s.%s%s → %s", class, verb, obj, outcome)
}
