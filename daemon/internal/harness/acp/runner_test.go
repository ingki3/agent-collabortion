package acp_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

type memSink struct {
	mu       sync.Mutex
	events   []contracts.TaskEvent
	previews []string
}

func (s *memSink) Emit(ev contracts.TaskEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *memSink) Preview(t string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.previews = append(s.previews, t)
}

func (s *memSink) nPreviews() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.previews)
}

func (s *memSink) all() []contracts.TaskEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]contracts.TaskEvent{}, s.events...)
}

func (s *memSink) find(class, verb, outcome string) []contracts.TaskEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []contracts.TaskEvent
	for _, e := range s.events {
		if e.Class == class && (verb == "" || e.Verb == verb) && (outcome == "" || e.Outcome == outcome) {
			out = append(out, e)
		}
	}
	return out
}

const brief = "[1] identity\n[2] rules\n[4] session\n[5] roster\n[8] precedence\n"

func bundle(kind contracts.RuntimeKind) contracts.TaskBundle {
	b := contracts.TaskBundle{
		Task:      contracts.BundleTask{ID: "11111111-1111-1111-1111-111111111111", Attempt: 1, LaneID: "lane-1", SessionID: "sess-A", AgentID: "ag", AgentName: "Lead"},
		TaskToken: "ctk_test",
		Profile:   contracts.BundleProfile{RuntimeKind: kind, Model: "sonnet", AdapterPin: contracts.ClaudeAgentACPPin},
		Workdir:   contracts.BundleWorkdir{Kind: "dir", Reuse: true},
		Brief:     contracts.BundleBrief{Transport: contracts.BriefACPMetaSystemPrompt, Text: brief},
		Prompt:    "say PONG",
		Limits:    contracts.BundleLimits{StallSeconds: 180},
	}
	if kind == contracts.RuntimeHermes {
		b.Brief.Transport = contracts.BriefInstructionFile
	}
	return b
}

type fixture struct {
	t      *testing.T
	sink   *memSink
	record string
	dir    string
	runner *acp.Runner
	clk    clock.Clock
}

func newFixture(t *testing.T, script acpfake.Script, b contracts.TaskBundle, mut func(*acp.Attempt)) *fixture {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(t.TempDir(), "record.jsonl")
	cmd, args, env := acpfake.Command(script, record)
	f := &fixture{t: t, sink: &memSink{}, record: record, dir: dir}
	a := acp.Attempt{
		Bundle:  b,
		Workdir: dir,
		Cmd:     acp.Config{Command: cmd, Args: args, Env: env, KillAfter: 2 * time.Second},
		Sink:    f.sink,
		Quiet:   250 * time.Millisecond,
	}
	if mut != nil {
		mut(&a)
	}
	f.clk = a.Clock
	f.runner = acp.New(a)
	return f
}

func (f *fixture) run() acp.Result {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return f.runner.Run(ctx)
}

func (f *fixture) records() []acpfake.Record {
	r, err := acpfake.ReadRecords(f.record)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func assertGroupGone(t *testing.T, pgid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(-pgid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process group %d still alive", pgid)
}

// E12-01 — allow_once chosen by kind, optionId arbitrary.
func TestPermissionAllowOnceByKindNotID(t *testing.T) {
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{Permission: &acpfake.PermissionStep{ID: "t1", Title: "Bash ls", Kinds: []string{"allow_always", "allow_once", "reject_once"}, IDPrefix: "weird"}},
		{Chunk: "done"},
	}}}}
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "completed" || res.StopReason != "end_turn" {
		t.Fatalf("result %+v", res)
	}
	ev := f.sink.find("tool", "permission", "allowed")
	if len(ev) != 1 || ev[0].Payload["option_kind"] != "allow_once" || ev[0].ObjectRef != "Bash ls" {
		t.Fatalf("permission events %+v", ev)
	}
	var answered bool
	for _, r := range f.records() {
		if r.Method == "" && strings.Contains(string(r.Result), `"optionId":"weird-1"`) {
			answered = true
		}
	}
	if !answered {
		t.Fatal("harness did not answer with the allow_once optionId weird-1")
	}
	if len(f.sink.find("tool", "run_shell", "ok")) != 1 {
		t.Fatalf("tool call after allow not ok: %+v", f.sink.all())
	}
}

// E12-02·03 — no allow_once → reject_once, counted ×3 → capability flag input.
func TestPermissionAllowOnceMissingFallsBackAndCounts(t *testing.T) {
	step := acpfake.Step{Permission: &acpfake.PermissionStep{ID: "t", Title: "Write f", Kinds: []string{"reject_once"}}}
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{step, step, step, {Chunk: "still going"}}}}}
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("turn should continue after rejections: %+v", res)
	}
	rej := f.sink.find("tool", "permission", "rejected")
	if len(rej) != 3 || rej[0].Payload["allow_once_missing"] != true || rej[0].Payload["option_kind"] != "reject_once" {
		t.Fatalf("rejected events %+v", rej)
	}
	if res.AllowOnceMissing != 3 {
		t.Fatalf("AllowOnceMissing = %d", res.AllowOnceMissing)
	}
	if res.Text != "still going" {
		t.Fatalf("text %q", res.Text)
	}
}

