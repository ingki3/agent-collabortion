package httpapi

// T-S-roomlist (openapi 0.2.5): the S5 card's visibility · active_task_count,
// and createRoom inheriting the workspace's isolation default.

import (
	"encoding/json"
	"testing"
)

func (f *roomsFixture) listed(t *testing.T, c *client, roomID string) map[string]any {
	t.Helper()
	for _, it := range items(c.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/rooms?participating=false&include_archived=true&limit=200", nil)) {
		if m := it.(map[string]any); str(m, "id") == roomID {
			return m
		}
	}
	return nil
}

func (f *roomsFixture) isolationOf(t *testing.T, roomID string) map[string]any {
	t.Helper()
	var raw []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT isolation FROM room WHERE id = $1`, roomID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestRoomCreateInheritsIsolation: both places a workspace keeps its isolation
// default reach the new room — room_defaults.isolation_kind first, the older
// default_isolation column when room_defaults names none — as the kind alone
// (the repository path is S20's, before the first dispatch).
func TestRoomCreateInheritsIsolation(t *testing.T) {
	f := newRoomsFixture(t)
	settings := f.p + "/workspaces/" + f.wsID + "/settings"
	cases := []struct {
		name     string
		settings map[string]any
		want     string
	}{
		{"기본 — 둘 다 none", nil, "none"},
		{"room_defaults.isolation_kind = worktree", map[string]any{"room_defaults": map[string]any{"isolation_kind": "worktree"}}, "worktree"},
		{"room_defaults 비고 default_isolation = worktree", map[string]any{"default_isolation": "worktree"}, "worktree"},
		{"room_defaults none 이 default_isolation worktree 를 이긴다", map[string]any{"default_isolation": "worktree", "room_defaults": map[string]any{"isolation_kind": "none"}}, "none"},
		{"default_isolation container 는 방 격리가 없어 none", map[string]any{"default_isolation": "container"}, "none"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Start every case from a workspace with no override.
			f.exec(t, `UPDATE workspace_settings SET room_defaults = '{}'::jsonb, default_isolation = 'none' WHERE workspace_id = $1`, f.wsID)
			if c.settings != nil {
				f.api.must(200, "PATCH", settings, c.settings)
			}
			shown := f.api.must(200, "GET", settings, nil)["room_defaults"].(map[string]any)["isolation_kind"]
			room := f.mkRoom(t, f.member, c.name)
			iso, _ := room["isolation"].(map[string]any)
			if str(iso, "kind") != c.want {
				t.Fatalf("createRoom isolation = %v, want kind %s (S14 shows %v)", room["isolation"], c.want, shown)
			}
			if shown != "container" && shown != c.want {
				t.Fatalf("S14 room_defaults.isolation_kind = %v but the room got %s — the ⓘ line would lie", shown, c.want)
			}
			stored := f.isolationOf(t, str(room, "id"))
			if stored["kind"] != c.want {
				t.Fatalf("stored isolation = %v", stored)
			}
			if _, ok := stored["repo_path"]; ok {
				t.Fatalf("stored isolation = %v — the kind alone, no repository path", stored)
			}
		})
	}
}

// TestRoomInheritedWorktreeAsksNoIsolation: FR-2.1.1's isolation_confirm is
// for a `none` room only (R1b1). A room that inherited `worktree` — the shape
// createRoom now stores — is not asked on a computer with a repository.
func TestRoomInheritedWorktreeAsksNoIsolation(t *testing.T) {
	f := newRoomsFixture(t)
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{"room_defaults": map[string]any{"isolation_kind": "worktree"}})
	made := f.isolationOf(t, str(f.mkRoom(t, f.member, "워크트리 기본"), "id"))
	raw, _ := json.Marshal(made)
	f.exec(t, `UPDATE runtime SET repos = '[{"path": "/Users/x/repo", "remote_url": "", "branch": "main", "clean": true}]'::jsonb WHERE workspace_id = $1`, f.wsID)
	f.exec(t, `UPDATE room SET isolation = $2::jsonb WHERE id = $1`, f.sessionID, string(raw))
	_ = f.claimed(t)
	if n := f.count(t, `SELECT count(*) FROM hitl_request WHERE session_id = $1 AND purpose = 'isolation'`, f.sessionID); n != 0 {
		t.Fatalf("isolation requests = %d, want 0 — the workspace already chose worktree", n)
	}
	var pending []byte
	_ = f.pool.QueryRow(t.Context(), `SELECT isolation_pending FROM room WHERE id = $1`, f.sessionID).Scan(&pending)
	if len(pending) != 0 {
		t.Fatalf("isolation_pending = %s", pending)
	}
}

// TestRoomListActiveTaskCount: the card's active_task_count is the number
// archiveRoom's 409 tasks_active reports, for every task status — one
// definition, so the card's 「보관」 is disabled exactly when the server would
// refuse.
func TestRoomListActiveTaskCount(t *testing.T) {
	f := newRoomsFixture(t)
	var taskID string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM task WHERE session_id = $1 LIMIT 1`, f.sessionID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	rows, err := f.pool.Query(t.Context(), `SELECT unnest(enum_range(NULL::task_status))::text`)
	if err != nil {
		t.Fatal(err)
	}
	var statuses []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		statuses = append(statuses, s)
	}
	rows.Close()
	if len(statuses) < 8 {
		t.Fatalf("task_status = %v", statuses)
	}
	sp := f.roomPath(f.sessionID)
	sawActive, sawIdle := false, false
	for _, st := range statuses {
		t.Run(st, func(t *testing.T) {
			// Only the one task's status moves; the rest of the room stays.
			// paused carries its reason (task CHECK status↔paused_reason).
			f.exec(t, `UPDATE task SET status = $2::task_status,
				paused_reason = CASE WHEN $2 = 'paused' THEN 'director'::pause_reason END, paused_detail = NULL WHERE id = $1`, taskID, st)
			card := f.listed(t, f.api, f.sessionID)
			if card == nil {
				t.Fatal("room not listed")
			}
			got, ok := card["active_task_count"].(float64)
			if !ok {
				t.Fatalf("active_task_count missing: %v", card)
			}
			code, out, _ := f.api.do("POST", sp+"/archive", nil)
			switch code {
			case 409:
				if str(out, "code") != "tasks_active" {
					t.Fatalf("archive = 409 %v", out)
				}
				if n, _ := out["tasks_active"].(float64); n != got || n == 0 {
					t.Fatalf("status %s: archive tasks_active = %v, card active_task_count = %v", st, out["tasks_active"], got)
				}
				sawActive = true
			case 200:
				if got != 0 {
					t.Fatalf("status %s: archive allowed but the card says %v in flight", st, got)
				}
				sawIdle = true
				f.api.must(200, "POST", sp+"/unarchive", nil)
			default:
				t.Fatalf("archive = %d %v", code, out)
			}
		})
	}
	if !sawActive || !sawIdle {
		t.Fatalf("active=%v idle=%v — both sides of the 409 must be seen", sawActive, sawIdle)
	}
}

