package tasks

import "github.com/google/uuid"

// DispatchOutcome is what a daemon claim gets under a given session state.
type DispatchOutcome struct {
	Dispatched      int
	Order           []uuid.UUID
	RunningTurnKept bool // an in-flight turn was allowed to drain
	RunningTurnKill bool // an in-flight turn was cancelled
	TaskStatus      Status
	SessionState    string
	PauseReason     string
	SummaryMessages int
}

// PlanDispatch is the FR-2.3 session gate (C3′). The session, not the task, is
// the thing that decides whether a queued task may leave the queue: a paused
// session that keeps dispatching spends exactly the budget the pause was
// supposed to stop.
//
// The two pauses differ in what they do to a turn already running (FR-2.3,
// §8.2.2):
//
//	director → drain. The human asked for a stop, not for lost work.
//	budget   → cancel. Letting the turn finish defeats the limit (E5-07).
func PlanDispatch(sessionState, pauseReason string, queued []uuid.UUID, running bool) DispatchOutcome {
	o := DispatchOutcome{SessionState: sessionState, PauseReason: pauseReason, Order: []uuid.UUID{}}
	switch sessionState {
	case "active":
		o.Order = append(o.Order, queued...)
		o.Dispatched = len(o.Order)
	case "paused":
		if running {
			switch pauseReason {
			case "budget", "time":
				o.RunningTurnKill = true
				o.TaskStatus = Paused
			default:
				o.RunningTurnKept = true
			}
		}
	case "completing":
		// The summary is the last thing the session does, and it posts exactly
		// one session_summary message (FR-2.4) before the state settles.
		o.SessionState, o.SummaryMessages = "completed", 1
	}
	return o
}
