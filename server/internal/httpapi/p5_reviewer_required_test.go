package httpapi

// T-S18 — S-84 (openapi 0.1.4): `agent_approval` needs a reviewer who is a
// participant, the progress read model says WHY an atom cannot be met
// (agent_id · agent_name · blocked_reason · next_actor), and updateSession
// may replace the completion tree of an active/paused session — which is how
// the Director's "STO 시장 조사" session (`{"type":"agent_approval"}`, no
// agent_id, never closable) gets rescued.
//
// Each contract line is its own subtest (P3 §0-7). The old-shape session is
// written straight into the row: createSession refuses to make one now, and
// the point of the read-model tests is that a row from before 0.1.4 still
// answers 200.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// createWith posts a session with `tree` and the given participants.
func (f *p2Fixture) createWith(tree map[string]any, participants ...string) (int, map[string]any) {
	parts := make([]map[string]any, 0, len(participants))
	for _, id := range participants {
		parts = append(parts, map[string]any{"agent_id": id})
	}
	body := map[string]any{
		"title": "S-84", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"participants": parts, "assignee_agent_id": participants[0],
		"completion_condition": tree,
	}
	st, out, _ := f.api.do("POST", f.p+"/workspaces/"+f.wsID+"/sessions", body)
	return st, out
}

func and(conds ...map[string]any) map[string]any {
	return map[string]any{"op": "and", "conditions": conds}
}

