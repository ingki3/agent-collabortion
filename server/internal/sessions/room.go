package sessions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Since 0025 (PRD v0.19 §7) a v0.18 session is stored as a `room` plus a
// `work`: the room keeps the id, the runtime, isolation, limits and autonomy;
// the work holds the goal side (title, goal, criteria, Director, status,
// cost). A room may hold several missions since T-R1b2; the old one-mission
// reading joins the room to ITS mission, room.legacy_work_id (LegacyJoin),
// which never fans out. (The `/sessions/*` API that answered that shape was
// removed in openapi v0.3.0, D22.)

// firstLine is the §10 migration rule's room description: the goal's first
// line. 0025 derives it with split_part(goal, E'\n', 1); this is the same cut.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func placeholders(n int) string {
	ph := make([]string, n)
	for i := range ph {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	return strings.Join(ph, ", ")
}

// seedPeople writes the human rows of room_participant for a new room the way
// 0025 seeds a migrated one: the owner row, then the Director and deputy as
// members (skipped when they are already there — the owner is usually the
// Director). Nothing in R1a reads these rows yet; they exist so a room created
// between R1a and R1b is indistinguishable from a migrated one.
func seedPeople(ctx context.Context, q db.DBTX, roomID, owner, director uuid.UUID, deputy *uuid.UUID, now time.Time) error {
	if _, err := q.Exec(ctx, `INSERT INTO room_participant (room_id, user_id, role, joined_at) VALUES ($1, $2, 'owner', $3)`, roomID, owner, now); err != nil {
		return fmt.Errorf("sessions: seed room owner: %w", err)
	}
	for _, u := range []*uuid.UUID{&director, deputy} {
		if u == nil {
			continue
		}
		if err := addMember(ctx, q, roomID, *u, now); err != nil {
			return err
		}
	}
	return nil
}

// addMember makes a person a room member unless they already have a row.
func addMember(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID, now time.Time) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO room_participant (room_id, user_id, role, joined_at) VALUES ($1, $2, 'member', $3)
		ON CONFLICT (room_id, user_id) WHERE user_id IS NOT NULL
		DO UPDATE SET left_at = NULL, joined_at = EXCLUDED.joined_at WHERE room_participant.left_at IS NOT NULL`, roomID, userID, now); err != nil {
		return fmt.Errorf("sessions: add room member: %w", err)
	}
	return nil
}
