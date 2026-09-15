// In-turn usage — harness §7 v0.8.5, daemon-protocol §4.2, FR-7.3 M9 (D-17).
//
// The numbers in `measuredRequests` / `measuredTotal` are not invented: they
// are one real turn of claude-agent-acp 0.74.0 + Claude Code 2.1.258 (12.2s,
// three tool calls, four model requests), captured off the wire. They are here
// so the dedup rule stays pinned to a measurement instead of to whatever the
// code happens to do.
package acp_test

import (
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// hookSink forwards to memSink and lets a test look at the runner WHILE the
// turn is still running — the only way to assert on a mid-turn number.
type hookSink struct {
	inner acp.Sink
	on    func(contracts.TaskEvent)
}

func (h *hookSink) Emit(ev contracts.TaskEvent) {
	h.inner.Emit(ev)
	if h.on != nil {
		h.on(ev)
	}
}
func (h *hookSink) Preview(t string) { h.inner.Preview(t) }

// The four model requests of the measured turn, in order.
var measuredRequests = []acpfake.SDKRequestStep{
	{Input: 10, Output: 298, CacheRead: 13615, CacheWrite: 8184},
	{Input: 8, Output: 172, CacheRead: 21799, CacheWrite: 804},
	{Input: 8, Output: 102, CacheRead: 22603, CacheWrite: 316},
	{Input: 8, Output: 81, CacheRead: 22919, CacheWrite: 158},
}

// What the `session/prompt` response reported for that same turn. Every field
// is the sum of the four requests above — that equality IS the dedup rule.
var measuredTotal = acp.PromptUsage{InputTokens: 34, OutputTokens: 653, CachedReadTokens: 80936, CachedWriteTokens: 9462, TotalTokens: 91085}

// midturnScript runs the four requests with a tool call after the second, so
// a sink hook has a mid-turn event to snapshot on.
func midturnScript(cost *float64) acpfake.Script {
	steps := []acpfake.Step{
		{SDKRequest: &measuredRequests[0]},
		{SDKRequest: &measuredRequests[1]},
		{ToolCall: &acpfake.ToolCallStep{ID: "mid", Title: "ls", Kind: "execute"}},
		{ToolUpdate: &acpfake.ToolUpdateStep{ID: "mid", Status: "completed", Text: "ok"}},
		{SDKRequest: &measuredRequests[2]},
		{SDKRequest: &measuredRequests[3]},
		{Chunk: "DONE"},
	}
	u := measuredTotal
	return acpfake.Script{Kind: "claude", Turns: []acpfake.Turn{{Steps: steps, Usage: &u, CostUSD: cost}}}
}

// runMidturn runs the script and returns the result plus the usage snapshot
// taken on the mid-turn tool event.
func runMidturn(t *testing.T, script acpfake.Script, rawSDK bool) (acp.Result, contracts.Usage, *fixture) {
	t.Helper()
	var mid contracts.Usage
	var f *fixture
	f = newFixture(t, script, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) {
		a.RawSDKMessages = rawSDK
		// newFixture has already put the memSink in place; wrap THAT, since
		// the fixture is not assigned to `f` until this callback returns.
		a.Sink = &hookSink{inner: a.Sink, on: func(ev contracts.TaskEvent) {
			if ev.Class == "tool" && ev.Outcome == "ok" {
				mid = f.runner.Usage()
			}
		}}
	})
	res := f.run()
	return res, mid, f
}

func wantUsage(t *testing.T, got contracts.Usage, in, out, cr, cw int64, where string) {
	t.Helper()
	if got.InputTokens != in || got.OutputTokens != out || got.CacheReadTokens != cr || got.CacheWriteTokens != cw {
		t.Errorf("%s usage = in %d / out %d / cache_read %d / cache_write %d, want %d / %d / %d / %d",
			where, got.InputTokens, got.OutputTokens, got.CacheReadTokens, got.CacheWriteTokens, in, out, cr, cw)
	}
}

// The heartbeat's `usage` is non-zero BEFORE the turn ends, and the four
// per-request numbers add up to exactly what the turn reported — no double
// count from the duplicated `assistant` messages, no lost output tokens.
func TestMidturnUsageDedupMatchesTurnTotal(t *testing.T) {
	res, mid, _ := runMidturn(t, midturnScript(nil), true)
	if res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	// After two of the four requests. A zero here means the whole in-turn
	// enforcement half of FR-7.3 is dead again: the server's check is gated
	// on usage > 0 (S-44).
	wantUsage(t, mid, 18, 470, 35414, 8988, "mid-turn")
	if !mid.Estimated || mid.CostUSD != 0 {
		t.Errorf("mid-turn cost = %v estimated=%v — mid-turn tokens have no price yet, so the "+
			"total must be an estimate with no number (harness §7 v0.7.1)", mid.CostUSD, mid.Estimated)
	}
	// And the turn's own total supersedes the approximation rather than
	// adding to it.
	wantUsage(t, res.Usage, 34, 653, 80936, 9462, "finish")
	if !res.UsageMidturn {
		t.Error("UsageMidturn = false after in-turn usage arrived — probe §9 would advertise the wrong thing")
	}
}

