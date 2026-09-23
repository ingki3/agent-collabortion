package router

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestLoopDepthSurvivesALongRoom is NN3 (V19_impl §4 위험 3, PRD v0.19 §3.1):
// a room lives for weeks, and the depth limit must not switch itself off when
// the last person spoke more than a window ago.
//
// The history is one person's message followed by 250 agent hops in a single
// causal chain, cycling through three agents (so no pair run forms) and all
// older than the rolling hour (so hops_per_hour sees nothing). The old loader
// read the last 200 rows: the person fell outside, chainDepth saw an
// agent-only history and answered 0, and the 251st hop was ALLOWED with
// max_chain_depth = 8. Reading from the last human hop keeps the chain rooted
// and the hop trips chain_depth.
func TestLoopDepthSurvivesALongRoom(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	s := New(pool, clock.NewFake(now), nil, nil)
	agents := []uuid.UUID{seed.AgentID, uuid.New(), uuid.New()}
	for i, a := range agents[1:] {
		if _, err := pool.Exec(ctx, `INSERT INTO agent (id, workspace_id, name, role, role_description, instructions, owner_id, created_at, updated_at)
			VALUES ($1, $2, $3, 'engineer', 'w', 'w', $4, $5, $5)`, a, seed.WorkspaceID, []string{"B", "C"}[i], seed.UserID, now); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-3 * time.Hour)
	var cause int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, allowed, created_at)
		VALUES ($1, NULL, $2, 2, true, $3) RETURNING id`, seed.SessionID, agents[0], old).Scan(&cause); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250; i++ {
		from, to := agents[i%3], agents[(i+1)%3]
		if err := pool.QueryRow(ctx, `
			INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, allowed, created_at, cause_hop_id)
			VALUES ($1, $2, $3, 2, true, $4, $5) RETURNING id`,
			seed.SessionID, from, to, old.Add(time.Duration(i+1)*time.Second), cause).Scan(&cause); err != nil {
			t.Fatal(err)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	next := Hop{FromAgent: agents[250%3], ToAgent: agents[251%3], At: now, CauseID: cause}
	v, err := s.judgeHop(ctx, tx, seed.SessionID, seed.WorkspaceID, next, uuid.Nil, 2, now)
	if err != nil {
		t.Fatal(err)
	}
	if v.Allowed || v.Detail != DetailChainDepth {
		t.Fatalf("hop 251 of a chain the person started = %+v, want tripped on chain_depth — "+
			"the last human hop is outside a 200-row window and depth must not read as 0 (NN3)", v)
	}
	if v.ChainDepth <= DefaultLimits().MaxChainDepth {
		t.Fatalf("chain depth = %d, want past the limit %d", v.ChainDepth, DefaultLimits().MaxChainDepth)
	}
}
