package runtimes

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestSweepOfflineIsIdempotent is E14-10 on the DB path — #162 review NN2.
//
// The golden row for E14-10 calls PlanOffline with `SessionState: "paused"`,
// which is the pure half: a session already paused is not paused again. But the
// sweep runs every minute, and what actually keeps it from filing a second
// Director notification every minute is two SQL guards — the candidate query's
// `sess.status = 'active'` and pauseForOffline's `WHERE id = $1 AND status =
// 'active'` UPDATE. The reviewer relaxed both to `IN ('active','paused')` and
// nothing went red.
//
// One inbox card per outage is the requirement: FR-9.2's notification is what
// the Director acts on, and an inbox that refiles it 1,440 times a day is an
// inbox nobody reads.
func TestSweepOfflineIsIdempotentAcrossPasses(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(t0)
	s := New(pool, fake, nil, "http://colab.test")

	sessionID, runtimeID := seedOfflineSession(ctx, t, pool, t0)
	// The grace window is 7 days (FR-9.2); the machine has been gone for eight.
	if _, err := pool.Exec(ctx, `UPDATE runtime SET status = 'offline', offline_since = $2 WHERE id = $1`,
		runtimeID, t0.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}

	n, err := s.SweepOffline(ctx)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if n != 1 {
		t.Fatalf("first sweep paused %d sessions, want 1 (FR-9.2 7일 유예)", n)
	}
	assertOfflineState(ctx, t, pool, sessionID, runtimeID, 1, "after the first sweep")

	// A minute later the scheduler runs again. The runtime is still gone and
	// the session is still parked, and that must produce nothing at all.
	fake.Advance(time.Minute)
	n, err = s.SweepOffline(ctx)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if n != 0 {
		t.Errorf("second sweep paused %d sessions, want 0 — the candidate query must exclude "+
			"sessions it already parked (E14-10)", n)
	}
	assertOfflineState(ctx, t, pool, sessionID, runtimeID, 1, "after the second sweep")

	// And a third, because a scheduler tick is not a one-off.
	fake.Advance(time.Minute)
	if _, err := s.SweepOffline(ctx); err != nil {
		t.Fatalf("third sweep: %v", err)
	}
	assertOfflineState(ctx, t, pool, sessionID, runtimeID, 1, "after the third sweep")
}

// TestSweepOfflineLeavesSessionsInsideTheGraceWindow: the sweep is not "pause
// everything on an offline machine" — FR-9.2 is a SEVEN DAY grace, and a laptop
// that closed for the night must find its session where it left it.
func TestSweepOfflineLeavesSessionsInsideTheGraceWindow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s := New(pool, clock.NewFake(t0), nil, "http://colab.test")

	sessionID, runtimeID := seedOfflineSession(ctx, t, pool, t0)
	if _, err := pool.Exec(ctx, `UPDATE runtime SET status = 'offline', offline_since = $2 WHERE id = $1`,
		runtimeID, t0.Add(-2*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SweepOffline(ctx); err != nil || n != 0 {
		t.Fatalf("sweep inside the grace window paused %d sessions (err %v), want 0 (E14-09)", n, err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status::text FROM session WHERE id = $1`, sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Errorf("session = %q two days into a seven-day grace, want active", status)
	}
}

func assertOfflineState(ctx context.Context, t *testing.T, pool *pgxpool.Pool,
	sessionID, runtimeID uuid.UUID, wantCards int, when string) {
	t.Helper()
	var status, reason string
	if err := pool.QueryRow(ctx, `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`,
		sessionID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || reason != "runtime_offline" {
		t.Errorf("%s: session = %s(%s), want paused(runtime_offline)", when, status, reason)
	}
	var cards int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM inbox_item WHERE session_id = $1 AND ref_id = $2
		   AND type = 'runtime_offline'`, sessionID, runtimeID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if cards != wantCards {
		t.Errorf("%s: runtime_offline inbox cards = %d, want %d — FR-9.2's notification is what "+
			"the Director acts on, and one per minute is one nobody reads", when, cards, wantCards)
	}
}

// seedOfflineSession is one active session pinned to one runtime, with a
// Director who has an inbox to file into.
func seedOfflineSession(ctx context.Context, t *testing.T, pool *pgxpool.Pool, now time.Time) (sessionID, runtimeID uuid.UUID) {
	t.Helper()
	var wsID, userID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('w', 'w-`+uuid.NewString()[:8]+`') RETURNING id`).Scan(&wsID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name) VALUES ($1, 'Dir') RETURNING id`,
		uuid.NewString()+"@x.test").Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`,
		wsID, userID, now); err != nil {
		t.Fatalf("member: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_settings (workspace_id) VALUES ($1)`, wsID); err != nil {
		t.Fatalf("workspace settings: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO runtime (workspace_id, name, status, last_seen_at, created_at, updated_at)
		VALUES ($1, 'mac-1', 'online', $2, $2, $2) RETURNING id`, wsID, now).Scan(&runtimeID); err != nil {
		t.Fatalf("runtime: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO session (workspace_id, title, goal, director_user_id, runtime_id, isolation, status,
		                     created_by, created_at, updated_at, started_at)
		VALUES ($1, 's', 'g', $2, $3, '{"kind":"worktree"}', 'active', $2, $4, $4, $4) RETURNING id`,
		wsID, userID, runtimeID, now).Scan(&sessionID); err != nil {
		t.Fatalf("session: %v", err)
	}
	return sessionID, runtimeID
}
