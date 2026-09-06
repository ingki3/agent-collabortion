package workdirs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

const workdirCols = `w.id, w.session_id, w.agent_id, w.lane_id, w.kind::text, w.path_or_ref,
	 w.branch, w.status::text, w.disk_bytes, w.last_used_at, w.retain_until, w.dirty,
	 w.created_at, w.updated_at, s.title, s.status::text`

func scanWorkdir(row pgx.Row) (gen.Workdir, uuid.UUID, error) {
	var wd gen.Workdir
	var agentID, laneID *uuid.UUID
	var kind, status, sessionStatus, sessionTitle string
	var branch *string
	var lastUsed, retain *time.Time
	var dirty *bool
	var sessionID uuid.UUID
	if err := row.Scan(&wd.Id, &sessionID, &agentID, &laneID, &kind, &wd.PathOrRef,
		&branch, &status, &wd.DiskBytes, &lastUsed, &retain, &dirty,
		&wd.CreatedAt, &wd.UpdatedAt, &sessionTitle, &sessionStatus); err != nil {
		return wd, uuid.Nil, err
	}
	wd.SessionId = openapi_types.UUID(sessionID)
	wd.Kind = gen.WorkdirKind(kind)
	wd.Status = gen.WorkdirStatus(status)
	wd.AgentId = nullableUUID(agentID)
	wd.LaneId = nullableUUID(laneID)
	wd.Branch = nullableString(branch)
	wd.LastUsedAt = nullableTime(lastUsed)
	wd.RetainUntil = nullableTime(retain)
	if dirty != nil {
		wd.Dirty = nullable.NewNullableWithValue(*dirty)
	} else {
		wd.Dirty = nullable.NewNullNullable[bool]()
	}
	wd.Session = &gen.SessionRef{
		Id: openapi_types.UUID(sessionID), Title: sessionTitle, Status: gen.SessionStatus(sessionStatus),
	}
	return wd, sessionID, nil
}

// Load returns one workdir as the contract's Workdir (SSE `workdir.updated`).
func Load(ctx context.Context, q db.DBTX, id uuid.UUID) (gen.Workdir, error) {
	wd, _, err := scanWorkdir(q.QueryRow(ctx, `
		SELECT `+workdirCols+` FROM workdir w JOIN session s ON s.id = w.session_id WHERE w.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return wd, apperr.NotFound("workdir")
	}
	if err != nil {
		return wd, fmt.Errorf("workdirs: load: %w", err)
	}
	return wd, nil
}

// ListQuery is listRuntimeWorkdirs' filter (S13).
type ListQuery struct {
	RuntimeID uuid.UUID
	Status    *string
	SessionID *uuid.UUID
	Limit     int
}

// ListForRuntime answers listRuntimeWorkdirs: every workdir of every session
// pinned to this machine, with the disk total S13 draws the usage bar from.
//
// A `deleted` row is kept out unless the caller asks for it by status: S13's
// point is what is ON the machine, and a list dominated by tombstones makes
// the one directory that still needs a decision hard to find.
func ListForRuntime(ctx context.Context, q db.DBTX, ql ListQuery) ([]gen.Workdir, int64, error) {
	limit := ql.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.Query(ctx, `
		SELECT `+workdirCols+`
		FROM workdir w JOIN session s ON s.id = w.session_id
		WHERE s.runtime_id = $1
		  AND ($2::text IS NULL OR w.status::text = $2)
		  AND ($2::text IS NOT NULL OR w.status <> 'deleted')
		  AND ($3::uuid IS NULL OR w.session_id = $3)
		ORDER BY w.last_used_at DESC NULLS LAST, w.created_at DESC
		LIMIT $4`, ql.RuntimeID, ql.Status, ql.SessionID, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("workdirs: list: %w", err)
	}
	defer rows.Close()
	out := []gen.Workdir{}
	for rows.Next() {
		wd, _, err := scanWorkdir(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, wd)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := q.QueryRow(ctx, `
		SELECT COALESCE(sum(w.disk_bytes), 0) FROM workdir w JOIN session s ON s.id = w.session_id
		WHERE s.runtime_id = $1 AND w.status <> 'deleted'`, ql.RuntimeID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// RuntimeDiskUsed is E13-16's numerator: how much the workspace's machines are
// holding right now.
func RuntimeDiskUsed(ctx context.Context, q db.DBTX, wsID uuid.UUID) (int64, error) {
	var used int64
	err := q.QueryRow(ctx, `
		SELECT COALESCE(sum(w.disk_bytes), 0)
		FROM workdir w JOIN session s ON s.id = w.session_id
		WHERE s.workspace_id = $1 AND w.status <> 'deleted'`, wsID).Scan(&used)
	return used, err
}

// BlockReason reads the GC block a sweep recorded, for `deleteWorkdir`'s 409.
func BlockReason(ctx context.Context, q db.DBTX, id uuid.UUID) (reason string, merged bool, ahead int, treeDirty bool, err error) {
	var m *bool
	var d *bool
	var r *string
	err = q.QueryRow(ctx, `
		SELECT gc_blocked_reason, merged, commits_ahead, COALESCE(tree_dirty, dirty, false)
		FROM workdir WHERE id = $1`, id).Scan(&r, &m, &ahead, &d)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, 0, false, apperr.NotFound("workdir")
	}
	if err != nil {
		return "", false, 0, false, err
	}
	if r != nil {
		reason = *r
	}
	merged = m != nil && *m
	treeDirty = d != nil && *d
	return reason, merged, ahead, treeDirty, nil
}

func nullableUUID(u *uuid.UUID) nullable.Nullable[openapi_types.UUID] {
	if u == nil {
		return nullable.NewNullNullable[openapi_types.UUID]()
	}
	return nullable.NewNullableWithValue(openapi_types.UUID(*u))
}

func nullableString(s *string) nullable.Nullable[string] {
	if s == nil {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(*s)
}

func nullableTime(t *time.Time) nullable.Nullable[time.Time] {
	if t == nil {
		return nullable.NewNullNullable[time.Time]()
	}
	return nullable.NewNullableWithValue(*t)
}
