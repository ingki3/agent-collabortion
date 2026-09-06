// P3 전 백로그 두 건의 DB 통합 테스트 — S-16(listParticipants 501)과
// S-20(비용 추정 롤업).
package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/router"
)

// ---------------------------------------------------------------------------
// S-16 — listParticipants
// ---------------------------------------------------------------------------

// The operation is x-phase P2 and was the last one T-S2 left at 501. The web
// worked around it with the session detail's `participants`, which is exactly
// why it could sit unnoticed — and exactly why the two must not be allowed to
// drift: they are one derivation (FR-1.3 is computed, never stored).
func TestListParticipants(t *testing.T) {
	f := newP2Fixture(t)

	items := f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/participants", nil)
	if len(items) != 3 {
		t.Fatalf("participants = %d, want the session's 3", len(items))
	}
	names := map[string]bool{}
	for _, raw := range items {
		p := raw.(map[string]any)
		agent, _ := p["agent"].(map[string]any)
		names[str(agent, "name")] = true
		if str(p, "agent_id") == "" || str(p, "status") == "" || p["profile"] == nil {
			t.Fatalf("participant is missing the chip's fields: %v", p)
		}
	}
	for _, want := range []string{"Lead", "R", "W"} {
		if !names[want] {
			t.Fatalf("participants %v missing %s", names, want)
		}
	}

	// the list and the session detail are the same rows, in the same order
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	detail, _ := sess["participants"].([]any)
	a, _ := json.Marshal(items)
	b, _ := json.Marshal(detail)
	if string(a) != string(b) {
		t.Fatalf("listParticipants and getSession.participants disagree:\n%s\n%s", a, b)
	}
}

// Status is derived from this session's tasks, so a running task has to show
// up in the list — a 200 that always says `idle` would pass a shape test and
// tell the board nothing (G4 2판 W7 was exactly that).
func TestListParticipantsDerivesStatus(t *testing.T) {
	f := newG4Fixture(t)
	post := f.post(t, map[string]any{"content": router.MentionLink("R", f.rUUID) + " 조사해줘"})
	taskID := ""
	for _, raw := range post["triggers"].([]any) {
		if tr := raw.(map[string]any); str(tr, "agent_id") == f.r {
			taskID = str(tr, "task_id")
		}
	}
	if taskID == "" {
		t.Fatalf("mentioning R triggered no task: %v", post["triggers"])
	}
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/claim", map[string]any{"capacity": 5, "wait_ms": 0})
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/phase", map[string]any{"phase": "running", "pgid": 100})

	for _, raw := range f.api.mustList(200, "GET", f.p+"/sessions/"+f.sessionID+"/participants", nil) {
		p := raw.(map[string]any)
		agent, _ := p["agent"].(map[string]any)
		if str(agent, "name") != "R" {
			continue
		}
		if str(p, "status") != "working" {
			t.Fatalf("R status = %s while its task is running, want working", str(p, "status"))
		}
		return
	}
	t.Fatal("R is not in the participant list")
}

// ---------------------------------------------------------------------------
// S-20 — 추정 비용 롤업
// ---------------------------------------------------------------------------

// estimatedFinish reports an attempt the way the ACP path really reports one
// (harness v0.7.1): tokens, no measured cost, `estimated: true`.
func estimatedFinish(model string) contracts.Finish {
	return contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{
			InputTokens: 500_000, OutputTokens: 100_000, CacheReadTokens: 2_000_000,
			Estimated: true, Model: model,
		},
	}
}

// runTurn takes one mention through claim → running → finish and returns the
// session's rolled-up cost and its badge.
func runTurn(t *testing.T, f *g4Fixture, mention string, agentID uuid.UUID, fin contracts.Finish) (float64, bool) {
	t.Helper()
	post := f.post(t, map[string]any{"content": router.MentionLink(mention, agentID) + " 해줘"})
	taskID := str(post["triggers"].([]any)[0].(map[string]any), "task_id")
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/phase", map[string]any{"phase": "running", "pgid": 100})
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/finish", fin)
	sess := f.api.must(200, "GET", f.p+"/sessions/"+f.sessionID, nil)
	cost, _ := sess["cost_usd"].(float64)
	est, _ := sess["cost_estimated"].(bool)
	return cost, est
}

func near(got, want float64) bool {
	d := got - want
	return d < 1e-4 && d > -1e-4
}

// The defect: a runtime that reports tokens but no cost left the session at
// $0.00 forever. The server owns the price list (harness v0.7.1, PRD §8.2.6),
// so the roll-up prices the tokens itself — at the profile's model, since the
// daemon reported none.
//
// Sonnet 5 default rates: 0.5M in × $2 + 0.1M out × $10 + 2M cache read ×
// $0.20 per 1M = $1.00 + $1.00 + $0.40 = $2.40.
func TestEstimatedCostIsPricedFromTokens(t *testing.T) {
	f := newG4Fixture(t)
	cost, est := runTurn(t, f, "Lead", f.leadUUID, estimatedFinish(""))
	if !near(cost, 2.40) {
		t.Fatalf("session cost_usd = %v, want 2.40 (0.5M·$2 + 0.1M·$10 + 2M·$0.20)", cost)
	}
	if !est {
		t.Fatal("cost_estimated = false; an estimate has to say it is one (FR-7.3)")
	}
}

