package router

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestJudgeHopDoesNotPause is S-79 (PR #213 리뷰 NN2): the verdict and its
// consequence are two functions. judgeHop records the hop (allowed=false
// when it tripped) and answers — the session is still `active` afterwards.
// Stopping it is pauseForLoop, which each caller invokes in the open
// (delegate.go · status.go wake · Post), so a new trigger site cannot inherit
// a pause it never asked for.
func TestJudgeHopDoesNotPause(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	seed := testdb.Plant(t, pool, now)
	s := New(pool, clock.NewFake(now), nil, nil)
	// Second agent for the pair: ChainDepth is 1 per hop, so a hops_per_hour
	// limit of 1 trips on the second hop with no chain to build.
	if _, err := pool.Exec(ctx, `UPDATE workspace_settings SET loop_limits = '{"max_hops_per_hour": 1}' WHERE workspace_id = $1`, seed.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	other := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO agent (id, workspace_id, name, role, role_description, instructions, owner_id, created_at, updated_at)
		VALUES ($1, $2, 'W', 'engineer', 'w', 'w', $3, $4, $4)`, other, seed.WorkspaceID, seed.UserID, now); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	hop := Hop{FromAgent: seed.AgentID, ToAgent: other, At: now}
	first, err := s.judgeHop(ctx, tx, seed.SessionID, seed.WorkspaceID, hop, uuid.Nil, RulePlatform, now)
	if err != nil || !first.Allowed {
		t.Fatalf("first hop = %+v err %v, want allowed", first, err)
	}
	second, err := s.judgeHop(ctx, tx, seed.SessionID, seed.WorkspaceID, Hop{FromAgent: other, ToAgent: seed.AgentID, At: now.Add(time.Second)}, uuid.Nil, RulePlatform, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Allowed || second.Detail != DetailHopsPerHour {
		t.Fatalf("second hop = %+v, want tripped on hops_per_hour", second)
	}

	var status string
	var recorded, refused int
	if err := tx.QueryRow(ctx, `SELECT status::text FROM work WHERE room_id = $1`, seed.SessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE NOT allowed) FROM session_hop WHERE session_id = $1`, seed.SessionID).Scan(&recorded, &refused); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("session = %q after judgeHop, want active — the verdict must not pause; that is pauseForLoop's job (S-79)", status)
	}
	if recorded != 2 || refused != 1 {
		t.Fatalf("session_hop rows = %d (refused %d), want 2/1 — the tripped hop is recorded so the next decision reads a complete history", recorded, refused)
	}

	// And the consequence, applied in the open: one pause per session.
	if err := s.pauseForLoop(ctx, tx, seed.SessionID, seed.WorkspaceID, &seed.UserID, second, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT status::text FROM work WHERE room_id = $1`, seed.SessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "paused" {
		t.Fatalf("session = %q after pauseForLoop, want paused", status)
	}
}
