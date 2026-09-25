package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// T-APPROVAL (Director 2026-09-25, 「게임 제작 방」): the assignee submitted
// v1 of its artifact and said it keeps integrating; the platform asked the
// Director "종료 조건이 모두 충족되었습니다. 승인하시겠습니까?" at that very
// instant, while the turn was still running. And S7 drew that request as a
// plain system line — the card arrived as `message.created`, the request
// itself never did (`hitl.created` was published by two paths of seven).
//
// Two rules measured here:
//   1. user_approval is HELD while the mission has a queued · dispatched ·
//      preparing · running task, and issued when the last one ends — by a
//      finish, a cancel or a failure. The hold shows on the progress row
//      (held_reason: running_tasks, openapi v0.3.3).
//   2. every path that opens a HITL publishes `hitl.created` with the card's
//      message_id in it, so S7 can pair the card with its request.

// settleWork ends every live task of a mission in place (no hook runs) — for
// the rows that are about something after the hold.
func (f *p2Fixture) settleWork(t *testing.T, workID string) {
	t.Helper()
	f.exec(t, `UPDATE task SET status = 'completed', finished_at = now()
		WHERE work_id = $1 AND status IN ('queued', 'dispatched', 'preparing', 'running')`, workID)
}

// hitlFrames drains the subscription and returns the hitl.* frames by type.
func hitlFrames(sub *realtime.Subscription) map[string][]map[string]any {
	out := map[string][]map[string]any{}
	for {
		select {
		case e := <-sub.C:
			if e.Type != "hitl.created" && e.Type != "hitl.updated" {
				continue
			}
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			out[e.Type] = append(out[e.Type], p)
		default:
			return out
		}
	}
}

// assertCreatedFrame is rule 2: exactly one `hitl.created` for a request of
// this purpose, carrying the id of the timeline card (openapi
// HitlRequest.message_id) — the pair S7 needs to draw buttons.
func assertCreatedFrame(t *testing.T, frames map[string][]map[string]any, purpose string) {
	t.Helper()
	var got []map[string]any
	for _, p := range frames["hitl.created"] {
		if str(p, "purpose") == purpose {
			got = append(got, p)
		}
	}
	if len(got) != 1 {
		t.Fatalf("%s: hitl.created frames = %d (%v), want 1 — without it S7 draws the card as a plain line", purpose, len(got), frames)
	}
	if str(got[0], "message_id") == "" {
		t.Fatalf("%s: hitl.created payload has no message_id — S7 cannot pair it with its card: %v", purpose, got[0])
	}
}

// userApprovalRow is getWork's user_approval progress row.
func (f *p2Fixture) userApprovalRow(t *testing.T, sess string) map[string]any {
	t.Helper()
	by, _ := f.conds(t, sess)
	c := by["user_approval"]
	if c == nil {
		t.Fatalf("no user_approval row in %v", by)
	}
	return c
}

