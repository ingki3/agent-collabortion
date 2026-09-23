package httpapi

// openapi 0.2.9 (PR #308, T-R2-W4a gaps) over HTTP: setRoomSubscription ·
// Room.my_subscription · Lane.my_subscription · Agent.rooms[] ·
// running_task_count · Runtime.room_count · InboxItem.room ·
// BlockedDetail.open_works_remaining_usd.

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// TestR2RoomSubscription: the caller's own level, stored per person; with no
// row the personal default where the value sets meet (completion_only has no
// room counterpart → all). Who may set it is Decide(ActSubscribe) — a
// participant; a member who can only look is 403, a room they cannot see 404.
func TestR2RoomSubscription(t *testing.T) {
	f := newRoomsFixture(t)
	rid := str(f.mkRoom(t, f.member, "구독 방"), "id")
	rp := f.roomPath(rid)
	sub := func(c *client) string {
		t.Helper()
		return str(c.must(200, "GET", rp, nil), "my_subscription")
	}
	if got := sub(f.member); got != "all" {
		t.Fatalf("no row, default all: my_subscription = %q, want all", got)
	}
	f.member.must(200, "PATCH", f.p+"/me/notification-settings", map[string]any{"default_subscription": "hitl_only"})
	if got := sub(f.member); got != "hitl_only" {
		t.Fatalf("no row, default hitl_only: my_subscription = %q, want hitl_only", got)
	}
	f.member.must(200, "PATCH", f.p+"/me/notification-settings", map[string]any{"default_subscription": "completion_only"})
	if got := sub(f.member); got != "all" {
		t.Fatalf("no row, default completion_only: my_subscription = %q, want all (no room counterpart)", got)
	}

	for _, level := range []string{"my_works", "off", "hitl_only", "all", "my_works"} {
		if st, _, _ := f.member.raw("PUT", rp+"/subscription", map[string]any{"level": level}); st != 204 {
			t.Fatalf("setRoomSubscription %s = %d, want 204", level, st)
		}
		if got := sub(f.member); got != level {
			t.Fatalf("after set %s: my_subscription = %q", level, got)
		}
	}
	// Per person: the workspace owner reading the same room sees their own.
	if got := sub(f.api); got != "all" {
		t.Fatalf("another person's my_subscription = %q, want their own default all", got)
	}
	if st, out, _ := f.member.do("PUT", rp+"/subscription", map[string]any{"level": "completion_only"}); st != 422 || !hasFieldError(out, "level", "enum") {
		t.Fatalf("room level outside RoomSubscriptionLevel = %d %v, want 422 level/enum", st, out)
	}
	// Looking is not subscribing (Decide ActSubscribe = participant).
	if st, out, _ := f.other.do("PUT", rp+"/subscription", map[string]any{"level": "off"}); st != 403 {
		t.Fatalf("non-participant member of a workspace room = %d %v, want 403", st, out)
	}
	f.member.must(200, "PATCH", rp, map[string]any{"visibility": "invited"})
	if st, out, _ := f.other.do("PUT", rp+"/subscription", map[string]any{"level": "off"}); st != 404 {
		t.Fatalf("a room the caller cannot see = %d %v, want 404", st, out)
	}
	if n := f.count(t, `SELECT count(*) FROM session_subscription WHERE session_id = $1`, rid); n != 1 {
		t.Fatalf("stored rows = %d, want the member's one", n)
	}
}

// TestR2LaneMySubscription: listLanes carries the caller's setLaneSubscription
// value, null when they never set one; another person's choice is not theirs.
func TestR2LaneMySubscription(t *testing.T) {
	f := newRoomsFixture(t)
	lanes := func(c *client) map[string]any {
		t.Helper()
		out := map[string]any{}
		for _, raw := range c.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil) {
			l := raw.(map[string]any)
			v, ok := l["my_subscription"]
			if !ok {
				t.Fatalf("lane %s has no my_subscription key, want a value or null", str(l, "id"))
			}
			out[str(l, "id")] = v
		}
		return out
	}
	before := lanes(f.api)
	if len(before) == 0 {
		t.Fatal("premise: the session has a lane")
	}
	var lane string
	for id, v := range before {
		lane = id
		if v != nil {
			t.Fatalf("lane %s my_subscription = %v before any choice, want null", id, v)
		}
	}
	f.api.must(204, "PUT", f.p+"/lanes/"+lane+"/subscription", map[string]any{"enabled": false})
	if v := lanes(f.api)[lane]; v != false {
		t.Fatalf("after off: my_subscription = %v, want false", v)
	}
	f.api.must(204, "PUT", f.p+"/lanes/"+lane+"/subscription", map[string]any{"enabled": true})
	if v := lanes(f.api)[lane]; v != true {
		t.Fatalf("after on: my_subscription = %v, want true", v)
	}
	if v := lanes(f.admin)[lane]; v != nil {
		t.Fatalf("admin's view of the Dir's choice = %v, want null (per caller)", v)
	}
}