// TestRoomListVisibility: every card carries the room's visibility, and an
// owner·admin's audit listing keeps `invited` as it is — S5 counts 「워크스페이스에
// 공개된 방이 N개 더」 from it.
func TestRoomListVisibility(t *testing.T) {
	f := newRoomsFixture(t)
	open := str(f.mkRoom(t, f.member, "공개"), "id")
	closed := str(f.mkRoom(t, f.member, "초대만"), "id")
	f.member.must(200, "PATCH", f.roomPath(closed), map[string]any{"visibility": "invited"})

	for _, c := range []struct {
		who    string
		client *client
	}{{"방장", f.member}, {"ws owner 감사", f.api}, {"ws admin 감사", f.admin}} {
		t.Run(c.who, func(t *testing.T) {
			if v := str(f.listed(t, c.client, open), "visibility"); v != "workspace" {
				t.Fatalf("open room visibility = %q", v)
			}
			card := f.listed(t, c.client, closed)
			if card == nil || str(card, "visibility") != "invited" {
				t.Fatalf("invited room card = %v", card)
			}
		})
	}
	if f.listed(t, f.other, closed) != nil {
		t.Fatal("invited room listed to an uninvited member")
	}
	if v := str(f.listed(t, f.other, open), "visibility"); v != "workspace" {
		t.Fatalf("open room to a member = %q", v)
	}
}
