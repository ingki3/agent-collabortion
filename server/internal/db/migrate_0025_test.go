package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestMigrate0025Verify is T-R1a's migration check (PRD v0.19 §10 R1): build a
// 0024 database with sessions in every state and every child table populated,
// snapshot it with verify_0025_pre.sql, apply 0025, and require every row of
// verify_0025.sql to be 0. The same two files are what R4 runs on real data
// and what e2e/p5/87_migrate_0025.sh runs through the old and new servers.
func TestMigrate0025Verify(t *testing.T) {
	base := testURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Own database: TestMigrate drops schema public on the base one.
	admin, err := Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "colab_m25_" + hex.EncodeToString(b[:])
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+name+` WITH (FORCE)`) }()
	u, _ := url.Parse(base)
	u.Path = "/" + name
	pool, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	if _, err := migrateTo(ctx, pool, 24); err != nil {
		t.Fatalf("migrate to 0024: %v", err)
	}
	if _, err := pool.Exec(ctx, seed0024); err != nil {
		t.Fatalf("seed 0024: %v", err)
	}
	pre, err := os.ReadFile("../../migrations/verify/verify_0025_pre.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(pre)); err != nil {
		t.Fatalf("verify_0025_pre: %v", err)
	}
	if n, err := MigratePool(ctx, pool); err != nil || n < 1 {
		t.Fatalf("apply 0025: applied %d, err %v", n, err)
	}
	verify, err := os.ReadFile("../../migrations/verify/verify_0025.sql")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, string(verify))
	if err != nil {
		t.Fatalf("verify_0025: %v", err)
	}
	checks := 0
	for rows.Next() {
		var chk string
		var n int64
		if err := rows.Scan(&chk, &n); err != nil {
			t.Fatal(err)
		}
		checks++
		if n != 0 {
			t.Errorf("verify_0025 %q = %d, want 0", chk, n)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if checks < 40 {
		t.Fatalf("verify_0025 returned %d checks — the file lost rows", checks)
	}

	// The seed is not empty, or every "- pre" check above is 0 − 0.
	var rooms, works, humans int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM room), (SELECT count(*) FROM work),
		(SELECT count(*) FROM room_participant WHERE user_id IS NOT NULL)`).Scan(&rooms, &works, &humans); err != nil {
		t.Fatal(err)
	}
	if rooms != 5 || works != 5 {
		t.Fatalf("rooms=%d works=%d, want 5 and 5", rooms, works)
	}
	// 5 owners + the one session whose Director is not its creator + one deputy.
	if humans != 7 {
		t.Errorf("human participants = %d, want 7", humans)
	}

	// The old table is kept but frozen: one write path (room_participant).
	_, err = pool.Exec(ctx, `UPDATE session_participant SET joined_at = now()`)
	if err == nil || !strings.Contains(err.Error(), "frozen") {
		t.Errorf("writing session_participant after 0025: err = %v, want the frozen trigger", err)
	}
}

// seed0024 is one workspace in the 0024 schema: five sessions (draft, active
// with a deputy and a Director who is not the creator, paused(budget) with a
// detail, completed, cancelled — one with a multi-line goal) and at least one
// row in every child table 0025 touches.
const seed0024 = `
INSERT INTO app_user (id, email, display_name) VALUES
  ('00000000-0000-0000-0000-0000000000a1', 'a@x.test', 'A'),
  ('00000000-0000-0000-0000-0000000000a2', 'b@x.test', 'B'),
  ('00000000-0000-0000-0000-0000000000a3', 'c@x.test', 'C');
INSERT INTO workspace (id, name, slug) VALUES ('00000000-0000-0000-0000-0000000000b1', 'ws', 'ws-m25');
INSERT INTO workspace_settings (workspace_id) VALUES ('00000000-0000-0000-0000-0000000000b1');
INSERT INTO member (id, workspace_id, user_id, role) VALUES
  ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'owner'),
  ('00000000-0000-0000-0000-0000000000c2', '00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a2', 'member');
INSERT INTO runtime (id, workspace_id, name, status) VALUES
  ('00000000-0000-0000-0000-0000000000d1', '00000000-0000-0000-0000-0000000000b1', 'mac', 'online');
INSERT INTO agent (id, workspace_id, name, role, role_description, instructions, owner_id) VALUES
  ('00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000b1', 'Lead', 'lead', 'x', 'x', '00000000-0000-0000-0000-0000000000a1'),
  ('00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000b1', 'Rev', 'reviewer', 'x', 'x', '00000000-0000-0000-0000-0000000000a1');
INSERT INTO agent_profile (id, agent_id, name, runtime_kind, model, is_default) VALUES
  ('00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-0000000000e1', 'default', 'claude_code', 'claude-sonnet-5', true),
  ('00000000-0000-0000-0000-0000000000f2', '00000000-0000-0000-0000-0000000000e2', 'default', 'claude_code', 'claude-sonnet-5', true);

