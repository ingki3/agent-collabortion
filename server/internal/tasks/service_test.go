package tasks

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

var t0 = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

func newService(t *testing.T) (*Service, *clock.Fake, testdb.Seed) {
	pool := testdb.New(t)
	c := clock.NewFake(t0)
	s := New(pool, c, tokens.New(c), realtime.New(pool, c))
	return s, c, testdb.Plant(t, pool, t0)
}

// dispatch moves a seeded queued task to dispatched on the seed runtime and
// returns its token (what Claim does).
func dispatch(t *testing.T, s *Service, seed testdb.Seed, taskID uuid.UUID) string {
	t.Helper()
	ctx := context.Background()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	row, err := lockTask(ctx, tx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := s.MarkDispatched(ctx, tx, row, seed.RuntimeID, s.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return tok
}

func row(t *testing.T, s *Service, id uuid.UUID) *Row {
	t.Helper()
	r, err := Get(context.Background(), s.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// E5-02: dispatched with no preparing report for 5 minutes → timeout.
// Default max_attempts requeues (daemon-protocol §4.1); max_attempts=1 fails.
func TestExpireStaleDispatchedTimeout(t *testing.T) {
	s, c, seed := newService(t)
	ctx := context.Background()
	retry := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	final := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	if _, err := s.DB.Exec(ctx, `UPDATE task SET max_attempts = 1 WHERE id = $1`, final); err != nil {
		t.Fatal(err)
	}
	tokRetry := dispatch(t, s, seed, retry)
	dispatch(t, s, seed, final)

	c.Advance(4 * time.Minute)
	if n, _ := s.ExpireStale(ctx, c.Now()); n != 0 {
		t.Fatalf("expired %d at 4m, want 0", n)
	}
	c.Advance(time.Minute + time.Second)
	n, err := s.ExpireStale(ctx, c.Now())
	if err != nil || n != 2 {
		t.Fatalf("expired %d err=%v, want 2", n, err)
	}
	r := row(t, s, retry)
	if r.Status != Queued || r.Attempt != 2 || r.RuntimeID != nil {
		t.Fatalf("retry task = %s attempt %d rt %v; want queued attempt 2", r.Status, r.Attempt, r.RuntimeID)
	}
	atts, _ := ListAttempts(ctx, s.DB, retry)
	if len(atts) != 1 || atts[0].Outcome == nil || *atts[0].Outcome != "timeout" {
		t.Fatalf("attempt history = %+v, want attempt 1 outcome timeout", atts)
	}
	f := row(t, s, final)
	if f.Status != Failed || f.FailureKind == nil || *f.FailureKind != "timeout" {
		t.Fatalf("final task = %s %v; want failed(timeout)", f.Status, f.FailureKind)
	}
	if _, err := s.Tokens.Verify(ctx, s.DB, tokRetry); err != tokens.ErrRevoked {
		t.Fatalf("token after requeue: %v, want revoked", err)
	}
}

// daemon-protocol §4.2 v0.2 (PR #22 N5): preparing is not a heartbeat subject —
// a slow cold start is not cut at 3 minutes; §4.1's 5 minutes from dispatch
// bounds it instead (outcome timeout).
func TestExpireStalePreparingUsesDispatchTimeout(t *testing.T) {
	s, c, seed := newService(t)
	ctx := context.Background()
	id := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	dispatch(t, s, seed, id)
	c.Advance(time.Minute)
	if err := s.Phase(ctx, id, 1, "preparing"); err != nil {
		t.Fatal(err)
	}
	c.Advance(contracts.HeartbeatExpiry + time.Second) // 4m01s after dispatch, 3m01s silent in preparing
	if n, err := s.ExpireStale(ctx, c.Now()); err != nil || n != 0 {
		t.Fatalf("preparing expired by heartbeat rule: n=%d err=%v, want 0", n, err)
	}
	if r := row(t, s, id); r.Status != Preparing {
		t.Fatalf("task = %s, want preparing", r.Status)
	}
	c.Advance(time.Minute) // 5m01s after dispatch
	if n, err := s.ExpireStale(ctx, c.Now()); err != nil || n != 1 {
		t.Fatalf("preparing past DispatchedTimeout: n=%d err=%v, want 1", n, err)
	}
	r := row(t, s, id)
	if r.Status != Queued || r.Attempt != 2 {
		t.Fatalf("task = %s attempt %d, want queued attempt 2", r.Status, r.Attempt)
	}
	atts, _ := ListAttempts(ctx, s.DB, id)
	if len(atts) != 1 || atts[0].Outcome == nil || *atts[0].Outcome != "timeout" {
		t.Fatalf("attempt history = %+v, want timeout", atts)
	}
}

// E5-03 / E11-03: running with no heartbeat for 3 minutes → runtime offline,
// task requeued (attempt+1), token revoked, revoke command queued.
func TestExpireStaleHeartbeat(t *testing.T) {
	s, c, seed := newService(t)
	ctx := context.Background()
	id := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	tok := dispatch(t, s, seed, id)
	if err := s.Phase(ctx, id, 1, "preparing"); err != nil {
		t.Fatal(err)
	}
	if err := s.Phase(ctx, id, 1, "running"); err != nil {
		t.Fatal(err)
	}
	// heartbeats keep it alive
	for i := 0; i < 8; i++ {
		c.Advance(15 * time.Second)
		if err := s.Heartbeat(ctx, id, 1, c.Now()); err != nil {
			t.Fatal(err)
		}
	}
	c.Advance(2*time.Minute + 59*time.Second)
	if n, _ := s.ExpireStale(ctx, c.Now()); n != 0 {
		t.Fatalf("expired %d before 3m, want 0", n)
	}
	c.Advance(2 * time.Second)
	n, err := s.ExpireStale(ctx, c.Now())
	if err != nil || n != 1 {
		t.Fatalf("expired %d err=%v, want 1", n, err)
	}
	r := row(t, s, id)
	if r.Status != Queued || r.Attempt != 2 {
		t.Fatalf("task = %s attempt %d, want queued attempt 2", r.Status, r.Attempt)
	}
	if _, err := s.Tokens.Verify(ctx, s.DB, tok); err != tokens.ErrRevoked {
		t.Fatalf("token: %v, want revoked (E11-03)", err)
	}
	var rtStatus string
	_ = s.DB.QueryRow(ctx, `SELECT status FROM runtime WHERE id = $1`, seed.RuntimeID).Scan(&rtStatus)
	if rtStatus != "offline" {
		t.Fatalf("runtime = %s, want offline", rtStatus)
	}
	cmds, err := tokens.PendingCommands(ctx, s.DB, seed.RuntimeID, c.Now())
	if err != nil || len(cmds) != 1 || cmds[0].Type != contracts.CmdRevoke || cmds[0].TaskID != id.String() || cmds[0].Attempt != 1 {
		t.Fatalf("commands = %+v err=%v, want one revoke for attempt 1", cmds, err)
	}
	// Stale attempt reports are rejected; the daemon learns via the revoke.
	if err := s.Heartbeat(ctx, id, 1, c.Now()); err != ErrStaleAttempt {
		t.Fatalf("stale heartbeat: %v", err)
	}
	if err := s.Phase(ctx, id, 1, "running"); err != ErrStaleAttempt {
		t.Fatalf("stale phase: %v", err)
	}
}

// finish is idempotent per attempt and revokes the token.
func TestFinishIdempotent(t *testing.T) {
	s, _, seed := newService(t)
	ctx := context.Background()
	id := testdb.AddTask(t, s.DB, seed, seed.SessionID, t0)
	tok := dispatch(t, s, seed, id)
	_ = s.Phase(ctx, id, 1, "running")
	f := contracts.Finish{Outcome: "completed", StopReason: "end_turn", Usage: contracts.Usage{InputTokens: 10, OutputTokens: 5, CostUSD: 0.01}}
	st, err := s.Finish(ctx, id, 1, f)
	if err != nil || st != Completed {
		t.Fatalf("finish = %s %v", st, err)
	}
	st, err = s.Finish(ctx, id, 1, contracts.Finish{Outcome: "failed", FailureKind: contracts.FailOther})
	if err != nil || st != Completed {
		t.Fatalf("second finish = %s %v; want first result kept", st, err)
	}
	if _, err := s.Tokens.Verify(ctx, s.DB, tok); err != tokens.ErrRevoked {
		t.Fatalf("token after completion: %v", err)
	}
	var lane string
	_ = s.DB.QueryRow(ctx, `SELECT l.status FROM lane l JOIN task t ON t.lane_id = l.id WHERE t.id = $1`, id).Scan(&lane)
	if lane != "done" {
		t.Fatalf("lane = %s, want done", lane)
	}
	u, _ := GetUsage(ctx, s.DB, id)
	if u == nil || u.InputTokens != 10 {
		t.Fatalf("usage = %+v", u)
	}
}
