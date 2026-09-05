package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func exec(t *testing.T, env map[string]string, args ...string) (int, map[string]any, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, clienttest.Getenv(env), strings.NewReader(""), &out, &errb)
	var v map[string]any
	if out.Len() > 0 {
		if err := json.Unmarshal(out.Bytes(), &v); err != nil {
			t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
		}
	}
	return code, v, errb.String()
}

func errCode(v map[string]any) string {
	e, _ := v["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if code := run([]string{"version"}, func(string) string { return "" }, nil, &out, &out); code != 0 || !strings.HasPrefix(out.String(), "colab ") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestUsageExit2(t *testing.T) {
	env := clienttest.New(t).Env(t.TempDir())
	for _, args := range [][]string{{}, {"bogus"}, {"session"}, {"session", "nope"}, {"message"}, {"message", "post"}, {"message", "post", "--body", ""}, {"session", "get", "extra"}, {"lane", "delegate"}, {"mcp"}, {"session", "messages", "--limit", "0"}, {"session", "messages", "--limit", "201"}} {
		code, _, _ := exec(t, env, args...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

// E15-04
func TestNoTokenExit4(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	delete(env, "COLAB_TASK_TOKEN")
	code, v, stderr := exec(t, env, "message", "post", "--body", "hi")
	if code != 4 || errCode(v) != "no_token" || !strings.Contains(stderr, "no token") {
		t.Fatalf("code=%d v=%v stderr=%q", code, v, stderr)
	}
	if len(s.Posted) != 0 {
		t.Fatal("posted without a token")
	}
}

// E11-04
func TestRevokedExit4(t *testing.T) {
	s := clienttest.New(t)
	s.Revoked = true
	env := s.Env(t.TempDir())
	code, v, _ := exec(t, env, "message", "post", "--body", "hi", "--json")
	if code != 4 || errCode(v) != "token_revoked" {
		t.Fatalf("code=%d v=%v", code, v)
	}
	code, v, _ = exec(t, env, "session", "get")
	if code != 4 || errCode(v) != "token_revoked" {
		t.Fatalf("session get: code=%d v=%v", code, v)
	}
}

func TestRefusedExit3AndUnreachable5(t *testing.T) {
	s := clienttest.New(t)
	s.Fail, s.FailCode = 403, "not_participant"
	env := s.Env(t.TempDir())
	if code, v, _ := exec(t, env, "session", "messages"); code != 3 || errCode(v) != "not_participant" {
		t.Fatalf("403: code=%d v=%v", code, v)
	}
	s.Fail = 503
	if code, _, _ := exec(t, env, "session", "get"); code != 5 {
		t.Fatalf("503: code=%d", code)
	}
	env["COLAB_SERVER_URL"] = "http://127.0.0.1:1"
	if code, v, _ := exec(t, env, "session", "get"); code != 5 || errCode(v) != "unreachable" {
		t.Fatalf("dead: code=%d v=%v", code, v)
	}
}

// E8-04 at the CLI level: attempt 1 posts seq 1·2 (two processes); the task is
// re-queued as attempt 2 (last_seq = 2) — its first post is seq 3, and a
// re-send of seq 1 (explicit key or COLAB_CLIENT_SEQ) is replayed, not stored.
func TestPostIdempotentAcrossAttempts(t *testing.T) {
	s := clienttest.New(t)
	s.Attempt = 1
	env := s.Env(t.TempDir())
	code, v1, _ := exec(t, env, "message", "post", "--body", "m1", "--mention", "@Reviewer")
	if code != 0 {
		t.Fatalf("post: %d %v", code, v1)
	}
	if v1["idempotency_key"] != clienttest.Key(1) || v1["replayed"] != false {
		t.Fatalf("v1 = %v", v1)
	}
	if tr, _ := v1["triggered"].([]any); len(tr) != 1 || tr[0] != "Reviewer" {
		t.Fatalf("triggered = %v", v1["triggered"])
	}
	code, v2, _ := exec(t, env, "message", "post", "--body", "m2")
	if code != 0 || v2["idempotency_key"] != clienttest.Key(2) {
		t.Fatalf("v2 = %v", v2)
	}

	// kill → re-queue as attempt 2 (same host: same state dir; daemon sets COLAB_TASK_ATTEMPT=2)
	s.Attempt = 2
	env["COLAB_TASK_ATTEMPT"] = "2"
	code, v3, _ := exec(t, env, "message", "post", "--body", "m1")
	if code != 0 || v3["idempotency_key"] != clienttest.Key(3) || v3["replayed"] != false {
		t.Fatalf("attempt 2 first post = %v (want seq 3)", v3)
	}
	code, v4, _ := exec(t, env, "message", "post", "--body", "m1", "--idempotency-key", clienttest.Key(1))
	if code != 0 || v4["replayed"] != true || v4["message_id"] != v1["message_id"] {
		t.Fatalf("v4 = %v", v4)
	}
	env["COLAB_CLIENT_SEQ"] = "2"
	code, v5, _ := exec(t, env, "message", "post", "--body", "m2")
	if code != 0 || v5["replayed"] != true || v5["message_id"] != v2["message_id"] || v5["idempotency_key"] != clienttest.Key(2) {
		t.Fatalf("v5 = %v", v5)
	}
	if len(s.Posted) != 3 {
		t.Fatalf("server has %d messages, want 3 (re-sends stored 0)", len(s.Posted))
	}
}

func TestSessionGetAndMessagesJSON(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	code, v, _ := exec(t, env, "session", "get", "--json")
	if code != 0 || v["goal"] != "Find 3 competitors" {
		t.Fatalf("get: %d %v", code, v)
	}
	exec(t, env, "message", "post", "--body", "root")
	code, v, _ = exec(t, env, "session", "messages", "--limit", "10")
	if code != 0 || v["included"] != float64(1) || v["truncated"] != false {
		t.Fatalf("messages: %d %v", code, v)
	}
	items := v["items"].([]any)
	root := items[0].(map[string]any)["id"].(string)
	exec(t, env, "message", "post", "--body", "reply", "--reply-to", root)
	code, v, _ = exec(t, env, "session", "messages", "--thread", root)
	if code != 0 || v["included"] != float64(2) {
		t.Fatalf("thread: %d %v", code, v)
	}
	code, v, _ = exec(t, env, "session", "messages", "--since", root)
	if code != 0 || v["included"] != float64(1) {
		t.Fatalf("since: %d %v", code, v)
	}
}

func TestMCPServeViaCLI(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	in := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"colab_session_get","arguments":{}}}
`
	var out, errb bytes.Buffer
	if code := run([]string{"mcp", "serve"}, clienttest.Getenv(env), strings.NewReader(in), &out, &errb); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses (notification has none), got %d:\n%s", len(lines), out.String())
	}
	if !strings.Contains(lines[1], `"goal":"Find 3 competitors"`) {
		t.Fatalf("tool result: %s", lines[1])
	}
}
