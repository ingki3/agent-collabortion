// K-14 (daemon-protocol v0.8.3 §4.1·§6, T-D15) through the loop: the bundle's
// `workdir.id` rides on the lane-end §6 row, survives a daemon restart via
// the checkout's name tag (the probe's full report), and comes back on the gc
// receipt — while the checkout's `git status` never shows the tag.
package loop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

const bundleWorkdirID = "7c1d2e3f-0000-4000-8000-0000000000d2"

// rowsFor returns every §6 row reported for path, in wire order.
func rowsFor(srv *memServer, path string) []workdir.Info {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	var out []workdir.Info
	for _, r := range srv.workdirReports {
		for _, w := range r.Workdirs {
			if w.Path == path {
				out = append(out, w)
			}
		}
	}
	return out
}

func TestBundleWorkdirIDIsEchoedOnEveryReport(t *testing.T) {
	repo := initRepo(t)
	b := worktreeBundle("t-k14", repo, 1)
	b.Task.SessionID = "66666666-6666-4666-8666-666666666666"
	b.Task.AgentID = "77777777-7777-4777-8777-777777777777"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)
	b.Workdir.ID = bundleWorkdirID
	b.Workdir.Path = workdir.WorktreePath(root, "sess", "backend")
	srv.queue[0] = b
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(script, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 30*time.Second, func() bool { return srv.finished() == 1 })
	waitFor(t, 10*time.Second, func() bool { return len(rowsFor(srv, b.Workdir.Path)) > 0 })
	cancel()
	<-done

	// (1) The lane-end row: the bundle's id, plus everything v0.7.3 wanted.
	rows := rowsFor(srv, b.Workdir.Path)
	if len(rows) != 1 {
		t.Fatalf("%d rows for the checkout after one attempt, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ID != bundleWorkdirID {
		t.Errorf("lane-end row id = %q, want the bundle's %q (§6 v0.8.3 \"번들이 준 값을 그대로\")", row.ID, bundleWorkdirID)
	}
	if row.SessionID != b.Task.SessionID || row.AgentID != b.Task.AgentID || row.Kind != "worktree" || row.Git == nil || row.Bytes <= 0 {
		t.Errorf("lane-end row = %+v, want session·agent·kind·git·bytes as before", row)
	}

	// (2) Hygiene: the tag is in the checkout and out of `git status`, in the
	// checkout and in the source repository (E13-03~06).
	if got := workdir.ReadMarker(b.Workdir.Path); got != bundleWorkdirID {
		t.Errorf("name tag = %q, want %q", got, bundleWorkdirID)
	}
	if out, _ := gitrepo.Run(b.Workdir.Path, "status", "--porcelain"); out != "" {
		t.Errorf("`git status` in the checkout after the lane = %q, want empty", out)
	}
	if out, _ := gitrepo.Run(repo, "status", "--porcelain"); out != "" {
		t.Errorf("the SOURCE repository is dirty after the lane: %q", out)
	}
	if entries, _ := os.ReadDir(workdir.IndexDir(root)); len(entries) != 0 {
		t.Errorf("index directory has %d entries after a v0.8.3 bundle, want 0 (index 폐기)", len(entries))
	}

	// (3) A RESTARTED daemon — nothing in memory, same root — reports the
	// checkout on its start-up probe with the id read from the tag.
	srv2 := &memServer{root: root}
	d2 := &Daemon{
		Cfg:               config.Config{ServerURL: "mem", RuntimeID: "rt", DaemonToken: "cdt", WorkdirRoot: root, Capacity: 1},
		Server:            srv2,
		Version:           "test",
		Orphans:           orphan.Store{Root: root, KillAfter: time.Second},
		Log:               t.Logf,
		HeartbeatInterval: 60 * time.Millisecond,
		ClaimWait:         50 * time.Millisecond,
		ProbeCommand:      func(contracts.RuntimeKind) (string, []string, []string, bool) { return "", nil, nil, false },
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- d2.Run(ctx2) }()
	waitFor(t, 10*time.Second, func() bool { return len(rowsFor(srv2, b.Workdir.Path)) > 0 })
	probeRow := rowsFor(srv2, b.Workdir.Path)[0]
	if probeRow.ID != bundleWorkdirID {
		t.Errorf("probe row id after restart = %q, want %q (§6: 재시작 뒤에는 `<path>/.colab-workdir.json` 을 읽는다)", probeRow.ID, bundleWorkdirID)
	}
	if probeRow.Kind != "worktree" || probeRow.Git == nil {
		t.Errorf("probe row after restart = %+v, want kind and git", probeRow)
	}

	// (4) The gc receipt names the row the same way, and the answer to the
	// command echoes the command's id too (§4.3 {id, path} ↔ §6 gc.id).
	d2.gc(ctx2, contracts.Command{
		Type: contracts.CmdGC, SessionID: b.Task.SessionID,
		Workdirs: []contracts.GCWorkdir{{ID: bundleWorkdirID, Path: b.Workdir.Path}},
	})
	cancel2()
	<-done2
	var receipt *workdir.Info
	for _, r := range rowsFor(srv2, b.Workdir.Path) {
		if r.GC != nil {
			r := r
			receipt = &r
		}
	}
	if receipt == nil {
		t.Fatalf("no gc receipt row for %s", b.Workdir.Path)
	}
	if receipt.ID != bundleWorkdirID || receipt.GC.ID != bundleWorkdirID || receipt.GC.Status != workdir.GCDeleted {
		t.Errorf("receipt = %+v (gc %+v), want id %s on the row and on gc, status deleted", receipt, receipt.GC, bundleWorkdirID)
	}
	if _, err := os.Stat(b.Workdir.Path); !os.IsNotExist(err) {
		t.Errorf("checkout still on disk after gc")
	}
}

