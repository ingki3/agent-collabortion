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
	// RawSDKMessages asks claude_code for the raw SDK stream. It is two
	// things at once: the §12(c) probe/smoke evidence (system/init tools land
	// in Result.RawInit) and, since harness §7 v0.8.5, the ONLY place the
	// adapter reports usage during a turn (midturn.go). The loop turns it on
	// for every claude_code attempt unless the daemon config says otherwise.
	RawSDKMessages bool
	// OnUsage fires as soon as a turn's usage has been folded in — after the
	// `usage.report` event, before the attempt finishes. The loop sends one
	// heartbeat there so the server's in-turn budget check (FR-7.3, §4.2)
	// sees a MEASURED number before `finish` arrives, on every runtime
	// including the ones with no mid-turn usage at all (D-17).
	OnUsage func()
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
	// UsageMidturn is the harness §9 v0.8.5 advertisement, MEASURED: did any
	// usage reach the daemon while the turn was still running (midturn.go)?
	// False for hermes, and false for a claude_code attempt that ran with the
	// raw SDK stream off — an unmeasured capability is not advertised.
	UsageMidturn bool
	// ToolSurface is the harness §10 judgement measured on THIS attempt's
	// initialize ("mcp" | "cli_wrapper"). Empty when initialize never
	// answered — an absent measurement is not a cli_wrapper measurement.
	ToolSurface string
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
	// cancelGate is closed when the §5 step-2 gate opens. Permission
	// requests that arrived after the cancel intent park on it.
	cancelGate chan struct{}
	// parked counts permission requests waiting on cancelGate.
	parked     int
	cancelReq  *CancelRequest
	stalled    bool
	promptDone chan struct{}
	// cancelDone is closed when the §5 procedure has run to its end. Run
	// waits on it before its own close so the two paths into closeProcess
	// cannot interleave (D-15).
	cancelDone chan struct{}
	closeOnce  sync.Once
	rawInit    *RawInit
	caps       AgentCaps
	protocol   int
	mcp        []MCPServer
	mcpDropped []string
	surface    string
	planTotal  int
	planDone   int
	usage      contracts.Usage
	// turn is the mid-turn approximation for the turn in flight (midturn.go);
	// recordUsage discards it when the turn's authoritative total lands.
	turn turnTokens
	// turnCost is `result.total_cost_usd` for the turn in flight — the
	// MEASURED cost harness §7 v0.8.5 lets the daemon report (estimated:false).
	turnCost *float64
	// sawMidturn is sticky: it stays true after recordUsage clears `turn`,
	// because probe §9 asks whether the runtime CAN report in-turn usage.
	sawMidturn bool
	available  []string
	// budget (FR-7.3 M9 — see budget.go)
	budgetExceeded    bool
	pendingBudgetNote *budgetNote
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
	return &Runner{a: a, clk: a.Clock, tools: map[string]*toolState{}, promptDone: make(chan struct{}),
		cancelGate: make(chan struct{}), cancelDone: make(chan struct{})}
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

// Usage returns the usage burned so far, closed turns PLUS the turn in
// flight (heartbeat, daemon-protocol §4.2).
func (r *Runner) Usage() contracts.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totalLocked()
}

