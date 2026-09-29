package httpapi

// T-RF1: characterization of the two ways a lane becomes `done`.
//
//	(A) the agent's `colab status set done` → router.SetAgentStatus → lanes.MarkDone
//	    with the after-done follow-up (join FR-6.5 · re-entry report).
//	(B) the daemon's end of turn → tasks.Finish(completed) → lanes.MarkDone with
//	    NO follow-up.
//
// These rows pin what each path does TODAY, so the refactor that routes both
// through lanes.MarkDone can be measured as behaviour-preserving. (B) not
// firing the join / re-entry report is the current behaviour — whether it is a
// defect is Lead's decision (T-RF1 PR body, TODO(T-RF1-B)); the rows assert it
// as it is so a change there is a visible, deliberate diff.
//
// 회귀 주입 (PR 본문 표): MarkDone 의 조건부 CASE 를 무조건 'done' 으로 →
// (B-queued)·(A→B) FAIL; SetAgentStatus 의 afterLaneDone 호출을 지우면
// (A-deleg)·(A-agent)·(A-user) FAIL; Finish 쪽 MarkDone 을 afterDone 을 부르게
// 바꾸면 (B-deleg)·(B-agent)·(B-user) FAIL; blocked 보존 조건을 지우면
// (B-blocked) FAIL; MarkDone 의 lane.updated 발행을 지우면 (A-deleg) FAIL.

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// laneObs is one path's observable result.
type laneObs struct {
	laneStatus     string
	laneFinished   bool
	joinFired      bool   // the delegating task's join_fired_at (FR-6.5)
	bundles        int    // join bundle system messages (new)
	reentryNotices int    // 「요청하신 작업이 끝났습니다.」 system messages (new)
	mentionInbox   int    // inbox_item type=mention (new)
	leadQueued     int    // Lead's queued tasks now
	laneFrames     int    // lane.updated frames for the lane (new)
	costFrames     int    // cost.updated frames (new)
	taskStatus     string // the child task's status
}

type obsMark struct{ event, mention int64 }

func (f *p2Fixture) mark(t *testing.T) obsMark {
	t.Helper()
	var m obsMark
	if err := f.pool.QueryRow(t.Context(), `
		SELECT COALESCE((SELECT max(id) FROM stream_event), 0),
		       (SELECT count(*) FROM inbox_item WHERE type = 'mention')`).Scan(&m.event, &m.mention); err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *p2Fixture) observe(t *testing.T, m obsMark, laneID, childTask uuid.UUID, delegTask *uuid.UUID) laneObs {
	t.Helper()
	var o laneObs
	var finished *string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, finished_at::text FROM lane WHERE id = $1`, laneID).
		Scan(&o.laneStatus, &finished); err != nil {
		t.Fatal(err)
	}
	o.laneFinished = finished != nil
	if delegTask != nil {
		o.joinFired = joinFired(t, f, *delegTask)
	}
	o.bundles = f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'system'
		AND content LIKE '%위임한 작업이 모두 끝났습니다%'`, f.sessionID)
	o.reentryNotices = f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'system'
		AND content = '요청하신 작업이 끝났습니다.'`, f.sessionID)
	o.mentionInbox = f.count(t, `SELECT count(*) FROM inbox_item WHERE type = 'mention'`) - int(m.mention)
	o.leadQueued = f.count(t, `SELECT count(*) FROM task WHERE agent_id = $1 AND status = 'queued'`, f.leadUUID)
	o.laneFrames = f.count(t, `SELECT count(*) FROM stream_event WHERE id > $1 AND type = 'lane.updated' AND payload->>'id' = $2`,
		m.event, laneID.String())
	o.costFrames = f.count(t, `SELECT count(*) FROM stream_event WHERE id > $1 AND type = 'cost.updated'`, m.event)
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM task WHERE id = $1`, childTask).Scan(&o.taskStatus); err != nil {
		t.Fatal(err)
	}
	return o
}

func (f *p2Fixture) finishCompleted(t *testing.T, taskID uuid.UUID) {
	t.Helper()
	if _, err := f.srv.Tasks.Finish(t.Context(), taskID, currentAttempt(t, f, taskID),
		contracts.Finish{Outcome: "completed", StopReason: "end_turn",
			Usage: contracts.Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.01}}); err != nil {
		t.Fatal(err)
	}
}

