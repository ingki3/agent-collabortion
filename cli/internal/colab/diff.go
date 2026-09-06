// diff.go — `colab artifact submit --type diff` without `--file`: the CLI
// builds the unified diff of the agent's own worktree (PRD FR-4.3, §3
// scenario B step 4, EVAL E16-B). The agent never hands another lane a path;
// the diff artifact is the only thing that crosses (FR-6.1).
//
// Two rules shape everything here:
//
//  1. **Only this worktree.** Every git command runs with the process's own
//     working directory (the workdir the daemon spawned the attempt in,
//     harness.md §5) and there is no flag, tool argument or environment knob
//     that points it anywhere else. A diff of a repository the agent was not
//     given is not something this command can produce.
//  2. **No new wire fields.** openapi `submitArtifact` is multipart
//     {name, type, file, description} and nothing else, so the branch/base/
//     commit metadata rides in the two places that already exist: the
//     description's first line and one comment line at the top of the diff
//     body. Rebinding re-apply (E14-06) reads exactly those two.
package colab

import (
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// ArtifactTypeDiff is the openapi `type` value (open set) that turns on diff
// generation.
const ArtifactTypeDiff = "diff"

// DiffHeaderPrefix starts the one comment line prepended to a generated diff.
// `git apply` skips everything before the first patch header, so a `#` line
// is metadata the tooling reads and the patch machinery ignores (proved by
// TestGeneratedDiffAppliesWithHeader, which really runs `git apply`).
const DiffHeaderPrefix = "# colab-diff:"

// diffMeta is the repository state a generated diff was taken from.
type diffMeta struct {
	Branch string // current branch, e.g. colab/<session>/frontend ("HEAD" when detached)
	Base   string // what the diff is against, e.g. origin/main
	Commit string // short HEAD commit
}

// headerLine is the single comment line at the top of a generated diff body.
func (m diffMeta) headerLine() string {
	return fmt.Sprintf("%s branch=%s base=%s commit=%s", DiffHeaderPrefix, m.Branch, m.Base, m.Commit)
}

// descriptionLine is the fixed first line of the artifact description. A
// caller's --description follows it after a newline.
func (m diffMeta) descriptionLine() string {
	return fmt.Sprintf("diff %s@%s vs %s", m.Branch, m.Commit, m.Base)
}

// diffResult is what buildDiff produces: the body to upload plus what the
// caller needs to name it and to explain an empty result.
type diffResult struct {
	Body      []byte
	Meta      diffMeta
	Untracked []string // files git does not track — deliberately NOT in the body
}

// baseCandidates are tried in order when --base is absent and the repository
// has no origin/HEAD to ask.
var baseCandidates = []string{"origin/main", "origin/master", "main", "master"}

// maxUntrackedListed caps the untracked paths echoed back so a workdir full
// of build output cannot bury the message.
const maxUntrackedListed = 10

// buildDiff runs git in dir and returns one unified diff covering everything
// this branch changed: commits since it diverged from base, staged changes,
// and unstaged working-tree changes.
//
// It is one `git diff <merge-base>` rather than `git diff base...HEAD` plus
// `git diff --cached` plus `git diff` concatenated: three patches of the same
// files stack hunks that no longer apply in sequence, while the merge-base
// form is a single patch of base → what is on disk right now.
//
// Untracked files are reported, never included — `git diff` does not see
// them, and the alternatives (`git add -N`) write to the agent's index.
func buildDiff(dir, base string) (*diffResult, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, &client.Error{Exit: client.ExitUsage, Code: "git_unavailable",
			Title:  "git is not on PATH",
			Detail: "`--type diff` builds the patch with git; submit a patch file with `--file` instead"}
	}
	if _, err := gitOut(dir, "rev-parse", "--show-toplevel"); err != nil {
		return nil, &client.Error{Exit: client.ExitUsage, Code: "not_a_git_repo",
			Title: "this workdir is not a git worktree",
			Detail: "`--type diff` without `--file` builds the diff of the workdir the task runs in; " +
				"this session may not be worktree-isolated — submit a patch file with `--file` instead"}
	}
	head, err := gitOut(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		return nil, &client.Error{Exit: client.ExitUsage, Code: "no_commit",
			Title: "the workdir has no commit yet",
			Detail: "`git rev-parse HEAD` failed — commit at least once before submitting a diff, " +
				"or submit the file itself with `--file`"}
	}
	branch, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" {
		branch = "HEAD"
	}
	base, err = resolveBase(dir, base)
	if err != nil {
		return nil, err
	}
	meta := diffMeta{Branch: branch, Base: base, Commit: head}

	// The diff is taken from the merge base so that commits landing on the
	// base branch after this worktree forked do not show up as reversals.
	from := base
	if mb, err := gitOut(dir, "merge-base", base, "HEAD"); err == nil && mb != "" {
		from = mb
	}
	body, err := gitOut(dir, "diff", "--no-color", "--no-ext-diff", "--find-renames", from, "--")
	if err != nil {
		return nil, &client.Error{Exit: client.ExitUsage, Code: "diff_failed",
			Title: "could not build the diff", Detail: err.Error()}
	}
	out := &diffResult{Meta: meta}
	if others, err := gitOut(dir, "ls-files", "--others", "--exclude-standard"); err == nil && others != "" {
		out.Untracked = strings.Split(others, "\n")
	}
	if strings.TrimSpace(body) == "" {
		return out, emptyDiff(meta, out.Untracked)
	}
	out.Body = []byte(meta.headerLine() + "\n" + body + "\n")
	return out, nil
}

