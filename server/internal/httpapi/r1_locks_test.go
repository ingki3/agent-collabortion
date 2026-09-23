package httpapi

// T-R1-locks: #294 review NN1~NN6 — the 1:N properties the review reproduced
// by hand but no test held (each injection it tried stayed green), plus the
// openapi 0.2.4 corrections (`work.deleted`, `completeWork.confirm`). Every
// room here is made by createRoom and holds two missions at once.

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// postIn posts a person's message into a room through the old messages path
// (the one the composer uses) and returns the created message and triggers.
func (f *roomsFixture) postIn(t *testing.T, rid string, body map[string]any) map[string]any {
	t.Helper()
	f.fake.Advance(time.Minute)
	return f.api.must(201, "POST", f.p+"/sessions/"+rid+"/messages", body, "Idempotency-Key", uuid.NewString())
}

func msgID(out map[string]any) string { return str(out["message"].(map[string]any), "id") }

// missionTask is a task for `agent` filed under mission `wid` (rule 1).
func (f *roomsFixture) missionTask(t *testing.T, rid, wid string, agent uuid.UUID, name string) uuid.UUID {
	t.Helper()
	out := f.postIn(t, rid, map[string]any{"content": router.MentionLink(name, agent) + " 부탁합니다", "work_id": wid})
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent.String() {
			id := mustUUID(t, str(tr, "task_id"))
			if n := f.count(t, `SELECT count(*) FROM task WHERE id = $1 AND work_id = $2`, id, wid); n != 1 {
				t.Fatalf("task %s is not filed under mission %s", id, wid)
			}
			return id
		}
	}
	t.Fatalf("no task for %s: %v", name, out["triggers"])
	return uuid.Nil
}

// overrun runs a task and reports `cost` for its turn, then enforces.
func (f *roomsFixture) overrun(t *testing.T, task uuid.UUID, cost float64) {
	t.Helper()
	f.runTask(t, task)
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), task, contracts.Usage{CostUSD: cost}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), task); err != nil {
		t.Fatal(err)
	}
}

func (f *roomsFixture) roomBlocked(t *testing.T, rid string) string {
	t.Helper()
	var reason *string
	if err := f.pool.QueryRow(t.Context(), `SELECT blocked_reason::text FROM room WHERE id = $1`, rid).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil {
		return ""
	}
	return *reason
}

// TestR1LocksMissionBudgetIsolation is NN1 (enforcement): missions A ($100)
// and B ($1) in one room, B's task spends $1.25 — B alone pauses for its own
// budget, A runs on, and the room's gate stays down. Reading any mission of
// the room but the task's own (the room's first, say) lets B's overrun pass
// silently: A was opened first so that is the one a room-keyed read meets.
func TestR1LocksMissionBudgetIsolation(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	a := str(f.openWork(t, f.api, rid, map[string]any{"goal": "넉넉한 미션", "limits": map[string]any{"budget_usd": 100}}), "id")
	b := str(f.openWork(t, f.api, rid, map[string]any{"goal": "빠듯한 미션", "limits": map[string]any{"budget_usd": 1}}), "id")
	f.overrun(t, f.missionTask(t, rid, b, f.rUUID, "R"), 1.25)

	if g := f.api.must(200, "GET", workPath(f, b), nil); str(g, "status") != "paused" || str(g, "paused_reason") != "budget" {
		t.Fatalf("mission B after $1.25 on its $1 = %v(%v), want paused(budget) — its OWN limit", g["status"], g["paused_reason"])
	}
	if g := f.api.must(200, "GET", workPath(f, a), nil); str(g, "status") != "active" {
		t.Fatalf("mission A = %v(%v), want active — B's overrun is not A's", g["status"], g["paused_reason"])
	}
	if reason := f.roomBlocked(t, rid); reason != "" {
		t.Fatalf("room gate = %q, want none — a mission's limit is not the room's", reason)
	}
	if n := f.count(t, `SELECT count(*) FROM hitl_request WHERE work_id = $1 AND purpose = 'budget' AND status = 'open'`, b); n != 1 {
		t.Fatalf("budget requests on B = %d, want 1 (its Director is asked)", n)
	}
}

