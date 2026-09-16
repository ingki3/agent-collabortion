package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestUpdateMemberRoleConcurrentDemotion is the race PR #209 review NN2 could
// not reproduce (S-75): two owners demote each other at the same moment. With
// the workspace row lock the second transaction reads the owner count only
// AFTER the first committed, sees 1 and answers `409 last_owner`; without it
// both read 2 and the workspace ends with no owner at all.
//
// The window is widened through afterLockMember: the first transaction holds
// the lock for 300ms after its read, long enough for the second to be queued
// behind it. Removing `FOR UPDATE` from lockMember makes both reads see two
// owners and this test fail with "owners = 0".
func TestUpdateMemberRoleConcurrentDemotion(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	var otherUser uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name, created_at) VALUES ('own2@example.com', 'Own2', $1) RETURNING id`, now).Scan(&otherUser); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`, seed.WorkspaceID, otherUser, now); err != nil {
		t.Fatal(err)
	}
	memberOf := func(user uuid.UUID) uuid.UUID {
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `SELECT id FROM member WHERE workspace_id = $1 AND user_id = $2`, seed.WorkspaceID, user).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, b := memberOf(seed.UserID), memberOf(otherUser)

	s := New(pool, clock.NewFake(now), "http://web")
	var once sync.Once
	afterLockMember = func() {
		// Only the first transaction through the lock dawdles; the second is
		// already waiting on the workspace row by then.
		once.Do(func() { time.Sleep(300 * time.Millisecond) })
	}
	t.Cleanup(func() { afterLockMember = nil })

	type result struct {
		who string
		err error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, c := range []struct {
		who            string
		caller, target uuid.UUID
	}{{"A demotes B", seed.UserID, b}, {"B demotes A", otherUser, a}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.UpdateMemberRole(ctx, seed.WorkspaceID, c.target, c.caller, "owner", "admin")
			results <- result{c.who, err}
		}()
		time.Sleep(50 * time.Millisecond) // the second call queues behind the first's lock
	}
	wg.Wait()
	close(results)

	var ok, lastOwner int
	for r := range results {
		var p *apperr.Problem
		switch {
		case r.err == nil:
			ok++
		case errors.As(r.err, &p) && p.Code == CodeLastOwner:
			lastOwner++
		default:
			t.Fatalf("%s: unexpected %v", r.who, r.err)
		}
	}
	var owners int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM member WHERE workspace_id = $1 AND role = 'owner'`, seed.WorkspaceID).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if ok != 1 || lastOwner != 1 || owners != 1 {
		t.Fatalf("concurrent demotions: ok=%d last_owner=%d owners=%d, want 1/1/1 — the workspace must never be left "+
			"without an owner (S-75, openapi updateMemberRole 409)", ok, lastOwner, owners)
	}
}
