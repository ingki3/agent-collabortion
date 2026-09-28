package queue

// T-FOCUS (harness v0.9.13, PRD FR-3.1.5): brief [2] carries a fixed line —
// per surface, the contract's words — telling the agent to declare what it
// is doing now. The contract quotes the shell line whole and the mcp line
// with 「…」 for the shared parts, so the check is: the shell constant IS the
// contract's sentence (backticks aside — the brief puts the command in
// backticks for the wrapper rewrite), and the mcp constant has the contract's
// tool phrase with the same head and tail.
//
// 회귀 주입: Section2 에서 s.FocusRule 을 빼면 (brief2) FAIL; FocusRule 의
// 「Not after every tool call」 을 지우면 (contract) FAIL; mcpSurface 의
// FocusRule 을 셸 문장으로 두면 (surface) FAIL.

import (
	"regexp"
	"strings"
	"testing"
)

func contractV0913(t *testing.T) (shell, mcp string) {
	t.Helper()
	c := harnessContract(t)
	m := regexp.MustCompile("v0\\.9\\.13 — [^|]*?셸 `([^`]+)` / mcp `([^`]+)`").FindStringSubmatch(c)
	if m == nil {
		t.Fatal("harness.md has no v0.9.13 brief [2] shell/mcp sentences")
	}
	return m[1], m[2]
}

func TestFocusRuleMatchesContractV0913(t *testing.T) {
	shell, mcp := contractV0913(t)

	// (contract) the shell line is the contract's sentence, word for word.
	got := strings.TrimSuffix(strings.TrimPrefix(FocusRule, "- "), "\n")
	if strings.ReplaceAll(got, "`", "") != shell {
		t.Errorf("(contract) shell line differs from harness v0.9.13:\n got %q\nwant %q", strings.ReplaceAll(got, "`", ""), shell)
	}
	// The command sits in backticks, in command position (toolwrap.cliRe).
	if !strings.Contains(FocusRule, "`colab status set working --note ") {
		t.Errorf("(contract) shell command is not in backticks: %q", FocusRule)
	}

	// (contract) the mcp line: the contract's tool phrase, and the same head
	// and tail as the shell one (the contract's 「…」).
	parts := strings.Split(mcp, "…")
	if len(parts) < 3 {
		t.Fatalf("contract mcp quote changed shape: %q", mcp)
	}
	tool := strings.TrimSpace(parts[1])
	tool = strings.TrimSuffix(tool, ". ")
	for _, want := range []string{"with the colab_status_set tool", "status: working", "note:"} {
		if !strings.Contains(tool, want) || !strings.Contains(strings.ReplaceAll(FocusRuleMCP, "`", ""), want) {
			t.Errorf("(contract) mcp line lacks %q\n  contract %q\n  line     %q", want, tool, FocusRuleMCP)
		}
	}
	head := shell[:strings.Index(shell, ": colab status set")]
	if !strings.HasPrefix(FocusRuleMCP, "- "+head+" with the `colab_status_set` tool") {
		t.Errorf("(contract) mcp line does not start like the shell one: %q", FocusRuleMCP)
	}
	if !strings.HasSuffix(FocusRuleMCP, "Not after every tool call.\n") || !strings.HasSuffix(FocusRule, "Not after every tool call.\n") {
		t.Errorf("(contract) a line lost 「Not after every tool call.」")
	}

	// (surface) mcp names no shell command; shell names no tool.
	if strings.Contains(FocusRuleMCP, "`colab ") || strings.Contains(FocusRuleMCP, "--note") {
		t.Errorf("(surface) mcp line names the shell command: %q", FocusRuleMCP)
	}
	if strings.Contains(FocusRule, "colab_") {
		t.Errorf("(surface) shell line names an MCP tool: %q", FocusRule)
	}

	// (brief2) both surfaces carry it in [2], once.
	for kind, s := range map[string]Surface{"mcp": SurfaceFor("claude_code"), "cli_wrapper": SurfaceFor("hermes")} {
		if n := strings.Count(s.Section2(), s.FocusRule); s.FocusRule == "" || n != 1 {
			t.Errorf("(brief2) %s [2] carries the focus line %d times:\n%s", kind, n, s.Section2())
		}
	}
	if SurfaceFor("claude_code").FocusRule != FocusRuleMCP || SurfaceFor("hermes").FocusRule != FocusRule {
		t.Error("(surface) SurfaceFor picked the wrong focus line")
	}
}
