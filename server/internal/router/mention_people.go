package router

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
)

// notifyMentionedPeople files a `mention` inbox item (info) for each person a
// posted message mentions — PRD FR-3.2 「[@김민수](mention://user/…) 사람
// (Director가 아니어도 알림)」, SCREEN §4.14 「나를 멘션한 메시지」. The item
// quotes the message (quote_message_id) and carries its mission.
//
// Only a live participant of the room is told: someone who left, or who was
// never in an invited room, must not learn of the room through their inbox.
// The author never notifies themselves, and one message is one item per
// person however often it names them.
//
// It wakes nobody: routing (Decide rule 3) never triggers for a person
// (T-HUMANMENTION, colab-cli v0.9.4).
func notifyMentionedPeople(ctx context.Context, tx pgx.Tx, wsID, roomID, msgID uuid.UUID, work *uuid.UUID, author Author, mentions []gen.Mention, now time.Time) error {
	seen := map[uuid.UUID]bool{}
	for _, m := range mentions {
		if m.Kind != gen.MentionKindUser {
			continue
		}
		id, err := uuid.Parse(m.Id)
		if err != nil || seen[id] {
			continue
		}
		seen[id] = true
		if author.Type == "user" && author.UserID != nil && *author.UserID == id {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at, work_id, quote_message_id)
			SELECT mb.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5, $6, $4
			FROM member mb
			JOIN room_participant rp ON rp.room_id = $3 AND rp.user_id = mb.user_id AND rp.left_at IS NULL
			WHERE mb.workspace_id = $7 AND mb.user_id = $8`,
			inbox.TypeMention, inbox.Severity(inbox.TypeMention), roomID, msgID, now, work, wsID, id); err != nil {
			return fmt.Errorf("router: mention inbox: %w", err)
		}
	}
	return nil
}
