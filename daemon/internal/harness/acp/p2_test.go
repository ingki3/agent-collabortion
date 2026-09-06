package acp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// PRD §8.2.3 — the MCP list is filtered against the runtime's advertised
// `mcpCapabilities`. stdio is the ACP baseline and is never advertised, so it
// always survives; http/sse need the flag; an unknown transport is dropped.
func TestFilterMCPServersByCapabilities(t *testing.T) {
	all := []acp.MCPServer{
		{Name: "colab", Command: "colab", Args: []string{"mcp", "serve"}},
		{Name: "remote", Type: acp.MCPHTTP, URL: "https://example.test/mcp"},
		{Name: "stream", Type: acp.MCPSSE, URL: "https://example.test/sse"},
		{Name: "weird", Type: "carrier-pigeon"},
	}
	cases := []struct {
		name string
		caps acp.MCPCapabilities
		kept []string
	}{
		{"stdio only", acp.MCPCapabilities{}, []string{"colab"}},
		{"http", acp.MCPCapabilities{HTTP: true}, []string{"colab", "remote"}},
		{"http+sse", acp.MCPCapabilities{HTTP: true, SSE: true}, []string{"colab", "remote", "stream"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, dropped := acp.FilterMCPServers(all, tc.caps)
			var names []string
			for _, s := range kept {
				names = append(names, s.Name)
			}
			if strings.Join(names, ",") != strings.Join(tc.kept, ",") {
				t.Fatalf("kept %v want %v", names, tc.kept)
			}
			if len(kept)+len(dropped) != len(all) {
				t.Fatalf("kept %d + dropped %d != %d", len(kept), len(dropped), len(all))
			}
		})
	}
}

// The filter runs against what initialize advertised, and a dropped server is
// never silent: session/new carries only what the runtime accepts and the
// activity feed names the rest (harness §7 runtime class).
func TestMCPServersFilteredOnTheWire(t *testing.T) {
	servers := []acp.MCPServer{
		{Name: "colab", Command: "colab", Args: []string{"mcp", "serve"}, Env: []acp.EnvVar{}},
		{Name: "remote", Type: acp.MCPHTTP, URL: "https://example.test/mcp"},
	}
	for _, tc := range []struct {
		name     string
		http     bool
		wantSent []string
		wantNote bool
	}{
		{"http not advertised", false, []string{"colab"}, true},
		{"http advertised", true, []string{"colab", "remote"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle(contracts.RuntimeClaudeCode)
			f := newFixture(t, acpfake.Script{MCPHTTP: tc.http}, b, func(a *acp.Attempt) { a.MCPServers = servers })
			res := f.run()
			if res.Outcome != "completed" {
				t.Fatalf("result %+v", res)
			}
			var sent []string
			for _, r := range f.records() {
				if r.Method != acp.MethodSessionNew {
					continue
				}
				var p struct {
					MCPServers []acp.MCPServer `json:"mcpServers"`
				}
				if err := json.Unmarshal(r.Params, &p); err != nil {
					t.Fatal(err)
				}
				for _, s := range p.MCPServers {
					sent = append(sent, s.Name)
				}
			}
			if strings.Join(sent, ",") != strings.Join(tc.wantSent, ",") {
				t.Fatalf("session/new mcpServers %v want %v", sent, tc.wantSent)
			}
			notes := 0
			for _, e := range f.sink.all() {
				if e.Class == "runtime" && e.Outcome == "info" && strings.Contains(detail(e), "mcp server") {
					notes++
				}
			}
			if tc.wantNote != (notes == 1) {
				t.Fatalf("dropped-server feed notes = %d (want note: %v)", notes, tc.wantNote)
			}
			if got := len(res.MCPDropped); tc.wantNote != (got == 1) {
				t.Fatalf("Result.MCPDropped %v", res.MCPDropped)
			}
		})
	}
}

func detail(e contracts.TaskEvent) string {
	s, _ := e.Payload["detail"].(string)
	return s
}

