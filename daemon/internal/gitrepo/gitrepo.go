// Package gitrepo is the daemon's thin shell over the git commands P4 needs:
// worktree preparation (FR-6.4, E13-02), the probe's `repos[]` judgement
// (daemon-protocol §3 — remote_url is what decides a rebinding candidate,
// E14-04·05), the §6 workdir report's `git` block, and the ONE way a
// `worktree` workdir may be collected — `git worktree remove`, which keeps
// the branch (§6, E13-10).
//
// Everything here shells out. A git library would have to model worktrees,
// the common dir, and `info/exclude` resolution exactly as the git binary
// does, and the whole point of spike 5 is that a mismatch between what we
// think git does and what it does is where the data loss lives. The binary
// is the specification.
//
// NOTHING HERE WRITES A TRACKED FILE. The daemon's entire footprint in a
// user's checkout is one untracked file (`COLAB_BRIEF.md`, package brief)
// and one bracketed block in `.git/info/exclude`, which is not part of the
// working tree at all.
package gitrepo

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrNotRepo is returned when a path is not inside a git working tree.
var ErrNotRepo = errors.New("gitrepo: not a git working tree")

// Run executes git in dir and returns trimmed stdout. On failure the error
// carries git's own stderr: a refusal the daemon reports to the server
// (§6 `gc: {status: refused, reason}`) is only useful if it says what git
// said.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A git that stops to ask for credentials or opens an editor would hang
	// a lane for its whole stall timeout.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_EDITOR=true",
	)
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// IsRepo reports whether dir is inside a git working tree.
func IsRepo(dir string) bool {
	if dir == "" {
		return false
	}
	out, err := Run(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && out == "true"
}

// TopLevel is the root of the working tree containing dir.
func TopLevel(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", ErrNotRepo
	}
	return out, nil
}

// CommonDir is the shared git directory — for a linked worktree that is the
// MAIN repository's `.git`, not `.git/worktrees/<name>`. `info/exclude` lives
// there and is therefore shared by every worktree of the repository, which is
// why Exclude* below is written to tolerate concurrent lanes (measured
// 2026-09-07: `git rev-parse --git-common-dir` inside a linked worktree
// answers the main `.git`).
func CommonDir(dir string) (string, error) {
	out, err := Run(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", ErrNotRepo
	}
	if !filepath.IsAbs(out) {
		out = filepath.Join(dir, out)
	}
	return filepath.Clean(out), nil
}

