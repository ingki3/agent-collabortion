package events

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
)

// S-77 (T-I5 PR #206 신규 결함 2): the adapter puts the whole command line in
// `tool.title` — G6 database: `"title": "ls -la | grep manual"` — so masking
// `summary` and `command` left the arguments one key over. The schema is closed
// (S-52), so the fix is the value, not a new key.
func TestMaskCutsTitleToItsFirstWord(t *testing.T) {
	e := &contracts.TaskEvent{Class: "tool", Verb: "run_shell", ObjectRef: "echo", Outcome: "ok",
		Payload: map[string]any{
			"tool_call_id": "e1", "kind": "execute",
			"title":   "echo SECRET-SHELL-OUTPUT-8842",
			"command": "echo SECRET-SHELL-OUTPUT-8842",
			"summary": "SECRET-SHELL-OUTPUT-8842\n",
		}}
	if !Mask(e) {
		t.Fatal("Mask reported nothing replaced")
	}
	for _, key := range []string{"title", "command", "summary"} {
		v, _ := e.Payload[key].(string)
		if strings.Contains(v, "SECRET") {
			t.Errorf("%s = %q still carries the argument", key, v)
		}
	}
	// The card shape survives: first word of the command AND of the title.
	if got := e.Payload["title"]; got != "echo [마스킹됨 · 24자]" {
		t.Errorf("title = %q, want the first word + summary", got)
	}
	if got := e.Payload["command"]; got != "echo [마스킹됨 · 24자]" {
		t.Errorf("command = %q", got)
	}
	if e.Payload["masked"] != true {
		t.Error("tool.masked flag not set")
	}
}

// A one-word title ("Read", "Bash") has no arguments to hide; an edit's title
// keeps its verb while the content goes.
func TestMaskLeavesSingleWordTitleAndKeepsPath(t *testing.T) {
	e := &contracts.TaskEvent{Class: "tool", Verb: "edit_file", Outcome: "ok",
		Payload: map[string]any{"tool_call_id": "e2", "kind": "edit", "title": "edit secret.txt", "path": "secret.txt",
			"lines_added": 1, "summary": "+SECRET-DIFF-BODY-7731"}}
	Mask(e)
	if e.Payload["path"] != "secret.txt" || e.Payload["lines_added"] != 1 {
		t.Errorf("metadata changed: %v", e.Payload)
	}
	if v := e.Payload["title"].(string); !strings.HasPrefix(v, "edit [마스킹됨") {
		t.Errorf("title = %q", v)
	}
	one := &contracts.TaskEvent{Class: "tool", Verb: "read", Outcome: "ok",
		Payload: map[string]any{"tool_call_id": "e3", "kind": "read", "title": "Read"}}
	if Mask(one) {
		t.Errorf("a one-word title was rewritten: %v", one.Payload)
	}
}

// The permission request names the same command line.
func TestMaskPermissionTitle(t *testing.T) {
	e := &contracts.TaskEvent{Class: "tool", Verb: "permission", Outcome: "allowed",
		Payload: map[string]any{"tool_call_id": "p1", "title": "rm -rf /tmp/SECRET-DIR", "options_offered": []string{"allow_once"}}}
	if !Mask(e) {
		t.Fatal("permission title not masked")
	}
	if v := e.Payload["title"].(string); strings.Contains(v, "SECRET") || !strings.HasPrefix(v, "rm ") {
		t.Errorf("title = %q", v)
	}
	if _, has := e.Payload["masked"]; has {
		t.Error("the permission payload is closed and has no `masked` key")
	}
}
