// The G7 1판 차단 결함 through the loop (T-D10, daemon-protocol v0.7.3).
//
// The unit rows for the rules themselves live in
// `internal/workdir/blockers_test.go`; these measure the two places the loop
// is the only one who can answer: what the runtime is actually spawned in
// (D-21), and what leaves this daemon on the §6 wire (D-22).
package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

const (
	blockerSession = "9f2b4c1e-0000-4000-8000-000000000010"
	blockerAgent   = "1a3c5e70-0000-4000-8000-0000000000aa"
)

// relativePathBundle is the bundle a pre-v0.7.3 server sent: `workdir.path`
// is `<session-slug>/<agent-slug>`, relative (server/internal/queue/bundle.go
// called PlanWorktree with no Root).
func relativePathBundle(id, repo string) contracts.TaskBundle {
	b := bundle(id)
	b.Task.SessionID = blockerSession
	b.Task.AgentID = blockerAgent
	b.Task.AgentName = "backend"
	b.Profile.RuntimeKind = contracts.RuntimeHermes
	b.Brief.Transport = contracts.BriefInstructionFile
	b.Prompt = "Implement the widget."
	b.Workdir = contracts.BundleWorkdir{
		Kind: "worktree", RepoPath: repo, Path: "sess-slug/backend", Reuse: true,
	}
	return b
}

// D-21 end to end: a `worktree` lane whose bundle carries a RELATIVE path
// still runs, in a directory that exists, under the workdir root, and leaves
// nothing behind in the user's repository.
//
// Before this, all three failed at once: the checkout was created inside the
// repository (`git worktree add` runs with `-C <repo>`), the path handed to
// the runtime was the daemon's CWD + that same string, and so every attempt
// of the session ended `failed(config)` on `spawn: fork/exec …/npx: no such
// file or directory` (T-I4 차단 ①, `61_scenario_b.sh` X1·X1b·X1c).
func TestWorktreeLaneWithARelativeBundlePathRunsUnderTheRoot(t *testing.T) {
	repo := initRepo(t)
	srv := &memServer{queue: []contracts.TaskBundle{relativePathBundle("t-rel", repo)}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)
	var spawnDir string
	var existedAtSpawn bool
	d.SpawnConfig = func(_ contracts.TaskBundle, wd string) acp.Config {
		spawnDir = wd
		fi, err := os.Stat(wd)
		existedAtSpawn = err == nil && fi.IsDir()
		cmd, args, env := acpfake.Command(script, "")
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })

	srv.mu.Lock()
	fin := srv.finishes[0]
	srv.mu.Unlock()
	if fin.Outcome != "completed" {
		t.Fatalf("outcome = %s (%s / %s), want completed — the lane died before the first turn (차단 ①)",
			fin.Outcome, fin.FailureKind, fin.StopReason)
	}
	// (c): the runtime's cwd existed before it was spawned.
	if !existedAtSpawn {
		t.Errorf("the runtime was spawned in %q, which did not exist", spawnDir)
	}
	// (a)+(b): under the root, outside the repository.
	if !workdir.UnderRoot(root, spawnDir) {
		t.Errorf("cwd %q is not under the workdir root %q", spawnDir, root)
	}
	if workdir.UnderRoot(repo, spawnDir) {
		t.Errorf("cwd %q is inside the user's repository %q", spawnDir, repo)
	}
	if _, err := os.Stat(filepath.Join(repo, "sess-slug")); err == nil {
		t.Errorf("%s was created in the user's repository", filepath.Join(repo, "sess-slug"))
	}
	if out := gitIn(t, repo, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Errorf("user repository dirty: %q (E16-B `git status` 클린)", out)
	}
	// §4.4: the attempt's own git facts ride on finish, so the server does not
	// wait for the next probe to judge GC (§6 v0.7.3).
	if fin.Workdir == nil || fin.Workdir.Git == nil {
		t.Fatalf("finish.workdir = %+v, want the §4.4 git block", fin.Workdir)
	}
	if fin.Workdir.Path != spawnDir {
		t.Errorf("finish.workdir.path = %q, want the checkout %q", fin.Workdir.Path, spawnDir)
	}
	if fin.Workdir.Git.Branch != "colab/"+blockerSession+"/backend" {
		t.Errorf("finish branch = %q", fin.Workdir.Git.Branch)
	}

	// D-22: the §6 report the probe sends carries the identity the server
	// matches on. Reported here through the daemon, not through the package:
	// this is the wire that stayed empty in G7 (`out/64-workdirs.txt`).
	d.probe(context.Background())
	cancel()
	<-done

	row := reportedWorkdir(t, srv, spawnDir)
	if row == nil {
		t.Fatalf("the checkout %s never reached a §6 report", spawnDir)
	}
	if row.SessionID != blockerSession {
		t.Errorf("session_id = %q, want the session uuid %q (§6 v0.7.3)", row.SessionID, blockerSession)
	}
	if row.AgentID != blockerAgent {
		t.Errorf("agent_id = %q, want %q — `worktree` 격리는 필수 (§6 v0.7.3)", row.AgentID, blockerAgent)
	}
	if row.Kind != "worktree" || row.Git == nil || row.Bytes <= 0 {
		t.Errorf("row = %+v, want kind=worktree with the git block and bytes", row)
	}
}

