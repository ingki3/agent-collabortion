package workdirs

import "testing"

// TestPlanWorktreeIgnoresARelativeStoredPath is S-62 at the place the path is
// made. `ExistingForAgent` is the only remaining way a relative string could
// reach the wire after S-55: it comes from a workdir row, and a row written
// before migration 0019 can hold one.
//
// "THE PATH IS ABSOLUTE OR IT IS NOTHING" — a stored relative path is not a
// candidate for reuse, so the plan is made from the root like any first lane.
func TestPlanWorktreeIgnoresARelativeStoredPath(t *testing.T) {
	got := PlanWorktree(WorktreeRequest{
		Root: "/Users/x/.colab", SessionSlug: "s", AgentSlug: "backend",
		ExistingForAgent: "S/backend", BaseBranch: "main",
	})
	want := "/Users/x/.colab/" + WorktreesDir + "/s/backend"
	if got.Path != want {
		t.Errorf("path = %q, want %q — a relative stored path is absolutised by the daemon against "+
			"its own CWD, which puts the checkout inside the user's repository", got.Path, want)
	}
	if !got.Created {
		t.Errorf("created = false — nothing was reused, so the daemon must be told to make it")
	}
}

// TestPlanWorktreeWithNoRootAndARelativeStoredPathNamesNothing is the same rule
// where it costs something: with no probe there is no material for an absolute
// path, and the relative row must not be used as a fallback. The caller
// (queue.buildBundle) turns the empty path into a refusal.
func TestPlanWorktreeWithNoRootAndARelativeStoredPathNamesNothing(t *testing.T) {
	got := PlanWorktree(WorktreeRequest{
		SessionSlug: "s", AgentSlug: "backend", ExistingForAgent: "S/backend",
	})
	if got.Path != "" {
		t.Errorf("path = %q, want \"\" — 조용한 상대 경로보다 시끄러운 실패가 낫다", got.Path)
	}
}

// TestPlanWorktreeStillReusesAnAbsoluteStoredPath keeps FR-6.4/C3: one worktree
// per agent, reused across that agent's lanes.
func TestPlanWorktreeStillReusesAnAbsoluteStoredPath(t *testing.T) {
	got := PlanWorktree(WorktreeRequest{
		Root: "/Users/x/.colab", SessionSlug: "s", AgentSlug: "backend",
		ExistingForAgent: "/somewhere/else/backend",
	})
	if got.Path != "/somewhere/else/backend" || got.Created {
		t.Errorf("plan = %+v, want the existing absolute checkout reused (C3)", got)
	}
}
