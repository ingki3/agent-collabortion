package queue

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// T-DETAIL-2 (#333 NN3) · T-SURFACE (harness v0.9.6): brief [2]'s 「대화와 작업 내용」 lines are held to
// the contract, not to themselves. harness.md states the rule once, in
// Korean, as the v0.9.4 quote 「`--body` 는 대화 — …」; the brief is English
// (every other [2] line is), so the check is by element: every element of the
// contract's quote maps to a phrase that must be in the constant that carries
// it, and the quote with every known element removed leaves only particles
// and punctuation. A clause added to the contract that this table does not
// know fails here; so does a constant that drops one (T-S13b: the sentence is
// read from the contract file, not retyped in the test).

// detailRuleElements is contract element → (constant, phrase it must carry),
// once per tool surface (harness v0.9.6): the shell surface keeps the
// contract's own command spelling, the mcp surface says the same element in
// the tool's argument names. Longest first matters only for removal, so the
// table is sorted there.
type ruleElement struct {
	constant string
	phrase   string
}

var detailRuleElements = map[string]map[string]ruleElement{
	SurfaceCLIWrapper: {
		"`--body`":          {"DetailRule", "`--body`"},
		"대화":                {"DetailRule", "is the conversation"},
		"누구에게":              {"DetailRule", "who it is for"},
		"무엇을":               {"DetailRule", "what"},
		"결론":                {"DetailRule", "the conclusion"},
		"다음 할 일":            {"DetailRule", "the next step"},
		"5줄 안팎":             {"DetailRule", "about five lines"},
		"조사 결과":             {"DetailRule", "Research results"},
		"초안 전문":             {"DetailRule", "full drafts"},
		"표":                 {"DetailRule", "tables"},
		"`--detail`":        {"DetailRule", "`--detail`"},
		"제출물":               {"DeliverableRule", "A final deliverable"},
		"`artifact submit`": {"DeliverableRule", "`colab artifact submit`"},
	},
	SurfaceMCP: {
		"`--body`":          {"DetailRuleMCP", "`body`"},
		"대화":                {"DetailRuleMCP", "is the conversation"},
		"누구에게":              {"DetailRuleMCP", "who it is for"},
		"무엇을":               {"DetailRuleMCP", "what"},
		"결론":                {"DetailRuleMCP", "the conclusion"},
		"다음 할 일":            {"DetailRuleMCP", "the next step"},
		"5줄 안팎":             {"DetailRuleMCP", "about five lines"},
		"조사 결과":             {"DetailRuleMCP", "Research results"},
		"초안 전문":             {"DetailRuleMCP", "full drafts"},
		"표":                 {"DetailRuleMCP", "tables"},
		"`--detail`":        {"DetailRuleMCP", "`detail`"},
		"제출물":               {"DeliverableRuleMCP", "A final deliverable"},
		"`artifact submit`": {"DeliverableRuleMCP", "`colab_artifact_submit`"},
	},
}

