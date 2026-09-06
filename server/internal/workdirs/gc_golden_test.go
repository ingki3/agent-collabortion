//go:build p4golden

// Golden table for worktree preparation, brief-file pollution and workdir GC
// (EVAL E13, 17 rows) — PRD FR-6.4 (격리와 작업 공간 정리, M4), PRD §8.4
// (지시 파일이 워크트리를 오염시키지 않게 한다, M3·M6), contracts/openapi.yaml
// `checkRepo` · `listRuntimeWorkdirs` · `deleteWorkdir` (`workdir_dirty`,
// `workdir_quota_exceeded`) and contracts/daemon-protocol.md §4.3 `gc` · §6
// (보고 행 `gc: {status, reason}`, v0.7).
//
// WHAT THIS FILE PINS THAT IS EASY TO GET BACKWARDS
//
//   - GC has TWO independent green lights, not one. "보존 기한이 지났다" only
//     opens the question; the answer is yes only when (병합됨 AND 클린) or
//     (커밋 0 AND 클린). FR-6.4 M4 spells out why the tree condition cannot be
//     dropped: 시나리오 B submits a diff WITHOUT committing, so "커밋 없음" is
//     the NORMAL state there and a rule that reads commits alone deletes the
//     work it was written to protect.
//   - A blocked GC is not silence. FR-6.4 says 삭제하지 않고 **알린다** —
//     without the notification the Director never learns a directory is being
//     kept, and the quota fills with work nobody knows about.
//   - Deleting a worktree deletes the WORKTREE, never the branch (FR-6.4
//     "브랜치는 남긴다"). Branches are cheap and a wrong deletion is
//     unrecoverable.
//   - `container`·`none` do not wait for a retention window at all — they are
//     deleted the moment the session ends (E13-14·15). Running them through
//     the 14-day rule keeps disposable directories for two weeks.
//   - Hiding the brief file is TWO different mechanisms and picking by taste
//     breaks one of them: `.git/info/exclude` has no effect on a TRACKED file
//     (§8.4 M3), so a tracked `CLAUDE.md` needs `skip-worktree`. This is the
//     single most likely defect in P4 because both paths "look" the same in a
//     clean-checkout demo.
//   - Restoration removes the MARKER SECTION only. A whole-file restore is the
//     other easy shortcut and it silently deletes an edit the agent made as
//     part of its actual job (§8.4, E13-05).
//
// HOW THIS FILE FAILS TODAY. `workdirs.Record`/`ApplyGCReports` store what the
// daemon reports and close the §6 GC loop, and `sessions.issueGC` emits the
// command for `none`·`container` at session end. Nothing decides retention,
// nothing evaluates 병합·클린, and nothing enforces the disk quota.
// `daemon/internal/brief` already writes and strips the marker BLOCK, but its
// own doc comment says the tracked/untracked handling (skip-worktree /
// `.git/info/exclude`, §8.4 M3) is P4 — so §3's rows are the new part and they
// fail. Every hook below is nil until T-S9/T-D9 wire it.
//
// A MODULE BOUNDARY THE WIRING PR MUST DECIDE (T-D9 ask). `planBriefFile`
// describes daemon behaviour, but this table lives in the server module and Go
// forbids importing `daemon/internal/…` from here — the same wall P3a hit with
// acpfake. Two honest ways out, Lead's call: (a) T-D9 mirrors these rows in the
// daemon module against `brief` + the new git handling and leaves this hook
// deliberately unwired with a "NOT WIRED, see …" note, exactly as
// `cliFallbackArgs` did in the E9 table (PR #121); or (b) the brief planner
// moves to a shared, non-internal package. What must NOT happen is a
// server-side re-implementation wired to this hook: that is a shadow hook, and
// the table would then measure the adapter instead of the daemon.
//
// VOCABULARY. `hideMethod`, `restoreAction` and the GC reason codes are this
// table's own words for decisions the contracts describe in prose. The adapter
// maps them (P2_TASKS §0-8); the strings are not wire format.
package workdirs

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// caseNameP4 keeps the EVAL id in the test name, like the P2/P3 goldens.
func caseNameP4(eval, name string) string {
	out := make([]byte, 0, len(eval))
	for i := 0; i < len(eval); i++ {
		if eval[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, eval[i])
	}
	return string(out) + "_" + name
}

