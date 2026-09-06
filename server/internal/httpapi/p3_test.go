package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// The P3 server slice over the real transaction (T-S5). The golden tables pin
// the DECISIONS; these rows pin that the HTTP path reaches them and writes what
// they say — and they are also where the E9/E10 rows that stay behind the
// `p3golden` tag (their daemon halves belong to T-D5) are held on this side.

// hitlOn registers a HITL request as the agent would, with its TaskToken.
func (f *p2Fixture) hitlOn(t *testing.T, tok string, body map[string]any, wantStatus int) map[string]any {
	t.Helper()
	st, out := f.rawPost(t, f.p+"/sessions/"+f.sessionID+"/hitl-requests", tok, body)
	if st != wantStatus {
		t.Fatalf("createHitlRequest = %d %v, want %d", st, out, wantStatus)
	}
	return out
}

func (f *p2Fixture) taskStatus(t *testing.T, taskID uuid.UUID) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM task WHERE id = $1`, taskID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestP3HitlRegistrationAndTurnEnd is E7-01·02·03·04·05·20: registration flags
// the task and does NOT move it, a second request is refused with the first
// standing, and only `turn_end` reaches waiting_human.
func TestP3HitlRegistrationAndTurnEnd(t *testing.T) {
	f := newP2Fixture(t)
	tok, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")

	// E7-05 / E7-20: no default, no request.
	f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?"}, 422)
	f.hitlOn(t, tok, map[string]any{"type": "choice", "question": "어느 쪽?", "options": []string{"A", "B"}}, 422)
	// E7-16: fail closed on an unsupported approver spec.
	f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자",
		"approver_spec": "role:reviewer"}, 422)

	out := f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자"}, 201)
	if out["turn_end_required"] != true {
		t.Fatalf("turn_end_required = %v, want true (FR-5.2)", out["turn_end_required"])
	}
	first := str(out["hitl_request"].(map[string]any), "id")
	if str(out, "message_id") == "" {
		t.Fatal("the request is posted to the timeline as a card (FR-5.2)")
	}
	// E7-01: the task is flagged and still RUNNING. Registration is not a
	// transition; only turn_end is.
	var pending bool
	if err := f.pool.QueryRow(t.Context(), `SELECT pending_hitl FROM task WHERE id = $1`, taskID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if !pending {
		t.Fatal("registration sets pending_hitl (FR-7.1 step 1)")
	}
	if st := f.taskStatus(t, taskID); st == "waiting_human" {
		t.Fatal("registration must NOT transition the task (E7-01)")
	}

	// E7-02: the agent keeps working. Its messages are kept.
	if st, body := f.rawKeyed(t, f.p+"/sessions/"+f.sessionID+"/messages", tok, "application/json",
		mustJSON(t, map[string]any{"content": "그동안 초안을 정리했습니다"}), uuid.NewString()); st != 201 {
		t.Fatalf("a post after the request = %d %v, want 201 — the message already happened (FR-7.1 step 2)", st, body)
	}

	// E7-04: the second request is refused, the first stands, the feed records it.
	f.hitlOn(t, tok, map[string]any{"type": "question", "question": "예산?", "proposed_default": "$100"}, 409)
	var open string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE task_id = $1 AND status = 'open'`, taskID).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != first {
		t.Fatalf("open request = %s, want the FIRST one %s", open, first)
	}
	var refusals int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM task_event WHERE task_id = $1 AND object_ref = '"hitl.rejected"'`, taskID).Scan(&refusals); err != nil {
		t.Fatal(err)
	}
	if refusals != 1 {
		t.Fatalf("feed entries for the refusal = %d, want 1 (E7-04)", refusals)
	}

	// E7-03: turn_end. The daemon reports a finished turn; the SERVER decides
	// waiting_human from pending_hitl (daemon-protocol §4.4).
	f.endTurn(t, taskID)
	if st := f.taskStatus(t, taskID); st != "waiting_human" {
		t.Fatalf("task = %q after turn_end, want waiting_human (FR-7.1 step 3)", st)
	}
	var laneStatus string
	if err := f.pool.QueryRow(t.Context(), `SELECT l.status::text FROM lane l JOIN task t ON t.lane_id = l.id WHERE t.id = $1`, taskID).Scan(&laneStatus); err != nil {
		t.Fatal(err)
	}
	if laneStatus != "waiting_human" {
		t.Fatalf("lane = %q, want waiting_human", laneStatus)
	}
	// The attempt's token is revoked — a process that no longer exists must not
	// be able to post (FR-9.1).
	if st, _ := f.rawGet(t, f.p+"/cli/context", tok); st != 401 {
		t.Fatalf("token after waiting_human = %d, want 401", st)
	}
	// The Director has an action_required item (FR-8).
	items := f.api.must(200, "GET", f.p+"/inbox?session_id="+f.sessionID, nil)
	found := false
	for _, raw := range items["items"].([]any) {
		row := raw.(map[string]any)
		if str(row, "ref_id") == first {
			found = true
			if str(row, "severity") != "action_required" {
				t.Fatalf("inbox severity = %q, want action_required (FR-8)", str(row, "severity"))
			}
		}
	}
	if !found {
		t.Fatalf("the request is not in the Director's inbox: %v", items["items"])
	}
}

// TestP3HitlAnswerRequeues is E7-07·08·17: the answer records one decision and
// re-queues a NEW attempt, a second answer is ignored, and a rejection resumes
// the task rather than failing it.
func TestP3HitlAnswerRequeues(t *testing.T) {
	f := newP2Fixture(t)
	tok, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	out := f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자"}, 201)
	id := str(out["hitl_request"].(map[string]any), "id")
	f.endTurn(t, taskID)

	ans := f.api.must(200, "POST", f.p+"/hitl-requests/"+id+"/response",
		map[string]any{"answer": "경영진"}, "Idempotency-Key", uuid.NewString())
	if ans["ignored"] != false || ans["decision_id"] == nil {
		t.Fatalf("answer = %v, want a recorded decision (FR-5.2)", ans)
	}
	// E7-07: a NEW attempt. waiting_human cannot go back to running (E5-01).
	var status string
	var attempt int
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, attempt FROM task WHERE id = $1`, taskID).Scan(&status, &attempt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || attempt != 2 {
		t.Fatalf("task = %s attempt %d, want queued attempt 2 (FR-5.4)", status, attempt)
	}
	var decisions int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM decision WHERE ref_id = $1`, id).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("decisions = %d, want exactly 1 (FR-5.2)", decisions)
	}
	// E7-08: the second response is a no-op, not an error, and the first answer
	// stands.
	second := f.api.must(200, "POST", f.p+"/hitl-requests/"+id+"/response",
		map[string]any{"answer": "실무자"}, "Idempotency-Key", uuid.NewString())
	if second["ignored"] != true {
		t.Fatalf("second response ignored = %v, want true (E7-08)", second["ignored"])
	}
	req := second["hitl_request"].(map[string]any)
	if str(req, "answer") != "경영진" {
		t.Fatalf("stored answer = %q, want the FIRST answer", str(req, "answer"))
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM decision WHERE ref_id = $1`, id).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 1 {
		t.Fatalf("decisions after the ignored response = %d, want 1", decisions)
	}
}

