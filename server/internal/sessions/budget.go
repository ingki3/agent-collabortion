package sessions

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
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

	// SessionRemainingUSD is what is left of the session budget for this task
	// (session limit minus what the OTHER tasks already spent). D-16 makes it
	// a ceiling on the task limit, so a $5 task inside a session with $0.40
	// left may not spend $5. Zero means the session has no budget at all.
	SessionRemainingUSD float64

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

	// Scope is which limit was crossed after D-16's min() has been applied:
	// "task" (the agent's budget_per_task) or "session". The caller needs it
	// to quote the right pair of numbers, and it is NOT the same question as
	// HitlTaskID — an ESTIMATED overrun of a task budget still pauses the
	// SESSION (E9-05 has no per-task drain), so the request is session-scoped
	// while the numbers that crossed are the task's (S-48).
	Scope string

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

// TaskCeiling is FR-7.3 C2′: the raise applies to THIS task, and an override
// REPLACES the agent's per-task budget rather than being compared with it —
// an approved raise below the default would otherwise be silently ignored.
func TaskCeiling(limit, override float64) float64 {
	if override > 0 {
		return override
	}
	return limit
}

// EffectiveTaskLimit is daemon-protocol v0.7.1 §4.4 (D-16): the effective
// budget is min(task ceiling, session remaining).
//
// The priority scheme it replaces (override > session > task) could hand a
// task a limit ABOVE what the session had left, so the last lane of a session
// spent past the session budget and the session pause arrived after the money
// was gone. `sessionRemaining <= 0` means the session carries no budget, not
// that it has none left — a session with no limit must not pin every task to
// zero.
//
// production callers: sessions.PlanBudget, httpapi.applyBudgetPause (the
// number written into paused_detail) and queue.buildBundle (the number the
// daemon enforces its own half against, §4.1 `limits.budget_usd`).
func EffectiveTaskLimit(limit, override, sessionRemaining float64) float64 {
	ceiling := TaskCeiling(limit, override)
	if sessionRemaining > 0 && (ceiling <= 0 || sessionRemaining < ceiling) {
		return sessionRemaining
	}
	return ceiling
}

// PlanBudget decides one enforcement point.
//
// production caller: httpapi.Server.enforceBudgetFor, called from the daemon
// heartbeat (daemon-protocol §4.2 `usage`) and — since S-44 — from
// httpapi.finishAndEnforce, right after tasks.Finish has stored the attempt's
// usage and rolled it up (§4.4). The second call site is what makes the rule
// hold for a runtime that reports usage only at the end of the turn; before it
// existed this comment named it anyway, and nothing enforced anything there.
//
// The two differ in one thing only, and it is applied by the caller: after a
// finish the task is TERMINAL, so there is no turn to cancel and no task to
// park. httpapi.applyBudgetPause parks the LANE instead, which is what stops
// the next task by the same agent.
func PlanBudget(in BudgetInput) BudgetOutcome {
	o := BudgetOutcome{SessionState: "active", TaskStatus: "running"}
	scope, limit := in.Scope, in.SessionLimitUSD
	if in.Scope == "task" {
		ceiling := TaskCeiling(in.TaskLimitUSD, in.OverrideUSD)
		limit = EffectiveTaskLimit(in.TaskLimitUSD, in.OverrideUSD, in.SessionRemainingUSD)
		// D-16: the session's remainder is the half of the min that bound.
		// Pausing only this task would leave the other lanes free to spend a
		// session budget that is already gone (FR-7.3, E9-04).
		//
		// The condition is EffectiveTaskLimit's own, restated: the remainder
		// bound when it is positive AND the task ceiling is either absent or
		// larger. It used to read `limit < ceiling`, which is false for a
		// ceiling of 0 — so an agent with NO budget_per_task (the column is
		// nullable and most agents leave it) took the session remainder as its
		// TASK limit and, on crossing it, paused only itself while the session
		// budget it had just exhausted stayed `active` and the other lanes kept
		// spending. Found while wiring the finish-time check (S-44).
		if limit == in.SessionRemainingUSD && (ceiling <= 0 || limit < ceiling) {
			scope = "session"
		}
	}
	if limit <= 0 || in.SpentUSD < limit {
		return o
	}
	o.Exceeded = true
	o.Scope = scope
	o.DirectorNotified = true
	o.QueuedDispatched = 0

	if in.Estimated {
		// The number is our own guess from the price table. Killing real work
		// on a wrong guess is the failure FR-7.3 names, so the turn drains and
		// only new dispatch stops.
		o.TurnDrained = true
		o.SessionState, o.SessionPauseReason = "paused", PauseBudget
		o.TaskStatus, o.PausedReason = "running", ""
		// S-48: "Director에게 알린다" is a REQUEST, not a notice. Before this
		// the estimated pause raised no HITL at all and the Director got an
		// `session_paused` inbox card whose only answer was to go and call
		// resumeSession — while the measured pause, one branch below, handed
		// them a card they could approve with a new limit. The two pauses are
		// the same decision ("이 세션에 돈을 더 쓸까요?"), so they now ask it
		// the same way: `source: system` + `approval` + `purpose: budget`,
		// answered by respondHitlRequest (K-10 makes that answer resume the
		// session in one action).
		//
		// HitlTaskID stays EMPTY even for a task-scope overrun: what paused is
		// the session, and a request naming a task would be answered by
		// resuming that task while the session stayed `paused`.
		o.HitlIssued = true
		o.HitlSource = "system"
		o.HitlType = hitl.KindApproval
		o.HitlPurpose = hitl.PurposeBudget
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

	switch scope {
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
// production caller: httpapi.answerAgentHitl (the budget branch) and
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

// ---------------------------------------------------------------------------
// S-49 — one definition of "이미 쓴 돈", one sentence about it
// ---------------------------------------------------------------------------

// SpentUSD is how much a session has spent, for every "the new ceiling must be
// higher than this" check.
//
// WHY `greatest`. `session.cost_usd` is a rollup written at finish; the
// per-task `task_usage` rows are written as usage arrives, so between a
// heartbeat and the finish that follows it the rollup is BEHIND. The two
// budget-raise handlers each picked one of the two and got different answers
// for the same session (S-49): the resume path read the lagging column and
// accepted a new ceiling the session had already blown through, so the very
// next usage report re-tripped the pause and the Director saw the same banner
// with nothing changed.
//
// production callers: httpapi.answerAgentHitl (the K-10 session-budget
// approval) and httpapi.ResumeSession.
func SpentUSD(ctx context.Context, q db.DBTX, sessionID uuid.UUID) (float64, error) {
	var spent float64
	err := q.QueryRow(ctx, `
		SELECT greatest(s.cost_usd, COALESCE((SELECT sum(u.cost_usd) FROM task_usage u
		                                        JOIN task t ON t.id = u.task_id
		                                       WHERE t.session_id = s.id), 0))
		FROM session s WHERE s.id = $1`, sessionID).Scan(&spent)
	if err != nil {
		return 0, fmt.Errorf("sessions: spent: %w", err)
	}
	return spent, nil
}

// BudgetTooLowError is the one wording for a raise that is not a raise. Two
// handlers with two sentences taught the Director that the number to beat
// depends on which button they pressed (S-49).
func BudgetTooLowError(field string, spent float64) error {
	return apperr.Validation(apperr.Field(field, "too_low",
		fmt.Sprintf("이미 $%.2f를 썼습니다 — 새 상한은 그보다 커야 합니다", spent)))
}
