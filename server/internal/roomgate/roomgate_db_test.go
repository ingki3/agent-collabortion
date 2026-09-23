package roomgate

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

func block(t *testing.T, pool *pgxpool.Pool, room uuid.UUID, reason string, now time.Time) (int, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := Lock(ctx, tx, room); err != nil {
		t.Fatal(err)
	}
	d := tasks.PausedDetail(reason, now)
	n, err := Block(ctx, tx, room, reason, gen.BlockedDetail{}, &d, now)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit(ctx)
}

func unblock(t *testing.T, pool *pgxpool.Pool, room uuid.UUID, reason string, now time.Time) ([]uuid.UUID, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ids, err := Unblock(ctx, tx, room, reason, now)
	if err != nil {
		return nil, err
	}
	return ids, tx.Commit(ctx)
}

func workState(t *testing.T, pool *pgxpool.Pool, room uuid.UUID) (string, string, bool) {
	t.Helper()
	var status, reason string
	var detail []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT status::text, COALESCE(paused_reason::text, ''), paused_detail FROM work WHERE room_id = $1`, room).
		Scan(&status, &reason, &detail); err != nil {
		t.Fatal(err)
	}
	return status, reason, IsMirror(detail)
}

// TestBlockMirrorsOnlyActiveMissionsAndUnblockLiftsOnlyItsOwn is the boundary
// the Lead fixed for T-R1b1 Q2: a room block parks the room's ACTIVE missions
// with the room's mark, and lifting it brings back only the marked ones — a
// mission paused on its own (Director · its own budget) stays paused.
func TestBlockMirrorsOnlyActiveMissionsAndUnblockLiftsOnlyItsOwn(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)

	// 1. An active mission is parked as the room's, and comes back.
	n, err := block(t, pool, seed.SessionID, ReasonLoop, now)
	if err != nil || n != 1 {
		t.Fatalf("block(loop) = %d, %v, want 1 mission stopped", n, err)
	}
	if st, reason, mirror := workState(t, pool, seed.SessionID); st != "paused" || reason != "loop" || !mirror {
		t.Fatalf("mission = %s(%s) mirror=%v, want paused(loop) marked as the room's", st, reason, mirror)
	}
	if _, err := block(t, pool, seed.SessionID, ReasonBudget, now); err != ErrAlreadyBlocked {
		t.Fatalf("second block = %v, want ErrAlreadyBlocked — one reason at a time", err)
	}
	if _, err := unblock(t, pool, seed.SessionID, ReasonBudget, now); err != ErrNotBlocked {
		t.Fatalf("unblock(budget) of a loop gate = %v, want ErrNotBlocked", err)
	}
	ids, err := unblock(t, pool, seed.SessionID, ReasonLoop, now)
	if err != nil || len(ids) != 1 {
		t.Fatalf("unblock(loop) = %v, %v, want the one parked mission back", ids, err)
	}
	if st, _, _ := workState(t, pool, seed.SessionID); st != "active" {
		t.Fatalf("mission = %s after unblock, want active", st)
	}

	// 2. A mission its Director paused is not the room's to stop or to free.
	if _, err := pool.Exec(ctx, `UPDATE work SET status = 'paused', paused_reason = 'director', paused_detail = NULL WHERE room_id = $1`, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	if n, err := block(t, pool, seed.SessionID, ReasonBudget, now); err != nil || n != 0 {
		t.Fatalf("block(budget) over a Director-paused mission = %d, %v, want 0 parked", n, err)
	}
	if _, err := unblock(t, pool, seed.SessionID, ReasonBudget, now); err != nil {
		t.Fatal(err)
	}
	if st, reason, _ := workState(t, pool, seed.SessionID); st != "paused" || reason != "director" {
		t.Fatalf("mission = %s(%s) after the room came back, want paused(director) — the room did not stop it", st, reason)
	}

	// 3. Same reason, not the room's: a mission paused for its OWN budget
	// (no mark) survives a room budget unblock.
	if _, err := pool.Exec(ctx, `UPDATE work SET paused_reason = 'budget', paused_detail = '{"reason":"budget"}' WHERE room_id = $1`, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := block(t, pool, seed.SessionID, ReasonBudget, now); err != nil {
		t.Fatal(err)
	}
	if _, err := unblock(t, pool, seed.SessionID, ReasonBudget, now); err != nil {
		t.Fatal(err)
	}
	if st, reason, _ := workState(t, pool, seed.SessionID); st != "paused" || reason != "budget" {
		t.Fatalf("mission = %s(%s), want its own paused(budget) kept", st, reason)
	}

	// 4. `manual` parks nothing — the gate alone holds the room.
	if _, err := pool.Exec(ctx, `UPDATE work SET status = 'active', paused_reason = NULL, paused_detail = NULL WHERE room_id = $1`, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	if n, err := block(t, pool, seed.SessionID, ReasonManual, now); err != nil || n != 1 {
		t.Fatalf("block(manual) = %d, %v, want works_stopped 1 (the active mission it holds)", n, err)
	}
	if st, _, _ := workState(t, pool, seed.SessionID); st != "active" {
		t.Fatalf("mission = %s under a manual stop, want active — manual is the room's gate only", st)
	}
}

// TestApproversChain is FR-2A.3's hand-over: owner, then the room's deputy,
// or — with no deputy — the workspace's oldest OTHER owner.
func TestApproversChain(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	a, err := LoadApprovers(ctx, pool, seed.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Owner != seed.UserID || a.Delegate() != nil {
		t.Fatalf("sole owner = %+v, want the owner and nobody to hand to", a)
	}
	mk := func(email string, role string, at time.Time) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name, created_at) VALUES ($1, $1, $2) RETURNING id`, email, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, $3, $4)`, seed.WorkspaceID, id, role, at); err != nil {
			t.Fatal(err)
		}
		return id
	}
	younger := mk("o3@example.com", "owner", now.Add(2*time.Hour))
	older := mk("o2@example.com", "owner", now.Add(time.Hour))
	_ = mk("admin@example.com", "admin", now.Add(-time.Hour)) // an admin is not an owner
	if a, err = LoadApprovers(ctx, pool, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	if d := a.Delegate(); d == nil || *d != older {
		t.Fatalf("delegate = %v, want the oldest other owner %s (not %s, not the admin)", d, older, younger)
	}
	created, due := now, now.Add(24*time.Hour)
	if who, next := a.Now(created, due, now.Add(time.Hour)); who != seed.UserID || next == nil || !next.Equal(now.Add(12*time.Hour)) {
		t.Fatalf("before half = %s next=%v, want the owner until +12h", who, next)
	}
	if who, next := a.Now(created, due, now.Add(13*time.Hour)); who != older || next != nil {
		t.Fatalf("after half = %s next=%v, want the delegate", who, next)
	}
	deputy := mk("dep@example.com", "member", now)
	if _, err := pool.Exec(ctx, `UPDATE room SET deputy_owner_user_id = $2 WHERE id = $1`, seed.SessionID, deputy); err != nil {
		t.Fatal(err)
	}
	if a, err = LoadApprovers(ctx, pool, seed.SessionID); err != nil {
		t.Fatal(err)
	}
	if d := a.Delegate(); d == nil || *d != deputy {
		t.Fatalf("delegate = %v, want the room's deputy first", d)
	}
}
