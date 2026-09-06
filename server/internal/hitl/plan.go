package hitl

import (
	"time"

	"github.com/google/uuid"
)

// The HITL rules of FR-5.1 · FR-5.2 · FR-5.4 · FR-7.1 as pure functions.
//
// They are pure for the reason PlanDispatch and PlanSweep are (PLAN §10.3):
// the rules are a table in the PRD, the golden file states that table, and a
// decision that lives inside a handler can only be checked by driving the
// handler. Every function here has a production caller in httpapi or in this
// package's service — the list is in golden_wire_test.go.

// Types (FR-5.1). v1 uses question and approval; choice and info have schemas
// now because SCREEN §2.3 C4 shows them.
const (
	KindQuestion = "question"
	KindChoice   = "choice"
	KindApproval = "approval"
	KindInfo     = "info"
)

// Statuses (hitl_status, FR-5.4). `expired` is deliberately absent — a request
// past its deadline is `open` with the overdue flag (s-9).
const (
	StatusOpen         = "open"
	StatusAnswered     = "answered"
	StatusAutoAnswered = "auto_answered"
	StatusCancelled    = "cancelled"
)

// Purposes (openapi HitlRequest.purpose, 0012). Platform-issued requests carry
// a fixed value per purpose; an agent's own request is `agent`.
const (
	PurposeAgent        = "agent"
	PurposeUserApproval = "user_approval"
	PurposeBudget       = "budget"
	PurposeTime         = "time"
	PurposeLoop         = "loop"
)

// Approver specs supported in v1 (FR-5.4). Anything else is refused at
// registration — fail closed, because an unrecognised spec that is stored
// unchecked is a spec nobody enforces.
const (
	SpecDirector  = "director"
	SpecAnyMember = "any_member"
)

// Error codes the contract names (openapi Problem.code).
const (
	CodeValidation  = "validation"
	CodeAlreadyOpen = "hitl_already_open"
	CodeForbidden   = "forbidden"
)

// DefaultDueIn is FR-5.4's deadline; half of it is the deputy hand-over.
const DefaultDueIn = 24 * time.Hour

// ---------------------------------------------------------------------------
// Registration — `colab hitl ask` / `approve-request` / `request-info`
// ---------------------------------------------------------------------------

// RegisterInput is one createHitlRequest call, already parsed out of the
// HitlCreate oneOf.
type RegisterInput struct {
	Kind            string
	Question        string
	Options         []string
	ProposedDefault string
	// ApproverSpec is the raw value; "" means the contract default.
	ApproverSpec string
	// AlreadyOpen is "this task already holds an open request" (FR-7.1 step 4).
	AlreadyOpen bool
}

// RegisterPlan is the verdict. A refusal is a refusal: there is no branch that
// stores a downgraded request, because a `question` stored without its default
// stalls the session forever when the deadline passes with nobody watching.
type RegisterPlan struct {
	Accepted  bool
	ErrorCode string
	// ErrorField / ErrorMessage fill Problem.errors[] for a 422.
	ErrorField   string
	ErrorMessage string

	// TurnEndRequired is the contract's `turn_end_required`: the CLI tells the
	// agent to stop. It does NOT block waiting for an answer (FR-5.2).
	TurnEndRequired bool
	// PendingHitl is the flag set on the task. The task stays `running` —
	// only turn_end transitions (FR-7.1 step 1, E7-01).
	PendingHitl bool
	TaskStatus  string
	// FeedRecorded: a refused second request is still written to the activity
	// feed, so the Director can see the agent tried (E7-04).
	FeedRecorded bool
	// ApproverSpec is the normalised value to store.
	ApproverSpec string
	// Purpose is what to store in hitl_request.purpose.
	Purpose string
}

