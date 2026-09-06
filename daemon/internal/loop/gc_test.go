// The §4.3 `gc` command and its §6 report.
//
// Before this the daemon logged "command gc ignored (P4)" and did nothing —
// the server re-issued the command forever, the disk kept filling, and the
// only person who could act never saw it.
package loop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

func gcDaemon(t *testing.T, srv *memServer) (*Daemon, string) {
	t.Helper()
	root := t.TempDir()
	d := &Daemon{
		Cfg:    config.Config{ServerURL: "mem", RuntimeID: "rt", WorkdirRoot: root, Capacity: 1},
		Server: srv,
		Log:    t.Logf,
	}
	d.init()
	return d, root
}

func makeLane(t *testing.T, root, session, lane string) string {
	t.Helper()
	p := workdir.Path(root, session, lane)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "note.md"), []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func gcRow(t *testing.T, srv *memServer, path string) *workdir.GCResult {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, r := range srv.workdirReports {
		for _, w := range r.Workdirs {
			if w.Path == path && w.GC != nil {
				return w.GC
			}
		}
	}
	return nil
}

// The command names {id, path}: the daemon deletes the path and reports the
// row deleted, echoing the server's id so the answer matches the question.
func TestGCDeletesNamedWorkdirsAndReports(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	keep := makeLane(t, root, "sess-a", "lane-keep")
	drop := makeLane(t, root, "sess-a", "lane-drop")

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-a",
		Workdirs: []contracts.GCWorkdir{{ID: "wd-1", Path: drop}},
	})

	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Fatalf("workdir %s still on disk", drop)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("an unnamed workdir was collected too: %v", err)
	}
	row := gcRow(t, srv, drop)
	if row == nil || row.Status != workdir.GCDeleted || row.ID != "wd-1" {
		t.Fatalf("gc row = %+v, want deleted with the server's id", row)
	}
	// The survivor must still be in the report: the server consumes the
	// command by no longer SEEING what it asked about (§4.3), so a report of
	// only the collected rows would make every other workdir look gone.
	var sawKeep bool
	srv.mu.Lock()
	for _, r := range srv.workdirReports {
		for _, w := range r.Workdirs {
			if w.Path == keep {
				sawKeep = true
			}
		}
	}
	srv.mu.Unlock()
	if !sawKeep {
		t.Fatalf("the surviving workdir is missing from the §6 report")
	}
}

// The pre-v0.7 payload named only ids the daemon cannot resolve. Falling back
// to "every lane folder of this session" is what the only issuer means
// (sessions.gcWorkdirs issues one command per completed session).
func TestGCWithoutPathsFallsBackToTheSession(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	a := makeLane(t, root, "sess-b", "lane-1")
	b := makeLane(t, root, "sess-b", "lane-2")
	other := makeLane(t, root, "sess-c", "lane-1")

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-b", WorkdirIDs: []string{"x", "y"},
	})

	for _, p := range []string{a, b} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s still on disk", p)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another session's workdir was collected: %v", err)
	}
}

// worktree isolation is P4 — `git worktree remove` plus a branch to keep
// (§6, E13-10), not an rm -rf. The refusal is REPORTED.
func TestGCRefusesAWorktreeAndSaysSo(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	p := makeLane(t, root, "sess-d", "lane-1")
	if err := os.WriteFile(filepath.Join(p, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-d",
		Workdirs: []contracts.GCWorkdir{{ID: "wd-w", Path: p}},
	})

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("a worktree was deleted anyway: %v", err)
	}
	row := gcRow(t, srv, p)
	if row == nil || row.Status != workdir.GCRefused || row.Reason != workdir.GCReasonWorktreeP4 {
		t.Fatalf("gc row = %+v, want refused(isolation_worktree_p4) — silence is not an answer", row)
	}
}

// A path outside the workdir root is refused, not deleted. The path comes off
// a server row; a bug there must not turn the daemon into a remote rm -rf.
func TestGCRefusesAPathOutsideTheRoot(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = root

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-e",
		Workdirs: []contracts.GCWorkdir{{ID: "wd-o", Path: outside}},
	})

	if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
		t.Fatalf("deleted outside the workdir root: %v", err)
	}
	if row := gcRow(t, srv, outside); row == nil || row.Status != workdir.GCRefused {
		t.Fatalf("gc row = %+v, want refused", row)
	}
}

// §4.3 idempotency is keyed on (type, task_id, attempt) — which a gc has
// neither of. De-duplicating on that key drops every gc after the first, and a
// gc is re-issued precisely BECAUSE the server has not observed the last one.
func TestGCIsNotDroppedByTheCommandDedupe(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	one := makeLane(t, root, "sess-f", "lane-1")
	cmds := []contracts.Command{
		{Type: contracts.CmdGC, SessionID: "sess-f", Workdirs: []contracts.GCWorkdir{{ID: "wd-1", Path: one}}},
	}
	d.handleCommands(context.Background(), cmds)
	waitFor(t, 5*time.Second, func() bool { _, err := os.Stat(one); return os.IsNotExist(err) })

	two := makeLane(t, root, "sess-f", "lane-2")
	d.handleCommands(context.Background(), []contracts.Command{
		{Type: contracts.CmdGC, SessionID: "sess-f", Workdirs: []contracts.GCWorkdir{{ID: "wd-2", Path: two}}},
	})
	waitFor(t, 5*time.Second, func() bool { _, err := os.Stat(two); return os.IsNotExist(err) })
}
