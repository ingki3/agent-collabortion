package eventschema

import (
	"encoding/json"
	"os"
	"slices"
	"sort"
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

// TestPayloadPropertiesMatchSchema is the S-41 drift guard: the closed
// property lists Validate enforces must equal the schema's, or the check
// either rejects a legal event or lets an illegal one through — the two
// failures that made S-41 invisible for three phases.
func TestPayloadPropertiesMatchSchema(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/task_event.schema.json")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc struct {
		Defs map[string]struct {
			AdditionalProperties *bool                      `json:"additionalProperties"`
			Properties           map[string]json.RawMessage `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	for def, spec := range doc.Defs {
		if spec.AdditionalProperties == nil || *spec.AdditionalProperties {
			t.Errorf("$defs.%s is not closed in the schema — Validate assumes every payload is", def)
			continue
		}
		got, ok := PayloadProperties[def]
		if !ok {
			t.Errorf("PayloadProperties has no %q — that payload is unchecked", def)
			continue
		}
		want := make([]string, 0, len(spec.Properties))
		for k := range spec.Properties {
			want = append(want, k)
		}
		sort.Strings(want)
		have := append([]string(nil), got...)
		sort.Strings(have)
		if !slices.Equal(have, want) {
			t.Errorf("%s payload properties = %v, schema says %v", def, have, want)
		}
	}
}

// TestValidateRejectsUnknownPayloadKeys is S-41's actual guard: the schema
// closes every payload, and an unknown key used to be accepted with a 200.
//
// T-D5's first implementation broke five of these and nobody found out. A key
// the server stores but no reader knows is a feed field that silently does
// nothing; a key that was MEANT to be one of the closed set is a misspelling
// that costs a whole column of the activity view.
func TestValidateRejectsUnknownPayloadKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   contracts.TaskEvent
	}{
		{"status payload has no note", contracts.TaskEvent{
			Attempt: 1, Seq: 1, TS: time.Now(), Class: "status", Verb: "post_message", Outcome: "ok",
			Payload: map[string]any{"command": "message post", "note": "hi"},
		}},
		{"tool payload has no stdout", contracts.TaskEvent{
			Attempt: 1, Seq: 1, TS: time.Now(), Class: "tool", Verb: "run_shell", Outcome: "ok",
			Payload: map[string]any{"tool_call_id": "c1", "kind": "execute", "stdout": "…"},
		}},
		{"runtime payload has no message", contracts.TaskEvent{
			Attempt: 1, Seq: 1, TS: time.Now(), Class: "runtime", Verb: "error", Outcome: "failed",
			Payload: map[string]any{"detail": "x", "message": "y"},
		}},
	} {
		if err := Validate(&tc.ev); err == nil {
			t.Errorf("%s: accepted — contracts/task_event.schema.json sets "+
				"additionalProperties:false on every payload (S-41)", tc.name)
		}
	}
	// The negative control: a legal payload still passes, or the check would
	// reject the daemon's ordinary traffic.
	ok := contracts.TaskEvent{
		Attempt: 1, Seq: 1, TS: time.Now(), Class: "tool", Verb: "run_shell", Outcome: "ok",
		Payload: map[string]any{"tool_call_id": "c1", "kind": "execute", "command": "go test", "exit_code": 0},
	}
	if err := Validate(&ok); err != nil {
		t.Errorf("a legal tool payload was rejected: %v", err)
	}
	// `tool` + `permission` validates against the permission $defs, not tool's
	// (schema oneOf, harness §4) — a check that used the wrong one would reject
	// every permission event.
	perm := contracts.TaskEvent{
		Attempt: 1, Seq: 1, TS: time.Now(), Class: "tool", Verb: "permission", Outcome: "allowed",
		Payload: map[string]any{"tool_call_id": "c1", "option_kind": "allow_once", "options_offered": 2},
	}
	if err := Validate(&perm); err != nil {
		t.Errorf("a legal permission payload was rejected: %v", err)
	}
}
