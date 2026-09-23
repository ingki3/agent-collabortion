package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// ---------------------------------------------------------------------------
// K-14 — the bundle carries `workdir.id`, the §6 report echoes it
// (daemon-protocol v0.8.3 §4.1 · §6, T-S21)
// ---------------------------------------------------------------------------

// TestV11BundleCarriesWorkdirID: the row exists BEFORE the bundle leaves, the
// id in the bundle is that row, and the same agent's next bundle in the same
// session carries the SAME id (C3: one worktree per agent, reused).
func TestV11BundleCarriesWorkdirID(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)

	wdID, err := uuid.Parse(b.Workdir.ID)
	if err != nil || wdID == uuid.Nil {
		t.Fatalf("workdir.id = %q, want the workdir row's uuid (daemon-protocol v0.8.3 §4.1, K-14)", b.Workdir.ID)
	}
	var path, kind string
	var agentID *uuid.UUID
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT path_or_ref, kind::text, agent_id FROM workdir WHERE id = $1`, wdID).
		Scan(&path, &kind, &agentID); err != nil {
		t.Fatalf("the bundle names id %s but no such row exists — the server makes the row first, then "+
			"ships the id: %v", wdID, err)
	}
	if path != b.Workdir.Path || kind != "worktree" || agentID == nil || *agentID != f.leadUUID {
		t.Errorf("row (%s, %s, agent %v) ≠ bundle (%s, worktree, agent %s)", path, kind, agentID, b.Workdir.Path, f.leadUUID)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d workdir rows after one bundle, want 1", rows)
	}
	// The lane is bound at bundle time, not a probe later (FR-6.1).
	var laneWD *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workdir_id FROM lane WHERE id = $1`, mustUUID(t, b.Task.LaneID)).Scan(&laneWD); err != nil {
		t.Fatal(err)
	}
	if laneWD == nil || *laneWD != wdID {
		t.Errorf("lane.workdir_id = %v, want %s", laneWD, wdID)
	}

	// Second bundle of the same agent: the lane finishes, the Lead is
	// mentioned again, the new task's bundle reuses the checkout — and the id.
	f.finishTurn(t, taskID, contracts.Finish{Outcome: "completed", StopReason: "end_turn"})
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 하나 더"})
	task2 := latestTaskOf(t, f, sessionID)
	if task2 == taskID {
		t.Fatal("no second task was queued")
	}
	b2 := claimBundleOn(t, f, rtID, task2)
	if b2.Workdir.ID != b.Workdir.ID {
		t.Errorf("second bundle workdir.id = %q, want %q — same session, same agent, same checkout (C3), "+
			"so the same row", b2.Workdir.ID, b.Workdir.ID)
	}
	if b2.Workdir.Path != b.Workdir.Path || !b2.Workdir.Reuse {
		t.Errorf("second bundle path=%q reuse=%v, want %q/true", b2.Workdir.Path, b2.Workdir.Reuse, b.Workdir.Path)
	}
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d workdir rows after two bundles, want 1", rows)
	}
}

