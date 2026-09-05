// Package tasks owns the task state machine (PRD FR-7.1) and the attempt
// lifecycle reported by the daemon (daemon-protocol §4.2, §4.4): phase,
// heartbeat, finish, requeue and the stale sweep. All time comes from the
// injected contracts/clock so tests advance it (E5-02, E5-03).
package tasks

import (
	"errors"
	"fmt"
)

// Status mirrors the task_status enum (0001_init.sql).
type Status string

const (
	Deferred     Status = "deferred"
	Queued       Status = "queued"
	Dispatched   Status = "dispatched"
	Preparing    Status = "preparing"
	Running      Status = "running"
	WaitingHuman Status = "waiting_human"
	Paused       Status = "paused"
	Completed    Status = "completed"
	Failed       Status = "failed"
	Cancelled    Status = "cancelled"
)

// ErrInvalidTransition is returned (wrapped) for a move the FR-7.1 diagram
// does not allow — e.g. waiting_human → running (E5-01).
var ErrInvalidTransition = errors.New("tasks: invalid transition")

// transitions is FR-7.1:
//
//	deferred → queued → dispatched → preparing → running → waiting_human | completed | failed | cancelled | paused
//	waiting_human → queued (only exit, N4) · paused → queued · retry: dispatched/preparing/running → queued
var transitions = map[Status][]Status{
	Deferred:     {Queued, Cancelled},
	Queued:       {Dispatched, Cancelled, Failed},
	Dispatched:   {Preparing, Running, Queued, Failed, Cancelled},
	Preparing:    {Running, Queued, Failed, Cancelled, Paused},
	Running:      {WaitingHuman, Completed, Failed, Cancelled, Paused, Queued},
	WaitingHuman: {Queued},
	Paused:       {Queued, Cancelled},
}

// CanTransition reports whether from → to is allowed.
func CanTransition(from, to Status) bool {
	for _, s := range transitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Transition returns to when from → to is allowed, else ErrInvalidTransition.
func Transition(from, to Status) (Status, error) {
	if !CanTransition(from, to) {
		return from, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, from, to)
	}
	return to, nil
}

// Terminal reports whether no further daemon report is expected.
func Terminal(s Status) bool {
	return s == Completed || s == Failed || s == Cancelled
}
