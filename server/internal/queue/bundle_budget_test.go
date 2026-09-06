package queue

import "testing"

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
		if got := sessionRemainingBudget(nil, nil, [16]byte{}, tc.limit); got != nil {
			t.Errorf("%s: remaining = %v, want nil (omitted from the bundle)", tc.name, *got)
		}
	}
}

func f64(v float64) *float64 { return &v }
