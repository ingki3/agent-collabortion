package httpapi

// T-FOLDERS (daemon-protocol v0.10.0 §4.1·§6.1, harness v0.9.7 `<folders>`,
// Director 판정 2026-09-26 D1~D8): the values that cross the wire, read off a
// real claim — the bundle is where the server's path, the row and the turn
// prompt meet.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/queue"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

const folderRoot = "/Users/x/.colab"

// roomPiece / agentPiece are §6.1's pieces from the CURRENT names — what a
// NEW row is named with.
func folderPieces(t *testing.T, f *p2Fixture, roomID, workID, agentID uuid.UUID) (room, mission, agent string) {
	t.Helper()
	var rn, wt, an string
	if err := f.pool.QueryRow(t.Context(), `SELECT r.name, w.title, a.name FROM room r, work w, agent a WHERE r.id = $1 AND w.id = $2 AND a.id = $3`,
		roomID, workID, agentID).Scan(&rn, &wt, &an); err != nil {
		t.Fatal(err)
	}
	return workdirs.Piece(rn, roomID, 8), workdirs.Piece(wt, workID, 8), workdirs.Piece(an, agentID, 8)
}

func between2(s, open, close string) string {
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return s[i : i+j+len(close)]
}

// Row 1 + first-attempt id: a `none` mission turn gets `rooms/<room>/<mission>/<agent>`,
// an id from the FIRST attempt (the row is made before the bundle leaves),
// the mission's `_shared`, and the lane bound — all in one claim.
func TestFoldersNoneMissionBundle(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	room, work := mustUUID(t, f.sessionID), mustUUID(t, f.missionID)
	if _, err := f.pool.Exec(ctx, `UPDATE room SET name = '게임 제작' WHERE id = $1`, room); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE work SET title = '스네이크 v2' WHERE id = $1`, work); err != nil {
		t.Fatal(err)
	}
	task := f.mentionTask(t, f.rUUID, "R", f.missionID)
	b := f.claimBundle(t, task)

	rp, mp, ap := folderPieces(t, f, room, work, f.rUUID)
	want := folderRoot + "/rooms/" + rp + "/" + mp + "/" + ap
	if b.Workdir.Kind != "dir" || b.Workdir.Path != want {
		t.Fatalf("workdir = %s %q, want dir %q (§6.1 표 1행)", b.Workdir.Kind, b.Workdir.Path, want)
	}
	if !strings.Contains(b.Workdir.Path, "/rooms/게임-제작-") || !strings.Contains(b.Workdir.Path, "/스네이크-v2-") {
		t.Errorf("path %q lost the Hangul names — the path slug keeps them (D1 하위 결정)", b.Workdir.Path)
	}
	if b.Workdir.SharedPath != folderRoot+"/rooms/"+rp+"/"+mp+"/_shared" {
		t.Errorf("shared_path = %q", b.Workdir.SharedPath)
	}
	id, err := uuid.Parse(b.Workdir.ID)
	if err != nil || id == uuid.Nil {
		t.Fatalf("first attempt workdir.id = %q — v0.10.0 §4.1 carries it from the first attempt, dir too", b.Workdir.ID)
	}
	var path, role string
	var wid, laneWD *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT path_or_ref, role, work_id FROM workdir WHERE id = $1`, id).Scan(&path, &role, &wid); err != nil {
		t.Fatalf("no row behind the bundle's id: %v", err)
	}
	if path != want || role != "agent" || wid == nil || *wid != work {
		t.Errorf("row = (%s, %s, %v)", path, role, wid)
	}
	if err := f.pool.QueryRow(ctx, `SELECT workdir_id FROM lane WHERE id = $1`, mustUUID(t, b.Task.LaneID)).Scan(&laneWD); err != nil || laneWD == nil || *laneWD != id {
		t.Errorf("lane.workdir_id = %v, want %s (bound at bundle time)", laneWD, id)
	}
	var shared int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE work_id = $1 AND role = 'shared' AND agent_id IS NULL AND lane_id IS NULL`, work).Scan(&shared)
	if shared != 1 {
		t.Errorf("_shared rows = %d, want 1 (role=shared, no agent, no lane)", shared)
	}
}

// Row 3: a turn outside any mission → `_room/<agent>`, no shared_path, no
// shared line, no peers.
func TestFoldersOutsideMission(t *testing.T) {
	f := newP2Fixture(t)
	room := mustUUID(t, f.sessionID)
	task := f.mentionTask(t, f.rUUID, "R", "")
	if _, err := f.pool.Exec(t.Context(), `UPDATE task SET work_id = NULL WHERE id = $1`, task); err != nil {
		t.Fatal(err)
	}
	b := f.claimBundle(t, task)
	var rn string
	_ = f.pool.QueryRow(t.Context(), `SELECT name FROM room WHERE id = $1`, room).Scan(&rn)
	want := folderRoot + "/rooms/" + workdirs.Piece(rn, room, 8) + "/_room/" + workdirs.Piece("R", f.rUUID, 8)
	if b.Workdir.Path != want || b.Workdir.SharedPath != "" {
		t.Fatalf("outside a mission: path=%q shared=%q, want %q and none", b.Workdir.Path, b.Workdir.SharedPath, want)
	}
	folders := between2(b.Prompt, "<folders>", "</folders>")
	if strings.Contains(folders, "shared:") || strings.Contains(folders, "(read only)") {
		t.Errorf("<folders> outside a mission names a shared folder or peers:\n%s", folders)
	}
}

// Renaming the room, the mission and the agent does not move the folder
// (§6.1 만들 때 고정): the next bundle of the same (room, mission, agent) names
// the stored path.
func TestFoldersPathSurvivesRename(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	first := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	for _, q := range []string{
		`UPDATE room SET name = '이름을 바꾼 방' WHERE id = '` + f.sessionID + `'`,
		`UPDATE work SET title = '바뀐 미션' WHERE id = '` + f.missionID + `'`,
		`UPDATE agent SET name = 'Researcher2' WHERE id = '` + f.r + `'`,
	} {
		if _, err := f.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	second := f.claimBundle(t, f.mentionTask(t, f.rUUID, "Researcher2", f.missionID))
	if second.Workdir.Path != first.Workdir.Path || second.Workdir.ID != first.Workdir.ID || second.Workdir.SharedPath != first.Workdir.SharedPath {
		t.Fatalf("after renames: %q/%s/%q, want %q/%s/%q unchanged", second.Workdir.Path, second.Workdir.ID, second.Workdir.SharedPath,
			first.Workdir.Path, first.Workdir.ID, first.Workdir.SharedPath)
	}
	if !second.Workdir.Reuse {
		t.Error("reuse = false for a folder that already existed")
	}
}

// Two lanes of the same agent in the same mission share ONE row (D3 A), and
// each gets the lane-label line (harness v0.9.7).
func TestFoldersSameAgentTwoLanesShareTheFolder(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	t1 := f.mentionTask(t, f.rUUID, "R", f.missionID)
	b1 := f.claimBundle(t, t1)
	// A second lane of R in the same mission (a parallel delegation).
	var lane2 uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at, work_id)
		SELECT session_id, agent_id, profile_id, 'queued', now(), now(), work_id FROM lane WHERE id = $1 RETURNING id`,
		mustUUID(t, b1.Task.LaneID)).Scan(&lane2); err != nil {
		t.Fatal(err)
	}
	t2 := f.mentionTask(t, f.rUUID, "R", f.missionID)
	if _, err := f.pool.Exec(ctx, `UPDATE task SET lane_id = $2 WHERE id = $1`, t2, lane2); err != nil {
		t.Fatal(err)
	}
	b2 := f.claimBundle(t, t2)
	if b2.Workdir.ID != b1.Workdir.ID || b2.Workdir.Path != b1.Workdir.Path {
		t.Fatalf("two lanes of one agent in one mission: %s %q vs %s %q — D3 A: one folder", b1.Workdir.ID, b1.Workdir.Path, b2.Workdir.ID, b2.Workdir.Path)
	}
	folders := between2(b2.Prompt, "<folders>", "</folders>")
	if !strings.Contains(folders, "put your lane label `"+lane2.String()[:8]+"`") {
		t.Errorf("<folders> of the second lane has no lane-label line:\n%s", folders)
	}
	var rows int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1 AND role = 'agent' AND agent_id = $2`, f.sessionID, f.rUUID).Scan(&rows)
	if rows != 1 {
		t.Errorf("agent rows for R = %d, want 1", rows)
	}
}

// Two agents of one mission: B's `<folders>` lists A's folder (read only),
// only once A's row exists; the block sits between <roster_status> and
// <trigger>; the last sentence is in the tool surface's words.
func TestFoldersPeersAndPlacement(t *testing.T) {
	f := newP2Fixture(t)
	// W speaks first: R has no folder yet → no peer line for R.
	bw := f.claimBundle(t, f.mentionTask(t, f.wUUID, "W", f.missionID))
	if strings.Contains(between2(bw.Prompt, "<folders>", "</folders>"), "- R:") {
		t.Errorf("a peer with no row was listed:\n%s", bw.Prompt)
	}
	br := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	folders := between2(br.Prompt, "<folders>", "</folders>")
	for _, want := range []string{
		"you: " + br.Workdir.Path + "  (your working folder — write here)\n",
		"shared: " + br.Workdir.SharedPath + "  (everyone on this mission reads and writes)\n",
		"- W: " + bw.Workdir.Path + "  (read only)\n",
		queue.FoldersLastMCP + "\n</folders>",
	} {
		if !strings.Contains(folders, want) {
			t.Errorf("<folders> lacks %q:\n%s", want, folders)
		}
	}
	if strings.Contains(folders, "- R:") || strings.Contains(folders, "colab room read") {
		t.Errorf("<folders> lists itself as a peer, or names the shell command on the mcp surface:\n%s", folders)
	}
	rs, fo, tr := strings.Index(br.Prompt, "<roster_status>"), strings.Index(br.Prompt, "<folders>"), strings.Index(br.Prompt, "<trigger>")
	if !(rs >= 0 && rs < fo && fo < tr) {
		t.Errorf("<folders> is not between <roster_status> and <trigger>: %d %d %d", rs, fo, tr)
	}
}

// The cli_wrapper (hermes) surface says `colab room read` in both places; the
// brief [2] line is the same bytes in a mission turn and outside one (E12-11).
func TestFoldersSurfaceSentencesAndBriefBytes(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE agent_profile SET runtime_kind = 'hermes' WHERE agent_id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	in := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	out := f.mentionTask(t, f.rUUID, "R", "")
	// A turn outside any mission: the room's open mission would be resolved
	// through the LANE too, so both have to let go (this is the shape of a
	// task left queued when its mission was deleted).
	if _, err := f.pool.Exec(ctx, `UPDATE task SET work_id = NULL WHERE id = $1`, out); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET work_id = NULL WHERE id = (SELECT lane_id FROM task WHERE id = $1)`, out); err != nil {
		t.Fatal(err)
	}
	outB := f.claimBundle(t, out)
	if strings.Contains(outB.Prompt, "<folders>\nyou:") && strings.Contains(outB.Prompt, "shared:") {
		t.Errorf("a turn outside a mission has no shared folder:\n%s", between2(outB.Prompt, "<folders>", "</folders>"))
	}
	if !strings.Contains(in.Prompt, queue.FoldersLast+"\n</folders>") {
		t.Errorf("hermes <folders> last sentence is not the cli_wrapper one:\n%s", between2(in.Prompt, "<folders>", "</folders>"))
	}
	two := section(in.Brief.Text, 2)
	if !strings.Contains(two, queue.FoldersRule+"\n") {
		t.Errorf("brief [2] lacks the fixed folder line:\n%s", two)
	}
	// The line is in [2] whether or not the turn is in a mission, and [2] is
	// the same bytes either way (E12-11: [2] is part of the cached prefix and
	// a turn that steps out of a mission must not rewrite it).
	if !strings.Contains(section(outB.Brief.Text, 2), queue.FoldersRule+"\n") {
		t.Errorf("brief [2] of a turn outside a mission lacks the fixed folder line:\n%s", section(outB.Brief.Text, 2))
	}
	if two != section(outB.Brief.Text, 2) {
		t.Errorf("brief [2] differs between a mission turn and a turn outside one — E12-11:\n%s\n---\n%s", two, section(outB.Brief.Text, 2))
	}
	// mcp surface: the tool words, never the shell command.
	lead := f.claimBundle(t, f.mentionTask(t, f.leadUUID, "Lead", f.missionID))
	two = section(lead.Brief.Text, 2)
	if !strings.Contains(two, queue.FoldersRuleMCP) || strings.Contains(two, "`colab room read`") {
		t.Errorf("mcp brief [2] folder line:\n%s", two)
	}
	if !strings.Contains(lead.Prompt, queue.FoldersLastMCP+"\n</folders>") || strings.Contains(lead.Prompt, queue.FoldersLast) {
		t.Errorf("mcp <folders> last sentence is not the tool one:\n%s", between2(lead.Prompt, "<folders>", "</folders>"))
	}
}

