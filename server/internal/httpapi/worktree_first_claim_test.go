package httpapi

// T-S-wt: a worktree room with no repository (createRoom inherits the kind
// alone, openapi 0.2.5) is settled on its first claim instead of sitting at
// `queued` forever; listMessages' v0.2.0 filters; openapi 0.2.6's two fields.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// worktreeRoom turns the fixture's room into the shape createRoom stores for
// a workspace whose default is worktree — the kind alone, no computer — and
// gives the fixture's computer `repos`.
func (f *p2Fixture) worktreeRoom(t *testing.T, repos ...string) {
	t.Helper()
	list := make([]string, 0, len(repos))
	for _, r := range repos {
		list = append(list, fmt.Sprintf(`{"path": %q, "remote_url": "", "branch": "main", "clean": true}`, r))
	}
	f.exec(t, `UPDATE runtime SET repos = $2::jsonb WHERE workspace_id = $1`, f.wsID, "["+strings.Join(list, ",")+"]")
	f.exec(t, `UPDATE room SET isolation = '{"kind": "worktree"}'::jsonb, runtime_id = NULL WHERE id = $1`, f.sessionID)
}

func (f *p2Fixture) roomSettle(t *testing.T) (runtime *uuid.UUID, isolation string, pending bool) {
	t.Helper()
	var iso, pend []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT runtime_id, isolation, isolation_pending FROM room WHERE id = $1`, f.sessionID).
		Scan(&runtime, &iso, &pend); err != nil {
		t.Fatal(err)
	}
	return runtime, string(iso), len(pend) > 0
}

func (f *p2Fixture) notices(t *testing.T, like string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE $2`,
		f.sessionID, like).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestWorktreeFirstClaimOneRepo: a computer with exactly one repository takes
// the room on its first claim — the path is filled, the room pinned, the
// timeline says where, and the task goes in the SAME poll.
func TestWorktreeFirstClaimOneRepo(t *testing.T) {
	f := newP2Fixture(t)
	f.worktreeRoom(t, "/Users/x/repo")
	if got := f.claimed(t); len(got) == 0 {
		t.Fatal("first claim handed out nothing — the worktree room with no repository is still stuck at queued")
	}
	rt, iso, pending := f.roomSettle(t)
	if rt == nil || pending || !strings.Contains(iso, `"repo_path": "/Users/x/repo"`) || !strings.Contains(iso, `"kind": "worktree"`) {
		t.Fatalf("room runtime=%v isolation=%s pending=%v, want pinned worktree on /Users/x/repo", rt, iso, pending)
	}
	if n := f.notices(t, "이 방은 mac-1의 /Users/x/repo 에서 워크트리로 돕니다%"); n != 1 {
		t.Fatalf("「이 방은 mac-1의 /Users/x/repo 에서 워크트리로 돕니다」 notices = %d, want 1", n)
	}
	if n := f.notices(t, "이 방은 mac-1에서 돕니다%"); n != 0 {
		t.Fatalf("the plain computer notice was written too (%d) — one notice per pin", n)
	}
	if n := f.count(t, `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND purpose = 'isolation'`, f.sessionID); n != 0 {
		t.Fatalf("isolation requests = %d, want 0 — one repository needs no question", n)
	}
}

