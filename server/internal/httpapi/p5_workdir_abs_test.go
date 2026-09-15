package httpapi

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// ---------------------------------------------------------------------------
// S-62 — a workdir row stored before migration 0019 must not reach the wire
// ---------------------------------------------------------------------------
//
// S-55 stopped the server from PRODUCING a relative path and 0019 gave it the
// material to build an absolute one, but neither looked at rows already in the
// table. `ExistingForAgent` handed a stored string straight back, so the first
// `worktree` lane of an upgraded deployment reused a relative path and the
// daemon absolutised it against its own CWD — T-I4 차단 ① all over again, on
// the one kind of installation nobody tests on (PR #173 리뷰 NN1).

// seedWorkdirRow reports a directory through §6 and then rewrites the stored
// path, which is what a row written before 0019 looks like today.
func seedWorkdirRow(t *testing.T, f *p2Fixture, d *client, rtID, sessionID, agentID uuid.UUID, stored string) {
	t.Helper()
	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{
			"kind": "worktree", "path": "/Users/x/.colab/worktrees/s/seed", "session_id": sessionID.String(),
			"agent_id": agentID.String(), "bytes": 4096,
		}},
	})
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE workdir SET path_or_ref = $2 WHERE session_id = $1 AND path_or_ref = '/Users/x/.colab/worktrees/s/seed'`,
		sessionID, stored); err != nil {
		t.Fatal(err)
	}
}

// TestP5RelativeWorkdirRowIsNotReusedInTheBundle is S-62.
func TestP5RelativeWorkdirRowIsNotReusedInTheBundle(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)
	seedWorkdirRow(t, f, d, rtID, sessionID, f.leadUUID, "S/lead")

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)

	if b.Workdir.Path == "S/lead" {
		t.Fatalf("the bundle reused the stored RELATIVE path %q — the daemon absolutises that against "+
			"its own CWD and checks the worktree out inside the user's repository (S-62)", b.Workdir.Path)
	}
	if !strings.HasPrefix(b.Workdir.Path, "/") {
		t.Fatalf("workdir.path = %q, want an absolute path (daemon-protocol v0.7.3 §4.1)", b.Workdir.Path)
	}
	want := "/Users/x/.colab/worktrees/s/lead"
	if b.Workdir.Path != want {
		t.Errorf("workdir.path = %q, want %q — the relative row is ignored and the checkout is "+
			"planned afresh from the probe's `workdir_root`", b.Workdir.Path, want)
	}
	if b.Workdir.Reuse {
		t.Errorf("workdir.reuse = true — nothing is being reused, the old directory was not usable")
	}
	// "그 사실을 진단 이벤트로": the ignored directory is still on disk, possibly
	// with uncommitted work in it.
	var notes int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event
		WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'
		  AND object_ref = to_jsonb('workdir.relative'::text)`, taskID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Errorf("the ignored relative row left %d notes on the feed, want exactly 1 (the claim "+
			"long-polls, so it must not redraw the note every second)", notes)
	}
	var detail string
	if err := f.pool.QueryRow(ctx, `
		SELECT payload->>'detail' FROM task_event
		WHERE task_id = $1 AND object_ref = to_jsonb('workdir.relative'::text) LIMIT 1`, taskID).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail, "S/lead") {
		t.Errorf("the note does not name the ignored path: %q", detail)
	}
}

