// Package loop is the daemon's claim loop (daemon-protocol §4): orphan sweep
// → probe → claim (long-poll) → per-attempt run (workdir, brief, harness,
// events, heartbeat 15s, finish) → commands (cancel / revoke / probe).
package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/brief"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
	"github.com/ingki3/agent-collabortion/daemon/internal/probe"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// Daemon holds the loop state. Everything time- or server-related is
// injectable for tests.
type Daemon struct {
	Cfg     config.Config
	Server  api.Server
	Clock   clock.Clock
	Version string
	Orphans orphan.Store
	Log     func(format string, args ...any)

	// ProbeTurn runs the PONG turn on the start-up / daily / commanded probe.
	ProbeTurn bool
	// ProbeCommand overrides the adapter command for probes (tests).
	ProbeCommand func(kind contracts.RuntimeKind) (string, []string, []string, bool)
	// SpawnConfig overrides how an attempt's process is built (tests →
	// acpfake). Nil → acp.Command + acp.Env.
	SpawnConfig func(b contracts.TaskBundle, wd string) acp.Config

	HeartbeatInterval time.Duration // 0 → contracts.HeartbeatInterval
	ClaimWait         time.Duration // 0 → contracts.ClaimMaxWait
	ProbeInterval     time.Duration // 0 → 24h
	KillAfter         time.Duration // 0 → contracts.KillAfterTerm
	// ShutdownDrain bounds the whole §5 shutdown procedure of all running
	// attempts. 0 → defaultShutdownDrain.
	ShutdownDrain time.Duration

	mu           sync.Mutex
	running      map[string]*attemptRun
	seen         map[string]bool
	allowMissing map[contracts.RuntimeKind]bool
	// surface caches the MEASURED harness §10 tool_surface per runtime (the
	// probe turn, then every attempt). An attempt has to decide whether to
	// write the CLI wrapper before it can measure anything of its own, so it
	// asks here first and falls back to acp.DefaultToolSurface.
	surface   map[contracts.RuntimeKind]string
	slotFreed chan struct{}
	wg        sync.WaitGroup
	// Claimed counts claim calls (tests).
	Claimed int
}

type attemptRun struct {
	bundle contracts.TaskBundle
	runner *acp.Runner
	// workdir is the resolved absolute path this attempt writes in. GC asks
	// for it (§6 refusal `process_alive`).
	workdir string
}

// defaultShutdownDrain bounds the shutdown cancel of every running attempt
// (§5 step 1 ≤30s + step 4 ≤10s + step 5 SIGTERM→SIGKILL are per-attempt
// bounds; this is the daemon-wide cap after which the attempt context is
// cancelled anyway — the attempt still reports outcome=cancelled).
const defaultShutdownDrain = 15 * time.Second

func key(taskID string, attempt int) string { return fmt.Sprintf("%s.%d", taskID, attempt) }

func (d *Daemon) init() {
	if d.Clock == nil {
		d.Clock = clock.Real{}
	}
	if d.Log == nil {
		d.Log = func(string, ...any) {}
	}
	if d.HeartbeatInterval == 0 {
		d.HeartbeatInterval = contracts.HeartbeatInterval
	}
	if d.ClaimWait == 0 {
		d.ClaimWait = contracts.ClaimMaxWait
	}
	if d.ProbeInterval == 0 {
		d.ProbeInterval = 24 * time.Hour
	}
	if d.Orphans.Root == "" {
		d.Orphans.Root = d.Cfg.WorkdirRoot
	}
	d.running = map[string]*attemptRun{}
	d.seen = map[string]bool{}
	d.allowMissing = map[contracts.RuntimeKind]bool{}
	d.surface = map[contracts.RuntimeKind]string{}
	d.slotFreed = make(chan struct{}, 1)
}

