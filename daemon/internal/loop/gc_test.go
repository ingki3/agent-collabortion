// The §4.3 `gc` command and its §6 report.
//
// Before this the daemon logged "command gc ignored (P4)" and did nothing —
// the server re-issued the command forever, the disk kept filling, and the
// only person who could act never saw it.
package loop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

// initRepo makes a throwaway repository with one commit on `main`. The
// experiment target is NEVER this repository (P4_TASKS §0-18).
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "daemon@test")
	gitIn(t, dir, "config", "user.name", "daemon test")
	writeFile(t, filepath.Join(dir, "README.md"), "seed\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "init")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitrepo.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

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

// CHANGED IN P4 (was TestGCRefusesAWorktreeAndSaysSo). Until T-D9 a `gc`
// naming a checkout was answered `refused(isolation_worktree_p4)` because the
// daemon had no collector. It has one now (§6, E13-10): `git worktree remove`
// only, and the branch survives.
func TestGCRemovesAWorktreeAndKeepsTheBranch(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	repo := initRepo(t)
	wt := filepath.Join(root, "worktrees", "sess-d", "backend")
	if err := gitrepo.WorktreeAdd(repo, wt, "colab/sess-d/backend", "main"); err != nil {
		t.Fatal(err)
	}
	// A commit on the branch, so "the branch survived" is observable.
	writeFile(t, filepath.Join(wt, "work.txt"), "done")
	gitIn(t, wt, "add", "-A")
	gitIn(t, wt, "commit", "-m", "work")

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-d",
		Workdirs: []contracts.GCWorkdir{{ID: "wd-w", Path: wt}},
	})

	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("the checkout survived `git worktree remove`: %v", err)
	}
	if out, err := gitrepo.Run(repo, "rev-parse", "--verify", "refs/heads/colab/sess-d/backend"); err != nil || out == "" {
		t.Fatalf("the branch was deleted with the worktree (§6 '브랜치는 남긴다', E13-10): %v", err)
	}
	row := gcRow(t, srv, wt)
	if row == nil || row.Status != workdir.GCDeleted {
		t.Fatalf("gc row = %+v, want deleted", row)
	}
}

// git itself refuses a checkout with modified or untracked files, and that
// refusal is the answer the server asked for — the daemon does not reach for
// --force (E13-13 is the same case the server judges).
func TestGCReportsGitsRefusalForADirtyWorktree(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	repo := initRepo(t)
	wt := filepath.Join(root, "worktrees", "sess-g", "frontend")
	if err := gitrepo.WorktreeAdd(repo, wt, "colab/sess-g/frontend", "main"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(wt, "uncommitted.txt"), "diff only, never committed")

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-g",
		Workdirs: []contracts.GCWorkdir{{ID: "wd-d", Path: wt}},
	})

	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a dirty checkout was deleted anyway: %v", err)
	}
	row := gcRow(t, srv, wt)
	if row == nil || row.Status != workdir.GCRefused || row.Reason != workdir.GCReasonWorktreeRemove {
		t.Fatalf("gc row = %+v, want refused(%s)", row, workdir.GCReasonWorktreeRemove)
	}
}

