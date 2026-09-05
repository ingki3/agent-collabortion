package acpprobe

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as a fake ACP agent when ACPPROBE_FAKE_AGENT=1 (the
// standard "helper process" pattern). The fake: answers initialize with
// protocol 1, session/new with a fixed id, and on session/prompt emits one
// agent_message_chunk, asks permission (options from ACPPROBE_FAKE_OPTIONS:
// "std" → allow_always/allow_once/reject_once, "noallow" → reject_once only),
// then replies end_turn — or "cancelled" if session/cancel arrived first.
func TestMain(m *testing.M) {
	if os.Getenv("ACPPROBE_FAKE_AGENT") == "1" {
		fakeAgent()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeAgent() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 8<<20)
	out := bufio.NewWriter(os.Stdout)
	send := func(v any) {
		b, _ := json.Marshal(v)
		out.Write(append(b, '\n'))
		out.Flush()
	}
	var nextID int64 = 100
	cancelled := map[string]bool{}
	slow := os.Getenv("ACPPROBE_FAKE_SLOW") == "1"
	for in.Scan() {
		var m message
		if err := json.Unmarshal(in.Bytes(), &m); err != nil {
			continue
		}
		switch m.Method {
		case MethodInitialize:
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": InitializeResult{ProtocolVersion: 1, AgentInfo: &Implementation{Name: "fake"}}})
		case MethodSessionNew:
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"sessionId": "sess-1"}})
		case MethodSessionLoad, MethodSessionResume:
			var p LoadSessionParams
			_ = json.Unmarshal(m.Params, &p)
			if p.SessionID != "sess-1" {
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": RPCError{Code: -32000, Message: "Session not found"}})
				continue
			}
			send(map[string]any{"jsonrpc": "2.0", "method": MethodSessionUpdate, "params": map[string]any{"sessionId": "sess-1", "update": map[string]any{"sessionUpdate": "user_message_chunk", "content": map[string]any{"type": "text", "text": "replayed"}}}})
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{}})
		case MethodSessionCancel:
			var p CancelParams
			_ = json.Unmarshal(m.Params, &p)
			cancelled[p.SessionID] = true
		case MethodSessionPrompt:
			var p PromptParams
			_ = json.Unmarshal(m.Params, &p)
			send(map[string]any{"jsonrpc": "2.0", "method": MethodSessionUpdate, "params": map[string]any{"sessionId": p.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "hello "}}}})
			if slow {
				time.Sleep(300 * time.Millisecond)
			}
			if cancelled[p.SessionID] {
				delete(cancelled, p.SessionID)
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": PromptResult{StopReason: "cancelled"}})
				continue
			}
			var opts []PermissionOption
			if os.Getenv("ACPPROBE_FAKE_OPTIONS") == "noallow" {
				opts = []PermissionOption{{OptionID: "x-deny", Name: "Deny", Kind: "reject_once"}}
			} else {
				opts = []PermissionOption{{OptionID: "x-always", Name: "Always", Kind: "allow_always"}, {OptionID: "x-once", Name: "Allow", Kind: "allow_once"}, {OptionID: "x-deny", Name: "Reject", Kind: "reject_once"}}
			}
			nextID++
			send(map[string]any{"jsonrpc": "2.0", "id": nextID, "method": MethodRequestPermission, "params": RequestPermissionParams{SessionID: p.SessionID, Options: opts, ToolCall: json.RawMessage(`{"toolCallId":"t1","title":"fake tool"}`)}})
			// wait for the permission answer
			var answer message
			for in.Scan() {
				var cm message
				if err := json.Unmarshal(in.Bytes(), &cm); err != nil {
					continue
				}
				if cm.Method == MethodSessionCancel {
					cancelled[p.SessionID] = true
					continue
				}
				if cm.ID != nil && cm.Method == "" {
					answer = cm
					break
				}
			}
			var pr RequestPermissionResult
			_ = json.Unmarshal(answer.Result, &pr)
			status := "completed"
			if pr.Outcome.Outcome != "selected" || pr.Outcome.OptionID == "x-deny" {
				status = "failed"
			}
			send(map[string]any{"jsonrpc": "2.0", "method": MethodSessionUpdate, "params": map[string]any{"sessionId": p.SessionID, "update": map[string]any{"sessionUpdate": "tool_call", "toolCallId": "t1", "title": "fake tool", "status": status}}})
			send(map[string]any{"jsonrpc": "2.0", "method": MethodSessionUpdate, "params": map[string]any{"sessionId": p.SessionID, "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "world"}}}})
			stop := "end_turn"
			if cancelled[p.SessionID] || pr.Outcome.Outcome == "cancelled" {
				stop = "cancelled"
				delete(cancelled, p.SessionID)
			}
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": PromptResult{StopReason: stop}})
		}
	}
}

