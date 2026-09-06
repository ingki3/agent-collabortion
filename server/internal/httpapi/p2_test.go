package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// p2Fixture is a workspace with three agents in one session: Lead (assignee),
// R and W. The P2 routing rules need at least three so rule 8's "suppress the
// delegator, not the third party" is observable.
type p2Fixture struct {
	pool                   *pgxpool.Pool
	srv                    *Server
	fake                   *clock.Fake
	api                    *client
	p                      string
	wsID, sessionID        string
	lead, r, w             string
	leadUUID, rUUID, wUUID uuid.UUID
}

func newP2Fixture(t *testing.T) *p2Fixture {
	t.Helper()
	pool := testdb.New(t)
	fake := clock.NewFake(t0)
	s := NewServer(Deps{DB: pool, Clock: fake, ServerURL: "http://colab.test"})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	f := &p2Fixture{pool: pool, srv: s, fake: fake, api: &client{t: t, srv: ts}, p: "/api/v1"}

	_, _, hdr := f.api.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "Dir", "email": "dir@example.com", "password": "password123"})
	f.api.cookie = hdr.Get("Set-Cookie")
	f.wsID = str(f.api.must(201, "POST", f.p+"/workspaces", map[string]any{"name": "Acme"}), "id")
	// A session needs a machine to run on (FR-2.1 M10); pairing a real daemon
	// is not what these rows are about.
	testdb.AddRuntime(t, pool, mustUUID(t, f.wsID), "mac-1", t0)

	mk := func(name, role string) string {
		a := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
			"name": name, "role": role, "role_description": "d", "instructions": "i",
			"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
		})
		return str(a, "id")
	}
	f.lead, f.r, f.w = mk("Lead", "lead"), mk("R", "researcher"), mk("W", "writer")
	f.leadUUID, f.rUUID, f.wUUID = mustUUID(t, f.lead), mustUUID(t, f.r), mustUUID(t, f.w)

	sess := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/sessions", map[string]any{
		"title": "S", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.lead,
		"participants": []map[string]any{
			{"agent_id": f.lead}, {"agent_id": f.r}, {"agent_id": f.w},
		},
	})
	f.sessionID = str(sess, "id")
	return f
}

func (f *p2Fixture) post(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	// Lane rule 3 reuses the MOST RECENT lane, so two posts must not share a
	// clock reading — with a frozen fake every lane looks equally recent.
	f.fake.Advance(time.Minute)
	return f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/messages", body, "Idempotency-Key", uuid.NewString())
}

// postedAgents lists who a POST actually triggered. MessagePostResult carries
// no rule number (that is the preview's job), so only the set is comparable.
func postedAgents(out map[string]any) map[string]bool {
	got := map[string]bool{}
	for _, raw := range out["triggers"].([]any) {
		got[str(raw.(map[string]any), "agent_id")] = true
	}
	return got
}

func (f *p2Fixture) preview(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	return f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/messages/preview", body)
}

func triggerAgents(out map[string]any) map[string]int {
	got := map[string]int{}
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		rule := 0
		if v, ok := tr["rule"].(float64); ok {
			rule = int(v)
		}
		got[str(tr, "agent_id")] = rule
	}
	return got
}

// TestP2RoutingOverHTTP walks the FR-3.3 rules the golden table proves in
// isolation through the real transaction: the rules only matter if the
// premises (thread position, delegation) are actually loaded.
func TestP2RoutingOverHTTP(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	// Rule 1: /note is stored, never routed — and it beats the mention.
	out := f.post(t, map[string]any{"content": "/note " + router.MentionLink("R", f.rUUID) + " 봐줘"})
	if n := len(out["triggers"].([]any)); n != 0 {
		t.Fatalf("/note triggers = %d, want 0", n)
	}

	// Rule 3: @all suppresses implicit routing; Lead (the assignee) is NOT woken.
	out = f.post(t, map[string]any{"content": "[@all](mention://all/all) 진행 상황 공유"})
	if n := len(out["triggers"].([]any)); n != 0 {
		t.Fatalf("@all triggers = %d, want 0", n)
	}

	// Rule 5: a reply to an agent's message goes to that agent, not the
	// assignee. The agent message is planted directly — the rule under test is
	// the routing, not how the message got there.
	var agentMsg uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, created_at)
		VALUES ($1, 'agent', $2, '조사 결과입니다', 'text', $3) RETURNING id`,
		f.sessionID, f.r, t0).Scan(&agentMsg); err != nil {
		t.Fatal(err)
	}
	out = f.post(t, map[string]any{"content": "조금 더 좁혀주세요", "parent_id": agentMsg.String()})
	got := postedAgents(out)
	if !got[f.r] {
		t.Fatalf("reply triggers = %v, want R (rule 5), not the assignee", got)
	}
	// Rule 7 rides along: the assignee gets a deferred fallback five minutes out.
	var deferred int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task WHERE session_id = $1 AND agent_id = $2 AND status = 'deferred'
		  AND not_before IS NOT NULL`, f.sessionID, f.lead).Scan(&deferred); err != nil {
		t.Fatal(err)
	}
	if deferred != 1 {
		t.Fatalf("rule 7 deferred assignee tasks = %d, want 1", deferred)
	}
	var due time.Time
	var trigAt time.Time
	if err := f.pool.QueryRow(ctx, `SELECT not_before, created_at FROM task WHERE session_id = $1 AND agent_id = $2 AND status = 'deferred'`,
		f.sessionID, f.lead).Scan(&due, &trigAt); err != nil {
		t.Fatal(err)
	}
	if got := due.Sub(trigAt); got != 5*time.Minute {
		t.Fatalf("fallback delay = %s, want 5m (rule 7)", got)
	}
}

