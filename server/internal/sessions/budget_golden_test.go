//go:build p3golden

// Golden table for budgets (EVAL E9, 7 rows) — PRD FR-7.3 (비용 집계와 강제,
// M9·C2′), FR-2.2 (예산 소진은 종료가 아니라 paused), PRD §8.2.2 (취소 절차),
// contracts/daemon-protocol.md §4.3·§4.4 (`cancel reason=budget`,
// `outcome: paused_budget`) and openapi `respondHitlRequest` ·
// `getSessionCost`.
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - Exceeding a budget is a POLICY event, not an error. The task lands on
//     `paused(budget)`, never `failed` — the difference is whether the work can
//     be resumed after the Director raises the limit (FR-7.3 M9).
//   - A raised limit applies to THAT TASK only (`task.budget_override`). The
//     agent's `budget_per_task` is untouched, or one HITL click silently
//     re-prices every future session (FR-7.3 C2′).
//   - Rejecting the raise does NOT kill the task. It stays parked until a human
//     presses 중단 (E9-03) — an implementation that fails it on rejection
//     destroys the workdir state the Director may still want.
//   - An ESTIMATED cost never hard-cuts. `usage=false` runtimes pause the
//     session and drain the turn instead (FR-7.3 마지막 bullet, E9-05).
//   - The budget HITL is `source: system` but MUST carry `task_id` — it is the
//     only handle on which task to resume (FR-7.3, s-13).
//
// HOW THIS FILE FAILS TODAY. `tasks.PlanDispatch`/`PauseSessionTasks` already
// cancel a running turn on a budget pause (gate.go, E5-07) and `internal/cost`
// already prices estimates, but nothing enforces a budget DURING a turn, and
// `respondHitlRequest` refuses `budget_override_usd` outright
// (handlers_hitl.go:84). Every hook below is nil until T-S5 wires it.
package sessions

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// caseNameP3 keeps the EVAL id in the test name (see the P2 helper).
func caseNameP3(eval, name string) string { return caseName(eval, name) }

var (
	p3AgentR   = uuid.MustParse("a0000000-0000-4000-8000-000000000002")
	p3TaskR1   = uuid.MustParse("c0000000-0000-4000-8000-000000000002")
	p3LaneR1   = uuid.MustParse("d0000000-0000-4000-8000-000000000002")
	p3Director = uuid.MustParse("b0000000-0000-4000-8000-000000000001")
	p3Runtime  = uuid.MustParse("f0000000-0000-4000-8000-00000000000a")
)

// ---------------------------------------------------------------------------
// 1. In-turn enforcement — E9-01, E9-04, E9-05
// ---------------------------------------------------------------------------

// budgetCase is one accumulation of usage during a running turn.
type budgetCase struct {
	// Scope is which limit was crossed: "task" (agent budget_per_task, E9-01)
	// or "session" (session limits.budget_usd, E9-04).
	Scope string

	TaskLimitUSD    float64
	SessionLimitUSD float64
	// SpentUSD is the running total the daemon reported via usage_update.
	SpentUSD float64

	// Estimated is FR-7.3's `usage=false` case: the number is our own guess
	// from the workspace price table, not a runtime measurement (E9-05).
	Estimated bool

	// Override, when non-zero, is a previously approved task.budget_override.
	OverrideUSD float64
}

// budgetOutcome is what the server must do when the limit is crossed.
type budgetOutcome struct {
	TaskStatus         string // paused | failed | running
	PausedReason       string
	LaneStatus         string
	SessionState       string
	SessionPauseReason string

	// CancelCommand describes the daemon command (daemon-protocol §4.3). A
	// budget stop goes through the §8.2.2 procedure, never a kill.
	CancelCommandIssued    bool
	CancelReason           string
	CancelAfterCurrentTool bool
	// HardCut is true when the implementation killed the turn outright rather
	// than draining it (E9-05 forbids this for estimates).
	HardCut bool
	// TurnDrained is the estimate path: the running turn is allowed to finish.
	TurnDrained bool

	// The system HITL the Director answers.
	HitlIssued  bool
	HitlSource  string
	HitlType    string
	HitlPurpose string
	HitlTaskID  uuid.UUID

	// QueuedDispatched counts tasks handed out after the pause. Must be 0.
	QueuedDispatched int
	DirectorNotified bool
}

// enforceBudget is wired by T-S5. See the P3a hand-off report, "required API".
var enforceBudget func(c budgetCase) budgetOutcome

