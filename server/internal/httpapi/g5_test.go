// G5 결함 S-24~S-31 (T-I2 2부 보고서 §7). Each test names the defect it pins and
// the EVAL row the defect broke; every one of them fails on the code as it was.
package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// ---------------------------------------------------------------------------
// S-31 — 재진입한 lane 이 합류 그룹의 마지막으로 끝나면 합류가 사라진다
// ---------------------------------------------------------------------------

// The order EVAL walks (E3-05 → E3-06 → E3-07) answers the question from the
// join bundle, so the re-entered lane always finishes FIRST. Reverse it — the
// delegator answers the immediate notice at once, which is what E3-05 exists to
// make it do — and the re-entered child ends the group. `afterLaneDone` took
// the `reentry > 0` branch and returned, so nothing ever asked whether the
// group was complete: `join_fired_at` stayed null and FR-6.5's bundle vanished
// with no error anywhere.
func TestG5JoinFiresWhenReenteredLaneEndsLast(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))

	toR, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	toW, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.wUUID, Brief: "B 초안"})
	if err != nil {
		t.Fatal(err)
	}
	rTask := mustUUID(t, toR.Task.Id.String())
	res, err := f.srv.Router.SetAgentStatus(ctx, rTask, 1, "blocked", "범위가 어디까지인가요?")
	if err != nil {
		t.Fatal(err)
	}
	card := *res.QuestionMessageID

	// Lead answers the immediate notice straight away, mentioning the child as
	// the wake-up message tells it to (S-28). That re-enters R's lane.
	f.fake.Advance(time.Minute)
	reply, err := f.srv.Router.Post(ctx, sessionID,
		router.Author{Type: "agent", AgentID: &f.leadUUID, TaskID: &leadTask, Attempt: 1},
		gen.MessageCreate{
			Content:  router.MentionLink("R", f.rUUID) + " 국내만 보면 됩니다",
			ParentId: nullableUUID(card),
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(reply.Triggers) != 1 || reply.Triggers[0].AgentId != f.rUUID {
		t.Fatalf("triggers = %v, want R — the answer re-enters the blocked lane", reply.Triggers)
	}
	var reentered uuid.UUID
	var reentry int
	if err := f.pool.QueryRow(ctx, `SELECT id, reentry_count FROM lane WHERE id = $1`, toR.Lane.Id).Scan(&reentered, &reentry); err != nil {
		t.Fatal(err)
	}
	if reentry != 1 {
		t.Fatalf("reentry_count = %d, want 1 — lane rule 1 reuses the blocked lane", reentry)
	}
	reTask := mustUUID(t, reply.Triggers[0].TaskId.String())

	// The sibling ends first; the group is not complete yet.
	if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, toW.Task.Id.String()), 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	if joinFired(t, f, leadTask) {
		t.Fatal("the join fired while the re-entered lane is still running")
	}
	// …and now the RE-ENTERED lane completes it. This is the whole defect.
	if _, err := f.srv.Router.SetAgentStatus(ctx, reTask, 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	if !joinFired(t, f, leadTask) {
		t.Fatal("S-31: the re-entered lane ended the group — FR-6.5's bundle must still fire once")
	}
	var bundles int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM message
		WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%위임한 작업이 모두 끝났습니다%'`,
		f.sessionID).Scan(&bundles); err != nil {
		t.Fatal(err)
	}
	if bundles != 1 {
		t.Fatalf("join bundles = %d, want exactly 1 (FR-6.5 fires once per group)", bundles)
	}
	// Both notices exist — the re-entry report AND the join — and FR-3.4 merges
	// them onto ONE queued task for the delegator rather than two turns.
	var notices int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM message
		WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%요청하신 작업이 끝났습니다%'`,
		f.sessionID).Scan(&notices); err != nil {
		t.Fatal(err)
	}
	if notices != 1 {
		t.Fatalf("re-entry notices = %d, want 1 — the re-entry's author is still told", notices)
	}
	var leadQueued int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task WHERE session_id = $1 AND agent_id = $2 AND status = 'queued'`,
		f.sessionID, f.lead).Scan(&leadQueued); err != nil {
		t.Fatal(err)
	}
	if leadQueued != 1 {
		t.Fatalf("Lead queued tasks = %d, want 1 — FR-3.4 merges per lane", leadQueued)
	}
	for _, want := range []string{"위임한 작업이 모두 끝났습니다", "요청하신 작업이 끝났습니다"} {
		var attached int
		if err := f.pool.QueryRow(ctx, `
			SELECT count(*) FROM task t JOIN message m ON (m.id = t.trigger_message_id OR m.id = ANY (t.coalesced_message_ids))
			WHERE t.session_id = $1 AND t.agent_id = $2 AND t.status = 'queued' AND m.content LIKE '%' || $3 || '%'`,
			f.sessionID, f.lead, want).Scan(&attached); err != nil {
			t.Fatal(err)
		}
		if attached != 1 {
			t.Fatalf("%q reached the delegator's queued task %d times, want 1", want, attached)
		}
	}
}

// ---------------------------------------------------------------------------
// S-27 · S-28 — 질문 카드의 멘션과 기상 메시지의 인용
// ---------------------------------------------------------------------------

// S-27: the K3 badge is `질문 → @위임자` and the web reads the delegator out of
// message.mentions; the server posted the card with none, so the badge fell
// back to a bare `질문`. S-28: the wake-up said "답만 하고 턴을 끝내세요" and
// nothing else — no card id to reply to, no question body, and no word that an
// agent's reply without a mention wakes nobody (FR-3.3 rule 4), which is the
// rule that had silently swallowed a delegator's answer.
func TestG5BlockedCardMentionAndWakeQuote(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	leadTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))
	toR, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A 조사"})
	if err != nil {
		t.Fatal(err)
	}
	before := f.laneTaskCount(t, f.lead)

	const note = "경쟁 제품의 범위가 불명확합니다. 국내만인가요?"
	res, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, toR.Task.Id.String()), 1, "blocked", note)
	if err != nil {
		t.Fatal(err)
	}

	// S-27 — the card names the delegator, both in the body (FR-3.2 link form)
	// and in the stored mentions the web badge reads.
	var content string
	var mentions []byte
	if err := f.pool.QueryRow(ctx, `SELECT content, mentions FROM message WHERE id = $1`, *res.QuestionMessageID).
		Scan(&content, &mentions); err != nil {
		t.Fatal(err)
	}
	if !contains(content, router.MentionLink("Lead", f.leadUUID)) {
		t.Fatalf("blocked_q card = %q, want the delegator mention (S-27)", content)
	}
	if !contains(content, note) {
		t.Fatalf("blocked_q card = %q, want the question itself", content)
	}
	var parsed []gen.Mention
	if err := json.Unmarshal(mentions, &parsed); err != nil {
		t.Fatalf("card mentions are not a mention array: %s", mentions)
	}
	if len(parsed) != 1 || parsed[0].Id != f.lead {
		t.Fatalf("card mentions = %v, want exactly the delegator — the K3 badge reads this", parsed)
	}
	// lane.blocked_note stays the question ALONE: the join bundle re-reads it
	// as "질문: …" and a mention link inside that line reads as a new address.
	var blockedNote *string
	if err := f.pool.QueryRow(ctx, `SELECT blocked_note FROM lane WHERE id = $1`, toR.Lane.Id).Scan(&blockedNote); err != nil {
		t.Fatal(err)
	}
	if blockedNote == nil || *blockedNote != note {
		t.Fatalf("lane.blocked_note = %v, want the bare question", blockedNote)
	}

	// …and that mention triggers NOBODY. The card is inserted directly, not
	// routed, so the immediate wake-up stays the only trigger (openapi
	// setTaskStatus: "위임자를 즉시 깨운다", once).
	if after := f.laneTaskCount(t, f.lead); after != before {
		t.Fatalf("delegator tasks %d → %d: the card's mention must not route (즉시 기상 1회만)", before, after)
	}

	// S-28 — what the delegator actually wakes up on.
	var wake string
	if err := f.pool.QueryRow(ctx, `
		SELECT content FROM message
		WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%질문이 왔습니다%'
		ORDER BY created_at DESC LIMIT 1`, f.sessionID).Scan(&wake); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		res.QuestionMessageID.String(),   // the card id, so there is something to reply to
		note,                             // the question body, as the join bundle already carries
		"스레드 답글",                         // answer ON the card
		router.MentionLink("R", f.rUUID), // …mentioning the child
		"멘션 없는 에이전트 메시지는 아무도 깨우지 않으므로(FR-3.3 규칙 4)", // and why
	} {
		if !contains(wake, want) {
			t.Fatalf("wake-up message is missing %q:\n%s", want, wake)
		}
	}
}

// laneTaskCount counts every task an agent holds in the fixture's session.
func (f *p2Fixture) laneTaskCount(t *testing.T, agentID string) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM task WHERE session_id = $1 AND agent_id = $2`,
		f.sessionID, agentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func nullableUUID(id uuid.UUID) nullable.Nullable[openapi_types.UUID] {
	return nullable.NewNullableWithValue(openapi_types.UUID(id))
}

