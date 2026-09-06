//go:build p3golden

// Golden table for cancellation and the kill switch (EVAL E10, 13 rows) —
// PRD FR-3.4 (취소는 UI 조작으로만, 30초 보류, 권한), FR-1.9 M8 (`respond_to:
// nobody`의 즉시 효과), PRD §8.2.2 / contracts/harness.md §5 (취소 절차),
// contracts/daemon-protocol.md §4.3 (`cancel` 명령) and openapi
// `cancelLane` · `restartLane` · `updateAgent`.
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - The cancel ORDER is the contract, not an implementation detail: wait for
//     an in-flight edit (≤30s) → answer the pending permission request →
//     `session/cancel` → drain → only then signal the process group. Killing
//     first corrupts the runtime's own history and can leave a half-written
//     file (§8.2.2, harness §5).
//   - deputy is asymmetric on purpose: it must WAIT to approve (FR-5.4 M7) but
//     may cancel IMMEDIATELY (FR-3.4 t-3). Reusing the approval window for the
//     cancel button makes a runaway agent un-stoppable for 12 hours.
//   - The kill switch is not "stop giving it work". It cancels the running
//     turn, cancels queued tasks, KEEPS the open HITL, and preserves the
//     workdir (FR-1.9 M8) — four different verbs on four different objects.
//   - An answered HITL under a kill switch records the answer and HOLDS the
//     re-queue (E10-08). Re-queueing would restart the agent the owner just
//     disabled.
//   - Permission is judged on the human ORIGINATOR at the top of the chain,
//     never the agent that happens to be posting (FR-1.9, E10-12).
//
// HOW THIS FILE FAILS TODAY. `tasks.CancelLane` (cancel_test.go) covers the
// P1 slice — a running lane cancels and does not re-queue. The 30-second hold,
// the permission ordering, deputy rights, the kill switch's four effects and
// the originator rule are P3 and fail until T-S5 lands them.
package tasks

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	p3CancelLane   = uuid.MustParse("d0000000-0000-4000-8000-000000000003")
	p3CancelTask   = uuid.MustParse("c0000000-0000-4000-8000-000000000004")
	p3UserDirector = uuid.MustParse("b0000000-0000-4000-8000-000000000001")
	p3UserDeputy   = uuid.MustParse("b0000000-0000-4000-8000-000000000003")
	p3UserMember   = uuid.MustParse("b0000000-0000-4000-8000-000000000002")
	p3UserOwnerM1  = uuid.MustParse("b0000000-0000-4000-8000-000000000004")
)

// ---------------------------------------------------------------------------
// 1. The cancel procedure — E10-01, E10-02, E10-03
// ---------------------------------------------------------------------------

// cancelCase is the runtime state when the human presses 중단.
type cancelCase struct {
	// LastEvent is the most recent task_event: "edit_started" (no completion
	// yet), "edit_completed", "shell_started", or "" for an idle turn.
	LastEvent string
	// PermissionPending is a `session/request_permission` awaiting an answer.
	PermissionPending bool
	// CompletionAfter is how long the in-flight tool takes to report done.
	// Longer than 30s exercises E10-02.
	CompletionAfter time.Duration
}

// cancelProcedure records what the daemon did, in order.
type cancelProcedure struct {
	// Steps is the ordered list of actions actually taken. The contract's
	// order (harness §5) is:
	//   wait_tool_completion? → answer_permission? → session_cancel → drain
	//   → signal_process_group
	Steps []string

	// HeldFor is how long the cancel waited for the in-flight tool.
	HeldFor time.Duration
	// ForcedAfterTimeout is E10-02: the 30-second cap expired.
	ForcedAfterTimeout bool
	// FeedNote is the activity-feed line a forced cancel leaves.
	FeedNote string

	// PermissionOutcome is what the pending permission request was answered
	// with (harness §4: `cancelled`, not allow/reject).
	PermissionOutcome string

	// ProcessTreeRemaining must be 0 (E10-03, E11-07).
	ProcessTreeRemaining int
	// ImmediateKill is true when the process was signalled before the drain.
	ImmediateKill bool
}

// cancelTurn is wired by T-S5/T-D5. See the P3a hand-off report.
var cancelTurn func(c cancelCase) cancelProcedure

func mustCancel(t *testing.T, c cancelCase) cancelProcedure {
	t.Helper()
	if cancelTurn == nil {
		t.Fatalf("unimplemented: the §8.2.2 cancel procedure as an observable sequence " +
			"(harness §5). T-S5/T-D5 must wire `cancelTurn` — see the P3a hand-off report")
	}
	return cancelTurn(c)
}