func atom(typ string, kv ...string) map[string]any {
	m := map[string]any{"type": typ}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// oldShapeSession writes a pre-0.1.4 row: `agent_approval` with no reviewer.
func (f *p2Fixture) oldShapeSession(t *testing.T, tree map[string]any) string {
	t.Helper()
	sess := f.artifactSession(t, and(atom("user_approval")))
	raw, _ := json.Marshal(tree)
	if _, err := f.pool.Exec(t.Context(), `UPDATE work SET completion_condition = $2 WHERE room_id = $1`, sess, raw); err != nil {
		t.Fatal(err)
	}
	return sess
}

// conds indexes getSession's completion_progress.conditions[] by type.
func (f *p2Fixture) conds(t *testing.T, sess string) (map[string]map[string]any, map[string]any) {
	t.Helper()
	out := f.api.must(200, "GET", f.p+"/sessions/"+sess, nil)
	prog := out["completion_progress"].(map[string]any)
	by := map[string]map[string]any{}
	for _, raw := range prog["conditions"].([]any) {
		c := raw.(map[string]any)
		by[str(c, "type")] = c
	}
	return by, prog
}

func (f *p2Fixture) patchCond(t *testing.T, c *client, sess string, tree map[string]any) (int, map[string]any) {
	t.Helper()
	st, out, _ := c.do("PATCH", f.p+"/sessions/"+sess, map[string]any{"completion_condition": tree})
	return st, out
}

func (f *p2Fixture) openHitls(t *testing.T, sess string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND purpose = 'user_approval' AND status = 'open'`, sess).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestP5ReviewerRequiredOnCreate — createSession: "`agent_approval` 은
// `agent_id`(리뷰어) 필수이고 그 에이전트가 참여자여야 한다 (errors[].code:
// reviewer_required / reviewer_not_participant)".
func TestP5ReviewerRequiredOnCreate(t *testing.T) {
	f := newP2Fixture(t)

	t.Run("agent_approval without agent_id → 422 reviewer_required", func(t *testing.T) {
		st, out := f.createWith(and(atom("artifact_submitted", "who", "assignee"), atom("agent_approval")), f.lead, f.r)
		if st != 422 || fieldCode(out, "completion_condition/conditions/1/agent_id") != "reviewer_required" {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("agent_approval with a non-participant → 422 reviewer_not_participant", func(t *testing.T) {
		st, out := f.createWith(and(atom("agent_approval", "agent_id", f.w)), f.lead, f.r)
		if st != 422 || fieldCode(out, "completion_condition/conditions/0/agent_id") != "reviewer_not_participant" {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("artifact_submitted with a non-participant agent_id → 422 reviewer_not_participant", func(t *testing.T) {
		st, out := f.createWith(and(atom("artifact_submitted", "agent_id", f.w), atom("user_approval")), f.lead, f.r)
		if st != 422 || fieldCode(out, "completion_condition/conditions/0/agent_id") != "reviewer_not_participant" {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("a reviewer among the participants → 201 with agent_name on the row", func(t *testing.T) {
		st, out := f.createWith(and(atom("artifact_submitted", "who", "assignee"), atom("agent_approval", "agent_id", f.r)), f.lead, f.r)
		if st != 201 {
			t.Fatalf("= %d %v", st, out)
		}
		by, prog := f.conds(t, str(out, "id"))
		if got := by["agent_approval"]; str(got, "agent_id") != f.r || str(got, "agent_name") != "R" || got["blocked_reason"] != nil || str(got, "next_actor") != "R" {
			t.Fatalf("agent_approval row = %v, want agent R, no blocked_reason, next_actor R", got)
		}
		// `who: assignee` resolves to the assignee — Lead — for the screen.
		if got := by["artifact_submitted"]; str(got, "agent_id") != f.lead || str(got, "agent_name") != "Lead" || got["blocked_reason"] != nil || str(got, "next_actor") != "Lead" {
			t.Fatalf("artifact_submitted row = %v, want the assignee Lead", got)
		}
		if sat, _ := prog["satisfied"].(bool); sat {
			t.Fatalf("satisfied on a fresh session: %v", prog)
		}
	})
	t.Run("the assignee counts as a participant for the reviewer check", func(t *testing.T) {
		// participants[] carries Lead only; the assignee is Lead. A reviewer
		// of Lead is a participant by both routes — the row is the point.
		st, out := f.createWith(and(atom("agent_approval", "agent_id", f.lead)), f.lead)
		if st != 201 {
			t.Fatalf("= %d %v", st, out)
		}
	})
}

// TestP5ProgressBlockedReason — CompletionProgress.conditions[].blocked_reason:
// "이 조건이 지금 구조상 충족될 수 없는 이유 — 옛 세션(리뷰어 없는 agent_approval)
// 이나 리뷰어가 세션을 떠난 경우". Three reasons, and getSession answers 200 on
// every one of them (the Director's session used to be unreadable ONLY in the
// sense that nothing said why it would not close; the 500 this guards against
// is the join on a missing agent).
func TestP5ProgressBlockedReason(t *testing.T) {
	f := newP2Fixture(t)

	t.Run("reviewer_missing — the old shape, no agent_id", func(t *testing.T) {
		sess := f.oldShapeSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("agent_approval")))
		by, prog := f.conds(t, sess)
		got := by["agent_approval"]
		if str(got, "blocked_reason") != "reviewer_missing" || got["agent_id"] != nil || got["agent_name"] != nil || got["next_actor"] != nil {
			t.Fatalf("agent_approval row = %v, want blocked_reason reviewer_missing and no agent", got)
		}
		if sat, _ := prog["satisfied"].(bool); sat {
			t.Fatal("a blocked tree reads as satisfied")
		}
		// user_approval is the Director's turn, said in the screen's word.
		if str(by["artifact_submitted"], "next_actor") != "Lead" {
			t.Fatalf("artifact_submitted next_actor = %v, want Lead (the assignee)", by["artifact_submitted"])
		}
	})
	t.Run("reviewer_not_participant — the reviewer left the session", func(t *testing.T) {
		sess := f.artifactSession(t, and(atom("agent_approval", "agent_id", f.r)))
		if _, err := f.pool.Exec(t.Context(), `DELETE FROM room_participant WHERE room_id = $1 AND agent_id = $2`, sess, f.r); err != nil {
			t.Fatal(err)
		}
		by, _ := f.conds(t, sess)
		got := by["agent_approval"]
		if str(got, "blocked_reason") != "reviewer_not_participant" || str(got, "agent_id") != f.r || str(got, "agent_name") != "R" || got["next_actor"] != nil {
			t.Fatalf("agent_approval row = %v, want reviewer_not_participant with R still named", got)
		}
	})
	t.Run("agent_archived — the reviewer was archived", func(t *testing.T) {
		sess := f.artifactSession(t, and(atom("agent_approval", "agent_id", f.r)))
		if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET archived_at = now() WHERE id = $1`, f.r); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = f.pool.Exec(t.Context(), `UPDATE agent SET archived_at = NULL WHERE id = $1`, f.r) })
		by, _ := f.conds(t, sess)
		if got := by["agent_approval"]; str(got, "blocked_reason") != "agent_archived" || str(got, "agent_name") != "R" {
			t.Fatalf("agent_approval row = %v, want agent_archived", got)
		}
	})
	t.Run("a met atom is neither blocked nor anyone's turn", func(t *testing.T) {
		sess := f.oldShapeSession(t, and(atom("artifact_submitted", "agent_id", f.w), atom("agent_approval")))
		wTok, _ := f.agentToken(t, sess, f.wUUID, "W")
		if st, out := f.submit(t, sess, wTok, "draft.md", "doc", []byte("초안")); st != 201 {
			t.Fatalf("submit = %d %v", st, out)
		}
		if _, err := f.pool.Exec(t.Context(), `DELETE FROM room_participant WHERE room_id = $1 AND agent_id = $2`, sess, f.w); err != nil {
			t.Fatal(err)
		}
		by, _ := f.conds(t, sess)
		got := by["artifact_submitted"]
		if got["met"] != true || got["blocked_reason"] != nil || got["next_actor"] != nil || str(got, "agent_name") != "W" {
			t.Fatalf("met artifact_submitted row = %v, want met, no blocked_reason, no next_actor", got)
		}
	})
	t.Run("user_approval · manual → next_actor director", func(t *testing.T) {
		sess := f.artifactSession(t, map[string]any{"op": "or", "conditions": []map[string]any{atom("user_approval"), atom("manual")}})
		by, _ := f.conds(t, sess)
		for _, typ := range []string{"user_approval", "manual"} {
			if got := by[typ]; str(got, "next_actor") != "director" || got["agent_id"] != nil || got["blocked_reason"] != nil {
				t.Fatalf("%s row = %v, want next_actor director", typ, got)
			}
		}
	})
}

