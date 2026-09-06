package colab_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// `colab artifact submit --type diff` (PRD FR-4.3, §3 scenario B step 4,
// EVAL E16-B): the agent submits its OWN worktree's changes as one patch, and
// the reviewer reads the artifact instead of the worktree (FR-6.1).

// git runs a git command in dir with an environment that ignores the
// developer's own git configuration, so these tests read the same on any
// machine and in CI.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=colab", "GIT_AUTHOR_EMAIL=colab@example.com",
		"GIT_COMMITTER_NAME=colab", "GIT_COMMITTER_EMAIL=colab@example.com",
		"GIT_AUTHOR_DATE=2026-09-07T00:00:00+00:00", "GIT_COMMITTER_DATE=2026-09-07T00:00:00+00:00")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// baseRepo is a repository on `main` with one commit — what the daemon's
// `git worktree add` forks from.
func baseRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	write(t, dir, "a.txt", "one\n")
	git(t, dir, "add", "a.txt")
	git(t, dir, "commit", "-qm", "base")
	return dir
}

// scenarioBWorktree is the state E16-B step 3 finds an engineer's worktree
// in: an agent branch with a commit, something staged, something modified but
// not staged, and a file git does not track at all.
func scenarioBWorktree(t *testing.T) string {
	t.Helper()
	dir := baseRepo(t)
	git(t, dir, "checkout", "-q", "-b", "colab/S/frontend")
	write(t, dir, "a.txt", "one\ntwo committed\n")
	git(t, dir, "commit", "-qam", "committed work")
	write(t, dir, "staged.txt", "staged work\n")
	git(t, dir, "add", "staged.txt")
	write(t, dir, "a.txt", "one\ntwo committed\nthree unstaged\n")
	write(t, dir, "junk.log", "build noise\n")
	return dir
}

// One request, one patch: commits since the base, staged and unstaged
// changes together. The metadata lands in the two places the multipart body
// already has (description first line + `# colab-diff:` comment) because
// openapi submitArtifact defines exactly {name, type, file, description}.
func TestArtifactSubmitDiffGeneratesWorktreePatch(t *testing.T) {
	dir := scenarioBWorktree(t)
	s := clienttest.New(t)
	head := git(t, dir, "rev-parse", "--short", "HEAD")

	res, err := colab.ArtifactSubmit(context.Background(), newClient(t, s), colab.ArtifactSubmitArgs{
		Type: "diff", Description: "회원 탈퇴 화면", Workdir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Submissions) != 1 {
		t.Fatalf("submissions = %d", len(s.Submissions))
	}
	sub := s.Submissions[0]
	if sub.Fields["type"] != "diff" {
		t.Fatalf("type = %q", sub.Fields["type"])
	}
	// The name is the branch's last segment so a second submission from the
	// same worktree is version+1 (FR-4.3), not a second artifact.
	if sub.Fields["name"] != "frontend.diff" || sub.FileName != "frontend.diff" {
		t.Fatalf("name = %q / filename = %q", sub.Fields["name"], sub.FileName)
	}
	if sub.ContentType != "text/plain" {
		t.Fatalf("file part Content-Type = %q", sub.ContentType)
	}
	wantFirst := "diff colab/S/frontend@" + head + " vs main"
	desc := strings.SplitN(sub.Fields["description"], "\n", 2)
	if desc[0] != wantFirst {
		t.Fatalf("description first line = %q, want %q", desc[0], wantFirst)
	}
	if len(desc) != 2 || desc[1] != "회원 탈퇴 화면" {
		t.Fatalf("--description must follow on its own line: %q", sub.Fields["description"])
	}
	body := string(sub.Data)
	wantHeader := "# colab-diff: branch=colab/S/frontend base=main commit=" + head
	if first, _, _ := strings.Cut(body, "\n"); first != wantHeader {
		t.Fatalf("first body line = %q, want %q", first, wantHeader)
	}
	if strings.Count(body, colab.DiffHeaderPrefix) != 1 {
		t.Fatalf("expected exactly one %s line:\n%s", colab.DiffHeaderPrefix, body)
	}
	for _, want := range []string{"+two committed", "+three unstaged", "+staged work", "staged.txt"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body is missing %q:\n%s", want, body)
		}
	}
	// Untracked files are not part of a diff — reported, never silently
	// dropped and never `git add`-ed behind the agent's back.
	if strings.Contains(body, "junk.log") {
		t.Fatalf("untracked file leaked into the diff:\n%s", body)
	}
	if res.Diff == nil || res.Diff.Branch != "colab/S/frontend" || res.Diff.Base != "main" || res.Diff.Commit != head {
		t.Fatalf("diff summary = %+v", res.Diff)
	}
	if strings.Join(res.Diff.UntrackedNotIncluded, ",") != "junk.log" {
		t.Fatalf("untracked = %v", res.Diff.UntrackedNotIncluded)
	}
	if res.SizeBytes != len(sub.Data) || res.Name != "frontend.diff" {
		t.Fatalf("res = %+v", res)
	}
}

