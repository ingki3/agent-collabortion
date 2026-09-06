package acp

import (
	"fmt"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Budget enforcement inside one attempt (PRD FR-7.3 M9, §8.2.2,
// daemon-protocol §4.4, EVAL E9-01·04·05·08).
//
// WHO ENFORCES WHAT, AND WHY IT IS SPLIT
//
// FR-7.3 asks for enforcement DURING a turn, not only between tasks, so a
// single runaway task cannot blow past the cap. On today's ACP path the
// daemon cannot do that alone: harness §7 measured that `usage_update` carries
// only the context window and the rate-limit block — no tokens and no cost —
// and the cost arrives exactly once, in the `session/prompt` response at turn
// end. So the split is:
//
//   - The daemon ships whatever usage it has on every heartbeat (§4.2), and
//     the SERVER — which owns the workspace price table (§8.2.6) and the
//     session's remaining budget, neither of which the daemon knows — judges
//     and sends `cancel {reason: "budget"}`. That command runs the ordinary
//     §5 procedure, so a budget cancel is drained, not killed.
//   - The daemon judges the one thing it can judge on its own: a MEASURED
//     cost it has already accumulated against `limits.budget_usd`. That is
//     the §4.4 `paused_budget` outcome.
//
// Neither half hard-cuts on an ESTIMATE (E9-05). `usage.Estimated` is true
// whenever any turn reported no cost of its own, which on the ACP path is
// always — so `estimated` is not a rare corner here, it is the common case,
// and cutting on it would end turns on a number the daemon invented.

// budgetLimit is the cap this attempt is enforced against: the task's approved
// override wins over the agent's per-task budget (E9-08 — an override that is
// stored but not read at enforcement time puts the task straight back into
// paused(budget) the moment it resumes), and `limits.budget_usd` is the
// server's already-resolved value.
func budgetLimit(b contracts.TaskBundle) float64 {
	if v := b.Task.BudgetOverrideUSD; v != nil && *v > 0 {
		return *v
	}
	if v := b.Limits.BudgetUSD; v != nil && *v > 0 {
		return *v
	}
	if v := b.Task.BudgetUSD; v != nil && *v > 0 {
		return *v
	}
	return 0
}

// noteBudget is called with r.mu held whenever usage changed. It records the
// crossing; the outcome is applied where the turn ends.
//
// The `Estimated` guard is the whole of E9-05 on this side: an attempt is
// never ended on a cost the daemon did not measure. It is not a corner case —
// `recordUsage` sets Estimated the moment any turn reports tokens without a
// cost, which is every turn on the ACP path today — so without the guard the
// first task with a budget would end itself on a fabricated number.
func (r *Runner) noteBudget() {
	limit := budgetLimit(r.a.Bundle)
	if limit <= 0 || r.usage.Estimated || r.usage.CostUSD < limit {
		return
	}
	if !r.budgetExceeded {
		r.budgetExceeded = true
		r.pendingBudgetNote = &budgetNote{Limit: limit, Cost: r.usage.CostUSD}
	}
}

type budgetNote struct {
	Limit float64
	Cost  float64
}

// flushBudgetNote emits whatever noteBudget recorded (it runs under the lock
// and cannot emit itself).
func (r *Runner) flushBudgetNote() {
	r.mu.Lock()
	n := r.pendingBudgetNote
	r.pendingBudgetNote = nil
	r.mu.Unlock()
	if n == nil {
		return
	}
	// class/verb/outcome are all from task_event.schema.json's enums and the
	// payload from the closed `runtime` $def — `usage` has no room for a
	// budget and `budget`/`exceeded` are not words the schema knows. The
	// overrun IS the reason the attempt stops, which is what §8.2.2 calls the
	// cancel procedure, so it belongs on the cancel line.
	r.emit("runtime", "cancel", "", "info", map[string]any{
		"runtime_kind": string(r.kind()),
		"detail": fmt.Sprintf("실측 비용 $%.4f 가 task 예산 $%.4f 를 넘었다 — paused_budget (FR-7.3 M9, E9-01)",
			n.Cost, n.Limit),
	})
}

// budgetHit reports whether a MEASURED overrun was recorded.
func (r *Runner) budgetHit() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.budgetExceeded
}