// TestWorktreeFirstClaimSeveralRepos: several repositories → the room owner
// picks one (purpose isolation, type choice, approver room_owner, the
// isolation_confirm card); nothing runs or is pinned until they do, a path
// that is not an option is refused, and the pick settles the room on the
// computer that asked.
func TestWorktreeFirstClaimSeveralRepos(t *testing.T) {
	f := newP2Fixture(t)
	f.worktreeRoom(t, "/Users/x/a", "/Users/x/b")
	if got := f.claimed(t); len(got) != 0 {
		t.Fatalf("first claim = %v, want nothing until the owner picks a repository", got)
	}
	_ = f.claimed(t) // a second poll must not ask twice
	if rt, _, pending := f.roomSettle(t); rt != nil || !pending {
		t.Fatalf("room runtime=%v pending=%v, want unpinned with the question pending", rt, pending)
	}
	var id, typ, spec string
	var options []string
	var def *string
	var n int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id::text, type::text, approver_spec, options, proposed_default, count(*) OVER ()
		  FROM hitl_request WHERE session_id = $1 AND purpose = 'isolation'`, f.sessionID).
		Scan(&id, &typ, &spec, &options, &def, &n); err != nil {
		t.Fatal(err)
	}
	if n != 1 || typ != "choice" || spec != "room_owner" || strings.Join(options, ",") != "/Users/x/a,/Users/x/b" || def == nil || *def != "/Users/x/a" {
		t.Fatalf("request n=%d type=%s spec=%s options=%v default=%v, want one room_owner choice over both repositories, the first proposed", n, typ, spec, options, def)
	}
	// Past the deadline in an autonomous room, the question still waits: the
	// sweep does not pick a checkout for the owner.
	f.exec(t, `UPDATE room SET autonomy = 'autonomous' WHERE id = $1`, f.sessionID)
	f.exec(t, `UPDATE work SET autonomy = 'autonomous' WHERE room_id = $1`, f.sessionID)
	f.fake.Advance(25 * time.Hour)
	if _, err := f.srv.SweepHitlDeadlines(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM hitl_request WHERE id = $1 AND status = 'open'`, id); n != 1 {
		t.Fatal("the expired repository question was auto-answered — the owner must pick")
	}
	if n := f.count(t, `SELECT count(*) FROM inbox_item WHERE ref_id = $1 AND type = 'isolation_confirm'`, id); n != 1 {
		t.Fatalf("isolation_confirm cards = %d, want 1", n)
	}
	path := f.p + "/hitl-requests/" + id + "/response"
	f.api.must(422, "POST", path, map[string]any{"answer": "/Users/x/elsewhere"}, "Idempotency-Key", uuid.NewString())
	f.api.must(200, "POST", path, map[string]any{"answer": "/Users/x/b"}, "Idempotency-Key", uuid.NewString())
	rt, iso, pending := f.roomSettle(t)
	if rt == nil || pending || !strings.Contains(iso, `"repo_path": "/Users/x/b"`) {
		t.Fatalf("after the pick: runtime=%v isolation=%s pending=%v, want pinned on /Users/x/b", rt, iso, pending)
	}
	if n := f.notices(t, "이 방은 mac-1의 /Users/x/b 에서 워크트리로 돕니다%"); n != 1 {
		t.Fatalf("worktree notices = %d, want 1", n)
	}
	if got := f.claimed(t); len(got) == 0 {
		t.Fatal("the first run did not go after the pick")
	}
}

// TestWorktreeFirstClaimNoRepo: a computer with no repository passes the room
// by — nothing pinned, nothing asked — and the room's queued task says why
// (`queued_reason: runtime`), so the screen can say 「저장소가 있는 컴퓨터를
// 기다립니다」. A second computer with a repository then takes it.
func TestWorktreeFirstClaimNoRepo(t *testing.T) {
	f := newP2Fixture(t)
	f.worktreeRoom(t)
	if got := f.claimed(t); len(got) != 0 {
		t.Fatalf("claim = %v, want nothing — no repository to cut worktrees from", got)
	}
	if rt, _, pending := f.roomSettle(t); rt != nil || pending {
		t.Fatalf("room runtime=%v pending=%v, want untouched", rt, pending)
	}
	if n := f.count(t, `SELECT count(*) FROM task WHERE session_id = $1 AND status = 'queued' AND queued_reason = 'runtime'`, f.sessionID); n == 0 {
		t.Fatal("queued task has no reason — the room would sit at queued saying nothing")
	}
	var lane string
	if err := f.pool.QueryRow(t.Context(), `SELECT lane_id::text FROM task WHERE session_id = $1 AND status = 'queued' LIMIT 1`, f.sessionID).Scan(&lane); err != nil {
		t.Fatal(err)
	}
	var reason any = "(lane missing)"
	for _, it := range f.api.mustList(200, "GET", f.p+"/rooms/"+f.sessionID+"/lanes", nil) {
		if l := it.(map[string]any); str(l, "id") == lane {
			reason = l["queued_reason"]
		}
	}
	if reason != "runtime" {
		t.Fatalf("listLanes queued_reason = %v, want runtime on the wire (the lane card's line)", reason)
	}
	// A room that already names its repository waits for a computer that HAS
	// it — another machine's checkout is not the same room.
	f.exec(t, `UPDATE room SET isolation = '{"kind": "worktree", "repo_path": "/Users/x/repo"}'::jsonb WHERE id = $1`, f.sessionID)
	f.exec(t, `UPDATE runtime SET repos = '[{"path": "/Users/x/other"}]'::jsonb WHERE workspace_id = $1`, f.wsID)
	if got := f.claimed(t); len(got) != 0 {
		t.Fatalf("claim on a computer without the room's repository = %v, want nothing", got)
	}
	f.exec(t, `UPDATE runtime SET repos = '[{"path": "/Users/x/other"}, {"path": "/Users/x/repo"}]'::jsonb WHERE workspace_id = $1`, f.wsID)
	if got := f.claimed(t); len(got) == 0 {
		t.Fatal("a computer with the room's repository did not take it")
	}
	if n := f.count(t, `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND purpose = 'isolation'`, f.sessionID); n != 0 {
		t.Fatalf("isolation requests = %d, want 0 — the room named its repository already", n)
	}
}

