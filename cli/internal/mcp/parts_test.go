package mcp_test

// colab-cli v0.9.5 through MCP: colab_message_post `parts` posts a part
// message; `parts` with `body` is an argument error; the tool schema lists
// `parts` and colab_room_messages lists `group`.
//
// 회귀 주입: server.go 의 colab.Post 를 colab.MessagePost 로 되돌리면 (parts) FAIL;
// Post 의 body 조합 검사를 끄면 (combo) FAIL.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func TestMCPMessagePostParts(t *testing.T) {
	s := clienttest.New(t)
	c := dial(t, newClient(t, s, nil))
	c.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "0"}})

	list := c.call("tools/list", nil)
	var postSchema, msgsSchema string
	for _, tl := range list.Result["tools"].([]any) {
		m := tl.(map[string]any)
		switch m["name"] {
		case "colab_message_post":
			postSchema = asJSON(m["inputSchema"])
		case "colab_room_messages":
			msgsSchema = asJSON(m["inputSchema"])
		}
	}
	if !strings.Contains(postSchema, `"parts"`) || strings.Contains(postSchema, `"required":["body"]`) {
		t.Fatalf("colab_message_post schema lacks parts or still requires body: %s", postSchema)
	}
	if !strings.Contains(msgsSchema, `"group"`) {
		t.Fatalf("colab_room_messages schema lacks group: %s", msgsSchema)
	}

	// (parts)
	r := c.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{
		"parts": []map[string]any{
			{"to": []string{"@Simplist"}, "body": "보고"},
			{"to": []string{"@Reviewer"}, "body": "요청"},
		},
	}})
	if r.Error != nil || r.Result["isError"] == true {
		t.Fatalf("parts = %+v", r)
	}
	sc := r.Result["structuredContent"].(map[string]any)
	if sc["group_id"] == "" || len(sc["parts"].([]any)) != 2 {
		t.Fatalf("structuredContent = %v", sc)
	}
	if !strings.HasSuffix(s.Requests[len(s.Requests)-1].URL.Path, "/message-groups") {
		t.Fatalf("parts went to %s", s.Requests[len(s.Requests)-1].URL.Path)
	}

	// (combo) parts + body → argument error, nothing posted.
	before := len(s.Posted)
	bad := c.call("tools/call", map[string]any{"name": "colab_message_post", "arguments": map[string]any{
		"body":  "섞인 글",
		"parts": []map[string]any{{"to": []string{"@Simplist"}, "body": "a"}, {"to": []string{"@Reviewer"}, "body": "b"}},
	}})
	if bad.Error != nil || bad.Result["isError"] != true || !strings.Contains(asJSON(bad.Result), "cannot be combined") {
		t.Fatalf("parts+body = %+v", bad)
	}
	if len(s.Posted) != before {
		t.Fatalf("parts+body reached the server")
	}
}

func asJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
