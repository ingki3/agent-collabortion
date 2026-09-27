package router

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	openapi_types "github.com/oapi-codegen/runtime/types"
)

// MaxAttachments is openapi v0.3.7 MessageCreate.attachment_ids maxItems.
const MaxAttachments = 10

// NormalizeAttachments is the id list as the message will keep it: first
// occurrence wins and the order is the sender's (「중복은 한 번」). More than
// MaxAttachments DISTINCT ids is 422 — duplicates do not count toward it.
func NormalizeAttachments(ids *[]openapi_types.UUID) ([]uuid.UUID, error) {
	if ids == nil {
		return nil, nil
	}
	seen := map[uuid.UUID]bool{}
	out := make([]uuid.UUID, 0, len(*ids))
	for _, id := range *ids {
		u := uuid.UUID(id)
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	if len(out) > MaxAttachments {
		return nil, apperr.Validation(apperr.Field("attachment_ids", "too_many_attachments",
			fmt.Sprintf("파일은 한 메시지에 %d개까지 붙일 수 있습니다 — %d개를 보냈습니다", MaxAttachments, len(out))))
	}
	return out, nil
}

// attach writes message_attachment rows (migration 0040) after checking that
// every id is an artifact of THIS room — 422 attachment_not_in_room names the
// first one that is not, whether it lives in another room, another workspace
// or nowhere (the answer is the same so a stranger learns nothing about an id
// outside their room). The rows point at the artifact row itself, i.e. at the
// version the sender saw (Message.attachments 「버전 그대로」).
func attach(ctx context.Context, tx pgx.Tx, roomID, msgID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id FROM artifact WHERE session_id = $1 AND id = ANY($2)`, roomID, ids)
	if err != nil {
		return err
	}
	in := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		in[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if !in[id] {
			return apperr.Validation(apperr.Field("attachment_ids", "attachment_not_in_room",
				fmt.Sprintf("이 방의 파일이 아닙니다 (%s) — 이 방에 올린 파일만 붙일 수 있습니다", id)))
		}
	}
	for i, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO message_attachment (message_id, artifact_id, position) VALUES ($1, $2, $3)`,
			msgID, id, i); err != nil {
			return fmt.Errorf("router: attach: %w", err)
		}
	}
	return nil
}