// Run blocks until ctx is done. On exit every running attempt goes through
// the harness §5 cancel procedure and reports finish outcome=cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	d.init()
	// Attempts run on a context that deliberately outlives ctx: on SIGTERM the
	// §5 procedure must run (cancel intent → permission answers → session/cancel
	// → drain) BEFORE anything tears session/prompt down. Cancelling ctx first
	// ended the prompt with "context canceled" → finish failed(other) → the
	// server requeued the attempt (G3 D-1, E10-13).
	attemptCtx, cancelAttempts := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelAttempts()
	// FR-9.1: orphans BEFORE the first claim (E11-05)
	swept, err := d.Orphans.Sweep()
	if err != nil {
		d.Log("orphan sweep: %v", err)
	}
	for _, s := range swept {
		d.Log("orphan %s.%d pgid=%d alive=%v killed=%v", s.Record.TaskID, s.Record.Attempt, s.Record.PGID, s.Alive, s.Killed)
	}
	// The §10 wrapper carries an attempt token, so it is swept in the same
	// place and for the same reason as the pgid record: nothing of this
	// daemon runs yet, so whatever is left belongs to a dead attempt.
	if err := toolwrap.SweepAll(d.Cfg.WorkdirRoot); err != nil {
		d.Log("tool wrapper sweep: %v", err)
	}
	d.probe(ctx)
	nextProbe := d.Clock.After(d.ProbeInterval)
	for ctx.Err() == nil {
		select {
		case <-nextProbe:
			d.probe(ctx)
			nextProbe = d.Clock.After(d.ProbeInterval)
		default:
		}
		d.mu.Lock()
		free := d.Cfg.Capacity - len(d.running)
		d.mu.Unlock()
		if free <= 0 {
			select {
			case <-ctx.Done():
			case <-d.slotFreed:
			case <-nextProbe:
				d.probe(ctx)
				nextProbe = d.Clock.After(d.ProbeInterval)
			}
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, d.ClaimWait+15*time.Second)
		d.mu.Lock()
		d.Claimed++
		d.mu.Unlock()
		res, err := d.Server.Claim(cctx, d.Cfg.RuntimeID, api.ClaimRequest{Capacity: free, WaitMS: int(d.ClaimWait / time.Millisecond)})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			d.Log("claim: %v", err)
			select {
			case <-ctx.Done():
			case <-d.Clock.After(2 * time.Second):
			}
			continue
		}
		d.handleCommands(ctx, res.Commands)
		for _, b := range res.Tasks {
			d.start(attemptCtx, b)
		}
	}
	d.stop(context.WithoutCancel(ctx), cancelAttempts)
	d.wg.Wait()
	return ctx.Err()
}

func (d *Daemon) shutdownDrain() time.Duration {
	if d.ShutdownDrain > 0 {
		return d.ShutdownDrain
	}
	return defaultShutdownDrain
}

// stop cancels every attempt still running (§5, reason kill_switch) and waits
// for their finish, bounded by shutdownDrain. Over the bound the note "드레인
// 초과" goes on each activity feed and the attempt context is cancelled — the
// cancel intent is already set, so the attempt is still reported cancelled and
// never failed(other) (E10-13).
func (d *Daemon) stop(ctx context.Context, cancelAttempts context.CancelFunc) {
	d.mu.Lock()
	runs := make([]*attemptRun, 0, len(d.running))
	for _, r := range d.running {
		runs = append(runs, r)
	}
	d.mu.Unlock()
	if len(runs) == 0 {
		cancelAttempts()
		return
	}
	var wg sync.WaitGroup
	for _, r := range runs {
		wg.Add(1)
		go func(r *attemptRun) {
			defer wg.Done()
			d.cancelRun(ctx, r, acp.CancelRequest{Reason: "kill_switch"})
		}(r)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		d.wg.Wait() // the finish call of every attempt
		close(done)
	}()
	select {
	case <-done:
	case <-d.Clock.After(d.shutdownDrain()):
		d.Log("shutdown: cancel drain over %s — forcing", d.shutdownDrain())
		for _, r := range runs {
			r.runner.CancelNote("드레인 초과")
		}
	}
	cancelAttempts()
}

// cancelRun is the one entry to the harness §5 procedure: the server `cancel`
// command (§4.3), `revoke`, and daemon shutdown all go through it.
func (d *Daemon) cancelRun(ctx context.Context, run *attemptRun, req acp.CancelRequest) {
	run.runner.Cancel(ctx, req)
}