// D6 A: a lane whose row is an old `sessions/<room>/<lane>` folder keeps it on
// re-entry — the runtime's resume cwd must not move.
func TestFoldersOldLaneKeepsItsPath(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	first := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	old := folderRoot + "/sessions/" + f.sessionID + "/" + first.Task.LaneID
	if _, err := f.pool.Exec(ctx, `UPDATE workdir SET path_or_ref = $2, work_id = NULL WHERE id = $1`, first.Workdir.ID, old); err != nil {
		t.Fatal(err)
	}
	t2 := f.mentionTask(t, f.rUUID, "R", f.missionID)
	if _, err := f.pool.Exec(ctx, `UPDATE task SET lane_id = $2 WHERE id = $1`, t2, first.Task.LaneID); err != nil {
		t.Fatal(err)
	}
	b := f.claimBundle(t, t2)
	if b.Workdir.Path != old || b.Workdir.ID != first.Workdir.ID {
		t.Fatalf("re-entry of an old-layout lane: %q %s, want the stored %q %s (D6 A)", b.Workdir.Path, b.Workdir.ID, old, first.Workdir.ID)
	}
}

// §4.1 v0.10.0 claim rule: `none` without `workdir_root` is refused like
// `worktree` — no bundle, task stays queued, the feed says why.
func TestFoldersNoneRefusedWithoutRoot(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `UPDATE runtime SET workdir_root = NULL WHERE workspace_id = $1`, f.wsID); err != nil {
		t.Fatal(err)
	}
	task := f.mentionTask(t, f.rUUID, "R", f.missionID)
	var rt uuid.UUID
	_ = f.pool.QueryRow(ctx, `SELECT id FROM runtime WHERE workspace_id = $1`, f.wsID).Scan(&rt)
	bundles, err := f.srv.Queue.Claim(ctx, rt.String(), 5, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		if b.Task.ID == task.String() {
			t.Fatalf("a `none` bundle went out with no workdir_root: %+v", b.Workdir)
		}
	}
	var status string
	var notes int
	_ = f.pool.QueryRow(ctx, `SELECT status::text FROM task WHERE id = $1`, task).Scan(&status)
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'`, task).Scan(&notes)
	if status != "queued" || notes == 0 {
		t.Errorf("task %s, feed notes %d — want queued and one sentence on the feed", status, notes)
	}
}