// TestP2LaneResolutionOverHTTP pins lane rules 3 and 4 and the composer's
// "새 lane으로 보내기" toggle against the database.
func TestP2LaneResolutionOverHTTP(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	mention := router.MentionLink("R", f.rUUID)

	first := f.post(t, map[string]any{"content": mention + " 조사"})
	lane1 := str(first["triggers"].([]any)[0].(map[string]any), "lane_id")

	// Rule 3: the second top-level mention reuses R's most recent lane.
	second := f.post(t, map[string]any{"content": mention + " 보완"})
	if got := str(second["triggers"].([]any)[0].(map[string]any), "lane_id"); got != lane1 {
		t.Fatalf("second mention lane = %s, want the same lane %s (rule 3)", got, lane1)
	}
	// …and it merges into the queued task rather than making a second one.
	if second["triggers"].([]any)[0].(map[string]any)["coalesced"] != true {
		t.Fatal("FR-3.4: a queued task on the lane absorbs the second message")
	}

	// The toggle skips rule 3 and forks (rule 4) — the human's only way to
	// parallelise the same agent.
	third := f.post(t, map[string]any{"content": mention + " 다른 건", "new_lane": true})
	lane3 := str(third["triggers"].([]any)[0].(map[string]any), "lane_id")
	if lane3 == lane1 {
		t.Fatal("new_lane: true must skip rule 3 and create a lane")
	}
	// The toggle does not stick: the next message is back on rule 3, and it
	// reuses the MOST RECENT lane, which is now the forked one.
	fourth := f.post(t, map[string]any{"content": mention + " 이어서"})
	if got := str(fourth["triggers"].([]any)[0].(map[string]any), "lane_id"); got != lane3 {
		t.Fatalf("after the toggle, lane = %s, want the most recent lane %s — the toggle must auto-clear", got, lane3)
	}
	var lanes int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM lane WHERE session_id = $1 AND agent_id = $2`, f.sessionID, f.r).Scan(&lanes); err != nil {
		t.Fatal(err)
	}
	if lanes != 2 {
		t.Fatalf("R lanes = %d, want 2 (one by rule 4, one by the toggle)", lanes)
	}
}

// TestP2LoopLimitPauses drives FR-3.5 end to end: the workspace setting is
// lowered, the pair ping-pongs, and the session stops with a reason that names
// the limit.
func TestP2LoopLimitPauses(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_pair_roundtrips": 1},
	})
	// Lead ↔ R ping-pong. Humans are never limited — that is the reset, not the
	// offence — so the trigger under test has to come from an agent.
	for i := 0; i < 3; i++ {
		for _, pair := range [][2]string{{f.lead, f.r}, {f.r, f.lead}} {
			if _, err := f.pool.Exec(ctx, `
				INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, created_at)
				VALUES ($1, $2, $3, 2, $4)`, f.sessionID, pair[0], pair[1], t0); err != nil {
				t.Fatal(err)
			}
		}
	}
	author := router.Author{Type: "agent", AgentID: &f.rUUID}
	out, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID), author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 또",
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(out.Triggers); n != 0 {
		t.Fatalf("triggers = %d, want 0 — the limit is exceeded", n)
	}
	if len(out.Warnings) != 1 || out.Warnings[0].Code != "loop_limit" {
		t.Fatalf("warnings = %v, want loop_limit — the author must be told why", out.Warnings)
	}
	var status, reason *string
	var detail *gen.PausedDetail
	if err := f.pool.QueryRow(ctx, `SELECT status::text, paused_reason::text, paused_detail FROM session WHERE id = $1`, f.sessionID).
		Scan(&status, &reason, &detail); err != nil {
		t.Fatal(err)
	}
	if status == nil || *status != "paused" || reason == nil || *reason != "loop" {
		t.Fatalf("session = %v/%v, want paused/loop", deref(status), deref(reason))
	}
	// The banner needs the shape the contract defines, not a bare string:
	// which limit, the count that tripped it, and who was looping (S5, O6).
	if detail == nil || detail.Loop == nil || detail.Loop.Limit == nil {
		t.Fatalf("paused_detail = %+v, want the contract's PausedDetail with a loop branch", detail)
	}
	if string(*detail.Loop.Limit) != "pair_roundtrips" {
		t.Fatalf("loop.limit = %q, want pair_roundtrips — `loop` alone does not tell the Director what to raise", *detail.Loop.Limit)
	}
	if detail.Loop.Count == nil || *detail.Loop.Count < 2 {
		t.Fatalf("loop.count = %v, want the roundtrip count that tripped the limit", detail.Loop.Count)
	}
	if detail.Loop.Agents == nil || len(*detail.Loop.Agents) != 2 {
		t.Fatalf("loop.agents = %v, want both ends of the ping-pong", detail.Loop.Agents)
	}
	if string(detail.Reason) != "loop" || detail.PausedAt.IsZero() {
		t.Fatalf("paused_detail reason/paused_at = %q/%v, both are required by the contract", detail.Reason, detail.PausedAt)
	}

	// And the session read model exposes it under the contract's key, so the
	// web can render the banner from getSession as well as from the stream.
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	pd, ok := sess["paused_detail"].(map[string]any)
	if !ok {
		t.Fatalf("getSession paused_detail = %v, want an object under that exact key", sess["paused_detail"])
	}
	if loop, ok := pd["loop"].(map[string]any); !ok || loop["limit"] != "pair_roundtrips" {
		t.Fatalf("getSession paused_detail.loop = %v", pd["loop"])
	}
	// FR-3.5: the Director gets a system-issued HITL, not a silent stop.
	var hitl int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND source = 'system'`, f.sessionID).Scan(&hitl); err != nil {
		t.Fatal(err)
	}
	if hitl != 1 {
		t.Fatalf("system HITL rows = %d, want 1", hitl)
	}
}