func indexOfStep(steps []string, want string) int {
	for i, s := range steps {
		if s == want {
			return i
		}
	}
	return -1
}

func TestCancelProcedureGolden(t *testing.T) {
	t.Run(caseNameP3("E10-01", "an_unfinished_edit_holds_the_cancel_until_it_completes"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{LastEvent: "edit_started", CompletionAfter: 5 * time.Second})

		if p.HeldFor != 5*time.Second {
			t.Errorf("held for %s, want 5s — the cancel waits for the in-flight edit so a file is "+
				"not left half written (FR-3.4, harness §5 step 1)", p.HeldFor)
		}
		if p.ForcedAfterTimeout {
			t.Error("the edit completed inside 30s — this is not a forced cancel")
		}
		if i := indexOfStep(p.Steps, "wait_tool_completion"); i != 0 {
			t.Errorf("steps = %v, want the wait FIRST", p.Steps)
		}
	})

	t.Run(caseNameP3("E10-02", "the_hold_is_capped_at_thirty_seconds_and_is_recorded"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{LastEvent: "edit_started", CompletionAfter: 90 * time.Second})

		if p.HeldFor > 30*time.Second {
			t.Errorf("held for %s, want at most 30s — an unbounded wait turns 중단 into a button "+
				"that does nothing (FR-3.4)", p.HeldFor)
		}
		if !p.ForcedAfterTimeout {
			t.Error("past 30s the cancel proceeds regardless (E10-02)")
		}
		if p.FeedNote == "" {
			t.Error("the forced cancel is recorded in the activity feed — otherwise a truncated " +
				"edit looks like the agent's own work (E10-02)")
		}
	})

	t.Run(caseNameP3("E10-03", "a_pending_permission_is_answered_before_session_cancel_and_the_tree_is_clean"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{PermissionPending: true})

		perm := indexOfStep(p.Steps, "answer_permission")
		cancel := indexOfStep(p.Steps, "session_cancel")
		drain := indexOfStep(p.Steps, "drain")
		signal := indexOfStep(p.Steps, "signal_process_group")

		if perm < 0 || cancel < 0 || drain < 0 {
			t.Fatalf("steps = %v, want answer_permission → session_cancel → drain (harness §5)", p.Steps)
		}
		if !(perm < cancel && cancel < drain) {
			t.Errorf("steps = %v, wrong order: a pending permission left unanswered blocks the "+
				"agent loop, so session/cancel never gets processed (harness §5 steps 2-4)", p.Steps)
		}
		if signal >= 0 && signal < drain {
			t.Errorf("steps = %v — the process group is signalled only AFTER the drain; killing "+
				"mid-turn breaks the runtime's stored history (§8.2.2)", p.Steps)
		}
		if p.PermissionOutcome != "cancelled" {
			t.Errorf("permission outcome = %q, want cancelled — answering allow/reject would run "+
				"or refuse a tool we are abandoning (harness §4)", p.PermissionOutcome)
		}
		if p.ImmediateKill {
			t.Error("the process is not killed immediately (E10-03)")
		}
		if p.ProcessTreeRemaining != 0 {
			t.Errorf("process tree remaining = %d, want 0 (E10-03, E11-07)", p.ProcessTreeRemaining)
		}
	})
}

// ---------------------------------------------------------------------------
// 2. What a cancel leaves behind — E10-04
// ---------------------------------------------------------------------------

type cancelEffect struct {
	LaneStatus  string
	TaskStatus  string
	FailureKind string
	FeedNote    string
	// NewTasks counts tasks created by the cancel. 중단 creates none;
	// only 중단하고 다시 지시 does (FR-3.4 표).
	NewTasks int
	Requeued bool
}

var cancelEffects func() cancelEffect

func TestCancelEffectGolden(t *testing.T) {
	t.Run(caseNameP3("E10-04", "cancel_lands_the_lane_on_failed_cancelled_and_creates_no_new_task"), func(t *testing.T) {
		if cancelEffects == nil {
			t.Fatalf("unimplemented: cancel outcome (FR-3.4 표, openapi cancelLane). " +
				"T-S5 must wire `cancelEffects` — see the P3a hand-off report")
		}
		e := cancelEffects()
		if e.LaneStatus != "failed" {
			t.Errorf("lane = %q, want failed (FR-3.4 '중단' row)", e.LaneStatus)
		}
		if e.TaskStatus != "cancelled" || e.FailureKind != "cancelled" {
			t.Errorf("task = %q(%q), want cancelled(cancelled) (openapi cancelLane)", e.TaskStatus, e.FailureKind)
		}
		if e.FeedNote == "" {
			t.Error("the feed says a person stopped it — otherwise a cancel is indistinguishable " +
				"from a crash (E10-04)")
		}
		if e.NewTasks != 0 {
			t.Errorf("new tasks = %d, want 0 — 중단 gives no new instruction (FR-3.4)", e.NewTasks)
		}
		if e.Requeued {
			t.Error("a cancelled task is not re-queued: cancellation is deliberate, not a failure " +
				"to retry (E10-13)")
		}
	})
}

