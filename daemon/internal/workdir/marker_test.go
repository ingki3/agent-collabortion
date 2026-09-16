// K-14 (daemon-protocol v0.8.3 §4.1·§6, T-D15): the bundle's `workdir.id`
// becomes the directory's name tag and every §6 row echoes it; the v0.7.3
// index is the fallback for a bundle with none.
package workdir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/gitrepo"
)

const wdID = "7c1d2e3f-0000-4000-8000-0000000000d1"

// A `worktree` bundle with an id: the tag is written into the checkout, the
// checkout and the source repository stay clean (E13-03~06 — the tag rides
// on .git/info/exclude, never .gitignore), the index gets NO record, and
// every row built afterwards — the lane-end/gc row (Describe) and the probe's
// full list (ListWorktrees, what a restarted daemon has) — carries the id.
func TestBundleIDBecomesTheCheckoutsNameTag(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	b := worktreeBundle(t, repo, WorktreePath(root, "sess-slug", "backend"))
	b.Workdir.ID = wdID
	path, err := PrepareWorktree(root, b)
	if err != nil {
		t.Fatal(err)
	}

	blob, err := os.ReadFile(filepath.Join(path, MarkerName))
	if err != nil {
		t.Fatalf("no name tag in the checkout: %v", err)
	}
	if got := strings.TrimSpace(string(blob)); got != `{"id":"`+wdID+`"}` {
		t.Errorf("tag = %q, want one line with the id only (§6 v0.8.3)", got)
	}
	if got := ReadMarker(path); got != wdID {
		t.Errorf("ReadMarker = %q, want %q", got, wdID)
	}
	if out, _ := gitrepo.Run(path, "status", "--porcelain"); out != "" {
		t.Errorf("`git status` in the checkout = %q, want empty — the tag must be excluded (E13-03)", out)
	}
	if out, _ := gitrepo.Run(repo, "status", "--porcelain"); out != "" {
		t.Errorf("the SOURCE repository is dirty: %q", out)
	}
	if !gitrepo.ExcludeHas(path, MarkerName) {
		t.Errorf(".git/info/exclude does not list %s", MarkerName)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf(".gitignore was touched — it belongs to the repository (E13-07)")
	}
	if _, ok := LookupWorkdir(root, path); ok {
		t.Errorf("an index record was written for a tagged directory — the index is the fallback for bundles WITHOUT an id")
	}

	if row := Describe(root, path, b.Task.SessionID); row.ID != wdID {
		t.Errorf("Describe row id = %q, want the bundle's %q", row.ID, wdID)
	}
	// The probe's full report after a restart: nothing in memory, only the
	// disk. The row is found by the scan and named by the tag.
	rows := ListWorktrees(root)
	if len(rows) != 1 || rows[0].Path != path {
		t.Fatalf("ListWorktrees = %+v, want the one checkout", rows)
	}
	if rows[0].ID != wdID {
		t.Errorf("full-report row id = %q, want %q (§6: 재시작 뒤에는 표식 파일을 읽는다)", rows[0].ID, wdID)
	}
	if rows[0].Git == nil || rows[0].Bytes <= 0 {
		t.Errorf("full-report row = %+v, want git and bytes on every row (v0.7.3)", rows[0])
	}
}

