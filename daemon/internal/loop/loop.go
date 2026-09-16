// Package loop is the daemon's claim loop (daemon-protocol §4): orphan sweep
// → probe → claim (long-poll) → per-attempt run (workdir, brief, harness,
// events, heartbeat 15s, finish) → commands (cancel / revoke / probe).
package loop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/brief"
	"github.com/ingki3/agent-collabortion/daemon/internal/commands"
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
	// ConfigPath, when set, is re-read before every probe for the fields an
	// operator edits while the daemon runs — today `repos[]` (D-20: `daemon
	// repos add` writes the file; the next probe advertises it without a
	// restart). Pairing state is never taken from the re-read.
	ConfigPath string
	// Debug is the progress log's detail tier (D-24, internal/dlog): one
	// line per task_event the attempt puts on the wire. Nil → dropped, which
	// is also what a daemon at the default level does.
	Debug func(format string, args ...any)

	// ProbeTurn runs the PONG turn on the start-up / daily / commanded probe.
	ProbeTurn bool
	// ProbeCommand overrides the adapter command for probes (tests).
	ProbeCommand func(kind contracts.RuntimeKind) (string, []string, []string, bool)
	// SpawnConfig overrides how an attempt's process is built (tests →
	// acpfake). Nil → acp.Command + acp.Env.
	SpawnConfig func(b contracts.TaskBundle, wd string) acp.Config
	// PrepareWorkdir overrides how the attempt's working directory is made
	// (tests). Nil → workdir.Prepare.
	//
	// It exists so the §4.1 데몬 방어 gate below can be measured THROUGH THE
	// LOOP (PR #172 리뷰 NN3). The gate answers one question — "the directory
	// the runtime is about to run in is not there" — and since D-21 that
	// answer can no longer be produced by `workdir.Prepare` itself: `dir`
	// isolation mkdir -p's the path and `worktree` isolation gets it from
	// `git worktree add`. The fault it defends against came from a preparer
	// that RETURNED A PATH IT HAD NOT MADE (T-I4 차단 ①: the checkout landed
	// in the user's repository while the daemon handed the runtime a
	// CWD-relative path), so a test that wants to see the gate has to supply
	// such a preparer. Same seam, same reason, as SpawnConfig.
	PrepareWorkdir func(root string, b contracts.TaskBundle) (string, error)

	HeartbeatInterval time.Duration // 0 → contracts.HeartbeatInterval
	ClaimWait         time.Duration // 0 → contracts.ClaimMaxWait
	ProbeInterval     time.Duration // 0 → 24h
	KillAfter         time.Duration // 0 → contracts.KillAfterTerm
	// ShutdownDrain bounds the whole §5 shutdown procedure of all running
	// attempts. 0 → defaultShutdownDrain.
	ShutdownDrain time.Duration

	mu      sync.Mutex
	running map[string]*attemptRun
	// reserved holds the slot of a claimed attempt that is not in `running`
	// (D-28): claimed and still preparing, or exited and still reporting its
	// finish. `free` below counts both. The claim loop used to count only
	// `running`, and an attempt enters that map only after its workdir,
	// wrapper and brief are prepared — during that window the next claim
	// went out for the full `capacity` again, and a burst of short turns ran
	// capacity+1 at once (T-I6 REPORT §6: 4 on a capacity of 3). The key is
	// the same as running's; `start` takes the reservation on the claim
	// goroutine, BEFORE the attempt goroutine exists, and the deferred
	// `release` gives it back after runAttempt's finish call.
	reserved     map[string]struct{}
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
	// claimErrors folds a run of identical claim failures into a few log
	// lines (PR #181 NN4).
	claimErrors repeatFold
}

// repeatFold is the D-24 "반복 오류 축약": a server that is down makes the
// claim loop fail every 2s, and at the default level that buried an
// eight-hour session's few interesting lines under thousands of identical
// ones. The first failure is logged, then the 10th, 100th, 1000th … of the
// same message; a different message starts over; the first success after a
// run says how many were folded.
type repeatFold struct {
	last  string
	count int
}

func (f *repeatFold) note(msg string, log func(string, ...any)) {
	if msg != f.last {
		if f.count > 1 {
			log("claim: previous error repeated %d times", f.count)
		}
		f.last, f.count = msg, 0
	}
	f.count++
	switch {
	case f.count == 1:
		log("claim: %s", msg)
	case f.count == 10, f.count == 100, f.count == 1000, f.count%10000 == 0:
		log("claim: %s (repeated %d times, still failing)", msg, f.count)
	}
}

