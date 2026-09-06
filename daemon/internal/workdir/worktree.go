// worktree.go implements `worktree` isolation (PRD FR-6.4, EVAL E13-01·02·10,
// E16-B 2단계) — the P4 half of this package. `none`/`dir` isolation is in
// workdir.go and is unchanged.
//
// THE ONE RULE THAT SHAPES EVERYTHING HERE: a `worktree` workdir belongs to
// the AGENT, not to the lane (FR-6.4/C3). Backend's second lane, its retry
// and its resume all land in the same checkout, which is why those lanes run
// sequentially (FR-6.3) and why the crash-recovery property of FR-9.1 has to
// be re-measured under `worktree` — two processes in one checkout corrupt a
// user's repository, not our scratch space.
package workdir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// WorktreeRequest is what the daemon needs to decide one agent's checkout.
type WorktreeRequest struct {
	// Root is the daemon's workdir root. Checkouts live UNDER it, never
	// inside the user's repository: the orphan records (daemon-protocol §5),
	// the CLI wrappers (harness §10) and the stderr logs are all rooted here,
	// and a checkout nested in the source repository would put every one of
	// them in the user's `git status`.
	Root string
	// RepoPath is the repository the worktree is cut from (E13-01).
	RepoPath string
	// SessionSlug and AgentSlug name the branch (FR-6.4).
	SessionSlug string
	AgentSlug   string
	// BaseBranch overrides the repository's default branch.
	BaseBranch string
}

// WorktreePlan is what the daemon is told to do — the shape the P4a golden
// table measures (server/internal/workdirs/gc_golden_test.go, E13-02).
type WorktreePlan struct {
	Branch string
	Path   string
	// Created is false when an existing worktree is reused (에이전트당 1개).
	Created bool
	// BaseBranch is what the worktree is cut from.
	BaseBranch string
}

// WorktreeBranch is FR-6.4's `colab/<session>/<agent>`.
//
// The slugs are sanitised but NOT hashed: E16-B's verdict line names
// `colab/S/backend` literally, and a branch a human cannot recognise in
// `git branch` defeats the reason the branch is preserved by GC at all.
func WorktreeBranch(sessionSlug, agentSlug string) string {
	return "colab/" + refSafe(sessionSlug) + "/" + refSafe(agentSlug)
}

// WorktreePath is <root>/worktrees/<session>/<agent> — one per agent (C3).
func WorktreePath(root, sessionSlug, agentSlug string) string {
	return filepath.Join(root, "worktrees", safe(sessionSlug), safe(agentSlug))
}

// PlanWorktree decides the branch, the path and whether this lane creates the
// checkout or reuses the agent's existing one.
func PlanWorktree(r WorktreeRequest) WorktreePlan {
	p := WorktreePlan{
		Branch:     WorktreeBranch(r.SessionSlug, r.AgentSlug),
		Path:       WorktreePath(r.Root, r.SessionSlug, r.AgentSlug),
		BaseBranch: r.BaseBranch,
	}
	if p.BaseBranch == "" && r.RepoPath != "" {
		p.BaseBranch = gitrepo.DefaultBranch(r.RepoPath)
	}
	p.Created = !gitrepo.IsWorktreeCheckout(p.Path)
	return p
}

// refSafe maps a slug into the characters git accepts in a ref component.
// Empty becomes "_" so `colab//backend` — a ref git rejects — is impossible.
func refSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '-'
	}, s)
	s = strings.Trim(s, "-.")
	if s == "" {
		return "_"
	}
	return s
}

// ErrNoRepo is returned when a `worktree` bundle names no usable repository.
var ErrNoRepo = errors.New("workdir: worktree isolation needs repo_path")

// prepLocks serialises PrepareWorktree per path within this daemon.
var prepLocks sync.Map // path → *sync.Mutex

