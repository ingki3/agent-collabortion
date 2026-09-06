package loop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
)

func TestMain(m *testing.M) {
	acpfake.MaybeMain()
	os.Exit(m.Run())
}

// memServer is an in-memory api.Server: hands out queued bundles, records
// everything, and can attach commands to heartbeat responses.
type memServer struct {
	mu        sync.Mutex
	queue     []contracts.TaskBundle
	claims    int
	probes    []contracts.Probe
	phases    []api.PhaseRequest
	phaseFile []bool // pgid record existed when phase=preparing arrived
	events    []contracts.TaskEvent
	hbs       []api.HeartbeatRequest
	hbCmds    []contracts.Command
	finishes  []api.FinishRequest
	root      string
	claimHook func()
	phaseHook func(api.PhaseRequest)
}

func (m *memServer) Pair(context.Context, api.PairRequest) (api.PairResponse, error) {
	return api.PairResponse{RuntimeID: "rt", DaemonToken: "cdt"}, nil
}
func (m *memServer) Probe(_ context.Context, _ string, p contracts.Probe) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.probes = append(m.probes, p)
	return nil
}
func (m *memServer) Claim(ctx context.Context, _ string, req api.ClaimRequest) (api.ClaimResponse, error) {
	m.mu.Lock()
	m.claims++
	if m.claimHook != nil {
		m.claimHook()
	}
	var out []contracts.TaskBundle
	for len(m.queue) > 0 && len(out) < req.Capacity {
		out = append(out, m.queue[0])
		m.queue = m.queue[1:]
	}
	m.mu.Unlock()
	if len(out) == 0 {
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(req.WaitMS) * time.Millisecond):
		}
	}
	return api.ClaimResponse{Tasks: out}, nil
}
func (m *memServer) Phase(_ context.Context, task string, attempt int, req api.PhaseRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.phases = append(m.phases, req)
	if m.phaseHook != nil {
		m.phaseHook(req)
	}
	if req.Phase == "preparing" {
		_, err := os.Stat(filepath.Join(m.root, ".colab", "attempts", task+".1.json"))
		m.phaseFile = append(m.phaseFile, err == nil)
	}
	return nil
}
func (m *memServer) Events(_ context.Context, _ string, _ int, evs []contracts.TaskEvent) (api.EventsResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	max := 0
	for _, e := range evs {
		m.events = append(m.events, e)
		if e.Seq > max {
			max = e.Seq
		}
	}
	return api.EventsResponse{AcceptedSeqMax: max}, nil
}
func (m *memServer) Heartbeat(_ context.Context, _ string, _ int, req api.HeartbeatRequest) (api.HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hbs = append(m.hbs, req)
	return api.HeartbeatResponse{Commands: m.hbCmds}, nil
}
func (m *memServer) Finish(_ context.Context, _ string, _ int, req api.FinishRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishes = append(m.finishes, req)
	return nil
}
func (m *memServer) Workdirs(context.Context, string, api.WorkdirsRequest) error { return nil }

func (m *memServer) finished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.finishes)
}

func newDaemon(t *testing.T, srv *memServer, script acpfake.Script) (*Daemon, string) {
	root := t.TempDir()
	srv.root = root
	d := &Daemon{
		Cfg:               config.Config{ServerURL: "mem", RuntimeID: "rt", DaemonToken: "cdt", WorkdirRoot: root, Capacity: 2},
		Server:            srv,
		Version:           "test",
		Orphans:           orphan.Store{Root: root, KillAfter: time.Second},
		Log:               t.Logf,
		HeartbeatInterval: 60 * time.Millisecond,
		ClaimWait:         50 * time.Millisecond,
		KillAfter:         time.Second,
		ProbeCommand:      func(contracts.RuntimeKind) (string, []string, []string, bool) { return "", nil, nil, false },
		SpawnConfig: func(b contracts.TaskBundle, wd string) acp.Config {
			cmd, args, env := acpfake.Command(script, "")
			return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
		},
	}
	return d, root
}

// newRecordingDaemon is newDaemon with the fake's request log kept, so a test
// can assert the on-the-wire order of the §5 procedure.
func newRecordingDaemon(t *testing.T, srv *memServer, script acpfake.Script) (*Daemon, string) {
	d, _ := newDaemon(t, srv, script)
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(script, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}
	return d, record
}

