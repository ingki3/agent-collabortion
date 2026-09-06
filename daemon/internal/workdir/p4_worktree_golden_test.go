//go:build p4golden

// P4 golden MIRROR — worktree preparation (E13-02, FR-6.4).
//
// The table of record is `server/internal/workdirs/gc_golden_test.go`
// `TestWorktreePreparationGolden` (lines 196-250 as of PR #154). Its
// `planWorktree` hook stays NOT WIRED there for the same reason
// `planBriefFile` does: the plan is daemon behaviour and `server/…` cannot
// import `daemon/internal/…` (Lead decision on PR #152, option (a); confirmed
// for planWorktree 2026-09-07).
//
// Case names and failure messages are copied verbatim so the two tables can
// be diffed mechanically. `worktreeRequest`/`worktreePlan` gain a `Root`
// here — the server-side shape has no notion of a machine's workdir root,
// because over there the plan is a description and here it is a path on this
// disk.
//
// The rows below run the real `git worktree add` against a throwaway
// repository. The experiment target is never this repository (P4_TASKS §0-18).
package workdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

func planWorktreeGolden(t *testing.T, root, repo, sessionSlug, agentSlug, existingForAgent string) WorktreePlan {
	t.Helper()
	if existingForAgent != "" {
		// The agent already has a checkout: make it, exactly as the previous
		// lane would have.
		if err := gitrepo.WorktreeAdd(repo, existingForAgent, WorktreeBranch(sessionSlug, agentSlug), "main"); err != nil {
			t.Fatal(err)
		}
	}
	return PlanWorktree(WorktreeRequest{Root: root, RepoPath: repo, SessionSlug: sessionSlug, AgentSlug: agentSlug})
}

func TestWorktreePreparationGoldenMirror(t *testing.T) {
	t.Run("E13-02_the_branch_is_colab_session_agent", func(t *testing.T) {
		root, repo := t.TempDir(), newGitRepo(t)
		p := planWorktreeGolden(t, root, repo, "S", "backend", "")

		if p.Branch != "colab/S/backend" {
			t.Errorf("branch = %q, want %q (FR-6.4 `colab/<session>/<agent>`, E16-B 2단계 "+
				"names `colab/S/backend` literally)", p.Branch, "colab/S/backend")
		}
		if !p.Created {
			t.Error("the first lane of an agent creates its worktree")
		}
		if p.Path == "" {
			t.Error("the plan must name the path the daemon checks out into")
		}
	})

	t.Run("E13-02_a_second_lane_of_the_same_agent_reuses_the_same_worktree", func(t *testing.T) {
		root, repo := t.TempDir(), newGitRepo(t)
		existing := WorktreePath(root, "S", "backend")
		p := planWorktreeGolden(t, root, repo, "S", "backend", existing)
		if p.Created {
			t.Error("a second worktree for the same agent — FR-6.4/C3 bind ONE workdir per agent " +
				"under `worktree`, which is also why those lanes run sequentially (FR-6.3). " +
				"E16-B's verdict line counts 워크트리 2개, not 4")
		}
		if p.Path != existing {
			t.Errorf("path = %q, want the existing %s", p.Path, existing)
		}
	})

	t.Run("E13-02_different_agents_get_different_worktrees", func(t *testing.T) {
		root, repo := t.TempDir(), newGitRepo(t)
		be := planWorktreeGolden(t, root, repo, "S", "backend", "")
		fe := planWorktreeGolden(t, root, repo, "S", "frontend", "")
		if be.Path == fe.Path || be.Branch == fe.Branch {
			t.Errorf("backend %s@%s and frontend %s@%s collide — parallel lanes would write the "+
				"same checkout (E16-B 2단계)", be.Path, be.Branch, fe.Path, fe.Branch)
		}
	})
}

// The plan is a description; PrepareWorktree is the thing that runs. E16-B
// step 2's verdict line — 워크트리 2개, not 4 — is counted on disk here.
func TestPrepareWorktreeCreatesTwoCheckoutsForTwoAgents(t *testing.T) {
	root, repo := t.TempDir(), newGitRepo(t)
	bundle := func(agent, lane string) contracts.TaskBundle {
		return contracts.TaskBundle{
			Task:    contracts.BundleTask{SessionID: "S", AgentName: agent, LaneID: lane},
			Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo},
		}
	}
	be1, err := Prepare(root, bundle("backend", "l1"))
	if err != nil {
		t.Fatal(err)
	}
	fe1, err := Prepare(root, bundle("frontend", "l2"))
	if err != nil {
		t.Fatal(err)
	}
	// The re-entrant lane of E16-B step 5: same agent, same lane, resumed.
	be2, err := Prepare(root, bundle("backend", "l1"))
	if err != nil {
		t.Fatal(err)
	}
	// And a SECOND lane of the same agent (delegation): still one checkout.
	be3, err := Prepare(root, bundle("backend", "l3"))
	if err != nil {
		t.Fatal(err)
	}
	if be1 != be2 || be1 != be3 {
		t.Errorf("backend got %q, %q, %q — FR-6.4/C3 binds ONE workdir per agent", be1, be2, be3)
	}
	if be1 == fe1 {
		t.Error("backend and frontend share a checkout")
	}
	wts, err := gitrepo.Worktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	// main + backend + frontend
	if len(wts) != 3 {
		t.Errorf("git worktree list = %d entries (%+v), want 3 (main + 2 agents) — E16-B's "+
			"verdict line is 워크트리 2개, not 4", len(wts), wts)
	}
	for _, want := range []string{"colab/S/backend", "colab/S/frontend"} {
		if _, err := gitrepo.Run(repo, "rev-parse", "--verify", "refs/heads/"+want); err != nil {
			t.Errorf("branch %s missing: %v", want, err)
		}
	}
	if !gitrepo.IsWorktreeCheckout(be1) {
		t.Errorf("%s is not a linked worktree", be1)
	}
	if !gitrepo.Clean(be1) {
		t.Errorf("a fresh checkout is dirty")
	}
}

