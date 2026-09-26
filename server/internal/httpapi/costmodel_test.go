package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
)

// T-COSTMODEL (Director 2026-09-26) — the budget cap did not trip in the
// middle of a claude_code Lead's long turn.
//
// Measured on the 「게임 제작 방」: the Lead's profile model is the alias
// "default", which no price table knows. Mid-turn heartbeats carried no model
// (the daemon's approximation deliberately had none), so repriceEstimates fell
// back to the profile's "default" → unpriced → $0 with
// 「가격표에 없는 모델 … (모델: default, 추정치)」, while input 40 · output
// 22,629 had already been burned. Only the finish (model
// `claude-opus-5[1m]`, cost $9.9~$21.8, estimated=f) put a number on it — after
// the money was spent.
//
// Three server rules close it: the mid-turn model the daemon now sends is
// priced (normalised: `[1m]`, comma lists, date forms), and when a heartbeat
// has no model and the profile's is unpriced, the same agent's most recent
// MEASURED model in the same room prices it. Only when neither exists does
// the unpriced note stay.

// leadAlias turns R's profile into the Director's Lead shape: claude_code with
// model "default", a mission ceiling of $1 and no per-task budget, so the
// mission's remainder is the only ceiling in play.
func (f *p2Fixture) leadAlias(t *testing.T) {
	t.Helper()
	f.exec(t, `UPDATE agent_profile SET model = 'default' WHERE agent_id = $1`, f.rUUID)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	f.exec(t, `UPDATE work SET limits = '{"budget_usd": 1}'::jsonb WHERE room_id = $1`, f.sessionID)
}