// ---------------------------------------------------------------------------
// 3. Who may cancel — E10-05, E10-06
// ---------------------------------------------------------------------------

type cancelPermission struct {
	Allowed    bool
	HTTPStatus int
	// ButtonEnabled is what the UI shows (SCREEN: visible but disabled).
	ButtonEnabled bool
	// AvailableFrom is non-zero only where a right is delayed. Cancel is never
	// delayed, so this must stay zero even for the deputy.
	AvailableFrom time.Duration
}

var mayCancel func(actor uuid.UUID, isDeputy bool, elapsed time.Duration) cancelPermission

func mustMayCancel(t *testing.T, actor uuid.UUID, isDeputy bool, elapsed time.Duration) cancelPermission {
	t.Helper()
	if mayCancel == nil {
		t.Fatalf("unimplemented: cancel authorisation (FR-5.3 표, FR-3.4 t-3). " +
			"T-S5 must wire `mayCancel` — see the P3a hand-off report")
	}
	return mayCancel(actor, isDeputy, elapsed)
}

func TestCancelPermissionGolden(t *testing.T) {
	t.Run(caseNameP3("E10-05", "a_plain_member_cannot_cancel_and_the_api_says_403"), func(t *testing.T) {
		p := mustMayCancel(t, p3UserMember, false, time.Hour)
		if p.Allowed {
			t.Fatal("cancellation is Director/deputy only (FR-5.3 표)")
		}
		if p.HTTPStatus != 403 {
			t.Errorf("status = %d, want 403 — the button being greyed out is not enforcement",
				p.HTTPStatus)
		}
		if p.ButtonEnabled {
			t.Error("the button is visible but disabled (FR-5.3 마지막 bullet)")
		}
	})

	t.Run(caseNameP3("E10-06", "the_deputy_can_cancel_immediately_unlike_approving"), func(t *testing.T) {
		p := mustMayCancel(t, p3UserDeputy, true, time.Minute)
		if !p.Allowed {
			t.Fatal("the deputy cancels IMMEDIATELY — the approval half-deadline (FR-5.4 M7) does " +
				"not apply, because a runaway turn gets more expensive while you wait (FR-3.4 t-3)")
		}
		if p.AvailableFrom != 0 {
			t.Errorf("available_from = %s, want 0 — a delay here is the E7-09 rule leaking into a "+
				"place the PRD deliberately kept immediate", p.AvailableFrom)
		}
		if !p.ButtonEnabled {
			t.Error("the deputy's cancel button is enabled from the start (E10-06)")
		}
	})

	t.Run(caseNameP3("E10-06", "the_director_can_always_cancel"), func(t *testing.T) {
		p := mustMayCancel(t, p3UserDirector, false, 0)
		if !p.Allowed || !p.ButtonEnabled {
			t.Error("the Director may cancel at any time (FR-5.3 표)")
		}
	})
}

// ---------------------------------------------------------------------------
// 4. Kill switch — E10-07, E10-08, E10-09
// ---------------------------------------------------------------------------

// killSwitchState is the agent's work when `respond_to` becomes `nobody`.
type killSwitchState struct {
	Running      int
	Queued       int
	WaitingHuman int // each with an open HITL
}

// killSwitchEffect is FR-1.9 M8's four-row table, made observable.
type killSwitchEffect struct {
	RunningCancelled int
	CancelFeedNote   string
	QueuedCancelled  int
	// HitlStillOpen is the count of open requests kept. Closing them takes the
	// answer away from a human who may still want to give it.
	HitlStillOpen int
	// WorkdirsPreserved must equal the number of workdirs that existed.
	WorkdirsPreserved int
	AgentStatus       string

	// InviteAllowed is E10-09: a disabled agent cannot be invited to a new
	// session.
	InviteAllowed bool
}

var applyKillSwitch func(s killSwitchState) killSwitchEffect

func mustKill(t *testing.T, s killSwitchState) killSwitchEffect {
	t.Helper()
	if applyKillSwitch == nil {
		t.Fatalf("unimplemented: kill switch immediate effects (FR-1.9 M8, openapi updateAgent). " +
			"T-S5 must wire `applyKillSwitch` — see the P3a hand-off report")
	}
	return applyKillSwitch(s)
}