// TestR1LocksBundleBudgetIsTheTasksMission is NN1 (bundle): each mission
// carries a DIFFERENT limit, so `limits.budget_usd` = min(미션 잔여, 방 잔여)
// shows whose mission it read — TestR1b1EffectiveBudgetIsTheMin gives every
// mission of its room the same limits and cannot tell.
func TestR1LocksBundleBudgetIsTheTasksMission(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	var rt uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE room SET runtime_id = $2 WHERE id = $1`, rid, rt)
	a := str(f.openWork(t, f.api, rid, map[string]any{"goal": "A", "limits": map[string]any{"budget_usd": 0.4}}), "id")
	b := str(f.openWork(t, f.api, rid, map[string]any{"goal": "B", "limits": map[string]any{"budget_usd": 0.7}}), "id")
	ta := f.missionTask(t, rid, a, f.rUUID, "R")
	tb := f.missionTask(t, rid, b, f.leadUUID, "Lead")

	bundles, err := f.srv.Queue.Claim(t.Context(), rt.String(), 10, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*float64{}
	for _, bd := range bundles {
		got[bd.Task.ID] = bd.Limits.BudgetUSD
	}
	for _, c := range []struct {
		task uuid.UUID
		want float64
		name string
	}{{ta, 0.4, "A"}, {tb, 0.7, "B"}} {
		v, ok := got[c.task.String()]
		if !ok {
			t.Fatalf("no bundle for mission %s's task (claimed %v)", c.name, got)
		}
		if v == nil {
			t.Fatalf("mission %s task limits.budget_usd = null, want %v", c.name, c.want)
		}
		if *v != c.want {
			t.Fatalf("mission %s task limits.budget_usd = %v, want %v — its own mission's limit", c.name, *v, c.want)
		}
	}
}

// TestR1LocksNewRoomHasNoLegacyMission is NN2: in a room made by createRoom
// with a mission open, a post WITHOUT the work_id key is honestly "no
// mission" — the old-client compatibility rule belongs to old-path rooms
// (room.legacy_work_id), and a new room must not read its first mission as one.
func TestR1LocksNewRoomHasNoLegacyMission(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	f.openWork(t, f.api, rid, map[string]any{"goal": "하나뿐인 미션"})

	pv := f.api.must(200, "POST", f.p+"/sessions/"+rid+"/messages/preview", map[string]any{"content": "안녕하세요"})
	if pv["work"] != nil || str(pv, "work_source") != "none" {
		t.Fatalf("preview without work_id in a createRoom room = %v / %v, want none", pv["work"], pv["work_source"])
	}
	out := f.postIn(t, rid, map[string]any{"content": "안녕하세요"})
	if n := f.count(t, `SELECT count(*) FROM message WHERE id = $1 AND work_id IS NULL`, msgID(out)); n != 1 {
		t.Fatal("a post without work_id in a createRoom room was filed under its mission")
	}
}

// TestR1LocksRoomMirrorProjectsNoReason is NN3: the room's budget parks every
// active mission as the room's (roomgate mirror, stored paused_reason
// budget). The Work response answers `paused` with `paused_reason: null` —
// the reason is the room's (Room.blocked_reason). TestR1b2Legacy… used
// `loop`, which projects to null on its own and so held nothing.
func TestR1LocksRoomMirrorProjectsNoReason(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.exec(t, `UPDATE room SET limits = limits || '{"budget_usd": 1}'::jsonb WHERE id = $1`, rid)
	a := str(f.openWork(t, f.api, rid, map[string]any{"goal": "A"}), "id")
	b := str(f.openWork(t, f.api, rid, map[string]any{"goal": "B"}), "id")
	f.overrun(t, f.missionTask(t, rid, a, f.rUUID, "R"), 1.25)

	if reason := f.roomBlocked(t, rid); reason != "budget" {
		t.Fatalf("room gate = %q, want budget", reason)
	}
	for _, id := range []string{a, b} {
		if n := f.count(t, `SELECT count(*) FROM work WHERE id = $1 AND status = 'paused' AND paused_reason = 'budget' AND (paused_detail->>'room_blocked')::bool`, id); n != 1 {
			t.Fatalf("mission %s is not parked as the room's (stored paused(budget) with the mirror mark)", id)
		}
		g := f.api.must(200, "GET", workPath(f, id), nil)
		if str(g, "status") != "paused" || g["paused_reason"] != nil {
			t.Fatalf("GET /works/%s = %v / %v, want paused with paused_reason null (the room's reason)", id, g["status"], g["paused_reason"])
		}
	}
	for _, it := range items(f.api.must(200, "GET", f.roomPath(rid)+"/works", nil)) {
		w := it.(map[string]any)
		if r, ok := w["paused_reason"]; ok && r != nil {
			t.Fatalf("listWorks %s paused_reason = %v, want null", str(w, "id"), r)
		}
	}
}

// TestR1LocksAdoptLeavesOtherMissionsReplies is NN4: 「이걸 미션으로」 on a
// thread takes what belongs to no mission, once — a reply (and the task it
// triggered) already filed under another mission stays there.
func TestR1LocksAdoptLeavesOtherMissionsReplies(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	w1 := str(f.openWork(t, f.api, rid, map[string]any{"goal": "먼저 연 미션"}), "id")
	root := msgID(f.postIn(t, rid, map[string]any{"content": "이 버그 어떻게 할까요"}))
	other := f.postIn(t, rid, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 이건 먼저 연 미션 일로", "parent_id": root, "work_id": w1})
	otherTask := str(other["triggers"].([]any)[0].(map[string]any), "task_id")
	plain := msgID(f.postIn(t, rid, map[string]any{"content": "재현 절차 첨부", "parent_id": root}))

	w2 := str(f.openWork(t, f.api, rid, map[string]any{"goal": "버그 고치기", "from_message_id": plain}), "id")
	if n := f.count(t, `SELECT count(*) FROM message WHERE id IN ($1, $2) AND work_id = $3`, root, plain, w2); n != 2 {
		t.Fatalf("thread messages with no mission adopted = %d of 2", n)
	}
	if n := f.count(t, `SELECT count(*) FROM message WHERE id = $1 AND work_id = $2`, msgID(other), w1); n != 1 {
		t.Fatal("a reply already in another mission was taken by the adopting one")
	}
	if n := f.count(t, `SELECT count(*) FROM task t JOIN lane l ON l.id = t.lane_id WHERE t.id = $1 AND t.work_id = $2 AND l.work_id = $2`, otherTask, w1); n != 1 {
		t.Fatal("the other mission's task (or its lane) moved to the adopting mission")
	}
}

// TestR1LocksAdoptDeepThread is NN5: a thread is a tree (message.parent_id).
// The router stores a reply to a reply against the root, but a deeper row —
// written by another path — is still in the thread: the mission adopts it
// from the top whether it is opened from the root or from the deepest reply.
func TestR1LocksAdoptDeepThread(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	var me uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT owner_user_id FROM room WHERE id = $1`, rid).Scan(&me); err != nil {
		t.Fatal(err)
	}
	thread := func(tag string) (root, reply, deep string) {
		root = msgID(f.postIn(t, rid, map[string]any{"content": tag + " 시작"}))
		reply = msgID(f.postIn(t, rid, map[string]any{"content": tag + " 답글", "parent_id": root}))
		if err := f.pool.QueryRow(t.Context(), `
			INSERT INTO message (session_id, author_type, author_id, parent_id, content, kind, state, created_at)
			VALUES ($1, 'user', $2, $3, $4, 'text', 'posted', $5) RETURNING id::text`,
			rid, me, reply, tag+" 답글의 답글", f.fake.Now()).Scan(&deep); err != nil {
			t.Fatal(err)
		}
		return root, reply, deep
	}
	for _, from := range []string{"deep", "root"} {
		root, reply, deep := thread(from)
		pick := map[string]string{"deep": deep, "root": root}[from]
		w := str(f.openWork(t, f.api, rid, map[string]any{"goal": "깊은 스레드 " + from, "from_message_id": pick}), "id")
		if n := f.count(t, `SELECT count(*) FROM message WHERE id IN ($1, $2, $3) AND work_id = $4`, root, reply, deep, w); n != 3 {
			t.Fatalf("opened from the %s: %d of the thread's 3 messages adopted", from, n)
		}
	}
}

