package httpapi

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/quiet"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tokens"
)

// T-QUIET (PRD v0.19.13 FR-2A.2.3, Director 결정 2026-09-28 — 안 A).
//
// 실측(「게임 제작 방」 2026-09-28): 게임이 완성되고 Lead 가 「팀 쪽에 열린 일은
// 0」이라고 보고한 뒤에도 Lead·Writer·Researcher 가 30분 동안 서로 멘션하며
// 가이드 각주를 고쳤다. 도는 할 일이 끊이지 않아 FR-2A.2.1 의 보류가 영영 안
// 풀렸고 승인 요청은 한 번도 뜨지 않았다.
//
// 이 파일의 행:
//   (judge)     종료 조건에서 user_approval 만 남으면 승인 대기(work.approval_quiet)
//   (hold)      승인 대기 동안 에이전트 메시지의 트리거는 approval_pending 으로 보류,
//               claim 이 건너뛴다, 게시 결과에 harness v0.9.15 한 줄
//   (human)     사람 메시지의 트리거는 보류하지 않는다 — 그리고 보류가 풀린다(③)
//   (chain)     오늘 모양: 셋이 서로 멘션하는 사슬 → 도는 턴이 끝나면 승인 요청이 연다
//   (approve)   승인 → 보류 할 일 취소(사유 문장)
//   (reject)    수정 요청 → 보류 풀림, 사유는 Lead 의 다음 턴에
//   (condition) 조건 변경으로 승인 외 조건 미충족 → 풀림(④)
//   (report)    사람이 푼 뒤 에이전트가 사람에게 보고하면 다시 승인 대기(Lead 판정 (c))
//   (settle)    사람이 푼 뒤 일이 멈췄는데 승인 요청이 열려 있으면 다시 승인 대기((b))
//   (delegate)  lane delegate 도 보류
//   (race)      승인과 에이전트 메시지가 동시에 — 큐에 떠도는 할 일이 남지 않는다
//
// 회귀 주입(각각 끄면 FAIL — PR 본문에 결과):
//   I1 complete.go 의 quiet.Enter 를 빼면 (judge)(hold)(chain) FAIL
//   I2 router/service.go 의 quiet.Hold 를 빼면 (hold)(chain) FAIL
//   I3 queue/postgres.go claim 의 approval_pending 줄을 빼면 (hold) claim FAIL
//   I4 approval_hold.go busyTaskSQL 의 HeldTaskSQL 을 빼면 (chain) FAIL
//   I5 router/quiet.go 의 user 가지(Release)를 빼면 (human) FAIL
//   I6 complete.go 의 CancelHeld 를 빼면 (approve) FAIL
//   I7 complete.go 의 director_reject 가지를 빼면 (reject) FAIL
//   I8 complete.go 의 !needsUserApproval 가지를 빼면 (condition) FAIL
//   I9 router/quiet.go 의 reportsToPerson 재진입을 빼면 (report) FAIL
//   I10 router/quiet.go holdsFor 의 closed → CancelClosed 를 빼면 (race) FAIL
//   I11 delegate.go 의 quiet.Hold 를 빼면 (delegate) FAIL

// quietTree is the game room's condition: the assignee's artifact, then the
// Director's approval.
func quietTree() map[string]any {
	return and(atom("artifact_submitted", "who", "assignee"), atom("user_approval"))
}

// postAs posts into room as the agent behind tok and returns the result.
func (f *p2Fixture) postAs(t *testing.T, room, tok string, body map[string]any) map[string]any {
	t.Helper()
	f.fake.Advance(time.Minute)
	c := &client{t: t, srv: f.api.srv, bearer: tok}
	return c.must(201, "POST", f.p+"/rooms/"+room+"/messages", body, "Idempotency-Key", uuid.NewString())
}

// humanPost posts into room as the Director.
func (f *p2Fixture) humanPost(t *testing.T, room, content string) map[string]any {
	t.Helper()
	f.fake.Advance(time.Minute)
	return f.api.must(201, "POST", f.p+"/rooms/"+room+"/messages", map[string]any{"content": content}, "Idempotency-Key", uuid.NewString())
}

func (f *p2Fixture) quietState(t *testing.T, room string) string {
	t.Helper()
	var st *string
	if err := f.pool.QueryRow(t.Context(), `SELECT approval_quiet FROM work WHERE id = $1`, f.missionOf(t, room)).Scan(&st); err != nil {
		t.Fatal(err)
	}
	if st == nil {
		return ""
	}
	return *st
}

