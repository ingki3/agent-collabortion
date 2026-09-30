package httpapi

// T-CARD-S integration (PRD FR-3.8, openapi v0.3.10, harness v0.9.16): the
// card's life through the real router, lanedone gate and turn prompt.
//
// 회귀 주입 (PR 본문 표 — 각 규칙을 끄면 아래 이름의 단언이 FAIL):
//   - lanedone CARD GATE 의 AgentDone 거절 삭제            → (gate-done-409)
//   - 턴 종료 게이트(후속 task) 삭제                         → (gate-followup-1)
//   - MaxFollowUps 비교를 없애 자동 결과를 막으면            → (gate-auto)
//   - EndQuestion 대신 일반 종료                             → (question-lane-unchanged)
//   - roles.AllowsFor 의 질문 교집합 삭제                    → (question-artifact-403)
//   - delegate 의 CheckDraft 호출 삭제                       → (card-invalid)
//   - groupResultCards 를 합류 묶음에서 빼면                 → (join-result-cards)
//   - Revise 의 판 +1 / 재진입 삭제                          → (revise-reenter)
//   - MayJudge 위임자 비교 삭제                              → (judge-other-agent)
//   - cards.NextNumber 의 잠금 삭제                          → (numbering-race)

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

func problemCode(err error) string {
	var p *apperr.Problem
	if errors.As(err, &p) {
		return p.Code
	}
	return ""
}

