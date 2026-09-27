package queue

// Brief [5]'s people (harness v0.9.10, T-HUMANMENTION): the room's human
// participants, so an agent can read from its own brief how to call the
// Director — the defect was that it could not (실사용 2026-09-27, #362).

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// briefHuman is one person of brief [5]: the display name, the ROOM role
// (owner · deputy · member — not the workspace role) and the id the mention
// link needs.
type briefHuman struct {
	UserID uuid.UUID
	Name   string
	Role   string
}

// humanRoleLabel is the room role as brief [5] writes it. The brief is in
// English elsewhere ([4] "Owner:", the roster's agent lines), so the label is
// too — the contract's 방장·부방장·멤버 name the three room_role values.
func humanRoleLabel(role string) string {
	switch role {
	case "owner":
		return "room owner"
	case "deputy":
		return "room deputy"
	default:
		return "member"
	}
}

// briefHumansSQL is the one place brief [5]'s people and their order live
// (harness v0.9.10: 방장 → 부방장 → 멤버, 같으면 이름순; 나간 사람 제외). It is
// a constant so the contract test can read the order out of it.
const briefHumansSQL = `
		SELECT u.id, u.display_name, rp.role::text
		FROM room_participant rp JOIN app_user u ON u.id = rp.user_id
		WHERE rp.room_id = $1 AND rp.left_at IS NULL
		ORDER BY CASE rp.role WHEN 'owner' THEN 0 WHEN 'deputy' THEN 1 ELSE 2 END, u.display_name, u.id`

// briefHumans lists the room's people in the contract's order: owner → deputy
// → member, then by name. People who left are not in it (they are not in
// `/cli/context` humans[] either — the same set).
//
// The order is the room role's, not join order, so the lines are stable while
// the room's people are: two turns of the same room get byte-identical [5]
// (E12-11) and it changes only when a person joins, leaves or changes role.
func briefHumans(ctx context.Context, q db.DBTX, roomID uuid.UUID) ([]briefHuman, error) {
	rows, err := q.Query(ctx, briefHumansSQL, roomID)
	if err != nil {
		return nil, fmt.Errorf("queue: brief humans: %w", err)
	}
	defer rows.Close()
	out := []briefHuman{}
	for rows.Next() {
		var h briefHuman
		var role string
		if err := rows.Scan(&h.UserID, &h.Name, &role); err != nil {
			return nil, err
		}
		h.Role = humanRoleLabel(role)
		out = append(out, h)
	}
	return out, rows.Err()
}
