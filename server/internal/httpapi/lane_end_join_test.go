package httpapi

// T-FIX-B: the join (PRD FR-6.5) follows every way a child lane ends —
// `status set done`, the turn's end, and a `failed` end (cancel · last
// failure) — and fires ONCE per group whatever the order or the overlap.
//
// 회귀 주입 (PR 본문 표): LaneEnded 배선(httpapi.NewServer)을 지우면
// (mixed)·(failed-*) FAIL; AfterLaneEnded 의 failed 가지를 비우면
// (failed-*) FAIL; maybeFireJoin 의 join_fired_at 검사를 지우면 (mixed) 의
// 반복 finish·(race) FAIL; 묶음의 실패 사유 줄을 지우면 (failed-cancel) FAIL.

import (
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// twoChildren: Lead's turn runs and delegates two children to R (two lanes of
// one group), both running.
func (f *p2Fixture) twoChildren(t *testing.T) (leadTask, c1, c2 uuid.UUID) {
	t.Helper()
	out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask = mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
	f.runTask(t, leadTask)
	var ids [2]uuid.UUID
	for i, brief := range []string{"A 조사", "B 조사"} {
		res, err := f.srv.Router.Delegate(t.Context(), leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: brief})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = uuid.UUID(res.Task.Id)
		f.runTask(t, ids[i])
	}
	return leadTask, ids[0], ids[1]
}

func (f *p2Fixture) bundleCount(t *testing.T) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'system'
		AND content LIKE '%위임한 작업이 모두 끝났습니다%'`, f.sessionID)
}

func (f *p2Fixture) leadQueued(t *testing.T) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM task WHERE agent_id = $1 AND status = 'queued'`, f.leadUUID)
}

// (mixed) one child says `status set done` and ends its turn; the other only
// ends its turn. The join waits for the second and fires once; a repeated
// finish (the daemon re-sending) adds nothing.
func TestLaneEndJoinMixed(t *testing.T) {
	f := newP2Fixture(t)
	leadTask, c1, c2 := f.twoChildren(t)
	if _, err := f.srv.Router.SetAgentStatus(t.Context(), c1, 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	f.finishCompleted(t, c1)
	if joinFired(t, f, leadTask) || f.bundleCount(t) != 0 {
		t.Fatal("join fired with a sibling still running")
	}
	f.finishCompleted(t, c2)
	if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
		t.Fatalf("after the last turn end: fired=%v bundles=%d leadQueued=%d, want true/1/1",
			joinFired(t, f, leadTask), f.bundleCount(t), f.leadQueued(t))
	}
	f.finishCompleted(t, c2) // a repeat finish of the same attempt
	if f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
		t.Fatalf("repeat finish: bundles=%d leadQueued=%d, want 1/1", f.bundleCount(t), f.leadQueued(t))
	}
}

// (failed-cancel) one child done, the other cancelled by the Director while
// running (the daemon's `cancelled` finish lands it): FR-6.5 「종료 상태(done
// 또는 failed)」 — the join fires once, and the bundle carries the reason.
// (failed-queued) the same with the child cancelled before it ran (the cancel
// is immediate, no finish comes).
// (failed-final) the child's last attempt fails.
func TestLaneEndJoinFailedSibling(t *testing.T) {
	t.Run("failed-cancel", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, c1, c2 := f.twoChildren(t)
		f.finishCompleted(t, c1)
		lane2 := f.childLane(t, c2)
		if _, immediate, err := f.srv.Tasks.CancelLane(t.Context(), lane2, uuid.Nil); err != nil || immediate {
			t.Fatalf("cancel running child: immediate=%v err=%v", immediate, err)
		}
		if joinFired(t, f, leadTask) {
			t.Fatal("join fired before the cancelled child's finish")
		}
		if _, err := f.srv.Tasks.Finish(t.Context(), c2, currentAttempt(t, f, c2),
			contracts.Finish{Outcome: "cancelled", StopReason: "cancelled"}); err != nil {
			t.Fatal(err)
		}
		f.wantFailedJoin(t, leadTask, lane2, "cancelled")
	})
	t.Run("failed-queued", func(t *testing.T) {
		f := newP2Fixture(t)
		out := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
		leadTask := mustUUID(t, str(out["triggers"].([]any)[0].(map[string]any), "task_id"))
		f.runTask(t, leadTask)
		r1, err := f.srv.Router.Delegate(t.Context(), leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A"})
		if err != nil {
			t.Fatal(err)
		}
		r2, err := f.srv.Router.Delegate(t.Context(), leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "B"})
		if err != nil {
			t.Fatal(err)
		}
		c1 := uuid.UUID(r1.Task.Id)
		f.runTask(t, c1)
		f.finishCompleted(t, c1)
		lane2 := uuid.UUID(r2.Lane.Id)
		if _, immediate, err := f.srv.Tasks.CancelLane(t.Context(), lane2, uuid.Nil); err != nil || !immediate {
			t.Fatalf("cancel queued child: immediate=%v err=%v", immediate, err)
		}
		f.wantFailedJoin(t, leadTask, lane2, "cancelled")
	})
	t.Run("failed-final", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, c1, c2 := f.twoChildren(t)
		f.finishCompleted(t, c1)
		if _, err := f.pool.Exec(t.Context(), `UPDATE task SET max_attempts = attempt WHERE id = $1`, c2); err != nil {
			t.Fatal(err)
		}
		if _, err := f.srv.Tasks.Finish(t.Context(), c2, currentAttempt(t, f, c2),
			contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther}); err != nil {
			t.Fatal(err)
		}
		f.wantFailedJoin(t, leadTask, f.childLane(t, c2), string(contracts.FailOther))
	})
}

