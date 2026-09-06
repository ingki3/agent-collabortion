package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

var t0 = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

type client struct {
	t      *testing.T
	srv    *httptest.Server
	cookie string
	bearer string
}

func (c *client) raw(method, path string, body any, headers ...string) (int, []byte, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, raw, res.Header
}

func (c *client) do(method, path string, body any, headers ...string) (int, map[string]any, http.Header) {
	c.t.Helper()
	st, raw, h := c.raw(method, path, body, headers...)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return st, out, h
}

// mustList is must for the contract's bare-array responses — listDecisions,
// listArtifacts, listLanes, listAgentTemplates, listRuntimes, listWorkspaces,
// listInvites. It decodes into []any, so a handler that wraps the array in
// {"items": …} fails here instead of shipping (G4 결함 1: that wrapper killed
// the whole S7 screen with `props.decisions.map is not a function`).
func (c *client) mustList(status int, method, path string, body any, headers ...string) []any {
	c.t.Helper()
	st, raw, _ := c.raw(method, path, body, headers...)
	if st != status {
		c.t.Fatalf("%s %s = %d, want %d: %s", method, path, st, status, raw)
	}
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		c.t.Fatalf("%s %s: response is not a JSON array (contract says type: array): %s", method, path, raw)
	}
	return out
}

