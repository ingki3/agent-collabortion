package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// TestP2DelegationJoinAndBlocked walks the delegation model end to end, because
// its three halves only make sense together: rule 8 hides the child's mentions,
// `status set blocked` is the escape hatch that lets it ask anyway, and the
// join is the one path a result travels back on.
func TestP2DelegationJoinAndBlocked(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	sessionID := mustUUID(t, f.sessionID)

	// Lead gets a task the human way, and delegates from it.
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
	if toR.Lane.Id == toW.Lane.Id {
		t.Fatal("lane rule 2:每 delegation gets its own lane")
	}
	// Same agent, second delegation → a second lane, not a reuse (E2-03).
	again, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{AgentID: f.rUUID, Brief: "A2"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Lane.Id == toR.Lane.Id {
		t.Fatal("a second delegation to the same agent must fork a lane (E2-03)")
	}
	if !again.Lane.DelegatedFromTaskId.IsSpecified() || again.Lane.DelegatedFromTaskId.IsNull() {
		t.Fatal("delegated_from_task_id is the join-group key and must be set")
	}
	// The target has to be a participant — FR-1.9 makes participation the grant.
	stranger := f.api.must(201, "POST", f.p+"/workspaces/"+f.wsID+"/agents", map[string]any{
		"name": "X", "role": "custom", "role_description": "d", "instructions": "i",
		"profiles": []map[string]any{{"name": "default", "runtime_kind": "claude_code", "model": "m"}},
	})
	if _, err := f.srv.Router.Delegate(ctx, leadTask, router.DelegateInput{
		AgentID: mustUUID(t, str(stranger, "id")), Brief: "몰래"}); err == nil {
		t.Fatal("delegating to a non-participant must fail — an agent cannot grant itself help")
	}

	// Rule 8: while the group has not fired, R mentioning Lead posts but does
	// not wake it. The message still exists — it rides the bundle.
	rTask := mustUUID(t, toR.Task.Id.String())
	author := router.Author{Type: "agent", AgentID: &f.rUUID, TaskID: &rTask, Attempt: 1}
	out, err := f.srv.Router.Post(ctx, sessionID, author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 완료했습니다",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Triggers) != 0 {
		t.Fatalf("triggers = %v, want 0 — rule 8 suppresses the delegator", out.Triggers)
	}
	// …but a third party mentioned alongside is NOT suppressed.
	out, err = f.srv.Router.Post(ctx, sessionID, author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " " + router.MentionLink("QA", f.wUUID) + " 확인",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Triggers) != 1 || out.Triggers[0].AgentId != f.wUUID {
		t.Fatalf("triggers = %v, want exactly W — suppression is scoped to the delegator", out.Triggers)
	}

	// blocked: the child asks anyway, and the delegator is woken immediately.
	res, err := f.srv.Router.SetAgentStatus(ctx, rTask, 1, "blocked", "범위가 어디까지인가요?")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TurnEndRequired {
		t.Error("blocked ends the turn (contracts setTaskStatus)")
	}
	if res.QuestionMessageID == nil {
		t.Fatal("blocked must post a question card")
	}
	var laneStatus string
	var blockedMsg *uuid.UUID
	var blockedNote *string
	if err := f.pool.QueryRow(ctx, `SELECT status::text, blocked_message_id, blocked_note FROM lane WHERE id = $1`, toR.Lane.Id).
		Scan(&laneStatus, &blockedMsg, &blockedNote); err != nil {
		t.Fatal(err)
	}
	if laneStatus != "blocked" || blockedMsg == nil || *blockedMsg != *res.QuestionMessageID {
		t.Fatalf("lane = %s, blocked_message_id = %v, want blocked + the question card", laneStatus, blockedMsg)
	}
	var kind string
	if err := f.pool.QueryRow(ctx, `SELECT kind::text FROM message WHERE id = $1`, *res.QuestionMessageID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "blocked_q" {
		t.Fatalf("question card kind = %q, want blocked_q", kind)
	}
	// The delegator is woken NOW, not at join time. Lead already has a queued
	// task, so FR-3.4 merges the wake-up into it rather than making a second
	// one — the proof is that the notice reaches that task either way.
	var woken int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM task t
		WHERE t.session_id = $1 AND t.agent_id = $2 AND t.status = 'queued'
		  AND EXISTS (
		        SELECT 1 FROM message m
		        WHERE m.author_type = 'system' AND m.session_id = $1
		          AND (m.id = t.trigger_message_id OR m.id = ANY (t.coalesced_message_ids)))`,
		f.sessionID, f.lead).Scan(&woken); err != nil {
		t.Fatal(err)
	}
	if woken == 0 {
		t.Fatal("blocked must wake the delegator immediately — not at join time")
	}

	// The join waits for W and R's second lane; blocked counts as ended.
	wTask := mustUUID(t, toW.Task.Id.String())
	if _, err := f.srv.Router.SetAgentStatus(ctx, wTask, 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	if fired := joinFired(t, f, leadTask); fired {
		t.Fatal("the join fired while R's second lane is still queued")
	}
	if _, err := f.srv.Router.SetAgentStatus(ctx, mustUUID(t, again.Task.Id.String()), 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	if !joinFired(t, f, leadTask) {
		t.Fatal("every child has ended — the join must fire")
	}
	var bundles int
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM message
		WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%위임한 작업이 모두 끝났습니다%'`,
		f.sessionID).Scan(&bundles); err != nil {
		t.Fatal(err)
	}
	if bundles != 1 {
		t.Fatalf("join bundles = %d, want exactly 1", bundles)
	}
	var bundle string
	if err := f.pool.QueryRow(ctx, `
		SELECT content FROM message
		WHERE session_id = $1 AND content LIKE '%위임한 작업이 모두 끝났습니다%' LIMIT 1`, f.sessionID).Scan(&bundle); err != nil {
		t.Fatal(err)
	}
	// t-8: a delegator that missed the immediate notice must not close the
	// group without answering. The count is what stops it.
	if !contains(bundle, "답을 기다리는 자식 1개") {
		t.Fatalf("join bundle = %q, want the unanswered-question count (t-8)", bundle)
	}
	if !contains(bundle, "blocked") {
		t.Fatalf("join bundle = %q, want the blocked child listed", bundle)
	}

	// After the join, rule 8's suppression is over: the same mention now wakes
	// Lead, so a re-entered child is not stranded.
	out, err = f.srv.Router.Post(ctx, sessionID, author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 추가 질문",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Triggers) != 1 || out.Triggers[0].AgentId != f.leadUUID {
		t.Fatalf("triggers = %v, want Lead — suppression ends when the group fires (E1-17)", out.Triggers)
	}

	// One bundle per group: another child finishing does not re-fire it.
	if _, err := f.srv.Router.SetAgentStatus(ctx, wTask, 1, "done", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `
		SELECT count(*) FROM message
		WHERE session_id = $1 AND author_type = 'system' AND content LIKE '%위임한 작업이 모두 끝났습니다%'`,
		f.sessionID).Scan(&bundles); err != nil {
		t.Fatal(err)
	}
	if bundles != 1 {
		t.Fatalf("join bundles after a second done = %d, want still 1 — the join fires once per group (E)", bundles)
	}
}

func joinFired(t *testing.T, f *p2Fixture, delegTask uuid.UUID) bool {
	t.Helper()
	var at *string
	if err := f.pool.QueryRow(t.Context(), `SELECT join_fired_at::text FROM task WHERE id = $1`, delegTask).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at != nil
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestP2CompletionManual is E6-08 plus the guard that ending a session with
// work in flight is a deliberate act.
func TestP2CompletionManual(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})

	st, out, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/complete", map[string]any{})
	if st != 409 || str(out, "code") != "running_lanes" {
		t.Fatalf("complete with a queued lane = %d %v, want 409 running_lanes", st, out)
	}
	if n, ok := out["running_lane_count"].(float64); !ok || int(n) < 1 {
		t.Fatalf("409 must say how much work is at stake, got %v", out["running_lane_count"])
	}

	done := f.api.must(200, "POST", f.p+"/sessions/"+f.sessionID+"/complete", map[string]any{"confirm": true})
	if str(done, "status") != "completed" {
		t.Fatalf("session = %v, want completed", str(done, "status"))
	}
	var summaries int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM message WHERE session_id = $1 AND kind = 'summary'`, f.sessionID).Scan(&summaries); err != nil {
		t.Fatal(err)
	}
	if summaries != 1 {
		t.Fatalf("session_summary messages = %d, want exactly 1 (FR-2.4)", summaries)
	}
	var live int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM task WHERE session_id = $1 AND status IN ('queued', 'deferred')`, f.sessionID).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Fatalf("queued tasks after completion = %d, want 0 — a resumed daemon must not pick them up", live)
	}
	// A closed session stays closed.
	if st, _, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/complete", map[string]any{"confirm": true}); st != 409 {
		t.Fatalf("completing twice = %d, want 409", st)
	}
}

