package httpapi

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// ---------------------------------------------------------------------------
// S-55 — the bundle's workdir path is ABSOLUTE (daemon-protocol v0.7.3 §4.1)
// ---------------------------------------------------------------------------

// TestP4BundleWorkdirPathIsAbsolute is G7 차단 ①.
//
// The server planned the checkout with `path.Join("", <session>, <agent>)` and
// shipped `S/backend`. The daemon then absolutised that against its own CWD:
// it checked a worktree out INSIDE the user's repository and handed the
// runtime a directory that did not exist, so every attempt of every `worktree`
// session ended `failed(config)` behind a message about `npx`. Neither golden
// caught it because each side measured its own half — this test measures the
// value that actually crosses the wire.
func TestP4BundleWorkdirPathIsAbsolute(t *testing.T) {
	f := newP2Fixture(t)
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)

	b := claimBundleOn(t, f, rtID, taskID)
	if b.Workdir.Kind != "worktree" {
		t.Fatalf("workdir.kind = %q, want worktree", b.Workdir.Kind)
	}
	if !strings.HasPrefix(b.Workdir.Path, "/") {
		t.Fatalf("workdir.path = %q — daemon-protocol v0.7.3 §4.1 makes it ABSOLUTE. A relative "+
			"path is absolutised by the daemon against its own CWD, which puts the checkout inside "+
			"the user's repository and hands the runtime a directory that does not exist "+
			"(T-I4 차단 ①)", b.Workdir.Path)
	}
	// The layout is the daemon's (`<root>/worktrees/<session>/<agent>`,
	// daemon/internal/workdir/worktree.go WorktreePath). The server owns the
	// string, so it must be the SAME string.
	want := "/Users/x/.colab/" + workdirs.WorktreesDir + "/s/lead"
	if b.Workdir.Path != want {
		t.Errorf("workdir.path = %q, want %q — the server assembles the probe's `workdir_root` (§3) "+
			"with the daemon's layout", b.Workdir.Path, want)
	}
}

