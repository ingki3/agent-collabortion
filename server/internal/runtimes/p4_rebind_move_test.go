package runtimes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// TestP4RebindMovesTheRepositoryAndRevivesWork is G7 차단 ④ (S-58) plus the two
// things that ride with it: U2's stale workdir rows and S-60's stranded task.
//
// The rebind changed `runtime_id` and left `isolation.repo_path` pointing at an
// absolute path on the machine that is GONE, so the new daemon ran
// `git worktree add` in a directory that does not exist there and the first
// attempt ended `failed(config)` — E14-06 could not even begin. The path to
// move to was already computed: the candidate rule matches by remote URL and
// hands back `matched_repo`, which is what the picker draws.
func TestP4RebindMovesTheRepositoryAndRevivesWork(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	t0 := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	fake := clock.NewFake(t0)
	tok := tokens.New(fake)
	tsk := tasks.New(pool, fake, tok, nil)
	s := New(pool, fake, nil, "http://colab.test").WithTasks(tsk)

	sessionID, oldRT := seedOfflineSession(ctx, t, pool, t0)
	var wsID, userID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT workspace_id, director_user_id FROM session WHERE id = $1`, sessionID).
		Scan(&wsID, &userID); err != nil {
		t.Fatal(err)
	}
	const remote = "git@github.com:acme/app.git"
	if _, err := pool.Exec(ctx, `
		UPDATE session SET isolation = jsonb_build_object('kind', 'worktree', 'repo_path', '/old/app', 'remote_url', $2::text),
		       status = 'paused', paused_reason = 'runtime_offline' WHERE id = $1`, sessionID, remote); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE runtime SET repos = $2::jsonb, status = 'offline' WHERE id = $1`, oldRT,
		`[{"path":"/old/app","remote_url":"`+remote+`"}]`); err != nil {
		t.Fatal(err)
	}
	// The machine it moves to has the same repository at a DIFFERENT path
	// (E14-04: the match is the remote URL, never the path string).
	newRT := testdb.AddRuntime(t, pool, wsID, "mac-2", t0)
	if _, err := pool.Exec(ctx, `UPDATE runtime SET repos = $2::jsonb WHERE id = $1`, newRT,
		`[{"path":"/new/app","remote_url":"`+remote+`"}]`); err != nil {
		t.Fatal(err)
	}

	agentID, laneID := seedLane(ctx, t, pool, wsID, sessionID, userID, t0)
	// One workdir on the dead machine, and one task already handed to it.
	oldPath := "/old/root/worktrees/s/dev"
	if _, err := pool.Exec(ctx, `
		INSERT INTO workdir (session_id, agent_id, lane_id, kind, path_or_ref, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'worktree', $4, 'active', $5, $5)`, sessionID, agentID, laneID, oldPath, t0); err != nil {
		t.Fatal(err)
	}
	// A diff artifact the rebind must tell the new machine to fetch (E14-06).
	if _, err := pool.Exec(ctx, `
		INSERT INTO artifact (session_id, name, type, version, storage_ref, created_at)
		VALUES ($1, 'step-1', 'diff', 1, 'step-1', $2)`, sessionID, t0); err != nil {
		t.Fatal(err)
	}
	dispatched := seedTask(ctx, t, pool, sessionID, laneID, agentID, "dispatched", oldRT, t0)
	if _, err := tok.Issue(ctx, pool, tokens.Scope{
		TaskID: dispatched, Attempt: 1, LaneID: laneID, SessionID: sessionID, AgentID: agentID, RuntimeID: &oldRT,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Rebind(ctx, wsID, sessionID, newRT, true); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	// S-58: the repository moved with the session.
	var repoPath string
	if err := pool.QueryRow(ctx, `SELECT isolation->>'repo_path' FROM session WHERE id = $1`, sessionID).Scan(&repoPath); err != nil {
		t.Fatal(err)
	}
	if repoPath != "/new/app" {
		t.Errorf("isolation.repo_path = %q, want /new/app — the rebind must use the candidate rule's "+
			"`matched_repo`, or the new daemon opens a repository that is not there (T-I4 차단 ④)", repoPath)
	}
	// The rest of the isolation object survives.
	var kind, gotRemote string
	if err := pool.QueryRow(ctx, `SELECT isolation->>'kind', isolation->>'remote_url' FROM session WHERE id = $1`, sessionID).
		Scan(&kind, &gotRemote); err != nil {
		t.Fatal(err)
	}
	if kind != "worktree" || gotRemote != remote {
		t.Errorf("isolation lost fields: kind=%q remote_url=%q", kind, gotRemote)
	}

	// U2: the dead machine's workdir must not come back in the new bundle.
	paths, err := workdirs.BundleWorkdirPaths(ctx, pool, sessionID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		if p == oldPath {
			t.Errorf("the bundle still offers %q — a path on the computer that vanished. The e2e run "+
				"had to fold these rows away by hand (U2) for the rebind to go anywhere", p)
		}
	}

	// The `rebind_prepare` command's artifact urls must be fetchable. The daemon
	// joins a relative url to the SERVER ROOT (api.Client.Download), so a path
	// without openapi's `/api/v1` prefix reaches no route at all — T-I4 saw a
	// 401 from the auth middleware and never got far enough to find out.
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM daemon_command WHERE runtime_id = $1 AND type = 'rebind_prepare'`, newRT).
		Scan(&payload); err != nil {
		t.Fatalf("no rebind_prepare command: %v", err)
	}
	var cmd struct {
		Artifacts []struct {
			URL string `json:"url"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(payload, &cmd); err != nil {
		t.Fatal(err)
	}
	if len(cmd.Artifacts) == 0 {
		t.Fatal("rebind_prepare carried no artifacts")
	}
	for _, a := range cmd.Artifacts {
		if !strings.HasPrefix(a.URL, "/api/v1/artifacts/") {
			t.Errorf("rebind_prepare url = %q, want the openapi base path prefix — the daemon joins "+
				"this to the server root and `/v1/artifacts/...` is served by nothing (실측: "+
				"`server: 404: 404 page not found` in the rebind manifest)", a.URL)
		}
	}

	// S-60: the task the dead machine had already claimed is alive again.
	var status string
	var runtimeID *uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status::text, runtime_id FROM task WHERE id = $1`, dispatched).
		Scan(&status, &runtimeID); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Errorf("stranded task status = %q, want queued — it was handed to a machine that is gone and "+
			"would have sat there until §4.1's five-minute timeout failed it", status)
	}
	var revoked *time.Time
	if err := pool.QueryRow(ctx, `SELECT revoked_at FROM task_token WHERE task_id = $1 AND attempt = 1`, dispatched).
		Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if revoked == nil {
		t.Error("the stranded attempt's token was not revoked — a zombie daemon coming back could still report on it (FR-9.1)")
	}
}

func seedLane(ctx context.Context, t *testing.T, pool *pgxpool.Pool, wsID, sessionID, userID uuid.UUID, now time.Time) (agentID, laneID uuid.UUID) {
	t.Helper()
	var profileID uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, role, role_description, instructions, owner_id, created_at, updated_at)
		VALUES ($1, 'Dev', 'engineer', 'builds', 'be helpful', $2, $3, $3) RETURNING id`, wsID, userID, now).Scan(&agentID); err != nil {
		t.Fatalf("agent: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_profile (agent_id, name, runtime_kind, model, is_default, created_at, updated_at)
		VALUES ($1, 'default', 'claude_code', 'claude-sonnet-5', true, $2, $2) RETURNING id`, agentID, now).Scan(&profileID); err != nil {
		t.Fatalf("profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO session_participant (session_id, agent_id, profile_id, joined_at) VALUES ($1, $2, $3, $4)`,
		sessionID, agentID, profileID, now); err != nil {
		t.Fatalf("participant: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, 'running', $4, $4) RETURNING id`, sessionID, agentID, profileID, now).Scan(&laneID); err != nil {
		t.Fatalf("lane: %v", err)
	}
	return agentID, laneID
}

func seedTask(ctx context.Context, t *testing.T, pool *pgxpool.Pool, sessionID, laneID, agentID uuid.UUID, status string, runtimeID uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	var profileID, id uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM agent_profile WHERE agent_id = $1`, agentID).Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO task (lane_id, session_id, agent_id, profile_id, status, runtime_id, dispatched_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5::task_status, $6, $7, $7, $7) RETURNING id`,
		laneID, sessionID, agentID, profileID, status, runtimeID, now).Scan(&id); err != nil {
		t.Fatalf("task: %v", err)
	}
	return id
}