// The reason the metadata may live in the body at all: `git apply` skips
// everything before the first patch header, so the `# colab-diff:` line is
// readable metadata that does not break re-application (E14-06, where a
// rebound runtime replays the session's diff artifacts in order).
func TestGeneratedDiffAppliesWithHeader(t *testing.T) {
	dir := scenarioBWorktree(t)
	s := clienttest.New(t)
	if _, err := colab.ArtifactSubmit(context.Background(), newClient(t, s),
		colab.ArtifactSubmitArgs{Type: "diff", Workdir: dir}); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(t.TempDir(), "colab.diff")
	if err := os.WriteFile(patch, s.Submissions[0].Data, 0o600); err != nil {
		t.Fatal(err)
	}

	// A fresh checkout of the base — this is what a rebound machine has.
	fresh := t.TempDir()
	git(t, fresh, "clone", "-q", dir, "app")
	app := filepath.Join(fresh, "app")
	git(t, app, "checkout", "-q", "main")
	git(t, app, "apply", "--check", patch) // fails loudly if the header confused git
	git(t, app, "apply", patch)

	for name, want := range map[string]string{
		"a.txt":      "one\ntwo committed\nthree unstaged\n",
		"staged.txt": "staged work\n",
	} {
		got, err := os.ReadFile(filepath.Join(app, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("%s after apply = %q, want %q", name, got, want)
		}
	}
}

// `--file` with `--type diff` uploads the caller's patch byte for byte —
// rewriting someone's patch is how a patch stops applying. The description
// is still stamped, so E14-06 finds the metadata in one place either way.
func TestArtifactSubmitDiffFileIsVerbatim(t *testing.T) {
	dir := scenarioBWorktree(t)
	s := clienttest.New(t)
	head := git(t, dir, "rev-parse", "--short", "HEAD")
	patch := filepath.Join(t.TempDir(), "hand.patch")
	body := "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+x\n"
	if err := os.WriteFile(patch, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := colab.ArtifactSubmit(context.Background(), newClient(t, s),
		colab.ArtifactSubmitArgs{Type: "diff", File: patch, Workdir: dir}); err != nil {
		t.Fatal(err)
	}
	sub := s.Submissions[0]
	if string(sub.Data) != body {
		t.Fatalf("body = %q, want the file verbatim", sub.Data)
	}
	if sub.Fields["name"] != "hand.patch" {
		t.Fatalf("name = %q, want the file's base name", sub.Fields["name"])
	}
	if want := "diff colab/S/frontend@" + head + " vs main"; sub.Fields["description"] != want {
		t.Fatalf("description = %q, want %q", sub.Fields["description"], want)
	}
}

// An empty diff is refused before any request: an empty artifact would
// satisfy `artifact_submitted` (FR-2.2) with no work in it. The refusal names
// the untracked files, which is the most likely reason the diff came out
// empty.
func TestArtifactSubmitDiffEmptyIsRefused(t *testing.T) {
	dir := baseRepo(t)
	write(t, dir, "new.txt", "never added\n")
	s := clienttest.New(t)

	_, err := colab.ArtifactSubmit(context.Background(), newClient(t, s),
		colab.ArtifactSubmitArgs{Type: "diff", Workdir: dir})
	if exitOf(t, err) != client.ExitUsage {
		t.Fatalf("exit = %d, want 2", client.ExitCode(err))
	}
	e := client.AsError(err)
	if e.Code != "empty_diff" {
		t.Fatalf("code = %q", e.Code)
	}
	if !strings.Contains(e.Detail, "new.txt") || !strings.Contains(e.Detail, "git add") {
		t.Fatalf("detail should name the untracked file and the fix: %q", e.Detail)
	}
	if len(s.Submissions) != 0 {
		t.Fatal("no request may be sent for an empty diff")
	}
}

func TestArtifactSubmitDiffArgErrors(t *testing.T) {
	repo := scenarioBWorktree(t)
	notARepo := t.TempDir()
	cases := map[string]struct {
		args colab.ArtifactSubmitArgs
		code string
	}{
		// `--type diff` without --file needs a worktree to diff; a session
		// with `isolation: none` has none.
		"not a git worktree": {colab.ArtifactSubmitArgs{Type: "diff", Workdir: notARepo}, "not_a_git_repo"},
		"unknown --base":     {colab.ArtifactSubmitArgs{Type: "diff", Base: "no/such", Workdir: repo}, "unknown_base"},
		// --base is meaningless for any other type: refused, not ignored.
		"--base on a doc": {colab.ArtifactSubmitArgs{Type: "doc", Base: "main", File: "x"}, "usage"},
		// Every other type still requires --file exactly as before (C-1/C-2).
		"doc without --file": {colab.ArtifactSubmitArgs{Type: "doc", Workdir: repo}, "usage"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := clienttest.New(t)
			_, err := colab.ArtifactSubmit(context.Background(), newClient(t, s), tc.args)
			if exitOf(t, err) != client.ExitUsage {
				t.Fatalf("exit = %d, want 2", client.ExitCode(err))
			}
			if code := client.AsError(err).Code; code != tc.code {
				t.Fatalf("code = %q, want %q", code, tc.code)
			}
			if len(s.Submissions) != 0 {
				t.Fatal("no request may be sent for an argument error")
			}
		})
	}
}

// Re-entry (E16-B step 5→6): the same lane submits a second diff after review
// feedback. The default name is derived from the branch, so the server sees
// version 2 of one artifact rather than two artifacts.
func TestArtifactSubmitDiffSecondSubmissionIsVersion2(t *testing.T) {
	dir := scenarioBWorktree(t)
	s := clienttest.New(t)
	c := newClient(t, s)
	for want := 1; want <= 2; want++ {
		write(t, dir, "a.txt", strings.Repeat("fix\n", want))
		res, err := colab.ArtifactSubmit(context.Background(), c, colab.ArtifactSubmitArgs{Type: "diff", Workdir: dir})
		if err != nil {
			t.Fatal(err)
		}
		var art map[string]any
		if err := json.Unmarshal(res.Artifact, &art); err != nil {
			t.Fatal(err)
		}
		if art["version"] != float64(want) {
			t.Fatalf("version = %v, want %d", art["version"], want)
		}
	}
}

// --base narrows the comparison to a revision this worktree can see.
func TestArtifactSubmitDiffExplicitBase(t *testing.T) {
	dir := scenarioBWorktree(t)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "second")
	head := git(t, dir, "rev-parse", "--short", "HEAD")
	s := clienttest.New(t)

	if _, err := colab.ArtifactSubmit(context.Background(), newClient(t, s),
		colab.ArtifactSubmitArgs{Type: "diff", Base: "HEAD~1", Workdir: dir}); err != nil {
		t.Fatal(err)
	}
	sub := s.Submissions[0]
	if want := "diff colab/S/frontend@" + head + " vs HEAD~1"; !strings.HasPrefix(sub.Fields["description"], want) {
		t.Fatalf("description = %q, want prefix %q", sub.Fields["description"], want)
	}
	if !strings.Contains(string(sub.Data), "base=HEAD~1") {
		t.Fatalf("body header = %q", strings.SplitN(string(sub.Data), "\n", 2)[0])
	}
	// Only the last commit, so the first commit's line is not in the patch.
	if strings.Contains(string(sub.Data), "+two committed") {
		t.Fatalf("--base HEAD~1 should not include the earlier commit:\n%s", sub.Data)
	}
}

