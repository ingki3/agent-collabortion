package httpapi

// T-S14 (S-72): updateMemberRole · removeMember · getNotificationSettings ·
// updateNotificationSettings — the S14 members and notifications tabs against
// a real database. Each contract line gets its own subtest so one failing
// boundary does not hide the others (P3 §0-7 lesson).

import (
	"testing"

	"github.com/google/uuid"
)

// membersFixture is the P2 fixture plus three more accounts in the workspace:
// an admin, a plain member and a second owner-to-be. The P2 fixture's own
// account ("Dir") is the workspace's only owner at the start.
type membersFixture struct {
	*p2Fixture
	admin, member, other *client
	adminID, memberID    string // member ids (not user ids)
	otherID              string
	memberUserID         string
	otherUserID          string
}

func newMembersFixture(t *testing.T) *membersFixture {
	t.Helper()
	f := &membersFixture{p2Fixture: newP2Fixture(t)}
	join := func(name, email, role string) (*client, string, string) {
		c := &client{t: t, srv: f.api.srv}
		_, _, hdr := c.do("POST", f.p+"/auth/signup", map[string]any{"display_name": name, "email": email, "password": "password123"})
		c.cookie = hdr.Get("Set-Cookie")
		inv := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/invites", map[string]any{"role": role})
		m := c.must(200, "POST", f.p+"/invites/"+str(inv, "token")+"/accept", nil)
		return c, str(m, "id"), str(m["user"].(map[string]any), "id")
	}
	f.admin, f.adminID, _ = join("Adm", "adm@example.com", "admin")
	f.member, f.memberID, f.memberUserID = join("Mem", "mem@example.com", "member")
	f.other, f.otherID, f.otherUserID = join("Oth", "oth@example.com", "member")
	return f
}

func (f *membersFixture) memberPath(id string) string {
	return f.p + "/workspaces/" + f.wsID + "/members/" + id
}

func (f *membersFixture) roleOf(t *testing.T, memberID string) string {
	t.Helper()
	var role string
	if err := f.pool.QueryRow(t.Context(), `SELECT role::text FROM member WHERE id = $1`, memberID).Scan(&role); err != nil {
		t.Fatal(err)
	}
	return role
}

