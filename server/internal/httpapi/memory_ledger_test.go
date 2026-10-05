package httpapi

// T-LEDGER (PRD FR-4.6 v0.19.18 · openapi v0.3.12 `memory`): the four
// operations over HTTP with real task tokens — kind write rights, the lesson
// merge, the plan's one-active rule, supersede/retire changing state columns
// only, the 300-character refusal, the source check, the mission scope and
// the question table.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type ledgerFixture struct {
	*p2Fixture
	lead, r *client
	base    string // /works/{mission}/memory
}

func newLedgerFixture(t *testing.T) *ledgerFixture {
	t.Helper()
	f := newP2Fixture(t)
	lf := &ledgerFixture{p2Fixture: f, base: f.p + "/works/" + f.missionID + "/memory"}
	lb := f.claimBundle(t, f.mentionTask(t, f.leadUUID, "Lead", f.missionID))
	lf.lead = &client{t: t, srv: f.api.srv, bearer: lb.TaskToken}
	rb := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	lf.r = &client{t: t, srv: f.api.srv, bearer: rb.TaskToken}
	return lf
}

func (f *ledgerFixture) rows(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM memory_item WHERE work_id = '`+f.missionID+`' AND `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// FR-4.6 2: plan · progress are the lead's — a researcher gets 403
// memory_kind_forbidden and nothing is written; the other four kinds are
// everyone's. certainty is kept for a fact only, outcome for a lesson only.
//
// 회귀 주입: Author.mayWrite 가 늘 true 면 (403), Note 의 certainty kind 검사를
// 지우면 (assignment certainty) FAIL — 후자는 DB CHECK 가 막아 500 으로 FAIL.
func TestLedgerKindRights(t *testing.T) {
	f := newLedgerFixture(t)
	for _, k := range []string{"plan", "progress"} {
		st, out, _ := f.r.do("POST", f.base, map[string]any{"kind": k, "content": "내가 계획을 바꾼다"})
		if st != 403 || str(out, "code") != "memory_kind_forbidden" {
			t.Fatalf("(403) researcher %s = %d %v", k, st, out)
		}
		f.lead.must(201, "POST", f.base, map[string]any{"kind": k, "content": "lead 의 " + k})
	}
	if n := f.rows(t, `kind IN ('plan','progress') AND created_by_agent_id = $1`, f.rUUID); n != 0 {
		t.Fatalf("(403) a refused write left %d rows", n)
	}
	fact := f.r.must(201, "POST", f.base, map[string]any{"kind": "fact", "content": "API 한도는 분당 60회", "certainty": "given", "outcome": "useful"})
	if str(fact, "certainty") != "given" || fact["outcome"] != nil || fact["support_count"].(float64) != 0 || fact["promoted"] != true {
		t.Fatalf("fact = %v", fact)
	}
	if by := fact["created_by"].(map[string]any); by["kind"] != "agent" || by["name"] != "R" || by["id"] != f.p2Fixture.r {
		t.Fatalf("created_by = %v", by)
	}
	as := f.r.must(201, "POST", f.base, map[string]any{"kind": "assignment", "content": "R 이 파서를 맡는다", "certainty": "guess"})
	if as["certainty"] != nil {
		t.Fatalf("(assignment certainty) certainty kept on an assignment: %v", as)
	}
	if st, out, _ := f.r.do("POST", f.base, map[string]any{"kind": "summary", "content": "x"}); st != 422 {
		t.Fatalf("unknown kind = %d %v", st, out)
	}
}

// The lesson merge: the same trimmed content on an active lesson raises its
// support_count and returns it (200) instead of a new row; promoted flips at
// 2. A different content is a new lesson.
//
// 회귀 주입: Note 의 dup 조회를 지우면 (row count), btrim 비교를 원문 비교로 두면
// (trim) FAIL.
func TestLedgerLessonMerge(t *testing.T) {
	f := newLedgerFixture(t)
	first := f.r.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "스크래핑은 403 — 공식 API 를 쓴다", "outcome": "dead_end"})
	if first["support_count"].(float64) != 1 || first["promoted"] != false {
		t.Fatalf("first lesson = %v", first)
	}
	again := f.lead.must(200, "POST", f.base, map[string]any{"kind": "lesson", "content": "  스크래핑은 403 — 공식 API 를 쓴다 \n", "outcome": "useful"})
	if str(again, "id") != str(first, "id") || again["support_count"].(float64) != 2 || again["promoted"] != true || str(again, "outcome") != "dead_end" {
		t.Fatalf("(trim) merged lesson = %v", again)
	}
	if n := f.rows(t, `kind = 'lesson'`); n != 1 {
		t.Fatalf("(row count) %d lesson rows after a merge, want 1", n)
	}
	other := f.r.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "다른 교훈"})
	if str(other, "id") == str(first, "id") || other["support_count"].(float64) != 1 {
		t.Fatalf("a different lesson merged: %v", other)
	}
}

// plan: one active per mission — a new plan supersedes the active one
// automatically, the chain is linked both ways, the old row's content stays.
//
// 회귀 주입: Note 의 plan 자동 대체를 지우면 unique index 위반으로 500 — FAIL.
func TestLedgerPlanAutoSupersede(t *testing.T) {
	f := newLedgerFixture(t)
	p1 := f.lead.must(201, "POST", f.base, map[string]any{"kind": "plan", "content": "1단계: 파서"})
	f.fake.Advance(time.Minute)
	p2 := f.lead.must(201, "POST", f.base, map[string]any{"kind": "plan", "content": "2단계: 렌더러"})
	if str(p2, "supersedes") != str(p1, "id") {
		t.Fatalf("new plan supersedes = %v, want %s", p2["supersedes"], str(p1, "id"))
	}
	if n := f.rows(t, `kind = 'plan' AND status = 'active'`); n != 1 {
		t.Fatalf("%d active plans, want 1", n)
	}
	var status, content string
	var by uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT status, content, superseded_by FROM memory_item WHERE id = $1`, str(p1, "id")).Scan(&status, &content, &by); err != nil {
		t.Fatal(err)
	}
	if status != "superseded" || content != "1단계: 파서" || by.String() != str(p2, "id") {
		t.Fatalf("old plan = %s %q %s", status, content, by)
	}
}

