package workdirs

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestBundleWorkdirPathsExcludesOtherAgents is E13-08 against real rows.
//
// The golden table's adapter names one path per agent; this is the check that
// the QUERY behind it filters by ownership. Handing QA the Frontend checkout is
// not a stale read — under `worktree` it is two agents writing one git index,
// and the reviewer can edit the code it is reviewing.
func TestBundleWorkdirPathsExcludesOtherAgents(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()

	sessionID, feID, qaID := seedTwoAgentSession(ctx, t, pool, now)

	if _, err := Record(ctx, pool, Report{
		Kind: "worktree", Path: "/w/s/frontend", SessionID: sessionID, AgentID: &feID,
	}, now); err != nil {
		t.Fatalf("record frontend workdir: %v", err)
	}
	if _, err := Record(ctx, pool, Report{
		Kind: "worktree", Path: "/w/s/qa", SessionID: sessionID, AgentID: &qaID,
	}, now); err != nil {
		t.Fatalf("record qa workdir: %v", err)
	}

	qa, err := BundleWorkdirPaths(ctx, pool, sessionID, qaID)
	if err != nil {
		t.Fatalf("bundle paths: %v", err)
	}
	if len(qa) != 1 || qa[0] != "/w/s/qa" {
		t.Fatalf("QA bundle = %v, want exactly [/w/s/qa] — the review target is the artifact, "+
			"and naming Frontend's checkout lets a reviewer edit what it is reviewing (E13-08)", qa)
	}
	fe, err := BundleWorkdirPaths(ctx, pool, sessionID, feID)
	if err != nil {
		t.Fatalf("bundle paths: %v", err)
	}
	if len(fe) != 1 || fe[0] != "/w/s/frontend" {
		t.Fatalf("Frontend bundle = %v, want exactly [/w/s/frontend]", fe)
	}
}

// TestJudgeGCReasonsAreDistinguishable is E13-12 vs E13-13 stated once more at
// the level a reader of the notification cares about: the two blocked cases ask
// the Director for different things, so one string cannot serve both.
func TestJudgeGCReasonsAreDistinguishable(t *testing.T) {
	past := 30 * 24 * time.Hour
	unmerged := JudgeGC(GCCase{Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
		SinceSessionEnd: past, CommitsAhead: 3})
	dirty := JudgeGC(GCCase{Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
		SinceSessionEnd: past, TreeDirty: true})
	if unmerged.Reason == dirty.Reason {
		t.Fatalf("both blocked rows report %q", unmerged.Reason)
	}
	if GCReasonText(unmerged.Reason) == GCReasonText(dirty.Reason) {
		t.Errorf("the two sentences are identical — E13-12 asks for a merge, E13-13 for a commit")
	}
	// Both at once resolves to the uncommitted side: work that was never
	// committed exists nowhere else, while a commit survives on the branch this
	// GC is forbidden from deleting.
	both := JudgeGC(GCCase{Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
		SinceSessionEnd: past, CommitsAhead: 3, TreeDirty: true})
	if both.Reason != GCReasonUncommitted {
		t.Errorf("both-at-once reason = %q, want %q", both.Reason, GCReasonUncommitted)
	}
	if both.Delete {
		t.Error("a workdir with both problems was deleted")
	}
}

func seedTwoAgentSession(ctx context.Context, t *testing.T, pool *pgxpool.Pool, now time.Time) (sessionID, feID, qaID uuid.UUID) {
	t.Helper()
	var wsID, userID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ('w', 'w-`+uuid.NewString()[:8]+`') RETURNING id`).Scan(&wsID); err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name) VALUES ($1, 'u') RETURNING id`,
		uuid.NewString()+"@x.test").Scan(&userID); err != nil {
		t.Fatalf("user: %v", err)
	}
	for _, a := range []struct {
		name string
		out  *uuid.UUID
	}{{"frontend", &feID}, {"qa", &qaID}} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO agent (workspace_id, name, role, role_description, instructions, owner_id)
			VALUES ($1, $2, 'engineer', 'x', 'x', $3) RETURNING id`, wsID, a.name, userID).Scan(a.out); err != nil {
			t.Fatalf("agent %s: %v", a.name, err)
		}
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO session (workspace_id, title, goal, director_user_id, isolation, status, created_by, created_at, updated_at)
		VALUES ($1, 's', 'g', $2, '{"kind":"worktree"}', 'active', $2, $3, $3) RETURNING id`,
		wsID, userID, now).Scan(&sessionID); err != nil {
		t.Fatalf("session: %v", err)
	}
	return sessionID, feID, qaID
}

// TestRuntimeGoneRowsLeaveAndReturn is U2 (T-I4 §0.2), both directions.
//
// A rebind parks the dead machine's rows at `retained` with
// `gc_blocked_reason = 'runtime_gone'`. `BundleWorkdirPaths` only excluded
// `deleted`, so the NEW machine's bundle kept naming a path on the computer
// that vanished — the e2e run folded those rows away by hand to get past it.
// The other direction matters too: when the new machine happens to lay its
// checkouts out at the same path, a live report must bring the row back rather
// than leave the session with no workdir at all.
func TestRuntimeGoneRowsLeaveAndReturn(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sessionID, feID, _ := seedTwoAgentSession(ctx, t, pool, now)

	if _, err := Record(ctx, pool, Report{
		Kind: "worktree", Path: "/w/s/frontend", SessionID: sessionID, AgentID: &feID, Bytes: 2048,
	}, now); err != nil {
		t.Fatal(err)
	}
	// What runtimes.Rebind writes.
	if _, err := pool.Exec(ctx, `
		UPDATE workdir SET status = 'retained', gc_blocked_reason = 'runtime_gone' WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	paths, err := BundleWorkdirPaths(ctx, pool, sessionID, feID)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("bundle candidates = %v, want none — every one of those directories is on a computer "+
			"nobody can reach (U2)", paths)
	}

	// A GC refusal is a different thing: that directory is on THIS machine and
	// is kept on purpose, so it stays reusable.
	if _, err := pool.Exec(ctx, `
		UPDATE workdir SET gc_blocked_reason = 'unmerged_commits' WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	if paths, err = BundleWorkdirPaths(ctx, pool, sessionID, feID); err != nil || len(paths) != 1 {
		t.Fatalf("a GC-blocked row was dropped too (%v, %v) — only `runtime_gone` names a machine that is gone", paths, err)
	}

	// Back to runtime_gone, then a live report for the same path.
	if _, err := pool.Exec(ctx, `
		UPDATE workdir SET status = 'retained', gc_blocked_reason = 'runtime_gone' WHERE session_id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	// The finish route carries no size; it must not zero the measured one.
	if _, err := Record(ctx, pool, Report{
		Kind: "worktree", Path: "/w/s/frontend", SessionID: sessionID, AgentID: &feID,
	}, now); err != nil {
		t.Fatal(err)
	}
	var status string
	var reason *string
	var bytes int64
	if err := pool.QueryRow(ctx, `
		SELECT status::text, gc_blocked_reason, disk_bytes FROM workdir WHERE session_id = $1`, sessionID).
		Scan(&status, &reason, &bytes); err != nil {
		t.Fatal(err)
	}
	if status != "active" || reason != nil {
		t.Errorf("row after a live report: status=%q reason=%v, want active/NULL — the directory is on "+
			"the machine reporting it now", status, reason)
	}
	if bytes != 2048 {
		t.Errorf("disk_bytes = %d, want 2048 — a report without a size means \"not measured\"", bytes)
	}
}
