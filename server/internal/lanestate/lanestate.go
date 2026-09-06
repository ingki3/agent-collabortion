// Package lanestate is the pure part of the lane layer: which lane a trigger
// belongs to (PRD FR-3.3 "lane 해소 규칙" 1–4), whether a terminal lane may be
// re-entered (FR-6.2) and how isolation binds lanes to workdirs (FR-6.1).
//
// It is a leaf: no database, no clock, no contracts. Both the router (which
// resolves a lane while posting a message) and the task layer (which reasons
// about re-entry) depend on it, so it must not depend on either.
package lanestate

import (
	"time"

	"github.com/google/uuid"
)

// Status values of the lane_status enum (0001_init.sql).
const (
	Queued       = "queued"
	Running      = "running"
	WaitingHuman = "waiting_human"
	Blocked      = "blocked"
	Paused       = "paused"
	Done         = "done"
	Failed       = "failed"
)

// Terminal reports whether the lane's process has ended. FR-6.2: `blocked` is
// terminal for join purposes — otherwise the child that asked a question would
// hold its siblings' join forever.
func Terminal(status string) bool {
	return status == Done || status == Failed || status == Blocked
}

// Reentrant reports whether lane rule 3 may re-enter this lane. `failed` is
// deliberately excluded: a mention at a failed lane forks a new one (E5-10).
func Reentrant(status string) bool { return status == Done || status == Blocked }

// Candidate is one existing lane of the agent the trigger names.
type Candidate struct {
	ID           uuid.UUID
	AgentID      uuid.UUID
	Status       string
	ReentryCount int
	LastUsed     time.Time
}

// Request is everything the four lane rules read. Zero values mean "not part
// of the premise": no thread, no delegation, no existing lanes.
type Request struct {
	AgentID  uuid.UUID
	Existing []Candidate

	// ThreadRootLaneID is the lane that owns the thread root of a reply
	// (rule 1). uuid.Nil when the message is top level.
	ThreadRootLaneID uuid.UUID

	// ViaDelegate is `colab lane delegate` (rule 2) — always a new lane.
	ViaDelegate     bool
	DelegatorTaskID uuid.UUID

	// TopLevelMention gates rule 3; ForceNewLane is the composer's
	// "새 lane으로 보내기" toggle, which skips rule 3 (PRD t-2).
	TopLevelMention bool
	ForceNewLane    bool
}

// Decision is the resolved lane. Created means "the caller must insert one";
// LaneID is then uuid.Nil because the row does not exist yet.
type Decision struct {
	Rule                int
	LaneID              uuid.UUID
	Created             bool
	Reentry             bool
	FromStatus          string
	ReentryCount        int
	Status              string
	DelegatedFromTaskID uuid.UUID
}

// Resolve applies the four lane rules in order (PRD FR-3.3).
//
//	1 thread reply whose root belongs to a lane → that lane
//	2 `colab lane delegate`                     → always a new lane
//	3 top-level mention with an existing lane   → the most recent one,
//	                                              re-entered when done/blocked
//	4 otherwise                                 → a new lane
func Resolve(req Request) Decision {
	if req.ThreadRootLaneID != uuid.Nil {
		d := Decision{Rule: 1, LaneID: req.ThreadRootLaneID, Status: Running}
		if c, ok := find(req.Existing, req.ThreadRootLaneID); ok {
			d.FromStatus = c.Status
			d.ReentryCount = c.ReentryCount
			if Reentrant(c.Status) {
				d.Reentry = true
				d.ReentryCount = c.ReentryCount + 1
			} else {
				d.Status = c.Status
			}
		}
		return d
	}
	if req.ViaDelegate {
		return Decision{Rule: 2, Created: true, Status: Queued, DelegatedFromTaskID: req.DelegatorTaskID}
	}
	if req.TopLevelMention && !req.ForceNewLane {
		if c, ok := mostRecent(req.Existing, req.AgentID); ok && c.Status != Failed {
			d := Decision{Rule: 3, LaneID: c.ID, FromStatus: c.Status, ReentryCount: c.ReentryCount, Status: c.Status}
			if Reentrant(c.Status) {
				d.Reentry = true
				d.ReentryCount = c.ReentryCount + 1
				d.Status = Running
			}
			return d
		}
	}
	return Decision{Rule: 4, Created: true, Status: Queued}
}

// Reentry is the answer to "a trigger landed on a lane in this status".
type Reentry struct {
	Allowed      bool
	NewLane      bool
	Status       string
	ReentryCount int
}

// Reenter is the FR-6.2 lane re-entry machine: done and blocked go back to
// running with reentry_count+1; anything else (notably failed) forks a new
// lane (E5-09, E5-10).
func Reenter(from string, count int) Reentry {
	if Reentrant(from) {
		return Reentry{Allowed: true, Status: Running, ReentryCount: count + 1}
	}
	return Reentry{Allowed: false, NewLane: true, Status: Queued}
}

// Layout is FR-6.1: lane and workdir are different things, and isolation binds
// them. `worktree` shares one workdir per agent, so those lanes run
// sequentially (FR-6.3); `container` and `none` give each lane its own.
type Layout struct {
	WorkdirCount   int
	ConcurrentRuns int
}

// LayoutFor counts the workdirs and the concurrent runs the given lanes get.
// Lanes in a state whose process has ended occupy no slot (FR-6.3 t-1).
func LayoutFor(isolation string, lanes []Candidate) Layout {
	agents := map[uuid.UUID]bool{}
	live := 0
	for _, l := range lanes {
		agents[l.AgentID] = true
		if l.Status != WaitingHuman && !Terminal(l.Status) {
			live++
		}
	}
	if isolation == "worktree" {
		return Layout{WorkdirCount: len(agents), ConcurrentRuns: min(len(agents), live)}
	}
	return Layout{WorkdirCount: len(lanes), ConcurrentRuns: live}
}

func find(cs []Candidate, id uuid.UUID) (Candidate, bool) {
	for _, c := range cs {
		if c.ID == id {
			return c, true
		}
	}
	return Candidate{}, false
}

// mostRecent is lane rule 3's "가장 최근 lane": last used wins, and ties fall
// back to the later position in the caller's ordering.
func mostRecent(cs []Candidate, agent uuid.UUID) (Candidate, bool) {
	var best Candidate
	found := false
	for _, c := range cs {
		if c.AgentID != agent {
			continue
		}
		if !found || !c.LastUsed.Before(best.LastUsed) {
			best, found = c, true
		}
	}
	return best, found
}
