package httpapi

// #396 review NN1 · NN3: a lane-end follow-up that is lost after the finish
// committed (the hook errored, or the process died before it ran) leaves a
// group whose children have all ended with join_fired_at NULL. The scheduler
// sweep's RecoverJoins fires that join — once, whoever else is racing it —
// and every loss is countable in task_event (lane_end.*).
//
// 회귀 주입 (PR 본문 표): ExpireStale 가 RecoverJoins 를 부르지 않게 → (lost-hook)
// (process-died) FAIL; recoverJoin 의 join_fired_at 재확인 제거 + maybeFireJoin
// 의 fired 검사 제거 → (concurrent-sweeps) FAIL; 유예(grace)를 0 으로 → (grace)
// FAIL; lookback 제거 → (old-group) FAIL; blocked 자식 제외 제거 → (blocked) FAIL;
// NoteLaneEndFailed 호출 제거 → (lost-hook) 의 followup_failed 행 FAIL;
// join_recovered 기록 제거 → (lost-hook) FAIL.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

func (f *p2Fixture) laneEndRows(t *testing.T, ref string) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM task_event WHERE class = 'runtime' AND object_ref #>> '{}' = $1`, ref)
}

// sweep runs the scheduler sweep at the fixture clock + d.
func (f *p2Fixture) sweep(t *testing.T, d time.Duration) {
	t.Helper()
	f.fake.Advance(d)
	if _, err := f.srv.Tasks.ExpireStale(t.Context(), f.fake.Now()); err != nil {
		t.Fatal(err)
	}
}

// lostHook makes the next lane-end follow-up fail, as a crash after the
// finish's commit would lose it, and restores the real hook afterwards.
func (f *p2Fixture) lostHook(t *testing.T) (restore func()) {
	t.Helper()
	real := f.srv.Tasks.LaneEnded
	f.srv.Tasks.LaneEnded = func(context.Context, tasks.LaneEnd) error { return errors.New("injected: follow-up lost") }
	return func() { f.srv.Tasks.LaneEnded = real }
}

