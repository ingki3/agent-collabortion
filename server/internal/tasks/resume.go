package tasks

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

// Resume, retry and re-instruction (PRD FR-5.4 재개 모델, FR-7.1 M5, FR-3.4 B,
// §8.4 턴 프롬프트, contracts/harness.md §6, daemon-protocol §4.1·§4.4).
//
// One planner for all of them on purpose. A HITL answer, an automatic retry, a
// budget approval and a heartbeat re-queue differ only in WHY the next attempt
// exists; everything after that — resume first, cold start as the fallback,
// the same workdir, `<resumed>` and posted_message_ids — is one path. Writing
// them separately is how one of them quietly stops reusing the workdir.

// Causes of a new attempt. The cause is not cosmetic: it decides whether the
// prompt carries `<resumed>` (retry) or only a new instruction (restart), and
// whether the failure is retried at all (E8-10).
const (
	CauseHitlAnswer     = "hitl_answer"
	CauseBudgetApproved = "budget_approved"
	CauseRetryNetwork   = "retry_network"
	CauseRetryAuth      = "retry_auth"
	CauseRequeueSweep   = "requeue_heartbeat"
	CauseRestart        = "restart"
)

// DefaultHistoryLimit is how many messages §8.4's history section carries when
// the caller does not say. The number is a cap, and the section declares that
// it is one (E8-12).
const DefaultHistoryLimit = 30

// AttemptInput is the state the next attempt starts from. Everything here is
// something the caller already loaded; the planner reads no database.
type AttemptInput struct {
	// TaskID is the task the previous attempt ran under, and Attempt is that
	// attempt's number.
	TaskID  uuid.UUID
	Attempt int
	// MaxAttempts is task.max_attempts.
	MaxAttempts int
	// TriggerMessageID is the message that created the task.
	TriggerMessageID uuid.UUID

	// NewTaskID and NewTriggerMessageID are supplied for a re-instruction: the
	// caller allocates the row, the planner decides that a row is needed.
	NewTaskID           uuid.UUID
	NewTriggerMessageID uuid.UUID

	// SessionRef is lane.runtime_session_ref's session id (harness §6). Empty
	// means the lane has none, so only a cold start is possible.
	SessionRef string
	// RefRuntimeKind produced SessionRef; ProfileRuntimeKind will run the next
	// attempt. Different means a profile fallback happened (E8-08).
	RefRuntimeKind     string
	ProfileRuntimeKind string
	// ResumeRejected is the daemon's report (harness §6): the runtime no longer
	// has the session. Not a failure — it selects the cold start.
	ResumeRejected bool

	Cause string

	PostedMessageIDs []uuid.UUID
	PrevWorkdir      string
	// PrevPhase is how far the previous attempt got; "preparing" means the
	// workdir exists even though no turn ran (E8-11).
	PrevPhase string

	HistoryTotal int
	HistoryLimit int

	NewInstruction string

	// AlternateProfile: the same machine has another profile for this agent.
	AlternateProfile bool
}

// AttemptPlan is the decision: which row the attempt belongs to, what the
// bundle carries and what the prompt contains.
type AttemptPlan struct {
	TaskID              uuid.UUID
	Attempt             int
	RestartedFromTaskID uuid.UUID
	TriggerMessageID    uuid.UUID

	TaskStatus string
	LaneStatus string

	Workdir string
	// WorkdirCreated must be false for every re-entry: the half-finished work
	// is in the directory the previous attempt used (FR-7.1 M5 1, E8-11).
	WorkdirCreated bool

	// ResumeRef is the bundle's `resume` (daemon-protocol §4.1). Empty is a
	// cold start.
	ResumeRef string
	// ResumeAttempted records that the runtime session was tried BEFORE any
	// cold start — FR-5.4 makes that an order, not a preference.
	ResumeAttempted bool
	ColdStart       bool

	HasResumedSection       bool
	PostedMessageIDs        []uuid.UUID
	WorkdirCheckInstruction bool
	ColdStartSections       []string
	PromptContains          string

	HistoryIncluded  int
	HistoryTotal     int
	HistoryTruncated bool

	// Retries is how many automatic retries the cause still permits.
	Retries          int
	DirectorNotified bool
	// HandedToAnotherMachine is never true: the session is pinned to its
	// runtime (FR-2.1 M10) and both the workdir and the runtime session live
	// there (E8-09). The field exists so the golden can say so.
	HandedToAnotherMachine bool
	AgentStatus            string
}

