// Real runtimes, `worktree` isolation, on a THROWAWAY repository outside this
// one (P4_TASKS §0-18). This is the T-D9 DoD smoke:
//
//	COLAB_SMOKE=1 go test ./internal/loop -run SmokeP4 -v -timeout 25m
//
// It measures the two things a fake cannot. (a) The brief really reaches the
// runtime and the checkout really stays clean — spike 5's whole finding was
// that the v0.15 mechanism looked clean and was not. (b) Hermes's dedicated
// permission gate for instruction files ("Write to protected agent-instruction
// file(s): AGENTS.md. … approval is always required") does not catch
// COLAB_BRIEF.md, which SPIKE_05 §7.4 handed to T-D9 as an item to verify on
// real hardware rather than reason about.
//
// The instructions carry the §0-16 boilerplate: no wandering into other
// directories, no retrying a failed tool by another route.
package loop

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
)

const (
	smokeConfirmCode = "COLAB-BRIEF-7731"
	smokeRuleCode    = "ORIG-RULE-4471"
)

func p4SmokeGate(t *testing.T, cli string) {
	t.Helper()
	if os.Getenv("COLAB_SMOKE") != "1" {
		t.Skip("set COLAB_SMOKE=1 to run the real-runtime smoke")
	}
	if _, err := exec.LookPath(cli); err != nil {
		t.Skipf("%s not on this machine", cli)
	}
	if cli == "claude" {
		if _, err := exec.LookPath("npx"); err != nil {
			t.Skip("npx not on this machine")
		}
	}
}

// smokeRepo is a throwaway repository with a TRACKED AGENTS.md carrying a
// rule of its own — the state §8.4 M3 is about.
func smokeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.email", "smoke@test")
	gitIn(t, dir, "config", "user.name", "colab smoke")
	writeFile(t, filepath.Join(dir, "AGENTS.md"),
		"# House rules\n\nPROJECT_RULE_CODE is "+smokeRuleCode+".\n")
	writeFile(t, filepath.Join(dir, "catalog.md"), "# Widget catalog\n\n- bolt\n")
	writeFile(t, filepath.Join(dir, ".gitignore"), "out/\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "seed")
	return dir
}

func p4SmokeBundle(id, repo string, kind contracts.RuntimeKind, model, prompt string) contracts.TaskBundle {
	transport := contracts.BriefACPMetaSystemPrompt
	if kind == contracts.RuntimeHermes {
		transport = contracts.BriefInstructionFile
	}
	return contracts.TaskBundle{
		Task:      contracts.BundleTask{ID: id, Attempt: 1, LaneID: "lane-1", SessionID: "smoke", AgentName: "backend"},
		TaskToken: "ctk_smoke",
		Profile:   contracts.BundleProfile{RuntimeKind: kind, Model: model, AdapterPin: acp.AdapterPin},
		Workdir:   contracts.BundleWorkdir{Kind: "worktree", RepoPath: repo, Reuse: true},
		Brief: contracts.BundleBrief{Transport: transport, Text: "## [1] Agent Identity\n\n" +
			"You are Backend on a widget catalog.\n\n" +
			"## [2] Workspace Rules\n\n" +
			"SESSION_CONFIRM_CODE is " + smokeConfirmCode + ".\n" +
			"Do not look around this directory or any other directory for anything not named in the turn.\n" +
			"If a tool fails, stop and say so — do not retry it and do not look for another way.\n"},
		Prompt: prompt,
		Limits: contracts.BundleLimits{StallSeconds: 180},
	}
}

func p4SmokeDaemon(t *testing.T, srv *memServer, kind contracts.RuntimeKind) (*Daemon, string) {
	t.Helper()
	d, root := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "unused"}}}}})
	d.SpawnConfig = func(b contracts.TaskBundle, wd string) acp.Config {
		cmd, args := acp.Command(kind, b.Profile.AdapterPin, b.Profile.Args)
		env := acp.Env(kind, d.taskEnv(b), b.Profile.Env)
		return acp.Config{
			Command: cmd, Args: args, Env: env, Dir: wd,
			StderrPath: filepath.Join(root, "stderr-"+string(kind)+".txt"),
			KillAfter:  10 * time.Second,
		}
	}
	return d, root
}

func p4SmokeRun(t *testing.T, d *Daemon, srv *memServer, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Minute, func() bool { return srv.finished() >= want })
	cancel()
	<-done
	for i, f := range srv.finishes {
		t.Logf("finish[%d] outcome=%s stop=%s failure=%s workdir=%+v", i, f.Outcome, f.StopReason, f.FailureKind, f.Workdir)
	}
}

