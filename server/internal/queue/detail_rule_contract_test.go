package queue

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// T-DETAIL-2 (#333 NN3): brief [2]'s 「대화와 작업 내용」 lines are held to
// the contract, not to themselves. harness.md states the rule once, in
// Korean, as the v0.9.4 quote 「`--body` 는 대화 — …」; the brief is English
// (every other [2] line is), so the check is by element: every element of the
// contract's quote maps to a phrase that must be in the constant that carries
// it, and the quote with every known element removed leaves only particles
// and punctuation. A clause added to the contract that this table does not
// know fails here; so does a constant that drops one (T-S13b: the sentence is
// read from the contract file, not retyped in the test).

// detailRuleElements is contract element → (constant, phrase it must carry).
// Longest first matters only for removal, so the table is sorted there.
var detailRuleElements = map[string]struct {
	constant string
	phrase   string
}{
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
}

// contractDetailQuote is harness.md's v0.9.4 brief [2] sentence, the text
// between 「 and 」 after 「브리프 [2] 에」.
func contractDetailQuote(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wd, "..", "..", "..", "contracts", "harness.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`v0\.9\.4 — 대화와 작업 내용[^」]*?브리프 \[2\] 에 「([^」]+)」`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("harness.md has no v0.9.4 brief [2] 「…」 sentence")
	}
	return m[1]
}

func TestDetailRuleMatchesContract(t *testing.T) {
	quote := contractDetailQuote(t)
	constants := map[string]string{"DetailRule": DetailRule, "DeliverableRule": DeliverableRule}

	keys := make([]string, 0, len(detailRuleElements))
	for k := range detailRuleElements {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len([]rune(keys[i])) > len([]rune(keys[j])) })
	rest := quote
	for _, k := range keys {
		e := detailRuleElements[k]
		if !strings.Contains(quote, k) {
			t.Errorf("contract quote no longer has %q — update the table and %s:\n%s", k, e.constant, quote)
		}
		if !strings.Contains(constants[e.constant], e.phrase) {
			t.Errorf("%s lacks %q (contract: %q):\n%s", e.constant, e.phrase, k, constants[e.constant])
		}
		rest = strings.ReplaceAll(rest, k, " ")
	}
	// What is left must be glue: particles and punctuation, nothing that says
	// something the constants would have to say too.
	if strings.IndexFunc(rest, func(r rune) bool { return !strings.ContainsRune(" —·,.는은", r) }) >= 0 {
		t.Errorf("contract quote has elements this table does not know: %q (from %q)", rest, quote)
	}
	// The deliverable line stands alone (the daemon's role filter drops it
	// for a role without `artifact submit`), so DetailRule must not say it.
	if strings.Contains(DetailRule, "artifact submit") {
		t.Errorf("DetailRule names artifact submit; that belongs to DeliverableRule alone")
	}
}