// failureFor maps a cause to the failure class FR-7.1 retries by. A cause that
// is not a failure (an answer, an approval, a human's re-instruction) has no
// class and is never subject to the retry cap.
func failureFor(cause string) (contracts.FailureKind, bool) {
	switch cause {
	case CauseRetryAuth:
		return contracts.FailAuth, true
	case CauseRetryNetwork:
		return contracts.FailNetwork, true
	case CauseRequeueSweep:
		return contracts.FailRuntimeOffline, true
	}
	return "", false
}

// PlanAttempt is the shared resume/retry/restart path.
//
// Production call sites:
//
//	queue.buildBundle          resume ref, `<resumed>`, posted_message_ids,
//	                           history header and the workdir-check line
//	tasks.requeueLocked        queued-vs-failed, the retry budget and E8-09's
//	                           Director notice
//	httpapi.RestartLane        the re-instruction branch (a NEW task)
//	httpapi.answerAgentHitl    the HITL answer's re-queue
func PlanAttempt(in AttemptInput) AttemptPlan {
	maxAttempts := in.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	p := AttemptPlan{
		TaskID: in.TaskID, Attempt: in.Attempt + 1, TriggerMessageID: in.TriggerMessageID,
		Workdir: in.PrevWorkdir, TaskStatus: string(Queued), LaneStatus: "queued",
		PostedMessageIDs: in.PostedMessageIDs,
	}
	// The workdir is never recreated. `preparing` is the case that looks like
	// an exception and is not: the previous attempt already prepared the
	// directory (and, under worktree isolation, a checkout), so a second one
	// leaves an orphan (E8-11).
	p.WorkdirCreated = in.PrevWorkdir == ""

	// Resume first, cold start as the fallback (FR-5.4 step order). A ref from
	// a DIFFERENT runtime kind is not a ref: a Hermes session id means nothing
	// to Claude Code (daemon-protocol §4.4, E8-08).
	usable := in.SessionRef != "" && (in.RefRuntimeKind == "" || in.RefRuntimeKind == in.ProfileRuntimeKind)
	switch {
	case !usable:
		p.ColdStart = true
	case in.ResumeRejected:
		// The runtime told us the session is gone. Trying the same id again
		// would fail the same way (harness §6).
		p.ResumeAttempted, p.ColdStart = true, true
	default:
		p.ResumeAttempted, p.ResumeRef = true, in.SessionRef
	}
	if p.ColdStart {
		p.ColdStartSections = []string{"brief", "history", "decision_log", "posted_messages", "workdir_check"}
	}

	// History header (§8.4, E8-12). The flag is what tells the agent to read
	// more with `colab session messages`; a flag that is always true says
	// nothing.
	limit := in.HistoryLimit
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	p.HistoryTotal = in.HistoryTotal
	p.HistoryIncluded = in.HistoryTotal
	if p.HistoryIncluded > limit {
		p.HistoryIncluded = limit
	}
	p.HistoryTruncated = in.HistoryTotal > p.HistoryIncluded

	if in.Cause == CauseRestart {
		// FR-3.4 B: the human changed direction. A NEW task keeps the record of
		// what was asked the first time, and the prompt carries the new
		// instruction and nothing else — telling the agent to continue the work
		// a person just replaced is exactly wrong (§8.4, E8-06).
		p.TaskID = in.NewTaskID
		p.Attempt = 1
		p.RestartedFromTaskID = in.TaskID
		p.TriggerMessageID = in.NewTriggerMessageID
		p.PromptContains = in.NewInstruction
		p.PostedMessageIDs = nil
		p.HasResumedSection = false
		// The lane survives: the agent keeps its place in the session (E2-15).
		p.LaneStatus = "running"
		p.WorkdirCheckInstruction = false
		p.Retries = maxAttempts - 1
		p.AgentStatus = DeriveAgentStatus(Derived{RespondTo: "owner", RetryInFlight: true})
		return p
	}

	// `<resumed>` is for a continuation: attempt ≥ 2 of the same task (§8.4).
	p.HasResumedSection = p.Attempt >= 2
	p.WorkdirCheckInstruction = p.HasResumedSection || p.ColdStart

	kind, isFailure := failureFor(in.Cause)
	switch {
	case !isFailure:
		p.Retries = maxAttempts - p.Attempt + 1
		if p.Retries < 0 {
			p.Retries = 0
		}
	case !kind.Retryable():
		// auth · quota · config are never retried: a different profile on the
		// same machine has the same broken credentials (FR-7.1, harness §8).
		p.Retries = 0
		p.TaskStatus, p.LaneStatus = string(Failed), "failed"
		p.Attempt = in.Attempt
	case in.Attempt >= maxAttempts:
		p.Retries = 0
		p.TaskStatus, p.LaneStatus = string(Failed), "failed"
		p.Attempt = in.Attempt
	default:
		p.Retries = maxAttempts - in.Attempt
	}
	if isFailure && kind.Retryable() && !in.AlternateProfile {
		// E8-09: nothing else on this machine can run the work. The task waits
		// in `queued` and a human is told — a task parked with nobody told is
		// invisible. It is NEVER handed to another machine: that would
		// invalidate both runtime_session_ref and the workdir (FR-2.1 M10).
		p.DirectorNotified = true
	}
	d := Derived{RespondTo: "owner"}
	if p.TaskStatus == string(Failed) {
		d.LastFailureKind = string(kind)
	} else {
		d.RetryInFlight = true
	}
	p.AgentStatus = DeriveAgentStatus(d)
	return p
}

