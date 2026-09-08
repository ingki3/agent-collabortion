// P5-pre T-D11 — the two observability holes of a `run` nobody can follow.
//
// D-24: `daemon run` printed its start-up line, two probe lines, and then
// nothing at all while a session ran for hours (Director 실사용 2026-09-08,
// 287 bytes on disk). The tests below assert the daemon-protocol §4 lifecycle
// of one attempt is READABLE FROM THE LOG ALONE: claim → workdir → phase
// preparing → phase running → turn → finish → §6 report, in that order.
//
// D-23: §6 says the workdir list is reported "probe와 함께, 그리고 lane 종료
// 시". Only the probe halves existed, so `bytes` waited for the first gc
// sweep. The tests assert one report per attempt, carrying the identity §6
// v0.7.3 requires.
package loop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// logCapture is the daemon's stdout, as a slice.
type logCapture struct {
	mu    sync.Mutex
	lines []string
}

func (c *logCapture) sink(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf(format, args...))
}

func (c *logCapture) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.lines...)
}

// inOrder asserts every want appears as a substring, each after the previous
// one. Order matters here as much as presence: the point of D-24 is that a
// reader can FOLLOW the attempt, and lines that arrive shuffled do not tell
// anyone where it stopped.
func inOrder(t *testing.T, lines []string, want ...string) {
	t.Helper()
	at := 0
	for _, w := range want {
		found := -1
		for i := at; i < len(lines); i++ {
			if strings.Contains(lines[i], w) {
				found = i
				break
			}
		}
		if found < 0 {
			t.Errorf("missing (or out of order) log line %q", w)
			continue
		}
		at = found + 1
	}
	if t.Failed() {
		t.Logf("--- daemon log (%d lines) ---", len(lines))
		for _, l := range lines {
			t.Logf("%s", l)
		}
	}
}

func countContains(lines []string, sub string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, sub) {
			n++
		}
	}
	return n
}

// D-24 — one `dir` attempt, whole lifecycle, at the DEFAULT level.
func TestRunLogsAttemptLifecycleAtDefaultLevel(t *testing.T) {
	b := bundle("t-log")
	b.Task.LaneID = "lane-obs"
	b.Task.SessionID = "11111111-1111-4111-8111-111111111111"
	b.Task.AgentName = "backend"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, _ := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}, ModelUsage: true}}})
	cap := &logCapture{}
	d.Log = func(f string, a ...any) { cap.sink(f, a...); t.Logf(f, a...) }
	// Debug deliberately left nil: this is the shipped default.

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	waitFor(t, 10*time.Second, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.workdirReports) > 0
	})
	cancel()
	<-done

	lines := cap.all()
	inOrder(t, lines,
		"t-log.1 claim lane=lane-obs",
		"t-log.1 workdir path=",
		"t-log.1 phase preparing pgid=",
		"t-log.1 phase running",
		"t-log.1 turn outcome=completed",
		"t-log.1 finish outcome=completed",
		"t-log.1 workdir report ",
	)
	// The claim line carries what ties the log to the server's feed.
	for _, want := range []string{"session=11111111-1111-4111-8111-111111111111", "agent=backend", "runtime=claude_code", "isolation=dir"} {
		if countContains(lines, want) == 0 {
			t.Errorf("claim line missing %q", want)
		}
	}
	// The turn line carries the usage summary (D-24 "usage 요약").
	if countContains(lines, "usage=in=") == 0 {
		t.Errorf("turn line has no usage summary")
	}
	// The DETAIL tier must be off: this is the "과도한 잡음(도구 호출 전부) 금지"
	// half of D-24, and it is the half a logging change silently breaks.
	if n := countContains(lines, " event seq="); n != 0 {
		t.Errorf("%d per-event lines at the default level (want 0)", n)
	}
	if n := countContains(lines, "claim idle"); n != 0 {
		t.Errorf("%d idle-poll lines at the default level (want 0)", n)
	}
	// A whole attempt has to stay legible: a handful of lines, not a stream.
	if n := len(lines); n > 20 {
		t.Errorf("%d lines for one attempt — the default level is too loud", n)
	}
}