// §4 — tool outside the profile allow-list → reject_once regardless.
func TestPermissionProfileToolAllowList(t *testing.T) {
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{Permission: &acpfake.PermissionStep{ID: "t1", Title: "Bash rm", ToolName: "Bash", Kinds: []string{"allow_once", "reject_once"}}},
		{Permission: &acpfake.PermissionStep{ID: "t2", Title: "Read f", ToolName: "Read", Kinds: []string{"allow_once", "reject_once"}}},
	}}}}
	b := bundle(contracts.RuntimeClaudeCode)
	b.Profile.Tools = []string{"Read"}
	f := newFixture(t, s, b, nil)
	f.run()
	rej, alw := f.sink.find("tool", "permission", "rejected"), f.sink.find("tool", "permission", "allowed")
	if len(rej) != 1 || len(alw) != 1 {
		t.Fatalf("events %+v", f.sink.find("tool", "permission", ""))
	}
	// §4 row 4 "outcome=rejected(policy)" → permission.policy (task_event v0.2 N3)
	if rej[0].Payload["policy"] != "denied_by_profile" || rej[0].Payload["option_kind"] != "reject_once" || alw[0].Payload["policy"] != "allowed_by_profile" {
		t.Fatalf("policy payloads rejected=%+v allowed=%+v", rej[0].Payload, alw[0].Payload)
	}
	// _meta must carry the allow-list into disallowedTools + permissions.deny (§3)
	for _, r := range f.records() {
		if r.Method == acp.MethodSessionNew {
			p := string(r.Params)
			if !strings.Contains(p, `"disallowedTools":["AskUserQuestion"`) || !strings.Contains(p, `"Bash"`) || !strings.Contains(p, `"deny":[`) {
				t.Fatalf("session/new _meta lacks tool restrictions: %s", p)
			}
		}
	}
}

// E10-03 — cancel while a permission request is pending: answered
// "cancelled", session/cancel sent, stopReason cancelled, group gone (§5).
func TestCancelDuringPermissionRequest(t *testing.T) {
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{Chunk: "working"},
		{SleepMs: 400},
		{Permission: &acpfake.PermissionStep{ID: "t1", Title: "Bash rm", Kinds: []string{"allow_once", "reject_once"}}},
		{Chunk: "never"},
	}}}}
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.run(); close(done) }()
	waitFor(t, func() bool { return f.sink.nPreviews() > 0 })
	f.runner.Cancel(context.Background(), acp.CancelRequest{Reason: "director"})
	<-done
	if res.Outcome != "cancelled" || res.Failure == nil || res.Failure.Kind != contracts.FailCancelled {
		t.Fatalf("result %+v", res)
	}
	if pc := f.sink.find("tool", "permission", "cancelled"); len(pc) != 1 {
		t.Fatalf("permission not answered cancelled: %+v", f.sink.find("tool", "permission", ""))
	} else if _, has := pc[0].Payload["option_kind"]; has {
		t.Fatalf("option_kind recorded although nothing was chosen (task_event v0.2 N2): %+v", pc[0].Payload)
	}
	// order: session/cancel before the permission answer
	var cancelIdx, answerIdx = -1, -1
	for i, r := range f.records() {
		if r.Method == acp.MethodSessionCancel && cancelIdx < 0 {
			cancelIdx = i
		}
		if r.Method == "" && strings.Contains(string(r.Result), "cancelled") {
			answerIdx = i
		}
	}
	if cancelIdx < 0 || answerIdx < cancelIdx {
		t.Fatalf("order cancel=%d answer=%d", cancelIdx, answerIdx)
	}
	if res.Text != "working" {
		t.Fatalf("text after cancel %q", res.Text)
	}
	assertGroupGone(t, res.PGID)
}

// §5 step 1 — after_current_tool waits ≤30s for an in-flight shell tool.
func TestCancelAfterCurrentToolWaitsThenForces(t *testing.T) {
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "Bash", Kind: "execute", Command: "sleep 100"}},
		{Hang: true},
	}}}}
	clk := clock.NewFake(time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Clock = clk })
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.run(); close(done) }()
	waitFor(t, func() bool { return len(f.sink.find("tool", "run_shell", "started")) == 1 })
	cancelDone := make(chan struct{})
	go func() {
		f.runner.Cancel(context.Background(), acp.CancelRequest{AfterCurrentTool: true, Reason: "director"})
		close(cancelDone)
	}()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-cancelDone:
		t.Fatal("cancel did not wait for the in-flight tool")
	default:
	}
	clk.Advance(contracts.CancelDrainWait + time.Second)
	<-cancelDone
	<-done
	if res.Outcome != "cancelled" {
		t.Fatalf("result %+v", res)
	}
	forced := false
	for _, e := range f.sink.find("runtime", "cancel", "info") {
		// §5 step markers carry no `detail`, so read it defensively.
		if d, _ := e.Payload["detail"].(string); strings.Contains(d, "30초") {
			forced = true
		}
	}
	if !forced {
		t.Fatalf("no forced-cancel note: %+v", f.sink.find("runtime", "cancel", ""))
	}
	assertGroupGone(t, res.PGID)
}

