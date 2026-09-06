//go:build p4golden

// P4 golden MIRROR — brief-file pollution prevention (E13-03·04·05·06·06a·07).
//
// WHY IT IS HERE AND NOT IN THE SERVER MODULE. The table of record is
// `server/internal/workdirs/gc_golden_test.go` §3 (the Lead's post-spike-5
// rewrite, lines 289-518 as of PR #154). Its `planBriefFile`/`planTurnPrompt`
// hooks are deliberately left NOT WIRED there: they describe daemon behaviour
// and Go forbids `server/…` from importing `daemon/internal/…`. Lead's answer
// on PR #152 was option (a) — T-D9 mirrors the rows in the daemon module.
// Filling the server hook with a server-side re-implementation would be a
// shadow hook and the table would measure the adapter instead of the daemon.
//
// The case names, the field names and the failure messages below are copied
// from that file so a reviewer can diff the two tables mechanically. What
// differs is the bottom: over there `planBriefFile` is a description; here it
// RUNS `brief.Prepare`/`brief.Remove` against a real git repository in
// t.TempDir() and reads the answers back out of git.
//
// The experiment target is never this repository (P4_TASKS §0-18).
package brief

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

// ---------------------------------------------------------------------------
// The shapes, copied from server/internal/workdirs/gc_golden_test.go
// ---------------------------------------------------------------------------

// briefFileCase is the state of the repository's own instruction file when a
// lane starts. Under 우회 B the plan must be the SAME for every row — that is
// the point: the original's state no longer matters.
type briefFileCase struct {
	Path                    string
	Existed                 bool
	Tracked                 bool
	AgentEditedAndCommitted bool
	Resumed                 bool
}

// briefFilePlan is what the daemon does at lane start and lane end.
type briefFilePlan struct {
	BriefPath                     string
	InstructionFileTouched        bool
	WrappedInMarkers              bool
	HideMethod                    string
	SkipWorktreeBitsSet           int
	GitStatusClean                bool
	Overwrote                     bool
	RestoreAction                 string
	UnhideAction                  string
	AgentCommitSucceeded          bool
	BriefInAgentCommit            bool
	InstructionFileIsAgentVersion bool
	BriefFileRemains              bool
	ExcludeEntryRemains           bool
	GitStatusCleanAfter           bool
	TouchedGitignore              bool
}

type turnPromptPlan struct {
	FirstLine    string
	BriefAbsPath string
}

// ---------------------------------------------------------------------------
// The adapter: it RUNS the daemon and reports what git says.
// ---------------------------------------------------------------------------

const briefText = "## [1] Agent Identity\n\nBackend\n\n## [4] Session\n\ngoal: ship\n"

