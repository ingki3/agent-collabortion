package httpapi

// #400 리뷰(scratchpad/review400a.md · review400b.md) 후속 — 서버 쪽 NN 을
// 잠그는 테스트. 각 테스트의 주석에 물리는 주입을 적는다.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"

	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// 400a NN1 — PRD FR-3.8 2 ①: a blocked_q thread reply is the answer that
// re-enters the child only when the DELEGATOR writes it. Another agent
// mentioning the child there asks a question: the child's lane stays
// blocked, reentry unchanged, the task is kind question.
// 주입: question.go 의 위임자 대조를 빼면(모든 작성자 = 답) W 의 멘션이
// answer/card 가 되어 FAIL.
func TestBlockedThreadOnlyDelegatorAnswers(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sess := mustUUID(t, f.sessionID)
	leadTask, rTask, rLane := f.delegatedChild(t)
	res, err := f.setStatus(ctx, rTask, 1, "blocked", "범위가 어디까지인가요?")
	if err != nil || res.QuestionMessageID == nil {
		t.Fatalf("blocked: %v %+v", err, res)
	}
	f.finishNoResult(t, rTask) // a blocked turn is exempt from the card gate — the card stays open
	if st := f.laneStatus(t, rLane); st != "blocked" {
		t.Fatalf("lane = %s, want blocked", st)
	}
	var reentry0 int
	if err := f.pool.QueryRow(ctx, `SELECT reentry_count FROM lane WHERE id = $1`, rLane).Scan(&reentry0); err != nil {
		t.Fatal(err)
	}

	// W (not the delegator) has a turn of its own and replies in the thread.
	wTask := f.mentionTask(t, f.wUUID, "W", "")
	f.runTask(t, wTask)
	out, err := f.srv.Router.Post(ctx, sess,
		router.Author{Type: "agent", AgentID: &f.wUUID, TaskID: &wTask, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("R", f.rUUID) + " 제가 보기엔 국내만입니다", ParentId: nullableUUID(*res.QuestionMessageID)})
	if err != nil {
		t.Fatal(err)
	}
	if *out.Message.Speech != "question" || len(out.Triggers) != 1 {
		t.Fatalf("non-delegator reply: speech %s triggers %v, want question ×1", *out.Message.Speech, out.Triggers)
	}
	var kind string
	if err := f.pool.QueryRow(ctx, `SELECT kind FROM task WHERE id = $1`, uuid.UUID(out.Triggers[0].TaskId)).Scan(&kind); err != nil || kind != "question" {
		t.Fatalf("non-delegator reply task kind %q (%v), want question", kind, err)
	}
	var reentry1 int
	var st string
	if err := f.pool.QueryRow(ctx, `SELECT reentry_count, status::text FROM lane WHERE id = $1`, rLane).Scan(&reentry1, &st); err != nil {
		t.Fatal(err)
	}
	if reentry1 != reentry0 || st != "blocked" {
		t.Fatalf("a passer-by re-opened the card lane: reentry %d→%d status %s", reentry0, reentry1, st)
	}

	// The delegator's reply is the answer: the lane re-enters as a card task.
	leadNext, _, _ := f.queuedOnLane(t, f.laneOfTask(t, leadTask))
	f.runTask(t, leadNext)
	ans, err := f.srv.Router.Post(ctx, sess,
		router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadNext, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("R", f.rUUID) + " 국내만입니다", ParentId: nullableUUID(*res.QuestionMessageID)})
	if err != nil {
		t.Fatal(err)
	}
	if *ans.Message.Speech != "answer" {
		t.Fatalf("delegator reply speech %s, want answer", *ans.Message.Speech)
	}
	var reentry2 int
	if err := f.pool.QueryRow(ctx, `SELECT reentry_count FROM lane WHERE id = $1`, rLane).Scan(&reentry2); err != nil {
		t.Fatal(err)
	}
	if reentry2 != reentry0+1 {
		t.Fatalf("delegator answer reentry %d→%d, want +1", reentry0, reentry2)
	}
	var rk string
	if err := f.pool.QueryRow(ctx, `SELECT kind FROM task WHERE lane_id = $1 AND status = 'queued' ORDER BY created_at DESC LIMIT 1`, rLane).Scan(&rk); err != nil || rk != "card" {
		t.Fatalf("re-entry task kind %q (%v), want card", rk, err)
	}
}

