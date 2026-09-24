package httpapi

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

// T-S-offline (PRD FR-9.2 v0.19 · #313 리뷰 §4): a computer that stays gone
// past the grace stops the ROOM it is pinned to — room.blocked_reason
// runtime_offline, every active mission parked with the room's mark (the old
// /sessions/* still reads paused(runtime_offline)) — and the choice goes to
// the room owner's chain as ONE room_paused card. Rebinding, or cancelling
// every open mission, lifts the stop.

// pinRuntime pins a room to the fixture's computer (mac-1), as its first
// claim would.
func (f *p2Fixture) pinRuntime(t *testing.T, roomID string) (runtimeID string) {
	t.Helper()
	if err := f.pool.QueryRow(t.Context(), `
		UPDATE room SET runtime_id = (SELECT id FROM runtime WHERE workspace_id = $2 AND name = 'mac-1')
		WHERE id = $1 RETURNING runtime_id::text`, roomID, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	return runtimeID
}

// offlineSweep sends the session's computer away for eight days (grace is
// seven) and runs the sweep once.
func (f *p2Fixture) offlineSweep(t *testing.T) (runtimeID string) {
	t.Helper()
	runtimeID = f.pinRuntime(t, f.sessionID)
	f.exec(t, `UPDATE runtime SET status = 'offline', offline_since = $2 WHERE id = $1`,
		runtimeID, f.fake.Now().Add(-8*24*time.Hour))
	if n, err := f.srv.Runtimes.SweepOffline(t.Context()); err != nil || n != 1 {
		t.Fatalf("sweep stopped %d rooms (err %v), want 1", n, err)
	}
	return runtimeID
}

func TestR2RuntimeOfflineStopsTheRoom(t *testing.T) {
	f := newP2Fixture(t)
	deputy := f.addMember(t, "dep@example.com", "Dep")
	f.exec(t, `UPDATE room SET deputy_owner_user_id = $2 WHERE id = $1`, f.sessionID, deputy.userID)
	rt := f.offlineSweep(t)

	// The room's gate, naming the computer.
	reason, detail := f.roomGate(t)
	if reason != "runtime_offline" || str(detail, "runtime_id") != rt || detail["works_stopped"] != float64(1) {
		t.Fatalf("room gate = %q %v, want runtime_offline · runtime_id %s · works_stopped 1", reason, detail, rt)
	}
	// The mirror: the mission is parked with the room's mark …
	if st, why, mirror := f.missionState(t); st != "paused" || why != "runtime_offline" || !mirror {
		t.Fatalf("mission = %s(%s) mirror=%v, want paused(runtime_offline) marked as the room's", st, why, mirror)
	}
	// … so the old session shape reads exactly as it did before v0.19.
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	if str(sess, "status") != "paused" || str(sess, "paused_reason") != "runtime_offline" {
		t.Fatalf("old session = %s(%s), want paused(runtime_offline)", str(sess, "status"), str(sess, "paused_reason"))
	}
	// The banner names who answers now (the owner) and who from half the
	// deadline (the deputy) — the chain the card and the 403 use.
	room := f.api.must(200, "GET", f.p+"/rooms/"+f.sessionID, nil)
	bd, _ := room["blocked_detail"].(map[string]any)
	if ap, _ := bd["approver"].(map[string]any); ap == nil || str(ap, "display_name") != "Dir" {
		t.Fatalf("banner approver = %v, want the owner", bd["approver"])
	}
	if str(bd, "next_approver_role") != "room_deputy" || bd["delegate_at"] == nil {
		t.Fatalf("banner next = %v at %v, want room_deputy", bd["next_approver_role"], bd["delegate_at"])
	}

	// ONE room_paused card, ref = the lost computer; no runtime_offline item.
	if got := inboxOf(t, f.api, f.wsID, "runtime_offline"); len(got) != 0 {
		t.Fatalf("runtime_offline items = %v, want none — one stop, one card", got)
	}
	own := inboxOf(t, f.api, f.wsID, "room_paused")
	if len(own) != 1 || str(own[0], "ref_id") != rt || str(own[0], "recipient_basis") != "room_owner" ||
		str(own[0], "severity") != "action_required" {
		t.Fatalf("owner's room_paused = %v, want one card · ref the runtime · room_owner · action_required", own)
	}
	if a := actionsOf(own[0]); !slices.Equal(a, []string{"rebind", "open_room"}) {
		t.Fatalf("owner's actions = %v, want [rebind open_room]", a)
	}
	dep := inboxOf(t, deputy.client, f.wsID, "room_paused")
	if len(dep) != 1 || str(dep[0], "recipient_basis") != "room_deputy" || dep[0]["delegated"] != true {
		t.Fatalf("deputy's room_paused = %v, want one delegated room_deputy card", dep)
	}
	if a := actionsOf(dep[0]); !slices.Equal(a, []string{"open_room"}) {
		t.Fatalf("deputy's actions before half = %v, want [open_room]", a)
	}

	// A second pass files nothing (E14-10).
	f.fake.Advance(time.Minute)
	if n, err := f.srv.Runtimes.SweepOffline(t.Context()); err != nil || n != 0 {
		t.Fatalf("second sweep stopped %d (err %v), want 0", n, err)
	}
	if got := inboxOf(t, f.api, f.wsID, "room_paused"); len(got) != 1 {
		t.Fatalf("room_paused after the second sweep = %d, want 1", len(got))
	}

	// Before half the deadline the deputy may not rebind (the card said so).
	target := f.onlineTarget(t)
	deputy.client.must(403, "POST", f.p+"/sessions/"+f.sessionID+"/rebind", map[string]any{"runtime_id": target})
	// From half, the deputy answers — and rebinding lifts the room's stop.
	f.fake.Advance(13 * time.Hour)
	if a := actionsOf(inboxOf(t, deputy.client, f.wsID, "room_paused")[0]); !slices.Equal(a, []string{"rebind", "open_room"}) {
		t.Fatalf("deputy's actions after half = %v, want [rebind open_room]", a)
	}
	deputy.client.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/rebind", map[string]any{"runtime_id": target})
	if reason, _ := f.roomGate(t); reason != "" {
		t.Fatalf("room gate = %q after rebind, want lifted", reason)
	}
	if st, why, _ := f.missionState(t); st != "active" || why != "" {
		t.Fatalf("mission = %s(%s) after rebind, want active", st, why)
	}
	own = inboxOf(t, f.api, f.wsID, "room_paused")
	if len(own) != 1 || own[0]["read_at"] == nil {
		t.Fatalf("owner's card after rebind = %v, want resolved (read)", own)
	}
	if a := actionsOf(own[0]); !slices.Equal(a, []string{"open_room"}) {
		t.Fatalf("owner's actions after rebind = %v, want [open_room] — nothing left to choose", a)
	}
}

// TestR2RuntimeOfflineRoomCardsPerRoom: two rooms on the same lost computer,
// same owner — two cards. The card's ref is the runtime, which both share;
// a duplicate guard keyed on the ref alone filed only the first.
func TestR2RuntimeOfflineRoomCardsPerRoom(t *testing.T) {
	f := newP2Fixture(t)
	other := f.mkSession(t)
	rt := f.pinRuntime(t, f.sessionID)
	f.pinRuntime(t, other)
	f.exec(t, `UPDATE runtime SET status = 'offline', offline_since = $2 WHERE id = $1`, rt, f.fake.Now().Add(-8*24*time.Hour))
	if n, err := f.srv.Runtimes.SweepOffline(t.Context()); err != nil || n != 2 {
		t.Fatalf("sweep stopped %d rooms (err %v), want 2", n, err)
	}
	rooms := map[string]bool{}
	for _, it := range inboxOf(t, f.api, f.wsID, "room_paused") {
		rooms[str(it, "room_id")] = true
	}
	if !rooms[f.sessionID] || !rooms[other] {
		t.Fatalf("room_paused cards for %v, want both rooms", rooms)
	}
}

// TestR2RuntimeOfflineCancelLiftsTheStop is FR-9.2's other way out: the last
// open mission cancelled, the room has nothing left waiting for the machine.
func TestR2RuntimeOfflineCancelLiftsTheStop(t *testing.T) {
	f := newP2Fixture(t)
	f.offlineSweep(t)
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/cancel", map[string]any{})
	if reason, _ := f.roomGate(t); reason != "" {
		t.Fatalf("room gate = %q after cancelling every open mission, want lifted", reason)
	}
	if st, _, _ := f.missionState(t); st != "cancelled" {
		t.Fatalf("mission = %s, want cancelled (E14-07)", st)
	}
	if own := inboxOf(t, f.api, f.wsID, "room_paused"); len(own) != 1 || own[0]["read_at"] == nil {
		t.Fatalf("owner's card after cancel = %v, want resolved", own)
	}
}

// onlineTarget is a second computer the room may move to (the fixture's room
// has no repository, so any online machine is a candidate — E14-03).
func (f *p2Fixture) onlineTarget(t *testing.T) string {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO runtime (workspace_id, name, status, last_seen_at, created_at, updated_at)
		VALUES ($1, 'mac-2', 'online', $2, $2, $2) RETURNING id`, f.wsID, f.fake.Now()).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

// TestR2InboxHiddenItemOffersNoApprove is #313 NN1: a Director put out of an
// invited room keeps the approval card that came to them, but the room is gone
// for them — and so is the button. The response handler would 404, yet a
// drawn `approve` is a screen defect (the reviewer's INJ2: dropping
// `&& !hidden` stayed green).
func TestR2InboxHiddenItemOffersNoApprove(t *testing.T) {
	f := newP2Fixture(t)
	dir := f.addMember(t, "d2@example.com", "D2")
	f.exec(t, `UPDATE room SET visibility = 'invited' WHERE id = $1`, f.sessionID)
	f.exec(t, `INSERT INTO room_participant (room_id, user_id, role) VALUES ($1, $2, 'member')`, f.sessionID, dir.userID)
	f.exec(t, `UPDATE work SET director_user_id = $2 WHERE room_id = $1`, f.sessionID, dir.userID)
	var hitlID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO hitl_request (session_id, source, type, question, approver_spec, due_at, created_at)
		VALUES ($1, 'system', 'approval', 'SECRET-QUESTION', 'director', $2, $3) RETURNING id`,
		f.sessionID, f.fake.Now().Add(24*time.Hour), f.fake.Now()).Scan(&hitlID); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id)
		SELECT id, 'hitl_request', 'action_required', $2, $3 FROM member WHERE user_id = $1`, dir.userID, f.sessionID, hitlID)
	if got := inboxOf(t, dir.client, f.wsID, "hitl_request"); len(got) != 1 || !slices.Contains(actionsOf(got[0]), "approve") {
		t.Fatalf("premise: the Director's card = %v, want approve offered", got)
	}
	f.exec(t, `UPDATE room_participant SET left_at = now() WHERE room_id = $1 AND user_id = $2`, f.sessionID, dir.userID)
	got := inboxOf(t, dir.client, f.wsID, "hitl_request")
	if len(got) != 1 || got[0]["room"] != nil {
		t.Fatalf("after removal = %v, want the card with the room hidden", got)
	}
	if a := actionsOf(got[0]); slices.Contains(a, "approve") || slices.Contains(a, "reject") {
		t.Fatalf("hidden card actions = %v, want no approve/reject — the room is gone for them", a)
	}
}

// mkSession is a second old-path session in the fixture's workspace.
func (f *p2Fixture) mkSession(t *testing.T) string {
	t.Helper()
	return str(f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/sessions", map[string]any{
		"title": "S2", "goal": "g", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.lead,
		"participants":      []map[string]any{{"agent_id": f.lead}},
	}), "id")
}
