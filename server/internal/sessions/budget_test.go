package sessions

import "testing"

// TestEffectiveTaskLimitIsAMinimum is D-16 (daemon-protocol v0.7.1 §4.4). The
// priority scheme it replaced — override > session > task — could return a
// number ABOVE what the session had left, so the last lane of a session was
// handed a limit the session could not pay.
//
// It is a plain unit row rather than a golden one because the golden table's
// budget cases are scoped ("task" or "session") and never state the crossing
// itself: min() is a property of the two numbers, and this is where it is
// pinned.
func TestEffectiveTaskLimitIsAMinimum(t *testing.T) {
	for _, c := range []struct {
		name                             string
		limit, override, remaining, want float64
	}{
		{"the session remainder is tighter than the task ceiling", 5, 0, 0.4, 0.4},
		{"…and tighter than an approved raise", 1, 3, 0.4, 0.4},
		{"an approved raise wins while the session can pay it", 1, 3, 10, 3},
		{"a session with no budget does not pin the task to zero", 5, 0, 0, 5},
		{"an agent with no per-task budget still obeys the session", 0, 0, 0.4, 0.4},
	} {
		if got := EffectiveTaskLimit(c.limit, c.override, c.remaining); got != c.want {
			t.Errorf("%s: EffectiveTaskLimit(%v, %v, %v) = %v, want %v (D-16)",
				c.name, c.limit, c.override, c.remaining, got, c.want)
		}
	}
}
