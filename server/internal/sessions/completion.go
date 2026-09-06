package sessions

import (
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
)

// Completion conditions (PRD FR-2.2). Four atom types combine with AND/OR, and
// two asymmetries are easy to get backwards:
//
//   - criteria_met alone is rejected at creation (the agent would score its own
//     work) while agent_approval alone is allowed (a different role reviews).
//   - budget exhaustion is NOT completion. It pauses (FR-2.2, FR-2.1).
const (
	CondArtifactSubmitted = "artifact_submitted"
	CondAgentApproval     = "agent_approval"
	CondUserApproval      = "user_approval"
	CondCriteriaMet       = "criteria_met"
	CondManual            = "manual"
)

// Condition is one atom of the tree. Agent names the agent for the two
// agent-driven types; Who is the contract's role shorthand ("assignee").
type Condition struct {
	Type  string     `json:"type"`
	Agent *uuid.UUID `json:"agent_id,omitempty"`
	Who   string     `json:"who,omitempty"`
}

// Tree is the stored completion_condition: one operator over N atoms.
type Tree struct {
	Op         string      `json:"op"`
	Conditions []Condition `json:"conditions"`
}

// ErrInvalidTree is returned by ValidateTree; the handler maps it to 422.
var ErrInvalidTree = errors.New("sessions: invalid completion condition")

// ValidateTree is the session-creation guard (E6-07). criteria_met may never
// stand alone and may never sit under OR, where it would complete the session
// by itself — FR-2.2 requires it to be ANDed with an approval.
func ValidateTree(t Tree) error {
	if len(t.Conditions) == 0 {
		return fmt.Errorf("%w: at least one condition is required", ErrInvalidTree)
	}
	op := normOp(t.Op)
	if op != "AND" && op != "OR" {
		return fmt.Errorf("%w: op must be AND or OR, got %q", ErrInvalidTree, t.Op)
	}
	hasCriteria, hasApproval := false, false
	for _, c := range t.Conditions {
		switch c.Type {
		case CondCriteriaMet:
			hasCriteria = true
		case CondUserApproval, CondManual:
			hasApproval = true
		case CondAgentApproval:
			hasApproval = true
		case CondArtifactSubmitted:
		default:
			return fmt.Errorf("%w: unknown condition type %q", ErrInvalidTree, c.Type)
		}
	}
	if hasCriteria && (!hasApproval || op != "AND") {
		return fmt.Errorf("%w: criteria_met must be ANDed with an approval — the platform "+
			"may not be the only judge of the work it scored", ErrInvalidTree)
	}
	return nil
}

// Event is something that happens in the session and may satisfy an atom.
type Event struct {
	Kind  string // artifact_submit | review_approve | director_approve | director_reject | director_end | budget_exhausted
	Actor uuid.UUID
	Note  string
}

// State is the set of atoms already satisfied. It is carried on the session
// row, not recomputed: E6-04 pins that an artifact_submitted flag SURVIVES a
// Director rejection — the artifact still exists.
type State struct{ Met map[string]bool }

// Outcome is the session after applying one event.
type Outcome struct {
	SessionState string // active | paused | completing | completed
	PauseReason  string
	MetAtoms     []string

	HitlIssued  bool
	HitlSource  string // "system" for a platform-issued request
	HitlTaskID  uuid.UUID
	SummaryMsgs int

	DecisionRecorded bool
	RejectReason     string

	AgentTriggered bool
	CLIError       string
}

