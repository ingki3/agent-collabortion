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

// TestHistoryLimitMatchesEval is S-38. The golden table injects its own
// HistoryLimit (resume_golden_test.go passes 50), so nothing ever compared the
// constant the production path actually uses with the contract — it sat at 30
// through a green E8-12. This row is the comparison.
func TestHistoryLimitMatchesEval(t *testing.T) {
	if DefaultHistoryLimit != 50 {
		t.Fatalf("DefaultHistoryLimit = %d, want 50 (EVAL E8-12, S-38). queue.buildBundle takes its "+
			"history cap from here, so a wrong number here is a wrong prompt everywhere",
			DefaultHistoryLimit)
	}
}
