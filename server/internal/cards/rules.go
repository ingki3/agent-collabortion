// Package cards is PRD FR-3.8 (v0.19.15, Director 결정 2026-09-30): work an
// agent hands to another agent goes on a delegation card, and the agent that
// received it ends with a result card.
//
// This file is the pure half — every rule that decides something without a
// database: the delegation card's check (422 card_invalid), the result card's
// check and the met→partial downgrade (422 result_card_incomplete), the card
// state machine, who may judge, the automatic result card and the fixed
// sentences. card_test.go pins each as a table.
//
// Where cards are enforced is not here: lanedone.MarkDone is the one CARD GATE
// (a card lane does not end without a result), the router writes the cards and
// their bubbles, and the handlers only translate.
package cards

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
)

// Status is openapi CardStatus.
const (
	InProgress      = "in_progress"
	ResultSubmitted = "result_submitted"
	Accepted        = "accepted"
	Cancelled       = "cancelled"
)

// Task kinds (openapi TaskKind) and the one server trigger reason.
const (
	KindNormal   = "normal"
	KindCard     = "card"
	KindQuestion = "question"

	ReasonResultCardMissing = "result_card_missing"
)

// Limits from openapi TaskCardInput · CardResultInput.
const (
	MaxGoal         = 500
	MaxCriteria     = 7
	MaxCriterion    = 300
	MaxBoundaries   = 500
	MaxRefs         = 10
	MaxOutputFormat = 200
	MaxSummary      = 600
	MaxEvidence     = 5
	MaxNote         = 300
	MaxListItem     = 300
	MaxListItems    = 10
	MaxDeviation    = 600
	MaxFollowUps    = 2
)

// Methods is openapi CardCriterionMethod in contract order.
var Methods = []string{"test", "artifact", "run", "review", "inspect"}

// Criterion is one completion criterion of a card (TaskCard.criteria[]).
type Criterion struct {
	N      int    `json:"n"`
	Text   string `json:"text"`
	Method string `json:"method"`
}

// Ref is one reference (CardRefInput).
type Ref struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id"`
}

// Draft is a delegation card as the checks see it — TaskCardInput after JSON
// decoding, with the assignee already an id.
type Draft struct {
	AssigneeID   uuid.UUID
	Goal         string
	Criteria     []Criterion // N ignored on input; numbered 1.. in order
	Boundaries   string
	Refs         []Ref
	OutputFormat *string
	BudgetUSD    *float64
}

func runes(s string) int { return utf8.RuneCountInString(s) }

func blank(s string) bool { return strings.TrimSpace(s) == "" }