// ApplyEvent folds one event into the completion state and reports what the
// session does next.
func ApplyEvent(t Tree, st State, ev Event) Outcome {
	met := map[string]bool{}
	for k, v := range st.Met {
		if v {
			met[k] = true
		}
	}
	o := Outcome{SessionState: "active"}

	switch ev.Kind {
	case "artifact_submit":
		// E6-02: the atom names an agent. A submit by anyone else is stored as
		// an artifact but does not satisfy the condition.
		if namesActor(t, CondArtifactSubmitted, ev.Actor) {
			met[CondArtifactSubmitted] = true
		}
	case "review_approve":
		if namesActor(t, CondAgentApproval, ev.Actor) {
			met[CondAgentApproval] = true
		} else {
			o.CLIError = "review approve: this session designates a different reviewer"
			o.MetAtoms = atoms(met)
			return o
		}
	case "director_approve":
		met[CondUserApproval] = true
	case "director_reject":
		// A rejection ends nothing and triggers nobody: the human gives the
		// next instruction (E6-04). The reason goes to the decision log.
		o.DecisionRecorded, o.RejectReason = true, ev.Note
		o.MetAtoms = atoms(met)
		return o
	case "director_end":
		met[CondManual] = true
	case "budget_exhausted":
		// FR-2.2: a budget limit is a `limits` matter, not a completion
		// condition. It pauses and asks the Director whether to continue.
		o.SessionState, o.PauseReason = "paused", "budget"
		o.HitlIssued, o.HitlSource = true, "system"
		o.MetAtoms = atoms(met)
		return o
	}

	o.MetAtoms = atoms(met)
	if Satisfied(t, met) {
		// active → completing → completed; the summary posts exactly one
		// session_summary message (FR-2.4).
		o.SessionState, o.SummaryMsgs = "completed", 1
		return o
	}
	// FR-2.2: user_approval is issued BY THE PLATFORM once every other atom is
	// met — it is not an agent's turn, so task_id stays empty and the source is
	// `system` (§7).
	if needsUserApproval(t, met) {
		o.HitlIssued, o.HitlSource, o.HitlTaskID = true, "system", uuid.Nil
	}
	return o
}

// Satisfied evaluates the tree over the met atoms.
func Satisfied(t Tree, met map[string]bool) bool {
	if len(t.Conditions) == 0 {
		return false
	}
	if normOp(t.Op) == "OR" {
		for _, c := range t.Conditions {
			if met[c.Type] {
				return true
			}
		}
		return false
	}
	for _, c := range t.Conditions {
		if !met[c.Type] {
			return false
		}
	}
	return true
}

// needsUserApproval reports whether the tree has a user_approval atom that is
// the only thing still missing.
func needsUserApproval(t Tree, met map[string]bool) bool {
	has := false
	for _, c := range t.Conditions {
		if c.Type == CondUserApproval {
			has = true
			continue
		}
		if normOp(t.Op) == "AND" && !met[c.Type] {
			return false
		}
	}
	return has && !met[CondUserApproval]
}

func namesActor(t Tree, typ string, actor uuid.UUID) bool {
	for _, c := range t.Conditions {
		if c.Type == typ && c.Agent != nil && *c.Agent == actor {
			return true
		}
	}
	return false
}

func atoms(met map[string]bool) []string {
	out := make([]string, 0, len(met))
	for k, v := range met {
		if v {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

func normOp(op string) string {
	switch op {
	case "and", "AND":
		return "AND"
	case "or", "OR":
		return "OR"
	}
	return op
}

// SummaryOutcome is the result of the summary step of `completing`.
type SummaryOutcome struct {
	SessionState  string
	SummaryMsgs   int
	FeedError     bool
	ErrorCategory string
}

// RunSummary settles a `completing` session. A refused or failed platform-LLM
// summary must NOT strand the session in `completing` (E6-11): the session is
// over either way, and the activity feed carries the error with its
// stop_details.category.
func RunSummary(stopReason, category string) SummaryOutcome {
	if stopReason == "" || stopReason == "end_turn" || stopReason == "stop" {
		return SummaryOutcome{SessionState: "completed", SummaryMsgs: 1}
	}
	if category == "" {
		category = stopReason
	}
	return SummaryOutcome{SessionState: "completed", FeedError: true, ErrorCategory: category}
}