func (f *membersFixture) activity(t *testing.T, action string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM activity_log WHERE workspace_id = $1 AND action = $2`, f.wsID, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestP5UpdateMemberRoleAuthz is the contract's permission line for
// updateMemberRole: "권한: owner · admin. owner 강등은 owner 만. 마지막 owner 는
// 강등할 수 없다(409)."
func TestP5UpdateMemberRoleAuthz(t *testing.T) {
	f := newMembersFixture(t)
	ownerMemberID := func() string {
		var id string
		if err := f.pool.QueryRow(t.Context(), `SELECT id FROM member WHERE workspace_id = $1 AND role = 'owner' ORDER BY created_at LIMIT 1`, f.wsID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}()

	t.Run("member → 403 admin_required", func(t *testing.T) {
		st, out, _ := f.member.do("PATCH", f.memberPath(f.otherID), map[string]any{"role": "admin"})
		if st != 403 || str(out, "code") != "admin_required" {
			t.Fatalf("= %d %v", st, out)
		}
		if got := f.roleOf(t, f.otherID); got != "member" {
			t.Fatalf("role changed to %s by a member", got)
		}
	})
	t.Run("outsider → 403 not_member", func(t *testing.T) {
		outsider := &client{t: t, srv: f.api.srv}
		_, _, hdr := outsider.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "X", "email": "x@example.com", "password": "password123"})
		outsider.cookie = hdr.Get("Set-Cookie")
		st, out, _ := outsider.do("PATCH", f.memberPath(f.otherID), map[string]any{"role": "admin"})
		if st != 403 || str(out, "code") != "not_member" {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("admin promotes member → admin 200", func(t *testing.T) {
		out := f.admin.must(200, "PATCH", f.memberPath(f.otherID), map[string]any{"role": "admin"})
		if str(out, "role") != "admin" || str(out, "id") != f.otherID || str(out["user"].(map[string]any), "id") != f.otherUserID {
			t.Fatalf("Member = %v", out)
		}
		if got := f.roleOf(t, f.otherID); got != "admin" {
			t.Fatalf("stored role = %s", got)
		}
		if n := f.activity(t, "member.role_changed"); n != 1 {
			t.Fatalf("activity_log member.role_changed = %d, want 1 (PRD §7)", n)
		}
	})
	t.Run("admin demotes owner → 403 owner_only", func(t *testing.T) {
		st, out, _ := f.admin.do("PATCH", f.memberPath(ownerMemberID), map[string]any{"role": "admin"})
		if st != 403 || str(out, "code") != "owner_only" {
			t.Fatalf("= %d %v", st, out)
		}
		if got := f.roleOf(t, ownerMemberID); got != "owner" {
			t.Fatalf("owner demoted by an admin: %s", got)
		}
	})
	t.Run("admin promotes to owner → 403 owner_only", func(t *testing.T) {
		st, out, _ := f.admin.do("PATCH", f.memberPath(f.adminID), map[string]any{"role": "owner"})
		if st != 403 || str(out, "code") != "owner_only" {
			t.Fatalf("admin made themself owner: %d %v", st, out)
		}
	})
	t.Run("owner demotes the last owner → 409 last_owner", func(t *testing.T) {
		st, out, _ := f.api.do("PATCH", f.memberPath(ownerMemberID), map[string]any{"role": "admin"})
		if st != 409 || str(out, "code") != "last_owner" {
			t.Fatalf("= %d %v", st, out)
		}
		if got := f.roleOf(t, ownerMemberID); got != "owner" {
			t.Fatalf("last owner demoted: %s", got)
		}
	})
	t.Run("owner promotes to owner → 200, then demotes the first owner → 200", func(t *testing.T) {
		out := f.api.must(200, "PATCH", f.memberPath(f.otherID), map[string]any{"role": "owner"})
		if str(out, "role") != "owner" {
			t.Fatalf("= %v", out)
		}
		out = f.api.must(200, "PATCH", f.memberPath(ownerMemberID), map[string]any{"role": "member"})
		if str(out, "role") != "member" {
			t.Fatalf("= %v", out)
		}
		if got := f.roleOf(t, ownerMemberID); got != "member" {
			t.Fatalf("stored role = %s", got)
		}
	})
	t.Run("same role again → 200 without a log line", func(t *testing.T) {
		before := f.activity(t, "member.role_changed")
		f.other.must(200, "PATCH", f.memberPath(ownerMemberID), map[string]any{"role": "member"})
		if after := f.activity(t, "member.role_changed"); after != before {
			t.Fatalf("no-op logged: %d → %d", before, after)
		}
	})
	t.Run("unknown role → 422 role/enum", func(t *testing.T) {
		st, out, _ := f.other.do("PATCH", f.memberPath(f.adminID), map[string]any{"role": "god"})
		if st != 422 || !hasFieldError(out, "role", "enum") {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("member of another workspace → 404", func(t *testing.T) {
		st, out, _ := f.other.do("PATCH", f.memberPath(uuid.NewString()), map[string]any{"role": "admin"})
		if st != 404 || str(out, "code") != "not_found" {
			t.Fatalf("= %d %v", st, out)
		}
	})
}

// TestP5RemoveMemberAuthz is removeMember: "권한: owner · admin. 마지막 owner 는
// 제거할 수 없다(409). 그 멤버가 Director 인 활성 세션이 있으면 409(먼저
// Director 를 교체)."
func TestP5RemoveMemberAuthz(t *testing.T) {
	f := newMembersFixture(t)
	ownerMemberID := func() string {
		var id string
		if err := f.pool.QueryRow(t.Context(), `SELECT id FROM member WHERE workspace_id = $1 AND role = 'owner'`, f.wsID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}()
	exists := func(memberID string) bool {
		var n int
		if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM member WHERE id = $1`, memberID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	t.Run("member → 403 admin_required", func(t *testing.T) {
		st, out, _ := f.member.do("DELETE", f.memberPath(f.otherID), nil)
		if st != 403 || str(out, "code") != "admin_required" || !exists(f.otherID) {
			t.Fatalf("= %d %v exists=%v", st, out, exists(f.otherID))
		}
	})
	t.Run("admin removes owner → 403 owner_only", func(t *testing.T) {
		st, out, _ := f.admin.do("DELETE", f.memberPath(ownerMemberID), nil)
		if st != 403 || str(out, "code") != "owner_only" || !exists(ownerMemberID) {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("owner removes the last owner (themself) → 409 last_owner", func(t *testing.T) {
		st, out, _ := f.api.do("DELETE", f.memberPath(ownerMemberID), nil)
		if st != 409 || str(out, "code") != "last_owner" || !exists(ownerMemberID) {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("Director of an unfinished session → 204, the room's owner takes the seat (openapi 0.2.3)", func(t *testing.T) {
		// The P2 fixture's session is directed by "Dir" (the room's owner). Hand
		// it to the plain member; removing them used to be 409
		// member_is_director — since 0.2.3 the seat passes to the room's owner.
		f.api.must(200, "PUT", f.p+"/sessions/"+f.sessionID+"/director", map[string]any{"director_user_id": f.memberUserID})
		st, out, _ := f.admin.do("DELETE", f.memberPath(f.memberID), nil)
		if st != 204 || exists(f.memberID) {
			t.Fatalf("= %d %v exists=%v", st, out, exists(f.memberID))
		}
		var director, owner string
		if err := f.pool.QueryRow(t.Context(), `
			SELECT wk.director_user_id::text, s.owner_user_id::text FROM room s JOIN work wk ON wk.id = s.legacy_work_id WHERE s.id = $1`,
			f.sessionID).Scan(&director, &owner); err != nil {
			t.Fatal(err)
		}
		if director != owner || director == f.memberUserID {
			t.Fatalf("director = %s, want the room owner %s", director, owner)
		}
		if n := f.activity(t, "member.removed"); n != 1 {
			t.Fatalf("activity_log member.removed = %d, want 1", n)
		}
		if n := f.activity(t, "work.director_succeeded"); n != 1 {
			t.Fatalf("activity_log work.director_succeeded = %d, want 1", n)
		}
		var lines int
		if err := f.pool.QueryRow(t.Context(), `
			SELECT count(*) FROM message WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%Director 를 이어받았습니다%' AND work_id IS NOT NULL`,
			f.sessionID).Scan(&lines); err != nil || lines != 1 {
			t.Fatalf("succession line on the mission timeline = %d (%v), want 1", lines, err)
		}
		// The removed person is an outsider now — not even told the member list exists.
		if st, out, _ := f.member.do("GET", f.p+"/workspaces/"+f.wsID+"/members", nil); st != 403 || str(out, "code") != "not_member" {
			t.Fatalf("removed member still reads the roster: %d %v", st, out)
		}
	})
	t.Run("Director of a cancelled session is removable", func(t *testing.T) {
		if _, err := f.pool.Exec(t.Context(), `UPDATE work SET status = 'cancelled' WHERE room_id = $1`, f.sessionID); err != nil {
			t.Fatal(err)
		}
		st, out, _ := f.admin.do("DELETE", f.memberPath(f.otherID), nil)
		if st != 204 || exists(f.otherID) {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("removing twice → 404", func(t *testing.T) {
		st, out, _ := f.admin.do("DELETE", f.memberPath(f.otherID), nil)
		if st != 404 || str(out, "code") != "not_found" {
			t.Fatalf("= %d %v", st, out)
		}
	})
	t.Run("admin removes themself → 204", func(t *testing.T) {
		st, _, _ := f.admin.do("DELETE", f.memberPath(f.adminID), nil)
		if st != 204 || exists(f.adminID) {
			t.Fatalf("= %d", st)
		}
	})
}

// TestP5NotificationSettings is getNotificationSettings · updateNotificationSettings:
// "권한: 로그인한 사용자(본인)". Defaults are the openapi defaults; a PATCH is a
// partial update; an agent token cannot read anyone's settings.
func TestP5NotificationSettings(t *testing.T) {
	f := newMembersFixture(t)
	path := f.p + "/me/notification-settings"

	t.Run("defaults", func(t *testing.T) {
		out := f.member.must(200, "GET", path, nil)
		if out["email"] != true || out["push"] != false || str(out, "default_subscription") != "all" {
			t.Fatalf("defaults = %v, want email true · push false · all (openapi NotificationSettings)", out)
		}
	})
	t.Run("partial update keeps the rest", func(t *testing.T) {
		out := f.member.must(200, "PATCH", path, map[string]any{"push": true})
		if out["email"] != true || out["push"] != true || str(out, "default_subscription") != "all" {
			t.Fatalf("after push=true: %v", out)
		}
		out = f.member.must(200, "PATCH", path, map[string]any{"default_subscription": "hitl_only", "email": false})
		if out["email"] != false || out["push"] != true || str(out, "default_subscription") != "hitl_only" {
			t.Fatalf("after second patch: %v", out)
		}
		got := f.member.must(200, "GET", path, nil)
		if got["email"] != false || got["push"] != true || str(got, "default_subscription") != "hitl_only" {
			t.Fatalf("GET after PATCH: %v", got)
		}
	})
	t.Run("only the caller's own row moves", func(t *testing.T) {
		other := f.other.must(200, "GET", path, nil)
		if other["email"] != true || other["push"] != false || str(other, "default_subscription") != "all" {
			t.Fatalf("another user's settings changed: %v", other)
		}
	})
	t.Run("bad subscription level → 422", func(t *testing.T) {
		st, out, _ := f.member.do("PATCH", path, map[string]any{"default_subscription": "everything"})
		if st != 422 || !hasFieldError(out, "default_subscription", "enum") {
			t.Fatalf("= %d %v", st, out)
		}
		got := f.member.must(200, "GET", path, nil)
		if str(got, "default_subscription") != "hitl_only" {
			t.Fatalf("stored level changed by a rejected PATCH: %v", got)
		}
	})
	t.Run("anonymous → 401, agent token → 403 task_token_scope", func(t *testing.T) {
		anon := &client{t: t, srv: f.api.srv}
		if st, _, _ := anon.do("GET", path, nil); st != 401 {
			t.Fatalf("anonymous GET = %d", st)
		}
		cli := f.taskTokenFor(t, mustUUID(t, f.sessionID), f.leadUUID)
		if st, out, _ := cli.do("GET", path, nil); st != 403 || str(out, "code") != "task_token_scope" {
			t.Fatalf("agent token GET = %d %v", st, out)
		}
		if st, out, _ := cli.do("PATCH", path, map[string]any{"push": true}); st != 403 || str(out, "code") != "task_token_scope" {
			t.Fatalf("agent token PATCH = %d %v", st, out)
		}
	})
}