func bundle(id string) contracts.TaskBundle {
	return contracts.TaskBundle{
		Task:      contracts.BundleTask{ID: id, Attempt: 1, LaneID: "lane", SessionID: "sess", AgentName: "Lead"},
		TaskToken: "ctk_x",
		Profile:   contracts.BundleProfile{RuntimeKind: contracts.RuntimeClaudeCode, Model: "sonnet"},
		Workdir:   contracts.BundleWorkdir{Kind: "dir", Reuse: true},
		Brief:     contracts.BundleBrief{Transport: contracts.BriefACPMetaSystemPrompt, Text: "brief"},
		Prompt:    "PING",
		Limits:    contracts.BundleLimits{StallSeconds: 180},
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

// Full slice: sweep → probe → claim → preparing(pgid recorded) → running →
// events → heartbeat → finish completed → pgid record removed (E11-01·02).
func TestClaimRunFinish(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t1")}}
	d, root := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PO"}, {SleepMs: 150}, {Chunk: "NG"}}, ModelUsage: true}}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 15*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.probes) != 1 || srv.probes[0].WorkdirRoot != root {
		t.Fatalf("probe %+v", srv.probes)
	}
	if len(srv.phases) != 2 || srv.phases[0].Phase != "preparing" || srv.phases[0].PGID == 0 || srv.phases[1].Phase != "running" || !srv.phaseFile[0] {
		t.Fatalf("phases %+v file=%v", srv.phases, srv.phaseFile)
	}
	f := srv.finishes[0]
	if f.Outcome != "completed" || f.StopReason != "end_turn" || f.RuntimeSessionRef == nil || f.RuntimeSessionRef.SessionID != "sess-1" || f.LastSeq == 0 || f.Workdir == nil {
		t.Fatalf("finish %+v", f)
	}
	if len(srv.hbs) == 0 {
		t.Fatal("no heartbeat")
	}
	var say int
	for _, e := range srv.events {
		if e.Class == "message" && e.Verb == "say" && e.Payload["text"] == "PONG" {
			say++
		}
	}
	if say != 1 {
		t.Fatalf("events %+v", srv.events)
	}
	if _, err := os.Stat(filepath.Join(root, ".colab", "attempts", "t1.1.json")); !os.IsNotExist(err) {
		t.Fatal("pgid record not removed after normal exit (E11-02)")
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", "sess", "lane")); err != nil {
		t.Fatal("lane workdir missing")
	}
	if err := syscall.Kill(-srv.phases[0].PGID, 0); err == nil {
		t.Fatal("runtime group still alive")
	}
}

// cancel command arriving on a heartbeat response → §5 → finish cancelled,
// process group gone (E11-07); the same command twice is applied once.
func TestCancelCommandViaHeartbeat(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t2")}}
	d, _ := newDaemon(t, srv, acpfake.Script{StayAlive: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "working"}, {Hang: true}}}}})
	srv.hbCmds = []contracts.Command{{Type: contracts.CmdCancel, TaskID: "t2", Attempt: 1, Reason: "director"}, {Type: contracts.CmdCancel, TaskID: "t2", Attempt: 1, Reason: "director"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 15*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done
	srv.mu.Lock()
	defer srv.mu.Unlock()
	f := srv.finishes[0]
	// §4.4: cancelled is an outcome, not a failure — no failure_kind.
	if f.Outcome != "cancelled" || f.FailureKind != "" {
		t.Fatalf("finish %+v", f)
	}
	cancels := 0
	for _, e := range srv.events {
		if e.Class == "runtime" && e.Verb == "cancel" && e.Outcome == "started" {
			cancels++
		}
	}
	if cancels != 1 {
		t.Fatalf("cancel applied %d times", cancels)
	}
	if err := syscall.Kill(-srv.phases[0].PGID, 0); err == nil {
		t.Fatal("runtime group still alive after cancel")
	}
}

// E11-05 — a recorded live orphan group is killed BEFORE the first claim.
func TestOrphanSweptBeforeFirstClaim(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & sleep 60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	pgid := cmd.Process.Pid
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	st := orphan.Store{Root: root}
	st.Record(orphan.Record{TaskID: "old", Attempt: 1, PGID: pgid})
	aliveAtClaim := true
	srv.claimHook = func() { aliveAtClaim = orphan.Alive(pgid) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 10*time.Second, func() bool { srv.mu.Lock(); defer srv.mu.Unlock(); return srv.claims >= 1 })
	cancel()
	<-done
	if aliveAtClaim {
		t.Fatal("orphan group still alive at first claim")
	}
	if recs, _ := st.List(); len(recs) != 0 {
		t.Fatalf("records left %+v", recs)
	}
}

// revoke for an attempt that is not running kills its recorded orphan group.
func TestRevokeKillsRecordedOrphan(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & sleep 60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait()
	pgid := cmd.Process.Pid
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	d.init()
	st := orphan.Store{Root: root}
	st.Record(orphan.Record{TaskID: "gone", Attempt: 3, PGID: pgid})
	d.handleCommands(context.Background(), []contracts.Command{{Type: contracts.CmdRevoke, TaskID: "gone", Attempt: 3}})
	if orphan.Alive(pgid) {
		t.Fatal("group alive after revoke")
	}
	if recs, _ := st.List(); len(recs) != 0 {
		t.Fatalf("records left %+v", recs)
	}
}

// R1 / R2 — the default spawn config carries COLAB_TASK_ATTEMPT (§2.1) and
// the attempt's mcpServers is the colab server with the same COLAB_* env
// and the configured binary.
func TestSpawnConfigCarriesAttemptAndColabMCP(t *testing.T) {
	d := &Daemon{Cfg: config.Config{ServerURL: "http://s", RuntimeID: "rt", DaemonToken: "cdt", WorkdirRoot: t.TempDir(), Capacity: 1, ColabBin: "/opt/colab"}}
	b := bundle("t1")
	b.Task.Attempt = 2
	b.Profile.Env = map[string]string{"COLAB_SERVER_URL": "https://evil", "MY_KEY": "v"}
	cfg := d.spawnConfig(b, t.TempDir())
	for k, v := range map[string]string{"COLAB_TASK_ATTEMPT": "2", "COLAB_TASK_ID": "t1", "COLAB_SERVER_URL": "http://s", "COLAB_TASK_TOKEN": "ctk_x", "MY_KEY": "v"} {
		if acp.EnvValue(cfg.Env, k) != v {
			t.Fatalf("%s=%q want %q", k, acp.EnvValue(cfg.Env, k), v)
		}
	}
	mcp := d.mcpServers(b)
	if len(mcp) != 1 || mcp[0].Name != "colab" || mcp[0].Command != "/opt/colab" {
		t.Fatalf("mcp %+v", mcp)
	}
	env := map[string]string{}
	for _, e := range mcp[0].Env {
		env[e.Name] = e.Value
	}
	if env["COLAB_TASK_ATTEMPT"] != "2" || env["COLAB_TASK_TOKEN"] != "ctk_x" || env["COLAB_SERVER_URL"] != "http://s" || len(env) != 7 {
		t.Fatalf("mcp env %v", env)
	}
}

// events returns the events the daemon shipped for one class/verb/outcome.
func (m *memServer) findEvents(class, verb, outcome string) []contracts.TaskEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []contracts.TaskEvent
	for _, e := range m.events {
		if e.Class == class && (verb == "" || e.Verb == verb) && (outcome == "" || e.Outcome == outcome) {
			out = append(out, e)
		}
	}
	return out
}