// TestP4BundleRefusesWithoutWorkdirRoot is S-55's other half: no probe, no
// bundle. Shipping a relative path "so something goes out" is the failure this
// whole defect was.
func TestP4BundleRefusesWithoutWorkdirRoot(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	if _, err := f.pool.Exec(ctx, `UPDATE runtime SET workdir_root = NULL WHERE id = $1`, rtID); err != nil {
		t.Fatal(err)
	}

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)

	bundles, err := f.srv.Queue.Claim(ctx, rtID.String(), 5, f.fake.Now())
	if err != nil {
		t.Fatalf("the claim must not fail as a whole — one impossible bundle would take every other "+
			"session's queued task down with it: %v", err)
	}
	for _, b := range bundles {
		if b.Task.ID == taskID.String() {
			t.Fatalf("a bundle was handed out with workdir.path = %q even though the runtime never "+
				"probed a `workdir_root`", b.Workdir.Path)
		}
	}
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status::text FROM task WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Errorf("task status = %q, want queued — the savepoint rolls the dispatch back so the task "+
			"goes out on the claim after the probe lands", status)
	}
	// "그 사실을 알 수 있게": a task that silently stays queued is the same
	// silence the relative path was.
	var n int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event
		WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'
		  AND payload->>'detail' LIKE '%workdir_root%'`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("activity feed carries %d workdir_root notes, want exactly 1 (the claim long-polls, "+
			"so it must not redraw the note every second)", n)
	}
}

// ---------------------------------------------------------------------------
// S-56 — the git facts reach the server (daemon-protocol §4.4 · §6 v0.7.3)
// ---------------------------------------------------------------------------

// TestP4FinishWorkdirUpdatesTheRow is G7 차단 ② route 2 (§4.4).
//
// `contracts.Finish.Workdir` has existed since v0.7.2 and NOTHING in the server
// read it, so GC's only input stayed at its default — "커밋 0 · 클린" — and
// FR-6.4 M4 deleted unmerged commits (measured in 64_gc.sh).
func TestP4FinishWorkdirUpdatesTheRow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)

	base := "/v1/daemon/tasks/" + taskID.String() + "/attempts/1"
	d.must(200, "POST", base+"/phase", map[string]any{"phase": "preparing", "pgid": 4242, "workdir_path": b.Workdir.Path})
	// A measured size arrives with the §6 inventory; the finish carries no size
	// and must not erase it.
	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{
			"kind": "worktree", "path": b.Workdir.Path, "session_id": sessionID.String(),
			"agent_id": f.leadUUID.String(), "bytes": 4096,
		}},
	})
	d.must(200, "POST", base+"/phase", map[string]any{"phase": "running", "pgid": 4242})
	d.must(200, "POST", base+"/finish", map[string]any{
		"outcome": "completed", "stop_reason": "turn_end", "last_seq": 1,
		"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1, "cost_usd": 0.01},
		"workdir": map[string]any{"path": b.Workdir.Path, "git": map[string]any{"branch": b.Workdir.Branch, "merged": false, "dirty": true, "commits_ahead": 3}},
	})

	var merged, tree, dirty *bool
	var ahead int
	var bytes int64
	if err := f.pool.QueryRow(ctx, `
		SELECT merged, tree_dirty, dirty, commits_ahead, disk_bytes FROM workdir
		WHERE session_id = $1 AND path_or_ref = $2`, sessionID, b.Workdir.Path).
		Scan(&merged, &tree, &dirty, &ahead, &bytes); err != nil {
		t.Fatalf("no workdir row for the finished attempt: %v", err)
	}
	if merged == nil || *merged {
		t.Errorf("merged = %v, want false — §4.4 says the server updates the row from `workdir.git`", merged)
	}
	if tree == nil || !*tree {
		t.Errorf("tree_dirty = %v, want true (E13-13's input)", tree)
	}
	if dirty == nil || !*dirty {
		t.Errorf("dirty = %v, want true — the contract's OR", dirty)
	}
	if ahead != 3 {
		t.Errorf("commits_ahead = %d, want 3 (E13-12's input)", ahead)
	}
	if bytes != 4096 {
		t.Errorf("disk_bytes = %d, want 4096 — the finish carries no size, so it must keep the "+
			"measured one instead of zeroing the quota numerator (E13-16)", bytes)
	}
}

// TestP4WorkdirReportDropsLoudly is G7 차단 ② route 1 (§6).
//
// `workdirReport` answered a bare `false` with no log for anything it could not
// bind, so a daemon reporting a directory NAME where §6 asks for a session uuid
// — exactly what T-I4 measured — lost every row in silence.
func TestP4WorkdirReportDropsLoudly(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)

	url := "/v1/daemon/runtimes/" + rtID.String() + "/workdirs"
	// (1) the session_id is a directory name (T-I4's actual daemon behaviour).
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/s/lead", "session_id": "s", "agent_id": f.leadUUID.String(),
	}}})
	// (2) a worktree row with no agent_id: §6 v0.7.3 makes it required, because
	// that isolation has one workdir per AGENT (C3) — without it no particular
	// row is named.
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/s/lead2", "session_id": sessionID.String(),
	}}})

	var rows int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("an unbindable report wrote %d rows", rows)
	}
	var notes int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event
		WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'
		  AND payload->>'detail' LIKE '%§6%'`, taskID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	// Only (2) names a session the server can find; (1) has nowhere to hang a
	// note, which is why the log line is part of the fix too.
	if notes != 1 {
		t.Errorf("dropped §6 entries left %d notes on the feed, want 1 — 침묵은 명령에 대한 답이 "+
			"아니다 applies to the server's receiving end (daemon-protocol §6)", notes)
	}
}

// TestP4GCReceiptClosesTheRow is S-56(c). The receipt is not the inventory: a
// directory the daemon just DELETED may well be unbindable now, and refusing
// its receipt left the `gc` command unconsumed and the row open forever.
func TestP4GCReceiptClosesTheRow(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	url := "/v1/daemon/runtimes/" + rtID.String() + "/workdirs"
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/s/lead", "session_id": sessionID.String(),
		"agent_id": f.leadUUID.String(), "bytes": 10,
	}}})
	var wdID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM workdir WHERE session_id = $1`, sessionID).Scan(&wdID); err != nil {
		t.Fatal(err)
	}
	// The server asked for it (§4.3 `gc` carries {id, path}) — the same command
	// the sweep builds.
	if err := tokens.QueueCommand(ctx, f.pool, rtID,
		workdirs.BuildGCCommand(sessionID, []uuid.UUID{wdID}, []string{"/w/worktrees/s/lead"})); err != nil {
		t.Fatalf("queue gc command: %v", err)
	}
	// The receipt comes back on an entry the server can no longer bind — the
	// directory is gone, so the daemon has nothing but the id it was given.
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"id": wdID.String(), "kind": "worktree", "path": "/w/worktrees/s/lead", "session_id": "s",
		"gc": map[string]any{"status": "deleted"},
	}}})

	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status::text FROM workdir WHERE id = $1`, wdID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" {
		t.Errorf("workdir status = %q, want deleted — §6 v0.7 says the `gc` field IS the receipt and "+
			"there is no silent path", status)
	}
}