// 400a NN2: a revise over the FR-3.5 limit changes the card but makes no task
// and stops the room (openapi reviseCard). When the room owner lets the room
// go on, that version gets its card task — otherwise it never runs and the
// join above it waits forever.
// 주입: unblockRoomForLoop 의 ResumeStalledCards 호출을 빼면 FAIL.
func TestReviseStoppedByLoopResumes(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	leadTask, c1, c2 := f.twoChildren(t)
	f.report(t, c1)
	f.finishCompleted(t, c1)
	card := f.taskCard(t, c1)
	// Leave the Lead's delegating turn running; fill the pair counter so the
	// revise's hop (Lead → R) trips it.
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_pair_roundtrips": 1, "max_hops_per_hour": 50},
	})
	now := time.Now()
	for i := 0; i < 3; i++ {
		for _, pair := range [][2]string{{f.lead, f.r}, {f.r, f.lead}} {
			if _, err := f.pool.Exec(ctx, `INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, created_at) VALUES ($1, $2, $3, 2, $4)`,
				f.sessionID, pair[0], pair[1], now); err != nil {
				t.Fatal(err)
			}
		}
	}
	rv, err := f.srv.Router.Revise(ctx, card.ID, router.Judgement{TaskID: &leadTask, Attempt: 1}, "출처를 붙여 주세요", router.Patch{})
	if err != nil {
		t.Fatal(err)
	}
	if rv.Task != nil {
		t.Fatalf("revise over the loop limit made a task %v — the test did not trip the limit", rv.Task.Id)
	}
	if c := f.taskCard(t, c1); c.Version != 2 || c.Status != "in_progress" {
		t.Fatalf("card after the stopped revise: v%d %s", c.Version, c.Status)
	}
	before := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1`, card.LaneID)
	var hitlID string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'loop' AND status = 'open'`, f.sessionID).Scan(&hitlID); err != nil {
		t.Fatalf("no loop stop: %v", err)
	}
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response", map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1`, card.LaneID); n != before+1 {
		t.Fatalf("after the resume the card lane has %d tasks, want %d (the stopped version's task)", n, before+1)
	}
	var kind string
	var cardID *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT kind, card_id FROM task WHERE lane_id = $1 ORDER BY created_at DESC LIMIT 1`, card.LaneID).Scan(&kind, &cardID); err != nil {
		t.Fatal(err)
	}
	if kind != "card" || cardID == nil || *cardID != card.ID {
		t.Fatalf("resumed task kind %q card %v", kind, cardID)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND status = 'queued'`, card.LaneID); n != 1 {
		t.Fatalf("queued on the card lane = %d, want 1", n)
	}
	// The other open card (C-2 v1, its task already made) is not queued again.
	if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1`, f.laneOfTask(t, c2)); n != 1 {
		t.Fatalf("an open card whose version already has its task got another: %d", n)
	}
}

// ── 400a NN3·NN4·NN5: the reviewer's eight probes (review400a §회귀 주입 —
// each guard below survived the PR suite when removed). ─────────────────

// secondRoomMessage makes another room of the workspace with one message in
// it and returns that message's id — a ref that exists, but not here.
func (f *p2Fixture) secondRoomMessage(t *testing.T) uuid.UUID {
	t.Helper()
	room := str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/rooms", map[string]any{"name": "다른 방"}), "id")
	msg := f.api.must(201, "POST", f.p+"/rooms/"+room+"/messages", map[string]any{"content": "/note 다른 방의 말"}, "Idempotency-Key", uuid.NewString())
	return mustUUID(t, str(msg["message"].(map[string]any), "id"))
}

func metWith(kind, ref string) cards.ResultIn {
	return cards.ResultIn{Summary: "했습니다.", Confirmed: []string{"확인"}, Assumed: []string{},
		Verdicts: []cards.VerdictIn{{Criterion: 1, Verdict: "met", Evidence: []cards.Evidence{{Kind: kind, Ref: ref}}}}}
}

// Probe 5 (근거 존재): evidence pointing at a message of another room is
// refused; the same room's message is accepted.
// 주입: SubmitResult 의 CheckEvidenceExist 를 빼면 FAIL.
func TestCardResultEvidenceMustBeInTheRoom(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	_, rTask, _ := f.delegatedChild(t)
	c := f.taskCard(t, rTask)
	other := f.secondRoomMessage(t)
	if _, err := f.srv.Router.SubmitResult(ctx, rTask, 1, c.ID, metWith("message", other.String())); problemCode(err) != "result_card_incomplete" {
		t.Fatalf("evidence in another room: err = %v, want result_card_incomplete", err)
	}
	here := f.post(t, map[string]any{"content": "/note 이 방의 근거"})
	hereID := str(here["message"].(map[string]any), "id")
	if _, err := f.srv.Router.SubmitResult(ctx, rTask, 1, c.ID, metWith("message", hereID)); err != nil {
		t.Fatalf("evidence in this room: %v", err)
	}
}

// Probe 6 (refs 같은 방): a card whose reference is another room's message
// is card_invalid; this room's message is fine.
// 주입: Delegate 의 CheckRefsExist 를 빼면 FAIL.
func TestCardRefsMustBeInTheRoom(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, leadTask)
	other := f.secondRoomMessage(t)
	in := testCard(f.rUUID, "A 조사")
	in.Card.Refs = []cards.Ref{{Kind: "message", ID: other}}
	if _, err := f.srv.Router.Delegate(ctx, leadTask, in); problemCode(err) != "card_invalid" {
		t.Fatalf("ref in another room: err = %v, want card_invalid", err)
	}
	if n := f.count(t, `SELECT count(*) FROM task_card WHERE room_id = $1`, f.sessionID); n != 0 {
		t.Fatalf("refused card was stored: %d", n)
	}
	here := f.post(t, map[string]any{"content": "/note 참고"})
	in.Card.Refs = []cards.Ref{{Kind: "message", ID: mustUUID(t, str(here["message"].(map[string]any), "id"))}}
	if _, err := f.srv.Router.Delegate(ctx, leadTask, in); err != nil {
		t.Fatalf("ref in this room: %v", err)
	}
}

// Probe 7 (옛 판 제출): the turn that was working on v1 (and already
// submitted it) when a revise made v2 may not submit again — its result would be judged against criteria it never
// saw. The v2 turn submits.
// 주입: SubmitResult 의 「현재 판」 dispatched_at 대조를 빼면 FAIL.
func TestCardResultFromAnOldVersionRefused(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	leadTask, rTask, rLane := f.delegatedChild(t)
	c := f.taskCard(t, rTask)
	// R submits v1 and is still in that turn when Lead revises.
	f.report(t, rTask)
	f.fake.Advance(time.Minute)
	goal := "A 조사 — 출처까지"
	rv, err := f.srv.Router.Revise(ctx, c.ID, router.Judgement{TaskID: &leadTask, Attempt: 1}, "범위를 바꿉니다", router.Patch{Goal: &goal})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.autoReport(ctx, rTask, 1); problemCode(err) != "not_card_task" {
		t.Fatalf("v1 turn submitting after the revise: err = %v, want not_card_task", err)
	}
	if rv.Task == nil || f.laneOfTask(t, uuid.UUID(rv.Task.Id)) != rLane {
		t.Fatalf("revise re-entry = %+v", rv.Task)
	}
	f.finishNoResult(t, rTask)
	v2 := uuid.UUID(rv.Task.Id)
	f.fake.Advance(time.Minute)
	f.runTask(t, v2)
	f.report(t, v2)
	if got := f.taskCard(t, v2); got.Status != cards.ResultSubmitted || got.Version != 2 {
		t.Fatalf("v2 result: %s v%d", got.Status, got.Version)
	}
}

// Probe 8 (요약 카운트, NN4): CompletionProgress.cards counts per status and
// the weak criteria (partial·unmet) of submitted/accepted results.
// 주입: Summary 가 partial 을 세지 않게 바꾸면 FAIL.
func TestCardSummaryCounts(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	_, c1, _ := f.twoChildren(t)
	c := f.taskCard(t, c1)
	note := "출처 하나가 모자랍니다"
	in := cards.ResultIn{Summary: "대부분 했습니다.", Confirmed: []string{"확인"}, Assumed: []string{},
		Verdicts: []cards.VerdictIn{{Criterion: 1, Verdict: "partial", Note: &note}}}
	if _, err := f.srv.Router.SubmitResult(ctx, c1, 1, c.ID, in); err != nil {
		t.Fatal(err)
	}
	if c.WorkID == nil {
		t.Fatal("card has no mission")
	}
	s, err := cards.Summary(ctx, f.pool, *c.WorkID)
	if err != nil || s == nil {
		t.Fatalf("summary: %v %v", s, err)
	}
	if s.Total != 2 || s.PendingJudgement != 1 || s.InProgress != 1 || s.Accepted != 0 || s.WeakCriteria != 1 {
		t.Fatalf("summary %+v, want total 2 · pending 1 · in progress 1 · accepted 0 · weak 1", *s)
	}
	if got := s.Line(); got != "작업 카드 2장 — 수락 0 · 판정 대기 1 · 진행 중 1 · 부분/미충족 기준 1" {
		t.Fatalf("summary line %q", got)
	}
}

// Probes 1·2 (게이트 예외): a card turn that ends while another task already
// waits on its lane (a person's word) makes no follow-up — that task is the
// next card turn; a turn that ended `blocked` is exempt too.
// 주입: lanedone 게이트의 queued 대조 / blocked 예외를 빼면 FAIL.
func TestCardGateExceptions(t *testing.T) {
	t.Run("queued task waits", func(t *testing.T) {
		f := newP2Fixture(t)
		_, rTask, rLane := f.delegatedChild(t)
		f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 표도 넣어 주세요"})
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND status = 'queued'`, rLane); n != 1 {
			t.Fatalf("the person's word did not queue on the card lane: %d", n)
		}
		f.finishNoResult(t, rTask)
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND trigger_reason = 'result_card_missing'`, rLane); n != 0 {
			t.Fatalf("follow-up made although a card turn waits: %d", n)
		}
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND status = 'queued' AND kind = 'card'`, rLane); n != 1 {
			t.Fatalf("the waiting task is not the next card turn: %d", n)
		}
	})
	t.Run("blocked turn", func(t *testing.T) {
		f := newP2Fixture(t)
		_, rTask, rLane := f.delegatedChild(t)
		if _, err := f.setStatus(t.Context(), rTask, 1, "blocked", "범위가 어디까지인가요?"); err != nil {
			t.Fatal(err)
		}
		f.finishNoResult(t, rTask)
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND trigger_reason = 'result_card_missing'`, rLane); n != 0 {
			t.Fatalf("a blocked turn got a follow-up: %d", n)
		}
		if st := f.laneStatus(t, rLane); st != "blocked" {
			t.Fatalf("lane = %s, want blocked", st)
		}
		if c := f.taskCard(t, rTask); c.Status != cards.InProgress || c.FollowUps != 0 {
			t.Fatalf("card after a blocked turn: %s follow-ups %d", c.Status, c.FollowUps)
		}
	})
}

// Probes 3·4 (질문 lane): a question that made its own lane ends it `done`
// (nothing else will); a person's word merged into a queued question task
// promotes it to a normal task (a person's instruction is never a question).
// 주입: EndQuestion 의 새 lane done / promoteQueued 를 빼면 FAIL.
func TestQuestionLaneEndAndPromotion(t *testing.T) {
	t.Run("own lane ends done", func(t *testing.T) {
		f := newP2Fixture(t)
		ctx := t.Context()
		leadTask, _, _ := f.delegatedChild(t)
		res, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID),
			router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTask, Attempt: 1},
			gen.MessageCreate{Content: router.MentionLink("W", f.wUUID) + " 문체는 어떻게 할까요?"})
		if err != nil || len(res.Triggers) != 1 {
			t.Fatalf("question to W: %v %v", err, res)
		}
		q := uuid.UUID(res.Triggers[0].TaskId)
		wLane := f.laneOfTask(t, q)
		f.runTask(t, q)
		f.finishNoResult(t, q)
		if st := f.laneStatus(t, wLane); st != "done" {
			t.Fatalf("question-made lane = %s, want done", st)
		}
	})
	t.Run("person's word promotes", func(t *testing.T) {
		f := newP2Fixture(t)
		ctx := t.Context()
		leadTask, _, _ := f.delegatedChild(t)
		res, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID),
			router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTask, Attempt: 1},
			gen.MessageCreate{Content: router.MentionLink("W", f.wUUID) + " 문체는 어떻게 할까요?"})
		if err != nil || len(res.Triggers) != 1 {
			t.Fatalf("question to W: %v %v", err, res)
		}
		q := uuid.UUID(res.Triggers[0].TaskId)
		f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 그리고 초안도 써 주세요"})
		var kind string
		var n int
		if err := f.pool.QueryRow(ctx, `SELECT kind, cardinality(coalesced_message_ids) FROM task WHERE id = $1`, q).Scan(&kind, &n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal("the person's word did not merge into the queued question task")
		}
		if kind != "normal" {
			t.Fatalf("merged task kind = %q, want normal (a person's instruction is not a question)", kind)
		}
	})
}

// 400b NN6: a card turn's follow-up fails for good → lane failed, card
// cancelled (the join goes on). When the Director restarts that lane the
// card comes back — same version, in_progress — so the restarted turn is a
// card turn and its result is accepted.
// 주입: restartLane 의 cards.ReviveOnLane 을 빼면 FAIL(card_not_open).
func TestRestartRevivesCancelledCard(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	_, rTask, rLane := f.delegatedChild(t)
	c := f.taskCard(t, rTask)
	f.finishNoResult(t, rTask) // → result_card_missing follow-up
	fu, _, reason := f.queuedOnLane(t, rLane)
	if reason == nil || *reason != "result_card_missing" {
		t.Fatalf("no follow-up queued: %v", reason)
	}
	f.runTask(t, fu)
	for {
		ft, err := f.srv.Tasks.Finish(ctx, fu, currentAttempt(t, f, fu), contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther})
		if err != nil {
			t.Fatal(err)
		}
		if string(ft) != "queued" {
			break
		}
		f.runTask(t, fu) // a retry attempt; fail it too
	}
	if st := f.laneStatus(t, rLane); st != "failed" {
		t.Fatalf("lane = %s, want failed", st)
	}
	if got := f.taskCard(t, rTask); got.Status != cards.Cancelled {
		t.Fatalf("card after the failed follow-up = %s, want cancelled", got.Status)
	}
	st, out, _ := f.api.do("POST", f.p+"/lanes/"+rLane.String()+"/restart", map[string]any{"content": "다시 해 주세요"}, "Idempotency-Key", uuid.NewString())
	if st != 202 {
		t.Fatalf("restart: %d %v", st, out)
	}
	got, err := cards.Get(ctx, f.pool, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != cards.InProgress || got.Version != 1 || got.FollowUps != 0 {
		t.Fatalf("card after restart: %s v%d follow-ups %d, want in_progress v1 0", got.Status, got.Version, got.FollowUps)
	}
	nt, kind, _ := f.queuedOnLane(t, rLane)
	var cardID *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT card_id FROM task WHERE id = $1`, nt).Scan(&cardID); err != nil {
		t.Fatal(err)
	}
	if kind != "card" || cardID == nil || *cardID != c.ID {
		t.Fatalf("restarted task kind %q card %v, want a card task of %s", kind, cardID, c.ID)
	}
	f.fake.Advance(time.Minute)
	f.runTask(t, nt)
	f.report(t, nt)
	if got := f.taskCard(t, nt); got.Status != cards.ResultSubmitted {
		t.Fatalf("result after restart: %s", got.Status)
	}
}