// E13-01: a repo_path that is not a git working tree fails with a reason a
// human can act on, not a bare git error. The wizard blocks this at session
// creation, but a repository can be moved between then and the first task.
func TestPrepareWorktreeRefusesAMissingRepository(t *testing.T) {
	root := t.TempDir()
	for _, repo := range []string{"", filepath.Join(t.TempDir(), "gone"), t.TempDir()} {
		_, err := Prepare(root, contracts.TaskBundle{
			Task:    contracts.BundleTask{SessionID: "S", AgentName: "backend"},
			Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo},
		})
		if err == nil {
			t.Errorf("repo_path %q was accepted", repo)
		}
	}
}

// §6 / E13-10 measured on disk: the checkout goes, the branch and its commits
// stay. GC exists so a Director can still open the branch two weeks later.
func TestRemoveWorktreeKeepsTheBranch(t *testing.T) {
	root, repo := t.TempDir(), newGitRepo(t)
	wt, err := Prepare(root, contracts.TaskBundle{
		Task:    contracts.BundleTask{SessionID: "S", AgentName: "backend"},
		Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "feature.go"), []byte("package feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(wt, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(wt, "commit", "-qm", "feat"); err != nil {
		t.Fatal(err)
	}
	head, err := gitrepo.Run(wt, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if reason, err := RemoveWorktree(wt); err != nil {
		t.Fatalf("RemoveWorktree: %s %v", reason, err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("the checkout survived")
	}
	got, err := gitrepo.Run(repo, "rev-parse", "refs/heads/colab/S/backend")
	if err != nil || got != head {
		t.Fatalf("branch tip = %q (%v), want %q — §6 '브랜치는 남긴다'", got, err, head)
	}
}

// No --force. git refuses a checkout with modified or untracked files and the
// daemon reports that refusal (§6): it executes the server's decision, it does
// not overrule it. E13-13 (diff submitted, never committed) is this case.
func TestRemoveWorktreeRefusesUncommittedWork(t *testing.T) {
	root, repo := t.TempDir(), newGitRepo(t)
	wt, err := Prepare(root, contracts.TaskBundle{
		Task:    contracts.BundleTask{SessionID: "S", AgentName: "frontend"},
		Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("never committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reason, err := RemoveWorktree(wt)
	if err == nil {
		t.Fatal("a checkout with uncommitted work was deleted")
	}
	if reason != GCReasonWorktreeRemove {
		t.Errorf("reason = %q, want %q", reason, GCReasonWorktreeRemove)
	}
	if _, statErr := os.Stat(filepath.Join(wt, "wip.txt")); statErr != nil {
		t.Errorf("the uncommitted file is gone: %v", statErr)
	}
}

// The §6 report's `git` block is what the SERVER judges 병합·클린 from
// (E13-10~13). The daemon states; it does not decide.
func TestGitInfoReportsMergedAndDirty(t *testing.T) {
	root, repo := t.TempDir(), newGitRepo(t)
	wt, err := Prepare(root, contracts.TaskBundle{
		Task:    contracts.BundleTask{SessionID: "S", AgentName: "backend"},
		Workdir: contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo},
	})
	if err != nil {
		t.Fatal(err)
	}
	g := Git(wt)
	if g == nil || g.Branch != "colab/S/backend" || !g.Merged || g.Dirty || g.CommitsAhead != 0 {
		t.Fatalf("fresh checkout git = %+v, want branch colab/S/backend, merged, clean, 0 ahead", g)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if g := Git(wt); g == nil || !g.Dirty {
		t.Fatalf("uncommitted change not reported: %+v", g)
	}
	if _, err := gitrepo.Run(wt, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Run(wt, "commit", "-qm", "a"); err != nil {
		t.Fatal(err)
	}
	g = Git(wt)
	if g == nil || g.Merged || g.CommitsAhead != 1 || g.Dirty {
		t.Fatalf("after one commit git = %+v, want unmerged, 1 ahead, clean (E13-12)", g)
	}
}

func newGitRepo(t *testing.T) string {
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
