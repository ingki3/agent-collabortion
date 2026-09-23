package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/roomgate"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// T-R1b1 — the room gate (PRD v0.19 FR-2.4 · FR-2A.3 · FR-2.1.1 · FR-3.1.1 ·
// FR-3.5 · §3.1). The rows here are the room's; the old `/sessions/*` shape of
// the same events is held by the P2~P5 rows, unchanged.

func (f *p2Fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), q, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *p2Fixture) roomGate(t *testing.T) (string, map[string]any) {
	t.Helper()
	var reason *string
	var detail []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT blocked_reason::text, blocked_detail FROM room WHERE id = $1`, f.sessionID).
		Scan(&reason, &detail); err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	_ = json.Unmarshal(detail, &d)
	if reason == nil {
		return "", d
	}
	return *reason, d
}

func (f *p2Fixture) missionState(t *testing.T) (status, reason string, mirror bool) {
	t.Helper()
	var detail []byte
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, ''), paused_detail FROM work WHERE room_id = $1`, f.sessionID).
		Scan(&status, &reason, &detail); err != nil {
		t.Fatal(err)
	}
	return status, reason, roomgate.IsMirror(detail)
}

func (f *p2Fixture) workID(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM work WHERE room_id = $1`, f.sessionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// outsideMission detaches a task (and its lane) from every mission — a run
// FR-2A.1 allows ("일에 속하지 않은 실행"), which R1b1's legacy single-work
// rule never produces on its own.
func (f *p2Fixture) outsideMission(t *testing.T, taskID uuid.UUID) {
	t.Helper()
	f.exec(t, `UPDATE task SET work_id = NULL WHERE id = $1`, taskID)
	f.exec(t, `UPDATE lane SET work_id = NULL WHERE id = (SELECT lane_id FROM task WHERE id = $1)`, taskID)
}

// TestR1b1RoomBudgetStopsTheRoom is FR-2A.3 "방의 상한": the room's gate
// goes up (blocked_reason budget), every task of the room waits — in or out
// of a mission — the room owner is asked (approver_spec room_owner), and the
// approval with a raise brings the room back in one action (K-10).
func TestR1b1RoomBudgetStopsTheRoom(t *testing.T) {
	f := newP2Fixture(t)
	f.exec(t, `UPDATE room SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	parked := f.overrunSession(t, f.rUUID, "R", 1.25)

	reason, detail := f.roomGate(t)
	if reason != "budget" || detail["budget_usd"] != 1.0 || detail["cost_usd"] != 1.25 || detail["works_stopped"] != 1.0 {
		t.Fatalf("room gate = %q %v, want budget with budget_usd 1 · cost_usd 1.25 · works_stopped 1", reason, detail)
	}
	if st, r, mirror := f.missionState(t); st != "paused" || r != "budget" || !mirror {
		t.Fatalf("mission = %s(%s) mirror=%v, want paused(budget) marked as the room's — the old session shape", st, r, mirror)
	}
	var spec string
	var noTask, noWork bool
	if err := f.pool.QueryRow(t.Context(), `
		SELECT approver_spec, task_id IS NULL, work_id IS NULL FROM hitl_request
		WHERE session_id = $1 AND purpose = 'budget' AND status = 'open'`, f.sessionID).Scan(&spec, &noTask, &noWork); err != nil {
		t.Fatal(err)
	}
	if spec != "room_owner" || !noTask || !noWork {
		t.Fatalf("request = %s task_null=%v work_null=%v, want the room's own (room_owner, no task, no mission)", spec, noTask, noWork)
	}
	// The gate holds a run outside any mission too — that is what a ROOM
	// limit adds over a mission's.
	_, outside := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.outsideMission(t, outside)
	if got := f.claimed(t); has(got, outside) || has(got, parked) {
		t.Fatalf("claim under the room gate handed out %v — nothing of a blocked room dispatches", got)
	}

	hitlID := f.openSessionBudgetHitl(t)
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 5}, "Idempotency-Key", uuid.NewString())
	if reason, _ := f.roomGate(t); reason != "" {
		t.Fatalf("room gate = %q after the approval, want lifted", reason)
	}
	if st, _, _ := f.missionState(t); st != "active" {
		t.Fatalf("mission = %s after the approval, want active", st)
	}
	got := f.claimed(t)
	if !has(got, outside) || !has(got, parked) {
		t.Fatalf("claim after the approval = %v, want the parked task and the out-of-mission one", got)
	}
}

