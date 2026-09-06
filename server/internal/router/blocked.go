package router

import "github.com/google/uuid"

// BlockedPlan is what `colab status set blocked --note "<질문>"` makes the
// server do (PRD FR-6.2.1). It is the escape hatch from rule 8: the suppression
// hides completion reports, and without this path a child could never ask its
// delegator a question at all.
type BlockedPlan struct {
	// QuestionCardID is the message the server posts into the lane's thread.
	// A status change alone leaves nothing to reply to (리뷰#04-2), so the card
	// exists to BE the thread root; lane.blocked_message_id caches its id and
	// lane.blocked_note caches only the last note — the history is the message.
	QuestionCardID       uuid.UUID
	LaneBlockedMessageID uuid.UUID
	Note                 string

	// DelegatorAgentID is woken immediately, mention suppression
	// notwithstanding: waiting for the join would deliver a question raised at
	// minute 2 forty minutes later, behind the slowest sibling.
	DelegatorAgentID *uuid.UUID
	DelegatorWoken   bool

	// ToDirectorInbox is the 리뷰#04-3 case: a lane the Director created by
	// mentioning an agent has no delegator to wake, so the question card goes
	// to the Director's inbox as `lane_blocked` / action_required (FR-8).
	ToDirectorInbox bool

	// TurnEndRequired is the CLI's answer (contracts/colab-cli.md §2.2): the
	// child ends its turn here and the lane becomes `blocked`.
	TurnEndRequired bool
}

// PlanBlocked builds that plan. newID mints the question card's id so the
// caller can insert the message and cache the same id on the lane in one
// transaction; production passes uuid.New.
func PlanBlocked(delegator *uuid.UUID, note string, newID func() uuid.UUID) BlockedPlan {
	id := newID()
	p := BlockedPlan{
		QuestionCardID: id, LaneBlockedMessageID: id, Note: note,
		DelegatorAgentID: delegator, TurnEndRequired: true,
	}
	if delegator != nil {
		p.DelegatorWoken = true
	} else {
		p.ToDirectorInbox = true
	}
	return p
}
