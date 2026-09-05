package workdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
)

func TestPrepareNoneCreatesLaneFolderAndReuses(t *testing.T) {
	root := t.TempDir()
	b := contracts.TaskBundle{Task: contracts.BundleTask{SessionID: "s/1", LaneID: "lane-1"}, Workdir: contracts.BundleWorkdir{Kind: "dir", Reuse: true}}
	p, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if p != filepath.Join(root, "sessions", "s_1", "lane-1") {
		t.Fatalf("path %s", p)
	}
	os.WriteFile(filepath.Join(p, "left-by-orphan.txt"), []byte("x"), 0o644)
	b.Workdir.Reuse = false
	p2, err := Prepare(root, b)
	if err != nil || p2 != p {
		t.Fatalf("%s %v", p2, err)
	}
	if _, err := os.Stat(filepath.Join(p, "left-by-orphan.txt")); err != nil {
		t.Fatal("orphan changes were deleted (E11-06)")
	}
	list, err := List(root)
	if err != nil || len(list) != 1 || list[0].Bytes != 1 || list[0].LaneID != "lane-1" {
		t.Fatalf("list %+v %v", list, err)
	}
}

func TestPrepareExplicitPathAndWorktreeUnsupported(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "custom")
	p, err := Prepare(root, contracts.TaskBundle{Workdir: contracts.BundleWorkdir{Kind: "dir", Path: explicit}})
	if err != nil || p != explicit {
		t.Fatalf("%s %v", p, err)
	}
	if _, err := Prepare(root, contracts.TaskBundle{Workdir: contracts.BundleWorkdir{Kind: "worktree"}}); err == nil {
		t.Fatal("worktree should be unsupported in P1")
	}
}