// ---------------------------------------------------------------------------
// runtime_session_ref round trip (E8-13, a G3 regression row)
// ---------------------------------------------------------------------------

// ValidateSessionRef is the finish-time gate on harness §6's ref. The lane
// CHECK (0004) requires runtime_kind and session_id; rejecting here turns a
// constraint violation into a typed error the daemon can act on.
//
// Production caller: tasks.Service.Finish (service.go).
func ValidateSessionRef(ref *contracts.RuntimeSessionRef) (*contracts.RuntimeSessionRef, error) {
	if ref == nil {
		return nil, nil
	}
	if ref.RuntimeKind == "" || ref.SessionID == "" {
		return nil, ErrInvalidSessionRef
	}
	return ref, nil
}

// PlanBundleResume turns the stored lane.runtime_session_ref into the bundle's
// `resume` (daemon-protocol §4.1). Storing the ref and not handing it back
// makes every attempt a silent cold start while the API still claims resume is
// supported — the G3 defect E8-13 was promoted from.
//
// Production caller: queue.buildBundle (bundle.go).
func PlanBundleResume(stored []byte, profileRuntimeKind string) *contracts.RuntimeSessionRef {
	if len(stored) == 0 {
		return nil
	}
	var ref contracts.RuntimeSessionRef
	if err := json.Unmarshal(stored, &ref); err != nil || ref.SessionID == "" {
		return nil
	}
	if profileRuntimeKind != "" && ref.RuntimeKind != "" && string(ref.RuntimeKind) != profileRuntimeKind {
		// E8-08 again, one layer down: a ref the next runtime cannot load is
		// not a resume, and pretending otherwise costs an attempt.
		return nil
	}
	return &ref
}

// CauseOfFailure maps a failure class back to the cause of the next attempt.
// It is the inverse of failureFor, and it exists so the requeue path names its
// cause instead of re-deriving the retry rule.
//
// Production caller: tasks.requeueLocked (service.go).
func CauseOfFailure(reason contracts.FailureKind) string {
	if !reason.Retryable() {
		// auth · quota · config · cancelled: FR-7.1 never retries these, and
		// CauseRetryAuth is the cause that carries that rule.
		return CauseRetryAuth
	}
	switch reason {
	case contracts.FailRuntimeOffline, contracts.FailTimeout, contracts.FailStall:
		return CauseRequeueSweep
	default:
		return CauseRetryNetwork
	}
}