// TestP5UpdateCompletionConditionActive — updateSession: "`completion_condition`
// 은 `active`·`paused` 에서도 Director 가 바꿀 수 있다 — 검증은 createSession 과
// 같고, 바꾸면 진행률을 다시 계산해 `session.completion_progress` 를 보낸다. 이미
// 충족된 원자는 그대로 유지".
func TestP5UpdateCompletionConditionActive(t *testing.T) {
	f := newP2Fixture(t)
	member := &client{t: t, srv: f.api.srv}
	_, _, hdr := member.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "Mem", "email": "mem84@example.com", "password": "password123"})
	member.cookie = hdr.Get("Set-Cookie")
	inv := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/invites", map[string]any{"role": "member"})
	member.must(200, "POST", f.p+"/invites/"+str(inv, "token")+"/accept", nil)

	t.Run("the old session is rescued: reviewer named → Lead approves → completed", func(t *testing.T) {
		sess := f.oldShapeSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("agent_approval")))
		frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+sess)
		defer stop()

		st, out := f.patchCond(t, f.api, sess, and(atom("artifact_submitted", "agent_id", f.w), atom("agent_approval", "agent_id", f.lead)))
		if st != 200 {
			t.Fatalf("PATCH = %d %v", st, out)
		}
		got := waitTypes(t, frames, "session.completion_progress", "session.updated")
		var frame struct {
			SessionID string `json:"session_id"`
			Progress  struct {
				Conditions []map[string]any `json:"conditions"`
			} `json:"completion_progress"`
		}
		if err := json.Unmarshal(got["session.completion_progress"], &frame); err != nil || frame.SessionID != sess {
			t.Fatalf("session.completion_progress frame = %s (%v)", got["session.completion_progress"], err)
		}
		if len(frame.Progress.Conditions) != 2 || str(frame.Progress.Conditions[1], "agent_name") != "Lead" || frame.Progress.Conditions[1]["blocked_reason"] != nil {
			t.Fatalf("frame conditions = %v, want the new tree with Lead named and nothing blocked", frame.Progress.Conditions)
		}
		by, _ := f.conds(t, sess)
		if c := by["agent_approval"]; c["blocked_reason"] != nil || str(c, "agent_name") != "Lead" || str(c, "next_actor") != "Lead" {
			t.Fatalf("after the fix agent_approval row = %v", c)
		}
		var n int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM activity_log WHERE session_id = $1 AND action = 'session.completion_condition_changed'`, sess).Scan(&n); err != nil || n != 1 {
			t.Fatalf("activity_log session.completion_condition_changed = %d (%v), want 1", n, err)
		}

		wTok, _ := f.agentToken(t, sess, f.wUUID, "W")
		leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
		st, out = f.submit(t, sess, wTok, "report.md", "doc", []byte("보고서"))
		if st != 201 {
			t.Fatalf("submit = %d %v", st, out)
		}
		art := str(out["artifact"].(map[string]any), "id")
		st, out = f.rawPost(t, f.p+"/artifacts/"+art+"/review", leadTok, map[string]any{"verdict": "approve"})
		if st != 200 {
			t.Fatalf("Lead approve = %d %v", st, out)
		}
		if got := f.sessionStatus(t, sess); got != "completed" {
			t.Fatalf("session = %q, want completed — the rescued tree closes", got)
		}
	})
	t.Run("validation is createSession's: reviewer_required · reviewer_not_participant · criteria_met alone", func(t *testing.T) {
		sess := f.artifactSession(t, and(atom("user_approval")))
		if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval"))); st != 422 || fieldCode(out, "completion_condition/conditions/0/agent_id") != "reviewer_required" {
			t.Fatalf("no reviewer = %d %v", st, out)
		}
		outsider := str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
			"name": "Out", "role": "researcher", "role_description": "d", "instructions": "i",
			"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5"}},
		}), "id")
		if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval", "agent_id", outsider))); st != 422 || fieldCode(out, "completion_condition/conditions/0/agent_id") != "reviewer_not_participant" {
			t.Fatalf("outsider reviewer = %d %v", st, out)
		}
		if st, out := f.patchCond(t, f.api, sess, and(atom("criteria_met"))); st != 422 || fieldCode(out, "completion_condition") != "criteria_met_alone" {
			t.Fatalf("criteria_met alone = %d %v", st, out)
		}
		// Nothing was stored by the three refusals.
		by, _ := f.conds(t, sess)
		if len(by) != 1 || by["user_approval"] == nil {
			t.Fatalf("tree changed by a refused PATCH: %v", by)
		}
	})
	t.Run("a member is not the Director → 403", func(t *testing.T) {
		sess := f.artifactSession(t, and(atom("user_approval")))
		if st, out := f.patchCond(t, member, sess, and(atom("manual"))); st != 403 || str(out, "code") != "director_required" {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("met atoms stay met: the new tree satisfied as it stands → completed", func(t *testing.T) {
		sess := f.oldShapeSession(t, and(atom("artifact_submitted", "agent_id", f.w), atom("agent_approval")))
		wTok, _ := f.agentToken(t, sess, f.wUUID, "W")
		if st, out := f.submit(t, sess, wTok, "a.md", "doc", []byte("a")); st != 201 {
			t.Fatalf("submit = %d %v", st, out)
		}
		st, out := f.patchCond(t, f.api, sess, and(atom("artifact_submitted", "agent_id", f.w)))
		if st != 200 || str(out, "status") != "completed" {
			t.Fatalf("PATCH = %d status %v, want completed (artifact_submitted was already met)", st, out["status"])
		}
		var summaries int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'summary'`, sess).Scan(&summaries); err != nil || summaries != 1 {
			t.Fatalf("summary messages = %d (%v), want 1 — the existing completing path ran", summaries, err)
		}
	})
	t.Run("only user_approval left → the platform's request, issued once", func(t *testing.T) {
		sess := f.oldShapeSession(t, and(atom("artifact_submitted", "agent_id", f.w), atom("agent_approval")))
		wTok, _ := f.agentToken(t, sess, f.wUUID, "W")
		if st, out := f.submit(t, sess, wTok, "a.md", "doc", []byte("a")); st != 201 {
			t.Fatalf("submit = %d %v", st, out)
		}
		if n := f.openHitls(t, sess); n != 0 {
			t.Fatalf("open user_approval before the change = %d", n)
		}
		tree := and(atom("artifact_submitted", "agent_id", f.w), atom("user_approval"))
		if st, out := f.patchCond(t, f.api, sess, tree); st != 200 {
			t.Fatalf("PATCH = %d %v", st, out)
		}
		if n := f.openHitls(t, sess); n != 1 {
			t.Fatalf("open user_approval after the change = %d, want 1", n)
		}
		f.fake.Advance(time.Minute)
		if st, out := f.patchCond(t, f.api, sess, tree); st != 200 {
			t.Fatalf("second PATCH = %d %v", st, out)
		}
		if n := f.openHitls(t, sess); n != 1 {
			t.Fatalf("open user_approval after a second change = %d, want still 1 — one card to answer, not two", n)
		}
		by, _ := f.conds(t, sess)
		if c := by["user_approval"]; str(c, "next_actor") != "director" {
			t.Fatalf("user_approval row = %v", c)
		}
		if got := f.sessionStatus(t, sess); got != "active" {
			t.Fatalf("session = %q, want active until the Director answers", got)
		}
	})
	t.Run("paused accepts the change and stays paused", func(t *testing.T) {
		sess := f.oldShapeSession(t, and(atom("agent_approval")))
		f.api.must(200, "POST", f.p+"/sessions/"+sess+"/pause", map[string]any{"mode": "drain"})
		if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval", "agent_id", f.r))); st != 200 || str(out, "status") != "paused" {
			t.Fatalf("PATCH on paused = %d status %v", st, out["status"])
		}
		by, _ := f.conds(t, sess)
		if c := by["agent_approval"]; c["blocked_reason"] != nil || str(c, "agent_name") != "R" {
			t.Fatalf("agent_approval row = %v", c)
		}
	})
	t.Run("completed · cancelled · completing → 422 immutable", func(t *testing.T) {
		for _, status := range []string{"completed", "cancelled", "completing"} {
			sess := f.artifactSession(t, and(atom("user_approval")))
			finished := "NULL"
			if status != "completing" {
				finished = "now()"
			}
			if _, err := f.pool.Exec(t.Context(), `UPDATE work SET status = $2::session_status, finished_at = `+finished+` WHERE room_id = $1`, sess, status); err != nil {
				t.Fatal(err)
			}
			if st, out := f.patchCond(t, f.api, sess, and(atom("manual"))); st != 422 || fieldCode(out, "completion_condition") != "immutable" {
				t.Fatalf("%s: = %d %v", status, st, out)
			}
		}
	})
	t.Run("draft is validated the same way and re-evaluates nothing", func(t *testing.T) {
		st, out, _ := f.api.do("POST", f.p+"/workspaces/"+f.wsID+"/sessions", map[string]any{
			"title": "D", "goal": "g", "isolation": map[string]any{"kind": "none"}, "draft": true,
			"participants": []map[string]any{{"agent_id": f.lead}},
		})
		if st != 201 {
			t.Fatalf("draft = %d %v", st, out)
		}
		sess := str(out, "id")
		if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval"))); st != 422 || fieldCode(out, "completion_condition/conditions/0/agent_id") != "reviewer_required" {
			t.Fatalf("draft no reviewer = %d %v", st, out)
		}
		if st, out := f.patchCond(t, f.api, sess, and(atom("agent_approval", "agent_id", f.lead))); st != 200 || str(out, "status") != "draft" {
			t.Fatalf("draft PATCH = %d %v", st, out)
		}
		var n int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM activity_log WHERE session_id = $1 AND action = 'session.completion_condition_changed'`, sess).Scan(&n); err != nil || n != 0 {
			t.Fatalf("draft wrote a condition_changed line: %d (%v)", n, err)
		}
	})
}

// TestP5ProgressFrameMatchesRead — submitArtifact's `completion_progress` and
// the `session.completion_progress` frame carry the S-84 columns the same way
// getSession does: one function renders all three.
func TestP5ProgressFrameMatchesRead(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.oldShapeSession(t, and(atom("artifact_submitted", "agent_id", f.w), atom("agent_approval")))
	wTok, _ := f.agentToken(t, sess, f.wUUID, "W")
	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+sess)
	defer stop()
	st, out := f.submit(t, sess, wTok, "a.md", "doc", []byte("a"))
	if st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	fromSubmit := out["completion_progress"].(map[string]any)
	raw := waitFrame(t, frames, "session.completion_progress", nil)
	var frame struct {
		Progress map[string]any `json:"completion_progress"`
	}
	if err := json.Unmarshal(raw, &frame); err != nil {
		t.Fatal(err)
	}
	read := f.api.must(200, "GET", f.p+"/sessions/"+sess, nil)["completion_progress"].(map[string]any)
	a, _ := json.Marshal(fromSubmit["conditions"])
	b, _ := json.Marshal(frame.Progress["conditions"])
	c, _ := json.Marshal(read["conditions"])
	if string(a) != string(b) || string(b) != string(c) {
		t.Fatalf("three renderings differ:\n submit %s\n frame  %s\n read   %s", a, b, c)
	}
	var rows []map[string]any
	_ = json.Unmarshal(c, &rows)
	if len(rows) != 2 || rows[0]["met"] != true || str(rows[1], "blocked_reason") != "reviewer_missing" {
		t.Fatalf("rows = %v", rows)
	}
	_ = uuid.Nil
}
