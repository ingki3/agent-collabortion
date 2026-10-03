package cards

// render.go is every sentence a card becomes: the two bubbles' human text
// (Message.content — the web draws the card from getCard, the content is what
// a reader without the renderer, the history lines and the inbox see), and
// harness v0.9.16's turn-prompt blocks and fixed lines, per tool surface
// (mcp = tool names, shell = `colab …` in backticks so the hermes wrapper
// rewrite anchors, harness v0.9.6·v0.9.11).

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

func trimSpace(s string) string { return strings.TrimSpace(s) }

// head is the first n runes, with an ellipsis when cut.
func head(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

var methodLabel = map[string]string{
	"test": "테스트", "artifact": "산출물", "run": "실행 결과", "review": "검토", "inspect": "눈으로 확인",
}

var verdictLabel = map[string]string{"met": "충족", "partial": "부분", "unmet": "미충족"}

// DelegationBubble is the delegation card bubble's content: the mention of
// the assignee first (so the timeline's chips and the history line name who
// it is for), then the card in people's words.
func DelegationBubble(mention string, label string, version int, d Draft, reason *string) string {
	var b strings.Builder
	if reason != nil {
		fmt.Fprintf(&b, "%s %s v%d 수정 요청 · 사유: %s\n", mention, label, version, trimSpace(*reason))
	} else {
		fmt.Fprintf(&b, "%s %s 위임 카드\n", mention, label)
	}
	fmt.Fprintf(&b, "목표: %s\n완료 기준:\n", trimSpace(d.Goal))
	for i, c := range d.Criteria {
		fmt.Fprintf(&b, "%d. %s (확인: %s)\n", i+1, trimSpace(c.Text), methodLabel[c.Method])
	}
	fmt.Fprintf(&b, "하지 않을 것: %s", trimSpace(d.Boundaries))
	if len(d.Refs) > 0 {
		fmt.Fprintf(&b, "\n참고 자료 %d개", len(d.Refs))
	}
	if d.OutputFormat != nil && trimSpace(*d.OutputFormat) != "" {
		fmt.Fprintf(&b, "\n결과물: %s", trimSpace(*d.OutputFormat))
	}
	return b.String()
}

// ResultBubble is the result card bubble's content.
func ResultBubble(r *Row, res Result) string {
	var b strings.Builder
	auto := ""
	if res.Auto {
		auto = " (자동)"
	}
	fmt.Fprintf(&b, "%s v%d 결과 카드%s — 기준 %d/%d 충족\n%s\n", r.Label(), r.Version, auto, res.MetCount, len(r.Criteria), res.Summary)
	for _, v := range res.Verdicts {
		lbl := verdictLabel[v.Verdict]
		if v.Downgraded {
			lbl += " · 근거 없음"
		}
		text := ""
		for _, c := range r.Criteria {
			if c.N == v.Criterion {
				text = head(c.Text, 60)
			}
		}
		fmt.Fprintf(&b, "%d. %s — %s\n", v.Criterion, lbl, text)
	}
	if len(res.Assumed) > 0 {
		fmt.Fprintf(&b, "가정함: %s", strings.Join(res.Assumed, " · "))
	} else {
		b.WriteString("가정함: 없음")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ── turn prompt (harness v0.9.16) ─────────────────────────────────────────

// TaskCardBlock is `<task_card>` for a card task, with its fixed line.
func TaskCardBlock(r *Row, refs []RefLine, mcp bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<task_card id=%q label=%q version=\"%d\" delegator=%q>\n", r.ID.String(), r.Label(), r.Version, r.DelegatorName)
	fmt.Fprintf(&b, "Goal: %s\nCriteria:\n", r.Goal)
	for _, c := range r.Criteria {
		fmt.Fprintf(&b, "%d. %s (verify: %s)\n", c.N, c.Text, c.Method)
	}
	fmt.Fprintf(&b, "Do not: %s\n", r.Boundaries)
	if len(refs) > 0 {
		b.WriteString("References:\n")
		for _, x := range refs {
			if x.Missing {
				fmt.Fprintf(&b, "- %s (deleted) (id %s)\n", x.Kind, x.ID)
			} else {
				fmt.Fprintf(&b, "- %s %s (id %s)\n", x.Kind, x.Label, x.ID)
			}
		}
	}
	if r.OutputFormat != nil && *r.OutputFormat != "" {
		fmt.Fprintf(&b, "Output: %s\n", *r.OutputFormat)
	}
	if r.BudgetUSD != nil {
		fmt.Fprintf(&b, "Budget: $%s\n", trimFloat(*r.BudgetUSD))
	}
	if r.Version >= 2 && r.ReviseReason != nil {
		fmt.Fprintf(&b, "Revision %d — reason: %s\n", r.Version, *r.ReviseReason)
	}
	b.WriteString("</task_card>\n")
	b.WriteString(TaskCardLine(mcp) + "\n\n")
	return b.String()
}

// RefLine is one reference as the prompt shows it.
type RefLine struct {
	Kind, ID, Label string
	Missing         bool
}

func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s
}

// TaskCardLine is the fixed line after `<task_card>`.
func TaskCardLine(mcp bool) string {
	if mcp {
		return "When this card's work is done, submit a result card with the `colab_card_report` tool — a verdict for every criterion with evidence, and what you confirmed apart from what you assumed. Then the `colab_status_set` tool (status: done)."
	}
	return "When this card's work is done, submit a result card: `colab card report --file <result.json>` — a verdict for every criterion with evidence, and what you confirmed apart from what you assumed. Then `colab status set done`."
}

// ResultCardsBlock is `<result_cards>` for the delegator's join turn.
func ResultCardsBlock(rs []*Row, mcp bool) string {
	var body strings.Builder
	n := 0
	for _, r := range rs {
		res := ParseResult(r.Result)
		if res == nil {
			continue
		}
		n++
		auto := ""
		if res.Auto {
			auto = ` auto="true"`
		}
		fmt.Fprintf(&body, "<result_card label=%q version=\"%d\" assignee=%q met=\"%d/%d\"%s>\n", r.Label(), r.Version, r.AssigneeName, res.MetCount, len(r.Criteria), auto)
		fmt.Fprintf(&body, "Summary: %s\n", res.Summary)
		vs := append([]Verdict(nil), res.Verdicts...)
		sort.Slice(vs, func(i, j int) bool { return vs[i].Criterion < vs[j].Criterion })
		for _, v := range vs {
			text := ""
			for _, c := range r.Criteria {
				if c.N == v.Criterion {
					text = head(c.Text, 80)
				}
			}
			down := ""
			if v.Downgraded {
				down = " (saved as partial: no evidence)"
			}
			line := fmt.Sprintf("%d. %s%s — %s", v.Criterion, v.Verdict, down, text)
			if len(v.Evidence) > 0 {
				ev := make([]string, len(v.Evidence))
				for i, e := range v.Evidence {
					lbl := e.Ref
					if e.Label != nil && *e.Label != "" {
						lbl = *e.Label
					}
					ev[i] = e.Kind + " " + lbl
				}
				line += " — evidence: " + strings.Join(ev, "; ")
			} else if v.Note != nil && *v.Note != "" {
				line += " — note: " + *v.Note
			}
			body.WriteString(line + "\n")
		}
		if len(res.Confirmed) > 0 {
			fmt.Fprintf(&body, "Confirmed: %s\n", strings.Join(res.Confirmed, "; "))
		}
		if len(res.Assumed) > 0 {
			fmt.Fprintf(&body, "Assumed: %s\n", strings.Join(res.Assumed, "; "))
		} else {
			body.WriteString("Assumed: none\n")
		}
		if res.Deviations != nil && *res.Deviations != "" {
			fmt.Fprintf(&body, "Deviations: %s\n", *res.Deviations)
		}
		if res.OpenIssues != nil && *res.OpenIssues != "" {
			fmt.Fprintf(&body, "Open issues: %s\n", *res.OpenIssues)
		}
		body.WriteString("</result_card>\n")
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("<result_cards count=%d>\n%s</result_cards>\n%s\n\n", n, body.String(), ResultCardsLine(mcp))
}

// ResultCardsLine is the fixed line after `<result_cards>`.
func ResultCardsLine(mcp bool) string {
	if mcp {
		return "Judge each card with the `colab_card_accept` or `colab_card_revise` tool (comment for accept, reason for revise: \"<what you checked / what is missing>\"). Read Assumed and unsupported verdicts first."
	}
	return "Judge each card: `colab card accept C-n --comment \"<what you checked>\"`, or `colab card revise C-n --reason \"<what is missing>\"`. Read Assumed and unsupported verdicts first."
}

// BoardBlock is `<card_board>`: the open cards of the mission (in_progress ·
// result_submitted) in number order, sub-cards under their parent; closed ones
// only as a count. Empty when the mission has no card.
func BoardBlock(work string, rs []*Row) string {
	if len(rs) == 0 {
		return ""
	}
	var open []*Row
	pending, closed := 0, 0
	for _, r := range rs {
		switch r.Status {
		case InProgress:
			open = append(open, r)
		case ResultSubmitted:
			open = append(open, r)
			pending++
		default:
			closed++
		}
	}
	byParent := map[string][]*Row{}
	openIDs := map[string]bool{}
	for _, r := range open {
		openIDs[r.ID.String()] = true
	}
	var roots []*Row
	for _, r := range open {
		if r.ParentCardID != nil && openIDs[r.ParentCardID.String()] {
			byParent[r.ParentCardID.String()] = append(byParent[r.ParentCardID.String()], r)
		} else {
			roots = append(roots, r)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<card_board work=%q total=%d pending=%d closed=%d>\n", work, len(rs), pending, closed)
	var walk func(r *Row, depth int)
	walk = func(r *Row, depth int) {
		prefix := "- "
		if depth > 0 {
			prefix = strings.Repeat("  ", depth) + "└ "
		}
		fmt.Fprintf(&b, "%s%s v%d %s [%s] %s — do not: %s\n", prefix, r.Label(), r.Version, r.AssigneeName, r.Status, head(r.Goal, 80), head(r.Boundaries, 80))
		for _, c := range byParent[r.ID.String()] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	b.WriteString("</card_board>\nBefore writing a new card, check that it does not overlap these.\n\n")
	return b.String()
}

// QuestionHead is the question task's fixed first line inside `<trigger>`.
func QuestionHead(asker string) string {
	return fmt.Sprintf("This is a question from %s. Answer it and end your turn — do not start new work. Only a card hands over work.\n", asker)
}

// FollowUpTrigger is the `result_card_missing` trigger (n of 2).
func FollowUpTrigger(r *Row, n int, mcp bool) string {
	how := "Submit it now: `colab card report --file <result.json>` — a verdict for every criterion."
	if mcp {
		how = "Submit it now with the `colab_card_report` tool — a verdict for every criterion."
	}
	return fmt.Sprintf("<trigger reason=\"result_card_missing\" card=%q version=\"%d\" follow_up=\"%d\" of=\"%d\">\nYour last turn on %s ended without a result card. %s If you cannot finish, say so in the verdicts (partial / unmet) — do not leave it empty.\n</trigger>\n\n",
		r.Label(), r.Version, n, MaxFollowUps, r.Label(), how)
}

// FollowUpMessage is the system line the follow-up task hangs off (a task
// needs a trigger message; the prompt uses FollowUpTrigger instead).
func FollowUpMessage(r *Row, n int) string {
	return fmt.Sprintf("%s v%d — %s 의 턴이 결과 카드 없이 끝나 결과 카드를 요청했습니다 (%d/%d).", r.Label(), r.Version, r.AssigneeName, n, MaxFollowUps)
}

// Brief lines (harness v0.9.16).

// ReportRule is brief [2]'s fixed line for every role.
func ReportRule(mcp bool) string {
	if mcp {
		return "- If you received a card, finish with a result card (the `colab_card_report` tool) before setting status done.\n"
	}
	return "- If you received a card, finish with a result card (`colab card report`) before `colab status set done`.\n"
}

// DelegateRule is brief [3]'s delegation sentence (lead only).
func DelegateRule(mcp bool) string {
	if mcp {
		return "- Delegate only with a card: the `colab_card_delegate` tool — goal, criteria each with how to verify (test, artifact, run, review, inspect), what not to do, and references by id. Mentioning another agent only asks a question.\n"
	}
	return "- Delegate only with a card: `colab card delegate --file <card.json>` — goal, criteria each with how to verify (test, artifact, run, review, inspect), what not to do, and references by id. Mentioning another agent only asks a question.\n"
}

// NewWorkRule is brief [3]'s 「새 일은 새 카드」 sentence.
func NewWorkRule(mcp bool) string {
	if mcp {
		return "- New work is a new card; to change a card's work, use the `colab_card_revise` tool.\n"
	}
	return "- New work is a new card; to change a card's work, use `colab card revise`.\n"
}
