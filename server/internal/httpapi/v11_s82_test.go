package httpapi

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestS82GCReceiptTargetPriority pins the ORDER gcReceiptTargets reads a §6
// receipt in (PR #220 리뷰 NN2): the top-level `id` first, then the id the
// daemon echoes inside `gc`, and only with neither the path lookup through
// the runtime's pending gc commands. Each path worked on its own before; the
// precedence had no assertion, so swapping it broke nothing visible — until
// a worktree path shared by two rows makes the fallback answer differently.
func TestS82GCReceiptTargetPriority(t *testing.T) {
	f := newG4Fixture(t)
	ctx := t.Context()
	runtimeID := mustUUID(t, f.runtimeID)
	top, inner, byPath := uuid.New(), uuid.New(), uuid.New()
	const path = "/tmp/colab/ws/lane-x"
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO daemon_command (runtime_id, type, payload, created_at)
		VALUES ($1, 'gc', jsonb_build_object('workdirs', jsonb_build_array(jsonb_build_object('id', $2::text, 'path', $3::text))), $4)`,
		runtimeID, byPath.String(), path, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name        string
		top, gc, pt string
		want        []uuid.UUID
	}{
		{"top-level id wins over gc.id and the path", top.String(), inner.String(), path, []uuid.UUID{top}},
		{"gc.id wins over the path", "", inner.String(), path, []uuid.UUID{inner}},
		{"a top-level id that is not a uuid falls through to gc.id", "session-row", inner.String(), path, []uuid.UUID{inner}},
		{"neither id → the pending gc command's row for the path", "", "", path, []uuid.UUID{byPath}},
		{"an unknown path names nothing", "", "", "/tmp/colab/elsewhere", nil},
		{"nothing at all → nothing", "", "", "", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := f.srv.gcReceiptTargets(ctx, runtimeID, c.top, c.gc, c.pt)
			if len(got) != len(c.want) {
				t.Fatalf("targets = %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("targets = %v, want %v", got, c.want)
				}
			}
		})
	}
}

// TestS82DeleteRacesPostMessage is the direction PR #220's review left
// unmeasured (NN3): a delete already under way, and a message arriving for
// the same session. Post locks the session row too, so it queues behind the
// delete and, once the delete commits, finds no row — 404, not a message
// written into a session that is gone (FK error → 500) and not a 201 for a
// message nobody will ever read.
//
// The window is opened by a third transaction holding the row lock while
// both requests queue behind it; the delete is queued first, so it is the
// one that runs first when the lock is released.
func TestS82DeleteRacesPostMessage(t *testing.T) {
	f := newP2Fixture(t)
	ctx := context.Background()
	sessionID := mustUUID(t, f.sessionID)
	if _, err := f.pool.Exec(ctx, `UPDATE work SET status = 'completed', updated_at = $2 WHERE room_id = $1`, sessionID, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	hold, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `SELECT id FROM room WHERE id = $1 FOR UPDATE`, sessionID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var delStatus, postStatus int
	var postOut map[string]any
	wg.Add(1)
	go func() {
		defer wg.Done()
		delStatus, _, _ = f.api.do("DELETE", f.p+"/rooms/"+f.sessionID, nil)
	}()
	time.Sleep(150 * time.Millisecond) // the delete is waiting on the row now
	wg.Add(1)
	go func() {
		defer wg.Done()
		postStatus, postOut, _ = f.api.do("POST", f.p+"/rooms/"+f.sessionID+"/messages",
			map[string]any{"content": "늦게 온 말"}, "Idempotency-Key", uuid.NewString())
	}()
	time.Sleep(150 * time.Millisecond) // and so is the post, behind it
	if err := hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	if delStatus != 204 {
		t.Fatalf("delete = %d, want 204", delStatus)
	}
	if postStatus != 404 || str(postOut, "code") != "not_found" {
		t.Fatalf("post after the delete = %d %v, want 404 not_found — the message must not land in a session that is gone (S-82)", postStatus, postOut)
	}
	var msgs int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1`, sessionID).Scan(&msgs); err != nil {
		t.Fatal(err)
	}
	if msgs != 0 {
		t.Fatalf("messages left for the deleted session = %d, want 0", msgs)
	}
}

// TestS82SessionGoneMemoAsksOnce is NN4: one report, many directories of one
// deleted session — the existence question is asked once per session.
func TestS82SessionGoneMemoAsksOnce(t *testing.T) {
	f := newP2Fixture(t)
	m := sessionGoneMemo{}
	gone := uuid.NewString()
	r := httptest.NewRequest("GET", "/healthz", nil)
	if !m.is(r, f.srv, gone) || !m.is(r, f.srv, gone) {
		t.Fatalf("an unknown session id must read as gone")
	}
	if m.is(r, f.srv, f.sessionID) {
		t.Fatalf("a live session must not read as gone")
	}
	if m.is(r, f.srv, "not-a-uuid") {
		t.Fatalf("a malformed id is not the deleteSession case")
	}
	if len(m) != 3 {
		t.Fatalf("memo holds %d entries, want 3 — one per distinct session id in the report", len(m))
	}
	// Once memoised, the answer no longer comes from the database: deleting
	// the live session leaves the memo's "not gone" in place for this report.
	if _, err := f.pool.Exec(t.Context(), `DELETE FROM room WHERE id = $1`, mustUUID(t, f.sessionID)); err != nil {
		t.Fatal(err)
	}
	if m.is(r, f.srv, f.sessionID) {
		t.Fatalf("the memo must answer from its cache within one report (S-82 NN4)")
	}
}