// totalLocked is `usage` (turns already closed) plus the mid-turn
// approximation for the turn in flight. Call with r.mu held.
//
// The mid-turn tokens have no price of their own — `result.total_cost_usd`
// arrives at turn end — so as long as any of them are in the sum the total is
// an ESTIMATE with no number, exactly as harness §7 v0.7.1 requires of a sum
// that mixes a measured and a missing cost. That is also what keeps the
// daemon's own §5 budget cancel honest (budget.go): it fires on a measured
// overrun only, so mid-turn it stays quiet and the SERVER — which owns the
// price table — is the one that judges the tokens this reports.
func (r *Runner) totalLocked() contracts.Usage {
	u := r.usage
	if !r.turn.any() {
		return u
	}
	u.InputTokens += r.turn.in
	u.OutputTokens += r.turn.out
	u.CacheReadTokens += r.turn.cacheRead
	u.CacheWriteTokens += r.turn.cacheWrite
	u.Estimated, u.CostUSD = true, 0
	return u
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
	// A cancelled or failed turn never reaches recordUsage, so without the
	// mid-turn half its `finish` would report zero tokens for work that was
	// really burned (E9 budget accounting).
	res.Usage = r.totalLocked()
	res.Text = r.say.String()
	res.Caps = r.caps
	res.ProtocolVersion = r.protocol
	res.MCPDropped = r.mcpDropped
	res.ToolSurface = r.surface
	res.UsageMidturn = r.sawMidturn
	r.mu.Unlock()
	if r.c != nil {
		res.PGID = r.c.PID()
		// A §5 procedure that is still running owns the close (and with it
		// the step-5 line closeProcess now emits). Whichever goroutine got
		// here first would otherwise decide whether the feed reads
		// "… → drain → signal" or "… → signal → drain" (D-15).
		r.awaitCancelProcedure()
		r.closeProcess()
	}
	return res
}