func mustEnforce(t *testing.T, c budgetCase) budgetOutcome {
	t.Helper()
	if enforceBudget == nil {
		t.Fatalf("unimplemented: in-turn budget enforcement (FR-7.3 M9 '턴 중 강제'). " +
			"T-S5 must wire `enforceBudget` — see the P3a hand-off report 'required API'")
	}
	return enforceBudget(c)
}

func TestTaskBudgetEnforcementGolden(t *testing.T) {
	t.Run(caseNameP3("E9-01", "task_budget_overrun_pauses_the_task_it_does_not_fail_it"), func(t *testing.T) {
		o := mustEnforce(t, budgetCase{Scope: "task", TaskLimitUSD: 1, SpentUSD: 1.01})

		if o.TaskStatus == "failed" {
			t.Fatal("a budget overrun is a POLICY event, not an error — `failed` throws away work " +
				"the Director can still authorise (FR-7.3 M9)")
		}
		if o.TaskStatus != "paused" || o.PausedReason != "budget" {
			t.Errorf("task = %q(%q), want paused(budget)", o.TaskStatus, o.PausedReason)
		}
		if o.LaneStatus != "paused" {
			t.Errorf("lane = %q, want paused (E9-01)", o.LaneStatus)
		}
		if !o.CancelCommandIssued || o.CancelReason != "budget" {
			t.Errorf("cancel command issued = %t reason = %q, want a cancel with reason=budget "+
				"(daemon-protocol §4.3)", o.CancelCommandIssued, o.CancelReason)
		}
		if !o.CancelAfterCurrentTool {
			t.Error("the cancel follows §8.2.2 — the current tool call finishes first (max 30s), " +
				"so a half-written file is not left behind")
		}
		if !o.HitlIssued || o.HitlSource != "system" {
			t.Error("the platform asks the Director whether to continue (source: system, FR-7.3)")
		}
		if o.HitlTaskID != p3TaskR1 {
			t.Errorf("hitl task_id = %s, want %s — a task-budget HITL MUST name its task or there is "+
				"nothing to resume (FR-7.3 s-13)", o.HitlTaskID, p3TaskR1)
		}
		if o.HitlType != "approval" {
			t.Errorf("hitl type = %q, want approval", o.HitlType)
		}
		if o.HitlPurpose != "budget" {
			t.Errorf("hitl purpose = %q, want budget — `source: system` + `approval` alone cannot be "+
				"told apart from the completion approval (migration 0012, handlers_hitl.go:191)", o.HitlPurpose)
		}
	})

	t.Run(caseNameP3("E9-04", "session_budget_overrun_pauses_the_session_and_stops_dispatch"), func(t *testing.T) {
		o := mustEnforce(t, budgetCase{Scope: "session", SessionLimitUSD: 0.5, SpentUSD: 0.6})

		if o.SessionState != "paused" || o.SessionPauseReason != "budget" {
			t.Errorf("session = %q(%q), want paused(budget) (FR-2.2: a spent budget is not completion)",
				o.SessionState, o.SessionPauseReason)
		}
		if !o.CancelCommandIssued {
			t.Error("the running turn is cancelled — draining it spends past the limit the pause " +
				"exists to hold (E5-07, E9-04)")
		}
		if o.QueuedDispatched != 0 {
			t.Errorf("dispatched = %d, want 0 while paused (E5-04)", o.QueuedDispatched)
		}
		if o.HitlTaskID != uuid.Nil {
			t.Errorf("hitl task_id = %s, want empty — a SESSION budget HITL is answered by resuming "+
				"the session, not one task (openapi resumeSession)", o.HitlTaskID)
		}
	})

	t.Run(caseNameP3("E9-05", "an_estimated_cost_pauses_and_drains_it_never_hard_cuts"), func(t *testing.T) {
		o := mustEnforce(t, budgetCase{
			Scope: "session", SessionLimitUSD: 1, SpentUSD: 1.0, Estimated: true,
		})
		if o.HardCut {
			t.Fatal("an estimate must not kill a turn: the number is our own guess from the price " +
				"table, and killing real work on a wrong guess is the failure FR-7.3 names")
		}
		if !o.TurnDrained {
			t.Error("the running turn is allowed to finish (drain) — only new dispatch stops")
		}
		if o.SessionState != "paused" {
			t.Errorf("session = %q, want paused — the pause still happens, the kill does not",
				o.SessionState)
		}
		if !o.DirectorNotified {
			t.Error("the Director is told (FR-7.3 '세션을 paused로 만들고 Director에게 알린다')")
		}
	})

	t.Run(caseNameP3("E9-01", "an_approved_override_raises_the_ceiling_for_this_task"), func(t *testing.T) {
		// The mirror of E9-01: with an override in force the same spend must
		// NOT trip the limit, or an approved continuation pauses immediately
		// again and the approval is meaningless.
		o := mustEnforce(t, budgetCase{
			Scope: "task", TaskLimitUSD: 1, OverrideUSD: 3, SpentUSD: 1.01,
		})
		if o.TaskStatus == "paused" {
			t.Error("with budget_override = $3 a spend of $1.01 is inside the limit — the override " +
				"is what the enforcement reads (FR-7.3 C2′, E9-02)")
		}
		if o.CancelCommandIssued {
			t.Error("no cancel while inside the effective limit")
		}
	})
}

