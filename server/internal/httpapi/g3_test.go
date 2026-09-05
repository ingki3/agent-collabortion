package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// G3 integration defects (plan/G3_REPORT.md §4 S-2·S-4·S-5) over HTTP:
//   - S-2 cancelLane: Director/deputy only (403), running attempt → daemon
//     cancel command until finish(cancelled) → task cancelled(failure_kind
//     cancelled), lane failed, 409 afterwards (E10-04)
//   - S-4 re-pairing the same hostname → 201 with a "-2" suffix (E11-12)
//   - S-5 invite.url on COLAB_WEB_URL, install_commands on COLAB_SERVER_URL
func TestG3ServerFixes(t *testing.T) {
	pool := testdb.New(t)
	fake := clock.NewFake(t0)
	s := NewServer(Deps{DB: pool, Clock: fake, ServerURL: "http://colab.test", WebURL: "http://web.test:3000"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	api := &client{t: t, srv: ts}
	const p = "/api/v1"

	_, _, hdr := api.do("POST", p+"/auth/signup", map[string]any{"display_name": "Dir", "email": "dir@example.com", "password": "password123"})
	api.cookie = hdr.Get("Set-Cookie")
	wsID := str(api.must(201, "POST", p+"/workspaces", map[string]any{"name": "Acme"}), "id")

	// --- S-5: invite links open the web UI, install commands hit the server ---
	inv := api.must(201, "POST", p+"/workspaces/"+wsID+"/invites", map[string]any{})
	if !strings.HasPrefix(str(inv, "url"), "http://web.test:3000/invite/") {
		t.Fatalf("invite url = %q, want COLAB_WEB_URL origin (S-5)", str(inv, "url"))
	}
	pairing := api.must(201, "POST", p+"/workspaces/"+wsID+"/runtimes/pairings", map[string]any{"name": "laptop"})
	for _, c := range pairing["install_commands"].([]any) {
		if !strings.Contains(c.(string), "http://colab.test") || strings.Contains(c.(string), "web.test") {
			t.Fatalf("install command %q must use the server URL, not the web URL", c)
		}
	}

	// --- S-4: the same hostname paired again into the same workspace ---
	daemon := &client{t: t, srv: ts}
	pair := func(code, host string) (string, string) {
		out := daemon.must(201, "POST", "/v1/daemon/pair", map[string]any{"pairing_code": code, "hostname": host, "os": "darwin", "daemon_version": "0.1"})
		return str(out, "runtime_id"), str(out, "daemon_token")
	}
	runtimeID, daemonToken := pair(str(pairing, "pairing_token"), "mac.local")
	daemon.bearer = daemonToken
	daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/probe", contracts.Probe{
		DaemonVersion: "0.1", Hostname: "mac.local", WorkdirRoot: "/tmp/colab",
		Capabilities: []contracts.Capability{{Kind: contracts.RuntimeClaudeCode, Version: "2.1", LoggedIn: true, Models: []string{"claude-sonnet-5"}, ProtocolVersion: 1, BriefTransport: contracts.BriefACPMetaSystemPrompt}},
	})
	nameOf := func(id string) string {
		var name string
		if err := pool.QueryRow(t.Context(), `SELECT name FROM runtime WHERE id = $1`, id).Scan(&name); err != nil {
			t.Fatal(err)
		}
		return name
	}
	if nameOf(runtimeID) != "mac.local" {
		t.Fatalf("first runtime name = %q", nameOf(runtimeID))
	}
	second, _ := pair(str(api.must(201, "POST", p+"/workspaces/"+wsID+"/runtimes/pairings", map[string]any{}), "pairing_token"), "mac.local")
	third, _ := pair(str(api.must(201, "POST", p+"/workspaces/"+wsID+"/runtimes/pairings", map[string]any{}), "pairing_token"), "mac.local")
	if nameOf(second) != "mac.local-2" || nameOf(third) != "mac.local-3" {
		t.Fatalf("re-paired names = %q, %q, want mac.local-2, mac.local-3 (E11-12)", nameOf(second), nameOf(third))
	}
	var runtimeRows int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM runtime WHERE workspace_id = $1`, wsID).Scan(&runtimeRows)
	if runtimeRows != 3 {
		t.Fatalf("runtime rows = %d, want 3 (re-pairing must not 500 — S-4)", runtimeRows)
	}

	// --- S-2: cancelLane ---
	agent := api.must(201, "POST", p+"/workspaces/"+wsID+"/agents", map[string]any{
		"name": "Lead", "role": "lead", "role_description": "coordinates", "instructions": "be helpful",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	})
	agentID := str(agent, "id")
	sess := api.must(201, "POST", p+"/workspaces/"+wsID+"/sessions", map[string]any{
		"title": "Cancel", "goal": "sleep", "isolation": map[string]any{"kind": "none"}, "runtime_id": runtimeID,
		"participants": []map[string]any{{"agent_id": agentID}},
	})
	sessionID := str(sess, "id")
	post := api.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": router.MentionLink("Lead", mustUUID(t, agentID)) + " sleep 120"}, "Idempotency-Key", "33333333-3333-4333-8333-333333333333")
	tr := post["triggers"].([]any)[0].(map[string]any)
	taskID, laneID := str(tr, "task_id"), str(tr, "lane_id")

	// Another member (not Director/deputy) is 403; a stranger sees 404.
	stranger := &client{t: t, srv: ts}
	_, _, sh := stranger.do("POST", p+"/auth/signup", map[string]any{"display_name": "Other", "email": "other@example.com", "password": "password123"})
	stranger.cookie = sh.Get("Set-Cookie")
	if st, out, _ := stranger.do("POST", p+"/lanes/"+laneID+"/cancel", nil); st != 404 {
		t.Fatalf("stranger cancel = %d %v, want 404", st, out)
	}
	memberID := str(stranger.must(200, "GET", p+"/me", nil)["user"].(map[string]any), "id")
	if _, err := pool.Exec(t.Context(), `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'member', $3)`, wsID, memberID, t0); err != nil {
		t.Fatal(err)
	}
	if st, out, _ := stranger.do("POST", p+"/lanes/"+laneID+"/cancel", nil); st != 403 || str(out, "code") != "director_required" {
		t.Fatalf("member cancel = %d %v, want 403 director_required", st, out)
	}

	// claim → running, then the Director cancels.
	claim := daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	if len(claim["tasks"].([]any)) != 1 {
		t.Fatalf("claim = %v", claim)
	}
	attemptPath := "/v1/daemon/tasks/" + taskID + "/attempts/1"
	daemon.must(200, "POST", attemptPath+"/phase", map[string]any{"phase": "preparing", "pgid": 100})
	daemon.must(200, "POST", attemptPath+"/phase", map[string]any{"phase": "running", "pgid": 100})

	lane := api.must(202, "POST", p+"/lanes/"+laneID+"/cancel", nil)
	if str(lane, "status") != "running" || str(lane["current_task"].(map[string]any), "id") != taskID || lane["actions"] == nil {
		t.Fatalf("cancel response = %v, want running lane with current_task", lane)
	}
	hasCancel := func(cmds any) bool {
		for _, c := range cmds.([]any) {
			m := c.(map[string]any)
			if str(m, "type") == "cancel" && str(m, "task_id") == taskID && m["attempt"].(float64) == 1 && m["after_current_tool"] == true && str(m, "reason") == "director" {
				return true
			}
		}
		return false
	}
	hb := daemon.must(200, "POST", attemptPath+"/heartbeat", map[string]any{"usage": map[string]any{}, "last_seq": 0})
	if !hasCancel(hb["commands"]) {
		t.Fatalf("heartbeat commands = %v, want cancel{after_current_tool, director} (§4.3)", hb["commands"])
	}
	again := daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	if !hasCancel(again["commands"]) {
		t.Fatalf("cancel must ride every response until finish: %v", again["commands"])
	}
	if str(api.must(200, "GET", p+"/tasks/"+taskID, nil), "status") != "running" {
		t.Fatal("task must stay running until the daemon's finish")
	}

	fin := daemon.must(200, "POST", attemptPath+"/finish", contracts.Finish{Outcome: "cancelled", StopReason: "cancelled"})
	if str(fin, "status") != "cancelled" {
		t.Fatalf("finish = %v", fin)
	}
	task := api.must(200, "GET", p+"/tasks/"+taskID, nil)
	if str(task, "status") != "cancelled" || str(task, "failure_kind") != "cancelled" || task["attempt"].(float64) != 1 {
		t.Fatalf("task after cancel = %v, want cancelled/failure_kind cancelled/attempt 1 (E10-04)", task)
	}
	after := daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	if len(after["tasks"].([]any)) != 0 || hasCancel(after["commands"]) {
		t.Fatalf("after finish: tasks %v commands %v, want no requeue and cancel consumed", after["tasks"], after["commands"])
	}
	feed := api.must(200, "GET", p+"/tasks/"+taskID+"/events", nil)
	noted := false
	for _, it := range feed["items"].([]any) {
		m := it.(map[string]any)
		if pl, ok := m["payload"].(map[string]any); ok && pl["note"] == "사람이 중단함" && str(m, "class") == "status" {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("feed must record 사람이 중단함 (E10-04): %v", feed["items"])
	}
	if st, out, _ := api.do("POST", p+"/lanes/"+laneID+"/cancel", nil); st != 409 || str(out, "code") != "lane_not_cancellable" {
		t.Fatalf("cancel on failed lane = %d %v, want 409", st, out)
	}
	var laneUpdated int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM stream_event WHERE workspace_id = $1 AND type = 'lane.updated'`, wsID).Scan(&laneUpdated)
	if laneUpdated < 2 {
		t.Fatalf("lane.updated stream events = %d, want ≥ 2 (cancel request + finish)", laneUpdated)
	}
	// restartLane ("중단하고 다시 지시") stays P2.
	if st, _, _ := api.do("POST", p+"/lanes/"+laneID+"/restart", map[string]any{"content": "again"}, "Idempotency-Key", "44444444-4444-4444-8444-444444444444"); st != 501 {
		t.Fatalf("restartLane = %d, want 501", st)
	}
}