func harnessContract(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, "..", "..", "..", "contracts", "harness.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// contractDetailQuote is harness.md's v0.9.4 brief [2] sentence, the text
// between 「 and 」 after 「브리프 [2] 에」.
func contractDetailQuote(t *testing.T) string {
	t.Helper()
	m := regexp.MustCompile(`v0\.9\.4 — 대화와 작업 내용[^」]*?브리프 \[2\] 에 「([^」]+)」`).FindStringSubmatch(harnessContract(t))
	if m == nil {
		t.Fatal("harness.md has no v0.9.4 brief [2] 「…」 sentence")
	}
	return m[1]
}

func TestDetailRuleMatchesContract(t *testing.T) {
	quote := contractDetailQuote(t)
	constants := map[string]string{
		"DetailRule": DetailRule, "DeliverableRule": DeliverableRule,
		"DetailRuleMCP": DetailRuleMCP, "DeliverableRuleMCP": DeliverableRuleMCP,
	}
	for surface, table := range detailRuleElements {
		t.Run(surface, func(t *testing.T) {
			keys := make([]string, 0, len(table))
			for k := range table {
				keys = append(keys, k)
			}
			sort.Slice(keys, func(i, j int) bool { return len([]rune(keys[i])) > len([]rune(keys[j])) })
			rest := quote
			for _, k := range keys {
				e := table[k]
				if !strings.Contains(quote, k) {
					t.Errorf("contract quote no longer has %q — update the table and %s:\n%s", k, e.constant, quote)
				}
				if !strings.Contains(constants[e.constant], e.phrase) {
					t.Errorf("%s lacks %q (contract: %q):\n%s", e.constant, e.phrase, k, constants[e.constant])
				}
				rest = strings.ReplaceAll(rest, k, " ")
			}
			// What is left must be glue: particles and punctuation, nothing
			// that says something the constants would have to say too.
			if strings.IndexFunc(rest, func(r rune) bool { return !strings.ContainsRune(" —·,.는은", r) }) >= 0 {
				t.Errorf("contract quote has elements this table does not know: %q (from %q)", rest, quote)
			}
		})
	}
	// The deliverable line stands alone (the daemon's role filter drops it
	// for a role without `artifact submit`), so the detail line must not say it.
	for name, c := range map[string]string{"DetailRule": DetailRule, "DetailRuleMCP": DetailRuleMCP} {
		if strings.Contains(c, "artifact submit") || strings.Contains(c, "artifact_submit") {
			t.Errorf("%s names artifact submit; that belongs to the deliverable line alone", name)
		}
	}
}

// harness v0.9.6: the tools and arguments the contract names for the mcp
// surface — 「`colab_message_post`(`body`·`detail`·`detail_file`·`reply_to`·
// `top_level`), `colab_room_messages`, `colab_room_get`,
// `colab_artifact_submit` 등」 — are each in the mcp surface's text, read from
// the contract file (a tool added there fails here until the brief says it).
//
// 회귀 주입: SurfaceFor 가 hermes 가 아닌 것에도 shellSurface 를 내면(표면
// 분기 제거) (surface) FAIL; mcpSurface.ThreadReply 를 셸 문장으로 되돌리면
// (tools) FAIL.
func TestSurfaceMatchesContractV096(t *testing.T) {
	c := harnessContract(t)
	i := strings.Index(c, "v0.9.6 — ")
	j := strings.Index(c, "**v0.9.5 — ")
	if i < 0 || j < i {
		t.Fatal("harness.md has no v0.9.6 entry before v0.9.5")
	}
	entry := c[i:j]
	tools := regexp.MustCompile("`(colab_[a-z_]+)`").FindAllStringSubmatch(entry, -1)
	argsM := regexp.MustCompile("`colab_message_post`\\(([^)]*)\\)").FindStringSubmatch(entry)
	if len(tools) < 4 || argsM == nil {
		t.Fatalf("v0.9.6 entry lost its tool list (tools %d, args %v):\n%s", len(tools), argsM, entry)
	}
	mcp := SurfaceFor(string("claude_code"))
	text := mcp.Section2() + mcp.HitlAskLine + mcp.Respond + mcp.ThreadReply
	for _, m := range tools {
		if !strings.Contains(text, "`"+m[1]+"`") {
			t.Errorf("(tools) contract names %s for the mcp surface; the brief/closing lines never do:\n%s", m[1], text)
		}
	}
	for _, a := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(argsM[1], -1) {
		if !strings.Contains(text, "`"+a[1]+"`") {
			t.Errorf("(tools) colab_message_post argument %q is never named on the mcp surface", a[1])
		}
	}
	// (surface) runtime_kind picks the surface: claude_code → mcp, hermes →
	// cli_wrapper, and nothing on the mcp side reads as a shell command.
	if mcp.Kind != SurfaceMCP || SurfaceFor("hermes").Kind != SurfaceCLIWrapper {
		t.Fatalf("(surface) SurfaceFor(claude_code)=%s, SurfaceFor(hermes)=%s", mcp.Kind, SurfaceFor("hermes").Kind)
	}
	if shellCommand.MatchString(text) {
		t.Errorf("(surface) mcp text names a shell command: %v", shellCommand.FindAllString(text, -1))
	}
	// v0.9.3's thread line in each surface's words: the shell keeps the
	// contract's quote (`colab message post` · `--top-level`), mcp names the
	// tool and its argument.
	if s := SurfaceFor("hermes").ThreadReply; !strings.Contains(s, "`colab message post`") || !strings.Contains(s, "`--top-level`") {
		t.Errorf("shell thread line lost the v0.9.3 command: %q", s)
	}
	if !strings.Contains(mcp.ThreadReply, "`colab_message_post`") || !strings.Contains(mcp.ThreadReply, "`top_level`") {
		t.Errorf("mcp thread line lacks the tool and top_level: %q", mcp.ThreadReply)
	}
}
