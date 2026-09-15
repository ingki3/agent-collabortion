package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

func repo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "d@t"},
		{"config", "user.name", "d"},
	} {
		if _, err := gitrepo.Run(dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	if remote != "" {
		if _, err := gitrepo.Run(dir, "remote", "add", "origin", remote); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
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

// daemon-protocol §3: `repos[].remote_url` is what decides a rebinding
// candidate (E14-04·05). A machine that reports no repositories can never be
// one, so the probe has to carry them.
func TestReposReportsRemoteBranchAndClean(t *testing.T) {
	clean := repo(t, "git@x:app.git")
	dirty := repo(t, "")
	if err := os.WriteFile(filepath.Join(dirty, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Repos(Options{Repos: []string{clean, dirty, "/nope/not/a/repo", ""}})
	if len(got) != 2 {
		t.Fatalf("repos = %+v, want 2 (the non-repository is dropped, not reported empty)", got)
	}
	byPath := map[string]contracts.Repo{}
	for _, r := range got {
		byPath[r.Path] = r
	}
	c, err := gitrepo.TopLevel(clean)
	if err != nil {
		t.Fatal(err)
	}
	if r := byPath[c]; r.RemoteURL != "git@x:app.git" || r.Branch != "main" || !r.Clean {
		t.Errorf("clean repo = %+v, want remote git@x:app.git, branch main, clean", r)
	}
	d, err := gitrepo.TopLevel(dirty)
	if err != nil {
		t.Fatal(err)
	}
	if r := byPath[d]; r.RemoteURL != "" || r.Clean {
		t.Errorf("dirty repo = %+v, want no remote and clean=false", r)
	}
}

// A repository the daemon only learned about from a bundle's `repo_path`
// still has to be reported, or the machine currently RUNNING a worktree
// session would not be a candidate to rebind that session back to.
func TestReposFindsRepositoriesBehindWorktreeWorkdirs(t *testing.T) {
	src := repo(t, "git@x:app.git")
	root := t.TempDir()
	wt := workdir.WorktreePath(root, "S", "backend")
	if err := gitrepo.WorktreeAdd(src, wt, "colab/S/backend", "main"); err != nil {
		t.Fatal(err)
	}

	got := Repos(Options{WorkdirRoot: root})
	if len(got) != 1 {
		t.Fatalf("repos = %+v, want the repository behind the worktree", got)
	}
	top, err := gitrepo.TopLevel(src)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Path != top {
		t.Errorf("path = %q, want the MAIN repository %q — a `git worktree add` runs against "+
			"that, not against a linked checkout", got[0].Path, top)
	}
	if got[0].RemoteURL != "git@x:app.git" {
		t.Errorf("remote = %q", got[0].RemoteURL)
	}
	// Configuring the same repository as well must not double it.
	if again := Repos(Options{WorkdirRoot: root, Repos: []string{src}}); len(again) != 1 {
		t.Errorf("repos = %+v, want 1 after de-duplication", again)
	}
}
