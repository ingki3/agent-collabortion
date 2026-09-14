package httpapi

// T-S17 — deleteSession (openapi 0.1.3, FR-2.7, Director request 2026-09-14).
// Each contract line is its own subtest so one failing boundary does not hide
// the others (P3 §0-7). The fixture is the members one: it already has the
// Director/owner ("Dir"), an admin, a plain member and a fourth account, and a
// runtime the workdir rows can hang on.

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// newSessionWithStatus creates a second session in the fixture's workspace and
// forces it into `status` — the transitions themselves are other operations'
// tests; deleteSession only reads the column.
func (f *membersFixture) newSessionWithStatus(t *testing.T, status string) string {
	t.Helper()
	sess := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/sessions", map[string]any{
		"title": "삭제 대상 " + status, "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.lead, "participants": []map[string]any{{"agent_id": f.lead}},
	})
	id := str(sess, "id")
	f.setStatus(t, id, status)
	return id
}

// setStatus forces the column (and the columns its CHECK constraints demand).
func (f *membersFixture) setStatus(t *testing.T, id, status string) {
	t.Helper()
	finished, paused := "NULL", "NULL"
	if status == "completed" || status == "cancelled" {
		finished = "now()"
	}
	if status == "paused" {
		paused = "'director'"
	}
	if _, err := f.pool.Exec(t.Context(), `UPDATE session SET status = $2::session_status, finished_at = `+finished+`, paused_reason = `+paused+` WHERE id = $1`, id, status); err != nil {
		t.Fatal(err)
	}
}