// D8 B: closing a mission issues no gc for its folders; the sweep collects
// the agent rows AND the `_shared` row once `last_used_at + retention` has
// passed — not before.
func TestFoldersGCRetentionAfterClose(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	b := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	f.api.must(200, "POST", f.p+"/works/"+f.missionID+"/complete", map[string]any{"confirm": true})
	gcFor := func() (n int) {
		_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM daemon_command WHERE type = 'gc' AND consumed_at IS NULL
			AND (payload::text LIKE '%' || $1 || '%')`, b.Workdir.ID).Scan(&n)
		return
	}
	if gcFor() != 0 {
		t.Fatal("closing the mission issued a gc for its folder — D8 B: the close is not a delete trigger")
	}
	if _, err := f.srv.Workdirs.SweepGC(ctx); err != nil {
		t.Fatal(err)
	}
	if gcFor() != 0 {
		t.Fatal("the sweep collected a mission folder right after the close — D8 B keeps it for workdir_retention_days")
	}
	// 13 days later: still kept.
	f.fake.Advance(13 * 24 * time.Hour)
	if _, err := f.pool.Exec(ctx, `UPDATE workdir SET last_used_at = $2 WHERE session_id = $1`, f.sessionID, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Workdirs.SweepGC(ctx); err != nil {
		t.Fatal(err)
	}
	if gcFor() != 0 {
		t.Fatal("collected at 13 days with a 14-day retention")
	}
	f.fake.Advance(2 * 24 * time.Hour)
	if _, err := f.srv.Workdirs.SweepGC(ctx); err != nil {
		t.Fatal(err)
	}
	var payload string
	if err := f.pool.QueryRow(ctx, `SELECT payload::text FROM daemon_command WHERE type = 'gc' AND consumed_at IS NULL
		AND payload::text LIKE '%' || $1 || '%'`, b.Workdir.ID).Scan(&payload); err != nil {
		t.Fatalf("no gc after retention: %v", err)
	}
	if !strings.Contains(payload, b.Workdir.SharedPath) {
		t.Errorf("gc after retention = %s, want the `_shared` row too", payload)
	}
}

// An open mission keeps its folders at any age (GC table row 1).
func TestFoldersOpenMissionNeverCollected(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	b := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	if _, err := f.pool.Exec(ctx, `UPDATE workdir SET last_used_at = $2 WHERE session_id = $1`, f.sessionID, t0.Add(-400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'done' WHERE session_id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.Workdirs.SweepGC(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = f.pool.QueryRow(ctx, `SELECT count(*) FROM daemon_command WHERE type = 'gc' AND payload::text LIKE '%' || $1 || '%'`, b.Workdir.SharedPath).Scan(&n)
	if n != 0 {
		t.Error("the sweep collected an OPEN mission's folders")
	}
}

// §6 `role: shared` report with no id (the daemon writes no name tag in
// `_shared`) lands on the `_shared` row by its stored path.
func TestFoldersSharedReportFindsItsRow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	var rt uuid.UUID
	_ = f.pool.QueryRow(ctx, `SELECT id FROM runtime WHERE workspace_id = $1`, f.wsID).Scan(&rt)
	d := f.daemonFor(t, rt)
	b := f.claimBundle(t, f.mentionTask(t, f.rUUID, "R", f.missionID))
	d.must(200, "POST", "/v1/daemon/runtimes/"+rt.String()+"/workdirs", map[string]any{"workdirs": []map[string]any{{
		"kind": "dir", "path": b.Workdir.SharedPath, "role": "shared", "bytes": 4242,
	}}})
	var bytes int64
	_ = f.pool.QueryRow(ctx, `SELECT disk_bytes FROM workdir WHERE path_or_ref = $1`, b.Workdir.SharedPath).Scan(&bytes)
	if bytes != 4242 {
		t.Errorf("_shared row bytes = %d, want 4242 from the role=shared report", bytes)
	}
	// S13: ?work_id= lists the mission's rows (every agent claimed in it + one
	// `_shared`) with work·role. (claimBundle claims every queued bundle, so
	// the Lead's folder may be there too.)
	page := f.api.must(200, "GET", f.p+"/runtimes/"+rt.String()+"/workdirs?work_id="+f.missionID, nil)
	items := page["items"].([]any)
	roles := map[string]int{}
	for _, raw := range items {
		it := raw.(map[string]any)
		roles[str(it, "role")]++
		if w, _ := it["work"].(map[string]any); str(w, "id") != f.missionID {
			t.Errorf("item work = %v", it["work"])
		}
	}
	if roles["agent"] < 1 || roles["shared"] != 1 || len(items) != roles["agent"]+roles["shared"] {
		t.Errorf("roles = %v over %d items", roles, len(items))
	}
	if page["disk_bytes_total"].(float64) != 4242 {
		t.Errorf("disk_bytes_total for the mission = %v, want 4242", page["disk_bytes_total"])
	}
}

// D7 C: a `worktree` room's checkout is room × agent under `_worktrees`, on
// branch `colab/<Slug(room)>-<room_id8>/<Slug(agent)>` (FINDING-1: the ROOM
// name, not the first mission title); a mission turn also gets the mission's
// `_shared`, outside the repository; peers are the checkouts of agents with a
// lane in the mission.
func TestFoldersWorktreeRoom(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	room := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, room)
	if _, err := f.pool.Exec(ctx, `UPDATE room SET name = '결제 앱' WHERE id = $1`, room); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE work SET title = 'Checkout Flow' WHERE id = $1`, f.missionID); err != nil {
		t.Fatal(err)
	}
	bw := claimBundleOn(t, f, rtID, f.mentionTask(t, f.wUUID, "W", f.missionID))
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed' WHERE id = $1`, bw.Task.ID); err != nil {
		t.Fatal(err)
	}
	br := claimBundleOn(t, f, rtID, f.mentionTask(t, f.rUUID, "R", f.missionID))
	rp := workdirs.Piece("결제 앱", room, 8)
	if br.Workdir.Kind != "worktree" || br.Workdir.Path != folderRoot+"/rooms/"+rp+"/_worktrees/"+workdirs.Piece("R", f.rUUID, 8) {
		t.Fatalf("worktree path = %q", br.Workdir.Path)
	}
	if want := "colab/x-" + room.String()[:8] + "/r"; br.Workdir.Branch != want {
		t.Errorf("branch = %q, want %q — room name + room id8, not the mission title (FINDING-1)", br.Workdir.Branch, want)
	}
	if !strings.HasPrefix(br.Workdir.SharedPath, folderRoot+"/rooms/"+rp+"/checkout-flow-") || !strings.HasSuffix(br.Workdir.SharedPath, "/_shared") {
		t.Errorf("worktree room shared_path = %q (D7 C — outside the repository)", br.Workdir.SharedPath)
	}
	// E13-08 (계약, e2e 73): a bundle under `worktree` names no other
	// agent's checkout — that is a dirty working copy of the user's
	// repository, and a reviewer reading it reviews what nobody submitted.
	// The mission meets in `_shared`, which D7 C puts outside the repository.
	folders := between2(br.Prompt, "<folders>", "</folders>")
	if strings.Contains(folders, bw.Workdir.Path) || strings.Contains(folders, "(read only)") {
		t.Errorf("worktree <folders> names a peer checkout (E13-08):\n%s", folders)
	}
	if !strings.Contains(folders, "shared: "+br.Workdir.SharedPath) {
		t.Errorf("worktree <folders> has no shared line:\n%s", folders)
	}
}

var _ = contracts.BundleWorkdir{}
