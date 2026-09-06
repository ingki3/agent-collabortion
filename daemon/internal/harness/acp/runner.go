package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
)

// Sink receives normalised task_events (seq assigned by the runner) and
// non-persisted streaming previews (daemon-protocol §4.2).
//
// Preview takes the partial text and NOTHING else. The §4.2 v0.3 wire pair is
// {text, message_id}, but §4.2 v0.5 makes message_id the SERVER's to fill:
// agents post through the colab CLI/MCP straight to the server
// (colab-cli.md §1), so the daemon sees neither the round trip nor the id the
// server minted — and a preview is by definition output that has not been
// posted yet, so at that moment no id exists at all. There is therefore no
// code path that could supply a correct id, and this interface deliberately
// offers no place to pass a wrong one: an id invented here would make the
// server attribute the delta to somebody else's message. The empty value goes
// on the wire in api.Batcher (see previewMessageID there).
type Sink interface {
	Emit(ev contracts.TaskEvent)
	Preview(text string)
}

// Attempt is one task attempt to run (daemon-protocol §4.1 TaskBundle plus
// what the daemon prepared for it).
type Attempt struct {
	Bundle  contracts.TaskBundle
	Workdir string
	// Cmd is the process to spawn (Command/Args/Env/StderrPath/KillAfter).
	// Dir is overridden with Workdir. Built by the loop from Command()/Env();
	// tests point it at the fake agent.
	Cmd           Config
	Sink          Sink
	Clock         clock.Clock
	DaemonVersion string
	// SetupTimeout bounds initialize / session/new / load / set model.
	// Zero → 3 minutes (npx cold start).
	SetupTimeout time.Duration
	// RawSDKMessages asks claude_code for the raw SDK stream (probe/smoke,
	// harness §12(c)); the system/init tools land in Result.RawInit.
	RawSDKMessages bool
	// OnSpawn fires once the process exists (pgid record, phase=preparing).
	OnSpawn func(pgid int)
	// OnRunning fires right before session/prompt (phase=running).
	OnRunning func()
	// Quiet is the post-response wait for Hermes (§2.2). Zero → 250ms.
	Quiet time.Duration
	// MCPServers go into session/new and session/load `mcpServers` (harness
	// §2 lifecycle: the colab MCP server, colab-cli.md §3). Built by the loop
	// with ColabMCPServer; nil → none (probe).
	MCPServers []MCPServer
}

// RawInit is the claude_code raw `system/init` evidence (§12(c)).
type RawInit struct {
	Tools      []string `json:"tools"`
	MCPServers []string `json:"mcp_servers"`
	Hooks      int      `json:"hook_started"`
}

// Result is what one attempt produced (→ daemon-protocol §4.4 finish).
type Result struct {
	Outcome       string // completed | failed | cancelled
	StopReason    string
	Failure       *Failure
	Usage         contracts.Usage
	SessionRef    *contracts.RuntimeSessionRef
	ResumeOutcome string // resumed | cold_start | ""
	LastSeq       int
	PGID          int
	// Text is the merged agent_message_chunk text of the turn.
	Text string
	// AllowOnceMissing counts §4 fallbacks; ≥3 → capability flag (E12-03).
	AllowOnceMissing int
	RawInit          *RawInit
	Models           []string
	AdapterVersion   string
	// AvailableModels is what session/new advertised (probe §9).
	AvailableModels []string
	// ProtocolVersion is what initialize answered (probe §9, measured — the
	// runner already refuses anything but contracts.ACPProtocolVersion).
	ProtocolVersion int
	// Caps is what the session advertised (PRD §8.2.1). probe §9 `resume`
	// comes from Caps.LoadSession, never from the runtime name.
	Caps AgentCaps
	// MCPDropped names the mcpServers the runtime could not accept (§8.2.3
	// mcpCapabilities filter). Empty on the v1 stdio-only path.
	MCPDropped []string
}

// CancelRequest is harness §5 input.
type CancelRequest struct {
	AfterCurrentTool bool
	Reason           string
}