// ---------------------------------------------------------------------------
// S-57 — downloadArtifact accepts a DaemonToken (openapi v0.7.3)
// ---------------------------------------------------------------------------

// TestP4DaemonDownloadsArtifact is G7 차단 ③. §4.3 `rebind_prepare` ORDERS the
// daemon to download the session's diff artifacts; with no scheme for `cdt_`
// every GET came back 401, the manifest recorded two errors and the rebind
// prompt pointed at a directory with no diffs (E14-06 unreachable).
func TestP4DaemonDownloadsArtifact(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	artID := f.submitArtifactBody(t, sessionID, f.taskTokenFor(t, sessionID, f.wUUID).bearer, "step-1", "diff --git a/x b/x\n")

	st, body, _ := d.raw("GET", f.p+"/artifacts/"+artID.String()+"/content", nil)
	if st != 200 {
		t.Fatalf("daemon download = %d, want 200 (openapi v0.7.3 adds DaemonToken to downloadArtifact): %s", st, body)
	}
	if !strings.Contains(string(body), "diff --git") {
		t.Errorf("body = %q, want the artifact content", string(body))
	}

	// Scope: the session pinned to THIS runtime, and nothing else.
	other := testdb.AddRuntime(t, f.pool, mustUUID(t, f.wsID), "mac-2", f.fake.Now())
	if _, err := f.pool.Exec(ctx, `UPDATE session SET runtime_id = $2 WHERE id = $1`, sessionID, other); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := d.raw("GET", f.p+"/artifacts/"+artID.String()+"/content", nil); st != 404 {
		t.Errorf("download of another runtime's session = %d, want 404 — the scope is "+
			"\"그 런타임에 고정된 세션의 아티팩트만\"", st)
	}

	// And the widening is ONE operation: a daemon token reads a body, nothing else.
	if st, _, _ := d.raw("GET", f.p+"/artifacts/"+artID.String(), nil); st == 200 {
		t.Errorf("getArtifact accepted a daemon token — openapi lists DaemonToken on downloadArtifact only")
	}
}

// ---------------------------------------------------------------------------
// S-59 — `review reject` re-enters the submitting lane (openapi v0.7.3)
// ---------------------------------------------------------------------------

