// The PR #172 review's two test gaps and its one code note (T-D10b).
//
//	NN5 — the "never inside the user's repository" guard, measured ALONE.
//	      `blockers_test.go` puts the repository in its own t.TempDir(), which
//	      is OUTSIDE the workdir root, so the root guard answers first and the
//	      repository guard is never the one under test: the reviewer's
//	      injection 2 (delete the repository guard) left every row green.
//	NN1 — `ResolvePath` has no CWD fallback left, so §4.1's one forbidden
//	      absolutisation is not in the code at all.
//
// Same rules as blockers_test.go: real git, throwaway repository, never this
// repository (P4_TASKS §0-18).
package workdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// repoUnderRoot is the fixture NN5 is about: the user's repository lives
// UNDER the daemon's workdir root (`<root>/repos/app` — a perfectly ordinary
// layout when the operator points both at the same disk). Every path inside
// it therefore passes the root guard, and the ONLY thing that can keep a
// checkout out of it is `WorktreeTarget`'s repository guard.
func repoUnderRoot(t *testing.T) (root, repo string) {
	t.Helper()
	root = t.TempDir()
	repo = initGitRepo(t, filepath.Join(root, "repos", "app"))
	return root, repo
}

// NN5 — the repository guard on its own, with the root guard disarmed by the
// fixture.
//
// Reviewer injection 2 (drop the `UnderRoot(repoTop, path)` branch from
// `WorktreeTarget`) has to fail this: without it the bundle path is honoured,
// `git worktree add` runs with `-C <repo>` and the checkout appears inside
// the user's repository — T-I4 차단 ①, measured as `…/repo/<session>/<agent>`.
func TestWorktreeRefusesACheckoutInsideARepositoryThatIsItselfUnderTheRoot(t *testing.T) {
	root, repo := repoUnderRoot(t)
	inside := filepath.Join(repo, "sess-slug", "backend")

	// The fixture, asserted: the root guard cannot be the one that answers.
	if !UnderRoot(root, inside) {
		t.Fatalf("fixture broken: %q must be under the workdir root %q, or the root guard "+
			"is what this test measures (NN5)", inside, root)
	}
	if !UnderRoot(repo, inside) {
		t.Fatalf("fixture broken: %q must be inside the repository %q", inside, repo)
	}

	got, err := PrepareWorktree(root, worktreeBundle(t, repo, inside))
	if err != nil {
		t.Fatalf("PrepareWorktree: %v", err)
	}
	if UnderRoot(repo, got) {
		t.Fatalf("checkout %q is INSIDE the user's repository %q — the repository guard "+
			"is the only thing standing here (§4.1 v0.7.3 데몬 방어, T-I4 차단 ①)", got, repo)
	}
	if !UnderRoot(root, got) {
		t.Errorf("checkout %q left the workdir root %q", got, root)
	}
	if want := WorktreePath(root, "9f2b4c1e-0000-4000-8000-000000000001", "backend"); got != want {
		t.Errorf("checkout = %q, want the daemon's own plan %q", got, want)
	}
	if !gitrepo.IsWorktreeCheckout(got) {
		t.Errorf("%q is not a git worktree checkout", got)
	}
	// The repository is exactly as it was: nothing new on disk, nothing in
	// `git status` (E16-B `git status` 클린).
	if entries, err := os.ReadDir(filepath.Join(repo, "sess-slug")); err == nil {
		t.Errorf("%s exists in the user's repository (%d entries)", filepath.Join(repo, "sess-slug"), len(entries))
	}
	if out, err := gitrepo.Run(repo, "status", "--porcelain"); err != nil || strings.TrimSpace(out) != "" {
		t.Errorf("user repository dirty after preparation: %q (%v)", out, err)
	}
}

// The same guard at the function that owns it, with the root guard removed
// from the question entirely — every path below is under the root.
func TestWorktreeTargetRelocatesOnlyWhatTheContractForbids(t *testing.T) {
	root, repo := repoUnderRoot(t)
	top, err := gitrepo.TopLevel(repo)
	if err != nil {
		t.Fatal(err)
	}
	plan := PlanWorktree(WorktreeRequest{Root: root, RepoPath: top, SessionSlug: "s", AgentSlug: "backend"})

	for _, tc := range []struct {
		name       string
		bundlePath string
		want       string
	}{
		{"inside the repository, still under the root", filepath.Join(repo, "wt"), plan.Path},
		{"the repository's own top level", repo, plan.Path},
		{"beside the repository, under the root", filepath.Join(root, "repos", "wt"), filepath.Join(root, "repos", "wt")},
		{"the server's own layout", filepath.Join(root, "sessions", "s", "backend"), filepath.Join(root, "sessions", "s", "backend")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !UnderRoot(root, tc.bundlePath) && tc.bundlePath != repo {
				t.Fatalf("fixture broken: %q is not under the root", tc.bundlePath)
			}
			if got := WorktreeTarget(root, tc.bundlePath, plan, top); got != tc.want {
				t.Errorf("WorktreeTarget(%q) = %q, want %q", tc.bundlePath, got, tc.want)
			}
		})
	}
}

// NN1 — a relative path with no root resolves to NOTHING, never to the
// daemon's CWD. §4.1 forbids that absolutisation; the branch that did it is
// gone, so it cannot come back through a config the operator got wrong.
func TestResolvePathHasNoCWDFallback(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := ResolvePath("", "sess-slug/backend"); got != "" {
		t.Errorf("ResolvePath(no root, relative) = %q, want \"\" — the daemon's CWD "+
			"(%s) is not a workdir (§4.1 v0.7.3, NN1)", got, cwd)
	}
	// An absolute path needs no root and is still the server's own.
	abs := filepath.Join(t.TempDir(), "wt")
	if got := ResolvePath("", abs); got != abs {
		t.Errorf("ResolvePath(no root, absolute) = %q, want %q", got, abs)
	}
	// And the empty answer dies with a message about the missing directory,
	// not about the adapter binary.
	if err := Verify(ResolvePath("", "sess-slug/backend")); err == nil {
		t.Fatal("an unresolvable workdir must not reach the spawn")
	} else if !strings.Contains(err.Error(), "no directory to run in") {
		t.Errorf("Verify = %v, want the \"no directory to run in\" refusal", err)
	}
	// Through `Prepare`, the whole way: refused, and nothing created in the CWD.
	//
	// The probe directory is named for this test alone. A generic
	// `sess-slug/` would also match the litter the ORIGINAL bug left in this
	// package's directory (found while writing this: the CWD fallback had
	// created `daemon/internal/{loop,workdir}/sess-slug`, both empty and both
	// invisible to `git status` because git does not track empty directories).
	const probe = "colab-nn1-cwd-probe"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(cwd, probe)) })
	b := contracts.TaskBundle{
		Task:    contracts.BundleTask{SessionID: "s-1", LaneID: "l-1"},
		Workdir: contracts.BundleWorkdir{Kind: "dir", Path: probe + "/backend"},
	}
	if p, err := Prepare("", b); err == nil {
		t.Errorf("Prepare(no root) = %q, want a refusal", p)
	}
	if _, err := os.Stat(filepath.Join(cwd, probe)); err == nil {
		t.Errorf("the daemon created %s — its CWD is not a workdir", filepath.Join(cwd, probe))
	}
}
