package router

import (
	"context"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestLoadHopsIsCapped is #292 review NN2: the "last human hop onward" window
// has a safety net — the NEWEST HopReadCap rows, still in id order — so a
// workspace that raised every loop limit out of reach cannot make one post
// read a room's whole history into memory.
func TestLoadHopsIsCapped(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	s := New(pool, clock.NewFake(now), nil, nil)

	var ids []int64
	for i := 0; i < 30; i++ {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, allowed, created_at)
			VALUES ($1, $2, $2, 2, true, $3) RETURNING id`, seed.SessionID, seed.AgentID, now.Add(-time.Duration(30-i)*time.Second)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	old := HopReadCap
	HopReadCap = 10
	t.Cleanup(func() { HopReadCap = old })

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	hops, err := s.loadHops(ctx, tx, seed.SessionID, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 10 {
		t.Fatalf("loadHops read %d rows, want the cap 10", len(hops))
	}
	for i, h := range hops {
		if want := ids[20+i]; h.ID != want {
			t.Fatalf("hop %d = id %d, want %d (the newest ten, oldest first)", i, h.ID, want)
		}
	}
}
