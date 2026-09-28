package httpapi

// T-QUIET 리뷰 #390 NN1~NN4 — 동작은 맞지만 아무 테스트도 밟지 않던 가드 네 개.
// 리뷰어가 주입으로 찾았다(가드를 꺼도 초록): 여기서 각 가드에 단정을 하나씩 건다.
//
// 회귀 주입(각각 끄면 FAIL):
//   NN1 router/quiet.go 의 `isNote(content)` 를 빼면 (note) FAIL
//   NN2 router/quiet.go 의 `speech != "report"`(reportsToPerson) 를 빼면 (question) FAIL
//   NN4 router/service.go 의 `tr.Rule != RulePlatform` 을 빼면 (reviewer-reject) FAIL
//
// NN3(idempotent)만 다르다 — 가드가 **둘**이고 서로 덮는다: router/quiet.go 의
// `st == quiet.StateReleased`(이미 quiet 이면 Enter 를 부르지 않는다)와 quiet.Enter 의
// `approval_quiet IS DISTINCT FROM 'quiet'`(불려도 덮지 않는다). 그래서 하나만 끄면
// 초록이고(리뷰 #390 M9 가 초록이던 이유가 이것이다 — 공백이 아니라 이중화),
// **둘 다** 끄면 이 행이 FAIL 한다(진입 시각이 10분 밀린다). 한 겹이 사라져도 이 행은
// 침묵하므로, 그때는 남은 한 겹이 지키고 있다는 뜻으로 읽어야 한다.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/quiet"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// dirUUID is the fixture's Director (the only person in the room).
func (f *p2Fixture) dirUUID(t *testing.T) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM app_user ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// (note) NN1 — `/note` 는 기록이지 지시가 아니다. 사람이 메모만 남겼는데 승인 대기가
// 풀리면 「기록만 했는데 팀이 움직였다」가 된다(FR-3.3 규칙 1: /note 는 라우팅되지 않는다).
func TestTQuietNoteDoesNotRelease(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 각주 확인"})
	before := len(f.heldTasks(t, tm.room))
	if before == 0 {
		t.Fatal("setup: 보류가 없다")
	}

	f.humanPost(t, tm.room, "/note 각주 논의는 나중에 본다")

	if st := f.quietState(t, tm.room); st != quiet.StateQuiet {
		t.Fatalf("(note) `/note` 뒤 approval_quiet = %q, want quiet — 기록은 일을 다시 열지 않는다", st)
	}
	if n := len(f.heldTasks(t, tm.room)); n != before {
		t.Fatalf("(note) 보류 = %d, want %d — `/note` 가 보류를 풀었다", n, before)
	}
	// 그리고 진짜 지시는 여전히 푼다 — 가드가 사람 말을 통째로 막은 것이 아니다.
	f.humanPost(t, tm.room, router.MentionLink("R", f.rUUID)+" 3장 근거 하나만 더")
	if n := len(f.heldTasks(t, tm.room)); n != 0 {
		t.Fatalf("(note) 진짜 지시 뒤 보류 = %d, want 0", n)
	}
}