// supersede adds a row and touches only the target's state columns: kind,
// content, certainty, outcome, author, created_at and sources of the target
// are unchanged in the DB; a lesson's support_count is inherited; a second
// supersede of the same target is 409; kind/work_id in the body is 422.
//
// 회귀 주입: markSuperseded 가 content 도 바꾸면 (unchanged), Supersede 의 status
// 검사를 지우면 (409), support_count 를 1 로 두면 (inherit), kind_immutable 검사를
// 지우면 (422) FAIL.
func TestLedgerSupersedeAddsOnly(t *testing.T) {
	f := newLedgerFixture(t)
	src := str(f.post(t, map[string]any{"content": "한도는 분당 60회라고 공지됨", "work_id": f.missionID})["message"].(map[string]any), "id")
	fact := f.r.must(201, "POST", f.base, map[string]any{"kind": "fact", "content": "API 한도 60/분", "certainty": "to_verify", "source_message_ids": []string{src}})
	id := str(fact, "id")
	snap := func() string {
		var s string
		if err := f.pool.QueryRow(t.Context(), `SELECT concat_ws('|', kind, content, certainty, outcome, support_count, created_by_agent_id, created_at, source_message_ids::text, supersedes) FROM memory_item WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before := snap()
	total := f.rows(t, `true`)
	f.fake.Advance(time.Minute)
	if st, out, _ := f.r.do("POST", f.p+"/memory/"+id+"/supersede", map[string]any{"content": "x", "kind": "lesson"}); st != 422 || str(out, "code") != "kind_immutable" {
		t.Fatalf("(422) kind in body = %d %v", st, out)
	}
	out := f.lead.must(201, "POST", f.p+"/memory/"+id+"/supersede", map[string]any{"content": "API 한도 120/분 (문서 확인)", "certainty": "given"})
	item, old := out["item"].(map[string]any), out["superseded"].(map[string]any)
	if str(item, "kind") != "fact" || str(item, "certainty") != "given" || str(item, "supersedes") != id || str(item, "status") != "active" {
		t.Fatalf("new item = %v", item)
	}
	if str(old, "status") != "superseded" || str(old, "superseded_by") != str(item, "id") || old["invalidated_at"] == nil {
		t.Fatalf("old item = %v", old)
	}
	if after := snap(); after != before {
		t.Fatalf("(unchanged) the target's content columns changed:\n before %s\n after  %s", before, after)
	}
	if n := f.rows(t, `true`); n != total+1 {
		t.Fatalf("rows %d → %d, want +1", total, n)
	}
	if st, out, _ := f.r.do("POST", f.p+"/memory/"+id+"/supersede", map[string]any{"content": "또"}); st != 409 || str(out, "code") != "memory_not_active" {
		t.Fatalf("(409) superseding a superseded item = %d %v", st, out)
	}

	// A lesson's support_count is inherited.
	l := f.r.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "캐시를 먼저 비운다"})
	f.lead.must(200, "POST", f.base, map[string]any{"kind": "lesson", "content": "캐시를 먼저 비운다"})
	nl := f.r.must(201, "POST", f.p+"/memory/"+str(l, "id")+"/supersede", map[string]any{"content": "캐시와 색인을 먼저 비운다"})["item"].(map[string]any)
	if nl["support_count"].(float64) != 2 || nl["promoted"] != true || str(nl, "kind") != "lesson" {
		t.Fatalf("(inherit) superseding lesson = %v", nl)
	}
	// A researcher cannot supersede the lead's plan.
	p := f.lead.must(201, "POST", f.base, map[string]any{"kind": "plan", "content": "계획"})
	if st, out, _ := f.r.do("POST", f.p+"/memory/"+str(p, "id")+"/supersede", map[string]any{"content": "내 계획"}); st != 403 || str(out, "code") != "memory_kind_forbidden" {
		t.Fatalf("researcher superseding the plan = %d %v", st, out)
	}
}

// retire: status retired + invalidated_at, gone from the default list, still
// there under status=all, the reason kept beside the row; twice is 409.
func TestLedgerRetireStaysVisible(t *testing.T) {
	f := newLedgerFixture(t)
	q := f.r.must(201, "POST", f.base, map[string]any{"kind": "open_question", "content": "결제 모듈은 누가?"})
	id := str(q, "id")
	out := f.r.must(200, "POST", f.p+"/memory/"+id+"/retire", map[string]any{"reason": "Director 가 범위에서 뺐다"})
	if str(out, "status") != "retired" || out["invalidated_at"] == nil || str(out, "content") != "결제 모듈은 누가?" {
		t.Fatalf("retired = %v", out)
	}
	if list := f.r.mustList(200, "GET", f.base, nil); len(list) != 0 {
		t.Fatalf("default list still shows the retired item: %v", list)
	}
	all := f.api.mustList(200, "GET", f.base+"?status=all", nil)
	if len(all) != 1 || str(all[0].(map[string]any), "status") != "retired" {
		t.Fatalf("status=all (person) = %v", all)
	}
	var reason string
	if err := f.pool.QueryRow(t.Context(), `SELECT retire_reason FROM memory_item WHERE id = $1`, id).Scan(&reason); err != nil || reason != "Director 가 범위에서 뺐다" {
		t.Fatalf("reason = %q %v", reason, err)
	}
	if st, out, _ := f.r.do("POST", f.p+"/memory/"+id+"/retire", map[string]any{"reason": "또"}); st != 409 || str(out, "code") != "memory_not_active" {
		t.Fatalf("retire twice = %d %v", st, out)
	}
	if st, _, _ := f.r.do("POST", f.p+"/memory/"+id+"/retire", map[string]any{"reason": " "}); st != 422 {
		t.Fatalf("blank reason = %d", st)
	}
}

// Bounds and scope: 301 characters is refused (not cut); a source that is
// not a message of this room is 422; a token of a turn outside the mission
// cannot write it; a person can read but not write; a question task may
// read (memory_get) but not note.
//
// 회귀 주입: cleanContent 의 길이 검사를 지우면 DB CHECK 로 500 (422 아님) — FAIL;
// checkSources 를 지우면 (other room) FAIL; QuestionCommands 에서 memory_get 을
// 빼면 (question get) FAIL.
func TestLedgerBoundsAndScope(t *testing.T) {
	f := newLedgerFixture(t)
	if st, out, _ := f.r.do("POST", f.base, map[string]any{"kind": "fact", "content": strings.Repeat("가", 301)}); st != 422 {
		t.Fatalf("301 characters = %d %v", st, out)
	}
	f.r.must(201, "POST", f.base, map[string]any{"kind": "fact", "content": strings.Repeat("가", 300)})
	other := f.secondRoomMessage(t)
	if st, out, _ := f.r.do("POST", f.base, map[string]any{"kind": "fact", "content": "x", "source_message_ids": []string{other.String()}}); st != 422 {
		t.Fatalf("(other room) source = %d %v", st, out)
	}
	if st, out, _ := f.r.do("POST", f.base, map[string]any{"kind": "fact", "content": "x", "source_message_ids": []string{uuid.NewString()}}); st != 422 {
		t.Fatalf("missing source = %d %v", st, out)
	}
	// A person reads (the mission panel) but does not write.
	if st, out, _ := f.api.do("POST", f.base, map[string]any{"kind": "fact", "content": "x"}); st != 403 {
		t.Fatalf("person note = %d %v", st, out)
	}
	if list := f.api.mustList(200, "GET", f.base+"?kind=fact", nil); len(list) != 1 {
		t.Fatalf("person list = %d items", len(list))
	}
	// A turn outside the mission.
	wb := f.claimBundle(t, f.mentionTask(t, f.wUUID, "W", f.missionID))
	f.exec(t, `UPDATE task SET work_id = NULL WHERE id = $1`, wb.Task.ID)
	w := &client{t: t, srv: f.api.srv, bearer: wb.TaskToken}
	if st, out, _ := w.do("POST", f.base, map[string]any{"kind": "fact", "content": "x"}); st != 403 || str(out, "code") != "outside_task_scope" {
		t.Fatalf("outside the mission = %d %v", st, out)
	}
	// A question task: memory_get yes, memory_note no.
	f.exec(t, `UPDATE task SET kind = 'question', work_id = $2 WHERE id = $1`, wb.Task.ID, f.missionID)
	if st, out, _ := w.do("POST", f.base, map[string]any{"kind": "fact", "content": "x"}); st != 403 || str(out, "code") != "command_not_allowed" {
		t.Fatalf("question note = %d %v", st, out)
	}
	if st, out, _ := w.do("GET", f.base, nil); st != 200 {
		t.Fatalf("(question get) = %d %v", st, out)
	}
	_ = fmt.Sprint()
}

// A ledger write leaves its row on the attempt's feed (colab-cli §4) — so a
// turn that only wrote the ledger is not an empty turn.
func TestLedgerWriteFeedRow(t *testing.T) {
	f := newLedgerFixture(t)
	it := f.r.must(201, "POST", f.base, map[string]any{"kind": "fact", "content": "x"})
	if n := f.count(t, `SELECT count(*) FROM task_event WHERE class = 'status' AND verb = 'update' AND object_ref #>> '{}' = $1 AND outcome = 'ok'`, str(it, "id")); n != 1 {
		t.Fatalf("feed rows for the write = %d, want 1", n)
	}
}

// #409 리뷰 J5: concurrent writes. 20 lesson notes of the same content at
// once (two agents) are ONE row; 20 plan notes at once are 20 × 201 with
// exactly one active plan and an unbroken chain — no 500 from the
// one-active-plan index.
//
// 회귀 주입: lockWork 를 지우면 lesson 이 행 둘 이상으로 갈라지고 plan 이 500 — FAIL.
func TestLedgerConcurrentWrites(t *testing.T) {
	f := newLedgerFixture(t)
	const n = 20
	run := func(body func(i int) (int, map[string]any)) []int {
		codes := make([]int, n)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				codes[i], _ = body(i)
			}(i)
		}
		close(start)
		wg.Wait()
		return codes
	}
	lesson := map[string]any{"kind": "lesson", "content": "동시에 겪은 교훈"}
	codes := run(func(i int) (int, map[string]any) {
		c := f.lead
		if i%2 == 1 {
			c = f.r
		}
		st, out, _ := c.do("POST", f.base, lesson)
		return st, out
	})
	for _, st := range codes {
		if st != 200 && st != 201 {
			t.Fatalf("(lesson) concurrent note answered %v", codes)
		}
	}
	if rows := f.rows(t, `kind = 'lesson'`); rows != 1 {
		t.Fatalf("(lesson) %d rows after %d concurrent notes, want 1 (codes %v)", rows, n, codes)
	}
	if sc := f.rows(t, `kind = 'lesson' AND support_count = 2`); sc != 1 {
		t.Fatalf("(lesson) support_count is not 2 (two distinct agents)")
	}
	codes = run(func(i int) (int, map[string]any) {
		st, out, _ := f.lead.do("POST", f.base, map[string]any{"kind": "plan", "content": fmt.Sprintf("plan %02d", i)})
		return st, out
	})
	for _, st := range codes {
		if st != 201 {
			t.Fatalf("(plan) concurrent plan notes answered %v", codes)
		}
	}
	if a := f.rows(t, `kind = 'plan' AND status = 'active'`); a != 1 {
		t.Fatalf("(plan) %d active plans", a)
	}
	if broken := f.rows(t, `kind = 'plan' AND status = 'superseded' AND superseded_by NOT IN (SELECT id FROM memory_item WHERE supersedes IS NOT NULL)`); broken != 0 {
		t.Fatalf("(plan) %d broken chain links", broken)
	}
	if heads := f.rows(t, `kind = 'plan' AND supersedes IS NULL`); heads != 1 {
		t.Fatalf("(plan) %d chain heads, want 1", heads)
	}
}

