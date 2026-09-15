package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// S-50 — the `finish` of a task the budget parked used to be a 500.
//
// The path (G6 2판 §9.5, reproduced 3/3): applyBudgetPause writes
// `paused(budget)` + `paused_detail` and queues the §8.2.2 `cancel` command →
// the daemon closes the attempt and reports `paused_budget` → tasks.Finish
// promoted every non-`completed` outcome that had a cancel command to
// `cancelled` → cancelLocked cleared `paused_reason` but not `paused_detail`
// → `task_paused_detail_check` (0006) → the whole transaction rolled back.
//
// What rolled back with it is the point. The `task_attempt` row (outcome,
// finished_at, stop_reason) and `lane.runtime_session_ref` are written by
// Finish and by nothing else, so the attempt stayed open-ended on the feed and
// the approved resume had no session to attach to — a cold start, against
// E9-02's "resume 우선". The pause itself survived only because the CHECK broke
// the transaction that was about to overwrite it.

// runningTask takes one mention through the real daemon path — post → claim →
// phase running — so the attempt belongs to the paired runtime and its finish
// can be posted over HTTP. The 500 is what the daemon saw, so the test has to
// look at a status code, not at an internal error value.
func (g *g4Fixture) runningTask(t *testing.T, mention string, agentID uuid.UUID) uuid.UUID {
	t.Helper()
	post := g.post(t, map[string]any{"content": router.MentionLink(mention, agentID) + " 시작해줘"})
	var taskID string
	for _, raw := range post["triggers"].([]any) {
		if tr := raw.(map[string]any); str(tr, "agent_id") == agentID.String() {
			taskID = str(tr, "task_id")
		}
	}
	if taskID == "" {
		t.Fatalf("no task for %s: %v", mention, post["triggers"])
	}
	g.daemon.must(200, "POST", "/v1/daemon/runtimes/"+g.runtimeID+"/claim", map[string]any{"capacity": 4, "wait_ms": 0})
	g.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/phase", map[string]any{"phase": "running", "pgid": 100})
	return mustUUID(t, taskID)
}