// ---------------------------------------------------------------------------
// S-29 — 완료 시 workdir GC (E6-03 마지막 칸)
// ---------------------------------------------------------------------------

// E6-03 ends with "`container`/`none` workdir 즉시 삭제". daemon-protocol §6
// puts the judgement on the SERVER — the daemon never deletes on its own — and
// the completed branch issued no `gc` at all, so a finished session left its
// directories on the machine for good.
func TestG5CompletedSessionCollectsWorkdirs(t *testing.T) {
	f := newG4Fixture(t)
	ctx := t.Context()

	if _, err := f.pool.Exec(ctx, `UPDATE session SET runtime_id = $2 WHERE id = $1`, f.sessionID, f.runtimeID); err != nil {
		t.Fatal(err)
	}
	var laneID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO lane (session_id, agent_id, profile_id, status, created_at, updated_at)
		SELECT $1, $2, p.profile_id, 'done', now(), now() FROM session_participant p
		WHERE p.session_id = $1 AND p.agent_id = $2 RETURNING id`, f.sessionID, f.r).Scan(&laneID); err != nil {
		t.Fatal(err)
	}
	var workdirID uuid.UUID
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO workdir (session_id, lane_id, kind, path_or_ref, status, created_at, updated_at)
		VALUES ($1, $2, 'dir', '/tmp/colab/wd-1', 'active', now(), now()) RETURNING id`, f.sessionID, laneID).Scan(&workdirID); err != nil {
		t.Fatal(err)
	}

	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/complete", map[string]any{"confirm": true})

	var gcs int
	var ids string
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*), coalesce(max(payload->>'workdir_ids'), '') FROM daemon_command
		WHERE session_id = $1 AND type = 'gc'`, f.sessionID).Scan(&gcs, &ids); err != nil {
		t.Fatal(err)
	}
	if gcs != 1 {
		t.Fatalf("gc commands after completion = %d, want 1 (S-29, daemon-protocol §6)", gcs)
	}
	if !contains(ids, workdirID.String()) {
		t.Fatalf("gc payload = %s, want the session's workdir %s", ids, workdirID)
	}
	// The row is still `active`: the server asked, it has not seen the result.
	var status string
	if err := f.pool.QueryRow(ctx, `SELECT status::text FROM workdir WHERE id = $1`, workdirID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("workdir status = %q before the daemon reports; want active — the server does not delete", status)
	}
	// …and the daemon's next §6 report, which no longer lists it, is the receipt.
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/workdirs", map[string]any{"workdirs": []any{}})
	if err := f.pool.QueryRow(ctx, `SELECT status::text FROM workdir WHERE id = $1`, workdirID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "deleted" {
		t.Fatalf("workdir status after the deletion report = %q, want deleted", status)
	}
	var pending int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_command WHERE session_id = $1 AND type = 'gc' AND consumed_at IS NULL`,
		f.sessionID).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("unconsumed gc commands = %d, want 0 once the workdir is gone (§4.3)", pending)
	}
}

