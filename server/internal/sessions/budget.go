package sessions

import (
	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Budget enforcement (PRD FR-7.3 M9·C2′, FR-2.2, §8.2.2,
// contracts/daemon-protocol.md §4.3·§4.4).
//
// Exceeding a budget is a POLICY event, not an error. The difference between
// `paused(budget)` and `failed` is whether the work can be resumed after the
// Director raises the limit — and a `failed` task has already thrown away the
// answer to that question.

// BudgetInput is one usage accumulation during a running turn.
type BudgetInput struct {
	// Scope is the limit that was crossed: "task" (the agent's
	// budget_per_task) or "session" (session.limits.budget_usd).
	Scope string

	TaskLimitUSD    float64
	SessionLimitUSD float64
	SpentUSD        float64

	// OverrideUSD is a previously approved task.budget_override. It is read
	// HERE, at enforcement time — storing it and still enforcing against the
	// agent's budget_per_task means the Director's approval buys nothing and
	// the task pauses again the moment it resumes (E9-08).
	OverrideUSD float64

	// Estimated is FR-7.3's `usage=false` runtime: the number is our own guess
	// from the workspace price table, not a measurement (E9-05).
	Estimated bool

	TaskID uuid.UUID
}

// BudgetOutcome is what the server does when the limit is crossed.
type BudgetOutcome struct {
	Exceeded bool

	TaskStatus         string
	PausedReason       string
	LaneStatus         string
	SessionState       string
	SessionPauseReason string

	CancelCommandIssued    bool
	CancelReason           string
	CancelAfterCurrentTool bool
	// HardCut is killing the turn outright instead of draining it. It is never
	// true for an estimate (E9-05).
	HardCut bool
	// TurnDrained is the estimate path: the running turn finishes; only new
	// dispatch stops.
	TurnDrained bool

	HitlIssued  bool
	HitlSource  string
	HitlType    string
	HitlPurpose string
	// HitlTaskID is filled for a TASK budget and empty for a SESSION one: the
	// task request is answered by resuming that task, the session one by
	// resuming the session (FR-7.3 s-13, openapi resumeSession).
	HitlTaskID uuid.UUID

	// QueuedDispatched is how many tasks are handed out after the pause. Zero
	// (E5-04) — a paused session that keeps dispatching spends exactly what the
	// pause exists to stop.
	QueuedDispatched int
	DirectorNotified bool
}

// EffectiveTaskLimit is FR-7.3 C2′: the raise applies to THIS task, and it is
// what enforcement compares against.
func EffectiveTaskLimit(limit, override float64) float64 {
	if override > 0 {
		return override
	}
	return limit
}

// PlanBudget decides one enforcement point.
//
// Production caller: httpapi.Server.enforceBudget, called from the daemon
// heartbeat (daemon-protocol §4.2 `usage`) and from tasks.Finish's usage
// rollup.
func PlanBudget(in BudgetInput) BudgetOutcome {
	o := BudgetOutcome{SessionState: "active", TaskStatus: "running"}
	limit := in.SessionLimitUSD
	if in.Scope == "task" {
		limit = EffectiveTaskLimit(in.TaskLimitUSD, in.OverrideUSD)
	}
	if limit <= 0 || in.SpentUSD < limit {
		return o
	}
	o.Exceeded = true
	o.DirectorNotified = true
	o.QueuedDispatched = 0

	if in.Estimated {
		// The number is our own guess from the price table. Killing real work
		// on a wrong guess is the failure FR-7.3 names, so the turn drains and
		// only new dispatch stops.
		o.TurnDrained = true
		o.SessionState, o.SessionPauseReason = "paused", PauseBudget
		o.TaskStatus, o.PausedReason = "running", ""
		return o
	}

	// §8.2.2: the current tool call finishes first (max 30s), so a half-written
	// file is not left behind. That is a cancel COMMAND to the daemon, never a
	// kill from here (daemon-protocol §4.3).
	o.CancelCommandIssued = true
	o.CancelReason = PauseBudget
	o.CancelAfterCurrentTool = tasks.PlanCancelCommand(PauseBudget).AfterCurrentTool

	o.HitlIssued = true
	o.HitlSource = "system"
	o.HitlType = hitl.KindApproval
	// `source: system` + `approval` alone cannot be told apart from the
	// completion approval — three different pauses share the pair, which is why
	// 0012 stores `purpose` (E9-01).
	o.HitlPurpose = hitl.PurposeBudget

	switch in.Scope {
	case "task":
		o.TaskStatus, o.PausedReason = "paused", PauseBudget
		o.LaneStatus = "paused"
		o.HitlTaskID = in.TaskID
	default:
		o.SessionState, o.SessionPauseReason = "paused", PauseBudget
		o.TaskStatus, o.PausedReason = "paused", PauseBudget
		o.LaneStatus = "paused"
		// HitlTaskID stays empty.
	}
	return o
}

// ---------------------------------------------------------------------------
// The Director's answer (E9-02, E9-03)
// ---------------------------------------------------------------------------

// BudgetAnswerInput is the response plus the state it applies to. The lane, the
// workdir and the agent's own budget are inputs because the row is about them
// NOT changing.
type BudgetAnswerInput struct {
	Approved    bool
	NewLimitUSD float64
	Reason      string

	TaskID  uuid.UUID
	LaneID  uuid.UUID
	Workdir string
	// AgentBudgetPerTask must come out unchanged: one HITL click may not
	// re-price every future session (FR-7.3 C2′).
	AgentBudgetPerTask float64
	SessionRef         string
	RuntimeKind        string
	Attempt            int
}

// BudgetResume is the state after the answer.
type BudgetResume struct {
	TaskStatus         string
	PausedReason       string
	TaskBudgetOverride float64
	AgentBudgetPerTask float64

	LaneID  uuid.UUID
	Workdir string
	// ResumeAttempted: the budget resume is the SAME resume-first path as a
	// HITL answer and a retry (FR-5.4, FR-7.3 M9).
	ResumeAttempted bool
	// NewTriggerRequired is false — the approval IS the trigger. The agent is
	// not waiting to be mentioned again.
	NewTriggerRequired bool

	HitlStatus string
}

// PlanBudgetAnswer applies the Director's verdict.
//
// Production caller: httpapi.answerAgentHitl (the budget branch) and
// tasks.Service.ResumeFromHuman.
func PlanBudgetAnswer(in BudgetAnswerInput) BudgetResume {
	r := BudgetResume{
		AgentBudgetPerTask: in.AgentBudgetPerTask,
		LaneID:             in.LaneID, Workdir: in.Workdir,
		HitlStatus: hitl.StatusAnswered,
	}
	if !in.Approved {
		// E9-03: rejecting does NOT fail or cancel the task. It stays parked
		// until a person presses 중단 — failing it here destroys the workdir
		// state the Director may still want.
		r.TaskStatus, r.PausedReason = "paused", PauseBudget
		return r
	}
	r.TaskBudgetOverride = in.NewLimitUSD
	p := tasks.PlanAttempt(tasks.AttemptInput{
		TaskID: in.TaskID, Attempt: in.Attempt, Cause: tasks.CauseBudgetApproved,
		SessionRef: in.SessionRef, RefRuntimeKind: in.RuntimeKind, ProfileRuntimeKind: in.RuntimeKind,
		PrevWorkdir: in.Workdir,
	})
	r.TaskStatus = p.TaskStatus
	r.Workdir = p.Workdir
	r.ResumeAttempted = p.ResumeAttempted
	return r
}
