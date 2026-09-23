package auth

import (
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// TestPlanRoleChange is the updateMemberRole permission table — SCREEN §2.3
// "멤버 초대·역할 변경 owner ✅ admin ✅ · owner 강등은 owner 만" plus the 409.
func TestPlanRoleChange(t *testing.T) {
	for _, c := range []struct {
		name   string
		in     RoleChangeCase
		status int
		code   string
	}{
		{"admin promotes member to admin", RoleChangeCase{"admin", "member", "admin", 1}, 0, ""},
		{"admin demotes admin to member", RoleChangeCase{"admin", "admin", "member", 1}, 0, ""},
		{"admin demotes owner", RoleChangeCase{"admin", "owner", "admin", 2}, 403, CodeOwnerOnly},
		{"admin promotes to owner", RoleChangeCase{"admin", "member", "owner", 1}, 403, CodeOwnerOnly},
		{"owner demotes the only owner", RoleChangeCase{"owner", "owner", "admin", 1}, 409, CodeLastOwner},
		{"owner demotes one of two owners", RoleChangeCase{"owner", "owner", "admin", 2}, 0, ""},
		{"owner promotes to owner", RoleChangeCase{"owner", "member", "owner", 1}, 0, ""},
		{"owner re-states owner (no demotion)", RoleChangeCase{"owner", "owner", "owner", 1}, 0, ""},
		// S-75 (PR #209 리뷰 NN5): the caller demoting THEMSELF is the same
		// row as "one of two owners" — the planner has no notion of self,
		// and the contract does not forbid it. The confirmation dialog is
		// the web's (T-W12), not a server rule.
		{"owner demotes themself, another owner remains", RoleChangeCase{"owner", "owner", "member", 2}, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := PlanRoleChange(c.in)
			if c.status == 0 {
				if p != nil {
					t.Fatalf("want allowed, got %d %s", p.Status, p.Code)
				}
				return
			}
			if p == nil || p.Status != c.status || p.Code != c.code {
				t.Fatalf("want %d %s, got %v", c.status, c.code, p)
			}
		})
	}
}

// TestPlanRemoval is removeMember: last owner 409, owner removal owner-only.
// A Director seat no longer refuses (openapi 0.2.3 — it passes to the room's
// owner, rooms.LeaveWorkspace).
func TestPlanRemoval(t *testing.T) {
	for _, c := range []struct {
		name   string
		in     RemovalCase
		status int
		code   string
	}{
		{"admin removes member", RemovalCase{"admin", "member", 1}, 0, ""},
		{"admin removes admin", RemovalCase{"admin", "admin", 1}, 0, ""},
		{"admin removes owner", RemovalCase{"admin", "owner", 2}, 403, CodeOwnerOnly},
		{"owner removes the only owner", RemovalCase{"owner", "owner", 1}, 409, CodeLastOwner},
		{"owner removes one of two owners", RemovalCase{"owner", "owner", 2}, 0, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := PlanRemoval(c.in)
			if c.status == 0 {
				if p != nil {
					t.Fatalf("want allowed, got %d %s", p.Status, p.Code)
				}
				return
			}
			if p == nil || p.Status != c.status || p.Code != c.code {
				t.Fatalf("want %d %s, got %v", c.status, c.code, p)
			}
		})
	}
}

func TestNotificationPatch(t *testing.T) {
	def := DefaultNotificationSettings()
	if !def.Email || def.Push || def.DefaultSubscription != gen.SubscriptionLevelAll {
		t.Fatalf("default = %+v, want email true · push false · all", def)
	}
	f, tr, lvl := false, true, "completion_only"
	got := ApplyNotificationPatch(def, NotificationPatch{Push: &tr})
	if !got.Email || !got.Push || got.DefaultSubscription != gen.SubscriptionLevelAll {
		t.Fatalf("push only: %+v", got)
	}
	got = ApplyNotificationPatch(got, NotificationPatch{Email: &f, DefaultSubscription: &lvl})
	if got.Email || !got.Push || got.DefaultSubscription != gen.SubscriptionLevelCompletionOnly {
		t.Fatalf("email+level: %+v", got)
	}
	if ApplyNotificationPatch(got, NotificationPatch{}) != got {
		t.Fatal("empty patch changed something")
	}
	for _, ok := range []string{"all", "hitl_only", "completion_only"} {
		if p := ValidateSubscriptionLevel(ok); p != nil {
			t.Fatalf("%s rejected: %v", ok, p)
		}
	}
	if p := ValidateSubscriptionLevel("everything"); p == nil || p.Status != 422 || len(p.Errors) != 1 || p.Errors[0].Field != "default_subscription" {
		t.Fatalf("bad level: %v", p)
	}
	// A pre-0021 row (or a hand-edited jsonb) with keys missing or invalid
	// reads as the default for those keys, never as a zero value.
	got = decodeNotificationSettings([]byte(`{"push": true, "default_subscription": "nonsense"}`))
	if !got.Email || !got.Push || got.DefaultSubscription != gen.SubscriptionLevelAll {
		t.Fatalf("partial row: %+v", got)
	}
	if decodeNotificationSettings(nil) != def || decodeNotificationSettings([]byte(`garbage`)) != def {
		t.Fatal("empty/garbage row should read as the default")
	}
}
