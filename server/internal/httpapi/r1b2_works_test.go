package httpapi

// T-R1b2: the mission API (openapi 0.2.x `works` tag) against a real
// database, with `work_room_single` gone — several missions in one room.
// Every test here builds an actual 1:N room (TestR1b3OneToMany's point: a
// join that fans out passes on a 1:1 fixture).

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// worksRoom is a room made by createRoom (no legacy mission) with two agents
// and the plain member as participants.
func (f *roomsFixture) worksRoom(t *testing.T) string {
	t.Helper()
	rid := str(f.mkRoom(t, f.api, "미션 방"), "id")
	for _, a := range []string{f.lead, f.r} {
		f.api.must(201, "POST", f.roomPath(rid)+"/participants", map[string]any{"agent_id": a})
	}
	f.api.must(201, "POST", f.roomPath(rid)+"/participants", map[string]any{"user_id": f.memberUserID})
	return rid
}

func (f *roomsFixture) openWork(t *testing.T, c *client, rid string, body map[string]any) map[string]any {
	t.Helper()
	f.fake.Advance(time.Minute)
	return c.must(201, "POST", f.roomPath(rid)+"/works", body)
}

func workPath(f *roomsFixture, id string) string { return f.p + "/works/" + id }

func condTypes(w map[string]any) []string {
	var out []string
	var walk func(n any)
	walk = func(n any) {
		m, _ := n.(map[string]any)
		if cs, ok := m["conditions"].([]any); ok {
			for _, c := range cs {
				walk(c)
			}
			return
		}
		out = append(out, str(m, "type"))
	}
	walk(w["completion_condition"])
	return out
}

