// Package roomgate is the room's stop switch (PRD v0.19 FR-2.4 · FR-2A.3 ·
// §12.1-9): `room.blocked_reason`.
//
// A room never pauses — its status is `active ⇄ archived`, the life a person
// sees. What stops spending is a GATE: while `blocked_reason` is set the claim
// hands out none of the room's tasks (queue.Claim), in or out of a mission.
// Four reasons, two ways out:
//
//   - budget · loop · runtime_offline — lifted by the approval HITL the block
//     raised (or by re-binding), never by the op below;
//   - manual — a person put it up (blockRoom) and the same set of people take
//     it down (unblockRoom), no HITL.
//
// # The mirror (T-R1b1 Q2, Lead)
//
// The old `/sessions/*` API (removed in openapi v0.3.0) read a room and its one mission as ONE session whose
// `status` said `paused(budget|loop)`. A budget or loop block that only set the
// room column would read `active` there and resumeSession would answer 409, so
// every client of the old shape would lose the pause. Block therefore also
// parks each ACTIVE mission of the room with the same reason, and marks the
// park as the room's (`paused_detail.room_blocked`). Unblock resumes only the
// marked ones: a mission paused for its OWN budget or by its Director stays
// paused when the room comes back — the room did not stop it.
//
// `loop` is not a WorkPauseReason in the contract (it is the room's), so the
// Work API (R1b2) projects a marked `paused` as the room's reason instead of
// showing `loop` on the mission.
package roomgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// room_blocked_reason (0025).
const (
	ReasonBudget         = "budget"
	ReasonLoop           = "loop"
	ReasonManual         = "manual"
	ReasonRuntimeOffline = "runtime_offline"
)

// MirrorKey marks a mission paused BY the room (see the package comment). It
// lives inside work.paused_detail so it travels with the pause it describes
// and disappears with it; the typed PausedDetail the old Session shape decodes
// drops unknown keys, so nothing about the old response changes.
const MirrorKey = "room_blocked"

var (
	// ErrAlreadyBlocked: the room is stopped already, for this or another
	// reason. One block at a time — the second reason would be lost on the
	// first unblock otherwise (openapi blockRoom `409 already_blocked`).
	ErrAlreadyBlocked = errors.New("roomgate: room already blocked")
	// ErrNotBlocked: nothing to lift, or the gate is up for another reason.
	ErrNotBlocked = errors.New("roomgate: room not blocked for that reason")
	// ErrNotFound: no such room.
	ErrNotFound = errors.New("roomgate: room not found")
)

// State is the room row the gate reads.
type State struct {
	ID            uuid.UUID
	WorkspaceID   uuid.UUID
	Owner         uuid.UUID
	Deputy        *uuid.UUID
	Status        string
	BlockedReason *string
	BlockedDetail []byte
}

const selectState = `SELECT id, workspace_id, owner_user_id, deputy_owner_user_id, status::text, blocked_reason::text, blocked_detail FROM room WHERE id = $1`

func scan(row pgx.Row) (*State, error) {
	var s State
	err := row.Scan(&s.ID, &s.WorkspaceID, &s.Owner, &s.Deputy, &s.Status, &s.BlockedReason, &s.BlockedDetail)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("roomgate: load room: %w", err)
	}
	return &s, nil
}

// Load reads the room without a lock.
func Load(ctx context.Context, q db.DBTX, roomID uuid.UUID) (*State, error) {
	return scan(q.QueryRow(ctx, selectState, roomID))
}

// Lock reads the room under FOR UPDATE. Every writer of blocked_reason takes
// this lock first, and every path that locks the room and a mission locks the
// room FIRST (R1a's order), so a block racing a mission's own pause cannot
// deadlock.
func Lock(ctx context.Context, tx pgx.Tx, roomID uuid.UUID) (*State, error) {
	return scan(tx.QueryRow(ctx, selectState+` FOR UPDATE`, roomID))
}

// Mirrors reports whether a reason parks the room's missions too. `manual`
// does not: it is new in v0.19 and has no old-shape reader to protect, and a
// mission the room merely holds is not a mission anyone paused.
// `runtime_offline` mirrors too (FR-9.2 v0.19, T-S-offline): the old session
// screen has shown `paused(runtime_offline)` since P4, and rebindSession ·
// cancelSession still read it off the mission.
func Mirrors(reason string) bool {
	return reason == ReasonBudget || reason == ReasonLoop || reason == ReasonRuntimeOffline
}