// TestR1b1MissionBudgetStopsOnlyTheMission is FR-2A.3 "일의 상한": a
// mission's own ceiling pauses THAT mission and asks its Director; the room
// stays open, so a run outside the mission still goes.
func TestR1b1MissionBudgetStopsOnlyTheMission(t *testing.T) {
	f := newP2Fixture(t)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.exec(t, `UPDATE work SET limits = '{"budget_usd": 1}'::jsonb WHERE room_id = $1`, f.sessionID)
	parked := f.overrunSession(t, f.rUUID, "R", 1.25)

	if reason, _ := f.roomGate(t); reason != "" {
		t.Fatalf("room gate = %q, want none — a mission's limit is not the room's", reason)
	}
	if st, r, mirror := f.missionState(t); st != "paused" || r != "budget" || mirror {
		t.Fatalf("mission = %s(%s) mirror=%v, want its OWN paused(budget)", st, r, mirror)
	}
	var spec string
	var work *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT approver_spec, work_id FROM hitl_request WHERE session_id = $1 AND purpose = 'budget' AND status = 'open'`,
		f.sessionID).Scan(&spec, &work); err != nil {
		t.Fatal(err)
	}
	if spec != "director" || work == nil || *work != f.workID(t) {
		t.Fatalf("request = %s work=%v, want the mission's Director on the mission", spec, work)
	}
	_, outside := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.outsideMission(t, outside)
	got := f.claimed(t)
	if !has(got, outside) || has(got, parked) {
		t.Fatalf("claim = %v, want the out-of-mission run and not the paused mission's task", got)
	}

	var hitlID string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'budget' AND status = 'open'`, f.sessionID).Scan(&hitlID); err != nil {
		t.Fatal(err)
	}
	f.api.must(422, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 1}, "Idempotency-Key", uuid.NewString())
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 3}, "Idempotency-Key", uuid.NewString())
	var limit float64
	if err := f.pool.QueryRow(t.Context(), `SELECT (limits->>'budget_usd')::float FROM work WHERE room_id = $1`, f.sessionID).Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if st, _, _ := f.missionState(t); st != "active" || limit != 3 {
		t.Fatalf("mission = %s limit %v after the approval, want active with the raise as ITS ceiling", st, limit)
	}
	var roomLimits []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT limits FROM room WHERE id = $1`, f.sessionID).Scan(&roomLimits); err != nil {
		t.Fatal(err)
	}
	if budgetOf(roomLimits) != 0 {
		t.Fatalf("room limits = %s — a mission raise must not become the room's budget", roomLimits)
	}
}

// TestR1b1EffectiveBudgetIsTheMin is FR-2A.3's last bullet at the bundle:
// `limits.budget_usd` = min(미션 잔여, 방 잔여) (daemon-protocol v0.9.0 §4.4).
func TestR1b1EffectiveBudgetIsTheMin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		room, work string
		want       float64
	}{
		{"mission tighter", `{"budget_usd": 2}`, `{"budget_usd": 0.5}`, 0.5},
		{"room tighter", `{"budget_usd": 0.3}`, `{"budget_usd": 0.5}`, 0.3},
		{"mission only", `{}`, `{"budget_usd": 0.7}`, 0.7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newP2Fixture(t)
			f.exec(t, `UPDATE room SET limits = $2::jsonb WHERE id = $1`, f.sessionID, tc.room)
			f.exec(t, `UPDATE work SET limits = $2::jsonb WHERE room_id = $1`, f.sessionID, tc.work)
			_, task := f.agentToken(t, f.sessionID, f.rUUID, "R")
			got := f.claimLimit(t, task)
			if got == nil || *got != tc.want {
				t.Fatalf("limits.budget_usd = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestR1b1BlockRoomManual is FR-2.4 `manual` (openapi blockRoom ·
// unblockRoom): the managers put it up, running turns end like 「중단」, new
// runs wait, the timeline and activity_log say so, and the same people take it
// down — HITL-lifted reasons are not theirs to lift (409 not_manual).
func TestR1b1BlockRoomManual(t *testing.T) {
	f := newP2Fixture(t)
	member := f.addMember(t, "m@example.com", "M")
	_, running := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, running)
	_, waiting := f.agentToken(t, f.sessionID, f.wUUID, "W")
	room := f.p + "/rooms/" + f.sessionID

	if st, body, _ := member.do("POST", room+"/block", nil); st != 403 || str(body, "code") != "not_room_manager" {
		t.Fatalf("member block = %d %v, want 403 not_room_manager", st, body)
	}
	out := f.api.must(200, "POST", room+"/block", nil)
	if str(out, "blocked_reason") != "manual" {
		t.Fatalf("block = %v, want blocked_reason manual", out)
	}
	d, _ := out["blocked_detail"].(map[string]any)
	by, _ := d["blocked_by_user"].(map[string]any)
	if str(by, "display_name") != "Dir" || d["works_stopped"] != 1.0 {
		t.Fatalf("blocked_detail = %v, want blocked_by_user Dir and works_stopped 1", d)
	}
	var cancels int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM daemon_command WHERE type = 'cancel' AND payload->>'task_id' = $1`, running.String()).Scan(&cancels); err != nil {
		t.Fatal(err)
	}
	if cancels != 1 {
		t.Fatalf("cancel commands for the running turn = %d, want 1 — 「중단」 through the daemon (§8.2.2)", cancels)
	}
	if got := f.claimed(t); has(got, waiting) {
		t.Fatal("a manually stopped room dispatched its waiting task")
	}
	var notices, audit int
	_ = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '%이 방을 멈췄습니다%'`, f.sessionID).Scan(&notices)
	_ = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM activity_log WHERE session_id = $1 AND action = 'room.blocked'`, f.sessionID).Scan(&audit)
	if notices != 1 || audit != 1 {
		t.Fatalf("timeline notices/activity lines = %d/%d, want 1/1 (FR-2.2)", notices, audit)
	}
	if st, body, _ := f.api.do("POST", room+"/block", nil); st != 409 || str(body, "code") != "already_blocked" {
		t.Fatalf("second block = %d %v, want 409 already_blocked", st, body)
	}
	if st, _, _ := member.do("POST", room+"/unblock", nil); st != 403 {
		t.Fatalf("member unblock = %d, want 403", st)
	}
	out = f.api.must(200, "POST", room+"/unblock", nil)
	if out["blocked_reason"] != nil {
		t.Fatalf("unblock = %v, want blocked_reason null", out)
	}
	if !has(f.claimed(t), waiting) {
		t.Fatal("the waiting task did not go once the room was unblocked")
	}
	if st, body, _ := f.api.do("POST", room+"/unblock", nil); st != 409 || str(body, "code") != "not_blocked" {
		t.Fatalf("unblock of an open room = %d %v, want 409 not_blocked", st, body)
	}
	f.exec(t, `UPDATE room SET blocked_reason = 'loop' WHERE id = $1`, f.sessionID)
	if st, body, _ := f.api.do("POST", room+"/unblock", nil); st != 409 || str(body, "code") != "not_manual" {
		t.Fatalf("unblock of a loop stop = %d %v, want 409 not_manual — that one is the approval's", st, body)
	}
}