// A `worktree` session is NOT collected on completion: one workdir is shared by
// all of an agent's lanes (C3) and holds a branch that may not be merged, so
// FR-6.4's retention policy owns it, not this path.
func TestG5WorktreeSessionIsNotCollectedOnCompletion(t *testing.T) {
	f := newG4Fixture(t)
	ctx := t.Context()
	if _, err := f.pool.Exec(ctx, `
		UPDATE session SET runtime_id = $2, isolation = '{"kind":"worktree","repo_path":"/repo"}' WHERE id = $1`,
		f.sessionID, f.runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO workdir (session_id, agent_id, kind, path_or_ref, status, created_at, updated_at)
		VALUES ($1, $2, 'worktree', '/repo/.wt/r', 'active', now(), now())`, f.sessionID, f.r); err != nil {
		t.Fatal(err)
	}
	f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/complete", map[string]any{"confirm": true})
	var gcs int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM daemon_command WHERE session_id = $1 AND type = 'gc'`, f.sessionID).Scan(&gcs); err != nil {
		t.Fatal(err)
	}
	if gcs != 0 {
		t.Fatalf("gc commands for a worktree session = %d, want 0 (C3 · FR-6.4 retention)", gcs)
	}
}

// ---------------------------------------------------------------------------
// S-25 — user_approval 을 충족시킬 P2 입구
// ---------------------------------------------------------------------------

