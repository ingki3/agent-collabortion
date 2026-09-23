package httpapi

// T-R1b3: the room API (openapi 0.2.0) against a real database — room,
// roster, links, unread, archive/delete refusals, §12.1-4 succession, the
// per-person SSE frame. The permission table itself is rooms.TestDecideTable
// (pure); these subtests check that the handlers ask it and act on it.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/metrics"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

type roomsFixture struct {
	*membersFixture
}

func newRoomsFixture(t *testing.T) *roomsFixture {
	return &roomsFixture{newMembersFixture(t)}
}

func (f *roomsFixture) mkRoom(t *testing.T, c *client, name string) map[string]any {
	t.Helper()
	return c.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/rooms", map[string]any{"name": name})
}

func (f *roomsFixture) roomPath(id string) string { return f.p + "/rooms/" + id }

func (f *roomsFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func items(m map[string]any) []any {
	if v, ok := m["items"].([]any); ok {
		return v
	}
	return nil
}

func hasRoom(list []any, id string) bool {
	for _, it := range list {
		if str(it.(map[string]any), "id") == id {
			return true
		}
	}
	return false
}

func caps(room map[string]any) map[string]bool {
	out := map[string]bool{}
	if l, ok := room["my_capabilities"].([]any); ok {
		for _, c := range l {
			out[c.(string)] = true
		}
	}
	return out
}

func participantOf(t *testing.T, c *client, path, userID string) string {
	t.Helper()
	for _, it := range items(c.must(200, "GET", path+"/participants", nil)) {
		p := it.(map[string]any)
		if u, ok := p["user"].(map[string]any); ok && str(u, "id") == userID {
			return str(p, "id")
		}
	}
	return ""
}

func TestR1b3RoomLifecycle(t *testing.T) {
	f := newRoomsFixture(t)
	var roomID, rp string

	t.Run("createRoom: 이름 한 칸 → 201, 만든 사람이 방장, 기본값 상속", func(t *testing.T) {
		room := f.mkRoom(t, f.member, "설계 방")
		roomID, rp = str(room, "id"), f.roomPath(str(room, "id"))
		if str(room, "owner_user_id") != f.memberUserID || str(room, "my_room_role") != "owner" || str(room, "visibility") != "workspace" {
			t.Fatalf("room = %v", room)
		}
		if lim := room["limits"].(map[string]any); lim["max_concurrent_works"].(float64) != 3 || lim["max_parallel_lanes"].(float64) != 5 {
			t.Fatalf("limits = %v", lim)
		}
		c := caps(room)
		for _, want := range []string{"post", "invite", "configure", "link", "archive", "delete", "transfer_owner", "summarize"} {
			if !c[want] {
				t.Fatalf("owner capability %s missing: %v", want, room["my_capabilities"])
			}
		}
		st, out, _ := f.member.do("POST", f.p+"/workspaces/"+f.wsID+"/rooms", map[string]any{"name": "  "})
		if st != 422 {
			t.Fatalf("blank name = %d %v", st, out)
		}
	})

	t.Run("room_defaults 가 새 방에 상속된다", func(t *testing.T) {
		f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
			"room_defaults": map[string]any{"visibility": "invited", "limits": map[string]any{"max_concurrent_works": 2}},
		})
		room := f.mkRoom(t, f.member, "비공개 기본")
		if str(room, "visibility") != "invited" || room["limits"].(map[string]any)["max_concurrent_works"].(float64) != 2 {
			t.Fatalf("defaults not inherited: %v", room)
		}
		set := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/settings", nil)
		if rd := set["room_defaults"].(map[string]any); rd["visibility"] != "invited" || rd["limits"].(map[string]any)["max_concurrent_works"].(float64) != 2 {
			t.Fatalf("settings = %v", set)
		}
		f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{"room_defaults": map[string]any{"visibility": "workspace"}})
	})

	t.Run("invited 방은 초대 안 된 멤버에게 없다 · owner 는 감사 열람(기록)", func(t *testing.T) {
		f.member.must(200, "PATCH", rp, map[string]any{"visibility": "invited"})
		if st, _, _ := f.other.do("GET", rp, nil); st != 404 {
			t.Fatalf("uninvited member GET = %d, want 404", st)
		}
		list := items(f.other.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms?participating=false", nil))
		if hasRoom(list, roomID) {
			t.Fatal("invited room listed to an uninvited member")
		}
		room := f.api.must(200, "GET", rp, nil) // Dir = ws owner, not a participant
		if room["my_room_role"] != nil || caps(room)["post"] {
			t.Fatalf("audit view = %v", room)
		}
		if n := f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'room.audit_viewed' AND session_id = $1`, roomID); n != 1 {
			t.Fatalf("room.audit_viewed = %d", n)
		}
		if n := f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'room.visibility_changed' AND session_id = $1`, roomID); n != 1 {
			t.Fatalf("room.visibility_changed = %d", n)
		}
	})

	t.Run("사람 초대 → 201 · room_invited · 참여자는 초대 못 한다", func(t *testing.T) {
		p := f.member.must(201, "POST", rp+"/participants", map[string]any{"user_id": f.otherUserID})
		if str(p, "kind") != "user" || str(p, "room_role") != "member" {
			t.Fatalf("participant = %v", p)
		}
		if n := f.count(t, `SELECT count(*) FROM inbox_item i JOIN member m ON m.id = i.member_id WHERE m.user_id = $1 AND i.type = 'room_invited'`, f.otherUserID); n != 1 {
			t.Fatalf("room_invited items = %d", n)
		}
		inbox := f.other.must(200, "GET", f.p+"/inbox?workspace_id="+f.wsID, nil)
		it := items(inbox)[0].(map[string]any)
		if str(it, "type") != "room_invited" || str(it, "room_id") != roomID || it["actions"].([]any)[0] != "open_room" {
			t.Fatalf("inbox card = %v", it)
		}
		f.other.must(200, "GET", rp, nil)
		st, out, _ := f.other.do("POST", rp+"/participants", map[string]any{"user_id": f.adminID})
		if st != 403 || str(out, "code") != "room_steward_required" {
			t.Fatalf("member invites = %d %v", st, out)
		}
		st, out, _ = f.member.do("POST", rp+"/participants", map[string]any{"user_id": f.otherUserID})
		if st != 409 || str(out, "code") != "already_participant" {
			t.Fatalf("second invite = %d %v", st, out)
		}
		st, _, _ = f.member.do("POST", rp+"/participants", map[string]any{"user_id": uuid.NewString()})
		if st != 422 {
			t.Fatalf("non-member invite = %d, want 422", st)
		}
	})

	t.Run("에이전트 초대는 FR-1.9(respond_to) 를 부른 사람으로 판정", func(t *testing.T) {
		// Lead's respond_to is `owner` (Dir). Mem is the room owner but not
		// the agent's owner.
		st, out, _ := f.member.do("POST", rp+"/participants", map[string]any{"agent_id": f.lead})
		if st != 403 || str(out, "code") != "not_invitable" {
			t.Fatalf("= %d %v", st, out)
		}
		out = f.api.must(201, "POST", rp+"/participants", map[string]any{"agent_id": f.lead})
		if str(out, "kind") != "agent" || out["warnings"] == nil {
			t.Fatalf("agent invite = %v", out)
		}
	})

	t.Run("나가기: 방장 409 is_owner · 본인 204 · 떠난 사람은 재초대로 되돌아온다", func(t *testing.T) {
		me := participantOf(t, f.member, rp, f.memberUserID)
		st, out, _ := f.member.do("DELETE", rp+"/participants/"+me, nil)
		if st != 409 || str(out, "code") != "is_owner" {
			t.Fatalf("owner leaves = %d %v", st, out)
		}
		oth := participantOf(t, f.other, rp, f.otherUserID)
		f.other.must(204, "DELETE", rp+"/participants/"+oth, nil)
		if st, _, _ := f.other.do("GET", rp, nil); st != 404 {
			t.Fatalf("left member still sees invited room: %d", st)
		}
		all := items(f.member.must(200, "GET", rp+"/participants?include_left=true", nil))
		left := 0
		for _, p := range all {
			if p.(map[string]any)["left_at"] != nil {
				left++
			}
		}
		if left != 1 {
			t.Fatalf("include_left rows with left_at = %d", left)
		}
		f.member.must(201, "POST", rp+"/participants", map[string]any{"user_id": f.otherUserID})
		if got := participantOf(t, f.other, rp, f.otherUserID); got != oth {
			t.Fatalf("re-invite made a new row %s (was %s)", got, oth)
		}
	})

	t.Run("방장 넘기기 → 옛 방장은 member · 부방장 지정/해제", func(t *testing.T) {
		st, out, _ := f.member.do("PUT", rp+"/owner", map[string]any{"user_id": f.adminID})
		if st != 422 {
			t.Fatalf("non-participant owner = %d %v", st, out)
		}
		f.member.must(200, "PUT", rp+"/deputy", map[string]any{"user_id": f.otherUserID})
		room := f.other.must(200, "GET", rp, nil)
		if str(room, "my_room_role") != "deputy" || !caps(room)["invite"] || caps(room)["delete"] {
			t.Fatalf("deputy view = %v", room)
		}
		room = f.member.must(200, "PUT", rp+"/owner", map[string]any{"user_id": f.otherUserID})
		if str(room, "owner_user_id") != f.otherUserID || room["deputy_owner_user_id"] != nil || str(room, "my_room_role") != "member" {
			t.Fatalf("after transfer = %v", room)
		}
		f.other.must(200, "PUT", rp+"/deputy", map[string]any{"user_id": nil})
		me := participantOf(t, f.member, rp, f.memberUserID)
		f.member.must(204, "DELETE", rp+"/participants/"+me, nil)
	})

	t.Run("안 읽음: 남의 메시지만 세고 markRoomRead 는 앞으로만", func(t *testing.T) {
		f.other.must(201, "POST", rp+"/participants", map[string]any{"user_id": f.memberUserID})
		f.fake.Advance(time.Minute)
		room := f.member.must(200, "GET", rp, nil)
		n := int(room["unread_count"].(float64))
		if n == 0 {
			t.Fatalf("unread after system lines = 0")
		}
		list := items(f.member.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms", nil))
		for _, it := range list {
			if str(it.(map[string]any), "id") == roomID && int(it.(map[string]any)["unread_count"].(float64)) != n {
				t.Fatalf("list unread %v != getRoom %d", it, n)
			}
		}
		var first, last string
		if err := f.pool.QueryRow(t.Context(), `SELECT (array_agg(id ORDER BY created_at, id))[1], (array_agg(id ORDER BY created_at DESC, id DESC))[1] FROM message WHERE session_id = $1`, roomID).Scan(&first, &last); err != nil {
			t.Fatal(err)
		}
		out := f.member.must(200, "POST", rp+"/read", map[string]any{"last_read_message_id": last})
		if out["unread_count"].(float64) != 0 {
			t.Fatalf("after read = %v", out)
		}
		out = f.member.must(200, "POST", rp+"/read", map[string]any{"last_read_message_id": first})
		if out["unread_count"].(float64) != 0 {
			t.Fatalf("marker moved backwards: %v", out)
		}
		if n := f.count(t, `SELECT count(*) FROM stream_event WHERE type = 'room.unread' AND user_id = $1`, f.memberUserID); n != 2 {
			t.Fatalf("room.unread frames addressed to the reader = %d", n)
		}
	})

	t.Run("여기까지 정리 → 202 summary + 범위 기록, 미션 완료 요약 검사에 안 섞인다", func(t *testing.T) {
		since := t0.Add(-time.Hour).UTC().Format(time.RFC3339)
		msg := f.member.must(202, "POST", rp+"/summaries", map[string]any{"since": since})
		if str(msg, "kind") != "summary" || str(msg, "session_id") != roomID {
			t.Fatalf("summary = %v", msg)
		}
		if n := f.count(t, `SELECT count(*) FROM message WHERE id = $1 AND summary_range ? 'message_ids'`, str(msg, "id")); n != 1 {
			t.Fatal("range not recorded")
		}
		st, _, _ := f.member.do("POST", rp+"/summaries", map[string]any{})
		if st != 422 {
			t.Fatalf("no range = %d", st)
		}
	})
}

