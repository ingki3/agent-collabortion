//go:build p2golden

// Wiring for the E5 golden table. The decisions live in sweep.go, gate.go and
// derive.go; this file only shapes them into the table's structs.
package tasks

import (
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
)

func init() {
	sweepAt = adaptSweep
	dispatchUnder = adaptDispatch
	reenterLane = adaptReenter
	deriveStatus = adaptDerive
}

// defaultMaxAttempts mirrors task.max_attempts (0001_init.sql).
const defaultMaxAttempts = 3

func adaptSweep(initial Status, since time.Duration) sweepResult {
	o, stale := PlanSweep(initial, since, 1, defaultMaxAttempts)
	if !stale {
		return sweepResult{TaskStatus: initial, Attempt: 1, RuntimeState: "online"}
	}
	return sweepResult{
		TaskStatus: o.TaskStatus, FailureKind: o.FailureKind, Attempt: o.Attempt,
		RuntimeState: o.RuntimeState, TokenRevoked: o.TokenRevoked,
	}
}

func adaptDispatch(sessionState, pauseReason string, queued []uuid.UUID, running bool) dispatchOutcome {
	o := PlanDispatch(sessionState, pauseReason, queued, running)
	return dispatchOutcome{
		Dispatched: o.Dispatched, Order: o.Order,
		RunningTurnKept: o.RunningTurnKept, RunningTurnKill: o.RunningTurnKill,
		TaskStatus: o.TaskStatus, SessionState: o.SessionState,
		PauseReason: o.PauseReason, SummaryMessages: o.SummaryMessages,
	}
}

func adaptReenter(from string) laneReentry {
	r := lanestate.Reenter(from, 0)
	return laneReentry{Allowed: r.Allowed, NewLane: r.NewLane, Status: r.Status, ReentryCount: r.ReentryCount}
}

func adaptDerive(in derivedInput) string {
	return DeriveAgentStatus(Derived{
		RespondTo: in.RespondTo, RuntimeOffline: in.RuntimeOffline,
		Running: in.Running, WaitingHuman: in.WaitingHuman,
		Blocked: in.Blocked, PausedBudget: in.PausedBudget,
		LastFailureKind: in.LastFailureKind, RetryInFlight: in.RetryInFlight,
	})
}