// issueCompletionApproval walks the session to the point E6-01 describes: the
// designated agent has submitted, every other atom is met, and the PLATFORM has
// issued the approval request. It returns that request's id.
func (f *p2Fixture) issueCompletionApproval(t *testing.T) string {
	t.Helper()
	if _, err := f.srv.Sessions.ApplyCompletionEvent(t.Context(), mustUUID(t, f.sessionID),
		sessions.Event{Kind: "artifact_submit", Actor: f.leadUUID}); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id::text FROM hitl_request WHERE session_id = $1 AND source = 'system' AND purpose = 'user_approval'`,
		f.sessionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// E6-03: 승인 → active → completing → completed + `session_summary` 1개. Before
// this the only P2 entrance was `completeSession`, which satisfies `manual` —
// a different rule, measured in place of the one EVAL names (S-25).
func TestG5ApprovalCompletesSession(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)

	out := f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	if out["ignored"] != false {
		t.Fatalf("first response ignored = %v, want false", out["ignored"])
	}
	req, _ := out["hitl_request"].(map[string]any)
	if str(req, "status") != "answered" || req["approved"] != true {
		t.Fatalf("hitl_request = %v, want answered + approved", req)
	}

	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	if str(sess, "status") != "completed" {
		t.Fatalf("session status = %q, want completed (E6-03)", str(sess, "status"))
	}
	if met := f.completionMet(t); !met["user_approval"] {
		t.Fatalf("completion_met = %v, want user_approval satisfied — that is the atom S-25 could not reach", met)
	}
	if !metByProgress(sess, "user_approval") {
		t.Fatalf("completion_progress = %v, want user_approval met (S7 reads this)", sess["completion_progress"])
	}
	var summaries int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'summary'`, f.sessionID).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 1 {
		t.Fatalf("session_summary messages = %d, want exactly 1 (FR-2.4)", summaries)
	}
}

// E6-04: 거절 → 세션 `active` 유지 · `artifact_submitted` 유지 · 사유가 결정 기록에.
func TestG5ApprovalRejectionKeepsSessionActive(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)

	f.api.must(422, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": false}, "Idempotency-Key", uuid.NewString())

	out := f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": false, "reason": "3장이 비었습니다"}, "Idempotency-Key", uuid.NewString())
	if out["decision_id"] == nil {
		t.Fatal("a rejection records one decision (E6-04)")
	}
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	if str(sess, "status") != "active" {
		t.Fatalf("session status = %q, want active — a rejection ends nothing", str(sess, "status"))
	}
	if met := f.completionMet(t); !met["artifact_submitted"] || met["user_approval"] {
		t.Fatalf("completion_met = %v, want artifact_submitted preserved and user_approval unmet (E6-04)", met)
	}
	decisions := f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/decisions", nil)
	found := false
	for _, raw := range decisions {
		d := raw.(map[string]any)
		if str(d, "source") == "hitl" && str(d, "rationale") == "3장이 비었습니다" && str(d, "ref_id") == hitl {
			found = true
		}
	}
	if !found {
		t.Fatalf("decisions = %v, want the rejection reason with source hitl and the request as ref_id", decisions)
	}
}