INSERT INTO session (id, workspace_id, title, goal, acceptance_criteria, director_user_id, deputy_director_user_id,
                     assignee_agent_id, runtime_id, isolation, limits, autonomy, status, paused_reason, paused_detail,
                     cost_usd, completion_met, created_by, created_at, updated_at, started_at, finished_at) VALUES
  ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-0000000000b1', 'draft', 'g1', '{}',
   '00000000-0000-0000-0000-0000000000a1', NULL, NULL, NULL, '{"kind":"none"}', '{"max_parallel_lanes": 5}', 'guided',
   'draft', NULL, NULL, 0, '{}', '00000000-0000-0000-0000-0000000000a1', now() - interval '5 days', now() - interval '5 days', NULL, NULL),
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000b1', 'active', E'first line\nsecond line', '{"a","b"}',
   '00000000-0000-0000-0000-0000000000a2', '00000000-0000-0000-0000-0000000000a3', '00000000-0000-0000-0000-0000000000e1',
   '00000000-0000-0000-0000-0000000000d1', '{"kind":"worktree","repo_path":"/r"}', '{"max_parallel_lanes": 3, "budget_usd": 2}', 'autonomous',
   'active', NULL, NULL, 1.25, '{"0": true}', '00000000-0000-0000-0000-0000000000a1', now() - interval '4 days', now() - interval '1 day', now() - interval '4 days', NULL),
  ('00000000-0000-0000-0000-000000000103', '00000000-0000-0000-0000-0000000000b1', 'paused', 'g3', '{}',
   '00000000-0000-0000-0000-0000000000a1', NULL, '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000d1',
   '{"kind":"none"}', '{"max_parallel_lanes": 5, "max_concurrent_works": 2}', 'guided', 'paused', 'budget', '{"reason":"budget"}',
   3.5, '{}', '00000000-0000-0000-0000-0000000000a1', now() - interval '3 days', now() - interval '2 days', now() - interval '3 days', NULL),
  ('00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-0000000000b1', 'done', 'g4', '{}',
   '00000000-0000-0000-0000-0000000000a1', NULL, '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000d1',
   '{"kind":"none"}', '{"max_parallel_lanes": 5}', 'guided', 'completed', NULL, NULL,
   0.5, '{}', '00000000-0000-0000-0000-0000000000a1', now() - interval '9 days', now() - interval '8 days', now() - interval '9 days', now() - interval '8 days'),
  ('00000000-0000-0000-0000-000000000105', '00000000-0000-0000-0000-0000000000b1', 'gone', 'g5', '{}',
   '00000000-0000-0000-0000-0000000000a1', NULL, NULL, NULL, '{"kind":"none"}', '{"max_parallel_lanes": 5}', 'supervised',
   'cancelled', NULL, NULL, 0, '{}', '00000000-0000-0000-0000-0000000000a1', now() - interval '7 days', now() - interval '6 days', NULL, now() - interval '6 days');
UPDATE session SET rebind_prompt = 'p' WHERE id = '00000000-0000-0000-0000-000000000103';

INSERT INTO session_participant (session_id, agent_id, profile_id) VALUES
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000f1'),
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000e2', '00000000-0000-0000-0000-0000000000f2'),
  ('00000000-0000-0000-0000-000000000103', '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000f1'),
  ('00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000f1');
INSERT INTO session_context (session_id, type, ref) VALUES ('00000000-0000-0000-0000-000000000102', 'url', 'https://x.test');

INSERT INTO message (id, session_id, author_type, author_id, content) VALUES
  ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000102', 'user', '00000000-0000-0000-0000-0000000000a2', 'hi'),
  ('00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-000000000104', 'user', '00000000-0000-0000-0000-0000000000a1', 'go');
INSERT INTO lane (id, session_id, agent_id, profile_id, status) VALUES
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000f1', 'running'),
  ('00000000-0000-0000-0000-000000000302', '00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-0000000000e1', '00000000-0000-0000-0000-0000000000f1', 'done');
INSERT INTO task (id, lane_id, session_id, agent_id, profile_id, trigger_message_id, originator_user_id, status) VALUES
  ('00000000-0000-0000-0000-000000000401', '00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000e1',
   '00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-0000000000a2', 'running'),
  ('00000000-0000-0000-0000-000000000402', '00000000-0000-0000-0000-000000000302', '00000000-0000-0000-0000-000000000104', '00000000-0000-0000-0000-0000000000e1',
   '00000000-0000-0000-0000-0000000000f1', '00000000-0000-0000-0000-000000000202', '00000000-0000-0000-0000-0000000000a1', 'completed');
INSERT INTO task_event (task_id, seq, class, verb) VALUES ('00000000-0000-0000-0000-000000000401', 0, 'status', 'started');
INSERT INTO workdir (session_id, agent_id, kind, path_or_ref) VALUES
  ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000e1', 'worktree', '/w/102/lead');
INSERT INTO decision (session_id, summary, source) VALUES ('00000000-0000-0000-0000-000000000102', 'use x', 'agent');
INSERT INTO hitl_request (session_id, source, type, question, due_at) VALUES
  ('00000000-0000-0000-0000-000000000103', 'system', 'approval', 'raise budget?', now() + interval '1 day');
INSERT INTO artifact (session_id, name, type, storage_ref, submitted_by_task_id) VALUES
  ('00000000-0000-0000-0000-000000000104', 'out.md', 'file', 'inline:x', '00000000-0000-0000-0000-000000000402');
INSERT INTO inbox_item (member_id, type, severity, session_id) VALUES
  ('00000000-0000-0000-0000-0000000000c1', 'session_paused', 'action_required', '00000000-0000-0000-0000-000000000103'),
  ('00000000-0000-0000-0000-0000000000c1', 'runtime_offline', 'attention', NULL);
INSERT INTO session_hop (session_id, to_agent_id, rule) VALUES ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-0000000000e1', 1);
INSERT INTO activity_log (workspace_id, session_id, actor_type, action) VALUES
  ('00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-000000000102', 'user', 'session.director_changed');
`