// Runner executes one Attempt. Create with New, then Run once; Cancel may be
// called concurrently from another goroutine.
type Runner struct {
	a   Attempt
	clk clock.Clock
	c   *Client

	mu           sync.Mutex
	seq          int
	sessionID    string
	replaying    bool
	lastActivity time.Time
	say          strings.Builder
	think        strings.Builder
	tools        map[string]*toolState
	lastTool     *toolState
	toolDone     chan struct{} // closed when the in-flight edit/shell completes
	lastRL       *RateLimitMeta
	allowMissing int
	// intent is set the moment Cancel is called — before the §5 step-1
	// after_current_tool wait — so a session/prompt that ends with
	// "context canceled" while the procedure runs is classified cancelled,
	// not failed(other) (G3 D-1, E10-13). cancelling is the narrower §5
	// step-2 gate: only from there are pending permission requests answered
	// "cancelled". The stall watcher sets cancelling without intent.
	intent     bool
	cancelling bool
	cancelReq  *CancelRequest
	stalled    bool
	promptDone chan struct{}
	closeOnce  sync.Once
	rawInit    *RawInit
	caps       AgentCaps
	protocol   int
	mcp        []MCPServer
	mcpDropped []string
	planTotal  int
	planDone   int
	usage      contracts.Usage
	available  []string
}

// New prepares a Runner.
func New(a Attempt) *Runner {
	if a.Clock == nil {
		a.Clock = clock.Real{}
	}
	if a.SetupTimeout == 0 {
		a.SetupTimeout = 3 * time.Minute
	}
	if a.Quiet == 0 {
		a.Quiet = contracts.HermesQuietWait
	}
	return &Runner{a: a, clk: a.Clock, tools: map[string]*toolState{}, promptDone: make(chan struct{})}
}

func (r *Runner) kind() contracts.RuntimeKind { return r.a.Bundle.Profile.RuntimeKind }

// emit assigns seq/ts and forwards to the sink.
func (r *Runner) emit(class, verb, objectRef, outcome string, payload map[string]any) int {
	r.mu.Lock()
	r.seq++
	ev := contracts.TaskEvent{
		TaskID: r.a.Bundle.Task.ID, Attempt: r.a.Bundle.Task.Attempt, Seq: r.seq, TS: r.clk.Now().UTC(),
		Class: class, Verb: verb, ObjectRef: clip(objectRef, 512), Outcome: outcome, Payload: payload,
	}
	seq := r.seq
	r.mu.Unlock()
	if r.a.Sink != nil {
		r.a.Sink.Emit(ev)
	}
	return seq
}

func (r *Runner) touch() {
	r.mu.Lock()
	r.lastActivity = r.clk.Now()
	r.mu.Unlock()
}

// Usage returns the usage accumulated so far (heartbeat).
func (r *Runner) Usage() contracts.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.usage
}

// Run executes the attempt end to end. It never returns a Go error for
// runtime failures — those are classified into Result.Failure.
func (r *Runner) Run(ctx context.Context) Result {
	res := r.run(ctx)
	r.mu.Lock()
	res.LastSeq = r.seq
	res.AvailableModels = r.available
	res.AllowOnceMissing = r.allowMissing
	res.RawInit = r.rawInit
	res.Usage = r.usage
	res.Text = r.say.String()
	res.Caps = r.caps
	res.ProtocolVersion = r.protocol
	res.MCPDropped = r.mcpDropped
	r.mu.Unlock()
	if r.c != nil {
		res.PGID = r.c.PID()
		r.closeProcess()
	}
	return res
}

func (r *Runner) fail(kind contracts.FailureKind, detail string, nb *time.Time) Result {
	f := &Failure{Kind: kind, Detail: detail, NotBefore: nb}
	p := map[string]any{"runtime_kind": string(r.kind()), "failure_kind": string(kind), "detail": clip(detail, 2000)}
	if nb != nil {
		p["not_before"] = nb.UTC().Format(time.RFC3339)
	}
	r.emit("runtime", "error", "", "failed", p)
	return Result{Outcome: "failed", Failure: f}
}

func (r *Runner) classify(err error) Result {
	r.mu.Lock()
	rl, cancelled, req := r.lastRL, r.intent || r.cancelling, r.cancelReq
	r.mu.Unlock()
	if cancelled && !r.isStalled() {
		return Result{Outcome: "cancelled", StopReason: "cancelled", Failure: &Failure{Kind: contracts.FailCancelled, Detail: cancelReason(req)}}
	}
	f := Classify(ClassifyInput{Err: err, LastRateLimit: rl, Stderr: r.c.Stderr(), Now: r.clk.Now()})
	return r.fail(f.Kind, f.Detail, f.NotBefore)
}

func (r *Runner) isStalled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stalled
}

