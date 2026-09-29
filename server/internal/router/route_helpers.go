package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/lanestate"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// T-RF1: names for the shapes postRow · Preview · Delegate · wake used to
// spell out inline.

// postTrigger is one MessagePostResult.triggers[] entry. An ALIAS of the
// generated anonymous struct (not a new type), so it appends to
// gen.MessagePostResult.Triggers as is and the wire shape cannot drift.
type postTrigger = struct {
	AgentId       openapi_types.UUID           `json:"agent_id"`
	Coalesced     bool                         `json:"coalesced"`
	DeferredUntil nullable.Nullable[time.Time] `json:"deferred_until,omitempty"`
	LaneId        openapi_types.UUID           `json:"lane_id"`
	TaskId        openapi_types.UUID           `json:"task_id"`
}

// routeWarning is one warnings[] entry of MessagePostResult and of
// TriggerPreview — the generated code has the same anonymous struct in both.
type routeWarning = struct {
	AgentId nullable.Nullable[openapi_types.UUID] `json:"agent_id,omitempty"`
	Code    string                                `json:"code"`
	Message string                                `json:"message"`
}

func warningOf(agent *uuid.UUID, code, message string) routeWarning {
	return routeWarning{AgentId: tasks.NullUUID(agent), Code: code, Message: message}
}

// heldAgent is a trigger T-QUIET held (or cancelled because the mission
// closed): the post's result names it in an approval_pending warning.
type heldAgent struct {
	id   uuid.UUID
	name string
}

// taskOriginator reads a task's person originator (PRD FR-4.5). found is
// false when the task does not exist (or the read failed — both callers
// always ignored that error, and still do: a post or a delegation is not
// refused over the originator).
func taskOriginator(ctx context.Context, tx pgx.Tx, taskID uuid.UUID) (originator *uuid.UUID, found bool) {
	if err := tx.QueryRow(ctx, `SELECT originator_user_id FROM task WHERE id = $1`, taskID).Scan(&originator); err != nil {
		return nil, false
	}
	return originator, true
}

// queuedTask is the lane's oldest queued task, locked (FOR UPDATE) — the task
// a new trigger on the lane merges into (FR-3.4). ok is false when the lane
// has none.
type queuedTask struct {
	ID        uuid.UUID
	Coalesced []uuid.UUID
}

func lockQueuedTask(ctx context.Context, tx pgx.Tx, laneID uuid.UUID) (queuedTask, bool, error) {
	var q queuedTask
	err := tx.QueryRow(ctx, `
		SELECT id, coalesced_message_ids FROM task
		WHERE lane_id = $1 AND status = 'queued' ORDER BY created_at LIMIT 1 FOR UPDATE`, laneID).
		Scan(&q.ID, &q.Coalesced)
	if errors.Is(err, pgx.ErrNoRows) {
		return queuedTask{}, false, nil
	}
	if err != nil {
		return queuedTask{}, false, err
	}
	return q, true, nil
}

// newQueuedTask is a `queued` task row a trigger makes when its lane has none
// to merge into (Post, wake) or a delegation's first task (Delegate).
type newQueuedTask struct {
	LaneID, SessionID, AgentID, ProfileID, TriggerMessageID uuid.UUID
	DelegatedFrom                                           *uuid.UUID
	Originator                                              *uuid.UUID
	Coalesced                                               []uuid.UUID // nil → '{}'
	Work                                                    *uuid.UUID
	Now                                                     time.Time
}

func insertQueuedTask(ctx context.Context, tx pgx.Tx, n newQueuedTask) (uuid.UUID, error) {
	coalesced := n.Coalesced
	if coalesced == nil {
		coalesced = []uuid.UUID{} // the column is NOT NULL DEFAULT '{}'
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, delegated_from_task_id,
		                  originator_user_id, coalesced_message_ids, status, created_at, updated_at, work_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'queued', $9, $9, $10) RETURNING id`,
		n.LaneID, n.SessionID, n.AgentID, n.ProfileID, n.TriggerMessageID, n.DelegatedFrom,
		n.Originator, coalesced, n.Now, n.Work).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("insert task: %w", err)
	}
	return id, nil
}

// laneCandidates loads an agent's existing lanes in a room — the candidates
// lanestate.Resolve reads (FR-3.3 lane rules). Post and Preview both call it,
// so the two cannot read different rows by accident.
//
// mission != nil applies T-R1b2's filter: only lanes of the trigger's mission
// or bound to none, plus the pinned lane. Post always passes it.
//
// TODO(T-RF1-P): Preview passes nil and so still reads every lane of the
// agent — in a room with several missions it can promise a reuse Post will
// not make. Kept as it was (Lead's call); pinned by TestPreviewLaneParity.
func laneCandidates(ctx context.Context, tx pgx.Tx, sessionID, agentID uuid.UUID, mission *laneOpts) ([]lanestate.Candidate, error) {
	var rows pgx.Rows
	var err error
	if mission == nil {
		rows, err = tx.Query(ctx, `
			SELECT id, agent_id, status::text, reentry_count, GREATEST(created_at, updated_at)
			FROM lane WHERE session_id = $1 AND agent_id = $2 ORDER BY created_at`, sessionID, agentID)
	} else {
		// PRD v0.19: a lane belongs to at most one mission ("그 lane 이 매인
		// 일"), so the lanes a trigger may land on are its own mission's and
		// the unbound ones (resolveLaneFor).
		rows, err = tx.Query(ctx, `
			SELECT id, agent_id, status::text, reentry_count, GREATEST(created_at, updated_at)
			FROM lane WHERE session_id = $1 AND agent_id = $2
			  AND (work_id IS NULL OR work_id IS NOT DISTINCT FROM $3::uuid OR ($4 AND id = $5))
			ORDER BY created_at`, sessionID, agentID, mission.work, mission.pinned, mission.threadRootLane)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []lanestate.Candidate
	for rows.Next() {
		var c lanestate.Candidate
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Status, &c.ReentryCount, &c.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