// TestR2AgentRoomsAndRunning: Agent.rooms[] names only the rooms the caller
// can open (Decide ActView — the same judgement getRoom's 404 uses) and counts
// the rest in hidden_room_count; running_task_count is the agent's slot count
// across every room (hitl.OccupyingStatuses — max_concurrent_tasks' count).
func TestR2AgentRoomsAndRunning(t *testing.T) {
	f := newRoomsFixture(t)
	secret := str(f.mkRoom(t, f.member, "비밀 방"), "id")
	f.api.must(201, "POST", f.roomPath(secret)+"/participants", map[string]any{"agent_id": f.lead}) // only its maker invites it
	f.member.must(200, "PATCH", f.roomPath(secret), map[string]any{"visibility": "invited"})
	var open string
	if err := f.pool.QueryRow(t.Context(), `SELECT name FROM room WHERE id = $1`, f.sessionID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	roomsOf := func(c *client) (map[string]string, int, int) {
		t.Helper()
		a := c.must(200, "GET", f.p+"/agents/"+f.lead, nil)
		got := map[string]string{}
		for _, raw := range a["rooms"].([]any) {
			r := raw.(map[string]any)
			got[str(r, "id")] = str(r, "name")
		}
		if int(a["room_count"].(float64)) != len(got) {
			t.Fatalf("room_count = %v but rooms[] has %d", a["room_count"], len(got))
		}
		return got, int(a["hidden_room_count"].(float64)), int(a["running_task_count"].(float64))
	}
	got, hidden, _ := roomsOf(f.other)
	if len(got) != 1 || got[f.sessionID] != open || hidden != 1 {
		t.Fatalf("plain member sees rooms=%v hidden=%d, want only %q and 1 hidden", got, hidden, open)
	}
	if _, leaked := got[secret]; leaked {
		t.Fatal("an invited room the caller is not in was named")
	}
	for name, c := range map[string]*client{"participant": f.member, "ws admin": f.admin} {
		if got, hidden, _ := roomsOf(c); len(got) != 2 || got[secret] != "비밀 방" || hidden != 0 {
			t.Fatalf("%s sees rooms=%v hidden=%d, want both named", name, got, hidden)
		}
	}
	// The agents list carries the same fields.
	for _, raw := range items(f.other.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/agents", nil)) {
		if a := raw.(map[string]any); str(a, "id") == f.lead {
			if rs := a["rooms"].([]any); len(rs) != 1 {
				t.Fatalf("listAgents rooms = %v, want the one visible room", rs)
			}
		}
	}

	// running_task_count: a running task in the hidden room counts too.
	mk := func(room, status string) {
		t.Helper()
		f.exec(t, `
			INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, created_at, updated_at)
			SELECT l.id, $1, l.agent_id, l.profile_id, $3::task_status, now(), now() FROM lane l WHERE l.session_id = $2 AND l.agent_id = $4 LIMIT 1`, room, f.sessionID, status, f.lead)
	}
	_, _, before := roomsOf(f.other)
	mk(f.sessionID, "running")
	mk(secret, "dispatched")
	mk(f.sessionID, "waiting_human") // process gone — holds no slot
	mk(f.sessionID, "completed")
	if _, _, n := roomsOf(f.other); n != before+2 {
		t.Fatalf("running_task_count = %d, want %d (+running +dispatched, not waiting_human/completed)", n, before+2)
	}
}

// TestR2RuntimeRoomCount: rooms fixed to the computer.
func TestR2RuntimeRoomCount(t *testing.T) {
	f := newRoomsFixture(t)
	var rt uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1`, f.wsID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		return int(f.api.must(200, "GET", f.p+"/runtimes/"+rt.String(), nil)["room_count"].(float64))
	}
	base := count()
	if want := f.count(t, `SELECT count(*) FROM room WHERE runtime_id = $1`, rt); base != want {
		t.Fatalf("room_count = %d, want %d", base, want)
	}
	rid := str(f.mkRoom(t, f.api, "고정 방"), "id")
	f.exec(t, `UPDATE room SET runtime_id = $2 WHERE id = $1`, rid, rt)
	if got := count(); got != base+1 {
		t.Fatalf("after pinning one more room: room_count = %d, want %d", got, base+1)
	}
	f.exec(t, `UPDATE room SET runtime_id = NULL WHERE id = $1`, rid)
	if got := count(); got != base {
		t.Fatalf("after unpinning: room_count = %d, want %d", got, base)
	}
	for _, raw := range f.api.mustList(200, "GET", f.p+"/workspaces/"+f.wsID+"/runtimes", nil) {
		if r := raw.(map[string]any); str(r, "id") == rt.String() && int(r["room_count"].(float64)) != base {
			t.Fatalf("listRuntimes room_count = %v, want %d", r["room_count"], base)
		}
	}
}

// TestR2InboxItemRoom: the card names its room in full; a card of no room
// says null.
func TestR2InboxItemRoom(t *testing.T) {
	f := newRoomsFixture(t)
	long := "아주 긴 방 이름 — 줄이지 않는다(SCREEN §4.14) 끝까지 보여야 한다"
	f.exec(t, `UPDATE room SET name = $2 WHERE id = $1`, f.sessionID, long)
	f.exec(t, `INSERT INTO inbox_item (member_id, type, severity, session_id) SELECT id, 'session_paused', 'action_required', $2 FROM member WHERE workspace_id = $1 AND role = 'owner'`, f.wsID, f.sessionID)
	f.exec(t, `INSERT INTO inbox_item (member_id, type, severity) SELECT id, 'runtime_offline', 'info' FROM member WHERE workspace_id = $1 AND role = 'owner'`, f.wsID)
	seen := 0
	for _, raw := range items(f.api.must(200, "GET", f.p+"/inbox?workspace_id="+f.wsID, nil)) {
		it := raw.(map[string]any)
		v, ok := it["room"]
		if !ok {
			t.Fatalf("inbox item %s has no room key", str(it, "id"))
		}
		switch str(it, "type") {
		case "session_paused":
			seen++
			r, _ := v.(map[string]any)
			if r == nil || str(r, "id") != f.sessionID || str(r, "name") != long {
				t.Fatalf("room = %v, want {%s, %q}", v, f.sessionID, long)
			}
		case "runtime_offline":
			seen++
			if v != nil {
				t.Fatalf("card of no room: room = %v, want null", v)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d of the 2 planted items", seen)
	}
}

// TestR2OpenWorksRemaining: the release card's sum is over the missions the
// approval sets moving again — for budget the ones the gate parked — each
// its own budget minus cost, never below 0; a mission with no budget adds
// nothing, and with no budgeted mission at all the sum is null.
func TestR2OpenWorksRemaining(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	done := str(f.openWork(t, f.api, rid, map[string]any{"goal": "끝", "limits": map[string]any{"budget_usd": 100}}), "id")
	f.exec(t, `UPDATE work SET status = 'completed', finished_at = now() WHERE id = $1`, done)
	w1 := str(f.openWork(t, f.api, rid, map[string]any{"goal": "하나", "limits": map[string]any{"budget_usd": 10}}), "id")
	w2 := str(f.openWork(t, f.api, rid, map[string]any{"goal": "둘", "limits": map[string]any{"budget_usd": 5}}), "id")
	w3 := str(f.openWork(t, f.api, rid, map[string]any{"goal": "셋"}), "id")
	f.exec(t, `UPDATE work SET status = 'active' WHERE id = ANY($1::uuid[])`, []string{w1, w2, w3})
	f.exec(t, `UPDATE work SET cost_usd = 3 WHERE id = $1`, w1)
	f.exec(t, `UPDATE work SET cost_usd = 6 WHERE id = $1`, w2) // over budget → 0, not −1

	remaining := func(room string) any {
		t.Helper()
		d, _ := f.api.must(200, "GET", f.roomPath(room), nil)["blocked_detail"].(map[string]any)
		if d == nil {
			t.Fatal("blocked_detail absent on a blocked room")
		}
		v, ok := d["open_works_remaining_usd"]
		if !ok {
			return "<absent>"
		}
		return v
	}
	block := func(room, reason string) {
		t.Helper()
		ctx := context.Background()
		tx, err := f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var wd *gen.PausedDetail
		if roomgate.Mirrors(reason) {
			d := tasks.PausedDetail(reason, f.fake.Now())
			wd = &d
		}
		if _, err := roomgate.Block(ctx, tx, mustUUID(t, room), reason, gen.BlockedDetail{}, wd, f.fake.Now()); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	block(rid, roomgate.ReasonBudget)
	if got := remaining(rid); got != 7.0 {
		t.Fatalf("budget block: open_works_remaining_usd = %v, want 7 (10−3 + max(5−6,0), no-budget and completed ones out)", got)
	}
	// A mission the Director paused on its own is not one the approval restarts.
	f.exec(t, `UPDATE work SET paused_detail = paused_detail - $2 WHERE id = $1`, w1, roomgate.MirrorKey)
	if got := remaining(rid); got != 0.0 {
		t.Fatalf("with the budgeted mission no longer the gate's: %v, want 0", got)
	}

	// Manual: the active missions the gate holds.
	other := f.worksRoom(t)
	wa := str(f.openWork(t, f.api, other, map[string]any{"goal": "가", "limits": map[string]any{"budget_usd": 4}}), "id")
	f.exec(t, `UPDATE work SET status = 'active', cost_usd = 1.5 WHERE id = $1`, wa)
	block(other, roomgate.ReasonManual)
	if got := remaining(other); got != 2.5 {
		t.Fatalf("manual block: %v, want 2.5", got)
	}
	f.exec(t, `UPDATE work SET limits = '{}'::jsonb WHERE id = $1`, wa)
	if got := remaining(other); got != nil {
		t.Fatalf("no budgeted mission: %v, want null", got)
	}
}
