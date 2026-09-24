package client

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// AllCommands is the openapi.yaml ColabCommand enum, in order.
func TestAllCommandsMatchOpenapiEnum(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	i := strings.Index(text, "\n    ColabCommand:\n")
	if i < 0 {
		t.Fatal("openapi.yaml has no ColabCommand schema")
	}
	m := regexp.MustCompile(`enum: \[([^\]]+)\]`).FindStringSubmatch(text[i:])
	if m == nil {
		t.Fatal("ColabCommand has no enum line")
	}
	want := strings.Split(m[1], ",")
	if len(want) != len(AllCommands) {
		t.Fatalf("enum has %d values, AllCommands %d", len(want), len(AllCommands))
	}
	for i, w := range want {
		if strings.TrimSpace(w) != string(AllCommands[i]) {
			t.Errorf("enum[%d] = %q, AllCommands[%d] = %q", i, strings.TrimSpace(w), i, AllCommands[i])
		}
	}
}

// The refusal sentence is the contract's, byte for byte: colab-cli.md §2.5
// fixes one sentence for the 403 and the exit 3 — "이 역할(<role>)은 <명령>
// 를 쓸 수 없습니다", the parenthesis dropped when the role is unknown. This
// reads the sentence out of the contract file (a hard dependency of this
// module: go.mod replaces it with ../contracts, so it is never absent), so
// the CLI cannot drift from the contract silently. The old version compared
// only against server/ source and t.Skip-ped without it (#251 NN2) — a CLI
// built outside the monorepo then never checked the sentence at all.
func TestNotAllowedSentenceIsTheContracts(t *testing.T) {
	contract, noRole := contractRefusalSentence(t)
	if contract != notAllowedFormat {
		t.Fatalf("contract sentence %q, CLI %q", contract, notAllowedFormat)
	}
	if got := NotAllowedSentence("reviewer", CmdLaneDelegate); got != fmt.Sprintf(contract, "reviewer", "lane delegate") {
		t.Fatalf("sentence = %q", got)
	}
	if got, want := NotAllowedSentence("", CmdHitlApproveRequest), fmt.Sprintf(noRole, "hitl approve-request"); got != want {
		t.Fatalf("no-role sentence = %q, want %q (contract: the parenthesis is dropped)", got, want)
	}
	// Pinned literally too, so a contract edit that changes the sentence
	// fails here (and is then a deliberate CLI change), not only above.
	if got := NotAllowedSentence("reviewer", CmdLaneDelegate); got != "이 역할(reviewer)은 lane delegate 를 쓸 수 없습니다" {
		t.Fatalf("sentence = %q", got)
	}
	if got := NotAllowedSentence("", CmdHitlApproveRequest); got != "이 역할은 hitl approve-request 를 쓸 수 없습니다" {
		t.Fatalf("no-role sentence = %q", got)
	}
}

// contractRefusalSentence returns colab-cli.md §2.5's refusal sentence as a
// fmt string (role, command), and its no-role form (command).
func contractRefusalSentence(t *testing.T) (withRole, noRole string) {
	t.Helper()
	raw, err := os.ReadFile("../../../contracts/colab-cli.md")
	if err != nil {
		t.Fatal("contracts/colab-cli.md must be readable (it is this module's dependency):", err)
	}
	m := regexp.MustCompile("`3 command_not_allowed` 로 거부한다[^\\n]*?\"([^\"]*<role>[^\"]*<명령>[^\"]*)\"").FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("colab-cli.md §2.5: the command_not_allowed sentence is not where this test expects it")
	}
	sentence := m[1]
	if !strings.Contains(sentence, "(<role>)") {
		t.Fatalf("contract sentence %q has no (<role>) parenthesis to drop", sentence)
	}
	noRole = strings.Replace(strings.Replace(sentence, "(<role>)", "", 1), "<명령>", "%s", 1)
	withRole = strings.Replace(strings.Replace(sentence, "<role>", "%s", 1), "<명령>", "%s", 1)
	return withRole, noRole
}

// The server spells the same sentence (its 403); when its source is in
// reach — the monorepo — it is compared too. Absent source is not a Skip:
// the contract comparison above already ran, this only adds the server.
func TestNotAllowedSentenceIsTheServers(t *testing.T) {
	raw, err := os.ReadFile("../../../server/internal/httpapi/commands_gate.go")
	if err != nil {
		t.Log("server source not present; the contract comparison (TestNotAllowedSentenceIsTheContracts) stands alone:", err)
		return
	}
	m := regexp.MustCompile(`apperr\.Forbidden\("command_not_allowed", fmt\.Sprintf\("([^"]+)"`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("commands_gate.go: the 403 sentence is not where this test expects it")
	}
	if m[1] != notAllowedFormat {
		t.Fatalf("server sentence %q, CLI %q", m[1], notAllowedFormat)
	}
}

// CLIName is the command as typed: underscores to spaces, except the two
// hyphenated HITL sub-commands (colab-cli.md §2.4) — the server's
// roles.CLIName rule.
func TestCLIName(t *testing.T) {
	want := map[Command]string{
		CmdRoomGet: "room get", CmdRoomMessages: "room messages", CmdArtifactGet: "artifact get",
		CmdMessagePost: "message post", CmdStatusSet: "status set", CmdDecisionRecord: "decision record",
		CmdLaneDelegate: "lane delegate", CmdArtifactSubmit: "artifact submit",
		CmdReviewApprove: "review approve", CmdReviewReject: "review reject",
		CmdHitlAsk: "hitl ask", CmdHitlApproveRequest: "hitl approve-request", CmdHitlRequestInfo: "hitl request-info",
		CmdRoomList: "room list", CmdRoomRead: "room read", CmdWorkPropose: "work propose",
	}
	for _, c := range AllCommands {
		if c.CLIName() != want[c] {
			t.Errorf("%s.CLIName() = %q, want %q", c, c.CLIName(), want[c])
		}
		if c.ToolName() != "colab_"+string(c) {
			t.Errorf("%s.ToolName() = %q", c, c.ToolName())
		}
	}
}

func TestSplitCommands(t *testing.T) {
	if got := SplitCommands(" room_get, ,message_post ,"); len(got) != 2 || got[0] != "room_get" || got[1] != "message_post" {
		t.Fatalf("got %v", got)
	}
	if got := SplitCommands(" , "); got == nil || len(got) != 0 {
		t.Fatalf("all-empty must be an empty non-nil list, got %#v", got)
	}
	// FromEnv: unset → nil (ask the server); set → the list.
	if c := FromEnv(func(string) string { return "" }); c.AllowedCommands != nil {
		t.Fatalf("unset env gave %v", c.AllowedCommands)
	}
	c := FromEnv(func(k string) string {
		if k == EnvAllowedCommands {
			return "room_get"
		}
		return ""
	})
	if len(c.AllowedCommands) != 1 || c.AllowedCommands[0] != "room_get" {
		t.Fatalf("env list = %v", c.AllowedCommands)
	}
}

func TestOwnRole(t *testing.T) {
	cc := &CliContext{AgentID: "a", Participants: []Participant{{AgentID: "b", Role: "lead"}, {AgentID: "a", Role: "reviewer"}}}
	if cc.OwnRole() != "reviewer" {
		t.Fatalf("OwnRole = %q", cc.OwnRole())
	}
	if (&CliContext{AgentID: "z"}).OwnRole() != "" {
		t.Fatal("absent from the roster must be empty, not a guess")
	}
}