// openapi v0.3.13 (#409 리뷰 NN3): support_count counts DISTINCT writers. The
// same agent noting the same lesson again gets the item back (200) with
// support_count and last_reinforced_at unchanged; another agent raises both;
// that agent again changes nothing. last_reinforced_at starts at created_at.
//
// 회귀 주입: Note 의 already 검사를 지우면 (same agent) FAIL; last_reinforced_at 을
// 갱신하지 않으면 (reinforced) FAIL.
func TestLedgerLessonSupportIsPerWriter(t *testing.T) {
	f := newLedgerFixture(t)
	body := map[string]any{"kind": "lesson", "content": "로그부터 본다"}
	first := f.r.must(201, "POST", f.base, body)
	if first["last_reinforced_at"] == nil {
		t.Fatalf("a new lesson has no last_reinforced_at: %v", first)
	}
	created := str(first, "created_at")
	f.fake.Advance(time.Hour)
	again := f.r.must(200, "POST", f.base, body)
	if again["support_count"].(float64) != 1 || again["promoted"] != false || str(again, "id") != str(first, "id") {
		t.Fatalf("(same agent) re-note raised support: %v", again)
	}
	if f.reinforced(t, str(first, "id")) != f.created(t, str(first, "id")) {
		t.Fatalf("(same agent) last_reinforced_at moved (created %s)", created)
	}
	f.fake.Advance(time.Hour)
	other := f.lead.must(200, "POST", f.base, body)
	if other["support_count"].(float64) != 2 || other["promoted"] != true {
		t.Fatalf("(other agent) = %v", other)
	}
	if !f.reinforced(t, str(first, "id")).Equal(f.fake.Now()) {
		t.Fatalf("(reinforced) last_reinforced_at = %s, want %s", f.reinforced(t, str(first, "id")), f.fake.Now())
	}
	if again := f.lead.must(200, "POST", f.base, body); again["support_count"].(float64) != 2 {
		t.Fatalf("(other agent twice) = %v", again)
	}
	if f.rows(t, `kind = 'lesson'`) != 1 {
		t.Fatal("a re-note added a row")
	}
	// A person-less, non-lesson item carries no last_reinforced_at.
	if fact := f.r.must(201, "POST", f.base, map[string]any{"kind": "fact", "content": "x"}); fact["last_reinforced_at"] != nil {
		t.Fatalf("a fact has last_reinforced_at: %v", fact)
	}
}