// TestP3RejectedApprovalResumes is E7-17: a rejected approval is a branch the
// agent handles, not a failure.
func TestP3RejectedApprovalResumes(t *testing.T) {
	f := newP2Fixture(t)
	tok, taskID := f.agentToken(t, f.sessionID, f.wUUID, "W")
	out := f.hitlOn(t, tok, map[string]any{"type": "approval", "summary": "초안 승인 요청"}, 201)
	id := str(out["hitl_request"].(map[string]any), "id")
	f.endTurn(t, taskID)
	f.api.must(200, "POST", f.p+"/hitl-requests/"+id+"/response",
		map[string]any{"approved": false, "reason": "근거가 부족합니다"}, "Idempotency-Key", uuid.NewString())
	if st := f.taskStatus(t, taskID); st != "queued" {
		t.Fatalf("task = %q after a rejection, want queued — a rejection is not a failure (FR-5.4)", st)
	}
}

// TestP3DeputyWindow is E7-09·10·11: `director` means "the Director, and after
// HALF the deadline the deputy". A plain member never becomes eligible, and the
// 403 says so by leaving can_respond_from null.
func TestP3DeputyWindow(t *testing.T) {
	f := newP2Fixture(t)
	deputy := f.addMember(t, "deputy@example.com", "Deputy")
	member := f.addMember(t, "m2@example.com", "M2")
	f.api.must(200, "PATCH", f.p+"/sessions/"+f.sessionID, map[string]any{"deputy_director_user_id": deputy.userID})

	tok, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	out := f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자"}, 201)
	id := str(out["hitl_request"].(map[string]any), "id")
	f.endTurn(t, taskID)

	// E7-11: a workspace member sees the card and has no response right, ever.
	st, body, _ := member.do("POST", f.p+"/hitl-requests/"+id+"/response",
		map[string]any{"answer": "경영진"}, "Idempotency-Key", uuid.NewString())
	if st != 403 {
		t.Fatalf("member response = %d %v, want 403", st, body)
	}
	if v, ok := body["can_respond_from"]; !ok || v != nil {
		t.Fatalf("can_respond_from = %v, want null — a member never becomes eligible (E7-11)", v)
	}

	// E7-09: the deputy before half the deadline, with the instant the UI shows.
	f.fake.Advance(11 * time.Hour)
	st, body, _ = deputy.do("POST", f.p+"/hitl-requests/"+id+"/response",
		map[string]any{"answer": "경영진"}, "Idempotency-Key", uuid.NewString())
	if st != 403 || str(body, "code") != "deputy_not_yet" {
		t.Fatalf("deputy at 11h = %d %v, want 403 deputy_not_yet", st, body)
	}
	if body["can_respond_from"] == nil {
		t.Fatal("the deputy's 403 carries the instant they may answer (E7-09)")
	}

	// E7-10: after half the deadline the deputy may answer. Notifying a deputy
	// without granting the right makes the notification useless.
	f.fake.Advance(2 * time.Hour)
	st, body, _ = deputy.do("POST", f.p+"/hitl-requests/"+id+"/response",
		map[string]any{"answer": "경영진"}, "Idempotency-Key", uuid.NewString())
	if st != 200 {
		t.Fatalf("deputy at 13h = %d %v, want 200 (FR-5.4 M7)", st, body)
	}
	if st := f.taskStatus(t, taskID); st != "queued" {
		t.Fatalf("task = %q, want queued", st)
	}
}

