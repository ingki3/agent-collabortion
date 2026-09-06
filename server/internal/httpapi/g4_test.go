// G4 통합(T-I2)이 실서버에서 격리한 서버 결함 6건 + 계약 모양 위반 1건의 DB 통합
// 테스트 (T-S4). 각 함수는 결함 번호를 이름에 달고 있고, 고친 것만 본다.
//
//	1 listDecisions 가 배열이다 (그리고 다른 배열 op 도 전부)
//	2 listLanes 가 501 이 아니다
//	3 listRuntimeCandidates 가 501 이 아니다
//	4 agent-templates · apply 가 501 이 아니다
//	5 데몬이 보고한 workdir 이 행이 되고 lane.workdir_id 를 채운다
//	6 probe 의 colab_cli 가 저장되고 Runtime · RuntimeDetail 에 실린다
//	7 respond_to: nobody 를 멘션하면 agent_disabled 경고가 나온다
package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// foreignSession builds a second workspace with its own session, so a boundary
// check has something real on the other side of the fence.
func foreignSession(t *testing.T, f *p2Fixture) string {
	t.Helper()
	other := &client{t: t, srv: f.api.srv}
	_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "Other", "email": "other@example.com", "password": "password123"})
	other.cookie = hdr.Get("Set-Cookie")
	ws := str(other.must(201, "POST", f.p+"/workspaces", map[string]any{"name": "Other"}), "id")
	testdb.AddRuntime(t, f.pool, mustUUID(t, ws), "other-mac", t0)
	a := str(other.must(201, "POST", f.p+"/workspaces/"+ws+"/agents", map[string]any{
		"name": "O", "role": "lead", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	}), "id")
	return str(other.must(201, "POST", f.p+"/workspaces/"+ws+"/sessions", map[string]any{
		"title": "S", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": a, "participants": []map[string]any{{"agent_id": a}},
	}), "id")
}

// g4 is a workspace with a really paired daemon: 결함 5·6 are about what the
// daemon reports, so a seeded runtime row would test the wrong thing.
type g4Fixture struct {
	*p2Fixture
	daemon    *client
	runtimeID string
}

func newG4Fixture(t *testing.T) *g4Fixture {
	t.Helper()
	f := newP2Fixture(t)
	pairing := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/runtimes/pairings", map[string]any{"name": "laptop"})
	d := &client{t: t, srv: httptest.NewServer(f.srv.Handler())}
	t.Cleanup(func() { d.srv.Close() })
	paired := d.must(201, "POST", "/v1/daemon/pair", map[string]any{
		"pairing_code": str(pairing, "pairing_token"), "hostname": "mac.local", "os": "darwin", "daemon_version": "0.9",
	})
	d.bearer = str(paired, "daemon_token")
	return &g4Fixture{p2Fixture: f, daemon: d, runtimeID: str(paired, "runtime_id")}
}

func (g *g4Fixture) probe(t *testing.T, p contracts.Probe) {
	t.Helper()
	g.daemon.must(200, "POST", "/v1/daemon/runtimes/"+g.runtimeID+"/probe", p)
}

func fullProbe(cli contracts.ColabCLI, repos ...contracts.Repo) contracts.Probe {
	return contracts.Probe{
		DaemonVersion: "0.9", Hostname: "mac.local", WorkdirRoot: "/tmp/colab",
		Capabilities: []contracts.Capability{{
			Kind: contracts.RuntimeClaudeCode, Version: "2.1", LoggedIn: true,
			Models: []string{"claude-sonnet-5", "claude-opus-5"}, ProtocolVersion: 1,
			BriefTransport: contracts.BriefACPMetaSystemPrompt,
		}},
		Repos: repos, ColabCLI: cli,
	}
}

// ---------------------------------------------------------------------------
// 결함 1 — 계약이 `type: array` 라고 말한 응답은 전부 배열이다
// ---------------------------------------------------------------------------

