package mcp_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/mcp"
)

// dialWith is dial with Options (--allow).
func dialWith(t *testing.T, c *client.Client, o mcp.Options) *conn {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- mcp.ServeWith(context.Background(), c, inR, outW, "test", o); outW.Close() }()
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
	return &conn{t: t, w: inW, dec: newDecoder(outR)}
}

func toolNames(r rpc) []string {
	var out []string
	for _, x := range r.Result["tools"].([]any) {
		out = append(out, x.(map[string]any)["name"].(string))
	}
	return out
}

// FilterTools keeps Tools' order and only the allowed commands' tools; an
// empty list keeps everything (daemon-protocol §4.1 "비면 전부"); a name that
// is not a command registers nothing.
func TestFilterTools(t *testing.T) {
	if got := mcp.FilterTools(nil); len(got) != len(mcp.Tools) {
		t.Fatalf("nil allow → %d tools, want all %d", len(got), len(mcp.Tools))
	}
	if got := mcp.FilterTools([]string{}); len(got) != len(mcp.Tools) {
		t.Fatalf("empty allow → %d tools, want all %d", len(got), len(mcp.Tools))
	}
	got := mcp.FilterTools([]string{"review_reject", "session_get", "not_a_command", "review_approve"})
	if len(got) != 3 || got[0].Name != "colab_session_get" || got[1].Name != "colab_review_approve" || got[2].Name != "colab_review_reject" {
		names := make([]string, 0, len(got))
		for _, x := range got {
			names = append(names, x.Name)
		}
		t.Fatalf("filtered = %v, want session_get · review_approve · review_reject in table order", names)
	}
	// Every command has a tool, so a full allow list is the full table.
	all := make([]string, 0, len(client.AllCommands))
	for _, c := range client.AllCommands {
		all = append(all, string(c))
	}
	if got := mcp.FilterTools(all); len(got) != len(mcp.Tools) {
		t.Fatalf("all commands → %d tools, want %d", len(got), len(mcp.Tools))
	}
}

// `colab mcp serve --allow` (colab-cli.md §2.5·§3): tools/list is the
// subset — the reviewer row here — and a call to a tool the list left out is
// a tool result with the CLI's command_not_allowed error, sent nowhere.
func TestServeAllowRegistersOnlyTheSubset(t *testing.T) {
	reviewer := []string{"session_get", "session_messages", "message_post", "status_set", "decision_record",
		"artifact_get", "review_approve", "review_reject", "hitl_ask", "hitl_request_info"}
	s := clienttest.New(t)
	s.Role = "reviewer"
	var unknown []string
	cfg := client.FromEnv(clienttest.Getenv(s.Env(t.TempDir())))
	cfg.AllowedCommands = reviewer // what cmd/colab does for --allow: the same list gates the actions
	c := dialWith(t, client.New(cfg), mcp.Options{Allow: append(reviewer, "bogus"), Unknown: func(n string) { unknown = append(unknown, n) }})

	names := toolNames(c.call("tools/list", nil))
	if len(names) != len(reviewer) {
		t.Fatalf("tools/list = %v, want the %d reviewer tools", names, len(reviewer))
	}
	for _, n := range names {
		if n == "colab_lane_delegate" || n == "colab_artifact_submit" || n == "colab_hitl_approve_request" {
			t.Fatalf("tools/list registered %s, which the reviewer row denies", n)
		}
	}
	if len(unknown) != 1 || unknown[0] != "bogus" {
		t.Fatalf("unknown names reported = %v, want [bogus]", unknown)
	}

	r := c.call("tools/call", map[string]any{"name": "colab_lane_delegate", "arguments": map[string]any{"agent": "Lead", "brief": "b"}})
	if r.Error != nil || r.Result["isError"] != true {
		t.Fatalf("unregistered tool call = %+v, want an isError tool result (not a protocol error)", r)
	}
	e := r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != client.ErrCodeCommandNotAllowed || e["exit"] != float64(3) || e["command"] != "lane_delegate" {
		t.Fatalf("error = %v", e)
	}
	if e["detail"] != "이 역할은 lane delegate 를 쓸 수 없습니다" {
		t.Fatalf("detail = %q (no context fetched → no role in the sentence)", e["detail"])
	}
	if len(s.Requests) != 0 {
		t.Fatalf("a filtered tool sent %d requests; want none", len(s.Requests))
	}

	// A registered tool works, and after its context read the sentence names
	// the role.
	r = c.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{"body": "hi", "mention": []string{"@Lead"}}})
	if r.Error != nil || r.Result["isError"] == true {
		t.Fatalf("registered tool = %+v", r)
	}
	r = c.call("tools/call", map[string]any{"name": "colab_artifact_submit", "arguments": map[string]any{"type": "doc", "file": "x"}})
	e = r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != client.ErrCodeCommandNotAllowed || e["detail"] != "이 역할(reviewer)은 artifact submit 를 쓸 수 없습니다" {
		t.Fatalf("error after a context read = %v", e)
	}

	// A name that is not a tool at all stays a protocol error, as before.
	if r := c.call("tools/call", map[string]any{"name": "colab_nope"}); r.Error == nil || !strings.Contains(r.Error.Message, "unknown tool") {
		t.Fatalf("unknown tool = %+v", r)
	}
}

// Without --allow the table is whole and the gate is the actions' own:
// getCliContext.allowed_commands refuses through the tool result.
func TestServeWithoutAllowGatesFromContext(t *testing.T) {
	s := clienttest.New(t)
	s.Role = "writer"
	s.AllowedCommands = []string{"session_get", "message_post"}
	c := dial(t, newClient(t, s, nil))
	if names := toolNames(c.call("tools/list", nil)); len(names) != len(mcp.Tools) {
		t.Fatalf("tools/list = %d, want all %d without --allow", len(names), len(mcp.Tools))
	}
	r := c.call("tools/call", map[string]any{"name": "colab_review_approve", "arguments": map[string]any{"artifact": clienttest.ArtifactID}})
	e := r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != client.ErrCodeCommandNotAllowed || e["detail"] != "이 역할(writer)은 review approve 를 쓸 수 없습니다" {
		t.Fatalf("error = %v", e)
	}
	for _, q := range s.Requests {
		if !strings.HasSuffix(q.URL.Path, "/cli/context") {
			t.Fatalf("refused tool sent %s", q.URL.Path)
		}
	}
}