// TestR1b1RoomOwnerAbsenceHandOver is FR-2A.3 (SCR-A G-10): a room_owner
// request is answerable by the owner at once, by the room's deputy from half
// the deadline — and with no deputy by the workspace's oldest other owner —
// and never by a plain member.
func TestR1b1RoomOwnerAbsenceHandOver(t *testing.T) {
	f := newP2Fixture(t)
	owner2 := f.addMember(t, "o2@example.com", "O2")
	f.exec(t, `UPDATE member SET role = 'owner' WHERE user_id = $1`, owner2.userID)
	member := f.addMember(t, "m@example.com", "M")
	ask := func() string {
		t.Helper()
		var id string
		if err := f.pool.QueryRow(t.Context(), `
			INSERT INTO hitl_request (session_id, source, type, question, approver_spec, purpose, due_at, created_at)
			VALUES ($1, 'system', 'approval', '계속할까요?', 'room_owner', 'loop', $2, $3) RETURNING id::text`,
			f.sessionID, f.fake.Now().Add(24*time.Hour), f.fake.Now()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	answer := func(c *client, id string) (int, map[string]any) {
		st, body, _ := c.do("POST", f.p+"/hitl-requests/"+id+"/response",
			map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
		return st, body
	}

	// No deputy: the oldest other workspace owner is the delegate.
	id := ask()
	f.fake.Advance(time.Hour)
	st, body := answer(owner2.client, id)
	if st != 403 || str(body, "code") != "deputy_not_yet" || !strings.Contains(str(body, "detail"), "방장 응답 대기 중") || body["can_respond_from"] == nil {
		t.Fatalf("owner2 at +1h = %d %v, want 403 deputy_not_yet naming the room owner with the instant", st, body)
	}
	if st, body := answer(member.client, id); st != 403 || body["can_respond_from"] != nil {
		t.Fatalf("member = %d %v, want 403 with no instant — a member never becomes eligible", st, body)
	}
	got := owner2.must(200, "GET", f.p+"/hitl-requests/"+id, nil)
	if got["can_respond"] != false || got["can_respond_from"] == nil {
		t.Fatalf("owner2's card = %v, want can_respond false with can_respond_from", got)
	}
	f.fake.Advance(12 * time.Hour)
	if st, body := answer(owner2.client, id); st != 200 {
		t.Fatalf("owner2 past half = %d %v, want 200", st, body)
	}

	// With a deputy, the deputy is the delegate (and owner2 no longer is).
	deputy := f.addMember(t, "dep@example.com", "Dep")
	f.exec(t, `UPDATE room SET deputy_owner_user_id = $2 WHERE id = $1`, f.sessionID, deputy.userID)
	id = ask()
	f.fake.Advance(13 * time.Hour)
	if st, body := answer(owner2.client, id); st != 403 {
		t.Fatalf("owner2 with a deputy in place = %d %v, want 403", st, body)
	}
	if st, body := answer(deputy.client, id); st != 200 {
		t.Fatalf("deputy past half = %d %v, want 200", st, body)
	}
	// The owner never waits.
	id = ask()
	if st, body := answer(f.api, id); st != 200 {
		t.Fatalf("room owner = %d %v, want 200 at once", st, body)
	}
}

// TestR1b1QueuedReasonAgentGlobal is §3.1 `queued_reason` with §12.1-5: an
// agent's max_concurrent_tasks spans rooms, and the task it holds back says
// so — on the task and on its lane.
func TestR1b1QueuedReasonAgentGlobal(t *testing.T) {
	f := newP2Fixture(t)
	f.exec(t, `UPDATE agent SET max_concurrent_tasks = 1 WHERE id = $1`, f.rUUID)
	// A second room whose assignee is R — its start task is R's too.
	other := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/sessions", map[string]any{
		"title": "S2", "goal": "g2", "isolation": map[string]any{"kind": "none"},
		"assignee_agent_id": f.r, "participants": []map[string]any{{"agent_id": f.r}},
	})
	// R's start task in the other room goes out FIRST, on its own claim, so
	// the cap is held by a task that is already running — not by a rival in
	// the same claim (which the claim's own ranking would hold back anyway).
	if len(f.claimed(t)) == 0 {
		t.Fatal("premise: the other room's start task did not go out")
	}
	_, here := f.agentToken(t, f.sessionID, f.rUUID, "R")
	got := f.claimed(t)
	if has(got, here) {
		t.Fatalf("claim = %v — R is already running in the other room and its cap is 1", got)
	}
	var reason *string
	if err := f.pool.QueryRow(t.Context(), `SELECT queued_reason::text FROM task WHERE id = $1`, here).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil || *reason != "agent_global" {
		t.Fatalf("queued_reason = %v, want agent_global (다른 방에서 작업 중)", reason)
	}
	task := f.api.must(200, "GET", f.p+"/tasks/"+here.String(), nil)
	if str(task, "queued_reason") != "agent_global" {
		t.Fatalf("GET task queued_reason = %v, want agent_global", task["queued_reason"])
	}
	var laneID string
	_ = f.pool.QueryRow(t.Context(), `SELECT lane_id::text FROM task WHERE id = $1`, here).Scan(&laneID)
	found := false
	for _, raw := range f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/lanes", nil) {
		l := raw.(map[string]any)
		if str(l, "id") == laneID {
			found = true
			if str(l, "queued_reason") != "agent_global" {
				t.Fatalf("listLanes queued_reason = %v, want agent_global", l["queued_reason"])
			}
		}
	}
	if !found {
		t.Fatalf("listLanes has no lane %s", laneID)
	}
	_ = other

	// The room's own lane cap names itself.
	f.exec(t, `UPDATE agent SET max_concurrent_tasks = 5 WHERE id = $1`, f.rUUID)
	f.exec(t, `UPDATE room SET limits = limits || '{"max_parallel_lanes": 1}'::jsonb WHERE id = $1`, f.sessionID)
	f.exec(t, `UPDATE task SET status = 'running' WHERE session_id = $1 AND status = 'dispatched'`, f.sessionID)
	_, w := f.agentToken(t, f.sessionID, f.wUUID, "W")
	_ = f.claimed(t)
	if err := f.pool.QueryRow(t.Context(), `SELECT queued_reason::text FROM task WHERE id = $1`, w).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil || *reason != "room_lanes" {
		t.Fatalf("queued_reason = %v, want room_lanes", reason)
	}
}

