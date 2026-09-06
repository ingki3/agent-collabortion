package tasks

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// Cancellation, the kill switch and the FR-1.9 permission gate (PRD FR-3.4,
// FR-1.9 M8, FR-5.3, PRD §8.2.2, contracts/harness.md §5).
//
// The DAEMON carries out the §8.2.2 sequence (wait for an in-flight tool →
// answer the pending permission → session/cancel → drain → signal). What the
// SERVER owns is on this side of the command: which reason it carries, whether
// the current tool is allowed to finish, what the cancel leaves behind, and who
// may press the button. Those are the parts implemented here.

// CancelResult is what a cancel leaves behind (FR-3.4 표, openapi cancelLane).
type CancelResult struct {
	LaneStatus  string
	TaskStatus  string
	FailureKind string
	FeedNote    string
	// NewTasks is 0 for 중단 and 1 for 중단하고 다시 지시 — the difference
	// between the two rows of FR-3.4's table is exactly this number.
	NewTasks int
	// Requeue is false: a cancelled attempt is not retried. Cancellation is
	// deliberate, not a failure (E10-04, E10-13).
	Requeue bool
}

// PlanCancelResult is FR-3.4's table. `newInstruction` empty is 중단; a new
// instruction is 중단하고 다시 지시, which cancels the same way and then
// creates ONE new task (a re-instruction, not a retry — FR-3.4 B).
//
// production callers: tasks.cancelLocked (the lane/task statuses and the
// feed note it writes) and httpapi.RestartLane (the new task).
func PlanCancelResult(newInstruction string) CancelResult {
	r := CancelResult{
		LaneStatus: "failed", TaskStatus: string(Cancelled),
		FailureKind: string(contracts.FailCancelled),
		FeedNote:    "사람이 중단함",
	}
	if newInstruction != "" {
		r.NewTasks = 1
	}
	return r
}

// CancelCommand is the §4.3 `cancel` payload the server queues. `after_current_tool`
// is the server's call because the server is what sees the task_event stream:
// an unfinished edit or shell command means the daemon must wait for it (≤30s,
// harness §5 step 1) rather than cancelling into a half-written file.
type CancelCommand struct {
	AfterCurrentTool bool
	Reason           string
	// HoldFor is the cap the daemon applies to that wait. It is carried here so
	// the feed note the server writes when the daemon reports a forced cancel
	// quotes the same number the contract states.
	HoldFor time.Duration
}

// PlanCancelCommand is the §4.3 `cancel` payload rule.
//
// `after_current_tool` is true for EVERY cancel the server issues — a human's
// 중단, a budget or loop pause, the kill switch and a session cancel all go
// through the §8.2.2 procedure. There is no reason in the contract's list that
// justifies leaving a file or a migration half written, so the server has no
// immediate-abort branch to choose. Whether a tool is actually in flight is the
// daemon's observation (harness §5 step 1), not the server's: the server sees
// task_events after the fact and would race the tool it is asking about.
//
// production callers: tasks.Service.CancelLane, tasks.Service.CancelForSession
// and tasks.Service.pauseLocked — the three places a cancel command is queued.
func PlanCancelCommand(reason string) CancelCommand {
	return CancelCommand{AfterCurrentTool: true, Reason: reason, HoldFor: contracts.CancelDrainWait}
}

// CancelPermission is who may press 중단 (FR-5.3 표, FR-3.4 t-3).
type CancelPermission struct {
	Allowed    bool
	HTTPStatus int
	// ButtonEnabled is what the UI shows: the button is visible but disabled
	// for a member (FR-5.3 last bullet), never hidden — hiding it makes the
	// permission invisible instead of explained.
	ButtonEnabled bool
	// AvailableFrom stays zero even for the deputy. The approval half-deadline
	// (FR-5.4 M7) does NOT apply to cancellation: a runaway turn gets more
	// expensive while you wait (FR-3.4 t-3).
	AvailableFrom time.Duration
}

// MayCancel is the cancel gate.
//
// production caller: httpapi.CancelLane and lanes.Load's `can_control`.
func MayCancel(actor, director uuid.UUID, deputy *uuid.UUID) CancelPermission {
	if actor == director || (deputy != nil && actor == *deputy) {
		return CancelPermission{Allowed: true, HTTPStatus: 200, ButtonEnabled: true}
	}
	return CancelPermission{HTTPStatus: 403}
}

// ---------------------------------------------------------------------------
// Kill switch (FR-1.9 M8)
// ---------------------------------------------------------------------------

// KillSwitchState is the agent's work when `respond_to` becomes `nobody`.
type KillSwitchState struct {
	Running      int
	Queued       int
	WaitingHuman int
	Workdirs     int
}

// KillSwitchEffect is FR-1.9 M8's table: four verbs on four objects.
type KillSwitchEffect struct {
	CancelRunning int
	CancelQueued  int
	// KeepHitlOpen: the open requests are NOT closed. The human's chance to
	// answer is not the agent's to lose (M8 표 3행).
	KeepHitlOpen int
	// PreserveWorkdirs: re-enabling must be able to continue.
	PreserveWorkdirs int
	AgentStatus      string
	FeedNote         string
	// InviteAllowed is false: `nobody` blocks new invitations as well as
	// stopping current work (E10-09).
	InviteAllowed bool
}

