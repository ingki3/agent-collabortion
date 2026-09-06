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
//     cost it has already accumulated against the §4.4 유효 예산 —
//     min(task 상한, 세션 잔여), see effectiveBudget. That is the §4.4
//     `paused_budget` outcome.
//
// Neither half hard-cuts on an ESTIMATE (E9-05). `usage.Estimated` is true
// whenever any turn reported no cost of its own, which on the ACP path is
// always — so `estimated` is not a rare corner here, it is the common case,
// and cutting on it would end turns on a number the daemon invented.

// budgetSide names the cap that bound, for the feed line: an attempt paused at
// $2 has to say WHICH $2 it was, or the Director cannot tell "raise this
// task's cap" from "the session is out of money".
const (
	sideTask     = "task 상한"
	sideOverride = "task 상한(승인 상향)"
	sideSession  = "세션 잔여"
)

// effectiveBudget is daemon-protocol §4.4 v0.7.1 (D-16): the cap this attempt
// is enforced against is
//
//	min(task 상한, 세션 잔여)
//
// where the task 상한 is `budget_override_usd` when the Director approved one
// (E9-08 — an override that is stored but not read at enforcement time puts
// the task straight back into paused(budget) the moment it resumes) and
// `budget_usd` (the agent's `budget_per_task`) otherwise, and the 세션 잔여 is
// `limits.budget_usd`, the session's REMAINING budget as the server resolved
// it into the bundle.
//
// The old priority ladder (override > limits > task) read the same three
// numbers and picked ONE, so an approved $3 override on a session with $2 left
// spent $3: raising a task's cap silently raised the session's. min() cannot
// do that — whichever cap is nearer ends the attempt, and both are still
// `paused_budget`, because both are policy the Director can lift.
//
// A cap of 0 (or absent) is "not set", not "no money": with neither set the
// attempt is unenforced, which is what a task with no budget has always meant.
func effectiveBudget(b contracts.TaskBundle) (limit float64, side string) {
	task, taskSide := 0.0, sideTask
	if v := b.Task.BudgetOverrideUSD; v != nil && *v > 0 {
		task, taskSide = *v, sideOverride
	} else if v := b.Task.BudgetUSD; v != nil && *v > 0 {
		task = *v
	}
	session := 0.0
	if v := b.Limits.BudgetUSD; v != nil && *v > 0 {
		session = *v
	}
	switch {
	case task > 0 && session > 0:
		// A tie is reported as the task cap: it is the number the Director
		// set on this task, and it is the one they would raise.
		if session < task {
			return session, sideSession
		}
		return task, taskSide
	case task > 0:
		return task, taskSide
	case session > 0:
		return session, sideSession
	}
	return 0, ""
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
	limit, side := effectiveBudget(r.a.Bundle)
	if limit <= 0 || r.usage.Estimated || r.usage.CostUSD < limit {
		return
	}
	if !r.budgetExceeded {
		r.budgetExceeded = true
		r.pendingBudgetNote = &budgetNote{Limit: limit, Side: side, Cost: r.usage.CostUSD}
	}
}

type budgetNote struct {
	Limit float64
	Side  string
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
		"detail": fmt.Sprintf("실측 비용 $%.4f 가 유효 예산 $%.4f 를 넘었다 — 넘긴 쪽은 %s "+
			"(유효 예산 = min(task 상한, 세션 잔여), §4.4 v0.7.1 D-16) — paused_budget (FR-7.3 M9, E9-01)",
			n.Cost, n.Limit, n.Side),
	})
}

// budgetHit reports whether a MEASURED overrun was recorded.
func (r *Runner) budgetHit() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.budgetExceeded
}