// TestR1b2WorkLifecycle is FR-2A.1~2A.6 on one room with several missions:
// defaults, the concurrent ceiling, pause/resume/complete/cancel/delete each
// touching ITS mission only, the summary once per mission.
func TestR1b2WorkLifecycle(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	var dirID string
	if err := f.pool.QueryRow(t.Context(), `SELECT owner_user_id::text FROM room WHERE id = $1`, rid).Scan(&dirID); err != nil {
		t.Fatal(err)
	}

	// FR-2A.1: goal only — Director = the person opening it (no room default),
	// no assignee → user_approval alone.
	w1 := f.openWork(t, f.api, rid, map[string]any{"goal": "릴리스 노트 정리\n세부는 나중에"})
	if str(w1, "title") != "릴리스 노트 정리" || str(w1, "director_user_id") != dirID || str(w1, "status") != "active" ||
		str(w1, "my_work_role") != "director" {
		t.Fatalf("w1 = %v", w1)
	}
	if ct := condTypes(w1); len(ct) != 1 || ct[0] != "user_approval" {
		t.Fatalf("no-assignee default condition = %v, want user_approval alone (FR-2A.1)", ct)
	}
	// The member opens the second one with an assignee: its first task starts.
	w2 := f.openWork(t, f.member, rid, map[string]any{"goal": "경쟁사 조사", "assignee_agent_id": f.r, "director_user_id": f.memberUserID})
	if ct := condTypes(w2); len(ct) != 2 || ct[0] != "artifact_submitted" || ct[1] != "user_approval" {
		t.Fatalf("assignee default condition = %v", ct)
	}
	id1, id2 := str(w1, "id"), str(w2, "id")
	if n := f.count(t, `SELECT count(*) FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.work_id = $1 AND l.work_id = $1 AND t.agent_id = $2`, id2, f.r); n != 1 {
		t.Fatalf("assignee kick-off tasks in w2 = %d, want 1", n)
	}
	// A room default Director wins over the opener.
	f.api.must(200, "PATCH", f.roomPath(rid), map[string]any{"default_director_user_id": f.memberUserID})
	w3 := f.openWork(t, f.api, rid, map[string]any{"goal": "세 번째"})
	if str(w3, "director_user_id") != f.memberUserID {
		t.Fatalf("w3 director = %s, want the room default %s", str(w3, "director_user_id"), f.memberUserID)
	}
	id3 := str(w3, "id")

	// FR-2A.5: the ceiling (default 3) — the fourth is 409 with the open ones.
	st, out, _ := f.api.do("POST", f.roomPath(rid)+"/works", map[string]any{"goal": "네 번째"})
	if st != 409 || str(out, "code") != "max_concurrent_works" {
		t.Fatalf("4th mission = %d %v, want 409 max_concurrent_works", st, out)
	}
	if ow, _ := out["open_works"].([]any); len(ow) != 3 {
		t.Fatalf("open_works = %v, want the 3 open missions", out["open_works"])
	}
	// A draft does not run, so it is not counted.
	f.api.must(201, "POST", f.roomPath(rid)+"/works", map[string]any{"goal": "초안", "draft": true})

	list := items(f.api.must(200, "GET", f.roomPath(rid)+"/works", nil))
	if len(list) != 4 {
		t.Fatalf("listWorks = %d items, want 4", len(list))
	}

	// Pause w1: only w1 (its Director's), w2 untouched.
	f.member.must(403, "POST", workPath(f, id1)+"/pause", nil)
	if p := f.api.must(200, "POST", workPath(f, id1)+"/pause", nil); str(p, "status") != "paused" || str(p, "paused_reason") != "director" {
		t.Fatalf("paused w1 = %v", p)
	}
	if g := f.member.must(200, "GET", workPath(f, id2), nil); str(g, "status") != "active" {
		t.Fatalf("w2 after pausing w1 = %s, want active (FR-2A.3 — missions pause alone)", str(g, "status"))
	}
	// Mission-scoped budget stops w2 only: its own limit, not the room's.
	f.member.must(200, "PATCH", workPath(f, id2), map[string]any{"limits": map[string]any{"budget_usd": 5, "time_limit": "PT6H"}})
	if g := f.member.must(200, "GET", workPath(f, id2), nil); g["limits"].(map[string]any)["budget_usd"].(float64) != 5 {
		t.Fatalf("w2 limits = %v", g["limits"])
	}
	f.api.must(200, "POST", workPath(f, id1)+"/resume", nil)

	// Complete w1 then w3: each leaves ITS summary in the room (one per
	// mission — a room-keyed guard would skip the second).
	f.api.must(200, "POST", workPath(f, id1)+"/complete", map[string]any{"confirm": true})
	f.member.must(200, "POST", workPath(f, id3)+"/complete", map[string]any{"confirm": true})
	for _, id := range []string{id1, id3} {
		g := f.api.must(200, "GET", workPath(f, id), nil)
		if str(g, "status") != "completed" || g["summary_message_id"] == nil {
			t.Fatalf("completed %s = %v", id, g)
		}
		if n := f.count(t, `SELECT count(*) FROM message WHERE work_id = $1 AND kind = 'summary' AND summary_range IS NULL`, id); n != 1 {
			t.Fatalf("summaries of %s = %d, want 1", id, n)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM inbox_item i JOIN member m ON m.id = i.member_id WHERE i.type = 'work_completed' AND i.work_id = $1 AND m.user_id = $2`, id1, dirID); n != 1 {
		t.Fatalf("work_completed to w1's Director = %d, want 1", n)
	}
	if g := f.member.must(200, "GET", workPath(f, id2), nil); str(g, "status") != "active" {
		t.Fatalf("w2 after completing w1·w3 = %s, want active", str(g, "status"))
	}
	f.api.must(409, "POST", workPath(f, id1)+"/complete", map[string]any{"confirm": true})
	past := items(f.api.must(200, "GET", f.roomPath(rid)+"/works?status=completed", nil))
	if len(past) != 2 {
		t.Fatalf("past missions = %d, want 2", len(past))
	}

	// Delete: a running mission is 409; a finished one goes and its messages
	// stay in the room with work_id null (FR-2A.6).
	f.member.must(409, "DELETE", workPath(f, id2), nil)
	msgs := f.count(t, `SELECT count(*) FROM message WHERE work_id = $1`, id1)
	f.api.must(204, "DELETE", workPath(f, id1), nil)
	f.api.must(404, "GET", workPath(f, id1), nil)
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND work_id IS NULL AND kind = 'summary'`, rid); n < 1 || msgs == 0 {
		t.Fatalf("messages of the deleted mission: %d kept with work_id null (had %d)", n, msgs)
	}

	// Cancel w2 (its Director): its tasks stop, the room stays.
	c := f.member.must(200, "POST", workPath(f, id2)+"/cancel", nil)
	if str(c, "status") != "cancelled" {
		t.Fatalf("cancel = %v", c)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE work_id = $1 AND status NOT IN ('cancelled', 'completed', 'failed')`, id2); n != 0 {
		t.Fatalf("w2 live tasks after cancel = %d", n)
	}

	// Subscription (the caller's own) and Director hand-over.
	f.member.must(204, "PUT", workPath(f, id2)+"/subscription", map[string]any{"level": "hitl_only"})
	if g := f.member.must(200, "GET", workPath(f, id2), nil); str(g, "subscription") != "hitl_only" {
		t.Fatalf("subscription = %v", g["subscription"])
	}
	f.member.must(204, "PUT", workPath(f, id2)+"/subscription", map[string]any{"level": nil})
	if n := f.count(t, `SELECT count(*) FROM work_subscription WHERE work_id = $1`, id2); n != 0 {
		t.Fatalf("null level must clear the row, %d left", n)
	}
	f.other.must(403, "PUT", workPath(f, id2)+"/director", map[string]any{"director_user_id": f.otherUserID})
	if g := f.admin.must(200, "PUT", workPath(f, id2)+"/director", map[string]any{"director_user_id": dirID}); str(g, "director_user_id") != dirID {
		t.Fatalf("changeWorkDirector by an admin = %v", g["director_user_id"])
	}
}

// TestR1b2FromMessage is FR-3.1.1's 사후 귀속 (「이걸 미션으로」): the message
// and the rest of its thread with no mission join the new one, once, with
// the tasks and lanes that thread started; a message already in a mission is
// 409 message_has_work.
func TestR1b2FromMessage(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	post := func(body map[string]any) map[string]any {
		f.fake.Advance(time.Minute)
		return f.api.must(201, "POST", f.p+"/sessions/"+rid+"/messages", body, "Idempotency-Key", uuid.NewString())
	}
	// A room made by createRoom has no old-session mission: an old-style
	// post (no work_id key) is honestly "no mission" (T-R1b2 narrowed the
	// legacy rule to old-path rooms).
	root := post(map[string]any{"content": router.MentionLink("R", f.rUUID) + " 이 버그 좀 봐 주세요"})
	rootID := str(root["message"].(map[string]any), "id")
	reply := post(map[string]any{"content": "재현 절차 첨부", "parent_id": rootID})
	replyID := str(reply["message"].(map[string]any), "id")
	if n := f.count(t, `SELECT count(*) FROM message WHERE id IN ($1, $2) AND work_id IS NULL`, rootID, replyID); n != 2 {
		t.Fatalf("thread before 이걸 미션으로: %d without mission, want 2", n)
	}
	taskID := str(root["triggers"].([]any)[0].(map[string]any), "task_id")

	w := f.openWork(t, f.api, rid, map[string]any{"goal": "버그 고치기", "from_message_id": replyID, "assignee_agent_id": f.r})
	wid := str(w, "id")
	if str(w, "opened_from_message_id") != replyID {
		t.Fatalf("opened_from_message_id = %v", w["opened_from_message_id"])
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE id IN ($1, $2) AND work_id = $3`, rootID, replyID, wid); n != 2 {
		t.Fatalf("thread adopted = %d of 2", n)
	}
	var tw, lw *string
	_ = f.pool.QueryRow(t.Context(), `SELECT t.work_id::text, l.work_id::text FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, taskID).Scan(&tw, &lw)
	if tw == nil || *tw != wid || lw == nil || *lw != wid {
		t.Fatalf("thread's task/lane mission = %v/%v, want %s", tw, lw, wid)
	}
	// The assignee already works that thread's lane in the mission: no second
	// kick-off lane.
	if n := f.count(t, `SELECT count(*) FROM lane WHERE work_id = $1 AND agent_id = $2`, wid, f.r); n != 1 {
		t.Fatalf("assignee lanes in the adopted mission = %d, want 1 (no duplicate kick-off)", n)
	}
	st, out, _ := f.api.do("POST", f.roomPath(rid)+"/works", map[string]any{"goal": "또", "from_message_id": rootID})
	if st != 409 || str(out, "code") != "message_has_work" {
		t.Fatalf("second 이걸 미션으로 = %d %v, want 409 message_has_work", st, out)
	}
	// After it: a reply in that thread follows the thread's mission (rule 2).
	later := post(map[string]any{"content": "고쳤나요?", "parent_id": rootID})
	if n := f.count(t, `SELECT count(*) FROM message WHERE id = $1 AND work_id = $2`, str(later["message"].(map[string]any), "id"), wid); n != 1 {
		t.Fatal("a later reply does not follow the thread's mission")
	}
}

// TestR1b2Proposals is FR-2A.1's agent path: an agent proposes (TaskToken),
// a person opens it and becomes its Director, a second decision is 409
// already_resolved, a refusal tells the agent on the timeline.
func TestR1b2Proposals(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	lead := f.taskTokenFor(t, mustUUID(t, rid), f.leadUUID)

	f.api.must(403, "POST", f.roomPath(rid)+"/work-proposals", map[string]any{"goal": "g", "rationale": "r"})
	lead.must(422, "POST", f.roomPath(rid)+"/work-proposals", map[string]any{"goal": "g", "rationale": " "})
	p1 := lead.must(201, "POST", f.roomPath(rid)+"/work-proposals", map[string]any{"goal": "성능 회귀 추적", "rationale": "주간 지표가 20% 나빠졌다"})
	p2 := lead.must(201, "POST", f.roomPath(rid)+"/work-proposals", map[string]any{"goal": "문서 정리", "rationale": "중복이 많다"})
	if str(p1, "status") != "open" || str(p1["agent"].(map[string]any), "id") != f.lead {
		t.Fatalf("proposal = %v", p1)
	}
	// 받은 요청 work_proposed: every person in the room.
	for _, u := range []string{f.memberUserID} {
		if n := f.count(t, `SELECT count(*) FROM inbox_item i JOIN member m ON m.id = i.member_id WHERE i.type = 'work_proposed' AND i.ref_id = $1 AND m.user_id = $2`, str(p1, "id"), u); n != 1 {
			t.Fatalf("work_proposed for %s = %d", u, n)
		}
	}
	if l := items(f.member.must(200, "GET", f.roomPath(rid)+"/work-proposals?status=open", nil)); len(l) != 2 {
		t.Fatalf("open proposals = %d, want 2", len(l))
	}
	// The member opens it: THEY direct it (not the agent, not the room default).
	res := f.member.must(200, "POST", f.p+"/work-proposals/"+str(p1, "id")+"/resolution", map[string]any{"action": "accept"})
	wk := res["work"].(map[string]any)
	if str(wk, "director_user_id") != f.memberUserID || str(wk, "goal") != "성능 회귀 추적" ||
		str(res["proposal"].(map[string]any), "status") != "accepted" || str(res["proposal"].(map[string]any), "work_id") != str(wk, "id") {
		t.Fatalf("accept = %v", res)
	}
	st, out, _ := f.api.do("POST", f.p+"/work-proposals/"+str(p1, "id")+"/resolution", map[string]any{"action": "reject", "reason": "늦었다"})
	if st != 409 || str(out, "code") != "already_resolved" || str(out, "status") != "accepted" {
		t.Fatalf("second decision = %d %v, want 409 already_resolved", st, out)
	}
	f.api.must(200, "POST", f.p+"/work-proposals/"+str(p2, "id")+"/resolution", map[string]any{"action": "reject", "reason": "이번 분기 범위 밖"})
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '%Lead 의 미션 제안 「문서 정리」을 거절했습니다. 사유: 이번 분기 범위 밖'`, rid); n != 1 {
		t.Fatal("refusal line to the agent missing")
	}
	if g := f.member.must(200, "GET", f.p+"/work-proposals/"+str(p2, "id"), nil); str(g, "status") != "rejected" || str(g, "reject_reason") != "이번 분기 범위 밖" {
		t.Fatalf("rejected proposal = %v", g)
	}
	// Invisible to a person not in an invited room.
	f.api.must(200, "PATCH", f.roomPath(rid), map[string]any{"visibility": "invited"})
	f.other.must(404, "GET", f.p+"/work-proposals/"+str(p2, "id"), nil)
}

// TestR1b2LegacySessionInAManyMissionRoom: an old-path room (createSession)
// that opens a second mission through the works API keeps its old
// `/sessions/{id}` shape pinned to ITS mission (room.legacy_work_id), and
// the old paths act on that mission alone.
func TestR1b2LegacySessionInAManyMissionRoom(t *testing.T) {
	f := newRoomsFixture(t)
	legacy := str(f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil), "title")
	var legacyWork string
	if err := f.pool.QueryRow(t.Context(), `SELECT legacy_work_id::text FROM room WHERE id = $1`, f.sessionID).Scan(&legacyWork); err != nil {
		t.Fatal(err)
	}
	w2 := f.openWork(t, f.api, f.sessionID, map[string]any{"goal": "새 미션"})
	for i := 0; i < 3; i++ {
		s := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
		if str(s, "title") != legacy {
			t.Fatalf("GET /sessions/{id} title = %q, want the session's own %q (never the other mission)", str(s, "title"), legacy)
		}
	}
	// Pausing the session pauses its mission, not the new one.
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/pause", nil)
	if g := f.api.must(200, "GET", workPath(f, str(w2, "id")), nil); str(g, "status") != "active" {
		t.Fatalf("new mission after pauseSession = %s, want active", str(g, "status"))
	}
	if g := f.api.must(200, "GET", workPath(f, legacyWork), nil); str(g, "status") != "paused" {
		t.Fatalf("session mission after pauseSession = %s", str(g, "status"))
	}
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/resume", nil)
	// deleteSession deletes the room — refused while the other mission runs.
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/cancel", nil)
	if st, out, _ := f.api.do("DELETE", f.p+"/sessions/"+f.sessionID, nil); st != 409 || str(out, "code") != "session_active" {
		t.Fatalf("deleteSession with an open mission = %d %v, want 409 session_active", st, out)
	}
	// Room-gate mirror (T-R1b1 Q2): the Work response projects the room's
	// reason — paused, paused_reason null — and resumeWork is not the lift.
	f.exec(t, `UPDATE room SET blocked_reason = 'loop' WHERE id = $1`, f.sessionID)
	f.exec(t, `UPDATE work SET status = 'paused', paused_reason = 'loop',
		paused_detail = jsonb_build_object('reason', 'loop', 'paused_at', now(), 'room_blocked', true) WHERE id = $1`, str(w2, "id"))
	g := f.api.must(200, "GET", workPath(f, str(w2, "id")), nil)
	if str(g, "status") != "paused" || g["paused_reason"] != nil {
		t.Fatalf("mirrored pause = %v / %v, want paused with paused_reason null (loop is not a WorkPauseReason)", g["status"], g["paused_reason"])
	}
	if st, out, _ := f.api.do("POST", workPath(f, str(w2, "id"))+"/resume", nil); st != 409 || str(out, "code") != "room_blocked" {
		t.Fatalf("resumeWork on a room-gated mission = %d %v, want 409 room_blocked", st, out)
	}
}

