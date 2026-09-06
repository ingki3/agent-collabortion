package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/mcp"
)

type rpc struct {
	ID     json.RawMessage `json:"id"`
	Result map[string]any  `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// conn drives Serve over in-memory pipes like a stdio client would.
type conn struct {
	t   *testing.T
	w   io.WriteCloser
	dec *json.Decoder
	id  int
}

func dial(t *testing.T, c *client.Client) *conn {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- mcp.Serve(context.Background(), c, inR, outW, "test"); outW.Close() }()
	t.Cleanup(func() {
		inW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("serve did not exit on stdin close")
		}
	})
	return &conn{t: t, w: inW, dec: json.NewDecoder(outR)}
}

func (c *conn) call(method string, params any) rpc {
	c.t.Helper()
	c.id++
	msg := map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	if _, err := c.w.Write(append(b, '\n')); err != nil {
		c.t.Fatal(err)
	}
	var r rpc
	if err := c.dec.Decode(&r); err != nil {
		c.t.Fatalf("decode: %v", err)
	}
	if string(r.ID) != json.Number(itoa(c.id)).String() {
		c.t.Fatalf("id mismatch: %s vs %d", r.ID, c.id)
	}
	return r
}

func (c *conn) notify(method string) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	c.w.Write(append(b, '\n'))
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func newClient(t *testing.T, s *clienttest.Server, mut func(map[string]string)) *client.Client {
	env := s.Env(t.TempDir())
	if mut != nil {
		mut(env)
	}
	return client.New(client.FromEnv(clienttest.Getenv(env)))
}

func TestRoundTrip(t *testing.T) {
	s := clienttest.New(t)
	c := dial(t, newClient(t, s, nil))

	init := c.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "0"}})
	if init.Error != nil || init.Result["protocolVersion"] != mcp.ProtocolVersion {
		t.Fatalf("initialize = %+v", init)
	}
	if info := init.Result["serverInfo"].(map[string]any); info["name"] != "colab" || info["version"] != "test" {
		t.Fatalf("serverInfo = %v", info)
	}
	c.notify("notifications/initialized")

	list := c.call("tools/list", nil)
	tools := list.Result["tools"].([]any)
	var names []string
	for _, tl := range tools {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	// contracts/colab-cli.md §3: one tool per command, named for the command
	// path with underscores. Order is stable so tools/list is diffable.
	want := "colab_session_get,colab_session_messages,colab_message_post," +
		"colab_status_set,colab_lane_delegate,colab_decision_record," +
		"colab_artifact_submit,colab_artifact_get,colab_review_approve,colab_review_reject," +
		"colab_hitl_ask,colab_hitl_approve_request,colab_hitl_request_info"
	if strings.Join(names, ",") != want {
		t.Fatalf("tools = %v\nwant  %s", names, want)
	}
	for _, tl := range tools {
		if _, ok := tl.(map[string]any)["inputSchema"].(map[string]any); !ok {
			t.Fatalf("tool without inputSchema: %v", tl)
		}
	}

	post := c.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{"body": "hello", "mention": []string{"@Reviewer"}}})
	if post.Error != nil || post.Result["isError"] == true {
		t.Fatalf("post = %+v", post)
	}
	sc := post.Result["structuredContent"].(map[string]any)
	if sc["message_id"] == "" || sc["triggered"].([]any)[0] != "Reviewer" || sc["idempotency_key"] != clienttest.Key(1) {
		t.Fatalf("structuredContent = %v", sc)
	}
	text := post.Result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `"message_id"`) {
		t.Fatalf("content text = %s", text)
	}

	get := c.call("tools/call", map[string]any{"name": "colab_session_get", "arguments": map[string]any{}})
	if get.Error != nil || get.Result["structuredContent"].(map[string]any)["goal"] != "Find 3 competitors" {
		t.Fatalf("get = %+v", get)
	}

	msgs := c.call("tools/call", map[string]any{"name": "colab_session_messages", "arguments": map[string]any{"limit": 10}})
	if msgs.Error != nil || msgs.Result["structuredContent"].(map[string]any)["included"] != float64(1) {
		t.Fatalf("messages = %+v", msgs)
	}
	// N4: explicit limit 0 is a usage error (exit 2 in the error object), not "default".
	bad := c.call("tools/call", map[string]any{"name": "colab_session_messages", "arguments": map[string]any{"limit": 0}})
	if bad.Error != nil || bad.Result["isError"] != true {
		t.Fatalf("limit 0 = %+v", bad)
	}
	if e := bad.Result["structuredContent"].(map[string]any)["error"].(map[string]any); e["exit"] != float64(2) {
		t.Fatalf("limit 0 error = %v", e)
	}

	// string form of mention is accepted too.
	post2 := c.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{"body": "again", "mention": "@Reviewer,@Lead"}})
	sc2 := post2.Result["structuredContent"].(map[string]any)
	if sc2["suppressed"].([]any)[0] != "Lead" || sc2["idempotency_key"] != clienttest.Key(2) {
		t.Fatalf("post2 = %v", sc2)
	}
	// v0.3: the MCP tool posts through client.PostMessage, so each derived key
	// arrives with X-Colab-Client-Seq = its seq and the fake's last_seq = max.
	if len(s.Posted) != 2 || s.Posted[0].ClientSeq != 1 || s.Posted[1].ClientSeq != 2 || s.LastSeq != 2 {
		t.Fatalf("client_seq headers = %+v last_seq=%d, want 1,2 / 2", s.Posted, s.LastSeq)
	}

	if r := c.call("ping", nil); r.Error != nil {
		t.Fatalf("ping = %+v", r)
	}
	// colab_hitl_ask used to stand in here as "a tool that does not exist
	// yet"; it exists as of P3 (§2.4), so the probe is a name that never will.
	if r := c.call("tools/call", map[string]any{"name": "colab_session_delete", "arguments": map[string]any{}}); r.Error == nil || r.Error.Code != -32602 {
		t.Fatalf("unknown tool = %+v", r)
	}
	if r := c.call("resources/list", nil); r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("unknown method = %+v", r)
	}
}

// Command failures are tool results (isError) with the CLI's error JSON, not
// JSON-RPC errors — the model needs code/detail to react (E11-04, E15-04).
func TestToolErrors(t *testing.T) {
	s := clienttest.New(t)
	s.Revoked = true
	c := dial(t, newClient(t, s, nil))
	r := c.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{"body": "orphan"}})
	if r.Error != nil || r.Result["isError"] != true {
		t.Fatalf("r = %+v", r)
	}
	e := r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != "token_revoked" || e["exit"] != float64(4) || e["status"] != float64(401) {
		t.Fatalf("error = %v", e)
	}
	if len(s.Posted) != 0 {
		t.Fatal("stored a message with a revoked token")
	}

	c2 := dial(t, newClient(t, s, func(e map[string]string) { delete(e, "COLAB_TASK_TOKEN") }))
	r = c2.call("tools/call", map[string]any{"name": "colab_session_get"})
	e = r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if r.Result["isError"] != true || e["code"] != "no_token" {
		t.Fatalf("no token = %v", r.Result)
	}
	r = c2.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{}})
	e = r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["exit"] != float64(2) {
		t.Fatalf("missing body should be exit 2: %v", e)
	}
}

func TestParseErrorAndBatch(t *testing.T) {
	s := clienttest.New(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go func() { mcp.Serve(context.Background(), newClient(t, s, nil), inR, outW, "t"); outW.Close() }()
	dec := json.NewDecoder(outR)
	inW.Write([]byte("not json\n[{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\"}]\n"))
	var r1, r2 rpc
	if err := dec.Decode(&r1); err != nil || r1.Error == nil || r1.Error.Code != -32700 {
		t.Fatalf("parse error = %+v (%v)", r1, err)
	}
	if err := dec.Decode(&r2); err != nil || r2.Error == nil || r2.Error.Code != -32600 {
		t.Fatalf("batch = %+v (%v)", r2, err)
	}
	inW.Close()
}

// The MCP tool is the same function the command calls, so `type: "diff"`
// without `file` builds the diff of the server process's own workdir. And the
// schema deliberately has no field for pointing git anywhere else (FR-6.1) —
// the diff an agent can submit is its own worktree's, or none.
func TestArtifactSubmitDiffTool(t *testing.T) {
	s := clienttest.New(t)
	dir := mcpDiffRepo(t)
	t.Chdir(dir)
	c := dial(t, client.New(client.FromEnv(clienttest.Getenv(s.Env(t.TempDir())))))
	c.call("initialize", map[string]any{"protocolVersion": mcp.ProtocolVersion})
	c.notify("notifications/initialized")

	var schema map[string]any
	for _, tl := range c.call("tools/list", nil).Result["tools"].([]any) {
		if m := tl.(map[string]any); m["name"] == "colab_artifact_submit" {
			schema = m["inputSchema"].(map[string]any)
		}
	}
	if schema == nil {
		t.Fatal("colab_artifact_submit is missing from tools/list")
	}
	props := schema["properties"].(map[string]any)
	if _, ok := props["base"]; !ok {
		t.Fatal("schema has no `base` — an agent cannot say what to diff against")
	}
	for _, forbidden := range []string{"workdir", "dir", "repo", "path"} {
		if _, ok := props[forbidden]; ok {
			t.Fatalf("schema exposes %q: the diff must always be of this workdir (FR-6.1)", forbidden)
		}
	}
	// `file` is required for every other type; the action enforces that.
	if req := schema["required"].([]any); len(req) != 1 || req[0] != "type" {
		t.Fatalf("required = %v, want [type] only (file is optional for diff)", req)
	}

	r := c.call("tools/call", map[string]any{"name": "colab_artifact_submit",
		"arguments": map[string]any{"type": "diff", "description": "탈퇴 API"}})
	if r.Error != nil || r.Result["isError"] == true {
		t.Fatalf("submit = %+v", r)
	}
	sc := r.Result["structuredContent"].(map[string]any)
	if sc["name"] != "backend.diff" {
		t.Fatalf("name = %v", sc["name"])
	}
	d, ok := sc["diff"].(map[string]any)
	if !ok || d["branch"] != "colab/S/backend" || d["base"] != "main" {
		t.Fatalf("diff summary = %v", sc["diff"])
	}
	sub := s.Submissions[0]
	if !strings.HasPrefix(string(sub.Data), "# colab-diff: branch=colab/S/backend base=main commit=") {
		t.Fatalf("body head = %q", strings.SplitN(string(sub.Data), "\n", 2)[0])
	}
	if !strings.HasSuffix(sub.Fields["description"], "\n탈퇴 API") {
		t.Fatalf("description = %q", sub.Fields["description"])
	}
	// An argument that names another repository is simply not in the schema,
	// and an unknown key changes nothing about what gets diffed.
	r2 := c.call("tools/call", map[string]any{"name": "colab_artifact_submit",
		"arguments": map[string]any{"type": "diff", "workdir": "/etc"}})
	if r2.Error != nil || r2.Result["isError"] == true {
		t.Fatalf("submit with a stray key = %+v", r2)
	}
	if d2 := r2.Result["structuredContent"].(map[string]any)["diff"].(map[string]any); d2["branch"] != "colab/S/backend" {
		t.Fatalf("a stray `workdir` moved the diff: %v", d2)
	}
}

// mcpDiffRepo is an agent worktree with one uncommitted change.
func mcpDiffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := osexec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=colab", "GIT_AUTHOR_EMAIL=colab@example.com",
			"GIT_COMMITTER_NAME=colab", "GIT_COMMITTER_EMAIL=colab@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte("package api\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "api.go")
	git("commit", "-qm", "base")
	git("checkout", "-q", "-b", "colab/S/backend")
	if err := os.WriteFile(filepath.Join(dir, "api.go"), []byte("package api\n\nfunc Delete() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}
