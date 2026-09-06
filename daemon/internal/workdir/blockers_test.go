// The G7 1판 차단 결함, daemon half (T-D10, daemon-protocol v0.7.3).
//
//	D-21 — where a `worktree` lane checks out, and what the daemon says when
//	       the runtime's cwd is not there (§4.1 데몬 방어)
//	D-22 — what a §6 workdir report has to carry for the server to store it
//
// Every row here runs the real git against a throwaway repository under
// t.TempDir(); the experiment target is never this repository (P4_TASKS §0-18).
package workdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// tempGitRepo is a one-commit repository on `main`. (The p4golden mirror has
// its own `newGitRepo`; this file is untagged, so it cannot borrow it.)
func tempGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "daemon@test"},
		{"config", "user.name", "daemon test"},
	} {
		if _, err := gitrepo.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(dir, "commit", "-qm", "init"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func worktreeBundle(t *testing.T, repo, path string) contracts.TaskBundle {
	t.Helper()
	return contracts.TaskBundle{
		Task: contracts.BundleTask{
			SessionID: "9f2b4c1e-0000-4000-8000-000000000001",
			AgentID:   "1a3c5e70-0000-4000-8000-0000000000a1",
			AgentName: "backend",
			LaneID:    "5b6d8f90-0000-4000-8000-0000000000b1",
		},
		Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo, Path: path, Reuse: true},
	}
}

// D-21(a) — a relative `workdir.path` is the workdir ROOT's, not the CWD's.
//
// The daemon used to hand the runtime `filepath.Abs(<relative>)`, which is
// resolved against whatever directory the operator launched the daemon from.
// Nothing is ever there, so every attempt died on the adapter's spawn.
func TestRelativeBundlePathResolvesAgainstTheWorkdirRoot(t *testing.T) {
	root := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	got := ResolvePath(root, "sess-slug/backend")
	if want := filepath.Join(root, "sess-slug", "backend"); got != want {
		t.Errorf("ResolvePath = %q, want %q (§4.1 v0.7.3 \"path 가 상대면 <workdir_root> 기준\")", got, want)
	}
	if strings.HasPrefix(got, filepath.Clean(cwd)+string(filepath.Separator)) {
		t.Errorf("ResolvePath = %q — resolved against the daemon's CWD %q (T-I4 차단 ①)", got, cwd)
	}
	// An absolute path is the server's own and is left exactly as sent.
	abs := filepath.Join(root, "elsewhere", "backend")
	if got := ResolvePath(root, abs); got != abs {
		t.Errorf("ResolvePath(absolute) = %q, want %q — the server owns the path (§4.1)", got, abs)
	}

	// The same rule through the `dir` isolation the P1 daemon has always had.
	b := contracts.TaskBundle{
		Task:    contracts.BundleTask{SessionID: "s-1", LaneID: "l-1"},
		Workdir: contracts.BundleWorkdir{Kind: "dir", Path: "sessions/s-1/l-1"},
	}
	p, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "sessions", "s-1", "l-1"); p != want {
		t.Fatalf("Prepare = %q, want %q", p, want)
	}
	if _, err := os.Stat(filepath.Join(cwd, "sessions")); err == nil {
		t.Fatalf("the daemon created %s — its CWD is not a workdir", filepath.Join(cwd, "sessions"))
	}
}

