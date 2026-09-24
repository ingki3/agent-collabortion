package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// T-R1c — PRD v0.19 FR-4.5 over HTTP: listReadableRooms · readRoom ·
// listRoomReads, and the NN7 originator inheritance a woken turn needs to
// read at all.

// roomReadFixture is the p2 fixture (room A: Lead · R · W, Director = Dir)
// plus four more rooms, each answering one condition of FR-4.5 for Lead:
//
//	B  Lead is a participant                       → readable
//	C  Lead is a participant, Dir never was         → originator_not_participant
//	D  R only, but A links to D                     → readable via link
//	E  R only, no link                              → agent_not_allowed
type roomReadFixture struct {
	*p2Fixture
	a, b, c, d, e uuid.UUID
}

func newRoomReadFixture(t *testing.T) *roomReadFixture {
	t.Helper()
	f := &roomReadFixture{p2Fixture: newP2Fixture(t)}
	f.a = mustUUID(t, f.sessionID)
	mk := func(title string, agents ...string) uuid.UUID {
		parts := []map[string]any{}
		for _, a := range agents {
			parts = append(parts, map[string]any{"agent_id": a})
		}
		f.fake.Advance(time.Second)
		out := sessionRoom(t, f.api, f.pool, f.p, f.wsID, map[string]any{
			"title": title, "goal": title + " 목표", "isolation": map[string]any{"kind": "none"},
			"assignee_agent_id": agents[0], "participants": parts,
		})
		return mustUUID(t, str(out, "id"))
	}
	f.b = mk("인프라", f.lead)
	f.c = mk("비밀", f.lead)
	f.d = mk("참고", f.r)
	f.e = mk("남의방", f.r)
	f.exec(t, `DELETE FROM room_participant WHERE room_id = $1 AND user_id IS NOT NULL`, f.c)
	f.exec(t, `INSERT INTO room_link (room_id, target_room_id, created_by)
		SELECT $1, $2, owner_user_id FROM room WHERE id = $1`, f.a, f.d)
	return f
}