func (f *p2Fixture) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestListMessagesWorkFilterAndAround: the v0.2.0 query parameters are
// applied, the page is filled AFTER the filter (the other mission's messages
// do not eat the 50), and around_message_id centres 25 + 1 + 25.
func TestListMessagesWorkFilterAndAround(t *testing.T) {
	f := newP2Fixture(t)
	work := f.workID(t)
	var other uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO work (room_id, title, goal, status, director_user_id, created_by)
		SELECT room_id, 'other', goal, 'draft', director_user_id, created_by FROM work WHERE id = $1 RETURNING id`, work).Scan(&other); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `DELETE FROM message WHERE session_id = $1`, f.sessionID)
	// 110 of this mission, 60 of the other, 10 of none — interleaved by time
	// so a filter applied after LIMIT would come back short.
	var ids []uuid.UUID
	base := time.Date(2026, 9, 24, 9, 0, 0, 0, time.UTC)
	for i := 0; i < 180; i++ {
		var w *uuid.UUID
		switch i % 3 {
		case 0:
			w = &work
		case 1:
			w = &other
		}
		if i >= 150 && i%3 == 2 {
			w = nil
		} else if i%3 == 2 {
			w = &work
		}
		var id uuid.UUID
		if err := f.pool.QueryRow(t.Context(), `
			INSERT INTO message (session_id, author_type, content, kind, created_at, work_id)
			VALUES ($1, 'system', $2, 'system', $3, $4) RETURNING id`, f.sessionID, fmt.Sprintf("m%03d", i), base.Add(time.Duration(i)*time.Second), w).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	list := func(q string) map[string]any {
		return f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID+"/messages?"+q, nil)
	}
	page := list("work_id=" + work.String())
	if got := len(items(page)); got != 50 {
		t.Fatalf("work_id page = %d messages, want a full 50 after the filter", got)
	}
	for _, it := range items(page) {
		var w *uuid.UUID
		_ = f.pool.QueryRow(t.Context(), `SELECT work_id FROM message WHERE id = $1`, str(it.(map[string]any), "id")).Scan(&w)
		if w == nil || *w != work {
			t.Fatalf("work_id page holds %v of mission %v", str(it.(map[string]any), "content"), w)
		}
	}
	if page["has_more_before"] != true {
		t.Fatal("work_id page has_more_before = false with 110 of that mission's messages")
	}
	none := list("no_work=true")
	if got := len(items(none)); got != 10 {
		t.Fatalf("no_work page = %d, want the 10 messages of no mission", got)
	}
	f.api.must(422, "GET", f.p+"/rooms/"+f.sessionID+"/messages?no_work=true&work_id="+work.String(), nil)

	anchor := ids[90]
	around := items(list("around_message_id=" + anchor.String()))
	if len(around) != 51 || str(around[25].(map[string]any), "id") != anchor.String() {
		t.Fatalf("around page = %d with %v in the middle, want 25 + anchor + 25", len(around), str(around[25].(map[string]any), "content"))
	}
	if str(around[0].(map[string]any), "content") != "m065" || str(around[50].(map[string]any), "content") != "m115" {
		t.Fatalf("around edges = %s … %s, want m065 … m115 (chronological)", str(around[0].(map[string]any), "content"), str(around[50].(map[string]any), "content"))
	}
	edge := items(list("around_message_id=" + ids[3].String()))
	if len(edge) != 29 {
		t.Fatalf("around the 4th message = %d, want 3 + anchor + 25", len(edge))
	}
	f.api.must(422, "GET", f.p+"/rooms/"+f.sessionID+"/messages?around_message_id="+uuid.NewString(), nil)
}

// TestRoomAuditViewAndDeleteWorkdirs is openapi 0.2.6: the S5 card's
// audit_view (getRoom's AuditViewed on the row's facts) and deleteRoom's
// 409 workdir_unmerged listing {id, path, runtime_id, runtime_name,
// commits_ahead, dirty}.
func TestRoomAuditViewAndDeleteWorkdirs(t *testing.T) {
	f := newRoomsFixture(t)
	invited := f.mkRoom(t, f.member, "비공개")
	f.member.must(200, "PATCH", f.roomPath(str(invited, "id")), map[string]any{"visibility": "invited"})
	open := f.mkRoom(t, f.member, "공개")
	for _, c := range []struct {
		who     *client
		room    map[string]any
		want    bool
		comment string
	}{
		{f.api, invited, true, "ws owner, not in the invited room → audit"},
		{f.api, open, false, "workspace-visible room is nobody's audit"},
		{f.member, invited, false, "the participant reads their own room"},
	} {
		got := f.listed(t, c.who, str(c.room, "id"))
		if got == nil || got["audit_view"] != c.want {
			t.Fatalf("%s: audit_view = %v, want %v", c.comment, got["audit_view"], c.want)
		}
	}

	// deleteRoom's refusal names the folder, the computer, and what holds it.
	room := str(open, "id")
	var rt uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE room SET runtime_id = $2 WHERE id = $1`, room, rt)
	var agent uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&agent); err != nil {
		t.Fatal(err)
	}
	var wd uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO workdir (session_id, agent_id, kind, path_or_ref, branch, merged, commits_ahead, tree_dirty)
		VALUES ($1, $2, 'worktree', '/Users/x/.colab/wt/a', 'colab/a', false, 3, true) RETURNING id`, room, agent).Scan(&wd); err != nil {
		t.Fatal(err)
	}
	body := f.member.must(409, "DELETE", f.roomPath(room), nil)
	list, _ := body["workdirs"].([]any)
	if len(list) != 1 {
		t.Fatalf("409 workdirs = %v, want the one folder", body["workdirs"])
	}
	got := list[0].(map[string]any)
	var name string
	_ = f.pool.QueryRow(t.Context(), `SELECT name FROM runtime WHERE id = $1`, rt).Scan(&name)
	if str(got, "id") != wd.String() || str(got, "path") != "/Users/x/.colab/wt/a" || str(got, "runtime_id") != rt.String() ||
		str(got, "runtime_name") != name || got["commits_ahead"] != float64(3) || got["dirty"] != true || len(got) != 6 {
		t.Fatalf("409 workdir = %v, want {id, path, runtime_id, runtime_name %q, commits_ahead 3, dirty true} and nothing else", got, name)
	}
}

// TestRoomRuntimePinnedIsThe409 is openapi 0.2.7: Room.runtime_pinned is the
// judgement updateRoom's 409 runtime_pinned makes — false while the computer
// and isolation may still change (even with a computer named), true from the
// first attempt, and the PATCH agrees with the flag on both sides.
func TestRoomRuntimePinnedIsThe409(t *testing.T) {
	f := newP2Fixture(t)
	room := f.p + "/rooms/" + f.sessionID
	pinned := func() any { return f.api.must(200, "GET", room, nil)["runtime_pinned"] }
	if got := pinned(); got != false {
		t.Fatalf("before any run: runtime_pinned = %v, want false", got)
	}
	// S20 names the computer ahead of the first run: runtime_id is set, the
	// room is NOT pinned — the flag is not `runtime_id != null`.
	var rt string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	got := f.api.must(200, "PATCH", room, map[string]any{"runtime_id": rt, "isolation": map[string]any{"kind": "none"}})
	if got["runtime_id"] != rt || got["runtime_pinned"] != false {
		t.Fatalf("updateRoom response runtime_id=%v runtime_pinned=%v, want %s and false — the change it just accepted", got["runtime_id"], got["runtime_pinned"], rt)
	}
	if got := f.claimed(t); len(got) == 0 {
		t.Fatal("the first run did not go")
	}
	if got := pinned(); got != true {
		t.Fatalf("after the first attempt: runtime_pinned = %v, want true", got)
	}
	if body := f.api.must(409, "PATCH", room, map[string]any{"isolation": map[string]any{"kind": "none"}}); body["code"] != "runtime_pinned" {
		t.Fatalf("PATCH isolation after the first attempt = %v, want 409 runtime_pinned", body["code"])
	}
}