// Block puts the gate up. `workDetail` is the PausedDetail the parked
// missions carry (mirrored reasons only; nil for manual). It returns how many
// missions it stopped — `works_stopped` on the banner: the parked ones for a
// mirrored reason, the active ones the gate now holds for `manual`.
//
// The caller has locked the room (Lock) and owns every other consequence —
// the HITL, the timeline card, the running turns — because those differ per
// reason.
func Block(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, reason string, detail gen.BlockedDetail, workDetail *gen.PausedDetail, now time.Time) (int, error) {
	var stopped int
	if Mirrors(reason) {
		raw, err := markedDetail(workDetail)
		if err != nil {
			return 0, err
		}
		tag, err := tx.Exec(ctx, `
			UPDATE work SET status = 'paused', paused_reason = $2::pause_reason, paused_detail = $3, updated_at = $4
			WHERE room_id = $1 AND status = 'active'`, roomID, reason, raw, now)
		if err != nil {
			return 0, fmt.Errorf("roomgate: park missions: %w", err)
		}
		stopped = int(tag.RowsAffected())
	} else if err := tx.QueryRow(ctx, `SELECT count(*) FROM work WHERE room_id = $1 AND status = 'active'`, roomID).Scan(&stopped); err != nil {
		return 0, err
	}
	r := gen.RoomBlockedReason(reason)
	detail.Reason = &r
	detail.WorksStopped = &stopped
	if detail.BlockedAt == nil {
		at := now.UTC()
		detail.BlockedAt = &at
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE room SET blocked_reason = $2::room_blocked_reason, blocked_detail = $3, updated_at = $4
		WHERE id = $1 AND blocked_reason IS NULL`, roomID, reason, raw, now)
	if err != nil {
		return 0, fmt.Errorf("roomgate: block: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrAlreadyBlocked
	}
	return stopped, nil
}

// OpenWorksRemaining is openapi 0.2.9 BlockedDetail.open_works_remaining_usd
// — the S5/S7 release card's 「승인하면 미션 N개가 한꺼번에 … 잔여 합계 $X」.
// The missions are the ones lifting the gate sets moving again, counted as
// Block counts works_stopped: the parked ones Unblock resumes for a mirrored
// reason, the active ones the gate holds otherwise. A mission's remaining
// budget is its own limits.budget_usd (WorkListItem.budget_usd) minus its
// cost, never below 0; a mission with no budget adds nothing. nil when none
// of them has a budget — there is no sum to show.
func OpenWorksRemaining(ctx context.Context, q db.DBTX, roomID uuid.UUID, reason string) (*float64, error) {
	held, args := `status = 'active'`, []any{roomID}
	if Mirrors(reason) {
		held = `status = 'paused' AND paused_reason::text = $2 AND COALESCE((paused_detail->>'` + MirrorKey + `')::boolean, false)`
		args = append(args, reason)
	}
	var sum *float64
	err := q.QueryRow(ctx, `
		SELECT sum(GREATEST((limits->>'budget_usd')::numeric - cost_usd, 0))::float8
		  FROM work
		 WHERE room_id = $1 AND jsonb_typeof(limits->'budget_usd') = 'number' AND `+held, args...).Scan(&sum)
	if err != nil {
		return nil, fmt.Errorf("roomgate: open works remaining: %w", err)
	}
	return sum, nil
}

// Unblock takes the gate down when it is up for `reason`, and brings back the
// missions the block parked (marked ones only — see the package comment). It
// returns those missions so the caller can re-queue what they parked.
func Unblock(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE room SET blocked_reason = NULL, blocked_detail = NULL, updated_at = $3
		WHERE id = $1 AND blocked_reason = $2::room_blocked_reason`, roomID, reason, now)
	if err != nil {
		return nil, fmt.Errorf("roomgate: unblock: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotBlocked
	}
	if !Mirrors(reason) {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		UPDATE work SET status = 'active', paused_reason = NULL, paused_detail = NULL, updated_at = $3
		WHERE room_id = $1 AND status = 'paused' AND paused_reason = $2::pause_reason
		  AND COALESCE((paused_detail->>'`+MirrorKey+`')::boolean, false)
		RETURNING id`, roomID, reason, now)
	if err != nil {
		return nil, fmt.Errorf("roomgate: resume missions: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// IsMirror reads the mark off a stored work.paused_detail.
func IsMirror(pausedDetail []byte) bool {
	if len(pausedDetail) == 0 {
		return false
	}
	var m map[string]any
	if json.Unmarshal(pausedDetail, &m) != nil {
		return false
	}
	v, _ := m[MirrorKey].(bool)
	return v
}

func markedDetail(d *gen.PausedDetail) ([]byte, error) {
	m := map[string]any{}
	if d != nil {
		raw, err := json.Marshal(d)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
	}
	m[MirrorKey] = true
	return json.Marshal(m)
}

// ---------------------------------------------------------------------------
// Who answers for the room (FR-2A.3 방장 부재 위임, SCR-A G-10)
// ---------------------------------------------------------------------------

// Approvers is the room_owner approver chain: the owner, and — after half of
// a request's deadline — the room's deputy, or, when the room has none, the
// workspace's longest-standing owner. One person's absence must not stop a
// room for good.
type Approvers struct {
	Owner  uuid.UUID
	Deputy *uuid.UUID
	// WsOwner is the oldest workspace owner other than the room owner — the
	// fallback when there is no deputy. nil when the room owner IS the only
	// workspace owner (there is nobody further to hand to).
	WsOwner *uuid.UUID
}

// LoadApprovers reads the chain for a room.
func LoadApprovers(ctx context.Context, q db.DBTX, roomID uuid.UUID) (Approvers, error) {
	var a Approvers
	var ws uuid.UUID
	err := q.QueryRow(ctx, `SELECT owner_user_id, deputy_owner_user_id, workspace_id FROM room WHERE id = $1`, roomID).
		Scan(&a.Owner, &a.Deputy, &ws)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, fmt.Errorf("roomgate: approvers: %w", err)
	}
	var owner uuid.UUID
	err = q.QueryRow(ctx, `
		SELECT user_id FROM member WHERE workspace_id = $1 AND role = 'owner' AND user_id <> $2
		ORDER BY created_at, id LIMIT 1`, ws, a.Owner).Scan(&owner)
	if err == nil {
		a.WsOwner = &owner
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return a, fmt.Errorf("roomgate: oldest owner: %w", err)
	}
	return a, nil
}

// Delegate is who may answer once half the deadline has passed.
func (a Approvers) Delegate() *uuid.UUID {
	d, _ := a.delegate()
	return d
}

// Delegate roles — openapi 0.2.8 BlockedDetail.next_approver_role.
const (
	DelegateDeputy  = "room_deputy"
	DelegateWsOwner = "workspace_owner"
)

// delegate is the one place the chain's second link is chosen: the deputy,
// else the oldest other workspace owner. The role says which of the two it
// was, for the banner's 「부방장 〈서연〉이」 sentence.
func (a Approvers) delegate() (*uuid.UUID, string) {
	if a.Deputy != nil {
		return a.Deputy, DelegateDeputy
	}
	if a.WsOwner != nil {
		return a.WsOwner, DelegateWsOwner
	}
	return nil, ""
}

// AuthzInput fills hitl.Authorize's room fields.
func (a Approvers) AuthzInput(in hitl.AuthzInput) hitl.AuthzInput {
	in.RoomOwner = a.Owner
	if d := a.Delegate(); d != nil {
		in.RoomDelegate = *d
	}
	return in
}

// Turn is the banner's approver chain at one instant (openapi BlockedDetail
// approver · delegate_at · next_approver · next_approver_role).
type Turn struct {
	// Approver is who answers now.
	Approver uuid.UUID
	// DelegateAt is when Next may answer too; nil once that has happened or
	// when there is nobody to hand to.
	DelegateAt *time.Time
	// Next is the person who can answer from DelegateAt, NextRole which link
	// of the chain they are (DelegateDeputy | DelegateWsOwner). Both are set
	// exactly when DelegateAt is.
	Next     *uuid.UUID
	NextRole string
}

// Now is the banner's chain for a request raised at `created` with deadline
// `due`: the owner until half the deadline, the delegate from then on (the
// owner can still answer — the banner names the person who newly can).
// Before half it also names that delegate, so the approver, the instant and
// the next person all come out of one judgement.
func (a Approvers) Now(created, due, now time.Time) Turn {
	half := created.Add(due.Sub(created) / 2)
	d, role := a.delegate()
	if d == nil {
		return Turn{Approver: a.Owner}
	}
	if now.Before(half) {
		return Turn{Approver: a.Owner, DelegateAt: &half, Next: d, NextRole: role}
	}
	return Turn{Approver: *d}
}

// ---------------------------------------------------------------------------
// room.updated
// ---------------------------------------------------------------------------

// PublishUpdated sends the partial Room frame (openapi StreamEvent
// `room.updated`: blocked_reason · blocked_detail · status). The banner
// (S5 · S7) reads it; a gate that goes up without a frame is a room that
// looks alive until the next reload.
func PublishUpdated(ctx context.Context, hub *realtime.Hub, q db.DBTX, roomID uuid.UUID) {
	if hub == nil {
		return
	}
	s, err := Load(ctx, q, roomID)
	if err != nil {
		return
	}
	var detail any
	if len(s.BlockedDetail) > 0 {
		detail = json.RawMessage(s.BlockedDetail)
	}
	id := roomID
	_ = hub.Publish(ctx, q, s.WorkspaceID, &id, "room.updated", map[string]any{
		"id": roomID, "status": s.Status, "blocked_reason": s.BlockedReason, "blocked_detail": detail,
	})
}
