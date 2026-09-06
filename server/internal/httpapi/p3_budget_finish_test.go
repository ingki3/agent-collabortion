package httpapi

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// S-44 — the budget is enforced at `finish`, not only mid-turn.
//
// T-I3 measured a session where the agent's budget_per_task was $0.002 and the
// turn cost $0.0599, and the task came out `completed`, the session `active`,
// with zero HITL and zero cancel commands. The cause was one missing call:
// enforceBudgetFor ran on the heartbeat's `usage` and nowhere else, so a
// runtime that reports its usage only in the finish body (D-17) was never
// measured against any limit. FR-7.3 M9 says the check exists precisely
// because "task 사이에서만 검사하면 단일 task 가 크게 초과" — the post-turn
// check cannot un-spend that money, but it is what stops the NEXT task from
// spending the same way.
//
// The post-turn case differs from E9-01 in one thing: the task is terminal.
// `completed → paused` is not an edge the state machine has (E5) and the
// session has already acted on the completion, so the LANE takes the pause and
// the HITL still names the task that crossed the line (FR-7.3 s-13).

// finishTurn reports an end-of-turn through the same function the daemon's
// finish handler calls, so the enforcement wiring is part of what is measured.
func (f *p2Fixture) finishTurn(t *testing.T, taskID uuid.UUID, fin contracts.Finish) {
	t.Helper()
	f.runTask(t, taskID)
	if _, err := f.srv.finishAndEnforce(t.Context(), taskID, currentAttempt(t, f, taskID), fin); err != nil {
		t.Fatal(err)
	}
}

