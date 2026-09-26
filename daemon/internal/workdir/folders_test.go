package workdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
)

// daemon-protocol v0.10.0 §4.1·§4.3·§6.1 — the daemon's half of the mission
// folder layout: use the server's path as given, mkdir -p `shared_path`,
// refuse anything outside the root, list all three trees, and after a gc
// remove the parents it left empty (only when empty).

func TestPrepareUsesServerPathAndMakesShared(t *testing.T) {
	root := t.TempDir()
	room := filepath.Join(root, "rooms", "게임-제작-3f2a91c0", "스네이크-8b11de02")
	b := contracts.TaskBundle{
		Task: contracts.BundleTask{SessionID: "s", LaneID: "lane-1", WorkID: "wk-1"},
		Workdir: contracts.BundleWorkdir{ID: "wd-1", Kind: "dir",
			Path: filepath.Join(room, "developer-0c7e5d19"), SharedPath: filepath.Join(room, "_shared")},
	}
	p, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if p != b.Workdir.Path {
		t.Errorf("cwd = %s, want the bundle's path %s", p, b.Workdir.Path)
	}
	if fi, err := os.Stat(b.Workdir.SharedPath); err != nil || !fi.IsDir() {
		t.Fatalf("_shared not created: %v", err)
	}
	// The name tag carries work_id and role (§6), and nothing is written into _shared.
	if m := readMarker(p); m.ID != "wd-1" || m.WorkID != "wk-1" || m.Role != "agent" {
		t.Errorf("marker = %+v", m)
	}
	if ents, _ := os.ReadDir(b.Workdir.SharedPath); len(ents) != 0 {
		t.Errorf("the daemon wrote into _shared: %v", ents)
	}
	// List: the agent folder by its tag, the _shared folder as role=shared.
	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Info{}
	for _, w := range list {
		got[w.Path] = w
	}
	if w := got[b.Workdir.Path]; w.ID != "wd-1" || w.WorkID != "wk-1" || w.Role != "agent" {
		t.Errorf("agent folder in List = %+v", w)
	}
	if w := got[b.Workdir.SharedPath]; w.Role != "shared" || w.ID != "" {
		t.Errorf("_shared in List = %+v", w)
	}
}

func TestPrepareRefusesOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for name, b := range map[string]contracts.TaskBundle{
		"path ..":       {Workdir: contracts.BundleWorkdir{ID: "x", Kind: "dir", Path: filepath.Join(root, "rooms", "..", "..", filepath.Base(outside))}},
		"shared ..":     {Workdir: contracts.BundleWorkdir{ID: "x", Kind: "dir", Path: filepath.Join(root, "rooms", "r", "m", "a"), SharedPath: outside}},
		"shared rel ..": {Workdir: contracts.BundleWorkdir{ID: "x", Kind: "dir", Path: filepath.Join(root, "rooms", "r", "m", "a"), SharedPath: "../escape"}},
	} {
		if _, err := Prepare(root, b); err == nil {
			t.Errorf("%s: Prepare accepted a path outside the workdir root", name)
		}
	}
	// A symlink under the root pointing out of it is outside too.
	if err := os.MkdirAll(filepath.Join(root, "rooms"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "rooms", "evil")); err != nil {
		t.Fatal(err)
	}
	b := contracts.TaskBundle{Workdir: contracts.BundleWorkdir{ID: "x", Kind: "dir", Path: filepath.Join(root, "rooms", "evil", "a")}}
	if _, err := Prepare(root, b); err == nil {
		t.Error("Prepare followed a symlink out of the root")
	}
	if _, err := os.Stat(filepath.Join(outside, "a")); err == nil {
		t.Error("a directory was created outside the root through the symlink")
	}
}

func TestOldServerBundleKeepsSessionsLayout(t *testing.T) {
	root := t.TempDir()
	p, err := Prepare(root, contracts.TaskBundle{Task: contracts.BundleTask{SessionID: "room-1", LaneID: "lane-1"}, Workdir: contracts.BundleWorkdir{Kind: "dir"}})
	if err != nil || p != filepath.Join(root, "sessions", "room-1", "lane-1") {
		t.Fatalf("old bundle → %s %v", p, err)
	}
}

func TestRemovePrunesOnlyEmptyParents(t *testing.T) {
	root := t.TempDir()
	room := filepath.Join(root, "rooms", "r-1")
	a := filepath.Join(room, "m-1", "a-1")
	b := filepath.Join(room, "m-1", "b-1")
	shared := filepath.Join(room, "m-1", "_shared")
	other := filepath.Join(room, "_room", "a-1")
	for _, d := range []string{a, b, shared, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(shared, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Removing a: m-1 still holds b and _shared → nothing above goes.
	if err := Remove(root, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(room, "m-1")); err != nil {
		t.Fatal("m-1 was removed while it still held folders")
	}
	for _, d := range []string{b, shared} {
		if err := Remove(root, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(room, "m-1")); !os.IsNotExist(err) {
		t.Errorf("m-1 left behind empty: %v", err)
	}
	if _, err := os.Stat(room); err != nil {
		t.Fatal("the room went while _room/a-1 was still there")
	}
	if err := Remove(root, other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(room); !os.IsNotExist(err) {
		t.Errorf("rooms/<room>/ left behind empty: %v", err)
	}
	// rooms/ and the root stay.
	if _, err := os.Stat(filepath.Join(root, "rooms")); err != nil {
		t.Error("rooms/ itself was removed")
	}
	// Old trees are never pruned.
	old := filepath.Join(root, "sessions", "s-1", "lane-1")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root, old); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", "s-1")); err != nil {
		t.Error("an old sessions/<room> parent was pruned — §4.3 prunes only under rooms/")
	}
}
