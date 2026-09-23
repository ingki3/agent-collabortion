package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// FR-3.1.1 — which mission a message belongs to (PRD v0.19, V19-A P1-7).
//
// `message.work_id` is decided in this order, and the person sees it before
// sending (the preview chip, FR-3.6):
//
//  1. the mission the person picked in the composer (`work_id`) — `chosen`;
//  2. the mission of the thread the message is in — `thread`;
//  3. the mission of a lane the mentioned agent is RUNNING in this room right
//     now — `running_lane`;
//  4. otherwise no mission — `none`.
//
// An agent's message follows the mission of its own task's lane.
//
// The lanes and tasks the message creates carry the same mission, which is
// what the claim gates on (queue.Claim: a task in a paused mission waits) and
// what the budget counts against (httpapi.loadBudgetState).

// WorkSource values (openapi WorkSource).
const (
	WorkChosen      = "chosen"
	WorkThread      = "thread"
	WorkRunningLane = "running_lane"
	WorkNone        = "none"
)

// Attribution is the answer: the mission (nil = none) and the rule that chose it.
type Attribution struct {
	WorkID *uuid.UUID
	Source string
}

// attribute is FR-3.1.1 for one message. Post and Preview both call it with
// the same premises, so the chip never promises a mission the post does not
// use.
func attribute(ctx context.Context, q db.DBTX, roomID uuid.UUID, in gen.MessageCreate, author Author, th thread, dec Decision) (Attribution, error) {
	if author.Type == "agent" && author.TaskID != nil {
		var w *uuid.UUID
		err := q.QueryRow(ctx, `
			SELECT COALESCE(l.work_id, t.work_id) FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, *author.TaskID).Scan(&w)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Attribution{}, fmt.Errorf("router: agent mission: %w", err)
		}
		if w != nil {
			return Attribution{WorkID: w, Source: WorkRunningLane}, nil
		}
		return Attribution{Source: WorkNone}, nil
	}
	// 1. chosen in the composer.
	if in.WorkId.IsSpecified() && !in.WorkId.IsNull() {
		id := uuid.UUID(in.WorkId.MustGet())
		var status string
		err := q.QueryRow(ctx, `SELECT status::text FROM work WHERE id = $1 AND room_id = $2`, id, roomID).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return Attribution{}, apperr.Validation(apperr.Field("work_id", "not_in_room", "이 방의 미션이 아닙니다"))
		}
		if err != nil {
			return Attribution{}, err
		}
		if status == "completed" || status == "cancelled" {
			return Attribution{}, apperr.Validation(apperr.Field("work_id", "closed", "끝난 미션에는 메시지를 넣을 수 없습니다 — 미션 없이 보내거나 열린 미션을 고르세요"))
		}
		return Attribution{WorkID: &id, Source: WorkChosen}, nil
	}
	// 2. the thread's mission.
	if th.Parent != nil {
		var w *uuid.UUID
		if err := q.QueryRow(ctx, `SELECT work_id FROM message WHERE id = $1`, *th.Parent).Scan(&w); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Attribution{}, err
		}
		if w != nil {
			return Attribution{WorkID: w, Source: WorkThread}, nil
		}
	}
	// 3. a mentioned agent's running lane, in mention order.
	for _, tr := range dec.Triggers {
		if tr.Rule != 2 {
			continue
		}
		var w *uuid.UUID
		err := q.QueryRow(ctx, `
			SELECT work_id FROM lane
			 WHERE session_id = $1 AND agent_id = $2 AND status = 'running' AND work_id IS NOT NULL
			 ORDER BY updated_at DESC, id LIMIT 1`, roomID, tr.AgentID).Scan(&w)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Attribution{}, err
		}
		if w != nil {
			return Attribution{WorkID: w, Source: WorkRunningLane}, nil
		}
	}
	// Temporary: the legacy single-work room rule (T-R1b1 Q1, Lead).
	if w, err := legacySingleWorkRoom(ctx, q, roomID); err != nil {
		return Attribution{}, err
	} else if w != nil {
		return Attribution{WorkID: w, Source: WorkChosen}, nil
	}
	// 4. no mission.
	return Attribution{Source: WorkNone}, nil
}

// legacySingleWorkRoom is a TEMPORARY compatibility rule between rules 3 and 4
// (T-R1b1 Q1, Lead-approved; plan/V19_R1B_HANDOFF.md).
//
// The old `/sessions/*` clients post without `work_id` — for them the session
// IS its one mission. Rule 4 would file almost every such message under "no
// mission", and a task outside any mission answers only to the room gate: a
// session the Director paused, or one that completed, would start dispatching
// again on the next message, and the mission's brief and cost would lose the
// run. While a room has exactly one mission, that mission is the answer —
// reported as `chosen` (the closed WorkSource enum has no "legacy" value).
//
// The rule counts the room's missions WHATEVER their status: a completed old
// session is still "the" mission of its room, and its new messages must keep
// waiting behind it as they did (claim: task's mission must be active).
//
// During R1b1 `work_room_single` holds, so every room qualifies. R1b2, which
// lets a room open a second mission, must narrow the premise to rooms made by
// the old path (createSession · 0025 migration) — from then on a new room's
// top-level chat is honestly "no mission".
func legacySingleWorkRoom(ctx context.Context, q db.DBTX, roomID uuid.UUID) (*uuid.UUID, error) {
	rows, err := q.Query(ctx, `SELECT id FROM work WHERE room_id = $1 LIMIT 2`, roomID)
	if err != nil {
		return nil, fmt.Errorf("router: legacy single-work room: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, nil
	}
	return &ids[0], nil
}

// bindLaneWork makes `laneID` carry mission `work` unless it already carries
// one, and returns the lane's mission — the mission a task on that lane runs
// for. A lane belongs to at most one mission ("그 lane 이 매인 일"): a message
// that reaches an already-bound lane (thread reply, rule-3 reuse) runs there
// for THAT mission, and an unbound lane is bound by the first mission message
// that reaches it.
func bindLaneWork(ctx context.Context, tx pgx.Tx, laneID uuid.UUID, work *uuid.UUID) (*uuid.UUID, error) {
	var out *uuid.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE lane SET work_id = COALESCE(work_id, $2) WHERE id = $1 RETURNING work_id`, laneID, work).Scan(&out); err != nil {
		return nil, fmt.Errorf("router: lane mission: %w", err)
	}
	return out, nil
}

// WorkRef is the preview's `work` ({id, title}) for an attribution.
func workRef(ctx context.Context, q db.DBTX, a Attribution) (*struct {
	Id    uuid.UUID `json:"id"`
	Title string    `json:"title"`
}, error) {
	if a.WorkID == nil {
		return nil, nil
	}
	out := &struct {
		Id    uuid.UUID `json:"id"`
		Title string    `json:"title"`
	}{Id: *a.WorkID}
	if err := q.QueryRow(ctx, `SELECT title FROM work WHERE id = $1`, *a.WorkID).Scan(&out.Title); err != nil {
		return nil, err
	}
	return out, nil
}

// routingAssignee is rule 6's assignee (FR-3.3 "그 외 사용자 메시지 → 세션
// assignee"). The assignee is a mission's (FR-2A.1), so it is the chosen
// mission's, else the room's only mission's (the legacy single-work room
// rule above); nil when there is neither — a room's plain chat has nobody to
// route to implicitly.
func routingAssignee(ctx context.Context, q db.DBTX, roomID uuid.UUID, in gen.MessageCreate) (*uuid.UUID, error) {
	var work *uuid.UUID
	if in.WorkId.IsSpecified() && !in.WorkId.IsNull() {
		id := uuid.UUID(in.WorkId.MustGet())
		work = &id
	} else {
		w, err := legacySingleWorkRoom(ctx, q, roomID)
		if err != nil {
			return nil, err
		}
		work = w
	}
	if work == nil {
		return nil, nil
	}
	var assignee *uuid.UUID
	err := q.QueryRow(ctx, `SELECT assignee_agent_id FROM work WHERE id = $1 AND room_id = $2`, *work, roomID).Scan(&assignee)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // attribute() answers the foreign work_id with its 422
	}
	return assignee, err
}
