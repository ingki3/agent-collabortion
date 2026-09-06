//go:build p3golden

// Golden table for HITL (EVAL E7, 19 rows) — PRD FR-5.1 (types), FR-5.2
// (behaviour), FR-5.4 (pause/resume model), FR-7.1 (the HITL transition), and
// contracts/openapi.yaml `createHitlRequest` · `respondHitlRequest`.
//
// Written by the Reviewer BEFORE the implementation (PLAN §10.3, P3a) so T-S5
// codes against a table it did not author. Case names carry their EVAL row id:
//
//	TestHitlRegisterGolden/E7_05_question_without_default_is_rejected
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - Registration does NOT change the task status. `pending_hitl` is a flag;
//     only `turn_end` moves the task to waiting_human (FR-7.1 HITL 전이, steps
//     1–3). An implementation that transitions on the tool call passes a naive
//     reading of FR-5.2 and breaks E7-01·E7-02.
//   - Expiry is per TYPE, not per session setting alone: `question`/`choice`
//     follow `autonomy`, while `approval`/`info` ALWAYS keep waiting. There is
//     no auto-approve and no auto-reject, in any autonomy (FR-5.4 M7).
//   - `expired` is not a status. Overdue is `open` + a flag (FR-5.4 s-9).
//   - `approver_spec: director` means "the Director, and after HALF the
//     deadline also the deputy" (FR-5.4 M7) — not "Director only".
//
// HOW THIS FILE FAILS TODAY. Nothing in `server/internal` implements agent
// HITL: the P2 slice answers exactly one platform-issued approval
// (httpapi/handlers_hitl.go:47) and everything else is 501. So every hook here
// is nil and every row fails with the message naming what to wire. T-S5 drops
// `-tags p3golden` from the slices it lands.
package hitl

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// caseNameP3 keeps the EVAL row id in the test name. It is spelled with the
// P3 suffix because the P2 golden files define their own `caseName` in other
// packages and a shared spelling would collide when both tags are on.
func caseNameP3(eval, name string) string {
	out := make([]byte, 0, len(eval))
	for i := 0; i < len(eval); i++ {
		if eval[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, eval[i])
	}
	return fmt.Sprintf("%s_%s", string(out), name)
}

// ---------------------------------------------------------------------------
// Fixed identities. Stable UUIDs keep failure output readable and match the
// EVAL premise: Director `Dir`, deputy, plain member `M2`, agents W and R.
// ---------------------------------------------------------------------------

var (
	agentW  = uuid.MustParse("a0000000-0000-4000-8000-000000000003")
	agentR  = uuid.MustParse("a0000000-0000-4000-8000-000000000002")
	userDir = uuid.MustParse("b0000000-0000-4000-8000-000000000001")
	userDep = uuid.MustParse("b0000000-0000-4000-8000-000000000003")
	userM2  = uuid.MustParse("b0000000-0000-4000-8000-000000000002")
	taskW1  = uuid.MustParse("c0000000-0000-4000-8000-000000000001")
)

// dueIn is the FR-5.4 default deadline; half of it is the deputy handover.
const dueIn = 24 * time.Hour

// ---------------------------------------------------------------------------
// 1. Registration — `colab hitl ask` / `approve-request` / `request-info`
// ---------------------------------------------------------------------------

// registerCase is one call of the registration API (openapi createHitlRequest,
// HitlCreate oneOf).
type registerCase struct {
	eval, name string

	kind            string // question | choice | approval | info
	question        string // --question / --summary / --what
	options         []string
	proposedDefault string // --default; "" means the flag was absent
	approverSpec    string // "" means the contract default (director)

	// alreadyOpen is E7-04: this task already has an open request.
	alreadyOpen bool
}