// FR-6.1: the diff is always of the workdir this task runs in. There is no
// argument — CLI flag or MCP tool field — that aims git at another
// repository, so a JSON payload that tries cannot set one.
func TestDiffTargetIsNotCallerSettable(t *testing.T) {
	var a colab.ArtifactSubmitArgs
	if err := json.Unmarshal([]byte(`{"type":"diff","workdir":"/etc","dir":"/etc","repo":"/etc"}`), &a); err != nil {
		t.Fatal(err)
	}
	if a.Workdir != "" {
		t.Fatalf("workdir was settable from JSON: %q", a.Workdir)
	}
}

// binaryV1/binaryV2 are two versions of a file git treats as binary (a NUL in
// the first bytes). They stand in for the image/font/`.dat` an engineer lane
// really changes.
const (
	binaryV1 = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01v1"
	binaryV2 = "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x02v2-longer\xff\xfe"
)

// binaryWorktree is a worktree that changed a binary file as well as a text
// one — an agent that touched an icon, a font or a lockfile.
func binaryWorktree(t *testing.T) string {
	t.Helper()
	dir := baseRepo(t)
	write(t, dir, "logo.png", binaryV1)
	git(t, dir, "add", "logo.png")
	git(t, dir, "commit", "-qm", "base binary")
	git(t, dir, "checkout", "-q", "-b", "colab/S/frontend")
	write(t, dir, "a.txt", "one\ntwo committed\n")
	git(t, dir, "commit", "-qam", "committed work")
	write(t, dir, "logo.png", binaryV2) // unstaged, like any other edit
	return dir
}