// E10-13 — a daemon SIGTERM (ctx cancelled) during a running attempt walks the
// harness §5 procedure — pending permission answered cancelled, session/cancel,
// drain — and reports finish outcome=cancelled with no failure_kind, so the
// server ends the task instead of requeueing it (G3 D-1).
func TestShutdownRunsCancelProcedureAndFinishesCancelled(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t5")}}
	script := acpfake.Script{StayAlive: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "Bash", Kind: "execute", Command: "sleep 100"}},
		{SleepMs: 3000},
		{Permission: &acpfake.PermissionStep{ID: "t1", Title: "Bash rm", Kinds: []string{"allow_once", "reject_once"}}},
		{Chunk: "never"},
	}}}}
	d, record := newRecordingDaemon(t, srv, script)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	// the turn is in flight (the shell tool started) — now "SIGTERM"
	waitFor(t, 20*time.Second, func() bool { return len(srv.findEvents("tool", "run_shell", "started")) == 1 })
	cancel()
	<-done

	if srv.finished() != 1 {
		t.Fatalf("finishes %+v", srv.finishes)
	}
	f := srv.finishes[0]
	if f.Outcome != "cancelled" || f.FailureKind != "" {
		t.Fatalf("finish %+v — want cancelled with no failure_kind (E10-13)", f)
	}
	if errs := srv.findEvents("runtime", "error", "failed"); len(errs) != 0 {
		t.Fatalf("shutdown reported a runtime error: %+v", errs)
	}
	if c := srv.findEvents("runtime", "cancel", "started"); len(c) != 1 {
		t.Fatalf("cancel started events %+v", c)
	}
	if p := srv.findEvents("tool", "permission", "cancelled"); len(p) != 1 {
		t.Fatalf("pending permission not answered cancelled (§5 step 2): %+v", srv.findEvents("tool", "permission", ""))
	}
	// §5 order on the wire: session/cancel (step 3) before the permission answer
	recs, err := acpfake.ReadRecords(record)
	if err != nil {
		t.Fatal(err)
	}
	cancelIdx, answerIdx, promptIdx := -1, -1, -1
	for i, r := range recs {
		switch {
		case r.Method == acp.MethodSessionPrompt && promptIdx < 0:
			promptIdx = i
		case r.Method == acp.MethodSessionCancel && cancelIdx < 0:
			cancelIdx = i
		case r.Method == "" && strings.Contains(string(r.Result), "cancelled"):
			answerIdx = i
		}
	}
	if promptIdx < 0 || cancelIdx < promptIdx || answerIdx < cancelIdx {
		t.Fatalf("§5 order prompt=%d cancel=%d permission-answer=%d", promptIdx, cancelIdx, answerIdx)
	}
	if err := syscall.Kill(-srv.phases[0].PGID, 0); err == nil {
		t.Fatal("runtime group still alive after shutdown (E11-07)")
	}
}

