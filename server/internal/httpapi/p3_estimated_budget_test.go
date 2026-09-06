package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

// S-48 — the budget is enforced against the ESTIMATE too.
//
// FR-7.3's last bullet and EVAL E9-05 do not say "an estimated cost is
// ignored"; they say it is not HARD CUT. The number still accumulates, still
// compares against the limit, and on crossing it still pauses the session and
// tells the Director — the turn drains instead of being cancelled.
//
// What the server did instead: `RecordTurnUsage` dropped an `estimated: true`
// report's cost to 0 (correct — the runtime measured nothing) and left the
// pricing to `repriceEstimates`, which runs only inside the finish roll-up. So
// the heartbeat wrote a 0 and `enforceBudgetFor` compared THAT against the
// limit. Every ACP runtime the product runs reports `estimated: true` — T-I3
// counted 72 of 72 runtime-written `task_usage` rows — so in-turn budget
// enforcement could not fire at all, and fixing the daemon's own half (D-17,
// "the heartbeat carries no usage") would only have made it report a 0 more
// often.

// estimatedTurn reports one heartbeat's worth of estimated usage and runs the
// enforcement the daemon heartbeat runs, in the same order.
//
// 100k in + 100k out of claude-sonnet-5 is $2 + $10 per MTok = $1.20 (cost.Defaults),
// and every digit of it is the server's own guess.
func (f *p2Fixture) estimatedTurn(t *testing.T, taskID uuid.UUID, model string, in, out int64) {
	t.Helper()
	if err := f.srv.Tasks.RecordTurnUsage(t.Context(), taskID, contracts.Usage{
		InputTokens: in, OutputTokens: out, CostUSD: 0, Estimated: true, Model: model,
	}, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.enforceBudgetFor(t.Context(), taskID); err != nil {
		t.Fatal(err)
	}
}

func (f *p2Fixture) storedUsage(t *testing.T, taskID uuid.UUID) (float64, bool) {
	t.Helper()
	var usd float64
	var estimated bool
	if err := f.pool.QueryRow(t.Context(), `SELECT cost_usd, estimated FROM task_usage WHERE task_id = $1`, taskID).
		Scan(&usd, &estimated); err != nil {
		t.Fatal(err)
	}
	return usd, estimated
}

// TestP3EstimatedUsageEnforcesInTurn is E9-05 on the path that actually runs:
// estimated-only usage, reported mid-turn, crosses the session limit.
func TestP3EstimatedUsageEnforcesInTurn(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	// No per-task budget: the session remainder is this task's only ceiling, so
	// crossing it is a SESSION overrun (D-16).
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = NULL`); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "claude-sonnet-5", 100000, 100000)

	// 1. The number reached the row. This is the whole defect: the daemon
	//    reported no cost, so the price table is the only thing that can say
	//    what the turn was worth, and it has to say it BEFORE the comparison.
	usd, estimated := f.storedUsage(t, taskID)
	if usd < 1.19 || usd > 1.21 {
		t.Fatalf("stored cost = $%.4f, want ≈$1.20 from the price table — an estimated report's "+
			"cost is priced at the heartbeat, not only in the finish roll-up (S-48)", usd)
	}
	if !estimated {
		t.Error("the row stays badged `estimated` — pricing it does not turn a guess into a measurement (E9-09)")
	}

	// 2. E9-05's two halves: the session pauses, and nothing is cut.
	var sessionStatus, reason string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT status::text, COALESCE(paused_reason::text, '') FROM session WHERE id = $1`, f.sessionID).
		Scan(&sessionStatus, &reason); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "paused" || reason != "budget" {
		t.Fatalf("session = %s(%s), want paused(budget) — an estimate at 100%% of the limit still "+
			"stops the session (FR-7.3, E9-05)", sessionStatus, reason)
	}
	var cancels int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM daemon_command WHERE type = 'cancel'`).Scan(&cancels); err != nil {
		t.Fatal(err)
	}
	if cancels != 0 {
		t.Fatalf("cancel commands = %d, want 0 — killing real work on our own guess is the failure "+
			"FR-7.3 names (E9-05)", cancels)
	}
	if st, _ := f.pausedTask(t, taskID); st != "running" {
		t.Fatalf("task = %q, want running — the turn DRAINS; only new dispatch stops (E9-05)", st)
	}
	if got := f.claimed(t); len(got) != 0 {
		t.Fatalf("the queue handed out %v from a paused session, want nothing (C3′, E5-04)", got)
	}

	// 3. The Director is told, and told in a way they can answer.
	var hitls int
	var hitlTask *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*), (array_agg(task_id))[1] FROM hitl_request
		WHERE session_id = $1 AND source = 'system' AND purpose = 'budget' AND status = 'open'`,
		f.sessionID).Scan(&hitls, &hitlTask); err != nil {
		t.Fatal(err)
	}
	if hitls != 1 {
		t.Fatalf("open budget HITL = %d, want 1 (E9-05 'Dir 알림', S-48)", hitls)
	}
	if hitlTask != nil {
		t.Fatalf("hitl task_id = %v, want empty — what paused is the SESSION (FR-7.3 s-13, K-10)", hitlTask)
	}
	var cards int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM inbox_item WHERE session_id = $1 AND type = 'hitl_request'`, f.sessionID).Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if cards != 1 {
		t.Fatalf("inbox items = %d, want 1", cards)
	}
	// The feed says the turn is being left alone, so the session view does not
	// look like a silent stop.
	var drained int
	if err := f.pool.QueryRow(t.Context(), `
		-- S-52: the verb "pause" and a top-level "estimated" key were in no
		-- part of task_event.schema.json. The row is runtime/report and the
		-- sentence (with the arithmetic, and the word 추정) is "detail".
		SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'runtime' AND verb = 'report'
		  AND object_ref = to_jsonb('budget'::text) AND payload->>'detail' LIKE '추정 비용이%'`, taskID).Scan(&drained); err != nil {
		t.Fatal(err)
	}
	if drained != 1 {
		t.Fatalf("drain feed events = %d, want 1 (E9-05 '턴은 계속')", drained)
	}

	// 4. A second heartbeat over the same limit does not file a second request.
	//    The session guard stops it, and so does the request's own uniqueness.
	f.estimatedTurn(t, taskID, "claude-sonnet-5", 120000, 120000)
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM hitl_request WHERE session_id = $1 AND purpose = 'budget'`, f.sessionID).Scan(&hitls); err != nil {
		t.Fatal(err)
	}
	if hitls != 1 {
		t.Fatalf("budget HITL after a second heartbeat = %d, want 1 — one card per pause, not one "+
			"per 15s heartbeat", hitls)
	}
}

