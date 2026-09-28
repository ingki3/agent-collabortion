package queue

// T-QUIET (harness v0.9.15, PRD FR-2A.2.3): brief [2] carries one fixed line
// — the contract gives a single sentence for both surfaces (it names no
// command) — telling the agent not to wake teammates once only the
// Director's approval is left.
//
// 회귀 주입: Section2 에서 QuietRule 을 빼면 (brief2) FAIL; QuietRule 의 문장을
// 한 글자 바꾸면 (contract) FAIL.

import (
	"regexp"
	"strings"
	"testing"
)

func contractV0915(t *testing.T) (brief, notice string) {
	t.Helper()
	c := harnessContract(t)
	m := regexp.MustCompile("v0\\.9\\.15 — [^|]*?브리프 \\[2\\] 고정 한 줄\\(표면별\\) — `([^`]+)`[^|]*?보류 안내 한 줄: `([^`]+)`").FindStringSubmatch(c)
	if m == nil {
		t.Fatal("harness.md has no v0.9.15 brief [2] / post-result sentences")
	}
	return m[1], m[2]
}

func TestQuietRuleMatchesContractV0915(t *testing.T) {
	brief, _ := contractV0915(t)
	got := strings.TrimSuffix(strings.TrimPrefix(QuietRule, "- "), "\n")
	if got != brief {
		t.Errorf("(contract) brief [2] line differs from harness v0.9.15:\n got %q\nwant %q", got, brief)
	}
	for kind, s := range map[string]Surface{"mcp": SurfaceFor("claude_code"), "cli_wrapper": SurfaceFor("hermes")} {
		if n := strings.Count(s.Section2(), QuietRule); n != 1 {
			t.Errorf("(brief2) %s [2] carries the quiet line %d times:\n%s", kind, n, s.Section2())
		}
	}
}
