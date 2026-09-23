package rooms

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// The two seats of a room — owner (room.owner_user_id) and deputy
// (room.deputy_owner_user_id) — are written in two places: the room row and
// the person's room_participant.role. SetOwner and SetDeputy are the only
// writers, so the two never disagree about who holds a seat.

// SetOwner moves the owner seat from `from` to `to` (both people; `to` must
// already be a live participant — the caller checks). The old owner stays in
// the room as a member. A deputy who becomes owner leaves the deputy seat.
func SetOwner(ctx context.Context, q db.DBTX, roomID, from, to uuid.UUID, now time.Time) error {
	if _, err := q.Exec(ctx, `
		UPDATE room SET owner_user_id = $2,
		       deputy_owner_user_id = CASE WHEN deputy_owner_user_id = $2 THEN NULL ELSE deputy_owner_user_id END,
		       updated_at = $3
		WHERE id = $1`, roomID, to, now); err != nil {
		return fmt.Errorf("rooms: set owner: %w", err)
	}
	if _, err := q.Exec(ctx, `
		UPDATE room_participant SET role = 'member' WHERE room_id = $1 AND user_id = $2 AND role = 'owner'`, roomID, from); err != nil {
		return fmt.Errorf("rooms: demote owner: %w", err)
	}
	return upsertRole(ctx, q, roomID, to, RoleOwner, now)
}

// SetDeputy moves the deputy seat from `from` (may be nil) to `to` (nil
// clears it).
func SetDeputy(ctx context.Context, q db.DBTX, roomID uuid.UUID, from, to *uuid.UUID, now time.Time) error {
	if _, err := q.Exec(ctx, `UPDATE room SET deputy_owner_user_id = $2, updated_at = $3 WHERE id = $1`, roomID, to, now); err != nil {
		return fmt.Errorf("rooms: set deputy: %w", err)
	}
	if from != nil {
		if _, err := q.Exec(ctx, `
			UPDATE room_participant SET role = 'member' WHERE room_id = $1 AND user_id = $2 AND role = 'deputy'`, roomID, *from); err != nil {
			return fmt.Errorf("rooms: demote deputy: %w", err)
		}
	}
	if to != nil {
		return upsertRole(ctx, q, roomID, *to, RoleDeputy, now)
	}
	return nil
}

// upsertRole gives a person a live row with this role — re-opening a row they
// left (the unique index is on (room, user) regardless of left_at).
func upsertRole(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID, role string, now time.Time) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO room_participant (room_id, user_id, role, joined_at) VALUES ($1, $2, $3::room_role, $4)
		ON CONFLICT (room_id, user_id) WHERE user_id IS NOT NULL
		DO UPDATE SET role = EXCLUDED.role,
		              joined_at = CASE WHEN room_participant.left_at IS NULL THEN room_participant.joined_at ELSE EXCLUDED.joined_at END,
		              left_at = NULL`, roomID, userID, role, now); err != nil {
		return fmt.Errorf("rooms: seat %s: %w", role, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// §12.1-4 — a person leaves the workspace
// ---------------------------------------------------------------------------

// Successor is §12.1-4's rule as a pure function: the workspace owner who has
// been an owner the longest, never the person leaving. `owners` is ordered
// oldest first (member.created_at). ok is false when no owner remains — the
// caller's PlanRemoval already refuses removing the last owner, so that is a
// bug, not a state.
func Successor(owners []uuid.UUID, leaving uuid.UUID) (uuid.UUID, bool) {
	for _, o := range owners {
		if o != leaving {
			return o, true
		}
	}
	return uuid.Nil, false
}

// DirectorSuccessor is PRD FR-5.3 / §12.1 표의 "Director 가 워크스페이스를
// 떠나면 방장이 Director 를 승계" as a pure function. The contract's
// removeMember still refuses a Director of an open mission (409
// member_is_director), so no production path reaches it in R1b3 — the
// mission stream (R1b2) wires it with the contract change. roomOwner may be
// the leaving person too (they own the room and direct its mission); then
// the room's successor takes both.
func DirectorSuccessor(roomOwner, leaving, roomSuccessor uuid.UUID) uuid.UUID {
	if roomOwner == leaving {
		return roomSuccessor
	}
	return roomOwner
}

// Succession is one room whose owner seat changed because its owner left.
type Succession struct {
	RoomID   uuid.UUID
	RoomName string
	From, To uuid.UUID
}

// LeaveWorkspace runs inside removeMember's transaction: the person's live
// room rows are closed (they are no longer a participant anywhere in this
// workspace), a deputy seat they held is emptied, and every room they owned
// passes to the oldest workspace owner (§12.1-4, SCREEN §2.3 「떠남」). The
// caller posts the timeline line and the activity_log entry for each
// Succession — this package does not know the router.
func LeaveWorkspace(ctx context.Context, tx pgx.Tx, wsID, userID uuid.UUID, now time.Time) ([]Succession, error) {
	rows, err := tx.Query(ctx, `
		SELECT user_id FROM member WHERE workspace_id = $1 AND role = 'owner' ORDER BY created_at, id`, wsID)
	if err != nil {
		return nil, err
	}
	var owners []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		owners = append(owners, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = tx.Query(ctx, `SELECT id, name FROM room WHERE workspace_id = $1 AND owner_user_id = $2 ORDER BY created_at FOR UPDATE`, wsID, userID)
	if err != nil {
		return nil, err
	}
	var owned []Succession
	for rows.Next() {
		var s Succession
		if err := rows.Scan(&s.RoomID, &s.RoomName); err != nil {
			rows.Close()
			return nil, err
		}
		owned = append(owned, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Succession
	if len(owned) > 0 {
		to, ok := Successor(owners, userID)
		if !ok {
			return nil, fmt.Errorf("rooms: no workspace owner left to take over %d rooms", len(owned))
		}
		for _, s := range owned {
			s.From, s.To = userID, to
			if err := SetOwner(ctx, tx, s.RoomID, userID, to, now); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE room SET deputy_owner_user_id = NULL, updated_at = $3
		WHERE workspace_id = $1 AND deputy_owner_user_id = $2`, wsID, userID, now); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE room_participant p SET left_at = $3, role = 'member'
		FROM room r WHERE r.id = p.room_id AND r.workspace_id = $1 AND p.user_id = $2 AND p.left_at IS NULL`, wsID, userID, now); err != nil {
		return nil, err
	}
	return out, nil
}