func (r *Runner) run(ctx context.Context) Result {
	b := r.a.Bundle
	cfg := r.a.Cmd
	cfg.Dir = r.a.Workdir
	if cfg.Command == "" {
		return r.fail(contracts.FailConfig, "no adapter command for runtime "+string(r.kind()), nil)
	}
	c, err := Spawn(ctx, cfg)
	if err != nil {
		return r.fail(contracts.FailConfig, "spawn: "+err.Error(), nil)
	}
	r.c = c
	c.Permission = r.decidePermission
	c.OnUpdate(r.onUpdate)
	if r.a.RawSDKMessages {
		c.OnNotification(r.onRawSDK)
	}
	r.touch()
	if r.a.OnSpawn != nil {
		r.a.OnSpawn(c.PID())
	}
	r.emit("runtime", "start", "", "started", map[string]any{"runtime_kind": string(r.kind()), "adapter_version": b.Profile.AdapterPin})

	sctx, cancelSetup := context.WithTimeout(ctx, r.a.SetupTimeout)
	defer cancelSetup()
	init, err := c.Initialize(sctx, r.a.DaemonVersion)
	if err != nil {
		return r.classify(err)
	}
	adapterVersion := ""
	if init.AgentInfo != nil {
		adapterVersion = init.AgentInfo.Version
	}
	r.applyCaps(init)
	// §1/§8 config: the adapter version must equal the pin — `_meta.*` is
	// adapter behaviour, not spec, so drift is a config error (no retry).
	if r.kind() == contracts.RuntimeClaudeCode {
		pin := b.Profile.AdapterPin
		if pin == "" {
			pin = AdapterPin
		}
		if adapterVersion != pin {
			res := r.fail(contracts.FailConfig, fmt.Sprintf("adapter version %q != pin %q", adapterVersion, pin), nil)
			res.AdapterVersion = adapterVersion
			return res
		}
	}

	meta := r.meta()
	var (
		sessionID     string
		resumeOutcome string
		provenance    *contracts.HermesProvenance
	)
	if b.Resume != nil && b.Resume.SessionID != "" {
		sid, prov, reason, ferr := r.load(sctx, meta)
		if ferr != nil {
			return r.classify(ferr)
		}
		if sid != "" {
			sessionID, provenance, resumeOutcome = sid, prov, "resumed"
			p := map[string]any{"runtime_kind": string(r.kind()), "session_id": sid}
			if reason != "" {
				p["resume_reason"] = reason
			}
			r.emit("runtime", "resume", sid, "resumed", p)
		} else {
			s, nerr := r.newSession(sctx, meta)
			if nerr != nil {
				return r.classify(nerr)
			}
			sessionID, provenance, resumeOutcome = s.SessionID, hermesProv(s), "cold_start"
			r.emit("runtime", "resume", sessionID, "cold_start", map[string]any{"runtime_kind": string(r.kind()), "session_id": sessionID, "resume_reason": reason})
		}
	} else {
		s, nerr := r.newSession(sctx, meta)
		if nerr != nil {
			return r.classify(nerr)
		}
		sessionID, provenance = s.SessionID, hermesProv(s)
	}
	r.mu.Lock()
	r.sessionID = sessionID
	r.mu.Unlock()
	ref := &contracts.RuntimeSessionRef{RuntimeKind: r.kind(), AdapterVersion: adapterVersion, SessionID: sessionID, CWD: r.a.Workdir, CreatedAt: r.clk.Now().UTC(), Provenance: provenance}
	if b.Resume != nil && resumeOutcome == "resumed" {
		ref.CreatedAt = b.Resume.CreatedAt
	}

	// Model — after session/new AND after every session/load (§1, 1b E1).
	if err := r.setModel(sctx, sessionID); err != nil {
		return r.fail(contracts.FailConfig, err.Error(), nil)
	}
	cancelSetup()

	if r.a.OnRunning != nil {
		r.a.OnRunning()
	}
	r.touch()
	stopStall := r.startStallWatch(ctx)
	pr, perr := c.Prompt(ctx, sessionID, b.Prompt)
	close(r.promptDone)
	stopStall()
	if r.kind() == contracts.RuntimeHermes {
		time.Sleep(r.a.Quiet) // §2.2: late agent_message_chunk after the response
	}
	r.mu.Lock()
	stalled, cancelled, cancelReq := r.stalled, r.intent || r.cancelling, r.cancelReq
	text, ntools := r.say.String(), len(r.tools)
	r.mu.Unlock()
	// §8 v0.3 Hermes body rule: a turn whose whole body is the provider
	// error format (and no tool activity) is a failure, judged in the same
	// place as refusal && 활동 0. The body is not posted as a message.
	var hermesFail *Failure
	if r.kind() == contracts.RuntimeHermes && perr == nil && pr.StopReason == "end_turn" && !cancelled {
		if f, ok := SniffHermesText(text, ntools, r.clk.Now()); ok {
			hermesFail = &f
		}
	}
	r.flushMessages(hermesFail == nil)

	base := Result{SessionRef: ref, ResumeOutcome: resumeOutcome, AdapterVersion: adapterVersion}
	if stalled {
		res := r.fail(contracts.FailStall, fmt.Sprintf("no session/update for %s", contracts.StallTimeout), nil)
		res.SessionRef, res.ResumeOutcome = ref, resumeOutcome
		return res
	}
	if perr != nil {
		if cancelled {
			res := base
			res.Outcome, res.StopReason = "cancelled", "cancelled"
			res.Failure = &Failure{Kind: contracts.FailCancelled, Detail: cancelReason(cancelReq)}
			return res
		}
		res := r.classify(perr)
		res.SessionRef, res.ResumeOutcome = ref, resumeOutcome
		return res
	}
	models := pr.ModelsUsed()
	r.recordUsage(pr, models)
	drift := false
	if len(models) > 0 && b.Profile.Model != "" {
		drift = true
		for _, m := range models {
			if ModelMatches(b.Profile.Model, m) {
				drift = false
			}
		}
	}
	tp := map[string]any{"runtime_kind": string(r.kind()), "session_id": sessionID, "stop_reason": pr.StopReason}
	r.emit("runtime", "turn_end", "", "ok", tp)
	// §7 v0.3: tokens/cost once per turn from the session/prompt usage
	// (usage_update carries only context size + rate_limit).
	up := map[string]any{"cumulative": true}
	if len(models) > 0 {
		up["model"] = strings.Join(models, ",")
	}
	if drift {
		up["model_drift"] = true
	}
	r.emit("usage", "report", "", "report", r.usagePayload(up))
	res := base
	res.Models = models
	res.StopReason = pr.StopReason
	if hermesFail != nil {
		res := r.fail(hermesFail.Kind, hermesFail.Detail, hermesFail.NotBefore)
		res.SessionRef, res.ResumeOutcome, res.Models, res.StopReason, res.AdapterVersion = ref, resumeOutcome, models, pr.StopReason, adapterVersion
		return res
	}
	switch pr.StopReason {
	case "cancelled":
		res.Outcome = "cancelled"
		res.Failure = &Failure{Kind: contracts.FailCancelled, Detail: cancelReason(cancelReq)}
	default:
		// end_turn, max_tokens, max_turn_requests, refusal → completed (§2.2)
		res.Outcome = "completed"
		if cancelled {
			res.Outcome = "cancelled"
			res.Failure = &Failure{Kind: contracts.FailCancelled, Detail: cancelReason(cancelReq)}
		}
	}
	return res
}

