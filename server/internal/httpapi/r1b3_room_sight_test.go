package httpapi

// roomSight is the SSE half of FR-5.3 invited visibility (review #291 NN1):
// the per-connection answer to "may this person see this room's frames?".
// These pin the three things it has to get right — a participant passes, a
// roster frame drops the cached answer, and inside the fresh window every
// frame re-reads the database instead of caching what the pre-commit read saw.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

type sightFixture struct {
	*roomsFixture
	room  uuid.UUID
	clock time.Time
}

// newSightFixture makes the fixture session an invited room with Mem as a
// participant and Oth outside it.
func newSightFixture(t *testing.T) *sightFixture {
	f := &sightFixture{roomsFixture: newRoomsFixture(t), clock: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)}
	f.room = mustUUID(t, f.sessionID)
	f.api.must(200, "PATCH", f.roomPath(f.sessionID), map[string]any{"visibility": "invited"})
	f.api.must(201, "POST", f.roomPath(f.sessionID)+"/participants", map[string]any{"user_id": f.memberUserID})
	return f
}

func (f *sightFixture) sight(t *testing.T, userID string) *roomSight {
	v := newRoomSight(f.pool, mustUUID(t, userID), "member")
	v.now = func() time.Time { return f.clock }
	return v
}

func (f *sightFixture) ev(typ string) realtime.Event {
	room := f.room
	return realtime.Event{Type: typ, WorkspaceID: uuid.MustParse(f.wsID), SessionID: &room}
}

// setLeft closes (or reopens) Mem's row directly — the "commit" of a leave
// that the hub announced before the writer's transaction ended.
func (f *sightFixture) setLeft(t *testing.T, left bool) {
	t.Helper()
	q := `UPDATE room_participant SET left_at = NULL WHERE room_id = $1 AND user_id = $2`
	if left {
		q = `UPDATE room_participant SET left_at = now() WHERE room_id = $1 AND user_id = $2`
	}
	if _, err := f.pool.Exec(t.Context(), q, f.room, f.memberUserID); err != nil {
		t.Fatal(err)
	}
}

func TestRoomSightParticipantPasses(t *testing.T) {
	f := newSightFixture(t)
	ctx := t.Context()
	if !f.sight(t, f.memberUserID).frame(ctx, f.ev("message.created")) {
		t.Fatal("a participant of an invited room did not get its frame")
	}
	if f.sight(t, f.otherUserID).frame(ctx, f.ev("message.created")) {
		t.Fatal("an uninvited member got a frame of an invited room")
	}
}

func TestRoomSightLeftIsRefused(t *testing.T) {
	f := newSightFixture(t)
	ctx := t.Context()
	v := f.sight(t, f.memberUserID)
	if !v.frame(ctx, f.ev("message.created")) { // cached: may see
		t.Fatal("participant refused before leaving")
	}
	f.setLeft(t, true)
	if v.frame(ctx, f.ev("participant.left")) {
		t.Fatal("participant.left frame (after the commit) still delivered to the person who left")
	}
	if v.frame(ctx, f.ev("message.created")) {
		t.Fatal("a message of the room reached the person who left")
	}
}

func TestRoomSightFreshWindowRereads(t *testing.T) {
	f := newSightFixture(t)
	ctx := t.Context()
	v := f.sight(t, f.memberUserID)
	if !v.frame(ctx, f.ev("message.created")) {
		t.Fatal("participant refused before leaving")
	}
	// The hub delivers participant.left while the writer's tx is still open:
	// this read still sees the old roster, so the frame goes through …
	if !v.frame(ctx, f.ev("participant.left")) {
		t.Fatal("pre-commit participant.left read should still see the old roster")
	}
	// … and must not be cached: once the leave commits, the next frame inside
	// the window reads the database again.
	f.setLeft(t, true)
	f.clock = f.clock.Add(sightFreshWindow - time.Second)
	if v.frame(ctx, f.ev("message.created")) {
		t.Fatal("inside the fresh window a frame used the pre-commit answer — the person who left still sees the room")
	}
	// After the window the answer is cached again: a later change is not seen
	// until the next roster frame (the point of caching at all).
	f.clock = f.clock.Add(2 * time.Second)
	if v.frame(ctx, f.ev("message.created")) {
		t.Fatal("after the window: left person sees the room")
	}
	f.setLeft(t, false)
	if v.frame(ctx, f.ev("message.created")) {
		t.Fatal("after the window the answer should be cached until a roster frame")
	}
	if !v.frame(ctx, f.ev("participant.joined")) {
		t.Fatal("participant.joined did not drop the cache")
	}
}
