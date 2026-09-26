package loop

// harness §10 v0.8.10 / daemon-protocol §4.1 v0.8.2 — 번들 `task.allowed_commands`
// 로 MCP 툴 목록·래퍼·브리프 [2] 를 자른다 (K-19, T-D13).
//
// (a) claude_code: session/new.mcpServers[colab].args 에 `--allow <list>`,
//     _meta.systemPrompt.append 의 [2] 에 허용 명령만 + "이 역할은 … 을 쓰지 않는다".
// (b) hermes: 래퍼 파일에 `export COLAB_ALLOWED_COMMANDS='<list>'`, COLAB_BRIEF.md
//     의 [2] 도 같은 모양(래퍼 경로로 치환된 채). 런타임 프로세스 env 에는 그 변수 없음(§2.1).
// (c) 비면(옛 서버): `--allow` 없음 · export 없음 · 브리프 그대로.
//
// 회귀 주입(§0-12 모양, 빌드를 깨지 않게):
//   - acp.ColabMCPServer 의 `commands.Args(allowed)` 를 `commands.Args(allowed[:0])` 로 → (a) args
//   - loop.wrapperEnv 의 `if e := …; e != ""` 를 `e != "" && false` 로 → (b) export
//   - loop.runAttempt 의 RestrictCommands 호출을 지우고 `_ = brief.RestrictCommands` 로 → (a)(b) 브리프
//   - brief.RestrictCommands 첫 줄을 `if true || len(allowed) == 0` 로 → (c) 는 그대로 통과하고 (a)(b) 가 깨진다
//
// 레시피: acpfake record(실제 session/new 본문) + phaseHook(preparing 시점의 디스크) — T-D9b.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/brief"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

// The server's [2] as bundle.go writes it, plus [4] so the section has an end.
const briefWith2 = "[1] Agent Identity\nYou are Rev, reviewer.\n\n" +
	"[2] Workspace rules and colab CLI\n" +
	"- Post every reply to the session with `colab message post --body \"<text>\"` (or the colab_message_post MCP tool).\n" +
	"- Read more history with `colab room messages`, room details with `colab room get`.\n\n" +
	"[4] Session\nGoal: g\n\n[5] Roster\n- Rev\n\n[8] Instruction precedence: user instruction > session goal.\n"

var twoCommands = []string{"room_get", "message_post"}

