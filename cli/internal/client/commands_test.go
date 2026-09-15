package client

import (
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

// The refusal sentence is the server's, byte for byte: colab-cli.md §2.5
// fixes one sentence for the 403 and the exit 3, and T-S19 spelled it in
// commands_gate.go. This reads the server's fmt string out of its source so
// the two cannot drift apart silently.
func TestNotAllowedSentenceIsTheServers(t *testing.T) {
	raw, err := os.ReadFile("../../../server/internal/httpapi/commands_gate.go")
	if err != nil {
		t.Skip("server source not present:", err)
	}
	m := regexp.MustCompile(`apperr\.Forbidden\("command_not_allowed", fmt\.Sprintf\("([^"]+)"`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("commands_gate.go: the 403 sentence is not where this test expects it")
	}
	if m[1] != notAllowedFormat {
		t.Fatalf("server sentence %q, CLI %q", m[1], notAllowedFormat)
	}
	if got := NotAllowedSentence("reviewer", CmdLaneDelegate); got != "이 역할(reviewer)은 lane delegate 를 쓸 수 없습니다" {
		t.Fatalf("sentence = %q", got)
	}
	if got := NotAllowedSentence("", CmdHitlApproveRequest); got != "이 역할은 hitl approve-request 를 쓸 수 없습니다" {
		t.Fatalf("no-role sentence = %q", got)
	}
}

// CLIName is the command as typed: underscores to spaces, except the two
// hyphenated HITL sub-commands (colab-cli.md §2.4) — the server's
// roles.CLIName rule.
func TestCLIName(t *testing.T) {
	want := map[Command]string{
		CmdSessionGet: "session get", CmdSessionMessages: "session messages", CmdArtifactGet: "artifact get",
		CmdMessagePost: "message post", CmdStatusSet: "status set", CmdDecisionRecord: "decision record",
		CmdLaneDelegate: "lane delegate", CmdArtifactSubmit: "artifact submit",
		CmdReviewApprove: "review approve", CmdReviewReject: "review reject",
		CmdHitlAsk: "hitl ask", CmdHitlApproveRequest: "hitl approve-request", CmdHitlRequestInfo: "hitl request-info",
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
	if got := SplitCommands(" session_get, ,message_post ,"); len(got) != 2 || got[0] != "session_get" || got[1] != "message_post" {
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
			return "session_get"
		}
		return ""
	})
	if len(c.AllowedCommands) != 1 || c.AllowedCommands[0] != "session_get" {
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
