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
