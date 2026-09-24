package brief

// harness §10 v0.8.10 — 브리프 [2] 를 번들 allowed_commands 로 자른다 (K-19, T-D13).

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

// serverBrief is server/internal/queue/bundle.go's [1]~[8] shape, [2] verbatim.
const serverBrief = "[1] Agent Identity\nYou are Rev, reviewer in the Colab workspace.\n\nInstructions:\nreview\n\n" +
	"[2] Workspace rules and colab CLI\n" +
	"- Mention syntax: [@Name](mention://agent/<id>). Only mention session participants listed in [5].\n" +
	"- Post every reply to the session with `colab message post --body \"<text>\"` (or the colab_message_post MCP tool). Text you print to stdout is NOT delivered.\n" +
	"- Read more history with `colab session messages`, session details with `colab session get`.\n" +
	"- Mentioning an agent creates work for it; do not mention agents just to acknowledge.\n" +
	"- Your COLAB_TASK_TOKEN is valid for this attempt only; if a call returns token_revoked, stop immediately.\n\n" +
	"[4] Session\nTitle: t\nGoal: g\nDirector: D\nIsolation: none\n\n[5] Roster\n- Rev\n\n[8] Instruction precedence: user instruction > session goal > agent instructions > runtime defaults.\n"

var reviewer = []string{"session_get", "session_messages", "message_post", "status_set", "decision_record", "artifact_get", "review_approve", "review_reject", "hitl_ask", "hitl_request_info", "room_list", "room_read"}

func section2(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, "[2] ")
	j := strings.Index(text, "\n[4] ")
	if i < 0 || j < 0 {
		t.Fatalf("no [2]/[4] in:\n%s", text)
	}
	return text[i:j]
}

func TestRestrictCommandsReviewer(t *testing.T) {
	got := RestrictCommands(serverBrief, reviewer)
	s2 := section2(t, got)
	// The server's [2] lines are still there, verbatim and first.
	if !strings.HasPrefix(got, serverBrief[:strings.Index(serverBrief, "\n\n[4]")]) {
		t.Fatalf("server [2] text changed:\n%s", got)
	}
	// Allowed commands, in CLI spelling, in bundle order.
	if !strings.Contains(s2, "`colab review approve`, `colab review reject`, `colab hitl ask`, `colab hitl request-info`, `colab room list`, `colab room read`") {
		t.Fatalf("allowed list missing or misspelt:\n%s", s2)
	}
	// Denied ones are NOT there as commands — neither CLI nor tool spelling.
	for _, bad := range []string{"lane delegate", "colab_lane_delegate", "artifact submit", "approve-request", "work propose", "colab_work_propose"} {
		if strings.Contains(s2, bad) {
			t.Fatalf("denied command %q named in [2]:\n%s", bad, s2)
		}
	}
	// … but the role is told, in the person's words, in one line.
	if !strings.Contains(s2, "- 이 역할은 위임 · 아티팩트 제출 · 완료 승인 요청 · 미션 제안을 쓰지 않는다.") {
		t.Fatalf("no 'does not use' line:\n%s", s2)
	}
	// Sections after [2] are untouched, and the separator before [4] survives.
	if !strings.HasSuffix(got, serverBrief[strings.Index(serverBrief, "[4] "):]) || !strings.Contains(got, "쓰지 않는다.\n\n[4] ") {
		t.Fatalf("tail changed:\n%s", got)
	}
	// Same input → same bytes (E12-11: [1]~[5] stable within a session).
	if RestrictCommands(serverBrief, reviewer) != got {
		t.Fatal("not deterministic")
	}
}

// The command names the daemon writes reach a hermes agent as the wrapper
// path like every other one — RestrictCommands runs before RewriteCLI.
func TestRestrictedLinesGetTheWrapperPath(t *testing.T) {
	got := toolwrap.RewriteCLI(RestrictCommands(serverBrief, reviewer), "/w/.colab/bin/t.1/colab")
	s2 := section2(t, got)
	if strings.Contains(s2, "`colab ") {
		t.Fatalf("bare colab command left:\n%s", s2)
	}
	if !strings.Contains(s2, "`/w/.colab/bin/t.1/colab review approve`") || !strings.Contains(s2, "`colab_message_post`") {
		t.Fatalf("rewrite wrong:\n%s", s2)
	}
}

// Everything allowed (lead · custom): the list line, no "does not use" line.
func TestRestrictCommandsEverything(t *testing.T) {
	// The server's allowed_commands order (openapi enum = gen.ColabCommandValues, #324 NN1).
	all := []string{"session_get", "session_messages", "artifact_get", "message_post", "status_set", "decision_record", "lane_delegate", "artifact_submit", "review_approve", "review_reject", "hitl_ask", "hitl_approve_request", "hitl_request_info", "room_list", "room_read", "work_propose"}
	s2 := section2(t, RestrictCommands(serverBrief, all))
	if !strings.Contains(s2, "`colab lane delegate`") || strings.Contains(s2, "쓰지 않는다") {
		t.Fatalf("everything-allowed shape wrong:\n%s", s2)
	}
}

// Empty list = everything (daemon-protocol §4.1 v0.8.2, an older server):
// the brief is untouched. So is a brief with no [2].
func TestRestrictCommandsEmptyOrNoSection(t *testing.T) {
	if RestrictCommands(serverBrief, nil) != serverBrief {
		t.Fatal("empty list changed the brief")
	}
	if RestrictCommands("[1] x\n[8] y\n", reviewer) != "[1] x\n[8] y\n" {
		t.Fatal("brief without [2] changed")
	}
}

// Defensive: a server [2] line that names a denied command in command
// position is dropped, in either spelling.
func TestRestrictCommandsDropsDeniedServerLines(t *testing.T) {
	text := "[2] Workspace rules and colab CLI\n- Post with `colab message post`.\n- Hand out work with `colab lane delegate --to X`.\n- Or the colab_hitl_approve_request tool.\n- the lane keyword in prose stays\n\n[4] Session\n"
	got := RestrictCommands(text, reviewer)
	if strings.Contains(got, "lane delegate") || strings.Contains(got, "colab_hitl_approve_request") {
		t.Fatalf("denied line kept:\n%s", got)
	}
	if !strings.Contains(got, "`colab message post`.") || !strings.Contains(got, "lane keyword in prose stays") {
		t.Fatalf("allowed line dropped:\n%s", got)
	}
}

// A denied command's alias (colab-cli.md v0.8 §3: `room get` is `session
// get`) is dropped with it — in both spellings.
func TestRestrictCommandsDropsDeniedAliasLines(t *testing.T) {
	text := "[2] Workspace rules and colab CLI\n- Read the room with `colab room get`.\n- Or the colab_room_messages tool.\n- Post with `colab message post`.\n\n[4] Session\n"
	got := RestrictCommands(text, []string{"message_post"})
	if strings.Contains(got, "colab room get") || strings.Contains(got, "colab_room_messages") {
		t.Fatalf("alias of a denied command kept:\n%s", got)
	}
	if !strings.Contains(got, "`colab message post`.") {
		t.Fatalf("allowed line dropped:\n%s", got)
	}
}

// Assemble's "## [2] Workspace Rules" header is recognised too.
func TestRestrictCommandsAssembledBrief(t *testing.T) {
	got := RestrictCommands(Assemble(parts("c", "d")), reviewer)
	i, j := strings.Index(got, "## [2]"), strings.Index(got, "## [3]")
	if i < 0 || j < 0 || !strings.Contains(got[i:j], "쓰지 않는다") {
		t.Fatalf("assembled [2] not restricted:\n%s", got)
	}
}