// E10-13 — over the shutdown drain bound the attempt context is cancelled, so
// session/prompt ends with "context canceled". The cancel intent is already
// set: the attempt is still reported cancelled, with a "드레인 초과" note.
func TestShutdownDrainOverrunStillFinishesCancelled(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t6")}}
	script := acpfake.Script{StayAlive: true, IgnoreSigterm: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "Bash", Kind: "execute", Command: "sleep 100"}},
		{HangForever: true},
	}}}}
	d, _ := newDaemon(t, srv, script)
	d.ShutdownDrain = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return len(srv.findEvents("tool", "run_shell", "started")) == 1 })
	cancel()
	<-done

	if srv.finished() != 1 {
		t.Fatalf("finishes %+v", srv.finishes)
	}
	f := srv.finishes[0]
	if f.Outcome != "cancelled" || f.FailureKind != "" {
		t.Fatalf("finish %+v — want cancelled even over the drain bound", f)
	}
	over := false
	for _, e := range srv.findEvents("runtime", "cancel", "info") {
		if s, _ := e.Payload["detail"].(string); strings.Contains(s, "드레인 초과") {
			over = true
		}
	}
	if !over {
		t.Fatalf("no drain-overrun note: %+v", srv.findEvents("runtime", "cancel", ""))
	}
	if err := syscall.Kill(-srv.phases[0].PGID, 0); err == nil {
		t.Fatal("runtime group still alive after shutdown")
	}
}

// A real runtime error with no cancel intent is still failed(other) — the
// cancel path must not swallow retryable failures (§8).
func TestRuntimeErrorWithoutCancelIntentStaysFailedOther(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t7")}}
	d, _ := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Error: &acp.RPCError{Code: -32603, Message: "boom"}}}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	f := srv.finishes[0]
	if f.Outcome != "failed" || f.FailureKind != contracts.FailOther {
		t.Fatalf("finish %+v — want failed(other)", f)
	}
	if c := srv.findEvents("runtime", "cancel", ""); len(c) != 0 {
		t.Fatalf("cancel events on a plain failure: %+v", c)
	}
}

// D-1 — every probe the daemon sends carries the machine's colab CLI state,
// so a machine whose agents cannot reach the platform is visible on S11
// instead of failing the first turn silently.
func TestProbeCarriesColabCLIState(t *testing.T) {
	dir := t.TempDir()
	colab := filepath.Join(dir, "colab")
	if err := os.WriteFile(colab, []byte("#!/bin/sh\necho 'colab 1.2.3'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		bin         string
		wantPresent bool
		wantVersion string
	}{
		{"installed", colab, true, "1.2.3"},
		{"missing", filepath.Join(dir, "nope"), false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &memServer{}
			d, _ := newDaemon(t, srv, acpfake.Script{})
			d.Cfg.ColabBin = tc.bin
			ctx, cancel := context.WithCancel(context.Background())
			go d.Run(ctx)
			waitFor(t, 5*time.Second, func() bool {
				srv.mu.Lock()
				defer srv.mu.Unlock()
				return len(srv.probes) > 0
			})
			cancel()
			srv.mu.Lock()
			got := srv.probes[0].ColabCLI
			srv.mu.Unlock()
			if got.Present != tc.wantPresent || got.Version != tc.wantVersion {
				t.Fatalf("colab_cli %+v want present=%v version=%q", got, tc.wantPresent, tc.wantVersion)
			}
		})
	}
}
