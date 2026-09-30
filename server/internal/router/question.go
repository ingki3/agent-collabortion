package router

// question.go is PRD FR-3.8 2 (v0.19.15) on the routing side: an agent's
// mention of another agent, with no card, asks a QUESTION — the task it makes
// is `kind: question` (answer only: the question table, and the lane does not
// move). Three cases are not questions (Lead 판정 2026-09-30 Q1·Q2):
//
//   - a reply in a `blocked_q` thread (FR-6.2.1): the delegator answering the
//     child's question re-enters the child to continue its work;
//   - what a question turn says back to the agent that asked: that is the
//     answer, and it wakes the asker in its own lane's kind (card or normal);
//   - platform triggers (a rejection re-entering the submitter's lane).
//
// A person's message is never a question (instruct), and neither are the
// server's own wake-ups (join, re-entry report, blocked notice).

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
)

// questionCtx is what one post knows about questions.
type questionCtx struct {
	// fromAgent: the author is an agent writing from a task.
	fromAgent bool
	// blockedThread: the post replies in a blocked_q card's thread.
	blockedThread bool
	// asker: when the author's own task is a question task, the agent that
	// asked it (the author of its trigger message), with its name.
	asker     *uuid.UUID
	askerName string
	// asks: the post's mentions make at least one question (the speech).
	asks bool
}

func (q questionCtx) askerAddr() *messages.Addressee {
	if q.asker == nil {
		return nil
	}
	id := *q.asker
	return &messages.Addressee{Kind: "agent", ID: &id, Name: q.askerName}
}

// makesQuestion is the rule for one trigger of the post.
func (q questionCtx) makesQuestion(tr Trigger) bool {
	return PlanQuestion(q.fromAgent, q.blockedThread, tr.Rule, q.asker, tr.AgentID)
}

// PlanQuestion is the pure rule (question_test.go's table).
func PlanQuestion(fromAgent, blockedThread bool, rule int, asker *uuid.UUID, target uuid.UUID) bool {
	if !fromAgent || blockedThread || rule != 2 {
		return false
	}
	if asker != nil && *asker == target {
		return false // the answer to the one who asked
	}
	return true
}

func questionPremise(ctx context.Context, tx pgx.Tx, author Author, th thread, platform *PlatformTrigger) (questionCtx, error) {
	var q questionCtx
	if author.Type != "agent" || author.TaskID == nil {
		return q, nil
	}
	q.fromAgent = true
	// PRD FR-3.8 2 ①: only the DELEGATOR's reply in a blocked_q thread is the
	// answer that re-enters the child (#400 리뷰 400a NN1). Anyone else who
	// mentions an agent there asks a question like anywhere else — a passer-by
	// must not re-open another agent's card lane.
	if th.RootKind == "blocked_q" && author.AgentID != nil && th.RootLane != uuid.Nil {
		var deleg *uuid.UUID
		err := tx.QueryRow(ctx, `
			SELECT d.agent_id FROM lane l JOIN task d ON d.id = l.delegated_from_task_id
			WHERE l.id = $1`, th.RootLane).Scan(&deleg)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return q, err
		}
		q.blockedThread = deleg != nil && *deleg == *author.AgentID
	}
	var kind string
	var asker *uuid.UUID
	var askerName *string
	err := tx.QueryRow(ctx, `
		SELECT t.kind, CASE WHEN m.author_type = 'agent' THEN m.author_id END, a.name
		FROM task t LEFT JOIN message m ON m.id = t.trigger_message_id
		LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
		WHERE t.id = $1`, *author.TaskID).Scan(&kind, &asker, &askerName)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, nil
	}
	if err != nil {
		return q, err
	}
	if kind == cards.KindQuestion && asker != nil {
		q.asker = asker
		if askerName != nil {
			q.askerName = *askerName
		}
	}
	q.asks = !q.blockedThread && platform == nil
	return q, nil
}

// refineAsks narrows `asks` to the post's actual mentions: a question turn
// that only mentions the asker is answering, not asking.
func (q *questionCtx) refineAsks(agentMentions []uuid.UUID) {
	if !q.asks {
		return
	}
	for _, id := range agentMentions {
		if q.asker == nil || id != *q.asker {
			return
		}
	}
	q.asks = false
}