// awaitCancelProcedure blocks while a §5 procedure is in flight. The bound is
// real time on purpose: it is not a contract deadline (every §5 wait is on the
// injected clock and already bounded) but a backstop so a wedged procedure
// cannot keep the attempt's own close from running.
func (r *Runner) awaitCancelProcedure() {
	r.mu.Lock()
	inFlight := r.intent || r.cancelling
	r.mu.Unlock()
	if !inFlight {
		return
	}
	select {
	case <-r.cancelDone:
	case <-time.After(contracts.CancelDrainWait + contracts.CancelPromptWait + 5*time.Second):
	}
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
		var res Result
		applyCancelOutcome(&res, req)
		return res
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
		sessionID string
		// resumeOutcome is what `finish` reports (daemon-protocol §4.4).
		resumeOutcome string
		// refusalRetried records that the cold start below was the D-13
		// second chance, so a second empty refusal fails instead of looping.
		refusalRetried bool
		provenance     *contracts.HermesProvenance
	)
	if b.Resume != nil && b.Resume.SessionID != "" {
		sid, prov, reason, rpcErr, ferr := r.load(sctx, meta)
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
			p := map[string]any{"runtime_kind": string(r.kind()), "session_id": sessionID, "resume_reason": reason}
			if e := rpcError(rpcErr); e != "" {
				p["detail"] = e
			}
			r.emit("runtime", "resume", sessionID, "cold_start", p)
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
	pr, perr := r.promptTurn(ctx, sessionID)
	r.mu.Lock()
	stalled, cancelled, cancelReq := r.stalled, r.intent || r.cancelling, r.cancelReq
	text, ntools := r.say.String(), len(r.tools)
	r.mu.Unlock()

	// D-13 — an empty `refusal` right after a resume is a SECOND loss signal.
	//
	// harness §2.2 maps `refusal` to a normal end and forbids using it as a
	// loss signal on its own (G1 F7): a runtime that genuinely declines a task
	// must not be re-run as if it had crashed. That stays true. What spike 4c
	// measured is narrower and is not covered by it: after `session/load`
	// succeeded, the very first prompt came back `refusal` having edited
	// nothing and posted nothing, 5/5, and the attempt was reported
	// `completed`. Work vanished with nobody saying so.
	//
	// The narrowing is what makes this safe. A refusal is only re-tried when
	// (a) this turn resumed a session, (b) the turn did nothing at all, and
	// (c) it has not been re-tried yet. A cold start is the cheapest way to
	// find out which of the two it was: if the runtime had lost the session,
	// the fresh one works; if it really is declining, it declines again and
	// the attempt fails honestly instead of reporting success.
	if r.shouldRetryRefusal(resumeOutcome, pr, perr, cancelled, stalled, ntools) {
		// The refused turn cost real tokens (a resume prompt carries the whole
		// history). Folding them in before the retry is what keeps the
		// attempt's `finish` usage — and the budget — counting both turns; the
		// second recordUsage below adds to this one, it does not replace it.
		r.recordUsage(pr, pr.ModelsUsed())
		s, nerr := r.newSession(ctx, meta)
		if nerr != nil {
			return r.classify(nerr)
		}
		sessionID, provenance, resumeOutcome = s.SessionID, hermesProv(s), "cold_start"
		refusalRetried = true
		ref = &contracts.RuntimeSessionRef{RuntimeKind: r.kind(), AdapterVersion: adapterVersion, SessionID: sessionID, CWD: r.a.Workdir, CreatedAt: r.clk.Now().UTC(), Provenance: provenance}
		r.mu.Lock()
		r.sessionID = sessionID
		r.mu.Unlock()
		r.emit("runtime", "resume", sessionID, "cold_start", map[string]any{
			"runtime_kind": string(r.kind()), "session_id": sessionID, "resume_reason": refusalAfterResume,
			"detail": "resume 직후 첫 턴이 stopReason=refusal + 활동 0 — 콜드 스타트로 1회 재시도 (D-13)",
		})
		if err := r.setModel(ctx, sessionID); err != nil {
			return r.fail(contracts.FailConfig, err.Error(), nil)
		}
		r.resetTurn()
		pr, perr = r.promptTurn(ctx, sessionID)
		r.mu.Lock()
		stalled, cancelled, cancelReq = r.stalled, r.intent || r.cancelling, r.cancelReq
		text, ntools = r.say.String(), len(r.tools)
		r.mu.Unlock()
	}
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
			applyCancelOutcome(&res, cancelReq)
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
	r.flushBudgetNote()
	// daemon-protocol §4.2 + FR-7.3: one heartbeat NOW, while the attempt is
	// still open. Until D-17 the server's in-turn budget check saw usage only
	// on heartbeats — which were all zero — so the first number it ever got
	// was `finish`, by which time the money was spent. This is the runtime's
	// measured total reaching the server one step before that, and it is the
	// only in-turn signal a runtime with no mid-turn usage (hermes) can give.
	if r.a.OnUsage != nil {
		r.a.OnUsage()
	}
	res := base
	res.Models = models
	res.StopReason = pr.StopReason
	if hermesFail != nil {
		res := r.fail(hermesFail.Kind, hermesFail.Detail, hermesFail.NotBefore)
		res.SessionRef, res.ResumeOutcome, res.Models, res.StopReason, res.AdapterVersion = ref, resumeOutcome, models, pr.StopReason, adapterVersion
		return res
	}
	if refusalRetried && pr.StopReason == "refusal" && ntools == 0 && !cancelled {
		// The cold start refused too, doing nothing again. That is not a lost
		// session, so it is not a resume problem — it is a turn that produced
		// no work, and reporting `completed` would tell the session that the
		// task is done (spike 4c §3).
		res := r.fail(contracts.FailOther, "resume 후 콜드 스타트 재시도도 stopReason=refusal + 활동 0 — 턴이 아무 일도 하지 않았다 (D-13, 스파이크 4c §3)", nil)
		res.SessionRef, res.ResumeOutcome, res.Models, res.StopReason, res.AdapterVersion = ref, resumeOutcome, models, pr.StopReason, adapterVersion
		return res
	}
	switch pr.StopReason {
	case "cancelled":
		applyCancelOutcome(&res, cancelReq)
	default:
		// end_turn, max_tokens, max_turn_requests, refusal → completed (§2.2)
		res.Outcome = "completed"
		if cancelled {
			applyCancelOutcome(&res, cancelReq)
		} else if r.budgetHit() {
			// daemon-protocol §4.4: a measured overrun is `paused_budget`,
			// with NO failure_kind — going over budget is policy, not an
			// error, and the Director raising the cap resumes the same lane
			// and workdir (FR-7.3 M9).
			res.Outcome, res.StopReason = "paused_budget", "budget"
		}
	}
	return res
}

