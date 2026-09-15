package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
)

// FallbackDelay is FR-3.3 rule 7's grace period before the assignee is woken.
const FallbackDelay = 5 * time.Minute

// Fallback is the deferred assignee task rule 7 schedules when a reply woke
// somebody other than the assignee. It is a plan, not a row: the caller
// inserts a task with status `deferred` and not_before = DueAt.
type Fallback struct {
	AgentID uuid.UUID
	Delay   time.Duration
	DueAt   time.Time
}

// PlanFallback is FR-3.3 rule 7. When rule 5 triggered a non-assignee, the
// assignee gets a deferred task five minutes out (E1-12); it is cancelled if
// the primary agent answers first (E1-13) and promoted to queued if nobody
// does (E1-14).
//
// now comes from contracts/clock, never from time.Now: the golden table drives
// the boundary and a wall clock cannot be driven.
func PlanFallback(d Decision, assignee *uuid.UUID, now time.Time) *Fallback {
	if assignee == nil {
		return nil
	}
	for _, tr := range d.Triggers {
		if tr.Rule != 5 || tr.AgentID == *assignee {
			continue
		}
		return &Fallback{AgentID: *assignee, Delay: FallbackDelay, DueAt: now.Add(FallbackDelay)}
	}
	return nil
}

// FallbackOutcome is what happened to a scheduled fallback once time passed.
// Exactly one of the two is true, or neither while the window is still open.
type FallbackOutcome struct {
	Cancelled bool
	Promoted  bool
}

// ResolveFallback decides the deferred task's fate. A reply from the primary
// agent inside the window cancels it; the window elapsing without one promotes
// it deferred → queued.
func ResolveFallback(f *Fallback, elapsed time.Duration, primaryReplied bool) FallbackOutcome {
	if f == nil {
		return FallbackOutcome{}
	}
	if primaryReplied && elapsed < f.Delay {
		return FallbackOutcome{Cancelled: true}
	}
	if !primaryReplied && elapsed >= f.Delay {
		return FallbackOutcome{Promoted: true}
	}
	return FallbackOutcome{}
}

// Arrival is FR-3.4's answer to "a message landed on a lane that is busy".
// The one invariant: nothing here ever cancels a running turn.
type Arrival struct {
	CancelledRunningTurn bool
	QueuedTaskCount      int
	CoalescedMessageIDs  []uuid.UUID
	LaneID               uuid.UUID
}

// PlanArrival merges arriving messages into the lane's single queued task
// (E2-09 … E2-11). queued is the message ids the lane's existing queued task
// already carries, in arrival order; arriving are the new ones.
//
// The merge unit is the LANE, not the agent: a second lane of the same agent
// keeps its own queue, which is why this takes a lane id and no agent id.
func PlanArrival(lane uuid.UUID, laneStatus string, queued, arriving []uuid.UUID) Arrival {
	a := Arrival{LaneID: lane, CoalescedMessageIDs: append(append([]uuid.UUID{}, queued...), arriving...)}
	if len(a.CoalescedMessageIDs) > 0 {
		a.QueuedTaskCount = 1
	}
	_ = laneStatus // a running lane queues exactly like an idle one (FR-3.4)
	return a
}

// LayoutFor re-exports the FR-6.1 binding so callers that already hold the
// router do not need the lanestate import.
func LayoutFor(isolation string, lanes []lanestate.Candidate) lanestate.Layout {
	return lanestate.LayoutFor(isolation, lanes)
}