// TestG4ListShapes walks every openapi list operation whose 200 schema is
// `type: array` and asserts the body really is one. listDecisions was the one
// that wrapped its rows in {"items": …}; the sweep is here so the next one
// cannot land quietly — a wrapped array fails to unmarshal into []any.
func TestG4ListShapes(t *testing.T) {
	f := newG4Fixture(t)
	// recordDecision is TaskToken-only, so the row that makes listDecisions
	// non-empty is written by an agent.
	tok, _ := f.agentToken(t, f.sessionID, f.rUUID, "R")
	if st, out := f.rawPost(t, f.p+"/sessions/"+f.sessionID+"/decisions", tok, map[string]any{"summary": "결정"}); st != 201 {
		t.Fatalf("agent decision = %d %v", st, out)
	}

	for _, path := range []string{
		"/workspaces",
		"/workspaces/" + f.wsID + "/invites",
		"/workspaces/" + f.wsID + "/runtimes",
		"/workspaces/" + f.wsID + "/agent-templates",
		"/sessions/" + f.sessionID + "/artifacts",
		"/sessions/" + f.sessionID + "/decisions",
		"/sessions/" + f.sessionID + "/lanes",
	} {
		if got := f.api.mustList(200, "GET", f.p+path, nil); got == nil {
			t.Fatalf("GET %s: array op answered null, not []", path)
		}
	}
	// The paged ones stay objects — Page + items is also the contract.
	for _, path := range []string{
		"/workspaces/" + f.wsID + "/agents",
		"/workspaces/" + f.wsID + "/members",
		"/sessions/" + f.sessionID + "/messages",
	} {
		if _, ok := f.api.must(200, "GET", f.p+path, nil)["items"]; !ok {
			t.Fatalf("GET %s: paged op must keep {items, next_cursor}", path)
		}
	}
}

// ---------------------------------------------------------------------------
// 결함 2 — listLanes (S7 좌열)
// ---------------------------------------------------------------------------