// TestP3HitlDeadlineSweep is E7-12·13·14·21: the type × autonomy grid, with the
// type deciding first.
func TestP3HitlDeadlineSweep(t *testing.T) {
	f := newP2Fixture(t)
	tok, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	out := f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자"}, 201)
	qid := str(out["hitl_request"].(map[string]any), "id")
	f.endTurn(t, taskID)

	// guided (the fixture default): the request stays OPEN and is flagged.
	f.fake.Advance(25 * time.Hour)
	if _, err := f.srv.SweepHitlDeadlines(t.Context()); err != nil {
		t.Fatal(err)
	}
	var status string
	var overdue bool
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, overdue FROM hitl_request WHERE id = $1`, qid).Scan(&status, &overdue); err != nil {
		t.Fatal(err)
	}
	if status != hitl.StatusOpen || !overdue {
		t.Fatalf("guided question = %s overdue=%t, want open + overdue (E7-13, `expired` is not a status)", status, overdue)
	}
	if st := f.taskStatus(t, taskID); st != "waiting_human" {
		t.Fatalf("task = %q, want waiting_human — nothing proceeded", st)
	}
	// E7-15: an overdue request can still be answered.
	f.api.must(200, "POST", f.p+"/hitl-requests/"+qid+"/response",
		map[string]any{"answer": "경영진"}, "Idempotency-Key", uuid.NewString())

	// autonomous: a question proceeds with the agent's proposal, and the
	// decision is marked automatic (E7-12).
	if _, err := f.pool.Exec(t.Context(), `UPDATE session SET autonomy = 'autonomous' WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	tok2, task2 := f.agentToken(t, f.sessionID, f.wUUID, "W")
	q2 := str(f.hitlOn(t, tok2, map[string]any{"type": "question", "question": "형식?", "proposed_default": "보고서"}, 201)["hitl_request"].(map[string]any), "id")
	f.endTurn(t, task2)
	// …and an approval in the SAME autonomy never proceeds (E7-14): type
	// decides before autonomy.
	tok3, task3 := f.agentToken(t, f.sessionID, f.leadUUID, "Lead")
	a3 := str(f.hitlOn(t, tok3, map[string]any{"type": "approval", "summary": "배포해도 될까요"}, 201)["hitl_request"].(map[string]any), "id")
	f.endTurn(t, task3)

	f.fake.Advance(25 * time.Hour)
	if _, err := f.srv.SweepHitlDeadlines(t.Context()); err != nil {
		t.Fatal(err)
	}
	var answer string
	var auto bool
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, COALESCE(answer, '') FROM hitl_request WHERE id = $1`, q2).Scan(&status, &answer); err != nil {
		t.Fatal(err)
	}
	if status != hitl.StatusAutoAnswered || answer != "보고서" {
		t.Fatalf("autonomous question = %s answer=%q, want auto_answered with the proposed default (E7-12)", status, answer)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT auto FROM decision WHERE ref_id = $1`, q2).Scan(&auto); err != nil {
		t.Fatal(err)
	}
	if !auto {
		t.Fatal("the decision is marked automatic — otherwise it reads as the Director's answer (E7-12)")
	}
	if st := f.taskStatus(t, task2); st != "queued" {
		t.Fatalf("task = %q, want queued — proceeding re-queues a new attempt", st)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM hitl_request WHERE id = $1`, a3).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != hitl.StatusOpen {
		t.Fatalf("autonomous approval = %s, want open — no auto-approve AND no auto-reject (E7-14)", status)
	}
	if st := f.taskStatus(t, task3); st != "waiting_human" {
		t.Fatalf("task = %q, want waiting_human (E7-14)", st)
	}
	var decisions int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM decision WHERE ref_id = $1`, a3).Scan(&decisions); err != nil {
		t.Fatal(err)
	}
	if decisions != 0 {
		t.Fatalf("decisions for the untouched approval = %d, want 0 — nothing was decided", decisions)
	}
}

