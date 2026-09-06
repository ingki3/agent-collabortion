// Wiring for the E8 golden table (resume · retry · re-instruction). The
// decisions live in resume.go; this file only shapes them and supplies the
// fixtures the table states in prose (the previous task's id, its trigger, the
// ids a re-instruction allocates).
//
// PRODUCTION CALL SITES:
//
//	planAttempt          → PlanAttempt          queue.buildBundle (resume ref,
//	                                            `<resumed>`, posted ids, history
//	                                            header), tasks.requeueLocked
//	                                            (queued vs failed, the retry
//	                                            budget, E8-09's notice),
//	                                            httpapi.RestartLane and
//	                                            httpapi.answerAgentHitl
//	finishWithSessionRef → ValidateSessionRef   tasks.Service.Finish
//	                     + PlanBundleResume     queue.buildBundle
package tasks

import (
	"encoding/json"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

func init() {
	planAttempt = adaptPlanAttempt
	finishWithSessionRef = adaptSessionRefRoundTrip
}

func adaptPlanAttempt(c resumeCase) attemptPlan {
	p := PlanAttempt(AttemptInput{
		TaskID: p3TaskOld, Attempt: 1, MaxAttempts: defaultMaxAttempts,
		TriggerMessageID: p3Trigger,
		// A re-instruction needs a row and a trigger; the caller allocates
		// them, the planner decides whether they are used (E8-06).
		NewTaskID: uuid.New(), NewTriggerMessageID: uuid.New(),
		SessionRef: c.SessionRef, RefRuntimeKind: c.RefRuntimeKind,
		ProfileRuntimeKind: c.ProfileRuntimeKind,
		ResumeRejected:     c.ResumeRejected,
		Cause:              c.Cause,
		PostedMessageIDs:   c.PostedMessageIDs,
		PrevWorkdir:        c.PrevWorkdir, PrevPhase: c.PrevPhase,
		HistoryTotal: c.HistoryTotal, HistoryLimit: c.HistoryLimit,
		NewInstruction: c.NewInstruction, AlternateProfile: c.AlternateProfile,
	})
	posted := p.PostedMessageIDs
	if posted == nil {
		posted = []uuid.UUID{}
	}
	return attemptPlan{
		TaskID: p.TaskID, Attempt: p.Attempt,
		RestartedFromTaskID: p.RestartedFromTaskID, TriggerMessageID: p.TriggerMessageID,
		TaskStatus: p.TaskStatus, LaneStatus: p.LaneStatus,
		Workdir: p.Workdir, WorkdirCreated: p.WorkdirCreated,
		ResumeRef: p.ResumeRef, ResumeAttempted: p.ResumeAttempted, ColdStart: p.ColdStart,
		HasResumedSection: p.HasResumedSection, PostedMessageIDs: posted,
		WorkdirCheckInstruction: p.WorkdirCheckInstruction,
		ColdStartSections:       p.ColdStartSections, PromptContains: p.PromptContains,
		HistoryIncluded: p.HistoryIncluded, HistoryTotal: p.HistoryTotal,
		HistoryTruncated: p.HistoryTruncated,
		Retries:          p.Retries, DirectorNotified: p.DirectorNotified,
		HandedToAnotherMachine: p.HandedToAnotherMachine, AgentStatus: p.AgentStatus,
	}
}

// adaptSessionRefRoundTrip chains the two halves E8-13 is about: what `finish`
// stores and what the NEXT bundle carries. The store between them is the lane
// row; here it is the jsonb the row would hold, so the row trip is the same
// marshal/unmarshal production does.
func adaptSessionRefRoundTrip(runtimeKind, sessionID string) sessionRefRoundTrip {
	in := &contracts.RuntimeSessionRef{
		RuntimeKind: contracts.RuntimeKind(runtimeKind), SessionID: sessionID,
	}
	stored, err := ValidateSessionRef(in)
	if err != nil {
		return sessionRefRoundTrip{FinishStatus: 422}
	}
	raw, _ := json.Marshal(stored)
	out := sessionRefRoundTrip{
		FinishStatus:      200,
		StoredRuntimeKind: string(stored.RuntimeKind), StoredSessionID: stored.SessionID,
	}
	if next := PlanBundleResume(raw, runtimeKind); next != nil {
		out.NextBundleResumeSessionID = next.SessionID
	}
	return out
}