func (f *p2Fixture) laneOf(t *testing.T, taskID uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	var id uuid.UUID
	var status string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT l.id, l.status::text FROM lane l JOIN task t ON t.lane_id = l.id WHERE t.id = $1`, taskID).
		Scan(&id, &status); err != nil {
		t.Fatal(err)
	}
	return id, status
}

// claimed is every task the queue hands this runtime right now.
func (f *p2Fixture) claimed(t *testing.T) map[string]*float64 {
	t.Helper()
	var runtimeID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT id FROM runtime WHERE workspace_id = $1 LIMIT 1`, f.wsID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	bundles, err := f.srv.Queue.Claim(t.Context(), runtimeID.String(), 10, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*float64{}
	for _, b := range bundles {
		out[b.Task.ID] = b.Task.BudgetOverrideUSD
	}
	return out
}

func has(claimed map[string]*float64, id uuid.UUID) bool {
	_, ok := claimed[id.String()]
	return ok
}

// TestP3BudgetEnforcedAtFinish is the S-44 regression: a task budget crossed
// by a usage report that only arrives with `finish`.
func TestP3BudgetEnforcedAtFinish(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")

	// No heartbeat ever carried usage — this is the D-17 daemon. The whole
	// turn is priced in the finish body (§4.4).
	f.finishTurn(t, taskID, contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 1.01},
	})

	var status string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM task WHERE id = $1`, taskID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "completed" {
		t.Fatalf("task = %s, want completed — the turn ended before the overrun was found, and completed has no edge to paused (E5)", status)
	}
	laneID, laneStatus := f.laneOf(t, taskID)
	if laneStatus != "paused" {
		t.Fatalf("lane = %s, want paused — the per-task budget is crossed and the NEXT task on this lane is what is left to stop (FR-7.3 M9)", laneStatus)
	}
	// §8.2.2 is about a turn in flight. There is none, so no cancel command.
	var cmds int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM daemon_command WHERE task_id = $1 AND type = 'cancel'`, taskID).Scan(&cmds); err != nil {
		t.Fatal(err)
	}
	if cmds != 0 {
		t.Fatalf("cancel commands = %d, want 0 — cancelling a turn that already ended asks the daemon to stop nothing", cmds)
	}
	var hitlID uuid.UUID
	var source, kind, purpose string
	var hitlTask *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id, source::text, type::text, purpose, task_id FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`,
		f.sessionID).Scan(&hitlID, &source, &kind, &purpose, &hitlTask); err != nil {
		t.Fatalf("no budget HITL after the finish overrun: %v", err)
	}
	if source != "system" || kind != "approval" {
		t.Fatalf("hitl = %s/%s, want system/approval (E9-01)", source, kind)
	}
	if hitlTask == nil || *hitlTask != taskID {
		t.Fatalf("hitl task_id = %v, want %s — a task-budget HITL must name its task (FR-7.3 s-13)", hitlTask, taskID)
	}

	// The gate is the lane: a new task for the same agent lands on it (rule 3)
	// and must not be handed out.
	f.fake.Advance(time.Minute)
	out := f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/messages",
		map[string]any{"content": router.MentionLink("R", f.rUUID) + " 하나만 더"},
		"Idempotency-Key", uuid.NewString())
	next := uuid.Nil
	for _, raw := range out["triggers"].([]any) {
		tr := raw.(map[string]any)
		if str(tr, "agent_id") == f.r {
			next = mustUUID(t, str(tr, "task_id"))
		}
	}
	if next == uuid.Nil {
		t.Fatalf("no follow-up task for R: %v", out["triggers"])
	}
	if lane, _ := f.laneOf(t, next); lane != laneID {
		t.Fatalf("the follow-up landed on lane %s, want the paused one %s — rule 3 reuses the most recent lane", lane, laneID)
	}
	if _, st := f.laneOf(t, next); st != "paused" {
		t.Fatalf("lane = %s after the follow-up landed on it, want paused still — a trigger must not lift a budget gate", st)
	}
	// The other lanes of this session are inside their own budgets and keep
	// running — a per-task overrun must not stop them (FR-7.3, E9-01).
	if got := f.claimed(t); has(got, next) {
		t.Fatalf("the queue handed out the follow-up task while its lane is paused for budget: %v (FR-7.3, C3′)", got)
	}

	// The Director raises the limit. The task the request names is finished, so
	// the approval has nothing to resume — what it must lift is the lane.
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID.String()+"/response",
		map[string]any{"approved": true, "budget_override_usd": 3}, "Idempotency-Key", uuid.NewString())
	if _, laneStatus := f.laneOf(t, taskID); laneStatus == "paused" {
		t.Fatal("the lane is still paused after an approved raise — the approval bought nothing (E9-02)")
	}
	var agentBudget float64
	if err := f.pool.QueryRow(t.Context(), `SELECT budget_per_task FROM agent WHERE id = $1`, f.rUUID).Scan(&agentBudget); err != nil {
		t.Fatal(err)
	}
	if agentBudget != 1 {
		t.Fatalf("agent.budget_per_task = %v, want 1 — one click must not re-price every future session (C2′)", agentBudget)
	}
	got := f.claimed(t)
	override, ok := got[next.String()]
	if !ok {
		t.Fatalf("the follow-up task was not dispatched after the raise, got %v", got)
	}
	if override == nil || *override != 3 {
		t.Fatalf("bundle budget_override_usd = %v, want 3 — the raise was granted on this lane and the finished task cannot carry it", override)
	}
}

// TestP3BudgetAtFinishSessionScope is the other half: what the finish crosses
// is the SESSION budget. Then the pause is the session's, the HITL names no
// task (it is answered by resumeSession), and nothing else dispatches.
func TestP3BudgetAtFinishSessionScope(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	// No per-task budget — the column is nullable and most agents leave it, so
	// the session remainder is the only ceiling this task has (D-16).
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	// A second lane that is inside its own budget and still queued.
	_, other := f.agentToken(t, f.sessionID, f.wUUID, "W")

	f.finishTurn(t, taskID, contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{InputTokens: 2000, OutputTokens: 2000, CostUSD: 1.25},
	})

	var sessionStatus, reason string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`, f.sessionID).
		Scan(&sessionStatus, &reason); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "paused" || reason != "budget" {
		t.Fatalf("session = %s(%s), want paused(budget) — the session budget is gone (E9-04)", sessionStatus, reason)
	}
	var hitlTask *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT task_id FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`, f.sessionID).Scan(&hitlTask); err != nil {
		t.Fatalf("no budget HITL: %v", err)
	}
	if hitlTask != nil {
		t.Fatalf("hitl task_id = %v, want empty — a session budget request is answered by resuming the session (FR-7.3 s-13)", hitlTask)
	}
	if got := f.claimed(t); len(got) != 0 {
		t.Fatalf("the queue handed out %v from a paused session, want nothing (C3′, E5-04) — %s was queued", got, other)
	}
}

// TestP3BudgetAtFinishEstimatedNeverCuts is E9-05 on the finish path: an
// ESTIMATED cost pauses the session and tells the Director, and never cancels
// anything. It also holds the notification half — before S-44 the estimate
// path raised no HITL and wrote no inbox item, so the one pause that has no
// approval card paused the session in silence.
func TestP3BudgetAtFinishEstimatedNeverCuts(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	// `estimated: true` means the runtime priced nothing — Finish drops the 0
	// that rides along and the roll-up prices the tokens from the workspace
	// table (S-20). 100k in + 100k out of claude-sonnet-5 is $1.20 of a $1
	// session budget, and every digit of it is our own guess.
	f.finishTurn(t, taskID, contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{InputTokens: 100000, OutputTokens: 100000, Estimated: true, Model: "claude-sonnet-5"},
	})

	var sessionStatus, reason string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`, f.sessionID).
		Scan(&sessionStatus, &reason); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "paused" || reason != "budget" {
		t.Fatalf("session = %s(%s), want paused(budget) on an estimate too (E9-05)", sessionStatus, reason)
	}
	var cmds int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM daemon_command WHERE type = 'cancel'`).Scan(&cmds); err != nil {
		t.Fatal(err)
	}
	if cmds != 0 {
		t.Fatalf("cancel commands = %d, want 0 — an estimate never hard-cuts (E9-05)", cmds)
	}
	// S-48 changed this expectation. It used to read "budget HITL = 0 · one
	// `session_paused` inbox card": the estimate path raised no request, so
	// the only answer the card offered was to go and call resumeSession, while
	// the MEASURED pause one branch over handed the Director an approval they
	// could answer with a new limit. E9-05's "Dir 알림" does not say which of
	// the two, and the two pauses are the same decision — so the estimate now
	// asks it the same way. What E9-05 does forbid is unchanged and asserted
	// above: no cancel command, no hard cut.
	var hitls int
	var hitlTask *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*), (array_agg(task_id))[1] FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`,
		f.sessionID).Scan(&hitls, &hitlTask); err != nil {
		t.Fatal(err)
	}
	if hitls != 1 {
		t.Fatalf("budget HITL = %d on the estimate path, want 1 — the drain still asks (E9-05 'Dir 알림', S-48)", hitls)
	}
	if hitlTask != nil {
		t.Fatalf("hitl task_id = %v, want empty — what paused is the SESSION, so the request is "+
			"answered by resuming the session (K-10)", hitlTask)
	}
	var cards, paused int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FILTER (WHERE type = 'hitl_request'), count(*) FILTER (WHERE type = 'session_paused')
		FROM inbox_item WHERE session_id = $1`, f.sessionID).Scan(&cards, &paused); err != nil {
		t.Fatal(err)
	}
	if cards != 1 {
		t.Fatalf("hitl_request inbox items = %d, want 1 — 'Dir 알림' is the other half of E9-05", cards)
	}
	if paused != 0 {
		t.Fatalf("session_paused inbox items = %d, want 0 — the HITL files its own card and two "+
			"cards for one pause is the inbox saying it twice", paused)
	}
	// S-45's card: the request is on the session timeline, not only in the inbox.
	var withCard int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM hitl_request h JOIN message m ON m.id = h.message_id
		WHERE h.session_id = $1 AND h.purpose = 'budget' AND m.kind = 'hitl'`, f.sessionID).Scan(&withCard); err != nil {
		t.Fatal(err)
	}
	if withCard != 1 {
		t.Fatalf("timeline cards for the estimated budget request = %d, want 1 (S-45)", withCard)
	}
	// A feed line says why, so the session view is not silent either.
	var events int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM task_event
		WHERE task_id = $1 AND verb = 'pause' AND object_ref = to_jsonb('budget'::text)`, taskID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events == 0 {
		t.Fatal("no pause(budget) feed event on the estimate path")
	}
}

// TestP3BudgetAtFinishRejectionKeepsTheGate is E9-10's last clause and E9-03's
// shape on this path: a REJECTED raise changes nothing. The lane stays parked
// until a person ends it explicitly — lifting the gate on a "no" would let the
// next task spend the money the Director just refused.
func TestP3BudgetAtFinishRejectionKeepsTheGate(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.finishTurn(t, taskID, contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 1.01},
	})
	var hitlID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`, f.sessionID).Scan(&hitlID); err != nil {
		t.Fatal(err)
	}
	f.api.must(200, "POST", f.p+"/hitl-requests/"+hitlID.String()+"/response",
		map[string]any{"approved": false, "reason": "여기까지"}, "Idempotency-Key", uuid.NewString())

	if _, st := f.laneOf(t, taskID); st != "paused" {
		t.Fatalf("lane = %s after a rejection, want paused still (E9-03, E9-10)", st)
	}
	f.fake.Advance(time.Minute)
	out := f.api.must(201, "POST", f.p+"/sessions/"+f.sessionID+"/messages",
		map[string]any{"content": router.MentionLink("R", f.rUUID) + " 그래도 하나만"},
		"Idempotency-Key", uuid.NewString())
	next := uuid.Nil
	for _, raw := range out["triggers"].([]any) {
		if tr := raw.(map[string]any); str(tr, "agent_id") == f.r {
			next = mustUUID(t, str(tr, "task_id"))
		}
	}
	if next == uuid.Nil {
		t.Fatalf("no follow-up task for R: %v", out["triggers"])
	}
	if got := f.claimed(t); has(got, next) {
		t.Fatalf("the queue handed out %s after a REJECTED raise — the refusal must hold (E9-03)", next)
	}
}