func TestKillSwitchGolden(t *testing.T) {
	t.Run(caseNameP3("E10-07", "nobody_cancels_running_and_queued_keeps_the_hitl_and_the_workdir"), func(t *testing.T) {
		e := mustKill(t, killSwitchState{Running: 1, Queued: 2, WaitingHuman: 1})

		if e.RunningCancelled != 1 {
			t.Errorf("running cancelled = %d, want 1 — a kill switch that only stops FUTURE work "+
				"leaves the runaway turn running, which is the case it exists for (FR-1.9 M8)",
				e.RunningCancelled)
		}
		if e.CancelFeedNote == "" {
			t.Error("the feed says the owner stopped it (FR-1.9 M8 표)")
		}
		if e.QueuedCancelled != 2 {
			t.Errorf("queued cancelled = %d, want 2", e.QueuedCancelled)
		}
		if e.HitlStillOpen != 1 {
			t.Errorf("open HITL = %d, want 1 KEPT — the human's chance to answer is not the "+
				"agent's to lose (FR-1.9 M8 표 3행)", e.HitlStillOpen)
		}
		if e.WorkdirsPreserved != 1 {
			t.Errorf("workdirs preserved = %d, want 1 — re-enabling must be able to continue",
				e.WorkdirsPreserved)
		}
		if e.AgentStatus != "disabled" {
			t.Errorf("agent status = %q, want disabled — distinct from offline, which means the "+
				"machine is unreachable (FR-1.9, E5-15)", e.AgentStatus)
		}
	})

	t.Run(caseNameP3("E10-09", "a_disabled_agent_cannot_be_invited_to_a_new_session"), func(t *testing.T) {
		e := mustKill(t, killSwitchState{})
		if e.InviteAllowed {
			t.Error("`nobody` blocks new invitations as well as stopping current work (FR-1.9 표)")
		}
	})
}

// killSwitchAnswer is E10-08: the Director answers the HITL that survived.
type killSwitchAnswer struct {
	HitlStatus string
	// TaskStatus must NOT be queued while the agent is disabled.
	TaskStatus string
	// RequeueHeld is the explicit "record the answer, hold the re-queue" state.
	RequeueHeld bool
	// AfterReenable is what happens when respond_to goes back to owner.
	AfterReenableTaskStatus string
}

var answerUnderKillSwitch func() killSwitchAnswer

func TestKillSwitchHitlAnswerGolden(t *testing.T) {
	t.Run(caseNameP3("E10-08", "an_answer_under_the_kill_switch_is_recorded_but_the_requeue_waits"), func(t *testing.T) {
		if answerUnderKillSwitch == nil {
			t.Fatalf("unimplemented: HITL answer while disabled (FR-1.9 M8, E10-08). " +
				"T-S5 must wire `answerUnderKillSwitch` — see the P3a hand-off report")
		}
		a := answerUnderKillSwitch()
		if a.HitlStatus != "answered" {
			t.Errorf("hitl = %q, want answered — the answer is recorded even though nothing runs yet",
				a.HitlStatus)
		}
		if a.TaskStatus == "queued" {
			t.Fatal("re-queueing restarts the agent the owner just disabled — the re-queue is HELD " +
				"(FR-1.9 M8 표 3행, E10-08)")
		}
		if !a.RequeueHeld {
			t.Error("the held state must be explicit; otherwise re-enabling has nothing to release")
		}
		if a.AfterReenableTaskStatus != "queued" {
			t.Errorf("after re-enable task = %q, want queued — the held answer resumes then "+
				"(E10-08 '다시 활성화하면 그때 이어진다')", a.AfterReenableTaskStatus)
		}
	})
}

// ---------------------------------------------------------------------------
// 5. Invitation rights and the originator rule — E10-10, E10-11, E10-12
// ---------------------------------------------------------------------------

// triggerCase asks whether an agent may be triggered/invited in a situation.
type triggerCase struct {
	// RespondTo is the agent's setting.
	RespondTo string
	// OwnerID owns the agent.
	OwnerID uuid.UUID
	// Actor is who is acting.
	Actor uuid.UUID
	// InSession is true for an in-session trigger (mention), false for an
	// invitation from outside.
	InSession bool
	// Participant is true when the agent is already a session participant.
	Participant bool
	// OriginatorUserID is the human at the top of the chain — the only
	// identity permission is judged on (FR-1.9).
	OriginatorUserID uuid.UUID
	// ViaAgent is true when an agent, not a human, posted the mention.
	ViaAgent bool
}

