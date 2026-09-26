package brief

import "testing"

// harness v0.9.7: a `dir` folder is shared by the parallel lanes of one agent
// in one mission, so each lane writes its own brief file; a worktree checkout
// runs its agent's lanes one at a time and keeps COLAB_BRIEF.md.
func TestFileNameForIsPerLaneInADirFolder(t *testing.T) {
	a := FileNameFor("dir", "0123456789abcdef")
	b := FileNameFor("dir", "fedcba9876543210")
	if a != "COLAB_BRIEF-01234567.md" || b != "COLAB_BRIEF-fedcba98.md" || a == b {
		t.Errorf("dir brief names = %q / %q", a, b)
	}
	if got := FileNameFor("worktree", "0123456789abcdef"); got != FileName {
		t.Errorf("worktree brief name = %q, want %q", got, FileName)
	}
	if got := PointerTo("/w/COLAB_BRIEF-01234567.md"); got != PromptPointerPrefix+"/w/COLAB_BRIEF-01234567.md"+PromptPointerSuffix {
		t.Errorf("pointer = %q", got)
	}
}