// TestP3BudgetEnforcement is E9-01·02·08: the turn is stopped mid-flight, the
// task is PAUSED (not failed), the raise is task-scoped, and enforcement READS
// the override afterwards.
func TestP3BudgetEnforcement(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)

	// The daemon's heartbeat carries the turn's running usage (§4.2). $1.01 of
	// a $1 budget.
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), taskID, contracts.Usage{
		InputTokens: 1000, OutputTokens: 1000, CostUSD: 1.01,
	}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	var status, reason string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, COALESCE(paused_reason::text, '') FROM task WHERE id = $1`, taskID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || reason != "budget" {
		t.Fatalf("task = %s(%s), want paused(budget) — an overrun is a POLICY event (FR-7.3 M9)", status, reason)
	}
	// §8.2.2 through the daemon: a cancel COMMAND, not a kill.
	var cmds int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM daemon_command WHERE task_id = $1 AND type = 'cancel' AND payload->>'reason' = 'budget'
		  AND (payload->>'after_current_tool')::boolean`, taskID).Scan(&cmds); err != nil {
		t.Fatal(err)
	}
	if cmds != 1 {
		t.Fatalf("cancel commands = %d, want 1 with reason=budget and after_current_tool (§4.3)", cmds)
	}
	// The system HITL. `purpose` is what tells it from the completion approval.
	var hitlID, purpose, source, kind string
	var hitlTask *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id::text, purpose, source::text, type::text, task_id FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`,
		f.sessionID).Scan(&hitlID, &purpose, &source, &kind, &hitlTask); err != nil {
		t.Fatal(err)
	}
	if source != "system" || kind != "approval" || purpose != "budget" {
		t.Fatalf("hitl = %s/%s/%s, want system/approval/budget (E9-01)", source, kind, purpose)
	}
	if hitlTask == nil || *hitlTask != taskID {
		t.Fatalf("hitl task_id = %v, want %s — a task-budget HITL must name its task (FR-7.3 s-13)", hitlTask, taskID)
	}

	// E9-02: the raise is task-scoped and re-queues the SAME task.
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": true, "budget_override_usd": 3}, "Idempotency-Key", uuid.NewString())
	var override *float64
	var agentBudget float64
	if err := f.pool.QueryRow(t.Context(), `SELECT budget_override FROM task WHERE id = $1`, taskID).Scan(&override); err != nil {
		t.Fatal(err)
	}
	if override == nil || *override != 3 {
		t.Fatalf("task.budget_override = %v, want 3", override)
	}
	if err := f.pool.QueryRow(t.Context(), `SELECT budget_per_task FROM agent WHERE id = $1`, f.rUUID).Scan(&agentBudget); err != nil {
		t.Fatal(err)
	}
	if agentBudget != 1 {
		t.Fatalf("agent.budget_per_task = %v, want 1 — one click must not re-price every future session (C2′)", agentBudget)
	}
	if st := f.taskStatus(t, taskID); st != "queued" {
		t.Fatalf("task = %q after the approval, want queued", st)
	}

	// E9-08: enforcement READS the override. $1.50 is inside $3, so nothing
	// pauses — an implementation that only STORES the raise pauses again here.
	f.runTask(t, taskID)
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), taskID, contracts.Usage{CostUSD: 1.50}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	if st := f.taskStatus(t, taskID); st != "running" {
		t.Fatalf("task = %q at $1.50 with a $3 override, want running (E9-08)", st)
	}
}

// TestP3BudgetRejectionParks is E9-03: rejecting the raise does not fail or
// cancel the task — only the explicit 중단 button ends it.
func TestP3BudgetRejectionParks(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), taskID, contracts.Usage{CostUSD: 2}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
	var hitlID string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`, f.sessionID).Scan(&hitlID); err != nil {
		t.Fatal(err)
	}
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"approved": false, "reason": "여기까지만"}, "Idempotency-Key", uuid.NewString())
	var status, reason string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, COALESCE(paused_reason::text, '') FROM task WHERE id = $1`, taskID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || reason != "budget" {
		t.Fatalf("task = %s(%s) after a rejection, want paused(budget) still (E9-03)", status, reason)
	}
}

// TestP3SessionPauseResume is FR-2.3's five reasons: a Director pause drains,
// a runtime_offline pause cannot be resumed here, and a budget pause needs a
// higher limit.
func TestP3SessionPauseResume(t *testing.T) {
	f := newP2Fixture(t)
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/pause", nil)
	var status, reason string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`, f.sessionID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "paused" || reason != "director" {
		t.Fatalf("session = %s(%s), want paused(director)", status, reason)
	}
	// A second pause is a 409, not a silent no-op.
	if st, _, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/pause", nil); st != 409 {
		t.Fatalf("pause on a paused session = %d, want 409", st)
	}
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/resume", nil)

	// runtime_offline is the reason this endpoint cannot fix: nothing here
	// makes the machine reachable (openapi resumeSession).
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET status = 'paused', paused_reason = 'runtime_offline' WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	st, body, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/resume", nil)
	if st != 409 || str(body, "code") != "runtime_offline" {
		t.Fatalf("resume of a runtime_offline pause = %d %v, want 409 runtime_offline", st, body)
	}

	// A budget pause resumed on the old limit re-trips immediately, so the
	// server refuses it (FR-7.3).
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET paused_reason = 'budget', cost_usd = 5, limits = '{"budget_usd": 4}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	if st, body, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/resume", nil); st != 422 {
		t.Fatalf("budget resume without a raise = %d %v, want 422", st, body)
	}
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/resume", map[string]any{"limits": map[string]any{"budget_usd": 10}})
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM session WHERE id = $1`, f.sessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("session = %q after a raised limit, want active", status)
	}
}

// TestP3KillSwitch is E10-07·08·09: four verbs on four objects, and the held
// re-queue that re-enabling releases.
func TestP3KillSwitch(t *testing.T) {
	f := newP2Fixture(t)
	tok, waiting := f.agentToken(t, f.sessionID, f.rUUID, "R")
	out := f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자"}, 201)
	hitlID := str(out["hitl_request"].(map[string]any), "id")
	f.endTurn(t, waiting)
	// A second, running task for the same agent.
	_, running := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, running)

	f.api.must(200, "PATCH", f.p+"/agents/"+f.r, map[string]any{"respond_to": "nobody"})

	// The running turn is cancelled through §8.2.2 (a command, not a kill).
	var cancelCmds int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM daemon_command WHERE task_id = $1 AND type = 'cancel'`, running).Scan(&cancelCmds); err != nil {
		t.Fatal(err)
	}
	if cancelCmds != 1 {
		t.Fatalf("cancel commands for the running turn = %d, want 1 — a kill switch that only stops FUTURE work leaves the runaway turn running (FR-1.9 M8)", cancelCmds)
	}
	// The open request is KEPT: the human's chance to answer is not the
	// agent's to lose.
	var hitlStatus string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM hitl_request WHERE id = $1`, hitlID).Scan(&hitlStatus); err != nil {
		t.Fatal(err)
	}
	if hitlStatus != hitl.StatusOpen {
		t.Fatalf("hitl = %q under the kill switch, want open (M8 표 3행)", hitlStatus)
	}

	// E10-08: the answer is recorded and the re-queue is HELD.
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID+"/response",
		map[string]any{"answer": "경영진"}, "Idempotency-Key", uuid.NewString())
	var held bool
	if err := f.pool.QueryRow(t.Context(), `SELECT requeue_held, status::text FROM hitl_request WHERE id = $1`, hitlID).Scan(&held, &hitlStatus); err != nil {
		t.Fatal(err)
	}
	if hitlStatus != hitl.StatusAnswered || !held {
		t.Fatalf("hitl = %s held=%t, want answered with the re-queue held (E10-08)", hitlStatus, held)
	}
	if st := f.taskStatus(t, waiting); st == "queued" {
		t.Fatal("re-queueing restarts the agent the owner just disabled (E10-08)")
	}

	// …and re-enabling releases it.
	f.api.must(200, "PATCH", f.p+"/agents/"+f.r, map[string]any{"respond_to": "workspace"})
	if st := f.taskStatus(t, waiting); st != "queued" {
		t.Fatalf("task after re-enabling = %q, want queued (E10-08 '다시 활성화하면 그때 이어진다')", st)
	}
}

// TestP3CostRollup is E9-07·09: four units and a badge that is not always on.
func TestP3CostRollup(t *testing.T) {
	f := newP2Fixture(t)
	_, t1 := f.agentToken(t, f.sessionID, f.rUUID, "R")
	_, t2 := f.agentToken(t, f.sessionID, f.wUUID, "W")
	for _, r := range []struct {
		id  uuid.UUID
		usd float64
		est bool
	}{{t1, 0.40, false}, {t2, 0.25, false}} {
		if err := f.srv.Tasks.RecordTurnUsage(t.Context(), r.id, contracts.Usage{CostUSD: r.usd, Estimated: r.est}, f.fake.Now()); err != nil {
			t.Fatal(err)
		}
	}
	rep := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID+"/cost", nil)
	if rep["estimated"] != false {
		t.Fatal("every row is measured, so the report is measured — an always-true badge tells the reader nothing (E9-09)")
	}
	if v := rep["total_usd"].(float64); v < 0.6499 || v > 0.6501 {
		t.Fatalf("total = %v, want 0.65", v)
	}
	for _, k := range []string{"by_task", "by_agent", "by_session", "by_runtime"} {
		if _, ok := rep[k].([]any); !ok {
			t.Fatalf("%s missing — FR-7.3 names four units", k)
		}
	}
	if n := len(rep["by_task"].([]any)); n != 2 {
		t.Fatalf("by_task buckets = %d, want 2", n)
	}
	// One estimated row makes the whole report estimated (E9-07).
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), t2, contracts.Usage{CostUSD: 0, Estimated: true}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	rep = f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID+"/cost", nil)
	if rep["estimated"] != true {
		t.Fatal("a mixed sum cannot be presented as measured (E9-07)")
	}
}

// TestP3Inbox is FR-8 / SCREEN §4.6: severities, the badge and "전부 읽음"
// leaving action_required alone.
func TestP3Inbox(t *testing.T) {
	f := newP2Fixture(t)
	tok, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.hitlOn(t, tok, map[string]any{"type": "question", "question": "독자?", "proposed_default": "투자자"}, 201)
	f.endTurn(t, taskID)
	sum := f.api.must(200, "GET", f.p+"/inbox/summary?workspace_id="+f.wsID, nil)
	if sum["action_required"].(float64) < 1 {
		t.Fatalf("summary = %v, want at least one action_required (the open request)", sum)
	}
	all := f.api.must(200, "POST", f.p+"/inbox/read-all?workspace_id="+f.wsID, nil)
	_ = all
	sum = f.api.must(200, "GET", f.p+"/inbox/summary?workspace_id="+f.wsID, nil)
	if sum["action_required"].(float64) < 1 {
		t.Fatal("전부 읽음 must not clear action_required — it is still waiting for this person (openapi markAllInboxRead)")
	}
}

// runTask puts a task in the state a claim leaves it: running on the seed
// runtime, with an attempt row. The budget rows need a task the daemon is
// actually executing; going through a real claim would also work and says less.
func (f *p2Fixture) runTask(t *testing.T, taskID uuid.UUID) {
	t.Helper()
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	now := f.fake.Now()
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE task SET status = 'running', runtime_id = $2, dispatched_at = $3, started_at = $3, heartbeat_at = $3
		WHERE id = $1`, taskID, runtimeID, now); err != nil {
		t.Fatal(err)
	}
	var attempt int
	if err := f.pool.QueryRow(t.Context(), `SELECT attempt FROM task WHERE id = $1`, taskID).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO task_attempt (task_id, attempt, runtime_id, dispatched_at, started_at)
		VALUES ($1, $2, $3, $4, $4) ON CONFLICT DO NOTHING`, taskID, attempt, runtimeID, now); err != nil {
		t.Fatal(err)
	}
}

// endTurn is the daemon's end-of-turn report for a task that is actually
// running: the claim is simulated (runTask) and then `finish` arrives with
// outcome completed. The SERVER decides waiting_human from pending_hitl —
// the daemon never sends that outcome (daemon-protocol §4.4).
func (f *p2Fixture) endTurn(t *testing.T, taskID uuid.UUID) {
	t.Helper()
	f.runTask(t, taskID)
	if _, err := f.srv.Tasks.Finish(t.Context(), taskID, currentAttempt(t, f, taskID), contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
	}); err != nil {
		t.Fatal(err)
	}
}

func currentAttempt(t *testing.T, f *p2Fixture, taskID uuid.UUID) int {
	t.Helper()
	var a int
	if err := f.pool.QueryRow(t.Context(), `SELECT attempt FROM task WHERE id = $1`, taskID).Scan(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

// extraMember is a second person in the workspace with their own session
// cookie, so the permission rows are measured through a real caller.
type extraMember struct {
	*client
	userID string
}

func (f *p2Fixture) addMember(t *testing.T, email, name string) *extraMember {
	t.Helper()
	c := &client{t: t, srv: httptest.NewServer(f.srv.Handler())}
	t.Cleanup(c.srv.Close)
	_, _, hdr := c.do("POST", f.p+"/auth/signup", map[string]any{"display_name": name, "email": email, "password": "password123"})
	c.cookie = hdr.Get("Set-Cookie")
	var uid string
	if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM app_user WHERE email = $1`, email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'member', now())`,
		f.wsID, uid); err != nil {
		t.Fatal(err)
	}
	return &extraMember{client: c, userID: uid}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

var _ = tasks.CauseHitlAnswer
var _ = router.MentionLink
