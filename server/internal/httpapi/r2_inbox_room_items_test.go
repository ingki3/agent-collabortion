package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

// T-S-inbox (V19_R1B_HANDOFF #309 NN1 · #311 서버 발견 ①②④ · NN3): what a
// room-level card says in 받은 요청, and to whom.

// inboxOf is the caller's items of one type, from the API (not the table): the
// card is what these rows are about.
func inboxOf(t *testing.T, c *client, wsID, itemType string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, raw := range items(c.must(200, "GET", "/api/v1/inbox?workspace_id="+wsID, nil)) {
		it := raw.(map[string]any)
		if str(it, "type") == itemType {
			out = append(out, it)
		}
	}
	return out
}

func card(it map[string]any) map[string]any {
	c, _ := it["card"].(map[string]any)
	return c
}

func actionsOf(it map[string]any) []string {
	var out []string
	for _, a := range it["actions"].([]any) {
		out = append(out, a.(string))
	}
	return out
}

// TestR2InboxRoomBudgetIsRoomPaused is #311 ①②: a room budget overrun stops
// the ROOM, and the owner's inbox gets the one `room_paused` card — the
// approval is its action (approve_continue), not a second `hitl_request` card.
// The absence hand-over's next link (here the deputy) is told at once, its
// card saying why it is there (room_deputy) and that it is a delegated copy.
func TestR2InboxRoomBudgetIsRoomPaused(t *testing.T) {
	f := newP2Fixture(t)
	deputy := f.addMember(t, "dep@example.com", "Dep")
	f.exec(t, `UPDATE room SET deputy_owner_user_id = $2, limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID, deputy.userID)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.overrunSession(t, f.rUUID, "R", 1.25)
	hitlID := f.openSessionBudgetHitl(t)

	if got := inboxOf(t, f.api, f.wsID, "hitl_request"); len(got) != 0 {
		t.Fatalf("owner's hitl_request cards = %v, want none — a room stop is ONE room_paused card", got)
	}
	own := inboxOf(t, f.api, f.wsID, "room_paused")
	if len(own) != 1 {
		t.Fatalf("owner's room_paused cards = %d, want 1", len(own))
	}
	it := own[0]
	if str(it, "ref_id") != hitlID || str(it, "severity") != "action_required" || str(it, "recipient_basis") != "room_owner" {
		t.Fatalf("owner's card = %v, want ref the budget request · action_required · basis room_owner", it)
	}
	if a := actionsOf(it); len(a) != 2 || a[0] != "approve_continue" || a[1] != "open_room" {
		t.Fatalf("owner's actions = %v, want [approve_continue open_room]", a)
	}
	if it["delegated"] != false || str(card(it), "purpose") != "budget" {
		t.Fatalf("owner's card delegated=%v purpose=%v, want false · budget", it["delegated"], card(it)["purpose"])
	}
	if r, _ := it["room"].(map[string]any); r == nil || str(r, "id") != f.sessionID {
		t.Fatalf("owner's card room = %v, want the room", it["room"])
	}

	dep := inboxOf(t, deputy.client, f.wsID, "room_paused")
	if len(dep) != 1 || str(dep[0], "recipient_basis") != "room_deputy" || dep[0]["delegated"] != true {
		t.Fatalf("deputy's room_paused = %v, want one card · basis room_deputy · delegated", dep)
	}
	// Before half the deadline the deputy may not answer yet — the card does
	// not offer what would 403 (FR-5.3).
	if a := actionsOf(dep[0]); len(a) != 1 || a[0] != "open_room" {
		t.Fatalf("deputy's actions before half = %v, want [open_room]", a)
	}

	// The action is the approval itself: approve_continue answers that ref.
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 5}, "Idempotency-Key", uuid.NewString())
	if reason, _ := f.roomGate(t); reason != "" {
		t.Fatalf("room gate = %q after approve_continue, want lifted", reason)
	}
}

// TestR2InboxRoomPausedBasisWorkspaceOwner is #311 ②'s other link: no deputy,
// so the hand-over reaches the oldest other workspace owner (roomgate.Approvers)
// — basis workspace_owner. A plain member gets nothing.
func TestR2InboxRoomPausedBasisWorkspaceOwner(t *testing.T) {
	f := newP2Fixture(t)
	owner2 := f.addMember(t, "o2@example.com", "O2")
	f.exec(t, `UPDATE member SET role = 'owner' WHERE user_id = $1`, owner2.userID)
	member := f.addMember(t, "m@example.com", "M")
	f.exec(t, `UPDATE room SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.overrunSession(t, f.rUUID, "R", 1.25)

	if got := inboxOf(t, f.api, f.wsID, "room_paused"); len(got) != 1 || str(got[0], "recipient_basis") != "room_owner" {
		t.Fatalf("owner = %v, want one room_owner card", got)
	}
	if got := inboxOf(t, owner2.client, f.wsID, "room_paused"); len(got) != 1 || str(got[0], "recipient_basis") != "workspace_owner" {
		t.Fatalf("other ws owner = %v, want one workspace_owner card", got)
	}
	if got := inboxOf(t, member.client, f.wsID, "room_paused"); len(got) != 0 {
		t.Fatalf("plain member = %v, want nothing", got)
	}
}

// TestR2InboxMissionBudgetStaysWorkPaused: a MISSION budget overrun is not a
// room stop — work_paused for the Director as before, and no room_paused.
func TestR2InboxMissionBudgetStaysWorkPaused(t *testing.T) {
	f := newP2Fixture(t)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.exec(t, `UPDATE work SET limits = '{"budget_usd": 1}'::jsonb WHERE room_id = $1`, f.sessionID)
	f.overrunSession(t, f.rUUID, "R", 1.25)
	if got := inboxOf(t, f.api, f.wsID, "room_paused"); len(got) != 0 {
		t.Fatalf("room_paused = %v on a mission stop, want none", got)
	}
	if got := inboxOf(t, f.api, f.wsID, "work_paused"); len(got) != 1 {
		t.Fatalf("work_paused = %d, want 1", len(got))
	}
}

// TestR2InboxIsolationConfirmCard is #311 ②④: the isolation question's card
// names who set the first turn going and quotes, on one line, the message that
// did it (openapi 0.2.10 card.actor_name · quote), with basis room_owner.
func TestR2InboxIsolationConfirmCard(t *testing.T) {
	f := newP2Fixture(t)
	// The fixture's start task is the session goal's; make the first turn a
	// person's message with a multi-line body, the case the quote is for.
	f.exec(t, `DELETE FROM task WHERE session_id = $1`, f.sessionID)
	f.post(t, map[string]any{"content": "@R 이 저장소의 README 를 정리해 주세요\n둘째 줄은 인용하지 않는다", "mentions": []map[string]any{{"kind": "agent", "id": f.r}}})
	f.exec(t, `UPDATE runtime SET repos = '[{"path": "/Users/x/repo", "remote_url": "", "branch": "main", "clean": true}]'::jsonb WHERE workspace_id = $1`, f.wsID)
	if got := f.claimed(t); len(got) != 0 {
		t.Fatalf("first claim = %v, want the isolation question first", got)
	}
	got := inboxOf(t, f.api, f.wsID, "isolation_confirm")
	if len(got) != 1 {
		t.Fatalf("isolation_confirm cards = %d, want 1", len(got))
	}
	c := card(got[0])
	if str(got[0], "recipient_basis") != "room_owner" {
		t.Fatalf("basis = %v, want room_owner", got[0]["recipient_basis"])
	}
	if str(c, "actor_name") != "Dir" || str(c, "quote") != "@R 이 저장소의 README 를 정리해 주세요" {
		t.Fatalf("card actor_name=%v quote=%v, want Dir and the first line of the trigger", c["actor_name"], c["quote"])
	}
	if a := actionsOf(got[0]); len(a) != 3 || a[0] != "approve" {
		t.Fatalf("actions = %v, want approve · reject · open_room", a)
	}
}

// TestR2InboxRoomInvitedActorAndLeave is #311 NN3 + #309 NN1: the invite card
// names who invited; once the person is put out of the INVITED room, the item
// stays in their inbox (it came to them) but the room is gone for them — no
// room, no body naming it — exactly like GET /rooms/{id}'s 404 (FR-5.3). In a
// workspace-visible room the same removal hides nothing: they can still see it.
func TestR2InboxRoomInvitedActorAndLeave(t *testing.T) {
	f := newRoomsFixture(t)
	for _, vis := range []string{"invited", "workspace"} {
		t.Run(vis, func(t *testing.T) {
			room := f.mkRoom(t, f.member, "비밀 방 "+vis)
			roomID := str(room, "id")
			rp := f.roomPath(roomID)
			f.member.must(200, "PATCH", rp, map[string]any{"visibility": vis})
			f.member.must(201, "POST", rp+"/participants", map[string]any{"user_id": f.otherUserID})

			find := func() map[string]any {
				t.Helper()
				for _, it := range inboxOf(t, f.other, f.wsID, "room_invited") {
					if str(it, "room_id") == roomID {
						return it
					}
				}
				t.Fatalf("no room_invited item for %s", roomID)
				return nil
			}
			it := find()
			if r, _ := it["room"].(map[string]any); r == nil || str(r, "name") != "비밀 방 "+vis {
				t.Fatalf("room before = %v", it["room"])
			}
			if str(card(it), "actor_name") != "Mem" || str(card(it), "body") != "비밀 방 "+vis {
				t.Fatalf("card before = %v, want actor Mem and the room's name", card(it))
			}

			oth := participantOf(t, f.member, rp, f.otherUserID)
			f.member.must(204, "DELETE", rp+"/participants/"+oth, nil)

			it = find()
			c := card(it)
			if vis == "invited" {
				if it["room"] != nil || c["body"] != nil || it["session"] != nil {
					t.Fatalf("after removal: room=%v body=%v session=%v, want the room hidden", it["room"], c["body"], it["session"])
				}
				if str(c, "title") != "방에 초대되었습니다" {
					t.Fatalf("title = %v, want the event without the room", c["title"])
				}
				// markInboxRead answers with the same card.
				read := f.other.must(200, "POST", f.p+"/inbox/"+str(it, "id")+"/read", nil)
				if read["room"] != nil {
					t.Fatalf("markInboxRead room = %v, want hidden too", read["room"])
				}
				return
			}
			if r, _ := it["room"].(map[string]any); r == nil || str(c, "body") != "비밀 방 "+vis {
				t.Fatalf("workspace room after removal: room=%v body=%v, want still named", it["room"], c["body"])
			}
		})
	}
}