// The same script with the raw stream off: nothing mid-turn, and the turn
// total is still exact. This is the hermes shape and the shape of a
// claude_code daemon that switched the stream off (config usage_midturn).
func TestMidturnUsageAbsentWhenRawSDKOff(t *testing.T) {
	res, mid, _ := runMidturn(t, midturnScript(nil), false)
	wantUsage(t, mid, 0, 0, 0, 0, "mid-turn (stream off)")
	wantUsage(t, res.Usage, 34, 653, 80936, 9462, "finish")
	if res.UsageMidturn {
		t.Error("UsageMidturn = true with the raw stream off — an unmeasured capability must not be advertised")
	}
	if !res.Usage.Estimated || res.Usage.CostUSD != 0 {
		t.Errorf("cost = %v estimated=%v, want the v0.7.1 pair (0, true) — no adapter reported a cost",
			res.Usage.CostUSD, res.Usage.Estimated)
	}
}

// `result.total_cost_usd` is a MEASURED cost (harness §7 v0.8.5 / D-6 rule 4):
// it goes out as `cost_usd` with `estimated: false`, and the `usage.report`
// payload carries the number instead of omitting it.
func TestResultCostIsMeasured(t *testing.T) {
	cost := 0.0303166
	res, _, f := runMidturn(t, midturnScript(&cost), true)
	if res.Usage.Estimated {
		t.Errorf("estimated = true with a runtime-reported cost — D-6 rule (4) says a cost the " +
			"runtime gives is measured, whichever channel gave it")
	}
	if res.Usage.CostUSD != cost {
		t.Errorf("cost_usd = %v, want %v", res.Usage.CostUSD, cost)
	}
	ev := f.sink.find("usage", "report", "report")
	if len(ev) == 0 {
		t.Fatal("no usage.report event")
	}
	last := ev[len(ev)-1]
	if got, ok := last.Payload["cost_usd"].(float64); !ok || got != cost {
		t.Errorf("usage.report cost_usd = %v (present=%v), want %v — §7 v0.7.1 omits the key only "+
			"when the cost is unknown", last.Payload["cost_usd"], ok, cost)
	}
}

// Without the raw stream there is no cost anywhere, so `usage.report` OMITS
// `cost_usd` — the v0.7.1 shape that stops a session reading a confident $0.
func TestNoCostOmitsCostKey(t *testing.T) {
	_, _, f := runMidturn(t, midturnScript(nil), false)
	ev := f.sink.find("usage", "report", "report")
	if len(ev) == 0 {
		t.Fatal("no usage.report event")
	}
	if _, present := ev[len(ev)-1].Payload["cost_usd"]; present {
		t.Errorf("usage.report carries cost_usd with no measured cost: %+v", ev[len(ev)-1].Payload)
	}
}

// A measured cost that crosses the §4.4 유효 예산 ends the attempt in
// `paused_budget` — the daemon's own half of FR-7.3 M9, which could never fire
// while every ACP turn was an estimate (budget.go).
func TestMeasuredCostCrossingBudgetPausesAttempt(t *testing.T) {
	cost := 2.5
	b := bundle(contracts.RuntimeClaudeCode)
	limit := 2.0
	b.Limits.BudgetUSD = &limit
	f := newFixture(t, midturnScript(&cost), b, func(a *acp.Attempt) { a.RawSDKMessages = true })
	res := f.run()
	if res.Outcome != "paused_budget" {
		t.Fatalf("outcome = %q, want paused_budget (cost $%.2f over the $%.2f session remainder)", res.Outcome, cost, limit)
	}
	if res.Failure != nil {
		t.Errorf("failure = %+v — §4.4: going over budget is policy, not an error", res.Failure)
	}
}

// The refusal retry runs TWO turns in one attempt (D-13). The abandoned turn's
// tokens are real money and must survive into the attempt's usage.
func TestRefusalRetryKeepsFirstTurnUsage(t *testing.T) {
	first := acp.PromptUsage{InputTokens: 100, OutputTokens: 5}
	second := acp.PromptUsage{InputTokens: 30, OutputTokens: 60}
	b := bundle(contracts.RuntimeClaudeCode)
	b.Resume = &contracts.RuntimeSessionRef{RuntimeKind: contracts.RuntimeClaudeCode, SessionID: "sess-1", CWD: "/tmp", CreatedAt: time.Now().UTC()}
	s := acpfake.Script{
		Kind: "claude", KnownSessions: []string{"sess-1"},
		Turns: []acpfake.Turn{
			{StopReason: "refusal", Usage: &first},
			{Steps: []acpfake.Step{{Chunk: "second turn"}}, Usage: &second},
		},
	}
	f := newFixture(t, s, b, nil)
	res := f.run()
	if res.ResumeOutcome != "cold_start" {
		t.Fatalf("resume outcome = %q, want cold_start (D-13)", res.ResumeOutcome)
	}
	wantUsage(t, res.Usage, 130, 65, 0, 0, "two-turn attempt")
}