// TestR1b1IsolationConfirm is FR-2.1.1: the first run of an `isolation: none`
// room on a computer with a repository waits for the room owner — nothing
// dispatched, nothing pinned — and the answer decides worktree (approve) or
// none (reject), pins the computer, and says so on the timeline.
func TestR1b1IsolationConfirm(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(map[bool]string{true: "approve→worktree", false: "reject→none"}[approve], func(t *testing.T) {
			f := newP2Fixture(t)
			f.exec(t, `UPDATE runtime SET repos = '[{"path": "/Users/x/repo", "remote_url": "", "branch": "main", "clean": true}]'::jsonb WHERE workspace_id = $1`, f.wsID)
			if got := f.claimed(t); len(got) != 0 {
				t.Fatalf("first claim = %v, want nothing — the first dispatch waits for the answer", got)
			}
			_ = f.claimed(t) // a second poll must not ask twice
			var pinned *uuid.UUID
			var pending []byte
			if err := f.pool.QueryRow(t.Context(), `SELECT runtime_id, isolation_pending FROM room WHERE id = $1`, f.sessionID).Scan(&pinned, &pending); err != nil {
				t.Fatal(err)
			}
			if pinned != nil || len(pending) == 0 {
				t.Fatalf("room runtime=%v pending=%s, want unpinned with the question pending", pinned, pending)
			}
			var id, spec string
			var n, inbox int
			if err := f.pool.QueryRow(t.Context(), `
				SELECT id::text, approver_spec, count(*) OVER () FROM hitl_request WHERE session_id = $1 AND purpose = 'isolation'`, f.sessionID).
				Scan(&id, &spec, &n); err != nil {
				t.Fatal(err)
			}
			_ = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM inbox_item WHERE ref_id = $1 AND type = 'isolation_confirm'`, id).Scan(&inbox)
			if spec != "room_owner" || n != 1 || inbox != 1 {
				t.Fatalf("request spec=%s count=%d inbox=%d, want one room_owner request and one isolation_confirm card", spec, n, inbox)
			}
			body := map[string]any{"approved": approve}
			if !approve {
				body["reason"] = "같은 폴더로 충분합니다"
			}
			f.api.must(200, "POST", f.p+"/hitl-requests/"+id+"/response", body, "Idempotency-Key", uuid.NewString())
			var iso []byte
			if err := f.pool.QueryRow(t.Context(), `SELECT runtime_id, isolation_pending, isolation FROM room WHERE id = $1`, f.sessionID).Scan(&pinned, &pending, &iso); err != nil {
				t.Fatal(err)
			}
			want := `"kind": "none"`
			if approve {
				want = `"kind": "worktree"`
			}
			if pinned == nil || len(pending) != 0 || !strings.Contains(string(iso), want) {
				t.Fatalf("after the answer: runtime=%v pending=%s isolation=%s, want pinned, cleared, %s", pinned, pending, iso, want)
			}
			if approve && !strings.Contains(string(iso), "/Users/x/repo") {
				t.Fatalf("isolation = %s, want the repository that asked", iso)
			}
			var notice int
			_ = f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'system' AND content LIKE '이 방은 mac-1에서 돕니다%'`, f.sessionID).Scan(&notice)
			if notice != 1 {
				t.Fatalf("「이 방은 mac-1에서 돕니다」 notices = %d, want 1", notice)
			}
			if got := f.claimed(t); len(got) == 0 {
				t.Fatal("the first run did not go after the answer")
			}
		})
	}
}

