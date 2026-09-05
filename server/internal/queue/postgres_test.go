package queue

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

var t0 = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func newQueue(t *testing.T) (*Postgres, *clock.Fake, testdb.Seed) {
	pool := testdb.New(t)
	c := clock.NewFake(t0)
	hub := realtime.New(pool, c)
	tsk := tasks.New(pool, c, tokens.New(c), hub)
	q := NewPostgres(pool, c, tsk, NewNotifier())
	return q, c, testdb.Plant(t, pool, t0)
}

func status(t *testing.T, q *Postgres, taskID uuid.UUID) (string, int, *uuid.UUID) {
	t.Helper()
	var st string
	var attempt int
	var rt *uuid.UUID
	if err := q.DB.QueryRow(context.Background(), `SELECT status, attempt, runtime_id FROM task WHERE id = $1`, taskID).Scan(&st, &attempt, &rt); err != nil {
		t.Fatal(err)
	}
	return st, attempt, rt
}

// E11-10: a `none` session with no runtime is fixed to the first claimer, and
// E11-09: a second runtime then gets nothing from that session.
func TestClaimFixesRuntimeAndRejectsOthers(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	other := testdb.AddRuntime(t, q.DB, s.WorkspaceID, "mac-2", t0)
	task1 := testdb.AddTask(t, q.DB, s, s.SessionID, t0)

	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 4, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].Task.ID != task1.String() || bundles[0].TaskToken == "" || bundles[0].Task.AgentName != "Lead" {
		t.Fatalf("bundle = %+v", bundles)
	}
	var fixed *uuid.UUID
	if err := q.DB.QueryRow(ctx, `SELECT runtime_id FROM session WHERE id = $1`, s.SessionID).Scan(&fixed); err != nil {
		t.Fatal(err)
	}
	if fixed == nil || *fixed != s.RuntimeID {
		t.Fatalf("session runtime not fixed: %v", fixed)
	}
	st, _, rt := status(t, q, task1)
	if st != "dispatched" || rt == nil || *rt != s.RuntimeID {
		t.Fatalf("task = %s %v", st, rt)
	}

	// A second task in the (now fixed) session: another runtime must not get it.
	task2 := testdb.AddTask(t, q.DB, s, s.SessionID, t0.Add(time.Second))
	bundles, err = q.Claim(ctx, other.String(), 4, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 0 {
		t.Fatalf("other runtime claimed %d tasks from a fixed session (E11-09)", len(bundles))
	}
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 4, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].Task.ID != task2.String() {
		t.Fatalf("fixed runtime should claim task2, got %+v", bundles)
	}
}

// E5-04 / not_before / one in-flight task per lane.
func TestClaimExclusions(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()

	paused := testdb.AddSession(t, q.DB, s, &s.RuntimeID, t0)
	if _, err := q.DB.Exec(ctx, `UPDATE session SET status = 'paused', paused_reason = 'director' WHERE id = $1`, paused); err != nil {
		t.Fatal(err)
	}
	testdb.AddTask(t, q.DB, s, paused, t0)

	later := testdb.AddTask(t, q.DB, s, s.SessionID, t0)
	if _, err := q.DB.Exec(ctx, `UPDATE task SET not_before = $2 WHERE id = $1`, later, t0.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}

	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 10, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 0 {
		t.Fatalf("paused session / future not_before must not dispatch, got %d", len(bundles))
	}
	c.Advance(10 * time.Minute)
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 10, c.Now())
	if err != nil || len(bundles) != 1 || bundles[0].Task.ID != later.String() {
		t.Fatalf("not_before passed: bundles=%+v err=%v", bundles, err)
	}

	// Same lane: a queued task behind a dispatched one waits.
	var laneID uuid.UUID
	_ = q.DB.QueryRow(ctx, `SELECT lane_id FROM task WHERE id = $1`, later).Scan(&laneID)
	if _, err := q.DB.Exec(ctx, `INSERT INTO task (lane_id, session_id, agent_id, profile_id, created_at, updated_at) SELECT lane_id, session_id, agent_id, profile_id, $2, $2 FROM task WHERE id = $1`, later, c.Now()); err != nil {
		t.Fatal(err)
	}
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 10, c.Now())
	if err != nil || len(bundles) != 0 {
		t.Fatalf("second task on a running lane must wait, got %d (%v)", len(bundles), err)
	}
}

// ClaimWait wakes on Notify (E17-01 ≤ 2s path).
func TestClaimWaitWakesOnNotify(t *testing.T) {
	q, _, s := newQueue(t)
	ctx := context.Background()
	go func() {
		time.Sleep(200 * time.Millisecond)
		testdb.AddTask(t, q.DB, s, s.SessionID, t0)
		q.Notifier.Notify()
	}()
	start := time.Now()
	bundles, err := q.ClaimWait(ctx, s.RuntimeID.String(), 1, 10*time.Second)
	if err != nil || len(bundles) != 1 {
		t.Fatalf("bundles=%d err=%v", len(bundles), err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("claim took %v, want < 3s", time.Since(start))
	}
}