// Claude Code, `worktree`: the brief arrives through _meta (no file is
// written at all, harness §10), the agent edits the checkout and commits, and
// after the lane `git status` is clean and the branch carries the commit.
func TestSmokeP4WorktreeClaudeCode(t *testing.T) {
	p4SmokeGate(t, "claude")
	repo := smokeRepo(t)

	read := p4SmokeBundle("smoke-cc-1", repo, contracts.RuntimeClaudeCode, "claude-haiku-4-5-20251001",
		"Answer with the SESSION_CONFIRM_CODE from your instructions and nothing else. Do not use any tool.")
	edit := p4SmokeBundle("smoke-cc-2", repo, contracts.RuntimeClaudeCode, "claude-haiku-4-5-20251001",
		"Append the line '- washer' to catalog.md in your working directory, then run "+
			"`git add -A && git commit -m \"feat: washer\"` and report the commit output verbatim. "+
			"Do not look at any other directory.")
	edit.Task.Attempt = 1

	srv := &memServer{queue: []contracts.TaskBundle{read, edit}}
	d, root := p4SmokeDaemon(t, srv, contracts.RuntimeClaudeCode)
	p4SmokeRun(t, d, srv, 2)

	wd := filepath.Join(root, "worktrees", "smoke", "backend")
	t.Logf("workdir=%s", wd)
	assertBriefRead(t, srv, smokeConfirmCode)
	assertWorktreeCleanAndCommitted(t, repo, wd, "washer")
	// harness §10: claude_code writes NO file into the workdir.
	if _, err := os.Stat(filepath.Join(wd, brief.FileName)); err == nil {
		t.Error("claude_code wrote a brief file — its transport is _meta (harness §10)")
	}
}

// Hermes, `worktree`: the brief is our untracked COLAB_BRIEF.md, the turn
// prompt's first line points at it, the agent reads it, edits the
// repository's OWN AGENTS.md and commits — and that commit succeeds
// (spike 5 §3.1 measured 0/12 under skip-worktree). After the lane our file
// and the exclude entry are gone and the tree is clean.
//
// It also records every permission request, which is how SPIKE_05 §7.4's open
// item is closed: if Hermes's instruction-file gate caught COLAB_BRIEF.md,
// a `Write to protected agent-instruction file(s)` request would appear here.
func TestSmokeP4WorktreeHermesBriefFile(t *testing.T) {
	p4SmokeGate(t, "hermes")
	repo := smokeRepo(t)

	read := p4SmokeBundle("smoke-hm-1", repo, contracts.RuntimeHermes, "haiku",
		"Answer with the SESSION_CONFIRM_CODE and the PROJECT_RULE_CODE, one per line, and nothing else.")
	edit := p4SmokeBundle("smoke-hm-2", repo, contracts.RuntimeHermes, "haiku",
		"Append the line 'AGENT NOTE: prefer squash merges' to AGENTS.md in your working directory, "+
			"then run `git add -A && git commit -m \"docs: agent note\"` and report the commit output verbatim. "+
			"Do not look at any other directory.")

	srv := &memServer{queue: []contracts.TaskBundle{read, edit}}
	d, root := p4SmokeDaemon(t, srv, contracts.RuntimeHermes)

	wd := filepath.Join(root, "worktrees", "smoke", "backend")
	var duringStatus, duringBrief, duringPrompt string
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase != "preparing" || req.WorkdirPath == "" || duringBrief != "" {
			return
		}
		b, _ := os.ReadFile(filepath.Join(req.WorkdirPath, brief.FileName))
		duringBrief = string(b)
		duringStatus, _ = gitrepo.Run(req.WorkdirPath, "status", "--porcelain")
		duringPrompt = brief.TurnPromptPointer(req.WorkdirPath)
	}

	p4SmokeRun(t, d, srv, 2)

	t.Logf("workdir=%s", wd)
	t.Logf("pointer line: %s", duringPrompt)
	// E13-03: our file exists, the tree is clean, no skip-worktree bit.
	if !strings.Contains(duringBrief, smokeConfirmCode) {
		t.Errorf("COLAB_BRIEF.md during the lane did not carry the brief: %q", duringBrief)
	}
	if duringStatus != "" {
		t.Errorf("`git status` during the lane = %q, want empty (E13-03)", duringStatus)
	}
	// E13-06a + the runtime really read it.
	assertBriefRead(t, srv, smokeConfirmCode)
	// The repository's own rule is still visible to the agent: we never
	// touched AGENTS.md, so Hermes reads it as it always would.
	if !anyTextContains(srv, smokeRuleCode) {
		t.Errorf("the agent did not see the repository's own PROJECT_RULE_CODE — §8.4 M3's "+
			"first worry (원본 규칙이 사라진다) would be back. events: %s", allText(srv))
	}
	// SPIKE_05 §7.4: the instruction-file gate must not catch our file.
	for _, ev := range permissionTitles(srv) {
		if strings.Contains(strings.ToLower(ev), "protected agent-instruction") && strings.Contains(ev, brief.FileName) {
			t.Errorf("Hermes's instruction-file permission gate caught %s: %q", brief.FileName, ev)
		}
		t.Logf("permission request: %q", ev)
	}
	// E13-05: the agent's own AGENTS.md edit committed normally.
	assertWorktreeCleanAndCommitted(t, repo, wd, "AGENT NOTE")
	body, err := gitrepo.Run(wd, "show", "HEAD:AGENTS.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "AGENT NOTE") {
		t.Errorf("the agent's AGENTS.md edit is not in the commit — this is spike 5 §3.1's "+
			"silent loss (0/12 commits under skip-worktree): %q", body)
	}
	if strings.Contains(body, brief.MarkerStart) {
		t.Errorf("our brief ended up in the agent's commit (spike 5 §6.2 did this 4/4)")
	}
	// E13-06: our file and the exclude entry are gone.
	if _, err := os.Stat(filepath.Join(wd, brief.FileName)); !os.IsNotExist(err) {
		t.Errorf("%s survived the lane", brief.FileName)
	}
	if gitrepo.ExcludeHas(wd, brief.FileName) {
		t.Errorf("`.git/info/exclude` still lists %s", brief.FileName)
	}
	// E13-07: .gitignore untouched.
	if got, _ := os.ReadFile(filepath.Join(wd, ".gitignore")); string(got) != "out/\n" {
		t.Errorf(".gitignore changed to %q", got)
	}
}