// G3 D-1 — the run context dying while the §5 procedure is still in its
// step-1 after_current_tool wait must not turn session/prompt's
// "context canceled" into failed(other): the cancel intent set by Cancel
// classifies it cancelled (E10-13).
func TestCancelIntentBeatsContextCancellation(t *testing.T) {
	s := acpfake.Script{StayAlive: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "Bash", Kind: "execute", Command: "sleep 100"}},
		{HangForever: true},
	}}}}
	clk := clock.NewFake(time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Clock = clk })
	ctx, cancel := context.WithCancel(context.Background())
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.runner.Run(ctx); close(done) }()
	waitFor(t, func() bool { return len(f.sink.find("tool", "run_shell", "started")) == 1 })
	go f.runner.Cancel(context.Background(), acp.CancelRequest{AfterCurrentTool: true, Reason: "kill_switch"})
	// the procedure is parked in step 1 (the tool never completes, the clock
	// never advances) — this is the D-1 race: ctx dies first.
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if res.Outcome != "cancelled" || res.Failure == nil || res.Failure.Kind != contracts.FailCancelled {
		t.Fatalf("result %+v — want cancelled, not failed(other)", res)
	}
	assertGroupGone(t, res.PGID)
}

// E12-04 — Hermes: chunk after the prompt response is captured (250ms wait).
func TestHermesLateChunkAfterResponse(t *testing.T) {
	s := acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PO"}}, LateChunk: "NG", LateDelayMs: 100}}}
	f := newFixture(t, s, bundle(contracts.RuntimeHermes), nil)
	res := f.run()
	if res.Text != "PONG" {
		t.Fatalf("text %q (late chunk lost)", res.Text)
	}
	say := f.sink.find("message", "say", "ok")
	if len(say) != 1 || say[0].Payload["text"] != "PONG" {
		t.Fatalf("say events %+v", say)
	}
	// hermes: set_model with anthropic: prefix, no _meta anywhere (E12-09)
	var setModel bool
	for _, r := range f.records() {
		if r.Method == acp.MethodSessionSetModel && strings.Contains(string(r.Params), `"anthropic:sonnet"`) {
			setModel = true
		}
		if strings.Contains(string(r.Params), "_meta") {
			t.Fatalf("hermes request carries _meta: %s %s", r.Method, r.Params)
		}
	}
	if !setModel {
		t.Fatal("session/set_model not sent for hermes")
	}
}

// §8 Hermes auxiliary signal — provider error as plain text with end_turn
// (observed in the real smoke: "API call failed after 1 retries: HTTP 429").
func TestHermesProviderErrorTextIsClassified(t *testing.T) {
	s := acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "API call failed after 1 retries: HTTP 429: This request would exceed your account's rate limit. Please try again later."}}}}}
	f := newFixture(t, s, bundle(contracts.RuntimeHermes), nil)
	res := f.run()
	if res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailRateLimited || res.Failure.NotBefore == nil {
		t.Fatalf("result %+v", res)
	}
	if ev := f.sink.find("runtime", "error", "failed"); len(ev) != 1 || ev[0].Payload["failure_kind"] != "rate_limited" {
		t.Fatalf("events %+v", ev)
	}
	// the error body is not posted as a message (§8 v0.3)
	if say := f.sink.find("message", "say", ""); len(say) != 0 {
		t.Fatalf("provider error body emitted as message: %+v", say)
	}
	// R4 — not evidence: a report that quotes the phrase, a bare HTTP 429, a real answer
	for _, body := range []string{
		"빌드 실패 원인: API call failed after 1 retries: HTTP 429 — 재시도 필요",
		"HTTP 429 Too Many Requests from the upstream; I will retry later.",
		"PONG",
	} {
		s2 := acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: body}}}}}
		f2 := newFixture(t, s2, bundle(contracts.RuntimeHermes), nil)
		if res := f2.run(); res.Outcome != "completed" || res.Failure != nil {
			t.Fatalf("%q → %+v", body, res)
		}
		if say := f2.sink.find("message", "say", "ok"); len(say) != 1 || say[0].Payload["text"] != body {
			t.Fatalf("%q say events %+v", body, say)
		}
	}
	// the prefix with tool activity is a real turn too
	s3 := acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "ls", Kind: "execute", Status: "completed"}},
		{Chunk: "API call failed after 1 retries: HTTP 429"},
	}}}}
	if res := newFixture(t, s3, bundle(contracts.RuntimeHermes), nil).run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	// auth prefix → auth
	s4 := acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "API call failed after 2 retries: HTTP 401: authentication_error"}}}}}
	if res := newFixture(t, s4, bundle(contracts.RuntimeHermes), nil).run(); res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailAuth {
		t.Fatalf("result %+v", res)
	}
}

