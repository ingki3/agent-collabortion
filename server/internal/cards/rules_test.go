package cards

import (
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// 회귀 주입(PR 본문 표): 각 규칙을 끄면 아래 표의 해당 행이 FAIL 한다 —
// method 검사 삭제 → (no-method) · self_delegation 삭제 → (self) · 기준 빠짐
// 검사 삭제 → (missing) · 중복 검사 삭제 → (dup) · 강등 삭제 → (downgrade) ·
// Transition 의 사람 revise-from-accepted 삭제 → (person-revise-accepted) ·
// MayJudge 의 위임자 비교 → (other-agent) · 질문 표의 status_set 값 필터 →
// TestQuestionTable.

func validDraft(a uuid.UUID) Draft {
	return Draft{AssigneeID: a, Goal: "로그인 화면을 만든다",
		Criteria:   []Criterion{{Text: "로그인 테스트가 통과한다", Method: "test"}},
		Boundaries: "결제 모듈은 건드리지 않는다"}
}

func TestCheckDraft(t *testing.T) {
	me, you := uuid.New(), uuid.New()
	s := func(v string) *string { return &v }
	f := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		name string
		mut  func(d *Draft)
		want []string // field:code, in order
	}{
		{"ok", func(d *Draft) {}, nil},
		{"no-assignee", func(d *Draft) { d.AssigneeID = uuid.Nil }, []string{"card.agent_id:required"}},
		{"self", func(d *Draft) { d.AssigneeID = me }, []string{"card.agent_id:self_delegation"}},
		{"no-goal", func(d *Draft) { d.Goal = "  " }, []string{"card.goal:required"}},
		{"long-goal", func(d *Draft) { d.Goal = strings.Repeat("가", MaxGoal+1) }, []string{"card.goal:too_long"}},
		{"no-criteria", func(d *Draft) { d.Criteria = nil }, []string{"card.criteria:required"}},
		{"eight-criteria", func(d *Draft) {
			for i := 0; i < 7; i++ {
				d.Criteria = append(d.Criteria, Criterion{Text: "x", Method: "run"})
			}
		}, []string{"card.criteria:too_many"}},
		{"no-method", func(d *Draft) { d.Criteria[0].Method = "" }, []string{"card.criteria[0].method:method_required"}},
		{"bad-method", func(d *Draft) { d.Criteria[0].Method = "vibes" }, []string{"card.criteria[0].method:method_required"}},
		{"empty-criterion", func(d *Draft) { d.Criteria[0].Text = "" }, []string{"card.criteria[0].text:required"}},
		{"no-boundaries", func(d *Draft) { d.Boundaries = "" }, []string{"card.boundaries:required"}},
		{"bad-ref", func(d *Draft) { d.Refs = []Ref{{Kind: "url", ID: uuid.New()}} }, []string{"card.refs[0].kind:invalid"}},
		{"ref-no-id", func(d *Draft) { d.Refs = []Ref{{Kind: "artifact"}} }, []string{"card.refs[0].id:required"}},
		{"long-output", func(d *Draft) { d.OutputFormat = s(strings.Repeat("a", 201)) }, []string{"card.output_format:too_long"}},
		{"zero-budget", func(d *Draft) { d.BudgetUSD = f(0) }, []string{"card.budget_usd:invalid"}},
		// every rule broken, not the first — the agent fixes all in one go.
		{"all-at-once", func(d *Draft) { d.Goal, d.Boundaries, d.Criteria[0].Method = "", "", "" }, []string{
			"card.goal:required", "card.criteria[0].method:method_required", "card.boundaries:required"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := validDraft(you)
			c.mut(&d)
			var got []string
			for _, e := range CheckDraft(d, me) {
				got = append(got, e.Field+":"+e.Code)
				if e.Message == "" || !hasHangul(e.Message) {
					t.Errorf("%s: sentence must be people's words, got %q", e.Field, e.Message)
				}
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("errors = %v, want %v", got, c.want)
			}
		})
	}
}

func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}

