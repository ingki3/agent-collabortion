package httpapi

// PR #345 review (review345a NN1·NN2): two of T-FOLDERS' refusals are
// defended twice, and a suite that only exercises the path where BOTH layers
// agree cannot see one of them die. Each test here reaches a state where the
// OTHER layer does not fire, so switching off the layer under test turns it
// red:
//
//   - root guard (queue/folders.go planBundleWorkdir `r == "" || !IsAbs(r)`):
//     the inner layers (PlanDir / PlanWorktreePath → "" → errNoWorkdirRoot)
//     run only when a path is PLANNED. A lane that already has its row (D6 A,
//     C3 reuse) plans nothing — only the outer guard refuses it.
//   - E13-08 (renderFolders `if !worktree`): MissionPeers' `none` query
//     already skips `kind = 'worktree'` rows, so a worktree room with only
//     checkouts hides the gap. A room that switched from `none` to `worktree`
//     keeps its mission's `dir` rows — only `if !worktree` keeps them out.

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// releaseAndReenter completes the claimed task and queues a new turn of the
// same agent on the same lane — the re-entry that reuses the lane's row.
func releaseAndReenter(t *testing.T, f *p2Fixture, claimedTask string, laneID string, agent uuid.UUID, name string, missionID string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed' WHERE id = $1`, claimedTask); err != nil {
		t.Fatal(err)
	}
	next := f.mentionTask(t, agent, name, missionID)
	if _, err := f.pool.Exec(ctx, `UPDATE task SET lane_id = $2 WHERE id = $1`, next, laneID); err != nil {
		t.Fatal(err)
	}
	return next
}

// outsideMissionTask is a turn with no mission on task AND lane (the room's
// open mission would otherwise be resolved through the lane).
func outsideMissionTask(t *testing.T, f *p2Fixture, agent uuid.UUID, name string) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	id := f.mentionTask(t, agent, name, "")
	if _, err := f.pool.Exec(ctx, `UPDATE task SET work_id = NULL WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET work_id = NULL WHERE id = (SELECT lane_id FROM task WHERE id = $1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func assertRefusedNoRoot(t *testing.T, f *p2Fixture, rt, task uuid.UUID, root string) {
	t.Helper()
	ctx := t.Context()
	bundles, err := f.srv.Queue.Claim(ctx, rt.String(), 5, f.fake.Now())
	if err != nil {
		t.Fatalf("root %q: claim error %v — the refusal is a queued task, not a claim failure", root, err)
	}
	for _, b := range bundles {
		if b.Task.ID == task.String() {
			t.Fatalf("root %q: a bundle went out naming %q — the outer root guard is the only layer that refuses a lane which already has its row (§4.1 v0.10.0)", root, b.Workdir.Path)
		}
	}
	var status string
	_ = f.pool.QueryRow(ctx, `SELECT status::text FROM task WHERE id = $1`, task).Scan(&status)
	if status != "queued" {
		t.Errorf("root %q: task %s, want queued", root, status)
	}
}

// `none`: the lane's `_room/<agent>` row exists; the root is then lost (NULL)
// or made relative. LaneRow finds the row, so PlanDir never runs.
func TestFoldersRootGuardOuterLayerNoneReusedRow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	var rt uuid.UUID
	_ = f.pool.QueryRow(ctx, `SELECT id FROM runtime WHERE workspace_id = $1`, f.wsID).Scan(&rt)
	first := f.claimBundle(t, outsideMissionTask(t, f, f.rUUID, "R"))
	if first.Workdir.Path == "" || first.Workdir.SharedPath != "" {
		t.Fatalf("setup: outside-mission bundle %+v", first.Workdir)
	}
	next := releaseAndReenter(t, f, first.Task.ID, first.Task.LaneID, f.rUUID, "R", "")
	if _, err := f.pool.Exec(ctx, `UPDATE task SET work_id = NULL WHERE id = $1`, next); err != nil {
		t.Fatal(err)
	}
	for _, root := range []*string{nil, strPtr("relative/.colab")} {
		if _, err := f.pool.Exec(ctx, `UPDATE runtime SET workdir_root = $2 WHERE id = $1`, rt, root); err != nil {
			t.Fatal(err)
		}
		assertRefusedNoRoot(t, f, rt, next, deref(root))
	}
}

// `worktree`: the agent's checkout exists (C3 reuse); the root is then lost.
// BundleWorkdirPaths finds the checkout, so PlanNewWorktree never runs.
func TestFoldersRootGuardOuterLayerWorktreeReusedCheckout(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	rt := worktreeSessionOn(t, f, mustUUID(t, f.sessionID))
	first := claimBundleOn(t, f, rt, outsideMissionTask(t, f, f.rUUID, "R"))
	if first.Workdir.Kind != "worktree" || first.Workdir.Path == "" {
		t.Fatalf("setup: worktree bundle %+v", first.Workdir)
	}
	next := releaseAndReenter(t, f, first.Task.ID, first.Task.LaneID, f.rUUID, "R", "")
	if _, err := f.pool.Exec(ctx, `UPDATE task SET work_id = NULL WHERE id = $1`, next); err != nil {
		t.Fatal(err)
	}
	for _, root := range []*string{nil, strPtr("relative/.colab")} {
		if _, err := f.pool.Exec(ctx, `UPDATE runtime SET workdir_root = $2 WHERE id = $1`, rt, root); err != nil {
			t.Fatal(err)
		}
		assertRefusedNoRoot(t, f, rt, next, deref(root))
	}
}

// E13-08, renderFolders layer: the room ran `none` (W's mission folder is a
// `dir` row), then switched to `worktree`. MissionPeers' none query would
// list W's dir row — only `if !worktree` in renderFolders keeps it out.
func TestFoldersE1308RenderLayerAfterIsolationSwitch(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	bw := f.claimBundle(t, f.mentionTask(t, f.wUUID, "W", f.missionID))
	if bw.Workdir.Kind != "dir" {
		t.Fatalf("setup: W's none bundle kind %q", bw.Workdir.Kind)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed' WHERE id = $1`, bw.Task.ID); err != nil {
		t.Fatal(err)
	}
	rt := worktreeSessionOn(t, f, mustUUID(t, f.sessionID))
	br := claimBundleOn(t, f, rt, f.mentionTask(t, f.rUUID, "R", f.missionID))
	if br.Workdir.Kind != "worktree" {
		t.Fatalf("setup: R's bundle kind %q, want worktree", br.Workdir.Kind)
	}
	folders := between2(br.Prompt, "<folders>", "</folders>")
	if strings.Contains(folders, bw.Workdir.Path) || strings.Contains(folders, "(read only)") {
		t.Errorf("worktree <folders> lists a peer folder (E13-08, renderFolders layer):\n%s", folders)
	}
	if !strings.Contains(folders, "shared: ") {
		t.Errorf("worktree <folders> lost its shared line:\n%s", folders)
	}
}

func strPtr(s string) *string { return &s }