// ---------------------------------------------------------------------------
// 2. The Director's answer — E9-02, E9-03
// ---------------------------------------------------------------------------

// budgetAnswer is the Director's response to the budget HITL.
type budgetAnswer struct {
	Approved bool
	// NewLimitUSD is the contract's `budget_override_usd`.
	NewLimitUSD float64
	Reason      string
}

// budgetResumeResult is the state after the answer.
type budgetResumeResult struct {
	TaskStatus   string
	PausedReason string
	// TaskBudgetOverride is what landed on task.budget_override.
	TaskBudgetOverride float64
	// AgentBudgetPerTask must be UNCHANGED (C2′).
	AgentBudgetPerTask float64

	LaneID  uuid.UUID
	Workdir string
	// ResumeAttempted: the same resume path as every other re-entry (FR-5.4).
	ResumeAttempted bool
	// NewTriggerRequired must be false — the Director's approval is the
	// trigger, the agent is not waiting to be mentioned again (FR-7.3).
	NewTriggerRequired bool

	HitlStatus string
}

var answerBudgetHitl func(a budgetAnswer) budgetResumeResult

func mustAnswerBudget(t *testing.T, a budgetAnswer) budgetResumeResult {
	t.Helper()
	if answerBudgetHitl == nil {
		t.Fatalf("unimplemented: budget HITL response (FR-7.3 C2′, openapi respondHitlRequest " +
			"`budget_override_usd` — refused today at handlers_hitl.go:84). " +
			"T-S5 must wire `answerBudgetHitl` — see the P3a hand-off report 'required API'")
	}
	return answerBudgetHitl(a)
}

func TestBudgetHitlAnswerGolden(t *testing.T) {
	t.Run(caseNameP3("E9-02", "an_approved_raise_is_task_scoped_and_resumes_the_same_lane_and_workdir"), func(t *testing.T) {
		r := mustAnswerBudget(t, budgetAnswer{Approved: true, NewLimitUSD: 3})

		if r.TaskBudgetOverride != 3 {
			t.Errorf("task.budget_override = %v, want 3", r.TaskBudgetOverride)
		}
		if r.AgentBudgetPerTask != 1 {
			t.Errorf("agent.budget_per_task = %v, want 1 — a single HITL click must NOT re-price "+
				"every future session (FR-7.3 C2′)", r.AgentBudgetPerTask)
		}
		if r.TaskStatus != "queued" {
			t.Errorf("task = %q, want queued — approval re-queues it (FR-7.3 'Director가 계속을 "+
				"승인하면 해당 task를 queued로 되돌린다')", r.TaskStatus)
		}
		if r.NewTriggerRequired {
			t.Error("no new trigger is needed; the approval IS the trigger")
		}
		if r.LaneID != p3LaneR1 {
			t.Errorf("lane = %s, want the same lane %s", r.LaneID, p3LaneR1)
		}
		if r.Workdir != "/w/lane-r1" {
			t.Errorf("workdir = %q, want the same workdir — resuming elsewhere loses the work the "+
				"pause was protecting", r.Workdir)
		}
		if !r.ResumeAttempted {
			t.Error("the budget resume uses the same resume-first path as HITL and retries (FR-7.3 M9)")
		}
		if r.HitlStatus != "answered" {
			t.Errorf("hitl status = %q, want answered", r.HitlStatus)
		}
	})

	t.Run(caseNameP3("E9-03", "a_rejected_raise_leaves_the_task_parked_not_failed"), func(t *testing.T) {
		r := mustAnswerBudget(t, budgetAnswer{Approved: false, Reason: "여기까지만"})

		if r.TaskStatus == "failed" {
			t.Fatal("rejecting the raise does NOT fail the task (E9-03 asks this question and " +
				"answers 아니다) — only the explicit 중단 button ends it as cancelled")
		}
		if r.TaskStatus == "cancelled" {
			t.Fatal("nor is it cancelled: cancellation is a separate, deliberate act (openapi cancelLane)")
		}
		if r.TaskStatus != "paused" || r.PausedReason != "budget" {
			t.Errorf("task = %q(%q), want paused(budget) still", r.TaskStatus, r.PausedReason)
		}
		if r.TaskBudgetOverride != 0 {
			t.Errorf("budget_override = %v, want 0 — nothing was approved", r.TaskBudgetOverride)
		}
		if r.HitlStatus != "answered" {
			t.Errorf("hitl status = %q, want answered — the rejection IS an answer", r.HitlStatus)
		}
	})
}

