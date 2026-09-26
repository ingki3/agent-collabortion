package acp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
				if e.Class == "runtime" && e.Outcome == "info" && strings.Contains(detail(e), "도구 서버") {
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

// SCREEN §4.6 v0.19.10 「작업 중」 말풍선 — text the agent writes between tool
// calls is a progress note; the turn text puts one blank line at each tool-call
// boundary so the screen can split paragraphs ("a" · tool · "b" → "a\n\nb").
// Chunks with no tool between them join as-is, nothing goes before the first
// text, and a text already ending in a newline is not doubled.
func TestSayOpensAParagraphAtEachToolBoundary(t *testing.T) {
	tool := func(id string) acpfake.Step {
		return acpfake.Step{ToolCall: &acpfake.ToolCallStep{ID: id, Title: "ls", Kind: "execute"}}
	}
	done := func(id string) acpfake.Step {
		return acpfake.Step{ToolUpdate: &acpfake.ToolUpdateStep{ID: id, Status: "completed"}}
	}
	cases := []struct {
		name  string
		steps []acpfake.Step
		want  string
	}{
		{"chunk tool chunk", []acpfake.Step{{Chunk: "a"}, tool("t1"), done("t1"), {Chunk: "b"}}, "a\n\nb"},
		{"consecutive chunks join", []acpfake.Step{{Chunk: "a"}, {Chunk: "b"}, tool("t1"), done("t1"), {Chunk: "c"}, {Chunk: "d"}}, "ab\n\ncd"},
		{"already ends in newline", []acpfake.Step{{Chunk: "a\n"}, tool("t1"), done("t1"), {Chunk: "b"}}, "a\n\nb"},
		{"already ends in blank line", []acpfake.Step{{Chunk: "a\n\n"}, tool("t1"), done("t1"), {Chunk: "b"}}, "a\n\nb"},
		{"tool before first text", []acpfake.Step{tool("t1"), done("t1"), {Chunk: "a"}}, "a"},
		{"two tools one boundary", []acpfake.Step{{Chunk: "a"}, tool("t1"), done("t1"), tool("t2"), done("t2"), {Chunk: "b"}}, "a\n\nb"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := acpfake.Script{Turns: []acpfake.Turn{{Steps: tc.steps}}}
			f := newFixture(t, script, bundle(contracts.RuntimeClaudeCode), nil)
			res := f.run()
			if res.Outcome != "completed" {
				t.Fatalf("result %+v", res)
			}
			if res.Text != tc.want {
				t.Fatalf("turn text %q want %q", res.Text, tc.want)
			}
			f.sink.mu.Lock()
			defer f.sink.mu.Unlock()
			if n := len(f.sink.previews); n == 0 || f.sink.previews[n-1] != tc.want {
				t.Fatalf("previews %q want last %q", f.sink.previews, tc.want)
			}
		})
	}
}

// T-BUBBLE NN2 (PR #356 리뷰) — the two orders the boundary rule has to get
// right and that the first round only argued for in prose: a tool call that
// arrives BEFORE any text must not open the turn with a blank line, and an
// empty chunk must not swallow the boundary a tool call opened.
func TestSayBoundaryHandlesToolFirstAndEmptyChunks(t *testing.T) {
	tool := func(id string) acpfake.Step {
		return acpfake.Step{ToolCall: &acpfake.ToolCallStep{ID: id, Title: "ls", Kind: "execute"}}
	}
	done := func(id string) acpfake.Step {
		return acpfake.Step{ToolUpdate: &acpfake.ToolUpdateStep{ID: id, Status: "completed"}}
	}
	cases := []struct {
		name  string
		steps []acpfake.Step
		want  string
	}{
		// A turn that starts by running something: the text that follows is the
		// turn's FIRST paragraph, so nothing precedes it.
		{"tool first, text after", []acpfake.Step{tool("t1"), done("t1"), {Chunk: "a"}, {Chunk: "b"}}, "ab"},
		{"tool first, then tool and text", []acpfake.Step{tool("t1"), done("t1"), {Chunk: "a"}, tool("t2"), done("t2"), {Chunk: "b"}}, "a\n\nb"},
		// An empty agent_message_chunk is a no-op, not a paragraph: the pending
		// boundary has to survive it and land on the next real text. Real
		// adapters do send these (a flush with nothing new).
		{"empty chunk keeps the pending boundary", []acpfake.Step{{Chunk: "a"}, tool("t1"), done("t1"), {EmptyChunk: true}, {Chunk: "b"}}, "a\n\nb"},
		{"empty chunk does not open the turn with a blank line", []acpfake.Step{{EmptyChunk: true}, tool("t1"), done("t1"), {Chunk: "a"}}, "a"},
		{"empty chunk between two texts changes nothing", []acpfake.Step{{Chunk: "a"}, {EmptyChunk: true}, {Chunk: "b"}}, "ab"},
		// The boundary is only SPENT by real text: an empty chunk arriving on a
		// pending boundary must not leave the turn ending in a blank line — that
		// trailing break would be stored in the message a person reads.
		{"empty chunk last does not leave a trailing blank line", []acpfake.Step{{Chunk: "a"}, tool("t1"), done("t1"), {EmptyChunk: true}}, "a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := acpfake.Script{Turns: []acpfake.Turn{{Steps: tc.steps}}}
			f := newFixture(t, script, bundle(contracts.RuntimeClaudeCode), nil)
			res := f.run()
			if res.Outcome != "completed" {
				t.Fatalf("result %+v", res)
			}
			if res.Text != tc.want {
				t.Fatalf("turn text %q want %q", res.Text, tc.want)
			}
		})
	}
}

