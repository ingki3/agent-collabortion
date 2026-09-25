package httpapi

// T-SURFACE (harness §10 v0.9.6): every server-written sentence that names a
// colab command speaks the tool surface of the bundle's runtime_kind — MCP
// tool names for claude_code, the shell command for hermes — read off a real
// claim. Same agent, same surface → [1]~[5] byte for byte (E12-11).
//
// 회귀 주입: queue.SurfaceFor 가 runtime_kind 와 무관하게 shellSurface 를 내면
// (claude_code) FAIL — 실측(삼성전자 미션 12:32)의 브리프가 그 모양이다.
// mcpSurface 를 내면 (hermes) FAIL.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

var shellColab = regexp.MustCompile("`colab [a-z]")

// surfaceBundleText is the brief and the turn prompt of a Lead turn that
// carries every surface-dependent sentence: [2], [3] (lead), [6] (an
// artifact), the <history> demotion line (a long detail), and the thread
// closing line (the trigger is a thread reply).
func surfaceBundleText(t *testing.T, f *p2Fixture) (brief, prompt string) {
	t.Helper()
	tok, first := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	root := msgID(f.agentPost(t, tok, map[string]any{"content": "미션 요약입니다.", "detail": strings.Repeat("가", 500)}))
	if code, out := f.submit(t, f.sessionID, tok, "report.md", "doc", []byte("# r")); code != 201 {
		t.Fatalf("artifact submit = %d %v", code, out)
	}
	f.endTurn(t, first)
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 요약 질문", "parent_id": root})
	b := f.claimBundle(t, f.triggerTask(t, out, f.leadUUID))
	return b.Brief.Text, b.Prompt
}

func TestSurfaceBriefClaudeCodeNamesToolsOnly(t *testing.T) {
	f := newP2Fixture(t) // agents are claude_code
	brief, prompt := surfaceBundleText(t, f)
	all := brief + prompt
	if m := shellColab.FindAllString(all, -1); len(m) > 0 {
		t.Fatalf("(claude_code) the bundle names shell commands %v:\n%s\n----\n%s", m, brief, prompt)
	}
	for _, want := range []string{
		"[2] Workspace rules and colab tools\n", "`colab_message_post` tool", "`colab_room_messages`",
		queue.DetailRuleMCP, "`colab_hitl_ask` tool", "read one with the `colab_artifact_get` tool",
		"`colab_room_messages` 툴의 `thread: \"", queue.ThreadReplyInstructionMCP,
		"Post your reply with the `colab_message_post` tool",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("(claude_code) bundle lacks %q", want)
		}
	}
}

func TestSurfaceBriefHermesNamesShellOnly(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent_profile SET runtime_kind = 'hermes' WHERE agent_id = $1`, f.leadUUID); err != nil {
		t.Fatal(err)
	}
	brief, prompt := surfaceBundleText(t, f)
	all := brief + prompt
	if strings.Contains(all, "colab_") {
		t.Fatalf("(hermes) the bundle names MCP tools the hermes agent does not have:\n%s\n----\n%s", brief, prompt)
	}
	for _, want := range []string{
		"[2] Workspace rules and colab CLI\n", "`colab message post --body", queue.DetailRule,
		"`colab hitl ask`", "`colab artifact get <id>`", "`colab room messages --thread ",
		queue.ThreadReplyInstruction, "Post your reply with `colab message post`",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("(hermes) bundle lacks %q", want)
		}
	}
}

// T-AGENTFIX B4: the [6] artifact line carries the artifact's id — the only
// thing `artifact get` accepts (colab-cli §2.1). 실측(게임 제작 방): 목록이
// 이름만 실어 Writer 가 colab_artifact_get 에 이름을 넣고 422 를 받았다.
//
// 회귀 주입: bundle.go briefContext 의 줄에서 `, id %s` 를 빼면 FAIL.
func TestSurfaceBriefArtifactLineCarriesID(t *testing.T) {
	for _, kind := range []string{"claude_code", "hermes"} {
		t.Run(kind, func(t *testing.T) {
			f := newP2Fixture(t)
			if _, err := f.pool.Exec(t.Context(), `UPDATE agent_profile SET runtime_kind = $2 WHERE agent_id = $1`, f.leadUUID, kind); err != nil {
				t.Fatal(err)
			}
			brief, _ := surfaceBundleText(t, f)
			var id string
			if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM artifact WHERE session_id = $1 AND name = 'report.md'`, mustUUID(t, f.sessionID)).Scan(&id); err != nil {
				t.Fatal(err)
			}
			if want := "- report.md (doc, v1, id " + id + ")"; !strings.Contains(brief, want) {
				t.Fatalf("(%s) [6] lacks %q:\n%s", kind, want, brief)
			}
		})
	}
}

// Same agent, same surface: [1]~[5] byte for byte across two turns (E12-11).
func TestSurfaceBriefStablePrefix(t *testing.T) {
	f := newP2Fixture(t)
	a := f.claimBundle(t, f.triggerTask(t, f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 하나"}), f.wUUID))
	b := f.claimBundle(t, f.triggerTask(t, f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 둘"}), f.wUUID))
	if stablePrefix(a.Brief.Text) != stablePrefix(b.Brief.Text) {
		t.Fatalf("[1]~[5] changed between two turns of the same agent")
	}
}