// TestP2PreviewMatchesPost is FR-3.6's whole point: the composer's promise is
// the post's own answer, computed by the same rules on the same premises.
func TestP2PreviewMatchesPost(t *testing.T) {
	f := newP2Fixture(t)
	body := map[string]any{"content": router.MentionLink("R", f.rUUID) + " " +
		router.MentionLink("W", f.wUUID) + " 나눠서 봐줘"}

	pv := f.preview(t, body)
	if pv["note_only"] != false {
		t.Fatalf("note_only = %v, want false", pv["note_only"])
	}
	want := map[string]int{f.r: 2, f.w: 2}
	if got := triggerAgents(pv); len(got) != 2 || got[f.r] != want[f.r] || got[f.w] != want[f.w] {
		t.Fatalf("preview triggers = %v, want %v", got, want)
	}
	for _, raw := range pv["triggers"].([]any) {
		tr := raw.(map[string]any)
		lane := tr["lane"].(map[string]any)
		if lane["lane_id"] != nil {
			t.Fatalf("preview lane_id = %v, want null (a new lane, rule 4)", lane["lane_id"])
		}
		if int(lane["resolution"].(float64)) != 4 {
			t.Fatalf("lane resolution = %v, want 4", lane["resolution"])
		}
		if tr["profile"] == nil || str(tr["profile"].(map[string]any), "name") != "default" {
			t.Fatalf("preview must name the profile, got %v", tr["profile"])
		}
	}

	post := f.post(t, body)
	if got := postedAgents(post); len(got) != 2 || !got[f.r] || !got[f.w] {
		t.Fatalf("post triggers = %v, want the preview's answer %v", got, want)
	}

	// A /note preview says so before anything is sent.
	if pv := f.preview(t, map[string]any{"content": "/note 회의록"}); pv["note_only"] != true || len(pv["triggers"].([]any)) != 0 {
		t.Fatalf("/note preview = %v", pv)
	}
	// A non-participant is warned in the preview, not after the fact.
	stranger := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "X", "role": "custom", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "m"}},
	})
	pv = f.preview(t, map[string]any{"content": router.MentionLink("X", mustUUID(t, str(stranger, "id"))) + " 도와줘"})
	warns := pv["warnings"].([]any)
	if len(warns) != 1 || str(warns[0].(map[string]any), "code") != "not_participant" {
		t.Fatalf("preview warnings = %v, want not_participant", warns)
	}
}

