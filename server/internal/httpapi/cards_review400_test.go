package httpapi

// #400 리뷰(scratchpad/review400a.md · review400b.md) 후속 — 서버 쪽 NN 을
// 잠그는 테스트. 각 테스트의 주석에 물리는 주입을 적는다.

import (
	"testing"
	"time"

	"github.com/google/uuid"

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
