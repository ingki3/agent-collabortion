// Budget enforcement inside one attempt — EVAL E9-01, E9-05, E9-06, E9-08.
//
// 이월됨(E9-06): server/internal/sessions/budget_golden_test.go 의
// `cliFallbackArgs` 행. 어댑터 명령줄은 harness §1 대로 데몬 소유라 서버 골든이
// 부를 수 없다. 기대값은 한 글자도 바꾸지 않았고 어댑터만 데몬 API 다(§0-8).
package acp_test

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

type cliBudgetArgs struct {
	Path string // "acp" | "cli"
	Args []string
}

// ADAPTER — production caller: acp.CLIFallback, the §8.2.4 argv builder.
func cliFallbackArgs(runtimeKind string, budgetUSD float64) cliBudgetArgs {
	inv := acp.CLIFallback(contracts.RuntimeKind(runtimeKind), acp.CLIFallbackOptions{
		Model: "sonnet", BudgetUSD: budgetUSD, MaxTurns: 20,
	})
	return cliBudgetArgs{Path: inv.Path, Args: inv.Args}
}

func TestCliBudgetFlagGolden(t *testing.T) {
	t.Run(caseNameP3("E9-06", "the_claude_code_cli_fallback_passes_max_budget_usd"), func(t *testing.T) {
		a := cliFallbackArgs("claude_code", 1.5)
		if a.Path != "cli" {
			t.Fatalf("path = %q, want cli", a.Path)
		}
		var found bool
		for i, arg := range a.Args {
			if arg == "--max-budget-usd" && i+1 < len(a.Args) {
				found = true
				if a.Args[i+1] != "1.5" {
					t.Errorf("--max-budget-usd %s, want 1.5", a.Args[i+1])
				}
			}
		}
		if !found {
			t.Errorf("argv = %v, missing --max-budget-usd — the runtime's own cap is the second "+
				"line of defence when our accumulation lags a turn (FR-7.3, §8.2.4)", a.Args)
		}
	})
}

// budgetBundle carries the three numbers §4.4 v0.7.1 reads: `session` is the
// session's REMAINING budget (`limits.budget_usd`), `override` the Director's
// approved raise, and `task` the agent's own `budget_per_task`. A 0 means "not
// set".
func budgetBundle(session float64, override *float64) contracts.TaskBundle {
	b := bundle(contracts.RuntimeClaudeCode)
	b.Limits.BudgetUSD = &session
	b.Task.BudgetOverrideUSD = override
	return b
}

func withTaskBudget(b contracts.TaskBundle, task float64) contracts.TaskBundle {
	b.Task.BudgetUSD = &task
	return b
}

// spend scripts one turn that reports `cost` as a MEASURED cost.
func spend(cost float64) acpfake.Script {
	return acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "spending"}},
		Usage: &acpfake.PromptUsage{InputTokens: 100, OutputTokens: 50, CostUSD: &cost},
	}}}
}

// budgetDetail returns the §4.4 feed line the attempt left, or "".
func budgetDetail(f *fixture) string {
	for _, e := range f.sink.find("runtime", "cancel", "info") {
		if d, _ := e.Payload["detail"].(string); strings.Contains(d, "paused_budget") {
			return d
		}
	}
	return ""
}

// E9-01 — a MEASURED overrun ends the attempt as paused_budget, not failed:
// going over budget is policy, and the Director raising the cap resumes the
// same lane and workdir (FR-7.3 M9, daemon-protocol §4.4).
func TestBudgetOverrunIsPausedNotFailed(t *testing.T) {
	cost := 1.01
	s := acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "spending"}},
		Usage: &acpfake.PromptUsage{InputTokens: 100, OutputTokens: 50, CostUSD: &cost},
	}}}
	f := newFixture(t, s, budgetBundle(1.0, nil), nil)
	res := f.run()
	if res.Outcome != "paused_budget" {
		t.Fatalf("outcome = %q, want paused_budget — a budget overrun is not a failure (E9-01)", res.Outcome)
	}
	if res.Failure != nil {
		t.Fatalf("failure = %+v, want none — §4.4 says paused_budget carries no failure_kind", res.Failure)
	}
	var said bool
	for _, e := range f.sink.find("runtime", "cancel", "info") {
		if d, _ := e.Payload["detail"].(string); strings.Contains(d, "paused_budget") {
			said = true
		}
	}
	if !said {
		t.Fatalf("the feed never says why the attempt stopped: %+v", f.sink.find("runtime", "cancel", ""))
	}
}