// addUsage gives the session one finished task with a usage row, so it shows
// up in getWorkspaceCost.by_session and in the metrics that count tasks.
func (f *membersFixture) addUsage(t *testing.T, sessionID string, usd float64) uuid.UUID {
	t.Helper()
	ctx := t.Context()
	var laneID, taskID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at)
		SELECT $1, $2, p.profile_id, 'done', now(), now() FROM session_participant p
		WHERE p.session_id = $1 AND p.agent_id = $2 RETURNING id`, sessionID, f.lead).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, started_at, finished_at, created_at, updated_at)
		SELECT $1, $2, $3, p.profile_id, 'completed', now() - interval '2 minutes', now(), now(), now() FROM session_participant p
		WHERE p.session_id = $2 AND p.agent_id = $3 RETURNING id`, laneID, sessionID, f.lead).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO task_usage (task_id, input_tokens, output_tokens, cost_usd) VALUES ($1, 1000, 100, $2)`, taskID, usd); err != nil {
		t.Fatal(err)
	}
	return taskID
}

func (f *membersFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (f *membersFixture) del(c *client, sessionID string) (int, map[string]any) {
	st, out, _ := c.do("DELETE", f.p+"/sessions/"+sessionID, nil)
	return st, out
}

// TestP5DeleteSessionAuthz — "권한: Director 또는 owner·admin". The fixture's
// Director is also the owner, so the Director case is exercised by a member
// who is made Director of their own session.
func TestP5DeleteSessionAuthz(t *testing.T) {
	f := newMembersFixture(t)

	t.Run("member (not Director) → 403 director_or_admin_required", func(t *testing.T) {
		id := f.newSessionWithStatus(t, "completed")
		st, out := f.del(f.member, id)
		if st != 403 || str(out, "code") != "director_or_admin_required" || str(out, "detail") != sessions.DeleteForbiddenDetail {
			t.Fatalf("= %d %v", st, out)
		}
		if f.count(t, `SELECT count(*) FROM session WHERE id = $1`, id) != 1 {
			t.Fatal("a member deleted a session")
		}
	})
	t.Run("Director (plain member role) → 204", func(t *testing.T) {
		id := f.newSessionWithStatus(t, "completed")
		if _, err := f.pool.Exec(t.Context(), `UPDATE session SET director_user_id = $2 WHERE id = $1`, id, f.memberUserID); err != nil {
			t.Fatal(err)
		}
		if st, out := f.del(f.member, id); st != 204 {
			t.Fatalf("Director delete = %d %v", st, out)
		}
	})
	t.Run("admin (not Director) → 204", func(t *testing.T) {
		id := f.newSessionWithStatus(t, "completed")
		if st, out := f.del(f.admin, id); st != 204 {
			t.Fatalf("admin delete = %d %v", st, out)
		}
	})
	t.Run("owner (Director here) → 204, second call 404", func(t *testing.T) {
		id := f.newSessionWithStatus(t, "completed")
		if st, out := f.del(f.api, id); st != 204 {
			t.Fatalf("owner delete = %d %v", st, out)
		}
		if st, out := f.del(f.api, id); st != 404 || str(out, "code") != "not_found" {
			t.Fatalf("second delete = %d %v, want 404 (멱등이 아니다)", st, out)
		}
	})
	t.Run("user of another workspace → 404, not 403", func(t *testing.T) {
		id := f.newSessionWithStatus(t, "completed")
		outsider := &client{t: t, srv: f.api.srv}
		_, _, hdr := outsider.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "X", "email": "x-del@example.com", "password": "password123"})
		outsider.cookie = hdr.Get("Set-Cookie")
		outsider.must(201, "POST", f.p+"/workspaces", map[string]any{"name": "Elsewhere"})
		st, out := f.del(outsider, id)
		if st != 404 {
			t.Fatalf("outsider delete = %d %v, want 404 (never reveal another workspace's session)", st, out)
		}
	})
	t.Run("task token → 403 (사람만)", func(t *testing.T) {
		id := f.newSessionWithStatus(t, "completed")
		tok := f.taskTokenFor(t, mustUUID(t, id), f.leadUUID)
		if st, _ := f.del(tok, id); st != 403 {
			t.Fatalf("task token delete = %d, want 403", st)
		}
	})
}

// TestP5DeleteSessionStatus — "끝난 세션만 — draft·completed·cancelled.
// active·paused·completing 이면 409 session_active" with the contract's sentence.
func TestP5DeleteSessionStatus(t *testing.T) {
	f := newMembersFixture(t)
	for _, status := range []string{"active", "paused", "completing"} {
		t.Run(status+" → 409 session_active", func(t *testing.T) {
			id := f.newSessionWithStatus(t, status)
			st, out := f.del(f.api, id)
			if st != 409 || str(out, "code") != "session_active" {
				t.Fatalf("= %d %v", st, out)
			}
			if str(out, "detail") != sessions.DeleteActiveDetail {
				t.Fatalf("detail = %q, want the contract's %q", str(out, "detail"), sessions.DeleteActiveDetail)
			}
			if f.count(t, `SELECT count(*) FROM session WHERE id = $1`, id) != 1 {
				t.Fatalf("%s session was deleted", status)
			}
		})
	}
	for _, status := range []string{"draft", "completed", "cancelled"} {
		t.Run(status+" → 204", func(t *testing.T) {
			id := f.newSessionWithStatus(t, status)
			if st, out := f.del(f.api, id); st != 204 {
				t.Fatalf("= %d %v", st, out)
			}
			if f.count(t, `SELECT count(*) FROM session WHERE id = $1`, id) != 0 {
				t.Fatalf("%s session still exists", status)
			}
		})
	}
	// The table is the rule; a status the enum grows later must be added here
	// on purpose, not deleted by default.
	for status, want := range map[string]bool{"draft": true, "completed": true, "cancelled": true,
		"active": false, "paused": false, "completing": false, "": false, "archived": false} {
		if got := sessions.CanDelete(status); got != want {
			t.Errorf("CanDelete(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestP5DeleteSessionUnmergedWorktree — "deleted 가 아닌 worktree 가 미병합 커밋
// 또는 미커밋 변경을 갖고 있으면 409 workdir_unmerged, Problem.workdirs[] 에 대상".
func TestP5DeleteSessionUnmergedWorktree(t *testing.T) {
	f := newMembersFixture(t)
	ctx := t.Context()
	id := f.newSessionWithStatus(t, "cancelled")
	sid := mustUUID(t, id)
	rtID := worktreeSessionOn(t, f.p2Fixture, sid)

	insert := func(path string, merged *bool, treeDirty bool, ahead int, status string) uuid.UUID {
		var wdID uuid.UUID
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO workdir (session_id, agent_id, kind, path_or_ref, branch, status, merged, tree_dirty, commits_ahead, created_at, updated_at)
			VALUES ($1, $2, 'worktree', $3, 'colab/x', $4::workdir_status, $5, $6, $7, now(), now()) RETURNING id`,
			sid, f.lead, path, status, merged, treeDirty, ahead).Scan(&wdID); err != nil {
			t.Fatal(err)
		}
		return wdID
	}
	fals, tru := false, true
	unmerged := insert("/w/worktrees/s/lead", &fals, false, 2, "active")
	dirty := insert("/w/worktrees/s/r", &tru, true, 0, "retained")
	// Two rows that must NOT block: a merged clean one, and an unmerged one
	// the daemon already deleted.
	insert("/w/worktrees/s/w", &tru, false, 0, "active")
	insert("/w/worktrees/s/gone", &fals, true, 3, "deleted")

	st, out := f.del(f.api, id)
	if st != 409 || str(out, "code") != "workdir_unmerged" {
		t.Fatalf("= %d %v, want 409 workdir_unmerged", st, out)
	}
	if str(out, "detail") != sessions.DeleteUnmergedDetail {
		t.Errorf("detail = %q", str(out, "detail"))
	}
	raw, ok := out["workdirs"].([]any)
	if !ok || len(raw) != 2 {
		t.Fatalf("Problem.workdirs = %v, want the 2 blocking rows", out["workdirs"])
	}
	got := map[string]map[string]any{}
	for _, r := range raw {
		wd := r.(map[string]any)
		got[str(wd, "id")] = wd
	}
	for _, want := range []uuid.UUID{unmerged, dirty} {
		wd, ok := got[want.String()]
		if !ok {
			t.Fatalf("workdirs[] lacks %s: %v", want, got)
		}
		// The rows are the contract's Workdir: the fields S13 draws are there.
		for _, k := range []string{"kind", "path_or_ref", "status", "merged", "commits_ahead", "session"} {
			if _, ok := wd[k]; !ok {
				t.Errorf("workdirs[%s] lacks %q: %v", want, k, wd)
			}
		}
		if str(wd, "kind") != "worktree" || str(wd, "session_id") != id {
			t.Errorf("workdirs[%s] = %v", want, wd)
		}
	}
	if f.count(t, `SELECT count(*) FROM session WHERE id = $1`, id) != 1 {
		t.Fatal("session deleted despite the unmerged worktree")
	}
	if f.count(t, `SELECT count(*) FROM daemon_command WHERE runtime_id = $1 AND type = 'gc'`, rtID) != 0 {
		t.Fatal("a gc command was queued by a refused delete")
	}

	// The blocking rows are resolved (the daemon reports merged, clean) and
	// the delete goes through — with a gc for every remaining row.
	if _, err := f.pool.Exec(ctx, `UPDATE workdir SET merged = true, tree_dirty = false, dirty = false, commits_ahead = 0 WHERE id = ANY($1)`,
		[]uuid.UUID{unmerged, dirty}); err != nil {
		t.Fatal(err)
	}
	if st, out := f.del(f.api, id); st != 204 {
		t.Fatalf("delete after merge = %d %v", st, out)
	}
	var payload string
	if err := f.pool.QueryRow(ctx, `SELECT payload::text FROM daemon_command WHERE runtime_id = $1 AND type = 'gc'`, rtID).Scan(&payload); err != nil {
		t.Fatalf("exactly one gc command expected: %v", err)
	}
	for _, p := range []string{"/w/worktrees/s/lead", "/w/worktrees/s/r", "/w/worktrees/s/w"} {
		if !strings.Contains(payload, p) {
			t.Errorf("gc payload lacks %s: %s", p, payload)
		}
	}
	if strings.Contains(payload, "/w/worktrees/s/gone") {
		t.Errorf("gc payload names the already-deleted row: %s", payload)
	}
}

