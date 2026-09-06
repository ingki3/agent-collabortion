//go:build p2golden

// Wiring for the E6 golden table. The evaluation lives in completion.go.
package sessions

import "github.com/google/uuid"

func init() {
	applyEvent = adaptApplyEvent
	validateTree = adaptValidateTree
	runSummary = adaptRunSummary
}

func adaptApplyEvent(tr tree, ev event) completionResult {
	o := ApplyEvent(toTree(tr), priorState(tr, ev), Event{Kind: ev.Kind, Actor: ev.Actor, Note: ev.Note})
	return completionResult{
		SessionState: o.SessionState, PauseReason: o.PauseReason, MetAtoms: o.MetAtoms,
		HitlIssued: o.HitlIssued, HitlSource: o.HitlSource, HitlTaskID: o.HitlTaskID,
		SummaryMsgs: o.SummaryMsgs, DecisionRecorded: o.DecisionRecorded,
		RejectReason: o.RejectReason, AgentTriggered: o.AgentTriggered, CLIError: o.CLIError,
	}
}

// priorState reconstructs the premise the table states in prose. E6-03 and
// E6-04 are both "E6-01 후": the Director can only approve or reject a
// user_approval HITL, and FR-2.2 issues that request ONLY once every other
// atom is met — so reaching a director verdict implies the rest are satisfied.
func priorState(tr tree, ev event) State {
	st := State{Met: map[string]bool{}}
	if ev.Kind != "director_approve" && ev.Kind != "director_reject" {
		return st
	}
	for _, c := range tr.Conditions {
		if c.Type != CondUserApproval {
			st.Met[c.Type] = true
		}
	}
	return st
}

func adaptValidateTree(tr tree) error { return ValidateTree(toTree(tr)) }

func adaptRunSummary(stopReason, category string) summaryOutcome {
	o := RunSummary(stopReason, category)
	return summaryOutcome{
		SessionState: o.SessionState, SummaryMsgs: o.SummaryMsgs,
		FeedError: o.FeedError, ErrorCategory: o.ErrorCategory,
	}
}

func toTree(tr tree) Tree {
	out := Tree{Op: tr.Op}
	for _, c := range tr.Conditions {
		cond := Condition{Type: c.Type}
		if c.Agent != uuid.Nil {
			id := c.Agent
			cond.Agent = &id
		}
		out.Conditions = append(out.Conditions, cond)
	}
	return out
}