func cancelReason(c *CancelRequest) string {
	if c == nil {
		return ""
	}
	return c.Reason
}

func hermesProv(s *SessionResult) *contracts.HermesProvenance {
	acp, root, kind, depth, ok := s.HermesProvenance()
	if !ok {
		return nil
	}
	return &contracts.HermesProvenance{ACPSessionID: acp, RootHermesSessionID: root, SessionKind: kind, CompressionDepth: depth}
}

// applyCaps records what the session advertised and filters the MCP server
// list against `mcpCapabilities` (PRD §8.2.3). A server we cannot send is
// reported on the activity feed rather than dropped silently — the agent
// would otherwise just be missing a tool namespace with no trace.
func (r *Runner) applyCaps(init *InitializeResult) {
	caps := init.Caps()
	kept, dropped := FilterMCPServers(r.a.MCPServers, caps.MCP)
	names := make([]string, 0, len(dropped))
	for _, s := range dropped {
		names = append(names, s.Name)
	}
	r.mu.Lock()
	r.caps, r.protocol, r.mcp, r.mcpDropped = caps, init.ProtocolVersion, kept, names
	r.mu.Unlock()
	for _, s := range dropped {
		r.emit("runtime", "start", "", "info", map[string]any{
			"runtime_kind": string(r.kind()),
			"detail":       fmt.Sprintf("mcp server %q dropped: transport %s is not in the runtime mcpCapabilities", s.Name, s.Transport()),
		})
	}
}