// overrunTaskBudget reports a MEASURED cost past the agent's per-task budget
// and runs the enforcement the heartbeat runs, in the same order.
func (g *g4Fixture) overrunTaskBudget(t *testing.T, taskID uuid.UUID, cost float64) {
	t.Helper()
	if err := g.srv.Tasks.RecordTurnUsage(t.Context(), taskID, contracts.Usage{
		InputTokens: 1000, OutputTokens: 1000, CostUSD: cost,
	}, g.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := g.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
}

// bundleFor claims for the PAIRED runtime (the fixture has two) and returns
// this task's bundle.
func (g *g4Fixture) bundleFor(t *testing.T, taskID uuid.UUID) *contracts.TaskBundle {
	t.Helper()
	bundles, err := g.srv.Queue.Claim(t.Context(), g.runtimeID, 10, g.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	for i := range bundles {
		if bundles[i].Task.ID == taskID.String() {
			return &bundles[i]
		}
	}
	return nil
}

func (g *g4Fixture) attemptRow(t *testing.T, taskID uuid.UUID, attempt int) (outcome, stopReason string, finished *time.Time, resumed *bool) {
	t.Helper()
	var out, stop *string
	if err := g.pool.QueryRow(t.Context(), `
		SELECT outcome, stop_reason, finished_at, resumed FROM task_attempt WHERE task_id = $1 AND attempt = $2`,
		taskID, attempt).Scan(&out, &stop, &finished, &resumed); err != nil {
		t.Fatal(err)
	}
	return derefString(out), derefString(stop), finished, resumed
}

var pausedRef = contracts.RuntimeSessionRef{
	RuntimeKind: contracts.RuntimeClaudeCode, SessionID: "acp-sess-paused", CWD: "/tmp/colab/lane",
}

// TestP3PausedBudgetFinishKeepsThePauseAndTheRef is the S-50 regression, end to
// end: the finish answers 200, the attempt is recorded, the resume resource the next
// attempt needs is stored, and the task is still `paused(budget)` — not
// `cancelled` (E9-01: a budget overrun is policy, not a failure).
func TestP3PausedBudgetFinishKeepsThePauseAndTheRef(t *testing.T) {
	f := newG4Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	taskID := f.runningTask(t, "R", f.rUUID)
	f.overrunTaskBudget(t, taskID, 2)

	// Premise: this is the pause that queues its own cancel (§8.2.2).
	if st, reason := f.pausedTask(t, taskID); st != "paused" || reason != "budget" {
		t.Fatalf("premise: task = %s(%s), want paused(budget) (E9-01)", st, reason)
	}
	var cmds int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM daemon_command WHERE task_id = $1 AND type = 'cancel' AND payload->>'reason' = 'budget'`,
		taskID).Scan(&cmds); err != nil {
		t.Fatal(err)
	}
	if cmds != 1 {
		t.Fatalf("premise: budget cancel commands = %d, want 1 (§4.3)", cmds)
	}
	var detail *string
	if err := f.pool.QueryRow(t.Context(), `SELECT paused_detail::text FROM task WHERE id = $1`, taskID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail == nil {
		t.Fatal("premise: paused_detail is NULL — the CHECK this defect trips needs it set (0006)")
	}

	// The daemon carries the cancel out and reports the attempt (§5, §4.4).
	// This is the call that answered 500.
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "paused_budget", StopReason: "budget",
		RuntimeSessionRef: &pausedRef,
		Usage:             contracts.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 2},
	})

	if st, reason := f.pausedTask(t, taskID); st != "paused" || reason != "budget" {
		t.Fatalf("task = %s(%s) after the finish, want paused(budget) — the cancel the pause itself "+
			"queued must not turn its own pause into a cancellation (E9-01)", st, reason)
	}
	outcome, stop, finished, _ := f.attemptRow(t, taskID, 1)
	if outcome != "paused_budget" {
		t.Fatalf("task_attempt.outcome = %q, want paused_budget — the row is the feed's record of how the attempt ended", outcome)
	}
	if finished == nil {
		t.Fatal("task_attempt.finished_at is NULL — an attempt the daemon closed reads as still running")
	}
	if stop != "budget" {
		t.Fatalf("task_attempt.stop_reason = %q, want budget", stop)
	}
	var storedRef *string
	var laneStatus string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT l.runtime_session_ref->>'session_id', l.status::text FROM lane l JOIN task t ON t.lane_id = l.id
		WHERE t.id = $1`, taskID).Scan(&storedRef, &laneStatus); err != nil {
		t.Fatal(err)
	}
	if storedRef == nil || *storedRef != pausedRef.SessionID {
		t.Fatalf("lane.runtime_session_ref session_id = %v, want %q — Finish is its only writer, so losing "+
			"this transaction makes the approved resume a cold start (E9-02)", storedRef, pausedRef.SessionID)
	}
	if laneStatus != "paused" {
		t.Fatalf("lane = %s, want paused — the claim query is what holds the gate until the Director answers (C3′)", laneStatus)
	}

	// E9-02: the raise re-queues the SAME task, and the next attempt is handed
	// the ref this finish stored — resume first, cold start only if it fails.
	var hitlID string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'budget' AND task_id = $2`,
		f.sessionID, taskID).Scan(&hitlID); err != nil {
		t.Fatalf("no task-scoped budget HITL: %v", err)
	}
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 5}, "Idempotency-Key", uuid.NewString())

	if st := f.taskStatus(t, taskID); st != "queued" {
		t.Fatalf("task = %q after the approved raise, want queued (E9-02)", st)
	}
	b := f.bundleFor(t, taskID)
	if b == nil {
		t.Fatal("the queue did not hand the resumed task back")
	}
	if b.Task.Attempt != 2 {
		t.Fatalf("bundle attempt = %d, want 2 — a resume is a NEW attempt on the same lane (FR-7.1 N4)", b.Task.Attempt)
	}
	if b.Resume == nil || b.Resume.SessionID != pausedRef.SessionID {
		t.Fatalf("bundle resume = %+v, want the stored ref %q — E9-02 says resume is tried FIRST", b.Resume, pausedRef.SessionID)
	}
}

// TestP3PausedTaskCancelsWithoutTheCheck is S-50 (b) on its own: `paused →
// cancelled` must be a 200 whatever brought the task here. The CHECK is on the
// PAIR (0006: `paused_detail IS NULL OR paused_reason IS NOT NULL`), so a
// cancel that clears only the reason breaks every parked task a person ends —
// not just the budget one.
func TestP3PausedTaskCancelsWithoutTheCheck(t *testing.T) {
	f := newG4Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	taskID := f.runningTask(t, "R", f.rUUID)
	f.overrunTaskBudget(t, taskID, 2)
	if st, reason := f.pausedTask(t, taskID); st != "paused" || reason != "budget" {
		t.Fatalf("premise: task = %s(%s), want paused(budget)", st, reason)
	}

	// 세션 취소 is the reachable route to a parked task: cancelLane refuses a
	// paused lane (openapi cancelLane, 409), while cancelSessionWork walks
	// `paused` rows straight into cancelLocked (§8.2.2).
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/cancel", map[string]any{"reason": "여기까지"})

	var status, pausedReason, detail *string
	var failureKind *string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, paused_reason::text, paused_detail::text, failure_kind::text FROM task WHERE id = $1`, taskID).
		Scan(&status, &pausedReason, &detail, &failureKind); err != nil {
		t.Fatal(err)
	}
	if derefString(status) != "cancelled" || derefString(failureKind) != "cancelled" {
		t.Fatalf("task = %v(%v), want cancelled(cancelled) (FR-3.4)", derefString(status), derefString(failureKind))
	}
	if pausedReason != nil || detail != nil {
		t.Fatalf("paused_reason = %v · paused_detail = %v after the cancel, want both NULL — 0006 checks the PAIR, "+
			"and leaving the detail behind is the 23514 that made this a 500", pausedReason, detail)
	}
}

// TestP3CancelRacedByTurnEndIsSaidSo is S-51. The cancel was accepted, the
// command was queued and the feed already says "사람이 중단함" — and then the
// turn ended by itself and the daemon reported `completed`. The task IS
// completed and the screen is right to show it; what was wrong is that the feed
// still told the reader a person had stopped it, with nothing saying the stop
// never landed (G6 2판 §9.6, measured on `51_`).
func TestP3CancelRacedByTurnEndIsSaidSo(t *testing.T) {
	f := newG4Fixture(t)
	taskID := f.runningTask(t, "R", f.rUUID)

	var laneID string
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id::text FROM task WHERE id = $1`, taskID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	f.api.must(202, "POST", f.p+"/lanes/"+laneID+"/cancel", map[string]any{})

	// The turn had already ended when the command was delivered.
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
	})

	if st := f.taskStatus(t, taskID); st != "completed" {
		t.Fatalf("task = %q, want completed — the work really finished, and a cancel that arrived after "+
			"the fact does not un-finish it (E10-04 is about a cancel that LANDS)", st)
	}
	var notes int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM task_event
		WHERE task_id = $1 AND class = 'status' AND verb = 'cancel'
		  AND object_ref = to_jsonb('cancel_raced_turn_end'::text)`, taskID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Fatalf("race notes on the feed = %d, want 1 — the feed says 사람이 중단함 and the screen says 완료; "+
			"one of the two has to explain itself (S-51)", notes)
	}
	var unconsumed int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM daemon_command WHERE task_id = $1 AND type = 'cancel' AND consumed_at IS NULL`,
		taskID).Scan(&unconsumed); err != nil {
		t.Fatal(err)
	}
	if unconsumed != 0 {
		t.Fatalf("unconsumed cancel commands = %d, want 0 — a command whose effect is settled must not "+
			"ride the next response (§4.3)", unconsumed)
	}
}

