package workdirs

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestSweepGCGateIsTheWorkdir pins T-R1a's GC reference point (PRD v0.19 NN8,
// Lead answer Q2) against real rows: retention counts from the directory's
// last use, and the gate is "no live lane uses this directory" — not the
// session's end, which a room never reaches.
//
// A dirty worktree past retention is the observable: SweepGC marks it
// `uncommitted_changes` exactly when the judgement ran on it, and leaves it
// alone when the gate held it back.
func TestSweepGCGateIsTheWorkdir(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessionID, feID, _ := seedTwoAgentSession(ctx, t, pool, now)
	dirty := true
	wd, err := Record(ctx, pool, Report{Kind: "worktree", Path: "/w/s/frontend", SessionID: sessionID, AgentID: &feID, TreeDirty: &dirty}, now)
	if err != nil {
		t.Fatal(err)
	}
	var profileID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO agent_profile (agent_id, name, runtime_kind, model, is_default)
		VALUES ($1, 'default', 'claude_code', 'claude-sonnet-5', true) RETURNING id`, feID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	var laneID uuid.UUID
	// The session's lane carries its mission (T-R1b1 writes work_id for every
	// lane a session makes; the r1b1_room_gate migration filled the ones written before).
	if err := pool.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, workdir_id, status, work_id)
		VALUES ($1, $2, $3, $4, 'running', (SELECT id FROM work WHERE room_id = $1)) RETURNING id`, sessionID, feID, profileID, wd).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, clock.NewFake(now), nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	set := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	reason := func() string {
		t.Helper()
		var r *string
		if err := pool.QueryRow(ctx, `SELECT gc_blocked_reason FROM workdir WHERE id = $1`, wd).Scan(&r); err != nil {
			t.Fatal(err)
		}
		set(`UPDATE workdir SET gc_blocked_reason = NULL, gc_notified_at = NULL WHERE id = $1`, wd)
		if r == nil {
			return ""
		}
		return *r
	}
	sweep := func() string {
		t.Helper()
		if _, err := svc.SweepGC(ctx); err != nil {
			t.Fatal(err)
		}
		return reason()
	}

	set(`UPDATE workdir SET last_used_at = $2 WHERE id = $1`, wd, now.Add(-30*24*time.Hour))
	if got := sweep(); got != "" {
		t.Errorf("a running lane's directory was judged (%q) — a live checkout is never a candidate (E13-18)", got)
	}

	// The mission is still open but this lane is done and the folder has sat
	// idle past retention: it IS a candidate. The old session-end gate never
	// got here in a room that does not end (NN8).
	set(`UPDATE lane SET status = 'done' WHERE id = $1`, laneID)
	if got := sweep(); got != GCReasonUncommitted {
		t.Errorf("idle folder of an open room: reason = %q, want %q — retention counts from last use", got, GCReasonUncommitted)
	}

	// Inside the window from its last use: nothing, however old the session.
	set(`UPDATE workdir SET last_used_at = $2 WHERE id = $1`, wd, now.Add(-time.Hour))
	if got := sweep(); got != "" {
		t.Errorf("folder used an hour ago was judged (%q) — retention is 14 days from last use", got)
	}

	// A lane left `queued` by completeSession (tasks are cancelled, lanes are
	// not) must not hold the folder of a finished mission forever.
	set(`UPDATE workdir SET last_used_at = $2 WHERE id = $1`, wd, now.Add(-30*24*time.Hour))
	set(`UPDATE lane SET status = 'queued' WHERE id = $1`, laneID)
	set(`UPDATE work SET status = 'completed', finished_at = $2 WHERE room_id = $1`, sessionID, now)
	if got := sweep(); got != GCReasonUncommitted {
		t.Errorf("finished mission with a stale queued lane: reason = %q, want %q", got, GCReasonUncommitted)
	}
}
