package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

// TestBudgetRemainderTable is #292 review NN1: remainder() is the half of the
// task's effective ceiling that is not the task's own — min(mission
// remainder, room remainder), zero meaning "no ceiling" on either side. It is
// a pure function over budgetState, and before this table nothing measured it:
// dropping the mission term (w) left the whole suite green.
//
// (w, r) are the mission's and the room's remainders for this task.
func TestBudgetRemainderTable(t *testing.T) {
	work := uuid.New()
	for _, c := range []struct {
		name      string
		w, r      float64
		want      float64
		wantScope string
	}{
		{"둘 다 상한 없음", 0, 0, 0, ""},
		{"미션만 상한", 5, 0, 5, scopeWork},
		{"방만 상한", 0, 5, 5, scopeRoom},
		{"미션이 더 작다", 3, 5, 3, scopeWork},
		{"방이 더 작다", 5, 3, 3, scopeRoom},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A task that has spent nothing itself, so each remainder is its
			// limit minus the others' spend (0 here) — i.e. the limit.
			b := &budgetState{WorkID: &work, WorkLimitUSD: c.w, SessionLimitUSD: c.r}
			got, scope := b.remainder()
			if got != c.want || scope != c.wantScope {
				t.Fatalf("remainder(w=%v, r=%v) = (%v, %q), want (%v, %q)", c.w, c.r, got, scope, c.want, c.wantScope)
			}
		})
	}
}
