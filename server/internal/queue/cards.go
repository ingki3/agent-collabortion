package queue

// cards.go is harness v0.9.16's three turn-prompt blocks (PRD FR-3.8 5):
// `<card_board>` for an agent that may delegate, `<task_card>` for a card
// task, `<result_cards>` for the delegator's join turn — and the question
// task's first trigger line and the follow-up trigger. Cards change turn to
// turn, so they are TURN PROMPT, never brief ([1]~[8] byte-identical, E12-11),
// and a resumed turn carries them whole (the ③ layer, not the delta).

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/cards"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roles"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// cardBlocks is what one bundle's card blocks are.
type cardBlocks struct {
	Board, TaskCard, ResultCards string
	// QuestionHead is the question task's first line inside <trigger>.
	QuestionHead string
	// FollowUp replaces the trigger of a result_card_missing task.
	FollowUp string
}

func loadCardBlocks(ctx context.Context, tx pgx.Tx, t *tasks.Row, agentRole string, surf Surface) (cardBlocks, error) {
	var out cardBlocks
	mcp := surf.Kind == SurfaceMCP
	// <card_board>: agents that may delegate (card_delegate in this task's
	// allowed commands — so never a question turn), the task's mission.
	if roles.AllowsFor(gen.AgentRole(agentRole), t.Kind, gen.ColabCommandCardDelegate) {
		rows, err := tx.Query(ctx, `SELECT id FROM task_card WHERE room_id = $1 AND work_id IS NOT DISTINCT FROM $2 ORDER BY number`, t.SessionID, t.WorkID)
		if err != nil {
			return out, fmt.Errorf("queue: card board: %w", err)
		}
		var ids []uuid.UUID
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return out, err
			}
			ids = append(ids, id)
		}
		rows.Close()
		var rs []*cards.Row
		for _, id := range ids {
			r, err := cards.Get(ctx, tx, id)
			if err != nil {
				return out, err
			}
			rs = append(rs, r)
		}
		work := "none"
		if t.WorkID != nil {
			work = t.WorkID.String()
		}
		out.Board = cards.BoardBlock(work, rs)
	}
	// <task_card>: a card task — the card's current version whole.
	if t.Kind == cards.KindCard && t.CardID != nil {
		r, err := cards.Get(ctx, tx, *t.CardID)
		if err == nil {
			refs := []cards.RefLine{}
			api, err := cards.ToAPI(ctx, tx, r, cards.Judge{}, false)
			if err != nil {
				return out, err
			}
			for _, x := range api.Refs {
				refs = append(refs, cards.RefLine{Kind: string(x.Kind), ID: x.Id.String(), Label: x.Label, Missing: x.Missing})
			}
			out.TaskCard = cards.TaskCardBlock(r, refs, mcp)
			if t.TriggerReason != nil && *t.TriggerReason == cards.ReasonResultCardMissing {
				out.FollowUp = cards.FollowUpTrigger(r, r.FollowUps, mcp)
			}
		}
	}
	// <result_cards>: the join / re-entry report hung them on this task.
	if len(t.ResultCardIDs) > 0 {
		var rs []*cards.Row
		for _, id := range t.ResultCardIDs {
			r, err := cards.Get(ctx, tx, id)
			if err != nil {
				continue
			}
			rs = append(rs, r)
		}
		out.ResultCards = cards.ResultCardsBlock(rs, mcp)
	}
	// The question's first line names who asked.
	if t.Kind == cards.KindQuestion && t.TriggerMessageID != nil {
		var name string
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(a.name, u.display_name, '') FROM message m
			LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
			LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id WHERE m.id = $1`, *t.TriggerMessageID).Scan(&name); err == nil {
			out.QuestionHead = cards.QuestionHead(name)
		}
	}
	return out, nil
}