func (f *ledgerFixture) reinforced(t *testing.T, id string) time.Time {
	t.Helper()
	var at time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT last_reinforced_at FROM memory_item WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func (f *ledgerFixture) created(t *testing.T, id string) time.Time {
	t.Helper()
	var at time.Time
	if err := f.pool.QueryRow(t.Context(), `SELECT created_at FROM memory_item WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// #409 리뷰 NN4: the retire reason is readable — retireMemory's answer and
// listMemory (status=all) carry retire_reason; an active item's is null.
//
// 회귀 주입: ToAPI 가 RetireReason 을 싣지 않으면 FAIL.
func TestLedgerRetireReasonIsReadable(t *testing.T) {
	f := newLedgerFixture(t)
	q := f.r.must(201, "POST", f.base, map[string]any{"kind": "open_question", "content": "누가 맡나?"})
	if q["retire_reason"] != nil {
		t.Fatalf("active item has a retire_reason: %v", q)
	}
	out := f.r.must(200, "POST", f.p+"/memory/"+str(q, "id")+"/retire", map[string]any{"reason": "  범위에서 뺐다 "})
	if str(out, "retire_reason") != "범위에서 뺐다" {
		t.Fatalf("retire answer = %v", out)
	}
	all := f.api.mustList(200, "GET", f.base+"?status=retired", nil)
	if len(all) != 1 || str(all[0].(map[string]any), "retire_reason") != "범위에서 뺐다" {
		t.Fatalf("list = %v", all)
	}
}

// #409 리뷰 J16 · J2 · J3 · J4: the gaps the review's injections found.
//
// 회귀 주입: memoryTarget 의 「이 턴의 미션」 검사를 지우면 (J16), Retire 의 kind
// 검사를 지우면 (J2), 병합 조회의 status='active' 를 지우면 (J3), btrim 비교를
// lower() 로 바꾸면 (J4) FAIL.
func TestLedgerReviewGaps(t *testing.T) {
	f := newLedgerFixture(t)
	fact := f.r.must(201, "POST", f.base, map[string]any{"kind": "fact", "content": "값"})
	plan := f.lead.must(201, "POST", f.base, map[string]any{"kind": "plan", "content": "계획"})

	// J16: a turn of the same room outside this mission cannot supersede or retire it.
	wb := f.claimBundle(t, f.mentionTask(t, f.wUUID, "W", f.missionID))
	f.exec(t, `UPDATE task SET work_id = NULL WHERE id = $1`, wb.Task.ID)
	w := &client{t: t, srv: f.api.srv, bearer: wb.TaskToken}
	if st, out, _ := w.do("POST", f.p+"/memory/"+str(fact, "id")+"/supersede", map[string]any{"content": "남의 값"}); st != 403 || str(out, "code") != "outside_task_scope" {
		t.Fatalf("(J16) outside-mission supersede = %d %v", st, out)
	}
	if st, out, _ := w.do("POST", f.p+"/memory/"+str(fact, "id")+"/retire", map[string]any{"reason": "x"}); st != 403 || str(out, "code") != "outside_task_scope" {
		t.Fatalf("(J16) outside-mission retire = %d %v", st, out)
	}

	// J2: a researcher cannot retire the lead's plan; nothing changes.
	if st, out, _ := f.r.do("POST", f.p+"/memory/"+str(plan, "id")+"/retire", map[string]any{"reason": "싫다"}); st != 403 || str(out, "code") != "memory_kind_forbidden" {
		t.Fatalf("(J2) researcher retire plan = %d %v", st, out)
	}
	if f.rows(t, `kind = 'plan' AND status = 'active'`) != 1 || f.rows(t, `status = 'active'`) != 2 {
		t.Fatal("(J2) a refused retire changed the plan")
	}

	// J3: the merge looks at ACTIVE lessons only — a superseded lesson's old
	// words make a new lesson.
	old := f.r.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "캐시를 먼저 비운다"})
	f.r.must(201, "POST", f.p+"/memory/"+str(old, "id")+"/supersede", map[string]any{"content": "캐시와 색인을 비운다"})
	again := f.lead.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "캐시를 먼저 비운다"})
	if str(again, "id") == str(old, "id") || again["support_count"].(float64) != 1 {
		t.Fatalf("(J3) merged into a superseded lesson: %v", again)
	}

	// J4: the match is exact after trim — case differs, so a separate lesson.
	f.r.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "Retry With Backoff"})
	lower := f.lead.must(201, "POST", f.base, map[string]any{"kind": "lesson", "content": "retry with backoff"})
	if lower["support_count"].(float64) != 1 {
		t.Fatalf("(J4) case-insensitive merge: %v", lower)
	}
}