// PlanRegister validates one registration and says what it does to the task.
//
// Production caller: httpapi.CreateHitlRequest (handlers_hitl_p3.go).
func PlanRegister(in RegisterInput) RegisterPlan {
	p := RegisterPlan{TaskStatus: "running", ApproverSpec: in.ApproverSpec, Purpose: PurposeAgent}
	if p.ApproverSpec == "" {
		p.ApproverSpec = SpecDirector
	}
	reject := func(code, field, msg string) RegisterPlan {
		p.Accepted, p.ErrorCode, p.ErrorField, p.ErrorMessage = false, code, field, msg
		p.PendingHitl, p.TurnEndRequired = false, false
		return p
	}
	switch in.Kind {
	case KindQuestion, KindChoice, KindApproval, KindInfo:
	default:
		return reject(CodeValidation, "type", "type must be question, choice, approval or info (FR-5.1)")
	}
	if in.Question == "" {
		return reject(CodeValidation, "question", "the request needs a question, summary or what (FR-5.1)")
	}
	// FR-5.1 names question AND choice on the same line. Implementing the rule
	// for `question` alone leaves a `choice` that expires with nothing to
	// proceed with (E7-05, E7-20).
	if (in.Kind == KindQuestion || in.Kind == KindChoice) && in.ProposedDefault == "" {
		return reject(CodeValidation, "proposed_default",
			"question and choice require proposed_default — an expiring request needs a value to proceed with (FR-5.1)")
	}
	if in.Kind == KindChoice && len(in.Options) < 2 {
		return reject(CodeValidation, "options", "choice needs at least two options")
	}
	if !SupportedApproverSpec(p.ApproverSpec) {
		// Fail closed (FR-5.4): a role-based spec stored unchecked is a spec
		// nobody enforces, and every member becomes an approver.
		return reject(CodeValidation, "approver_spec",
			"v1 supports approver_spec director, any_member or a user uuid")
	}
	if in.AlreadyOpen {
		// FR-7.1 step 4: one open request per task. The FIRST one stands — a
		// second call may not replace the question a human is looking at.
		p.Accepted, p.ErrorCode, p.FeedRecorded = false, CodeAlreadyOpen, true
		return p
	}
	p.Accepted, p.TurnEndRequired, p.PendingHitl = true, true, true
	return p
}

// SupportedApproverSpec is the v1 allow-list (FR-5.4).
func SupportedApproverSpec(spec string) bool {
	switch spec {
	case SpecDirector, SpecAnyMember:
		return true
	}
	_, err := uuid.Parse(spec)
	return err == nil
}

// ---------------------------------------------------------------------------
// The turn keeps running until turn_end (FR-7.1 "HITL 전이 시점")
// ---------------------------------------------------------------------------

// TurnPlan is the task after the agent's turn ends (or does not).
type TurnPlan struct {
	// MessagesStored is how many post-request messages are kept. They already
	// happened; dropping them rewrites history (step 2).
	MessagesStored int
	TaskStatus     string
	LaneStatus     string

	// CardPosted is the timeline card. It goes up at REGISTRATION, not at
	// turn_end — the Director should see the question while the agent is still
	// finishing its turn — so it is true either way (FR-5.2).
	CardPosted    bool
	InboxSeverity string

	// The four things waiting_human means (FR-5.4): no process, no slot, the
	// workdir kept, no heartbeat.
	ProcessRunning    bool
	OccupiesSlot      bool
	WorkdirPreserved  bool
	HeartbeatRequired bool
}

// PlanTurn is FR-7.1's HITL transition. `posts` is how many messages the agent
// sent after registering; turnEnd is whether its turn has ended.
//
// Production caller: tasks.Service.Finish (the `pending_hitl` branch of the
// daemon's end-of-turn report, service.go).
func PlanTurn(posts int, turnEnd bool) TurnPlan {
	p := TurnPlan{
		MessagesStored: posts, CardPosted: true, InboxSeverity: "action_required",
		WorkdirPreserved: true,
	}
	if !turnEnd {
		// The agent ignoring "end your turn" does not transition the task. It
		// is still running, still holding its slot and still beating.
		p.TaskStatus, p.LaneStatus = "running", "running"
		p.ProcessRunning, p.OccupiesSlot, p.HeartbeatRequired = true, true, true
		return p
	}
	p.TaskStatus, p.LaneStatus = "waiting_human", "waiting_human"
	return p
}

// ---------------------------------------------------------------------------
// Who may answer (FR-5.4 M7, FR-5.3)
// ---------------------------------------------------------------------------

// Authz is the answer to "may this person respond, and if not, ever?".
type Authz struct {
	Allowed bool
	// CanRespondFrom is the instant the caller becomes eligible, as an offset
	// from the request's creation. nil means NEVER — a plain member does not
	// become an approver by waiting, and promising a time would be a lie
	// (openapi Problem.can_respond_from, E7-11).
	CanRespondFrom *time.Duration
}

// AuthzInput is what the decision reads.
type AuthzInput struct {
	Spec      string
	Director  uuid.UUID
	Deputy    uuid.UUID
	Responder uuid.UUID
	IsMember  bool
	// Elapsed is time since the request was created; DueIn is its deadline.
	Elapsed time.Duration
	DueIn   time.Duration
}