// heldTasks is the mission's held triggers by agent id.
func (f *p2Fixture) heldTasks(t *testing.T, room string) map[string]string {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `
		SELECT t.id::text, t.agent_id::text FROM task t
		 WHERE t.work_id = $1 AND t.status = 'queued' AND t.queued_reason = 'approval_pending'`, f.missionOf(t, room))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, agent string
		if err := rows.Scan(&id, &agent); err != nil {
			t.Fatal(err)
		}
		out[id] = agent
	}
	return out
}

// triggerTask is the task the post made for agent.
func triggerTask(t *testing.T, out map[string]any, agent uuid.UUID) uuid.UUID {
	t.Helper()
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == agent.String() {
			return mustUUID(t, str(tr, "task_id"))
		}
	}
	t.Fatalf("no trigger for %s: %v", agent, out["triggers"])
	return uuid.Nil
}

// quietWarning is the post's approval_pending warning for agent ("" if none).
func quietWarning(out map[string]any, agent uuid.UUID) string {
	ws, _ := out["warnings"].([]any)
	for _, raw := range ws {
		w := raw.(map[string]any)
		if str(w, "code") == quiet.WarningCode && str(w, "agent_id") == agent.String() {
			return str(w, "message")
		}
	}
	return ""
}

func (f *p2Fixture) pausedAgentTriggers(t *testing.T, room string) int {
	t.Helper()
	_, prog := f.conds(t, room)
	if v, ok := prog["paused_agent_triggers"].(float64); ok {
		return int(v)
	}
	return 0
}

// team starts a quiet-ready room: Lead, R and W each woken by the Director
// and running, and Lead's artifact submitted mid-turn — the approval is held
// for the running work (FR-2A.2.1) and the mission is waiting for approval.
type team struct {
	room                   string
	leadTok, rTok, wTok    string
	leadTask, rTask, wTask uuid.UUID
}

func (f *p2Fixture) quietTeam(t *testing.T) team {
	t.Helper()
	var tm team
	tm.room = f.artifactSession(t, quietTree())
	tm.leadTok, tm.leadTask = f.agentToken(t, tm.room, f.leadUUID, "Lead")
	tm.rTok, tm.rTask = f.agentToken(t, tm.room, f.rUUID, "R")
	tm.wTok, tm.wTask = f.agentToken(t, tm.room, f.wUUID, "W")
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.runTask(t, id)
	}
	if st, out := f.submit(t, tm.room, tm.leadTok, "game.html", "doc", []byte("v39")); st != 201 {
		t.Fatalf("submit = %d %v", st, out)
	}
	return tm
}

// (judge) + (hold) — a mission waiting for approval holds an agent's mention;
// the post says whom it did not wake; the claim passes the held task by; the
// progress row counts it.
func TestTQuietAgentTriggerHeld(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	if st := f.quietState(t, tm.room); st != quiet.StateQuiet {
		t.Fatalf("(judge) approval_quiet after the last non-approval atom = %q, want quiet", st)
	}

	out := f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 각주 86% → 85% 로 고쳐 주세요"})
	task := triggerTask(t, out, f.wUUID)
	held := f.heldTasks(t, tm.room)
	if held[task.String()] != f.w {
		t.Fatalf("(hold) W's trigger is not held: held=%v task=%s", held, task)
	}
	if got, want := quietWarning(out, f.wUUID), "This mission is waiting for the Director's approval, so @W was not woken. If work remains after approval, tell the Director."; got != want {
		t.Fatalf("(hold) post result notice = %q, want harness v0.9.15 %q", got, want)
	}
	// W's running turn ends; the claim must not hand the held one out.
	f.endTurn(t, tm.wTask)
	if _, ok := f.claimAll(t)[task.String()]; ok {
		t.Fatalf("(hold) the claim handed out a held trigger")
	}
	if n := f.pausedAgentTriggers(t, tm.room); n != 1 {
		t.Fatalf("(progress) paused_agent_triggers = %d, want 1", n)
	}
}

