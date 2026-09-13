package workdirs

import (
	"testing"

	"github.com/google/uuid"
)

// S-65 (the gc half of S-62): a stored relative path never reaches the wire.
// The daemon absolutises against its own CWD, and for a gc command that is an
// `rm -rf` inside the user's repository. The row is dropped from BOTH lists —
// an id with no path makes a v0.6 daemon collect every lane workdir of the
// session — and returned so the caller can say so.
func TestBuildGCCommandDropsRelativePaths(t *testing.T) {
	sess := uuid.New()
	abs, rel, empty := uuid.New(), uuid.New(), uuid.New()
	cmd, skipped := BuildGCCommand(sess, []uuid.UUID{abs, rel, empty},
		[]string{"/home/u/.colab/worktrees/s/lead", "worktrees/s/qa", ""})
	if len(cmd.Workdirs) != 1 || cmd.Workdirs[0].ID != abs.String() || cmd.Workdirs[0].Path != "/home/u/.colab/worktrees/s/lead" {
		t.Fatalf("workdirs = %+v, want only the absolute row", cmd.Workdirs)
	}
	if len(cmd.WorkdirIDs) != 1 || cmd.WorkdirIDs[0] != abs.String() {
		t.Fatalf("workdir_ids = %v, want only the absolute row's id (an id without a path is the v0.6 fallback)", cmd.WorkdirIDs)
	}
	if len(skipped) != 2 || skipped[0] != rel || skipped[1] != empty {
		t.Fatalf("skipped = %v, want the relative and the empty row", skipped)
	}
	// Nothing absolute → nothing to send; the caller must not queue this.
	cmd, skipped = BuildGCCommand(sess, []uuid.UUID{rel}, []string{"w/x"})
	if len(cmd.Workdirs) != 0 || len(cmd.WorkdirIDs) != 0 || len(skipped) != 1 {
		t.Fatalf("all-relative: cmd=%+v skipped=%v", cmd, skipped)
	}
}