// Authorize implements FR-5.4 M7. `director` does not mean "the Director
// only": it means the Director, and after HALF the deadline the deputy as well
// — notifying a deputy without granting the right makes the notification
// useless (E7-09, E7-10).
//
// Production caller: httpapi.RespondHitlRequest and hitlAPI's `can_respond`
// (handlers_hitl.go) — the same judgement, so the button never says "you can"
// to someone the handler will refuse.
func Authorize(in AuthzInput) Authz {
	dueIn := in.DueIn
	if dueIn <= 0 {
		dueIn = DefaultDueIn
	}
	switch in.Spec {
	case SpecAnyMember:
		return Authz{Allowed: in.IsMember}
	case SpecDirector:
		if in.Responder == in.Director {
			return Authz{Allowed: true}
		}
		if in.Deputy != uuid.Nil && in.Responder == in.Deputy {
			half := dueIn / 2
			if in.Elapsed >= half {
				return Authz{Allowed: true}
			}
			return Authz{CanRespondFrom: &half}
		}
		return Authz{}
	default:
		if id, err := uuid.Parse(in.Spec); err == nil && id == in.Responder {
			return Authz{Allowed: true}
		}
		return Authz{}
	}
}

// ---------------------------------------------------------------------------
// Responding (FR-5.4, E7-07 … E7-11, E7-15, E7-17, E10-08)
// ---------------------------------------------------------------------------

// RespondInput is one respondHitlRequest call.
type RespondInput struct {
	Kind string
	// Status is the request's CURRENT status. Anything but `open` means this
	// is a second response.
	Status string
	Authz  Authz

	Approved *bool
	Answer   string
	Reason   string

	// AgentDisabled is the kill switch (FR-1.9 M8): the answer is recorded but
	// the re-queue is held (E10-08).
	AgentDisabled bool
}

// RespondPlan is the effect of the response.
type RespondPlan struct {
	Accepted  bool
	ErrorCode string
	// CanRespondFrom mirrors Authz.
	CanRespondFrom *time.Duration

	// Ignored is the contract's `ignored: true` — a second response is a
	// no-op answered with 200, not an error (FR-5.4 "멱등", E7-08).
	Ignored bool

	Status string
	// DecisionRecords is the number of decision rows the request has after the
	// call, not the number this call wrote — an ignored response adds none.
	DecisionRecords int

	// TaskStatus after the answer. `queued`, not `running`: the process that
	// asked the question is gone, so the answer starts a NEW attempt
	// (FR-5.4, FR-7.1 N4).
	TaskStatus  string
	RequeueHeld bool

	// The resume prompt (§8.4).
	PromptSections []string
	PromptApproved *bool
	PromptReason   string
}

// PlanRespond decides one response.
//
// Production caller: httpapi.Server.answerAgentHitl (handlers_hitl_p3.go).
func PlanRespond(in RespondInput) RespondPlan {
	p := RespondPlan{Status: in.Status, CanRespondFrom: in.Authz.CanRespondFrom}
	if in.Status != StatusOpen {
		// E7-08. The first answer stands and the caller gets it back.
		p.Accepted, p.Ignored, p.DecisionRecords = true, true, 1
		p.TaskStatus = ""
		return p
	}
	if !in.Authz.Allowed {
		p.ErrorCode = CodeForbidden
		return p
	}
	p.Accepted = true
	p.Status = StatusAnswered
	p.DecisionRecords = 1
	switch in.Kind {
	case KindApproval:
		p.PromptSections = []string{"approval_result"}
		p.PromptApproved = in.Approved
		p.PromptReason = in.Reason
	case KindInfo:
		p.PromptSections = []string{"requested_info"}
	default:
		p.PromptSections = []string{"question_answer"}
	}
	if in.AgentDisabled {
		// FR-1.9 M8: re-queueing restarts the agent the owner just disabled.
		// The answer is still recorded — the human's decision is not the
		// agent's to lose — and released when respond_to comes back (E10-08).
		p.TaskStatus, p.RequeueHeld = "waiting_human", true
		return p
	}
	// A rejected approval is a branch the agent handles, not a failure: the
	// task runs again with approved:false and the reason (E7-17).
	p.TaskStatus = "queued"
	return p
}

// ---------------------------------------------------------------------------
// Deadline expiry (FR-5.4 "기한 만료 시 동작" — the type × autonomy grid)
// ---------------------------------------------------------------------------