// mcpServers is the filtered list actually sent on session/new·load.
func (r *Runner) mcpServers() []MCPServer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mcp
}

func (r *Runner) meta() map[string]any {
	b := r.a.Bundle
	if b.Brief.Transport != contracts.BriefACPMetaSystemPrompt {
		return nil // instruction_file path: no _meta injection (E12-09)
	}
	var deny []string
	if v, ok := b.Profile.Options["deny"]; ok {
		if list, ok := v.([]any); ok {
			for _, x := range list {
				if s, ok := x.(string); ok {
					deny = append(deny, s)
				}
			}
		}
	}
	return Meta(r.kind(), MetaOptions{Brief: b.Brief.Text, Tools: b.Profile.Tools, DenyRules: deny, RawSDKMessages: r.a.RawSDKMessages})
}

func (r *Runner) newSession(ctx context.Context, meta map[string]any) (*SessionResult, error) {
	s, err := r.c.NewSession(ctx, r.a.Workdir, r.mcpServers(), meta)
	if err != nil {
		return nil, err
	}
	if s.SessionID == "" {
		return nil, errors.New("session/new: empty sessionId")
	}
	var avail []string
	if s.Models != nil {
		for _, m := range s.Models.AvailableModels {
			avail = append(avail, m.ModelID)
		}
	} else {
		avail = ConfigOptionValues(s.ConfigOptions, "model")
	}
	r.mu.Lock()
	r.available = avail
	r.mu.Unlock()
	return s, nil
}

// load implements harness §6. Returns the live session id ("" → cold start
// with reason) or a hard error.
func (r *Runner) load(ctx context.Context, meta map[string]any) (sid string, prov *contracts.HermesProvenance, reason string, err error) {
	ref := r.a.Bundle.Resume
	r.mu.Lock()
	r.replaying = true
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.replaying = false
		r.mu.Unlock()
	}()
	s, err := r.c.LoadSession(ctx, r.a.Workdir, ref.SessionID, r.mcpServers(), meta)
	switch r.kind() {
	case contracts.RuntimeClaudeCode:
		if err != nil {
			var rpc *RPCError
			if errors.As(err, &rpc) && strings.Contains(strings.ToLower(rpc.Message), "session not found") {
				return "", nil, "session_not_found", nil
			}
			return "", nil, "", err
		}
		return ref.SessionID, nil, "", nil
	default: // hermes
		if err != nil {
			return "", nil, "", err
		}
		if s == nil {
			return "", nil, "load_null", nil
		}
		acp, root, kind, depth, ok := s.HermesProvenance()
		if !ok {
			return ref.SessionID, nil, "", nil
		}
		p := &contracts.HermesProvenance{ACPSessionID: acp, RootHermesSessionID: root, SessionKind: kind, CompressionDepth: depth}
		if acp == ref.SessionID {
			return ref.SessionID, p, "", nil
		}
		if ref.Provenance != nil && root != "" && root == ref.Provenance.RootHermesSessionID {
			// compression rotation: keep, store the new id (spike 4a rec. 2)
			return acp, p, "compression_rotation", nil
		}
		return "", nil, "provenance_mismatch", nil
	}
}

func (r *Runner) setModel(ctx context.Context, sessionID string) error {
	model := r.a.Bundle.Profile.Model
	if model == "" {
		return nil
	}
	switch r.kind() {
	case contracts.RuntimeClaudeCode:
		res, err := r.c.SetConfigOption(ctx, sessionID, "model", model)
		if err != nil {
			return fmt.Errorf("set model %q: %w", model, err)
		}
		if cur := ConfigOptionValue(res.ConfigOptions, "model"); cur != "" && !ModelMatches(model, cur) {
			return fmt.Errorf("model %q not applied (currentValue %q)", model, cur)
		}
		return nil
	case contracts.RuntimeHermes:
		id := model
		if !strings.Contains(id, ":") {
			id = "anthropic:" + id
		}
		if err := r.c.SetModel(ctx, sessionID, id); err != nil {
			return fmt.Errorf("set model %q: %w", id, err)
		}
	}
	return nil
}

// ---- updates ----------------------------------------------------------------

