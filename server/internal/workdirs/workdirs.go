// Package workdirs records what the daemon reports about work directories
// (daemon-protocol §6) as rows, and binds lanes to them (FR-6.1, FR-6.4).
//
// Why this exists: the daemon creates exactly one workdir per lane on disk and
// says so, but the server kept no row, so `lane.workdir_id` was always null —
// S7 could not tell a lane's branch and S13 had nothing to show (G4 결함 5).
package workdirs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Report is one entry of the §6 report, or the workdir a `phase` report names.
type Report struct {
	Kind       string // workdir_kind: worktree | container | dir
	Path       string
	SessionID  uuid.UUID
	AgentID    *uuid.UUID
	LaneID     *uuid.UUID
	Bytes      int64
	LastUsedAt *time.Time
	Branch     *string
	Dirty      *bool
}

// Kind normalises what the daemon calls the workdir onto the workdir_kind
// enum. TaskBundle only ever says "worktree" or "dir" (§4.1); anything else is
// a directory as far as the row is concerned — an unknown string must not
// make the whole report fail with a 500.
func Kind(s string) string {
	switch s {
	case "worktree", "container", "dir":
		return s
	default:
		return "dir"
	}
}

// Record upserts the row for (session, path) and binds the lane(s) that run in
// it. It is idempotent: the daemon re-reports the same directories on every
// probe, and §6 carries no row id for the server to match on.
//
// Binding follows FR-6.1 / C3. `container`·`none` report a lane_id — one
// workdir per lane, so that lane points at it. `worktree` reports an agent_id
// — one workdir shared by that agent's lanes in the session, so every one of
// them that has no workdir yet points at it.
func Record(ctx context.Context, q db.DBTX, rep Report, now time.Time) (uuid.UUID, error) {
	if rep.Path == "" || rep.SessionID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("workdirs: report needs session_id and path")
	}
	if rep.AgentID == nil && rep.LaneID == nil {
		// workdir CHECK (agent_id IS NOT NULL OR lane_id IS NOT NULL): a row
		// that belongs to neither an agent nor a lane cannot be GC'd or shown.
		return uuid.Nil, fmt.Errorf("workdirs: report needs agent_id or lane_id")
	}
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		INSERT INTO workdir (session_id, agent_id, lane_id, kind, path_or_ref, branch, status, disk_bytes, last_used_at, dirty, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $8, $9, $10, $10)
		ON CONFLICT (session_id, path_or_ref) DO UPDATE SET
			agent_id     = COALESCE(EXCLUDED.agent_id, workdir.agent_id),
			lane_id      = COALESCE(EXCLUDED.lane_id, workdir.lane_id),
			kind         = EXCLUDED.kind,
			branch       = COALESCE(EXCLUDED.branch, workdir.branch),
			disk_bytes   = GREATEST(EXCLUDED.disk_bytes, 0),
			last_used_at = COALESCE(EXCLUDED.last_used_at, workdir.last_used_at),
			dirty        = COALESCE(EXCLUDED.dirty, workdir.dirty),
			updated_at   = EXCLUDED.updated_at
		RETURNING id`,
		rep.SessionID, rep.AgentID, rep.LaneID, Kind(rep.Kind), rep.Path, rep.Branch,
		max64(rep.Bytes, 0), rep.LastUsedAt, rep.Dirty, now).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("workdirs: upsert: %w", err)
	}
	switch {
	case rep.LaneID != nil:
		if _, err := q.Exec(ctx, `
			UPDATE lane SET workdir_id = $1, updated_at = $2
			WHERE id = $3 AND session_id = $4 AND workdir_id IS DISTINCT FROM $1`,
			id, now, *rep.LaneID, rep.SessionID); err != nil {
			return id, fmt.Errorf("workdirs: bind lane: %w", err)
		}
	case rep.AgentID != nil:
		if _, err := q.Exec(ctx, `
			UPDATE lane SET workdir_id = $1, updated_at = $2
			WHERE session_id = $3 AND agent_id = $4 AND workdir_id IS NULL`,
			id, now, rep.SessionID, *rep.AgentID); err != nil {
			return id, fmt.Errorf("workdirs: bind agent lanes: %w", err)
		}
	}
	return id, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// MarkGCd closes the GC loop of daemon-protocol §6. The server issues
// `gc {workdir_ids}`; the daemon deletes and reports; only then is the row
// `deleted`. Nothing did that last step, so a directory the daemon had already
// removed stayed `active` on S13 forever — and E6-03's "0개 남음" could never
// become true no matter how well the daemon obeyed.
//
// Only rows the server actually asked for are touched: a directory that simply
// stopped being reported (a machine unplugged, a partial report) is not
// evidence of a deletion.
func MarkGCd(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, presentWorkdirIDs []string, now time.Time) error {
	if presentWorkdirIDs == nil {
		presentWorkdirIDs = []string{}
	}
	_, err := q.Exec(ctx, `
		UPDATE workdir SET status = 'deleted', updated_at = $3
		WHERE status <> 'deleted'
		  AND NOT (id::text = ANY($2))
		  AND EXISTS (
		        SELECT 1 FROM daemon_command c
		        WHERE c.runtime_id = $1 AND c.type = 'gc'
		          AND jsonb_typeof(c.payload->'workdir_ids') = 'array'
		          AND jsonb_exists(c.payload->'workdir_ids', workdir.id::text))`,
		runtimeID, presentWorkdirIDs, now)
	if err != nil {
		return fmt.Errorf("workdirs: mark gc'd: %w", err)
	}
	return nil
}
