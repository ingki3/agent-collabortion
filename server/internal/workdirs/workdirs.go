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
	// Dirty is the contract's `Workdir.dirty` — "미병합 커밋 또는 미커밋 변경".
	// It stays the OR because that is what openapi says it means and what S13
	// already draws.
	Dirty *bool
	// Merged · CommitsAhead · TreeDirty are §6's `git` fields kept APART.
	// FR-6.4's two green lights need the three values separately, and E13-12
	// (미병합 → 병합해 달라) and E13-13 (미커밋 → 커밋하거나 버려 달라) ask the
	// Director for different things — collapsing them into `dirty` made both
	// unanswerable (T-S9).
	Merged       *bool
	CommitsAhead *int
	TreeDirty    *bool
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
		INSERT INTO workdir (session_id, agent_id, lane_id, kind, path_or_ref, branch, status, disk_bytes, last_used_at, dirty, merged, commits_ahead, tree_dirty, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $8, $9, $11, COALESCE($12, 0), $13, $10, $10)
		ON CONFLICT (session_id, path_or_ref) DO UPDATE SET
			agent_id      = COALESCE(EXCLUDED.agent_id, workdir.agent_id),
			lane_id       = COALESCE(EXCLUDED.lane_id, workdir.lane_id),
			kind          = EXCLUDED.kind,
			branch        = COALESCE(EXCLUDED.branch, workdir.branch),
			-- 0 bytes is "not measured", not "an empty checkout": the §4.4 finish
			-- report carries git facts but no size, and overwriting a measured
			-- value with 0 would zero the quota numerator (E13-16) every time a
			-- turn ended. A real directory is never 0 bytes.
			disk_bytes    = CASE WHEN EXCLUDED.disk_bytes > 0 THEN EXCLUDED.disk_bytes ELSE workdir.disk_bytes END,
			last_used_at  = COALESCE(EXCLUDED.last_used_at, workdir.last_used_at),
			dirty         = COALESCE(EXCLUDED.dirty, workdir.dirty),
			merged        = COALESCE($11, workdir.merged),
			commits_ahead = COALESCE($12, workdir.commits_ahead),
			tree_dirty    = COALESCE($13, workdir.tree_dirty),
			-- U2's other half: a live report for a path stamped runtime_gone
			-- means the directory is on the machine reporting it NOW, so the row
			-- comes back to life. Only that reason is revived — a GC-refused
			-- row is parked on purpose and reviving it would re-issue the gc
			-- command on every probe.
			status        = CASE WHEN workdir.gc_blocked_reason = 'runtime_gone'
			                     THEN 'active'::workdir_status ELSE workdir.status END,
			gc_blocked_reason = CASE WHEN workdir.gc_blocked_reason = 'runtime_gone'
			                         THEN NULL ELSE workdir.gc_blocked_reason END,
			updated_at    = EXCLUDED.updated_at
		RETURNING id`,
		rep.SessionID, rep.AgentID, rep.LaneID, Kind(rep.Kind), rep.Path, rep.Branch,
		max64(rep.Bytes, 0), rep.LastUsedAt, rep.Dirty, now,
		rep.Merged, rep.CommitsAhead, rep.TreeDirty).Scan(&id)
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

// GCReport is one workdir row's `gc` field in the §6 report (v0.7): the
// daemon says what it did with a directory the server asked it to collect.
type GCReport struct {
	// WorkdirID is the id the server put in the command; the daemon echoes it.
	WorkdirID uuid.UUID
	Status    string // deleted | refused
	Reason    string
}

// GCStatuses of §6.
const (
	GCDeleted = "deleted"
	GCRefused = "refused"
)

// Refusal is a `gc: {status: refused}` row the caller must put on the feed.
type Refusal struct {
	WorkdirID uuid.UUID
	Reason    string
}

// ApplyGCReports closes the GC loop of daemon-protocol §6 v0.7. The server
// issues `gc {session_id, workdirs}`; the daemon deletes or refuses and says
// which in the NEXT report; only then does the row move.
//
// It replaced an inference — "a workdir the server asked for and the daemon no
// longer lists is gone". That was wrong in both directions (review NN6): a
// daemon restarting mid-report closed directories that were still on disk, and
// a `refused` row is reported forever, so its command was never consumed and
// the refusal never reached a person. §6 is explicit that there is no silent
// path; the status field is the receipt.
//
// `refused` parks the row at `retained` rather than leaving it `active`:
// the daemon will not delete it (worktree before P4, say), so it is kept on
// purpose, and the command has nothing left to wait for.
//
// production caller: httpapi.Server.daemonWorkdirs (§6 report handler).
func ApplyGCReports(ctx context.Context, q db.DBTX, runtimeID uuid.UUID, reports []GCReport, now time.Time) ([]Refusal, error) {
	var refusals []Refusal
	for _, rep := range reports {
		if rep.WorkdirID == uuid.Nil {
			continue
		}
		status := ""
		switch rep.Status {
		case GCDeleted:
			status = "deleted"
		case GCRefused:
			status = "retained"
		default:
			// An unknown status is not a receipt. Leaving the row alone keeps
			// the command unconsumed, so the 24h TTL records it on the feed
			// rather than the server inventing an outcome.
			continue
		}
		// Only a workdir the server actually asked THIS runtime to collect
		// moves: the report is input, not authority.
		tag, err := q.Exec(ctx, `
			UPDATE workdir SET status = $3::workdir_status, updated_at = $4
			WHERE id = $1 AND status <> 'deleted'
			  AND EXISTS (SELECT 1 FROM daemon_command c
			              WHERE c.runtime_id = $2 AND c.type = 'gc'
			                AND workdir.id::text = ANY(gc_command_workdir_ids(c.payload)))`,
			rep.WorkdirID, runtimeID, status, now)
		if err != nil {
			return nil, fmt.Errorf("workdirs: apply gc report: %w", err)
		}
		if rep.Status == GCRefused && tag.RowsAffected() > 0 {
			refusals = append(refusals, Refusal{WorkdirID: rep.WorkdirID, Reason: rep.Reason})
		}
	}
	return refusals, nil
}