func lockPath(path string) func() {
	v, _ := prepLocks.LoadOrStore(path, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// PrepareWorktree creates or reuses the agent's checkout for a bundle
// (E13-02). The bundle's explicit path/branch win when the server sent them
// (daemon-protocol §4.1 `workdir: {path?, repo_path?, branch?}`); otherwise
// the daemon plans them.
func PrepareWorktree(root string, b contracts.TaskBundle) (string, error) {
	repo := b.Workdir.RepoPath
	if repo == "" {
		return "", ErrNoRepo
	}
	if !gitrepo.IsRepo(repo) {
		// E13-01: the wizard is supposed to have blocked this, but a
		// repository can be moved or deleted between session creation and the
		// first task, and a `git worktree add` into nothing produces a git
		// error with no hint of the real cause.
		return "", fmt.Errorf("%w: %s is not a git working tree", ErrNoRepo, repo)
	}
	top, err := gitrepo.TopLevel(repo)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrNoRepo, repo)
	}
	plan := PlanWorktree(WorktreeRequest{
		Root: root, RepoPath: top,
		SessionSlug: b.Task.SessionID, AgentSlug: b.Task.AgentName,
		BaseBranch: b.Workdir.Branch,
	})
	path, branch := b.Workdir.Path, plan.Branch
	if path == "" {
		path = plan.Path
	}
	if b.Workdir.Branch != "" {
		branch = b.Workdir.Branch
	}
	base := plan.BaseBranch
	if base == branch {
		base = ""
	}
	// One checkout, one preparer. Two lanes of the same agent do not run at
	// once (E2-12 keeps them sequential), but a retry that overlaps its
	// predecessor's teardown, or two agents cutting from the same repository,
	// both reach `git worktree add` — and git's own locking answers the loser
	// with a non-zero exit whose stderr says only "Preparing worktree",
	// which reads like nothing went wrong. Measured 2026-09-07.
	unlock := lockPath(path)
	defer unlock()
	// A stale administrative entry from a checkout someone deleted by hand
	// makes `worktree add` fail with "already registered".
	gitrepo.Prune(top)
	if err := gitrepo.WorktreeAdd(top, path, branch, base); err != nil {
		// The loser of a race still gets the checkout it asked for. Only a
		// path that is STILL not a worktree is a real failure.
		if !gitrepo.IsWorktreeCheckout(path) {
			return "", err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// GitInfo is the §6 report's `git` block and the §4.4 finish's.
type GitInfo struct {
	Branch       string `json:"branch"`
	Merged       bool   `json:"merged"`
	Dirty        bool   `json:"dirty"`
	CommitsAhead int    `json:"commits_ahead"`
}

// Git measures one checkout for the report. The server decides what may be
// collected from these numbers (§6 "GC 판정은 서버가 한다"); the daemon only
// states what it sees.
func Git(path string) *GitInfo {
	if !gitrepo.IsRepo(path) {
		return nil
	}
	branch := gitrepo.Branch(path)
	base := gitrepo.DefaultBranch(path)
	return &GitInfo{
		Branch:       branch,
		Merged:       gitrepo.Merged(path, branch, base),
		Dirty:        !gitrepo.Clean(path),
		CommitsAhead: gitrepo.CommitsAhead(path, branch, base),
	}
}

// GC refusal reasons for `worktree` (daemon-protocol §6). They are the
// daemon's own vocabulary for "git said no" and "somebody is still in there";
// the server shows them verbatim in the feed ("GC 거부: <reason>").
const (
	// GCReasonProcessAlive is a live attempt still holding the checkout. It
	// comes first: removing a directory a runtime is writing to is how you
	// get half-written files and a git index lock nobody owns.
	GCReasonProcessAlive = "process_alive"
	// GCReasonWorktreeLocked is `git worktree lock`.
	GCReasonWorktreeLocked = "worktree_locked"
	// GCReasonWorktreeRemove carries git's own refusal — "contains modified
	// or untracked files" is the common one and it is exactly the E13-13
	// case the server also judges.
	GCReasonWorktreeRemove = "worktree_remove_failed"
)

// RemoveWorktree collects one `worktree` workdir the ONLY way §6 allows:
// `git worktree remove`, with no --force, leaving the branch behind
// (E13-10). git's own refusals are returned, not overridden — the daemon
// executes the server's decision and reports, it does not overrule it.
func RemoveWorktree(path string) (reason string, err error) {
	top, err := gitrepo.TopLevel(path)
	if err != nil {
		// Not a checkout any more (already removed by hand): nothing to do,
		// and reporting `deleted` is the truth the server needs to close the
		// command (§4.3 "해당 workdir 보고에서 삭제 확인").
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			return "", nil
		}
		return GCReasonWorktreeRemove, err
	}
	if locked, lerr := worktreeLocked(top, path); lerr == nil && locked {
		return GCReasonWorktreeLocked, fmt.Errorf("worktree %s is locked", path)
	}
	if err := gitrepo.WorktreeRemove(top, path); err != nil {
		return GCReasonWorktreeRemove, err
	}
	return "", nil
}

func worktreeLocked(repo, path string) (bool, error) {
	wts, err := gitrepo.Worktrees(repo)
	if err != nil {
		return false, err
	}
	want, _ := filepath.Abs(path)
	for _, w := range wts {
		if abs, _ := filepath.Abs(w.Path); abs == want {
			return w.Locked, nil
		}
	}
	return false, nil
}

// ListWorktrees enumerates the checkouts under <root>/worktrees for the §6
// report, with the git block filled in.
func ListWorktrees(root string) []Info {
	base := filepath.Join(root, "worktrees")
	sessions, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []Info
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		agents, err := os.ReadDir(filepath.Join(base, s.Name()))
		if err != nil {
			continue
		}
		for _, a := range agents {
			if !a.IsDir() {
				continue
			}
			p := filepath.Join(base, s.Name(), a.Name())
			size, last := DiskUsage(p)
			out = append(out, Info{
				Kind: "worktree", Path: p, SessionID: s.Name(),
				Bytes: size, LastUsedAt: last, Git: Git(p),
			})
		}
	}
	return out
}
