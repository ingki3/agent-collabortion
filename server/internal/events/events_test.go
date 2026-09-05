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
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
		Defs map[string]struct {
			Properties map[string]struct {
				Enum []string `json:"enum"`
			} `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want []string) {
		if !slices.Equal(got, want) {
			t.Errorf("%s: code %v, schema %v", name, got, want)
		}
	}
	check("class", Classes, schema.Properties["class"].Enum)
	check("verb", Verbs, schema.Properties["verb"].Enum)
	check("outcome", Outcomes, schema.Properties["outcome"].Enum)
	check("message.kind", messageKinds, schema.Defs["message"].Properties["kind"].Enum)
	check("tool.kind", toolKinds, schema.Defs["tool"].Properties["kind"].Enum)
	check("permission.option_kind", optionKinds, schema.Defs["permission"].Properties["option_kind"].Enum)
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