// R3 / harness §1·§8 — initialize.agentInfo.version ≠ adapter pin → config
// (no session/new, no retry); the measured version is still reported.
func TestAdapterPinMismatchIsConfig(t *testing.T) {
	f := newFixture(t, acpfake.Script{AgentVersion: "0.73.0"}, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailConfig || !strings.Contains(res.Failure.Detail, `"0.73.0" != pin "0.74.0"`) {
		t.Fatalf("result %+v", res)
	}
	if res.AdapterVersion != "0.73.0" {
		t.Fatalf("measured adapter version %q", res.AdapterVersion)
	}
	for _, r := range f.records() {
		if r.Method == acp.MethodSessionNew || r.Method == acp.MethodSessionPrompt {
			t.Fatalf("%s sent after pin mismatch", r.Method)
		}
	}
	// profile pin is what counts when set
	b := bundle(contracts.RuntimeClaudeCode)
	b.Profile.AdapterPin = "0.73.0"
	if res := newFixture(t, acpfake.Script{AgentVersion: "0.73.0"}, b, nil).run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	// Hermes has no adapter pin
	if res := newFixture(t, acpfake.Script{Kind: "hermes", AgentVersion: "0.20.6"}, bundle(contracts.RuntimeHermes), nil).run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
}

// R2 / harness §2·§3, colab-cli.md §3 — the colab MCP server is the one
// entry of session/new and session/load mcpServers (both runtimes), carrying
// the attempt's COLAB_* env; with strictMcpConfig it is the only MCP the
// agent sees (raw system/init: mcp__colab__* only).
func TestColabMCPServerRegistered(t *testing.T) {
	b := bundle(contracts.RuntimeClaudeCode)
	env := acp.Env(contracts.RuntimeClaudeCode, acp.TaskEnv{TaskToken: b.TaskToken, ServerURL: "http://s", TaskID: b.Task.ID, Attempt: 1, LaneID: b.Task.LaneID, SessionID: b.Task.SessionID, AgentName: b.Task.AgentName}, nil)
	mcp := []acp.MCPServer{acp.ColabMCPServer("/opt/colab", env)}
	checkParams := func(t *testing.T, method string, raw json.RawMessage) {
		t.Helper()
		var p struct {
			MCPServers []acp.MCPServer `json:"mcpServers"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		if len(p.MCPServers) != 1 || p.MCPServers[0].Name != "colab" || p.MCPServers[0].Command != "/opt/colab" || strings.Join(p.MCPServers[0].Args, " ") != "mcp serve" {
			t.Fatalf("%s mcpServers %+v", method, p.MCPServers)
		}
		got := map[string]string{}
		for _, e := range p.MCPServers[0].Env {
			got[e.Name] = e.Value
		}
		for _, k := range []string{"COLAB_TASK_TOKEN", "COLAB_SERVER_URL", "COLAB_TASK_ID", "COLAB_TASK_ATTEMPT", "COLAB_LANE_ID", "COLAB_SESSION_ID", "COLAB_AGENT_NAME"} {
			if got[k] == "" {
				t.Fatalf("%s mcp env lacks %s: %v", method, k, got)
			}
		}
		if len(got) != 7 {
			t.Fatalf("%s mcp env %v", method, got)
		}
	}

	t.Run("claude_code new", func(t *testing.T) {
		f := newFixture(t, acpfake.Script{}, b, func(a *acp.Attempt) { a.MCPServers = mcp; a.RawSDKMessages = true })
		res := f.run()
		if res.Outcome != "completed" {
			t.Fatalf("result %+v", res)
		}
		n := 0
		for _, r := range f.records() {
			if r.Method == acp.MethodSessionNew {
				n++
				checkParams(t, r.Method, r.Params)
			}
		}
		if n != 1 {
			t.Fatalf("session/new count %d", n)
		}
		if res.RawInit == nil || strings.Join(res.RawInit.MCPServers, ",") != "colab" {
			t.Fatalf("raw init %+v", res.RawInit)
		}
		mcpTools := 0
		for _, tl := range res.RawInit.Tools {
			if strings.HasPrefix(tl, "mcp__") {
				mcpTools++
				if !strings.HasPrefix(tl, "mcp__colab__") {
					t.Fatalf("foreign mcp tool %s", tl)
				}
			}
		}
		if mcpTools == 0 {
			t.Fatalf("no mcp__colab__ tool: %v", res.RawInit.Tools)
		}
	})
	t.Run("claude_code load", func(t *testing.T) {
		rb := b
		rb.Resume = resumeRef(contracts.RuntimeClaudeCode, "sess-1", "")
		f := newFixture(t, acpfake.Script{KnownSessions: []string{"sess-1"}}, rb, func(a *acp.Attempt) { a.MCPServers = mcp })
		if res := f.run(); res.Outcome != "completed" || res.ResumeOutcome != "resumed" {
			t.Fatalf("result %+v", res)
		}
		n := 0
		for _, r := range f.records() {
			if r.Method == acp.MethodSessionLoad {
				n++
				checkParams(t, r.Method, r.Params)
			}
		}
		if n != 1 {
			t.Fatalf("session/load count %d", n)
		}
	})
	t.Run("hermes new", func(t *testing.T) {
		hb := bundle(contracts.RuntimeHermes)
		f := newFixture(t, acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}}, hb, func(a *acp.Attempt) { a.MCPServers = mcp })
		if res := f.run(); res.Outcome != "completed" {
			t.Fatalf("result %+v", res)
		}
		for _, r := range f.records() {
			if r.Method == acp.MethodSessionNew {
				checkParams(t, r.Method, r.Params)
				if strings.Contains(string(r.Params), "_meta") {
					t.Fatalf("hermes session/new carries _meta: %s", r.Params)
				}
			}
		}
	})
}

func resumeRef(kind contracts.RuntimeKind, id, root string) *contracts.RuntimeSessionRef {
	ref := &contracts.RuntimeSessionRef{RuntimeKind: kind, SessionID: id, CWD: "/x", CreatedAt: time.Now()}
	if root != "" {
		ref.Provenance = &contracts.HermesProvenance{ACPSessionID: id, RootHermesSessionID: root}
	}
	return ref
}

// E8-02·03 — Hermes resume: null → cold start, provenance mismatch → cold
// start, match → resumed, compression rotation → resumed with the new id.
func TestResumeHermes(t *testing.T) {
	cases := []struct {
		name        string
		script      acpfake.Script
		refID, root string
		wantOutcome string
		wantReason  string
		wantSession string
	}{
		{"null", acpfake.Script{Kind: "hermes"}, "old", "old", "cold_start", "load_null", "sess-1"},
		{"mismatch", acpfake.Script{Kind: "hermes", KnownSessions: []string{"old"}, LoadProvenance: &acpfake.Provenance{ACPSessionID: "other", RootHermesSessionID: "other"}}, "old", "old", "cold_start", "provenance_mismatch", "sess-1"},
		{"match", acpfake.Script{Kind: "hermes", KnownSessions: []string{"old"}}, "old", "old", "resumed", "", "old"},
		// Hermes 0.20.6 answers a deleted session with a bare `{}` — not null,
		// no provenance. Treating that as "resumed" ended the attempt
		// `completed` having done nothing at all (spike 4c, 5/5).
		{"no provenance", acpfake.Script{Kind: "hermes", KnownSessions: []string{"old"}, LoadNoProvenance: true}, "old", "old", "cold_start", "no_provenance", "sess-1"},
		{"rotation", acpfake.Script{Kind: "hermes", KnownSessions: []string{"old"}, LoadProvenance: &acpfake.Provenance{ACPSessionID: "rot", RootHermesSessionID: "old", CompressionDepth: 1}}, "old", "old", "resumed", "compression_rotation", "rot"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle(contracts.RuntimeHermes)
			b.Resume = resumeRef(contracts.RuntimeHermes, tc.refID, tc.root)
			f := newFixture(t, tc.script, b, nil)
			res := f.run()
			if res.Outcome != "completed" || res.ResumeOutcome != tc.wantOutcome || res.SessionRef == nil || res.SessionRef.SessionID != tc.wantSession {
				t.Fatalf("result %+v ref %+v", res, res.SessionRef)
			}
			ev := f.sink.find("runtime", "resume", tc.wantOutcome)
			if len(ev) != 1 {
				t.Fatalf("resume events %+v", f.sink.find("runtime", "resume", ""))
			}
			if got, _ := ev[0].Payload["resume_reason"].(string); got != tc.wantReason {
				t.Fatalf("reason %q want %q", got, tc.wantReason)
			}
		})
	}
}

// §6 claude_code: a lost session → cold start; known → resumed, replay
// chunks discarded, _meta + model re-sent after load (§12 (a)(b)).
//
// The lost-session error has two shapes and BOTH must become cold_start.
// The real adapter (0.74.0 + CLI 2.1.258) answers -32002 "Resource not found:
// <id>"; only the older -32000 "Session not found" was ever written down.
// Matching just the old string meant E8-02 never fired in the field — a forced
// cold start failed the attempt with failure_kind=other and burned all three
// attempts (spike 4c, 2026-09-06).
func TestResumeClaudeCode(t *testing.T) {
	for _, tc := range []struct{ name, kind string }{
		{"not found (adapter 0.74.0: -32002 Resource not found)", ""},
		{"not found (legacy: -32000 Session not found)", "legacy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := bundle(contracts.RuntimeClaudeCode)
			b.Resume = resumeRef(contracts.RuntimeClaudeCode, "gone", "")
			f := newFixture(t, acpfake.Script{LoadErrorKind: tc.kind}, b, nil)
			res := f.run()
			if res.ResumeOutcome != "cold_start" || res.SessionRef.SessionID != "sess-1" {
				t.Fatalf("result %+v", res)
			}
			if ev := f.sink.find("runtime", "resume", "cold_start"); len(ev) != 1 || ev[0].Payload["resume_reason"] != "session_not_found" {
				t.Fatalf("events %+v", ev)
			}
		})
	}
	t.Run("resumed replay dropped brief and model kept", func(t *testing.T) {
		s := acpfake.Script{KnownSessions: []string{"old"}, ReplayChunks: 5, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{EchoBrief: true}, {Chunk: "|"}, {EchoModel: true}}, ModelUsage: true}}}
		b := bundle(contracts.RuntimeClaudeCode)
		b.Resume = resumeRef(contracts.RuntimeClaudeCode, "old", "")
		f := newFixture(t, s, b, nil)
		res := f.run()
		if res.ResumeOutcome != "resumed" || res.SessionRef.SessionID != "old" {
			t.Fatalf("result %+v", res)
		}
		if strings.Contains(res.Text, "replayed") {
			t.Fatalf("replay chunks leaked into the turn text: %q", res.Text)
		}
		for _, e := range f.sink.all() {
			if e.Class == "message" && strings.Contains(e.Payload["text"].(string), "replayed") {
				t.Fatalf("replay chunk became a task_event: %+v", e)
			}
		}
		// (a) brief identifier survives load; (b) model re-applied after load
		if res.Text != brief+"|sonnet" {
			t.Fatalf("text %q", res.Text)
		}
		if len(res.Models) != 1 || res.Models[0] != "sonnet" {
			t.Fatalf("models %v", res.Models)
		}
		for _, e := range f.sink.find("usage", "report", "") {
			if e.Payload["model_drift"] == true {
				t.Fatalf("unexpected drift %+v", e)
			}
		}
		var loadIdx, setIdx = -1, -1
		metaInLoad := 0
		for i, r := range f.records() {
			if r.Method == acp.MethodSessionLoad {
				loadIdx = i
				metaInLoad = strings.Count(string(r.Params), `"systemPrompt"`)
			}
			if r.Method == acp.MethodSessionSetConfigOption && i > loadIdx && loadIdx >= 0 {
				setIdx = i
			}
		}
		if loadIdx < 0 || setIdx < 0 || metaInLoad != 1 {
			t.Fatalf("load=%d set=%d meta=%d", loadIdx, setIdx, metaInLoad)
		}
	})
}

// E12-08 — protocolVersion 2 → config, no retry.
func TestProtocolVersionMismatchIsConfig(t *testing.T) {
	f := newFixture(t, acpfake.Script{ProtocolVersion: 2}, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailConfig || res.Failure.Kind.Retryable() {
		t.Fatalf("result %+v", res)
	}
	if ev := f.sink.find("runtime", "error", "failed"); len(ev) != 1 || ev[0].Payload["failure_kind"] != "config" {
		t.Fatalf("error events %+v", ev)
	}
	assertGroupGone(t, res.PGID)
}

// §8 — rate_limited priority: prefix text with reset time, errorKind,
// structured rateLimit rejected; unknown -32603 → other.
func TestRateLimitedClassification(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC) // 09:00 Asia/Seoul
	clk := clock.NewFake(now)
	t.Run("prefix with reset", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "Internal error: You've hit your limit · resets 11am (Asia/Seoul)"}}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Clock = clk })
		res := f.run()
		if res.Failure == nil || res.Failure.Kind != contracts.FailRateLimited || res.Failure.NotBefore == nil {
			t.Fatalf("result %+v", res)
		}
		want := time.Date(2026, 9, 5, 2, 0, 0, 0, time.UTC) // 11:00 KST
		if !res.Failure.NotBefore.Equal(want) {
			t.Fatalf("not_before %v want %v", res.Failure.NotBefore, want)
		}
		ev := f.sink.find("runtime", "error", "failed")
		if len(ev) != 1 || ev[0].Payload["failure_kind"] != "rate_limited" || ev[0].Payload["not_before"] != want.Format(time.RFC3339) {
			t.Fatalf("events %+v", ev)
		}
	})
	t.Run("prefix without reset is quota", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "Internal error: Your org is out of usage · contact your admin"}}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
		if res := f.run(); res.Failure == nil || res.Failure.Kind != contracts.FailQuota {
			t.Fatalf("result %+v", res)
		}
	})
	t.Run("errorKind", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"errorKind":"rate_limit"}`)}}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Clock = clk })
		res := f.run()
		if res.Failure == nil || res.Failure.Kind != contracts.FailRateLimited || !res.Failure.NotBefore.Equal(now.Add(contracts.RateLimitFallback)) {
			t.Fatalf("result %+v", res)
		}
	})
	t.Run("structured rejected", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{
			Steps: []acpfake.Step{{Usage: &acpfake.UsageStep{Used: 10, RateLimit: &acp.RateLimitMeta{Status: "rejected", ResetsAt: 1788591000, RateLimitType: "five_hour", Utilization: 1}}}},
			Error: &acp.RPCError{Code: -32603, Message: "Internal error: something"},
		}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
		res := f.run()
		if res.Failure == nil || res.Failure.Kind != contracts.FailRateLimited || res.Failure.NotBefore.Unix() != 1788591000 {
			t.Fatalf("result %+v", res)
		}
		us := f.sink.find("usage", "report", "report")
		if len(us) != 1 {
			t.Fatalf("usage events %+v", us)
		}
		rl := us[0].Payload["rate_limit"].(map[string]any)
		if rl["status"] != "rejected" || rl["type"] != "five_hour" {
			t.Fatalf("rate_limit payload %+v", rl)
		}
	})
	t.Run("other", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "Internal error: boom"}}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
		if res := f.run(); res.Failure == nil || res.Failure.Kind != contracts.FailOther {
			t.Fatalf("result %+v", res)
		}
	})
}

