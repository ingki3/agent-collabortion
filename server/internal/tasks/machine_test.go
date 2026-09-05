package tasks

import (
	"errors"
	"testing"
)

func TestTransitions(t *testing.T) {
	allowed := [][2]Status{
		{Queued, Dispatched}, {Dispatched, Preparing}, {Preparing, Running}, {Running, Completed},
		{Running, Failed}, {Running, WaitingHuman}, {WaitingHuman, Queued}, {Dispatched, Queued},
		{Running, Queued}, {Paused, Queued}, {Deferred, Queued},
	}
	for _, p := range allowed {
		if _, err := Transition(p[0], p[1]); err != nil {
			t.Errorf("%s → %s should be allowed: %v", p[0], p[1], err)
		}
	}
	denied := [][2]Status{
		{WaitingHuman, Running}, // E5-01: the only exit is queued
		{WaitingHuman, Completed},
		{Completed, Running}, {Failed, Queued}, {Queued, Running}, {Cancelled, Queued},
	}
	for _, p := range denied {
		_, err := Transition(p[0], p[1])
		if !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s → %s should be rejected, got %v", p[0], p[1], err)
		}
	}
}