// TestR1b2WorkTimeLimit is FR-2A.3's time ceiling (there was no path before):
// the sweep pauses a mission past `limits.time_limit` with a Director request
// and work_paused, and approving with `time_extension` resumes it on a longer
// ceiling.
func TestR1b2WorkTimeLimit(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	w := f.openWork(t, f.api, rid, map[string]any{"goal": "짧은 일", "limits": map[string]any{"time_limit": "PT1H"}})
	other := f.openWork(t, f.api, rid, map[string]any{"goal": "긴 일"})
	wid := str(w, "id")
	if n, err := f.srv.SweepWorkTimeLimits(t.Context()); err != nil || n != 0 {
		t.Fatalf("sweep before the limit = %d %v", n, err)
	}
	f.fake.Advance(61 * time.Minute)
	if n, err := f.srv.SweepWorkTimeLimits(t.Context()); err != nil || n != 1 {
		t.Fatalf("sweep past the limit = %d %v, want 1", n, err)
	}
	g := f.api.must(200, "GET", workPath(f, wid), nil)
	if str(g, "status") != "paused" || str(g, "paused_reason") != "time" {
		t.Fatalf("mission past its time = %v / %v", g["status"], g["paused_reason"])
	}
	if o := f.api.must(200, "GET", workPath(f, str(other, "id")), nil); str(o, "status") != "active" {
		t.Fatal("the other mission must not pause")
	}
	var hid string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE work_id = $1 AND purpose = 'time' AND status = 'open'`, wid).Scan(&hid); err != nil {
		t.Fatalf("time request: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM inbox_item WHERE type = 'work_paused' AND work_id = $1`, wid); n != 1 {
		t.Fatalf("work_paused = %d, want 1", n)
	}
	if n, _ := f.srv.SweepWorkTimeLimits(t.Context()); n != 0 {
		t.Fatal("a paused mission is swept again")
	}
	f.api.must(422, "POST", f.p+"/hitl-requests/"+hid+"/response", map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hid+"/response", map[string]any{"approved": true, "time_extension": "PT2H"}, "Idempotency-Key", uuid.NewString())
	g = f.api.must(200, "GET", workPath(f, wid), nil)
	if str(g, "status") != "active" || str(g["limits"].(map[string]any), "time_limit") != "PT3H" {
		t.Fatalf("after the extension = %v / %v, want active on PT3H", g["status"], g["limits"])
	}
}