// ---------------------------------------------------------------------------
// 3. The CLI fallback carries the runtime's own budget flag — E9-06
// ---------------------------------------------------------------------------

type cliBudgetArgs struct {
	Path string // "acp" | "cli"
	Args []string
}

var cliFallbackArgs func(runtimeKind string, budgetUSD float64) cliBudgetArgs

func TestCliBudgetFlagGolden(t *testing.T) {
	t.Run(caseNameP3("E9-06", "the_claude_code_cli_fallback_passes_max_budget_usd"), func(t *testing.T) {
		if cliFallbackArgs == nil {
			t.Fatalf("unimplemented: CLI fallback argv (PRD §8.2.4, E9-06). T-S5/T-D5 must wire " +
				"`cliFallbackArgs` — see the P3a hand-off report")
		}
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

// ---------------------------------------------------------------------------
// 4. Four-level cost rollup with the estimate badge — E9-07
// ---------------------------------------------------------------------------

// costBucket mirrors the contract's CostBucket.
type costBucket struct {
	ID        uuid.UUID
	Name      string
	CostUSD   float64
	Estimated bool
}

// costReport mirrors the contract's CostReport: the four buckets FR-7.3 names.
type costReport struct {
	TotalUSD  float64
	Estimated bool

	ByTask    []costBucket
	ByAgent   []costBucket
	BySession []costBucket
	ByRuntime []costBucket
}

// usageRow is one task's usage as stored.
type usageRow struct {
	TaskID    uuid.UUID
	AgentID   uuid.UUID
	RuntimeID uuid.UUID
	CostUSD   float64
	Estimated bool
}

var rollupCost func(rows []usageRow) costReport

func TestCostRollupGolden(t *testing.T) {
	t.Run(caseNameP3("E9-07", "cost_rolls_up_by_task_agent_session_and_runtime_with_an_estimate_badge"), func(t *testing.T) {
		if rollupCost == nil {
			t.Fatalf("unimplemented: cost aggregation (FR-7.3, openapi getSessionCost). " +
				"T-S5 must wire `rollupCost` — see the P3a hand-off report")
		}
		rows := []usageRow{
			{TaskID: p3TaskR1, AgentID: p3AgentR, RuntimeID: p3Runtime, CostUSD: 0.40, Estimated: false},
			{TaskID: uuid.MustParse("c0000000-0000-4000-8000-000000000003"),
				AgentID: p3AgentR, RuntimeID: p3Runtime, CostUSD: 0.10, Estimated: true},
		}
		r := rollupCost(rows)

		if len(r.ByTask) != 2 {
			t.Errorf("by_task buckets = %d, want 2 (FR-7.3 names four units: task·agent·session·runtime)",
				len(r.ByTask))
		}
		if len(r.ByAgent) != 1 || len(r.ByRuntime) != 1 || len(r.BySession) != 1 {
			t.Errorf("agent/runtime/session buckets = %d/%d/%d, want 1/1/1",
				len(r.ByAgent), len(r.ByRuntime), len(r.BySession))
		}
		if r.TotalUSD < 0.4999 || r.TotalUSD > 0.5001 {
			t.Errorf("total = %v, want 0.50 — measured and estimated rows SUM; dropping the "+
				"estimated one understates the bill", r.TotalUSD)
		}
		if !r.Estimated {
			t.Error("one estimated row makes the whole report estimated — the badge is monotonic, " +
				"a mixed sum cannot be presented as measured (FR-7.3)")
		}
	})

	t.Run(caseNameP3("E9-07", "an_all_measured_report_is_not_badged_estimated"), func(t *testing.T) {
		if rollupCost == nil {
			t.Fatalf("unimplemented: see the row above")
		}
		r := rollupCost([]usageRow{
			{TaskID: p3TaskR1, AgentID: p3AgentR, RuntimeID: p3Runtime, CostUSD: 0.40, Estimated: false},
		})
		if r.Estimated {
			t.Error("an always-true badge tells the reader nothing — with every row measured the " +
				"report is measured")
		}
	})
}

// fixtures the wiring PR consumes.
var _ = []uuid.UUID{p3Director}
var _ = time.Minute