// E7-08: 같은 요청에 대한 두 번째 응답은 오류가 아니라 무시 — 200 + ignored: true.
// The same Idempotency-Key replays the first answer instead (openapi.md §1).
func TestG5ApprovalSecondResponseIsIgnored(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)
	key := uuid.NewString()

	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response", map[string]any{"approved": true}, "Idempotency-Key", key)

	_, _, hdr := f.api.do("POST", f.p+"/hitl-requests/"+hitl+"/response", map[string]any{"approved": true}, "Idempotency-Key", key)
	if hdr.Get("Idempotent-Replayed") != "true" {
		t.Fatal("the same Idempotency-Key must replay, not re-answer")
	}
	out := f.api.must(200, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": false, "reason": "생각이 바뀌었습니다"}, "Idempotency-Key", uuid.NewString())
	if out["ignored"] != true {
		t.Fatalf("second response ignored = %v, want true (E7-08)", out["ignored"])
	}
	req, _ := out["hitl_request"].(map[string]any)
	if req["approved"] != true {
		t.Fatal("the answer that stands is the first one")
	}
	// …and the session did not un-complete.
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	if str(sess, "status") != "completed" {
		t.Fatalf("session status = %q after an ignored rejection, want completed", str(sess, "status"))
	}
}

// P3 (T-S5) opened the rest of respondHitlRequest. This row used to assert
// 501 for an agent's question, a budget override and a loop pause; those are
// now answered (FR-5.4, E7-07·E9-02), so the test asserts what replaced the
// 501 rather than being deleted — the boundary it was guarding still exists,
// it just moved to `time_extension` and to a budget override on a request that
// is not the budget one.
func TestP3OtherHitlRequestsAreAnswered(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	mk := func(purpose, source string, task *uuid.UUID) string {
		t.Helper()
		var id string
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at)
			VALUES ($1, $2, $3::hitl_source, 'approval', 'q', 'director', $4, now() + interval '1 day', now()) RETURNING id::text`,
			f.sessionID, task, source, purpose).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	agentTask := mustUUID(t, str(post["triggers"].([]any)[0].(map[string]any), "task_id"))

	for _, id := range []string{mk("budget", "system", nil), mk("loop", "system", nil), mk("agent", "agent", &agentTask)} {
		out := f.api.must(200, "POST", f.p+"/hitl-requests/"+id+"/response",
			map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
		req, _ := out["hitl_request"].(map[string]any)
		if str(req, "status") != "answered" {
			t.Fatalf("hitl %s status = %q, want answered", id, str(req, "status"))
		}
		if out["decision_id"] == nil {
			t.Fatalf("hitl %s recorded no decision (FR-5.2: exactly one per answer)", id)
		}
	}
	// budget_override_usd belongs to the budget request only: accepting it on
	// the completion approval would raise a limit nobody asked about (C2′).
	hitl := f.issueCompletionApproval(t)
	f.api.must(422, "POST", f.p+"/hitl-requests/"+hitl+"/response",
		map[string]any{"approved": true, "budget_override_usd": 10}, "Idempotency-Key", uuid.NewString())
	// The session time limit is not in the P3 server slice, and says so.
	f.api.must(501, "POST", f.p+"/hitl-requests/"+mk("time", "system", nil)+"/response",
		map[string]any{"approved": true, "time_extension": "PT1H"}, "Idempotency-Key", uuid.NewString())
}

// approver_spec `director` is authorisation, not decoration: another member of
// the workspace sees the card and cannot answer it (403), and a stranger does
// not learn the session exists (404).
func TestG5ApprovalPermissionBoundary(t *testing.T) {
	f := newP2Fixture(t)
	hitl := f.issueCompletionApproval(t)

	t.Run("member of the workspace is not the Director", func(t *testing.T) {
		other := &client{t: t, srv: httptest.NewServer(f.srv.Handler())}
		t.Cleanup(other.srv.Close)
		_, _, hdr := other.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "M2", "email": "m2@example.com", "password": "password123"})
		other.cookie = hdr.Get("Set-Cookie")
		var uid string
		if err := f.pool.QueryRow(t.Context(), `SELECT id::text FROM app_user WHERE email = 'm2@example.com'`).Scan(&uid); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(t.Context(), `INSERT INTO member (workspace_id, user_id, role, created_at) VALUES ($1, $2, 'member', now())`,
			f.wsID, uid); err != nil {
			t.Fatal(err)
		}
		other.must(403, "POST", f.p+"/hitl-requests/"+hitl+"/response",
			map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	})

	t.Run("outsider gets 404, not the session's shape", func(t *testing.T) {
		out := &client{t: t, srv: httptest.NewServer(f.srv.Handler())}
		t.Cleanup(out.srv.Close)
		_, _, hdr := out.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "X", "email": "x@example.com", "password": "password123"})
		out.cookie = hdr.Get("Set-Cookie")
		out.must(404, "POST", f.p+"/hitl-requests/"+hitl+"/response",
			map[string]any{"approved": true}, "Idempotency-Key", uuid.NewString())
	})

	t.Run("the request is still open after both refusals", func(t *testing.T) {
		var status string
		if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM hitl_request WHERE id = $1`, hitl).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != "open" {
			t.Fatalf("hitl status = %q after refused responses, want open", status)
		}
	})
}