func (r *Runner) onUpdate(p SessionUpdateParams) {
	r.mu.Lock()
	if r.replaying { // session/load replay: discarded (§6, G1 F4)
		r.mu.Unlock()
		return
	}
	r.lastActivity = r.clk.Now()
	r.mu.Unlock()
	var u Update
	if json.Unmarshal(p.Update, &u) != nil {
		return
	}
	switch u.SessionUpdate {
	case "agent_message_chunk":
		t := u.ChunkText()
		r.mu.Lock()
		r.say.WriteString(t)
		preview := r.say.String()
		r.mu.Unlock()
		if r.a.Sink != nil && t != "" {
			r.a.Sink.Preview(preview)
		}
	case "agent_thought_chunk":
		r.mu.Lock()
		r.think.WriteString(u.ChunkText())
		r.mu.Unlock()
	case "tool_call":
		r.mu.Lock()
		ts := r.tools[u.ToolCallID]
		if ts == nil {
			ts = &toolState{id: u.ToolCallID}
			r.tools[u.ToolCallID] = ts
		}
		ts.absorb(&u)
		r.lastTool = ts
		if r.toolDone == nil || isClosed(r.toolDone) {
			r.toolDone = make(chan struct{})
		}
		first := ts.startSeq == 0
		r.mu.Unlock()
		if first {
			seq := r.emit("tool", VerbFor(ts.kind), ts.objectRef(), "started", ts.payload())
			r.mu.Lock()
			ts.startSeq = seq
			r.mu.Unlock()
		}
		if u.Status == "completed" || u.Status == "failed" {
			r.finishTool(ts, u.Status)
		}
	case "tool_call_update":
		r.mu.Lock()
		ts := r.tools[u.ToolCallID]
		if ts == nil {
			ts = &toolState{id: u.ToolCallID}
			r.tools[u.ToolCallID] = ts
		}
		ts.absorb(&u)
		r.mu.Unlock()
		if u.Status == "completed" || u.Status == "failed" {
			r.finishTool(ts, u.Status)
		}
	case "plan":
		total, done := len(u.Entries), 0
		var current string
		for _, e := range u.Entries {
			if e.Status == "completed" {
				done++
			} else if current == "" && e.Status == "in_progress" {
				current = e.Content
			}
		}
		r.mu.Lock()
		r.planTotal, r.planDone = total, done
		r.mu.Unlock()
		p := map[string]any{"entries_total": total, "entries_done": done}
		if current != "" {
			p["current"] = clip(current, 512)
		}
		r.emit("plan", "update", "", "update", p)
	case "usage_update":
		if rl := u.RateLimit(); rl != nil {
			r.mu.Lock()
			r.lastRL = rl
			r.usage.RateLimit = &contracts.RateLimit{Status: rl.Status, ResetsAt: time.Unix(rl.ResetsAt, 0).UTC(), Type: rl.RateLimitType, Utilization: rl.Utilization}
			r.mu.Unlock()
			rlp := map[string]any{"status": rl.Status}
			if rl.ResetsAt > 0 {
				rlp["resets_at"] = time.Unix(rl.ResetsAt, 0).UTC().Format(time.RFC3339)
			}
			if rl.RateLimitType != "" {
				rlp["type"] = rl.RateLimitType
			}
			if rl.Utilization > 0 {
				rlp["utilization"] = rl.Utilization
			}
			r.emit("usage", "report", "", "report", r.usagePayload(map[string]any{"cumulative": true, "rate_limit": rlp}))
		}
	}
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (r *Runner) finishTool(ts *toolState, status string) {
	r.mu.Lock()
	if ts.done {
		r.mu.Unlock()
		return
	}
	ts.done = true
	if r.lastTool == ts && r.toolDone != nil && !isClosed(r.toolDone) {
		close(r.toolDone)
	}
	r.mu.Unlock()
	outcome := "ok"
	if status == "failed" {
		outcome = "failed"
	}
	r.emit("tool", VerbFor(ts.kind), ts.objectRef(), outcome, ts.payload())
}

// flushMessages emits the turn's thought and text; withSay=false drops the
// text (a Hermes provider-error body, §8 — never posted as a message).
func (r *Runner) flushMessages(withSay bool) {
	r.mu.Lock()
	say, think := r.say.String(), r.think.String()
	r.mu.Unlock()
	if think != "" {
		r.emit("message", "think", "", "ok", map[string]any{"kind": "thought", "text": think, "chars": len(think)})
	}
	if say != "" && withSay {
		r.emit("message", "say", "", "ok", map[string]any{"kind": "text", "text": say, "chars": len(say)})
	}
}

// recordUsage folds one turn's usage into the attempt total (harness §7:
// tokens and cost come from the session/prompt response once per turn).
//
// `estimated` means "this cost is not a number the runtime measured" — and
// that is true in two ways, not one. Before harness v0.7 only the first was
// checked (usage absent entirely), so the ACP path — whose `usage` carries
// tokens and NO cost field at all (wire.PromptUsage, measured on the pinned
// adapter) — reported `cost_usd: 0, estimated: false` and every session read a
// confident $0.00 (G4 3판 W16). A turn that reports tokens without a cost
// makes the total an estimate, and once any turn does, the attempt's total is
// an estimate for good: a sum that mixes a measured and a missing number is
// not measured.
//
// The daemon never fills the gap itself — it does not know the workspace price
// table (PRD §8.2.6). It says "unknown" honestly and the server estimates at
// roll-up (S-20).
func (r *Runner) recordUsage(pr *PromptResult, models []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pr.Usage != nil {
		r.usage.InputTokens += pr.Usage.InputTokens
		r.usage.OutputTokens += pr.Usage.OutputTokens
		r.usage.CacheReadTokens += pr.Usage.CachedReadTokens
		r.usage.CacheWriteTokens += pr.Usage.CachedWriteTokens
	}
	if pr.Usage == nil || pr.Usage.CostUSD == nil {
		// v0.7.1: from here on the total is an estimate AND its number is 0.
		// A partial sum (one turn priced, the next not) would reach the server
		// looking like the whole bill; 0 with `estimated: true` is the one
		// shape the server is told to ignore and re-price.
		r.usage.Estimated, r.usage.CostUSD = true, 0
	} else if !r.usage.Estimated {
		r.usage.CostUSD += *pr.Usage.CostUSD
	}
	if len(models) > 0 {
		r.usage.Model = strings.Join(models, ",")
	}
}

func (r *Runner) usagePayload(extra map[string]any) map[string]any {
	r.mu.Lock()
	u := r.usage
	r.mu.Unlock()
	p := map[string]any{"input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "cache_read_tokens": u.CacheReadTokens, "cache_write_tokens": u.CacheWriteTokens, "estimated": u.Estimated}
	// harness v0.7.1: in a `usage.report` payload the key is OMITTED when the
	// runtime reported no cost, not
	// sent as 0 — `cost_usd: 0` is indistinguishable from a measured free turn
	// to every reader downstream. `estimated: true` with no number says
	// "ask the price table"; `cost_usd: 0` with `estimated: false` means the
	// turn really cost nothing. The task_event schema leaves `cost_usd`
	// optional precisely so this distinction can be made on the wire.
	//
	// (The `finish` body cannot do this — `contracts.Usage.CostUSD` is a
	// float64 — so there the pair is `0` + `estimated: true` and the server
	// ignores the 0 and re-prices from the workspace table. Only an
	// `estimated: false` 0 is a real zero.)
	if !u.Estimated {
		p["cost_usd"] = u.CostUSD
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func (r *Runner) onRawSDK(method string, params json.RawMessage) {
	if method != ExtNotificationSDKMessage {
		return
	}
	var p struct {
		Message json.RawMessage `json:"message"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	var head struct {
		Type       string   `json:"type"`
		Subtype    string   `json:"subtype"`
		Tools      []string `json:"tools"`
		MCPServers []struct {
			Name string `json:"name"`
		} `json:"mcp_servers"`
	}
	if json.Unmarshal(p.Message, &head) != nil || head.Type != "system" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rawInit == nil {
		r.rawInit = &RawInit{}
	}
	switch head.Subtype {
	case "init":
		r.rawInit.Tools = head.Tools
		r.rawInit.MCPServers = nil
		for _, s := range head.MCPServers {
			r.rawInit.MCPServers = append(r.rawInit.MCPServers, s.Name)
		}
	case "hook_started":
		r.rawInit.Hooks++
	}
}

// ---- permission (§4) ----------------------------------------------------------

func (r *Runner) decidePermission(p RequestPermissionParams) PermissionOutcome {
	r.touch()
	r.mu.Lock()
	cancelling := r.cancelling
	r.mu.Unlock()
	title := p.ToolCall.Title
	if cancelling {
		// §4 row 4 / task_event v0.2: no option was chosen → option_kind omitted.
		r.emit("tool", "permission", title, "cancelled", map[string]any{"tool_call_id": p.ToolCall.ToolCallID, "title": clip(title, 512), "options_offered": OptionKinds(p.Options)})
		return PermissionOutcome{Outcome: "cancelled"}
	}
	var d Decision
	outcome := "allowed"
	policy := ""
	name := ToolName(p.ToolCall)
	switch {
	case name != "" && !r.toolAllowed(name):
		d = Reject(p, false)
		outcome = "rejected"
		policy = "denied_by_profile" // §4 "outcome=rejected(policy)"
	default:
		if name != "" && len(r.a.Bundle.Profile.Tools) > 0 {
			policy = "allowed_by_profile"
		}
		d = DefaultPolicy{}.Decide(p)
		if d.AllowOnceMissing {
			outcome = "rejected"
			r.mu.Lock()
			r.allowMissing++
			r.mu.Unlock()
		}
		if d.Outcome.Outcome == "cancelled" {
			outcome = "cancelled"
		}
	}
	payload := map[string]any{"tool_call_id": p.ToolCall.ToolCallID, "title": clip(title, 512), "options_offered": OptionKinds(p.Options)}
	if d.OptionKind != "" { // omitted when nothing was chosen (outcome=cancelled)
		payload["option_kind"] = d.OptionKind
	}
	if policy != "" {
		payload["policy"] = policy
	}
	if d.AllowOnceMissing {
		payload["allow_once_missing"] = true
	}
	r.emit("tool", "permission", title, outcome, payload)
	return d.Outcome
}

func (r *Runner) toolAllowed(name string) bool {
	tools := r.a.Bundle.Profile.Tools
	if len(tools) == 0 {
		return true
	}
	for _, t := range tools {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}

// ---- stall (§7) ---------------------------------------------------------------

func (r *Runner) stallTimeout() time.Duration {
	if s := r.a.Bundle.Limits.StallSeconds; s > 0 {
		return time.Duration(s) * time.Second
	}
	return contracts.StallTimeout
}

func (r *Runner) startStallWatch(ctx context.Context) func() {
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	limit := r.stallTimeout()
	go func() {
		for {
			r.mu.Lock()
			idle := r.clk.Since(r.lastActivity)
			r.mu.Unlock()
			if idle >= limit {
				r.mu.Lock()
				r.stalled = true
				r.mu.Unlock()
				r.cancelProcedure(ctx, false)
				return
			}
			select {
			case <-r.clk.After(limit - idle):
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return stop
}

// ---- cancel (§5) --------------------------------------------------------------

// Cancel runs the §5 procedure. Safe to call from any goroutine; returns
// once the process is gone.
func (r *Runner) Cancel(ctx context.Context, req CancelRequest) {
	r.mu.Lock()
	r.cancelReq = &req
	r.intent = true
	r.mu.Unlock()
	r.emit("runtime", "cancel", "", "started", map[string]any{"runtime_kind": string(r.kind()), "detail": req.Reason})
	r.cancelProcedure(ctx, req.AfterCurrentTool)
}

// CancelNote records a §5 note on the attempt's activity feed (e.g. the
// daemon's shutdown drain running over its bound).
func (r *Runner) CancelNote(detail string) {
	r.emit("runtime", "cancel", "", "info", map[string]any{"runtime_kind": string(r.kind()), "detail": detail})
}

func (r *Runner) cancelProcedure(ctx context.Context, afterCurrentTool bool) {
	// 1. wait for an in-flight edit/shell tool (≤30s)
	if afterCurrentTool {
		r.mu.Lock()
		var wait chan struct{}
		if lt := r.lastTool; lt != nil && !lt.done && (lt.kind == "edit" || lt.kind == "execute") {
			wait = r.toolDone
		}
		r.mu.Unlock()
		if wait != nil {
			select {
			case <-wait:
			case <-r.clk.After(contracts.CancelDrainWait):
				r.emit("runtime", "cancel", "", "info", map[string]any{"detail": "30초 초과로 강제 취소"})
			case <-r.promptDone:
			}
		}
	}
	// 2. pending permission requests are answered "cancelled" from now on
	r.mu.Lock()
	r.cancelling = true
	sid := r.sessionID
	r.mu.Unlock()
	if r.c == nil {
		return
	}
	// 3. session/cancel  4. drain ≤10s for stopReason cancelled
	if sid != "" {
		_ = r.c.Cancel(sid)
		select {
		case <-r.promptDone:
		case <-r.clk.After(contracts.CancelPromptWait):
		case <-r.c.Done():
		}
	}
	// 5. SIGTERM → 10s → SIGKILL
	r.closeProcess()
}

func (r *Runner) closeProcess() {
	r.closeOnce.Do(func() {
		if r.c != nil {
			_ = r.c.Close()
		}
	})
}