// D-21(b) — `git worktree add` never checks out inside the user's repository.
//
// `git worktree add` runs with `-C <repo>`, so the relative path the server
// used to send made the checkout at `<repo>/<session>/<agent>`: an untracked
// directory in the user's `git status`, unreachable by GC (§6), and holding
// the orphan records and the CLI wrapper of FR-9.1 / harness §10.
func TestWorktreeNeverChecksOutInsideTheUserRepository(t *testing.T) {
	for _, tc := range []struct {
		name       string
		bundlePath func(root, repo string) string
	}{
		{"relative path from a pre-v0.7.3 server", func(_, _ string) string { return "sess-slug/backend" }},
		{"absolute path pointing into the repository", func(_, repo string) string {
			return filepath.Join(repo, "sess-slug", "backend")
		}},
		{"absolute path outside the workdir root", func(_, _ string) string {
			return filepath.Join(t.TempDir(), "somewhere-else")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, repo := t.TempDir(), tempGitRepo(t)
			got, err := PrepareWorktree(root, worktreeBundle(t, repo, tc.bundlePath(root, repo)))
			if err != nil {
				t.Fatalf("PrepareWorktree: %v", err)
			}
			if !UnderRoot(root, got) {
				t.Errorf("checkout %q is not under the workdir root %q (§4.1 v0.7.3 데몬 방어)", got, root)
			}
			if UnderRoot(repo, got) {
				t.Errorf("checkout %q is INSIDE the user's repository %q (T-I4 차단 ①)", got, repo)
			}
			if !gitrepo.IsWorktreeCheckout(got) {
				t.Errorf("%q is not a git worktree checkout", got)
			}
			// The user's repository is untouched: no new directory, and
			// `git status` stays clean (E16-B `git status` 클린).
			if entries, err := os.ReadDir(filepath.Join(repo, "sess-slug")); err == nil {
				t.Errorf("%s exists in the user's repository (%d entries)", filepath.Join(repo, "sess-slug"), len(entries))
			}
			if out, err := gitrepo.Run(repo, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
				t.Errorf("user repository dirty after preparation: %q (%v)", out, err)
			}
		})
	}
}

// A path the server chose UNDER the root is honoured as sent — relocating is
// a floor, not a preference (§4.1: the server owns the path so it can judge
// E13-08 and address the workdir in a `gc` command).
func TestWorktreeHonoursAnAbsoluteBundlePathUnderTheRoot(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	want := filepath.Join(root, "sess-slug", "backend")
	got, err := PrepareWorktree(root, worktreeBundle(t, repo, want))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("checkout = %q, want the bundle's own %q", got, want)
	}
}

// D-21(c) — the runtime's cwd is checked BEFORE the spawn, and the error says
// which path is missing.
func TestVerifyNamesTheMissingDirectory(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "worktrees", "sess-slug", "backend")
	err := Verify(missing)
	if err == nil {
		t.Fatal("a workdir that does not exist must not reach the spawn (§4.1 v0.7.3 데몬 방어)")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error %q does not name the path %q — the message it replaces "+
			"(`spawn: fork/exec …/npx: no such file or directory`) hid the cause", err, missing)
	}
	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(file); err == nil || !strings.Contains(err.Error(), file) {
		t.Errorf("Verify(file) = %v, want a refusal naming %q", err, file)
	}
	if err := Verify(root); err != nil {
		t.Errorf("Verify(existing dir) = %v, want nil", err)
	}
}