type triggerVerdict struct {
	Allowed bool
	// JudgedOn is the user id the decision was made against. It must be the
	// originator, never the posting agent's owner.
	JudgedOn uuid.UUID
}

var mayTrigger func(c triggerCase) triggerVerdict

func mustTrigger(t *testing.T, c triggerCase) triggerVerdict {
	t.Helper()
	if mayTrigger == nil {
		t.Fatalf("unimplemented: the FR-1.9 permission gate (참여 = 허용, originator 기준). " +
			"T-S5 must wire `mayTrigger` — see the P3a hand-off report")
	}
	return mayTrigger(c)
}

func TestInvitationAndOriginatorGolden(t *testing.T) {
	t.Run(caseNameP3("E10-10", "session_participation_is_itself_the_permission_to_trigger"), func(t *testing.T) {
		v := mustTrigger(t, triggerCase{
			RespondTo: "owner", OwnerID: p3UserOwnerM1,
			Actor: p3UserMember, InSession: true, Participant: true,
			OriginatorUserID: p3UserMember,
		})
		if !v.Allowed {
			t.Error("inviting the agent WAS the permission grant; inside the session `respond_to` " +
				"no longer gates mentions, or a team workspace's default blocks collaboration (FR-1.9)")
		}
	})

	t.Run(caseNameP3("E10-11", "a_non_owner_cannot_invite_an_owner_scoped_agent"), func(t *testing.T) {
		v := mustTrigger(t, triggerCase{
			RespondTo: "owner", OwnerID: p3UserOwnerM1,
			Actor: p3UserMember, InSession: false, Participant: false,
			OriginatorUserID: p3UserMember,
		})
		if v.Allowed {
			t.Error("`respond_to` governs what happens OUTSIDE the session: only the owner may " +
				"invite an owner-scoped agent (FR-1.9 표)")
		}
	})

	t.Run(caseNameP3("E10-12", "permission_is_judged_on_the_human_originator_not_the_calling_agent"), func(t *testing.T) {
		v := mustTrigger(t, triggerCase{
			RespondTo: "owner", OwnerID: p3UserOwnerM1,
			Actor: p3UserMember, InSession: true, Participant: true,
			OriginatorUserID: p3UserMember, ViaAgent: true,
		})
		if v.JudgedOn != p3UserMember {
			t.Errorf("judged on %s, want the originator %s — routing a request through an agent "+
				"must not escalate it to that agent's owner (FR-1.9)", v.JudgedOn, p3UserMember)
		}
	})
}

// ---------------------------------------------------------------------------
// 6. Daemon shutdown is a cancel, not a failure — E10-13 (G3 regression row)
// ---------------------------------------------------------------------------

type shutdownResult struct {
	// Steps must be the same §8.2.2 sequence as a human cancel.
	Steps []string
	// FinishOutcome is what the daemon reports (daemon-protocol §4.4).
	FinishOutcome string
	// Requeued must be false: SIGTERM is an orderly stop, and re-queueing
	// hands the task straight back to a daemon that is going away.
	Requeued             bool
	ProcessTreeRemaining int
}

var daemonSigterm func() shutdownResult

func TestDaemonShutdownCancelGolden(t *testing.T) {
	t.Run(caseNameP3("E10-13", "sigterm_cancels_the_running_attempt_through_the_same_procedure"), func(t *testing.T) {
		if daemonSigterm == nil {
			t.Fatalf("unimplemented: daemon SIGTERM path (harness §5, E10-13 — a G3 regression row). " +
				"T-D5 must wire `daemonSigterm` — see the P3a hand-off report")
		}
		r := daemonSigterm()
		if indexOfStep(r.Steps, "session_cancel") < 0 || indexOfStep(r.Steps, "drain") < 0 {
			t.Errorf("steps = %v, want the §8.2.2 sequence — a shutdown that skips it leaves the "+
				"runtime session inconsistent", r.Steps)
		}
		if r.FinishOutcome != "cancelled" {
			t.Errorf("finish outcome = %q, want cancelled (daemon-protocol §4.4)", r.FinishOutcome)
		}
		if r.Requeued {
			t.Error("a cancelled attempt is not re-queued (E10-13)")
		}
		if r.ProcessTreeRemaining != 0 {
			t.Errorf("process tree remaining = %d, want 0", r.ProcessTreeRemaining)
		}
	})
}

// fixtures the wiring PR consumes.
var _ = []uuid.UUID{p3CancelLane, p3CancelTask}