func (c *client) must(status int, method, path string, body any, headers ...string) map[string]any {
	c.t.Helper()
	st, out, _ := c.do(method, path, body, headers...)
	if st != status {
		c.t.Fatalf("%s %s = %d, want %d: %v", method, path, st, status, out)
	}
	return out
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

// TestVerticalSlice walks the P1 slice over HTTP: signup → workspace → pairing
// → daemon pair/probe → agent → session → post (idempotent) → claim → phase →
// events (idempotent) → CLI ops with the task token → heartbeat expiry → the
// revoked token gets 401 token_revoked (E11-04) → revoke command → finish.
func TestVerticalSlice(t *testing.T) {
	pool := testdb.New(t)
	fake := clock.NewFake(t0)
	s := NewServer(Deps{DB: pool, Clock: fake, ServerURL: "http://colab.test"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	api := &client{t: t, srv: ts}
	const p = "/api/v1"

	// --- auth + workspace ---
	st, _, hdr := api.do("POST", p+"/auth/signup", map[string]any{"display_name": "Dir", "email": "dir@example.com", "password": "password123"})
	if st != 201 || hdr.Get("Set-Cookie") == "" {
		t.Fatalf("signup = %d, cookie %q", st, hdr.Get("Set-Cookie"))
	}
	api.cookie = hdr.Get("Set-Cookie")
	if st, out, _ := api.do("POST", p+"/auth/login", map[string]any{"email": "dir@example.com", "password": "nope"}); st != 401 || str(out, "code") != "password_mismatch" {
		t.Fatalf("bad login = %d %v", st, out)
	}
	me := api.must(200, "GET", p+"/me", nil)
	if len(me["workspaces"].([]any)) != 0 {
		t.Fatal("new user should have no workspaces")
	}
	ws := api.must(201, "POST", p+"/workspaces", map[string]any{"name": "Acme Team"})
	wsID := str(ws, "id")
	if str(ws, "slug") != "acme-team" {
		t.Fatalf("slug = %s", str(ws, "slug"))
	}
	inv := api.must(201, "POST", p+"/workspaces/"+wsID+"/invites", map[string]any{})
	if str(inv, "status") != "pending" || str(inv, "url") == "" {
		t.Fatalf("invite = %v", inv)
	}
	api.must(200, "GET", p+"/invites/"+str(inv, "token"), nil)

	// no runtime yet → createSession 409 no_runtime
	if st, out, _ := api.do("POST", p+"/workspaces/"+wsID+"/sessions", map[string]any{"title": "S", "goal": "g", "isolation": map[string]any{"kind": "none"}, "participants": []any{}}); st != 422 {
		t.Fatalf("empty participants = %d %v", st, out)
	}

	// --- pairing + daemon ---
	pairing := api.must(201, "POST", p+"/workspaces/"+wsID+"/runtimes/pairings", map[string]any{"name": "laptop"})
	code := str(pairing, "pairing_token")
	if code == "" || len(pairing["install_commands"].([]any)) != 2 {
		t.Fatalf("pairing = %v", pairing)
	}
	daemon := &client{t: t, srv: ts}
	paired := daemon.must(201, "POST", "/v1/daemon/pair", map[string]any{"pairing_code": code, "hostname": "mac.local", "os": "darwin", "daemon_version": "0.1"})
	runtimeID, daemonToken := str(paired, "runtime_id"), str(paired, "daemon_token")
	daemon.bearer = daemonToken
	if st, out, _ := daemon.do("POST", "/v1/daemon/pair", map[string]any{"pairing_code": code}); st != 410 {
		t.Fatalf("reused pairing code = %d %v", st, out)
	}
	if str(api.must(200, "GET", p+"/workspaces/"+wsID+"/runtimes/pairings/"+str(pairing, "id"), nil), "status") != "connected" {
		t.Fatal("pairing should be connected")
	}
	daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/probe", contracts.Probe{
		DaemonVersion: "0.1", Hostname: "mac.local", WorkdirRoot: "/tmp/colab",
		Capabilities: []contracts.Capability{{Kind: contracts.RuntimeClaudeCode, Version: "2.1", LoggedIn: true, Models: []string{"claude-sonnet-5"}, ProtocolVersion: 1, BriefTransport: contracts.BriefACPMetaSystemPrompt}},
	})
	if str(api.must(200, "GET", p+"/workspaces/"+wsID+"/runtimes/pairings/"+str(pairing, "id"), nil), "status") != "ready" {
		t.Fatal("pairing should be ready after probe (E11-08)")
	}
	rts := api.must(200, "GET", p+"/workspaces/"+wsID+"/runtimes", nil)
	_ = rts

	// --- agent ---
	agent := api.must(201, "POST", p+"/workspaces/"+wsID+"/agents", map[string]any{
		"name": "Lead", "role": "lead", "role_description": "coordinates", "instructions": "be helpful",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	})
	agentID := str(agent, "id")
	if str(agent, "status") != "idle" || len(agent["profiles"].([]any)) != 1 {
		t.Fatalf("agent = %v", agent)
	}
	if st, _, _ := api.do("POST", p+"/workspaces/"+wsID+"/agents", map[string]any{"name": "Lead", "role": "lead", "role_description": "x", "instructions": "y",
		"profiles": []map[string]any{{"name": "d", "runtime_kind": "claude_code", "model": "m"}}}); st != 409 {
		t.Fatalf("duplicate agent name = %d, want 409", st)
	}
	other := api.must(201, "POST", p+"/workspaces/"+wsID+"/agents", map[string]any{
		"name": "X", "role": "custom", "role_description": "outsider", "instructions": "n/a",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "hermes", "model": "m"}},
	})

	// --- session ---
	sess := api.must(201, "POST", p+"/workspaces/"+wsID+"/sessions", map[string]any{
		"title": "Hello", "goal": "say hi", "isolation": map[string]any{"kind": "none"},
		"participants": []map[string]any{{"agent_id": agentID}},
	})
	sessionID := str(sess, "id")
	if str(sess, "status") != "active" || str(sess, "my_role") != "director" || sess["runtime_id"] != nil {
		t.Fatalf("session = %v", sess)
	}
	// A runtime paired to another workspace cannot be this session's runtime (FR-2.1 M10).
	foreignRuntime := testdb.AddRuntime(t, pool, testdb.AddWorkspace(t, pool, "other-ws", t0), "mac-b", t0)
	if st, out, _ := api.do("POST", p+"/workspaces/"+wsID+"/sessions", map[string]any{
		"title": "Leak", "goal": "g", "isolation": map[string]any{"kind": "none"}, "runtime_id": foreignRuntime.String(),
		"participants": []map[string]any{{"agent_id": agentID}},
	}); st != 422 || str(out, "code") != "validation_failed" || !hasFieldError(out, "runtime_id", "runtime_not_in_workspace") {
		t.Fatalf("foreign runtime_id = %d %v, want 422 runtime_not_in_workspace", st, out)
	}

	// --- post message: rule 2, idempotent replay, non-participant warning ---
	mention := router.MentionLink("Lead", mustUUID(t, agentID))
	key := "11111111-1111-4111-8111-111111111111"
	post := api.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": mention + " 인사해줘"}, "Idempotency-Key", key)
	trig := post["triggers"].([]any)
	if len(trig) != 1 {
		t.Fatalf("triggers = %v", trig)
	}
	tr := trig[0].(map[string]any)
	// Coalesced into the session's initial (queued) task on the assignee's lane (FR-3.4).
	if tr["coalesced"] != true {
		t.Fatalf("expected coalesce into the initial task, got %v", tr)
	}
	taskID := str(tr, "task_id")
	st, replay, rh := api.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": mention + " 인사해줘"}, "Idempotency-Key", key)
	if st != 201 || rh.Get("Idempotent-Replayed") != "true" || str(replay["message"].(map[string]any), "id") != str(post["message"].(map[string]any), "id") {
		t.Fatalf("replay = %d %s %v", st, rh.Get("Idempotent-Replayed"), replay)
	}
	if st, out, _ := api.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "different"}, "Idempotency-Key", key); st != 422 || str(out, "code") != "idempotency_key_reused" {
		t.Fatalf("reused key = %d %v", st, out)
	}
	if st, out, _ := api.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "x"}); st != 422 {
		t.Fatalf("missing key = %d %v", st, out)
	}
	warn := api.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": router.MentionLink("X", mustUUID(t, str(other, "id"))) + " 도와줘"}, "Idempotency-Key", "22222222-2222-4222-8222-222222222222")
	if len(warn["triggers"].([]any)) != 0 || len(warn["warnings"].([]any)) != 1 {
		t.Fatalf("E1-04: %v", warn)
	}
	msgs := api.must(200, "GET", p+"/sessions/"+sessionID+"/messages", nil)
	if n := len(msgs["items"].([]any)); n != 3 { // system start + 2 user posts
		t.Fatalf("messages = %d, want 3", n)
	}

	// --- claim ---
	claim := daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 2, "wait_ms": 0})
	bundles := claim["tasks"].([]any)
	if len(bundles) != 1 {
		t.Fatalf("claim = %v", claim)
	}
	bundle := bundles[0].(map[string]any)
	taskToken := str(bundle, "task_token")
	btask := bundle["task"].(map[string]any)
	if str(btask, "id") != taskID || btask["attempt"].(float64) != 1 || taskToken == "" {
		t.Fatalf("bundle task = %v", btask)
	}
	if str(bundle, "prompt") == "" || str(bundle["brief"].(map[string]any), "text") == "" {
		t.Fatal("bundle must carry prompt and brief")
	}
	sess = api.must(200, "GET", p+"/sessions/"+sessionID, nil)
	if str(sess, "runtime_id") != runtimeID {
		t.Fatalf("session runtime not fixed on first claim (E11-10): %v", sess["runtime_id"])
	}
	if len(daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 2, "wait_ms": 0})["tasks"].([]any)) != 0 {
		t.Fatal("second claim must be empty")
	}

	// --- phase, events (idempotent), heartbeat ---
	attemptPath := "/v1/daemon/tasks/" + taskID + "/attempts/1"
	daemon.must(200, "POST", attemptPath+"/phase", map[string]any{"phase": "preparing", "pgid": 100})
	daemon.must(200, "POST", attemptPath+"/phase", map[string]any{"phase": "running", "pgid": 100})
	ev := func(seq int, verb string) map[string]any {
		return map[string]any{"task_id": taskID, "attempt": 1, "seq": seq, "ts": fake.Now().Format(time.RFC3339), "class": "message", "verb": verb, "outcome": "ok", "payload": map[string]any{"kind": "text", "text": "hi"}}
	}
	batch := map[string]any{"events": []any{ev(1, "say"), ev(2, "think")}}
	acc := daemon.must(200, "POST", attemptPath+"/events", batch)
	if acc["accepted_seq_max"].(float64) != 2 {
		t.Fatalf("accepted_seq_max = %v", acc["accepted_seq_max"])
	}
	acc = daemon.must(200, "POST", attemptPath+"/events", map[string]any{"events": []any{ev(2, "think"), ev(3, "say")}}) // resend seq 2
	if acc["accepted_seq_max"].(float64) != 3 {
		t.Fatalf("accepted_seq_max after resend = %v", acc["accepted_seq_max"])
	}
	var nEvents int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM task_event WHERE task_id = $1 AND seq < 1000000`, taskID).Scan(&nEvents)
	if nEvents != 3 {
		t.Fatalf("task_event rows = %d, want 3 (E8-04 idempotent seq)", nEvents)
	}
	if st, out, _ := daemon.do("POST", attemptPath+"/events", map[string]any{"events": []any{map[string]any{"attempt": 1, "seq": 4, "ts": fake.Now(), "class": "bogus", "verb": "say", "outcome": "ok"}}}); st != 422 {
		t.Fatalf("invalid event = %d %v", st, out)
	}
	daemon.must(200, "POST", attemptPath+"/heartbeat", map[string]any{"usage": map[string]any{}, "last_seq": 3})
	feed := api.must(200, "GET", p+"/tasks/"+taskID+"/events", nil)
	if len(feed["items"].([]any)) != 3 {
		t.Fatalf("feed = %v", feed)
	}
	// openapi v0.4 TaskEvent (R2/N2/N3): payload and attempt are top-level,
	// object_ref is a string, usage carries no envelope.
	first := feed["items"].([]any)[0].(map[string]any)
	if pl, ok := first["payload"].(map[string]any); !ok || pl["kind"] != "text" || pl["text"] != "hi" {
		t.Fatalf("feed item payload = %v, want top-level {kind:text,text:hi}", first["payload"])
	}
	if first["attempt"].(float64) != 1 || first["usage"] != nil {
		t.Fatalf("feed item attempt/usage = %v / %v, want 1 / null", first["attempt"], first["usage"])
	}
	toolEv := map[string]any{"task_id": taskID, "attempt": 1, "seq": 4, "ts": fake.Now().Format(time.RFC3339), "class": "tool", "verb": "run_shell", "object_ref": "go test", "outcome": "started",
		"payload": map[string]any{"tool_call_id": "call_1", "kind": "execute"}}
	daemon.must(200, "POST", attemptPath+"/events", map[string]any{"events": []any{toolEv}})
	feed = api.must(200, "GET", p+"/tasks/"+taskID+"/events", map[string]any{})
	items := feed["items"].([]any)
	last := items[len(items)-1].(map[string]any)
	if last["payload"].(map[string]any)["tool_call_id"] != "call_1" {
		t.Fatalf("listTaskEvents payload.tool_call_id must be top-level (R2): %v", last)
	}
	if last["object_ref"] != "go test" || str(last, "sentence") != "tool.run_shell go test → started" {
		t.Fatalf("object_ref must be the schema string (N2): %v / %v", last["object_ref"], last["sentence"])
	}

	// --- CLI with the task token ---
	cli := &client{t: t, srv: ts, bearer: taskToken}
	cctx := cli.must(200, "GET", p+"/cli/context", nil)
	if str(cctx, "task_id") != taskID || str(cctx, "session_id") != sessionID || str(cctx, "agent_name") != "Lead" {
		t.Fatalf("cli context = %v", cctx)
	}
	cli.must(200, "GET", p+"/sessions/"+sessionID, nil)
	cli.must(200, "GET", p+"/sessions/"+sessionID+"/messages", nil)
	if st, out, _ := cli.do("GET", p+"/workspaces/"+wsID+"/agents", nil); st != 403 {
		t.Fatalf("task token outside scope = %d %v (Q8)", st, out)
	}
	// Idempotency-Key is a UUID for every principal (openapi.yaml); the CLI
	// derives UUIDv5(task:<task_id>:<seq>) with seq continuing across attempts
	// from CliContext.last_seq (colab-cli.md §1 v0.2). Anything else is 422.
	if cctx["last_seq"].(float64) != 0 {
		t.Fatalf("fresh task last_seq = %v, want 0", cctx["last_seq"])
	}
	if st, out, _ := cli.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "old key"}, "Idempotency-Key", taskID+":1:1"); st != 422 {
		t.Fatalf("non-uuid Idempotency-Key = %d %v, want 422", st, out)
	}
	reply := cli.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "안녕하세요!"}, "Idempotency-Key", cliKey(taskID, 1))
	rm := reply["message"].(map[string]any)
	if str(rm, "author_type") != "agent" || str(rm, "source_task_id") != taskID || len(reply["triggers"].([]any)) != 0 {
		t.Fatalf("agent reply = %v", reply)
	}
	st, again, rh := cli.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "안녕하세요!"}, "Idempotency-Key", cliKey(taskID, 1))
	if st != 201 || rh.Get("Idempotent-Replayed") != "true" || str(again["message"].(map[string]any), "id") != str(rm, "id") {
		t.Fatalf("CLI replay = %d %v", st, again)
	}
	cli.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "두 번째"}, "Idempotency-Key", cliKey(taskID, 2))
	var agentMsgs int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE source_task_id = $1`, taskID).Scan(&agentMsgs)
	if agentMsgs != 2 {
		t.Fatalf("agent messages = %d, want 2 (E8-04 duplicate 0)", agentMsgs)
	}
	// Keys 1 and 2 were stored without X-Colab-Client-Seq (older CLI): the
	// server walks UUIDv5(task:<id>:n) to find the last existing seq.
	if got := cli.must(200, "GET", p+"/cli/context", nil)["last_seq"].(float64); got != 2 {
		t.Fatalf("last_seq after seq 2 (header-less keys, UUIDv5 fallback) = %v, want 2", got)
	}

	// --- heartbeat expiry → requeue, token revoked → 401 token_revoked ---
	fake.Advance(contracts.HeartbeatExpiry + time.Second)
	if n, err := s.Queue.ExpireStale(t.Context(), fake.Now()); err != nil || n != 1 {
		t.Fatalf("ExpireStale = %d %v", n, err)
	}
	if st, out, _ := cli.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "orphan"}, "Idempotency-Key", cliKey(taskID, 3)); st != 401 || str(out, "code") != "token_revoked" {
		t.Fatalf("orphan post = %d %v, want 401 token_revoked (E11-04)", st, out)
	}
	if st, _, _ := cli.do("GET", p+"/cli/context", nil); st != 401 {
		t.Fatalf("revoked cli context = %d", st)
	}
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE source_task_id = $1`, taskID).Scan(&agentMsgs)
	if agentMsgs != 2 {
		t.Fatal("orphan message must not be stored")
	}
	task := api.must(200, "GET", p+"/tasks/"+taskID, nil)
	if str(task, "status") != "queued" || task["attempt"].(float64) != 2 {
		t.Fatalf("task after expiry = %v", task)
	}
	// Stale-attempt heartbeat → 409 with the revoke command.
	if st, out, _ := daemon.do("POST", attemptPath+"/heartbeat", map[string]any{"last_seq": 3}); st != 409 || str(out, "code") != "stale_attempt" {
		t.Fatalf("stale heartbeat = %d %v", st, out)
	}

	// --- reclaim attempt 2: revoke delivered, posted_message_ids present ---
	claim = daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	bundles = claim["tasks"].([]any)
	if len(bundles) != 1 {
		t.Fatalf("reclaim = %v", claim)
	}
	b2 := bundles[0].(map[string]any)
	if b2["task"].(map[string]any)["attempt"].(float64) != 2 || len(b2["posted_message_ids"].([]any)) != 2 {
		t.Fatalf("attempt 2 bundle = %v", b2)
	}
	// Attempt 2's context carries the task-wide last_seq so the CLI continues
	// at seq 3 and its UUIDv5 key never collides with attempt 1's (E8-04).
	if str(b2, "task_token") == "" {
		t.Fatal("attempt 2 token missing")
	}
	cli.bearer = str(b2, "task_token")
	cctx = cli.must(200, "GET", p+"/cli/context", nil)
	if cctx["attempt"].(float64) != 2 || cctx["last_seq"].(float64) != 2 {
		t.Fatalf("attempt 2 cli context = attempt %v last_seq %v, want 2 / 2", cctx["attempt"], cctx["last_seq"])
	}
	cli.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "attempt 2"}, "Idempotency-Key", cliKey(taskID, 3), "X-Colab-Client-Seq", "3")
	if got := cli.must(200, "GET", p+"/cli/context", nil)["last_seq"].(float64); got != 3 {
		t.Fatalf("last_seq after attempt 2 post = %v, want 3", got)
	}
	// R1 (Hermes' reproduction): the CLI consumes seq 4 locally, the post
	// fails on the network (never reaches the server), and it continues at 5.
	// last_seq must be the max actually used (5), not the count (4) — otherwise
	// the next post would reuse key 5 and be rejected 422 / silently replayed.
	cli.must(201, "POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "after a hole"}, "Idempotency-Key", cliKey(taskID, 5), "X-Colab-Client-Seq", "5")
	if got := cli.must(200, "GET", p+"/cli/context", nil)["last_seq"].(float64); got != 5 {
		t.Fatalf("last_seq with a hole at 4 = %v, want 5 (max, not count) — the CLI would reuse a key", got)
	}
	nextKey := cliKey(taskID, 6) // = last_seq + 1 as the CLI computes it
	st, fresh, fh := cli.do("POST", p+"/sessions/"+sessionID+"/messages", map[string]any{"content": "new content after the hole"}, "Idempotency-Key", nextKey, "X-Colab-Client-Seq", "6")
	if st != 201 || fh.Get("Idempotent-Replayed") != "" {
		t.Fatalf("post at last_seq+1 = %d %v (replayed=%q), want 201 new message", st, fresh, fh.Get("Idempotent-Replayed"))
	}
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE source_task_id = $1`, taskID).Scan(&agentMsgs)
	if agentMsgs != 5 {
		t.Fatalf("agent messages = %d, want 5 (no message lost to key reuse)", agentMsgs)
	}
	var storedSeq int
	_ = pool.QueryRow(t.Context(), `SELECT client_seq FROM idempotency_key WHERE scope = $1 AND key = $2`, "task:"+taskID, nextKey).Scan(&storedSeq)
	if storedSeq != 6 {
		t.Fatalf("idempotency_key.client_seq = %d, want 6", storedSeq)
	}

	// R3 (§4.3 v0.2, E11-05): the revoke for attempt 1 was in the stale
	// heartbeat's 409 and in the claim above, and it is NOT consumed by being
	// sent — a lost response must not lose the command. It rides every
	// response until attempt 1's finish arrives.
	hasRevoke := func(cmds any) bool {
		for _, c := range cmds.([]any) {
			m := c.(map[string]any)
			if str(m, "type") == "revoke" && str(m, "task_id") == taskID && m["attempt"].(float64) == 1 {
				return true
			}
		}
		return false
	}
	if !hasRevoke(claim["commands"]) {
		t.Fatalf("claim commands = %v, want revoke(attempt 1)", claim["commands"])
	}
	again2 := daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	if !hasRevoke(again2["commands"]) {
		t.Fatalf("revoke must be re-sent on the next response (at-least-once): %v", again2["commands"])
	}
	daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/2/phase", map[string]any{"phase": "preparing", "pgid": 101})
	daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/2/phase", map[string]any{"phase": "running", "pgid": 101})
	hb2 := daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/2/heartbeat", map[string]any{"usage": map[string]any{}, "last_seq": 0})
	if !hasRevoke(hb2["commands"]) {
		t.Fatalf("revoke must also ride heartbeat responses: %v", hb2["commands"])
	}
	var stampedDelivered int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM daemon_command WHERE runtime_id = $1 AND delivered_at IS NOT NULL`, runtimeID).Scan(&stampedDelivered)
	if stampedDelivered != 0 {
		t.Fatalf("PendingCommands must not stamp delivered_at (R3), got %d rows", stampedDelivered)
	}
	// The orphaned attempt-1 process reports finish → the revoke is consumed.
	daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/finish", contracts.Finish{Outcome: "cancelled", StopReason: "revoked"})
	after := daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	if hasRevoke(after["commands"]) {
		t.Fatalf("revoke must stop after attempt 1's finish arrived: %v", after["commands"])
	}
	var consumedBy string
	_ = pool.QueryRow(t.Context(), `SELECT consumed_by FROM daemon_command WHERE runtime_id = $1 AND type = 'revoke' AND task_id = $2 AND attempt = 1`, runtimeID, taskID).Scan(&consumedBy)
	if consumedBy != "finish" {
		t.Fatalf("revoke consumed_by = %q, want finish", consumedBy)
	}

	// N6: a malformed workdir report is 400, a well-formed one 200.
	if st, out, _ := daemon.do("POST", "/v1/daemon/runtimes/"+runtimeID+"/workdirs", nil); st != 400 || str(out, "code") != "malformed_json" {
		t.Fatalf("empty workdir report = %d %v, want 400 malformed_json", st, out)
	}
	daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/workdirs", map[string]any{"workdirs": []any{}})

	// --- finish (idempotent) ---
	daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/2/phase", map[string]any{"phase": "running", "pgid": 101})
	fin := daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/2/finish", contracts.Finish{Outcome: "completed", StopReason: "end_turn", LastSeq: 0})
	if str(fin, "status") != "completed" {
		t.Fatalf("finish = %v", fin)
	}
	fin = daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/2/finish", contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther})
	if str(fin, "status") != "completed" {
		t.Fatalf("second finish = %v, want completed kept", fin)
	}
	if st, out, _ := cli.do("GET", p+"/cli/context", nil); st != 401 || str(out, "code") != "token_revoked" {
		t.Fatalf("token after completion = %d %v", st, out)
	}

	// --- 501 for out-of-P1 operations, SSE backfill row count ---
	if st, out, _ := api.do("GET", p+"/inbox", nil); st != 501 || str(out, "code") != "not_implemented" {
		t.Fatalf("P2 op = %d %v", st, out)
	}
	var streamed int
	_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM stream_event WHERE workspace_id = $1 AND type = 'message.created'`, wsID).Scan(&streamed)
	// 2 user posts + 5 agent replies + the session-start system message. The
	// start message is not ROUTED, but it is still a message on the timeline,
	// so it gets a frame like every other one (G4 2판 W10).
	if streamed != 8 {
		t.Fatalf("stream message.created rows = %d, want 8", streamed)
	}
	fmt.Fprintln(io.Discard, agent, other)
}

// hasFieldError reports whether a validation_failed problem carries the
// given field/code pair in errors[].
func hasFieldError(out map[string]any, field, code string) bool {
	errs, _ := out["errors"].([]any)
	for _, e := range errs {
		m, _ := e.(map[string]any)
		if str(m, "field") == field && str(m, "code") == code {
			return true
		}
	}
	return false
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("uuid %q: %v", s, err)
	}
	return u
}

// cliKey is the CLI's Idempotency-Key for seq (server-side derivation in
// handlers_sessions.go; the namespace is pinned by TestCliIdempotencyNamespaceIsStable).
func cliKey(taskID string, seq int) string { return cliIdempotencyKey(uuid.MustParse(taskID), seq) }

func TestCliIdempotencyNamespaceIsStable(t *testing.T) {
	if got := cliIdempotencyNamespace.String(); got != "454e4096-cb98-57f5-b314-6c5499b55cc8" {
		t.Fatalf("namespace = %s; must match cli/internal/client/uuid.go or attempt-2 keys collide", got)
	}
}
