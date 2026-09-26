// T-COSTMODEL — the mid-turn heartbeat names the model the turn is running on.
//
// Director 2026-09-26: a claude_code Lead whose profile model is "default"
// read $0 for the whole of every turn ("가격표에 없는 모델 … (모델: default)"),
// because the mid-turn usage carried no model and the server fell back to the
// profile's. The adapter's raw stream names the model on every request's
// `message_start` (spike 1b wire: `"message":{"model":"claude-haiku-4-5-…"}`),
// and a subagent's requests carry the Task call's id in `parent_tool_use_id`.
package acp_test

import (
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

const (
	mainModel = "claude-opus-5[1m]"
	subModel  = "claude-haiku-4-5-20251001"
)

// costModelScript: one main request, then a subagent request that out-talks
// it (so "most tokens" would pick haiku), then the snapshot tool event, then
// one more main request after it.
func costModelScript(model bool) acpfake.Script {
	mainReq := acpfake.SDKRequestStep{Input: 40, Output: 500, CacheRead: 1000}
	subReq := acpfake.SDKRequestStep{Input: 9000, Output: 20000, CacheRead: 50000, Parent: "toolu_sub"}
	lastReq := acpfake.SDKRequestStep{Input: 5, Output: 50}
	if model {
		mainReq.Model, subReq.Model, lastReq.Model = mainModel, subModel, mainModel
	}
	steps := []acpfake.Step{
		{SDKRequest: &mainReq},
		{SDKRequest: &subReq},
		{ToolCall: &acpfake.ToolCallStep{ID: "mid", Title: "ls", Kind: "execute"}},
		{ToolUpdate: &acpfake.ToolUpdateStep{ID: "mid", Status: "completed", Text: "ok"}},
		{SDKRequest: &lastReq},
		{Chunk: "DONE"},
	}
	u := acp.PromptUsage{InputTokens: 9045, OutputTokens: 20550, CachedReadTokens: 51000}
	return acpfake.Script{Kind: "claude", Turns: []acpfake.Turn{{Steps: steps, Usage: &u}}}
}

// The heartbeat mid-turn carries the MAIN model, not the subagent's, even
// though the subagent burned forty times the tokens; the subagent's tokens
// still count.
func TestMidturnUsageCarriesMainModel(t *testing.T) {
	res, mid, f := runMidturn(t, costModelScript(true), true)
	if res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	if mid.Model != mainModel {
		t.Fatalf("mid-turn usage.model = %q, want %q — the main stream's message_start model, not "+
			"the subagent's (parent_tool_use_id set) and not empty (T-COSTMODEL)", mid.Model, mainModel)
	}
	wantUsage(t, mid, 9040, 20500, 51000, 0, "mid-turn (main + subagent)")
	// The heartbeat payload is the thing the server sees.
	if got := f.runner.Usage(); got.Model != "" {
		t.Errorf("after finish Usage().Model = %q, want empty — the finish model comes from "+
			"_meta.quota.model_usage (off in this script), the mid-turn one is discarded with the approximation", got.Model)
	}
	if res.Usage.Model != "" {
		t.Errorf("finish usage.model = %q, want empty — finish/model_drift are untouched by T-COSTMODEL", res.Usage.Model)
	}
}

// When model_usage IS reported at finish, it — not the mid-turn model —
// is what the finish carries, and drift is still judged against the profile.
func TestFinishModelUnchangedByMidturnModel(t *testing.T) {
	s := costModelScript(true)
	s.Turns[0].ModelUsage = true
	s.Turns[0].ReportModel = "claude-opus-5[1m],claude-haiku-4-5-20251001"
	res, mid, f := runMidturn(t, s, true)
	if mid.Model != mainModel {
		t.Fatalf("mid-turn model = %q, want %q", mid.Model, mainModel)
	}
	if res.Usage.Model != "claude-opus-5[1m],claude-haiku-4-5-20251001" {
		t.Errorf("finish model = %q, want the model_usage string verbatim", res.Usage.Model)
	}
	ev := f.sink.find("usage", "report", "report")
	if len(ev) == 0 {
		t.Fatal("no usage.report")
	}
	if ev[len(ev)-1].Payload["model_drift"] != true {
		t.Errorf("model_drift = %v, want true (profile 'sonnet' vs reported opus/haiku) — unchanged judgement", ev[len(ev)-1].Payload["model_drift"])
	}
}

// A stream that names no model (the pre-T-COSTMODEL fake, or an adapter that
// stops sending it) leaves usage.model empty — the server's fallback then
// decides; the daemon never invents one from the profile.
func TestMidturnUsageNoModelStaysEmpty(t *testing.T) {
	_, mid, _ := runMidturn(t, costModelScript(false), true)
	if mid.Model != "" {
		t.Fatalf("mid-turn model = %q with no message_start model on the wire, want empty", mid.Model)
	}
	wantUsage(t, mid, 9040, 20500, 51000, 0, "mid-turn (no model)")
}

// hermes path: no raw stream, so nothing mid-turn — including no model.
func TestMidturnModelAbsentOnHermesPath(t *testing.T) {
	var mid contracts.Usage
	var f *fixture
	s := costModelScript(true)
	s.Kind = "hermes"
	f = newFixture(t, s, bundle(contracts.RuntimeHermes), func(a *acp.Attempt) {
		a.RawSDKMessages = false
		a.Sink = &hookSink{inner: a.Sink, on: func(ev contracts.TaskEvent) {
			if ev.Class == "tool" && ev.Outcome == "ok" {
				mid = f.runner.Usage()
			}
		}}
	})
	f.run()
	if mid.Model != "" || mid.InputTokens != 0 {
		t.Fatalf("mid-turn with the raw stream off = %+v, want zero (hermes shape unchanged)", mid)
	}
}
