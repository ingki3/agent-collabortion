// S-65: the §4.3 gc command carries only absolute paths — from all three
// paths that build one (session completion, the sweep, the manual delete).
// A workdir row from before migration 0019 can hold a relative path; the
// daemon absolutises against its own CWD, and for a gc that is an `rm -rf`
// inside the user's repository. S-62 closed the turn bundle; this is the gc.
package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

func TestS65GCNeverCarriesARelativePath(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE room SET runtime_id = (SELECT id FROM runtime LIMIT 1) WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	var laneID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at, work_id)
		SELECT $1, $2, p.profile_id, 'done', now(), now(), r.legacy_work_id FROM room_participant p JOIN room r ON r.id = p.room_id
		WHERE p.room_id = $1 AND p.agent_id = $2 RETURNING id`, f.sessionID, f.r).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	var absID, relID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO workdir (session_id, lane_id, kind, path_or_ref, status, created_at, updated_at)
		VALUES ($1, $2, 'dir', '/tmp/colab/wd-abs', 'active', now(), now()) RETURNING id`, f.sessionID, laneID).Scan(&absID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO workdir (session_id, agent_id, kind, path_or_ref, status, created_at, updated_at)
		VALUES ($1, $2, 'worktree', 'worktrees/s/lead', 'active', now(), now()) RETURNING id`, f.sessionID, f.lead).Scan(&relID); err != nil {
		t.Fatal(err)
	}

	// Manual delete of the relative row: refused, not forwarded.
	if st, out, _ := f.api.do("DELETE", f.p+"/workdirs/"+relID.String(), nil); st != 409 || str(out, "code") != "workdir_relative_path" {
		t.Fatalf("DELETE relative workdir = %d %v, want 409 workdir_relative_path", st, out)
	}
	// Completion issues one gc for the session: the absolute row only.
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/complete", map[string]any{"confirm": true})
	var gcs int
	var ids, targets string
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(max(payload->>'workdir_ids'), ''), coalesce(max(payload->>'workdirs'), '')
		FROM daemon_command WHERE session_id = $1 AND type = 'gc'`, f.sessionID).Scan(&gcs, &ids, &targets); err != nil {
		t.Fatal(err)
	}
	if gcs != 1 {
		t.Fatalf("gc commands = %d, want 1", gcs)
	}
	if !contains(targets, absID.String()) || !contains(targets, "/tmp/colab/wd-abs") {
		t.Errorf("gc payload workdirs = %s, want the absolute row", targets)
	}
	if contains(targets, relID.String()) || contains(targets, "worktrees/s/lead") || contains(ids, relID.String()) {
		t.Errorf("gc payload carries the relative row: ids=%s workdirs=%s — the daemon would rm -rf against its CWD", ids, targets)
	}
	// The sweep (retention window long past) builds the same command shape and
	// leaves the relative row out too.
	if _, err := f.pool.Exec(ctx, `UPDATE work SET finished_at = now() - interval '30 days' WHERE room_id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Workdirs.SweepGC(ctx); err != nil {
		t.Fatal(err)
	}
	var relCmds int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_command WHERE type = 'gc' AND payload::text LIKE '%' || $1 || '%'`, relID.String()).Scan(&relCmds); err != nil {
		t.Fatal(err)
	}
	if relCmds != 0 {
		t.Errorf("the sweep queued %d gc command(s) naming the relative row", relCmds)
	}
}
