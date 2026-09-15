// Golden table for the state machines (EVAL E5, 18 rows):
//
//   - task status transitions (PRD FR-7.1) — the only pure part today
//   - session-driven dispatch gating (FR-2.3)
//   - lane re-entry (FR-6.2)
//   - the derived agent status priority ladder (FR-1.3, 6 steps)
//
// Written by the Reviewer before the implementation (PLAN §10.1, P2a).
//
// Time never comes from the wall clock here: daemon-protocol.md:163 requires
// every time-dependent path to go through contracts/clock, so E5-02 and E5-03
// advance an injected fake.
package tasks

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func caseName(eval, name string) string {
	out := make([]byte, 0, len(eval))
	for i := 0; i < len(eval); i++ {
		if eval[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, eval[i])
	}
	return fmt.Sprintf("%s_%s", string(out), name)
}

// ---------------------------------------------------------------------------
// E5-01 — task transitions. CanTransition/Transition already exist (P1).
// ---------------------------------------------------------------------------

func TestTaskTransitionGolden(t *testing.T) {
	t.Run(caseName("E5-01", "waiting_human_only_exits_to_queued"), func(t *testing.T) {
		// FR-7.1 N4: a human answer re-queues the task; the daemon never
		// resumes a waiting_human task in place.
		if CanTransition(WaitingHuman, Running) {
			t.Error("waiting_human → running must be rejected (E5-01)")
		}
		if _, err := Transition(WaitingHuman, Running); err == nil {
			t.Error("Transition(waiting_human, running) must return ErrInvalidTransition")
		}
		if !CanTransition(WaitingHuman, Queued) {
			t.Error("queued is the only exit from waiting_human")
		}
		for _, to := range []Status{Dispatched, Preparing, Completed, Failed, Cancelled, Paused, Deferred} {
			if CanTransition(WaitingHuman, to) {
				t.Errorf("waiting_human → %s must be rejected — queued is the ONLY exit", to)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// What the implementation must expose for the rest of E5.
// ---------------------------------------------------------------------------

// sweepResult is one run of the stale sweep at a given clock reading.
type sweepResult struct {
	TaskStatus   Status
	FailureKind  string
	Attempt      int
	RuntimeState string // "online" | "offline"
	TokenRevoked bool
}

// sweepAt is wired by T-S2/T-S1's existing ExpireStale. Signature in the report.
var sweepAt func(initial Status, since time.Duration) sweepResult

func mustSweep(t *testing.T, initial Status, since time.Duration) sweepResult {
	t.Helper()
	if sweepAt == nil {
		t.Fatalf("unimplemented: stale sweep as a testable unit. T-S2 must wire `sweepAt` " +
			"(see /tmp/p2a-report.md 'required API')")
	}
	return sweepAt(initial, since)
}

func TestStaleSweepGolden(t *testing.T) {
	t.Run(caseName("E5-02", "dispatched_without_claim_times_out_at_5m"), func(t *testing.T) {
		r := mustSweep(t, Dispatched, 5*time.Minute)
		if r.TaskStatus != Failed {
			t.Errorf("status = %s, want failed after 5m unclaimed", r.TaskStatus)
		}
		if r.FailureKind != "timeout" {
			t.Errorf("failure_kind = %q, want timeout", r.FailureKind)
		}
	})

	t.Run(caseName("E5-03", "running_without_heartbeat_requeues_at_3m"), func(t *testing.T) {
		r := mustSweep(t, Running, 3*time.Minute)
		if r.TaskStatus != Queued {
			t.Errorf("status = %s, want queued (re-queued) after 3m of silence", r.TaskStatus)
		}
		if r.Attempt != 2 {
			t.Errorf("attempt = %d, want 2 — the sweep starts a new attempt", r.Attempt)
		}
		if r.RuntimeState != "offline" {
			t.Errorf("runtime = %q, want offline", r.RuntimeState)
		}
		if !r.TokenRevoked {
			t.Error("the dead attempt's task token must be revoked (§E11, FR-9.1)")
		}
	})
}

// ---------------------------------------------------------------------------
// E5-04 … E5-08 — the session gates dispatch.
// ---------------------------------------------------------------------------

// dispatchOutcome is what a claim request yields under a given session state.
type dispatchOutcome struct {
	Dispatched      int
	Order           []uuid.UUID // dispatch order when several are released
	RunningTurnKept bool        // an in-flight turn was allowed to drain
	RunningTurnKill bool        // an in-flight turn was cancelled
	TaskStatus      Status
	SessionState    string
	PauseReason     string
	SummaryMessages int
}

// dispatchUnder is wired by T-S2: given a session state and a queue, what does
// the daemon's claim get?
var dispatchUnder func(sessionState, pauseReason string, queued []uuid.UUID, running bool) dispatchOutcome

func mustDispatch(t *testing.T, state, reason string, queued []uuid.UUID, running bool) dispatchOutcome {
	t.Helper()
	if dispatchUnder == nil {
		t.Fatalf("unimplemented: session-state dispatch gating (FR-2.3 C3′). T-S2 must wire " +
			"`dispatchUnder` (see /tmp/p2a-report.md 'required API')")
	}
	return dispatchUnder(state, reason, queued, running)
}

func TestSessionGatesDispatchGolden(t *testing.T) {
	q1 := uuid.MustParse("f0000000-0000-4000-8000-000000000001")
	q2 := uuid.MustParse("f0000000-0000-4000-8000-000000000002")
	q3 := uuid.MustParse("f0000000-0000-4000-8000-000000000003")
	queue := []uuid.UUID{q1, q2, q3}

	t.Run(caseName("E5-04", "paused_session_dispatches_nothing"), func(t *testing.T) {
		o := mustDispatch(t, "paused", "director", queue, false)
		if o.Dispatched != 0 {
			t.Errorf("dispatched = %d, want 0 — a paused session holds its queue (FR-2.3 C3′)", o.Dispatched)
		}
	})

	t.Run(caseName("E5-05", "resume_dispatches_in_queue_order"), func(t *testing.T) {
		o := mustDispatch(t, "active", "", queue, false)
		if o.Dispatched != 3 {
			t.Fatalf("dispatched = %d, want 3 after resume", o.Dispatched)
		}
		if len(o.Order) != 3 || o.Order[0] != q1 || o.Order[1] != q2 || o.Order[2] != q3 {
			t.Errorf("dispatch order = %v, want queue order [q1 q2 q3]", o.Order)
		}
	})

	t.Run(caseName("E5-06", "director_pause_drains_the_running_turn"), func(t *testing.T) {
		o := mustDispatch(t, "paused", "director", queue, true)
		if !o.RunningTurnKept || o.RunningTurnKill {
			t.Error("a Director pause lets the current turn finish (drain), FR-2.3")
		}
		if o.Dispatched != 0 {
			t.Errorf("dispatched = %d, want 0 after the turn ends", o.Dispatched)
		}
		if o.PauseReason != "director" {
			t.Errorf("pause_reason = %q, want director", o.PauseReason)
		}
	})

	t.Run(caseName("E5-07", "budget_pause_cancels_the_running_turn"), func(t *testing.T) {
		o := mustDispatch(t, "paused", "budget", queue, true)
		if !o.RunningTurnKill || o.RunningTurnKept {
			t.Error("a budget pause cancels the turn (§8.2.2) — letting it run defeats the limit")
		}
		if o.TaskStatus != Paused {
			t.Errorf("task = %s, want paused(budget)", o.TaskStatus)
		}
		if o.PauseReason != "budget" {
			t.Errorf("pause_reason = %q, want budget", o.PauseReason)
		}
	})

	t.Run(caseName("E5-08", "completing_session_dispatches_nothing_and_posts_one_summary"), func(t *testing.T) {
		o := mustDispatch(t, "completing", "", queue, false)
		if o.Dispatched != 0 {
			t.Errorf("dispatched = %d, want 0 while completing", o.Dispatched)
		}
		if o.SessionState != "completed" {
			t.Errorf("session = %q, want completed once the summary is done", o.SessionState)
		}
		if o.SummaryMessages != 1 {
			t.Errorf("session_summary messages = %d, want exactly 1", o.SummaryMessages)
		}
	})
}

// ---------------------------------------------------------------------------
// E5-09, E5-10 — lane re-entry (FR-6.2). done/blocked re-enter; failed does not.
// ---------------------------------------------------------------------------

type laneReentry struct {
	Allowed      bool
	NewLane      bool
	Status       string
	ReentryCount int
}

var reenterLane func(from string) laneReentry

func mustReenter(t *testing.T, from string) laneReentry {
	t.Helper()
	if reenterLane == nil {
		t.Fatalf("unimplemented: lane re-entry machine (FR-6.2). T-S2 must wire `reenterLane` " +
			"(see /tmp/p2a-report.md 'required API')")
	}
	return reenterLane(from)
}

func TestLaneReentryGolden(t *testing.T) {
	t.Run(caseName("E5-09", "done_lane_reenters_to_running"), func(t *testing.T) {
		r := mustReenter(t, "done")
		if !r.Allowed || r.NewLane {
			t.Error("done → running re-entry is allowed and reuses the lane (lane rule 3)")
		}
		if r.Status != "running" {
			t.Errorf("status = %q, want running", r.Status)
		}
		if r.ReentryCount != 1 {
			t.Errorf("reentry_count = %d, want 1", r.ReentryCount)
		}
	})

	t.Run(caseName("E5-10", "failed_lane_does_not_reenter_it_forks_a_new_lane"), func(t *testing.T) {
		r := mustReenter(t, "failed")
		if r.Allowed && !r.NewLane {
			t.Error("lane rule 3 re-enters done and blocked only — failed must start a NEW lane")
		}
		if !r.NewLane {
			t.Error("a mention at a failed lane creates a new lane (E5-10)")
		}
	})
}

// ---------------------------------------------------------------------------
// E5-11 … E5-18 — derived agent status (FR-1.3), six-step priority ladder.
//
//	1 respond_to: nobody          → disabled
//	2 session runtime offline     → offline
//	3 unrunnable error (auth·quota·config, the classes FR-7.1 never retries)
//	                              → error
//	4 any running task            → working
//	5 any waiting_human task      → waiting_human
//	6 otherwise                   → idle
//
// blocked and paused are deliberately NOT inputs: their processes are done and
// the lane card explains them (FR-1.3, 1st bullet).
// ---------------------------------------------------------------------------

// derivedInput is the agent-scoped snapshot the ladder reads.
type derivedInput struct {
	RespondTo      string // everyone | mentions | nobody
	RuntimeOffline bool

	Running      int
	WaitingHuman int
	Blocked      int // blocked lanes — must not affect the result
	PausedBudget int // paused(budget) tasks — must not affect the result

	// LastFailureKind is the failure_kind of the agent's most recent task, or
	// "" when it did not fail. Only auth·quota·config mean "cannot run"
	// (FR-1.3 step 3); network and friends are retried.
	LastFailureKind string
	// RetryInFlight is true while the server is re-queueing that task — which
	// now includes the server re-queueing onto an ALTERNATE PROFILE
	// (daemon-protocol v0.4: fallback is the server's job; the daemon only
	// reports failure_kind). A retry in flight must never read as `error`.
	RetryInFlight bool
}

var deriveStatus func(derivedInput) string

func mustDerive(t *testing.T, in derivedInput) string {
	t.Helper()
	if deriveStatus == nil {
		t.Fatalf("unimplemented: FR-1.3 derived agent status as a pure function. " +
			"P1 computes it inside a SQL query (sessions.LoadParticipants) which the " +
			"table cannot drive. T-S2 must wire `deriveStatus` (see /tmp/p2a-report.md)")
	}
	return deriveStatus(in)
}

func TestDerivedAgentStatusGolden(t *testing.T) {
	cases := []struct {
		eval, name string
		in         derivedInput
		want       string
	}{
		{
			"E5-11", "running_beats_waiting_human",
			derivedInput{RespondTo: "everyone", Running: 1, WaitingHuman: 1},
			"working", // step 4 before step 5
		},
		{
			"E5-12", "waiting_human_when_nothing_runs",
			derivedInput{RespondTo: "everyone", WaitingHuman: 1},
			"waiting_human",
		},
		{
			"E5-13", "blocked_lanes_are_excluded_from_derivation",
			derivedInput{RespondTo: "everyone", Blocked: 2},
			"idle",
		},
		{
			"E5-14", "paused_budget_tasks_are_excluded_from_derivation",
			derivedInput{RespondTo: "everyone", PausedBudget: 1},
			"idle",
		},
		{
			"E5-15", "respond_to_nobody_wins_over_running",
			derivedInput{RespondTo: "nobody", Running: 1},
			"disabled", // step 1 outranks step 4
		},
		{
			"E5-16", "runtime_offline_wins_over_running",
			derivedInput{RespondTo: "everyone", RuntimeOffline: true, Running: 1},
			"offline", // step 2 outranks step 4
		},
		{
			"E5-17", "auth_failure_is_error_and_is_not_retried",
			derivedInput{RespondTo: "everyone", LastFailureKind: "auth"},
			"error", // step 3
		},
		{
			"E5-18", "network_failure_under_retry_is_not_error",
			derivedInput{RespondTo: "everyone", LastFailureKind: "network", RetryInFlight: true, Running: 1},
			"working", // retryable class ⇒ step 3 does not fire
		},
	}

	for _, c := range cases {
		t.Run(caseName(c.eval, c.name), func(t *testing.T) {
			got := mustDerive(t, c.in)
			if got != c.want {
				t.Errorf("derived status = %q, want %q\ninput: %+v", got, c.want, c.in)
			}
		})
	}

	// The ladder is an ORDER, not a set of independent rules. This pins the
	// order itself so an implementation cannot pass the rows above by accident.
	t.Run(caseName("E5-15", "priority_ladder_order_disabled_offline_error_working"), func(t *testing.T) {
		all := derivedInput{
			RespondTo: "nobody", RuntimeOffline: true, LastFailureKind: "auth",
			Running: 1, WaitingHuman: 1,
		}
		if got := mustDerive(t, all); got != "disabled" {
			t.Errorf("everything true at once → %q, want disabled (step 1 wins)", got)
		}

		noDisable := all
		noDisable.RespondTo = "everyone"
		if got := mustDerive(t, noDisable); got != "offline" {
			t.Errorf("→ %q, want offline (step 2)", got)
		}

		noOffline := noDisable
		noOffline.RuntimeOffline = false
		if got := mustDerive(t, noOffline); got != "error" {
			t.Errorf("→ %q, want error (step 3)", got)
		}

		noError := noOffline
		noError.LastFailureKind = ""
		if got := mustDerive(t, noError); got != "working" {
			t.Errorf("→ %q, want working (step 4)", got)
		}

		noRunning := noError
		noRunning.Running = 0
		if got := mustDerive(t, noRunning); got != "waiting_human" {
			t.Errorf("→ %q, want waiting_human (step 5)", got)
		}

		none := noRunning
		none.WaitingHuman = 0
		if got := mustDerive(t, none); got != "idle" {
			t.Errorf("→ %q, want idle (step 6)", got)
		}
	})
}
