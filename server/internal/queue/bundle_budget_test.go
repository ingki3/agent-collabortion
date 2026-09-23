package queue

import (
	"testing"

	"github.com/google/uuid"
)

// TestSessionRemainingBudgetOmitsWhenUnset is D-18's server half: a session
// with no budget must produce an ABSENT `limits.budget_usd`, not a zero.
//
// The daemon reads the absence as "nothing to enforce on this attempt" and
// skips the mid-turn usage stream (4× messages, 2× bytes). A zero would read as
// a budget of zero — every turn over its limit before it starts — and would
// keep the expensive stream on for exactly the sessions that do not need it.
func TestSessionRemainingBudgetOmitsWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit *float64
	}{
		{"no session budget", nil},
		{"a zero budget is not a budget", f64(0)},
		{"a negative budget is not a budget", f64(-1)},
	} {
		id := uuid.New()
		if got := remainingBudget(nil, nil, `t.session_id = $1`, &id, tc.limit); got != nil {
			t.Errorf("%s: remaining = %v, want nil (omitted from the bundle)", tc.name, *got)
		}
	}
	// PRD v0.19 FR-2A.3: the bundle carries min(미션 잔여, 방 잔여), and two
	// absent ceilings are still absent — never a zero.
	if got := minRemaining(nil, nil); got != nil {
		t.Errorf("min(no mission budget, no room budget) = %v, want nil", *got)
	}
	if got := minRemaining(f64(0.4), nil); got == nil || *got != 0.4 {
		t.Errorf("min(0.4, none) = %v, want 0.4", got)
	}
	if got := minRemaining(f64(2), f64(0.4)); got == nil || *got != 0.4 {
		t.Errorf("min(2, 0.4) = %v, want 0.4 — the room's remainder binds", got)
	}
}

func f64(v float64) *float64 { return &v }