// rpcError renders a JSON-RPC error into the one column the cold-start event
// carries (D-11): `detail`, "<code> <message>". It is `detail` and not a key
// of its own because the `runtime` payload is closed
// (task_event.schema.json additionalProperties:false) and this stream does not
// edit contracts/.
func rpcError(e *RPCError) string {
	if e == nil {
		return ""
	}
	return clip(fmt.Sprintf("%d %s", e.Code, e.Message), 512)
}

// refusalAfterResume is the `resume_reason` of a D-13 cold start.
const refusalAfterResume = "refusal_after_resume"

// promptTurn sends one session/prompt under the stall watch, and does the
// §2.2 Hermes quiet wait. It is called twice at most (D-13).
func (r *Runner) promptTurn(ctx context.Context, sessionID string) (*PromptResult, error) {
	r.touch()
	stopStall := r.startStallWatch(ctx)
	done := r.promptDoneCh()
	pr, perr := r.c.Prompt(ctx, sessionID, r.a.Bundle.Prompt)
	close(done)
	stopStall()
	if r.kind() == contracts.RuntimeHermes {
		time.Sleep(r.a.Quiet) // §2.2: late agent_message_chunk after the response
	}
	return pr, perr
}

func (r *Runner) promptDoneCh() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.promptDone
}

// shouldRetryRefusal is the D-13 gate. Every clause narrows: only a turn that
// RESUMED, only a clean `refusal`, only with zero tool activity, only once.
func (r *Runner) shouldRetryRefusal(resumeOutcome string, pr *PromptResult, perr error, cancelled, stalled bool, ntools int) bool {
	return resumeOutcome == "resumed" && perr == nil && pr != nil &&
		pr.StopReason == "refusal" && ntools == 0 && !cancelled && !stalled
}

// resetTurn clears the accumulated turn state before the D-13 retry: the
// refused turn contributed no text, no thought and no tools, and carrying its
// (empty) builders forward would merge two turns into one message.
func (r *Runner) resetTurn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.say.Reset()
	r.think.Reset()
	r.tools = map[string]*toolState{}
	r.lastTool = nil
	r.toolDone = nil
	r.promptDone = make(chan struct{})
	// The next turn accumulates its own usage; whatever the abandoned turn
	// contributed has already been folded into r.usage by recordUsage.
	r.turn = turnTokens{}
	r.turnCost = nil
}

// CancelReasonBudget is the §4.3 `cancel {reason}` value the SERVER uses when
// it stops a turn because the session or task ran out of money.
const CancelReasonBudget = "budget"

// budgetCancel reports whether a cancel request is the server's budget pause.
func budgetCancel(c *CancelRequest) bool { return c != nil && c.Reason == CancelReasonBudget }