// RemoteURL is `origin`'s fetch URL, "" when the repository has no origin.
// daemon-protocol §3: this string, not the path, decides whether another
// machine is a rebinding candidate (E14-04·05).
func RemoteURL(dir string) string {
	out, err := Run(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return out
}

// Branch is the checked-out branch, or "" in a detached HEAD.
func Branch(dir string) string {
	out, err := Run(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// Clean reports whether `git status --porcelain` is empty — the probe's
// `repos[].clean` and the §6 report's `git.dirty` inverted.
//
// Untracked files count as dirty, which is exactly why the brief file is
// registered in `.git/info/exclude` (E13-03: the lane must not make the
// repository it borrowed look dirty).
func Clean(dir string) bool {
	out, err := Run(dir, "status", "--porcelain")
	return err == nil && out == ""
}

// DefaultBranch is the branch a worktree is cut from and measured against.
//
// The order matters and the first draft got it wrong. Called from INSIDE a
// linked worktree, "the current branch" is `colab/<session>/<agent>` — the
// very branch we want to compare against the trunk — so a fallback that
// reaches for HEAD makes every checkout look merged and 0 commits ahead, and
// E13-12 (미병합 커밋 있음 → 삭제 안 함) never fires.
//
//  1. `origin/HEAD` when the remote publishes one.
//  2. the MAIN working tree's branch — the repository the worktrees were cut
//     from is checked out there, and that is what the user calls the trunk.
//  3. `main`, then `master`.
//  4. the current branch, last, for a repository with none of the above.
func DefaultBranch(dir string) string {
	if out, err := Run(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil && out != "" {
		if i := strings.IndexByte(out, '/'); i >= 0 {
			return out[i+1:]
		}
		return out
	}
	if wts, err := Worktrees(dir); err == nil && len(wts) > 0 && wts[0].Branch != "" {
		return wts[0].Branch
	}
	for _, b := range []string{"main", "master"} {
		if _, err := Run(dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return Branch(dir)
}

// Worktree is one row of `git worktree list --porcelain`.
type Worktree struct {
	Path   string
	Branch string
	Locked bool
}

// Worktrees lists the working trees of the repository containing dir,
// including the main one.
func Worktrees(dir string) ([]Worktree, error) {
	out, err := Run(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []Worktree
	var cur *Worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, Worktree{Path: strings.TrimPrefix(line, "worktree ")})
			cur = &list[len(list)-1]
		case cur == nil:
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "locked" || strings.HasPrefix(line, "locked "):
			cur.Locked = true
		}
	}
	return list, nil
}

// IsWorktreeCheckout reports whether path is a LINKED worktree: `.git` is a
// file (`gitdir: …`) there, a directory in a plain clone.
func IsWorktreeCheckout(path string) bool {
	fi, err := os.Stat(filepath.Join(path, ".git"))
	return err == nil && !fi.IsDir()
}

// WorktreeAdd checks out branch at path, creating the branch from base when
// it does not exist yet (FR-6.4 `colab/<session>/<agent>`). An existing
// worktree at that path is reused as-is: FR-6.4/C3 binds ONE workdir per
// agent, and re-creating it on a retry would throw away the work the resume
// prompt is about to ask the agent to inspect (E11-06).
func WorktreeAdd(repo, path, branch, base string) error {
	if IsWorktreeCheckout(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// A retry after a crash can find the branch already created by the
	// attempt that died before its worktree survived; checking out the
	// existing branch keeps that attempt's commits.
	if branchExists(repo, branch) {
		_, err := Run(repo, "worktree", "add", path, branch)
		return err
	}
	args := []string{"worktree", "add", "-b", branch, path}
	if base != "" {
		args = append(args, base)
	}
	_, err := Run(repo, args...)
	return err
}

func branchExists(repo, branch string) bool {
	_, err := Run(repo, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// WorktreeRemove is the ONLY way the daemon deletes a `worktree` workdir
// (daemon-protocol §6, E13-10). No `--force`: git itself refuses a checkout
// with modified or untracked files, and that refusal is the answer the
// server asked for, not an obstacle to route around. The branch is left
// behind — `git worktree remove` never deletes it (measured 2026-09-07).
func WorktreeRemove(repo, path string) error {
	_, err := Run(repo, "worktree", "remove", path)
	return err
}

// Prune drops administrative entries for worktrees whose directory is gone.
func Prune(repo string) { _, _ = Run(repo, "worktree", "prune") }

// Merged reports whether every commit of branch is contained in base.
func Merged(dir, branch, base string) bool {
	if branch == "" || base == "" || branch == base {
		return true
	}
	_, err := Run(dir, "merge-base", "--is-ancestor", branch, base)
	return err == nil
}

// CommitsAhead counts commits on branch that base does not have.
func CommitsAhead(dir, branch, base string) int {
	if branch == "" || base == "" || branch == base {
		return 0
	}
	out, err := Run(dir, "rev-list", "--count", base+".."+branch)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(out)
	if err != nil {
		return 0
	}
	return n
}

// ---------------------------------------------------------------------------
// .git/info/exclude — §8.4 M3 v0.16 / harness §10 v0.8.6
// ---------------------------------------------------------------------------

// Exclude block markers. The block is bracketed so ExcludeRelease removes
// exactly what we wrote and nothing a human put in that file.
const (
	excludeStart = "# colab:exclude:start"
	excludeEnd   = "# colab:exclude:end"
)

// excludePath is <common-dir>/info/exclude.
func excludePath(dir string) (string, error) {
	common, err := CommonDir(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "info", "exclude"), nil
}

// ExcludeEnsure registers pattern so git stops seeing the brief file
// (E13-03·04). `.gitignore` is NEVER touched — it belongs to the repository
// and a commit of our line lands in the user's history (§8.4, E13-07).
//
// The file is shared by every worktree of the repository (see CommonDir), so
// two parallel lanes ask for the same pattern; the second is a no-op rather
// than a duplicate line.
func ExcludeEnsure(dir, pattern string) error {
	p, err := excludePath(dir)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hasPattern(body, pattern) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	var out bytes.Buffer
	out.Write(body)
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		out.WriteByte('\n')
	}
	out.WriteString(excludeStart + "\n" + pattern + "\n" + excludeEnd + "\n")
	return os.WriteFile(p, out.Bytes(), 0o644)
}

// ExcludeRelease removes our block (E13-06 등록 해제). A pattern still in use
// by ANOTHER live worktree of the same repository is left alone: the entry is
// repository-wide, and unregistering it while a sibling lane's brief file is
// still on disk would make that file appear as an untracked change in a
// checkout the agent is committing from.
func ExcludeRelease(dir, pattern string, stillUsed func() bool) error {
	if stillUsed != nil && stillUsed() {
		return nil
	}
	p, err := excludePath(dir)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !hasPattern(body, pattern) {
		return nil
	}
	return os.WriteFile(p, stripExcludeBlock(body, pattern), 0o644)
}

// ExcludeHas reports whether the pattern is registered (used by the P4
// golden mirror and the e2e smoke).
func ExcludeHas(dir, pattern string) bool {
	p, err := excludePath(dir)
	if err != nil {
		return false
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	return hasPattern(body, pattern)
}

func hasPattern(body []byte, pattern string) bool {
	for _, l := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(l) == pattern {
			return true
		}
	}
	return false
}

// stripExcludeBlock removes our bracketed block, and — defensively — a bare
// pattern line that an older daemon may have written without brackets.
func stripExcludeBlock(body []byte, pattern string) []byte {
	lines := strings.Split(string(body), "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case t == excludeStart:
			inBlock = true
		case t == excludeEnd:
			inBlock = false
		case inBlock && t == pattern:
		case !inBlock && t == pattern:
		default:
			out = append(out, l)
		}
	}
	s := strings.Join(out, "\n")
	// Collapse the blank line the removal can leave at the end.
	for strings.HasSuffix(s, "\n\n") {
		s = s[:len(s)-1]
	}
	return []byte(s)
}