func (d *Daemon) probe(ctx context.Context) {
	d.mu.Lock()
	am := make(map[contracts.RuntimeKind]bool, len(d.allowMissing))
	for k, v := range d.allowMissing {
		am[k] = v
	}
	d.mu.Unlock()
	o := probe.Options{DaemonVersion: d.Version, WorkdirRoot: d.Cfg.WorkdirRoot, Turn: d.ProbeTurn, AllowOnceMissing: am, Command: d.ProbeCommand, ColabBin: d.Cfg.ColabBin, Repos: d.Cfg.Repos, UsageMidturnOff: !d.Cfg.UsageMidturnEnabled(), Clock: d.Clock, Log: func(s string) { d.Log("%s", s) }}
	// probe.Run fills §3 colab_cli itself: the colab CLI is how every agent
	// reaches the platform (MCP server and shell path are the same binary),
	// so its absence rides on the probe instead of only the daemon log.
	p := probe.Run(ctx, o)
	d.mu.Lock()
	for _, c := range p.Capabilities {
		if c.ToolSurface != "" {
			d.surface[c.Kind] = c.ToolSurface
		}
	}
	d.mu.Unlock()
	if !p.ColabCLI.Present {
		d.Log("colab CLI not usable (%s) — agents have neither the MCP server nor the shell path", d.Cfg.ColabBin)
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := d.Server.Probe(pctx, d.Cfg.RuntimeID, p); err != nil {
		d.Log("probe: %v", err)
	}
	if wds, err := workdir.List(d.Cfg.WorkdirRoot); err == nil && len(wds) > 0 {
		_ = d.Server.Workdirs(pctx, d.Cfg.RuntimeID, api.WorkdirsRequest{Workdirs: wds})
	}
}

// handleCommands applies server commands idempotently on (type, task, attempt).
func (d *Daemon) handleCommands(ctx context.Context, cmds []contracts.Command) {
	for _, c := range cmds {
		k := string(c.Type) + ":" + key(c.TaskID, c.Attempt)
		d.mu.Lock()
		// probe and gc carry no (task_id, attempt), so the §4.3 idempotency
		// key does not separate two of them: de-duplicating on it would drop
		// every gc after the first, and a `gc` is re-issued precisely BECAUSE
		// the server has not observed the last one yet. Both are idempotent in
		// their own right (a probe re-measures, a delete of a gone directory
		// is a no-op), so they are simply re-run.
		// probe, gc and rebind_prepare carry no (task_id, attempt), so the
		// §4.3 idempotency key does not separate two of them; each is
		// idempotent in its own right and is simply re-run.
		if c.Type != contracts.CmdProbe && c.Type != contracts.CmdGC && c.Type != contracts.CmdRebindPrepare && d.seen[k] {
			d.mu.Unlock()
			continue
		}
		d.seen[k] = true
		run := d.running[key(c.TaskID, c.Attempt)]
		d.mu.Unlock()
		switch c.Type {
		case contracts.CmdCancel:
			if run != nil {
				go d.cancelRun(ctx, run, acp.CancelRequest{AfterCurrentTool: c.AfterCurrentTool, Reason: c.Reason})
			}
		case contracts.CmdRevoke:
			// token revoked: the attempt is dead server-side. Cancel a live
			// process; kill a recorded orphan group (§5).
			if run != nil {
				go d.cancelRun(ctx, run, acp.CancelRequest{Reason: "revoked"})
				continue
			}
			if recs, err := d.Orphans.List(); err == nil {
				for _, r := range recs {
					if r.TaskID == c.TaskID && r.Attempt == c.Attempt {
						if orphan.Alive(r.PGID) {
							orphan.Kill(r.PGID, d.killAfter())
						}
						_ = d.Orphans.Remove(r.TaskID, r.Attempt)
					}
				}
			}
		case contracts.CmdProbe:
			go d.probe(ctx)
		case contracts.CmdGC:
			go d.gc(ctx, c)
		case contracts.CmdRebindPrepare:
			go d.rebindPrepare(ctx, c)
		default:
			d.Log("command %s ignored (P4)", c.Type)
		}
	}
}

// holdsWorkdir reports whether a live process of this machine is still
// writing in p — a running attempt of this daemon, or a recorded process
// group that outlived one (daemon-protocol §5). Under `worktree` the answer
// decides whether a `gc` is executed or refused: the directory GC wants to
// delete is a checkout somebody may be mid-commit in.
func (d *Daemon) holdsWorkdir(p string) bool {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	d.mu.Lock()
	for _, r := range d.running {
		if r.workdir != "" {
			if rabs, err := filepath.Abs(r.workdir); err == nil && rabs == abs {
				d.mu.Unlock()
				return true
			}
		}
	}
	d.mu.Unlock()
	recs, err := d.Orphans.List()
	if err != nil {
		return false
	}
	for _, r := range recs {
		if r.Workdir == "" {
			continue
		}
		if rabs, err := filepath.Abs(r.Workdir); err == nil && rabs == abs && orphan.Alive(r.PGID) {
			return true
		}
	}
	return false
}

// gc executes a §4.3 `gc` command and reports the outcome (§6).
//
// The command names workdirs as {id, path} (daemon-protocol §4.3 v0.7). The
// path is the operative field: the daemon has no uuid ↔ path map, so a
// payload that carries only `workdir_ids` — the shape before v0.7 — is
// answered by collecting every lane folder of `session_id`, which is what the
// only issuer (sessions.gcWorkdirs, one command per completed session) means
// anyway.
//
// Every named workdir produces a row in the answer, deleted or refused. The
// server decides WHAT may go (§6); the daemon's whole job is to do it and say
// what happened, and a refusal it keeps to itself is indistinguishable from a
// daemon that is not listening.
func (d *Daemon) gc(ctx context.Context, c contracts.Command) {
	targets := make([]workdir.Info, 0, len(c.Workdirs))
	for _, w := range c.Workdirs {
		targets = append(targets, workdir.Info{Kind: "dir", Path: w.Path, SessionID: c.SessionID, GC: &workdir.GCResult{ID: w.ID}})
	}
	if len(targets) == 0 && c.SessionID != "" {
		for _, w := range workdir.SessionLanes(d.Cfg.WorkdirRoot, c.SessionID) {
			w.GC = &workdir.GCResult{}
			targets = append(targets, w)
		}
	}
	if len(targets) == 0 {
		d.Log("gc: nothing to collect (session=%q workdirs=%d)", c.SessionID, len(c.Workdirs))
		return
	}
	report := make([]workdir.Info, 0, len(targets))
	for _, w := range targets {
		res := *w.GC
		switch {
		case d.holdsWorkdir(w.Path):
			// A live attempt is still writing there. §6 lets the daemon
			// refuse with a reason (잠금·프로세스 잔존) and the server
			// re-issues the command; deleting the directory under a running
			// runtime is how a lane fails in a way nobody can explain.
			res.Status, res.Reason = workdir.GCRefused, workdir.GCReasonProcessAlive
		case workdir.IsWorktree(w.Path):
			// §6 / E13-10: `git worktree remove` only, and the branch stays.
			// No --force — git refuses a checkout with modified or untracked
			// files, and that refusal is the answer, not an obstacle.
			if reason, err := workdir.RemoveWorktree(w.Path); err != nil {
				res.Status, res.Reason = workdir.GCRefused, reason
				d.Log("gc %s: %s: %v", w.Path, reason, err)
			} else {
				res.Status = workdir.GCDeleted
				w.Bytes = 0
			}
		default:
			if err := workdir.Remove(d.Cfg.WorkdirRoot, w.Path); err != nil {
				res.Status, res.Reason = workdir.GCRefused, err.Error()
				d.Log("gc %s: %v", w.Path, err)
			} else {
				res.Status = workdir.GCDeleted
				w.Bytes = 0
			}
		}
		w.GC = &res
		report = append(report, w)
		d.Log("gc %s: %s %s", w.Path, res.Status, res.Reason)
	}
	// The answer carries the gc rows AND the directories still on disk: the
	// server consumes the command by no longer seeing what it asked about
	// (§4.3 "해당 workdir 보고에서 삭제 확인"), so a report that listed only
	// the collected rows would leave every surviving workdir looking gone.
	live, err := workdir.List(d.Cfg.WorkdirRoot)
	if err != nil {
		d.Log("gc: list workdirs: %v", err)
	}
	report = append(report, live...)
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := d.Server.Workdirs(rctx, d.Cfg.RuntimeID, api.WorkdirsRequest{Workdirs: report}); err != nil {
		d.Log("gc: report: %v", err)
	}
}

// finishWorkdir is §4.4's `workdir` block. The git measurement runs a couple
// of git commands against the checkout the attempt just left; for a plain
// folder it is nil and the block is just the path, as before.
func (d *Daemon) finishWorkdir(wd string) *api.FinishWorkdir {
	if wd == "" {
		return nil
	}
	return &api.FinishWorkdir{Path: wd, Git: workdir.Git(wd)}
}

func (d *Daemon) killAfter() time.Duration {
	if d.KillAfter > 0 {
		return d.KillAfter
	}
	return contracts.KillAfterTerm
}

func (d *Daemon) start(ctx context.Context, b contracts.TaskBundle) {
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.runAttempt(ctx, b)
	}()
}