// applyCancelOutcome fills a Result for a turn that ended because someone
// cancelled it (D-19).
//
// `cancelled` and `paused_budget` are two different things to the server:
// `cancelled` ends the task, `paused_budget` pauses the session so a Director
// who raises the cap resumes the SAME lane and workdir (FR-7.3 M9, §4.4). The
// daemon reaches this branch two ways — it measured the overrun itself
// (budgetHit) or the server told it to stop with `reason: budget` — and until
// D-19 only the first was reported as a pause: every cancel path here checked
// "was I cancelled?" before it checked why, so a server-driven budget pause
// came back as `cancelled` + failure_kind `cancelled` and the session ended
// instead of pausing. The reason is on the wire; this reads it.
func applyCancelOutcome(res *Result, req *CancelRequest) {
	if budgetCancel(req) {
		// §4.4: no failure_kind — going over budget is policy, not an error.
		res.Outcome, res.StopReason, res.Failure = "paused_budget", CancelReasonBudget, nil
		return
	}
	res.Outcome, res.StopReason = "cancelled", "cancelled"
	res.Failure = &Failure{Kind: contracts.FailCancelled, Detail: cancelReason(req)}
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
	r.surface = caps.ToolSurface()
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
func (r *Runner) load(ctx context.Context, meta map[string]any) (sid string, prov *contracts.HermesProvenance, reason string, rpcErr *RPCError, err error) {
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
			if errors.As(err, &rpc) && isSessionGone(rpc) {
				// D-11: the ORIGINAL code and message ride along to the
				// cold-start event. Spike 4c cost a full batch to the
				// difference between the contract's quoted wording and the
				// adapter's actual `-32002 Resource not found`, and nothing
				// in the feed said which one had arrived — the event only
				// said "cold_start". The next time the adapter changes its
				// mind, the answer is in the activity feed.
				return "", nil, "session_not_found", rpc, nil
			}
			return "", nil, "", nil, err
		}
		return ref.SessionID, nil, "", nil, nil
	default: // hermes
		if err != nil {
			return "", nil, "", nil, err
		}
		if s == nil {
			return "", nil, "load_null", nil, nil
		}
		acp, root, kind, depth, ok := s.HermesProvenance()
		if !ok || acp == "" {
			// No provenance at all — or a `sessionProvenance` object that
			// carries no `acpSessionId` (D-12). An empty id cannot equal the
			// requested one, so the mismatch branch below would already cold
			// start; it would do so under the reason `provenance_mismatch`,
			// which reads as "Hermes handed us a DIFFERENT session" when what
			// actually happened is that it handed us nothing to compare. The
			// reason is what a person reads in the feed, so it has to be the
			// true one.
			//
			// §6 only names "result null" and
			// "provenance mismatch", but Hermes 0.20.6 answers a session it no
			// longer has with a bare `{}` — non-null, no `_meta` (spike 4c wire
			// log, plan/spikes/logs/spike4c_hermes_wire.json). Reading that as
			// "resumed" loses the work silently: the next session/prompt comes
			// back `stopReason: refusal`, which §2.2 forbids using as a loss
			// signal, so the attempt ends `completed` having done nothing
			// (5/5 in spike 4c). Missing provenance is a lost session.
			return "", nil, "no_provenance", nil, nil
		}
		p := &contracts.HermesProvenance{ACPSessionID: acp, RootHermesSessionID: root, SessionKind: kind, CompressionDepth: depth}
		if acp == ref.SessionID {
			return ref.SessionID, p, "", nil, nil
		}
		if ref.Provenance != nil && root != "" && root == ref.Provenance.RootHermesSessionID {
			// compression rotation: keep, store the new id (spike 4a rec. 2)
			return acp, p, "compression_rotation", nil, nil
		}
		return "", nil, "provenance_mismatch", nil, nil
	}
}