// TestP5DeleteSessionCascade — "물리 삭제: 메시지·작업 줄기·할 일·활동 기록·확인
// 요청·아티팩트(파일 포함)·결정·받은 요청 항목·비용 기록이 함께 사라지고
// 워크스페이스 비용·지표 집계에서도 빠진다. activity_log 에 session.deleted 한 줄".
func TestP5DeleteSessionCascade(t *testing.T) {
	f := newMembersFixture(t)
	ctx := t.Context()
	id := f.newSessionWithStatus(t, "active") // rows are written while it runs; it ends below
	sid := mustUUID(t, id)
	keep := f.newSessionWithStatus(t, "completed") // the neighbour that must survive
	f.addUsage(t, keep, 0.25)

	// Rows in every table that hangs off the session.
	taskID := f.addUsage(t, id, 1.5)
	f.fake.Advance(1)
	f.api.must(201, "POST", f.p+"/sessions/"+id+"/messages", map[string]any{"content": "/note 남길 말"}, "Idempotency-Key", uuid.NewString())
	tok := f.taskTokenFor(t, sid, f.leadUUID)
	artID := f.submitArtifactBody(t, sid, tok.bearer, "report", "diff --git a/x b/x\n+hello\n")
	f.setStatus(t, id, "completed")
	var oid uint32
	if err := f.pool.QueryRow(ctx, `SELECT substr(storage_ref, 6)::oid FROM artifact WHERE id = $1`, artID).Scan(&oid); err != nil {
		t.Fatalf("artifact storage_ref: %v", err)
	}
	if f.count(t, `SELECT count(*) FROM pg_largeobject_metadata WHERE oid = $1`, oid) != 1 {
		t.Fatal("artifact large object not stored")
	}
	for _, sql := range []string{
		`INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, status, created_at, due_at) VALUES ($1, $2, 'agent', 'approval', 'q', 'director', 'open', now(), now())`,
		`INSERT INTO decision (session_id, summary, rationale, source, ref_id, created_at) VALUES ($1, 's', 'r', 'agent', $2, now())`,
		`INSERT INTO activity_log (workspace_id, session_id, actor_type, action, object_id, created_at) VALUES ((SELECT workspace_id FROM session WHERE id = $1), $1, 'system', 'session.started', $2, now())`,
		`INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at) SELECT m.id, 'hitl_request', 'action_required', $1, $2, now() FROM member m WHERE m.user_id = (SELECT director_user_id FROM session WHERE id = $1) LIMIT 1`,
		`INSERT INTO session_hop (session_id, to_agent_id, rule, created_at) VALUES ($1, (SELECT agent_id FROM task WHERE id = $2), 1, now())`,
	} {
		if _, err := f.pool.Exec(ctx, sql, id, taskID); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	tables := []string{"session_participant", "lane", "task", "task_usage", "message", "hitl_request", "artifact", "decision", "inbox_item", "session_hop", "task_token"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = f.count(t, `SELECT count(*) FROM `+tb+` t WHERE `+sessionJoin(tb)+` = $1`, id)
		if before[tb] == 0 {
			t.Fatalf("fixture left %s empty — the cascade test would prove nothing", tb)
		}
	}
	if f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1`, id) != 1 {
		t.Fatal("activity_log fixture")
	}
	// ON DELETE SET NULL would keep the session's lines with session_id NULL:
	// count the orphans before so the check after can tell them from the one
	// line the delete is allowed to add.
	orphansBefore := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1 AND session_id IS NULL AND action <> 'session.deleted'`, f.wsID)
	wsActBefore := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1`, f.wsID)

	// Cost and metrics before: the session is counted.
	costBefore := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/cost", nil)
	if !bySessionHas(costBefore, id) || !bySessionHas(costBefore, keep) {
		t.Fatalf("cost before = %v", costBefore["by_session"])
	}
	if n := metricN(t, f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/metrics", nil), "auto_complete_rate"); n != 2 {
		t.Fatalf("auto_complete_rate n before = %d, want 2 completed sessions", n)
	}

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream")
	defer stop()
	if st, out := f.del(f.api, id); st != 204 {
		t.Fatalf("delete = %d %v", st, out)
	}

	for _, tb := range tables {
		if n := f.count(t, `SELECT count(*) FROM `+tb+` t WHERE `+sessionJoin(tb)+` = $1`, id); n != 0 {
			t.Errorf("%s still has %d row(s) of the deleted session", tb, n)
		}
	}
	if f.count(t, `SELECT count(*) FROM pg_largeobject_metadata WHERE oid = $1`, oid) != 0 {
		t.Error("artifact large object orphaned — 0008's trigger did not fire on the cascade")
	}
	// activity_log: the session's own lines are gone; exactly one
	// session.deleted line remains, with session_id NULL (its FK target is
	// gone) and the id in object_id + payload.
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1 OR (object_id = $1 AND action <> 'session.deleted')`, id); n != 0 {
		t.Errorf("activity_log keeps %d line(s) of the deleted session", n)
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1 AND session_id IS NULL AND action <> 'session.deleted'`, f.wsID); n != orphansBefore {
		t.Errorf("activity_log has %d orphaned line(s) with session_id NULL (was %d) — the FK is SET NULL; the rows must be deleted by hand", n, orphansBefore)
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1`, f.wsID); n != wsActBefore-1+1 {
		t.Errorf("workspace activity_log = %d, want %d (the session's 1 line gone, 1 session.deleted added)", n, wsActBefore)
	}
	var payload []byte
	var actorType string
	var actorID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		SELECT actor_type::text, actor_id, payload FROM activity_log
		WHERE workspace_id = $1 AND action = 'session.deleted' AND object_type = 'session' AND object_id = $2`, f.wsID, id).
		Scan(&actorType, &actorID, &payload); err != nil {
		t.Fatalf("session.deleted activity line: %v", err)
	}
	var pl map[string]any
	_ = json.Unmarshal(payload, &pl)
	if actorType != "user" || str(pl, "title") != "삭제 대상 active" || str(pl, "session_id") != id || str(pl, "actor") != actorID.String() {
		t.Errorf("session.deleted line = %s %s %s", actorType, actorID, payload)
	}
	if f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'session.deleted'`) != 1 {
		t.Error("more than one session.deleted line")
	}

	// REST after: 404 · gone from the list · gone from cost · gone from metrics.
	if st, out, _ := f.api.do("GET", f.p+"/sessions/"+id, nil); st != 404 {
		t.Errorf("getSession after delete = %d %v", st, out)
	}
	for _, item := range f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/sessions", nil)["items"].([]any) {
		if str(item.(map[string]any), "id") == id {
			t.Error("listSessions still lists the deleted session")
		}
	}
	costAfter := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/cost", nil)
	if bySessionHas(costAfter, id) || !bySessionHas(costAfter, keep) {
		t.Errorf("cost after = %v", costAfter["by_session"])
	}
	if got, want := costAfter["total_usd"].(float64), 0.25; got < want-1e-6 || got > want+1e-6 {
		t.Errorf("total_usd after = %v, want %v (the neighbour only)", got, want)
	}
	if n := metricN(t, f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/metrics", nil), "auto_complete_rate"); n != 1 {
		t.Errorf("auto_complete_rate n after = %d, want 1", n)
	}
	// The neighbour is untouched.
	if st, _, _ := f.api.do("GET", f.p+"/sessions/"+keep, nil); st != 200 {
		t.Errorf("neighbour session = %d", st)
	}

	// SSE `session.deleted {session_id}` — persisted, so it is in stream_event
	// for backfill, and delivered to the workspace stream.
	fr := waitFrame(t, frames, "session.deleted", func(p json.RawMessage) bool {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		return str(m, "session_id") == id
	})
	if fr == nil {
		t.Fatal("no session.deleted frame")
	}
	if f.count(t, `SELECT count(*) FROM stream_event WHERE type = 'session.deleted' AND session_id = $1`, id) != 1 {
		t.Error("session.deleted not persisted for backfill")
	}
}