// TestP2LimitRangeIsEnforced is S-11: the contract says 1..200, so anything
// else is a 422. Clamping to 50 tells a client that asked for 500 nothing.
func TestP2LimitRangeIsEnforced(t *testing.T) {
	f := newP2Fixture(t)
	for _, bad := range []string{"0", "-1", "201", "999999"} {
		st, out, _ := f.api.do("GET", f.p+"/sessions/"+f.sessionID+"/messages?limit="+bad, nil)
		if st != 422 || !hasFieldError(out, "limit", "out_of_range") {
			t.Fatalf("limit=%s → %d %v, want 422 out_of_range", bad, st, out)
		}
	}
	for _, ok := range []string{"1", "50", "200"} {
		if st, out, _ := f.api.do("GET", f.p+"/sessions/"+f.sessionID+"/messages?limit="+ok, nil); st != 200 {
			t.Fatalf("limit=%s → %d %v, want 200", ok, st, out)
		}
	}
	// A missing limit still gets the server default rather than a 422.
	f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID+"/messages", nil)
	f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/agents?limit=200", nil)
	if st, _, _ := f.api.do("GET", f.p+"/workspaces/"+f.wsID+"/agents?limit=201", nil); st != 422 {
		t.Fatalf("agents limit=201 → %d, want 422", st)
	}
}

// TestP2WorkspaceSettingsAuthz is S-12: turning the operation on is exactly the
// moment authorisation has to exist. 501 was never a permission check.
func TestP2WorkspaceSettingsAuthz(t *testing.T) {
	f := newP2Fixture(t)

	got := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/settings", nil)
	loop := got["loop_limits"].(map[string]any)
	if int(loop["max_chain_depth"].(float64)) != 8 {
		t.Fatalf("default loop_limits = %v", loop)
	}
	updated := f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits":       map[string]any{"max_chain_depth": 4, "max_hops_per_hour": 60, "max_pair_roundtrips": 5},
		"default_isolation": "worktree",
	})
	if int(updated["loop_limits"].(map[string]any)["max_chain_depth"].(float64)) != 4 {
		t.Fatalf("updated = %v", updated)
	}
	if str(updated, "default_isolation") != "worktree" {
		t.Fatalf("default_isolation = %v", updated["default_isolation"])
	}
	// A limit of 0 would silently disable the limit — reject it.
	if st, out, _ := f.api.do("PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_chain_depth": 0}}); st != 422 ||
		!hasFieldError(out, "loop_limits.max_chain_depth", "out_of_range") {
		t.Fatalf("max_chain_depth=0 → %d %v, want 422", st, out)
	}

	// A plain member sees 403, not the settings.
	member := &client{t: t, srv: f.api.srv}
	_, _, hdr := member.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "M", "email": "m@example.com", "password": "password123"})
	member.cookie = hdr.Get("Set-Cookie")
	inv := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/invites", map[string]any{})
	member.must(200, "POST", f.p+"/invites/"+str(inv, "token")+"/accept", nil)
	if st, out, _ := member.do("GET", f.p+"/workspaces/"+f.wsID+"/settings", nil); st != 403 || str(out, "code") != "admin_required" {
		t.Fatalf("member GET settings = %d %v, want 403 admin_required", st, out)
	}
	if st, _, _ := member.do("PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{"task_event_masking": true}); st != 403 {
		t.Fatalf("member PATCH settings = %d, want 403", st)
	}

	// An outsider is not even told the workspace exists in settings terms.
	outsider := &client{t: t, srv: f.api.srv}
	_, _, hdr = outsider.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "O", "email": "o@example.com", "password": "password123"})
	outsider.cookie = hdr.Get("Set-Cookie")
	if st, out, _ := outsider.do("GET", f.p+"/workspaces/"+f.wsID+"/settings", nil); st != 403 || str(out, "code") != "not_member" {
		t.Fatalf("outsider GET settings = %d %v, want 403 not_member", st, out)
	}
}

// TestP2AcceptInviteIsIdempotent is S-10: accepting twice is a no-op, not a
// unique-violation 500.
func TestP2AcceptInviteIsIdempotent(t *testing.T) {
	f := newP2Fixture(t)
	inv := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/invites", map[string]any{})
	joiner := &client{t: t, srv: f.api.srv}
	_, _, hdr := joiner.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "J", "email": "j@example.com", "password": "password123"})
	joiner.cookie = hdr.Get("Set-Cookie")

	first := joiner.must(200, "POST", f.p+"/invites/"+str(inv, "token")+"/accept", nil)
	second := joiner.must(200, "POST", f.p+"/invites/"+str(inv, "token")+"/accept", nil)
	if str(first, "id") == "" || str(first, "id") != str(second, "id") {
		t.Fatalf("second accept = %v, want the same membership as %v", second, first)
	}
	var members int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM member WHERE workspace_id = $1`, f.wsID).Scan(&members); err != nil {
		t.Fatal(err)
	}
	if members != 2 {
		t.Fatalf("members = %d, want 2 (owner + joiner)", members)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