func TestTApprovalHeldWhileWorkRunsThenIssuedOnFinish(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("user_approval")))
	leadTok, leadTask := f.agentToken(t, sess, f.leadUUID, "Lead")
	f.runTask(t, leadTask)
	sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
	defer sub.Close()

	// v1 submitted mid-turn: the tree has only user_approval left.
	if st, out := f.submit(t, sess, leadTok, "game.html", "doc", []byte("v1")); st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	if n := f.openHitls(t, sess); n != 0 {
		t.Fatalf("open user_approval while Lead's turn runs = %d, want 0 — the Director must not be asked to close moving work", n)
	}
	if n := len(f.hitlCards(t)); n != 0 {
		t.Fatalf("hitl cards while held = %d, want 0", n)
	}
	row := f.userApprovalRow(t, sess)
	if str(row, "held_reason") != sessions.HeldRunningTasks || row["met"] != false {
		t.Fatalf("user_approval row while held = %v, want held_reason running_tasks (openapi v0.3.3)", row)
	}
	if str(row, "next_actor") != "director" {
		t.Fatalf("next_actor = %v, want director — held is not somebody else's turn", row["next_actor"])
	}
	if fr := hitlFrames(sub); len(fr["hitl.created"]) != 0 {
		t.Fatalf("hitl.created while held: %v", fr)
	}

	// A second submission mid-turn does not open it either.
	if st, out := f.submit(t, sess, leadTok, "game.html", "doc", []byte("v2")); st != 201 {
		t.Fatalf("submit v2 = %d %v", st, out)
	}
	if n := f.openHitls(t, sess); n != 0 {
		t.Fatalf("open user_approval after v2 mid-turn = %d, want 0", n)
	}

	// The turn ends: the request opens, once, with its frame.
	f.endTurn(t, leadTask)
	if n := f.openHitls(t, sess); n != 1 {
		t.Fatalf("open user_approval after the last turn ended = %d, want 1", n)
	}
	assertCreatedFrame(t, hitlFrames(sub), sessions.CondUserApproval)
	// The card names its request (openapi Message.hitl_request_id) — the
	// other half of the pairing, for a screen that missed the frame.
	var reqID string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'user_approval'`, sess).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	page := f.api.must(200, "GET", f.p+"/rooms/"+sess+"/messages", nil)
	paired := false
	for _, raw := range page["items"].([]any) {
		m := raw.(map[string]any)
		if str(m, "kind") == "hitl" && str(m, "hitl_request_id") == reqID {
			paired = true
		}
	}
	if !paired {
		t.Fatalf("no hitl message with hitl_request_id %s in %v", reqID, page["items"])
	}
	row = f.userApprovalRow(t, sess)
	if row["held_reason"] != nil || str(row, "next_actor") != "director" {
		t.Fatalf("user_approval row after release = %v, want held_reason null", row)
	}
	// A repeated finish (the daemon re-sends) does not ask twice.
	if _, err := f.srv.Tasks.Finish(t.Context(), leadTask, currentAttempt(t, f, leadTask), contracts.Finish{Outcome: "completed", StopReason: "end_turn"}); err != nil {
		t.Fatal(err)
	}
	if n := f.openHitls(t, sess); n != 1 {
		t.Fatalf("open user_approval after a repeated finish = %d, want still 1", n)
	}
}

// A queued task holds too, and a cancel is an ending like a finish.
func TestTApprovalHeldByQueuedTaskReleasedByCancel(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("user_approval")))
	leadTok, leadTask := f.agentToken(t, sess, f.leadUUID, "Lead")
	f.runTask(t, leadTask)
	// W is asked for something and has not started (queued).
	_, wTask := f.agentToken(t, sess, f.wUUID, "W")
	if st, out := f.submit(t, sess, leadTok, "a.md", "doc", []byte("a")); st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	// Lead's turn ends; W is still queued — still held.
	f.endTurn(t, leadTask)
	f.exec(t, `UPDATE task SET status = 'completed', finished_at = now()
		WHERE work_id = $1 AND status = 'queued' AND id <> $2`, f.missionOf(t, sess), wTask)
	if n := f.openHitls(t, sess); n != 0 {
		t.Fatalf("open user_approval with W queued = %d, want 0", n)
	}
	sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
	defer sub.Close()
	laneID, _ := f.laneOf(t, wTask)
	if _, _, err := f.srv.Tasks.CancelLane(t.Context(), laneID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if n := f.openHitls(t, sess); n != 1 {
		t.Fatalf("open user_approval after the queued task was cancelled = %d, want 1", n)
	}
	assertCreatedFrame(t, hitlFrames(sub), sessions.CondUserApproval)
}

// E6-04 still holds: a rejection re-asks nothing, even when a turn ends
// after it — only a HELD request is released.
func TestTApprovalRejectionIsNotReleasedAgain(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": false, "reason": "3장이 비었습니다"}, "Idempotency-Key", uuid.NewString())
	_, task := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	f.endTurn(t, task)
	if n := f.openHitls(t, f.sessionID); n != 0 {
		t.Fatalf("open user_approval after a rejection and a finished turn = %d, want 0 (E6-04)", n)
	}
}

// The scheduler's catch-all releases a hold whose work ended inside another
// transaction (a room block cancels turns without the task layer's hook).
func TestTApprovalSweepReleasesSettledHold(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("user_approval")))
	leadTok, leadTask := f.agentToken(t, sess, f.leadUUID, "Lead")
	f.runTask(t, leadTask)
	if st, out := f.submit(t, sess, leadTok, "a.md", "doc", []byte("a")); st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	f.exec(t, `UPDATE task SET status = 'cancelled', finished_at = now() WHERE id = $1`, leadTask)
	if n := f.openHitls(t, sess); n != 0 {
		t.Fatalf("open before the sweep = %d", n)
	}
	if n, err := f.srv.Sessions.ReleaseHeldApprovals(t.Context()); err != nil || n != 1 {
		t.Fatalf("ReleaseHeldApprovals = %d %v, want 1", n, err)
	}
	if n := f.openHitls(t, sess); n != 1 {
		t.Fatalf("open after the sweep = %d, want 1", n)
	}
	if n, _ := f.srv.Sessions.ReleaseHeldApprovals(t.Context()); n != 0 {
		t.Fatalf("second sweep opened %d more", n)
	}
}

// Rule 2, per path: budget pause, loop stop, isolation question and the
// mission time limit (the completion approval is measured above; the agent's
// own request already published).
func TestTApprovalEverySystemHitlPublishesCreated(t *testing.T) {
	t.Run("budget", func(t *testing.T) {
		f := newP2Fixture(t)
		if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
			t.Fatal(err)
		}
		_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
		sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
		defer sub.Close()
		f.finishTurn(t, taskID, contracts.Finish{
			Outcome: "completed", StopReason: "end_turn",
			Usage: contracts.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 1.01},
		})
		assertCreatedFrame(t, hitlFrames(sub), "budget")
	})
	t.Run("loop", func(t *testing.T) {
		f := newP2Fixture(t)
		ctx := t.Context()
		f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
			"loop_limits": map[string]any{"max_pair_roundtrips": 1},
		})
		for i := 0; i < 3; i++ {
			for _, pair := range [][2]string{{f.lead, f.r}, {f.r, f.lead}} {
				if _, err := f.pool.Exec(ctx, `
					INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, created_at)
					VALUES ($1, $2, $3, 2, $4)`, f.sessionID, pair[0], pair[1], t0); err != nil {
					t.Fatal(err)
				}
			}
		}
		sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
		defer sub.Close()
		if _, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID), router.Author{Type: "agent", AgentID: &f.rUUID}, gen.MessageCreate{
			Content: router.MentionLink("Lead", f.leadUUID) + " 또",
		}); err != nil {
			t.Fatal(err)
		}
		assertCreatedFrame(t, hitlFrames(sub), "loop")
	})
	t.Run("isolation", func(t *testing.T) {
		f := newP2Fixture(t)
		f.exec(t, `UPDATE runtime SET repos = '[{"path": "/Users/x/repo", "remote_url": "", "branch": "main", "clean": true}]'::jsonb WHERE workspace_id = $1`, f.wsID)
		sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
		defer sub.Close()
		_ = f.claimed(t)
		assertCreatedFrame(t, hitlFrames(sub), "isolation")
	})
	t.Run("time", func(t *testing.T) {
		f := newRoomsFixture(t)
		rid := f.worksRoom(t)
		f.openWork(t, f.api, rid, map[string]any{"goal": "짧은 일", "limits": map[string]any{"time_limit": "PT1H"}})
		sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
		defer sub.Close()
		f.fake.Advance(61 * time.Minute)
		if n, err := f.srv.SweepWorkTimeLimits(t.Context()); err != nil || n != 1 {
			t.Fatalf("sweep = %d %v", n, err)
		}
		assertCreatedFrame(t, hitlFrames(sub), "time")
	})
}

// hitl.updated reaches the other open screens when the completion approval
// is answered (the card turns into 「승인 — 누가·언제」 live).
func TestTApprovalAnswerPublishesUpdated(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)
	sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
	defer sub.Close()
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	fr := hitlFrames(sub)
	found := false
	for _, p := range fr["hitl.updated"] {
		if str(p, "id") == hitl && str(p, "status") == "answered" && str(p, "answered_by") != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("hitl.updated frames = %v, want the answered completion approval with answered_by", fr["hitl.updated"])
	}
}

// ── T-APPROVAL E·F (Lead 추가 범위, 실사용 결함 2건) ─────────────────────────
//
// E: 아티팩트가 제출될 때마다 새 승인 요청이 열리고 옛 것은 닫히지 않았다 —
//    13:41 · 13:45 · 14:04 · 14:08 · 14:21 · 14:30 여섯 건, 그중 다섯이 동시에 open.
// F: 조건은 「아티팩트 제출(who=assignee → Lead)」인데 **Writer** 의 제출(13:45:38 ·
//    14:08:59)에도 요청이 열렸다.
//
// 원인은 하나다: `ApplyEvent` 의 바닥이 「user_approval 만 남았는가」만 물었다. 그 답은
// 한 번 참이 되면 사람이 답할 때까지 참이라, 그 뒤 도착하는 **모든** 이벤트가 — 조건을
// 하나도 움직이지 않은 이벤트까지 — 요청을 다시 발행했다. 고친 뒤에는 트리를 실제로
// 움직인 이벤트(또는 명시적 재판정)만 묻는다.

// openApprovals is every OPEN platform approval of the mission.
func (f *p2Fixture) openApprovals(t *testing.T, room string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM hitl_request
		 WHERE session_id = $1 AND source = 'system' AND purpose = 'user_approval' AND status = 'open'`, room).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// allApprovals counts every approval ever opened for the mission — the measured