// D-22 — the §6 report row the server can actually store.
//
// The server matches the row by (session uuid, agent uuid) and skips it
// silently when either is missing; then `git` and `bytes` never arrive, GC
// reads "0 commits · clean" and deletes unmerged work (FR-6.4 M4).
func TestWorkdirReportCarriesSessionUUIDAgentIDGitAndBytes(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	b := worktreeBundle(t, repo, "sess-slug/backend")
	path, err := PrepareWorktree(root, b)
	if err != nil {
		t.Fatal(err)
	}
	// One unmerged commit and one uncommitted file — the two facts GC judges.
	if err := os.WriteFile(filepath.Join(path, "feature.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(path, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(path, "commit", "-qm", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "scratch.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rows := ListWorktrees(root)
	if len(rows) != 1 {
		t.Fatalf("ListWorktrees = %+v, want exactly the one checkout", rows)
	}
	w := rows[0]
	if w.SessionID != b.Task.SessionID {
		t.Errorf("session_id = %q, want the session UUID %q (§6 v0.7.3 \"슬러그·디렉터리 이름이 아니다\")", w.SessionID, b.Task.SessionID)
	}
	if w.AgentID != b.Task.AgentID {
		t.Errorf("agent_id = %q, want %q — `worktree` 격리는 agent_id 필수 (§6 v0.7.3)", w.AgentID, b.Task.AgentID)
	}
	if w.Kind != "worktree" || w.Path != path {
		t.Errorf("row = %+v, want kind=worktree path=%s", w, path)
	}
	if w.Bytes <= 0 {
		t.Errorf("bytes = %d — S13 용량과 쿼터 분자가 0 이 된다 (T-I4 차단 ②)", w.Bytes)
	}
	if w.Git == nil {
		t.Fatal("git block missing — GC 판정의 유일한 입력 (§6 v0.7.3)")
	}
	if w.Git.Branch != "colab/9f2b4c1e-0000-4000-8000-000000000001/backend" {
		t.Errorf("branch = %q", w.Git.Branch)
	}
	if w.Git.Merged || w.Git.CommitsAhead != 1 || !w.Git.Dirty {
		t.Errorf("git = %+v, want unmerged · 1 ahead · dirty — the facts that stop GC (E13-12·13)", *w.Git)
	}

	// The whole report (probe path) carries the same row.
	all, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range all {
		if r.Path == path {
			found = true
			if r.AgentID != b.Task.AgentID || r.Git == nil {
				t.Errorf("List row = %+v, want the identity and the git block", r)
			}
		}
	}
	if !found {
		t.Errorf("List(%s) does not contain the checkout %s", root, path)
	}
}

// A `dir` workdir carries the session uuid and the LANE (§6 lane_id), which
// is its identity — the agent's is the worktree's.
func TestDirWorkdirReportCarriesSessionAndLaneIDs(t *testing.T) {
	root := t.TempDir()
	b := contracts.TaskBundle{
		Task: contracts.BundleTask{
			SessionID: "9f2b4c1e-0000-4000-8000-000000000002",
			AgentID:   "1a3c5e70-0000-4000-8000-0000000000a2",
			LaneID:    "5b6d8f90-0000-4000-8000-0000000000b2",
		},
		Workdir: contracts.BundleWorkdir{Kind: "dir"},
	}
	p, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := List(root)
	if err != nil || len(rows) != 1 {
		t.Fatalf("List = %+v (%v)", rows, err)
	}
	if rows[0].SessionID != b.Task.SessionID || rows[0].LaneID != b.Task.LaneID {
		t.Errorf("row = %+v, want session=%s lane=%s", rows[0], b.Task.SessionID, b.Task.LaneID)
	}
	if rows[0].Path != p {
		t.Errorf("path = %q, want %q", rows[0].Path, p)
	}
}

// D-22, the gc receipt: it travels on a §6 row and therefore needs the same
// identity. `Describe` is what the loop builds that row with.
func TestDescribeFillsAGCReceiptRow(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	b := worktreeBundle(t, repo, "sess-slug/backend")
	path, err := PrepareWorktree(root, b)
	if err != nil {
		t.Fatal(err)
	}
	row := Describe(root, path, b.Task.SessionID)
	if row.Kind != "worktree" {
		t.Errorf("kind = %q, want worktree — the server stores the row it can match", row.Kind)
	}
	if row.SessionID != b.Task.SessionID || row.AgentID != b.Task.AgentID {
		t.Errorf("receipt row = %+v, want session=%s agent=%s (§6 v0.7.3)", row, b.Task.SessionID, b.Task.AgentID)
	}
	if row.Git == nil || row.Bytes <= 0 {
		t.Errorf("receipt row = %+v, want the git block and bytes", row)
	}
	// A collected workdir stops being reported, and its record goes with it.
	ForgetWorkdir(root, path)
	if _, ok := LookupWorkdir(root, path); ok {
		t.Error("the index record survived collection")
	}
}

// A checkout an older daemon left behind has no record. It still has to be
// reported — S13 cannot show, and GC cannot reach, a directory nobody names —
// so the directory name stays the fallback identity it always was.
func TestUnrecordedCheckoutIsStillReported(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	path := filepath.Join(root, "worktrees", "old-session", "backend")
	if err := gitrepo.WorktreeAdd(repo, path, "colab/old-session/backend", "main"); err != nil {
		t.Fatal(err)
	}
	rows := ListWorktrees(root)
	if len(rows) != 1 || rows[0].SessionID != "old-session" || rows[0].AgentID != "" {
		t.Fatalf("rows = %+v, want the directory-name fallback", rows)
	}
	if rows[0].Git == nil {
		t.Error("git block missing on the fallback row")
	}
}