// registerResult is what the server must answer.
type registerResult struct {
	// Accepted is false when the call was refused. Refusal must be a REFUSAL,
	// not a silently downgraded request.
	Accepted bool
	// ErrorCode distinguishes the refusals the contract names:
	// "validation" (422, E7-05·E7-16) and "hitl_already_open" (409, E7-04).
	ErrorCode string

	// TurnEndRequired is the contract's `turn_end_required` — the CLI tells the
	// agent to stop, it does not block waiting for an answer (FR-5.2, FR-5.4).
	TurnEndRequired bool

	// PendingHitl is the flag set on the task. TaskStatus must still be
	// running at this point (FR-7.1 step 1).
	PendingHitl bool
	TaskStatus  string

	// FeedRecorded is the activity-feed entry a refusal leaves (E7-04).
	FeedRecorded bool

	// OpenRequestID identifies which request stands after the call. For E7-04
	// it must still be the FIRST one.
	OpenRequestID uuid.UUID
}

// registerHitl is wired by T-S5 to the real registration path.
// See the hand-off report, "required API".
var registerHitl func(c registerCase) registerResult

func mustRegister(t *testing.T, c registerCase) registerResult {
	t.Helper()
	if registerHitl == nil {
		t.Fatalf("unimplemented: HITL registration (FR-5.1, openapi createHitlRequest). " +
			"T-S5 must wire `registerHitl` — see the P3a hand-off report 'required API'")
	}
	return registerHitl(c)
}

