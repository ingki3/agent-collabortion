package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"testing"
	"time"
)

// TestMigrate0036DedupesOpenApprovals reproduces the live database before 0036
// (실측 「게임 제작 방」): one mission with five open user_approval requests
// (13:41 · 14:04 · 14:08 · 14:21 · 14:30) and one answered (13:45), each with
// its inbox row. 0036's unique index must not fail on it: the latest open one
// stays, the other four are cancelled with their inbox rows removed, the
// answered one is untouched, and room-layer requests (work_id NULL) are not in
// scope.
func TestMigrate0036DedupesOpenApprovals(t *testing.T) {
	base := testURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	admin, err := Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var b [6]byte
	_, _ = rand.Read(b[:])
	name := "colab_m36_" + hex.EncodeToString(b[:])
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

	if _, err := migrateTo(ctx, pool, 35); err != nil {
		t.Fatalf("migrate to 0035: %v", err)
	}
	if _, err := pool.Exec(ctx, seed0035); err != nil {
		t.Fatalf("seed 0035: %v", err)
	}
	if n, err := migrateTo(ctx, pool, 36); err != nil || n != 1 {
		t.Fatalf("apply 0036: applied %d, err %v", n, err)
	}

	const work = "00000000-0000-0000-0000-000000000301"
	rows, err := pool.Query(ctx, `SELECT to_char(created_at AT TIME ZONE 'Asia/Seoul', 'HH24:MI'), status::text FROM hitl_request
		WHERE work_id = $1 ORDER BY created_at`, work)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for rows.Next() {
		var at, st string
		if err := rows.Scan(&at, &st); err != nil {
			t.Fatal(err)
		}
		got[at] = st
	}
	rows.Close()
	want := map[string]string{
		"13:41": "cancelled", "13:45": "answered", "14:04": "cancelled",
		"14:08": "cancelled", "14:21": "cancelled", "14:30": "open",
	}
	for at, st := range want {
		if got[at] != st {
			t.Errorf("request %s: status %q, want %q", at, got[at], st)
		}
	}
	if len(got) != len(want) {
		t.Errorf("mission has %d requests, want %d", len(got), len(want))
	}

	// Nobody answered the cancelled ones: no answer, no decision.
	var answeredCancelled, decisions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hitl_request
		WHERE work_id = $1 AND status = 'cancelled' AND (answered_at IS NOT NULL OR answered_by IS NOT NULL)`, work).
		Scan(&answeredCancelled); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM decision`).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if answeredCancelled != 0 || decisions != 0 {
		t.Errorf("cancelled-with-answer = %d, decisions = %d, want 0 and 0", answeredCancelled, decisions)
	}

	// Inbox: one open-request line for the mission — the survivor's.
	var inboxOpen int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item i JOIN hitl_request h ON h.id = i.ref_id
		WHERE i.type = 'hitl_request' AND h.work_id = $1 AND h.status = 'open'`, work).Scan(&inboxOpen); err != nil {
		t.Fatal(err)
	}
	var inboxCancelled int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inbox_item i JOIN hitl_request h ON h.id = i.ref_id
		WHERE i.type = 'hitl_request' AND h.status = 'cancelled'`).Scan(&inboxCancelled); err != nil {
		t.Fatal(err)
	}
	if inboxOpen != 1 || inboxCancelled != 0 {
		t.Errorf("inbox: %d open-request rows, %d on cancelled requests; want 1 and 0", inboxOpen, inboxCancelled)
	}

	// Room layer (work_id NULL) is not the mission rule: both stay open.
	var roomOpen int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM hitl_request
		WHERE work_id IS NULL AND purpose = 'user_approval' AND status = 'open'`).Scan(&roomOpen); err != nil {
		t.Fatal(err)
	}
	if roomOpen != 2 {
		t.Errorf("room-layer open user_approval = %d, want 2 (untouched)", roomOpen)
	}

	// And the index now holds the rule.
	_, err = pool.Exec(ctx, `INSERT INTO hitl_request (session_id, work_id, source, purpose, type, question, due_at)
		VALUES ('00000000-0000-0000-0000-000000000201', $1, 'system', 'user_approval', 'approval', 'again', now() + interval '1 day')`, work)
	if err == nil {
		t.Error("second open user_approval on the mission after 0036: want unique violation")
	}
}

// seed0035 is the pre-0036 live shape: one room, one mission, six
// user_approval requests on that mission (five open), each with an inbox row,
// plus two open room-layer user_approval requests (work_id NULL).
const seed0035 = `
INSERT INTO app_user (id, email, display_name) VALUES
  ('00000000-0000-0000-0000-0000000000a1', 'a@x.test', 'A');
INSERT INTO workspace (id, name, slug) VALUES ('00000000-0000-0000-0000-0000000000b1', 'ws', 'ws-m36');
INSERT INTO member (id, workspace_id, user_id, role) VALUES
  ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-0000000000b1', '00000000-0000-0000-0000-0000000000a1', 'owner');
INSERT INTO room (id, workspace_id, isolation, created_by, name, owner_user_id) VALUES
  ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-0000000000b1', '{"kind":"none"}',
   '00000000-0000-0000-0000-0000000000a1', '게임 제작 방', '00000000-0000-0000-0000-0000000000a1');
INSERT INTO work (id, room_id, title, goal, director_user_id, created_by) VALUES
  ('00000000-0000-0000-0000-000000000301', '00000000-0000-0000-0000-000000000201', 'game', 'g',
   '00000000-0000-0000-0000-0000000000a1', '00000000-0000-0000-0000-0000000000a1');

INSERT INTO hitl_request (id, session_id, work_id, source, purpose, type, question, due_at, status, approved,
                          answered_by, answered_at, created_at) VALUES
  ('00000000-0000-0000-0000-000000000401', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000301',
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 13:41+09'),
  ('00000000-0000-0000-0000-000000000402', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000301',
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'answered', true,
   '00000000-0000-0000-0000-0000000000a1', '2026-09-25 13:50+09', '2026-09-25 13:45+09'),
  ('00000000-0000-0000-0000-000000000403', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000301',
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 14:04+09'),
  -- ids do not follow time: the survivor is picked by created_at, not id.
  ('00000000-0000-0000-0000-000000000499', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000301',
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 14:08+09'),
  ('00000000-0000-0000-0000-000000000405', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000301',
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 14:21+09'),
  ('00000000-0000-0000-0000-000000000406', '00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000301',
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 14:30+09'),
  ('00000000-0000-0000-0000-000000000411', '00000000-0000-0000-0000-000000000201', NULL,
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 14:00+09'),
  ('00000000-0000-0000-0000-000000000412', '00000000-0000-0000-0000-000000000201', NULL,
   'system', 'user_approval', 'approval', 'q', '2026-09-25 23:00+09', 'open', NULL, NULL, NULL, '2026-09-25 14:10+09');

INSERT INTO inbox_item (member_id, type, severity, session_id, work_id, ref_id)
SELECT '00000000-0000-0000-0000-0000000000c1', 'hitl_request', 'action_required', session_id, work_id, id
  FROM hitl_request WHERE status = 'open';
`