var (
	p4Session  = uuid.MustParse("11111111-0000-4000-8000-000000000001")
	p4AgentBE  = uuid.MustParse("22222222-0000-4000-8000-000000000001")
	p4AgentFE  = uuid.MustParse("22222222-0000-4000-8000-000000000002")
	p4AgentQA  = uuid.MustParse("22222222-0000-4000-8000-000000000003")
	p4Workdir1 = uuid.MustParse("33333333-0000-4000-8000-000000000001")
)

// ---------------------------------------------------------------------------
// 1. Repository validation before the session exists — E13-01
// ---------------------------------------------------------------------------

// repoCheck mirrors the contract's RepoCheck (openapi `checkRepo`): what the
// daemon found when the wizard asked about `repo_path`.
type repoCheck struct {
	Exists        bool
	IsGit         bool
	Clean         bool
	DefaultBranch string
	RemoteURL     string
}

// repoVerdict is what the wizard does with it.
type repoVerdict struct {
	// OK is the contract's `ok`. False does NOT mean an error response —
	// `checkRepo` answers 200 either way and the form shows the reason.
	OK bool
	// FormBlocked is E13-01's "폼에서 차단": the wizard may not advance.
	FormBlocked bool
	// Problems is the reason list the form prints. Never empty when blocked —
	// a disabled Next button with no explanation is the bug this row exists
	// to prevent.
	Problems []string
	// HTTPStatus is the status `checkRepo` answered with.
	HTTPStatus int
}

var checkRepoVerdict func(c repoCheck) repoVerdict

func TestRepoValidationGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if checkRepoVerdict == nil {
			t.Fatalf("unimplemented: worktree repository validation (FR-2.1, E13-01, openapi " +
				"checkRepo). T-S9 must wire `checkRepoVerdict` — see the P4a hand-off report " +
				"'required API'")
		}
	}

	t.Run(caseNameP4("E13-01", "a_valid_clean_repository_lets_the_wizard_continue"), func(t *testing.T) {
		must(t)
		v := checkRepoVerdict(repoCheck{
			Exists: true, IsGit: true, Clean: true,
			DefaultBranch: "main", RemoteURL: "git@x:app.git",
		})
		if !v.OK || v.FormBlocked {
			t.Errorf("ok = %t blocked = %t, want ok and not blocked", v.OK, v.FormBlocked)
		}
	})

	t.Run(caseNameP4("E13-01", "a_missing_or_dirty_repository_blocks_the_form_with_a_reason"), func(t *testing.T) {
		must(t)
		for _, tc := range []struct {
			name string
			in   repoCheck
		}{
			{"path does not exist", repoCheck{Exists: false}},
			{"not a git repository", repoCheck{Exists: true, IsGit: false}},
			{"working tree dirty", repoCheck{Exists: true, IsGit: true, Clean: false, DefaultBranch: "main"}},
		} {
			v := checkRepoVerdict(tc.in)
			if v.OK {
				t.Errorf("%s: ok = true, want false (E13-01 데몬이 존재·클린·기본 브랜치를 검증한다)", tc.name)
			}
			if !v.FormBlocked {
				t.Errorf("%s: the wizard advanced — a session created on an unusable repository "+
					"fails at the first `git worktree add`, after the person has filled in six steps", tc.name)
			}
			if len(v.Problems) == 0 {
				t.Errorf("%s: problems = [], want the reason the form prints next to the disabled "+
					"button (openapi RepoCheck.problems)", tc.name)
			}
			if v.HTTPStatus != 200 {
				t.Errorf("%s: status = %d, want 200 — `ok: false` is an ANSWER, not an error "+
					"(openapi checkRepo: \"`ok: false`여도 200\")", tc.name, v.HTTPStatus)
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 2. Worktree preparation — E13-02, E13-08
// ---------------------------------------------------------------------------

// worktreeRequest is one lane starting under `worktree` isolation.
type worktreeRequest struct {
	SessionSlug string // the session token used in the branch name
	AgentSlug   string // the agent token used in the branch name
	AgentID     uuid.UUID
	// ExistingForAgent is the worktree this agent already has in this
	// session, empty when it has none.
	ExistingForAgent string
}

// worktreePlan is what the daemon is told to do.
type worktreePlan struct {
	Branch string
	Path   string
	// Created is false when an existing worktree is reused (에이전트당 1개).
	Created bool
	// BaseBranch is what the worktree is cut from.
	BaseBranch string
}

var planWorktree func(r worktreeRequest) worktreePlan

func TestWorktreePreparationGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if planWorktree == nil {
			t.Fatalf("unimplemented: worktree preparation (FR-6.4 표, E13-02). T-D9 owns the " +
				"`git worktree add`; T-S9/T-D9 must wire `planWorktree` — see the P4a hand-off " +
				"report 'required API'")
		}
	}

	t.Run(caseNameP4("E13-02", "the_branch_is_colab_session_agent"), func(t *testing.T) {
		must(t)
		p := planWorktree(worktreeRequest{SessionSlug: "S", AgentSlug: "backend", AgentID: p4AgentBE})

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

	t.Run(caseNameP4("E13-02", "a_second_lane_of_the_same_agent_reuses_the_same_worktree"), func(t *testing.T) {
		must(t)
		p := planWorktree(worktreeRequest{
			SessionSlug: "S", AgentSlug: "backend", AgentID: p4AgentBE,
			ExistingForAgent: "/w/S/backend",
		})
		if p.Created {
			t.Error("a second worktree for the same agent — FR-6.4/C3 bind ONE workdir per agent " +
				"under `worktree`, which is also why those lanes run sequentially (FR-6.3). " +
				"E16-B's verdict line counts 워크트리 2개, not 4")
		}
		if p.Path != "/w/S/backend" {
			t.Errorf("path = %q, want the existing /w/S/backend", p.Path)
		}
	})

	t.Run(caseNameP4("E13-02", "different_agents_get_different_worktrees"), func(t *testing.T) {
		must(t)
		be := planWorktree(worktreeRequest{SessionSlug: "S", AgentSlug: "backend", AgentID: p4AgentBE})
		fe := planWorktree(worktreeRequest{SessionSlug: "S", AgentSlug: "frontend", AgentID: p4AgentFE})
		if be.Path == fe.Path || be.Branch == fe.Branch {
			t.Errorf("backend %s@%s and frontend %s@%s collide — parallel lanes would write the "+
				"same checkout (E16-B 2단계)", be.Path, be.Branch, fe.Path, fe.Branch)
		}
	})
}

// bundleWorkdirPaths is what a lane's TaskBundle exposes as workdir
// (daemon-protocol §4.1 `workdir`). E13-08: QA reviews ARTIFACTS, so no other
// agent's checkout may appear.
var bundleWorkdirPaths func(sessionID, agentID uuid.UUID) []string

func TestWorktreeIsolationGolden(t *testing.T) {
	t.Run(caseNameP4("E13-08", "a_reviewers_bundle_names_only_its_own_workdir"), func(t *testing.T) {
		if bundleWorkdirPaths == nil {
			t.Fatalf("unimplemented: bundle workdir exposure (daemon-protocol §4.1, E13-08). " +
				"T-S9 must wire `bundleWorkdirPaths` — see the P4a hand-off report 'required API'")
		}
		qa := bundleWorkdirPaths(p4Session, p4AgentQA)
		if len(qa) != 1 {
			t.Fatalf("QA bundle names %d workdirs (%v), want exactly 1 — the review target is the "+
				"artifact, and handing QA the Frontend checkout lets a reviewer edit the code it "+
				"is reviewing (E13-08)", len(qa), qa)
		}
		fe := bundleWorkdirPaths(p4Session, p4AgentFE)
		for _, p := range qa {
			for _, other := range fe {
				if p == other {
					t.Errorf("QA workdir %q is Frontend's worktree (E13-08)", p)
				}
			}
		}
	})
}

// ---------------------------------------------------------------------------
// 3. The brief file must not pollute the checkout — E13-03..E13-07
// ---------------------------------------------------------------------------

// LEAD REWRITE AFTER SPIKE 5 (PR #153 → PRD v0.16 §8.4, harness v0.8.6 §10,
// EVAL v0.10 E13-03~06·06a). The P4a draft measured the v0.15 mechanism
// (marker append into the tracked instruction file + `skip-worktree`). Spike 5
// killed it: `skip-worktree` silently drops the agent's own edits from its
// commits (0/12) and blocks `switch`/`merge`. The contract now is 우회 B —
// the repository's instruction file (`AGENTS.md`/`CLAUDE.md`) is neither read
// nor written; the brief goes to an UNTRACKED `<workdir>/COLAB_BRIEF.md`
// registered in `.git/info/exclude`, and the first line of the hermes turn
// prompt points at its absolute path. These rows are the mirror T-D9 copies
// into the daemon module (decision (a) on PR #152); this server-side hook
// stays NOT WIRED (precedent: `cliFallbackArgs`, PR #121).

// briefFileCase is the state of the repository's own instruction file when a
// lane starts. Under 우회 B the plan must be the SAME for every row — that is
// the point: the original's state no longer matters.
type briefFileCase struct {
	Path string // the repository's instruction file: AGENTS.md · CLAUDE.md
	// Existed is whether the repository already had the file.
	Existed bool
	// Tracked is whether git tracks it.
	Tracked bool
	// AgentEditedAndCommitted is E13-05: the agent legitimately changed the
	// project's own instructions during its work and committed them.
	AgentEditedAndCommitted bool
	// Resumed is a second lane start on the same workdir (재개·재시도): the
	// brief file is already there from the previous attempt.
	Resumed bool
}

// briefFilePlan is what the daemon does at lane start and lane end.
type briefFilePlan struct {
	// --- lane start ---
	// BriefPath is where the brief was written, relative to the workdir.
	BriefPath string
	// InstructionFileTouched is true if the daemon read OR wrote the
	// repository's own instruction file. Must be false (§8.4 M3 v0.16).
	InstructionFileTouched bool
	// WrappedInMarkers is §8.4's 마커 구간 (still required inside our own file,
	// so the turn-prompt pointer and E12-11 byte-identity have an anchor).
	WrappedInMarkers bool
	// HideMethod: "git_exclude" | "skip_worktree" | "none".
	HideMethod string
	// SkipWorktreeBitsSet counts paths the daemon marked skip-worktree. 0.
	SkipWorktreeBitsSet int
	// GitStatusClean is the observable E13-03·04 demand — and under 우회 B it
	// is a REAL clean (no hidden index bits), which E13-05 then proves.
	GitStatusClean bool
	// Overwrote is the resumed case: the stale brief from the previous
	// attempt is replaced, never appended to.
	Overwrote bool

	// --- lane end ---
	// RestoreAction: "delete_file" | "remove_markers" | "restore_original".
	RestoreAction string
	// UnhideAction: "exclude_unregister" | "no_skip_worktree" | "none".
	UnhideAction string
	// AgentCommitSucceeded is E13-05: `git commit` of the agent's edit to the
	// instruction file reports `1 file changed`, not `nothing to commit`.
	AgentCommitSucceeded bool
	// BriefInAgentCommit is E13-05's 커밋에 브리프 섞이지 않음.
	BriefInAgentCommit bool
	// InstructionFileIsAgentVersion: after the lane the instruction file is
	// exactly what the agent committed (no restore ran over it).
	InstructionFileIsAgentVersion bool
	// BriefFileRemains must be false at the end (E13-06).
	BriefFileRemains bool
	// ExcludeEntryRemains is E13-06's 등록 해제 (must be false at the end).
	ExcludeEntryRemains bool
	// GitStatusCleanAfter closes the loop: the repository is as we found it,
	// plus whatever the agent committed.
	GitStatusCleanAfter bool

	// TouchedGitignore is §8.4's last bullet: `.gitignore` belongs to the
	// repository and we never write it (E13-07).
	TouchedGitignore bool
}

// turnPromptPlan is E13-06a: the hermes turn prompt's first line.
type turnPromptPlan struct {
	// FirstLine of the turn prompt handed to `session/prompt`.
	FirstLine string
	// BriefAbsPath the daemon resolved for this lane.
	BriefAbsPath string
}

// NOT WIRED in the server module (Lead decision on PR #152, option (a)):
// `planBriefFile` and `planTurnPrompt` describe daemon behaviour and Go
// forbids importing `daemon/internal/…` from here. T-D9 mirrors these rows
// in the daemon module against `daemon/internal/brief`; this table stays as
// the spec of record and reports "unimplemented" on purpose. Do NOT fill it
// with a server-side re-implementation (그림자 훅).
var planBriefFile func(c briefFileCase) briefFilePlan
var planTurnPrompt func(workdirAbs string) turnPromptPlan

func mustPlanBrief(t *testing.T, c briefFileCase) briefFilePlan {
	t.Helper()
	if planBriefFile == nil {
		t.Fatalf("unimplemented (NOT WIRED here by design): brief-file pollution prevention " +
			"(§8.4 M3·M6 v0.16, E13-03~07) lives in the daemon — T-D9 mirrors this table in " +
			"the daemon module; see the PR #152 Lead comment")
	}
	return planBriefFile(c)
}

func TestBriefFilePollutionGolden(t *testing.T) {
	// The same plan for every original state — 우회 B does not branch on it.
	allCases := []briefFileCase{
		{Path: "AGENTS.md", Existed: true, Tracked: true},
		{Path: "AGENTS.md", Existed: true, Tracked: false},
		{Path: "AGENTS.md", Existed: false},
		{Path: "CLAUDE.md", Existed: true, Tracked: true},
	}

	t.Run(caseNameP4("E13-03", "a_tracked_instruction_file_is_left_alone_and_the_brief_is_an_untracked_excluded_file"), func(t *testing.T) {
		p := mustPlanBrief(t, briefFileCase{Path: "AGENTS.md", Existed: true, Tracked: true})

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

	t.Run(caseNameP4("E13-04", "the_plan_does_not_depend_on_the_original_state"), func(t *testing.T) {
		ref := mustPlanBrief(t, allCases[0])
		for _, c := range allCases[1:] {
			p := mustPlanBrief(t, c)
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

	t.Run(caseNameP4("E13-05", "the_agents_own_edit_to_the_instruction_file_commits_normally_and_carries_no_brief"), func(t *testing.T) {
		p := mustPlanBrief(t, briefFileCase{
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

	t.Run(caseNameP4("E13-06", "our_brief_file_is_deleted_and_the_exclude_entry_is_removed"), func(t *testing.T) {
		for _, c := range allCases {
			p := mustPlanBrief(t, c)
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

	t.Run(caseNameP4("E13-06", "a_resumed_lane_overwrites_the_stale_brief_instead_of_appending"), func(t *testing.T) {
		p := mustPlanBrief(t, briefFileCase{Path: "AGENTS.md", Existed: true, Tracked: true, Resumed: true})
		if !p.Overwrote {
			t.Error("on 재개·재시도 the previous attempt's COLAB_BRIEF.md is replaced — appending " +
				"would give the agent two briefs and break E12-11 byte identity (harness §10 v0.8.6)")
		}
	})

	t.Run(caseNameP4("E13-07", "gitignore_is_never_touched"), func(t *testing.T) {
		for _, c := range allCases {
			p := mustPlanBrief(t, c)
			if p.TouchedGitignore {
				t.Errorf("%+v: wrote `.gitignore` — it is a file the REPOSITORY owns and a commit "+
					"of our line ends up in the user's project history (§8.4, E13-07 diff 0)", c)
			}
		}
	})

	t.Run(caseNameP4("E13-06a", "the_hermes_turn_prompt_starts_by_pointing_at_the_brief_files_absolute_path"), func(t *testing.T) {
		if planTurnPrompt == nil {
			t.Fatal("unimplemented (NOT WIRED here by design): hermes turn-prompt pointer (§8.4 v0.16, E13-06a) — T-D9")
		}
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

// ---------------------------------------------------------------------------
// 4. GC judgement — E13-09..E13-15
// ---------------------------------------------------------------------------

// gcCase is one workdir the GC pass looks at. The clock is injected: every
// row is stated in elapsed time since the session ended, never wall time
// (EVAL 검증 칸 "unit + clock").
type gcCase struct {
	Isolation string // worktree | container | none
	// SessionStatus at the time of the pass.
	SessionStatus string // completed | cancelled | active
	// RetentionDays is the workspace's `workdir_retention_days` (default 14).
	RetentionDays int
	// SinceSessionEnd is how long ago the session reached its final state.
	SinceSessionEnd time.Duration

	// The daemon's last §6 git report for this directory.
	BranchMerged bool
	CommitsAhead int
	TreeDirty    bool
}

// gcVerdict is the server's decision. The DAEMON never decides
// (daemon-protocol §6: "GC 판정은 서버가 한다 … 데몬은 스스로 지우지 않는다").
type gcVerdict struct {
	// Delete is whether a `gc` command is issued for this workdir.
	Delete bool
	// DeleteBranch must never be true (FR-6.4 "브랜치는 남긴다").
	DeleteBranch bool
	// NotifyDirector is FR-6.4's "삭제하지 않고 Director에게 알린다".
	NotifyDirector bool
	// Reason is why, in one machine token. Blocked rows must be told apart:
	// "unmerged commits" and "uncommitted changes" ask a person for different
	// things.
	Reason string
	// CommandIssued is whether a daemon `gc` command went out. It must track
	// Delete exactly — a decision nobody sends is not a deletion.
	CommandIssued bool
}

var judgeGC func(c gcCase) gcVerdict

func mustJudgeGC(t *testing.T, c gcCase) gcVerdict {
	t.Helper()
	if judgeGC == nil {
		t.Fatalf("unimplemented: workdir GC judgement (FR-6.4 정리(GC) 정책, E13-09~15, " +
			"daemon-protocol §6). T-S9 must wire `judgeGC` — see the P4a hand-off report " +
			"'required API'")
	}
	return judgeGC(c)
}

const day = 24 * time.Hour

func TestWorkdirGCGolden(t *testing.T) {
	t.Run(caseNameP4("E13-09", "inside_the_retention_window_nothing_is_deleted"), func(t *testing.T) {
		v := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 13*day + 23*time.Hour,
			BranchMerged:    true, CommitsAhead: 0, TreeDirty: false,
		})
		if v.Delete || v.CommandIssued {
			t.Errorf("delete = %t command = %t, want neither — 14일 보존은 병합 여부와 무관한 "+
				"1차 관문이다 (FR-6.4, E13-09 '삭제 0')", v.Delete, v.CommandIssued)
		}
		if v.NotifyDirector {
			t.Error("no notification either: nothing is wrong, the window simply has not passed")
		}
	})

	t.Run(caseNameP4("E13-10", "past_retention_merged_and_clean_deletes_the_worktree_and_keeps_the_branch"), func(t *testing.T) {
		v := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 14 * day,
			BranchMerged:    true, CommitsAhead: 2, TreeDirty: false,
		})
		if !v.Delete || !v.CommandIssued {
			t.Errorf("delete = %t command = %t, want both — merged + clean is the first of the two "+
				"green lights (FR-6.4)", v.Delete, v.CommandIssued)
		}
		if v.DeleteBranch {
			t.Fatal("the BRANCH was deleted. FR-6.4: 삭제는 워크트리만 하고 브랜치는 남긴다 — " +
				"branches are cheap and a wrong deletion is not recoverable")
		}
	})

	t.Run(caseNameP4("E13-11", "past_retention_no_commits_and_clean_deletes"), func(t *testing.T) {
		v := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 20 * day,
			BranchMerged:    false, CommitsAhead: 0, TreeDirty: false,
		})
		if !v.Delete {
			t.Error("커밋이 없고 작업 트리도 클린하다 = the second green light; an unmerged-but-empty " +
				"branch has nothing to lose (FR-6.4, E13-11)")
		}
		if v.DeleteBranch {
			t.Error("still worktree-only (FR-6.4)")
		}
	})

	t.Run(caseNameP4("E13-12", "unmerged_commits_block_the_delete_and_notify"), func(t *testing.T) {
		v := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 30 * day,
			BranchMerged:    false, CommitsAhead: 3, TreeDirty: false,
		})
		if v.Delete || v.CommandIssued {
			t.Fatal("unmerged commits live ONLY in this worktree's branch on this machine — " +
				"push is a non-goal (§2.2), so deleting here destroys the work (FR-6.4, E13-12)")
		}
		if !v.NotifyDirector {
			t.Error("Dir 알림 (E13-12): a directory kept forever with no notice is how the quota " +
				"fills with work nobody remembers")
		}
		if v.Reason == "" {
			t.Error("the notification must say WHY it was kept — the Director's next action " +
				"(merge) differs from E13-13's (commit or discard)")
		}
	})

	t.Run(caseNameP4("E13-13", "uncommitted_changes_block_the_delete_even_with_zero_commits"), func(t *testing.T) {
		v := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 30 * day,
			BranchMerged:    false, CommitsAhead: 0, TreeDirty: true,
		})
		if v.Delete {
			t.Fatal("THE 시나리오 B row. The agent submitted a diff artifact and never committed, " +
				"so '커밋 0' is the NORMAL state here — a rule that reads commits alone deletes " +
				"the whole feature (FR-6.4 M4, E13-13)")
		}
		if !v.NotifyDirector {
			t.Error("Dir 알림 (E13-13)")
		}
	})

	t.Run(caseNameP4("E13-13", "the_two_blocked_reasons_are_distinguishable"), func(t *testing.T) {
		unmerged := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 30 * day, CommitsAhead: 3,
		})
		dirty := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "completed", RetentionDays: 14,
			SinceSessionEnd: 30 * day, TreeDirty: true,
		})
		if unmerged.Reason == dirty.Reason {
			t.Errorf("both blocked rows report %q — E13-12 asks the Director to merge and E13-13 "+
				"asks them to commit or discard; one string cannot say both", unmerged.Reason)
		}
	})

	t.Run(caseNameP4("E13-14", "container_and_none_are_deleted_as_soon_as_the_session_completes"), func(t *testing.T) {
		for _, kind := range []string{"container", "none"} {
			v := mustJudgeGC(t, gcCase{
				Isolation: kind, SessionStatus: "completed", RetentionDays: 14,
				SinceSessionEnd: 0, TreeDirty: true, CommitsAhead: 5,
			})
			if !v.Delete {
				t.Errorf("%s: delete = false — 즉시 삭제, 아티팩트는 이미 서버에 (FR-6.4, E13-14). "+
					"The git columns are meaningless here; running these through the 14-day "+
					"worktree rule keeps disposable directories for two weeks", kind)
			}
		}
	})

	t.Run(caseNameP4("E13-15", "a_cancelled_session_collects_the_same_way"), func(t *testing.T) {
		for _, kind := range []string{"container", "none"} {
			v := mustJudgeGC(t, gcCase{
				Isolation: kind, SessionStatus: "cancelled", RetentionDays: 14, SinceSessionEnd: 0,
			})
			if !v.Delete {
				t.Errorf("%s: a cancelled session releases its directories too (E13-15)", kind)
			}
		}
	})

	// EVAL 제안 행 (E13-18): the negative control the table is missing. Every
	// row above ends the session first, so an implementation that keys
	// retention off `created_at` — or off nothing at all — passes E13-09~15
	// and deletes the workdir of a session that is still running.
	t.Run(caseNameP4("E13-18", "a_running_sessions_workdir_is_never_collected_however_old"), func(t *testing.T) {
		v := mustJudgeGC(t, gcCase{
			Isolation: "worktree", SessionStatus: "active", RetentionDays: 14,
			SinceSessionEnd: 90 * day,
			BranchMerged:    true, TreeDirty: false,
		})
		if v.Delete || v.CommandIssued {
			t.Error("retention counts from the session's END (FR-6.4 '세션 종료 후에도 기본 14일 " +
				"보존'). Collecting a live session's checkout deletes the files an agent is " +
				"editing right now [EVAL 제안 행]")
		}
		none := mustJudgeGC(t, gcCase{Isolation: "none", SessionStatus: "active", SinceSessionEnd: 90 * day})
		if none.Delete {
			t.Error("`none` deletes on completion, not on age — an active session keeps its " +
				"directory [EVAL 제안 행]")
		}
	})
}

// ---------------------------------------------------------------------------
// 5. Disk quota — E13-16
// ---------------------------------------------------------------------------

// quotaVerdict answers "may a new session be created right now?".
type quotaVerdict struct {
	Blocked bool
	// Code is the contract's Problem.code.
	Code string
	// DirectorAsked is FR-6.4's "Director에게 정리를 요청한다".
	DirectorAsked bool
	// HTTPStatus is what createSession answered.
	HTTPStatus int
}

var checkDiskQuota func(usedBytes int64, quotaGB int) quotaVerdict

func TestWorkdirQuotaGolden(t *testing.T) {
	must := func(t *testing.T) {
		t.Helper()
		if checkDiskQuota == nil {
			t.Fatalf("unimplemented: workdir disk quota (FR-6.4 마지막 bullet, E13-16, openapi " +
				"Problem `workdir_quota_exceeded`). T-S9 must wire `checkDiskQuota` — see the " +
				"P4a hand-off report 'required API'")
		}
	}
	const gb = int64(1) << 30

	t.Run(caseNameP4("E13-16", "at_or_above_the_quota_a_new_session_is_blocked"), func(t *testing.T) {
		must(t)
		v := checkDiskQuota(50*gb, 50)
		if !v.Blocked {
			t.Fatal("E13-16 says ≥, not > — a quota that only trips when exceeded lets the disk " +
				"fill to exactly full before anyone is told")
		}
		if v.Code != "workdir_quota_exceeded" {
			t.Errorf("code = %q, want workdir_quota_exceeded (openapi Problem.code 목록)", v.Code)
		}
		if !v.DirectorAsked {
			t.Error("Dir에게 정리 요청 (FR-6.4) — a blocked wizard with no cleanup path is a " +
				"dead end")
		}
	})

	t.Run(caseNameP4("E13-16", "below_the_quota_nothing_is_blocked"), func(t *testing.T) {
		must(t)
		if v := checkDiskQuota(49*gb, 50); v.Blocked {
			t.Errorf("blocked below the quota (%+v) — the limit exists to stop growth, not to "+
				"stop work", v)
		}
	})

	t.Run(caseNameP4("E13-16", "no_quota_configured_never_blocks"), func(t *testing.T) {
		must(t)
		// `workdir_disk_quota_gb` is nullable in the contract; 0 is this
		// table's spelling of "not set".
		if v := checkDiskQuota(900*gb, 0); v.Blocked {
			t.Errorf("blocked with no quota set (%+v) — the column is `[integer, 'null']` and a "+
				"null must not mean zero [EVAL 제안 행 E13-19]", v)
		}
	})
}

// ---------------------------------------------------------------------------
// 6. The GC command round trip — daemon-protocol §6 v0.7
// ---------------------------------------------------------------------------

// gcCommandPayload is what the server puts on the wire (§4.3 v0.7).
type gcCommandPayload struct {
	SessionID string
	// Workdirs is the v0.7 shape: the SERVER carries the paths.
	Workdirs []struct {
		ID   string
		Path string
	}
}

var buildGCCommand func(ids []uuid.UUID, paths []string) gcCommandPayload

func TestGCCommandGolden(t *testing.T) {
	t.Run(caseNameP4("E13-10", "the_gc_command_carries_the_paths_the_server_decided_on"), func(t *testing.T) {
		if buildGCCommand == nil {
			t.Fatalf("unimplemented: gc command payload (daemon-protocol §4.3 v0.7). T-S9 must " +
				"wire `buildGCCommand` — see the P4a hand-off report 'required API'")
		}
		p := buildGCCommand([]uuid.UUID{p4Workdir1}, []string{"/w/S/backend"})
		if len(p.Workdirs) != 1 {
			t.Fatalf("workdirs = %d, want 1", len(p.Workdirs))
		}
		if p.Workdirs[0].Path != "/w/S/backend" || p.Workdirs[0].ID != p4Workdir1.String() {
			t.Errorf("workdirs[0] = %+v, want id+path — 데몬은 uuid↔path 매핑을 가진 적이 없다; "+
				"an ids-only command makes the daemon fall back to EVERY lane workdir of the "+
				"session, which is not what the retention rules decided (§4.3 v0.7)", p.Workdirs[0])
		}
		if !strings.Contains(p.SessionID, "-") {
			t.Errorf("session_id = %q, want the session uuid (§4.3)", p.SessionID)
		}
	})
}
