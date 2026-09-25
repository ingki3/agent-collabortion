package httpapi

// T-BUDGETCAP (Director 2026-09-25): the two workspace budget defaults S14
// offers reach what they name — room_defaults.limits.budget_usd → a new room,
// budget_policy.default_session_budget_usd → a new mission that left its
// budget out. Unset, both stay "no cap" (the product default is unchanged).

import (
	"testing"
)

func workBudget(w map[string]any) any {
	l, _ := w["limits"].(map[string]any)
	return l["budget_usd"]
}

func roomBudget(r map[string]any) any {
	l, _ := r["limits"].(map[string]any)
	return l["budget_usd"]
}

func TestBudgetCapDefaultsReachNewRoomsAndMissions(t *testing.T) {
	f := newRoomsFixture(t)
	settings := f.p + "/workspaces/" + f.wsID + "/settings"
	f.exec(t, `UPDATE workspace_settings SET room_defaults = '{}'::jsonb, budget_policy = '{}'::jsonb WHERE workspace_id = $1`, f.wsID)

	// Nothing set — no cap anywhere (the default Director asked to keep).
	r0 := f.mkRoom(t, f.api, "상한 없는 방")
	if b := roomBudget(r0); b != nil {
		t.Fatalf("room budget with no workspace default = %v, want none", b)
	}
	rid0 := str(r0, "id")
	if b := workBudget(f.openWork(t, f.api, rid0, map[string]any{"goal": "상한 없음"})); b != nil {
		t.Fatalf("mission budget with no workspace default = %v, want none", b)
	}

	// room_defaults.limits.budget_usd — S14 「새 방의 기본 예산 상한」.
	f.api.must(200, "PATCH", settings, map[string]any{"room_defaults": map[string]any{"limits": map[string]any{"budget_usd": 20}}})
	r1 := f.mkRoom(t, f.api, "기본 상한 방")
	if b := roomBudget(r1); b != float64(20) {
		t.Fatalf("new room budget = %v, want the workspace default 20", b)
	}
	if b := roomBudget(f.api.must(200, "GET", f.roomPath(str(r0, "id")), nil)); b != nil {
		t.Fatalf("an existing room must not change: %v", b)
	}

	// budget_policy.default_session_budget_usd — S14 「새 미션의 기본 예산 상한」.
	f.api.must(200, "PATCH", settings, map[string]any{"budget_policy": map[string]any{"default_session_budget_usd": 5}})
	rid1 := str(r1, "id")
	if b := workBudget(f.openWork(t, f.api, rid1, map[string]any{"goal": "기본 상한 미션"})); b != float64(5) {
		t.Fatalf("mission without a budget = %v, want the workspace default 5", b)
	}
	// Other limit keys named, budget left out → still the default.
	if b := workBudget(f.openWork(t, f.api, rid1, map[string]any{"goal": "시간만", "limits": map[string]any{"time_limit": "PT4H"}})); b != float64(5) {
		t.Fatalf("mission with time_limit only = %v, want the workspace default 5", b)
	}
	// Its own budget wins over the default.
	if b := workBudget(f.openWork(t, f.api, rid0, map[string]any{"goal": "자기 상한", "limits": map[string]any{"budget_usd": 2}})); b != float64(2) {
		t.Fatalf("mission with its own budget = %v, want 2", b)
	}
	// An explicit null is "no mission cap — follow the room", not the default.
	if b := workBudget(f.openWork(t, f.api, rid0, map[string]any{"goal": "명시적 없음", "limits": map[string]any{"budget_usd": nil}})); b != nil {
		t.Fatalf("mission with budget_usd null = %v, want none", b)
	}

	// Clearing the default goes back to no cap.
	f.api.must(200, "PATCH", settings, map[string]any{"budget_policy": map[string]any{"default_session_budget_usd": nil}})
	if b := workBudget(f.openWork(t, f.api, rid1, map[string]any{"goal": "다시 없음"})); b != nil {
		t.Fatalf("mission after clearing the default = %v, want none", b)
	}
}
