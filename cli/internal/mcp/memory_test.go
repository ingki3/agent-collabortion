package mcp_test

import (
	"encoding/json"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/mcp"
)

var memoryTools = []string{"colab_memory_note", "colab_memory_supersede", "colab_memory_retire", "colab_memory_get"}

// The four ledger tools (colab-cli v0.9.12 §3) are registered when --allow
// lists their commands and left out when it does not; a full table has all
// four, each with an object inputSchema.
// 회귀 주입: Tools 에서 colab_memory_get 을 빼면 FAIL; ToolCommand 매핑을 깨면 FAIL.
func TestMemoryToolsFollowAllow(t *testing.T) {
	has := func(tools []mcp.Tool) map[string]bool {
		m := map[string]bool{}
		for _, tl := range tools {
			m[tl.Name] = true
		}
		return m
	}
	all := has(mcp.Tools)
	for _, n := range memoryTools {
		if !all[n] {
			t.Fatalf("Tools lacks %s", n)
		}
	}
	got := has(mcp.FilterTools([]string{"room_get", "memory_note", "memory_supersede", "memory_retire", "memory_get"}))
	for _, n := range memoryTools {
		if !got[n] {
			t.Fatalf("--allow with the memory commands left out %s: %v", n, got)
		}
	}
	if len(got) != 5 {
		t.Fatalf("filtered = %v, want room_get + the 4 ledger tools", got)
	}
	// A question turn's list (memory_get only of the four).
	got = has(mcp.FilterTools([]string{"room_get", "memory_get"}))
	if !got["colab_memory_get"] || got["colab_memory_note"] || got["colab_memory_supersede"] || got["colab_memory_retire"] {
		t.Fatalf("filtered = %v, want memory_get alone of the ledger tools", got)
	}
	got = has(mcp.FilterTools([]string{"room_get", "message_post"}))
	for _, n := range memoryTools {
		if got[n] {
			t.Fatalf("--allow without memory commands registered %s", n)
		}
	}
	for _, tl := range mcp.Tools {
		for _, n := range memoryTools {
			if tl.Name != n {
				continue
			}
			var s map[string]any
			if err := json.Unmarshal(tl.InputSchema, &s); err != nil || s["type"] != "object" {
				t.Fatalf("%s inputSchema = %s (%v)", n, tl.InputSchema, err)
			}
		}
	}
}

// Through the server: a ledger tool --allow left out is the CLI's
// command_not_allowed result with nothing sent; an allowed one reaches the
// ledger path; kind plan from a known non-lead role is refused before the
// POST, the same as the CLI.
func TestMemoryToolsThroughServe(t *testing.T) {
	s := clienttest.New(t)
	cfg := client.FromEnv(clienttest.Getenv(s.Env(t.TempDir())))
	cfg.AllowedCommands = []string{"memory_get"}
	c := dialWith(t, client.New(cfg), mcp.Options{Allow: []string{"memory_get"}})
	if names := toolNames(c.call("tools/list", nil)); len(names) != 1 || names[0] != "colab_memory_get" {
		t.Fatalf("tools/list = %v", names)
	}
	r := c.call("tools/call", map[string]any{"name": "colab_memory_note", "arguments": map[string]any{"kind": "fact", "content": "c"}})
	e := r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != client.ErrCodeCommandNotAllowed || e["command"] != "memory_note" {
		t.Fatalf("error = %v", e)
	}
	if len(s.Requests) != 0 {
		t.Fatalf("a filtered ledger tool sent %d requests", len(s.Requests))
	}
	r = c.call("tools/call", map[string]any{"name": "colab_memory_get", "arguments": map[string]any{"status": "all"}})
	if r.Error != nil || r.Result["isError"] == true {
		t.Fatalf("memory_get = %+v", r)
	}
	if sc := r.Result["structuredContent"].(map[string]any); sc["work_id"] != clienttest.WorkID || sc["status"] != "all" {
		t.Fatalf("structuredContent = %v", sc)
	}

	s2 := clienttest.New(t)
	s2.Role = "writer"
	c2 := dial(t, newClient(t, s2, nil))
	r = c2.call("tools/call", map[string]any{"name": "colab_memory_note", "arguments": map[string]any{"kind": "progress", "content": "반쯤"}})
	e = r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != "memory_kind_forbidden" || e["exit"] != float64(3) {
		t.Fatalf("error = %v", e)
	}
	if len(s2.MemoryCalls) != 0 {
		t.Fatalf("known non-lead plan reached the server: %+v", s2.MemoryCalls)
	}
	r = c2.call("tools/call", map[string]any{"name": "colab_memory_supersede", "arguments": map[string]any{"memory": clienttest.RetiredMemoryID, "content": "c"}})
	e = r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
	if e["code"] != "memory_not_active" || e["exit"] != float64(3) {
		t.Fatalf("error = %v", e)
	}
}