// delegatedChild: Lead's turn runs (so it has no queued task of its own) and
// delegates ONE child to R, whose turn is running.
func (f *p2Fixture) delegatedChild(t *testing.T) (leadTask, rTask, rLane uuid.UUID) {
	t.Helper()
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask = mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, leadTask)
	res, err := f.srv.Router.Delegate(t.Context(), leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	rTask, rLane = uuid.UUID(res.Task.Id), uuid.UUID(res.Lane.Id)
	f.runTask(t, rTask)
	return
}

// agentTriggered: Lead (running) mentions R in a message — R's lane is not a
// delegation, and its trigger's author is an agent.
func (f *p2Fixture) agentTriggered(t *testing.T) (rTask, rLane uuid.UUID) {
	t.Helper()
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, leadTask)
	author := router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTask, Attempt: 1}
	res, err := f.srv.Router.Post(t.Context(), mustUUID(t, f.sessionID), author, gen.MessageCreate{Content: router.MentionLink("R", f.rUUID) + " 확인 부탁"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Triggers) != 1 {
		t.Fatalf("triggers = %v, want R", res.Triggers)
	}
	rTask, rLane = uuid.UUID(res.Triggers[0].TaskId), uuid.UUID(res.Triggers[0].LaneId)
	f.runTask(t, rTask)
	return
}

// userTriggered: the Director mentions R directly.
func (f *p2Fixture) userTriggered(t *testing.T) (rTask, rLane uuid.UUID) {
	t.Helper()
	out := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사"})
	tr := out["triggers"].([]any)[0].(map[string]any)
	rTask, rLane = mustUUID(t, str(tr, "task_id")), mustUUID(t, str(tr, "lane_id"))
	f.runTask(t, rTask)
	return
}

func wantObs(t *testing.T, row string, got, want laneObs) {
	t.Helper()
	if got != want {
		t.Fatalf("(%s)\n got  %+v\n want %+v", row, got, want)
	}
}

// (A-deleg) status set done on a delegated child: lane done, the join fires
// once and wakes Lead with the bundle; the turn is still running.
// (A→B) the turn's own end afterwards changes nothing but the task — no
// second bundle, lane stays done.
func TestLaneDonePathAgentDelegated(t *testing.T) {
	f := newP2Fixture(t)
	leadTask, rTask, rLane := f.delegatedChild(t)
	m := f.mark(t)
	res, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", "")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TurnEndRequired {
		t.Fatal("done ends the turn")
	}
	wantObs(t, "A-deleg", f.observe(t, m, rLane, rTask, &leadTask), laneObs{
		laneStatus: "done", laneFinished: true, joinFired: true, bundles: 1,
		leadQueued: 1, laneFrames: 1, taskStatus: "running",
	})
	m = f.mark(t)
	f.finishCompleted(t, rTask)
	wantObs(t, "A→B", f.observe(t, m, rLane, rTask, &leadTask), laneObs{
		laneStatus: "done", laneFinished: true, joinFired: true, bundles: 1,
		leadQueued: 1, laneFrames: 1, costFrames: 1, taskStatus: "completed",
	})
}

// (B-deleg) the turn ends WITHOUT status set done: the lane goes done but the
// join does not fire and Lead is not woken. TODO(T-RF1-B): current behaviour,
// pinned as is — see the PR body for the FR-6.5 reading; Lead decides.
func TestLaneDonePathTurnEndDelegated(t *testing.T) {
	f := newP2Fixture(t)
	leadTask, rTask, rLane := f.delegatedChild(t)
	m := f.mark(t)
	f.finishCompleted(t, rTask)
	wantObs(t, "B-deleg", f.observe(t, m, rLane, rTask, &leadTask), laneObs{
		laneStatus: "done", laneFinished: true, joinFired: false, bundles: 0,
		leadQueued: 0, laneFrames: 1, costFrames: 1, taskStatus: "completed",
	})
}

// (A-agent) a lane an agent's mention made: done tells that agent
// (「요청하신 작업이 끝났습니다.」, Lead woken). (B-agent) the turn's end does not.
func TestLaneDonePathReentryNotice(t *testing.T) {
	t.Run("A-agent", func(t *testing.T) {
		f := newP2Fixture(t)
		rTask, rLane := f.agentTriggered(t)
		m := f.mark(t)
		if _, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", ""); err != nil {
			t.Fatal(err)
		}
		wantObs(t, "A-agent", f.observe(t, m, rLane, rTask, nil), laneObs{
			laneStatus: "done", laneFinished: true, reentryNotices: 1,
			leadQueued: 1, laneFrames: 1, taskStatus: "running",
		})
	})
	t.Run("B-agent", func(t *testing.T) {
		f := newP2Fixture(t)
		rTask, rLane := f.agentTriggered(t)
		m := f.mark(t)
		f.finishCompleted(t, rTask)
		wantObs(t, "B-agent", f.observe(t, m, rLane, rTask, nil), laneObs{
			laneStatus: "done", laneFinished: true, reentryNotices: 0,
			leadQueued: 0, laneFrames: 1, costFrames: 1, taskStatus: "completed",
		})
	})
}

