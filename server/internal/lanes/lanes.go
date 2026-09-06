// Package lanes loads a lane row into the contract Lane (S7 card). P1 needs it
// for cancelLane's response and the `lane.updated` stream event; failure_kind
// is derived from the lane's current task (the lane table has no column —
// SCREEN §4.5 classifies a failed lane by its task's failure_kind).
package lanes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

var ErrNotFound = errors.New("lanes: lane not found")

// Load returns the lane with its current (latest) task. canControl is whether
// the viewer is the session's Director or deputy: only they get the cancel
// action (openapi Lane.actions). restart stays out (P2, 501).
func Load(ctx context.Context, q db.DBTX, id uuid.UUID, canControl bool) (*gen.Lane, error) {
	var (
		out                                        gen.Lane
		parent, workdir, delegatedFrom, blockedMsg *uuid.UUID
		blockedNote, agentName                     *string
		status                                     string
		hasRef                                     bool
		finishedAt                                 *time.Time
	)
	err := q.QueryRow(ctx, `
		SELECT l.id, l.session_id, l.parent_lane_id, l.agent_id, l.profile_id, l.depends_on, l.workdir_id, l.delegated_from_task_id,
		       l.runtime_session_ref IS NOT NULL, l.status::text, l.blocked_note, l.blocked_message_id, l.reentry_count,
		       l.created_at, l.updated_at, l.finished_at, a.name
		FROM lane l JOIN agent a ON a.id = l.agent_id WHERE l.id = $1`, id).
		Scan(&out.Id, &out.SessionId, &parent, &out.AgentId, &out.ProfileId, &out.DependsOn, &workdir, &delegatedFrom,
			&hasRef, &status, &blockedNote, &blockedMsg, &out.ReentryCount, &out.CreatedAt, &out.UpdatedAt, &finishedAt, &agentName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lanes: load: %w", err)
	}
	out.Status = gen.LaneStatus(status)
	out.ParentLaneId = tasks.NullUUID(parent)
	out.WorkdirId = tasks.NullUUID(workdir)
	out.DelegatedFromTaskId = tasks.NullUUID(delegatedFrom)
	out.BlockedMessageId = tasks.NullUUID(blockedMsg)
	out.BlockedNote = tasks.NullString(blockedNote)
	out.FinishedAt = tasks.NullTime(finishedAt)
	out.HasRuntimeSession = &hasRef
	out.AgentName = agentName
	if out.DependsOn == nil {
		out.DependsOn = []openapi_types.UUID{}
	}
	out.FailureKind = nullable.NewNullNullable[gen.FailureKind]()
	out.Actions = []gen.LaneActions{}

	var taskID uuid.UUID
	err = q.QueryRow(ctx, `SELECT id FROM task WHERE lane_id = $1 ORDER BY created_at DESC LIMIT 1`, id).Scan(&taskID)
	if err == nil {
		t, err := tasks.Get(ctx, q, taskID)
		if err != nil {
			return nil, err
		}
		cur := tasks.ToAPI(t, nil, nil)
		out.CurrentTask = &cur
		if out.Status == gen.LaneStatusFailed && t.FailureKind != nil {
			out.FailureKind = nullable.NewNullableWithValue(gen.FailureKind(*t.FailureKind))
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("lanes: current task: %w", err)
	}
	if canControl && (out.Status == gen.LaneStatusRunning || out.Status == gen.LaneStatusQueued) {
		out.Actions = append(out.Actions, gen.LaneActionsCancel)
	}
	return &out, nil
}

// List returns the session's lanes for the S7 board (openapi listLanes). The
// response is a **bare array** — the contract says `type: array`, like
// listArtifacts. statuses filters by lane_status; empty means all seven.
func List(ctx context.Context, q db.DBTX, sessionID uuid.UUID, statuses []string, canControl bool) ([]gen.Lane, error) {
	rows, err := q.Query(ctx, `
		SELECT id FROM lane
		WHERE session_id = $1 AND (cardinality($2::text[]) = 0 OR status::text = ANY($2))
		ORDER BY created_at`, sessionID, statuses)
	if err != nil {
		return nil, fmt.Errorf("lanes: list: %w", err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]gen.Lane, 0, len(ids))
	for _, id := range ids {
		l, err := Load(ctx, q, id, canControl)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, nil
}