func (f *p2Fixture) unpricedNotes(t *testing.T, taskID uuid.UUID) (int, string) {
	t.Helper()
	var n int
	var detail string
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*), COALESCE(max(payload->>'detail'), '') FROM task_event
		WHERE task_id = $1 AND object_ref = to_jsonb('cost.unpriced'::text)`, taskID).Scan(&n, &detail); err != nil {
		t.Fatal(err)
	}
	return n, detail
}

// (1) + (4): the daemon's mid-turn model, in the exact spelling Claude Code
// reports, prices the heartbeat, and the $1 mission cap pauses the mission
// MID-TURN — the turn keeps running (E9-05), nothing is cancelled.
func TestCostModelMidturnModelTripsMissionCap(t *testing.T) {
	f := newP2Fixture(t)
	f.leadAlias(t)
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)

	// input 40 · output 22,629 — the Director's measured Lead turn — at opus
	// rates is $0.566; a second heartbeat later in the same turn crosses $1.
	f.estimatedTurn(t, taskID, "claude-opus-5[1m]", 40, 22629)
	usd, estimated := f.storedUsage(t, taskID)
	if usd < 0.56 || usd > 0.57 || !estimated {
		t.Fatalf("stored = $%.4f estimated=%v, want ≈$0.566 estimated — `claude-opus-5[1m]` is claude-opus-5 "+
			"at the base rate (T-COSTMODEL)", usd, estimated)
	}
	if st, _, _ := f.missionState(t); st != "active" {
		t.Fatalf("mission = %s under the cap, want active", st)
	}
	f.estimatedTurn(t, taskID, "claude-opus-5[1m]", 80, 45000)
	if st, r, _ := f.missionState(t); st != "paused" || r != "budget" {
		t.Fatalf("mission = %s(%s) after $1.13 of a $1 cap mid-turn, want paused(budget) — the "+
			"in-turn half of FR-7.3 on a claude_code Lead", st, r)
	}
	if st, _ := f.pausedTask(t, taskID); st != "running" {
		t.Fatalf("task = %q, want running — an estimated overrun drains, it is not cut (E9-05)", st)
	}
	if n, _ := f.unpricedNotes(t, taskID); n != 0 {
		t.Fatalf("cost.unpriced notes = %d, want 0 — the turn WAS priced", n)
	}
}

// The comma list the finish writes for a Researcher prices at the FIRST
// (main) model.
func TestCostModelCommaListPricesMainModel(t *testing.T) {
	f := newP2Fixture(t)
	f.leadAlias(t)
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "claude-opus-5[1m],claude-haiku-4-5-20251001", 0, 20000)
	if usd, _ := f.storedUsage(t, taskID); usd < 0.499 || usd > 0.501 {
		t.Fatalf("stored = $%.4f, want $0.50 (20k output at opus $25/MTok, not haiku's $0.10)", usd)
	}
}

// (3): a heartbeat with NO model — an older daemon, or a stream that has not
// reached its first message_start — on a "default" profile is priced from the
// same agent's most recent measured model in this room.
func TestCostModelFallbackToLastMeasuredModel(t *testing.T) {
	f := newP2Fixture(t)
	f.leadAlias(t)
	_, first := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.finishTurn(t, first, contracts.Finish{Outcome: "completed", StopReason: "end_turn", Usage: contracts.Usage{
		InputTokens: 10, OutputTokens: 100, CostUSD: 0.10, Estimated: false, Model: "claude-opus-5[1m]",
	}})

	_, second := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, second)
	f.estimatedTurn(t, second, "", 40, 40000) // $1.0002 at opus rates
	usd, estimated := f.storedUsage(t, second)
	if usd < 1.0 || usd > 1.001 || !estimated {
		t.Fatalf("stored = $%.4f estimated=%v, want ≈$1.00 estimated from the Lead's last measured "+
			"model (claude-opus-5[1m]) — not $0 from the unpriced profile alias", usd, estimated)
	}
	if st, r, _ := f.missionState(t); st != "paused" || r != "budget" {
		t.Fatalf("mission = %s(%s), want paused(budget) — $0.10 + $1.00 of a $1 cap", st, r)
	}
	if n, _ := f.unpricedNotes(t, second); n != 0 {
		t.Fatalf("cost.unpriced notes = %d, want 0 when the fallback priced the turn", n)
	}
	// The fallback prices; it does not rewrite what was measured.
	var model *string
	if err := f.pool.QueryRow(t.Context(), `SELECT model FROM task_usage WHERE task_id = $1`, second).Scan(&model); err != nil {
		t.Fatal(err)
	}
	if model != nil && *model != "" {
		t.Fatalf("task_usage.model = %q, want empty — the fallback is never written back", *model)
	}
}

// The fallback is the SAME agent's. Another agent's measured model in the
// same room says nothing about this one — the unpriced note stays.
func TestCostModelFallbackIgnoresOtherAgents(t *testing.T) {
	f := newP2Fixture(t)
	f.leadAlias(t)
	_, other := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.finishTurn(t, other, contracts.Finish{Outcome: "completed", StopReason: "end_turn", Usage: contracts.Usage{
		InputTokens: 10, OutputTokens: 100, CostUSD: 0.10, Estimated: false, Model: "claude-opus-5",
	}})
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "", 40, 40000)
	if usd, _ := f.storedUsage(t, taskID); usd != 0 {
		t.Fatalf("stored = $%.4f, want 0 — W's model is not R's", usd)
	}
	if n, _ := f.unpricedNotes(t, taskID); n != 1 {
		t.Fatalf("cost.unpriced notes = %d, want 1", n)
	}
}

// The fallback is a MEASURED model (estimated = false). An earlier turn's
// estimate carries only a model someone reported mid-turn — or a guess — and
// chaining guesses is how a fallback drifts away from anything measured.
func TestCostModelFallbackIgnoresEstimatedHistory(t *testing.T) {
	f := newP2Fixture(t)
	f.leadAlias(t)
	_, first := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.finishTurn(t, first, contracts.Finish{Outcome: "completed", StopReason: "end_turn", Usage: contracts.Usage{
		InputTokens: 10, OutputTokens: 100, Estimated: true, Model: "claude-opus-5",
	}})
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "", 40, 40000)
	if usd, _ := f.storedUsage(t, taskID); usd != 0 {
		t.Fatalf("stored = $%.4f, want 0 — no MEASURED model to fall back to", usd)
	}
	if n, _ := f.unpricedNotes(t, taskID); n != 1 {
		t.Fatalf("cost.unpriced notes = %d, want 1", n)
	}
}

// (3)'s last clause: no model on the heartbeat, an unpriced profile and no
// measured history — the note is exactly what it was, naming the profile's
// alias, and the cost stays $0 (an invented rate is worse).
func TestCostModelNoFallbackKeepsUnpricedNote(t *testing.T) {
	f := newP2Fixture(t)
	f.leadAlias(t)
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "", 40, 22629)
	if usd, _ := f.storedUsage(t, taskID); usd != 0 {
		t.Fatalf("stored = $%.4f, want 0 with nothing to price from", usd)
	}
	n, detail := f.unpricedNotes(t, taskID)
	if n != 1 || !contains(detail, "(모델: default, 추정치)") {
		t.Fatalf("cost.unpriced = %d %q, want one note naming the profile model 'default'", n, detail)
	}
	if st, _, _ := f.missionState(t); st != "active" {
		t.Fatalf("mission = %s, want active — an unknown cost is not an overrun", st)
	}
}

// hermes path unchanged: the profile names a priced model, heartbeats carry no
// model, and the profile's rate is used — even when the agent's last measured
// model is a different one. A priced profile outranks the fallback.
func TestCostModelPricedProfileOutranksFallback(t *testing.T) {
	f := newP2Fixture(t)
	f.exec(t, `UPDATE agent_profile SET runtime_kind = 'hermes', model = 'anthropic:claude-sonnet-5' WHERE agent_id = $1`, f.rUUID)
	f.exec(t, `UPDATE agent SET budget_per_task = NULL`)
	_, first := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.finishTurn(t, first, contracts.Finish{Outcome: "completed", StopReason: "end_turn", Usage: contracts.Usage{
		InputTokens: 1, OutputTokens: 1, CostUSD: 0.01, Estimated: false, Model: "claude-opus-5",
	}})
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.estimatedTurn(t, taskID, "", 100000, 100000)
	if usd, _ := f.storedUsage(t, taskID); usd < 1.19 || usd > 1.21 {
		t.Fatalf("stored = $%.4f, want $1.20 at the profile's sonnet rate, not opus' $3.00", usd)
	}
}