// colabServer returns the colab entry of the recorded session/new, and the
// _meta.systemPrompt.append text.
func sessionNewOf(t *testing.T, record string) (colab acp.MCPServer, briefText string) {
	t.Helper()
	recs, err := acpfake.ReadRecords(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Method != acp.MethodSessionNew {
			continue
		}
		var p struct {
			MCPServers []acp.MCPServer `json:"mcpServers"`
			Meta       struct {
				SystemPrompt struct {
					Append string `json:"append"`
				} `json:"systemPrompt"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil {
			t.Fatal(err)
		}
		for _, s := range p.MCPServers {
			if s.Name == acp.ColabMCPName {
				colab = s
			}
		}
		return colab, p.Meta.SystemPrompt.Append
	}
	t.Fatal("no session/new recorded")
	return
}

func section2Of(t *testing.T, text string) string {
	t.Helper()
	i, j := strings.Index(text, "[2] "), strings.Index(text, "\n[4] ")
	if i < 0 || j < 0 {
		t.Fatalf("no [2]/[4]:\n%s", text)
	}
	return text[i:j]
}

// assertRestricted checks the daemon's [2] lines. The allowed line speaks the
// surface's words (harness §10 v0.9.6): tool names for claude_code (mcp),
// the shell spelling for hermes (cli_wrapper, later rewritten to the wrapper).
func assertRestricted(t *testing.T, s2 string, mcp bool) {
	t.Helper()
	if strings.Contains(s2, "lane delegate") || strings.Contains(s2, "colab_lane_delegate") {
		t.Fatalf("[2] names a denied command:\n%s", s2)
	}
	if mcp {
		if !strings.Contains(s2, "- 이 역할이 쓸 수 있는 colab 툴: `colab_room_get`, `colab_message_post`.") {
			t.Fatalf("[2] does not list the allowed tools:\n%s", s2)
		}
	} else if !strings.Contains(s2, "room get`, `") || !strings.Contains(s2, "message post`.\n") {
		t.Fatalf("[2] does not list the allowed commands:\n%s", s2)
	}
	if !strings.Contains(s2, "- 이 역할은 메시지 읽기 · 아티팩트 읽기 · 상태 알리기 · 결정 기록 · 위임 · 아티팩트 제출 · 검토 승인 · 검토 반려 · 사람에게 질문 · 완료 승인 요청 · 사람에게 정보 요청 · 다른 방 목록 · 다른 방 읽기 · 미션 제안을 쓰지 않는다.") {
		t.Fatalf("[2] has no 'does not use' line:\n%s", s2)
	}
}

// (a) claude_code: --allow on the MCP argv, [2] restricted in _meta.
func TestAllowedCommandsMCP(t *testing.T) {
	b := bundle("t-ac")
	b.Task.AgentName = "Rev"
	b.Task.AllowedCommands = twoCommands
	b.Brief.Text = briefWith2
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, _ := newDaemon(t, srv, script)
	record, envs := recordingSpawn(t, d, script)
	runOne(t, d, srv)

	colab, text := sessionNewOf(t, record)
	if got := strings.Join(colab.Args, " "); got != "mcp serve --allow room_get,message_post" {
		t.Fatalf("colab MCP args %q", got)
	}
	assertRestricted(t, section2Of(t, text), true)
	// §2.1 is a closed list: the variable is the wrapper's, not the process's.
	if v := acp.EnvValue((*envs)[key("t-ac", 1)], "COLAB_ALLOWED_COMMANDS"); v != "" {
		t.Fatalf("COLAB_ALLOWED_COMMANDS=%q in the runtime process env", v)
	}
}

// (b) hermes: the wrapper exports the list, COLAB_BRIEF.md's [2] is
// restricted and rewritten to the wrapper path.
func TestAllowedCommandsWrapper(t *testing.T) {
	b := hermesBundle("t-ah")
	b.Task.AllowedCommands = twoCommands
	b.Brief.Text = briefWith2
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, root := newDaemon(t, srv, hermesScript())
	_, envs := recordingSpawn(t, d, hermesScript())
	wrapper := toolwrap.Path(root, "t-ah", 1)
	var script, file string
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase != "preparing" {
			return
		}
		x, _ := os.ReadFile(wrapper)
		script = string(x)
		y, _ := os.ReadFile(filepath.Join(req.WorkdirPath, brief.FileName))
		file = string(y)
	}
	runOne(t, d, srv)

	if !strings.Contains(script, "export COLAB_ALLOWED_COMMANDS='room_get,message_post'\n") {
		t.Fatalf("wrapper does not export the list:\n%s", script)
	}
	s2 := section2Of(t, file)
	assertRestricted(t, s2, false)
	if strings.Contains(s2, "`colab ") || !strings.Contains(s2, "`"+wrapper+" room get`") {
		t.Fatalf("daemon-written command not rewritten to the wrapper path:\n%s", s2)
	}
	if v := acp.EnvValue((*envs)[key("t-ah", 1)], "COLAB_ALLOWED_COMMANDS"); v != "" {
		t.Fatalf("COLAB_ALLOWED_COMMANDS=%q in the runtime process env", v)
	}
}

// (c) empty list (an older server, or lead/custom): nothing changes —
// the P1 argv, a wrapper without the export, the brief byte-for-byte.
func TestAllowedCommandsEmptyIsEverything(t *testing.T) {
	b := bundle("t-ae")
	b.Brief.Text = briefWith2
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, _ := newDaemon(t, srv, script)
	record, _ := recordingSpawn(t, d, script)
	runOne(t, d, srv)
	colab, text := sessionNewOf(t, record)
	if got := strings.Join(colab.Args, " "); got != "mcp serve" {
		t.Fatalf("colab MCP args %q, want the P1 argv", got)
	}
	if text != briefWith2 {
		t.Fatalf("brief changed with an empty list:\n%s", text)
	}

	h := hermesBundle("t-ae2")
	h.Brief.Text = briefWith2
	srv2 := &memServer{queue: []contracts.TaskBundle{h}}
	d2, root := newDaemon(t, srv2, hermesScript())
	recordingSpawn(t, d2, hermesScript())
	var script2 string
	srv2.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" {
			x, _ := os.ReadFile(toolwrap.Path(root, "t-ae2", 1))
			script2 = string(x)
		}
	}
	runOne(t, d2, srv2)
	if script2 == "" || strings.Contains(script2, "COLAB_ALLOWED_COMMANDS") {
		t.Fatalf("wrapper with an empty list:\n%s", script2)
	}
}