// completionMet reads session.completion_met — the flags themselves, which the
// REST body only exposes folded into completion_progress.
func (f *p2Fixture) completionMet(t *testing.T) map[string]bool {
	t.Helper()
	met := map[string]bool{}
	var raw []byte
	if err := f.pool.QueryRow(t.Context(), `SELECT completion_met FROM session WHERE id = $1`, f.sessionID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &met); err != nil {
		t.Fatalf("completion_met is not an object: %s", raw)
	}
	return met
}

// metByProgress reads one atom out of the session body's completion_progress —
// the shape S7 draws the bar from.
func metByProgress(sess map[string]any, typ string) bool {
	prog, _ := sess["completion_progress"].(map[string]any)
	conds, _ := prog["conditions"].([]any)
	for _, raw := range conds {
		c := raw.(map[string]any)
		if str(c, "type") == typ && c["met"] == true {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// S-24 · S-30 — 폴백 프로파일과 프로파일 op
// ---------------------------------------------------------------------------

// createAgent accepted `fallback_profile` / `fallback_profile_id` and dropped
// both — the INSERT column list had no fallback_profile_id — so E8-08 had to
// write the link straight into the database to measure profile fallback at all.
func TestG5CreateAgentStoresFallbackProfile(t *testing.T) {
	f := newP2Fixture(t)
	a := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "Fallbacker", "role": "researcher", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{
			{"name": "primary", "runtime_kind": "hermes", "model": "claude-haiku-4-5-TYPO", "is_default": true, "fallback_profile": "spare"},
			{"name": "spare", "runtime_kind": "claude_code", "model": "claude-sonnet-5"},
		},
	})
	byName := map[string]map[string]any{}
	for _, raw := range a["profiles"].([]any) {
		p := raw.(map[string]any)
		byName[str(p, "name")] = p
	}
	if got, want := str(byName["primary"], "fallback_profile_id"), str(byName["spare"], "id"); got != want {
		t.Fatalf("primary.fallback_profile_id = %q, want spare %q (S-24)", got, want)
	}
	if byName["spare"]["fallback_profile_id"] != nil {
		t.Fatalf("spare.fallback_profile_id = %v, want null", byName["spare"]["fallback_profile_id"])
	}
	// A name nothing in the request defines is a 422, not a silent drop — the
	// silent drop is the whole defect.
	f.api.must(422, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "Broken", "role": "researcher", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "primary", "runtime_kind": "hermes", "model": "m", "fallback_profile": "nope"}},
	})
}