func TestHitlRegisterGolden(t *testing.T) {
	t.Run(caseNameP3("E7-01", "question_with_default_registers_and_ends_the_turn"), func(t *testing.T) {
		r := mustRegister(t, registerCase{
			kind: "question", question: "독자?", proposedDefault: "투자자",
		})
		if !r.Accepted {
			t.Fatalf("a question with --default must be accepted (error=%q)", r.ErrorCode)
		}
		if !r.TurnEndRequired {
			t.Error("the response tells the agent to end the turn — it must not block on an answer (FR-5.2)")
		}
		if !r.PendingHitl {
			t.Error("registration sets task.pending_hitl (FR-7.1 step 1)")
		}
		// The most important half of the row: registration is NOT a transition.
		if r.TaskStatus != "running" {
			t.Errorf("task status = %q, want running — only turn_end transitions (E7-01, FR-7.1)", r.TaskStatus)
		}
	})

	t.Run(caseNameP3("E7-04", "second_request_in_the_same_turn_is_refused_and_the_first_stands"), func(t *testing.T) {
		first := mustRegister(t, registerCase{
			kind: "question", question: "독자?", proposedDefault: "투자자",
		})
		second := mustRegister(t, registerCase{
			kind: "question", question: "예산?", proposedDefault: "$100", alreadyOpen: true,
		})
		if second.Accepted {
			t.Fatal("a task may hold at most ONE open HITL (FR-7.1 step 4) — the second call is refused")
		}
		if second.ErrorCode != "hitl_already_open" {
			t.Errorf("error = %q, want hitl_already_open (openapi 409)", second.ErrorCode)
		}
		if !second.FeedRecorded {
			t.Error("the refused call is recorded in the activity feed (E7-04)")
		}
		if second.OpenRequestID != first.OpenRequestID {
			t.Error("the FIRST request must survive — a second call may not replace it")
		}
	})

	t.Run(caseNameP3("E7-05", "question_without_default_is_rejected"), func(t *testing.T) {
		r := mustRegister(t, registerCase{kind: "question", question: "독자?"})
		if r.Accepted {
			t.Fatal("proposed_default is REQUIRED for question (FR-5.1) — without it an expiring " +
				"request has no value to proceed with and the session stalls forever")
		}
		if r.ErrorCode != "validation" {
			t.Errorf("error = %q, want validation (openapi 422)", r.ErrorCode)
		}
	})

	t.Run(caseNameP3("E7-05", "choice_without_default_is_rejected_too"), func(t *testing.T) {
		// FR-5.1 names question AND choice. A rule implemented for one type
		// only passes the row above and still stalls a choice request.
		r := mustRegister(t, registerCase{
			kind: "choice", question: "어느 쪽?", options: []string{"A", "B"},
		})
		if r.Accepted {
			t.Fatal("proposed_default is required for choice as well as question (FR-5.1)")
		}
	})

	t.Run(caseNameP3("E7-06", "approval_without_default_is_accepted"), func(t *testing.T) {
		// The mirror of E7-05: approval has no proposed_default at all, so a
		// "default is always required" implementation breaks the approval path.
		r := mustRegister(t, registerCase{kind: "approval", question: "초안 승인 요청"})
		if !r.Accepted {
			t.Fatalf("approval carries --summary and no default (FR-5.1) — it must register (error=%q)", r.ErrorCode)
		}
		if !r.TurnEndRequired {
			t.Error("approval ends the turn like every other HITL type (FR-5.2)")
		}
	})

	t.Run(caseNameP3("E7-16", "unsupported_approver_spec_is_refused_at_registration"), func(t *testing.T) {
		// fail closed: v1 supports director | any_member | <user uuid> only.
		r := mustRegister(t, registerCase{
			kind: "question", question: "독자?", proposedDefault: "투자자",
			approverSpec: "role:reviewer",
		})
		if r.Accepted {
			t.Fatal("role-based approver specs are not supported in v1 and are refused AT STORAGE " +
				"(FR-5.4) — accepting one silently lets anyone approve")
		}
		if r.ErrorCode != "validation" {
			t.Errorf("error = %q, want validation (openapi 422)", r.ErrorCode)
		}
	})

	t.Run(caseNameP3("E7-16", "the_three_supported_approver_specs_are_accepted"), func(t *testing.T) {
		for _, spec := range []string{"director", "any_member", userM2.String()} {
			r := mustRegister(t, registerCase{
				kind: "question", question: "독자?", proposedDefault: "투자자", approverSpec: spec,
			})
			if !r.Accepted {
				t.Errorf("approver_spec %q must be accepted (FR-5.4), got error=%q", spec, r.ErrorCode)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 2. The turn keeps running until turn_end — E7-02, E7-03
// ---------------------------------------------------------------------------

// pendingTurnResult describes the task after the agent ignored the "end your
// turn" instruction and kept working, and then after turn_end finally arrived.
type pendingTurnResult struct {
	// MessagesStored is how many of the agent's post-request messages were
	// kept. They already happened; dropping them rewrites history (FR-7.1
	// step 2).
	MessagesStored int
	TaskStatus     string

	// After turn_end (FR-7.1 step 3):
	CardPosted        bool   // HITL card on the session timeline
	InboxSeverity     string // "action_required" for the Director
	ProcessRunning    bool   // the runtime process is gone
	OccupiesSlot      bool   // waiting_human does NOT occupy a concurrency slot
	WorkdirPreserved  bool   // …but the workdir IS kept (FR-5.4)
	HeartbeatRequired bool   // a task with no process sends no heartbeat
}

// pendingTurn is wired by T-S5: register a request, post `posts` more
// messages, then deliver turn_end when `turnEnd` is true.
var pendingTurn func(posts int, turnEnd bool) pendingTurnResult

func mustPendingTurn(t *testing.T, posts int, turnEnd bool) pendingTurnResult {
	t.Helper()
	if pendingTurn == nil {
		t.Fatalf("unimplemented: the HITL transition (FR-7.1 'HITL 전이 시점'). " +
			"T-S5 must wire `pendingTurn` — see the P3a hand-off report 'required API'")
	}
	return pendingTurn(posts, turnEnd)
}

func TestHitlTurnEndTransitionGolden(t *testing.T) {
	t.Run(caseNameP3("E7-02", "messages_posted_after_the_request_are_kept_and_the_task_still_runs"), func(t *testing.T) {
		r := mustPendingTurn(t, 2, false)
		if r.MessagesStored != 2 {
			t.Errorf("stored messages = %d, want 2 — they already happened (FR-7.1 step 2)", r.MessagesStored)
		}
		if r.TaskStatus != "running" {
			t.Errorf("task status = %q, want running — the agent ignoring the instruction does not "+
				"transition the task; turn_end does (E7-02)", r.TaskStatus)
		}
	})

	t.Run(caseNameP3("E7-03", "turn_end_moves_to_waiting_human_and_releases_the_slot"), func(t *testing.T) {
		r := mustPendingTurn(t, 0, true)
		if r.TaskStatus != "waiting_human" {
			t.Fatalf("task status = %q, want waiting_human on turn_end (FR-7.1 step 3)", r.TaskStatus)
		}
		if r.ProcessRunning {
			t.Error("the runtime process ends — HITL does not hold a process for up to 24h (FR-5.4)")
		}
		if r.OccupiesSlot {
			t.Error("waiting_human is excluded from max_concurrent_tasks and max_parallel_lanes (FR-5.4)")
		}
		if !r.WorkdirPreserved {
			t.Error("the workdir IS preserved — the answer resumes the same work (FR-5.4)")
		}
		if r.HeartbeatRequired {
			t.Error("a waiting_human attempt sends no heartbeat (daemon-protocol §4.2)")
		}
		if !r.CardPosted {
			t.Error("the request is posted to the session timeline as a card (FR-5.2)")
		}
		if r.InboxSeverity != "action_required" {
			t.Errorf("inbox severity = %q, want action_required (FR-8)", r.InboxSeverity)
		}
	})
}

// ---------------------------------------------------------------------------
// 3. Responding — E7-07 … E7-11, E7-15, E7-17
// ---------------------------------------------------------------------------

// respondCase is one call of openapi respondHitlRequest.
type respondCase struct {
	kind         string // question | approval
	approverSpec string // director | any_member | <uuid>
	responder    uuid.UUID
	// isDeputy marks the responder as the session's deputy_director_user_id.
	isDeputy bool

	elapsed time.Duration // since the request was created (due_in = 24h)

	answer   string
	approved *bool
	reason   string

	// second is E7-08: this is the SECOND response to the same request.
	second bool
	// firstAnswer is what the first response recorded, so the row can assert
	// the stored answer did not move.
	firstAnswer string
}

// respondResult is the effect of the response.
type respondResult struct {
	Accepted  bool
	ErrorCode string // "forbidden" (403) when the responder may not answer yet

	// CanRespondFrom is the contract's hint for a greyed-out button: the
	// instant the deputy may answer (half the deadline).
	CanRespondFrom time.Duration

	Status       string // open | answered | auto_answered
	StoredAnswer string
	// Ignored is the contract's `ignored: true` — a second response is not an
	// error, it is a no-op (FR-5.4, E7-08).
	Ignored bool

	DecisionRecords int
	// TaskStatus after the response: the answer re-queues a NEW attempt
	// (FR-5.4). `running` would mean resuming a process that no longer exists.
	TaskStatus string
	// PromptSections is what the resume prompt carries. E7-07 wants the
	// question/answer pair; E7-17 wants approved:false and the reason.
	PromptSections []string
	PromptApproved *bool
	PromptReason   string
}

var respondHitl func(c respondCase) respondResult

func mustRespond(t *testing.T, c respondCase) respondResult {
	t.Helper()
	if respondHitl == nil {
		t.Fatalf("unimplemented: HITL response (FR-5.4, openapi respondHitlRequest — the P2 slice " +
			"answers only the platform-issued user_approval, handlers_hitl.go:47). " +
			"T-S5 must wire `respondHitl` — see the P3a hand-off report 'required API'")
	}
	return respondHitl(c)
}

func hasSection(sections []string, want string) bool {
	for _, s := range sections {
		if s == want {
			return true
		}
	}
	return false
}

func boolp(b bool) *bool { return &b }

func TestHitlRespondGolden(t *testing.T) {
	t.Run(caseNameP3("E7-07", "director_answer_records_a_decision_and_requeues_a_new_attempt"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "question", approverSpec: "director", responder: userDir,
			elapsed: time.Hour, answer: "경영진",
		})
		if !r.Accepted {
			t.Fatalf("the Director may answer a director-spec request (error=%q)", r.ErrorCode)
		}
		if r.Status != "answered" {
			t.Errorf("status = %q, want answered", r.Status)
		}
		if r.StoredAnswer != "경영진" {
			t.Errorf("stored answer = %q, want 경영진", r.StoredAnswer)
		}
		if r.DecisionRecords != 1 {
			t.Errorf("decision records = %d, want exactly 1 (FR-5.2)", r.DecisionRecords)
		}
		if r.TaskStatus != "queued" {
			t.Errorf("task = %q, want queued — the answer re-queues a NEW attempt; waiting_human "+
				"cannot go back to running (FR-7.1 N4, E5-01)", r.TaskStatus)
		}
		if !hasSection(r.PromptSections, "question_answer") {
			t.Errorf("resume prompt sections = %v, want a question/answer section (E7-07, §8.4)", r.PromptSections)
		}
	})

	t.Run(caseNameP3("E7-08", "a_second_response_is_ignored_not_an_error"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "question", approverSpec: "director", responder: userDir,
			elapsed: 2 * time.Hour, answer: "실무자", second: true, firstAnswer: "경영진",
		})
		if r.ErrorCode != "" {
			t.Errorf("error = %q — a second response is NOT an error (FR-5.4 'idempotent')", r.ErrorCode)
		}
		if !r.Ignored {
			t.Error("the contract answers 200 with ignored: true (openapi respondHitlRequest)")
		}
		if r.StoredAnswer != "경영진" {
			t.Errorf("stored answer = %q, want the FIRST answer 경영진 to stand", r.StoredAnswer)
		}
		if r.DecisionRecords != 1 {
			t.Errorf("decision records = %d, want 1 — the ignored response records nothing", r.DecisionRecords)
		}
	})

	t.Run(caseNameP3("E7-09", "deputy_before_half_the_deadline_is_refused"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "question", approverSpec: "director", responder: userDep, isDeputy: true,
			elapsed: 11 * time.Hour, answer: "경영진",
		})
		if r.Accepted {
			t.Fatal("the deputy may answer only after HALF the deadline (FR-5.4 M7) — 11h < 12h")
		}
		if r.ErrorCode != "forbidden" {
			t.Errorf("error = %q, want forbidden (openapi 403)", r.ErrorCode)
		}
		if r.CanRespondFrom != dueIn/2 {
			t.Errorf("can_respond_from = %s, want %s — the UI shows \"HH:MM부터\" and needs the instant "+
				"(openapi Problem.can_respond_from, E7-09)", r.CanRespondFrom, dueIn/2)
		}
	})

	t.Run(caseNameP3("E7-10", "deputy_after_half_the_deadline_is_accepted"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "question", approverSpec: "director", responder: userDep, isDeputy: true,
			elapsed: 12*time.Hour + time.Minute, answer: "경영진",
		})
		if !r.Accepted {
			t.Fatalf("`director` means \"Director, and after half the deadline the deputy\" (FR-5.4 M7) "+
				"— notifying the deputy without granting the right makes the notification useless (error=%q)", r.ErrorCode)
		}
		if r.Status != "answered" {
			t.Errorf("status = %q, want answered", r.Status)
		}
		if r.TaskStatus != "queued" {
			t.Errorf("task = %q, want queued", r.TaskStatus)
		}
	})

	t.Run(caseNameP3("E7-11", "a_plain_member_may_never_respond"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "question", approverSpec: "director", responder: userM2,
			elapsed: 20 * time.Hour, answer: "경영진",
		})
		if r.Accepted {
			t.Fatal("a workspace member sees the card but has no response right (FR-5.3)")
		}
		if r.ErrorCode != "forbidden" {
			t.Errorf("error = %q, want forbidden", r.ErrorCode)
		}
		if r.CanRespondFrom != 0 {
			t.Errorf("can_respond_from = %s, want 0 — a plain member never becomes eligible, and "+
				"showing a time would promise a right they will not get", r.CanRespondFrom)
		}
	})

	t.Run(caseNameP3("E7-15", "an_overdue_request_can_still_be_answered"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "approval", approverSpec: "director", responder: userDir,
			elapsed: 30 * time.Hour, approved: boolp(true),
		})
		if !r.Accepted {
			t.Fatalf("overdue is a flag, not a closed state — a late answer is still an answer "+
				"(FR-5.4 s-9, E7-15). error=%q", r.ErrorCode)
		}
		if r.Status != "answered" {
			t.Errorf("status = %q, want answered", r.Status)
		}
	})

	t.Run(caseNameP3("E7-17", "a_rejected_approval_resumes_the_task_it_does_not_fail_it"), func(t *testing.T) {
		r := mustRespond(t, respondCase{
			kind: "approval", approverSpec: "director", responder: userDir,
			elapsed: time.Hour, approved: boolp(false), reason: "근거가 부족합니다",
		})
		if !r.Accepted {
			t.Fatalf("a rejection is a normal answer (error=%q)", r.ErrorCode)
		}
		if r.TaskStatus == "failed" {
			t.Fatal("a rejected approval is NOT a failure — it is a branch the agent handles (FR-5.4)")
		}
		if r.TaskStatus != "queued" {
			t.Errorf("task = %q, want queued so the agent runs again with the rejection", r.TaskStatus)
		}
		if r.PromptApproved == nil || *r.PromptApproved {
			t.Error("the resume prompt carries approved: false (FR-5.4 '후속 분기', E7-17)")
		}
		if r.PromptReason != "근거가 부족합니다" {
			t.Errorf("prompt reason = %q, want the Director's reason — without it the agent cannot "+
				"act on the rejection", r.PromptReason)
		}
	})
}