// (queued-no-join) a child whose turn ends while another task waits on its
// lane is not ended: no join.
func TestLaneEndJoinQueuedChildWaits(t *testing.T) {
	f := newP2Fixture(t)
	leadTask, c1, c2 := f.twoChildren(t)
	f.finishCompleted(t, c1)
	lane2 := f.childLane(t, c2)
	// A second task queued on child 2's lane (a thread reply would do this).
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, status, created_at, updated_at)
		SELECT lane_id, session_id, agent_id, profile_id, trigger_message_id, 'queued', now(), now() FROM task WHERE id = $1`, c2); err != nil {
		t.Fatal(err)
	}
	f.finishCompleted(t, c2)
	var st string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM lane WHERE id = $1`, lane2).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != "queued" || joinFired(t, f, leadTask) || f.bundleCount(t) != 0 {
		t.Fatalf("lane=%s fired=%v bundles=%d, want queued/false/0", st, joinFired(t, f, leadTask), f.bundleCount(t))
	}
}

// (race) `status set done` and the turn's end of the same attempt arrive
// together (the CLI call and the daemon's finish overlap). Exactly one of
// them moves the lane into done, so the join fires once.
func TestLaneEndJoinRace(t *testing.T) {
	for i := 0; i < 8; i++ {
		f := newP2Fixture(t)
		leadTask, rTask, _ := f.delegatedChild(t)
		attempt := currentAttempt(t, f, rTask)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := f.srv.Router.SetAgentStatus(t.Context(), rTask, attempt, "done", "")
			errs <- err
		}()
		go func() {
			defer wg.Done()
			_, err := f.srv.Tasks.Finish(t.Context(), rTask, attempt,
				contracts.Finish{Outcome: "completed", StopReason: "end_turn"})
			errs <- err
		}()
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
		}
		if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
			t.Fatalf("run %d: fired=%v bundles=%d leadQueued=%d, want true/1/1",
				i, joinFired(t, f, leadTask), f.bundleCount(t), f.leadQueued(t))
		}
	}
}

// (race-siblings) the two children's turns end at the same moment: each
// finish commits, then each LaneEnded runs and each can see the whole group
// ended. join_fired_at (under the delegating task's lock) lets one bundle out.
func TestLaneEndJoinSiblingsRace(t *testing.T) {
	for i := 0; i < 8; i++ {
		f := newP2Fixture(t)
		leadTask, c1, c2 := f.twoChildren(t)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, c := range []uuid.UUID{c1, c2} {
			wg.Add(1)
			go func(c uuid.UUID) {
				defer wg.Done()
				_, err := f.srv.Tasks.Finish(t.Context(), c, currentAttempt(t, f, c),
					contracts.Finish{Outcome: "completed", StopReason: "end_turn"})
				errs <- err
			}(c)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
		}
		if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
			t.Fatalf("run %d: fired=%v bundles=%d leadQueued=%d, want true/1/1",
				i, joinFired(t, f, leadTask), f.bundleCount(t), f.leadQueued(t))
		}
		// The losing order, made deterministic: both hooks run after both
		// commits and each sees the group ended — the second must add nothing.
		for _, c := range []uuid.UUID{c1, c2} {
			if err := f.srv.Router.AfterLaneEnded(t.Context(), tasks.LaneEnd{LaneID: f.childLane(t, c), TaskID: c, Status: "done"}); err != nil {
				t.Fatal(err)
			}
		}
		if f.bundleCount(t) != 1 {
			t.Fatalf("run %d: a late second hook fired another bundle (%d)", i, f.bundleCount(t))
		}
	}
}

func (f *p2Fixture) childLane(t *testing.T, taskID uuid.UUID) uuid.UUID {
	t.Helper()
	var l uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id FROM task WHERE id = $1`, taskID).Scan(&l); err != nil {
		t.Fatal(err)
	}
	return l
}

func (f *p2Fixture) wantFailedJoin(t *testing.T, leadTask, lane uuid.UUID, reason string) {
	t.Helper()
	var st string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM lane WHERE id = $1`, lane).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st != "failed" {
		t.Fatalf("lane = %s, want failed", st)
	}
	if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
		t.Fatalf("fired=%v bundles=%d leadQueued=%d, want true/1/1",
			joinFired(t, f, leadTask), f.bundleCount(t), f.leadQueued(t))
	}
	var body string
	if err := f.pool.QueryRow(t.Context(), `SELECT content FROM message WHERE session_id = $1 AND author_type = 'system'
		AND content LIKE '%위임한 작업이 모두 끝났습니다%'`, f.sessionID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, ": failed — 사유: "+reason) {
		t.Fatalf("bundle does not carry the failed child's reason %q:\n%s", reason, body)
	}
	// A failed child is not a finished piece of work: nobody is told 「요청하신
	// 작업이 끝났습니다」 for it.
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND content = '요청하신 작업이 끝났습니다.'`, f.sessionID); n != 0 {
		t.Fatalf("re-entry notices = %d for a failed lane, want 0", n)
	}
}