// harness v0.7.1: `estimated: true` means the 0 riding along in `cost_usd` is a
// type artefact (contracts.Usage cannot omit a float64), not a measurement —
// the server ignores it. This pins the ignoring: a daemon that sends a number
// with `estimated: true` does not get to set the price.
func TestEstimatedFinishIgnoresReportedCost(t *testing.T) {
	f := newG4Fixture(t)
	fin := estimatedFinish("")
	fin.Usage.CostUSD = 99.99
	cost, est := runTurn(t, f, "Lead", f.leadUUID, fin)
	if !near(cost, 2.40) || !est {
		t.Fatalf("session cost_usd = %v estimated=%v, want the table's 2.40", cost, est)
	}
}

// A measured cost is measured: the roll-up must not touch it, and no badge.
func TestMeasuredCostIsNotRepriced(t *testing.T) {
	f := newG4Fixture(t)
	fin := estimatedFinish("")
	fin.Usage.Estimated, fin.Usage.CostUSD = false, 7.5
	cost, est := runTurn(t, f, "Lead", f.leadUUID, fin)
	if !near(cost, 7.5) {
		t.Fatalf("session cost_usd = %v, want the reported 7.5 untouched", cost)
	}
	if est {
		t.Fatal("cost_estimated = true for a measured cost")
	}
}

// The price list is workspace-owned (PRD §8.2.6). An override wins over the
// default table, and it applies to usage that already landed — the roll-up
// re-prices rather than freezing whatever rate was configured at finish time.
//
// Override 1/1/1: (0.5M + 0.1M + 2M) × $1 per 1M = $2.60.
func TestPricingOverrideWinsAndAppliesRetroactively(t *testing.T) {
	f := newG4Fixture(t)
	cost, _ := runTurn(t, f, "Lead", f.leadUUID, estimatedFinish(""))
	if !near(cost, 2.40) {
		t.Fatalf("default-priced cost = %v, want 2.40", cost)
	}
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"budget_policy": map[string]any{
			"pricing_overrides": map[string]any{
				"claude-sonnet-5": map[string]any{"input": 1, "output": 1, "cache_read": 1},
			},
		},
	})
	// a second turn re-runs the roll-up over the whole session
	cost, est := runTurn(t, f, "R", f.rUUID, estimatedFinish(""))
	if !near(cost, 5.20) || !est {
		t.Fatalf("session cost_usd = %v estimated=%v, want 5.20 (both turns at the override)", cost, est)
	}
}

// model_drift: the daemon reports the model that actually served the turn
// (harness §7 `_meta.quota.model_usage[].model`), and it can differ from the
// profile's. Pricing the drifted turn at the profile's rate would bill the
// model that did not run.
//
// Opus 5 default rates: 0.5M × $5 + 0.1M × $25 + 2M × $0.50 = $6.00.
func TestReportedModelBeatsProfileForPricing(t *testing.T) {
	f := newG4Fixture(t)
	cost, est := runTurn(t, f, "Lead", f.leadUUID, estimatedFinish("claude-opus-5"))
	if !near(cost, 6.0) || !est {
		t.Fatalf("session cost_usd = %v estimated=%v, want 6.00 at the reported model's rate", cost, est)
	}
}

// A model with no rate anywhere stays unpriced. `estimated: true` with 0 then
// means "we do not know" — which is what the badge is for. Inventing a rate
// would be worse than the $0 this change removes, because a made-up number
// cannot be told from a measured one.
func TestUnknownModelIsLeftUnpriced(t *testing.T) {
	f := newG4Fixture(t)
	cost, est := runTurn(t, f, "Lead", f.leadUUID, estimatedFinish("some-local-llama-70b"))
	if cost != 0 || !est {
		t.Fatalf("session cost_usd = %v estimated=%v, want 0 + estimated for an unpriced model", cost, est)
	}
}

// The SSE frame and the reload have to carry the same number — the estimate is
// computed in the roll-up, and `cost.updated` is published from the same
// transaction.
func TestCostUpdatedCarriesTheEstimate(t *testing.T) {
	f := newG4Fixture(t)
	post := f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 시작"})
	taskID := str(post["triggers"].([]any)[0].(map[string]any), "task_id")
	f.daemon.must(200, "POST", "/v1/daemon/runtimes/"+f.runtimeID+"/claim", map[string]any{"capacity": 1, "wait_ms": 0})
	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/phase", map[string]any{"phase": "running", "pgid": 100})

	frames, stop := openStream(t, f.api, f.p+"/workspaces/"+f.wsID+"/stream?session_id="+f.sessionID)
	defer stop()

	f.daemon.must(200, "POST", "/v1/daemon/tasks/"+taskID+"/attempts/1/finish", estimatedFinish(""))

	var c struct {
		CostUSD   float64 `json:"cost_usd"`
		Estimated bool    `json:"estimated"`
	}
	if err := json.Unmarshal(waitFrame(t, frames, "cost.updated", nil), &c); err != nil {
		t.Fatal(err)
	}
	if !near(c.CostUSD, 2.40) || !c.Estimated {
		t.Fatalf("cost.updated = %+v, want the estimated 2.40", c)
	}
}