// isSessionGone says whether a `session/load` JSON-RPC error means the runtime
// no longer has the session — the signal that turns a resume into a cold start
// (harness §6, E8-02). The adapter does NOT answer with the one string the
// contract used to quote: 0.74.0 on Claude Code CLI 2.1.258 replies
//
//	-32002 "Resource not found: <sessionId>"  (data {"uri": "<sessionId>"})
//
// for both a never-created id and a transcript file that was actually deleted
// (spike 4c, 2026-09-06 — 10/10). Matching only "session not found" made every
// forced cold start fail the attempt with failure_kind=other and burn all three
// attempts, so E8-02 had never fired against a real adapter. Match the code and
// the generic wording, and keep the old string so an adapter that goes back to
// it still works.
//
// D-10 — should this narrow to `-32002` alone? Not yet. The generic "not
// found" half is the cheap insurance against an adapter that words it a third
// way, and its cost is a false cold start (slow) rather than a false resume
// (silent data loss, §6 a′). Narrowing is worth doing once the adapter has
// held one wording across a pin bump; until then this comment is the decision,
// not an oversight.
func isSessionGone(rpc *RPCError) bool {
	if rpc == nil {
		return false
	}
	if rpc.Code == -32002 {
		return true
	}
	return strings.Contains(strings.ToLower(rpc.Message), "not found")
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
	// The turn's own total supersedes the mid-turn approximation this turn
	// accumulated (midturn.go) — they measure the same tokens, so keeping
	// both would double the turn.
	r.turn = turnTokens{}
	cost := pr.CostOrNil()
	if cost == nil {
		// harness §7 v0.8.5: `result.total_cost_usd` from the raw SDK stream
		// is a MEASURED cost, so the D-6 rule "런타임이 비용을 주면 그 값 +
		// estimated:false" applies to it as much as to a cost the ACP
		// response carried. It is the only cost any pinned adapter reports.
		cost = r.turnCost
	}
	r.turnCost = nil
	if pr.Usage != nil {
		r.usage.InputTokens += pr.Usage.InputTokens
		r.usage.OutputTokens += pr.Usage.OutputTokens
		r.usage.CacheReadTokens += pr.Usage.CachedReadTokens
		r.usage.CacheWriteTokens += pr.Usage.CachedWriteTokens
	}
	if cost == nil {
		// v0.7.1: from here on the total is an estimate AND its number is 0.
		// A partial sum (one turn priced, the next not) would reach the server
		// looking like the whole bill; 0 with `estimated: true` is the one
		// shape the server is told to ignore and re-price.
		r.usage.Estimated, r.usage.CostUSD = true, 0
	} else if !r.usage.Estimated {
		r.usage.CostUSD += *cost
	}
	if len(models) > 0 {
		r.usage.Model = strings.Join(models, ",")
	}
	r.noteBudget()
}

