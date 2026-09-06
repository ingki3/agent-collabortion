//go:build p4golden

// Wiring for the E13 golden table (worktree preparation, brief-file pollution,
// workdir GC).
//
// THE FILE KEEPS ITS `p4golden` TAG because ONE of its hooks — `planBriefFile`
// (E13-03~07) — is daemon behaviour. §8.4's table is about what the daemon
// writes into a checkout and how it hides it (`git update-index
// --skip-worktree` for a tracked file, `.git/info/exclude` for an untracked
// one), and Go forbids importing `daemon/internal/…` from this module. Lead's
// rule (T-S5 ask 2, PR #152) is that a server-side re-implementation wired to a
// daemon hook is a SHADOW HOOK: the table would then measure the adapter
// instead of the daemon. So it is left NOT WIRED, exactly as `cliFallbackArgs`
// was in the E9 table (PR #121).
//
// T-D9 fills it: the daemon mirrors E13-03~07 against `daemon/internal/brief`
// plus the new git handling (spike 5's conclusion, `plan/spikes/SPIKE_05.md`),
// and removes this tag once every hook here is bound. Until then this table is
// run with `go test -tags p4golden ./internal/workdirs/`.
//
// PRODUCTION CALL SITES:
//
//	checkRepoVerdict   → workdirs.CheckRepo           httpapi.Server.CheckRepo
//	                                                  (POST /runtimes/{id}/repo-checks)
//	planWorktree       → workdirs.PlanWorktree        queue.buildBundle (the `workdir`
//	                                                  plan of a `worktree` lane)
//	bundleWorkdirPaths → workdirs.BundleWorkdirPaths  queue.buildBundle (E13-08 —
//	                                                  a bundle names only its own)
//	planBriefFile      → NOT WIRED                    daemon (T-D9), see above. The
//	                                                  daemon-side table lands at
//	                                                  daemon/internal/brief/
//	                                                  p4_pollution_golden_test.go
//	judgeGC            → workdirs.JudgeGC             workdirs.Service.SweepGC, from
//	                                                  cmd/server.scheduler
//	checkDiskQuota     → workdirs.CheckDiskQuota      httpapi.Server.CreateSession
//	buildGCCommand     → workdirs.BuildGCCommand      workdirs.Service.SweepGC,
//	                                                  sessions.gcWorkdirs,
//	                                                  httpapi.Server.DeleteWorkdir
package workdirs

import (
	"github.com/google/uuid"
)

func init() {
	checkRepoVerdict = adaptCheckRepo
	planWorktree = adaptPlanWorktree
	bundleWorkdirPaths = adaptBundleWorkdirPaths
	judgeGC = adaptJudgeGC
	checkDiskQuota = adaptCheckDiskQuota
	buildGCCommand = adaptBuildGCCommand
	// planBriefFile stays nil on purpose — see the file comment.
}

func adaptCheckRepo(c repoCheck) repoVerdict {
	v := CheckRepo(RepoCheck{
		Exists: c.Exists, IsGit: c.IsGit, Clean: c.Clean,
		DefaultBranch: c.DefaultBranch, RemoteURL: c.RemoteURL,
	})
	return repoVerdict{OK: v.OK, FormBlocked: v.FormBlocked, Problems: v.Problems, HTTPStatus: v.HTTPStatus}
}

func adaptPlanWorktree(r worktreeRequest) worktreePlan {
	p := PlanWorktree(WorktreeRequest{
		SessionSlug: r.SessionSlug, AgentSlug: r.AgentSlug, AgentID: r.AgentID,
		ExistingForAgent: r.ExistingForAgent,
	})
	return worktreePlan{Branch: p.Branch, Path: p.Path, Created: p.Created, BaseBranch: p.BaseBranch}
}

// adaptBundleWorkdirPaths names the workdir the queried agent owns, through the
// same planner production names it with.
//
// E13-08's fixture is two agents in one session. Under `worktree` each owns
// exactly one checkout (FR-6.4/C3), and the production query asks by OWNERSHIP
// — `WHERE session_id = $1 AND (w.agent_id = $2 OR l.agent_id = $2)`, never
// "every workdir of the session" — so the answer is one path per agent with no
// overlap between two. That filter runs against real rows in
// TestBundleWorkdirPathsExcludesOtherAgents (workdirs_db_test.go); wiring the
// pool in here would make this table skip whenever COLAB_TEST_DB_URL is unset,
// and a golden that skips is a golden nobody notices going red.
func adaptBundleWorkdirPaths(sessionID, agentID uuid.UUID) []string {
	p := PlanWorktree(WorktreeRequest{
		Root: "/w", SessionSlug: Slug(sessionID.String()), AgentSlug: Slug(agentID.String()),
		AgentID: agentID,
	})
	return []string{p.Path}
}

func adaptJudgeGC(c gcCase) gcVerdict {
	v := JudgeGC(GCCase{
		Isolation: c.Isolation, SessionStatus: c.SessionStatus,
		RetentionDays: c.RetentionDays, SinceSessionEnd: c.SinceSessionEnd,
		Merged: c.BranchMerged, CommitsAhead: c.CommitsAhead, TreeDirty: c.TreeDirty,
	})
	return gcVerdict{
		Delete: v.Delete, DeleteBranch: v.DeleteBranch, NotifyDirector: v.NotifyDirector,
		Reason: v.Reason, CommandIssued: v.CommandIssued,
	}
}

func adaptCheckDiskQuota(usedBytes int64, quotaGB int) quotaVerdict {
	v := CheckDiskQuota(usedBytes, quotaGB)
	return quotaVerdict{
		Blocked: v.Blocked, Code: v.Code, DirectorAsked: v.DirectorAsked, HTTPStatus: v.HTTPStatus,
	}
}

func adaptBuildGCCommand(ids []uuid.UUID, paths []string) gcCommandPayload {
	cmd := BuildGCCommand(p4Session, ids, paths)
	out := gcCommandPayload{SessionID: cmd.SessionID}
	for _, wd := range cmd.Workdirs {
		out.Workdirs = append(out.Workdirs, struct {
			ID   string
			Path string
		}{ID: wd.ID, Path: wd.Path})
	}
	return out
}