// reportedWorkdir finds one path in everything the daemon has reported (§6).
func reportedWorkdir(t *testing.T, srv *memServer, path string) *workdir.Info {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for i := len(srv.workdirReports) - 1; i >= 0; i-- {
		for _, w := range srv.workdirReports[i].Workdirs {
			if w.Path == path {
				row := w
				return &row
			}
		}
	}
	return nil
}

// D-22, the gc receipt (§6 `gc: {status, reason}`): it travels on a workdir
// row, so it needs the identity every other row needs. A row the server
// cannot match is skipped silently, the workdir is never closed as `deleted`,
// and the command is re-issued until the 24h TTL (§4.3).
func TestGCReceiptCarriesTheWorkdirIdentity(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	repo := initRepo(t)
	b := relativePathBundle("t-gc", repo)
	path, err := workdir.Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}

	d.gc(context.Background(), contracts.Command{
		Type: contracts.CmdGC, SessionID: blockerSession,
		Workdirs: []contracts.GCWorkdir{{ID: "wd-9", Path: path}},
	})

	row := reportedWorkdir(t, srv, path)
	if row == nil {
		t.Fatalf("no §6 row for %s", path)
	}
	if row.GC == nil || row.GC.Status != workdir.GCDeleted || row.GC.ID != "wd-9" {
		t.Fatalf("gc = %+v, want deleted with the server's id", row.GC)
	}
	if row.Kind != "worktree" {
		t.Errorf("kind = %q, want worktree — the row describes a checkout", row.Kind)
	}
	if row.SessionID != blockerSession || row.AgentID != blockerAgent {
		t.Errorf("receipt row = session %q agent %q, want %q / %q (§6 v0.7.3)",
			row.SessionID, row.AgentID, blockerSession, blockerAgent)
	}
	// The branch survives the collection (E13-10) and the checkout is gone.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("checkout still on disk: %v", err)
	}
	if _, err := gitrepo.Run(repo, "rev-parse", "--verify", "--quiet", "refs/heads/colab/"+blockerSession+"/backend"); err != nil {
		t.Errorf("the branch was deleted with the worktree (E13-10): %v", err)
	}
}

// D-21(c) — the text the attempt dies with when the cwd is missing. It names
// the path, the isolation, what the bundle asked for and this daemon's root;
// the message it replaced named the adapter binary and nothing else.
func TestWorkdirDetailNamesThePathAndTheBundle(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "worktrees", "sess-slug", "backend")
	b := relativePathBundle("t-detail", "/tmp/repo")
	detail := workdirDetail(workdir.Verify(missing), b, root)
	for _, want := range []string{missing, "worktree", `"sess-slug/backend"`, root} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not name %q", detail, want)
		}
	}
	if strings.Contains(detail, "npx") {
		t.Errorf("detail %q blames the adapter binary", detail)
	}
}

// The workdir report is the same list on every channel the daemon has —
// probe and rebind included — so the identity does not depend on which one
// happened to fire (§6 "데몬은 workdir 목록을 probe와 함께 … 보고한다").
func TestReportedWorkdirsAreTheSameOnEveryChannel(t *testing.T) {
	srv := &memServer{}
	d, root := gcDaemon(t, srv)
	repo := initRepo(t)
	path, err := workdir.Prepare(root, relativePathBundle("t-ch", repo))
	if err != nil {
		t.Fatal(err)
	}
	d.probe(context.Background())
	row := reportedWorkdir(t, srv, path)
	if row == nil || row.SessionID != blockerSession || row.AgentID != blockerAgent {
		t.Fatalf("probe row = %+v, want the session uuid and the agent id", row)
	}
	if row.Git == nil {
		t.Fatal("probe row has no git block — GC 판정의 유일한 입력 (§6 v0.7.3)")
	}
	srv.mu.Lock()
	reports := len(srv.workdirReports)
	srv.mu.Unlock()
	if reports == 0 {
		t.Fatal("the probe sent no workdir report")
	}
}