func (f *p2Fixture) taskCard(t *testing.T, taskID uuid.UUID) *cards.Row {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT card_id FROM task WHERE id = $1`, taskID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	c, err := cards.Get(t.Context(), f.pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *p2Fixture) laneStatus(t *testing.T, laneID uuid.UUID) string {
	t.Helper()
	var st string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM lane WHERE id = $1`, laneID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func (f *p2Fixture) laneOfTask(t *testing.T, taskID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id FROM task WHERE id = $1`, taskID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// queuedOnLane is the queued task on a lane (the follow-up / re-entry).
func (f *p2Fixture) queuedOnLane(t *testing.T, laneID uuid.UUID) (id uuid.UUID, kind string, reason *string) {
	t.Helper()
	if err := f.pool.QueryRow(t.Context(), `SELECT id, kind, trigger_reason FROM task WHERE lane_id = $1 AND status = 'queued' ORDER BY created_at DESC LIMIT 1`, laneID).
		Scan(&id, &kind, &reason); err != nil {
		t.Fatalf("no queued task on lane %s: %v", laneID, err)
	}
	return
}

// (card-invalid · self · numbering) delegateLane's card: every broken rule in
// errors[]; delegating to oneself is self_delegation; a good card gets C-1
// (per mission), a delegation bubble (speech delegate, card_role delegation),
// and a first task of kind card.
func TestCardDelegateChecks(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	lead := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, lead)

	bad := testCard(f.rUUID, "조사")
	bad.Card.Boundaries = ""
	bad.Card.Criteria[0].Method = ""
	_, err := f.srv.Router.Delegate(ctx, lead, bad)
	if problemCode(err) != "card_invalid" {
		t.Fatalf("(card-invalid) err = %v", err)
	}
	var p *apperr.Problem
	errors.As(err, &p)
	if len(p.Errors) != 2 {
		t.Fatalf("(card-invalid) errors[] = %+v, want both fields", p.Errors)
	}
	if _, err := f.srv.Router.Delegate(ctx, lead, testCard(f.leadUUID, "자기")); problemCode(err) != "self_delegation" {
		t.Fatalf("(self) err = %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM task_card WHERE room_id = $1`, f.sessionID); n != 0 {
		t.Fatalf("refused cards stored %d rows", n)
	}

	res, err := f.srv.Router.Delegate(ctx, lead, testCard(f.rUUID, "조사"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Card.Label != "C-1" || res.Card.Version != 1 || string(res.Card.Status) != cards.InProgress {
		t.Fatalf("card = %+v", res.Card)
	}
	if res.Message.Speech == nil || string(*res.Message.Speech) != "delegate" {
		t.Fatalf("bubble speech = %v", res.Message.Speech)
	}
	if role, _ := res.Message.CardRole.Get(); string(role) != "delegation" {
		t.Fatalf("bubble card_role = %v", res.Message.CardRole)
	}
	var kind string
	if err := f.pool.QueryRow(ctx, `SELECT kind FROM task WHERE id = $1`, uuid.UUID(res.Task.Id)).Scan(&kind); err != nil || kind != "card" {
		t.Fatalf("first task kind = %q (%v)", kind, err)
	}
	res2, err := f.srv.Router.Delegate(ctx, lead, testCard(f.wUUID, "초안"))
	if err != nil || res2.Card.Label != "C-2" {
		t.Fatalf("second card = %+v %v", res2.Card.Label, err)
	}
}

// (numbering-race) cards delegated at the same moment in one mission get
// distinct numbers 1..N.
func TestCardNumberingConcurrent(t *testing.T) {
	f := newP2Fixture(t)
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	lead := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, lead)
	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := f.srv.Router.Delegate(t.Context(), lead, testCard(f.rUUID, "조각 "+string(rune('A'+i))))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if d := f.count(t, `SELECT count(DISTINCT number) FROM task_card WHERE room_id = $1`, f.sessionID); d != n {
		t.Fatalf("(numbering-race) distinct numbers = %d, want %d", d, n)
	}
	if m := f.count(t, `SELECT max(number) FROM task_card WHERE room_id = $1`, f.sessionID); m != n {
		t.Fatalf("(numbering-race) max number = %d, want %d", m, n)
	}
}

// The card gate (lanedone CARD GATE): done without a result is 409
// result_card_required (turn_end_required false — #393 NN2); a turn end
// without one queues result_card_missing follow-ups (2 per version), then
// the automatic result card ends the lane so the join is never stuck.
func TestCardGate(t *testing.T) {
	t.Run("done-409", func(t *testing.T) {
		f := newP2Fixture(t)
		_, rTask, rLane := f.delegatedChild(t)
		_, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", "")
		if problemCode(err) != "result_card_required" {
			t.Fatalf("(gate-done-409) err = %v", err)
		}
		var p *apperr.Problem
		errors.As(err, &p)
		if p.Status != 409 || p.Extra["turn_end_required"] != false {
			t.Fatalf("(gate-done-409) problem = %+v", p)
		}
		if st := f.laneStatus(t, rLane); st == "done" {
			t.Fatal("(gate-done-409) the lane went done without a result")
		}
		// With the result in, done passes.
		f.report(t, rTask)
		if r, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", ""); err != nil || !r.TurnEndRequired {
			t.Fatalf("done after result: %+v %v", r, err)
		}
	})
	t.Run("turn-end-followups-then-auto", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, rTask, rLane := f.delegatedChild(t)
		f.finishNoResult(t, rTask)
		if st := f.laneStatus(t, rLane); st == "done" || joinFired(t, f, leadTask) {
			t.Fatalf("(gate-followup-1) lane %s / join fired after a turn end with no result", st)
		}
		fu1, kind, reason := f.queuedOnLane(t, rLane)
		if kind != "card" || reason == nil || *reason != cards.ReasonResultCardMissing {
			t.Fatalf("(gate-followup-1) follow-up kind %s reason %v", kind, reason)
		}
		// Its turn prompt carries the harness sentence and the card.
		b := f.claimBundle(t, fu1)
		if !strings.Contains(b.Prompt, `reason="result_card_missing"`) || !strings.Contains(b.Prompt, "<task_card") {
			t.Fatalf("(gate-followup-1) prompt:\n%s", b.Prompt)
		}
		f.runTask(t, fu1)
		f.finishNoResult(t, fu1)
		fu2, _, _ := f.queuedOnLane(t, rLane)
		if fu2 == fu1 {
			t.Fatal("(gate-followup-2) no second follow-up")
		}
		f.runTask(t, fu2)
		f.finishNoResult(t, fu2)
		c := f.taskCard(t, rTask)
		res := cards.ParseResult(c.Result)
		if c.Status != cards.ResultSubmitted || res == nil || !res.Auto || res.MetCount != 0 {
			t.Fatalf("(gate-auto) card %s result %+v", c.Status, res)
		}
		if st := f.laneStatus(t, rLane); st != "done" || !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 {
			t.Fatalf("(gate-auto) lane %s joinFired %v bundles %d", st, joinFired(t, f, leadTask), f.bundleCount(t))
		}
		if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND trigger_reason = 'result_card_missing'`, rLane); n != cards.MaxFollowUps {
			t.Fatalf("follow-ups = %d, want %d", n, cards.MaxFollowUps)
		}
	})
	// (race-submit-vs-end) the result and the turn's end at the same moment:
	// either order, one lane end, no follow-up once the result is in.
	t.Run("race-submit-vs-end", func(t *testing.T) {
		for i := 0; i < 6; i++ {
			f := newP2Fixture(t)
			leadTask, rTask, rLane := f.delegatedChild(t)
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			wg.Add(2)
			go func() { defer wg.Done(); errs <- f.autoReport(t.Context(), rTask, 1) }()
			go func() {
				defer wg.Done()
				_, err := f.srv.Tasks.Finish(t.Context(), rTask, 1, contracts.Finish{Outcome: "completed", StopReason: "end_turn"})
				errs <- err
			}()
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil && problemCode(err) != "not_card_task" {
					t.Fatalf("run %d: %v", i, err)
				}
			}
			c := f.taskCard(t, rTask)
			if c.Status != cards.ResultSubmitted {
				t.Fatalf("run %d: card %s", i, c.Status)
			}
			// Either the finish saw the result (lane done, join) or it came
			// first and queued one follow-up that is now moot.
			st := f.laneStatus(t, rLane)
			if st == "done" {
				if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 {
					t.Fatalf("run %d: lane done without one join", i)
				}
			} else if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND trigger_reason = 'result_card_missing'`, rLane); n != 1 {
				t.Fatalf("run %d: lane %s with %d follow-ups", i, st, n)
			}
		}
	})
}