// taskEnv is the harness §2.1 COLAB_* set for one attempt.
func (d *Daemon) taskEnv(b contracts.TaskBundle) acp.TaskEnv {
	return acp.TaskEnv{TaskToken: b.TaskToken, ServerURL: d.Cfg.ServerURL, TaskID: b.Task.ID, Attempt: b.Task.Attempt, LaneID: b.Task.LaneID, SessionID: b.Task.SessionID, AgentName: b.Task.AgentName}
}

// mcpServers is the session/new·load `mcpServers` list: the colab MCP server
// only (harness §2, colab-cli.md §3), carrying the attempt's COLAB_* env.
func (d *Daemon) mcpServers(b contracts.TaskBundle) []acp.MCPServer {
	env := acp.Env(b.Profile.RuntimeKind, d.taskEnv(b), nil)
	return []acp.MCPServer{acp.ColabMCPServer(d.Cfg.ColabBin, env)}
}

// toolSurface is the harness §10 surface to PREPARE this attempt for: the
// last value measured on this machine (probe turn or a previous attempt),
// else the §10 table. The wrapper file and the CLI-path rewrite have to be
// decided before the process exists, so there is no measurement of this
// attempt's own to use; the runner still measures and the value is fed back.
func (d *Daemon) toolSurface(kind contracts.RuntimeKind) string {
	d.mu.Lock()
	s := d.surface[kind]
	d.mu.Unlock()
	if s != "" {
		return s
	}
	return acp.DefaultToolSurface(kind)
}