func TestCheckResult(t *testing.T) {
	art := uuid.New().String()
	ev := []Evidence{{Kind: "artifact", Ref: art}}
	base := func() ResultIn {
		return ResultIn{Summary: "로그인 화면을 만들었고 테스트가 통과한다.",
			Verdicts: []VerdictIn{
				{Criterion: 1, Verdict: "met", Evidence: ev},
				{Criterion: 2, Verdict: "partial"},
				{Criterion: 3, Verdict: "unmet"},
			},
			Confirmed: []string{"테스트 12개 통과"}, Assumed: []string{}}
	}
	for _, c := range []struct {
		name     string
		mut      func(r *ResultIn)
		want     []string
		down     []int
		metCount int
	}{
		{"ok", func(r *ResultIn) {}, nil, []int{}, 1},
		{"missing", func(r *ResultIn) { r.Verdicts = r.Verdicts[:2] }, []string{"verdicts:missing_criteria"}, nil, 0},
		{"dup", func(r *ResultIn) { r.Verdicts[2].Criterion = 2 }, []string{"verdicts:missing_criteria", "verdicts:duplicate_criteria"}, nil, 0},
		{"out-of-range", func(r *ResultIn) {
			r.Verdicts = append(r.Verdicts, VerdictIn{Criterion: 4, Verdict: "met", Evidence: ev})
		},
			[]string{"verdicts[3].criterion:out_of_range"}, nil, 0},
		{"no-summary", func(r *ResultIn) { r.Summary = "" }, []string{"summary:required"}, nil, 0},
		{"no-confirmed", func(r *ResultIn) { r.Confirmed = nil }, []string{"confirmed:required"}, nil, 0},
		{"bad-verdict", func(r *ResultIn) { r.Verdicts[1].Verdict = "done" }, []string{"verdicts[1].verdict:invalid"}, nil, 0},
		{"bad-commit", func(r *ResultIn) { r.Verdicts[0].Evidence = []Evidence{{Kind: "commit", Ref: "xyz"}} }, []string{"verdicts[0].evidence[0].ref:invalid"}, nil, 0},
		{"commit-ok", func(r *ResultIn) { r.Verdicts[0].Evidence = []Evidence{{Kind: "commit", Ref: "abc1234"}} }, nil, []int{}, 1},
		// FR-3.8 3: a met with no evidence is saved as partial, not refused.
		{"downgrade", func(r *ResultIn) {
			r.Verdicts[0].Evidence = nil
			r.Verdicts[1] = VerdictIn{Criterion: 2, Verdict: "met"}
		}, nil, []int{1, 2}, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mut(&in)
			res, down, errs := CheckResult(in, 3)
			var got []string
			for _, e := range errs {
				got = append(got, e.Field+":"+e.Code)
			}
			if !slices.Equal(got, c.want) {
				t.Fatalf("errors = %v, want %v", got, c.want)
			}
			if c.want != nil {
				return
			}
			if !slices.Equal(down, c.down) {
				t.Errorf("downgraded = %v, want %v", down, c.down)
			}
			if res.MetCount != c.metCount {
				t.Errorf("met = %d, want %d", res.MetCount, c.metCount)
			}
			for _, v := range res.Verdicts {
				if slices.Contains(c.down, v.Criterion) && (v.Verdict != "partial" || v.StatedVerdict != "met" || !v.Downgraded) {
					t.Errorf("criterion %d: %+v, want saved partial stated met", v.Criterion, v)
				}
			}
		})
	}
	if n := DowngradeNotice([]int{1, 3}); n == nil || *n != "Criterion 1, 3 said met without evidence, so it was saved as partial. Add evidence and submit again if it is really met." {
		t.Errorf("notice = %v", n)
	}
	if DowngradeNotice(nil) != nil {
		t.Error("no downgrade, no notice")
	}
}

func TestAuto(t *testing.T) {
	r := Auto(3)
	if !r.Auto || r.MetCount != 0 || len(r.Verdicts) != 3 || r.Summary != AutoSummary {
		t.Fatalf("auto = %+v", r)
	}
	for i, v := range r.Verdicts {
		if v.Criterion != i+1 || v.Verdict != "unmet" {
			t.Errorf("verdict %d = %+v", i, v)
		}
	}
}