func sessionJoin(table string) string {
	switch table {
	case "task_usage":
		return `(SELECT session_id FROM task WHERE task.id = t.task_id)`
	case "task_token":
		return `t.session_id`
	}
	return `t.session_id`
}

func bySessionHas(cost map[string]any, id string) bool {
	items, _ := cost["by_session"].([]any)
	for _, it := range items {
		if str(it.(map[string]any), "id") == id {
			return true
		}
	}
	return false
}

func metricN(t *testing.T, out map[string]any, key string) int {
	t.Helper()
	for _, m := range out["metrics"].([]any) {
		mm := m.(map[string]any)
		if str(mm, "key") == key {
			return int(mm["n"].(float64))
		}
	}
	t.Fatalf("metric %s missing: %v", key, out)
	return 0
}

// TestP5DeleteSessionGC — "그 외 workdir 은 서버가 데몬에 gc 명령을 싣고 행을
// 지운다 — 데몬의 §6 보고 행이 이미 없는 workdir 을 가리키면 서버는 조용히
// 소비한다(v0.8.1)". The claim response carries the command; the receipt for
// a row that no longer exists is 200, consumes it, and leaves no feed line.
func TestP5DeleteSessionGC(t *testing.T) {
	f := newMembersFixture(t)
	ctx := t.Context()
	id := f.newSessionWithStatus(t, "completed")
	sid := mustUUID(t, id)
	rtID := worktreeSessionOn(t, f.p2Fixture, sid)
	d := f.daemonFor(t, rtID)
	url := "/v1/daemon/runtimes/" + rtID.String() + "/workdirs"

	// The daemon reported the worktree (merged, clean) — the normal §6 row.
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/s/lead", "session_id": id, "agent_id": f.lead, "bytes": 10,
		"git": map[string]any{"branch": "colab/s/lead", "merged": true, "dirty": false, "commits_ahead": 0},
	}}})
	var wdID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id FROM workdir WHERE session_id = $1`, sid).Scan(&wdID); err != nil {
		t.Fatal(err)
	}
	// A second, unrelated gc command for another session's workdir must not
	// be consumed by the deleted session's receipt.
	other := f.newSessionWithStatus(t, "completed")
	otherSID := mustUUID(t, other)
	if _, err := f.pool.Exec(ctx, `UPDATE session SET runtime_id = $2, isolation = '{"kind":"worktree","repo_path":"/Users/x/app"}' WHERE id = $1`, otherSID, rtID); err != nil {
		t.Fatal(err)
	}
	var otherWD uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO workdir (session_id, agent_id, kind, path_or_ref, status, created_at, updated_at)
		VALUES ($1, $2, 'worktree', '/w/worktrees/o/lead', 'active', now(), now()) RETURNING id`, otherSID, f.lead).Scan(&otherWD); err != nil {
		t.Fatal(err)
	}
	otherCmd, _ := workdirs.BuildGCCommand(otherSID, []uuid.UUID{otherWD}, []string{"/w/worktrees/o/lead"})
	if err := tokens.QueueCommand(ctx, f.pool, rtID, otherCmd); err != nil {
		t.Fatal(err)
	}

	if st, out := f.del(f.api, id); st != 204 {
		t.Fatalf("delete = %d %v", st, out)
	}
	if f.count(t, `SELECT count(*) FROM workdir WHERE id = $1`, wdID) != 0 {
		t.Fatal("workdir row survived the session")
	}

	// The gc rides the next claim response, with {id, path} (§4.3 v0.7).
	claim := d.must(200, "POST", "/v1/daemon/runtimes/"+rtID.String()+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	var gc map[string]any
	for _, c := range claim["commands"].([]any) {
		cm := c.(map[string]any)
		if str(cm, "type") == "gc" && str(cm, "session_id") == id {
			gc = cm
		}
	}
	if gc == nil {
		t.Fatalf("claim commands lack the deleted session's gc: %v", claim["commands"])
	}
	targets := gc["workdirs"].([]any)
	if len(targets) != 1 || str(targets[0].(map[string]any), "id") != wdID.String() || str(targets[0].(map[string]any), "path") != "/w/worktrees/s/lead" {
		t.Fatalf("gc.workdirs = %v", targets)
	}

	// Any report of ANY kind before the receipt must not consume it (the
	// row's absence is not the receipt — the test-chat lesson).
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/o/lead", "session_id": other, "agent_id": f.lead, "bytes": 10,
	}}})
	if n := f.count(t, `SELECT count(*) FROM daemon_command WHERE runtime_id = $1 AND type = 'gc' AND consumed_at IS NULL`, rtID); n != 2 {
		t.Fatalf("unconsumed gc commands after an unrelated report = %d, want 2", n)
	}

	// The receipt, in the daemon's actual shape: session_id echoed from the
	// command, the id inside `gc`, no top-level id. 200 — consumed — silent.
	eventsBefore := f.count(t, `SELECT count(*) FROM task_event`)
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/s/lead", "session_id": id, "bytes": 0,
		"gc": map[string]any{"status": "deleted", "id": wdID.String()},
	}}})
	var consumedBy *string
	if err := f.pool.QueryRow(ctx, `SELECT consumed_by FROM daemon_command WHERE runtime_id = $1 AND type = 'gc' AND session_id = $2`, rtID, sid).Scan(&consumedBy); err != nil {
		t.Fatal(err)
	}
	if consumedBy == nil || *consumedBy != "workdir_report" {
		t.Errorf("deleted session's gc consumed_by = %v, want workdir_report", consumedBy)
	}
	if n := f.count(t, `SELECT count(*) FROM daemon_command WHERE runtime_id = $1 AND type = 'gc' AND session_id = $2 AND consumed_at IS NULL`, rtID, otherSID); n != 1 {
		t.Errorf("the other session's gc was consumed by a receipt that was not about it")
	}
	if n := f.count(t, `SELECT count(*) FROM task_event`); n != eventsBefore {
		t.Errorf("the receipt for a deleted session wrote %d feed line(s) — 피드에 남기지 않는다", n-eventsBefore)
	}
	if f.count(t, `SELECT count(*) FROM workdir WHERE session_id = $1`, sid) != 0 {
		t.Error("the receipt re-created a workdir row for the deleted session")
	}

	// A refusal for a gone row is consumed the same way, and with the id
	// carried only by path.
	id2 := f.newSessionWithStatus(t, "cancelled")
	sid2 := mustUUID(t, id2)
	if _, err := f.pool.Exec(ctx, `UPDATE session SET runtime_id = $2, isolation = '{"kind":"worktree","repo_path":"/Users/x/app"}' WHERE id = $1`, sid2, rtID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO workdir (session_id, agent_id, kind, path_or_ref, status, merged, commits_ahead, created_at, updated_at)
		VALUES ($1, $2, 'worktree', '/w/worktrees/s2/lead', 'retained', true, 0, now(), now())`, sid2, f.lead); err != nil {
		t.Fatal(err)
	}
	if st, out := f.del(f.api, id2); st != 204 {
		t.Fatalf("delete 2 = %d %v", st, out)
	}
	d.must(200, "POST", url, map[string]any{"workdirs": []map[string]any{{
		"kind": "worktree", "path": "/w/worktrees/s2/lead", "session_id": id2, "bytes": 5,
		"gc": map[string]any{"status": "refused", "reason": "worktree_locked"},
	}}})
	if n := f.count(t, `SELECT count(*) FROM daemon_command WHERE runtime_id = $1 AND type = 'gc' AND session_id = $2 AND consumed_at IS NULL`, rtID, sid2); n != 0 {
		t.Errorf("refused receipt (path only) left the gc unconsumed")
	}
	if n := f.count(t, `SELECT count(*) FROM task_event`); n != eventsBefore {
		t.Errorf("the refusal for a deleted session wrote a feed line")
	}

	// The 24h TTL sweep: a cancel command whose task was cascaded away is
	// expired without a feed write (the FK target is gone) and without error.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO daemon_command (runtime_id, type, payload, task_id, attempt, session_id, created_at)
		VALUES ($1, 'cancel', '{"type":"cancel"}', $2, 1, $3, $4)`, rtID, uuid.New(), sid, f.fake.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, err := f.srv.ExpireCommands(ctx); err != nil || n < 1 {
		t.Fatalf("ExpireCommands = %d %v", n, err)
	}
	if n := f.count(t, `SELECT count(*) FROM task_event`); n != eventsBefore {
		t.Errorf("TTL expiry of a deleted task's command wrote a feed line")
	}
}

// TestP5DeleteSentencesMatchContract — the 409 `session_active` detail is the
// contract's own sentence (openapi deleteSession description), not a paraphrase.
func TestP5DeleteSentencesMatchContract(t *testing.T) {
	raw, err := os.ReadFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Skipf("contract not reachable from this checkout: %v", err)
	}
	re := regexp.MustCompile(`operationId: deleteSession[\s\S]*?code: session_active` + "`" + `, "([^"]+)"`)
	m := re.FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("openapi deleteSession no longer carries the session_active sentence in the expected shape")
	}
	if sessions.DeleteActiveDetail != m[1] {
		t.Fatalf("DeleteActiveDetail = %q, contract says %q", sessions.DeleteActiveDetail, m[1])
	}
	if !strings.Contains(sessions.DeleteUnmergedDetail, "작업 폴더") || !strings.Contains(sessions.DeleteUnmergedDetail, "병합") {
		t.Fatalf("DeleteUnmergedDetail = %q — must name the 작업 폴더 and the merge", sessions.DeleteUnmergedDetail)
	}
}