// E9-05 — no hard cut on an estimate. The ACP `usage` block carries tokens and
// NO cost (harness §7), so every turn on today's only path is `estimated`;
// without the guard the first task with a budget would end itself on a number
// the daemon invented.
func TestBudgetNeverCutsOnAnEstimate(t *testing.T) {
	s := acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "spending"}},
		Usage: &acpfake.PromptUsage{InputTokens: 900000, OutputTokens: 900000}, // no CostUSD
	}}}
	f := newFixture(t, s, budgetBundle(0.01, nil), nil)
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("outcome = %q, want completed — an estimated cost never hard-cuts (E9-05)", res.Outcome)
	}
	if !res.Usage.Estimated {
		t.Fatalf("usage %+v, want estimated:true (harness v0.7.1)", res.Usage)
	}
}

// E9-08 — the approved override is read AT ENFORCEMENT TIME. Storing it and
// then enforcing the agent's original budget puts the task straight back into
// paused(budget) the moment it resumes.
func TestBudgetOverrideWinsAtEnforcementTime(t *testing.T) {
	cost := 1.5
	over := 3.0
	s := acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "spending"}},
		Usage: &acpfake.PromptUsage{InputTokens: 10, OutputTokens: 10, CostUSD: &cost},
	}}}
	// The agent's original $1 rides on `task.budget_usd`, not on
	// `limits.budget_usd`: §4.4 v0.7.1 calls the latter the SESSION's
	// remaining budget, and a session with $1 left would (correctly) pause
	// this attempt no matter what the task's override says. The row's
	// expectation is unchanged — an approved override beats the agent's own
	// per-task budget at enforcement time.
	f := newFixture(t, s, withTaskBudget(budgetBundle(10.0, &over), 1.0), nil)
	if res := f.run(); res.Outcome != "completed" {
		t.Fatalf("outcome = %q, want completed — $1.50 is under the approved $3 override (E9-08)", res.Outcome)
	}
}

// D-16 / §4.4 v0.7.1 — the effective budget is min(task 상한, 세션 잔여), so an
// approved $3 override does NOT let one task spend a session that has $2 left.
// Under the old priority ladder (override > limits > task) it did: raising a
// task's cap silently raised the session's.
func TestSessionRemainingCapsAnApprovedOverride(t *testing.T) {
	over := 3.0
	f := newFixture(t, spend(2.4), budgetBundle(2.0, &over), nil)
	res := f.run()
	if res.Outcome != "paused_budget" {
		t.Fatalf("outcome = %q, want paused_budget — $2.40 is over the $2 the session has left, "+
			"and the $3 override cannot spend money the session does not have (D-16, §4.4 v0.7.1)", res.Outcome)
	}
	d := budgetDetail(f)
	if d == "" {
		t.Fatalf("the feed never says why the attempt stopped: %+v", f.sink.find("runtime", "cancel", ""))
	}
	if !strings.Contains(d, "세션 잔여") {
		t.Errorf("detail = %q, want it to name 세션 잔여 as the cap that bound — paused at $2 with a "+
			"$3 override approved, the Director cannot otherwise tell which cap to lift (D-16)", d)
	}
	if !strings.Contains(d, "$2.0000") {
		t.Errorf("detail = %q, want the enforced cap $2.0000, not the $3 override", d)
	}
}

// D-16 — the other side of the min(): no override, the agent's $1 per-task
// budget is nearer than the session's $5, so the task cap is what binds and
// what the feed names.
func TestTaskBudgetBindsWhenTheSessionHasRoom(t *testing.T) {
	f := newFixture(t, spend(1.2), withTaskBudget(budgetBundle(5.0, nil), 1.0), nil)
	res := f.run()
	if res.Outcome != "paused_budget" {
		t.Fatalf("outcome = %q, want paused_budget — $1.20 is over the agent's $1 per-task budget "+
			"even though the session has $5 left (D-16, §4.4 v0.7.1)", res.Outcome)
	}
	d := budgetDetail(f)
	if !strings.Contains(d, "task 상한") {
		t.Errorf("detail = %q, want it to name task 상한 as the cap that bound (D-16)", d)
	}
	if !strings.Contains(d, "$1.0000") {
		t.Errorf("detail = %q, want the enforced cap $1.0000, not the session's $5", d)
	}
}