// TestR1b1MessageAttribution is FR-3.1.1's four rules and the preview chip,
// in a room with two missions so rule 4 and the legacy session rule can be
// told apart (T-R1b2: the rule holds for an old-path room — this fixture's —
// when `work_id` is absent; `work_id: null` is the chip's 「미션 없음」).
func TestR1b1MessageAttribution(t *testing.T) {
	f := newP2Fixture(t)
	w1 := f.workID(t)
	// Legacy single-work room: no rule applies, the room's only mission does.
	pv := f.preview(t, map[string]any{"content": "안녕하세요"})
	if wk, _ := pv["work"].(map[string]any); str(wk, "id") != w1.String() || str(pv, "work_source") != "chosen" {
		t.Fatalf("preview in a one-mission room = %v / %v, want the room's mission (legacy single-work rule, chosen)", pv["work"], pv["work_source"])
	}

	var w2 uuid.UUID
	var dir uuid.UUID
	_ = f.pool.QueryRow(t.Context(), `SELECT director_user_id FROM work WHERE id = $1`, w1).Scan(&dir)
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO work (room_id, title, goal, director_user_id, status, created_by, started_at)
		VALUES ($1, '보고서 초안', 'g', $2, 'active', $2, now()) RETURNING id`, f.sessionID, dir).Scan(&w2); err != nil {
		t.Fatal(err)
	}
	msgWork := func(out map[string]any) *uuid.UUID {
		t.Helper()
		m, _ := out["message"].(map[string]any)
		var w *uuid.UUID
		_ = f.pool.QueryRow(t.Context(), `SELECT work_id FROM message WHERE id = $1`, str(m, "id")).Scan(&w)
		return w
	}
	// The old client (no work_id key) in the old-path room still lands on
	// its session's mission with a second mission open (T-R1b2).
	if w := msgWork(f.post(t, map[string]any{"content": "옛 화면"})); w == nil || *w != w1 {
		t.Fatalf("old client message work = %v, want the session's mission %s", w, w1)
	}
	// 4. two missions, 「미션 없음」 on the chip, no thread, nobody running: none.
	pv = f.preview(t, map[string]any{"content": "잡담", "work_id": nil})
	if pv["work"] != nil || str(pv, "work_source") != "none" {
		t.Fatalf("preview = %v / %v, want no mission (rule 4)", pv["work"], pv["work_source"])
	}
	if w := msgWork(f.post(t, map[string]any{"content": "잡담", "work_id": nil})); w != nil {
		t.Fatalf("message work = %v, want none", w)
	}
	// 1. chosen — and the lane and task it makes carry it.
	mention := router.MentionLink("R", f.rUUID) + " 초안 부탁"
	out := f.post(t, map[string]any{"content": mention, "work_id": w2.String()})
	if w := msgWork(out); w == nil || *w != w2 {
		t.Fatalf("chosen message work = %v, want %s", w, w2)
	}
	tr := out["triggers"].([]any)[0].(map[string]any)
	var taskWork, laneWork *uuid.UUID
	_ = f.pool.QueryRow(t.Context(), `SELECT t.work_id, l.work_id FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1`, str(tr, "task_id")).Scan(&taskWork, &laneWork)
	if taskWork == nil || *taskWork != w2 || laneWork == nil || *laneWork != w2 {
		t.Fatalf("task/lane work = %v/%v, want %s — the run belongs to the mission", taskWork, laneWork, w2)
	}
	// 2. a reply in that thread.
	root := str(out["message"].(map[string]any), "id")
	pv = f.preview(t, map[string]any{"content": "추가로", "parent_id": root})
	if wk, _ := pv["work"].(map[string]any); str(wk, "id") != w2.String() || str(pv, "work_source") != "thread" {
		t.Fatalf("thread preview = %v / %v, want %s thread", pv["work"], pv["work_source"], w2)
	}
	// 3. mentioning an agent RUNNING a lane of that mission.
	f.exec(t, `UPDATE lane SET status = 'running' WHERE id = $1`, str(tr, "lane_id"))
	pv = f.preview(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 진행 상황?"})
	if wk, _ := pv["work"].(map[string]any); str(wk, "id") != w2.String() || str(wk, "title") != "보고서 초안" || str(pv, "work_source") != "running_lane" {
		t.Fatalf("running-lane preview = %v / %v, want 보고서 초안 running_lane", pv["work"], pv["work_source"])
	}
	// A closed mission cannot be chosen; a foreign one is not this room's.
	f.exec(t, `UPDATE work SET status = 'completed' WHERE id = $1`, w1)
	if st, body, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/messages", map[string]any{"content": "x", "work_id": w1.String()},
		"Idempotency-Key", uuid.NewString()); st != 422 || !hasFieldError(body, "work_id", "closed") {
		t.Fatalf("closed mission = %d %v, want 422 work_id closed", st, body)
	}
	if st, body, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/messages", map[string]any{"content": "x", "work_id": uuid.NewString()},
		"Idempotency-Key", uuid.NewString()); st != 422 || !hasFieldError(body, "work_id", "not_in_room") {
		t.Fatalf("foreign mission = %d %v, want 422 work_id not_in_room", st, body)
	}
}

// TestR1b1BudgetReadsTheTasksOwnMission is V19_R1B_HANDOFF 우선 처리
// budget.go:65: the budget state joined "the room's mission", so in a room
// with two missions it read an arbitrary one — a paused sibling made the
// overrun look "already stopped" and the turn ran on past the room's budget
// (the old code returned early on a non-active status). The task's own
// mission decides; the room's spend is counted once.
func TestR1b1BudgetReadsTheTasksOwnMission(t *testing.T) {
	f := newP2Fixture(t)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.exec(t, `UPDATE room SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID)
	var dir uuid.UUID
	_ = f.pool.QueryRow(t.Context(), `SELECT director_user_id FROM work WHERE room_id = $1`, f.sessionID).Scan(&dir)
	f.exec(t, `INSERT INTO work (room_id, title, goal, director_user_id, status, paused_reason, created_by)
		VALUES ($1, '옆 미션', 'g', $2, 'paused', 'director', $2)`, f.sessionID, dir)
	// Touch the room's first mission so its live tuple lies AFTER the sibling
	// in the heap: an unordered join then meets the paused sibling first —
	// the order that made the old budget read silently skip enforcement.
	f.exec(t, `UPDATE work SET updated_at = now() WHERE room_id = $1 AND title <> '옆 미션'`, f.sessionID)
	f.overrunSession(t, f.rUUID, "R", 1.25)
	if reason, _ := f.roomGate(t); reason != "budget" {
		t.Fatalf("room gate = %q, want budget — a paused sibling mission must not hide the room's overrun", reason)
	}
	var spent float64
	var n int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT (blocked_detail->>'cost_usd')::float, (SELECT count(*) FROM work WHERE room_id = $1 AND paused_reason = 'director')
		FROM room WHERE id = $1`, f.sessionID).Scan(&spent, &n); err != nil {
		t.Fatal(err)
	}
	if spent != 1.25 || n != 1 {
		t.Fatalf("cost %v (want 1.25, counted once) · sibling still paused(director) %d (want 1)", spent, n)
	}
}