func (f *repeatFold) recovered(log func(string, ...any)) {
	if f.count > 0 {
		log("claim: ok again after %d failures (%s)", f.count, f.last)
	}
	f.last, f.count = "", 0
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
	if d.Debug == nil {
		d.Debug = func(string, ...any) {}
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
	d.reserved = map[string]struct{}{}
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
	// daemon-protocol §4.5 (g): a test chat's directory is normally removed
	// by the server's `gc` on close; when the server died first the command
	// never came, so anything older than 24h under `.colab/testchat/` is
	// swept here, in the same place as the other start-up leftovers.
	for _, p := range workdir.SweepTestChats(d.Cfg.WorkdirRoot, d.Clock.Now(), workdir.TestChatMaxAge) {
		d.Log("testchat sweep: removed %s (older than %s)", p, workdir.TestChatMaxAge)
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
		free := d.Cfg.Capacity - d.occupiedLocked()
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
			d.claimErrors.note(err.Error(), d.Log)
			select {
			case <-ctx.Done():
			case <-d.Clock.After(2 * time.Second):
			}
			continue
		}
		d.claimErrors.recovered(d.Log)
		if len(res.Tasks) == 0 && len(res.Commands) == 0 {
			// D-24: an idle long-poll is the ONE thing in this loop that
			// repeats forever, so it is the one line that cannot be at the
			// default level — 30s apart it would bury an eight-hour session's
			// six interesting lines.
			d.Debug("claim idle free=%d wait=%s", free, d.ClaimWait)
		}
		d.handleCommands(ctx, res.Commands)
		for _, b := range res.Tasks {
			// D-24: the claim is where a task becomes this machine's problem,
			// and until now nothing said so — the log jumped from the
			// start-up probe straight to `finished`, minutes or hours later.
			// Everything here is what a reader needs to tie the line to the
			// server's feed (task·attempt·lane) and to know what is about to
			// be spawned.
			d.Log("%s claim kind=%s lane=%s session=%s agent=%s runtime=%s model=%s isolation=%s",
				key(b.Task.ID, b.Task.Attempt), bundleKind(b), b.Task.LaneID, b.Task.SessionID, b.Task.AgentName,
				b.Profile.RuntimeKind, b.Profile.Model, b.Workdir.Kind)
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
			r.runner.CancelNote("종료 대기 시간을 넘겨 강제로 끝냈습니다")
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
	repos := d.Cfg.Repos
	if d.ConfigPath != "" {
		if fresh, err := config.Load(d.ConfigPath); err == nil {
			repos = fresh.Repos
		} else {
			d.Log("probe: re-read %s: %v (using the repos loaded at start)", d.ConfigPath, err)
		}
	}
	o := probe.Options{DaemonVersion: d.Version, WorkdirRoot: d.Cfg.WorkdirRoot, Turn: d.ProbeTurn, AllowOnceMissing: am, Command: d.ProbeCommand, ColabBin: d.Cfg.ColabBin, Repos: repos, UsageMidturnOff: !d.Cfg.UsageMidturnEnabled(), Clock: d.Clock, Log: func(s string) { d.Log("%s", s) }}
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
			// §4.3 re-issues a command until its effect is observed, so a
			// duplicate is the NORMAL case and belongs at the detail level.
			d.Debug("command %s %s already applied", c.Type, commandTarget(c))
			continue
		}
		d.seen[k] = true
		run := d.running[key(c.TaskID, c.Attempt)]
		d.mu.Unlock()
		// D-24: a command is the server reaching into this machine — a
		// cancel, a gc, a rebind. When one of those has no visible effect the
		// first question is whether the daemon ever received it, and until
		// now only the UNKNOWN types answered it.
		d.Log("command %s %s", c.Type, commandTarget(c))
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

// eventLogSink is the D-24 detail tier: it logs every task_event on its way
// to the batcher and changes nothing else. At the default level `log` is the
// no-op init() installed, so the wrapper costs one call per event.
type eventLogSink struct {
	inner acp.Sink
	key   string
	log   func(format string, args ...any)
}

func (s eventLogSink) Emit(ev contracts.TaskEvent) {
	s.log("%s event seq=%d %s/%s ref=%s outcome=%s", s.key, ev.Seq, ev.Class, ev.Verb, ev.ObjectRef, ev.Outcome)
	s.inner.Emit(ev)
}

func (s eventLogSink) Preview(text string) { s.inner.Preview(text) }

// commandTarget names what a §4.3 command is about, for the log. cancel and
// revoke carry (task, attempt); gc and rebind_prepare carry a session and a
// workdir list instead, and `key("", 0)` would print a bare ".0" for them.
func commandTarget(c contracts.Command) string {
	parts := make([]string, 0, 3)
	if c.TaskID != "" {
		parts = append(parts, "task="+key(c.TaskID, c.Attempt))
	}
	if c.SessionID != "" {
		parts = append(parts, "session="+c.SessionID)
	}
	if len(c.Workdirs) > 0 {
		parts = append(parts, fmt.Sprintf("workdirs=%d", len(c.Workdirs)))
	}
	if len(parts) == 0 {
		return "target=-"
	}
	return strings.Join(parts, " ")
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
	if c.TestChatID != "" {
		d.gcTestChat(ctx, c)
		return
	}
	targets := make([]workdir.Info, 0, len(c.Workdirs))
	for _, w := range c.Workdirs {
		// The receipt travels on a §6 row, so it needs the same identity every
		// other row needs (v0.7.3): a row the server cannot match is skipped,
		// and the command is then never consumed — it is re-issued every 30s
		// until the 24h TTL writes "명령 미소비 만료" into the feed (§4.3).
		row := workdir.Describe(d.Cfg.WorkdirRoot, w.Path, c.SessionID)
		row.GC = &workdir.GCResult{ID: w.ID}
		targets = append(targets, row)
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
				workdir.ForgetWorkdir(d.Cfg.WorkdirRoot, w.Path)
			}
		default:
			if err := workdir.Remove(d.Cfg.WorkdirRoot, w.Path); err != nil {
				res.Status, res.Reason = workdir.GCRefused, err.Error()
				d.Log("gc %s: %v", w.Path, err)
			} else {
				res.Status = workdir.GCDeleted
				w.Bytes = 0
				workdir.ForgetWorkdir(d.Cfg.WorkdirRoot, w.Path)
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

// gcTestChat is §4.5 (f): a `gc {test_chat_id, workdirs:[{id, path}]}` for a
// test chat's temporary directory. The path is deleted only when it lies under
// `<workdir_root>/.colab/testchat/` — the same realPath guard the other gc
// paths use, one directory narrower — and the receipt is the §6 row the
// contract spells out: `{id, kind: dir, path, test_chat_id, bytes: 0, gc:
// {status}}` with NO session_id (the server matches on test_chat_id and never
// stores the row). Anything else is `refused` with the reason; the server logs
// it and the command is still consumed, so a bad path cannot loop for 24h.
func (d *Daemon) gcTestChat(ctx context.Context, c contracts.Command) {
	report := make([]workdir.Info, 0, len(c.Workdirs))
	for _, w := range c.Workdirs {
		row := workdir.Info{ID: w.ID, Kind: "dir", Path: w.Path, TestChatID: c.TestChatID, LastUsedAt: d.Clock.Now().UTC()}
		res := workdir.GCResult{ID: w.ID}
		switch {
		case d.holdsWorkdir(w.Path):
			// The turn the server cancelled alongside this gc is still
			// running; the server re-issues gc until the receipt says deleted.
			res.Status, res.Reason = workdir.GCRefused, workdir.GCReasonProcessAlive
		default:
			if err := workdir.RemoveTestChat(d.Cfg.WorkdirRoot, w.Path); err != nil {
				res.Status, res.Reason = workdir.GCRefused, err.Error()
				d.Log("gc testchat %s: %v", w.Path, err)
			} else {
				res.Status = workdir.GCDeleted
			}
		}
		row.GC = &res
		report = append(report, row)
		d.Log("gc testchat=%s %s: %s %s", c.TestChatID, w.Path, res.Status, res.Reason)
	}
	if len(report) == 0 {
		d.Log("gc testchat=%s: no workdirs named", c.TestChatID)
		return
	}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := d.Server.Workdirs(rctx, d.Cfg.RuntimeID, api.WorkdirsRequest{Workdirs: report}); err != nil {
		d.Log("gc testchat=%s: report: %v", c.TestChatID, err)
	}
}

// IsTestChat reports whether the bundle is a daemon-protocol v0.8 §4.5 test
// chat turn: `task.kind == "test_chat"`. Everything that differs for one —
// the directory, the missing lane report, the gc shape — keys on this; the
// colab surface (env · MCP · wrapper) keys on the token instead, as harness
// §2.1 says (acp.TaskEnv.ColabSurface).
func IsTestChat(b contracts.TaskBundle) bool { return b.Task.Kind == "test_chat" }

// bundleKind is the claim line's `kind=`: "task" for a session task (the
// field is optional on the wire), else what the server sent.
func bundleKind(b contracts.TaskBundle) string {
	if b.Task.Kind == "" {
		return "task"
	}
	return b.Task.Kind
}

// reportLaneWorkdir is the D-23 §6 report: ONE row for the directory the
// attempt just left, sent once, right after finish.
//
// Why the row is built here and not by `workdir.List`. §6 v0.7.3 makes a row
// the server can store: the SESSION UUID (never a slug or a directory name),
// `agent_id` — mandatory under `worktree`, because that isolation gives one
// checkout per agent and the server skips a row it cannot match — plus `git`
// and `bytes`, which are GC's only inputs. `workdir.Describe` recovers all of
// that from the index sidecar and the disk, and the BUNDLE is layered on top
// where it disagrees: the bundle is what §4.1 actually said, while the index
// is this daemon's memory of an earlier preparation, and a sidecar lost to a
// half-written disk must not turn into a row the server drops.
//
// The git block is taken from §4.4's finish rather than measured again: they
// are the same shape (contracts.WorkdirGit) measured seconds apart, and a
// second `git status` on a large checkout is not free.
func (d *Daemon) reportLaneWorkdir(b contracts.TaskBundle, fw *contracts.FinishWorkdir) {
	k := key(b.Task.ID, b.Task.Attempt)
	row := workdir.Describe(d.Cfg.WorkdirRoot, fw.Path, b.Task.SessionID)
	if b.Workdir.Kind != "" {
		row.Kind = b.Workdir.Kind
	}
	if b.Task.AgentID != "" {
		row.AgentID = b.Task.AgentID
	}
	// A `worktree` belongs to the AGENT and outlives any one lane, so it is
	// reported without a lane — the same rule workdir.Record follows when it
	// writes the sidecar. A `dir` is one per lane and carries it.
	if row.Kind == "worktree" {
		row.LaneID = ""
	} else if b.Task.LaneID != "" {
		row.LaneID = b.Task.LaneID
	}
	if fw.Git != nil {
		row.Git = fw.Git
	}
	// Background, not the attempt's context: this runs after finish, and on
	// SIGTERM the attempt's context is already being torn down. The report is
	// the last thing the lane owes the server.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.Server.Workdirs(ctx, d.Cfg.RuntimeID, api.WorkdirsRequest{Workdirs: []workdir.Info{row}}); err != nil {
		d.Log("%s workdir report: %v", k, err)
		return
	}
	d.Log("%s workdir report kind=%s bytes=%d %s", k, row.Kind, row.Bytes, gitSummary(row.Git))
}

// usageSummary is the §4.4 `usage` block on one line (D-24). Cache counters
// are printed only when there are any: on hermes they are always zero and
// would be four dead columns in every turn line.
func usageSummary(u contracts.Usage) string {
	s := fmt.Sprintf("in=%d out=%d cost=%.4f", u.InputTokens, u.OutputTokens, u.CostUSD)
	if u.CacheReadTokens > 0 || u.CacheWriteTokens > 0 {
		s += fmt.Sprintf(" cache_read=%d cache_write=%d", u.CacheReadTokens, u.CacheWriteTokens)
	}
	if u.Estimated {
		s += " estimated"
	}
	if u.Model != "" {
		s += " model=" + u.Model
	}
	return s
}

// gitSummary is the §6 `git` block on one line (D-24). A plain folder has
// none, and says so rather than printing four zeroes that read like a clean
// checkout — the exact misreading §6 warns about.
func gitSummary(g *contracts.WorkdirGit) string {
	if g == nil {
		return "git=none"
	}
	return fmt.Sprintf("branch=%s merged=%v dirty=%v commits_ahead=%d", g.Branch, g.Merged, g.Dirty, g.CommitsAhead)
}

// finishWorkdir is §4.4's `workdir` block. The git measurement runs a couple
// of git commands against the checkout the attempt just left; for a plain
// folder it is nil and the block is just the path, as before.
func (d *Daemon) finishWorkdir(wd string) *contracts.FinishWorkdir {
	if wd == "" {
		return nil
	}
	return &contracts.FinishWorkdir{Path: wd, Git: workdir.Git(wd)}
}

// workdirDetail is the D-21(c) failure text. It names FOUR things — the path
// that is missing, the isolation, what the bundle asked for and this daemon's
// root — because the fault always lives in the gap between two of them, and
// the message it replaced (`spawn: fork/exec …/npx: no such file or
// directory`) named none.
//
// D-27: the head of the sentence is the error's PERSON register
// (workdir.DetailOf); the English register goes to the log in runAttempt.
func workdirDetail(err error, b contracts.TaskBundle, root string) string {
	return fmt.Sprintf("%s (격리 %s, 서버가 준 경로 %q, 이 컴퓨터의 기준 폴더 %s)",
		workdir.DetailOf(err), b.Workdir.Kind, b.Workdir.Path, root)
}

func (d *Daemon) killAfter() time.Duration {
	if d.KillAfter > 0 {
		return d.KillAfter
	}
	return contracts.KillAfterTerm
}

func (d *Daemon) start(ctx context.Context, b contracts.TaskBundle) {
	k := key(b.Task.ID, b.Task.Attempt)
	// D-28: the slot is taken HERE, on the claim goroutine, so the next
	// `free` the loop computes already counts this attempt. The goroutine
	// below moves the reservation to `running` once the runner exists, or
	// gives it back on any earlier exit.
	d.mu.Lock()
	d.reserved[k] = struct{}{}
	d.mu.Unlock()
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		defer d.release(k)
		d.runAttempt(ctx, b)
	}()
}

// occupiedLocked is the number of slots the claim loop must not hand out
// again: attempts running plus attempts claimed and still preparing (D-28).
// Caller holds d.mu. A key is in at most one of the two maps — runAttempt
// moves it under the same lock — so the sum never double-counts.
func (d *Daemon) occupiedLocked() int {
	return len(d.running) + len(d.reserved)
}

// release gives an attempt's slot back (D-28) once runAttempt has returned
// — after its finish call on every path, the early exits included — and
// nudges `slotFreed` so a loop parked on `free <= 0` claims again instead
// of waiting for the next probe tick. The maps are cleared defensively:
// the normal path has moved the key from `running` to `reserved` itself.
func (d *Daemon) release(k string) {
	d.mu.Lock()
	delete(d.reserved, k)
	delete(d.running, k)
	d.mu.Unlock()
	select {
	case d.slotFreed <- struct{}{}:
	default:
	}
}

// taskEnv is the harness §2.1 COLAB_* set for one attempt.
func (d *Daemon) taskEnv(b contracts.TaskBundle) acp.TaskEnv {
	return acp.TaskEnv{TaskToken: b.TaskToken, ServerURL: d.Cfg.ServerURL, TaskID: b.Task.ID, Attempt: b.Task.Attempt, LaneID: b.Task.LaneID, SessionID: b.Task.SessionID, AgentName: b.Task.AgentName}
}

// mcpServers is the session/new·load `mcpServers` list: the colab MCP server
// only (harness §2, colab-cli.md §3), carrying the attempt's COLAB_* env.
//
// None at all when the bundle has no token (harness §2.1 v0.8.8, §4.5 test
// chat): the MCP server is the agent's channel to the platform, and a test
// chat has no platform to talk to. The wrapper (harness §10) and COLAB_*
// (acp.Env) go off on the same condition.
func (d *Daemon) mcpServers(b contracts.TaskBundle) []acp.MCPServer {
	te := d.taskEnv(b)
	if !te.ColabSurface() {
		return nil
	}
	env := acp.Env(b.Profile.RuntimeKind, te, nil)
	return []acp.MCPServer{acp.ColabMCPServer(d.Cfg.ColabBin, env, b.Task.AllowedCommands)}
}

// wrapperEnv is what the hermes wrapper exports (harness §10): the attempt's
// COLAB_* set, plus `COLAB_ALLOWED_COMMANDS` (v0.8.10, K-19) when the bundle
// restricts the role — the CLI reads it and refuses the rest with exit 3
// (colab-cli.md §2.5). It is NOT added to the runtime process env (§2.1 is a
// closed allow-list; the wrapper is the CLI's only environment on this
// surface anyway).
func (d *Daemon) wrapperEnv(b contracts.TaskBundle) []string {
	env := acp.Env(b.Profile.RuntimeKind, d.taskEnv(b), nil)
	if e := commands.EnvEntry(b.Task.AllowedCommands); e != "" {
		env = append(env, e)
	}
	return env
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

// attemptEnv is the harness §2.1 environment of one attempt's runtime
// process: allow-listed system vars, the profile's additions, and COLAB_*
// only when the bundle carries a token (§2.1 v0.8.8). Tests that replace
// SpawnConfig call this so the fake runs under the daemon's own env.
func (d *Daemon) attemptEnv(b contracts.TaskBundle) []string {
	return acp.Env(b.Profile.RuntimeKind, d.taskEnv(b), b.Profile.Env)
}

func (d *Daemon) spawnConfig(b contracts.TaskBundle, wd string) acp.Config {
	if d.SpawnConfig != nil {
		return d.SpawnConfig(b, wd)
	}
	cmd, args := acp.Command(b.Profile.RuntimeKind, b.Profile.AdapterPin, b.Profile.Args)
	env := d.attemptEnv(b)
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
	// The DETAIL tier of D-24: one line per task_event, tool calls included.
	// It is a wrapper around the sink rather than a hook inside the batcher
	// because the loop emits events of its own (the §4.1 note, the §4.1 gate
	// failure) that never pass through the runner — logging in one place only
	// would show a stream with holes in it.
	sink := acp.Sink(eventLogSink{inner: batcher, key: k, log: d.Debug})
	finish := func(req contracts.Finish) {
		fctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = batcher.Close(fctx)
		req.LastSeq = batcher.LastSeq()
		// §4.5 v0.8 `finish.transport`: the path this daemon actually ran
		// the attempt on. v1 is ACP only (contracts.Transport); it goes on
		// every attempt, as the contract allows, and the server reads it for
		// test chats.
		req.Transport = contracts.TransportACP
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
		} else {
			// D-24: the finish is the attempt's last word to the server, and
			// its outcome is the one fact a person reading the log after a
			// bad night actually needs. Only its FAILURE was logged before.
			d.Log("%s finish outcome=%s failure_kind=%s last_seq=%d stop=%s",
				k, req.Outcome, req.FailureKind, req.LastSeq, req.StopReason)
		}
		// D-23 / daemon-protocol §6: "데몬은 workdir 목록을 probe와 함께,
		// 그리고 **lane 종료 시** 보고한다." Only the two probe-shaped paths
		// existed (start-up + daily, and the answer to a `gc` command), so a
		// checkout's `bytes` reached the server no earlier than the first GC
		// sweep — S13's capacity column and the E13-16 quota numerator read
		// low until then. This is the lane-end report, and it goes on EVERY
		// exit that had a directory: `req.Workdir` is set by exactly those
		// paths (the two early returns above have no directory to report —
		// one never made it, the other found it missing).
		//
		// Deliberately after the finish call, not instead of it: §4.4's
		// `Finish.Workdir.Git` is what the server folds into the row, and a
		// §6 report that overtook it would be judged on staler git facts.
		//
		// Not for a test chat (§4.5): its directory is not a workdir row —
		// the server drops a row with no session_id, loudly (S-56(b)) — and
		// the only §6 row it ever gets is the gc receipt.
		if req.Workdir != nil && req.Workdir.Path != "" && !IsTestChat(b) {
			d.reportLaneWorkdir(b, req.Workdir)
		}
	}
	// seq numbers the events the LOOP emits before the runner exists. The
	// runner continues from it (acp.Attempt.StartSeq): (task_id, attempt,
	// seq) is the idempotency key of §4.2, so a loop event and the runner's
	// first event may not both be seq 1 — the server keeps one and drops the
	// other without a word.
	seq := 0
	nextSeq := func() int { seq++; return seq }
	prepare := d.PrepareWorkdir
	if prepare == nil {
		prepare = workdir.Prepare
		if IsTestChat(b) {
			// §4.5 (b): `<workdir_root>/.colab/testchat/<id>`, mkdir -p,
			// never a checkout — and never outside that one directory.
			prepare = workdir.PrepareTestChat
		}
	}
	wd, err := prepare(d.Cfg.WorkdirRoot, b)
	if err != nil {
		d.Log("%s workdir: %v", k, err)
		finish(contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: err.Error()})
		return
	}
	// D-24: `git worktree add` on a cold repository is the longest silent
	// stretch of an attempt, and a claim that never reaches `phase preparing`
	// is a preparation that hung. One line closes that gap.
	d.Log("%s workdir path=%s isolation=%s reuse=%v", k, wd, b.Workdir.Kind, b.Workdir.Reuse)
	if b.Workdir.Path != "" && wd != filepath.Clean(b.Workdir.Path) {
		// Never silent: §4.1 v0.7.3 says the server sends an absolute path,
		// so any resolution the daemon has to do (a relative path read against
		// the root, or a target relocated out of the user's repository) is a
		// disagreement between the two halves and belongs in the log.
		//
		// AND ON THE WIRE (PR #172 리뷰 NN2). `d.Log` is this machine's
		// stderr: nobody watching the platform can see that the daemon had to
		// move the checkout, so a server that starts sending §4.1-violating
		// paths again — the exact G7 차단 ① regression — looks perfectly
		// healthy from the feed. The note is class=runtime · verb=report ·
		// outcome=info with the fact in `detail` (PRD §7 v0.16 / S-52 rule 2:
		// `detail` is the runtime class's one free-text field, and the payload
		// is closed to anything else).
		// D-25: the person's words (COMPONENTS §8.4) — this is a feed line.
		// The daemon log line above it keeps the same facts.
		detail := fmt.Sprintf("서버가 준 작업 폴더 경로 %q → 이 컴퓨터에서는 %s 를 씁니다 (격리 %s, 기준 폴더 %s)",
			b.Workdir.Path, wd, b.Workdir.Kind, d.Cfg.WorkdirRoot)
		d.Log("%s workdir bundle path %q → %s (isolation=%s, workdir_root=%s)", k, b.Workdir.Path, wd, b.Workdir.Kind, d.Cfg.WorkdirRoot)
		sink.Emit(contracts.TaskEvent{
			TaskID: b.Task.ID, Attempt: b.Task.Attempt, Seq: nextSeq(), TS: d.Clock.Now().UTC(),
			Class: "runtime", Verb: "report", ObjectRef: "workdir.path", Outcome: "info",
			Payload: map[string]any{
				"runtime_kind": string(b.Profile.RuntimeKind),
				"detail":       detail,
			},
		})
	}
	// §4.1 v0.7.3 데몬 방어 (D-21(c)): the directory the runtime will run in
	// has to exist BEFORE the spawn. Without this the missing cwd surfaced as
	// the adapter's own `spawn: fork/exec …/npx: no such file or directory` —
	// a message about node, for a fault that is about the path — and every
	// attempt of the session died that way (T-I4 차단 ①).
	if verr := workdir.Verify(wd); verr != nil {
		detail := workdirDetail(verr, b, d.Cfg.WorkdirRoot)
		// D-27: the log gets the English register (`cause`), the feed the
		// person's — same facts, same order, one language per surface.
		d.Log("%s workdir verify: %v (isolation=%s, bundle path=%q, workdir_root=%s)", k, verr, b.Workdir.Kind, b.Workdir.Path, d.Cfg.WorkdirRoot)
		sink.Emit(contracts.TaskEvent{
			TaskID: b.Task.ID, Attempt: b.Task.Attempt, Seq: nextSeq(), TS: d.Clock.Now().UTC(),
			Class: "runtime", Verb: "error", Outcome: "failed",
			Payload: map[string]any{
				"runtime_kind": string(b.Profile.RuntimeKind),
				"failure_kind": string(contracts.FailConfig),
				"detail":       detail,
			},
		})
		finish(contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: detail})
		return
	}
	// harness §10 v0.8.10 (K-19): the bundle's `task.allowed_commands` is the
	// role's subset of colab commands, and it goes to three places — the MCP
	// server argv (mcpServers), the wrapper's env (below) and brief [2]
	// (here, before the wrapper rewrite so the names it writes get the
	// wrapper path too). Empty → everything: no flag, no variable, no lines.
	if d.taskEnv(b).ColabSurface() && len(b.Task.AllowedCommands) > 0 {
		b.Brief.Text = brief.RestrictCommands(b.Brief.Text, b.Task.AllowedCommands)
		d.Log("%s allowed commands: %s (denied: %s)", k, commands.List(b.Task.AllowedCommands), commands.List(commands.Denied(b.Task.AllowedCommands)))
	}
	// harness §10: a cli_wrapper runtime ignores mcpServers and sanitises the
	// env of its shell tools, so the attempt's only channel to the platform is
	// a wrapper FILE, and every text we hand the agent must name it by
	// absolute path (v0.8.1 — the server cannot know a path we invent here).
	surface := d.toolSurface(b.Profile.RuntimeKind)
	if surface == acp.ToolSurfaceCLIWrapper && d.taskEnv(b).ColabSurface() {
		wrapper, werr := toolwrap.Write(d.Cfg.WorkdirRoot, b.Task.ID, b.Task.Attempt, d.Cfg.ColabBin, d.wrapperEnv(b))
		if werr != nil {
			d.Log("%s tool wrapper: %v", k, werr)
			finish(contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: "tool wrapper: " + werr.Error(), Workdir: d.finishWorkdir(wd)})
			return
		}
		// Every exit from here on — completed, failed, cancelled — drops the
		// wrapper: it holds the attempt token.
		defer func() { _ = toolwrap.Remove(d.Cfg.WorkdirRoot, b.Task.ID, b.Task.Attempt) }()
		b.Brief.Text = toolwrap.RewriteCLI(b.Brief.Text, wrapper)
		b.Prompt = toolwrap.RewriteCLI(b.Prompt, wrapper)
	}

	// harness §10 v0.8.7: the server writes `{{COLAB_REBIND_DIR}}` where it
	// needs a path only this daemon knows; we fill it here — after the
	// wrapper rewrite, before the pointer line. Every runtime, not just
	// cli_wrapper: the placeholder is about paths, not about tool surface.
	vals := d.placeholderValues(b)
	b.Brief.Text = substitutePlaceholders(b.Brief.Text, vals)
	b.Prompt = substitutePlaceholders(b.Prompt, vals)
	if left := leftoverPlaceholders(b.Brief.Text, b.Prompt); len(left) > 0 {
		// §10 v0.8.7: never hand the agent a broken path. `config` because
		// nothing about this machine can retry it into working — a newer
		// server is naming a placeholder this daemon does not implement.
		detail := "치환되지 않은 자리표시자: " + strings.Join(left, ", ")
		d.Log("%s %s", k, detail)
		sink.Emit(contracts.TaskEvent{
			TaskID: b.Task.ID, Attempt: b.Task.Attempt, Seq: nextSeq(), TS: d.Clock.Now().UTC(),
			Class: "runtime", Verb: "error", Outcome: "failed",
			Payload: map[string]any{
				"runtime_kind": string(b.Profile.RuntimeKind),
				"failure_kind": string(contracts.FailConfig),
				"detail":       detail,
			},
		})
		finish(contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: detail, Workdir: d.finishWorkdir(wd)})
		return
	}

	prep, err := brief.Prepare(wd, b.Brief.Transport, b.Brief.Text)
	if err != nil {
		finish(contracts.Finish{Outcome: "failed", FailureKind: contracts.FailConfig, StopReason: err.Error(), Workdir: d.finishWorkdir(wd)})
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

	if !d.taskEnv(b).ColabSurface() {
		// §4.5 / harness §2.1 v0.8.8: say so once, at the default level. A
		// test chat whose agent "cannot post" is the design, and the log is
		// where the next person checks that before suspecting the MCP setup.
		d.Log("%s colab surface off: no task_token (kind=%s) — no COLAB_* env, no mcpServers, no CLI wrapper", k, bundleKind(b))
	}
	midturn := d.usageMidturn(b)
	// The heartbeater exists before the runner because the runner calls back
	// into it (OnUsage); its `r` is filled in on the next line.
	hb := &heartbeater{d: d, b: b, bt: batcher}
	runner := acp.New(acp.Attempt{
		Bundle: b, Workdir: wd, Cmd: d.spawnConfig(b, wd), MCPServers: d.mcpServers(b), Sink: sink, Clock: d.Clock, DaemonVersion: d.Version,
		Log: func(format string, args ...any) { d.Log(k+" "+format, args...) },
		// The loop may already have spent seq 1 on the §4.1 note above.
		StartSeq: seq,
		// harness §7 v0.8.5: the raw SDK stream is what makes the heartbeat's
		// `usage` non-zero before the turn ends (D-17).
		RawSDKMessages: midturn,
		OnUsage:        func() { hb.send(ctx) },
		OnSpawn: func(pgid int) {
			if err := d.Orphans.Record(orphan.Record{TaskID: b.Task.ID, Attempt: b.Task.Attempt, PGID: pgid, StartedAt: d.Clock.Now().UTC(), Workdir: wd}); err != nil {
				d.Log("%s pgid record: %v", k, err)
			}
			// D-24: §4.2 phase went to the SERVER and nowhere else, so a
			// runtime that never got past `preparing` (npx cold start, a
			// login prompt) looked identical in the log to one that was
			// never claimed.
			d.Log("%s phase preparing pgid=%d", k, pgid)
			_ = d.Server.Phase(ctx, b.Task.ID, b.Task.Attempt, api.PhaseRequest{Phase: "preparing", PGID: pgid, WorkdirPath: wd})
		},
		OnRunning: func() {
			d.Log("%s phase running", k)
			_ = d.Server.Phase(ctx, b.Task.ID, b.Task.Attempt, api.PhaseRequest{Phase: "running", WorkdirPath: wd})
		},
	})
	run := &attemptRun{bundle: b, runner: runner, workdir: wd}
	d.mu.Lock()
	// D-28: reservation → running under one lock, so `free` never sees the
	// slot as both or as neither.
	delete(d.reserved, k)
	d.running[k] = run
	d.mu.Unlock()
	hb.r = runner
	hbStop := make(chan struct{})
	go d.heartbeat(ctx, hb, hbStop)

	res := runner.Run(ctx)
	close(hbStop)
	// D-24: the turn ended. `usage` rides along because "what did it cost"
	// and "why did it stop" are asked in the same breath, and the finish line
	// below carries neither.
	// PR #181 NN2: a failed turn carries its kind and detail HERE, not three
	// lines later on the finish — `r.fail()` fills Failure, not StopReason,
	// so `stop=` is empty exactly when a reader most needs a reason.
	if res.Failure != nil {
		d.Log("%s turn outcome=%s stop=%s failure=%s detail=%q usage=%s", k, res.Outcome, res.StopReason, res.Failure.Kind, res.Failure.Detail, usageSummary(res.Usage))
	} else {
		d.Log("%s turn outcome=%s stop=%s usage=%s", k, res.Outcome, res.StopReason, usageSummary(res.Usage))
	}
	// K-19 evidence (claude_code with the raw stream on): the colab tools the
	// runtime actually registered, from the raw system/init — the log line
	// that shows `--allow` reached the tool list, or did not.
	if res.RawInit != nil && len(res.RawInit.Tools) > 0 {
		d.Log("%s colab tools registered: %s", k, strings.Join(colabTools(res.RawInit.Tools), ","))
	}

	d.mu.Lock()
	// D-28: the slot stays held (`reserved`) until the finish below has been
	// SENT — the server counts an attempt as running from claim to finish,
	// and a claim that went out between the process exit and the finish
	// call put capacity+1 on the server's books. `running` is left now all
	// the same: a cancel or gc arriving from here on has no process to act
	// on. The deferred release in `start` gives the slot back and nudges
	// the loop.
	delete(d.running, k)
	d.reserved[k] = struct{}{}
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
	f.Workdir = d.finishWorkdir(wd)
	finish(f)
}

