// Package rooms is PRD v0.19 FR-4.5 — a running agent reading ANOTHER room on
// request (`colab room list` · `colab room read`), and the trail that read
// leaves in both rooms (S23 `listRoomReads`).
//
// The permission is a pure function (Judge) over facts read at the moment of
// the call ([V19-C]: a task lives for hours — HITL 24h, retries, rate_limited
// waits — and a dispatch-time check leaves a window in which a person who has
// already left still lends their rights). Everything else here reads the facts,
// shapes the answer and writes the record.
package rooms

// Membership is the task originator's standing in the TARGET room.
type Membership int

const (
	// NotMember: never had a row there — or the room does not exist, or is in
	// another workspace. The three must be indistinguishable to the caller.
	NotMember Membership = iota
	// Member: a room_participant row with left_at IS NULL.
	Member
	// Left: a row that has left_at. The person once saw the room, so its name
	// is already known to them — the one case the denial may name it.
	Left
)

// Reason is contracts RoomReadDeniedReason.
type Reason string

const (
	OriginatorNotParticipant Reason = "originator_not_participant"
	OriginatorLeft           Reason = "originator_left"
	AgentNotAllowed          Reason = "agent_not_allowed"
	NoOriginator             Reason = "no_originator"
)

// Facts are the four inputs of FR-4.5, read at call time.
type Facts struct {
	// HasOriginator: the reading task has a PERSON originator (task.
	// originator_user_id, inherited by woken turns — NN7). Never substituted
	// by the room owner or the Director.
	HasOriginator bool
	// Originator is that person's standing in the target room.
	Originator Membership
	// AgentParticipant: the reading agent is a participant of the target room.
	AgentParticipant bool
	// Linked: the reader's CURRENT room has a room_link to the target.
	Linked bool
}

// Verdict is Judge's answer.
type Verdict struct {
	Allowed bool
	Reason  Reason // "" when allowed
	// ViaLink: allowed through the reference link, not by the agent's own
	// participation (ReadableRoom.via_link).
	ViaLink bool
	// RevealRoom: the denial may name the target room (originator_left only).
	RevealRoom bool
}

// Judge is FR-4.5's two conditions, both required:
//
//  1. the person originator is a participant of the target room — rights do
//     not rise by going through an agent;
//  2. the agent is a participant of the target room, or the current room links
//     to it.
//
// Order is part of the rule, because the reasons leak different amounts:
//
//   - no_originator first — it says nothing about the target at all;
//   - condition 1 before condition 2 — a room the originator cannot see
//     (including one that does not exist) must answer exactly like a room
//     that does not exist, so `agent_not_allowed` is only ever said about a
//     room the requesting person can see anyway;
//   - `originator_left` only when the read would otherwise have gone through:
//     it names the room, and it exists so a person can tell "my leaving
//     blocked this" apart from the agent's own rights (FR-4.5). When
//     condition 2 fails as well, leaving is not what blocked it, and the room
//     stays hidden (originator_not_participant).
func Judge(f Facts) Verdict {
	if !f.HasOriginator {
		return Verdict{Reason: NoOriginator}
	}
	agentOK := f.AgentParticipant || f.Linked
	switch f.Originator {
	case Member:
	case Left:
		if agentOK {
			return Verdict{Reason: OriginatorLeft, RevealRoom: true}
		}
		return Verdict{Reason: OriginatorNotParticipant}
	default:
		return Verdict{Reason: OriginatorNotParticipant}
	}
	if !agentOK {
		return Verdict{Reason: AgentNotAllowed}
	}
	return Verdict{Allowed: true, ViaLink: !f.AgentParticipant}
}