// TestR1LocksRequestedDirectorWinsOverRoomDefault is NN6: FR-2A.1's order —
// the request's director_user_id, else the room default, else the opener —
// with the first two given at once.
func TestR1LocksRequestedDirectorWinsOverRoomDefault(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	f.api.must(200, "PATCH", f.roomPath(rid), map[string]any{"default_director_user_id": f.memberUserID})
	w := f.openWork(t, f.api, rid, map[string]any{"goal": "지정 Director", "director_user_id": f.otherUserID})
	if str(w, "director_user_id") != f.otherUserID {
		t.Fatalf("director = %s, want the requested %s over the room default %s", str(w, "director_user_id"), f.otherUserID, f.memberUserID)
	}
}

// TestR1LocksCompleteWorkConfirm is openapi 0.2.4 completeWork's body: with a
// sub-mission still queued/running the plain request is 409 running_lanes
// with the count, `confirm: false` is the same, and `confirm: true` ends it.
func TestR1LocksCompleteWorkConfirm(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	w := str(f.openWork(t, f.api, rid, map[string]any{"goal": "담당 있는 미션", "assignee_agent_id": f.r}), "id")
	for _, body := range []map[string]any{nil, {}, {"confirm": false}} {
		st, out, _ := f.api.do("POST", workPath(f, w)+"/complete", body)
		if st != 409 || str(out, "code") != "running_lanes" || out["running_lane_count"] != 1.0 {
			t.Fatalf("complete with %v = %d %v, want 409 running_lanes with running_lane_count 1", body, st, out)
		}
	}
	if g := f.api.must(200, "POST", workPath(f, w)+"/complete", map[string]any{"confirm": true}); str(g, "status") != "completed" {
		t.Fatalf("complete with confirm = %v", g["status"])
	}
}

// TestR1LocksDeleteWorkPublishes is openapi 0.2.4 `work.deleted {work_id,
// room_id}`: the chip row learns the mission is gone without a reload.
func TestR1LocksDeleteWorkPublishes(t *testing.T) {
	f := newRoomsFixture(t)
	rid := f.worksRoom(t)
	w := str(f.openWork(t, f.api, rid, map[string]any{"goal": "지울 미션"}), "id")
	keep := str(f.openWork(t, f.api, rid, map[string]any{"goal": "남을 미션"}), "id")
	f.api.must(200, "POST", workPath(f, w)+"/cancel", nil)
	f.api.must(204, "DELETE", workPath(f, w), nil)
	if n := f.count(t, `
		SELECT count(*) FROM stream_event WHERE type = 'work.deleted' AND session_id = $1
		   AND payload->>'work_id' = $2 AND payload->>'room_id' = $3`, rid, w, rid); n != 1 {
		t.Fatalf("work.deleted frames for the deleted mission = %d, want 1", n)
	}
	if n := f.count(t, `SELECT count(*) FROM stream_event WHERE type = 'work.deleted' AND payload->>'work_id' = $1`, keep); n != 0 {
		t.Fatal("work.deleted for a mission that was not deleted")
	}
}