// ── #400 재리뷰(review400c) 후속 ─────────────────────────────────────────

// stalledRevise: the room is already stopped for another reason (manual) when
// the delegator's revise goes over the FR-3.5 limit — pauseForLoop raises no
// loop gate (one gate per room), so nothing but that gate's lift can bring
// the version back. Returns the card and its lane's task count.
func (f *p2Fixture) stalledRevise(t *testing.T) (*cards.Row, int) {
	t.Helper()
	ctx := t.Context()
	leadTask, c1, _ := f.twoChildren(t)
	f.report(t, c1)
	f.finishCompleted(t, c1)
	card := f.taskCard(t, c1)
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_pair_roundtrips": 1, "max_hops_per_hour": 50},
	})
	now := time.Now()
	for i := 0; i < 3; i++ {
		for _, pair := range [][2]string{{f.lead, f.r}, {f.r, f.lead}} {
			if _, err := f.pool.Exec(ctx, `INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, created_at) VALUES ($1, $2, $3, 2, $4)`,
				f.sessionID, pair[0], pair[1], now); err != nil {
				t.Fatal(err)
			}
		}
	}
	// The manual stop, as the gate's row (the API's block also cancels the
	// delegator's running turn, whose token the revise below judges with).
	if _, err := f.pool.Exec(ctx, `UPDATE room SET blocked_reason = 'manual', blocked_detail = '{}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	rv, err := f.srv.Router.Revise(ctx, card.ID, router.Judgement{TaskID: &leadTask, Attempt: 1}, "출처를 붙여 주세요", router.Patch{})
	if err != nil {
		t.Fatal(err)
	}
	if rv.Task != nil {
		t.Fatal("revise over the loop limit made a task — the limit did not trip")
	}
	if n := f.count(t, `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND purpose = 'loop'`, f.sessionID); n != 0 {
		t.Fatalf("a loop request was raised on an already stopped room: %d", n)
	}
	var reason string
	if err := f.pool.QueryRow(ctx, `SELECT blocked_reason::text FROM room WHERE id = $1`, f.sessionID).Scan(&reason); err != nil || reason != "manual" {
		t.Fatalf("room gate = %q (%v), want manual", reason, err)
	}
	return card, f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1`, card.LaneID)
}