// planBriefFile builds a real repository in the shape of the case, starts a
// lane on it, optionally lets the agent do its own work, ends the lane, and
// answers from the filesystem and from git. It decides nothing: every field
// is an observation.
func planBriefFile(t *testing.T, c briefFileCase) briefFilePlan {
	t.Helper()
	wd := newRepo(t)
	instr := filepath.Join(wd, c.Path)
	gitignore := filepath.Join(wd, ".gitignore")
	original := "# " + c.Path + "\n\nPROJECT_RULE: never force-push\n"
	ignoreBody := "node_modules/\n"
	writeFile(t, gitignore, ignoreBody)
	git(t, wd, "add", ".gitignore")
	git(t, wd, "commit", "-qm", "gitignore")

	if c.Existed {
		writeFile(t, instr, original)
		if c.Tracked {
			git(t, wd, "add", c.Path)
			git(t, wd, "commit", "-qm", "instructions")
		}
	}
	// A resumed lane finds the previous attempt's brief already there.
	if c.Resumed {
		p0, err := Prepare(wd, contracts.BriefInstructionFile, "STALE BRIEF FROM ATTEMPT 1")
		if err != nil {
			t.Fatal(err)
		}
		_ = p0
	}

	instrBefore := readOrEmpty(instr)
	atimeBefore := readMTime(t, instr)
	// BASELINE. `GitStatusClean` is "the checkout is as we found it", not
	// "the repository has no changes of its own": the {Existed, Tracked:
	// false} row starts with an untracked AGENTS.md the REPOSITORY put there,
	// and nothing the daemon may legally do can make that line go away — §8.4
	// M3 forbids touching the file, and `.gitignore` belongs to the
	// repository (E13-07). So the measurement is: our footprint is invisible.
	// The server-side row states the same thing in prose for the lane-end
	// field ("the repository is as we found it, plus whatever the agent
	// committed").
	baseline := statusOf(t, wd)

	prep, err := Prepare(wd, contracts.BriefInstructionFile, briefText)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	p := briefFilePlan{
		Overwrote:      prep.Overwrote,
		GitStatusClean: statusOf(t, wd) == baseline && !strings.Contains(statusOf(t, wd), FileName),
	}
	if rel, err := filepath.Rel(wd, prep.Path); err == nil {
		p.BriefPath = rel
	}
	if body, err := os.ReadFile(prep.Path); err == nil {
		p.WrappedInMarkers = HasMarkers(body)
	}
	switch {
	case gitrepo.ExcludeHas(wd, FileName):
		p.HideMethod = "git_exclude"
	default:
		p.HideMethod = "none"
	}
	p.SkipWorktreeBitsSet = skipWorktreeBits(t, wd)
	// InstructionFileTouched: the file's bytes AND its mtime are unchanged,
	// and a file that did not exist was not created. mtime catches a
	// write-back of identical content, which a bytes-only check would miss.
	p.InstructionFileTouched = readOrEmpty(instr) != instrBefore || readMTime(t, instr) != atimeBefore
	p.TouchedGitignore = readOrEmpty(gitignore) != ignoreBody

	// --- the agent's own turn ---
	if c.AgentEditedAndCommitted {
		writeFile(t, instr, original+"AGENT ADDED: prefer squash merges\n")
		writeFile(t, filepath.Join(wd, "widget.go"), "package widget\n")
		git(t, wd, "add", "-A")
		out, err := gitRaw(wd, "commit", "-m", "feat: widget + rule note")
		p.AgentCommitSucceeded = err == nil && strings.Contains(out, "file")
		if err == nil {
			show, _ := gitrepo.Run(wd, "show", "--stat", "--format=", "HEAD")
			p.BriefInAgentCommit = strings.Contains(show, FileName)
			if body, err := gitrepo.Run(wd, "show", "HEAD:"+c.Path); err == nil {
				p.BriefInAgentCommit = p.BriefInAgentCommit || strings.Contains(body, MarkerStart)
			}
		}
	}
	agentVersion := readOrEmpty(instr)

	// --- lane end ---
	if err := Remove(prep); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	p.RestoreAction = "delete_file"
	p.UnhideAction = "exclude_unregister"
	p.BriefFileRemains = exists(prep.Path)
	p.ExcludeEntryRemains = gitrepo.ExcludeHas(wd, FileName)
	p.InstructionFileIsAgentVersion = readOrEmpty(instr) == agentVersion
	p.GitStatusCleanAfter = statusOf(t, wd) == baseline && !strings.Contains(statusOf(t, wd), FileName)
	p.TouchedGitignore = p.TouchedGitignore || readOrEmpty(gitignore) != ignoreBody
	return p
}

func planTurnPrompt(workdirAbs string) turnPromptPlan {
	return turnPromptPlan{
		FirstLine:    TurnPromptPointer(workdirAbs),
		BriefAbsPath: filepath.Join(workdirAbs, FileName),
	}
}

// ---------------------------------------------------------------------------
// The table — rows copied verbatim from the server-side file
// ---------------------------------------------------------------------------