// TestP3EstimatedTaskBudgetQuotesTheTaskNumbers is the pair the card shows. An
// estimated overrun of a TASK budget pauses the SESSION (E9-05 has no per-task
// drain), so the request is session-scoped — and picking the quoted numbers by
// the request's scope, as the code used to, then reported the session's limit
// for a session that has none: "세션이 예산 $0.00를 넘었습니다".
func TestP3EstimatedTaskBudgetQuotesTheTaskNumbers(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 0.5 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "claude-sonnet-5", 100000, 100000) // $1.20 of a $0.50 task budget

	var question string
	var detail []byte
	if err := f.pool.QueryRow(t.Context(), `
		SELECT h.question, s.paused_detail FROM hitl_request h JOIN session s ON s.id = h.session_id
		WHERE h.session_id = $1 AND h.purpose = 'budget'`, f.sessionID).Scan(&question, &detail); err != nil {
		t.Fatalf("no budget HITL for an estimated task overrun: %v", err)
	}
	if !contains(question, "$0.50") || !contains(question, "$1.20") {
		t.Fatalf("question = %q, want the TASK's limit ($0.50) and spend ($1.20) — the session has "+
			"no budget here, so quoting its numbers reads as $0.00 (S-48)", question)
	}
	if !contains(string(detail), "0.5") {
		t.Fatalf("paused_detail = %s, want the limit that was crossed", detail)
	}
}

// TestP3UnpricedModelIsSaidOutLoud is S-48's other half: a model the price
// table does not know stays at $0, because inventing a rate is worse than the
// $0 — but the feed says so once, so "왜 예산이 안 걸렸나" has an answer on
// screen instead of only in the workspace settings.
func TestP3UnpricedModelIsSaidOutLoud(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE session SET limits = '{"budget_usd": 1}'::jsonb WHERE id = $1`, f.sessionID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "some-other-vendor/llm-9", 100000, 100000)

	if usd, _ := f.storedUsage(t, taskID); usd != 0 {
		t.Fatalf("stored cost = $%.4f for an unknown model, want 0 — a made-up number cannot be "+
			"told apart from a measured one", usd)
	}
	var sessionStatus string
	if err := f.pool.QueryRow(t.Context(), `SELECT status::text FROM session WHERE id = $1`, f.sessionID).Scan(&sessionStatus); err != nil {
		t.Fatal(err)
	}
	if sessionStatus != "active" {
		t.Fatalf("session = %q, want active — an unknown cost is not an overrun", sessionStatus)
	}
	// Once, not once per heartbeat.
	f.estimatedTurn(t, taskID, "some-other-vendor/llm-9", 200000, 200000)
	var notes int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM task_event WHERE task_id = $1 AND object_ref = to_jsonb('cost.unpriced'::text)`,
		taskID).Scan(&notes); err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Fatalf("cost.unpriced feed notes = %d after two heartbeats, want 1", notes)
	}
}

// TestP3LostPostFinishEnforcementIsOnTheFeed is S-47. The check that runs
// after `finish` is its own transaction (finishAndEnforce explains the lock
// order), so when it fails the attempt still stands and nothing is paused: the
// lane stays unlocked, the next task dispatches on a budget nobody verified,
// and until now the only trace was a Warn line in the server log while the
// session's own timeline said the turn ended normally.
//
// The re-check itself needs no scheduling — enforceBudgetFor is state-driven,
// re-reading task_usage and the session status every time — which is why the
// note says "다음 heartbeat·finish 에서 다시 검사합니다" rather than promising a
// retry of its own.
func TestP3LostPostFinishEnforcementIsOnTheFeed(t *testing.T) {
	f := newP2Fixture(t)
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	attempt := currentAttempt(t, f, taskID)

	lost := errFailedEnforcement{}
	if err := f.srv.Tasks.NoteBudgetEnforceFailed(t.Context(), taskID, attempt, lost, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	// A second loss on the same attempt does not repeat the line — a note the
	// 15s heartbeat can multiply buries the feed (the same rule as the preview
	// drift warning).
	if err := f.srv.Tasks.NoteBudgetEnforceFailed(t.Context(), taskID, attempt, lost, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	var notes int
	var payload []byte
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*), (array_agg(payload))[1] FROM task_event
		WHERE task_id = $1 AND object_ref = to_jsonb('budget.enforce_failed'::text)`, taskID).
		Scan(&notes, &payload); err != nil {
		t.Fatal(err)
	}
	if notes != 1 {
		t.Fatalf("budget.enforce_failed feed notes = %d, want 1 (S-47)", notes)
	}
	if !contains(string(payload), "다시 검사") {
		t.Fatalf("payload = %s, want the note that says the next heartbeat·finish re-checks", payload)
	}
}

type errFailedEnforcement struct{}

func (errFailedEnforcement) Error() string { return "connection reset by peer" }
