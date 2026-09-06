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

// Closed sets from task_event.schema.json v0.2. events_test.go asserts they
// match the contract file so drift is caught in CI.
var (
	Classes  = []string{"message", "tool", "usage", "plan", "runtime", "status"}
	Verbs    = []string{"say", "think", "edit_file", "run_shell", "read", "search", "use_tool", "permission", "report", "update", "start", "resume", "error", "cancel", "turn_end", "post_message", "delegate", "set_status", "submit_artifact", "record_decision", "hitl", "review"}
	Outcomes = []string{"started", "ok", "failed", "allowed", "rejected", "cancelled", "resumed", "cold_start", "report", "update", "info"}

	messageKinds = []string{"text", "thought"}
	toolKinds    = []string{"edit", "execute", "read", "search", "fetch", "think", "other"}
	// optionKinds is optional since v0.2: outcome=cancelled picked no option (PR #20 N2).
	optionKinds = []string{"allow_once", "allow_always", "reject_once", "reject_always"}
	// policies is tool.policy / permission.policy (harness §4, v0.3 PR #20 N3).
	policies = []string{"allowed_by_profile", "denied_by_profile"}
	// rateLimitStatuses is usage.rate_limit.status — the per-usage_update
	// payload (harness v0.3 §7: tokens come once at turn end, rate_limit on
	// every usage_update).
	rateLimitStatuses = []string{"allowed", "allowed_warning", "rejected"}

	// PayloadProperties is `additionalProperties: false` per class, which
	// Validate never enforced (S-41). The schema closes every payload, and an
	// unknown key is not a harmless extra: T-D5's first implementation broke
	// five of them and nobody found out, because the server answered 200 and
	// the daemon's own memSink check was the only thing looking. A key the
	// server stores but no reader knows is a feed field that silently does
	// nothing — and a key that was MEANT to be one of these is a misspelling
	// that costs a whole column of the activity view.
	//
	// events_test.go asserts these lists against contracts/task_event.schema.json
	// so drift is caught in CI, the same way the enums above are.
	PayloadProperties = map[string][]string{
		"message":    {"kind", "text", "chars"},
		"tool":       {"tool_call_id", "kind", "title", "path", "lines_added", "lines_removed", "command", "exit_code", "summary", "masked", "duration_ms", "policy"},
		"permission": {"tool_call_id", "title", "option_kind", "options_offered", "allow_once_missing", "policy"},
		"usage":      {"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "cost_usd", "estimated", "cumulative", "model", "model_drift", "rate_limit"},
		"plan":       {"entries_total", "entries_done", "current"},
		"runtime":    {"runtime_kind", "adapter_version", "protocol_version", "session_id", "failure_kind", "detail", "not_before", "stop_reason", "resume_reason"},
		"status":     {"command", "args", "result_ref", "rejected_reason"},
	}
)

// payloadDefFor maps a class to the $defs entry its payload is validated
// against. `tool` is the one class with two: a permission event carries the
// `permission` payload (schema `oneOf`, harness §4).
func payloadDefFor(class, verb string) string {
	if class == "tool" && verb == "permission" {
		return "permission"
	}
	return class
}

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
		// optEnum: the key may be absent, but when present it must be in set.
		optEnum := func(field string, set []string) {
			if v, present := e.Payload[field]; present {
				if k, ok := v.(string); !ok || !slices.Contains(set, k) {
					errs = append(errs, apperr.Field("payload."+field, "enum", fmt.Sprintf("%s must be one of %v", field, set)))
				}
			}
		}
		// S-41: `additionalProperties: false`. Checked before the per-class
		// rules so a misspelt required key is reported as the unknown key it
		// is, rather than only as the missing one.
		if allowed, ok := PayloadProperties[payloadDefFor(e.Class, e.Verb)]; ok {
			for k := range e.Payload {
				if !slices.Contains(allowed, k) {
					errs = append(errs, apperr.Field("payload."+k, "additionalProperties",
						fmt.Sprintf("%s payload has no %q — contracts/task_event.schema.json closes it to %v",
							payloadDefFor(e.Class, e.Verb), k, allowed)))
				}
			}
		}
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
				optEnum("option_kind", optionKinds)
			} else if k, ok := str("kind"); !ok || !slices.Contains(toolKinds, k) {
				errs = append(errs, apperr.Field("payload.kind", "enum", "tool payload needs kind"))
			}
			optEnum("policy", policies)
		case "usage":
			if rl, present := e.Payload["rate_limit"]; present {
				m, ok := rl.(map[string]any)
				if !ok {
					errs = append(errs, apperr.Field("payload.rate_limit", "type", "rate_limit must be an object"))
				} else if st, ok := m["status"].(string); !ok || !slices.Contains(rateLimitStatuses, st) {
					errs = append(errs, apperr.Field("payload.rate_limit.status", "enum", fmt.Sprintf("rate_limit.status must be one of %v", rateLimitStatuses)))
				}
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
