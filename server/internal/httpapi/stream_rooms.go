package httpapi

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/rooms"
)

// roomSight answers "may this person see frames of this room?" for one SSE
// connection (FR-5.3 visibility). The answer per room is cached for the life
// of the connection and dropped whenever a frame says the answer may have
// changed — the roster (participant.joined/left) or the room itself
// (room.updated carries visibility changes). A workspace owner·admin sees
// every room (audit), so they never query.
//
// The hub delivers a frame while the writer's transaction is still open, so
// a lookup made on the frame that announces the change reads the OLD roster
// or visibility. Caching that answer would keep a person who just left an
// invited room on its stream until the next roster frame. So an invalidating
// frame opens a short window in which every frame of that room is judged
// fresh from the database and nothing is cached; the window outlives the
// writer's commit.
type roomSight struct {
	db      *pgxpool.Pool
	user    uuid.UUID
	admin   bool
	seen    map[uuid.UUID]bool
	noCache map[uuid.UUID]time.Time
	// now is the wall clock (a test moves it). The window is measured against
	// the writer's commit, which is real time, so this is not the server's
	// injected clock.
	now func() time.Time
}

// sightFreshWindow is how long after a roster/visibility frame a room is
// re-read per frame.
const sightFreshWindow = 5 * time.Second

func newRoomSight(db *pgxpool.Pool, user uuid.UUID, wsRole string) *roomSight {
	return &roomSight{db: db, user: user, admin: wsRole == "owner" || wsRole == "admin",
		seen: map[uuid.UUID]bool{}, noCache: map[uuid.UUID]time.Time{}, now: time.Now}
}

// frame reports whether e may be written to this connection.
func (v *roomSight) frame(ctx context.Context, e realtime.Event) bool {
	if v.admin || e.SessionID == nil {
		return true
	}
	room := *e.SessionID
	switch e.Type {
	case "participant.joined", "participant.left", "room.updated":
		delete(v.seen, room)
		v.noCache[room] = v.now().Add(sightFreshWindow)
	case "room.deleted", "session.deleted":
		// The room is gone; a card for it may be on anyone's screen who could
		// see it a moment ago, and the frame reveals nothing but the id.
		return true
	}
	if ok, cached := v.seen[room]; cached {
		return ok
	}
	a, err := rooms.LoadAccess(ctx, v.db, room, v.user)
	// A room that no longer exists or cannot be read is not shown — the
	// safe side of a failed read is silence, not a leak.
	ok := err == nil && rooms.Decide(rooms.ActView, a.Standing)
	if until, fresh := v.noCache[room]; fresh && v.now().Before(until) {
		return ok
	}
	delete(v.noCache, room)
	v.seen[room] = ok
	return ok
}