func TestG4ListLanes(t *testing.T) {
	f := newG4Fixture(t)
	ctx := t.Context()

	base := len(f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil))
	f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사"})
	f.post(t, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 초안", "new_lane": true})

	lanes := f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil)
	if len(lanes) != base+2 {
		t.Fatalf("lanes = %d, want %d", len(lanes), base+2)
	}
	first := lanes[0].(map[string]any)
	// The seven-state board needs these four to draw a card; every one of them
	// is in Lane's `required` list, so a missing key is a contract break.
	for _, k := range []string{"status", "reentry_count", "blocked_message_id", "workdir_id", "agent_id", "depends_on", "actions"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("lane card is missing %q: %v", k, first)
		}
	}
	if str(first, "agent_name") == "" {
		t.Fatal("lane card needs agent_name (S7 draws the agent, not a uuid)")
	}
	// The Director may cancel a live lane; the actions list is per caller.
	if acts, _ := first["actions"].([]any); len(acts) == 0 || acts[0].(string) != "cancel" {
		t.Fatalf("director's actions = %v, want [cancel]", first["actions"])
	}

	// status filter (form/explode=false → comma separated)
	if n := len(f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes?status=done", nil)); n != 0 {
		t.Fatalf("status=done = %d, want 0", n)
	}
	if n := len(f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes?status=queued,running", nil)); n != base+2 {
		t.Fatalf("status=queued,running = %d, want %d", n, base+2)
	}
	if st, out, _ := f.api.do("GET", f.p+"/sessions/"+f.sessionID+"/lanes?status=bogus", nil); st != 422 {
		t.Fatalf("unknown status = %d %v, want 422", st, out)
	}

	// A blocked lane must carry the question's message id — the card links it.
	var laneID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM lane WHERE session_id = $1 ORDER BY created_at LIMIT 1`, f.sessionID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	var msgID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM message WHERE session_id = $1 ORDER BY created_at LIMIT 1`, f.sessionID).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE lane SET status = 'blocked', blocked_note = '국내만인가요?', blocked_message_id = $2, reentry_count = 2 WHERE id = $1`, laneID, msgID); err != nil {
		t.Fatal(err)
	}
	blocked := f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes?status=blocked", nil)
	if len(blocked) != 1 {
		t.Fatalf("blocked lanes = %d, want 1", len(blocked))
	}
	b := blocked[0].(map[string]any)
	if str(b, "blocked_message_id") != msgID.String() || str(b, "blocked_note") == "" {
		t.Fatalf("blocked card = %v", b)
	}
	if n, _ := b["reentry_count"].(float64); n != 2 {
		t.Fatalf("reentry_count = %v, want 2", b["reentry_count"])
	}

	// Another workspace's member never learns the session exists.
	other := &client{t: t, srv: f.api.srv}
	_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "B", "email": "b@example.com", "password": "password123"})
	other.cookie = hdr.Get("Set-Cookie")
	if st, _, _ := other.do("GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil); st != 404 {
		t.Fatalf("outsider's lane board = %d, want 404", st)
	}
}

// ---------------------------------------------------------------------------
// 결함 3 — listRuntimeCandidates (S6 4단계)
// ---------------------------------------------------------------------------

func TestG4RuntimeCandidates(t *testing.T) {
	f := newG4Fixture(t)
	const repo = "git@github.com:acme/app.git"
	f.probe(t, fullProbe(contracts.ColabCLI{Present: true, Version: "0.9"},
		contracts.Repo{Path: "/Users/me/app", RemoteURL: repo, Branch: "main", Clean: true}))

	// `none`: every online runtime, and 자동 선택 allowed (SCREEN §4.4 4행).
	out := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/runtime-candidates?isolation=none", nil)
	if out["auto_select_allowed"] != true {
		t.Fatalf("none auto_select_allowed = %v, want true", out["auto_select_allowed"])
	}
	cands := out["candidates"].([]any)
	if len(cands) != 2 { // the paired one + newP2Fixture's seeded machine
		t.Fatalf("none candidates = %d, want 2", len(cands))
	}
	for _, c := range cands {
		if c.(map[string]any)["eligible"] != true {
			t.Fatalf("none must accept every online runtime: %v", c)
		}
	}

	// `worktree`: only the machine that has a clone of the SAME remote, and
	// 자동 선택 is off. The seeded runtime has no repos at all.
	out = f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/runtime-candidates?isolation=worktree&remote_url="+
		"https%3A%2F%2Fgithub.com%2Facme%2Fapp", nil)
	if out["auto_select_allowed"] != false {
		t.Fatal("worktree must disable 자동 선택 (FR-2.1 M10)")
	}
	eligible, ineligible := 0, 0
	for _, raw := range out["candidates"].([]any) {
		c := raw.(map[string]any)
		if c["eligible"] == true {
			eligible++
			// The URL form differs (scp vs https) but it is the same repo —
			// E14-04·05 is exactly this comparison.
			if c["matched_repo"] == nil {
				t.Fatalf("an eligible worktree candidate must name the repo it matched: %v", c)
			}
			continue
		}
		ineligible++
		if str(c, "reason") == "" {
			t.Fatalf("an ineligible candidate must say why (S6 draws it disabled + 사유): %v", c)
		}
	}
	if eligible != 1 || ineligible != 1 {
		t.Fatalf("worktree eligible/ineligible = %d/%d, want 1/1", eligible, ineligible)
	}

	// A different repository matches nothing.
	out = f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/runtime-candidates?isolation=worktree&remote_url=git%40github.com%3Aacme%2Fother.git", nil)
	for _, raw := range out["candidates"].([]any) {
		if raw.(map[string]any)["eligible"] == true {
			t.Fatalf("path-insensitive match must not become match-anything: %v", raw)
		}
	}

	// Offline machines are listed, not hidden — the reason is the point.
	if _, err := f.pool.Exec(t.Context(), `UPDATE runtime SET status = 'offline' WHERE workspace_id = $1`, f.wsID); err != nil {
		t.Fatal(err)
	}
	out = f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/runtime-candidates?isolation=none", nil)
	for _, raw := range out["candidates"].([]any) {
		c := raw.(map[string]any)
		if c["eligible"] == true || str(c, "reason") == "" {
			t.Fatalf("offline candidate = %v, want eligible:false + reason", c)
		}
	}

	// worktree without a repository to compare against is a 422, not an empty
	// list: "후보 0" and "당신이 저장소를 안 골랐다" are different answers.
	if st, _, _ := f.api.do("GET", f.p+"/workspaces/"+f.wsID+"/runtime-candidates?isolation=worktree", nil); st != 422 {
		t.Fatalf("worktree without remote_url = %d, want 422", st)
	}
	if st, _, _ := f.api.do("GET", f.p+"/workspaces/"+f.wsID+"/runtime-candidates?isolation=bogus", nil); st != 422 {
		t.Fatal("unknown isolation must be 422")
	}
}

// ---------------------------------------------------------------------------
// 결함 4 — agent-templates · apply (FR-1.4)
// ---------------------------------------------------------------------------

func TestG4AgentTemplates(t *testing.T) {
	f := newG4Fixture(t)

	// Before any probe there is no advertised runtime: the teams still list,
	// every agent unmapped with a reason (that is what S9 shows).
	cold := f.api.mustList(200, "GET", f.p+"/workspaces/"+f.wsID+"/agent-templates", nil)
	if len(cold) != 3 {
		t.Fatalf("templates = %d, want 3 (리서치·개발·콘텐츠)", len(cold))
	}
	keys := map[string]bool{}
	for _, raw := range cold {
		tpl := raw.(map[string]any)
		keys[str(tpl, "key")] = true
		for _, a := range tpl["agents"].([]any) {
			m := a.(map[string]any)["mapping"].(map[string]any)
			if str(m, "status") != "unmapped" || str(m, "reason") == "" {
				t.Fatalf("with no runtime every agent is unmapped + 사유: %v", m)
			}
		}
	}
	for _, k := range []string{"research_team", "dev_team", "content_team"} {
		if !keys[k] {
			t.Fatalf("template %s missing: %v", k, keys)
		}
	}

	f.probe(t, fullProbe(contracts.ColabCLI{Present: true, Version: "0.9"}))
	warm := f.api.mustList(200, "GET", f.p+"/workspaces/"+f.wsID+"/agent-templates", nil)
	for _, raw := range warm {
		for _, a := range raw.(map[string]any)["agents"].([]any) {
			m := a.(map[string]any)["mapping"].(map[string]any)
			if str(m, "status") != "mapped" || str(m, "model") == "" {
				t.Fatalf("a logged-in claude_code runtime maps every template agent: %v", m)
			}
		}
	}

	// apply — the P2 "3분 이내" path.
	out := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agent-templates/dev_team/apply", map[string]any{})
	made := out["agents"].([]any)
	if len(made) != 3 {
		t.Fatalf("dev_team = %d agents, want 3", len(made))
	}
	if n := len(out["unmapped"].([]any)); n != 0 {
		t.Fatalf("unmapped = %d, want 0 with a probed runtime", n)
	}
	for _, raw := range made {
		a := raw.(map[string]any)
		if str(a, "definition_source") != "dev_team" {
			t.Fatalf("definition_source = %q, want dev_team (FR-1.8)", str(a, "definition_source"))
		}
		profiles, _ := a["profiles"].([]any)
		if len(profiles) != 1 {
			t.Fatalf("%s got %d profiles, want 1 default", str(a, "name"), len(profiles))
		}
		// The Reviewer prefers Hermes; this workspace only advertises Claude
		// Code, so it is mapped there rather than left profile-less.
		if str(profiles[0].(map[string]any), "runtime_kind") != "claude_code" {
			t.Fatalf("%s mapped to %v", str(a, "name"), profiles[0])
		}
	}

	// Applying twice must not fail halfway: names get a suffix.
	again := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agent-templates/dev_team/apply", map[string]any{})
	if len(again["agents"].([]any)) != 3 {
		t.Fatalf("second apply = %v", again["agents"])
	}
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM agent WHERE workspace_id = $1 AND definition_source = 'dev_team'`, f.wsID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("agents from dev_team = %d, want 6 after two applies", n)
	}

	// name_overrides is the contract's way to avoid the collision up front.
	named := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agent-templates/research_team/apply",
		map[string]any{"name_overrides": map[string]string{"lead": "연구총괄"}})
	if str(named["agents"].([]any)[0].(map[string]any), "name") != "연구총괄" {
		t.Fatalf("name_overrides ignored: %v", named["agents"])
	}

	if st, _, _ := f.api.do("POST", f.p+"/workspaces/"+f.wsID+"/agent-templates/no_such_team/apply", map[string]any{}); st != 404 && st != 422 {
		t.Fatalf("unknown template = %d, want 404/422", st)
	}
}