// The same for a `dir` lane, in the order the server actually produces
// (Lead T-S21 decision A): the FIRST attempt has no id — the index record is
// written and the row goes out on the pair — and the second attempt has one,
// which replaces the record with the tag.
func TestDirLaneFirstAttemptFallsBackThenSecondGetsTheTag(t *testing.T) {
	root := t.TempDir()
	b := contracts.TaskBundle{
		Task: contracts.BundleTask{
			SessionID: "9f2b4c1e-0000-4000-8000-000000000003",
			AgentID:   "1a3c5e70-0000-4000-8000-0000000000a3",
			LaneID:    "5b6d8f90-0000-4000-8000-0000000000b3",
		},
		Workdir: contracts.BundleWorkdir{Kind: "dir"},
	}
	path, err := Prepare(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if ReadMarker(path) != "" {
		t.Errorf("a tag was written from a bundle with no id")
	}
	rec, ok := LookupWorkdir(root, path)
	if !ok || rec.SessionID != b.Task.SessionID || rec.LaneID != b.Task.LaneID {
		t.Fatalf("index record = %+v (%v), want the pair for the first attempt", rec, ok)
	}
	rows, _ := List(root)
	if len(rows) != 1 || rows[0].ID != "" || rows[0].SessionID != b.Task.SessionID {
		t.Fatalf("first-attempt rows = %+v, want no id and the session", rows)
	}

	b.Task.Attempt = 2
	b.Workdir.ID = wdID
	b.Workdir.Path = path
	if _, err := Prepare(root, b); err != nil {
		t.Fatal(err)
	}
	if got := ReadMarker(path); got != wdID {
		t.Errorf("tag = %q after the second attempt, want %q", got, wdID)
	}
	if _, ok := LookupWorkdir(root, path); ok {
		t.Errorf("the index record survived the tag — the two are exclusive")
	}
	rows, _ = List(root)
	if len(rows) != 1 || rows[0].ID != wdID {
		t.Errorf("rows = %+v, want the one lane with the id", rows)
	}
	if row := Describe(root, path, b.Task.SessionID); row.ID != wdID || row.LaneID != "" {
		// Describe fills lane from the index only; the loop layers the
		// bundle's lane on top. What matters here is the id.
		t.Errorf("Describe = %+v, want id %s", row, wdID)
	}
}

// An older server sends no id at all (daemon-protocol §4.1: "id 가 없는 옛
// 서버 번들에서는 예전처럼 index 로"). The v0.7.3 behaviour is kept exactly:
// record in the index, no tag, and the row goes out on session_id·agent_id.
func TestOldServerBundleKeepsTheIndexFallback(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	b := worktreeBundle(t, repo, WorktreePath(root, "sess-slug", "backend"))
	path, err := PrepareWorktree(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, MarkerName)); !os.IsNotExist(err) {
		t.Errorf("a tag was written from a bundle with no id")
	}
	rec, ok := LookupWorkdir(root, path)
	if !ok || rec.SessionID != b.Task.SessionID || rec.AgentID != b.Task.AgentID {
		t.Fatalf("index record = %+v (%v), want the pair (v0.7.3 fallback)", rec, ok)
	}
	rows := ListWorktrees(root)
	if len(rows) != 1 || rows[0].ID != "" || rows[0].SessionID != b.Task.SessionID || rows[0].AgentID != b.Task.AgentID {
		t.Errorf("rows = %+v, want no id and the pair from the index", rows)
	}
}

// A tag the disk lost heals on the next preparation, and a tag from a
// previous bundle follows the new one (the bundle is what §4.1 said).
func TestNameTagIsRewrittenOnEveryPreparation(t *testing.T) {
	root, repo := t.TempDir(), tempGitRepo(t)
	b := worktreeBundle(t, repo, WorktreePath(root, "sess-slug", "backend"))
	b.Workdir.ID = wdID
	path, err := PrepareWorktree(root, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, MarkerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareWorktree(root, b); err != nil {
		t.Fatal(err)
	}
	if got := ReadMarker(path); got != wdID {
		t.Errorf("tag after re-preparation = %q, want %q (healed)", got, wdID)
	}
	if out, _ := gitrepo.Run(path, "status", "--porcelain"); out != "" {
		t.Errorf("`git status` = %q after re-preparation, want empty", out)
	}
}

// The tag is unreadable when it is not one JSON line with an id: a row with
// an invented id is worse than the pair fallback.
func TestGarbledNameTagReadsAsNone(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, MarkerName), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadMarker(dir); got != "" {
		t.Errorf("ReadMarker(garbled) = %q, want empty", got)
	}
	if got := ReadMarker(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("ReadMarker(missing) = %q, want empty", got)
	}
}
