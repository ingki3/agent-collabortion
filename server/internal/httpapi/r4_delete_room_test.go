package httpapi

// T-S17 → R4 — deleteRoom's cascade (FR-2.7). These were deleteSession's
// tests (openapi 0.1.3, Director request 2026-09-14); v0.3.0 (D22) removed
// that op and deleteRoom is the one delete. What they held about the cascade —
// every child row gone, cost and metrics recount, the gc receipt consumed
// silently, the unmerged-worktree 409 — is deleteRoom's now, and is ported
// here as it was. The authorisation rows moved with the op's semantics: the
// room owner deletes (r1b3_rooms_test 「삭제: 부방장 403 · 방장 204」), and the
// status rule is `409 works_active` over the room's missions.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// newSessionWithStatus creates a room with one mission (sessionRoom) in the
// fixture's workspace and forces the mission into `status` — the transitions
// themselves are other operations' tests; deleteRoom only reads the column.
func (f *membersFixture) newSessionWithStatus(t *testing.T, status string) string {
	t.Helper()
	sess := sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{
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
	if _, err := f.pool.Exec(t.Context(), `UPDATE work SET status = $2::session_status, finished_at = `+finished+`, paused_reason = `+paused+` WHERE room_id = $1`, id, status); err != nil {
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
		SELECT $1, $2, p.profile_id, 'done', now(), now() FROM room_participant p
		WHERE p.room_id = $1 AND p.agent_id = $2 RETURNING id`, sessionID, f.lead).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, started_at, finished_at, created_at, updated_at)
		SELECT $1, $2, $3, p.profile_id, 'completed', now() - interval '2 minutes', now(), now(), now() FROM room_participant p
		WHERE p.room_id = $2 AND p.agent_id = $3 RETURNING id`, laneID, sessionID, f.lead).Scan(&taskID); err != nil {
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
	st, out, _ := c.do("DELETE", f.p+"/rooms/"+sessionID, nil)
	return st, out
}

// TestR4DeleteRoomStatus — deleteRoom refuses while any mission is in
// progress (`409 works_active`); a room whose missions are draft · completed ·
// cancelled goes. (deleteSession's `session_active` rule, re-read on the room.)
func TestR4DeleteRoomStatus(t *testing.T) {
	f := newMembersFixture(t)
	for _, status := range []string{"active", "paused", "completing"} {
		t.Run(status+" → 409 works_active", func(t *testing.T) {
			id := f.newSessionWithStatus(t, status)
			st, out := f.del(f.api, id)
			if st != 409 || str(out, "code") != "works_active" {
				t.Fatalf("= %d %v", st, out)
			}
			if str(out, "detail") != sessions.RoomWorksActiveDetail {
				t.Fatalf("detail = %q, want %q", str(out, "detail"), sessions.RoomWorksActiveDetail)
			}
			if f.count(t, `SELECT count(*) FROM room WHERE id = $1`, id) != 1 {
				t.Fatalf("%s room was deleted", status)
			}
		})
	}
	for _, status := range []string{"draft", "completed", "cancelled"} {
		t.Run(status+" → 204", func(t *testing.T) {
			id := f.newSessionWithStatus(t, status)
			if st, out := f.del(f.api, id); st != 204 {
				t.Fatalf("= %d %v", st, out)
			}
			if f.count(t, `SELECT count(*) FROM room WHERE id = $1`, id) != 0 {
				t.Fatalf("%s room still exists", status)
			}
		})
	}
}

// TestR4DeleteRoomUnmergedWorktree — "deleted 가 아닌 worktree 가 미병합 커밋
// 또는 미커밋 변경을 갖고 있으면 409 workdir_unmerged, Problem.workdirs[] 에 대상".
func TestR4DeleteRoomUnmergedWorktree(t *testing.T) {
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
		// deleteRoom's 409 rows (openapi 0.2.6): the folder, the computer and
		// what holds it — what the dialog lists with the S13 link.
		for _, k := range []string{"path", "commits_ahead", "dirty", "runtime_id", "runtime_name"} {
			if _, ok := wd[k]; !ok {
				t.Errorf("workdirs[%s] lacks %q: %v", want, k, wd)
			}
		}
	}
	if f.count(t, `SELECT count(*) FROM room WHERE id = $1`, id) != 1 {
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

// TestR4DeleteRoomCascade — "물리 삭제: 메시지·작업 줄기·할 일·활동 기록·확인
// 요청·아티팩트(파일 포함)·결정·받은 요청 항목·비용 기록이 함께 사라지고
// 워크스페이스 비용·지표 집계에서도 빠진다. activity_log 에 room.deleted 한 줄".
func TestR4DeleteRoomCascade(t *testing.T) {
	f := newMembersFixture(t)
	ctx := t.Context()
	id := f.newSessionWithStatus(t, "active") // rows are written while it runs; it ends below
	sid := mustUUID(t, id)
	keep := f.newSessionWithStatus(t, "completed") // the neighbour that must survive
	f.addUsage(t, keep, 0.25)

	// Rows in every table that hangs off the session.
	taskID := f.addUsage(t, id, 1.5)
	f.fake.Advance(1)
	f.api.must(201, "POST", f.p+"/rooms/"+id+"/messages", map[string]any{"content": "/note 남길 말"}, "Idempotency-Key", uuid.NewString())
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
		`INSERT INTO activity_log (workspace_id, session_id, actor_type, action, object_id, created_at) VALUES ((SELECT workspace_id FROM room WHERE id = $1), $1, 'system', 'session.started', $2, now())`,
		`INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at) SELECT m.id, 'hitl_request', 'action_required', $1, $2, now() FROM member m WHERE m.user_id = (SELECT director_user_id FROM work WHERE room_id = $1) LIMIT 1`,
		`INSERT INTO session_hop (session_id, to_agent_id, rule, created_at) VALUES ($1, (SELECT agent_id FROM task WHERE id = $2), 1, now())`,
	} {
		if _, err := f.pool.Exec(ctx, sql, id, taskID); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	tables := []string{"room_participant", "lane", "task", "task_usage", "message", "hitl_request", "artifact", "decision", "inbox_item", "session_hop", "task_token"}
	before := map[string]int{}
	for _, tb := range tables {
		before[tb] = f.count(t, `SELECT count(*) FROM `+tb+` t WHERE `+sessionJoin(tb)+` = $1`, id)
		if before[tb] == 0 {
			t.Fatalf("fixture left %s empty — the cascade test would prove nothing", tb)
		}
	}
	roomLines := f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1`, id)
	if roomLines < 1 {
		t.Fatal("activity_log fixture")
	}
	// ON DELETE SET NULL would keep the session's lines with session_id NULL:
	// count the orphans before so the check after can tell them from the one
	// line the delete is allowed to add.
	orphansBefore := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1 AND session_id IS NULL AND action <> 'room.deleted'`, f.wsID)
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
	// activity_log: the room's own lines are gone; exactly one room.deleted
	// line remains, with session_id NULL (its FK target is gone) and the id
	// in object_id + payload.
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1 OR (object_id = $1 AND action <> 'room.deleted')`, id); n != 0 {
		t.Errorf("activity_log keeps %d line(s) of the deleted session", n)
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1 AND session_id IS NULL AND action <> 'room.deleted'`, f.wsID); n != orphansBefore {
		t.Errorf("activity_log has %d orphaned line(s) with session_id NULL (was %d) — the FK is SET NULL; the rows must be deleted by hand", n, orphansBefore)
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE workspace_id = $1`, f.wsID); n != wsActBefore-roomLines+1 {
		t.Errorf("workspace activity_log = %d, want %d (the room's %d lines gone, 1 room.deleted added)", n, wsActBefore-roomLines+1, roomLines)
	}
	var payload []byte
	var actorType string
	var actorID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		SELECT actor_type::text, actor_id, payload FROM activity_log
		WHERE workspace_id = $1 AND action = 'room.deleted' AND object_type = 'room' AND object_id = $2`, f.wsID, id).
		Scan(&actorType, &actorID, &payload); err != nil {
		t.Fatalf("room.deleted activity line: %v", err)
	}
	var pl map[string]any
	_ = json.Unmarshal(payload, &pl)
	if actorType != "user" || str(pl, "name") != "삭제 대상 active" || str(pl, "room_id") != id || str(pl, "actor") != actorID.String() {
		t.Errorf("room.deleted line = %s %s %s", actorType, actorID, payload)
	}
	if f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'room.deleted'`) != 1 {
		t.Error("more than one room.deleted line")
	}

	// REST after: 404 · gone from the list · gone from cost · gone from metrics.
	if st, out, _ := f.api.do("GET", f.p+"/rooms/"+id, nil); st != 404 {
		t.Errorf("getRoom after delete = %d %v", st, out)
	}
	for _, item := range f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms?include_archived=true", nil)["items"].([]any) {
		if str(item.(map[string]any), "id") == id {
			t.Error("listRooms still lists the deleted room")
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
	if st, _, _ := f.api.do("GET", f.p+"/rooms/"+keep, nil); st != 200 {
		t.Errorf("neighbour room = %d", st)
	}

	// SSE `room.deleted {room_id}` — persisted, so it is in stream_event for
	// backfill, and delivered to the workspace stream. The old
	// `session.deleted` is not sent any more (openapi v0.3.0, D22).
	fr := waitFrame(t, frames, "room.deleted", func(p json.RawMessage) bool {
		var m map[string]any
		_ = json.Unmarshal(p, &m)
		return str(m, "room_id") == id
	})
	if fr == nil {
		t.Fatal("no room.deleted frame")
	}
	if f.count(t, `SELECT count(*) FROM stream_event WHERE type = 'room.deleted' AND session_id = $1`, id) != 1 {
		t.Error("room.deleted not persisted for backfill")
	}
	if f.count(t, `SELECT count(*) FROM stream_event WHERE type LIKE 'session.%'`) != 0 {
		t.Error("a session.* frame was sent (removed in openapi v0.3.0)")
	}
}

func sessionJoin(table string) string {
	switch table {
	case "task_usage":
		return `(SELECT session_id FROM task WHERE task.id = t.task_id)`
	case "task_token":
		return `t.session_id`
	case "room_participant":
		return `t.room_id`
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

// TestR4DeleteRoomGC — "그 외 workdir 은 서버가 데몬에 gc 명령을 싣고 행을
// 지운다 — 데몬의 §6 보고 행이 이미 없는 workdir 을 가리키면 서버는 조용히
// 소비한다(v0.8.1)". The claim response carries the command; the receipt for
// a row that no longer exists is 200, consumes it, and leaves no feed line.
func TestR4DeleteRoomGC(t *testing.T) {
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
	if _, err := f.pool.Exec(ctx, `UPDATE room SET runtime_id = $2, isolation = '{"kind":"worktree","repo_path":"/Users/x/app"}' WHERE id = $1`, otherSID, rtID); err != nil {
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
	if _, err := f.pool.Exec(ctx, `UPDATE room SET runtime_id = $2, isolation = '{"kind":"worktree","repo_path":"/Users/x/app"}' WHERE id = $1`, sid2, rtID); err != nil {
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

// TestR4DeleteUnmergedSentence — deleteRoom's 409 workdir_unmerged names the
// 작업 폴더 and the merge (the next action, not the rule).
func TestR4DeleteUnmergedSentence(t *testing.T) {
	if !strings.Contains(sessions.DeleteUnmergedDetail, "작업 폴더") || !strings.Contains(sessions.DeleteUnmergedDetail, "병합") {
		t.Fatalf("DeleteUnmergedDetail = %q — must name the 작업 폴더 and the merge", sessions.DeleteUnmergedDetail)
	}
}
