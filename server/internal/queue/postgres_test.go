package queue

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
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

// A runtime paired to another workspace is never a candidate for a `none`
// session with runtime_id NULL (FR-2.1 M10, FR-1.9): it must get nothing and
// must not pin the session. The session's own workspace runtime still claims
// and pins it (E11-10).
func TestClaimScopedToSessionWorkspace(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	wsB := testdb.AddWorkspace(t, q.DB, "ws-b", t0)
	rtB := testdb.AddRuntime(t, q.DB, wsB, "mac-b", t0)
	task1 := testdb.AddTask(t, q.DB, s, s.SessionID, t0)

	bundles, err := q.Claim(ctx, rtB.String(), 4, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 0 {
		t.Fatalf("workspace-B runtime claimed %d tasks from a workspace-A session", len(bundles))
	}
	var fixed *uuid.UUID
	if err := q.DB.QueryRow(ctx, `SELECT runtime_id FROM session WHERE id = $1`, s.SessionID).Scan(&fixed); err != nil {
		t.Fatal(err)
	}
	if fixed != nil {
		t.Fatalf("session pinned to %v by a foreign runtime", *fixed)
	}
	if st, _, _ := status(t, q, task1); st != "queued" {
		t.Fatalf("task status = %s, want queued", st)
	}

	// Unknown runtime id: nothing, no error.
	bundles, err = q.Claim(ctx, uuid.New().String(), 4, c.Now())
	if err != nil || len(bundles) != 0 {
		t.Fatalf("unknown runtime: bundles=%d err=%v", len(bundles), err)
	}

	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 4, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(bundles) != 1 || bundles[0].Task.ID != task1.String() {
		t.Fatalf("same-workspace runtime should claim task1, got %+v", bundles)
	}
	if err := q.DB.QueryRow(ctx, `SELECT runtime_id FROM session WHERE id = $1`, s.SessionID).Scan(&fixed); err != nil {
		t.Fatal(err)
	}
	if fixed == nil || *fixed != s.RuntimeID {
		t.Fatalf("session runtime not fixed to workspace-A runtime (E11-10): %v", fixed)
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

// finish {failed, rate_limited, not_before} re-queues the task (attempt+1) at
// not_before (daemon-protocol §4.1, G1 F3): the injected clock decides — claim
// yields nothing before not_before and the task once it has passed.
func TestFinishRateLimitedRequeuesAtNotBefore(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	id := testdb.AddTask(t, q.DB, s, s.SessionID, t0)

	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 {
		t.Fatalf("first claim = %+v %v", bundles, err)
	}
	resetsAt := t0.Add(30 * time.Minute)
	final, err := q.Tasks.Finish(ctx, id, 1, contracts.Finish{Outcome: "failed", FailureKind: contracts.FailRateLimited, NotBefore: &resetsAt, StopReason: "rate_limited"})
	if err != nil || final != tasks.Queued {
		t.Fatalf("finish rate_limited = %s %v, want queued", final, err)
	}
	st, attempt, _ := status(t, q, id)
	var notBefore *time.Time
	var kind *string
	_ = q.DB.QueryRow(ctx, `SELECT not_before FROM task WHERE id = $1`, id).Scan(&notBefore)
	_ = q.DB.QueryRow(ctx, `SELECT failure_kind::text FROM task_attempt WHERE task_id = $1 AND attempt = 1`, id).Scan(&kind)
	if st != "queued" || attempt != 2 || notBefore == nil || !notBefore.Equal(resetsAt) || kind == nil || *kind != "rate_limited" {
		t.Fatalf("after finish: status %s attempt %d not_before %v attempt1.failure_kind %v", st, attempt, notBefore, kind)
	}

	c.Advance(29 * time.Minute)
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 0 {
		t.Fatalf("claim before not_before = %d bundles %v, want 0", len(bundles), err)
	}
	c.Advance(time.Minute)
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 || bundles[0].Task.ID != id.String() || bundles[0].Task.Attempt != 2 {
		t.Fatalf("claim at not_before = %+v %v, want attempt 2 of the task", bundles, err)
	}
}

// harness §6 resume input: the runtime_session_ref a finish stores on the lane
// rides the next claim of that lane as TaskBundle.resume, unchanged.
func TestResumeRefRidesNextClaim(t *testing.T) {
	q, c, s := newQueue(t)
	ctx := context.Background()
	id := testdb.AddTask(t, q.DB, s, s.SessionID, t0)

	bundles, err := q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 || bundles[0].Resume != nil {
		t.Fatalf("first claim = %+v %v, want one bundle without resume", bundles, err)
	}
	ref := &contracts.RuntimeSessionRef{
		RuntimeKind: contracts.RuntimeClaudeCode, AdapterVersion: "0.74.0",
		SessionID: "acp-sess-42", CWD: "/work/lane", CreatedAt: t0.Add(time.Minute),
	}
	// attempt 1 dies retryably (max_attempts 3 → requeue as attempt 2) but had a live session.
	final, err := q.Tasks.Finish(ctx, id, 1, contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther, StopReason: "crash", RuntimeSessionRef: ref})
	if err != nil || final != tasks.Queued {
		t.Fatalf("finish with ref = %s %v, want queued", final, err)
	}
	c.Advance(time.Second)
	bundles, err = q.Claim(ctx, s.RuntimeID.String(), 1, c.Now())
	if err != nil || len(bundles) != 1 || bundles[0].Task.ID != id.String() || bundles[0].Task.Attempt != 2 {
		t.Fatalf("second claim = %+v %v, want attempt 2", bundles, err)
	}
	got := bundles[0].Resume
	if got == nil || got.RuntimeKind != contracts.RuntimeClaudeCode || got.SessionID != "acp-sess-42" || got.CWD != "/work/lane" ||
		got.AdapterVersion != "0.74.0" || !got.CreatedAt.Equal(ref.CreatedAt) {
		t.Fatalf("bundle.resume = %+v, want %+v", got, ref)
	}
	if !bundles[0].Workdir.Reuse {
		t.Fatalf("attempt 2 must reuse the workdir")
	}
}

// FR-6.3: the four concurrency layers are guards on the claim query, so a
// second task on the same lane, a second lane past max_parallel_lanes, and an
// agent over max_concurrent_tasks all stay queued rather than dispatching.
//
// A missing workspace_settings row must NOT stop dispatch — the layers fall
// back to their defaults.
func TestClaimRespectsConcurrencyLimits(t *testing.T) {
	q, c, seed := newQueue(t)
	pool := q.DB
	ctx := context.Background()

	// max_parallel_lanes = 1: the session may only have one lane in flight.
	if _, err := pool.Exec(ctx, `UPDATE session SET limits = '{"max_parallel_lanes": 1}' WHERE id = $1`, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	a := testdb.AddTask(t, pool, seed, seed.SessionID, t0)
	b := testdb.AddTask(t, pool, seed, seed.SessionID, t0)
	// Two different lanes, or the per-lane guard alone would explain the result.
	var lane2 uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'queued', $4, $4) RETURNING id`,
		seed.SessionID, seed.AgentID, seed.ProfileID, t0).Scan(&lane2); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE task SET lane_id = $2 WHERE id = $1`, b, lane2); err != nil {
		t.Fatal(err)
	}

	got, err := q.Claim(ctx, seed.RuntimeID.String(), 10, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("claimed %d, want 1 — max_parallel_lanes is 1", len(got))
	}
	_ = a

	// Raising the session limit is not enough while the agent's own cap binds.
	if _, err := pool.Exec(ctx, `UPDATE session SET limits = '{"max_parallel_lanes": 5}' WHERE id = $1`, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE agent SET max_concurrent_tasks = 1 WHERE id = $1`, seed.AgentID); err != nil {
		t.Fatal(err)
	}
	got, err = q.Claim(ctx, seed.RuntimeID.String(), 10, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("claimed %d, want 0 — the agent already has one task running", len(got))
	}

	// With both raised, the second lane goes out — including when the
	// workspace has no settings row at all.
	if _, err := pool.Exec(ctx, `UPDATE agent SET max_concurrent_tasks = 5 WHERE id = $1`, seed.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM workspace_settings WHERE workspace_id = $1`, seed.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	got, err = q.Claim(ctx, seed.RuntimeID.String(), 10, c.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("claimed %d, want 1 — a missing settings row must fall back to the defaults", len(got))
	}
}

// TestBundleHistoryLimitIsTheOneConstant is the other half of S-38: the bundle
// must not carry a second, private cap. Two literals is how the prompt and the
// planner's `truncated` flag came to disagree.
func TestBundleHistoryLimitIsTheOneConstant(t *testing.T) {
	if historyLimit != tasks.DefaultHistoryLimit {
		t.Fatalf("queue.historyLimit = %d, tasks.DefaultHistoryLimit = %d — one cap, one constant",
			historyLimit, tasks.DefaultHistoryLimit)
	}
}
