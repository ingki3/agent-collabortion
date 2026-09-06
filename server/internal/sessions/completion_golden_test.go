//go:build p2golden

// Golden table for completion conditions (EVAL E6, 11 rows) — PRD FR-2.2 and
// the session state machine FR-2.3.
//
// Four atom types combine with AND/OR:
//
//	artifact_submitted  a NAMED agent calls submit_artifact          (agent)
//	agent_approval      a NAMED agent calls `colab review approve`   (agent)
//	user_approval       the Director approves                        (PLATFORM —
//	                    auto-issued once every other condition is met)
//	criteria_met        the platform LLM scores acceptance_criteria  (platform,
//	                    NEVER allowed on its own)
//	manual              the Director ends the session directly       (human)
//
// Two asymmetries this table pins, because they are easy to get backwards:
//   - criteria_met alone is REJECTED at session creation (self-scoring), while
//     agent_approval alone is ALLOWED (a different role reviews) — FR-2.2.
//   - budget exhaustion is NOT completion. It pauses (FR-2.2, FR-2.1).
//
// Written by the Reviewer before the implementation (PLAN §10.1, P2a).
package sessions

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

func caseName(eval, name string) string {
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

var (
	agW  = uuid.MustParse("a0000000-0000-4000-8000-000000000003")
	agR  = uuid.MustParse("a0000000-0000-4000-8000-000000000002")
	agQA = uuid.MustParse("a0000000-0000-4000-8000-000000000004")
)

// ---------------------------------------------------------------------------
// What the implementation must expose.
// ---------------------------------------------------------------------------

// condition is one atom of the completion tree.
type condition struct {
	Type  string    // artifact_submitted | agent_approval | user_approval | criteria_met | manual
	Agent uuid.UUID // the named agent, for the two agent-driven types
}

// tree is a completion condition tree: one operator over N atoms.
type tree struct {
	Op         string // "AND" | "OR"
	Conditions []condition
}

func and(cs ...condition) tree { return tree{Op: "AND", Conditions: cs} }
func or(cs ...condition) tree  { return tree{Op: "OR", Conditions: cs} }

func artifact(a uuid.UUID) condition { return condition{Type: "artifact_submitted", Agent: a} }
func agentAppr(a uuid.UUID) condition {
	return condition{Type: "agent_approval", Agent: a}
}

var userAppr = condition{Type: "user_approval"}
var criteria = condition{Type: "criteria_met"}
var manual = condition{Type: "manual"}

// event is something that happens in the session and may satisfy an atom.
type event struct {
	Kind  string    // artifact_submit | review_approve | director_approve | director_reject | director_end | budget_exhausted
	Actor uuid.UUID // the agent that acted, when it was an agent
	Note  string
}

// completionResult is the state after applying an event to a session.
type completionResult struct {
	SessionState string // active | paused | completing | completed
	PauseReason  string

	// MetAtoms lists the condition types currently satisfied.
	MetAtoms []string

	// HitlIssued describes the platform-issued approval request (FR-2.2:
	// user_approval is auto-issued once the rest are met).
	HitlIssued  bool
	HitlSource  string // must be "system" for platform-issued
	HitlTaskID  uuid.UUID
	SummaryMsgs int

	// DecisionRecorded is set when a rejection stores its reason.
	DecisionRecorded bool
	RejectReason     string

	// AgentTriggered reports whether the event woke any agent.
	AgentTriggered bool

	// CLIError is what `colab review approve` returns to a non-designated agent.
	CLIError string
}

// applyEvent is wired by T-S2. Signature in the report.
var applyEvent func(t tree, ev event) completionResult

// validateTree is the session-creation guard (E6-07).
var validateTree func(t tree) error

func mustApply(t *testing.T, tr tree, ev event) completionResult {
	t.Helper()
	if applyEvent == nil {
		t.Fatalf("unimplemented: completion condition evaluation (FR-2.2). T-S2 must wire " +
			"`applyEvent` (see /tmp/p2a-report.md 'required API')")
	}
	return applyEvent(tr, ev)
}

func hasAtom(atoms []string, want string) bool {
	for _, a := range atoms {
		if a == want {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// E6-01 … E6-04 — the default tree: artifact_submitted(W) AND user_approval
// ---------------------------------------------------------------------------

func TestCompletionDefaultTreeGolden(t *testing.T) {
	def := and(artifact(agW), userAppr)

	t.Run(caseName("E6-01", "designated_agent_submit_issues_platform_approval_hitl"), func(t *testing.T) {
		r := mustApply(t, def, event{Kind: "artifact_submit", Actor: agW})

		if !hasAtom(r.MetAtoms, "artifact_submitted") {
			t.Errorf("met atoms = %v, want artifact_submitted", r.MetAtoms)
		}
		if !r.HitlIssued {
			t.Fatal("once the other atoms are met the PLATFORM issues the user_approval HITL (FR-2.2)")
		}
		if r.HitlSource != "system" {
			t.Errorf("hitl source = %q, want system — this is not an agent's turn", r.HitlSource)
		}
		if r.HitlTaskID != uuid.Nil {
			t.Errorf("hitl task_id = %s, want empty for a system-issued request (§7)", r.HitlTaskID)
		}
		if r.SessionState != "active" {
			t.Errorf("session = %q, want active — approval has not happened yet", r.SessionState)
		}
	})

	t.Run(caseName("E6-02", "wrong_agent_submit_does_not_satisfy_the_condition"), func(t *testing.T) {
		r := mustApply(t, def, event{Kind: "artifact_submit", Actor: agR})

		if hasAtom(r.MetAtoms, "artifact_submitted") {
			t.Error("artifact_submitted names W — a submit by R must not satisfy it")
		}
		if r.HitlIssued {
			t.Error("no HITL may be issued while the tree is unsatisfied")
		}
		if r.SessionState != "active" {
			t.Errorf("session = %q, want active", r.SessionState)
		}
	})

	t.Run(caseName("E6-03", "director_approval_completes_the_session"), func(t *testing.T) {
		r := mustApply(t, def, event{Kind: "director_approve"})

		if r.SessionState != "completed" {
			t.Errorf("session = %q, want completed (active → completing → completed)", r.SessionState)
		}
		if r.SummaryMsgs != 1 {
			t.Errorf("session_summary messages = %d, want exactly 1 (FR-2.4)", r.SummaryMsgs)
		}
	})

	t.Run(caseName("E6-04", "director_rejection_keeps_the_session_and_the_met_flag"), func(t *testing.T) {
		r := mustApply(t, def, event{Kind: "director_reject", Note: "근거가 부족합니다"})

		if r.SessionState != "active" {
			t.Errorf("session = %q, want active — a rejection does not end the session", r.SessionState)
		}
		if !hasAtom(r.MetAtoms, "artifact_submitted") {
			t.Error("the artifact_submitted flag SURVIVES a rejection (E6-04) — the artifact still exists")
		}
		if !r.DecisionRecorded || r.RejectReason == "" {
			t.Error("the rejection reason must be stored in the decision record")
		}
		if r.AgentTriggered {
			t.Error("a rejection triggers no agent — the human gives the next instruction (E6-04)")
		}
	})
}

// ---------------------------------------------------------------------------
// E6-05, E6-06 — agent_approval alone is legal (scenario B)
// ---------------------------------------------------------------------------

func TestCompletionAgentApprovalGolden(t *testing.T) {
	solo := tree{Op: "AND", Conditions: []condition{agentAppr(agQA)}}

	t.Run(caseName("E6-05", "designated_reviewer_approval_completes_without_a_human_gate"), func(t *testing.T) {
		r := mustApply(t, solo, event{Kind: "review_approve", Actor: agQA})

		if r.SessionState != "completed" {
			t.Errorf("session = %q, want completed — agent_approval alone is allowed (FR-2.2)", r.SessionState)
		}
		if r.HitlIssued {
			t.Error("no user_approval is auto-issued: the tree has no such atom")
		}
	})

	t.Run(caseName("E6-06", "non_designated_agent_approval_is_rejected_by_the_cli"), func(t *testing.T) {
		r := mustApply(t, solo, event{Kind: "review_approve", Actor: agR})

		if r.SessionState == "completed" || r.SessionState == "completing" {
			t.Errorf("session = %q — only the designated agent (QA) can approve", r.SessionState)
		}
		if r.CLIError == "" {
			t.Error("`colab review approve` by a non-designated agent must return an error (E6-06)")
		}
	})
}

// ---------------------------------------------------------------------------
// E6-07 — criteria_met may not stand alone
// ---------------------------------------------------------------------------

func TestCompletionTreeValidationGolden(t *testing.T) {
	t.Run(caseName("E6-07", "criteria_met_alone_is_rejected_at_creation"), func(t *testing.T) {
		if validateTree == nil {
			t.Fatalf("unimplemented: completion tree validation. T-S2 must wire `validateTree` " +
				"(see /tmp/p2a-report.md 'required API')")
		}
		solo := tree{Op: "AND", Conditions: []condition{criteria}}
		if err := validateTree(solo); err == nil {
			t.Error("criteria_met alone must be rejected — the agent would score its own work (FR-2.2)")
		}

		// Paired with an approval it is fine.
		paired := and(criteria, userAppr)
		if err := validateTree(paired); err != nil {
			t.Errorf("criteria_met AND user_approval must be accepted, got %v", err)
		}

		// The asymmetry: agent_approval alone IS allowed (a different role reviews).
		agentSolo := tree{Op: "AND", Conditions: []condition{agentAppr(agQA)}}
		if err := validateTree(agentSolo); err != nil {
			t.Errorf("agent_approval alone must be accepted (scenario B), got %v", err)
		}
	})
}

// ---------------------------------------------------------------------------
// E6-08 — manual
// ---------------------------------------------------------------------------

func TestCompletionManualGolden(t *testing.T) {
	t.Run(caseName("E6-08", "director_end_button_completes"), func(t *testing.T) {
		tr := tree{Op: "AND", Conditions: []condition{manual}}
		r := mustApply(t, tr, event{Kind: "director_end"})
		if r.SessionState != "completed" {
			t.Errorf("session = %q, want completed via completing", r.SessionState)
		}
		if r.SummaryMsgs != 1 {
			t.Errorf("session_summary messages = %d, want 1", r.SummaryMsgs)
		}
	})
}

// ---------------------------------------------------------------------------
// E6-09 — OR short-circuits
// ---------------------------------------------------------------------------

func TestCompletionOrGolden(t *testing.T) {
	t.Run(caseName("E6-09", "or_tree_completes_on_either_branch"), func(t *testing.T) {
		tr := or(artifact(agW), agentAppr(agQA))
		r := mustApply(t, tr, event{Kind: "review_approve", Actor: agQA})

		if r.SessionState != "completing" && r.SessionState != "completed" {
			t.Errorf("session = %q, want completing/completed — QA's branch is enough under OR", r.SessionState)
		}
		if hasAtom(r.MetAtoms, "artifact_submitted") {
			t.Error("W never submitted; only the agent_approval branch is met")
		}
	})
}

// ---------------------------------------------------------------------------
// E6-10 — budget exhaustion pauses, it does not complete
// ---------------------------------------------------------------------------

func TestCompletionBudgetIsNotCompletionGolden(t *testing.T) {
	t.Run(caseName("E6-10", "budget_exhaustion_pauses_and_asks_the_director"), func(t *testing.T) {
		tr := and(artifact(agW), userAppr)
		r := mustApply(t, tr, event{Kind: "budget_exhausted"})

		if r.SessionState == "completed" || r.SessionState == "completing" {
			t.Errorf("session = %q — a budget limit is NOT a completion condition (FR-2.2)", r.SessionState)
		}
		if r.SessionState != "paused" {
			t.Errorf("session = %q, want paused", r.SessionState)
		}
		if r.PauseReason != "budget" {
			t.Errorf("pause_reason = %q, want budget", r.PauseReason)
		}
		if !r.HitlIssued || r.HitlSource != "system" {
			t.Error("the platform asks the Director whether to continue (source: system)")
		}
	})
}

// ---------------------------------------------------------------------------
// E6-11 — a refused summary must not strand the session in `completing`
// ---------------------------------------------------------------------------

// summaryOutcome is the result of the summary step of `completing`.
type summaryOutcome struct {
	SessionState  string
	SummaryMsgs   int
	FeedError     bool
	ErrorCategory string
}

var runSummary func(stopReason, category string) summaryOutcome

func TestCompletionSummaryFailureGolden(t *testing.T) {
	t.Run(caseName("E6-11", "platform_llm_refusal_still_completes_without_a_summary"), func(t *testing.T) {
		if runSummary == nil {
			t.Fatalf("unimplemented: summary step of `completing`. T-S2 must wire `runSummary` " +
				"(see /tmp/p2a-report.md 'required API')")
		}
		o := runSummary("refusal", "refusal")

		if o.SessionState != "completed" {
			t.Errorf("session = %q, want completed — a refused summary must not strand `completing`", o.SessionState)
		}
		if o.SummaryMsgs != 0 {
			t.Errorf("summary messages = %d, want 0 (there is no summary)", o.SummaryMsgs)
		}
		if !o.FeedError {
			t.Error("the activity feed must show the error")
		}
		if o.ErrorCategory == "" {
			t.Error("the feed error carries stop_details.category")
		}
	})
}