// (A-user) a lane the Director made: done puts a mention item in the
// Director's inbox. (B-user) the turn's end does not.
func TestLaneDonePathUserInbox(t *testing.T) {
	t.Run("A-user", func(t *testing.T) {
		f := newP2Fixture(t)
		rTask, rLane := f.userTriggered(t)
		m := f.mark(t)
		if _, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", ""); err != nil {
			t.Fatal(err)
		}
		wantObs(t, "A-user", f.observe(t, m, rLane, rTask, nil), laneObs{
			laneStatus: "done", laneFinished: true, mentionInbox: 1,
			leadQueued: 1, laneFrames: 1, taskStatus: "running", // Lead: the room's initial assignee task
		})
	})
	t.Run("B-user", func(t *testing.T) {
		f := newP2Fixture(t)
		rTask, rLane := f.userTriggered(t)
		m := f.mark(t)
		f.finishCompleted(t, rTask)
		wantObs(t, "B-user", f.observe(t, m, rLane, rTask, nil), laneObs{
			laneStatus: "done", laneFinished: true, mentionInbox: 0,
			leadQueued: 1, laneFrames: 1, costFrames: 1, taskStatus: "completed",
		})
	})
}

// The two paths also disagree on a lane that still holds a queued task:
// (A-queued) status set done writes `done` regardless; (B-queued) the turn's
// end keeps it `queued` (the next task will run on it).
func TestLaneDonePathQueuedTaskOnLane(t *testing.T) {
	setup := func(t *testing.T) (*p2Fixture, uuid.UUID, uuid.UUID) {
		f := newP2Fixture(t)
		rTask, rLane := f.userTriggered(t)
		// A second Director message while R's turn runs: a new queued task on
		// the same (running) lane.
		out := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 하나 더"})
		if got := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "lane_id")); got != rLane {
			t.Fatalf("second message went to lane %s, want %s", got, rLane)
		}
		return f, rTask, rLane
	}
	t.Run("A-queued", func(t *testing.T) {
		f, rTask, rLane := setup(t)
		m := f.mark(t)
		if _, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "done", ""); err != nil {
			t.Fatal(err)
		}
		o := f.observe(t, m, rLane, rTask, nil)
		if o.laneStatus != "done" {
			t.Fatalf("(A-queued) lane = %s, want done — status set done writes done unconditionally today", o.laneStatus)
		}
	})
	t.Run("B-queued", func(t *testing.T) {
		f, rTask, rLane := setup(t)
		m := f.mark(t)
		f.finishCompleted(t, rTask)
		o := f.observe(t, m, rLane, rTask, nil)
		if o.laneStatus != "queued" {
			t.Fatalf("(B-queued) lane = %s, want queued — another queued task keeps the lane queued", o.laneStatus)
		}
	})
}

// (B-blocked) a lane the agent put in `blocked` keeps it when the turn ends
// (FR-6.2.1 — the question is still open).
func TestLaneDonePathTurnEndKeepsBlocked(t *testing.T) {
	f := newP2Fixture(t)
	leadTask, rTask, rLane := f.delegatedChild(t)
	if _, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, 1, "blocked", "범위?"); err != nil {
		t.Fatal(err)
	}
	f.finishCompleted(t, rTask)
	o := f.observe(t, f.mark(t), rLane, rTask, &leadTask)
	if o.laneStatus != "blocked" {
		t.Fatalf("(B-blocked) lane = %s, want blocked", o.laneStatus)
	}
	// `blocked` counts as ended for the join (FR-6.2.1) but does not itself
	// ask whether the group is complete — only `done` does. With the only
	// child blocked the join has not fired, and the turn's end does not fire
	// it either. Pinned as is (observed, not changed by T-RF1).
	if o.joinFired || o.bundles != 0 {
		t.Fatalf("(B-blocked) join fired=%v bundles=%d, want not fired", o.joinFired, o.bundles)
	}
}