func (d *Daemon) spawnConfig(b contracts.TaskBundle, wd string) acp.Config {
	if d.SpawnConfig != nil {
		return d.SpawnConfig(b, wd)
	}
	cmd, args := acp.Command(b.Profile.RuntimeKind, b.Profile.AdapterPin, b.Profile.Args)
	env := acp.Env(b.Profile.RuntimeKind, d.taskEnv(b), b.Profile.Env)
	var stderr string
	if d.Cfg.StderrDir != "" && os.MkdirAll(d.Cfg.StderrDir, 0o755) == nil {
		stderr = filepath.Join(d.Cfg.StderrDir, key(b.Task.ID, b.Task.Attempt)+".stderr.txt")
	}
	return acp.Config{Command: cmd, Args: args, Env: env, StderrPath: stderr, KillAfter: d.killAfter()}
}

func (d *Daemon) runAttempt(ctx context.Context, b contracts.TaskBundle) {
	k := key(b.Task.ID, b.Task.Attempt)
	batcher := api.NewBatcher(ctx, d.Server, b.Task.ID, b.Task.Attempt)
	batcher.OnCommands = func(cs []contracts.Command) { d.handleCommands(ctx, cs) }
	finish := func(req api.FinishRequest) {
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = batcher.Close(fctx)
		req.LastSeq = batcher.LastSeq()
		var err error
		for i := 0; i < 3; i++ {
			if err = d.Server.Finish(fctx, b.Task.ID, b.Task.Attempt, req); err == nil || !api.IsNetwork(err) {
				break
			}
			select {
			case <-fctx.Done():
			case <-time.After(time.Duration(i+1) * time.Second):
			}
		}
		if err != nil {
			d.Log("finish %s: %v", k, err)
		}
	}
	wd, err := workdir.Prepare(d.Cfg.WorkdirRoot, b)
	if err != nil {
		d.Log("%s workdir: %v", k, err)
		finish(api.FinishRequest{Finish: contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: err.Error()}})
		return
	}
	// harness §10: a cli_wrapper runtime ignores mcpServers and sanitises the
	// env of its shell tools, so the attempt's only channel to the platform is
	// a wrapper FILE, and every text we hand the agent must name it by
	// absolute path (v0.8.1 — the server cannot know a path we invent here).
	surface := d.toolSurface(b.Profile.RuntimeKind)
	if surface == acp.ToolSurfaceCLIWrapper {
		wrapper, werr := toolwrap.Write(d.Cfg.WorkdirRoot, b.Task.ID, b.Task.Attempt, d.Cfg.ColabBin, acp.Env(b.Profile.RuntimeKind, d.taskEnv(b), nil))
		if werr != nil {
			d.Log("%s tool wrapper: %v", k, werr)
			finish(api.FinishRequest{Finish: contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: "tool wrapper: " + werr.Error()}, Workdir: d.finishWorkdir(wd)})
			return
		}
		// Every exit from here on — completed, failed, cancelled — drops the
		// wrapper: it holds the attempt token.
		defer func() { _ = toolwrap.Remove(d.Cfg.WorkdirRoot, b.Task.ID, b.Task.Attempt) }()
		b.Brief.Text = toolwrap.RewriteCLI(b.Brief.Text, wrapper)
		b.Prompt = toolwrap.RewriteCLI(b.Prompt, wrapper)
	}

	prep, err := brief.Prepare(wd, b.Brief.Transport, b.Brief.Text)
	if err != nil {
		finish(api.FinishRequest{Finish: contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: err.Error()}, Workdir: d.finishWorkdir(wd)})
		return
	}
	defer func() { _ = brief.Remove(prep) }()
	if prep.Path != "" {
		// §8.4 턴 프롬프트 v0.16 / E13-06a: an instruction_file runtime is
		// told, on the FIRST line, to read the brief file by absolute path.
		// The daemon prepends it for the same reason it rewrites the CLI
		// wrapper path — the server does not know where this machine put the
		// workdir. It goes on AFTER the wrapper rewrite: the pointer contains
		// no `colab ` command, and rewriting it would be a no-op that only
		// risks mangling the path.
		b.Prompt = brief.PrependPointer(wd, b.Prompt)
	}

	midturn := d.usageMidturn(b)
	if b.Profile.RuntimeKind == contracts.RuntimeClaudeCode && !midturn && d.Cfg.UsageMidturnEnabled() {
		// D-18 tier 2. One log line and nothing else: it is not a task event
		// because nothing about the TASK changed — the session simply has no
		// budget for the in-turn check to enforce.
		d.Log("%s raw SDK stream off: no budget in the bundle (D-18)", k)
	}
	// The heartbeater exists before the runner because the runner calls back
	// into it (OnUsage); its `r` is filled in on the next line.
	hb := &heartbeater{d: d, b: b, bt: batcher}
	runner := acp.New(acp.Attempt{
		Bundle: b, Workdir: wd, Cmd: d.spawnConfig(b, wd), MCPServers: d.mcpServers(b), Sink: batcher, Clock: d.Clock, DaemonVersion: d.Version,
		// harness §7 v0.8.5: the raw SDK stream is what makes the heartbeat's
		// `usage` non-zero before the turn ends (D-17).
		RawSDKMessages: midturn,
		OnUsage:        func() { hb.send(ctx) },
		OnSpawn: func(pgid int) {
			if err := d.Orphans.Record(orphan.Record{TaskID: b.Task.ID, Attempt: b.Task.Attempt, PGID: pgid, StartedAt: d.Clock.Now().UTC(), Workdir: wd}); err != nil {
				d.Log("%s pgid record: %v", k, err)
			}
			_ = d.Server.Phase(ctx, b.Task.ID, b.Task.Attempt, api.PhaseRequest{Phase: "preparing", PGID: pgid, WorkdirPath: wd})
		},
		OnRunning: func() {
			_ = d.Server.Phase(ctx, b.Task.ID, b.Task.Attempt, api.PhaseRequest{Phase: "running", WorkdirPath: wd})
		},
	})
	run := &attemptRun{bundle: b, runner: runner, workdir: wd}
	d.mu.Lock()
	d.running[k] = run
	d.mu.Unlock()
	hb.r = runner
	hbStop := make(chan struct{})
	go d.heartbeat(ctx, hb, hbStop)

	res := runner.Run(ctx)
	close(hbStop)

	d.mu.Lock()
	delete(d.running, k)
	for s := range d.seen {
		if len(s) > len(k) && s[len(s)-len(k):] == k {
			delete(d.seen, s)
		}
	}
	if res.AllowOnceMissing >= 3 {
		d.allowMissing[b.Profile.RuntimeKind] = true
	}
	if res.ToolSurface != "" {
		d.surface[b.Profile.RuntimeKind] = res.ToolSurface
	}
	d.mu.Unlock()
	if res.ToolSurface != "" && res.ToolSurface != surface {
		// Never silent: the attempt was prepared for the wrong surface, so
		// the next one on this daemon is prepared for the measured one.
		d.Log("%s tool_surface measured=%s prepared=%s", k, res.ToolSurface, surface)
	}
	select {
	case d.slotFreed <- struct{}{}:
	default:
	}
	_ = d.Orphans.Remove(b.Task.ID, b.Task.Attempt) // E11-02

	f := contracts.Finish{Outcome: res.Outcome, StopReason: res.StopReason, Usage: res.Usage, RuntimeSessionRef: res.SessionRef, ResumeOutcome: res.ResumeOutcome}
	if res.Failure != nil {
		if f.StopReason == "" {
			f.StopReason = res.Failure.Detail
		}
		// daemon-protocol §4.4: `cancelled` is an outcome, not a failure — the
		// server records failure_kind itself when it ends the task (E10-13).
		if res.Outcome != "cancelled" {
			f.FailureKind = res.Failure.Kind
			f.NotBefore = res.Failure.NotBefore
		}
	}
	finish(api.FinishRequest{Finish: f, Workdir: d.finishWorkdir(wd)})
	d.Log("%s finished outcome=%s stop=%s", k, res.Outcome, f.StopReason)
}