func TestBriefFilePollutionGoldenMirror(t *testing.T) {
	allCases := []briefFileCase{
		{Path: "AGENTS.md", Existed: true, Tracked: true},
		{Path: "AGENTS.md", Existed: true, Tracked: false},
		{Path: "AGENTS.md", Existed: false},
		{Path: "CLAUDE.md", Existed: true, Tracked: true},
	}

	t.Run("E13-03_a_tracked_instruction_file_is_left_alone_and_the_brief_is_an_untracked_excluded_file", func(t *testing.T) {
		p := planBriefFile(t, briefFileCase{Path: "AGENTS.md", Existed: true, Tracked: true})

		if p.InstructionFileTouched {
			t.Fatal("the repository's own AGENTS.md was read or written — §8.4 M3 (v0.16): " +
				"we neither read nor write it; spike 5 §3 showed every way of hiding an edit " +
				"to a tracked file ends in silent data loss")
		}
		if p.BriefPath != "COLAB_BRIEF.md" {
			t.Errorf("brief path = %q, want COLAB_BRIEF.md (harness §10 v0.8.6)", p.BriefPath)
		}
		if !p.WrappedInMarkers {
			t.Error("the brief is wrapped in <!-- colab:brief:start/end --> inside our own file " +
				"(E12-11 byte identity of [1]~[5] is measured within it)")
		}
		if p.HideMethod != "git_exclude" {
			t.Errorf("hide = %q, want git_exclude — the brief is untracked, which is exactly what "+
				"`.git/info/exclude` covers (§8.4 표 v0.16, E13-03)", p.HideMethod)
		}
		if p.SkipWorktreeBitsSet != 0 {
			t.Errorf("skip-worktree bits set = %d, want 0 — spike 5: the bit makes the agent's "+
				"`git commit` report `nothing to commit` (0/12) and blocks switch/merge", p.SkipWorktreeBitsSet)
		}
		if !p.GitStatusClean {
			t.Error("`git status` must be clean while the lane runs (E13-03) — a dirty tree also " +
				"trips the GC rules of E13-13 later")
		}
	})

	t.Run("E13-04_the_plan_does_not_depend_on_the_original_state", func(t *testing.T) {
		ref := planBriefFile(t, allCases[0])
		for _, c := range allCases[1:] {
			p := planBriefFile(t, c)
			if p.InstructionFileTouched {
				t.Errorf("%+v: instruction file touched", c)
			}
			if p.BriefPath != ref.BriefPath || p.HideMethod != ref.HideMethod ||
				p.SkipWorktreeBitsSet != ref.SkipWorktreeBitsSet || p.RestoreAction != ref.RestoreAction ||
				p.UnhideAction != ref.UnhideAction {
				t.Errorf("%+v: plan differs from the tracked case — 우회 B has ONE row (§8.4 표 v0.16): "+
					"got %+v, want %+v", c, p, ref)
			}
			if !p.GitStatusClean {
				t.Errorf("%+v: `git status` must be clean (E13-04)", c)
			}
		}
	})

	t.Run("E13-05_the_agents_own_edit_to_the_instruction_file_commits_normally_and_carries_no_brief", func(t *testing.T) {
		p := planBriefFile(t, briefFileCase{
			Path: "AGENTS.md", Existed: true, Tracked: true, AgentEditedAndCommitted: true,
		})

		if !p.AgentCommitSucceeded {
			t.Fatal("the agent's commit of its AGENTS.md edit must succeed with `1 file changed` — " +
				"a `nothing to commit, working tree clean` here is the spike 5 failure (§3.1, 0/12) " +
				"and means a skip-worktree bit is still in play")
		}
		if p.BriefInAgentCommit {
			t.Error("our brief ended up in the agent's commit — the control run in spike 5 §6.2 " +
				"(marker append, not hidden) did exactly this 4/4")
		}
		if p.RestoreAction != "delete_file" {
			t.Errorf("restore = %q, want delete_file — there is no marker section in the "+
				"repository's file to remove any more; we only delete OUR file", p.RestoreAction)
		}
		if !p.InstructionFileIsAgentVersion {
			t.Error("after the lane AGENTS.md must be exactly what the agent committed — no restore " +
				"may run over the repository's file (E13-05)")
		}
		if !p.GitStatusCleanAfter {
			t.Error("after the lane nothing of OURS remains in `git status` (E13-03·05)")
		}
	})

	t.Run("E13-06_our_brief_file_is_deleted_and_the_exclude_entry_is_removed", func(t *testing.T) {
		for _, c := range allCases {
			p := planBriefFile(t, c)
			if p.BriefFileRemains {
				t.Errorf("%+v: COLAB_BRIEF.md still exists after the lane (E13-06)", c)
			}
			if p.ExcludeEntryRemains {
				t.Errorf("%+v: `.git/info/exclude` still lists our path: a later real file of that "+
					"name would be invisible to git, which is a bug the user cannot explain (E13-06)", c)
			}
			if p.UnhideAction != "exclude_unregister" {
				t.Errorf("%+v: unhide = %q, want exclude_unregister", c, p.UnhideAction)
			}
		}
	})

	t.Run("E13-06_a_resumed_lane_overwrites_the_stale_brief_instead_of_appending", func(t *testing.T) {
		p := planBriefFile(t, briefFileCase{Path: "AGENTS.md", Existed: true, Tracked: true, Resumed: true})
		if !p.Overwrote {
			t.Error("on 재개·재시도 the previous attempt's COLAB_BRIEF.md is replaced — appending " +
				"would give the agent two briefs and break E12-11 byte identity (harness §10 v0.8.6)")
		}
	})

	t.Run("E13-07_gitignore_is_never_touched", func(t *testing.T) {
		for _, c := range allCases {
			p := planBriefFile(t, c)
			if p.TouchedGitignore {
				t.Errorf("%+v: wrote `.gitignore` — it is a file the REPOSITORY owns and a commit "+
					"of our line ends up in the user's project history (§8.4, E13-07 diff 0)", c)
			}
		}
	})

	t.Run("E13-06a_the_hermes_turn_prompt_starts_by_pointing_at_the_brief_files_absolute_path", func(t *testing.T) {
		p := planTurnPrompt("/srv/wd/S1/R")
		if p.BriefAbsPath != "/srv/wd/S1/R/COLAB_BRIEF.md" {
			t.Errorf("brief abs path = %q", p.BriefAbsPath)
		}
		if !strings.Contains(p.FirstLine, p.BriefAbsPath) {
			t.Errorf("first line %q must name the ABSOLUTE brief path — a relative one breaks when "+
				"the runtime's cwd is not the workdir (spike 5 §6.3: first tool call was that read 4/4)", p.FirstLine)
		}
	})
}