// TestP4RejectReEntersTheSubmittingLane is E16-B step 5.
//
// The rejection reply is written by the REVIEWER agent with no mention, so
// routing rule 4 stopped it dead: T-I4 measured zero tasks carrying the
// rejection as their trigger, and the re-entry only happened because a person
// relayed it. The trigger is a PLATFORM event now, so rule 4 keeps its meaning
// for ordinary messages.
func TestP4RejectReEntersTheSubmittingLane(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	// The writer works and submits; the researcher is the designated reviewer.
	f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 만들어라"})
	writerTask := latestTaskOf(t, f, sessionID)
	var writerLane uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT lane_id FROM task WHERE id = $1`, writerTask).Scan(&writerLane); err != nil {
		t.Fatal(err)
	}
	// The researcher is the designated reviewer (FR-2.2 agent_approval, E6-06).
	if _, err := f.pool.Exec(ctx, `
		UPDATE session SET completion_condition = jsonb_build_object('op', 'and', 'conditions',
		    jsonb_build_array(jsonb_build_object('type', 'agent_approval', 'agent_id', $2::text)))
		WHERE id = $1`, sessionID, f.rUUID.String()); err != nil {
		t.Fatal(err)
	}
	// The artifact is submitted BY that task, with that task's token — the lane
	// the rejection has to come back to is the one that did the work.
	writerTok, err := f.srv.Tokens.Issue(ctx, f.pool, tokens.Scope{
		TaskID: writerTask, Attempt: 1, LaneID: writerLane, SessionID: sessionID, AgentID: f.wUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	artID := f.submitArtifactBody(t, sessionID, writerTok, "ui", "diff --git a/x b/x\n")
	// The lane finished its turn: step 5 is a RE-entry, not a join.
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'done', finished_at = $2 WHERE id = $1`, writerLane, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'completed' WHERE id = $1`, writerTask); err != nil {
		t.Fatal(err)
	}
	before := laneReentry(t, f, writerLane)

	reviewer := f.taskTokenFor(t, sessionID, f.rUUID)
	reviewer.must(200, "POST", f.p+"/artifacts/"+artID.String()+"/review",
		map[string]any{"verdict": "reject", "comments": "여백이 틀렸다"})

	if got := laneReentry(t, f, writerLane); got != before+1 {
		t.Errorf("reentry_count = %d, want %d — openapi reviewArtifact v0.7.3: 서버가 그 lane 을 "+
			"명시적으로 재진입시킨다(해소 규칙 1과 같은 결과)", got, before+1)
	}
	var lanes, tasks int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM lane WHERE session_id = $1 AND agent_id = $2`, sessionID, f.wUUID).Scan(&lanes); err != nil {
		t.Fatal(err)
	}
	if lanes != 1 {
		t.Errorf("the writer has %d lanes, want 1 — a rejection re-enters, it does not fork (E16-B 5단계 "+
			"counts 워크트리 2개, not 3)", lanes)
	}
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task t JOIN message m ON m.id = t.trigger_message_id
		WHERE t.lane_id = $1 AND t.status = 'queued' AND m.content LIKE '리뷰 반려%'`, writerLane).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 1 {
		t.Errorf("%d queued tasks triggered by the rejection message, want 1 — this is the count "+
			"T-I4 measured as 0 (B5g)", tasks)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// worktreeSessionOn turns the fixture's session into a `worktree` session
// pinned to a probed runtime, and returns that runtime.
func worktreeSessionOn(t *testing.T, f *p2Fixture, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	var rtID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 ORDER BY created_at LIMIT 1`,
		mustUUID(t, f.wsID)).Scan(&rtID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET isolation = '{"kind":"worktree","repo_path":"/Users/x/app","remote_url":"git@github.com:acme/app.git"}',
		       runtime_id = $2 WHERE id = $1`, sessionID, rtID); err != nil {
		t.Fatal(err)
	}
	return rtID
}

func claimBundleOn(t *testing.T, f *p2Fixture, runtimeID, taskID uuid.UUID) contracts.TaskBundle {
	t.Helper()
	bundles, err := f.srv.Queue.Claim(t.Context(), runtimeID.String(), 5, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		if b.Task.ID == taskID.String() {
			return b
		}
	}
	t.Fatalf("no bundle for task %s (got %d)", taskID, len(bundles))
	return contracts.TaskBundle{}
}

// daemonFor mints a daemon token for an existing runtime row by rewriting its
// token hash — pairing needs a code this fixture's runtime never had.
func (f *p2Fixture) daemonFor(t *testing.T, runtimeID uuid.UUID) *client {
	t.Helper()
	token := "cdt_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime SET daemon_token_hash = encode(sha256($2::bytea), 'hex') WHERE id = $1`,
		runtimeID, []byte(token)); err != nil {
		t.Fatal(err)
	}
	return &client{t: t, srv: f.api.srv, bearer: token}
}

// submitArtifactBody stores a real body so downloadArtifact has bytes to
// stream. It goes through the multipart surface the CLI uses.
func (f *p2Fixture) submitArtifactBody(t *testing.T, sessionID uuid.UUID, tok, name, body string) uuid.UUID {
	t.Helper()
	st, out := f.submit(t, sessionID.String(), tok, name, "diff", []byte(body))
	if st != 201 {
		t.Fatalf("submit artifact = %d: %v", st, out)
	}
	return mustUUID(t, str(out["artifact"].(map[string]any), "id"))
}

// taskTokenFor issues a task token for a running attempt of `agent`, which is
// how an agent talks to the server (colab-cli.md §1).
func (f *p2Fixture) taskTokenFor(t *testing.T, sessionID, agentID uuid.UUID) *client {
	t.Helper()
	ctx := t.Context()
	var laneID, taskID, profileID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT profile_id FROM agent_profile ap JOIN agent a ON a.id = ap.agent_id WHERE a.id = $1 LIMIT 1`, agentID).Scan(&profileID); err != nil {
		if err := f.pool.QueryRow(ctx, `SELECT id FROM agent_profile WHERE agent_id = $1 LIMIT 1`, agentID).Scan(&profileID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'running', $4, $4) RETURNING id`, sessionID, agentID, profileID, f.fake.Now()).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'running', $5, $5) RETURNING id`, laneID, sessionID, agentID, profileID, f.fake.Now()).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	tok, err := f.srv.Tokens.Issue(ctx, f.pool, tokens.Scope{
		TaskID: taskID, Attempt: 1, LaneID: laneID, SessionID: sessionID, AgentID: agentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &client{t: t, srv: f.api.srv, bearer: tok}
}

func laneReentry(t *testing.T, f *p2Fixture, laneID uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT reentry_count FROM lane WHERE id = $1`, laneID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
