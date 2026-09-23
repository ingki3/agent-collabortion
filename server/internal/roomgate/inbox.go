package roomgate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
)

// Item is one room-owner approval's inbox card (room_paused ·
// isolation_confirm). Actor and QuoteMessage are what the card quotes
// (openapi 0.2.10 card.actor_name · quote) — nil when there is nobody or
// nothing to quote.
type Item struct {
	Type         string
	WorkspaceID  uuid.UUID
	RoomID       uuid.UUID
	HitlID       uuid.UUID
	Created, Due time.Time
	Actor        *uuid.UUID
	QuoteMessage *uuid.UUID
}

// FileInbox files a room-owner approval in the inbox of everyone its approver
// chain reaches (FR-2A.3), each with the basis the card reads (「방장으로서」·
// 「부방장으로서」·「소유자로서」). The chain is the same Approvers.Now judgement
// the banner and the response check use: the owner answers now, the delegate
// from half the deadline — told now, like a room_owner hitl_request's
// delegate (httpapi hitlInbox), so the card is already there when their turn
// comes.
//
// Every room-level stop (budget · loop — and runtime_offline when the room
// gate takes it) comes through here, so the room owner sees ONE room_paused
// card per stop, never a room_paused and a hitl_request for the same request.
func FileInbox(ctx context.Context, tx pgx.Tx, it Item) error {
	ap, err := LoadApprovers(ctx, tx, it.RoomID)
	if err != nil {
		return err
	}
	turn := ap.Now(it.Created, it.Due, it.Created)
	type to struct {
		user  uuid.UUID
		basis string
	}
	targets := []to{{turn.Approver, inbox.BasisRoomOwner}}
	if turn.Next != nil {
		targets = append(targets, to{*turn.Next, turn.NextRole})
	}
	for _, t := range targets {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, recipient_basis, actor_user_id, quote_message_id)
			SELECT m.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5, $8, $9, $10
			FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7
			  AND NOT EXISTS (SELECT 1 FROM inbox_item i WHERE i.member_id = m.id AND i.ref_id = $4)`,
			it.Type, inbox.Severity(it.Type), it.RoomID, it.HitlID, it.Created, it.WorkspaceID, t.user, t.basis,
			it.Actor, it.QuoteMessage); err != nil {
			return fmt.Errorf("roomgate: %s inbox: %w", it.Type, err)
		}
	}
	return nil
}

// firstTurn is who set the room's first run going and the message that did
// it — the task the isolation question holds (FR-2.1.1). The person is the
// task's originator (FR-1.9, the top-of-chain human), else the trigger
// message's human author. Both nil when the room has no queued task with
// either (a system-started run).
func firstTurn(ctx context.Context, q db.DBTX, roomID uuid.UUID) (actor, msg *uuid.UUID, err error) {
	err = q.QueryRow(ctx, `
		SELECT COALESCE(t.originator_user_id, CASE WHEN m.author_type = 'user' THEN m.author_id END), m.id
		  FROM task t LEFT JOIN message m ON m.id = t.trigger_message_id
		 WHERE t.session_id = $1 AND t.status = 'queued'
		 ORDER BY t.created_at, t.id LIMIT 1`, roomID).Scan(&actor, &msg)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("roomgate: first turn: %w", err)
	}
	return actor, msg, nil
}

// QuoteMax is how many characters of the trigger message's first line the
// card quotes (openapi card.quote — "한 줄로 자른 본문").
const QuoteMax = 80

// Quote is card.quote for a message body: its first non-empty line, cut to
// QuoteMax characters with an ellipsis. "" for a body with no text.
func Quote(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) > QuoteMax {
			r := []rune(line)
			return strings.TrimSpace(string(r[:QuoteMax])) + "…"
		}
		return line
	}
	return ""
}