// bug left five OPEN, but even one extra CLOSED row is a question nobody asked.
func (f *p2Fixture) allApprovals(t *testing.T, room string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM hitl_request
		 WHERE session_id = $1 AND source = 'system' AND purpose = 'user_approval'`, room).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// E: 제출 3회 → 열린 요청은 하나다(그리고 발행 자체가 한 번이다).
func TestTApprovalOneOpenRequestPerMissionAcrossSubmissions(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("user_approval")))
	// 토큰은 살아 있어야 제출할 수 있다 — 할 일만 끝난 것으로 둔다(보류 조건 해제).
	leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	f.settleWork(t, f.missionOf(t, sess))
	sub := f.srv.Hub.Subscribe(mustUUID(t, f.wsID), nil)
	defer sub.Close()

	for i, body := range []string{"v1", "v2", "v3"} {
		if st, out := f.submit(t, sess, leadTok, "game.html", "doc", []byte(body)); st != 201 {
			t.Fatalf("submit %d = %d %v", i+1, st, out)
		}
		if n := f.openApprovals(t, sess); n != 1 {
			t.Fatalf("제출 %d회 뒤 열린 승인 요청 = %d, want 1 — 실측은 여섯 번 열려 다섯이 동시에 open 이었다", i+1, n)
		}
	}
	if n := f.allApprovals(t, sess); n != 1 {
		t.Fatalf("발행된 승인 요청 = %d, want 1 — 같은 질문을 다시 묻지 않는다", n)
	}
	// 카드도 프레임도 한 번이다 — 타임라인에 같은 카드가 셋 쌓이지 않는다.
	var cards int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'hitl'`, sess).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if cards != 1 {
		t.Fatalf("타임라인 hitl 카드 = %d, want 1", cards)
	}
	assertCreatedFrame(t, hitlFrames(sub), sessions.CondUserApproval)
}

