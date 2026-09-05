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

// AddRuntime adds an online runtime.
func AddRuntime(t *testing.T, pool *pgxpool.Pool, wsID uuid.UUID, name string, now time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `INSERT INTO runtime (workspace_id, name, status, last_seen_at, created_at, updated_at) VALUES ($1, $2, 'online', $3, $3, $3) RETURNING id`, wsID, name, now).Scan(&id); err != nil {
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
	if err := pool.QueryRow(ctx, `INSERT INTO session (workspace_id, title, goal, director_user_id, assignee_agent_id, runtime_id, isolation, status, created_by, created_at, updated_at, started_at)
		VALUES ($1, 'S', 'goal', $2, $3, $4, '{"kind":"none"}', 'active', $2, $5, $5, $5) RETURNING id`, s.WorkspaceID, s.UserID, s.AgentID, runtimeID, now).Scan(&id); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO session_participant (session_id, agent_id, profile_id, joined_at) VALUES ($1, $2, $3, $4)`, id, s.AgentID, s.ProfileID, now); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	return id
}

// AddTask adds a queued task (new lane) triggered by a fresh user message.
func AddTask(t *testing.T, pool *pgxpool.Pool, s Seed, sessionID uuid.UUID, now time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var msgID, laneID, taskID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO message (session_id, author_type, author_id, content, created_at) VALUES ($1, 'user', $2, 'hello', $3) RETURNING id`, sessionID, s.UserID, now).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO lane (session_id, agent_id, profile_id, created_at, updated_at) VALUES ($1, $2, $3, $4, $4) RETURNING id`, sessionID, s.AgentID, s.ProfileID, now).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO task (lane_id, session_id, agent_id, profile_id, trigger_message_id, originator_user_id, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $7) RETURNING id`,
		laneID, sessionID, s.AgentID, s.ProfileID, msgID, s.UserID, now).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	return taskID
}
