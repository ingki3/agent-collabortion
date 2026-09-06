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

// E8-08 (daemon half) — when the server retries a lane on a different
// profile, the workdir and everything in it (artifacts included) must survive
// the switch. The daemon derives the path from session+lane only, so a bundle
// whose runtime_kind, model and attempt all changed still lands in the same
// folder. Choosing the fallback profile is the server's call (it owns
// attempts, tokens and the runtime_id pin); keeping the directory is ours.
func TestWorkdirSurvivesProfileSwitch(t *testing.T) {
	root := t.TempDir()
	first := contracts.TaskBundle{
		Task:    contracts.BundleTask{SessionID: "sess-A", LaneID: "lane-7", Attempt: 1},
		Profile: contracts.BundleProfile{RuntimeKind: contracts.RuntimeHermes, Model: "sonnet"},
		Workdir: contracts.BundleWorkdir{Kind: "dir", Reuse: true},
	}
	p, err := Prepare(root, first)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := filepath.Join(p, "artifacts")
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifacts, "draft.md"), []byte("draft"), 0o644); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Task.Attempt = 2
	second.Profile = contracts.BundleProfile{RuntimeKind: contracts.RuntimeClaudeCode, Model: "opus"}
	p2, err := Prepare(root, second)
	if err != nil {
		t.Fatal(err)
	}
	if p2 != p {
		t.Fatalf("workdir moved on profile switch: %s → %s (E8-08)", p, p2)
	}
	if b, err := os.ReadFile(filepath.Join(artifacts, "draft.md")); err != nil || string(b) != "draft" {
		t.Fatalf("artifact directory lost on profile switch: %v", err)
	}
}

// E8-11 — resuming a `preparing` attempt REUSES the workdir: no new directory,
// and nothing already in it is touched. FR-9.1 goes further — an orphan's
// changes are never deleted (E11-06) — so "reuse" here means literally the
// same path with its contents intact.
func TestPreparingResumeReusesTheWorkdir(t *testing.T) {
	root := t.TempDir()
	b := contracts.TaskBundle{
		Task:    contracts.BundleTask{ID: "t1", Attempt: 1, SessionID: "sess-A", LaneID: "lane-1"},
		Workdir: contracts.BundleWorkdir{Kind: "dir", Reuse: true},
	}
	first, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	half := filepath.Join(first, "part-one.md")
	if err := os.WriteFile(half, []byte("half written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	b.Task.Attempt = 2 // the re-queued attempt
	second, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("attempt 2 got %q, want the same workdir %q (E8-11)", second, first)
	}
	if got, err := os.ReadFile(half); err != nil || string(got) != "half written\n" {
		t.Fatalf("the resumed attempt lost the previous one's work: %q %v", got, err)
	}
	lanes, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(lanes) != 1 {
		t.Fatalf("workdirs = %d, want 1 — a second directory means the resume started over (E8-11)", len(lanes))
	}
}
