// A `worktree` lane end to end through the loop: the checkout is prepared and
// reused, the brief is our own untracked file, the turn prompt points at it,
// the tree stays clean, and `finish` carries the §4.4 git block.
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
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/brief"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/workdir"
)

func worktreeBundle(id, repo string, attempt int) contracts.TaskBundle {
	b := bundle(id)
	b.Task.Attempt = attempt
	b.Task.AgentName = "backend"
	b.Profile.RuntimeKind = contracts.RuntimeHermes
	b.Brief.Transport = contracts.BriefInstructionFile
	b.Brief.Text = "## [1] Agent Identity\n\nBackend\n"
	b.Prompt = "Implement the widget."
	b.Workdir = contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo, Reuse: true}
	return b
}

func TestWorktreeLaneEndToEnd(t *testing.T) {
	repo := initRepo(t)
	// The repository owns a tracked AGENTS.md. §8.4 M3 v0.16: we neither
	// read nor write it, and this asserts the bytes afterwards.
	original := "# House rules\n\nPROJECT_RULE: squash merges only\n"
	writeFile(t, filepath.Join(repo, "AGENTS.md"), original)
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "-qm", "rules")

	srv := &memServer{queue: []contracts.TaskBundle{worktreeBundle("t-wt", repo, 1)}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(script, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	var wdPath, briefBody, statusDuring string
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase != "preparing" || req.WorkdirPath == "" {
			return
		}
		wdPath = req.WorkdirPath
		b, _ := os.ReadFile(filepath.Join(req.WorkdirPath, brief.FileName))
		briefBody = string(b)
		statusDuring, _ = gitrepo.Run(req.WorkdirPath, "status", "--porcelain")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	// E13-02: the checkout is under the workdir root — never nested in the
	// user's repository, where our orphan records and CLI wrappers would show
	// up in their `git status`.
	want := workdir.WorktreePath(root, "sess", "backend")
	if wdPath != want {
		t.Fatalf("workdir = %q, want %q", wdPath, want)
	}
	if b := gitIn(t, wdPath, "symbolic-ref", "--short", "HEAD"); b != "colab/sess/backend" {
		t.Errorf("branch = %q, want colab/sess/backend (FR-6.4)", b)
	}

	// E13-03: the brief is OUR file, and the tree is clean while the lane runs.
	if !strings.Contains(briefBody, brief.MarkerStart) || !strings.Contains(briefBody, "Backend") {
		t.Fatalf("brief file during the lane: %q", briefBody)
	}
	if statusDuring != "" {
		t.Errorf("`git status` during the lane = %q, want empty — the brief must be excluded "+
			"(E13-03), and a dirty tree also trips the GC rules of E13-13 later", statusDuring)
	}
	if n := skipWorktreeBits(t, wdPath); n != 0 {
		t.Errorf("skip-worktree bits = %d, want 0 (spike 5)", n)
	}

	// E13-06a: the turn prompt's FIRST line names the brief by absolute path.
	prompt := promptOf(t, record)
	first := strings.SplitN(prompt, "\n", 2)[0]
	abs := filepath.Join(wdPath, brief.FileName)
	if !strings.Contains(first, abs) {
		t.Errorf("first prompt line = %q, want the absolute path %q (§8.4 v0.16, E13-06a)", first, abs)
	}
	if !strings.Contains(prompt, "Implement the widget.") {
		t.Errorf("the server's prompt was replaced rather than prefixed: %q", prompt)
	}

	// Lane end: our file is gone, the exclude entry with it, the repository's
	// own AGENTS.md is byte-identical, and the tree is clean.
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("COLAB_BRIEF.md survived the lane (E13-06)")
	}
	if gitrepo.ExcludeHas(wdPath, brief.FileName) {
		t.Errorf("`.git/info/exclude` still lists our path (E13-06)")
	}
	if got, _ := os.ReadFile(filepath.Join(wdPath, "AGENTS.md")); string(got) != original {
		t.Errorf("the repository's AGENTS.md changed to %q (§8.4 M3 v0.16)", got)
	}
	if out, _ := gitrepo.Run(wdPath, "status", "--porcelain"); out != "" {
		t.Errorf("`git status` after the lane = %q, want empty", out)
	}
	if out, _ := gitrepo.Run(repo, "status", "--porcelain"); out != "" {
		t.Errorf("the SOURCE repository is dirty after the lane: %q", out)
	}

	// §4.4: the finish carries the workdir git block, so the server can judge
	// 병합·클린 (E13-12·13) with the attempt that made the state.
	f := srv.finishes[0]
	if f.Workdir == nil || f.Workdir.Path != wdPath {
		t.Fatalf("finish workdir = %+v", f.Workdir)
	}
	if f.Workdir.Git == nil || f.Workdir.Git.Branch != "colab/sess/backend" || f.Workdir.Git.Dirty {
		t.Errorf("finish workdir.git = %+v, want branch colab/sess/backend, not dirty", f.Workdir.Git)
	}
}

// FR-6.4/C3: the agent's second lane — and its retry — land in the SAME
// checkout, and the stale brief is replaced rather than appended to.
func TestWorktreeIsReusedAcrossAttempts(t *testing.T) {
	repo := initRepo(t)
	srv := &memServer{queue: []contracts.TaskBundle{
		worktreeBundle("t-r1", repo, 1),
		worktreeBundle("t-r1", repo, 2),
	}}
	script := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
	d, root := newDaemon(t, srv, script)

	var paths []string
	var blocks []int
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase != "preparing" || req.WorkdirPath == "" {
			return
		}
		paths = append(paths, req.WorkdirPath)
		b, _ := os.ReadFile(filepath.Join(req.WorkdirPath, brief.FileName))
		blocks = append(blocks, strings.Count(string(b), brief.MarkerStart))
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 30*time.Second, func() bool { return srv.finished() == 2 })
	cancel()
	<-done

	if len(paths) != 2 || paths[0] != paths[1] {
		t.Fatalf("workdirs = %v, want the same checkout twice (FR-6.4/C3)", paths)
	}
	for i, n := range blocks {
		if n != 1 {
			t.Errorf("attempt %d saw %d marker blocks, want 1 — a resumed lane REPLACES the "+
				"stale brief (harness §10 v0.8.6)", i+1, n)
		}
	}
	wts, err := gitrepo.Worktrees(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 2 {
		t.Errorf("git worktree list = %d, want 2 (main + backend) — %v", len(wts), wts)
	}
	_ = root
}

// skipWorktreeBits counts paths with the skip-worktree bit. Spike 5's finding
// is that this number must stay 0.
func skipWorktreeBits(t *testing.T, dir string) int {
	t.Helper()
	out, err := gitrepo.Run(dir, "ls-files", "-v")
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if c := line[0]; c >= 'a' && c <= 'z' {
			n++
		}
	}
	return n
}