// E12-09 — claude_code: exactly one _meta.systemPrompt on session/new, no
// instruction file written; session/new → set_config_option order.
func TestMetaInjectedOnceNoFile(t *testing.T) {
	f := newFixture(t, acpfake.Script{}, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	metaCount := 0
	var order []string
	for _, r := range f.records() {
		if r.Method != "" {
			order = append(order, r.Method)
		}
		if r.Method == acp.MethodSessionNew {
			p := string(r.Params)
			metaCount += strings.Count(p, `"systemPrompt"`)
			for _, want := range []string{`"append":"` + strings.ReplaceAll(brief, "\n", `\n`) + `"`, `"settingSources":[]`, `"strictMcpConfig":true`, `"permissionMode":"default"`, `"AskUserQuestion"`} {
				if !strings.Contains(p, want) {
					t.Fatalf("session/new _meta missing %s: %s", want, p)
				}
			}
		}
	}
	if metaCount != 1 {
		t.Fatalf("_meta.systemPrompt count = %d", metaCount)
	}
	if strings.Join(order, ",") != "initialize,session/new,session/set_config_option,session/prompt" {
		t.Fatalf("order %v", order)
	}
	// §7 v0.3: usage.report once at turn end (cumulative) even without model_usage / usage_update
	if us := f.sink.find("usage", "report", "report"); len(us) != 1 || us[0].Payload["cumulative"] != true || us[0].Payload["estimated"] != true {
		t.Fatalf("usage reports %+v", us)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if _, err := os_stat(filepath.Join(f.dir, name)); err == nil {
			t.Fatalf("%s written for claude_code", name)
		}
	}
}

// §12 (c) — isolation evidence: raw system/init has no mcp__ tools other
// than colab's (R2) and 0 hooks.
func TestRawInitIsolation(t *testing.T) {
	env := acp.Env(contracts.RuntimeClaudeCode, acp.TaskEnv{TaskToken: "ctk_test", ServerURL: "http://s", TaskID: "t", Attempt: 1}, nil)
	f := newFixture(t, acpfake.Script{}, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) {
		a.RawSDKMessages = true
		a.MCPServers = []acp.MCPServer{acp.ColabMCPServer("", env)}
	})
	res := f.run()
	if res.RawInit == nil {
		t.Fatal("no raw init captured")
	}
	for _, tl := range res.RawInit.Tools {
		if strings.HasPrefix(tl, "mcp__") && !strings.HasPrefix(tl, "mcp__colab__") {
			t.Fatalf("mcp tool leaked: %v", res.RawInit.Tools)
		}
	}
	for _, s := range res.RawInit.MCPServers {
		if s != "colab" {
			t.Fatalf("foreign mcp server %q: %v", s, res.RawInit.MCPServers)
		}
	}
	if res.RawInit.Hooks != 0 {
		t.Fatalf("raw init %+v", res.RawInit)
	}
}

