package httpapi

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// G3 re-check defects (plan/G3_DECISION.md §2):
//   - S-6 createWorkspace: the slug suffix retry ran in the enclosing tx, so a
//     second workspace with a colliding slug died on 25P02. A name with no
//     ASCII letters ("마케팅팀") folds to the same "ws" for every workspace, so
//     the collision is the common case, not the corner case.
var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)

func TestG3RecheckWorkspaceSlug(t *testing.T) {
	pool := testdb.New(t)
	s := NewServer(Deps{DB: pool, Clock: clock.NewFake(t0), ServerURL: "http://colab.test", WebURL: "http://web.test:3000"})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	const p = "/api/v1"

	signup := func(email string) *client {
		c := &client{t: t, srv: ts}
		_, _, hdr := c.do("POST", p+"/auth/signup", map[string]any{"display_name": "U", "email": email, "password": "password123"})
		c.cookie = hdr.Get("Set-Cookie")
		return c
	}
	owner := signup("owner@example.com")
	other := signup("other@example.com")

	seen := map[string]bool{}
	create := func(c *client, name string) string {
		ws := c.must(201, "POST", p+"/workspaces", map[string]any{"name": name})
		slug := str(ws, "slug")
		if slug == "" || !slugPattern.MatchString(slug) {
			t.Fatalf("slug %q for %q must match the contract pattern", slug, name)
		}
		if seen[slug] {
			t.Fatalf("slug %q handed out twice", slug)
		}
		seen[slug] = true
		// The whole transaction has to survive the retry, not just the INSERT.
		var settings, members int
		id := str(ws, "id")
		_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM workspace_settings WHERE workspace_id = $1`, id).Scan(&settings)
		_ = pool.QueryRow(t.Context(), `SELECT count(*) FROM member WHERE workspace_id = $1 AND role = 'owner'`, id).Scan(&members)
		if settings != 1 || members != 1 {
			t.Fatalf("workspace %s: settings %d, owner rows %d, want 1/1", slug, settings, members)
		}
		return slug
	}

	// Two workspaces with the same Korean-only name, back to back (S-6).
	a := create(owner, "마케팅팀")
	b := create(owner, "마케팅팀")
	if a == "ws" || b == "ws" || a == b {
		t.Fatalf("same-name slugs = %q, %q, want two distinct non-generic slugs", a, b)
	}
	// A different user, the same name — and a different Korean name must not
	// queue up behind the first one's stem.
	c := create(other, "마케팅팀")
	d := create(other, "개발팀")
	if strings.HasPrefix(d, strings.SplitN(a, "-", 3)[0]+"-"+strings.SplitN(a, "-", 3)[1]) {
		t.Fatalf("different names share a stem: %q vs %q", a, d)
	}
	_ = c

	// ASCII names keep the readable slug and the numbered suffixes.
	if got := create(owner, "Acme Team"); got != "acme-team" {
		t.Fatalf("ascii slug = %q, want acme-team", got)
	}
	if got := create(other, "Acme Team"); got != "acme-team-2" {
		t.Fatalf("second ascii slug = %q, want acme-team-2", got)
	}

	// A crowded stem falls back to a random tail instead of failing.
	for i := 0; i < 12; i++ {
		create(owner, "운영팀")
	}
	var rows int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM workspace`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != len(seen) {
		t.Fatalf("workspace rows = %d, want %d", rows, len(seen))
	}
}

// C-1 (server side): a heartbeat is a liveness signal — an unknown `preview`
// shape must refresh heartbeat_at and answer 200 with one feed warning, while
// the contract shape {text, message_id?} still becomes an SSE message.delta
// (contracts/daemon-protocol.md §4.2, v0.3).
func TestG3RecheckHeartbeatPreview(t *testing.T) {
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
	pairing := api.must(201, "POST", p+"/workspaces/"+wsID+"/runtimes/pairings", map[string]any{"name": "laptop"})

	daemon := &client{t: t, srv: ts}
	paired := daemon.must(201, "POST", "/v1/daemon/pair", map[string]any{
		"pairing_code": str(pairing, "pairing_token"), "hostname": "mac.local", "os": "darwin", "daemon_version": "0.1"})
	runtimeID := str(paired, "runtime_id")
	daemon.bearer = str(paired, "daemon_token")
	daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/probe", contracts.Probe{
		DaemonVersion: "0.1", Hostname: "mac.local", WorkdirRoot: "/tmp/colab",
		Capabilities: []contracts.Capability{{Kind: contracts.RuntimeClaudeCode, Version: "2.1", LoggedIn: true, Models: []string{"claude-sonnet-5"}, ProtocolVersion: 1, BriefTransport: contracts.BriefACPMetaSystemPrompt}},
	})
	agentID := str(api.must(201, "POST", p+"/workspaces/"+wsID+"/agents", map[string]any{
		"name": "Lead", "role": "lead", "role_description": "coordinates", "instructions": "be helpful",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
	}), "id")
	sess := api.must(201, "POST", p+"/workspaces/"+wsID+"/sessions", map[string]any{
		"title": "Preview", "goal": "stream", "isolation": map[string]any{"kind": "none"}, "runtime_id": runtimeID,
		"participants": []map[string]any{{"agent_id": agentID}},
	})
	sessionID := str(sess, "id")
	post := api.must(201, "POST", p+"/sessions/"+sessionID+"/messages",
		map[string]any{"content": router.MentionLink("Lead", mustUUID(t, agentID)) + " hello"},
		"Idempotency-Key", "44444444-4444-4444-8444-444444444444")
	taskID := str(post["triggers"].([]any)[0].(map[string]any), "task_id")

	daemon.must(200, "POST", "/v1/daemon/runtimes/"+runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	attemptPath := "/v1/daemon/tasks/" + taskID + "/attempts/1"
	daemon.must(200, "POST", attemptPath+"/phase", map[string]any{"phase": "preparing", "pgid": 100})
	daemon.must(200, "POST", attemptPath+"/phase", map[string]any{"phase": "running", "pgid": 100})

	heartbeatAt := func() time.Time {
		var at *time.Time
		if err := pool.QueryRow(t.Context(), `SELECT heartbeat_at FROM task WHERE id = $1`, taskID).Scan(&at); err != nil {
			t.Fatal(err)
		}
		if at == nil {
			t.Fatal("heartbeat_at is NULL")
		}
		return *at
	}
	warnings := func() int {
		var n int
		if err := pool.QueryRow(t.Context(), `
			SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'runtime' AND verb = 'error'
			  AND object_ref = to_jsonb('heartbeat.preview'::text)`, taskID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sub := s.Hub.Subscribe(mustUUID(t, wsID), nil)
	defer sub.Close()
	deltas := func() []realtime.Event {
		var out []realtime.Event
		for {
			select {
			case e := <-sub.C:
				if e.Type == "message.delta" {
					out = append(out, e)
				}
			default:
				return out
			}
		}
	}

	// (a) no preview at all — 200, heartbeat_at refreshed, no warning.
	fake.Advance(20 * time.Second)
	daemon.must(200, "POST", attemptPath+"/heartbeat", map[string]any{"usage": map[string]any{}, "last_seq": 0})
	if got := heartbeatAt(); !got.Equal(fake.Now()) {
		t.Fatalf("heartbeat_at = %s, want %s", got, fake.Now())
	}
	if warnings() != 0 || len(deltas()) != 0 {
		t.Fatalf("a bare heartbeat must not warn (%d) or stream (%d)", warnings(), len(deltas()))
	}

	// (b) a v0.2 daemon sending `preview` as a string — still 200, still alive,
	// warned once no matter how many heartbeats carry the old shape.
	fake.Advance(20 * time.Second)
	daemon.must(200, "POST", attemptPath+"/heartbeat", map[string]any{"usage": map[string]any{}, "last_seq": 1, "preview": "부분 출력…"})
	if got := heartbeatAt(); !got.Equal(fake.Now()) {
		t.Fatalf("a bad preview must not cost the attempt: heartbeat_at = %s, want %s (C-1)", got, fake.Now())
	}
	if warnings() != 1 {
		t.Fatalf("feed warnings after a string preview = %d, want 1", warnings())
	}
	if len(deltas()) != 0 {
		t.Fatal("a preview the server cannot read must not be streamed")
	}
	fake.Advance(20 * time.Second)
	daemon.must(200, "POST", attemptPath+"/heartbeat", map[string]any{"usage": map[string]any{}, "last_seq": 2, "preview": 7})
	if warnings() != 1 {
		t.Fatalf("feed warnings after a second bad preview = %d, want 1 per attempt", warnings())
	}
	feed := api.must(200, "GET", p+"/tasks/"+taskID+"/events", nil)
	warned := false
	for _, it := range feed["items"].([]any) {
		m := it.(map[string]any)
		// S-52: the `runtime` payload's only free-text slot is `detail` — the
		// old `field`/`spec`/`note` keys are in no part of the schema.
		if pl, ok := m["payload"].(map[string]any); ok && strings.Contains(str(pl, "detail"), "preview") && str(m, "class") == "runtime" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("the drift must be visible in the activity feed: %v", feed["items"])
	}

	// (c) the contract shape → SSE message.delta, nothing new in the feed.
	fake.Advance(20 * time.Second)
	daemon.must(200, "POST", attemptPath+"/heartbeat", map[string]any{
		"usage": map[string]any{}, "last_seq": 3,
		"preview": map[string]any{"text": "안녕하", "message_id": "8f14e45f-ceea-467a-a3cc-1a2b3c4d5e6f"}})
	got := deltas()
	if len(got) != 1 {
		t.Fatalf("message.delta events = %d, want 1", len(got))
	}
	if !strings.Contains(string(got[0].Payload), "안녕하") || !strings.Contains(string(got[0].Payload), "8f14e45f") {
		t.Fatalf("message.delta payload = %s", got[0].Payload)
	}
	if warnings() != 1 {
		t.Fatalf("a valid preview must not warn: %d", warnings())
	}
	if got := heartbeatAt(); !got.Equal(fake.Now()) {
		t.Fatalf("heartbeat_at = %s, want %s", got, fake.Now())
	}
}