// usageMidturn reports whether this attempt asks the runtime for in-turn
// usage. Only claude_code has a channel for it (harness §7 v0.8.5); on hermes
// the flag would buy nothing but `_meta`, which hermes drops anyway.
//
// D-18: the stream is bought for ONE purpose — the server's in-turn budget
// check (FR-7.3 M9) needs a non-zero `usage` on the heartbeat before the turn
// ends. A session with no budget at all has nothing for that check to
// enforce, so the ~4× messages and ~2× bytes measured in PR #145 buy nothing.
//
// THREE TIERS, in this order (Lead decision 2026-09-07):
//
//  1. an operator's explicit `usage_midturn: false` in daemon.json — a hard
//     kill switch, honoured even for a budgeted session. probe §9 then
//     advertises `usage_midturn: false` too (the EFFECTIVE value), so the
//     server knows to fall back to finish-time enforcement (E9-10) instead of
//     waiting for in-turn numbers that will never arrive.
//  2. the automatic per-attempt rule: budget set → on, no budget → off.
//  3. the default, on.
//
// The automatic rule replaces the DEFAULT, not the switch. Tier 2 is a
// per-attempt decision and does not change what the runtime is advertised as
// being capable of — it is a choice not to ask, not an absence of the
// ability — so it is recorded in the daemon log and nowhere else.
func (d *Daemon) usageMidturn(b contracts.TaskBundle) bool {
	if b.Profile.RuntimeKind != contracts.RuntimeClaudeCode {
		return false
	}
	if !d.Cfg.UsageMidturnEnabled() {
		return false
	}
	return BundleHasBudget(b)
}

