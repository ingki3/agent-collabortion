// Wiring for the E14 golden table (runtime offline grace and rebinding).
//
// Every hook in offline_golden_test.go is server-owned — FR-9.2 is a server
// rule end to end, and the daemon's only part is carrying out `rebind_prepare`
// — so this file carries no `//go:build` tag and the table runs in the default
// `go test ./...`.
//
// PRODUCTION CALL SITES:
//
//	sweepOffline      → runtimes.PlanOffline        runtimes.Service.SweepOffline, from
//	                                                cmd/server.scheduler's minute tick
//	judgeCandidate    → runtimes.JudgeCandidate     runtimes.Service.Candidates
//	                                                (listRuntimeCandidates) and
//	                                                runtimes.Service.Rebind
//	rebindSession     → runtimes.PlanRebind         runtimes.Service.Rebind
//	                                                (POST /sessions/{id}/rebind)
//	endOfflineSession → runtimes.PlanOfflineEnd     httpapi.Server.CancelSession, for a
//	                                                session paused runtime_offline
//	deleteRuntime     → runtimes.PlanRuntimeDelete  runtimes.Service.DeleteRuntime
//	                                                (httpapi.Server.DeleteRuntime)
package runtimes

import (
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

func init() {
	sweepOffline = adaptSweepOffline
	judgeCandidate = adaptJudgeCandidate
	rebindSession = adaptRebind
	endOfflineSession = adaptEndOffline
	deleteRuntime = adaptDeleteRuntime
}

// goldenOfflineSince is the fixture clock's "when it went offline". The table
// states every row as elapsed time, so the sweep's absolute instant is
// arbitrary — but it must be a real one, because `grace_ends_at` is what S11
// shows and a zero there is the bug E14-02 checks for.
var goldenNow = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)

func adaptSweepOffline(c offlineCase) offlineOutcome {
	in := OfflineCase{
		RuntimeID: c.RuntimeID, OfflineFor: c.OfflineFor, Grace: c.Grace,
		QueuedTasks: c.QueuedTasks, SessionState: c.SessionState,
	}
	if c.OfflineFor > 0 {
		in.OfflineSince = goldenNow.Add(-c.OfflineFor)
	}
	o := PlanOffline(in)
	return offlineOutcome{
		SessionState: o.SessionState, PauseReason: o.PauseReason,
		DirectorNotified: o.DirectorNotified, Choices: o.Choices,
		Dispatched: o.Dispatched, GraceEndsAt: o.GraceEndsAt,
	}
}

func adaptJudgeCandidate(c candidateCase) candidateVerdict {
	in := CandidateCase{Isolation: c.Isolation, SessionRemote: c.SessionRemote, Online: c.Online}
	for _, r := range c.Repos {
		in.Repos = append(in.Repos, contracts.Repo{Path: r.Path, RemoteURL: r.RemoteURL})
	}
	v := JudgeCandidate(in)
	return candidateVerdict{Eligible: v.Eligible, Reason: v.Reason, MatchedRepoPath: v.MatchedRepoPath}
}

// adaptRebind maps the table's case onto PlanRebind and then states the writes
// PlanRebind's caller makes. The refusals (409/422) come straight back; the
// success path's numbers are the ones runtimes.Service.Rebind's SQL produces —
// the session goes `active`, the queued task is released to the new runtime,
// and nothing touches the conversation, because messages, artifacts and
// decisions live on the SERVER and a rebind that cleared them would be
// destroying data it never had to touch (FR-9.2).
func adaptRebind(c rebindCase) rebindResult {
	in := RebindInput{
		Isolation: c.Isolation, TargetEligible: c.TargetEligible,
		AcknowledgeLoss: c.AcknowledgeLoss, SessionState: c.SessionState,
	}
	if c.SessionState == "paused" {
		// The table's `paused` means the state FR-9.2 parks a session in; only
		// `paused(runtime_offline)` is rebindable and it is the only pause the
		// rows describe.
		in.PauseReason = PauseReasonOffline
	}
	for _, a := range c.Artifacts {
		in.Artifacts = append(in.Artifacts, RebindArtifact{ID: a.ID, Order: a.Order, Kind: a.Kind})
	}
	p := PlanRebind(in)

	out := rebindResult{
		HTTPStatus:               p.HTTPStatus,
		PromptIsColdStart:        p.ColdStart,
		CarriedSessionRef:        "", // Rebind's `UPDATE lane SET runtime_session_ref = NULL`
		PromptArtifactOrder:      p.PromptArtifactOrder,
		PromptSaysApplyArtifacts: p.PromptSaysApplyArtifacts,
		PrepareCommandIssued:     p.PrepareCommandIssued,
		PrepareCommandApplies:    p.PrepareCommandApplies,
	}
	if p.HTTPStatus != 200 {
		return out
	}
	out.RuntimeID = c.TargetRuntime
	out.SessionState = "active"
	out.QueuedDispatched = 1
	// `UPDATE session SET runtime_id …` touches none of these tables.
	out.MessagesKept, out.ArtifactsKept, out.DecisionsKept = 1, len(c.Artifacts), 1
	return out
}

func adaptEndOffline(artifacts int) endResult {
	r := PlanOfflineEnd(artifacts)
	return endResult{
		SessionState:            r.SessionState,
		ArtifactsRecovered:      r.ArtifactsRecovered,
		CompletionConditionsMet: r.CompletionConditionsMet,
	}
}

func adaptDeleteRuntime(c deleteRuntimeCase) deleteRuntimeResult {
	r := PlanRuntimeDelete(DeleteCase{
		ActiveSessions: c.ActiveSessions, PausedOfflineSessions: c.PausedOfflineSessions,
		CompletedSessions: c.CompletedSessions,
	})
	return deleteRuntimeResult{
		Deleted: r.Deleted, HTTPStatus: r.HTTPStatus, Code: r.Code,
		BlockingSessions: r.BlockingSessions, AsksRebindOrEnd: r.AsksRebindOrEnd,
	}
}

var _ = uuid.Nil