// TestR1b2DirectorSuccession is openapi 0.2.3 removeMember (PRD §12.1-4): the
// leaving person's room goes to its deputy, and the open mission they
// directed to that room's (new) owner — no 409.
func TestR1b2DirectorSuccession(t *testing.T) {
	f := newRoomsFixture(t)
	rid := str(f.mkRoom(t, f.member, "Mem 의 방"), "id")
	f.member.must(201, "POST", f.roomPath(rid)+"/participants", map[string]any{"user_id": f.otherUserID})
	f.member.must(200, "PUT", f.roomPath(rid)+"/deputy", map[string]any{"user_id": f.otherUserID})
	w := f.openWork(t, f.member, rid, map[string]any{"goal": "Mem 의 미션"})
	done := f.openWork(t, f.member, rid, map[string]any{"goal": "끝난 미션"})
	f.member.must(200, "POST", workPath(f, str(done, "id"))+"/complete", map[string]any{"confirm": true})

	f.api.must(204, "DELETE", f.memberPath(f.memberID), nil)
	var owner, director, doneDirector string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT r.owner_user_id::text, w.director_user_id::text, d.director_user_id::text
		FROM room r, work w, work d WHERE r.id = $1 AND w.id = $2 AND d.id = $3`, rid, str(w, "id"), str(done, "id")).Scan(&owner, &director, &doneDirector); err != nil {
		t.Fatal(err)
	}
	if owner != f.otherUserID {
		t.Fatalf("room owner = %s, want the deputy %s", owner, f.otherUserID)
	}
	if director != owner {
		t.Fatalf("open mission's Director = %s, want the room's new owner %s", director, owner)
	}
	if doneDirector != f.memberUserID {
		t.Fatal("a finished mission's record keeps who directed it")
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND work_id = $2 AND content LIKE '%이 미션의 Director 를 이어받았습니다.'`, rid, str(w, "id")); n != 1 {
		t.Fatal("succession line on the mission's timeline missing")
	}
}

