// Wiring for the P2a golden tables (PLAN §10.3: the tables themselves are
// read-only for this PR). Every hook here is a thin adapter — the decisions
// live in rules.go, fallback.go, loop.go, blocked.go and internal/lanestate.
package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
)

func init() {
	planFallback = adaptFallback
	setBlocked = adaptBlocked
	resolveLane = adaptResolveLane
	messageArrives = adaptArrival
	layoutFor = adaptLayout
	checkLoop = adaptLoop
}

// t0Fallback is the clock reading rule 7's plan is made at. The golden table
// expresses time as an elapsed duration, so the origin only has to be stable.
var t0Fallback = time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

func adaptFallback(c routeCase, elapsed time.Duration, primaryReplied bool) fallbackPlan {
	d := decideForCase(c)
	f := PlanFallback(d, c.assignee, t0Fallback)
	out := fallbackPlan{}
	if f != nil {
		out.Scheduled = &fallbackWant{agent: f.AgentID, delay: f.Delay}
	}
	o := ResolveFallback(f, elapsed, primaryReplied)
	out.CancelledOnReply, out.PromotedAfter = o.Cancelled, o.Promoted
	return out
}

func adaptBlocked(laneDelegator uuid.UUID, note string) blockedEffect {
	var d *uuid.UUID
	if laneDelegator != uuid.Nil {
		d = &laneDelegator
	}
	p := PlanBlocked(d, note, uuid.New)
	return blockedEffect{
		DelegatorWoken:   p.DelegatorWoken,
		QuestionCardID:   p.QuestionCardID,
		LaneBlockedMsgID: p.LaneBlockedMessageID,
		TurnEndRequired:  p.TurnEndRequired,
	}
}

func adaptResolveLane(c laneCase) laneResult {
	req := lanestate.Request{
		AgentID:          c.agent,
		Existing:         candidates(c.existing),
		ThreadRootLaneID: c.threadRoot,
		ViaDelegate:      c.viaDelegate,
		DelegatorTaskID:  c.delegatorTaskID,
		TopLevelMention:  c.topLevelMent,
		ForceNewLane:     c.forceNewLane,
	}
	d := lanestate.Resolve(req)
	return laneResult{
		Rule: d.Rule, LaneID: d.LaneID, Created: d.Created,
		ReentryCount: d.ReentryCount, Status: d.Status, DelegatedFromID: d.DelegatedFromTaskID,
	}
}

func adaptArrival(lane uuid.UUID, laneStatus string, msgIDs []uuid.UUID) arrivalResult {
	a := PlanArrival(lane, laneStatus, nil, msgIDs)
	return arrivalResult{
		CancelledRunningTurn: a.CancelledRunningTurn,
		QueuedTaskCount:      a.QueuedTaskCount,
		CoalescedMessageIDs:  a.CoalescedMessageIDs,
		LaneOfQueuedTask:     a.LaneID,
	}
}

func adaptLayout(isolation string, lanes []laneState) workdirLayout {
	l := lanestate.LayoutFor(isolation, candidates(lanes))
	return workdirLayout{WorkdirCount: l.WorkdirCount, ConcurrentRuns: l.ConcurrentRuns}
}

func adaptLoop(history []hop, next hop, lim loopLimits, now time.Time) loopVerdict {
	v := CheckLoopLimits(hops(history), toHop(next), Limits(lim), now)
	return loopVerdict{
		Allowed: v.Allowed, TaskCreated: v.TaskCreated, SessionState: v.SessionState,
		PauseReason: v.PauseReason, Detail: v.Detail, HitlToDir: v.HitlToDir,
		ChainDepth: v.ChainDepth, HopsThisWindow: v.HopsThisWindow, PairRoundtrips: v.PairRoundtrips,
	}
}

func candidates(in []laneState) []lanestate.Candidate {
	out := make([]lanestate.Candidate, 0, len(in))
	for _, l := range in {
		out = append(out, lanestate.Candidate{ID: l.id, AgentID: l.agent, Status: l.status, LastUsed: l.lastUsed})
	}
	return out
}

func hops(in []hop) []Hop {
	out := make([]Hop, 0, len(in))
	for _, h := range in {
		out = append(out, toHop(h))
	}
	return out
}

func toHop(h hop) Hop { return Hop{FromAgent: h.fromAgent, ToAgent: h.toAgent, At: h.at} }