// ExpiryPlan is what happens to a request whose due_at passed.
type ExpiryPlan struct {
	Status  string
	Overdue bool
	// Answer is the value the request proceeded with, or "" when it did not.
	Answer string

	TaskStatus      string
	DecisionRecords int
	// DecisionAutomatic separates "the Director said 투자자" from "nobody
	// answered and we used the agent's proposal" in the log (E7-12).
	DecisionAutomatic bool
	// InboxTop is the overdue sort promotion (SCREEN §4.6).
	InboxTop bool
}

// PlanExpiry is FR-5.4's grid. TYPE decides first and autonomy second: an
// implementation that keys on autonomy alone auto-answers an approval, which
// empties the human gate the approval exists to be (E7-14, E7-21).
//
// Production caller: hitl.Service.SweepDeadlines (sweep.go), run by the
// scheduler.
func PlanExpiry(kind, autonomy, proposedDefault string) ExpiryPlan {
	p := ExpiryPlan{Overdue: true, InboxTop: true}
	switch kind {
	case KindQuestion, KindChoice:
		if autonomy == "autonomous" && proposedDefault != "" {
			p.Status, p.Answer = StatusAutoAnswered, proposedDefault
			p.TaskStatus = "queued"
			p.DecisionRecords, p.DecisionAutomatic = 1, true
			p.Overdue, p.InboxTop = false, false
			return p
		}
	case KindApproval, KindInfo:
		// Silence is neither approval nor rejection: approving empties the
		// gate, rejecting kills healthy work. Both always wait (M7).
	}
	p.Status = StatusOpen
	p.TaskStatus = "waiting_human"
	return p
}

// ---------------------------------------------------------------------------
// A waiting lane does not stop the session (FR-5.4, E7-18)
// ---------------------------------------------------------------------------

// ConcurrencyPlan is the slot accounting under an open HITL.
type ConcurrencyPlan struct {
	SessionState  string
	OtherLaneRuns bool
	// SlotsUsed counts max_concurrent_tasks / max_parallel_lanes slots.
	// waiting_human occupies none — a question held for 24h would otherwise
	// spend a slot for 24h (FR-5.4).
	SlotsUsed int
}

// OccupyingStatuses lists the task statuses that hold a concurrency slot
// (FR-6.3's four limits). `waiting_human` and `blocked` are absent on purpose:
// both processes have already exited, so holding a slot for them stalls the
// session while nothing runs — a question held for 24h would otherwise spend a
// slot for 24h (FR-5.4, t-1).
//
// Production caller: queue.Postgres.Claim, whose `busy` set is this list.
func OccupyingStatuses() []string {
	return []string{"dispatched", "preparing", "running"}
}

// PlanConcurrency is the slot rule, stated as a count so the golden can check
// it: a session with one waiting_human lane and one running lane uses ONE slot.
//
// Production caller: queue.Postgres.Claim through OccupyingStatuses — the
// claim query counts exactly the statuses that function names.
func PlanConcurrency(waitingHuman, running int) ConcurrencyPlan {
	return ConcurrencyPlan{
		SessionState: "active", OtherLaneRuns: running > 0, SlotsUsed: running,
	}
}

// ---------------------------------------------------------------------------
// `status set blocked` is the delegator path, not the Director path (E7-19)
// ---------------------------------------------------------------------------

// EscalationPlan is where one escalation goes.
type EscalationPlan struct {
	Path            string // blocked | hitl
	WakeDelegator   bool
	HitlRequests    int
	InboxItems      int
	TurnEndRequired bool
}

// PlanEscalation keeps the two escalation paths distinguishable. The server
// follows the call the agent made, not the one it should have made: routing a
// `blocked` to the Director because "the question is for a human" makes
// FR-6.2.1's immediate delegator wake unobservable, and the agent that picked
// the wrong path gets no signal that it did.
//
// Production caller: httpapi.SetTaskStatus (the `blocked` arm) and
// httpapi.CreateHitlRequest (the `hitl` arm).
func PlanEscalation(via string, delegator uuid.UUID) EscalationPlan {
	if via == "blocked" {
		p := EscalationPlan{Path: "blocked", TurnEndRequired: true, WakeDelegator: delegator != uuid.Nil}
		if !p.WakeDelegator {
			// No delegator to wake: the question has to reach a human, so the
			// Director's inbox gets it instead (FR-6.2.1 last bullet).
			p.InboxItems = 1
		}
		return p
	}
	return EscalationPlan{Path: "hitl", TurnEndRequired: true, HitlRequests: 1, InboxItems: 1}
}