// ---------------------------------------------------------------------------
// 결함 5 — 데몬이 보고한 workdir 이 행이 된다 (§6, FR-6.1/6.4)
// ---------------------------------------------------------------------------

func TestG4WorkdirRows(t *testing.T) {
	f := newG4Fixture(t)
	ctx := t.Context()
	f.probe(t, fullProbe(contracts.ColabCLI{Present: true, Version: "0.9"}))

	f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사"})
	var laneID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM lane WHERE session_id = $1 ORDER BY created_at LIMIT 1`, f.sessionID).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	path := "/tmp/colab/sessions/" + f.sessionID + "/" + laneID.String()
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/workdirs", map[string]any{
		"workdirs": []any{map[string]any{
			"kind": "dir", "path": path, "session_id": f.sessionID, "lane_id": laneID.String(),
			"bytes": 4096, "last_used_at": t0,
		}},
	})

	var wdID uuid.UUID
	var kind string
	var bytes int64
	if err := f.pool.QueryRow(ctx, `SELECT id, kind::text, disk_bytes FROM workdir WHERE session_id = $1 AND path_or_ref = $2`, f.sessionID, path).
		Scan(&wdID, &kind, &bytes); err != nil {
		t.Fatalf("the reported workdir must become a row (FR-6.4): %v", err)
	}
	if kind != "dir" || bytes != 4096 {
		t.Fatalf("workdir row = kind %s, %d bytes", kind, bytes)
	}
	var bound *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT workdir_id FROM lane WHERE id = $1`, laneID).Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound == nil || *bound != wdID {
		t.Fatalf("lane.workdir_id = %v, want %v — S7 cannot show a lane's workdir otherwise", bound, wdID)
	}
	// And the API says so, which is where the defect was visible.
	board := f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil)
	if got := str(board[0].(map[string]any), "workdir_id"); got != wdID.String() {
		t.Fatalf("lane.workdir_id over the API = %q, want %s", got, wdID)
	}

	// Re-reporting the same directory updates it; it does not pile up rows —
	// the daemon repeats the whole list on every probe and carries no row id.
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/workdirs", map[string]any{
		"workdirs": []any{map[string]any{
			"kind": "dir", "path": path, "session_id": f.sessionID, "lane_id": laneID.String(),
			"bytes": 8192, "last_used_at": t0,
			"git": map[string]any{"branch": "colab/r-1", "dirty": true, "merged": false, "commits_ahead": 2},
		}},
	})
	var rows int
	var dirty *bool
	var branch *string
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, f.sessionID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("workdir rows after a second report = %d, want 1", rows)
	}
	if err := f.pool.QueryRow(ctx, `SELECT dirty, branch FROM workdir WHERE id = $1`, wdID).Scan(&dirty, &branch); err != nil {
		t.Fatal(err)
	}
	if dirty == nil || !*dirty || branch == nil || *branch != "colab/r-1" {
		t.Fatalf("git report must land on the row: dirty=%v branch=%v", dirty, branch)
	}

	// A report about somebody else's session writes nothing (the ids come off
	// the daemon's own disk — they are input, not authority).
	foreign := foreignSession(t, f.p2Fixture)
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/workdirs", map[string]any{
		"workdirs": []any{map[string]any{"kind": "dir", "path": "/tmp/x", "session_id": foreign, "lane_id": laneID.String(), "bytes": 1}},
	})
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workdir WHERE session_id = $1`, foreign).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("a daemon wrote %d workdir rows into another workspace's session", rows)
	}
}

// ---------------------------------------------------------------------------
// 결함 6 — probe.colab_cli (daemon-protocol §3 v0.5)
// ---------------------------------------------------------------------------

func TestG4ProbeColabCLI(t *testing.T) {
	f := newG4Fixture(t)

	f.probe(t, fullProbe(contracts.ColabCLI{Present: true, Version: "0.9.3"}))
	list := f.api.mustList(200, "GET", f.p+"/workspaces/"+f.wsID+"/runtimes", nil)
	var seen bool
	for _, raw := range list {
		rt := raw.(map[string]any)
		if rt["id"] != f.runtimeID {
			continue
		}
		seen = true
		cli, ok := rt["colab_cli"].(map[string]any)
		if !ok {
			t.Fatalf("Runtime.colab_cli missing — S11·S12 warn on it: %v", rt)
		}
		if cli["present"] != true || cli["version"] != "0.9.3" {
			t.Fatalf("colab_cli = %v, want {present:true, version:0.9.3}", cli)
		}
	}
	if !seen {
		t.Fatal("paired runtime missing from the list")
	}

	detail := f.api.must(200, "GET", f.p+"/runtimes/"+f.runtimeID, nil)
	if cli, _ := detail["colab_cli"].(map[string]any); cli == nil || cli["present"] != true {
		t.Fatalf("RuntimeDetail.colab_cli = %v", detail["colab_cli"])
	}

	// A machine without the CLI reports it as absent, and the value survives —
	// "present: false" is the warning, not the absence of the field.
	f.probe(t, fullProbe(contracts.ColabCLI{Present: false, Version: ""}))
	detail = f.api.must(200, "GET", f.p+"/runtimes/"+f.runtimeID, nil)
	cli, _ := detail["colab_cli"].(map[string]any)
	if cli == nil || cli["present"] != false || cli["version"] != "" {
		t.Fatalf("colab_cli after a CLI-less probe = %v, want {present:false, version:\"\"}", detail["colab_cli"])
	}
}

// ---------------------------------------------------------------------------
// 결함 7 — respond_to: nobody 를 멘션하면 agent_disabled 경고 (colab-cli §2.2)
// ---------------------------------------------------------------------------

func TestG4AgentDisabledWarning(t *testing.T) {
	f := newG4Fixture(t)
	f.api.must(200, "PATCH", f.p+"/agents/"+f.r, map[string]any{"respond_to": "nobody"})

	body := map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사해줘"}
	pv := f.preview(t, body)
	if n := len(pv["triggers"].([]any)); n != 0 {
		t.Fatalf("preview triggered %d tasks for a killed agent, want 0 (FR-1.9 M8)", n)
	}
	codes := warningCodes(pv)
	if !codes["agent_disabled"] {
		t.Fatalf("preview warnings = %v, want agent_disabled", pv["warnings"])
	}

	out := f.post(t, body)
	if n := len(out["triggers"].([]any)); n != 0 {
		t.Fatalf("post triggered %d tasks for a killed agent, want 0", n)
	}
	if !warningCodes(out)["agent_disabled"] {
		t.Fatalf("post warnings = %v, want agent_disabled", out["warnings"])
	}
	// The message itself is still posted — the kill switch stops the trigger,
	// not the conversation.
	if out["message"] == nil {
		t.Fatal("the message must still be posted")
	}

	// Turning it back on restores the trigger (PRD FR-1.9: "다시 활성화하면
	// 그때부터 정상 동작한다").
	f.api.must(200, "PATCH", f.p+"/agents/"+f.r, map[string]any{"respond_to": "owner"})
	if n := len(f.preview(t, body)["triggers"].([]any)); n != 1 {
		t.Fatalf("re-enabled agent triggers = %d, want 1", n)
	}
}

func warningCodes(out map[string]any) map[string]bool {
	got := map[string]bool{}
	ws, _ := out["warnings"].([]any)
	for _, raw := range ws {
		got[str(raw.(map[string]any), "code")] = true
	}
	return got
}
