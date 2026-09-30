package cards

// Contract lock (#400 리뷰 400b NN1): every fixed sentence harness v0.9.16
// and openapi v0.3.10 give the card flow is read out of contracts/ and
// compared with what this package renders — so a wording change on either
// side fails here, not in a model's prompt. harness.md writes a sentence as
// one `…` span, so the commands the code wraps in backticks are compared
// with those stripped; where harness.md abbreviates the mcp surface with
// 「…」, every fragment between the ellipses must appear in the code's text.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func contractFile(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../../../contracts/" + name)
	if err != nil {
		t.Fatalf("contracts/%s must be readable: %v", name, err)
	}
	return string(raw)
}

var spanRe = regexp.MustCompile("`([^`\n]+)`")

// span is the one `…` span of harness.md that starts with prefix.
func span(t *testing.T, doc, prefix string) string {
	t.Helper()
	var hit []string
	for _, m := range spanRe.FindAllStringSubmatch(doc, -1) {
		if strings.HasPrefix(m[1], prefix) {
			hit = append(hit, m[1])
		}
	}
	if len(hit) != 1 {
		t.Fatalf("harness.md: %d spans start with %q, want exactly 1", len(hit), prefix)
	}
	return hit[0]
}

func plain(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(strings.ReplaceAll(s, "`", "")), "- "))
}

func same(t *testing.T, what, contract, code string) {
	t.Helper()
	if plain(contract) != plain(code) {
		t.Errorf("%s:\n contract %q\n code     %q", what, plain(contract), plain(code))
	}
}

// fragments: harness.md's abbreviated mcp form — each piece between 「…」
// must be in the code's text.
func fragments(t *testing.T, what, contract, code string) {
	t.Helper()
	c := plain(code)
	n := 0
	for _, f := range strings.Split(contract, "…") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		n++
		if !strings.Contains(c, f) {
			t.Errorf("%s: fragment %q missing from %q", what, f, c)
		}
	}
	if n == 0 {
		t.Fatalf("%s: no fragment in %q", what, contract)
	}
}

// mcpOf fills harness.md's abbreviated mcp form from the shell sentence: the
// 「…」 pieces are the shell sentence's text between the mcp form's stated
// fragments. contract shell "A: cmd — X. Then Y." with mcp "… B — … Then Z."
// → the code's mcp text must start with the shell text up to where B's
// surface differs and share every piece the ellipses stand for. Simpler,
// and as strict: every sentence-piece (split at " — " and ". ") of the
// shell text that names no command must appear in the mcp text unchanged.
func sharedPieces(t *testing.T, what, shell, mcp string) {
	t.Helper()
	sh, m := plain(shell), plain(mcp)
	for _, p := range regexp.MustCompile(` — |\. `).Split(sh, -1) {
		p = strings.TrimSuffix(strings.TrimSpace(p), ".")
		if p == "" || strings.Contains(p, "colab ") {
			continue // the surface-specific piece (a command vs a tool)
		}
		if !strings.Contains(m, p) {
			t.Errorf("%s: shared piece %q missing from mcp %q", what, p, m)
		}
	}
}

func TestHarnessCardSentences(t *testing.T) {
	h := contractFile(t, "harness.md")
	r := &Row{Number: 3, Version: 2}

	// <task_card> fixed line.
	same(t, "task_card line (shell)", span(t, h, "When this card's work is done, submit a result card: "), TaskCardLine(false))
	fragments(t, "task_card line (mcp)", span(t, h, "… submit a result card with the colab_card_report tool"), TaskCardLine(true))
	sharedPieces(t, "task_card line (mcp)", span(t, h, "When this card's work is done, submit a result card: "), TaskCardLine(true))

	// <result_cards> fixed line.
	same(t, "result_cards line (shell)", span(t, h, "Judge each card: "), ResultCardsLine(false))
	fragments(t, "result_cards line (mcp)", span(t, h, "… with the colab_card_accept or colab_card_revise tool"), ResultCardsLine(true))
	sharedPieces(t, "result_cards line (mcp)", span(t, h, "Judge each card: "), ResultCardsLine(true))

	// <card_board> end line.
	board := BoardBlock("none", []*Row{{Number: 1, Version: 1, Status: InProgress, AssigneeName: "R", Goal: "g", Boundaries: "b"}})
	endLine := strings.TrimSpace(board[strings.Index(board, "</card_board>")+len("</card_board>"):])
	same(t, "card_board end line", span(t, h, "Before writing a new card"), endLine)

	// Question task's first line.
	q := strings.Replace(span(t, h, "This is a question from "), "<묻는 쪽 이름>", "Lead", 1)
	same(t, "question head", q, QuestionHead("Lead"))

	// result_card_missing trigger sentence.
	fu := FollowUpTrigger(r, 1, false)
	body := fu[strings.Index(fu, "\n")+1 : strings.Index(fu, "\n</trigger>")]
	same(t, "result_card_missing (shell)", span(t, h, "Your last turn on C-3 ended without a result card."), body)
	fuM := FollowUpTrigger(r, 1, true)
	fragments(t, "result_card_missing (mcp)", span(t, h, "… Submit it now with the colab_card_report tool"), fuM)
	sharedPieces(t, "result_card_missing (mcp)", span(t, h, "Your last turn on C-3 ended without a result card."), fuM)
	if !strings.Contains(fu, `<trigger reason="result_card_missing" card="C-3" version="2" follow_up="1" of="2">`) {
		t.Errorf("result_card_missing trigger head: %q", fu)
	}

	// Downgrade notice.
	n := strings.Replace(span(t, h, "Criterion <N[, M]> said met"), "<N[, M]>", "1, 3", 1)
	same(t, "downgrade notice", n, *DowngradeNotice([]int{1, 3}))

	// Brief [3] delegation and new-work sentences, brief [2] report line.
	same(t, "brief [3] delegate (shell)", span(t, h, "Delegate only with a card: colab card delegate"), DelegateRule(false))
	same(t, "brief [3] delegate (mcp)", span(t, h, "Delegate only with a card: the colab_card_delegate tool"), DelegateRule(true))
	same(t, "brief [3] new work (shell)", span(t, h, "New work is a new card;"), NewWorkRule(false))
	if !strings.Contains(plain(NewWorkRule(true)), span(t, h, "the colab_card_revise tool")) {
		t.Errorf("brief [3] new work (mcp): %q", NewWorkRule(true))
	}
	same(t, "brief [2] report (shell)", span(t, h, "If you received a card, finish with a result card (colab card report)"), ReportRule(false))
	same(t, "brief [2] report (mcp)", span(t, h, "If you received a card, finish with a result card (the colab_card_report tool)"), ReportRule(true))
}

// The server's human sentences the contract quotes (openapi v0.3.10 「…」).
func TestOpenapiCardSentences(t *testing.T) {
	oa := contractFile(t, "openapi.yaml")
	for _, c := range []struct{ what, re, code string }{
		{"card_required", `422 card_required\x60, 사람 말 사유 「([^」]*)」`, CardRequiredSentence},
		{"result_card_required", `409 result_card_required\x60\(사람 말 「([^」]*)」`, ResultCardRequiredSentence},
	} {
		m := regexp.MustCompile(c.re).FindStringSubmatch(oa)
		if m == nil {
			t.Fatalf("openapi.yaml: the %s sentence is not where this test expects it", c.what)
		}
		if m[1] != c.code {
			t.Errorf("%s: openapi %q, server %q", c.what, m[1], c.code)
		}
	}
}