// (human) a person's message is never held, and it re-opens the work: the
// held triggers go back into line.
func TestTQuietHumanTriggerNotHeldAndReleases(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	out := f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 표를 다시 봐 주세요"})
	wHeld := triggerTask(t, out, f.wUUID)
	if len(f.heldTasks(t, tm.room)) != 1 {
		t.Fatalf("setup: want one held trigger")
	}
	// The Director asks R directly.
	h := f.humanPost(t, tm.room, router.MentionLink("R", f.rUUID)+" 3장 수치 근거를 하나 더 붙여 주세요")
	rTask := triggerTask(t, h, f.rUUID)
	var reason *string
	if err := f.pool.QueryRow(t.Context(), `SELECT queued_reason::text FROM task WHERE id = $1`, rTask).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != nil && *reason == quiet.ReasonApprovalPending {
		t.Fatalf("(human) a person's trigger was held")
	}
	if quietWarning(h, f.rUUID) != "" {
		t.Fatalf("(human) a person's post got the approval notice: %v", h["warnings"])
	}
	if n := len(f.heldTasks(t, tm.room)); n != 0 {
		t.Fatalf("(human) held triggers after the Director spoke = %d, want 0 (③)", n)
	}
	if st := f.quietState(t, tm.room); st != quiet.StateReleased {
		t.Fatalf("(human) approval_quiet = %q, want released", st)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT queued_reason::text FROM task WHERE id = $1`, wHeld).Scan(&reason); err != nil || reason != nil {
		t.Fatalf("(human) W's released task still has reason %v (%v)", reason, err)
	}
	// Agents work together again: R → W is not held now.
	out = f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 근거 붙였습니다"})
	if quietWarning(out, f.leadUUID) != "" || len(f.heldTasks(t, tm.room)) != 0 {
		t.Fatalf("(human) after release an agent mention is still held: %v", out["warnings"])
	}
}

// (chain) today's shape: three agents mention each other in turn after the
// game is done. Every one of those triggers is held; once the running turns
// end, the approval request opens — exactly once — and nothing runs.
func TestTQuietChainStillOpensApproval(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	if n := f.openHitls(t, tm.room); n != 0 {
		t.Fatalf("setup: approval open while three turns run = %d, want 0 (FR-2A.2.1)", n)
	}
	// Lead → W, W → R, R → Lead: the 각주 핑퐁, each from a running turn.
	f.postAs(t, tm.room, tm.leadTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 가이드 각주 ≈86% 확인 부탁"})
	f.postAs(t, tm.room, tm.wTok, map[string]any{"content": router.MentionLink("R", f.rUUID) + " ≈85% 가 맞나요?"})
	f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 4.1σ → 3.3σ 로 고쳤습니다"})
	if n := len(f.heldTasks(t, tm.room)); n != 3 {
		t.Fatalf("(chain) held triggers = %d, want 3", n)
	}
	f.endTurn(t, tm.leadTask)
	f.endTurn(t, tm.wTask)
	if n := f.openHitls(t, tm.room); n != 0 {
		t.Fatalf("(chain) approval opened while R still runs = %d", n)
	}
	f.endTurn(t, tm.rTask)
	if n := f.openHitls(t, tm.room); n != 1 {
		t.Fatalf("(chain) open approvals after the last running turn = %d, want 1 — held triggers must not count as work running", n)
	}
	held := f.heldTasks(t, tm.room)
	for id := range f.claimAll(t) {
		if _, ok := held[id]; ok {
			t.Fatalf("(chain) the claim handed out held turn %s", id)
		}
	}
	if n := f.pausedAgentTriggers(t, tm.room); n != 3 {
		t.Fatalf("(chain) paused_agent_triggers = %d, want 3", n)
	}
}

// (approve) the Director approves: the mission closes and every held
// trigger is cancelled with the sentence the feed shows.
func TestTQuietApproveCancelsHeld(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 각주 확인"})
	f.postAs(t, tm.room, tm.wTok, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 확인했습니다"})
	held := f.heldTasks(t, tm.room)
	if len(held) != 2 {
		t.Fatalf("setup held = %d, want 2", len(held))
	}
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.endTurn(t, id)
	}
	var hitl string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'user_approval' AND status = 'open'`, tm.room).Scan(&hitl); err != nil {
		t.Fatalf("no open approval: %v", err)
	}
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response", map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	for id := range held {
		var status, stop string
		if err := f.pool.QueryRow(t.Context(), `SELECT status::text, COALESCE(stop_reason, '') FROM task WHERE id = $1`, id).Scan(&status, &stop); err != nil {
			t.Fatal(err)
		}
		if status != "cancelled" || stop != quiet.StopApprovedClosed {
			t.Fatalf("(approve) held task %s = %s/%s, want cancelled/%s", id, status, stop, quiet.StopApprovedClosed)
		}
		var note string
		if err := f.pool.QueryRow(t.Context(), `
			SELECT COALESCE(payload->'args'->>'note', '') FROM task_event WHERE task_id = $1 AND verb = 'cancel' ORDER BY seq DESC LIMIT 1`, id).Scan(&note); err != nil {
			t.Fatal(err)
		}
		if note != "일이 승인되어 닫혔습니다" {
			t.Fatalf("(approve) feed note = %q, want PRD FR-2A.2.3 ① 「일이 승인되어 닫혔습니다」", note)
		}
	}
	if st := f.quietState(t, tm.room); st != "" {
		t.Fatalf("(approve) approval_quiet after close = %q, want cleared", st)
	}
}