func TestR1b3LinksArchiveDelete(t *testing.T) {
	f := newRoomsFixture(t)
	a := f.mkRoom(t, f.member, "A")
	b := f.mkRoom(t, f.other, "B")
	ap, bp := f.roomPath(str(a, "id")), f.roomPath(str(b, "id"))

	t.Run("링크: 대상 방 참여자가 아니면 403 not_participant_of_target", func(t *testing.T) {
		st, out, _ := f.member.do("POST", ap+"/links", map[string]any{"target_room_id": str(b, "id")})
		if st != 403 || str(out, "code") != "not_participant_of_target" {
			t.Fatalf("= %d %v", st, out)
		}
		st, out, _ = f.member.do("POST", ap+"/links", map[string]any{"target_room_id": uuid.NewString()})
		if st != 403 || str(out, "code") != "not_participant_of_target" {
			t.Fatalf("unknown target = %d %v (must not reveal existence)", st, out)
		}
	})
	var linkID string
	t.Run("링크 걸기 → 양쪽 방 시스템 메시지 · SSE · 중복 409", func(t *testing.T) {
		f.other.must(201, "POST", bp+"/participants", map[string]any{"user_id": f.memberUserID})
		before := f.count(t, `SELECT count(*) FROM message WHERE session_id IN ($1, $2) AND kind = 'system'`, str(a, "id"), str(b, "id"))
		link := f.member.must(201, "POST", ap+"/links", map[string]any{"target_room_id": str(b, "id")})
		linkID = str(link, "id")
		if link["target_room"].(map[string]any)["name"] != "B" {
			t.Fatalf("link = %v", link)
		}
		if after := f.count(t, `SELECT count(*) FROM message WHERE session_id IN ($1, $2) AND kind = 'system'`, str(a, "id"), str(b, "id")); after != before+2 {
			t.Fatalf("system lines %d → %d, want +2 (both rooms)", before, after)
		}
		if n := f.count(t, `SELECT count(*) FROM stream_event WHERE type = 'room_link.updated'`); n != 2 {
			t.Fatalf("room_link.updated frames = %d", n)
		}
		st, out, _ := f.member.do("POST", ap+"/links", map[string]any{"target_room_id": str(b, "id")})
		if st != 409 || str(out, "code") != "already_linked" {
			t.Fatalf("dup = %d %v", st, out)
		}
		if len(items(f.member.must(200, "GET", ap+"/links", nil))) != 1 {
			t.Fatal("listRoomLinks")
		}
	})
	t.Run("링크 풀기 — 대상 방의 방장도 풀 수 있다", func(t *testing.T) {
		f.other.must(204, "DELETE", bp+"/links/"+linkID, nil)
		if len(items(f.member.must(200, "GET", ap+"/links", nil))) != 0 {
			t.Fatal("link still listed")
		}
	})

	t.Run("보관 → 초대 409 room_archived · 해제", func(t *testing.T) {
		room := f.member.must(200, "POST", ap+"/archive", nil)
		if str(room, "status") != "archived" {
			t.Fatalf("= %v", room)
		}
		st, out, _ := f.member.do("POST", ap+"/participants", map[string]any{"user_id": f.adminID})
		if st != 409 || str(out, "code") != "room_archived" {
			t.Fatalf("invite into archived = %d %v", st, out)
		}
		if hasRoom(items(f.member.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms", nil)), str(a, "id")) {
			t.Fatal("archived room in default list")
		}
		if !hasRoom(items(f.member.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms?include_archived=true", nil)), str(a, "id")) {
			t.Fatal("include_archived misses it")
		}
		f.member.must(200, "POST", ap+"/unarchive", nil)
	})

	t.Run("진행 중 할 일 → 보관 409 tasks_active · 진행 중 미션 → 삭제 409 works_active", func(t *testing.T) {
		sp := f.roomPath(f.sessionID) // the P2 fixture's session: an active mission with a queued task
		st, out, _ := f.api.do("POST", sp+"/archive", nil)
		if st != 409 || str(out, "code") != "tasks_active" {
			t.Fatalf("archive = %d %v", st, out)
		}
		st, out, _ = f.api.do("DELETE", sp, nil)
		if st != 409 || str(out, "code") != "works_active" {
			t.Fatalf("delete = %d %v", st, out)
		}
	})

	t.Run("삭제: 부방장 403 · 방장 204 → room.deleted 한 줄 + SSE 두 이름", func(t *testing.T) {
		f.member.must(201, "POST", ap+"/participants", map[string]any{"user_id": f.otherUserID})
		f.member.must(200, "PUT", ap+"/deputy", map[string]any{"user_id": f.otherUserID})
		st, out, _ := f.other.do("DELETE", ap, nil)
		if st != 403 || str(out, "code") != "room_owner_required" {
			t.Fatalf("deputy delete = %d %v", st, out)
		}
		f.member.must(204, "DELETE", ap, nil)
		if st, _, _ := f.member.do("GET", ap, nil); st != 404 {
			t.Fatalf("GET deleted = %d", st)
		}
		if n := f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'room.deleted' AND object_id = $1`, str(a, "id")); n != 1 {
			t.Fatalf("room.deleted lines = %d", n)
		}
		if n := f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1`, str(a, "id")); n != 0 {
			t.Fatalf("room activity left = %d", n)
		}
		if n := f.count(t, `SELECT count(*) FROM stream_event WHERE session_id = $1 AND type IN ('room.deleted', 'session.deleted')`, str(a, "id")); n != 2 {
			t.Fatalf("delete frames = %d", n)
		}
	})

	t.Run("listActivityLog: owner·admin 만, room 거르기", func(t *testing.T) {
		if st, _, _ := f.member.do("GET", f.p+"/workspaces/"+f.wsID+"/activity-log", nil); st != 403 {
			t.Fatalf("member = %d", st)
		}
		out := f.admin.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/activity-log?room_id="+str(a, "id"), nil)
		l := items(out)
		if len(l) != 1 || str(l[0].(map[string]any), "action") != "room.deleted" {
			t.Fatalf("activity for deleted room = %v", out)
		}
		page := f.admin.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/activity-log?limit=1", nil)
		if len(items(page)) != 1 || page["next_cursor"] == nil {
			t.Fatalf("page = %v", page)
		}
	})
}

// TestR1b3OwnerSuccession is §12.1-4: a room owner leaving the workspace hands
// their rooms to the oldest workspace owner, with a timeline line and a
// room.owner_succeeded activity entry.
func TestR1b3OwnerSuccession(t *testing.T) {
	f := newRoomsFixture(t)
	room := f.mkRoom(t, f.other, "Oth 의 방")
	rid := str(room, "id")
	f.api.must(204, "DELETE", f.memberPath(f.otherID), nil)
	var owner string
	if err := f.pool.QueryRow(t.Context(), `SELECT owner_user_id::text FROM room WHERE id = $1`, rid).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	var dirUser string
	if err := f.pool.QueryRow(t.Context(), `SELECT user_id::text FROM member WHERE workspace_id = $1 AND role = 'owner' ORDER BY created_at LIMIT 1`, f.wsID).Scan(&dirUser); err != nil {
		t.Fatal(err)
	}
	if owner != dirUser {
		t.Fatalf("owner = %s, want oldest ws owner %s", owner, dirUser)
	}
	if n := f.count(t, `SELECT count(*) FROM room_participant WHERE room_id = $1 AND user_id = $2 AND role = 'owner' AND left_at IS NULL`, rid, dirUser); n != 1 {
		t.Fatal("successor has no live owner row")
	}
	if n := f.count(t, `SELECT count(*) FROM room_participant WHERE room_id = $1 AND user_id = $2 AND left_at IS NOT NULL`, rid, f.otherUserID); n != 1 {
		t.Fatal("leaver's row not closed")
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'room.owner_succeeded' AND session_id = $1`, rid); n != 1 {
		t.Fatal("room.owner_succeeded missing")
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '%방장을 이어받았습니다.'`, rid); n != 1 {
		t.Fatal("succession line missing")
	}
}

// TestR1b3SessionRosterIsRoomRoster: the old session participant ops write
// room_participant — removing is a left_at, the old list hides the row, and
// the agent can be invited back through either surface.
func TestR1b3SessionRosterIsRoomRoster(t *testing.T) {
	f := newRoomsFixture(t)
	sp := f.p + "/sessions/" + f.sessionID
	f.api.must(204, "DELETE", sp+"/participants/"+f.w, nil)
	if n := f.count(t, `SELECT count(*) FROM room_participant WHERE room_id = $1 AND agent_id = $2 AND left_at IS NOT NULL`, f.sessionID, f.w); n != 1 {
		t.Fatal("old removeParticipant did not leave a left_at row")
	}
	for _, p := range f.api.mustList(200, "GET", sp+"/participants", nil) {
		if str(p.(map[string]any), "agent_id") == f.w {
			t.Fatal("left agent still in the old list")
		}
	}
	if n := len(items(f.api.must(200, "GET", f.roomPath(f.sessionID)+"/participants", nil))); n != 3 { // Lead · R (W left) + Dir
		t.Fatalf("room roster = %d", n)
	}
	f.api.must(201, "POST", sp+"/participants", map[string]any{"agent_id": f.w})
	if n := f.count(t, `SELECT count(*) FROM room_participant WHERE room_id = $1 AND agent_id = $2 AND left_at IS NULL`, f.sessionID, f.w); n != 1 {
		t.Fatal("re-invite did not reopen the row")
	}
}

// TestR1b3PerPersonFrames: a frame addressed to one person reaches that
// person's subscription only, live and on backfill.
func TestR1b3PerPersonFrames(t *testing.T) {
	f := newRoomsFixture(t)
	ws := mustUUID(t, f.wsID)
	me, other := mustUUID(t, f.memberUserID), mustUUID(t, f.otherUserID)
	mine := f.srv.Hub.SubscribeFor(ws, nil, me)
	theirs := f.srv.Hub.SubscribeFor(ws, nil, other)
	defer mine.Close()
	defer theirs.Close()
	room := mustUUID(t, f.sessionID)
	if err := f.srv.Hub.PublishTo(t.Context(), nil, ws, &room, &me, "room.unread", map[string]any{"unread_count": 0}); err != nil {
		t.Fatal(err)
	}
	got := func(s *realtime.Subscription) int {
		n := 0
		for {
			select {
			case e := <-s.C:
				if e.Type == "room.unread" {
					n++
				}
			default:
				return n
			}
		}
	}
	if got(mine) != 1 || got(theirs) != 0 {
		t.Fatal("room.unread leaked to another person's stream")
	}
	var first int64
	if err := f.pool.QueryRow(t.Context(), `SELECT min(id) FROM stream_event WHERE workspace_id = $1`, ws).Scan(&first); err != nil {
		t.Fatal(err)
	}
	back, resync, err := f.srv.Hub.BackfillFor(t.Context(), ws, first, nil, other)
	if err != nil || resync || len(back) == 0 {
		t.Fatalf("backfill = %d frames, resync %v, err %v", len(back), resync, err)
	}
	mineBack, _, _ := f.srv.Hub.BackfillFor(t.Context(), ws, first, nil, me)
	if len(mineBack) != len(back)+1 {
		t.Fatalf("addressee backfill = %d, others = %d — want exactly one more", len(mineBack), len(back))
	}
	for _, e := range back {
		if e.Type == "room.unread" {
			t.Fatal("room.unread backfilled to another person")
		}
	}
}

// TestR1b3OneToMany is the R1b 인계 check for this PR's queries: drop
// `work_room_single` (R1b2 will) in this test's private database, give the
// fixture room a second mission, and see that the room list, getRoom, the
// inbox and §11 metric 5 count each thing once.
func TestR1b3OneToMany(t *testing.T) {
	f := newRoomsFixture(t)
	ctx := t.Context()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`DROP INDEX work_room_single`)
	var w1, w2, dir string
	if err := f.pool.QueryRow(ctx, `SELECT id::text, director_user_id::text FROM work WHERE room_id = $1`, f.sessionID).Scan(&w1, &dir); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO work (room_id, title, goal, director_user_id, status, created_by, started_at)
		VALUES ($1, 'Second', 'g2', $2, 'active', $2, now()) RETURNING id::text`, f.sessionID, dir).Scan(&w2); err != nil {
		t.Fatal(err)
	}

	t.Run("listRooms: 방은 한 줄, active_work_count 2", func(t *testing.T) {
		n := 0
		for _, it := range items(f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms", nil)) {
			m := it.(map[string]any)
			if str(m, "id") == f.sessionID {
				n++
				if m["active_work_count"].(float64) != 2 {
					t.Fatalf("active_work_count = %v", m["active_work_count"])
				}
				if l := m["participants"].([]any); len(l) != 4 { // Lead · R · W · Dir
					t.Fatalf("roster = %d rows", len(l))
				}
			}
		}
		if n != 1 {
			t.Fatalf("room listed %d times", n)
		}
	})
	t.Run("getRoom: counts 는 방 단위(미션 수만큼 불지 않는다)", func(t *testing.T) {
		room := f.api.must(200, "GET", f.roomPath(f.sessionID), nil)
		c := room["counts"].(map[string]any)
		lanes := f.count(t, `SELECT count(*) FROM lane WHERE session_id = $1 AND status IN ('queued','running','waiting_human','blocked','paused')`, f.sessionID)
		if c["works_active"].(float64) != 2 || int(c["lanes_active"].(float64)) != lanes {
			t.Fatalf("counts = %v (lanes %d)", c, lanes)
		}
	})
	t.Run("받은 요청: work_id 없는 옛 항목이 미션 수만큼 복제되지 않는다", func(t *testing.T) {
		exec(`INSERT INTO inbox_item (member_id, type, severity, session_id) SELECT id, 'session_paused', 'action_required', $2 FROM member WHERE workspace_id = $1 AND user_id = $3`, f.wsID, f.sessionID, dir)
		exec(`INSERT INTO inbox_item (member_id, type, severity, session_id, work_id) SELECT id, 'session_paused', 'action_required', $2, $4 FROM member WHERE workspace_id = $1 AND user_id = $3`, f.wsID, f.sessionID, dir, w2)
		got := 0
		for _, it := range items(f.api.must(200, "GET", f.p+"/inbox?workspace_id="+f.wsID, nil)) {
			m := it.(map[string]any)
			if str(m, "type") == "session_paused" {
				got++
				if str(m, "work_id") == w2 && m["session"].(map[string]any)["title"] != "Second" {
					t.Fatalf("work_id item carries the wrong mission: %v", m["session"])
				}
			}
		}
		if got != 2 {
			t.Fatalf("session_paused items = %d, want 2", got)
		}
	})
	t.Run("§11 지표 5: 미션 단위(다른 미션의 줄기·시간이 섞이지 않는다)", func(t *testing.T) {
		now := t0.Add(2 * time.Hour)
		// W1: two lanes (work_id set), tasks 60m + 60m, wall 80m → 1 - 80/120.
		exec(`UPDATE work SET status = 'completed', started_at = $2, finished_at = $3 WHERE id = $1`, w1, t0, t0.Add(80*time.Minute))
		exec(`UPDATE lane SET work_id = $2 WHERE session_id = $1`, f.sessionID, w1)
		exec(`UPDATE task SET work_id = $2, status = 'completed', started_at = $3, finished_at = $4 WHERE session_id = $1`, f.sessionID, w1, t0, t0.Add(60*time.Minute))
		var lane2 string
		if err := f.pool.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, status, work_id)
			SELECT session_id, $2, profile_id, 'done', $3 FROM lane WHERE session_id = $1 LIMIT 1 RETURNING id::text`, f.sessionID, f.r, w1).Scan(&lane2); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, work_id, started_at, finished_at)
			SELECT $1, session_id, agent_id, profile_id, 'completed', $2, $3, $4 FROM lane WHERE id = $1`, lane2, w1, t0.Add(10*time.Minute), t0.Add(70*time.Minute))
		// W2: completed, one lane of its own → not a sample. A mission-less
		// lane (work_id NULL) in a room with two missions belongs to neither.
		exec(`UPDATE work SET status = 'completed', started_at = $2, finished_at = $3 WHERE id = $1`, w2, t0, t0.Add(30*time.Minute))
		exec(`INSERT INTO lane (session_id, agent_id, profile_id, status, work_id)
			SELECT session_id, agent_id, profile_id, 'done', $2 FROM lane WHERE session_id = $1 LIMIT 1`, f.sessionID, w2)
		exec(`INSERT INTO lane (session_id, agent_id, profile_id, status)
			SELECT session_id, agent_id, profile_id, 'done' FROM lane WHERE session_id = $1 LIMIT 1`, f.sessionID)
		ms, err := metricsCompute(t, f, now)
		if err != nil {
			t.Fatal(err)
		}
		m := ms["parallel_wallclock_reduction"]
		if m.N != 1 || m.Value == nil || *m.Value < 0.33 || *m.Value > 0.34 {
			t.Fatalf("metric 5 = %v n=%d, want 1-80/120 over the one two-lane mission", m.Value, m.N)
		}
	})
}

func metricsCompute(t *testing.T, f *roomsFixture, now time.Time) (map[string]metrics.Metric, error) {
	list, err := metrics.Compute(t.Context(), f.pool, mustUUID(t, f.wsID), 30*24*time.Hour, now)
	if err != nil {
		return nil, err
	}
	out := map[string]metrics.Metric{}
	for _, m := range list {
		out[m.Key] = m
	}
	return out, nil
}