// D-24 — the detail tier adds the events and keeps the lifecycle.
func TestRunLogsEventsAtDebugLevel(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t-dbg")}}
	d, _ := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}})
	cap := &logCapture{}
	d.Log = cap.sink
	d.Debug = cap.sink

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	lines := cap.all()
	if n := countContains(lines, " event seq="); n == 0 {
		t.Fatalf("no per-event lines at debug level")
	}
	inOrder(t, lines, "t-dbg.1 claim ", "t-dbg.1 turn outcome=", "t-dbg.1 finish outcome=")
	// Every event the SERVER received has a line, and every line names the
	// class/verb pair — the log and the feed have to be comparable.
	srv.mu.Lock()
	got := map[string]bool{}
	for _, e := range srv.events {
		got[fmt.Sprintf("seq=%d %s/%s", e.Seq, e.Class, e.Verb)] = true
	}
	srv.mu.Unlock()
	if len(got) == 0 {
		t.Fatalf("the fake server received no events")
	}
	for want := range got {
		if countContains(lines, want) == 0 {
			t.Errorf("event %q reached the server but not the log", want)
		}
	}
}

// D-24 — a §4.3 command is the server reaching into this machine; when it has
// no visible effect the first question is whether the daemon received it.
func TestCommandsAreLogged(t *testing.T) {
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	cap := &logCapture{}
	d.Log = cap.sink
	d.Debug = cap.sink
	d.init()

	cmds := []contracts.Command{
		{Type: contracts.CmdCancel, TaskID: "t-c", Attempt: 2, Reason: "director"},
		{Type: contracts.CmdGC, SessionID: "sess-uuid", Workdirs: []contracts.GCWorkdir{{ID: "w1", Path: filepath.Join(root, "gone")}}},
		{Type: contracts.CmdCancel, TaskID: "t-c", Attempt: 2, Reason: "director"}, // §4.3 re-issue
	}
	d.handleCommands(context.Background(), cmds)
	waitFor(t, 10*time.Second, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.workdirReports) > 0
	})

	lines := cap.all()
	inOrder(t, lines,
		"command cancel task=t-c.2",
		"command gc session=sess-uuid workdirs=1",
		"command cancel task=t-c.2 already applied",
	)
	// The re-issue is the NORMAL case (§4.3 repeats until the effect shows),
	// so it belongs at the detail level and not next to the first one.
	if n := countContains(lines, "command cancel task=t-c.2"); n != 2 {
		t.Errorf("cancel logged %d times, want 2 (one applied, one duplicate)", n)
	}
}

// D-23 — a `dir` lane reports its workdir once, right after finish, with the
// session uuid and the lane the bundle named.
func TestFinishReportsWorkdirOnce(t *testing.T) {
	b := bundle("t-d23")
	b.Task.SessionID = "22222222-2222-4222-8222-222222222222"
	b.Task.AgentID = "33333333-3333-4333-8333-333333333333"
	b.Task.LaneID = "lane-d23"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, _ := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}})
	cap := &logCapture{}
	d.Log = cap.sink
	// The agent leaves something behind — that is what `bytes` is FOR, and an
	// empty t.TempDir() would let a report of zeroes pass.
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" && req.WorkdirPath != "" {
			writeFile(t, filepath.Join(req.WorkdirPath, "out.txt"), strings.Repeat("x", 4096))
		}
	}
	// The daily probe must not fire and supply the report for us: a 24h
	// interval is the default, and this attempt takes seconds.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	waitFor(t, 10*time.Second, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.workdirReports) > 0
	})
	cancel()
	<-done

	srv.mu.Lock()
	n := len(srv.workdirReports)
	first := srv.workdirReports[0]
	srv.mu.Unlock()

	if n != 1 {
		t.Errorf("%d workdir reports for one attempt, want exactly 1", n)
	}
	if len(first.Workdirs) != 1 {
		t.Fatalf("report carried %d rows, want 1", len(first.Workdirs))
	}
	row := first.Workdirs[0]
	if row.SessionID != b.Task.SessionID {
		t.Errorf("session_id %q — §6 v0.7.3 wants the session UUID, or the server skips the row", row.SessionID)
	}
	if row.LaneID != "lane-d23" {
		t.Errorf("lane_id %q, want lane-d23 (a `dir` workdir is one per lane)", row.LaneID)
	}
	if row.Kind != "dir" {
		t.Errorf("kind %q, want dir", row.Kind)
	}
	if row.Path == "" || !filepath.IsAbs(row.Path) {
		t.Errorf("path %q — §4.1 paths are absolute", row.Path)
	}
	// The report exists so `bytes` does not wait for the first gc sweep
	// (S13 용량, E13-16 쿼터 분자). A row of zeroes would be the defect.
	if row.Bytes <= 0 {
		t.Errorf("bytes=%d — the whole point of the lane-end report is that disk_bytes arrives now", row.Bytes)
	}
	if row.LastUsedAt.IsZero() {
		t.Errorf("last_used_at is zero")
	}
	// …and it goes AFTER the finish, so §4.4's git block reaches the row first.
	inOrder(t, cap.all(), "t-d23.1 finish outcome=", "t-d23.1 workdir report kind=dir bytes=4096")
}

