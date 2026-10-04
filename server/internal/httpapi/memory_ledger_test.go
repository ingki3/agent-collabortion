package httpapi

// T-LEDGER (PRD FR-4.6 v0.19.18 · openapi v0.3.12 `memory`): the four
// operations over HTTP with real task tokens — kind write rights, the lesson
// merge, the plan's one-active rule, supersede/retire changing state columns
// only, the 300-character refusal, the source check, the mission scope and
// the question table.

import (
	"fmt"
	"strings"
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