// CheckDraft is FR-3.8 1's table: every rule broken, not the first, each with
// the field path and a sentence the agent can act on (it reads errors[] and
// submits again). Participation and the refs' existence need the database and
// are the caller's (CheckRefsExist, router.Delegate).
func CheckDraft(d Draft, delegator uuid.UUID) []apperr.FieldError {
	var errs []apperr.FieldError
	add := func(field, code, msg string) { errs = append(errs, apperr.Field(field, code, msg)) }
	if d.AssigneeID == uuid.Nil {
		add("card.agent_id", "required", "담당 에이전트를 적어 주세요 — 방 참여자인 에이전트 한 명입니다")
	} else if d.AssigneeID == delegator {
		add("card.agent_id", "self_delegation", "자기 자신에게는 위임할 수 없습니다 — 직접 하거나 다른 참여자에게 맡기세요")
	}
	switch {
	case blank(d.Goal):
		add("card.goal", "required", "목표를 한두 문장으로 적어 주세요 — 무엇이 되면 끝인지")
	case runes(d.Goal) > MaxGoal:
		add("card.goal", "too_long", fmt.Sprintf("목표는 %d자까지입니다 — 한두 문장으로 줄이고 자세한 것은 참고 자료로 가리키세요", MaxGoal))
	}
	switch {
	case len(d.Criteria) == 0:
		add("card.criteria", "required", "완료 기준을 하나 이상 적어 주세요 — 기준마다 확인 방법(test · artifact · run · review · inspect)과 함께")
	case len(d.Criteria) > MaxCriteria:
		add("card.criteria", "too_many", fmt.Sprintf("완료 기준은 %d개까지입니다 — 더 많으면 카드를 나누세요", MaxCriteria))
	}
	for i, c := range d.Criteria {
		p := fmt.Sprintf("card.criteria[%d]", i)
		switch {
		case blank(c.Text):
			add(p+".text", "required", fmt.Sprintf("완료 기준 %d 이 비어 있습니다", i+1))
		case runes(c.Text) > MaxCriterion:
			add(p+".text", "too_long", fmt.Sprintf("완료 기준 %d 은 %d자까지입니다", i+1, MaxCriterion))
		}
		if !validMethod(c.Method) {
			add(p+".method", "method_required", fmt.Sprintf("완료 기준 %d 에 확인 방법이 없습니다 — test(테스트) · artifact(산출물) · run(실행 결과) · review(검토) · inspect(눈으로 확인) 중 하나를 고르세요. 확인할 수 없는 기준(「잘 만들기」)은 받지 않습니다", i+1))
		}
	}
	switch {
	case blank(d.Boundaries):
		add("card.boundaries", "required", "하지 않을 것을 적어 주세요 — 다른 담당과 겹치는 영역이나 손대지 말 파일")
	case runes(d.Boundaries) > MaxBoundaries:
		add("card.boundaries", "too_long", fmt.Sprintf("하지 않을 것은 %d자까지입니다", MaxBoundaries))
	}
	if len(d.Refs) > MaxRefs {
		add("card.refs", "too_many", fmt.Sprintf("참고 자료는 %d개까지입니다", MaxRefs))
	}
	for i, r := range d.Refs {
		p := fmt.Sprintf("card.refs[%d]", i)
		if r.Kind != "artifact" && r.Kind != "decision" && r.Kind != "message" {
			add(p+".kind", "invalid", "참고 자료는 artifact · decision · message 중 하나입니다 — 같은 방의 id 로 가리키세요")
		}
		if r.ID == uuid.Nil {
			add(p+".id", "required", "참고 자료의 id 를 적어 주세요")
		}
	}
	if d.OutputFormat != nil && runes(*d.OutputFormat) > MaxOutputFormat {
		add("card.output_format", "too_long", fmt.Sprintf("결과물 형식은 한 줄(%d자)까지입니다", MaxOutputFormat))
	}
	if d.BudgetUSD != nil && *d.BudgetUSD <= 0 {
		add("card.budget_usd", "invalid", "예산은 0보다 커야 합니다 — 정하지 않으려면 비워 두세요")
	}
	return errs
}

func validMethod(m string) bool {
	for _, x := range Methods {
		if m == x {
			return true
		}
	}
	return false
}

// Numbered gives criteria their 1-based numbers in order.
func Numbered(cs []Criterion) []Criterion {
	out := make([]Criterion, len(cs))
	for i, c := range cs {
		out[i] = Criterion{N: i + 1, Text: strings.TrimSpace(c.Text), Method: c.Method}
	}
	return out
}

// Evidence is one CardEvidence.
type Evidence struct {
	Kind  string  `json:"kind"`
	Ref   string  `json:"ref"`
	Label *string `json:"label"`
}

// VerdictIn is one CardResultInput.verdicts[].
type VerdictIn struct {
	Criterion int
	Verdict   string
	Evidence  []Evidence
	Note      *string
}

// ResultIn is CardResultInput.
type ResultIn struct {
	Summary    string
	Verdicts   []VerdictIn
	Confirmed  []string
	Assumed    []string
	Deviations *string
	OpenIssues *string
}

// Verdict is one stored CardResult.verdicts[].
type Verdict struct {
	Criterion     int        `json:"criterion"`
	Verdict       string     `json:"verdict"`
	StatedVerdict string     `json:"stated_verdict"`
	Downgraded    bool       `json:"downgraded"`
	Evidence      []Evidence `json:"evidence"`
	Note          *string    `json:"note"`
}

// Result is the stored CardResult (the columns the server fills — cost,
// duration, message, time — are set by the writer).
type Result struct {
	Summary     string    `json:"summary"`
	Verdicts    []Verdict `json:"verdicts"`
	Confirmed   []string  `json:"confirmed"`
	Assumed     []string  `json:"assumed"`
	Deviations  *string   `json:"deviations"`
	OpenIssues  *string   `json:"open_issues"`
	MetCount    int       `json:"met_count"`
	Auto        bool      `json:"auto"`
	CostUSD     *float64  `json:"cost_usd"`
	DurationS   *int      `json:"duration_s"`
	MessageID   *string   `json:"message_id"`
	SubmittedAt string    `json:"submitted_at"`
}

var commitRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