func (r *Runner) usagePayload(extra map[string]any) map[string]any {
	r.mu.Lock()
	u := r.totalLocked()
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
		// stream_event — the per-request usage the turn is judged on (§7 v0.8.5)
		Event json.RawMessage `json:"event,omitempty"`
		// result — one per turn, and the only MEASURED cost on the ACP path
		TotalCostUSD *float64 `json:"total_cost_usd,omitempty"`
	}
	if json.Unmarshal(p.Message, &head) != nil {
		return
	}
	switch head.Type {
	case "stream_event":
		r.foldTurnUsage(foldSDKStream(head.Event))
		return
	case "result":
		if head.TotalCostUSD != nil {
			c := *head.TotalCostUSD
			r.mu.Lock()
			r.turnCost = &c
			r.mu.Unlock()
		}
		return
	case "system":
	default:
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

// foldTurnUsage adds one raw SDK contribution to the turn in flight and lets
// the budget see the new total. Nothing is emitted here: the mid-turn number
// rides the ordinary 15s heartbeat (§4.2), so a chatty turn does not turn into
// a chatty attempt.
func (r *Runner) foldTurnUsage(t turnTokens) {
	if !t.any() {
		return
	}
	r.mu.Lock()
	r.turn.in += t.in
	r.turn.out += t.out
	r.turn.cacheRead += t.cacheRead
	r.turn.cacheWrite += t.cacheWrite
	r.sawMidturn = true
	r.noteBudget()
	r.mu.Unlock()
}

// ---- permission (§4) ----------------------------------------------------------

func (r *Runner) decidePermission(p RequestPermissionParams) PermissionOutcome {
	r.touch()
	cancelling := r.parkIfCancelling()
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

// parkIfCancelling holds a permission request that arrived after a cancel was
// decided but before the §5 step-2 gate opened, and reports whether the answer
// must be "cancelled".
//
// Without the hold, the §5 step-1 window — up to THIRTY SECONDS of waiting for
// an in-flight edit or shell command — was a window in which the daemon still
// granted brand new tool calls. A human had already pressed 중단 and the agent
// could start another `rm -rf` with our permission. "Cancel intent is set" and
// "we still authorise work" cannot both be true.
//
// It is also what makes §5's step ORDER reachable at all. §5 numbers the
// permission answer as step 2, before `session/cancel`, and notes that the
// case never fires against a real runtime because permission is granted first
// — which is precisely because an ACP client that answers on arrival can never
// have one pending. Parking creates the pending set the contract describes,
// and cancelProcedure drains it before step 3.
func (r *Runner) parkIfCancelling() bool {
	r.mu.Lock()
	if r.cancelling {
		r.mu.Unlock()
		return true
	}
	if !r.intent {
		r.mu.Unlock()
		return false
	}
	r.parked++
	gate := r.cancelGate
	r.mu.Unlock()
	var done <-chan struct{}
	if r.c != nil {
		done = r.c.Done()
	}
	select {
	case <-gate:
	case <-done:
	case <-time.After(contracts.CancelDrainWait):
		// The gate is opened by cancelProcedure a few instructions after the
		// intent, so this bound is a deadlock guard, not a policy.
	}
	r.mu.Lock()
	r.parked--
	r.mu.Unlock()
	return true
}

// waitParked lets the §5 step-2 gate drain: session/cancel is not sent while a
// permission request is still unanswered, because the agent loop is blocked on
// that answer and would never process the cancel (harness §5 steps 2-4).
//
// The bound is wall time on purpose. This waits for another goroutine of this
// process to write one JSON line, like the §2.2 quiet wait — it is not one of
// the contract deadlines (stall, cancel hold, not_before) that must move with
// the injected clock.
func (r *Runner) waitParked(bound time.Duration) {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		n := r.parked
		r.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
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

// The five §5 steps, named on the activity feed.
//
// They are emitted because the ORDER is the contract, not an implementation
// detail (§8.2.2: killing first corrupts the runtime's own history and can
// leave a half-written file), and until now nothing outside this function
// could see it. A cancel that skipped the drain and a cancel that did not
// looked identical in the feed and in the tests, so the only evidence for the
// contract's most consequential sequence was reading this code.
//
// They ride in `detail`, not in a field of their own: the `runtime` payload in
// task_event.schema.json is `additionalProperties: false`, and inventing a key
// is a contract change this stream may not make (P2_TASKS §0-3). `detail` is
// free text the feed already shows, so the line reads as a sentence to a
// person and parses as "§5 <n> <step> [k=v …]" to a test.
const (
	stepWaitTool      = "wait_tool_completion"
	stepPermission    = "answer_permission"
	stepSessionCancel = "session_cancel"
	stepDrain         = "drain"
	stepSignal        = "signal_process_group"
)

// cancelStep renders one §5 step line for `detail`.
func cancelStep(n int, step string, extra ...string) string {
	out := fmt.Sprintf("§5 %d %s", n, step)
	for _, e := range extra {
		out += " " + e
	}
	return out
}

func (r *Runner) emitStep(n int, step string, extra ...string) {
	r.emit("runtime", "cancel", "", "info", map[string]any{
		"runtime_kind": string(r.kind()), "detail": cancelStep(n, step, extra...),
	})
}

// cancelForcedNote is the E10-02 feed line, verbatim from harness §5 step 1.
const cancelForcedNote = "30초 초과로 강제 취소"

func (r *Runner) cancelProcedure(ctx context.Context, afterCurrentTool bool) {
	// Every exit from here — including the `r.c == nil` one below — has to
	// release Run's wait, or an attempt whose process appeared after the
	// procedure started would sit on the backstop bound (D-15).
	defer func() {
		r.mu.Lock()
		closeOnce(r.cancelDone)
		r.mu.Unlock()
	}()
	// 1. wait for an in-flight edit/shell tool (≤30s)
	//
	// "편집 또는 셸" is one rule with two verbs (harness §5 step 1, FR-3.4,
	// E10-14): a half-run migration or `rm -rf` is exactly as irreversible as
	// a half-written file. A completed tool is not waited for at all — an
	// unconditional 30s hold would satisfy the two rows above and make 중단
	// feel broken on an idle turn.
	if afterCurrentTool {
		r.mu.Lock()
		var wait chan struct{}
		if lt := r.lastTool; lt != nil && !lt.done && (lt.kind == "edit" || lt.kind == "execute") {
			wait = r.toolDone
		}
		r.mu.Unlock()
		if wait != nil {
			start := r.clk.Now()
			forced := false
			select {
			case <-wait:
			case <-r.clk.After(contracts.CancelDrainWait):
				forced = true
			case <-r.promptDoneCh():
			}
			// A forced hold lasted exactly the cap: the timer firing IS the
			// end of the wait, and reporting the wall clock instead would say
			// 30.4s — a number the contract's "최대 30초" reads as a breach.
			held := r.clk.Since(start)
			if forced || held > contracts.CancelDrainWait {
				held = contracts.CancelDrainWait
			}
			extra := []string{fmt.Sprintf("held_ms=%d", held.Milliseconds())}
			if forced {
				extra = append(extra, "forced", cancelForcedNote)
			}
			r.emitStep(1, stepWaitTool, extra...)
		}
	}
	// 2. pending permission requests are answered "cancelled" from now on.
	// The answer itself is emitted by decidePermission as
	// tool/permission outcome=cancelled — the step marker here records that
	// the gate opened even when no request happens to be in flight.
	r.mu.Lock()
	r.cancelling = true
	sid := r.sessionID
	npark := r.parked
	closeOnce(r.cancelGate) // under the lock: two cancels can race here
	r.mu.Unlock()
	// Nothing goes on the wire until every parked request has its answer: the
	// agent loop is blocked on that answer, so a session/cancel sent first
	// would never be read (harness §5 steps 2-4).
	r.waitParked(2 * time.Second)
	r.emitStep(2, stepPermission, fmt.Sprintf("answered=%d", npark))
	if r.c == nil {
		return
	}
	// 3. session/cancel  4. drain ≤10s for stopReason cancelled
	if sid != "" {
		r.emitStep(3, stepSessionCancel)
		_ = r.c.Cancel(sid)
		select {
		case <-r.promptDoneCh():
		case <-r.clk.After(contracts.CancelPromptWait):
		case <-r.c.Done():
		}
		r.emitStep(4, stepDrain)
	}
	// 5. SIGTERM → 10s → SIGKILL. Last, always: E10-03 is "프로세스 즉시 kill 아님".
	// The step-5 line is closeProcess's, not this function's — see there.
	r.closeProcess()
}

// closeOnce closes ch unless it is already closed. cancelProcedure can be
// entered twice (the stall watcher and a server cancel), so the caller holds
// r.mu — a bare check-then-close would double-close and panic.
func closeOnce(ch chan struct{}) {
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// closeProcess signals the runtime's process group (SIGTERM → 10s → SIGKILL)
// and reports that it did.
//
// The §5 step-5 line is emitted HERE, inside the same sync.Once as the signal,
// so the observation cannot be separated from the action: a future path that
// closes the process without walking the procedure still shows up in the feed,
// and the cancel-order golden catches it (D-15, PR #121 review NN1). Before
// this, `cancelProcedure` emitted the line itself and `Run` closed the process
// with nothing said — a kill that skipped the drain and one that did not
// looked identical, which is exactly what the golden exists to tell apart.
//
// The line is only for a close that a cancel reached: `Run` ends every attempt
// here, and labelling a completed turn "§5 5 signal_process_group" would put a
// cancel line in the feed of a task nobody cancelled.
func (r *Runner) closeProcess() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		cancelling := r.intent || r.cancelling
		r.mu.Unlock()
		if cancelling {
			r.emitStep(5, stepSignal)
		}
		if r.c != nil {
			_ = r.c.Close()
		}
	})
}
