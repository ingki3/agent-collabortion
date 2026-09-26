package mcp_test

// T-AGENTFIX B4 (Lead 판정 2026-09-25: 이름 풀이는 하지 않는다): an artifact
// NAME where the id belongs is an argument error (exit 2) the agent can act
// on, refused before any request. 실측(게임 제작 방): Writer 가
// colab_artifact_get 에 이름을 넣어 서버 422 「화면을 새로고침」만 받았다.
//
// 회귀 주입: colab.requireArtifactID 가 늘 nil 을 내면 FAIL(요청이 나간다).

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
)

func TestArtifactToolsRefuseANameBeforeAnyRequest(t *testing.T) {
	for _, call := range []struct{ tool, arg string }{
		{"colab_artifact_get", "artifact"},
		{"colab_review_approve", "artifact"},
	} {
		t.Run(call.tool, func(t *testing.T) {
			s := clienttest.New(t)
			c := dial(t, newClient(t, s, nil))
			r := c.call("tools/call", map[string]any{"name": call.tool, "arguments": map[string]any{call.arg: "게임 기획서.md"}})
			if r.Error != nil || r.Result["isError"] != true {
				t.Fatalf("result = %+v, want an isError tool result", r)
			}
			e := r.Result["structuredContent"].(map[string]any)["error"].(map[string]any)
			if e["exit"] != float64(2) {
				t.Fatalf("exit = %v, want 2 (argument error): %v", e["exit"], e)
			}
			d, _ := e["detail"].(string)
			for _, want := range []string{"아티팩트 id(uuid)가 필요합니다 — 받은 값: \"게임 기획서.md\"", "Artifacts 줄에 id 와 함께 있다"} {
				if !strings.Contains(d, want) {
					t.Errorf("detail %q lacks %q", d, want)
				}
			}
			if len(s.Requests) != 0 {
				t.Fatalf("a name went to the server (%d requests) — it must be refused first", len(s.Requests))
			}
		})
	}
}