// D-23 — a `worktree` lane. §6 v0.7.3 makes `agent_id` mandatory there and
// wants the git block on EVERY report: without them the server skips the row
// or reads "커밋 0 · 클린" and GC deletes unmerged work.
func TestFinishReportsWorktreeWorkdirWithIdentityAndGit(t *testing.T) {
	repo := initRepo(t)
	b := worktreeBundle("t-d23wt", repo, 1)
	b.Task.SessionID = "44444444-4444-4444-8444-444444444444"
	b.Task.AgentID = "55555555-5555-4555-8555-555555555555"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, _ := newDaemon(t, srv, script)
	cap := &logCapture{}
	d.Log = cap.sink
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(script, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 30*time.Second, func() bool { return srv.finished() == 1 })
	waitFor(t, 10*time.Second, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.workdirReports) > 0
	})
	cancel()
	<-done

	srv.mu.Lock()
	n := len(srv.workdirReports)
	row := srv.workdirReports[0].Workdirs[0]
	fin := srv.finishes[0]
	srv.mu.Unlock()

	if n != 1 {
		t.Errorf("%d workdir reports for one attempt, want exactly 1", n)
	}
	if row.Kind != "worktree" {
		t.Errorf("kind %q, want worktree", row.Kind)
	}
	if row.SessionID != b.Task.SessionID {
		t.Errorf("session_id %q, want the session UUID %q", row.SessionID, b.Task.SessionID)
	}
	if row.AgentID != b.Task.AgentID {
		t.Errorf("agent_id %q — §6 v0.7.3 makes it MANDATORY under worktree; without it the server skips the row", row.AgentID)
	}
	if row.LaneID != "" {
		t.Errorf("lane_id %q — a worktree belongs to the agent and outlives one lane", row.LaneID)
	}
	if row.Git == nil {
		t.Fatalf("git block missing — §6: 비면 서버는 \"커밋 0 · 클린\"으로 읽어 미병합을 지운다")
	}
	if fin.Workdir == nil || fin.Workdir.Git == nil {
		t.Fatalf("finish carried no git block to compare against")
	}
	if *row.Git != *fin.Workdir.Git {
		t.Errorf("git block %+v != finish's %+v — the two must state the same checkout", *row.Git, *fin.Workdir.Git)
	}
	if row.Git.Branch == "" {
		t.Errorf("git.branch empty")
	}
	if row.Bytes <= 0 {
		t.Errorf("bytes=%d on a checkout", row.Bytes)
	}
}

// D-23 — an attempt that dies before it has a directory reports nothing:
// there is no workdir to describe, and an invented row is worse than none.
func TestNoWorkdirReportWhenPreparationFailed(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t-noprep")}}
	d, _ := newDaemon(t, srv, acpfake.Script{})
	cap := &logCapture{}
	d.Log = cap.sink
	d.PrepareWorkdir = func(string, contracts.TaskBundle) (string, error) {
		return "", fmt.Errorf("disk full")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	srv.mu.Lock()
	n := len(srv.workdirReports)
	out := srv.finishes[0].Outcome
	srv.mu.Unlock()
	if out != "failed" {
		t.Fatalf("outcome %q, want failed", out)
	}
	if n != 0 {
		t.Errorf("%d workdir reports for an attempt that never had a directory", n)
	}
	// The failure is still in the log — that is the D-24 half.
	inOrder(t, cap.all(), "t-noprep.1 claim ", "t-noprep.1 workdir: disk full", "t-noprep.1 finish outcome=failed")
}
