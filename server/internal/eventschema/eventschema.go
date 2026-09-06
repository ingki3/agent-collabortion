// Package eventschema is contracts/task_event.schema.json expressed as Go: the
// closed enums, the closed per-class payload key sets, and the validator both
// ingest paths run.
//
// It sits below `events` (which imports `tasks`) so that `tasks` — where the
// SERVER writes its own feed rows — can run the same check without an import
// cycle. That cycle is why S-52 existed at all: the daemon's batches were
// validated and the server's own 13 InsertServerEvent call sites were not, so
// the server answered 422 to a daemon key it would happily have written itself.
package eventschema

import (
	"fmt"
	"slices"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
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

// ValidateServerEvent is Validate for a row the SERVER writes itself
// (tasks.InsertServerEvent / InsertServerEventOnce), which supplies only the
// five wire fields the caller chooses. `attempt`, `seq` and `ts` are the
// server's own and are always well-formed, so they are filled with valid
// stand-ins rather than reported as violations of the caller's payload.
func ValidateServerEvent(class, verb, objectRef, outcome string, payload map[string]any) error {
	e := contracts.TaskEvent{
		Attempt: 1, Seq: 1, TS: time.Unix(0, 0).UTC(),
		Class: class, Verb: verb, ObjectRef: objectRef, Outcome: outcome, Payload: payload,
	}
	return Validate(&e)
}
