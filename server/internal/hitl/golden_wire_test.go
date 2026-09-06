// Wiring for the E7 golden table. Every decision lives in plan.go; this file
// only shapes the plans into the table's structs and holds the one piece of
// bookkeeping the table needs (which request is open).
//
// PRODUCTION CALL SITES (the rule Lead set for T-S5: a hook is only wired when
// its planner is the code production actually runs):
//
//	registerHitl         → PlanRegister      httpapi.CreateHitlRequest
//	                                         (handlers_hitl_p3.go)
//	pendingTurn          → PlanTurn          tasks.Service.Finish, the
//	                                         pending_hitl branch of the daemon's
//	                                         end-of-turn report (service.go)
//	respondHitl          → Authorize +       httpapi.answerAgentHitl and
//	                       PlanRespond       hitlAPI's can_respond
//	                                         (handlers_hitl_p3.go, handlers_hitl.go)
//	expireHitl           → PlanExpiry        hitl.Service.SweepDeadlines (sweep.go),
//	                                         run by the scheduler (cmd/server)
//	concurrencyUnderHitl → PlanConcurrency   the claim query's in-flight count
//	                                         (queue/postgres.go) encodes it
//	escalate             → PlanEscalation    httpapi.SetTaskStatus (blocked arm)
//	                                         and httpapi.CreateHitlRequest
package hitl

import (
	"time"

	"github.com/google/uuid"
)

func init() {
	registerHitl = adaptRegister
	pendingTurn = adaptPendingTurn
	respondHitl = adaptRespond
	expireHitl = adaptExpire
	concurrencyUnderHitl = adaptConcurrency
	escalate = adaptEscalate
}

// openRequestID is the row the store holds. Registration returns a new id when
// the plan accepts and the standing one when it refuses — that is the store's
// behaviour, not a decision: PlanRegister already said which happened.
var openRequestID uuid.UUID

func adaptRegister(c registerCase) registerResult {
	p := PlanRegister(RegisterInput{
		Kind: c.kind, Question: c.question, Options: c.options,
		ProposedDefault: c.proposedDefault, ApproverSpec: c.approverSpec,
		AlreadyOpen: c.alreadyOpen,
	})
	if p.Accepted {
		openRequestID = uuid.New()
	}
	return registerResult{
		Accepted: p.Accepted, ErrorCode: p.ErrorCode,
		TurnEndRequired: p.TurnEndRequired, PendingHitl: p.PendingHitl,
		TaskStatus: p.TaskStatus, FeedRecorded: p.FeedRecorded,
		OpenRequestID: openRequestID,
	}
}

func adaptPendingTurn(posts int, turnEnd bool) pendingTurnResult {
	p := PlanTurn(posts, turnEnd)
	return pendingTurnResult{
		MessagesStored: p.MessagesStored, TaskStatus: p.TaskStatus,
		CardPosted: p.CardPosted, InboxSeverity: p.InboxSeverity,
		ProcessRunning: p.ProcessRunning, OccupiesSlot: p.OccupiesSlot,
		WorkdirPreserved: p.WorkdirPreserved, HeartbeatRequired: p.HeartbeatRequired,
	}
}

func adaptRespond(c respondCase) respondResult {
	deputy := uuid.Nil
	if c.isDeputy {
		deputy = c.responder
	}
	az := Authorize(AuthzInput{
		Spec: c.approverSpec, Director: userDir, Deputy: deputy, Responder: c.responder,
		IsMember: true, Elapsed: c.elapsed, DueIn: dueIn,
	})
	status := StatusOpen
	if c.second {
		status = StatusAnswered
	}
	p := PlanRespond(RespondInput{
		Kind: c.kind, Status: status, Authz: az,
		Approved: c.approved, Answer: c.answer, Reason: c.reason,
	})
	stored := c.answer
	if p.Ignored {
		stored = c.firstAnswer
	}
	var from time.Duration
	if p.CanRespondFrom != nil {
		from = *p.CanRespondFrom
	}
	return respondResult{
		Accepted: p.Accepted, ErrorCode: p.ErrorCode, CanRespondFrom: from,
		Status: p.Status, StoredAnswer: stored, Ignored: p.Ignored,
		DecisionRecords: p.DecisionRecords, TaskStatus: p.TaskStatus,
		PromptSections: p.PromptSections, PromptApproved: p.PromptApproved,
		PromptReason: p.PromptReason,
	}
}

// goldenProposedDefault is the fixture value an agent proposed. It is passed
// for every type; PlanExpiry is what decides that an approval has nothing to
// proceed with (E7-14, E7-21), not this file.
const goldenProposedDefault = "투자자"

func adaptExpire(kind, autonomy string, elapsed time.Duration) expiryResult {
	p := PlanExpiry(kind, autonomy, goldenProposedDefault)
	return expiryResult{
		Status: p.Status, Overdue: p.Overdue, Answer: p.Answer,
		TaskStatus: p.TaskStatus, DecisionRecords: p.DecisionRecords,
		DecisionMarkedAutomatic: p.DecisionAutomatic, InboxTop: p.InboxTop,
	}
}

func adaptConcurrency(waitingHuman, running int) concurrencyResult {
	p := PlanConcurrency(waitingHuman, running)
	return concurrencyResult{
		SessionState: p.SessionState, OtherLaneRuns: p.OtherLaneRuns, SlotsUsed: p.SlotsUsed,
	}
}

func adaptEscalate(via string, delegator uuid.UUID) escalationResult {
	p := PlanEscalation(via, delegator)
	return escalationResult{
		Path: p.Path, DelegatorWoken: p.WakeDelegator, HitlRequests: p.HitlRequests,
		InboxItems: p.InboxItems, TurnEndRequired: p.TurnEndRequired,
	}
}