// T-BUBBLE NN1 (Lead 판정 2026-09-26) — the heartbeat preview carries at most
// the last acp.PreviewMaxChars characters, cut at a paragraph boundary with
// the elision marker in front. The persisted turn text is NOT clipped.
func TestClipPreviewKeepsTheTailAtAParagraphBoundary(t *testing.T) {
	para := func(n int, r rune) string { return strings.Repeat(string(r), n) }
	t.Run("under the cap is untouched", func(t *testing.T) {
		s := para(acp.PreviewMaxChars, '가')
		if got := acp.ClipPreview(s); got != s {
			t.Fatalf("clipped at the cap: %d chars", len([]rune(got)))
		}
	})
	t.Run("cuts at the paragraph boundary and marks the elision", func(t *testing.T) {
		// 3 paragraphs of 9,000: the 16,000-char tail starts inside the second,
		// so the cut moves forward to the second → third boundary.
		s := strings.Join([]string{para(9000, '가'), para(9000, '나'), para(9000, '다')}, "\n\n")
		got := acp.ClipPreview(s)
		if !strings.HasPrefix(got, acp.PreviewElided) {
			t.Fatalf("no elision marker: %.40q", got)
		}
		body := strings.TrimPrefix(got, acp.PreviewElided)
		if !strings.HasPrefix(body, para(100, '다')) {
			t.Fatalf("body does not start at a paragraph boundary: %.40q", body)
		}
		if strings.ContainsRune(body, '나') || strings.ContainsRune(body, '가') {
			t.Fatalf("body carries an earlier paragraph")
		}
		if n := len([]rune(body)); n > acp.PreviewMaxChars {
			t.Fatalf("body %d chars > cap %d", n, acp.PreviewMaxChars)
		}
	})
	t.Run("no boundary in the tail — character cut, no mid-rune split", func(t *testing.T) {
		s := para(acp.PreviewMaxChars*2, '나')
		got := acp.ClipPreview(s)
		body := strings.TrimPrefix(got, acp.PreviewElided)
		if body == got {
			t.Fatalf("no elision marker: %.40q", got)
		}
		if n := len([]rune(body)); n != acp.PreviewMaxChars {
			t.Fatalf("body %d chars want %d", n, acp.PreviewMaxChars)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("cut split a rune")
		}
	})
	t.Run("a boundary at the very end does not empty the body", func(t *testing.T) {
		s := para(acp.PreviewMaxChars*2, '다') + "\n\n"
		body := strings.TrimPrefix(acp.ClipPreview(s), acp.PreviewElided)
		if strings.TrimSpace(body) == "" {
			t.Fatalf("empty body")
		}
	})
	t.Run("live turn — preview is clipped, the persisted turn text is not", func(t *testing.T) {
		// 6 chunks of 4,000 with a tool call between each: 24,000 chars of text.
		marks := []rune{'가', '나', '다', '라', '마', '바'}
		steps := []acpfake.Step{}
		for i, m := range marks {
			steps = append(steps, acpfake.Step{Chunk: para(4000, m)})
			if i < len(marks)-1 {
				id := string(rune('a' + i))
				steps = append(steps,
					acpfake.Step{ToolCall: &acpfake.ToolCallStep{ID: id, Title: "ls", Kind: "execute"}},
					acpfake.Step{ToolUpdate: &acpfake.ToolUpdateStep{ID: id, Status: "completed"}})
			}
		}
		f := newFixture(t, acpfake.Script{Turns: []acpfake.Turn{{Steps: steps}}}, bundle(contracts.RuntimeClaudeCode), nil)
		res := f.run()
		if res.Outcome != "completed" {
			t.Fatalf("result %+v", res)
		}
		full := len(marks)*4000 + (len(marks)-1)*2
		if n := len([]rune(res.Text)); n != full {
			t.Fatalf("persisted turn text %d chars want %d — the cap must not reach finish", n, full)
		}
		// The persisted `message.say` body is the other half of "preview only":
		// it is what a person reads in the timeline after the turn.
		says := f.sink.find("message", "say", "ok")
		if len(says) != 1 {
			t.Fatalf("message.say events: %d", len(says))
		}
		var said struct {
			Text string `json:"text"`
		}
		b, _ := json.Marshal(says[0].Payload)
		if json.Unmarshal(b, &said) != nil {
			t.Fatalf("say payload %v", says[0].Payload)
		}
		if n := len([]rune(said.Text)); n != full {
			t.Fatalf("persisted message.say %d chars want %d — the cap must not reach the stored body", n, full)
		}
		if strings.Contains(said.Text, acp.PreviewElided) {
			t.Fatalf("elision marker leaked into the persisted body")
		}
		f.sink.mu.Lock()
		defer f.sink.mu.Unlock()
		last := f.sink.previews[len(f.sink.previews)-1]
		if !strings.HasPrefix(last, acp.PreviewElided) {
			t.Fatalf("last preview not clipped: %d chars", len([]rune(last)))
		}
		if n := len([]rune(strings.TrimPrefix(last, acp.PreviewElided))); n > acp.PreviewMaxChars {
			t.Fatalf("preview body %d chars > cap %d", n, acp.PreviewMaxChars)
		}
		// 마지막 문단은 통째로 살아 있다 — 화면이 보여 주는 것이 그 문단이다.
		if !strings.Contains(last, para(4000, '바')) {
			t.Fatalf("last paragraph lost from the preview")
		}
	})
}