// An older server's bundle (no id): the row goes out on the pair exactly as
// v0.7.3 did, and the index record is written — the one-round fallback.
func TestOldServerBundleReportsOnThePairAndKeepsTheIndex(t *testing.T) {
	repo := initRepo(t)
	b := worktreeBundle("t-k14-old", repo, 1)
	b.Task.SessionID = "88888888-8888-4888-8888-888888888888"
	b.Task.AgentID = "99999999-9999-4999-8999-999999999999"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(script, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 30*time.Second, func() bool { return srv.finished() == 1 })
	// No server path in an old bundle: the daemon's own plan names it.
	path := workdir.WorktreePath(root, b.Task.SessionID, "backend")
	waitFor(t, 10*time.Second, func() bool { return len(rowsFor(srv, path)) > 0 })
	cancel()
	<-done

	row := rowsFor(srv, path)[0]
	if row.ID != "" {
		t.Errorf("row id = %q from a bundle with no id, want empty (pair fallback)", row.ID)
	}
	if row.SessionID != b.Task.SessionID || row.AgentID != b.Task.AgentID {
		t.Errorf("row = %+v, want the pair", row)
	}
	if workdir.ReadMarker(path) != "" {
		t.Errorf("a name tag was written from a bundle with no id")
	}
	if _, ok := workdir.LookupWorkdir(root, path); !ok {
		t.Errorf("no index record — the fallback for an old server is gone")
	}
}

// "`id` 는 번들이 준 값을 그대로" (§6 v0.8.3): the lane-end row takes the
// BUNDLE's id even when the checkout's tag says something else (a tag from a
// row the server re-keyed, or one an agent edited). The tag is what a
// restarted daemon falls back to; while the bundle is in hand, it wins.
func TestLaneEndRowTakesTheBundleIDOverAStaleTag(t *testing.T) {
	repo := initRepo(t)
	b := worktreeBundle("t-k14-stale", repo, 1)
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)
	b.Workdir.ID = bundleWorkdirID
	b.Workdir.Path = workdir.WorktreePath(root, "sess", "backend")
	srv.queue[0] = b
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" && req.WorkdirPath != "" {
			// After preparation wrote the tag, before the lane ends.
			if err := workdir.WriteMarker(req.WorkdirPath, "stale-0000-4000-8000-000000000000"); err != nil {
				t.Error(err)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 30*time.Second, func() bool { return srv.finished() == 1 })
	waitFor(t, 10*time.Second, func() bool { return len(rowsFor(srv, b.Workdir.Path)) > 0 })
	cancel()
	<-done
	if row := rowsFor(srv, b.Workdir.Path)[0]; row.ID != bundleWorkdirID {
		t.Errorf("lane-end row id = %q, want the bundle's %q over the stale tag", row.ID, bundleWorkdirID)
	}
}