// createAgentProfile · updateAgentProfile are x-phase P2 and were 501. That is
// also S-30: `applyAgentTemplate` registers an agent whose mapping failed with
// NO profile at all (openapi says to), and there was no operation left that
// could give it one — the three agents a template made were unusable for good.
func TestG5ProfileOperations(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()

	// The S-30 shape: an agent with zero profiles, exactly as an unmapped
	// template agent is left.
	var unmapped string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, role, role_description, instructions, owner_id, definition_source, created_at, updated_at)
		SELECT $1, 'Unmapped', 'researcher', 'd', 'i', m.user_id, 'research_team', now(), now()
		FROM member m WHERE m.workspace_id = $1 LIMIT 1 RETURNING id::text`, f.wsID).Scan(&unmapped); err != nil {
		t.Fatal(err)
	}
	got := f.api.must(200, "GET", f.p+"/agents/"+unmapped, nil)
	if len(got["profiles"].([]any)) != 0 {
		t.Fatal("premise: the unmapped agent starts with no profile")
	}

	first := f.api.must(201, "POST", f.p+"/agents/"+unmapped+"/profiles", map[string]any{
		"name": "default", "runtime_kind": "claude_code", "model": "claude-sonnet-5", "is_default": true,
	})
	if first["is_default"] != true {
		t.Fatalf("first profile is_default = %v, want true", first["is_default"])
	}
	second := f.api.must(201, "POST", f.p+"/agents/"+unmapped+"/profiles", map[string]any{
		"name": "spare", "runtime_kind": "hermes", "model": "claude-haiku-4-5-20251001",
		"fallback_profile_id": str(first, "id"),
	})
	if str(second, "fallback_profile_id") != str(first, "id") {
		t.Fatalf("fallback_profile_id = %q, want %q", str(second, "fallback_profile_id"), str(first, "id"))
	}
	// The agent can now be used: it has a default profile again (S-30 closed).
	got = f.api.must(200, "GET", f.p+"/agents/"+unmapped, nil)
	if len(got["profiles"].([]any)) != 2 {
		t.Fatalf("profiles = %v, want 2", got["profiles"])
	}

	// updateAgentProfile: the fallback link E8-08 had to write by hand.
	up := f.api.must(200, "PATCH", f.p+"/agents/"+unmapped+"/profiles/"+str(first, "id"), map[string]any{
		"fallback_profile_id": str(second, "id"), "model": "claude-opus-5",
	})
	if str(up, "fallback_profile_id") != str(second, "id") || str(up, "model") != "claude-opus-5" {
		t.Fatalf("update = %v, want the fallback link and the new model", up)
	}
	// …and it can be cleared.
	up = f.api.must(200, "PATCH", f.p+"/agents/"+unmapped+"/profiles/"+str(first, "id"), map[string]any{"fallback_profile_id": nil})
	if up["fallback_profile_id"] != nil {
		t.Fatalf("fallback_profile_id = %v after an explicit null, want null", up["fallback_profile_id"])
	}

	t.Run("a fallback must be another profile of the same agent", func(t *testing.T) {
		// Another agent's profile: following it would run this agent on a
		// profile nobody granted it (openapi updateAgentProfile → 422).
		other := f.api.must(200, "GET", f.p+"/agents/"+f.r, nil)
		alien := str(other["profiles"].([]any)[0].(map[string]any), "id")
		f.api.must(422, "PATCH", f.p+"/agents/"+unmapped+"/profiles/"+str(first, "id"),
			map[string]any{"fallback_profile_id": alien})
		f.api.must(422, "PATCH", f.p+"/agents/"+unmapped+"/profiles/"+str(first, "id"),
			map[string]any{"fallback_profile_id": str(first, "id")})
	})

	t.Run("switching the default moves it rather than duplicating it", func(t *testing.T) {
		f.api.must(200, "PATCH", f.p+"/agents/"+unmapped+"/profiles/"+str(second, "id"), map[string]any{"is_default": true})
		var defaults int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM agent_profile WHERE agent_id = $1 AND is_default`, unmapped).Scan(&defaults); err != nil {
			t.Fatal(err)
		}
		if defaults != 1 {
			t.Fatalf("default profiles = %d, want exactly 1", defaults)
		}
	})

	t.Run("a duplicate name is 409, an unknown profile 404", func(t *testing.T) {
		f.api.must(409, "POST", f.p+"/agents/"+unmapped+"/profiles",
			map[string]any{"name": "spare", "runtime_kind": "claude_code", "model": "m"})
		f.api.must(404, "PATCH", f.p+"/agents/"+unmapped+"/profiles/"+uuid.NewString(), map[string]any{"model": "m"})
	})

	t.Run("editing a profile is an owner-level act", func(t *testing.T) {
		outsider := &client{t: t, srv: httptest.NewServer(f.srv.Handler())}
		t.Cleanup(outsider.srv.Close)
		_, _, hdr := outsider.do("POST", f.p+"/auth/signup", map[string]any{"display_name": "Y", "email": "y@example.com", "password": "password123"})
		outsider.cookie = hdr.Get("Set-Cookie")
		outsider.must(404, "POST", f.p+"/agents/"+unmapped+"/profiles",
			map[string]any{"name": "sneak", "runtime_kind": "claude_code", "model": "m"})
	})
}

