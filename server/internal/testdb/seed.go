package testdb

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Seed is a minimal workspace: one user (owner), one runtime, one agent with a
// default profile, one active `none` session (runtime unfixed) with the agent
// as participant and assignee. Tasks are added by the tests.
type Seed struct {
	UserID, WorkspaceID, RuntimeID, AgentID, ProfileID, SessionID uuid.UUID
}

// Plant inserts the seed rows with created_at = now (the test clock's origin).
func Plant(t *testing.T, pool *pgxpool.Pool, now time.Time) Seed {
	t.Helper()
	ctx := context.Background()
	var s Seed
	must := func(err error) {
		if err != nil {
			t.Helper()
			t.Fatalf("seed: %v", err)
		}
	}
	must(pool.QueryRow(ctx, `INSERT INTO app_user (email, display_name, created_at) VALUES ('dir@example.com', 'Dir', $1) RETURNING id`, now).Scan(&s.UserID))
	must(pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, created_at, updated_at) VALUES ('ws', 'ws', $1, $1) RETURNING id`, now).Scan(&s.WorkspaceID))
	_, err := pool.Exec(ctx, `INSERT INTO workspace_settings (workspace_id) VALUES ($1)`, s.WorkspaceID)
	must(err)
	_, err = pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'owner', $3)`, s.WorkspaceID, s.UserID, now)
	must(err)
	s.RuntimeID = AddRuntime(t, pool, s.WorkspaceID, "mac-1", now)
	must(pool.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, role, role_description, instructions, owner_id, created_at, updated_at)
		VALUES ($1, 'Lead', 'lead', 'coordinates', 'be helpful', $2, $3, $3) RETURNING id`, s.WorkspaceID, s.UserID, now).Scan(&s.AgentID))
	must(pool.QueryRow(ctx, `INSERT INTO agent_profile (agent_id, name, runtime_kind, model, is_default, created_at, updated_at)
		VALUES ($1, 'default', 'claude_code', 'claude-sonnet-5', true, $2, $2) RETURNING id`, s.AgentID, now).Scan(&s.ProfileID))
	s.SessionID = AddSession(t, pool, s, nil, now)
	return s
}

// AddWorkspace adds a second, unrelated workspace (no members) so tests can
// check that nothing leaks across workspace boundaries.
func AddWorkspace(t *testing.T, pool *pgxpool.Pool, slug string, now time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, created_at, updated_at) VALUES ($1, $1, $2, $2) RETURNING id`, slug, now).Scan(&id); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_settings (workspace_id) VALUES ($1)`, id); err != nil {
		t.Fatalf("seed workspace settings: %v", err)
	}
	return id
}

// AddRuntime adds an online runtime.
func AddRuntime(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, name string, now time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	// `workdir_root` is seeded because a runtime that can CLAIM has probed
	// (daemon-protocol §3 — the daemon probes at start, before its first
	// claim), and since v0.7.3 §4.1 the server assembles the bundle's absolute
	// `workdir.path` from it. A seed without it describes a machine no
	// `worktree` session could ever run on (S-55).
	if err := pool.QueryRow(context.Background(), `INSERT INTO runtime (workspace_id, name, status, workdir_root, last_seen_at, created_at, updated_at) VALUES ($1, $2, 'online', '/Users/x/.colab', $3, $3, $3) RETURNING id`, wsID, name, now).Scan(&id); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	return id
}

// AddSession adds an active `none` session with the seed agent, optionally
// fixed to runtimeID.
func AddSession(t *testing.T, pool *pgxpool.Pool, s Seed, runtimeID *uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	// A session is a room plus its one work since 0025 (PRD v0.19 §7).
	if err := pool.QueryRow(ctx, `INSERT INTO room (workspace_id, name, description, owner_user_id, default_director_user_id, runtime_id, isolation, created_by, created_at, updated_at)
		VALUES ($1, 'S', 'goal', $2, $2, $3, '{"kind":"none"}', $2, $4, $4) RETURNING id`, s.WorkspaceID, s.UserID, runtimeID, now).Scan(&id); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	// … made by the old path, so the room carries its session's mark
	// (room.legacy_work_id, T-R1b2) as createSession's rooms do.
	if _, err := pool.Exec(ctx, `
		WITH w AS (INSERT INTO work (room_id, title, goal, director_user_id, assignee_agent_id, status, created_by, created_at, updated_at, started_at)
		           VALUES ($1, 'S', 'goal', $2, $3, 'active', $2, $4, $4, $4) RETURNING id)
		UPDATE room SET legacy_work_id = (SELECT id FROM w) WHERE id = $1`, id, s.UserID, s.AgentID, now); err != nil {
		t.Fatalf("seed work: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO room_participant (room_id, agent_id, profile_id, joined_at) VALUES ($1, $2, $3, $4)`, id, s.AgentID, s.ProfileID, now); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO room_participant (room_id, user_id, role, joined_at) VALUES ($1, $2, 'owner', $3)`, id, s.UserID, now); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	return id
}

// AddTask adds a queued task (new lane) triggered by a fresh user message.
func AddTask(t *testing.T, pool *pgxpool.Pool, s Seed, sessionID uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var msgID, laneID, taskID uuid.UUID
	// FR-3.1.1 (T-R1b1): a session's message, lane and task belong to its one
	// mission — the claim gates a task on ITS mission (queue.Claim).
	if err := pool.QueryRow(ctx, `INSERT INTO message (session_id, author_type, author_id, content, created_at, work_id) VALUES ($1, 'user', $2, 'hello', $3, (SELECT id FROM work WHERE room_id = $1)) RETURNING id`, sessionID, s.UserID, now).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, created_at, updated_at, work_id) VALUES ($1, $2, $3, $4, $4, (SELECT id FROM work WHERE room_id = $1)) RETURNING id`, sessionID, s.AgentID, s.ProfileID, now).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, originator_user_id, created_at, updated_at, work_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $7, (SELECT id FROM work WHERE room_id = $2)) RETURNING id`,
		laneID, sessionID, s.AgentID, s.ProfileID, msgID, s.UserID, now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}