// TestP5AbsoluteWorkdirRowIsStillReused is the other half: the defence must not
// cost FR-6.4/C3 its one-worktree-per-agent reuse.
func TestP5AbsoluteWorkdirRowIsStillReused(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)
	seedWorkdirRow(t, f, d, rtID, sessionID, f.leadUUID, "/Users/x/.colab/worktrees/s/lead-old")

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)
	b := claimBundleOn(t, f, rtID, taskID)

	if b.Workdir.Path != "/Users/x/.colab/worktrees/s/lead-old" {
		t.Errorf("workdir.path = %q, want the agent's existing ABSOLUTE checkout (C3: one worktree "+
			"per agent, reused across its lanes)", b.Workdir.Path)
	}
	if !b.Workdir.Reuse {
		t.Errorf("workdir.reuse = false for an existing checkout")
	}
	var notes int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task_event WHERE task_id = $1
		  AND object_ref = to_jsonb('workdir.relative'::text)`, taskID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 0 {
		t.Errorf("an absolute row raised %d relative-path notes", notes)
	}
}

// ---------------------------------------------------------------------------
// NN2 — the dropped-report note lands on the row's own attempt
// ---------------------------------------------------------------------------

// TestP5DroppedReportNoteLandsOnTheReportedLane is PR #173 리뷰 NN2.
//
// Under `worktree` a session runs several agents at once, and the note used to
// go to "the session's most recent task" — so a warning about the Researcher's
// directory appeared on the Writer's attempt, at the Writer's attempt number,
// which is the one place nobody looking for it would be.
func TestP5DroppedReportNoteLandsOnTheReportedLane(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	// Two agents, two lanes. The second post is the session's most recent task.
	f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사해줘"})
	f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 써줘"})
	rTask := taskOfAgent(t, f, sessionID, f.rUUID)
	wTask := taskOfAgent(t, f, sessionID, f.wUUID)
	if rTask == wTask {
		t.Fatal("fixture: the two posts did not make two tasks")
	}
	var rLane uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT lane_id FROM task WHERE id = $1`, rTask).Scan(&rLane); err != nil {
		t.Fatal(err)
	}

	// A report the server cannot bind, for the RESEARCHER's lane.
	d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/workdirs", map[string]any{
		"workdirs": []map[string]any{{
			"kind": "worktree", "path": "/Users/x/.colab/worktrees/s/r", "session_id": sessionID.String(),
			"lane_id": rLane.String(),
		}},
	})

	var onR, onW int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE task_id = $1), count(*) FILTER (WHERE task_id = $2)
		FROM task_event WHERE class = 'runtime' AND verb = 'error'
		  AND payload->>'detail' LIKE '%§6%'`, rTask, wTask).Scan(&onR, &onW); err != nil {
		t.Fatal(err)
	}
	if onR != 1 {
		t.Errorf("the dropped report left %d notes on the lane it was ABOUT, want 1 (NN2)", onR)
	}
	if onW != 0 {
		t.Errorf("the dropped report left %d notes on an unrelated agent's attempt — that is the "+
			"one place nobody looking for it would be (NN2)", onW)
	}
}

// ---------------------------------------------------------------------------
// NN3 — "agent_id 없음" was three different problems
// ---------------------------------------------------------------------------

// TestP5DroppedReportSaysWhyTheAgentDidNotBind is PR #173 리뷰 NN3: an agent
// that is not a participant, an agent_id that is not a uuid and a missing
// agent_id each ask the Director for something different.
func TestP5DroppedReportSaysWhyTheAgentDidNotBind(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)
	rtID := worktreeSessionOn(t, f, sessionID)
	d := f.daemonFor(t, rtID)

	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 해줘"})
	taskID := firstTaskOf(t, f, sessionID)

	// An agent of this workspace that is NOT in this session (the "다른 세션의
	// 워크트리를 보고했다" case the old message called "agent_id 없음").
	outsider := str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "Outsider", "role": "researcher", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	}), "id")

	url := "/v1/daemon/runtimes/" + rtID.String() + "/workdirs"
	for _, c := range []struct{ name, agent, want string }{
		{"not a participant", outsider, "참가자가 아닙니다"},
		{"not a uuid", "backend", "uuid 가 아닙니다"},
		{"missing", "", "비어 있습니다"},
	} {
		if _, err := f.pool.Exec(ctx, `DELETE FROM task_event WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'`, taskID); err != nil {
			t.Fatal(err)
		}
		body := map[string]any{"kind": "worktree", "path": "/Users/x/.colab/worktrees/s/" + c.name,
			"session_id": sessionID.String()}
		if c.agent != "" {
			body["agent_id"] = c.agent
		}
		d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{body}})

		var detail string
		if err := f.pool.QueryRow(ctx, `
			SELECT payload->>'detail' FROM task_event
			WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'
			ORDER BY seq DESC LIMIT 1`, taskID).Scan(&detail); err != nil {
			t.Fatalf("%s: no note on the feed: %v", c.name, err)
		}
		if !strings.Contains(detail, c.want) {
			t.Errorf("%s: the note says %q, which does not tell the Director %q (NN3)", c.name, detail, c.want)
		}
	}
}

func taskOfAgent(t *testing.T, f *p2Fixture, sessionID, agentID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id FROM task WHERE session_id = $1 AND agent_id = $2 ORDER BY created_at DESC LIMIT 1`,
		sessionID, agentID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