// colabTools picks the colab MCP tools out of a raw system/init tool list
// (`mcp__colab__colab_message_post` → `colab_message_post`).
func colabTools(tools []string) []string {
	var out []string
	for _, t := range tools {
		if n, ok := strings.CutPrefix(t, "mcp__"+acp.ColabMCPName+"__"); ok {
			out = append(out, n)
		}
	}
	return out
}

// usageMidturn reports whether this attempt asks the runtime for in-turn
// usage. Only claude_code has a channel for it (harness §7 v0.8.5); on hermes
// the flag would buy nothing but `_meta`, which hermes drops anyway.
//
// The raw stream is bought for TWO purposes (harness §7 v0.8.9, S-66):
//
//   - the server's in-turn budget check (FR-7.3 M9) needs a non-zero `usage`
//     on the heartbeat before the turn ends (D-17);
//   - the stall watch (harness §7) needs to SEE the model generating a long
//     tool input — during a 17 KB Write the adapter sends 900+ raw
//     `input_json_delta` events and not one session/update for 100 s, and
//     the 30 KB report of a real writing turn crosses the 3-minute line.
//     Two real sessions lost 6/6 writing turns to `stall` that way.
//
// The second purpose is why D-18's tier 2 ("no budget in the bundle → stream
// off", PR #145 measured ~4× messages / ~2× bytes on the local pipe) is gone:
// an unbudgeted session has nothing for the budget check to enforce, but its
// writing turns die all the same. What remains is the operator's explicit
// `usage_midturn: false` in daemon.json — a hard kill switch; probe §9 then
// advertises `usage_midturn: false` (the EFFECTIVE value) so the server falls
// back to finish-time enforcement (E9-10). An operator who turns it off also
// turns the stall protection for long tool inputs off, and the README says so.
func (d *Daemon) usageMidturn(b contracts.TaskBundle) bool {
	if b.Profile.RuntimeKind != contracts.RuntimeClaudeCode {
		return false
	}
	return d.Cfg.UsageMidturnEnabled()
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