// PR #160 R1: a worktree that changed a binary file must still produce a
// patch that re-applies. Without `git diff --binary` the body carries one
// `Binary files a/logo.png and b/logo.png differ` line and `git apply`
// refuses the WHOLE patch — the text hunks are lost with it while the CLI
// exits 0, which is the silent-loss shape E14-06 (rebinding re-apply) and the
// scenario B reviewer both hit. The apply here is real, on a fresh clone of
// the base, exactly like a rebound machine.
func TestGeneratedDiffAppliesWithBinaryChange(t *testing.T) {
	dir := binaryWorktree(t)
	s := clienttest.New(t)
	if _, err := colab.ArtifactSubmit(context.Background(), newClient(t, s),
		colab.ArtifactSubmitArgs{Type: "diff", Workdir: dir}); err != nil {
		t.Fatal(err)
	}
	body := string(s.Submissions[0].Data)
	// The tell-tale of a patch git cannot apply: the summary line instead of
	// the payload. `--binary` replaces it with `GIT binary patch`.
	if strings.Contains(body, "Binary files") {
		t.Fatalf("the diff summarises the binary change instead of carrying it:\n%s", body)
	}
	if !strings.Contains(body, "GIT binary patch") {
		t.Fatalf("no binary payload in the patch:\n%s", body)
	}
	patch := filepath.Join(t.TempDir(), "colab.diff")
	if err := os.WriteFile(patch, s.Submissions[0].Data, 0o600); err != nil {
		t.Fatal(err)
	}

	fresh := t.TempDir()
	git(t, fresh, "clone", "-q", dir, "app")
	app := filepath.Join(fresh, "app")
	git(t, app, "checkout", "-q", "main")
	git(t, app, "apply", "--check", patch) // exit 0 or this test fails
	git(t, app, "apply", patch)

	got, err := os.ReadFile(filepath.Join(app, "logo.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != binaryV2 {
		t.Fatalf("logo.png after apply = %q, want the worktree's bytes %q", got, binaryV2)
	}
	// The text change rode along; a rejected binary hunk would have taken it.
	text, err := os.ReadFile(filepath.Join(app, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "one\ntwo committed\n" {
		t.Fatalf("a.txt after apply = %q", text)
	}
}

// movedOnBaseWorktree is the fork this repository actually produces: the
// agent branched, and `main` gained a commit afterwards while the agent
// worked.
func movedOnBaseWorktree(t *testing.T) string {
	t.Helper()
	dir := baseRepo(t)
	git(t, dir, "checkout", "-q", "-b", "colab/S/frontend")
	write(t, dir, "a.txt", "one\ntwo committed\n")
	git(t, dir, "commit", "-qam", "committed work")
	git(t, dir, "checkout", "-q", "main")
	write(t, dir, "b.txt", "base moved on\n")
	git(t, dir, "add", "b.txt")
	git(t, dir, "commit", "-qm", "landed on main after the fork")
	git(t, dir, "checkout", "-q", "colab/S/frontend")
	write(t, dir, "a.txt", "one\ntwo committed\nthree unstaged\n")
	return dir
}

// The diff is taken from the MERGE BASE, not from the base branch's tip.
// Diffing against the tip would describe this branch as deleting b.txt —
// a reversal of work the agent never touched, which a reviewer reads as
// vandalism and a re-apply performs. Nothing that landed on main after the
// fork may appear in the patch.
func TestGeneratedDiffUsesMergeBaseWhenBaseMovedOn(t *testing.T) {
	dir := movedOnBaseWorktree(t)
	s := clienttest.New(t)
	if _, err := colab.ArtifactSubmit(context.Background(), newClient(t, s),
		colab.ArtifactSubmitArgs{Type: "diff", Workdir: dir}); err != nil {
		t.Fatal(err)
	}
	body := string(s.Submissions[0].Data)
	// The branch's own work is there — otherwise this assertion is vacuous.
	for _, want := range []string{"+two committed", "+three unstaged"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body is missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"b.txt", "base moved on", "deleted file"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("the patch reverses main's later commit (%q in body):\n%s", unwanted, body)
		}
	}
	// Still stated as "vs main": the base is the branch, the merge base is
	// only where the comparison starts (E14-06 re-applies onto the base).
	if want := "base=main"; !strings.Contains(strings.SplitN(body, "\n", 2)[0], want) {
		t.Fatalf("header = %q, want %q", strings.SplitN(body, "\n", 2)[0], want)
	}
}