// TestV11WorkdirReportByIDUpdatesTheRow: a §6 entry carrying the bundle's id
// updates THAT row, with nothing but the id to go on — a daemon that restarted
// reads only `<path>/.colab-workdir.json` and knows neither session nor agent.
// The §4.2 `phase` report in between (path only) must not make a second row.
func TestV11WorkdirReportByIDUpdatesTheRow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)
	wdID := mustUUID(t, b.Workdir.ID)

	base := "/v1/daemon/tasks/" + taskID.String() + "/attempts/1"
	d.must(200, "POST", base+"/phase", map[string]any{"phase": "preparing", "pgid": 4242, "workdir_path": b.Workdir.Path})

	// id only — no session_id, no agent_id. Before v0.8.3 this entry was
	// dropped ("session_id 가 uuid 가 아닙니다").
	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{
			"id": b.Workdir.ID, "kind": "worktree", "path": b.Workdir.Path, "bytes": 7777,
			"git": map[string]any{"branch": b.Workdir.Branch, "merged": false, "dirty": false, "commits_ahead": 2},
		}},
	})

	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows after bundle + phase + §6-by-id, want exactly 1 (the same row throughout)", rows)
	}
	var bytes int64
	var ahead int
	var merged *bool
	if err := f.pool.QueryRow(ctx, `SELECT disk_bytes, commits_ahead, merged FROM workdir WHERE id = $1`, wdID).
		Scan(&bytes, &ahead, &merged); err != nil {
		t.Fatal(err)
	}
	if bytes != 7777 || ahead != 2 || merged == nil || *merged {
		t.Errorf("row after §6-by-id: bytes=%d ahead=%d merged=%v, want 7777/2/false — the id-only "+
			"entry must update the bundle's row (daemon-protocol v0.8.3 §6)", bytes, ahead, merged)
	}
	var notes int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'`, taskID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 0 {
		t.Errorf("an id-bound entry left %d dropped-report notes on the feed", notes)
	}

	// The gc receipt goes by id too (already the case; confirmed here).
	gcCmd, _ := workdirs.BuildGCCommand(sessionID, []uuid.UUID{wdID}, []string{b.Workdir.Path})
	if err := tokens.QueueCommand(ctx, f.pool, rtID, gcCmd); err != nil {
		t.Fatal(err)
	}
	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{"id": b.Workdir.ID, "kind": "worktree", "path": b.Workdir.Path,
			"gc": map[string]any{"status": "deleted"}}},
	})
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status::text FROM workdir WHERE id = $1`, wdID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" {
		t.Errorf("status after id-only gc receipt = %q, want deleted", status)
	}
}

// TestV11OldDaemonReportFallsBackToThePair: a daemon older than v0.8.3 echoes
// no id and reports (session_id, agent_id, path). That must land on the row
// the bundle made, not beside it.
//
// 회귀 주입: in daemonWorkdirs, replace the pair fallback
// (`rep, why = s.workdirReport(…)` after workdirReportByID) with a drop
// (`why = "id required"`) — the entry is then dropped and this test fails on
// disk_bytes == 0 (measured: T-S21). The injection must fail HERE, not only
// in the daemon's tests.
func TestV11OldDaemonReportFallsBackToThePair(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)
	wdID := mustUUID(t, b.Workdir.ID)

	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{
			"kind": "worktree", "path": b.Workdir.Path, "session_id": sessionID.String(),
			"agent_id": f.leadUUID.String(), "bytes": 5555,
		}},
	})
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows after an id-less report, want 1 — the pair (session, path) finds the bundle's row", rows)
	}
	var bytes int64
	if err := f.pool.QueryRow(ctx, `SELECT disk_bytes FROM workdir WHERE id = $1`, wdID).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	if bytes != 5555 {
		t.Errorf("disk_bytes = %d, want 5555 — the old daemon's report was skipped instead of "+
			"updating the bundle's row (§6 pair fallback)", bytes)
	}
}

// TestV11UnknownWorkdirIDFallsBackToThePair: an id the server does not know
// (a directory whose row was removed with its session, then re-reported under
// a new session; or a corrupt marker file) is not authority — the entry is
// keyed on the pair like an id-less one, and nothing is lost.
func TestV11UnknownWorkdirIDFallsBackToThePair(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{
			"id": uuid.NewString(), "kind": "worktree", "path": "/Users/x/.colab/worktrees/s/lead",
			"session_id": sessionID.String(), "agent_id": f.leadUUID.String(), "bytes": 11,
		}},
	})
	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1 AND disk_bytes = 11`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d rows for a report with an unknown id, want 1 via the pair", rows)
	}
}

// TestV11WorkdirIDOfAnotherWorkspaceIsRefused: the id is checked the way a
// reported session_id is — a daemon token writes only rows of its own
// workspace, and an id from elsewhere is dropped loudly, not re-keyed.
func TestV11WorkdirIDOfAnotherWorkspaceIsRefused(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)
	wdID := mustUUID(t, b.Workdir.ID)

	// Move the session to another workspace under the row's feet: the daemon
	// still holds the id.
	var otherWS uuid.UUID
	if err := f.pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('other', 'other-' || substr(md5(random()::text), 1, 8)) RETURNING id`).Scan(&otherWS); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE room SET workspace_id = $2 WHERE id = $1`, sessionID, otherWS); err != nil {
		t.Fatal(err)
	}
	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{"id": b.Workdir.ID, "kind": "worktree", "path": b.Workdir.Path, "bytes": 999}},
	})
	var bytes int64
	if err := f.pool.QueryRow(ctx, `SELECT disk_bytes FROM workdir WHERE id = $1`, wdID).Scan(&bytes); err != nil {
		t.Fatal(err)
	}
	if bytes == 999 {
		t.Errorf("a daemon of another workspace updated the row through its id")
	}
}