// TestP2CompletionTreeValidation is E6-07's asymmetry: criteria_met may not be
// the judge of its own work, agent_approval may.
func TestP2CompletionTreeValidation(t *testing.T) {
	f := newP2Fixture(t)
	create := func(cond map[string]any) (int, map[string]any) {
		body := map[string]any{
			"title": "S", "goal": "g", "isolation": map[string]any{"kind": "none"},
			"participants":         []map[string]any{{"agent_id": f.lead}},
			"completion_condition": cond,
		}
		st, out, _ := f.api.do("POST", f.p+"/workspaces/"+f.wsID+"/sessions", body)
		return st, out
	}
	if st, out := create(map[string]any{"op": "and", "conditions": []map[string]any{{"type": "criteria_met"}}}); st != 422 {
		t.Fatalf("criteria_met alone = %d %v, want 422", st, out)
	}
	// OR is the subtler case: `criteria_met OR user_approval` lets the platform
	// close the session by itself, which is the very thing FR-2.2 forbids.
	if st, out := create(map[string]any{"op": "or", "conditions": []map[string]any{
		{"type": "criteria_met"}, {"type": "user_approval"}}}); st != 422 {
		t.Fatalf("criteria_met OR user_approval = %d %v, want 422", st, out)
	}
	if st, out := create(map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "criteria_met"}, {"type": "user_approval"}}}); st != 201 {
		t.Fatalf("criteria_met AND user_approval = %d %v, want 201", st, out)
	}
	if st, out := create(map[string]any{"op": "and", "conditions": []map[string]any{
		{"type": "agent_approval", "agent_id": f.r}}}); st != 201 {
		t.Fatalf("agent_approval alone = %d %v, want 201 — a different role reviews", st, out)
	}
}

// TestP2DecisionLog is FR-4.2.
func TestP2DecisionLog(t *testing.T) {
	f := newP2Fixture(t)
	f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/decisions", map[string]any{
		"summary": "API 응답은 JSON", "rationale": "클라이언트가 셋 다 JSON을 쓴다",
	})
	out := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID+"/decisions", nil)
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("decisions = %d, want 1", len(items))
	}
	d := items[0].(map[string]any)
	if str(d, "summary") != "API 응답은 JSON" || str(d, "source") != "hitl" {
		t.Fatalf("decision = %v", d)
	}
	if st, _, _ := f.api.do("POST", f.p+"/sessions/"+f.sessionID+"/decisions", map[string]any{"summary": "  "}); st != 422 {
		t.Fatalf("blank summary = %d, want 422", st)
	}
}