// (reject) a change request re-opens the work: the held triggers run in
// order, and the reason reaches Lead's next turn with the decision.
func TestTQuietChangeRequestReleases(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	out := f.postAs(t, tm.room, tm.wTok, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 각주 고쳤습니다"})
	leadHeld := triggerTask(t, out, f.leadUUID)
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.endTurn(t, id)
	}
	var hitl string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'user_approval' AND status = 'open'`, tm.room).Scan(&hitl); err != nil {
		t.Fatalf("no open approval: %v", err)
	}
	const why = "3장 튜토리얼이 비었습니다 — 채워 주세요"
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response", map[string]any{"approved": false, "reason": why}, "Idempotency-Key", uuid.NewString())
	if n := len(f.heldTasks(t, tm.room)); n != 0 {
		t.Fatalf("(reject) held after a change request = %d, want 0 (②)", n)
	}
	if st := f.quietState(t, tm.room); st != quiet.StateReleased {
		t.Fatalf("(reject) approval_quiet = %q, want released", st)
	}
	b, ok := f.claimAll(t)[leadHeld.String()]
	if !ok {
		t.Fatalf("(reject) Lead's released trigger was not handed out")
	}
	raw, _ := json.Marshal(b)
	if !strings.Contains(string(raw), why) {
		t.Fatalf("(reject) Lead's next turn does not carry the reason %q", why)
	}
}

// (condition) the Director adds a reviewer atom: approval is no longer the
// only thing missing, so the work is not waiting for it.
func TestTQuietConditionChangeReleases(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	f.postAs(t, tm.room, tm.rTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 각주 확인"})
	if st, out := f.patchCond(t, f.api, tm.room, and(atom("artifact_submitted", "who", "assignee"), atom("agent_approval", "agent_id", f.r), atom("user_approval"))); st != 200 {
		t.Fatalf("patch condition = %d %v", st, out)
	}
	if n := len(f.heldTasks(t, tm.room)); n != 0 {
		t.Fatalf("(condition) held after an unmet atom came back = %d, want 0 (④)", n)
	}
	if st := f.quietState(t, tm.room); st != "" {
		t.Fatalf("(condition) approval_quiet = %q, want cleared", st)
	}
}

// (report) + (settle): after a person re-opened the work, an agent's report
// to a person closes the round — the next agent mention is held again.
func TestTQuietReportToPersonReenters(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.endTurn(t, id)
	}
	// 19:52 「다시 진행해」 — the Director wakes Lead.
	leadTok, _ := f.agentToken(t, tm.room, f.leadUUID, "Lead")
	if st := f.quietState(t, tm.room); st != quiet.StateReleased {
		t.Fatalf("setup: approval_quiet after the Director spoke = %q, want released", st)
	}
	// Lead delegates by mention — free while released.
	out := f.postAs(t, tm.room, leadTok, map[string]any{"content": router.MentionLink("W", f.wUUID) + " 가이드 v20 부탁"})
	if quietWarning(out, f.wUUID) != "" {
		t.Fatalf("(report) released mission held a mention")
	}
	wTask := triggerTask(t, out, f.wUUID)
	f.runTask(t, wTask)
	wTok, err := f.srv.Tokens.Issue(t.Context(), f.pool, tokens.Scope{
		TaskID: wTask, Attempt: 1, LaneID: func() uuid.UUID { l, _ := f.laneOf(t, wTask); return l }(), SessionID: mustUUID(t, tm.room), AgentID: f.wUUID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 20:47 Lead reports to the Director — no mention, woken by a person.
	f.postAs(t, tm.room, leadTok, map[string]any{"content": "Simplist 님, 이번이 마지막이었습니다. 팀 쪽에 열린 일은 0 입니다."})
	var speech string
	if err := f.pool.QueryRow(t.Context(), `SELECT speech FROM message WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`, tm.room).Scan(&speech); err != nil || speech != "report" {
		t.Fatalf("setup: Lead's line speech = %q (%v), want report", speech, err)
	}
	if st := f.quietState(t, tm.room); st != quiet.StateQuiet {
		t.Fatalf("(report) approval_quiet after Lead reported to a person = %q, want quiet (Lead 판정 (c))", st)
	}
	out = f.postAs(t, tm.room, wTok, map[string]any{"content": router.MentionLink("R", f.rUUID) + " ≈86% → ≈85%?"})
	if quietWarning(out, f.rUUID) == "" {
		t.Fatalf("(report) the ping-pong after the report is not held: %v", out["warnings"])
	}
}

func TestTQuietSettleReenters(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	for _, id := range []uuid.UUID{tm.leadTask, tm.rTask, tm.wTask} {
		f.endTurn(t, id)
	}
	if n := f.openHitls(t, tm.room); n != 1 {
		t.Fatalf("setup: open approvals = %d, want 1", n)
	}
	// The Director asks R one thing; the card stays open.
	_, rTask := f.agentToken(t, tm.room, f.rUUID, "R")
	if st := f.quietState(t, tm.room); st != quiet.StateReleased {
		t.Fatalf("setup: %q, want released", st)
	}
	f.endTurn(t, rTask)
	if st := f.quietState(t, tm.room); st != quiet.StateQuiet {
		t.Fatalf("(settle) approval_quiet after the work settled with the card open = %q, want quiet (Lead 판정 (b))", st)
	}
}

// (delegate) `lane delegate` is an agent's trigger like a mention.
func TestTQuietDelegateHeld(t *testing.T) {
	f := newP2Fixture(t)
	tm := f.quietTeam(t)
	c := &client{t: t, srv: f.api.srv, bearer: tm.leadTok}
	out := c.must(201, "POST", f.p+"/rooms/"+tm.room+"/lanes", map[string]any{"agent_id": f.w, "brief": "가이드 각주 한 번 더"}, "Idempotency-Key", uuid.NewString())
	task, _ := out["task"].(map[string]any)
	if str(task, "queued_reason") != quiet.ReasonApprovalPending {
		t.Fatalf("(delegate) delegated task queued_reason = %q, want approval_pending: %v", str(task, "queued_reason"), task)
	}
}

// (race) the Director approves while an agent posts. Whichever takes the
// room lock second sees the other's result; no queued task is left behind
// on a closed mission, and nothing of it is ever handed out.
func TestTQuietApproveRace(t *testing.T) {
	for i := 0; i < 5; i++ {
		f := newP2Fixture(t)
		tm := f.quietTeam(t)
		for _, id := range []uuid.UUID{tm.leadTask, tm.wTask} {
			f.endTurn(t, id)
		}
		// R still runs; hold its approval open by settling R in place so
		// the card opens, and keep R's token live for the racing post.
		f.exec(t, `UPDATE task SET status = 'completed', finished_at = now() WHERE id = $1`, tm.rTask)
		if _, err := f.srv.Sessions.ReleaseHeldApproval(t.Context(), mustUUID(t, f.missionOf(t, tm.room))); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `UPDATE task SET status = 'running', finished_at = NULL WHERE id = $1`, tm.rTask)
		var hitl string
		if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'user_approval' AND status = 'open'`, tm.room).Scan(&hitl); err != nil {
			t.Fatalf("no open approval: %v", err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			c := &client{t: t, srv: f.api.srv, bearer: tm.rTok}
			c.do("POST", f.p+"/rooms/"+tm.room+"/messages", map[string]any{"content": router.MentionLink("W", f.wUUID) + " 마지막 각주"}, "Idempotency-Key", uuid.NewString())
		}()
		go func() {
			defer wg.Done()
			f.api.do("POST", f.p+"/hitl-requests/"+hitl+"/response", map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
		}()
		wg.Wait()
		var status string
		var left int
		if err := f.pool.QueryRow(t.Context(), `
			SELECT wk.status::text, (SELECT count(*) FROM task t WHERE t.work_id = wk.id AND t.status = 'queued')
			  FROM work wk WHERE wk.id = $1`, f.missionOf(t, tm.room)).Scan(&status, &left); err != nil {
			t.Fatal(err)
		}
		if status != "completed" || left != 0 {
			t.Fatalf("(race %d) mission %s with %d queued tasks left, want completed and 0", i, status, left)
		}
	}
}