// §7 — stall: no update for 3 minutes → failure stall, process gone.
func TestStallAfterThreeMinutes(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
	s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "hi"}, {Hang: true}}}}}
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Clock = clk })
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.run(); close(done) }()
	waitFor(t, func() bool { return f.sink.nPreviews() > 0 })
	clk.Advance(2 * time.Minute)
	select {
	case <-done:
		t.Fatal("stalled too early")
	case <-time.After(200 * time.Millisecond):
	}
	clk.Advance(2 * time.Minute)
	<-done
	if res.Outcome != "failed" || res.Failure == nil || res.Failure.Kind != contracts.FailStall || !res.Failure.Kind.Retryable() {
		t.Fatalf("result %+v", res)
	}
	assertGroupGone(t, res.PGID)
}

// E12-07 — normalisation: class/verb/object_ref/outcome filled, seq monotonic,
// edit diff line counts, shell exit code, plan, thought, model drift.
func TestEventNormalisation(t *testing.T) {
	old := "a\nb\n"
	exit := 1
	s := acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{
			{Thought: "hmm"},
			{Plan: []acp.PlanEntry{{Content: "one", Status: "completed"}, {Content: "two", Status: "in_progress"}}},
			{ToolCall: &acpfake.ToolCallStep{ID: "e1", Title: "Write note.txt", Kind: "edit", Path: "/w/note.txt"}},
			{ToolUpdate: &acpfake.ToolUpdateStep{ID: "e1", Status: "completed", Path: "/w/note.txt", OldText: &old, NewText: "a\nb\nc\nd\n"}},
			{ToolCall: &acpfake.ToolCallStep{ID: "s1", Title: "Bash", Kind: "execute", Command: "make test"}},
			{ToolUpdate: &acpfake.ToolUpdateStep{ID: "s1", Status: "failed", ExitCode: &exit, Text: "boom"}},
			{ToolCall: &acpfake.ToolCallStep{ID: "r1", Title: "Read File", Kind: "read", Path: "/w/x.go", Status: "completed"}},
			{ToolCall: &acpfake.ToolCallStep{ID: "o1", Title: "WebFetch", Kind: "fetch", Status: "completed"}},
			{Chunk: "PO"}, {Chunk: "NG"},
		},
		ModelUsage: true, ReportModel: "claude-haiku-4-5", Usage: &acp.PromptUsage{InputTokens: 10, OutputTokens: 5, CachedReadTokens: 100},
	}}}
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	seq := 0
	for _, e := range f.sink.all() {
		if e.Seq != seq+1 {
			t.Fatalf("seq not monotonic at %+v", e)
		}
		seq = e.Seq
		if e.Class == "" || e.Verb == "" || e.Outcome == "" || e.TaskID == "" || e.Attempt != 1 || e.TS.IsZero() {
			t.Fatalf("incomplete event %+v", e)
		}
		if e.Class == "tool" && e.ObjectRef == "" {
			t.Fatalf("tool event without object_ref %+v", e)
		}
	}
	if res.LastSeq != seq {
		t.Fatalf("LastSeq %d vs %d", res.LastSeq, seq)
	}
	edit := f.sink.find("tool", "edit_file", "")
	if len(edit) != 2 || edit[0].Outcome != "started" || edit[1].Outcome != "ok" || edit[1].ObjectRef != "/w/note.txt" || edit[1].Payload["lines_added"] != 4 || edit[1].Payload["lines_removed"] != 2 {
		t.Fatalf("edit events %+v", edit)
	}
	sh := f.sink.find("tool", "run_shell", "")
	if len(sh) != 2 || sh[1].Outcome != "failed" || sh[1].ObjectRef != "make" || sh[1].Payload["exit_code"] != 1 || sh[1].Payload["command"] != "make test" || sh[1].Payload["summary"] != "boom" {
		t.Fatalf("shell events %+v", sh)
	}
	if rd := f.sink.find("tool", "read", ""); len(rd) != 2 || rd[1].Outcome != "ok" {
		t.Fatalf("read events %+v", rd)
	}
	if se := f.sink.find("tool", "search", ""); len(se) != 2 || se[0].Payload["kind"] != "fetch" {
		t.Fatalf("search events %+v", se)
	}
	if pl := f.sink.find("plan", "update", "update"); len(pl) != 1 || pl[0].Payload["entries_total"] != 2 || pl[0].Payload["entries_done"] != 1 || pl[0].Payload["current"] != "two" {
		t.Fatalf("plan %+v", pl)
	}
	if th := f.sink.find("message", "think", "ok"); len(th) != 1 || th[0].Payload["text"] != "hmm" {
		t.Fatalf("think %+v", th)
	}
	if say := f.sink.find("message", "say", "ok"); len(say) != 1 || say[0].Payload["text"] != "PONG" || say[0].Payload["chars"] != 4 {
		t.Fatalf("say %+v", say)
	}
	if len(f.sink.previews) != 2 || f.sink.previews[1] != "PONG" {
		t.Fatalf("previews %v", f.sink.previews)
	}
	if te := f.sink.find("runtime", "turn_end", "ok"); len(te) != 1 || te[0].Payload["stop_reason"] != "end_turn" {
		t.Fatalf("turn_end %+v", te)
	}
	us := f.sink.find("usage", "report", "report")
	if len(us) != 1 || us[0].Payload["model_drift"] != true || us[0].Payload["model"] != "claude-haiku-4-5" || us[0].Payload["input_tokens"] != int64(10) {
		t.Fatalf("usage %+v", us)
	}
	if res.Usage.CacheReadTokens != 100 || res.Usage.OutputTokens != 5 {
		t.Fatalf("usage %+v", res.Usage)
	}
	// the say event is the last message event and comes after all tool events
	evs := f.sink.all()
	last := evs[len(evs)-1]
	if last.Class != "usage" {
		t.Fatalf("last event %+v", last)
	}
}