// BundleHasBudget reports whether the bundle carries any budget the daemon
// could hit: a task cap (`budget_usd`, or the approved raise
// `budget_override_usd`) or the session's remaining budget
// (`limits.budget_usd`) — the same three numbers §4.4's 유효 예산 is a min of.
func BundleHasBudget(b contracts.TaskBundle) bool {
	return b.Task.BudgetUSD != nil || b.Task.BudgetOverrideUSD != nil || b.Limits.BudgetUSD != nil
}

// heartbeater owns the attempt's heartbeat channel. Two goroutines use it —
// the 15s ticker and the runner's OnUsage callback the moment a turn's usage
// lands — so the send is serialised: `TakePreview` consumes state, and two
// concurrent heartbeats would race for the same partial output.
type heartbeater struct {
	d  *Daemon
	b  contracts.TaskBundle
	r  *acp.Runner
	bt *api.Batcher
	mu sync.Mutex
}

func (h *heartbeater) send(ctx context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	res, err := h.d.Server.Heartbeat(hctx, h.b.Task.ID, h.b.Task.Attempt, api.HeartbeatRequest{Usage: h.r.Usage(), LastSeq: h.bt.LastSeq(), Preview: h.bt.TakePreview()})
	cancel()
	if err != nil {
		// A heartbeat is a liveness signal, never fatal to the attempt
		// (§4.2 v0.3): the server ignores a bad `preview` and still
		// returns 200, and any other status — 4xx included — is only
		// logged so the next tick retries.
		h.d.Log("heartbeat %s: %v", key(h.b.Task.ID, h.b.Task.Attempt), err)
		return
	}
	h.d.handleCommands(ctx, res.Commands)
}

func (d *Daemon) heartbeat(ctx context.Context, hb *heartbeater, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-d.Clock.After(d.HeartbeatInterval):
		}
		hb.send(ctx)
	}
}