// ---------------------------------------------------------------------------
// S-26 — 설정 부분 갱신이 같은 객체의 다른 키를 지운다
// ---------------------------------------------------------------------------

// PATCH is per key. `mergeJSON` marshalled the partial object and ASSIGNED it,
// so lowering one loop limit set the other two to null: the router silently
// fell back to DefaultLimits() and S14 showed null where an admin had set a
// value. Changing one of the three limits is the normal case (E4-03 does it).
func TestG5SettingsPartialUpdateKeepsSiblingKeys(t *testing.T) {
	f := newP2Fixture(t)
	base := f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits":   map[string]any{"max_chain_depth": 7, "max_hops_per_hour": 40, "max_pair_roundtrips": 5},
		"budget_policy": map[string]any{"default_task_budget_usd": 2.5, "default_session_budget_usd": 20},
	})
	if l, _ := base["loop_limits"].(map[string]any); l["max_chain_depth"] != float64(7) {
		t.Fatalf("premise: loop_limits = %v", base["loop_limits"])
	}

	out := f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_pair_roundtrips": 2},
	})
	l, _ := out["loop_limits"].(map[string]any)
	if l["max_pair_roundtrips"] != float64(2) {
		t.Fatalf("max_pair_roundtrips = %v, want the new 2", l["max_pair_roundtrips"])
	}
	if l["max_chain_depth"] != float64(7) || l["max_hops_per_hour"] != float64(40) {
		t.Fatalf("loop_limits = %v — the keys the request did not name were erased (S-26)", l)
	}
	// A different group is untouched, and its own keys survive the same way.
	b, _ := out["budget_policy"].(map[string]any)
	if b["default_task_budget_usd"] != 2.5 {
		t.Fatalf("budget_policy = %v, want it untouched by a loop_limits patch", b)
	}
	out = f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"budget_policy": map[string]any{"default_task_budget_usd": 4},
	})
	b, _ = out["budget_policy"].(map[string]any)
	if b["default_session_budget_usd"] != float64(20) {
		t.Fatalf("budget_policy = %v — a sibling key of the same object was erased", b)
	}
	// The reload agrees with the response (S14 reads this).
	reload := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/settings", nil)
	if rl, _ := reload["loop_limits"].(map[string]any); rl["max_chain_depth"] != float64(7) {
		t.Fatalf("reloaded loop_limits = %v, want max_chain_depth 7", rl)
	}
}