// Question task (FR-3.8 2): an agent's mention of another agent is a
// question — commands narrowed to role ∩ question table (artifact submit
// refused with the contract sentence), status set only working·blocked, the
// lane is not re-entered and ends as it was, the reply to the asker is an
// answer.
func TestQuestionTask(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	// R already has a finished lane (a delegated card, reported).
	leadTask, rTask, rLane := f.delegatedChild(t)
	f.finishCompleted(t, rTask)
	var reentry int
	if err := f.pool.QueryRow(ctx, `SELECT reentry_count FROM lane WHERE id = $1`, rLane).Scan(&reentry); err != nil {
		t.Fatal(err)
	}
	// Lead's next turn asks R a question by mention.
	leadNext, _, _ := f.queuedOnLane(t, f.laneOfTask(t, leadTask))
	f.runTask(t, leadNext)
	res, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID),
		router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadNext, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("R", f.rUUID) + " 표 2의 출처가 어디죠?"})
	if err != nil {
		t.Fatal(err)
	}
	if *res.Message.Speech != "question" || len(res.Triggers) != 1 {
		t.Fatalf("speech %s triggers %v", *res.Message.Speech, res.Triggers)
	}
	q := uuid.UUID(res.Triggers[0].TaskId)
	var kind string
	if err := f.pool.QueryRow(ctx, `SELECT kind FROM task WHERE id = $1`, q).Scan(&kind); err != nil || kind != "question" {
		t.Fatalf("task kind %q", kind)
	}
	var reentry2 int
	var st string
	if err := f.pool.QueryRow(ctx, `SELECT reentry_count, status::text FROM lane WHERE id = $1`, rLane).Scan(&reentry2, &st); err != nil {
		t.Fatal(err)
	}
	if reentry2 != reentry || st != "done" {
		t.Fatalf("(question-lane-unchanged) routing moved the lane: reentry %d→%d status %s", reentry, reentry2, st)
	}
	b := f.claimBundle(t, q)
	if !strings.Contains(b.Prompt, cards.QuestionHead("Lead")) {
		t.Fatalf("no question head:\n%s", b.Prompt)
	}
	for _, c := range b.Task.AllowedCommands {
		if c == "artifact_submit" || c == "card_delegate" || c == "decision_record" {
			t.Fatalf("(question-artifact-403) bundle allows %s: %v", c, b.Task.AllowedCommands)
		}
	}
	// The server gate: artifact submit refused with the contract sentence.
	st2, body := f.submit(t, f.sessionID, b.TaskToken, "q.md", "doc", []byte("# x"))
	if st2 != 403 || str(body, "code") != "command_not_allowed" || str(body, "detail") != cards.QuestionRefusal("artifact submit") {
		t.Fatalf("(question-artifact-403) %d %v", st2, body)
	}
	// status set done refused; working allowed.
	if _, err := f.srv.Router.SetAgentStatus(ctx, q, 1, "done", ""); problemCode(err) != "command_not_allowed" {
		t.Fatalf("question done: %v", err)
	}
	// The answer to the asker is `answer`, and wakes Lead.
	ans, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID),
		router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &q, Attempt: 1},
		gen.MessageCreate{Content: router.MentionLink("Lead", f.leadUUID) + " 출처는 통계청입니다"})
	if err != nil {
		t.Fatal(err)
	}
	if *ans.Message.Speech != "answer" {
		t.Fatalf("reply speech = %s, want answer", *ans.Message.Speech)
	}
	f.finishNoResult(t, q)
	if st := f.laneStatus(t, rLane); st != "done" {
		t.Fatalf("(question-lane-unchanged) lane after the question turn = %s", st)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE lane_id = $1 AND trigger_reason = 'result_card_missing'`, rLane); n != 0 {
		t.Fatal("a question turn ran the card gate")
	}
}

// Join bundle carries the result cards; the delegator's turn prompt has
// <result_cards>; accept closes one, revise re-enters the same lane with a
// new version (loop-counted); a second agent may not judge.
func TestCardJudgeFlow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	leadTask, c1, c2 := f.twoChildren(t)
	f.report(t, c1)
	f.finishCompleted(t, c1)
	f.report(t, c2)
	f.finishCompleted(t, c2)
	if !joinFired(t, f, leadTask) {
		t.Fatal("join did not fire")
	}
	var bundle string
	if err := f.pool.QueryRow(ctx, `SELECT content FROM message WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%위임한 작업이 모두 끝났습니다%'`, f.sessionID).Scan(&bundle); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bundle, "C-1") || !strings.Contains(bundle, "C-2") {
		t.Fatalf("(join-result-cards) bundle:\n%s", bundle)
	}
	leadNext, _, _ := f.queuedOnLane(t, f.laneOfTask(t, leadTask))
	// Lead's delegating turn is over (one running task per agent).
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed' WHERE id = $1`, leadTask); err != nil {
		t.Fatal(err)
	}
	b := f.claimBundle(t, leadNext)
	if !strings.Contains(b.Prompt, "<result_cards count=2>") || !strings.Contains(b.Prompt, "<card_board") {
		t.Fatalf("(join-result-cards) lead prompt:\n%s", b.Prompt)
	}
	card1, card2 := f.taskCard(t, c1), f.taskCard(t, c2)

	// Another agent (R, the assignee) may not judge.
	if _, err := f.srv.Router.Accept(ctx, card1.ID, router.Judgement{TaskID: &c1, Attempt: 1}); problemCode(err) != "not_card_judge" {
		t.Fatalf("(judge-other-agent) err = %v", err)
	}
	if _, err := f.srv.Router.Accept(ctx, card1.ID, router.Judgement{TaskID: &leadNext, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	if c := f.taskCard(t, c1); c.Status != cards.Accepted {
		t.Fatalf("accepted card = %s", c.Status)
	}
	// Accept twice (agent): not judgeable.
	if _, err := f.srv.Router.Accept(ctx, card1.ID, router.Judgement{TaskID: &leadNext, Attempt: 1}); problemCode(err) != "card_not_judgeable" {
		t.Fatalf("second accept: %v", err)
	}
	goal := "B 조사 — 출처를 붙여서"
	rv, err := f.srv.Router.Revise(ctx, card2.ID, router.Judgement{TaskID: &leadNext, Attempt: 1}, "출처가 없습니다", router.Patch{Goal: &goal})
	if err != nil {
		t.Fatal(err)
	}
	c := f.taskCard(t, c2)
	if c.Version != 2 || c.Status != cards.InProgress || c.Goal != goal {
		t.Fatalf("(revise-reenter) card v%d %s %q", c.Version, c.Status, c.Goal)
	}
	if rv.Task == nil || f.laneOfTask(t, uuid.UUID(rv.Task.Id)) != card2.LaneID {
		t.Fatalf("(revise-reenter) re-entry task = %+v, want on lane %s", rv.Task, card2.LaneID)
	}
	var kind string
	if err := f.pool.QueryRow(ctx, `SELECT kind FROM task WHERE id = $1`, uuid.UUID(rv.Task.Id)).Scan(&kind); err != nil || kind != "card" {
		t.Fatalf("(revise-reenter) task kind %q", kind)
	}
	re := uuid.UUID(rv.Task.Id)
	b2 := f.claimBundle(t, re)
	if !strings.Contains(b2.Prompt, "출처가 없습니다") || !strings.Contains(b2.Prompt, `version="2"`) {
		t.Fatalf("revise prompt lacks the reason / version:\n%s", b2.Prompt)
	}
	// The re-entry ends with a new result: the delegator is told with the card.
	f.report(t, re)
	f.finishCompleted(t, re)
	if c := f.taskCard(t, c2); c.Status != cards.ResultSubmitted || c.Version != 2 {
		t.Fatalf("after re-entry: %s v%d", c.Status, c.Version)
	}
	// A person (the Director) may revise even an accepted card.
	var dir uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM app_user WHERE email = 'dir@example.com'`).Scan(&dir); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Router.Revise(ctx, card1.ID, router.Judgement{UserID: &dir}, "다시 보자", router.Patch{}); err != nil {
		t.Fatalf("person revise accepted: %v", err)
	}
	// Versions are kept: getCard's history has v1.
	if n := f.count(t, `SELECT jsonb_array_length(versions) FROM task_card WHERE id = $1`, card2.ID); n != 1 {
		t.Fatalf("versions kept = %d, want 1", n)
	}
}

// Mission close cancels its open cards.
func TestCardCancelOnMissionClose(t *testing.T) {
	f := newP2Fixture(t)
	_, rTask, _ := f.delegatedChild(t)
	c := f.taskCard(t, rTask)
	// The Director cancels the mission (the room's cancel path).
	f.api.must(200, "POST", f.p+"/works/"+c.WorkID.String()+"/cancel", map[string]any{})
	if got := f.taskCard(t, rTask); got.Status != cards.Cancelled {
		t.Fatalf("card after mission cancel = %s", got.Status)
	}
}