// F: 지정되지 않은 에이전트(Writer)의 제출은 조건을 충족시키지도, 요청을 열지도 않는다.
func TestTApprovalNonDesignatedSubmitDoesNotReopen(t *testing.T) {
	f := newP2Fixture(t)
	sess := f.artifactSession(t, and(atom("artifact_submitted", "who", "assignee"), atom("user_approval")))
	// 아직 아무도 제출하지 않았다 — Writer 가 먼저 낸다(E6-02: 저장은 되고 충족은 안 된다).
	wTok, _ := f.agentToken(t, sess, f.wUUID, "W")
	leadTok, _ := f.agentToken(t, sess, f.leadUUID, "Lead")
	f.settleWork(t, f.missionOf(t, sess))
	if st, out := f.submit(t, sess, wTok, "play-guide.md", "doc", []byte("가이드")); st != 201 {
		t.Fatalf("writer submit = %d %v", st, out)
	}
	if n := f.allApprovals(t, sess); n != 0 {
		t.Fatalf("지정되지 않은 에이전트의 제출로 승인 요청 %d 건 — want 0 (E6-02)", n)
	}

	// 담당(Lead)이 내면 그때 하나 열린다.
	if st, out := f.submit(t, sess, leadTok, "game.html", "doc", []byte("v1")); st != 201 {
		t.Fatalf("lead submit = %d %v", st, out)
	}
	if n := f.openApprovals(t, sess); n != 1 {
		t.Fatalf("담당 제출 뒤 열린 승인 요청 = %d, want 1", n)
	}

	// 그 뒤 Writer 가 또 내도 늘지 않는다 — 실측에서 14:08:59 Writer v2 가 또 열었다.
	if st, out := f.submit(t, sess, wTok, "play-guide.md", "doc", []byte("가이드 v2")); st != 201 {
		t.Fatalf("writer submit v2 = %d %v", st, out)
	}
	if n := f.allApprovals(t, sess); n != 1 {
		t.Fatalf("Writer 재제출 뒤 승인 요청 = %d, want 1", n)
	}
}

// 바닥 자물쇠 — 판정·조회를 지나쳐도 DB 가 두 번째 open 을 거부한다(경합).
func TestTApprovalOneOpenPerWorkIsEnforcedByTheIndex(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)
	_ = hitl
	var workID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM work WHERE room_id = $1`, mustUUID(t, f.sessionID)).Scan(&workID); err != nil {
		t.Fatal(err)
	}
	_, err := f.pool.Exec(t.Context(), `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at, work_id)
		VALUES ($1, NULL, 'system', 'approval', '두 번째', 'director', 'user_approval', now() + interval '1 day', now(), $2)`,
		f.sessionID, workID)
	if err == nil {
		t.Fatal("두 번째 open user_approval 이 들어갔다 — 부분 유니크 인덱스가 막아야 한다")
	}
	if !contains(err.Error(), "hitl_request_one_open_user_approval_per_work") {
		t.Fatalf("거부 이유 = %v, want the partial unique index", err)
	}
}