func assertBriefRead(t *testing.T, srv *memServer, code string) {
	t.Helper()
	if !anyTextContains(srv, code) {
		t.Fatalf("the runtime never answered with %s — the brief did not reach it. events:\n%s", code, allText(srv))
	}
}

func assertWorktreeCleanAndCommitted(t *testing.T, repo, wd, marker string) {
	t.Helper()
	log, err := gitrepo.Run(wd, "log", "--oneline", "-5")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("git log:\n%s", log)
	status, _ := gitrepo.Run(wd, "status", "--porcelain")
	if status != "" {
		t.Errorf("`git status` after the lane = %q, want empty", status)
	}
	if src, _ := gitrepo.Run(repo, "status", "--porcelain"); src != "" {
		t.Errorf("the SOURCE repository is dirty: %q", src)
	}
	show, err := gitrepo.Run(wd, "show", "--stat", "--format=%s", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HEAD:\n%s", show)
	if !strings.Contains(show, "file") {
		t.Errorf("HEAD is not a commit of the agent's work: %q", show)
	}
	if n := gitrepo.CommitsAhead(wd, "colab/smoke/backend", "main"); n < 1 {
		t.Errorf("commits ahead of main = %d, want ≥ 1 — the agent's commit did not land "+
			"(spike 5 §3.1: `nothing to commit, working tree clean`)", n)
	}
	if b := gitIn(t, wd, "symbolic-ref", "--short", "HEAD"); b != "colab/smoke/backend" {
		t.Errorf("branch = %q, want colab/smoke/backend", b)
	}
}

func allText(srv *memServer) string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	var b strings.Builder
	for _, ev := range srv.events {
		b.WriteString(ev.Class + "/" + ev.Verb + "/" + ev.Outcome + " " + ev.ObjectRef + "\n")
		for k, v := range ev.Payload {
			b.WriteString("    " + k + "=" + trunc(v) + "\n")
		}
	}
	return b.String()
}

func anyTextContains(srv *memServer, want string) bool {
	return strings.Contains(allText(srv), want)
}

func permissionTitles(srv *memServer) []string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	var out []string
	for _, ev := range srv.events {
		if ev.Class == "tool" && ev.Verb == "permission" {
			out = append(out, ev.ObjectRef+" "+trunc(ev.Payload["title"]))
		}
	}
	return out
}

func trunc(v any) string {
	s := strings.TrimSpace(strings.ReplaceAll(fmt.Sprint(v), "\n", " | "))
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
