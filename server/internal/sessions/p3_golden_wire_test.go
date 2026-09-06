//go:build p3golden

// Wiring for the E9 golden table (budgets).
//
// The file keeps its `p3golden` tag because ONE of its hooks — cliFallbackArgs
// (E9-06) — belongs to the daemon: PRD §8.2.4 defines the CLI fallback argv and
// neither contracts/daemon-protocol.md nor contracts/harness.md says the server
// builds it, so wiring a server-side builder to that hook would be a shadow
// hook (Lead's rule, T-S5 ask 2). Everything else here is server-owned and is
// measured by running `go test -tags p3golden ./internal/sessions/`.
//
// PRODUCTION CALL SITES:
//
//	enforceBudget    → PlanBudget         httpapi.Server.enforceBudget, from the
//	                                      daemon heartbeat's `usage` (§4.2) and
//	                                      from the finish rollup
//	answerBudgetHitl → PlanBudgetAnswer   httpapi.answerAgentHitl (budget branch)
//	                                      → tasks.ResumeFromHuman
//	rollupCost       → cost.Rollup        httpapi.GetSessionCost and
//	                                      httpapi.GetWorkspaceCost
//	cliFallbackArgs  → NOT WIRED          daemon (T-D5), see above
package sessions

import (
	"github.com/ingki3/agent-collabortion/server/internal/cost"
)

func init() {
	enforceBudget = adaptEnforceBudget
	answerBudgetHitl = adaptAnswerBudget
	rollupCost = adaptRollup
}

// The fixture the E9 rows state in prose: agent R's budget_per_task is $1 and
// its task runs on lane R1 in /w/lane-r1 with a live runtime session.
const (
	goldenAgentBudget = 1.0
	goldenWorkdir     = "/w/lane-r1"
	goldenSessionRef  = "sess-r1"
)

func adaptEnforceBudget(c budgetCase) budgetOutcome {
	o := PlanBudget(BudgetInput{
		Scope: c.Scope, TaskLimitUSD: c.TaskLimitUSD, SessionLimitUSD: c.SessionLimitUSD,
		SpentUSD: c.SpentUSD, Estimated: c.Estimated, OverrideUSD: c.OverrideUSD,
		TaskID: p3TaskR1,
	})
	return budgetOutcome{
		TaskStatus: o.TaskStatus, PausedReason: o.PausedReason, LaneStatus: o.LaneStatus,
		SessionState: o.SessionState, SessionPauseReason: o.SessionPauseReason,
		CancelCommandIssued: o.CancelCommandIssued, CancelReason: o.CancelReason,
		CancelAfterCurrentTool: o.CancelAfterCurrentTool,
		HardCut:                o.HardCut, TurnDrained: o.TurnDrained,
		HitlIssued: o.HitlIssued, HitlSource: o.HitlSource, HitlType: o.HitlType,
		HitlPurpose: o.HitlPurpose, HitlTaskID: o.HitlTaskID,
		QueuedDispatched: o.QueuedDispatched, DirectorNotified: o.DirectorNotified,
	}
}

func adaptAnswerBudget(a budgetAnswer) budgetResumeResult {
	r := PlanBudgetAnswer(BudgetAnswerInput{
		Approved: a.Approved, NewLimitUSD: a.NewLimitUSD, Reason: a.Reason,
		TaskID: p3TaskR1, LaneID: p3LaneR1, Workdir: goldenWorkdir,
		AgentBudgetPerTask: goldenAgentBudget,
		SessionRef:         goldenSessionRef, RuntimeKind: "claude_code", Attempt: 1,
	})
	return budgetResumeResult{
		TaskStatus: r.TaskStatus, PausedReason: r.PausedReason,
		TaskBudgetOverride: r.TaskBudgetOverride, AgentBudgetPerTask: r.AgentBudgetPerTask,
		LaneID: r.LaneID, Workdir: r.Workdir, ResumeAttempted: r.ResumeAttempted,
		NewTriggerRequired: r.NewTriggerRequired, HitlStatus: r.HitlStatus,
	}
}

func adaptRollup(rows []usageRow) costReport {
	in := make([]cost.UsageRow, 0, len(rows))
	for _, r := range rows {
		in = append(in, cost.UsageRow{
			TaskID: r.TaskID, AgentID: r.AgentID, RuntimeID: r.RuntimeID,
			CostUSD: r.CostUSD, Estimated: r.Estimated,
		})
	}
	rep := cost.Rollup(in)
	conv := func(bs []cost.Bucket) []costBucket {
		out := make([]costBucket, 0, len(bs))
		for _, b := range bs {
			out = append(out, costBucket{ID: b.ID, Name: b.Name, CostUSD: b.CostUSD, Estimated: b.Estimated})
		}
		return out
	}
	return costReport{
		TotalUSD: rep.TotalUSD, Estimated: rep.Estimated,
		ByTask: conv(rep.ByTask), ByAgent: conv(rep.ByAgent),
		BySession: conv(rep.BySession), ByRuntime: conv(rep.ByRuntime),
	}
}