// resolveBase turns --base into a verified revision, or finds the
// repository's default branch when it was not given.
func resolveBase(dir, base string) (string, error) {
	if b := strings.TrimSpace(base); b != "" {
		if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", b+"^{commit}"); err != nil {
			return "", &client.Error{Exit: client.ExitUsage, Code: "unknown_base",
				Title:  "--base " + b + " is not a revision in this workdir",
				Detail: "pass a branch or commit this worktree can see (e.g. origin/main)"}
		}
		return b, nil
	}
	// The repository's own answer first: origin/HEAD is what `git clone` set
	// to the remote's default branch.
	if ref, err := gitOut(dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil && ref != "" {
		return ref, nil
	}
	for _, cand := range baseCandidates {
		if _, err := gitOut(dir, "rev-parse", "--verify", "--quiet", cand+"^{commit}"); err == nil {
			return cand, nil
		}
	}
	return "", &client.Error{Exit: client.ExitUsage, Code: "unknown_base",
		Title: "could not tell which branch this work is based on",
		Detail: "no origin/HEAD and none of " + strings.Join(baseCandidates, ", ") +
			" exist here — pass --base <branch>"}
}

// emptyDiff is the refusal for "nothing to submit". It is exit 2 and no
// request is sent: an empty artifact would satisfy `artifact_submitted`
// (FR-2.2) with no work in it.
func emptyDiff(m diffMeta, untracked []string) error {
	detail := fmt.Sprintf("%s has no changes against %s — nothing was submitted", m.Branch, m.Base)
	if n := len(untracked); n > 0 {
		shown := untracked
		if n > maxUntrackedListed {
			shown = untracked[:maxUntrackedListed]
		}
		more := ""
		if n > len(shown) {
			more = ", …"
		}
		detail += fmt.Sprintf(". %d untracked file(s) are NOT part of a diff (%s%s) — `git add` them first",
			n, strings.Join(shown, ", "), more)
	}
	return &client.Error{Exit: client.ExitUsage, Code: "empty_diff", Title: "no changes to submit", Detail: detail}
}

// gitOut runs git in dir and returns stdout with the trailing newline
// trimmed. It never takes a directory from the caller's arguments: dir is
// the process working directory, so the command cannot reach a repository
// outside the worktree this attempt was given.
//
// GIT_OPTIONAL_LOCKS=0 keeps a read from taking the index lock or rewriting
// the index in the agent's worktree.
func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// diffNameUnsafe is everything an artifact name should not carry from a
// branch name (path separators, whitespace).
var diffNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// defaultDiffName is the artifact name for a generated diff when --name was
// not given. It has to be *stable* for the same worktree: E16-B step 5→6 has
// the same lane submit a second diff after review feedback, and FR-4.3 makes
// that version 2 only when the name matches. The branch's last segment is
// that stable key (`colab/<session>/frontend` → `frontend.diff`).
func defaultDiffName(branch, agentName string) string {
	name := ""
	if branch != "" && branch != "HEAD" {
		name = diffNameUnsafe.ReplaceAllString(path.Base(branch), "-")
	}
	if name == "" || name == "-" {
		name = diffNameUnsafe.ReplaceAllString(strings.TrimPrefix(agentName, "@"), "-")
	}
	if name == "" || name == "-" {
		name = "workdir"
	}
	return strings.Trim(name, "-.") + ".diff"
}

// repoMeta reports the workdir's branch/base/commit so a patch submitted
// with `--file --type diff` carries the same description first line a
// generated one does (E14-06 reads one place, however the diff was made).
//
// It is best effort: a workdir that is not a git worktree, or a repository
// whose base branch cannot be guessed, returns ok=false and the description
// is left alone. An explicit --base that does not resolve is an error
// though — a caller who names a base is owed the truth about it.
func repoMeta(dir, base string) (diffMeta, bool, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return diffMeta{}, false, nil
	}
	head, err := gitOut(dir, "rev-parse", "--short", "HEAD")
	if err != nil {
		if strings.TrimSpace(base) != "" {
			return diffMeta{}, false, &client.Error{Exit: client.ExitUsage, Code: "not_a_git_repo",
				Title:  "--base was given but this workdir is not a git worktree with a commit",
				Detail: "drop --base to submit the patch file as it is"}
		}
		return diffMeta{}, false, nil
	}
	resolved, err := resolveBase(dir, base)
	if err != nil {
		if strings.TrimSpace(base) != "" {
			return diffMeta{}, false, err
		}
		return diffMeta{}, false, nil
	}
	branch, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil || branch == "" {
		branch = "HEAD"
	}
	return diffMeta{Branch: branch, Base: resolved, Commit: head}, true, nil
}

// stampDescription puts the fixed metadata line first and the caller's own
// --description underneath it, as one description field — openapi
// submitArtifact has no room for another part.
func stampDescription(m diffMeta, userDescription string) string {
	first := m.descriptionLine()
	if strings.TrimSpace(userDescription) == "" {
		return first
	}
	return first + "\n" + userDescription
}

// diffFileName is the multipart `filename` for a generated diff — the
// artifact name with a patch extension, so a download lands on disk as
// something `git apply` accepts by name.
func diffFileName(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".diff", ".patch":
		return name
	}
	return name + ".diff"
}
