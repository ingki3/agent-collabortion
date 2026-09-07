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
	"sort"
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

// WorktreeTarget decides where `git worktree add` actually checks out
// (daemon-protocol §4.1 v0.7.3 데몬 방어, D-21(a)·(b)).
//
// Two rules, and the second is the one that matters:
//
//  1. a RELATIVE bundle path is resolved against `<workdir_root>`, never
//     against the daemon's CWD (ResolvePath).
//  2. the target is ALWAYS under the workdir root. A path that lands outside
//     it — the `<session-slug>/<agent-slug>` a pre-v0.7.3 server sent, read
//     relative to `git -C <repo>`, or an absolute path pointing anywhere
//     else — is replaced by the daemon's own plan.
//
// Rule 2 exists because of what the alternative did: `git worktree add` runs
// with `-C <repo>`, so a relative target created the checkout INSIDE THE
// USER'S REPOSITORY (T-I4 차단 ①, measured: `…/repo/<session>/<agent>`).
// That is not a workdir the daemon may ever make — every path FR-9.1 and
// harness §10 root here (orphan records, CLI wrapper, stderr) would land in
// the user's `git status`, and GC (§6) cannot reach it. The bundle path is
// still honoured whenever it is inside the root, which is where §4.1 says the
// server puts it; relocating is a floor, not a preference.
func WorktreeTarget(root, bundlePath string, plan WorktreePlan, repoTop string) string {
	path := ResolvePath(root, bundlePath)
	if path == "" {
		return plan.Path
	}
	if repoTop != "" && UnderRoot(repoTop, path) {
		return plan.Path
	}
	if root != "" && !UnderRoot(root, path) {
		return plan.Path
	}
	return path
}

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
	path, branch := WorktreeTarget(root, b.Workdir.Path, plan, top), plan.Branch
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
	// §6 v0.7.3: `worktree` rows need the session uuid AND the agent_id, and
	// the checkout's directory name carries neither (the server names it with
	// slugs). Written on every preparation, not only the first — a record the
	// disk lost heals on the agent's next lane.
	if root != "" {
		if err := RecordWorkdir(root, Record{
			Kind: "worktree", Path: abs, SessionID: b.Task.SessionID,
			AgentID: b.Task.AgentID, AgentName: b.Task.AgentName, Branch: branch,
		}); err != nil {
			return "", fmt.Errorf("workdir index: %w", err)
		}
	}
	return abs, nil
}

// The §6 report's `git` block and the §4.4 finish's are one and the same
// shape, and since daemon-protocol v0.7.2 that shape is a contract type
// (`contracts.WorkdirGit`) — the local `GitInfo` that stood in while the
// contract PR (#157) was pending is gone (NN2).

// Git measures one checkout for the report. The server decides what may be
// collected from these numbers (§6 "GC 판정은 서버가 한다"); the daemon only
// states what it sees.
func Git(path string) *contracts.WorkdirGit {
	if !gitrepo.IsRepo(path) {
		return nil
	}
	branch := gitrepo.Branch(path)
	base := gitrepo.DefaultBranch(path)
	return &contracts.WorkdirGit{
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

// ListWorktrees enumerates this machine's `worktree` checkouts for the §6
// report, with the identity and the git block filled in.
//
// TWO SOURCES, and both are needed (v0.7.3, T-I4 차단 ②):
//
//   - the index (index.go) — the session uuid and the agent_id the bundle
//     stated. A row without them is dropped by the server without a word, so
//     a scan that can only read directory NAMES reports nothing usable;
//   - the disk scan of `<root>/worktrees/<session>/<agent>` — the daemon's own
//     plan, which is also every checkout an older daemon left behind and any
//     record a wiped `.colab` lost. It keeps a workdir visible to S13 and
//     reachable by GC even when its identity is unknown.
//
// `git` and `bytes` ride on every row: §6 makes them the only input the
// server's GC judgement has, and a missing block reads as "0 commits, clean"
// — which is how unmerged work gets deleted (FR-6.4 M4).
func ListWorktrees(root string) []Info {
	idx := LoadIndex(root)
	seen := map[string]bool{}
	var out []Info
	add := func(p, sessionFallback string) {
		abs := absClean(p)
		if seen[abs] {
			return
		}
		seen[abs] = true
		size, last := DiskUsage(abs)
		info := Info{
			Kind: "worktree", Path: abs, SessionID: sessionFallback,
			Bytes: size, LastUsedAt: last, Git: Git(abs),
		}
		if rec, ok := idx[abs]; ok {
			info.apply(rec)
		}
		out = append(out, info)
	}
	base := filepath.Join(root, "worktrees")
	sessions, _ := os.ReadDir(base)
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
			// The directory name is the fallback identity it always was: the
			// server skips a row it cannot match, and that is strictly better
			// than not reporting the disk at all.
			add(filepath.Join(base, s.Name(), a.Name()), s.Name())
		}
	}
	// A checkout at the path the SERVER chose (§4.1 — anywhere under the
	// root, not necessarily `worktrees/…`) is only findable through its
	// record.
	for path, rec := range idx {
		if rec.Kind != "worktree" {
			continue
		}
		if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
			continue // collected, or moved by hand
		}
		add(path, rec.SessionID)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