// (question) NN2 — 사람에게 하는 말이라고 다 보고는 아니다. 재진입은 `speech=report`
// 일 때만이다(#370 표). 동료가 깨운 턴이 Director 에게 하는 말은 `chat` 이고, 그것으로
// 사람이 연 라운드가 닫히면 안 된다.
func TestTQuietChatToPersonDoesNotReenter(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.endTurn(t, id)
	}
	leadTok, _ := f.agentToken(t, tm.room, f.leadUUID, "Lead") // 사람이 다시 열었다
	if st := f.quietState(t, tm.room); st != quiet.StateReleased {
		t.Fatalf("setup: %q, want released", st)
	}

	// Lead 가 W 를 깨운다 — 풀린 상태라 보류되지 않는다.
	out := f.postAs(t, tm.room, leadTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 각주 v20 부탁"})
	wTask := triggerTask(t, out, f.wUUID)
	f.runTask(t, wTask)
	laneID, _ := f.laneOf(t, wTask)
	wTok, err := f.srv.Tokens.Issue(t.Context(), f.pool, tokens.Scope{
		TaskID: wTask, Attempt: 1, LaneID: laneID, SessionID: mustUUID(t, tm.room), AgentID: f.wUUID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 동료(Lead)가 깨운 턴이 Director 에게 말한다 — 윗선 요청자가 Lead 라 보고가 아니라 대화다.
	f.postAs(t, tm.room, wTok, map[string]any{"content": "[@Simplist](mention://user/" + f.dirUUID(t).String() + ") 각주를 ≈85% 로 통일할까요?"})
	var speech, kinds string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT coalesce(speech,''), coalesce((SELECT string_agg(x->>'kind', ',') FROM jsonb_array_elements(addressees) x), '')
		  FROM message WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`, tm.room).Scan(&speech, &kinds); err != nil {
		t.Fatal(err)
	}
	if speech == "report" || !strings.Contains(kinds, "user") {
		t.Fatalf("setup: 이 말은 speech=%q addressees=%q — 「사람에게 말하지만 보고는 아님」이 아니다(#370 이 바뀌었으면 이 행도 고쳐라)", speech, kinds)
	}
	if st := f.quietState(t, tm.room); st != quiet.StateReleased {
		t.Fatalf("(question) 사람에게 한 대화 뒤 approval_quiet = %q, want released — 보고만 라운드를 닫는다", st)
	}
	// 그래서 그 뒤 협업은 아직 자유다.
	out = f.postAs(t, tm.room, leadTok, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 답 오면 반영합시다"})
	if quietWarning(out, f.rUUID) != "" {
		t.Fatalf("(question) 대화가 재진입시켰다 — 멘션이 보류됐다: %v", out["warnings"])
	}
}

// (idempotent) NN3 — 이미 승인 대기인데 또 사람에게 보고해도 진입 시각을 덮지 않는다.
// 덮으면 「언제부터 멈춰 있었나」가 마지막 보고 시각으로 밀린다.
func TestTQuietReportWhileQuietIsIdempotent(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.endTurn(t, id)
	}
	at := func() string {
		var s string
		if err := f.pool.QueryRow(t.Context(),
			`SELECT coalesce(approval_quiet_at::text,'') FROM work WHERE id = $1`, f.missionOf(t, tm.room)).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	speechOfLast := func() string {
		var s string
		if err := f.pool.QueryRow(t.Context(),
			`SELECT coalesce(speech,'') FROM message WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`, tm.room).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// 사람이 열고(released) → Lead 가 사람에게 보고 → 다시 승인 대기(그때가 T1).
	leadTok, _ := f.agentToken(t, tm.room, f.leadUUID, "Lead")
	f.postAs(t, tm.room, leadTok, map[string]any{"content": "Simplist 님, 각주까지 끝났습니다."})
	if got := speechOfLast(); got != "report" {
		t.Fatalf("setup: 첫 보고의 speech = %q, want report (#370 이 바뀌었으면 이 행도 고쳐라)", got)
	}
	if st := f.quietState(t, tm.room); st != quiet.StateQuiet {
		t.Fatalf("setup: 보고 뒤 approval_quiet = %q, want quiet", st)
	}
	first := at()
	if first == "" {
		t.Fatal("setup: approval_quiet_at 이 비었다")
	}

	// 10분 뒤 같은 턴이 또 사람에게 보고한다 — 이미 승인 대기다.
	f.fake.Advance(10 * time.Minute)
	f.postAs(t, tm.room, leadTok, map[string]any{"content": "Simplist 님, 한 가지 덧붙입니다 — 각주 출처도 붙였습니다."})
	if got := speechOfLast(); got != "report" {
		t.Fatalf("setup: 두 번째 보고의 speech = %q, want report", got)
	}
	if st := f.quietState(t, tm.room); st != quiet.StateQuiet {
		t.Fatalf("(idempotent) approval_quiet = %q, want quiet", st)
	}
	if now := at(); now != first {
		t.Fatalf("(idempotent) approval_quiet_at 이 %q → %q 로 덮였다 — 이미 승인 대기면 그대로 둔다", first, now)
	}
}

// (reviewer-reject) NN4 — **이 PR 에서 가장 값이 큰 공백**(리뷰 #390).
// 승인 대기 중에도 에이전트 검토자의 거절은 제출자에게 닿아야 한다. 서버가 제출자 lane 에
// 넣는 것은 platform 트리거(시스템의 말)이지 에이전트끼리의 새 일이 아니다 — 보류하면
// 제출자는 고칠 기회를 영영 받지 못한다.
func TestTQuietReviewerRejectReachesSubmitter(t *testing.T) {
	f := newP2Fixture(t)
	f.setRole(t, f.rUUID, "reviewer") // K-19: 검토는 reviewer 역할의 것
	// 제출(담당) + 검토자 승인 + Director 승인 — 검토자가 거절할 수 있는 미션.
	sess := f.artifactSession(t, and(
		atom("artifact_submitted", "who", "assignee"),
		atom("agent_approval", "agent_id", f.r),
		atom("user_approval")))
	leadTok, leadTask := f.agentToken(t, sess, f.leadUUID, "Lead")
	reviewerTok, reviewerTask := f.agentToken(t, sess, f.rUUID, "R")
	f.runTask(t, leadTask)
	f.runTask(t, reviewerTask)

	st, out := f.submit(t, sess, leadTok, "game.html", "doc", []byte("v39"))
	if st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	artID := str(out["artifact"].(map[string]any), "id")

	// 검토자가 승인 → 이제 남은 것은 Director 승인뿐 = 승인 대기.
	if st, out := f.rawPost(t, f.p+"/artifacts/"+artID+"/review", reviewerTok, map[string]any{"verdict": "approve"}); st != 200 {
		t.Fatalf("approve = %d %v", st, out)
	}
	if got := f.quietState(t, sess); got != quiet.StateQuiet {
		t.Fatalf("setup: 검토 승인 뒤 approval_quiet = %q, want quiet", got)
	}
	// 그 상태에서 에이전트끼리의 말은 보류된다(대조군).
	if w := quietWarning(f.postAs(t, sess, leadTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 각주 확인"}), f.wUUID); w == "" {
		t.Fatal("setup: 승인 대기인데 에이전트 멘션이 보류되지 않았다")
	}

	// 같은 검토자가 v2 를 거절한다 — 서버가 제출자(Lead) lane 에 platform 트리거를 넣는다.
	if st, out := f.submit(t, sess, leadTok, "game.html", "doc", []byte("v40")); st != 201 {
		t.Fatalf("submit v2 = %d %v", st, out)
	}
	st, out = f.rawPost(t, f.p+"/artifacts/"+artID+"/review", reviewerTok,
		map[string]any{"verdict": "reject", "comments": "3장 튜토리얼이 비었습니다"})
	if st != 200 {
		t.Fatalf("reject = %d %v", st, out)
	}

	// 거절이 만든 Lead 의 task 는 보류가 아니다 — 큐에서 나갈 수 있어야 한다.
	var reason *string
	var status string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT t.status::text, t.queued_reason::text FROM task t
		 WHERE t.session_id = $1 AND t.agent_id = $2 AND t.id <> $3
		 ORDER BY t.created_at DESC LIMIT 1`,
		mustUUID(t, sess), f.leadUUID, leadTask).Scan(&status, &reason); err != nil {
		t.Fatalf("거절이 제출자 task 를 만들지 않았다: %v", err)
	}
	if reason != nil && *reason == quiet.ReasonApprovalPending {
		t.Fatalf("(reviewer-reject) 검토자 거절이 보류됐다 — 제출자가 고칠 기회를 영영 못 받는다 (task %s)", status)
	}
	// 게시 결과에도 보류 안내가 붙지 않는다(안내는 깨우지 않은 상대에게만).
	msg, _ := out["message"].(map[string]any)
	if msg != nil && strings.Contains(str(msg, "content"), "was not woken") {
		t.Fatalf("(reviewer-reject) 거절 본문에 보류 안내가 붙었다: %q", str(msg, "content"))
	}
	// 그리고 실제로 claim 이 집어 간다.
	var handed bool
	for id := range f.claimAll(t) {
		var agent uuid.UUID
		if err := f.pool.QueryRow(t.Context(), `SELECT agent_id FROM task WHERE id = $1`, mustUUID(t, id)).Scan(&agent); err == nil && agent == f.leadUUID {
			handed = true
		}
	}
	if !handed {
		t.Fatal("(reviewer-reject) claim 이 제출자의 거절 turn 을 주지 않았다")
	}
}
