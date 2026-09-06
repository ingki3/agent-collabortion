package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

// S-46 — resuming a session puts back the tasks its pause parked.
//
// PRD FR-2.3 and §8.2.2 split a pause in two: a Director pause DRAINS the turn
// in flight, a budget or time pause CANCELS it. Cancelling means
// tasks.PauseSessionTasks parks every running task at `paused(budget)` with its
// lane `paused`. resumeSession set the session back to `active` and lifted only
// the lanes that still held a QUEUED task — nothing moved the parked tasks, and
// no other endpoint can: respondHitlRequest re-queues the task a request NAMES,
// and a session-scoped budget request names none (FR-7.3 s-13). The work the
// pause stopped stayed paused forever and the session dispatched nothing
// (found in T-S6; #136 lifted the lane gate of the post-turn case, a different
// row).

// pausedTask is a task's status and pause reason in one read.
func (f *p2Fixture) pausedTask(t *testing.T, taskID uuid.UUID) (string, string) {
	t.Helper()
	var status, reason string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM task WHERE id = $1`, taskID).
		Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	return status, reason
}

// overrunSession drives the SESSION budget past its limit from a running task,
// which is the pause that parks turns (E9-04). It returns that task.
func (f *p2Fixture) overrunSession(t *testing.T, agent uuid.UUID, name string, cost float64) uuid.UUID {
	t.Helper()
	_, taskID := f.agentToken(t, f.sessionID, agent, name)
	f.runTask(t, taskID)
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), taskID, contracts.Usage{CostUSD: cost}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}

// TestP3ResumeSessionRequeuesParkedTasks is the S-46 regression: pause →
// resume → the parked task is queued again and the queue hands it out.
func TestP3ResumeSessionRequeuesParkedTasks(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	// No per-task budget: the session remainder is this task's only ceiling, so
	// crossing it is a SESSION overrun (D-16) and the pause is the session's.
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	parked := f.overrunSession(t, f.rUUID, "R", 1.25)
	// A second lane that was only ever queued — it must come back too, and it
	// is what the old lane-only sweep already handled.
	_, queued := f.agentToken(t, f.sessionID, f.wUUID, "W")

	var sessionStatus, sessionReason string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`, f.sessionID).
		Scan(&sessionStatus, &sessionReason); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "paused" || sessionReason != "budget" {
		t.Fatalf("premise: session = %s(%s), want paused(budget) (E9-04)", sessionStatus, sessionReason)
	}
	if st, reason := f.pausedTask(t, parked); st != "paused" || reason != "budget" {
		t.Fatalf("premise: task = %s(%s), want paused(budget) — the pause cancels the turn (§8.2.2)", st, reason)
	}

	// The Director raises the limit and presses 재개. That IS the answer to the
	// session-scoped budget request (openapi resumeSession).
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/resume",
		map[string]any{"limits": map[string]any{"budget_usd": 10}})

	if st := f.taskStatus(t, parked); st != "queued" {
		t.Fatalf("parked task = %q after 재개, want queued — resuming a session that dispatches nothing is not a resume (FR-2.3)", st)
	}
	// The re-queue is a NEW attempt on the SAME lane: resume is tried first and
	// the workdir is kept (E9-02, FR-5.4).
	var attempt int
	var laneStatus string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT t.attempt, l.status::text FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, parked).
		Scan(&attempt, &laneStatus); err != nil {
		t.Fatal(err)
	}
	if attempt != 2 {
		t.Fatalf("attempt = %d after the resume, want 2 — `running` is not reachable from `paused`, so the only honest move is a new attempt (FR-7.1 N4)", attempt)
	}
	if laneStatus != "queued" {
		t.Fatalf("lane = %q, want queued — the claim query refuses a paused lane (C3′)", laneStatus)
	}
	got := f.claimed(t)
	if !has(got, parked) {
		t.Fatalf("the queue did not hand out the resumed task: %v", got)
	}
	if !has(got, queued) {
		t.Fatalf("the queue did not hand out the lane that was queued all along: %v", got)
	}
}

// TestP3ResumeSessionKeepsRefusedBudgetTask is E9-03's other side: a task whose
// OWN budget raise was refused stays parked, and a session pause/resume cycle
// around it must not launder that refusal into a dispatch. Only 중단 ends it.
func TestP3ResumeSessionKeepsRefusedBudgetTask(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 3}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL WHERE id = $1`, f.wUUID); err != nil {
		t.Fatal(err)
	}
	// R crosses its OWN budget_per_task: the task parks, the session stays
	// active, and the request names the task (E9-01).
	refused := f.overrunSession(t, f.rUUID, "R", 2)
	var hitlID string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'budget' AND task_id = $2`,
		f.sessionID, refused).Scan(&hitlID); err != nil {
		t.Fatalf("no task-scoped budget HITL: %v", err)
	}
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": false, "reason": "여기까지만"}, "Idempotency-Key", uuid.NewString())
	if st, reason := f.pausedTask(t, refused); st != "paused" || reason != "budget" {
		t.Fatalf("premise: refused task = %s(%s), want paused(budget) (E9-03)", st, reason)
	}

	// Now W pushes the SESSION over its own limit, which parks W's turn.
	parked := f.overrunSession(t, f.wUUID, "W", 2)
	if st, _ := f.pausedTask(t, parked); st != "paused" {
		t.Fatalf("premise: W task = %q, want paused — the session budget is gone (E9-04)", st)
	}

	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/resume",
		map[string]any{"limits": map[string]any{"budget_usd": 20}})

	if st := f.taskStatus(t, parked); st != "queued" {
		t.Fatalf("the task the session pause parked = %q, want queued", st)
	}
	if st, reason := f.pausedTask(t, refused); st != "paused" || reason != "budget" {
		t.Fatalf("the REFUSED task = %s(%s) after 재개, want paused(budget) still — resuming the session must not overturn a refusal (E9-03)", st, reason)
	}
	if got := f.claimed(t); has(got, refused) {
		t.Fatalf("the queue handed out a task whose budget raise was refused: %v", got)
	}
}