// CheckResult is FR-3.8 3's table against a card with n criteria. It returns
// every broken rule (422 result_card_incomplete), and — when there is none —
// the result to store and the criteria whose `met` had no evidence and were
// saved as `partial` (openapi submitCardResult: 낮춰 저장하고 거절하지 않는다).
// Whether an artifact·message ref exists in the room is the caller's
// (CheckEvidenceExist).
func CheckResult(in ResultIn, n int) (Result, []int, []apperr.FieldError) {
	var errs []apperr.FieldError
	add := func(field, code, msg string) { errs = append(errs, apperr.Field(field, code, msg)) }
	switch {
	case blank(in.Summary):
		add("summary", "required", "요약을 2~3 문장으로 적어 주세요 — 무엇을 했고 결과가 어떤지")
	case runes(in.Summary) > MaxSummary:
		add("summary", "too_long", fmt.Sprintf("요약은 %d자까지입니다", MaxSummary))
	}
	seen := map[int]int{}
	for i, v := range in.Verdicts {
		p := fmt.Sprintf("verdicts[%d]", i)
		if v.Criterion < 1 || v.Criterion > n {
			add(p+".criterion", "out_of_range", fmt.Sprintf("기준 %d 은 이 카드에 없습니다 — 기준은 1부터 %d까지입니다", v.Criterion, n))
		} else {
			seen[v.Criterion]++
		}
		if v.Verdict != "met" && v.Verdict != "partial" && v.Verdict != "unmet" {
			add(p+".verdict", "invalid", "판정은 met(충족) · partial(부분) · unmet(미충족) 중 하나입니다")
		}
		if len(v.Evidence) > MaxEvidence {
			add(p+".evidence", "too_many", fmt.Sprintf("근거는 기준마다 %d개까지입니다", MaxEvidence))
		}
		for j, e := range v.Evidence {
			ep := fmt.Sprintf("%s.evidence[%d]", p, j)
			switch e.Kind {
			case "artifact", "message":
				if _, err := uuid.Parse(e.Ref); err != nil {
					add(ep+".ref", "invalid", "아티팩트·메시지 근거는 같은 방의 id(uuid)로 가리키세요 — 로그·스크린샷은 아티팩트로 내고 그 id 를")
				}
			case "commit":
				if !commitRe.MatchString(e.Ref) {
					add(ep+".ref", "invalid", "커밋 근거는 해시 7~40자입니다")
				}
			default:
				add(ep+".kind", "invalid", "근거는 artifact · message · commit 중 하나입니다")
			}
		}
		if v.Note != nil && runes(*v.Note) > MaxNote {
			add(p+".note", "too_long", fmt.Sprintf("메모는 %d자까지입니다", MaxNote))
		}
	}
	var missing, dup []string
	for c := 1; c <= n; c++ {
		switch {
		case seen[c] == 0:
			missing = append(missing, strconv.Itoa(c))
		case seen[c] > 1:
			dup = append(dup, strconv.Itoa(c))
		}
	}
	if len(missing) > 0 {
		add("verdicts", "missing_criteria", fmt.Sprintf("기준 %s 의 판정이 없습니다 — 위임 카드의 기준을 하나도 빠짐없이(1부터 %d까지 각 한 번) 판정하세요. 못 끝냈으면 partial · unmet 으로", strings.Join(missing, ", "), n))
	}
	if len(dup) > 0 {
		add("verdicts", "duplicate_criteria", fmt.Sprintf("기준 %s 을 두 번 이상 판정했습니다 — 기준마다 한 번씩", strings.Join(dup, ", ")))
	}
	if len(in.Confirmed) == 0 {
		add("confirmed", "required", "직접 확인한 것을 하나 이상 적어 주세요 — 확인하지 않고 가정한 것은 assumed 에 따로")
	}
	listCheck := func(field string, xs []string) {
		if len(xs) > MaxListItems {
			add(field, "too_many", fmt.Sprintf("%s 는 %d개까지입니다", field, MaxListItems))
		}
		for i, x := range xs {
			if blank(x) {
				add(fmt.Sprintf("%s[%d]", field, i), "required", "빈 줄이 있습니다")
			} else if runes(x) > MaxListItem {
				add(fmt.Sprintf("%s[%d]", field, i), "too_long", fmt.Sprintf("한 줄은 %d자까지입니다", MaxListItem))
			}
		}
	}
	listCheck("confirmed", in.Confirmed)
	listCheck("assumed", in.Assumed)
	if in.Deviations != nil && runes(*in.Deviations) > MaxDeviation {
		add("deviations", "too_long", fmt.Sprintf("벗어난 점은 %d자까지입니다", MaxDeviation))
	}
	if in.OpenIssues != nil && runes(*in.OpenIssues) > MaxDeviation {
		add("open_issues", "too_long", fmt.Sprintf("남은 문제는 %d자까지입니다", MaxDeviation))
	}
	if len(errs) > 0 {
		return Result{}, nil, errs
	}
	out := Result{Summary: strings.TrimSpace(in.Summary), Confirmed: nonNil(in.Confirmed), Assumed: nonNil(in.Assumed),
		Deviations: in.Deviations, OpenIssues: in.OpenIssues}
	var downgraded []int
	for _, v := range in.Verdicts {
		ev := v.Evidence
		if ev == nil {
			ev = []Evidence{}
		}
		sv := Verdict{Criterion: v.Criterion, Verdict: v.Verdict, StatedVerdict: v.Verdict, Evidence: ev, Note: v.Note}
		if v.Verdict == "met" && len(ev) == 0 {
			sv.Verdict, sv.Downgraded = "partial", true
			downgraded = append(downgraded, v.Criterion)
		}
		if sv.Verdict == "met" {
			out.MetCount++
		}
		out.Verdicts = append(out.Verdicts, sv)
	}
	sort.Slice(out.Verdicts, func(i, j int) bool { return out.Verdicts[i].Criterion < out.Verdicts[j].Criterion })
	sort.Ints(downgraded)
	if downgraded == nil {
		downgraded = []int{}
	}
	return out, downgraded, nil
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// DowngradeNotice is harness v0.9.16's sentence for submitCardResult.notice.
func DowngradeNotice(ns []int) *string {
	if len(ns) == 0 {
		return nil
	}
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	s := fmt.Sprintf("Criterion %s said met without evidence, so it was saved as partial. Add evidence and submit again if it is really met.", strings.Join(parts, ", "))
	return &s
}

// AutoSummary is the automatic result card's summary (PRD FR-3.8 3 「결과
// 카드 없이 끝남」).
const AutoSummary = "결과 카드 없이 끝났습니다 — 서버가 두 번 요청했지만 담당이 결과 카드를 내지 않아 모든 기준을 미충족으로 적었습니다."

// Auto is the automatic result card for a card with n criteria: every
// criterion unmet, `auto` set — the lane then ends so the join is never stuck
// (FR-6.5).
func Auto(n int) Result {
	r := Result{Summary: AutoSummary, Confirmed: []string{}, Assumed: []string{}, Auto: true}
	for c := 1; c <= n; c++ {
		note := "결과 카드가 없어 확인되지 않았습니다"
		r.Verdicts = append(r.Verdicts, Verdict{Criterion: c, Verdict: "unmet", StatedVerdict: "unmet", Evidence: []Evidence{}, Note: &note})
	}
	return r
}

// ── state machine ─────────────────────────────────────────────────────────

// Action is what is being done to a card.
type Action string

const (
	ActSubmit Action = "submit"
	ActAccept Action = "accept"
	ActRevise Action = "revise"
	ActCancel Action = "cancel"
)

// Transition is FR-3.8's card state machine: in_progress → result_submitted →
// accepted | revise(→ in_progress, version +1) | cancelled. byPerson widens
// revise to `accepted` (수락 취소 — openapi reviseCard). It returns the next
// status, or ok=false for a move the card cannot make now.
func Transition(from string, a Action, byPerson bool) (string, bool) {
	switch a {
	case ActSubmit:
		// A second result on the same version wins (openapi submitCardResult
		// 「같은 판에 두 번 내면 뒤의 것이 이긴다」) — still in_progress or
		// already result_submitted, never after a judgement.
		if from == InProgress || from == ResultSubmitted {
			return ResultSubmitted, true
		}
	case ActAccept:
		if from == ResultSubmitted {
			return Accepted, true
		}
	case ActRevise:
		if from == ResultSubmitted || (byPerson && from == Accepted) {
			return InProgress, true
		}
	case ActCancel:
		if from == InProgress || from == ResultSubmitted {
			return Cancelled, true
		}
	}
	return from, false
}

// Judge is who asks to accept or revise.
type Judge struct {
	// Agent: the calling agent (TaskToken) — nil for a person.
	Agent *uuid.UUID
	// Person: the calling user — nil for an agent.
	Person *uuid.UUID
}

// Judges is who may judge a card: its delegator agent, and the mission's
// Director and deputy (outside a mission: the room's owner and deputy).
type Judges struct {
	Delegator uuid.UUID
	People    []uuid.UUID
}

// MayJudge is openapi acceptCard/reviseCard's permission row.
func MayJudge(j Judge, w Judges) bool {
	if j.Agent != nil {
		return *j.Agent == w.Delegator
	}
	if j.Person != nil {
		for _, p := range w.People {
			if p == *j.Person {
				return true
			}
		}
	}
	return false
}

// Actions is TaskCard.actions for a caller.
func Actions(status string, j Judge, w Judges) []string {
	out := []string{}
	if !MayJudge(j, w) {
		return out
	}
	person := j.Person != nil
	if _, ok := Transition(status, ActAccept, person); ok {
		out = append(out, "accept")
	}
	if _, ok := Transition(status, ActRevise, person); ok {
		out = append(out, "revise")
	}
	return out
}

// Label is the display number 「C-3」.
func Label(n int) string { return "C-" + strconv.Itoa(n) }

// ParseLabel reads 「C-3」 (case-insensitive) as 3.
func ParseLabel(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 3 || (s[0] != 'C' && s[0] != 'c') || s[1] != '-' {
		return 0, false
	}
	n, err := strconv.Atoi(s[2:])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// ── the question table (colab-cli.md §2.5 v0.9.10) ───────────────────────

// QuestionCommands is the question task's table: the commands a turn that
// answers a question may use, intersected with the role's row. status_set is
// in it with values working · blocked only (QuestionStatusAllowed).
var QuestionCommands = []string{
	"room_get", "room_messages", "room_list", "room_read", "artifact_get",
	"card_get", "card_list", "message_post", "status_set", "hitl_ask",
}

// InQuestionTable reports whether cmd is in the question table.
func InQuestionTable(cmd string) bool {
	for _, c := range QuestionCommands {
		if c == cmd {
			return true
		}
	}
	return false
}

// QuestionStatusAllowed is the value filter on status_set in a question task.
func QuestionStatusAllowed(status string) bool { return status == "working" || status == "blocked" }

// QuestionRefusal is the 403 command_not_allowed sentence for a question
// task (colab-cli.md §2.5, byte for byte the CLI's).
func QuestionRefusal(cliName string) string {
	return "이 턴은 질문에 답하는 턴입니다 — " + cliName + " 를 쓸 수 없습니다. 일을 맡기려면 카드로 위임하세요"
}

// Sentences the handlers answer with (openapi v0.3.10).
const (
	CardRequiredSentence       = "위임은 카드로 합니다 — 목표·완료 기준(확인 방법)·하지 않을 것을 적은 카드를 내세요"
	ResultCardRequiredSentence = "먼저 결과 카드를 내세요 — colab card report"
	NotCardTaskSentence        = "이 턴은 이 카드로 받은 일이 아닙니다 — 결과 카드는 카드로 받은 턴에서만 낼 수 있습니다"
	CardNotOpenSentence        = "이 카드는 지금 결과를 받지 않습니다 — 이미 판정됐거나 취소됐습니다"
	NotCardJudgeSentence       = "이 카드를 판정할 수 없습니다 — 위임한 에이전트나 미션 Director·부 Director 만 수락·수정 요청합니다"
	CardNotJudgeableSentence   = "지금은 이 카드를 판정할 때가 아닙니다 — 결과 카드가 나온 뒤에 수락·수정 요청하세요"
	// JudgementCommentRequiredSentence is acceptCard's 422
	// judgement_comment_required (openapi v0.3.11, PRD FR-3.8 4).
	JudgementCommentRequiredSentence = "무엇을 확인했는지 코멘트를 적으세요"
	JudgementCommentTooLongSentence  = "코멘트는 600자까지입니다"
)

// JudgementCommentMax is acceptCard's comment maxLength (openapi v0.3.11).
const JudgementCommentMax = 600

// ApplyPatch is TaskCardPatch over the card's current version: given fields
// replace, missing ones keep (the assignee never changes).
func ApplyPatch(r *Row, goal *string, criteria *[]Criterion, boundaries *string, refs *[]Ref, output **string, budget **float64) Draft {
	d := Draft{AssigneeID: r.AssigneeID, Goal: r.Goal, Criteria: r.Criteria, Boundaries: r.Boundaries,
		Refs: r.Refs, OutputFormat: r.OutputFormat, BudgetUSD: r.BudgetUSD}
	if goal != nil {
		d.Goal = *goal
	}
	if criteria != nil {
		d.Criteria = *criteria
	}
	if boundaries != nil {
		d.Boundaries = *boundaries
	}
	if refs != nil {
		d.Refs = *refs
	}
	if output != nil {
		d.OutputFormat = *output
	}
	if budget != nil {
		d.BudgetUSD = *budget
	}
	return d
}
