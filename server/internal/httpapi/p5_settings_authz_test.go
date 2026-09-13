package httpapi

import (
	"testing"
)

// S-69 · S-70 (T-W6 PR #199 대조, Lead 번호): getWorkspaceSettings is member
// READ (openapi "권한: 워크스페이스 멤버(읽기)"), updateWorkspaceSettings is
// owner·admin — and `task_event_masking` (보안 탭) is owner only.
//
// Before this the GET asked for owner/admin (a member opening the S14 tabs got
// the 403 sentence) and the PATCH let an admin flip the masking that decides
// what of a diff or a shell output is stored at all.
func TestP5SettingsMemberReadsOwnerMasks(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	join := func(email string, role string) *client {
		t.Helper()
		c := &client{t: t, srv: f.api.srv}
		_, _, hdr := c.do("POST", f.p+"/auth/signup", map[string]any{"display_name": role, "email": email, "password": "password123"})
		c.cookie = hdr.Get("Set-Cookie")
		inv := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/invites", map[string]any{})
		c.must(200, "POST", f.p+"/invites/"+str(inv, "token")+"/accept", nil)
		if role != "member" {
			// updateMemberRole is a P5 op still behind 501 in this build; the
			// role is what the test is about, not the verb that sets it.
			if _, err := f.pool.Exec(ctx, `UPDATE member SET role = $3 WHERE workspace_id = $1 AND user_id = (SELECT id FROM app_user WHERE email = $2)`,
				mustUUID(t, f.wsID), email, role); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}
	member := join("member@example.com", "member")
	admin := join("admin@example.com", "admin")
	path := f.p + "/workspaces/" + f.wsID + "/settings"

	// S-69: a member reads.
	got := member.must(200, "GET", path, nil)
	if got["loop_limits"] == nil || got["task_event_masking"] == nil {
		t.Errorf("member GET returned %v, want the whole WorkspaceSettings", got)
	}
	if str(got, "workspace_id") != f.wsID {
		t.Errorf("workspace_id = %q, want %s (was the zero uuid — found while testing S-69)", str(got, "workspace_id"), f.wsID)
	}
	// …and still cannot write.
	if st, out, _ := member.do("PATCH", path, map[string]any{"workdir_retention_days": 7}); st != 403 || str(out, "code") != "admin_required" {
		t.Errorf("member PATCH = %d %v, want 403 admin_required", st, out)
	}

	// S-70: an admin writes everything but the masking.
	upd := admin.must(200, "PATCH", path, map[string]any{"workdir_retention_days": 7})
	if upd["workdir_retention_days"].(float64) != 7 {
		t.Errorf("admin PATCH retention = %v", upd["workdir_retention_days"])
	}
	st, out, _ := admin.do("PATCH", path, map[string]any{"task_event_masking": true})
	if st != 403 || str(out, "code") != "owner_required" {
		t.Errorf("admin PATCH task_event_masking = %d %v, want 403 owner_required", st, out)
	}
	// The refusal is whole: nothing else in the same body is applied either.
	st, out, _ = admin.do("PATCH", path, map[string]any{"task_event_masking": true, "workdir_retention_days": 3})
	if st != 403 {
		t.Errorf("admin PATCH masking+retention = %d %v, want 403", st, out)
	}
	if got := admin.must(200, "GET", path, nil); got["workdir_retention_days"].(float64) != 7 || got["task_event_masking"] != false {
		t.Errorf("a refused PATCH changed settings: %v", got)
	}
	// The owner may.
	if got := f.api.must(200, "PATCH", path, map[string]any{"task_event_masking": true}); got["task_event_masking"] != true {
		t.Errorf("owner PATCH task_event_masking = %v, want true", got["task_event_masking"])
	}
}