func (f *roomReadFixture) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func (f *roomReadFixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func (f *roomReadFixture) read(t *testing.T, tok string, room uuid.UUID, query string) (int, map[string]any) {
	t.Helper()
	return f.rawGet(t, f.p+"/cli/rooms/"+room.String()+"/read"+query, tok)
}

func (f *roomReadFixture) readable(t *testing.T, tok string) map[string]map[string]any {
	t.Helper()
	st, out := f.rawGet(t, f.p+"/cli/rooms", tok)
	if st != 200 {
		t.Fatalf("listReadableRooms = %d %v", st, out)
	}
	got := map[string]map[string]any{}
	for _, raw := range out["items"].([]any) {
		it := raw.(map[string]any)
		got[str(it, "name")] = it
	}
	return got
}

func (f *roomReadFixture) reads(t *testing.T, room uuid.UUID, dir string) []map[string]any {
	t.Helper()
	path := f.p + "/rooms/" + room.String() + "/reads"
	if dir != "" {
		path += "?direction=" + dir
	}
	out := f.api.must(200, "GET", path, nil)
	items := []map[string]any{}
	for _, raw := range out["items"].([]any) {
		items = append(items, raw.(map[string]any))
	}
	return items
}

func deniedReason(out map[string]any) string {
	s, _ := out["denied_reason"].(string)
	return s
}

func TestRoomReadPermissionOverHTTP(t *testing.T) {
	f := newRoomReadFixture(t)
	tok, leadTask := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")

	// list: B (participant) and D (link); never C, E, or A itself.
	got := f.readable(t, tok)
	if len(got) != 2 || got["인프라"] == nil || got["참고"] == nil {
		t.Fatalf("readable = %v, want exactly 인프라 · 참고", keys(got))
	}
	if got["인프라"]["agent_is_participant"] != true || got["인프라"]["via_link"] != false {
		t.Errorf("인프라 = %v, want a participant read", got["인프라"])
	}
	if got["참고"]["agent_is_participant"] != false || got["참고"]["via_link"] != true {
		t.Errorf("참고 = %v, want a read through the link", got["참고"])
	}

	// read B: allowed, recorded on both sides.
	f.fake.Advance(time.Second)
	st, out := f.read(t, tok, f.b, "")
	if st != 200 {
		t.Fatalf("read 인프라 = %d %v", st, out)
	}
	if room := out["room"].(map[string]any); str(room, "name") != "인프라" || out["truncated"] != false {
		t.Fatalf("read = %v", out)
	}
	if n := f.count(t, `SELECT count(*) FROM room_read_log WHERE room_id = $1 AND target_room_id = $2 AND allowed AND reader_task_id = $3`, f.a, f.b, leadTask); n != 1 {
		t.Fatalf("room_read_log rows = %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE 'Dir의 요청으로 @Lead이(가) 이 방을 읽었습니다(최근 %'`, f.b); n != 1 {
		t.Fatalf("read room's system line = %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND content LIKE '%이 방을 읽었습니다%'`, f.a); n != 0 {
		t.Fatalf("the reading room got the read room's line")
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE action = 'room.read' AND session_id IN ($1, $2)`, f.a, f.b); n != 2 {
		t.Fatalf("room.read activity rows = %d, want one per room", n)
	}
	if n := f.count(t, `SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'status' AND verb = 'read' AND outcome = 'ok'
		AND payload->'args'->>'note' LIKE '「인프라」 방을 읽었습니다%'`, leadTask); n != 1 {
		t.Fatalf("reading task's feed note = %d, want 1", n)
	}

	// C (Dir never in it) and a room that does not exist answer the same.
	stC, outC := f.read(t, tok, f.c, "")
	stX, outX := f.read(t, tok, uuid.New(), "")
	if stC != 403 || str(outC, "code") != "room_read_denied" || deniedReason(outC) != "originator_not_participant" {
		t.Fatalf("read 비밀 = %d %v", stC, outC)
	}
	if stX != stC || str(outX, "detail") != str(outC, "detail") || deniedReason(outX) != deniedReason(outC) {
		t.Fatalf("a hidden room (%d %v) and a missing one (%d %v) must be indistinguishable", stC, outC, stX, outX)
	}
	if strings.Contains(str(outC, "detail"), "비밀") {
		t.Fatalf("the denial names the hidden room: %q", str(outC, "detail"))
	}

	// E: Dir is in it, Lead is not and there is no link.
	stE, outE := f.read(t, tok, f.e, "")
	if stE != 403 || deniedReason(outE) != "agent_not_allowed" {
		t.Fatalf("read 남의방 = %d %v", stE, outE)
	}

	// D through the link.
	f.fake.Advance(time.Second)
	if st, out := f.read(t, tok, f.d, ""); st != 200 {
		t.Fatalf("read 참고 via link = %d %v", st, out)
	}

	// S23: A read two rooms and was refused three times; B was read once.
	if out := f.reads(t, f.a, "out"); len(out) != 2 {
		t.Fatalf("A out = %d, want 2", len(out))
	}
	denied := f.reads(t, f.a, "denied")
	if len(denied) != 3 {
		t.Fatalf("A denied = %d, want 3", len(denied))
	}
	for _, e := range denied {
		if e["other_room"] != nil {
			t.Fatalf("denied row names the room: %v", e)
		}
	}
	in := f.reads(t, f.b, "in")
	if len(in) != 1 || str(in[0]["other_room"].(map[string]any), "id") != f.sessionID ||
		str(in[0]["originator_user"].(map[string]any), "display_name") != "Dir" {
		t.Fatalf("B in = %v, want one read from A on Dir's behalf", in)
	}
	if all := f.reads(t, f.a, ""); len(all) != 5 {
		t.Fatalf("A all = %d, want 5", len(all))
	}

	// [V19-C] read-time check: Dir leaves B, and the very next read stops.
	f.exec(t, `UPDATE room_participant SET left_at = now() WHERE room_id = $1 AND user_id IS NOT NULL`, f.b)
	st, out = f.read(t, tok, f.b, "")
	if st != 403 || deniedReason(out) != "originator_left" || !strings.Contains(str(out, "detail"), "인프라") {
		t.Fatalf("read after the originator left = %d %v", st, out)
	}
	if got := f.readable(t, tok); len(got) != 1 || got["참고"] == nil {
		t.Fatalf("readable after leaving = %v, want only 참고", keys(got))
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1 AND action = 'room.read.denied'
		AND payload->>'note' = 'Dir이(가) 인프라 방을 떠나 @Lead이(가)의 참고 읽기가 막혔습니다'`, f.a); n != 0 {
		// the sentence is 「〈사람〉이 〈방〉을 떠나 〈에이전트〉의 참고 읽기가 막혔습니다」
		// — no particle after the agent's name.
		t.Fatalf("activity note has a stray particle")
	}
	if n := f.count(t, `SELECT count(*) FROM activity_log WHERE session_id = $1 AND action = 'room.read.denied'
		AND payload->>'note' = 'Dir이(가) 인프라 방을 떠나 @Lead의 참고 읽기가 막혔습니다'`, f.a); n != 1 {
		t.Fatalf("originator_left activity line = %d, want 1", n)
	}
	left := f.reads(t, f.a, "denied")[0]
	if left["denied_reason"] != "originator_left" || str(left["other_room"].(map[string]any), "name") != "인프라" {
		t.Fatalf("originator_left row = %v, want it to name 인프라", left)
	}

	// no_originator: never substituted by the owner or the Director.
	f.exec(t, `UPDATE task SET originator_user_id = NULL WHERE id = $1`, leadTask)
	st, out = f.read(t, tok, f.d, "")
	if st != 403 || deniedReason(out) != "no_originator" {
		t.Fatalf("read with no originator = %d %v", st, out)
	}
	if got := f.readable(t, tok); len(got) != 0 {
		t.Fatalf("readable with no originator = %v, want none", keys(got))
	}

	// A person cannot use the agent endpoints. S23 is whoever may view the
	// room — rooms.Decide(ActView), the one table (review #290 R1-1 · #291
	// NN3): a plain member reads a workspace-visible room's S23 like its
	// timeline; an invited room they are not in does not exist (404); the
	// workspace owner reads it for audit.
	if st, _ := f.rawGet(t, f.p+"/cli/rooms", ""); st != 401 {
		t.Fatalf("listReadableRooms without a token = %d, want 401", st)
	}
	outsider := f.addMember(t, "s23-outsider@example.com", "Outsider")
	s23 := f.p + "/rooms/" + f.c.String() + "/reads"
	if st, _, _ := outsider.do("GET", s23, nil); st != 200 {
		t.Fatalf("S23 of a workspace-visible room, plain member = %d, want 200", st)
	}
	f.exec(t, `UPDATE room SET visibility = 'invited' WHERE id = $1`, f.c)
	if st, out, _ := outsider.do("GET", s23, nil); st != 404 || str(out, "code") != "not_found" {
		t.Fatalf("S23 of an invited room a plain member is not in = %d %v, want 404", st, out)
	}
	if st, _, _ := f.api.do("GET", s23, nil); st != 200 {
		t.Fatalf("S23 for the workspace owner (audit read) = %d, want 200", st)
	}
}

func keys(m map[string]map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestRoomReadCaps(t *testing.T) {
	f := newRoomReadFixture(t)
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"room_read": map[string]any{"max_rooms_per_turn": 1, "max_tokens": 500}})
	set := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/settings", nil)
	if rr := set["room_read"].(map[string]any); rr["max_rooms_per_turn"] != float64(1) || rr["max_tokens"] != float64(500) {
		t.Fatalf("settings room_read = %v", rr)
	}
	if st, _, _ := f.api.do("PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"room_read": map[string]any{"max_tokens": 10}}); st != 422 {
		t.Fatalf("max_tokens 10 = %d, want 422 (minimum 500)", st)
	}

	// B gets more text than 500 tokens.
	for i := 0; i < 4; i++ {
		f.fake.Advance(time.Second)
		f.api.must(201, "POST", f.p+"/rooms/"+f.b.String()+"/messages",
			map[string]any{"content": strings.Repeat("긴 문장입니다. ", 60)}, "Idempotency-Key", uuid.NewString())
	}
	tok, leadTask := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	f.fake.Advance(time.Second)
	st, out := f.read(t, tok, f.b, "")
	if st != 200 || out["truncated"] != true {
		t.Fatalf("read over max_tokens = %d truncated=%v", st, out["truncated"])
	}
	if n := len(out["messages"].([]any)); n == 0 || n >= 5 {
		t.Fatalf("messages = %d, want some but not all", n)
	}
	var truncated bool
	if err := f.pool.QueryRow(t.Context(), `SELECT truncated FROM room_read_log WHERE reader_task_id = $1`, leadTask).Scan(&truncated); err != nil || !truncated {
		t.Fatalf("log truncated = %v %v", truncated, err)
	}

	// A second room in the same turn is over max_rooms_per_turn = 1: an empty,
	// truncated answer and no read recorded anywhere.
	f.fake.Advance(time.Second)
	st, out = f.read(t, tok, f.d, "")
	if st != 200 || out["truncated"] != true || len(out["messages"].([]any)) != 0 {
		t.Fatalf("read over max_rooms_per_turn = %d %v", st, out)
	}
	if n := f.count(t, `SELECT count(*) FROM room_read_log WHERE target_room_id = $1`, f.d); n != 0 {
		t.Fatalf("a capped read left %d log rows", n)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE session_id = $1 AND content LIKE '%이 방을 읽었습니다%'`, f.d); n != 0 {
		t.Fatalf("a capped read told the room it was read")
	}
	// Reading the same room again costs no slot.
	if st, out := f.read(t, tok, f.b, "?tail=2"); st != 200 || len(out["messages"].([]any)) > 2 {
		t.Fatalf("re-read of 인프라 = %d %v", st, out)
	}
	// A new attempt is a new turn.
	f.exec(t, `INSERT INTO task_attempt (task_id, attempt, dispatched_at) VALUES ($1, 2, $2)`, leadTask, f.fake.Now().Add(time.Second))
	f.fake.Advance(2 * time.Second)
	tok2, err := f.srv.Tokens.Issue(t.Context(), f.pool, tokens.Scope{TaskID: leadTask, Attempt: 2, LaneID: laneOf(t, f, leadTask), SessionID: f.a, AgentID: f.leadUUID})
	if err != nil {
		t.Fatal(err)
	}
	if st, out := f.read(t, tok2, f.d, ""); st != 200 || out["truncated"] == true {
		t.Fatalf("read in the next turn = %d %v", st, out)
	}
}

func laneOf(t *testing.T, f *roomReadFixture, task uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id FROM task WHERE id = $1`, task).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestWokenTurnInheritsOriginator is NN7 / [V19-B]: the join notice, the
// blocked-question wake-up and the re-entry notice make tasks with the
// originator of the task that woke them — and a chain with no person in it
// stays without one (no owner substitution).
func TestWokenTurnInheritsOriginator(t *testing.T) {
	f := newRoomReadFixture(t)
	ctx := t.Context()
	var dirID uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT owner_user_id FROM room WHERE id = $1`, f.a).Scan(&dirID); err != nil {
		t.Fatal(err)
	}
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
	toR, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "조사"})
	if err != nil {
		t.Fatal(err)
	}
	toW, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.wUUID, Brief: "초안"})
	if err != nil {
		t.Fatal(err)
	}
	// Lead's turn has ended, so a wake-up is a NEW task, not a merge.
	f.exec(t, `UPDATE task SET status = 'completed', finished_at = now() WHERE id = $1`, leadTask)
	f.exec(t, `UPDATE lane SET status = 'done' WHERE id = (SELECT lane_id FROM task WHERE id = $1)`, leadTask)

	woken := func(label string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		var orig *uuid.UUID
		if err := f.pool.QueryRow(ctx, `SELECT id, originator_user_id FROM task WHERE agent_id = $1 AND status = 'queued'
			ORDER BY created_at DESC LIMIT 1`, f.leadUUID).Scan(&id, &orig); err != nil {
			t.Fatalf("%s: no woken Lead task: %v", label, err)
		}
		if orig == nil || *orig != dirID {
			t.Fatalf("%s: woken task originator = %v, want Dir %s (NN7)", label, orig, dirID)
		}
		return id
	}

	// blocked question → Lead wakes now.
	f.fake.Advance(time.Second)
	if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, toW.Task.Id.String()), 1, "blocked", "범위는?"); err != nil {
		t.Fatal(err)
	}
	q := woken("blocked wake")
	f.exec(t, `UPDATE task SET status = 'completed', finished_at = now() WHERE id = $1`, q)

	// join → Lead wakes with the bundle, and that turn can read B.
	f.fake.Advance(time.Second)
	if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, toR.Task.Id.String()), 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	joinTask := woken("join wake")
	tok, err := f.srv.Tokens.Issue(ctx, f.pool, tokens.Scope{TaskID: joinTask, Attempt: 1, LaneID: laneOf(t, f, joinTask), SessionID: f.a, AgentID: f.leadUUID})
	if err != nil {
		t.Fatal(err)
	}
	if st, out := f.read(t, tok, f.b, ""); st != 200 {
		t.Fatalf("the join turn reads 인프라 = %d %v — a woken turn must carry its originator", st, out)
	}

	// A chain with no person in it: neither the waker nor the requester has
	// an originator, and the woken task has none either — never Dir, who is
	// the room owner and the Director.
	f.exec(t, `UPDATE task SET status = 'completed', finished_at = now() WHERE id = $1`, joinTask)
	f.exec(t, `UPDATE task SET originator_user_id = NULL WHERE id IN ($1, $2)`, toW.Task.Id, leadTask)
	f.fake.Advance(time.Second)
	if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, toW.Task.Id.String()), 1, "blocked", "다시 질문"); err != nil {
		t.Fatal(err)
	}
	var orphan uuid.UUID
	var orig *uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT id, originator_user_id FROM task WHERE agent_id = $1 AND status = 'queued'
		ORDER BY created_at DESC LIMIT 1`, f.leadUUID).Scan(&orphan, &orig); err != nil {
		t.Fatal(err)
	}
	if orig != nil {
		t.Fatalf("a woken task with no person in its chain got originator %v — no substitution (FR-4.5 [V19-B])", *orig)
	}
	tok, err = f.srv.Tokens.Issue(ctx, f.pool, tokens.Scope{TaskID: orphan, Attempt: 1, LaneID: laneOf(t, f, orphan), SessionID: f.a, AgentID: f.leadUUID})
	if err != nil {
		t.Fatal(err)
	}
	if st, out := f.read(t, tok, f.b, ""); st != 403 || deniedReason(out) != "no_originator" {
		t.Fatalf("read from an orphan turn = %d %v, want 403 no_originator", st, out)
	}
}