// A director cancel still decides the outcome. This is the other side of the
// S-50 exclusion: only the BUDGET pause's own cancel is discounted, and the
// four reasons that mean somebody asked the attempt to stop are unchanged
// (E10-04 — no requeue, lane failed).
func TestP3DirectorCancelStillDecidesTheOutcome(t *testing.T) {
	f := newG4Fixture(t)
	taskID := f.runningTask(t, "R", f.rUUID)
	var laneID string
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id::text FROM task WHERE id = $1`, taskID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	f.api.must(202, "POST", f.p+"/lanes/"+laneID+"/cancel", map[string]any{})

	// The daemon reports the attempt as FAILED after the cancel — §4.4 leaves
	// the mapping to the server, and E10-04 says a cancelled attempt is not
	// retried.
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "failed", FailureKind: contracts.FailOther, StopReason: "cancelled",
	})

	if st := f.taskStatus(t, taskID); st != "cancelled" {
		t.Fatalf("task = %q after a director cancel, want cancelled — a failure report that follows a "+
			"cancel IS the cancel taking effect (E10-04)", st)
	}
	var attempt int
	if err := f.pool.QueryRow(t.Context(), `SELECT attempt FROM task WHERE id = $1`, taskID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	if attempt != 1 {
		t.Fatalf("attempt = %d, want 1 — a cancelled attempt is not requeued (E10-04)", attempt)
	}
}
