package events

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// The enum lists must equal contracts/task_event.schema.json.
func TestEnumsMatchSchema(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/task_event.schema.json")
	if err != nil {
		t.Skip("contracts schema not available:", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	// at walks a JSON path and returns the node.
	at := func(path ...string) any {
		var cur any = schema
		for _, k := range path {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Fatalf("schema path %v: %q is not an object", path, k)
			}
			cur = m[k]
		}
		return cur
	}
	enum := func(path ...string) []string {
		raw, _ := at(append(path, "enum")...).([]any)
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			out = append(out, v.(string))
		}
		return out
	}
	required := func(path ...string) []string {
		raw, _ := at(append(path, "required")...).([]any)
		out := make([]string, 0, len(raw))
		for _, v := range raw {
			out = append(out, v.(string))
		}
		return out
	}
	check := func(name string, got, want []string) {
		if !slices.Equal(got, want) {
			t.Errorf("%s: code %v, schema %v", name, got, want)
		}
	}
	check("class", Classes, enum("properties", "class"))
	check("verb", Verbs, enum("properties", "verb"))
	check("outcome", Outcomes, enum("properties", "outcome"))
	check("message.kind", messageKinds, enum("$defs", "message", "properties", "kind"))
	check("tool.kind", toolKinds, enum("$defs", "tool", "properties", "kind"))
	check("tool.policy", policies, enum("$defs", "tool", "properties", "policy"))
	check("permission.option_kind", optionKinds, enum("$defs", "permission", "properties", "option_kind"))
	check("permission.policy", policies, enum("$defs", "permission", "properties", "policy"))
	check("usage.rate_limit.status", rateLimitStatuses, enum("$defs", "usage", "properties", "rate_limit", "properties", "status"))
	// v0.2: option_kind is optional (outcome=cancelled picks no option).
	check("permission.required", []string{"tool_call_id"}, required("$defs", "permission"))
	check("tool.required", []string{"tool_call_id", "kind"}, required("$defs", "tool"))
}

func TestValidate(t *testing.T) {
	ok := contracts.TaskEvent{Attempt: 1, Seq: 1, TS: time.Now(), Class: "message", Verb: "say", Outcome: "ok", Payload: map[string]any{"kind": "text", "text": "hi"}}
	if err := Validate(&ok); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	bad := []contracts.TaskEvent{
		{Attempt: 1, Seq: 0, TS: time.Now(), Class: "message", Verb: "say", Outcome: "ok"},
		{Attempt: 1, Seq: 1, TS: time.Now(), Class: "nope", Verb: "say", Outcome: "ok"},
		{Attempt: 1, Seq: 1, TS: time.Now(), Class: "tool", Verb: "edit_file", Outcome: "ok", Payload: map[string]any{"kind": "edit"}}, // no tool_call_id
		{Attempt: 1, Seq: 1, TS: time.Now(), Class: "status", Verb: "post_message", Outcome: "ok", Payload: map[string]any{}},
		{Attempt: 1, Seq: 1, Class: "message", Verb: "say", Outcome: "ok"}, // no ts
	}
	for i, e := range bad {
		if err := Validate(&e); err == nil {
			t.Errorf("bad[%d] accepted", i)
		}
	}
}

// task_event.schema.json v0.2 / harness v0.3 §7·§8: usage.report comes once
// per turn with tokens (cumulative) and on every usage_update with only
// rate_limit; permission.option_kind is optional (cancelled); tool and
// permission carry an optional policy verdict.
func TestValidateV02(t *testing.T) {
	ts := time.Now()
	ok := []contracts.TaskEvent{
		// turn-end usage: tokens + cost, cumulative
		{Attempt: 1, Seq: 1, TS: ts, Class: "usage", Verb: "report", Outcome: "report", Payload: map[string]any{
			"input_tokens": 1200, "output_tokens": 300, "cache_read_tokens": 900, "cache_write_tokens": 0, "cost_usd": 0.02, "cumulative": true, "model": "claude-sonnet-5"}},
		// per usage_update: rate_limit only, no tokens
		{Attempt: 1, Seq: 2, TS: ts, Class: "usage", Verb: "report", Outcome: "report", Payload: map[string]any{
			"rate_limit": map[string]any{"status": "allowed_warning", "resets_at": "2026-09-05T13:00:00Z", "type": "five_hour", "utilization": 0.91}}},
		// permission cancelled: no option_kind
		{Attempt: 1, Seq: 3, TS: ts, Class: "tool", Verb: "permission", Outcome: "cancelled", Payload: map[string]any{"tool_call_id": "c1", "title": "rm -rf"}},
		// permission answered with option_kind + policy
		{Attempt: 1, Seq: 4, TS: ts, Class: "tool", Verb: "permission", Outcome: "allowed", Payload: map[string]any{"tool_call_id": "c2", "option_kind": "allow_once", "policy": "allowed_by_profile"}},
		// tool with policy
		{Attempt: 1, Seq: 5, TS: ts, Class: "tool", Verb: "run_shell", Outcome: "failed", Payload: map[string]any{"tool_call_id": "c3", "kind": "execute", "policy": "denied_by_profile"}},
	}
	for i, e := range ok {
		if err := Validate(&e); err != nil {
			t.Errorf("ok[%d] rejected: %v", i, err)
		}
	}
	bad := []contracts.TaskEvent{
		{Attempt: 1, Seq: 1, TS: ts, Class: "usage", Verb: "report", Outcome: "report", Payload: map[string]any{"rate_limit": map[string]any{"status": "throttled"}}},
		{Attempt: 1, Seq: 1, TS: ts, Class: "usage", Verb: "report", Outcome: "report", Payload: map[string]any{"rate_limit": "rejected"}},
		{Attempt: 1, Seq: 1, TS: ts, Class: "tool", Verb: "permission", Outcome: "allowed", Payload: map[string]any{"tool_call_id": "c2", "option_kind": "maybe"}},
		{Attempt: 1, Seq: 1, TS: ts, Class: "tool", Verb: "permission", Outcome: "allowed", Payload: map[string]any{"tool_call_id": "c2", "option_kind": "allow_once", "policy": "yolo"}},
		{Attempt: 1, Seq: 1, TS: ts, Class: "tool", Verb: "run_shell", Outcome: "ok", Payload: map[string]any{"tool_call_id": "c3", "kind": "execute", "policy": 1}},
	}
	for i, e := range bad {
		if err := Validate(&e); err == nil {
			t.Errorf("bad[%d] accepted", i)
		}
	}
}