func TestJoinRecovery(t *testing.T) {
	// (lost-hook) the only child ends by its turn; the hook errors. The finish
	// stands, the join does not fire, a followup_failed row counts it. The
	// sweep inside the grace leaves it; after the grace it fires ONE bundle,
	// wakes Lead once and writes join_recovered. More sweeps add nothing.
	t.Run("lost-hook", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, rTask, _ := f.delegatedChild(t)
		restore := f.lostHook(t)
		f.finishCompleted(t, rTask)
		restore()
		if joinFired(t, f, leadTask) || f.bundleCount(t) != 0 {
			t.Fatal("setup: the lost hook must leave the join unfired")
		}
		if n := f.laneEndRows(t, tasks.LaneEndFollowupFailed); n != 1 {
			t.Fatalf("followup_failed rows = %d, want 1", n)
		}
		f.sweep(t, 5*time.Second)
		if joinFired(t, f, leadTask) {
			t.Fatal("the sweep fired inside the grace — it must leave the lane end's own follow-up time to run")
		}
		f.sweep(t, time.Minute)
		if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
			t.Fatalf("after the sweep: fired=%v bundles=%d leadQueued=%d, want true/1/1",
				joinFired(t, f, leadTask), f.bundleCount(t), f.leadQueued(t))
		}
		if n := f.laneEndRows(t, tasks.LaneEndJoinRecovered); n != 1 {
			t.Fatalf("join_recovered rows = %d, want 1", n)
		}
		// Lead's own first turn is still `running` in this fixture; keep it
		// inside the heartbeat window, or the sweep's (unrelated) heartbeat
		// rule requeues it and the count below measures that instead.
		f.keepAlive(t, leadTask)
		f.sweep(t, time.Minute)
		f.sweep(t, time.Minute)
		if f.bundleCount(t) != 1 || f.leadQueued(t) != 1 || f.laneEndRows(t, tasks.LaneEndJoinRecovered) != 1 {
			t.Fatalf("repeat sweeps: bundles=%d leadQueued=%d recovered=%d, want 1/1/1",
				f.bundleCount(t), f.leadQueued(t), f.laneEndRows(t, tasks.LaneEndJoinRecovered))
		}
	})

	// (process-died) the hook never runs at all — no followup_failed row
	// exists to say so — and the sweep still finds the group by its state.
	t.Run("process-died", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, c1, c2 := f.twoChildren(t)
		f.finishCompleted(t, c1)
		real := f.srv.Tasks.LaneEnded
		f.srv.Tasks.LaneEnded = nil // the process died between commit and hook
		f.finishCompleted(t, c2)
		f.srv.Tasks.LaneEnded = real
		if joinFired(t, f, leadTask) || f.laneEndRows(t, tasks.LaneEndFollowupFailed) != 0 {
			t.Fatal("setup: nothing ran after the commit")
		}
		f.sweep(t, time.Minute)
		if !joinFired(t, f, leadTask) || f.bundleCount(t) != 1 || f.leadQueued(t) != 1 {
			t.Fatalf("fired=%v bundles=%d leadQueued=%d, want true/1/1",
				joinFired(t, f, leadTask), f.bundleCount(t), f.leadQueued(t))
		}
	})

	// (failed-child) the lost follow-up was a cancel's: FR-6.5 「done 또는
	// failed」 holds for the recovery too, and the bundle carries the reason.
	t.Run("failed-child", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, c1, c2 := f.twoChildren(t)
		f.finishCompleted(t, c1)
		restore := f.lostHook(t)
		if _, err := f.srv.Tasks.Finish(t.Context(), c2, currentAttempt(t, f, c2),
			contracts.Finish{Outcome: "cancelled", StopReason: "cancelled"}); err != nil {
			t.Fatal(err)
		}
		restore()
		if joinFired(t, f, leadTask) {
			t.Fatal("setup: the lost hook must leave the join unfired")
		}
		f.sweep(t, time.Minute)
		f.wantFailedJoin(t, leadTask, f.childLane(t, c2), "cancelled")
	})

	// (concurrent-sweeps) two sweeps (two servers) and the late hook all race
	// the same group: one bundle.
	t.Run("concurrent-sweeps", func(t *testing.T) {
		for i := 0; i < 6; i++ {
			f := newP2Fixture(t)
			leadTask, rTask, rLane := f.delegatedChild(t)
			restore := f.lostHook(t)
			f.finishCompleted(t, rTask)
			restore()
			f.fake.Advance(time.Minute)
			now := f.fake.Now()
			var wg sync.WaitGroup
			errs := make(chan error, 3)
			var mu sync.Mutex
			swept := 0
			for k := 0; k < 2; k++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					n, err := f.srv.Router.RecoverJoins(t.Context(), now)
					mu.Lock()
					swept += n
					mu.Unlock()
					errs <- err
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- f.srv.Router.AfterLaneEnded(t.Context(), tasks.LaneEnd{LaneID: rLane, TaskID: rTask, Attempt: 1, Status: "done"})
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
			// #396 re-review NN1·NN4: the sweeps' returned counts are exact —
			// each is maybeFireJoin's own `fired` — so their sum is the number
			// of join_recovered rows (1 when a sweep won, 0 when the hook did).
			// 회귀 주입: recoverJoin 이 fired 대신 「join_fired_at 이 찼나」로
			// 판단하면 합이 2 가 되어 FAIL.
			rows := f.laneEndRows(t, tasks.LaneEndJoinRecovered)
			if rows > 1 || swept != rows {
				t.Fatalf("run %d: sweeps returned %d, join_recovered rows = %d — want equal and ≤ 1", i, swept, rows)
			}
		}
	})

	// (sibling-running) a group with a child still running is not complete:
	// the sweep does nothing, however long it waits.
	t.Run("sibling-running", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, c1, c2 := f.twoChildren(t)
		restore := f.lostHook(t)
		f.finishCompleted(t, c1)
		restore()
		f.keepAlive(t, c2)
		f.sweep(t, 2*time.Minute)
		if joinFired(t, f, leadTask) || f.bundleCount(t) != 0 {
			t.Fatal("the sweep fired a join with a child still running")
		}
	})

	// (blocked) a group whose last child is `blocked` is not the sweep's: the
	// delegator was woken with the question (FR-6.2.1); the group completes by
	// the answer. Firing it here would be new behaviour, not recovery.
	t.Run("blocked", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, rTask, _ := f.delegatedChild(t)
		if _, err := f.setStatus(t.Context(), rTask, 1, "blocked", "범위?"); err != nil {
			t.Fatal(err)
		}
		f.finishCompleted(t, rTask)
		f.sweep(t, 2*time.Minute)
		if joinFired(t, f, leadTask) || f.bundleCount(t) != 0 {
			t.Fatal("the sweep fired a join for a blocked child")
		}
	})

	// (closed-mission) #396 re-review NN5: a group of a mission already
	// completed or cancelled is not woken — the mission is over.
	// 회귀 주입: RecoverJoins 쿼리의 닫힌 미션 제외 줄을 지우면 FAIL.
	t.Run("closed-mission", func(t *testing.T) {
		for _, st := range []string{"completed", "cancelled"} {
			f := newP2Fixture(t)
			leadTask, rTask, _ := f.delegatedChild(t)
			restore := f.lostHook(t)
			f.finishCompleted(t, rTask)
			restore()
			res, err := f.pool.Exec(t.Context(), `UPDATE work SET status = $2 WHERE id = (SELECT work_id FROM task WHERE id = $1)`, leadTask, st)
			if err != nil {
				t.Fatal(err)
			}
			if res.RowsAffected() != 1 {
				t.Skip("fixture has no mission")
			}
			f.sweep(t, time.Minute)
			if joinFired(t, f, leadTask) || f.bundleCount(t) != 0 {
				t.Fatalf("%s mission: the sweep fired a join", st)
			}
		}
	})

	// (old-group) a group that ended before the lookback — the old code left
	// such groups on purpose (T-RF1-B) — is not woken on deploy.
	t.Run("old-group", func(t *testing.T) {
		f := newP2Fixture(t)
		leadTask, rTask, rLane := f.delegatedChild(t)
		restore := f.lostHook(t)
		f.finishCompleted(t, rTask)
		restore()
		if _, err := f.pool.Exec(t.Context(), `UPDATE lane SET finished_at = finished_at - interval '3 hours' WHERE id = $1`, rLane); err != nil {
			t.Fatal(err)
		}
		f.sweep(t, time.Minute)
		if joinFired(t, f, leadTask) {
			t.Fatal("the sweep woke a delegator for a group older than the lookback")
		}
	})
}

// keepAlive keeps a running task inside the sweep's heartbeat window.
func (f *p2Fixture) keepAlive(t *testing.T, taskID uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `UPDATE task SET heartbeat_at = $2 WHERE id = $1`, taskID, f.fake.Now().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
}