// §6 잠금·프로세스 잔존: a live process group recorded against the checkout
// refuses the collection before git is even asked. Deleting a directory a
// runtime is writing in produces half-written files and an index lock nobody
// owns, and the server re-issues the command anyway.
func TestGCRefusesAWorkdirAProcessStillHolds(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	p := makeLane(t, root, "sess-h", "lane-1")
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL); _, _ = cmd.Process.Wait() })
	if err := d.Orphans.Record(orphan.Record{TaskID: "t-h", Attempt: 1, PGID: pgid, StartedAt: time.Now(), Workdir: p}); err != nil {
		t.Fatal(err)
	}

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: "sess-h",
		Workdirs: []contracts.GCWorkdir{{ID: "wd-p", Path: p}},
	})

	if _, err := os.Stat(p); err != nil {
		t.Fatalf("a workdir a live process holds was deleted: %v", err)
	}
	row := gcRow(t, srv, p)
	if row == nil || row.Status != workdir.GCRefused || row.Reason != workdir.GCReasonProcessAlive {
		t.Fatalf("gc row = %+v, want refused(%s)", row, workdir.GCReasonProcessAlive)
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

// §4.3 `rebind_prepare {session_id, artifacts[]}`: download in submission
// order, into the workdir root — never into a checkout — and do NOT apply.
// Applying is the prompt's job (FR-9.2, E14-06): a `git apply` the daemon
// runs can conflict, and the daemon has nobody to ask.
func TestRebindPrepareDownloadsInOrderAndDoesNotApply(t *testing.T) {
	srv := &memServer{downloadBody: map[string]string{
		"/v1/artifacts/a1/content": "diff-one",
		"/v1/artifacts/a2/content": "diff-two",
	}}
	d, root := gcDaemon(t, srv)

	d.handleCommands(context.Background(), []contracts.Command{{
		Type: contracts.CmdRebindPrepare, SessionID: "sess-r",
		// Deliberately out of order on the wire: `order` is the truth.
		Artifacts: []contracts.ArtifactRef{
			{ID: "a2", Order: 2, URL: "/v1/artifacts/a2/content"},
			{ID: "a1", Order: 1, URL: "/v1/artifacts/a1/content"},
		},
	}})

	dir := RebindDir(root, "sess-r")
	waitFor(t, 5*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "manifest.json")); return err == nil })

	srv.mu.Lock()
	got := append([]string(nil), srv.downloads...)
	srv.mu.Unlock()
	want := []string{"/v1/artifacts/a1/content", "/v1/artifacts/a2/content"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("downloads = %v, want submission order %v (E14-06 '아티팩트 목록 순서 = 제출 순서')", got, want)
	}
	for name, body := range map[string]string{"001-a1": "diff-one", "002-a2": "diff-two"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(b) != body {
			t.Errorf("%s = %q (%v), want %q", name, b, err, body)
		}
	}
	// Not inside any checkout: a diff file in the working tree lands in the
	// very commit it is meant to produce (§8.4 M6's class of bug).
	if !strings.HasPrefix(dir, filepath.Join(root, ".colab")) {
		t.Errorf("rebind dir %q is not under <workdir_root>/.colab", dir)
	}
	var m RebindManifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 2 || m.Artifacts[0].ID != "a1" || m.Artifacts[0].Error != "" {
		t.Fatalf("manifest = %+v", m)
	}
}

// One artifact that will not download does not cancel the rest: the agent
// applies what it has and the manifest says what is missing. A silent partial
// set is worse than a loud one.
func TestRebindPrepareReportsAFailedArtifact(t *testing.T) {
	srv := &memServer{
		downloadBody: map[string]string{"/ok": "fine"},
		downloadErr:  map[string]error{"/gone": errors.New("404 not_found")},
	}
	d, root := gcDaemon(t, srv)

	d.rebindPrepare(context.Background(), contracts.Command{
		Type: contracts.CmdRebindPrepare, SessionID: "sess-p",
		Artifacts: []contracts.ArtifactRef{
			{ID: "g", Order: 1, URL: "/gone"},
			{ID: "o", Order: 2, URL: "/ok"},
		},
	})

	b, err := os.ReadFile(filepath.Join(RebindDir(root, "sess-p"), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m RebindManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Artifacts) != 2 {
		t.Fatalf("manifest = %+v", m)
	}
	if m.Artifacts[0].Error == "" {
		t.Error("the failed artifact is not recorded — the agent would apply an incomplete set " +
			"and nobody would know which piece is missing")
	}
	if m.Artifacts[1].Path == "" || m.Artifacts[1].Error != "" {
		t.Errorf("the second artifact was skipped after the first failed: %+v", m.Artifacts[1])
	}
}