// TestR1b2OneToManyReads: the reads that joined "the room's mission" — cost
// rows, the runtime's active sessions, the S13 directory list — answer once
// per usage row / room / directory when the room holds two missions
// (V19_R1B_HANDOFF (a)·(d)).
func TestR1b2OneToManyReads(t *testing.T) {
	f := newRoomsFixture(t)
	ctx := t.Context()
	var rt string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE room SET runtime_id = $2 WHERE id = $1`, f.sessionID, rt)
	f.openWork(t, f.api, f.sessionID, map[string]any{"goal": "둘째 미션"})
	var task string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM task WHERE session_id = $1 ORDER BY created_at LIMIT 1`, f.sessionID).Scan(&task); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `INSERT INTO task_usage (task_id, cost_usd, input_tokens, output_tokens) VALUES ($1, 1.25, 10, 10)`, task)
	f.exec(t, `INSERT INTO workdir (session_id, lane_id, kind, path_or_ref, status) SELECT $1, id, 'dir', '/tmp/colab/one', 'active' FROM lane WHERE session_id = $1 ORDER BY created_at LIMIT 1`, f.sessionID)

	if c := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID+"/cost", nil); c["total_usd"].(float64) != 1.25 {
		t.Fatalf("session cost = %v, want 1.25 once (not once per mission)", c["total_usd"])
	}
	if c := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/cost", nil); c["total_usd"].(float64) != 1.25 {
		t.Fatalf("workspace cost = %v, want 1.25", c["total_usd"])
	}
	d := f.api.must(200, "GET", f.p+"/runtimes/"+rt, nil)
	if as, _ := d["active_sessions"].([]any); len(as) != 1 {
		t.Fatalf("runtime active_sessions = %v, want one row for the one room", d["active_sessions"])
	}
	if l := items(f.api.must(200, "GET", f.p+"/runtimes/"+rt+"/workdirs", nil)); len(l) != 1 {
		t.Fatalf("runtime workdirs = %d rows, want 1 (one directory)", len(l))
	}
}