// ---------------------------------------------------------------------------
// 4. Deadline expiry — E7-12, E7-13, E7-14
//
// The table is the FR-5.4 M7 grid: (type × autonomy) → what happens at 24h.
// Time comes from the injected clock (contracts/clock), never the wall clock.
// ---------------------------------------------------------------------------

type expiryResult struct {
	Status  string // open | answered | auto_answered
	Overdue bool
	Answer  string // the value the request proceeded with, if any

	TaskStatus      string // queued when it proceeded, waiting_human when it waits
	DecisionRecords int
	// DecisionMarkedAutomatic separates "the Director said 투자자" from "nobody
	// answered and we used the agent's proposal" in the decision log (E7-12).
	DecisionMarkedAutomatic bool
	// InboxTop is the `overdue` sort promotion (FR-5.4, SCREEN §4.6).
	InboxTop bool
}

// expireHitl is wired by T-S5: advance the injected clock past `due_in` for a
// request of this type under this session autonomy.
var expireHitl func(kind, autonomy string, elapsed time.Duration) expiryResult

func mustExpire(t *testing.T, kind, autonomy string, elapsed time.Duration) expiryResult {
	t.Helper()
	if expireHitl == nil {
		t.Fatalf("unimplemented: HITL deadline handling (FR-5.4 '기한 만료 시 동작'). " +
			"T-S5 must wire `expireHitl` — see the P3a hand-off report 'required API'")
	}
	return expireHitl(kind, autonomy, elapsed)
}

