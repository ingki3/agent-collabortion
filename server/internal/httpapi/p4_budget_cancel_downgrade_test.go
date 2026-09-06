package httpapi

import (
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
)

// S-50, the other direction (#151 review NN1).
//
// PR #151 stopped the SERVER from promoting a budget pause into a cancellation:
// `nonBudgetCancelRequested` excludes the pause's own `cancel` command, so a
// daemon reporting `paused_budget` is no longer rewritten as `cancelled`.
//
// It left the mirror image open. §8.2.2 makes a budget pause stop the turn
// through the ordinary `cancel` command, and a daemon that carries that out and
// reports `outcome: "cancelled"` — a perfectly honest report of what it did —
// arrives ALREADY saying cancelled. There is nothing to promote, so the guard
// never runs, `cancelLocked` executes on a row that is already
// `paused(budget)`, and the same 0006 CHECK breaks the same transaction. The
// attempt row and `lane.runtime_session_ref` go with it, and the Director's
// approved resume becomes a cold start (E9-02 'resume 우선').
//
// The downgrade is deliberately narrow: ONLY when every cancel command on the
// attempt is a budget one. A director · kill_switch · loop · session_paused
// cancel is somebody else asking the turn to stop, and E10-04 is unchanged —
// TestP4DirectorCancelStillCancels below is the negative control.
func TestP4BudgetOnlyCancelledFinishIsDowngradedToPausedBudget(t *testing.T) {
	f := newG4Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	taskID := f.runningTask(t, "R", f.rUUID)
	f.overrunTaskBudget(t, taskID, 2)

	if st, reason := f.pausedTask(t, taskID); st != "paused" || reason != "budget" {
		t.Fatalf("premise: task = %s(%s), want paused(budget)", st, reason)
	}
	var budgetCancels, otherCancels int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FILTER (WHERE payload->>'reason' = 'budget'),
		       count(*) FILTER (WHERE COALESCE(payload->>'reason', '') <> 'budget')
		FROM daemon_command WHERE task_id = $1 AND type = 'cancel'`, taskID).
		Scan(&budgetCancels, &otherCancels); err != nil {
		t.Fatal(err)
	}
	if budgetCancels != 1 || otherCancels != 0 {
		t.Fatalf("premise: cancels = %d budget / %d other, want 1/0", budgetCancels, otherCancels)
	}

	// The daemon reports what it actually did: it cancelled the turn.
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "cancelled", StopReason: "budget",
		RuntimeSessionRef: &pausedRef,
		Usage:             contracts.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 2},
	})

	if st, reason := f.pausedTask(t, taskID); st != "paused" || reason != "budget" {
		t.Fatalf("task = %s(%s), want paused(budget) — the only cancel on this attempt was the "+
			"budget pause's own, so `cancelled` is the daemon describing the mechanism, not a "+
			"decision anybody took about the work (E9-01)", st, reason)
	}
	outcome, _, finished, _ := f.attemptRow(t, taskID, 1)
	if outcome != "paused_budget" {
		t.Fatalf("task_attempt.outcome = %q, want paused_budget", outcome)
	}
	if finished == nil {
		t.Fatal("task_attempt.finished_at is NULL — the row rolled back with the transaction")
	}
	// The resume resource is the reason any of this matters.
	var storedRef *string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT l.runtime_session_ref->>'session_id' FROM lane l
		JOIN task t ON t.lane_id = l.id WHERE t.id = $1`, taskID).Scan(&storedRef); err != nil {
		t.Fatal(err)
	}
	if storedRef == nil || *storedRef != pausedRef.SessionID {
		t.Fatalf("lane.runtime_session_ref = %v, want %q — without it the approved resume is a "+
			"cold start (E9-02)", storedRef, pausedRef.SessionID)
	}
}

// TestP4DirectorCancelStillCancels is E10-04, unchanged. A cancel somebody
// asked for stays a cancellation even when a budget cancel is also outstanding
// — the downgrade only fires when the cancel set is ENTIRELY budget.
func TestP4DirectorCancelStillCancels(t *testing.T) {
	f := newG4Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	taskID := f.runningTask(t, "R", f.rUUID)
	f.overrunTaskBudget(t, taskID, 2)

	// A person presses "중단" on top of the budget pause.
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO daemon_command (runtime_id, type, payload, task_id, attempt)
		SELECT t.runtime_id, 'cancel', $2::jsonb, t.id, t.attempt FROM task t WHERE t.id = $1`,
		taskID, `{"type":"cancel","reason":"director"}`); err != nil {
		t.Fatal(err)
	}

	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID.String()+"/attempts/1/finish", contracts.Finish{
		Outcome: "cancelled", StopReason: "director",
		Usage: contracts.Usage{InputTokens: 10, OutputTokens: 10, CostUSD: 2},
	})

	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM task WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("task = %q, want cancelled — a director cancel is a decision about the work and "+
			"E10-04 is unchanged by the S-50 downgrade", status)
	}
}