// harness §6 / E8-02·03 — both Hermes loss paths must actually cold start on
// the wire: after the load that lost the session, a session/new follows and
// the brief goes with it (a cold start without the brief poisons the history,
// §3).
func TestHermesSessionLossColdStartsOnTheWire(t *testing.T) {
	cases := []struct {
		name   string
		script acpfake.Script
	}{
		{"load null", acpfake.Script{Kind: "hermes"}},
		{"provenance mismatch", acpfake.Script{Kind: "hermes", KnownSessions: []string{"old"},
			LoadProvenance: &acpfake.Provenance{ACPSessionID: "other", RootHermesSessionID: "other"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle(contracts.RuntimeHermes)
			b.Resume = resumeRef(contracts.RuntimeHermes, "old", "old")
			f := newFixture(t, tc.script, b, nil)
			res := f.run()
			if res.ResumeOutcome != "cold_start" || res.SessionRef == nil || res.SessionRef.SessionID != "sess-1" {
				t.Fatalf("result %+v ref %+v", res, res.SessionRef)
			}
			var order []string
			for _, r := range f.records() {
				switch r.Method {
				case acp.MethodSessionLoad, acp.MethodSessionNew, acp.MethodSessionPrompt:
					order = append(order, r.Method)
				}
			}
			want := []string{acp.MethodSessionLoad, acp.MethodSessionNew, acp.MethodSessionPrompt}
			if strings.Join(order, " ") != strings.Join(want, " ") {
				t.Fatalf("wire order %v want %v", order, want)
			}
			// Hermes carries the brief in AGENTS.md, never in _meta (§3, E12-09).
			for _, r := range f.records() {
				if strings.Contains(string(r.Params), `"_meta"`) {
					t.Fatalf("_meta sent to hermes: %s %s", r.Method, r.Params)
				}
			}
		})
	}
}

// PRD §8.4 / harness §10 — the daemon DELIVERS TaskBundle.brief.text, it does
// not compose it: [6][7][8] arrive byte-identical in _meta.systemPrompt.append
// and no brief file is written on the claude_code transport.
func TestBriefTextDeliveredByteIdentical(t *testing.T) {
	full := brief + "[6] context: previous session summary\n[7] decisions: chose Postgres\n[8] precedence: user > goal > agent\n"
	b := bundle(contracts.RuntimeClaudeCode)
	b.Brief.Text = full
	script := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{EchoBrief: true}}}}}
	f := newFixture(t, script, b, nil)
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	if res.Text != full {
		t.Fatalf("brief round trip mismatch:\n got %q\nwant %q", res.Text, full)
	}
}

// daemon-protocol §4.2 — the runner streams the partial text and only the
// partial text; preview.message_id is the server's (v0.5) and the Sink has
// nowhere to put one. The wire-level guard lives with the code that writes
// the heartbeat: api.TestDaemonNeverFillsPreviewMessageID.
func TestPreviewCarriesTheGrowingTurnText(t *testing.T) {
	script := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "part one "}, {Chunk: "part two"}}}}}
	f := newFixture(t, script, bundle(contracts.RuntimeClaudeCode), nil)
	if res := f.run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	f.sink.mu.Lock()
	defer f.sink.mu.Unlock()
	if len(f.sink.previews) < 2 || f.sink.previews[len(f.sink.previews)-1] != "part one part two" {
		t.Fatalf("previews %q", f.sink.previews)
	}
}

// daemon-protocol §4.4 v0.4 — "프로파일 폴백은 서버가 결정한다": the server
// owns attempts, tokens and workdir.reuse, and the session is pinned to one
// runtime_id, so "same machine" (E8-08) is structural and "no alternative →
// queued, never another machine" (E8-09) is its call. The daemon's whole part
// is to report a failure_kind precise enough for that decision — a retryable
// kind may move to the fallback profile, a non-retryable one must not.
func TestFailureKindRetryabilityIsReportedPrecisely(t *testing.T) {
	rl := acp.RateLimitMeta{Status: "rejected", ResetsAt: time.Now().Add(time.Hour).Unix()}
	cases := []struct {
		name      string
		script    acpfake.Script
		kind      contracts.RuntimeKind
		want      contracts.FailureKind
		retryable bool
	}{
		{"protocol version", acpfake.Script{ProtocolVersion: 2}, contracts.RuntimeClaudeCode, contracts.FailConfig, false},
		{"adapter pin drift", acpfake.Script{AgentVersion: "0.73.0"}, contracts.RuntimeClaudeCode, contracts.FailConfig, false},
		{"auth", acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"errorKind":"authentication_failed"}`)}}}}, contracts.RuntimeClaudeCode, contracts.FailAuth, false},
		{"quota", acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"errorKind":"billing_error"}`)}}}}, contracts.RuntimeClaudeCode, contracts.FailQuota, false},
		{"rate limited", acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Usage: &acpfake.UsageStep{Used: 10, RateLimit: &rl}}}, Error: &acp.RPCError{Code: -32603, Message: "Internal error"}}}}, contracts.RuntimeClaudeCode, contracts.FailRateLimited, true},
		{"hermes provider error body", acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "API call failed after 1 retries: HTTP 500 upstream"}}}}}, contracts.RuntimeHermes, contracts.FailOther, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.script, bundle(tc.kind), nil)
			res := f.run()
			if res.Outcome != "failed" || res.Failure == nil {
				t.Fatalf("result %+v", res)
			}
			if res.Failure.Kind != tc.want {
				t.Fatalf("failure_kind %q want %q (detail %q)", res.Failure.Kind, tc.want, res.Failure.Detail)
			}
			if got := res.Failure.Kind.Retryable(); got != tc.retryable {
				t.Fatalf("%s retryable=%v want %v", res.Failure.Kind, got, tc.retryable)
			}
		})
	}
}