// 400c NN1: the manual lift brings the stopped version back. With the limit
// no longer tripped (raised meanwhile) the card task is queued at the lift;
// still over it, the lift raises the loop gate instead, and the owner's
// approval then queues it — either way the version runs exactly once.
// 주입: unblockRoom 의 ResumeStalledCards 호출을 빼면 (lifted) FAIL; 비-loop
// 사유의 hop 재판정을 빼면 (still over) 가 한도를 넘어 task 를 만들어 FAIL.
func TestReviseStalledUnderAnotherGate(t *testing.T) {
	t.Run("lifted", func(t *testing.T) {
		f := newP2Fixture(t)
		card, before := f.stalledRevise(t)
		f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
			"loop_limits": map[string]any{"max_pair_roundtrips": 20, "max_hops_per_hour": 50},
		})
		f.api.must(200, "POST", f.p+"/rooms/"+f.sessionID+"/unblock", nil)
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND kind = 'card' AND card_id = $2 AND status = 'queued'`, card.LaneID, card.ID); n != 1 {
			t.Fatalf("after the manual lift the card lane has %d queued card tasks, want 1 (lane tasks before %d)", n, before)
		}
		// A second lift path (nothing stalled any more) queues nothing more.
		if _, err := f.pool.Exec(t.Context(), `UPDATE room SET blocked_reason = 'manual', blocked_detail = '{}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
			t.Fatal(err)
		}
		f.api.must(200, "POST", f.p+"/rooms/"+f.sessionID+"/unblock", nil)
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1`, card.LaneID); n != before+1 {
			t.Fatalf("lane tasks after a second lift = %d, want %d", n, before+1)
		}
	})
	t.Run("still over the limit", func(t *testing.T) {
		f := newP2Fixture(t)
		card, before := f.stalledRevise(t)
		f.api.must(200, "POST", f.p+"/rooms/"+f.sessionID+"/unblock", nil)
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1`, card.LaneID); n != before {
			t.Fatalf("a lift under a still-tripped limit queued the card (%d → %d)", before, n)
		}
		var hitlID string
		if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'loop' AND status = 'open'`, f.sessionID).Scan(&hitlID); err != nil {
			t.Fatalf("the lift did not raise the loop gate: %v", err)
		}
		f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response", map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND kind = 'card' AND card_id = $2`, card.LaneID, card.ID); n != 2 {
			t.Fatalf("card tasks on the lane after the loop approval = %d, want 2 (v1's + the stopped v2's)", n)
		}
	})
}

