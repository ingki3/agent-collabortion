package acpfake

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestExecStep — Step.Exec runs in the fake's environment, sees the turn's
// prompt as ACPFAKE_PROMPT, and is reported as one execute tool call whose
// update carries the output and exit code (e2e/p5 relies on all three).
func TestExecStep(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := Script{Turns: []Turn{{Steps: []Step{
		{Exec: `printf 'turn=%s prompt=%s' "$ACPFAKE_TURN" "$ACPFAKE_PROMPT"`},
		{Exec: "exit 3"},
	}}}}
	go Serve(inR, outW, s, nil)
	send := func(v any) {
		b, _ := json.Marshal(v)
		if _, err := inW.Write(append(b, '\n')); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}})
	send(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "session/new", "params": map[string]any{}})
	send(map[string]any{"jsonrpc": "2.0", "id": 3, "method": "session/prompt", "params": map[string]any{
		"sessionId": "sess-1", "prompt": []map[string]any{{"type": "text", "text": "hello"}}}})
	sc := bufio.NewScanner(outR)
	var updates []map[string]any
	var stop string
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		if m["method"] == "session/update" {
			u := m["params"].(map[string]any)["update"].(map[string]any)
			updates = append(updates, u)
		}
		if id, ok := m["id"].(float64); ok && id == 3 {
			stop, _ = m["result"].(map[string]any)["stopReason"].(string)
			break
		}
	}
	_ = inW.Close()
	if stop != "end_turn" {
		t.Fatalf("stopReason=%q", stop)
	}
	var calls, done []map[string]any
	for _, u := range updates {
		switch u["sessionUpdate"] {
		case "tool_call":
			calls = append(calls, u)
		case "tool_call_update":
			done = append(done, u)
		}
	}
	if len(calls) != 2 || len(done) != 2 {
		t.Fatalf("want 2 tool_call + 2 tool_call_update, got %d/%d", len(calls), len(done))
	}
	if calls[0]["kind"] != "execute" {
		t.Errorf("kind=%v", calls[0]["kind"])
	}
	text := done[0]["content"].([]any)[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	if !strings.Contains(text, "turn=1 prompt=hello") {
		t.Errorf("output=%q", text)
	}
	if done[0]["status"] != "completed" || done[1]["status"] != "failed" {
		t.Errorf("status=%v/%v", done[0]["status"], done[1]["status"])
	}
	if code := done[1]["rawOutput"].(map[string]any)["exitCode"].(float64); code != 3 {
		t.Errorf("exitCode=%v", code)
	}
}