func spawnFake(t *testing.T, env ...string) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rec, err := NewRecorder(t.TempDir() + "/log.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rec.Close() })
	c, err := Spawn(context.Background(), Config{
		Command:  exe,
		Args:     []string{"-test.run=XXX_NONE"},
		Env:      append([]string{"ACPPROBE_FAKE_AGENT=1"}, env...),
		Recorder: rec,
		Label:    "fake",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDefaultPolicyPicksAllowOnceByKindNotID(t *testing.T) {
	p := RequestPermissionParams{Options: []PermissionOption{
		{OptionID: "allow", Kind: "allow_always"},
		{OptionID: "weird-id-42", Kind: "allow_once"},
		{OptionID: "reject", Kind: "reject_once"},
	}}
	d := DefaultPolicy{}.Decide(p)
	if d.Outcome.Outcome != "selected" || d.Outcome.OptionID != "weird-id-42" || d.AllowOnceMissing {
		t.Fatalf("got %+v", d)
	}
	d = DefaultPolicy{}.Decide(RequestPermissionParams{Options: []PermissionOption{{OptionID: "r", Kind: "reject_once"}}})
	if d.Outcome.OptionID != "r" || !d.AllowOnceMissing {
		t.Fatalf("expected reject_once fallback, got %+v", d)
	}
	d = DefaultPolicy{}.Decide(RequestPermissionParams{})
	if d.Outcome.Outcome != "cancelled" || !d.AllowOnceMissing {
		t.Fatalf("expected cancelled, got %+v", d)
	}
}

func TestHandshakePromptPermission(t *testing.T) {
	c := spawnFake(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	init, err := c.Initialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if init.ProtocolVersion != 1 {
		t.Fatalf("protocol %d", init.ProtocolVersion)
	}
	s, err := c.NewSession(ctx, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionID != "sess-1" {
		t.Fatalf("session %q", s.SessionID)
	}
	tr, err := c.Prompt(ctx, s.SessionID, "do it")
	if err != nil {
		t.Fatal(err)
	}
	if tr.StopReason != "end_turn" || tr.Text != "hello world" || tr.ToolCalls != 1 || tr.Permissions != 1 {
		t.Fatalf("turn %+v", tr)
	}
	if c.Stats.AllowOnceMissing != 0 || c.Stats.PermissionRequests != 1 {
		t.Fatalf("stats %+v", c.Stats)
	}
	if _, err := c.LoadSession(ctx, t.TempDir(), "sess-1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.LoadSession(ctx, t.TempDir(), "nope", nil); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestAllowOnceMissingFallsBackToReject(t *testing.T) {
	c := spawnFake(t, "ACPPROBE_FAKE_OPTIONS=noallow")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := c.NewSession(ctx, t.TempDir(), nil)
	tr, err := c.Prompt(ctx, s.SessionID, "do it")
	if err != nil {
		t.Fatal(err)
	}
	if tr.StopReason != "end_turn" || c.Stats.AllowOnceMissing != 1 {
		t.Fatalf("turn %+v stats %+v", tr, c.Stats)
	}
}

func TestCancelAnswersPendingPermissionCancelled(t *testing.T) {
	c := spawnFake(t, "ACPPROBE_FAKE_SLOW=1")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := c.NewSession(ctx, t.TempDir(), nil)
	done := make(chan *TurnResult, 1)
	go func() {
		tr, err := c.Prompt(ctx, s.SessionID, "do it")
		if err != nil {
			t.Error(err)
		}
		done <- tr
	}()
	time.Sleep(100 * time.Millisecond) // fake is inside its 300ms sleep before asking permission
	if err := c.Cancel(s.SessionID); err != nil {
		t.Fatal(err)
	}
	tr := <-done
	if tr == nil || tr.StopReason != "cancelled" {
		t.Fatalf("turn %+v", tr)
	}
}

func TestCloseKillsProcessGroup(t *testing.T) {
	c := spawnFake(t)
	pid := c.PID()
	_ = c.Close()
	exited, _ := c.Exited()
	if !exited {
		t.Fatal("not exited")
	}
	// kill(-pgid, 0) fails with ESRCH once every member of the group is gone.
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Fatalf("process group %s still alive", strconv.Itoa(pid))
	}
}