// PlanKillSwitch is what `respond_to: nobody` does immediately.
//
// production caller: httpapi.UpdateAgent (applyKillSwitch).
func PlanKillSwitch(s KillSwitchState) KillSwitchEffect {
	return KillSwitchEffect{
		CancelRunning: s.Running, CancelQueued: s.Queued,
		KeepHitlOpen: s.WaitingHuman, PreserveWorkdirs: s.Workdirs,
		AgentStatus:   DeriveAgentStatus(Derived{RespondTo: "nobody"}),
		FeedNote:      "소유자가 이 에이전트를 중지했습니다",
		InviteAllowed: false,
	}
}

// KillSwitchAnswer is E10-08: a human answers the request that survived.
type KillSwitchAnswer struct {
	HitlStatus string
	TaskStatus string
	// RequeueHeld must be explicit; otherwise re-enabling has nothing to
	// release.
	RequeueHeld bool
	// AfterReenableTaskStatus is where the held answer lands when respond_to
	// goes back.
	AfterReenableTaskStatus string
}

// PlanKillSwitchAnswer records the answer and holds the re-queue.
//
// production caller: hitl.PlanRespond's AgentDisabled branch, applied by
// httpapi.answerAgentHitl; the release is httpapi.releaseHeldRequeues.
func PlanKillSwitchAnswer() KillSwitchAnswer {
	return KillSwitchAnswer{
		HitlStatus: "answered", TaskStatus: string(WaitingHuman),
		RequeueHeld: true, AfterReenableTaskStatus: string(Queued),
	}
}

// ---------------------------------------------------------------------------
// The FR-1.9 permission gate
// ---------------------------------------------------------------------------

// TriggerInput asks whether an agent may be triggered or invited.
type TriggerInput struct {
	RespondTo string
	OwnerID   uuid.UUID
	Allowlist []uuid.UUID
	// InSession is an in-session trigger (a mention); false is an invitation
	// from outside.
	InSession   bool
	Participant bool
	// OriginatorUserID is the human at the top of the chain. It is the ONLY
	// identity permission is judged on — routing a request through an agent
	// must not escalate it to that agent's owner (E10-12).
	OriginatorUserID uuid.UUID
}

// TriggerVerdict is the answer plus the identity it was judged on, so the
// second half of E10-12 is observable rather than implied.
type TriggerVerdict struct {
	Allowed  bool
	JudgedOn uuid.UUID
	Reason   string
}

// MayTrigger is FR-1.9. Inside a session, participation IS the permission:
// inviting the agent was the grant, and re-checking `respond_to` on every
// mention makes a team workspace's default block the collaboration it was
// invited for (E10-10). Outside, `respond_to` governs who may invite (E10-11).
//
// production callers: router.Post's trigger gate and httpapi.AddParticipant.
func MayTrigger(in TriggerInput) TriggerVerdict {
	v := TriggerVerdict{JudgedOn: in.OriginatorUserID}
	if in.RespondTo == "nobody" {
		v.Reason = "kill switch: respond_to is nobody"
		return v
	}
	if in.InSession && in.Participant {
		v.Allowed = true
		return v
	}
	switch in.RespondTo {
	case "workspace":
		v.Allowed = true
	case "allowlist":
		if in.OriginatorUserID == in.OwnerID {
			v.Allowed = true
			break
		}
		for _, id := range in.Allowlist {
			if id == in.OriginatorUserID {
				v.Allowed = true
				break
			}
		}
		if !v.Allowed {
			v.Reason = "not on this agent's allowlist"
		}
	default: // owner
		v.Allowed = in.OriginatorUserID == in.OwnerID
		if !v.Allowed {
			v.Reason = "only the owner can invite this agent"
		}
	}
	return v
}

// ---------------------------------------------------------------------------
// Production side
// ---------------------------------------------------------------------------

// CancelForSession cancels one task inside a transaction the caller owns
// (cancelSession, the kill switch). A task nobody is running yet ends at once;
// an in-flight attempt gets the §8.2.2 `cancel` command and ends when the
// daemon's finish arrives — the server never signals a process itself.
func (s *Service) CancelForSession(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, reason string, now time.Time) error {
	t, err := lockTask(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if Terminal(t.Status) {
		return nil
	}
	res := PlanCancelResult("")
	if err := InsertServerEvent(ctx, tx, t.ID, t.Attempt, "status", "cancel", reason, "ok",
		// S-52: `status` payload is closed to {command,args,result_ref,
		// rejected_reason}. The human sentence and the reason are the
		// command's arguments, and `args` is the schema's open object.
		map[string]any{"command": "cancel", "args": map[string]any{
			"note": res.FeedNote, "reason": reason,
		}}, now); err != nil {
		return err
	}
	switch t.Status {
	case Dispatched, Preparing, Running:
		if t.RuntimeID != nil {
			if requested, err := cancelRequested(ctx, tx, t.ID, t.Attempt); err != nil {
				return err
			} else if !requested {
				cmd := cancelCommandFor(t, reason)
				if err := tokens.QueueCommand(ctx, tx, *t.RuntimeID, cmd); err != nil {
					return err
				}
			}
			return nil
		}
	}
	return s.cancelLocked(ctx, tx, t, reason, now)
}

// cancelCommandFor turns PlanCancelCommand's verdict into the §4.3 payload.
func cancelCommandFor(t *Row, reason string) contracts.Command {
	plan := PlanCancelCommand(reason)
	return contracts.Command{
		Type: contracts.CmdCancel, TaskID: t.ID.String(), Attempt: t.Attempt,
		AfterCurrentTool: plan.AfterCurrentTool, Reason: plan.Reason,
	}
}