// §2 — process group is fully gone after a normal turn (E11-07 unit half).
func TestProcessGroupGoneAfterTurn(t *testing.T) {
	f := newFixture(t, acpfake.Script{StayAlive: true}, bundle(contracts.RuntimeClaudeCode), nil)
	res := f.run()
	if res.Outcome != "completed" || res.PGID == 0 {
		t.Fatalf("result %+v", res)
	}
	assertGroupGone(t, res.PGID)
}

// §2 — SIGTERM ignored → SIGKILL after KillAfter.
func TestCloseEscalatesToSigkill(t *testing.T) {
	f := newFixture(t, acpfake.Script{StayAlive: true, IgnoreSigterm: true}, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Cmd.KillAfter = 300 * time.Millisecond })
	res := f.run()
	assertGroupGone(t, res.PGID)
}

// spawn failure (CLI missing) → config.
func TestMissingAdapterIsConfig(t *testing.T) {
	f := newFixture(t, acpfake.Script{}, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Cmd.Command = "/nonexistent/hermes" })
	res := f.run()
	if res.Outcome != "failed" || res.Failure.Kind != contracts.FailConfig {
		t.Fatalf("result %+v", res)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// D-6 / harness v0.7 — what `cost_usd` and `estimated` mean, in all three
// states the wire can be in.
//
// The bug this pins: `estimated` used to be set only when `usage` was missing
// altogether, and `cost_usd` was never assigned at all, so the ordinary ACP
// turn (tokens, no cost — the only shape the pinned adapter sends) went out as
// `cost_usd: 0, estimated: false`. That reads as a measured $0.00 and every
// session's cost banner showed one (G4 3판 W16).
func TestUsageCostRule(t *testing.T) {
	cost := func(v float64) *float64 { return &v }

	// (1) the runtime priced the turn → carry its number, not an estimate.
	t.Run("cost reported", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{
			Steps: []acpfake.Step{{Chunk: "ok"}},
			Usage: &acp.PromptUsage{InputTokens: 10, OutputTokens: 5, CostUSD: cost(0.0125)},
		}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
		res := f.run()
		us := f.sink.find("usage", "report", "report")
		if len(us) != 1 || us[0].Payload["cost_usd"] != 0.0125 || us[0].Payload["estimated"] != false {
			t.Fatalf("usage %+v", us)
		}
		if res.Usage.CostUSD != 0.0125 || res.Usage.Estimated {
			t.Fatalf("result usage %+v", res.Usage)
		}
	})

	// (2) tokens but no cost — the real ACP shape. The key is ABSENT; a 0
	// here is the defect.
	t.Run("tokens only", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{
			Steps: []acpfake.Step{{Chunk: "ok"}},
			Usage: &acp.PromptUsage{InputTokens: 10, OutputTokens: 5, CachedReadTokens: 100},
		}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
		res := f.run()
		us := f.sink.find("usage", "report", "report")
		if len(us) != 1 || us[0].Payload["estimated"] != true {
			t.Fatalf("usage %+v", us)
		}
		if _, ok := us[0].Payload["cost_usd"]; ok {
			t.Fatalf("cost_usd present with no measured cost: %+v", us[0].Payload)
		}
		// the tokens still went up — this is an unknown cost, not unknown usage
		if us[0].Payload["input_tokens"] != int64(10) || us[0].Payload["cache_read_tokens"] != int64(100) {
			t.Fatalf("tokens %+v", us[0].Payload)
		}
		if !res.Usage.Estimated || res.Usage.InputTokens != 10 {
			t.Fatalf("result usage %+v", res.Usage)
		}
	})

	// (3) no usage at all → estimated, no cost, no tokens.
	t.Run("no usage", func(t *testing.T) {
		s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
		f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
		res := f.run()
		us := f.sink.find("usage", "report", "report")
		if len(us) != 1 || us[0].Payload["estimated"] != true {
			t.Fatalf("usage %+v", us)
		}
		if _, ok := us[0].Payload["cost_usd"]; ok {
			t.Fatalf("cost_usd present with no usage: %+v", us[0].Payload)
		}
		if !res.Usage.Estimated || res.Usage.InputTokens != 0 {
			t.Fatalf("result usage %+v", res.Usage)
		}
	})
}