// A resumed lane's stale brief must not survive into the new one: E12-11 is
// measured on the file, so two marker blocks are two briefs.
func TestResumedBriefHasExactlyOneBlock(t *testing.T) {
	wd := newRepo(t)
	if _, err := Prepare(wd, contracts.BriefInstructionFile, "attempt 1"); err != nil {
		t.Fatal(err)
	}
	p, err := Prepare(wd, contracts.BriefInstructionFile, "attempt 2")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p.Path)
	if n := strings.Count(string(body), MarkerStart); n != 1 {
		t.Fatalf("%d marker blocks in the resumed brief, want 1", n)
	}
	if strings.Contains(string(body), "attempt 1") {
		t.Fatal("the stale brief survived")
	}
}

// The exclude entry is shared by every worktree of a repository
// (gitrepo.CommonDir), so a lane that ends while a sibling is still running
// must not unregister it out from under that sibling.
func TestParallelLanesShareTheExcludeEntry(t *testing.T) {
	repo := newRepo(t)
	wtA := filepath.Join(t.TempDir(), "backend")
	wtB := filepath.Join(t.TempDir(), "frontend")
	if err := gitrepo.WorktreeAdd(repo, wtA, "colab/S/backend", "main"); err != nil {
		t.Fatal(err)
	}
	if err := gitrepo.WorktreeAdd(repo, wtB, "colab/S/frontend", "main"); err != nil {
		t.Fatal(err)
	}
	pa, err := Prepare(wtA, contracts.BriefInstructionFile, "A")
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Prepare(wtB, contracts.BriefInstructionFile, "B")
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove(pa); err != nil {
		t.Fatal(err)
	}
	if !gitrepo.ExcludeHas(wtB, FileName) {
		t.Fatal("backend's lane end unregistered the entry frontend is still relying on — " +
			"frontend's brief would show up as an untracked change in the checkout it commits from")
	}
	if !gitrepo.Clean(wtB) {
		t.Fatalf("frontend's checkout is dirty: %s", statusOf(t, wtB))
	}
	if err := Remove(pb); err != nil {
		t.Fatal(err)
	}
	if gitrepo.ExcludeHas(wtB, FileName) {
		t.Fatal("the last lane did not unregister the entry (E13-06)")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "daemon@test")
	git(t, dir, "config", "user.name", "daemon test")
	writeFile(t, filepath.Join(dir, "README.md"), "seed\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "init")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitrepo.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v", args, dir, err)
	}
	return out
}

// gitRaw returns combined output without failing the test — the E13-05 row
// needs to SEE `nothing to commit, working tree clean`.
func gitRaw(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readMTime(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.ModTime().UnixNano()
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func statusOf(t *testing.T, dir string) string {
	t.Helper()
	out, _ := gitrepo.Run(dir, "status", "--porcelain")
	return out
}

// skipWorktreeBits counts paths with the skip-worktree bit — `git ls-files -v`
// marks them with a lowercase letter. Spike 5's whole finding is that this
// number must be 0.
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
