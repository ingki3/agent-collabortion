package tasks

import (
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// SweepOutcome is what one stale task looks like after the scheduler sweep.
type SweepOutcome struct {
	TaskStatus   Status
	FailureKind  string
	Attempt      int
	RuntimeState string // online | offline
	TokenRevoked bool
}

// PlanSweep classifies a task that has gone quiet (daemon-protocol §7). The
// second return is false when the task is not stale at all.
//
// Two different silences with two different answers (FR-7.1):
//
//	dispatched/preparing, 5m without a running report → failed(timeout).
//	  Nobody ever claimed it, so there is no work in progress to resume and
//	  re-queueing would hand the same task to the same silent runtime (E5-02).
//	running, 3m without a heartbeat → the runtime is gone. Mark it offline and
//	  re-queue onto a new attempt, revoking the dead attempt's token (E5-03).
func PlanSweep(initial Status, idle time.Duration, attempt, maxAttempts int) (SweepOutcome, bool) {
	switch initial {
	case Dispatched, Preparing:
		if idle < contracts.DispatchedTimeout {
			return SweepOutcome{}, false
		}
		return SweepOutcome{
			TaskStatus: Failed, FailureKind: string(contracts.FailTimeout),
			Attempt: attempt, RuntimeState: "online", TokenRevoked: true,
		}, true
	case Running:
		if idle < contracts.HeartbeatExpiry {
			return SweepOutcome{}, false
		}
		o := SweepOutcome{
			FailureKind:  string(contracts.FailRuntimeOffline),
			RuntimeState: "offline", TokenRevoked: true,
		}
		if attempt < maxAttempts {
			o.TaskStatus, o.Attempt = Queued, attempt+1
		} else {
			o.TaskStatus, o.Attempt = Failed, attempt
		}
		return o, true
	}
	return SweepOutcome{}, false
}

// idleSince is how long the task has been silent, measured the way the sweep's
// two rules measure it: from dispatch for an unclaimed task, from the last
// heartbeat for a running one.
func idleSince(t *Row, now time.Time) time.Duration {
	var last time.Time
	switch t.Status {
	case Running:
		for _, c := range []*time.Time{t.HeartbeatAt, t.StartedAt, t.DispatchedAt} {
			if c != nil {
				last = *c
				break
			}
		}
	default:
		if t.DispatchedAt != nil {
			last = *t.DispatchedAt
		}
	}
	if last.IsZero() {
		last = t.CreatedAt
	}
	return now.Sub(last)
}