// 400c NN2: the restart's revival stops at its two edges — a card its
// closed mission cancelled stays cancelled, and an accepted card stays
// accepted (the restart's task is then a normal one).
// 주입: ReviveOnLane 의 닫힌 미션 조건을 빼면 (closed mission) FAIL,
// status = 'cancelled' 조건을 넓히면 (accepted) FAIL.
func TestRestartRevivalEdges(t *testing.T) {
	restart := func(t *testing.T, f *p2Fixture, lane uuid.UUID) {
		t.Helper()
		if st, out, _ := f.api.do("POST", f.p+"/lanes/"+lane.String()+"/restart", map[string]any{"content": "다시 해 주세요"}, "Idempotency-Key", uuid.NewString()); st != 202 {
			t.Fatalf("restart: %d %v", st, out)
		}
	}
	t.Run("closed mission", func(t *testing.T) {
		f := newP2Fixture(t)
		ctx := t.Context()
		_, rTask, rLane := f.delegatedChild(t)
		c := f.taskCard(t, rTask)
		// The mission closes (its open cards are cancelled with it) and its
		// lane failed; a restart of that lane does not bring the card back.
		if _, err := f.pool.Exec(ctx, `UPDATE task_card SET status = 'cancelled' WHERE work_id = $1`, *c.WorkID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE work SET status = 'cancelled' WHERE id = $1`, *c.WorkID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'failed' WHERE id = $1`, rLane); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'failed' WHERE id = $1`, rTask); err != nil {
			t.Fatal(err)
		}
		restart(t, f, rLane)
		if got, _ := cards.Get(ctx, f.pool, c.ID); got.Status != cards.Cancelled {
			t.Fatalf("closed mission's card after restart = %s, want cancelled", got.Status)
		}
	})
	t.Run("accepted", func(t *testing.T) {
		f := newP2Fixture(t)
		ctx := t.Context()
		leadTask, rTask, rLane := f.delegatedChild(t)
		c := f.taskCard(t, rTask)
		f.report(t, rTask)
		f.finishCompleted(t, rTask)
		if _, err := f.srv.Router.Accept(ctx, c.ID, router.Judgement{TaskID: &leadTask, Attempt: 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'failed' WHERE id = $1`, rLane); err != nil {
			t.Fatal(err)
		}
		restart(t, f, rLane)
		if got, _ := cards.Get(ctx, f.pool, c.ID); got.Status != cards.Accepted {
			t.Fatalf("accepted card after restart = %s, want accepted", got.Status)
		}
		if _, kind, _ := f.queuedOnLane(t, rLane); kind != "normal" {
			t.Fatalf("restart task on an accepted card's lane is %q, want normal", kind)
		}
	})
}