func TestHitlExpiryGolden(t *testing.T) {
	past := dueIn + time.Minute

	t.Run(caseNameP3("E7-12", "autonomous_question_proceeds_with_the_proposed_default"), func(t *testing.T) {
		r := mustExpire(t, "question", "autonomous", past)
		if r.Status != "auto_answered" {
			t.Fatalf("status = %q, want auto_answered — the distinct status is what tells a reader "+
				"a human did not answer (FR-5.4 상태 값)", r.Status)
		}
		if r.Answer == "" {
			t.Error("the request proceeds with proposed_default; an empty answer means it stalled anyway")
		}
		if r.TaskStatus != "queued" {
			t.Errorf("task = %q, want queued — proceeding means re-queueing a new attempt", r.TaskStatus)
		}
		if r.DecisionRecords != 1 || !r.DecisionMarkedAutomatic {
			t.Errorf("decision records = %d automatic = %t, want 1 marked automatic (E7-12)",
				r.DecisionRecords, r.DecisionMarkedAutomatic)
		}
	})

	t.Run(caseNameP3("E7-13", "guided_question_keeps_waiting_and_is_flagged_overdue"), func(t *testing.T) {
		r := mustExpire(t, "question", "guided", past)
		if r.Status != "open" {
			t.Fatalf("status = %q, want open — `expired` is not a status (FR-5.4 s-9)", r.Status)
		}
		if !r.Overdue {
			t.Error("the overdue FLAG is set instead")
		}
		if !r.InboxTop {
			t.Error("the inbox sorts overdue items to the top (FR-5.4, SCREEN §4.6)")
		}
		if r.TaskStatus != "waiting_human" {
			t.Errorf("task = %q, want waiting_human — nothing proceeded", r.TaskStatus)
		}
	})

	t.Run(caseNameP3("E7-14", "approval_never_auto_proceeds_even_when_autonomous"), func(t *testing.T) {
		r := mustExpire(t, "approval", "autonomous", past)
		if r.Status != "open" {
			t.Fatalf("status = %q, want open. Silence is neither approval nor rejection: approving "+
				"empties the human gate, rejecting kills healthy work (FR-5.4 M7)", r.Status)
		}
		if !r.Overdue {
			t.Error("overdue flag is set, the status is not")
		}
		if r.Answer != "" {
			t.Errorf("answer = %q, want empty — an approval has no proposed_default to proceed with", r.Answer)
		}
		if r.TaskStatus != "waiting_human" {
			t.Errorf("task = %q, want waiting_human — no auto-approve AND no auto-reject", r.TaskStatus)
		}
		if r.DecisionRecords != 0 {
			t.Errorf("decision records = %d, want 0 — nothing was decided", r.DecisionRecords)
		}
	})

	t.Run(caseNameP3("E7-14", "info_never_auto_proceeds_either"), func(t *testing.T) {
		// FR-5.4's table names approval AND info. Implementing the rule for
		// `approval` alone passes the row above and auto-answers info.
		r := mustExpire(t, "info", "autonomous", past)
		if r.Status != "open" {
			t.Errorf("status = %q, want open — approval and info always wait (FR-5.4 표)", r.Status)
		}
	})

	// The type × autonomy grid as an ORDER of decisions, so an implementation
	// cannot satisfy the rows above by keying on autonomy alone.
	t.Run(caseNameP3("E7-12", "expiry_grid_type_decides_before_autonomy"), func(t *testing.T) {
		grid := []struct {
			kind, autonomy string
			wantStatus     string
		}{
			{"question", "autonomous", "auto_answered"},
			{"question", "guided", "open"},
			{"choice", "autonomous", "auto_answered"},
			{"choice", "guided", "open"},
			{"approval", "autonomous", "open"},
			{"approval", "guided", "open"},
			{"info", "autonomous", "open"},
			{"info", "guided", "open"},
		}
		for _, g := range grid {
			got := mustExpire(t, g.kind, g.autonomy, past)
			if got.Status != g.wantStatus {
				t.Errorf("(%s × %s) → %q, want %q", g.kind, g.autonomy, got.Status, g.wantStatus)
			}
			if !got.Overdue && got.Status == "open" {
				t.Errorf("(%s × %s) stayed open without the overdue flag", g.kind, g.autonomy)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 5. HITL does not stop the rest of the session — E7-18
// ---------------------------------------------------------------------------

type concurrencyResult struct {
	SessionState string
	// OtherLaneRuns is whether the unrelated lane kept going.
	OtherLaneRuns bool
	// SlotsUsed counts the concurrency slots occupied. waiting_human is
	// excluded (FR-5.4), so a session with 1 waiting + 1 running uses 1.
	SlotsUsed int
}

var concurrencyUnderHitl func(waitingHuman, running int) concurrencyResult

func TestHitlDoesNotBlockOtherLanesGolden(t *testing.T) {
	t.Run(caseNameP3("E7-18", "a_waiting_lane_does_not_pause_the_session_or_other_lanes"), func(t *testing.T) {
		if concurrencyUnderHitl == nil {
			t.Fatalf("unimplemented: concurrency accounting with waiting_human excluded (FR-5.4). " +
				"T-S5 must wire `concurrencyUnderHitl` — see the P3a hand-off report")
		}
		r := concurrencyUnderHitl(1, 1)
		if r.SessionState != "active" {
			t.Errorf("session = %q, want active — one agent's question does not pause the session", r.SessionState)
		}
		if !r.OtherLaneRuns {
			t.Error("R's unrelated lane keeps running (FR-5.2 last bullet, E7-18)")
		}
		if r.SlotsUsed != 1 {
			t.Errorf("slots used = %d, want 1 — waiting_human occupies none (FR-5.4)", r.SlotsUsed)
		}
	})
}

// ---------------------------------------------------------------------------
// 6. `status set blocked` is the delegator path, not the Director path — E7-19
//
// This row is here rather than in the router golden because it pins the
// BOUNDARY between the two escalation paths: an agent that picks the wrong one
// still gets the documented behaviour of the path it picked. Routing it to the
// Director "because the question is for a human" would make the two paths
// indistinguishable and FR-6.2.1's immediate delegator wake unobservable.
// ---------------------------------------------------------------------------

type escalationResult struct {
	Path            string // "blocked" | "hitl"
	DelegatorWoken  bool
	HitlRequests    int
	InboxItems      int
	TurnEndRequired bool
}

var escalate func(via string, delegator uuid.UUID) escalationResult

func TestHitlVsBlockedPathGolden(t *testing.T) {
	t.Run(caseNameP3("E7-19", "blocked_wakes_the_delegator_even_when_the_question_is_for_a_human"), func(t *testing.T) {
		if escalate == nil {
			t.Fatalf("unimplemented: the blocked/HITL path split (FR-6.2.1 vs FR-5.1). " +
				"T-S5 must wire `escalate` — see the P3a hand-off report")
		}
		r := escalate("blocked", agentW)
		if r.Path != "blocked" {
			t.Fatalf("path = %q — the server follows the call the agent made, not the one it should "+
				"have made (E7-19)", r.Path)
		}
		if !r.DelegatorWoken {
			t.Error("`status set blocked` wakes the delegator immediately (FR-6.2.1)")
		}
		if r.HitlRequests != 0 {
			t.Errorf("hitl requests = %d, want 0 — blocked does not create a HITL request", r.HitlRequests)
		}
		if !r.TurnEndRequired {
			t.Error("blocked also ends the turn (FR-6.2.1)")
		}
	})

	t.Run(caseNameP3("E7-19", "hitl_ask_reaches_the_director_and_wakes_no_delegator"), func(t *testing.T) {
		if escalate == nil {
			t.Fatalf("unimplemented: see the row above")
		}
		r := escalate("hitl", agentW)
		if r.DelegatorWoken {
			t.Error("`colab hitl ask` is the human path — it must not wake the delegating agent")
		}
		if r.HitlRequests != 1 {
			t.Errorf("hitl requests = %d, want 1", r.HitlRequests)
		}
		if r.InboxItems != 1 {
			t.Errorf("inbox items = %d, want 1 for the Director (FR-8)", r.InboxItems)
		}
	})
}

// silence the unused-identifier check for fixtures the wiring PR will use.
var _ = []uuid.UUID{agentR, taskW1}