func TestTransition(t *testing.T) {
	for _, c := range []struct {
		from   string
		a      Action
		person bool
		to     string
		ok     bool
	}{
		{InProgress, ActSubmit, false, ResultSubmitted, true},
		{ResultSubmitted, ActSubmit, false, ResultSubmitted, true}, // 뒤의 것이 이긴다
		{Accepted, ActSubmit, false, Accepted, false},
		{Cancelled, ActSubmit, false, Cancelled, false},
		{ResultSubmitted, ActAccept, false, Accepted, true},
		{InProgress, ActAccept, false, InProgress, false},
		{Accepted, ActAccept, true, Accepted, false},
		{ResultSubmitted, ActRevise, false, InProgress, true},
		{Accepted, ActRevise, false, Accepted, false}, // an agent cannot un-accept
		{Accepted, ActRevise, true, InProgress, true}, // person-revise-accepted
		{InProgress, ActRevise, true, InProgress, false},
		{Cancelled, ActRevise, true, Cancelled, false},
		{InProgress, ActCancel, false, Cancelled, true},
		{ResultSubmitted, ActCancel, false, Cancelled, true},
		{Accepted, ActCancel, false, Accepted, false},
	} {
		to, ok := Transition(c.from, c.a, c.person)
		if to != c.to || ok != c.ok {
			t.Errorf("%s %s person=%v → %s,%v; want %s,%v", c.from, c.a, c.person, to, ok, c.to, c.ok)
		}
	}
}

func TestMayJudge(t *testing.T) {
	deleg, other, dir, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	w := Judges{Delegator: deleg, People: []uuid.UUID{dir}}
	for _, c := range []struct {
		name string
		j    Judge
		want bool
	}{
		{"delegator", Judge{Agent: &deleg}, true},
		{"other-agent", Judge{Agent: &other}, false},
		{"director", Judge{Person: &dir}, true},
		{"stranger", Judge{Person: &stranger}, false},
		{"nobody", Judge{}, false},
		// an agent id that happens to equal a person's id is still an agent
		{"agent-as-person", Judge{Agent: &dir}, false},
	} {
		if got := MayJudge(c.j, w); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if a := Actions(Accepted, Judge{Person: &dir}, w); !slices.Equal(a, []string{"revise"}) {
		t.Errorf("person on accepted: %v", a)
	}
	if a := Actions(Accepted, Judge{Agent: &deleg}, w); len(a) != 0 {
		t.Errorf("agent on accepted: %v", a)
	}
	if a := Actions(ResultSubmitted, Judge{Agent: &deleg}, w); !slices.Equal(a, []string{"accept", "revise"}) {
		t.Errorf("delegator on result_submitted: %v", a)
	}
}

func TestQuestionTable(t *testing.T) {
	for _, c := range []string{"artifact_submit", "card_delegate", "card_report", "decision_record", "review_approve", "hitl_approve_request", "work_propose", "card_accept", "card_revise"} {
		if InQuestionTable(c) {
			t.Errorf("%s must not be in the question table", c)
		}
	}
	for _, c := range []string{"room_get", "message_post", "status_set", "hitl_ask", "card_get"} {
		if !InQuestionTable(c) {
			t.Errorf("%s must be in the question table", c)
		}
	}
	if QuestionStatusAllowed("done") || !QuestionStatusAllowed("working") || !QuestionStatusAllowed("blocked") {
		t.Error("status set in a question: working · blocked only")
	}
	if got := QuestionRefusal("artifact submit"); got != "이 턴은 질문에 답하는 턴입니다 — artifact submit 를 쓸 수 없습니다. 일을 맡기려면 카드로 위임하세요" {
		t.Errorf("refusal = %q", got)
	}
}

func TestLabel(t *testing.T) {
	if Label(3) != "C-3" {
		t.Fatal(Label(3))
	}
	for in, want := range map[string]int{"C-3": 3, "c-12": 12, " C-1 ": 1} {
		if n, ok := ParseLabel(in); !ok || n != want {
			t.Errorf("ParseLabel(%q) = %d,%v", in, n, ok)
		}
	}
	for _, in := range []string{"C-0", "C-", "3", "D-3", "C-x"} {
		if _, ok := ParseLabel(in); ok {
			t.Errorf("ParseLabel(%q) accepted", in)
		}
	}
}
