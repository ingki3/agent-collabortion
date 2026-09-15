//go:build p3golden

// Wiring for the E10 golden table (cancellation · kill switch · the FR-1.9
// gate).
//
// The file keeps its `p3golden` tag because two of its hooks are the DAEMON's
// work: `cancelTurn` (E10-01·02·03·14 — the §8.2.2 sequence, its 30-second cap
// and the pending-permission answer, all carried out by the daemon per
// harness.md §5) and `daemonSigterm` (E10-13). Wiring a server-side stand-in to
// either would be a shadow hook (Lead's rule, T-S5 ask 2), so they stay nil and
// T-D5 lands them.
//
// PRODUCTION CALL SITES for the hooks that ARE wired:
//
//	cancelEffects         → PlanCancelResult      tasks.cancelLocked (lane/task
//	                                              statuses, the feed note) and
//	                                              httpapi.RestartLane (the one
//	                                              new task)
//	mayCancel             → MayCancel             httpapi.CancelLane and
//	                                              lanes.Load's can_control
//	applyKillSwitch       → PlanKillSwitch        httpapi.UpdateAgent
//	answerUnderKillSwitch → PlanKillSwitchAnswer  httpapi.answerAgentHitl (the
//	                                              held branch) and
//	                                              httpapi.releaseHeldRequeues
//	mayTrigger            → MayTrigger            router.Post's trigger gate and
//	                                              httpapi.AddParticipant
package tasks

import (
	"time"

	"github.com/google/uuid"
)

func init() {
	cancelEffects = adaptCancelEffects
	mayCancel = adaptMayCancel
	applyKillSwitch = adaptKillSwitch
	answerUnderKillSwitch = adaptKillSwitchAnswer
	mayTrigger = adaptMayTrigger
}

func adaptCancelEffects() cancelEffect {
	r := PlanCancelResult("")
	return cancelEffect{
		LaneStatus: r.LaneStatus, TaskStatus: r.TaskStatus, FailureKind: r.FailureKind,
		FeedNote: r.FeedNote, NewTasks: r.NewTasks, Requeued: r.Requeue,
	}
}

func adaptMayCancel(actor uuid.UUID, isDeputy bool, elapsed time.Duration) cancelPermission {
	var deputy *uuid.UUID
	if isDeputy {
		d := actor
		deputy = &d
	}
	p := MayCancel(actor, p3UserDirector, deputy)
	return cancelPermission{
		Allowed: p.Allowed, HTTPStatus: p.HTTPStatus,
		ButtonEnabled: p.ButtonEnabled, AvailableFrom: p.AvailableFrom,
	}
}

func adaptKillSwitch(s killSwitchState) killSwitchEffect {
	// One workdir per running lane is the fixture the row states: the agent's
	// running work has a directory, and E10-07 asks that it survives.
	e := PlanKillSwitch(KillSwitchState{
		Running: s.Running, Queued: s.Queued, WaitingHuman: s.WaitingHuman, Workdirs: s.Running,
	})
	return killSwitchEffect{
		RunningCancelled: e.CancelRunning, CancelFeedNote: e.FeedNote,
		QueuedCancelled: e.CancelQueued, HitlStillOpen: e.KeepHitlOpen,
		WorkdirsPreserved: e.PreserveWorkdirs, AgentStatus: e.AgentStatus,
		InviteAllowed: e.InviteAllowed,
	}
}

func adaptKillSwitchAnswer() killSwitchAnswer {
	a := PlanKillSwitchAnswer()
	return killSwitchAnswer{
		HitlStatus: a.HitlStatus, TaskStatus: a.TaskStatus,
		RequeueHeld: a.RequeueHeld, AfterReenableTaskStatus: a.AfterReenableTaskStatus,
	}
}

func adaptMayTrigger(c triggerCase) triggerVerdict {
	v := MayTrigger(TriggerInput{
		RespondTo: c.RespondTo, OwnerID: c.OwnerID,
		InSession: c.InSession, Participant: c.Participant,
		OriginatorUserID: c.OriginatorUserID,
	})
	return triggerVerdict{Allowed: v.Allowed, JudgedOn: v.JudgedOn}
}
