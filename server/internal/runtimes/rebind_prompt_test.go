package runtimes

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestRebindPromptUsesTheDownloadedFiles is #162 review NN5.
//
// daemon-protocol §4.3 v0.7.2 has the daemon DOWNLOAD the diff artifacts on
// `rebind_prepare`, into `<workdir_root>/.colab/rebind/<session_id>` next to a
// `manifest.json`, and harness §10 v0.8.7 has the server name that directory
// with the placeholder `{{COLAB_REBIND_DIR}}` because only the daemon knows the
// absolute path. The first version of this prompt told the agent to
// `colab artifact get` the artifacts instead — which works, and makes the whole
// download a second, slower copy of bytes already on disk.
//
// The placeholder must appear VERBATIM and exactly once: the daemon substitutes
// it, and an unsubstituted one is a `failed(config)` finish (§10), so a server
// that helpfully built a path would hand the agent a directory that does not
// exist on the new machine.
func TestRebindPromptUsesTheDownloadedFiles(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	got := RebindPrompt([]RebindArtifact{
		{ID: a, Order: 1, Kind: "diff"},
		{ID: b, Order: 2, Kind: "diff"},
	})

	if n := strings.Count(got, RebindDirPlaceholder); n != 1 {
		t.Errorf("`%s` appears %d times, want exactly 1 (harness §10 v0.8.7)\n--- prompt ---\n%s",
			RebindDirPlaceholder, n, got)
	}
	if !strings.Contains(got, RebindDirPlaceholder+"/manifest.json") {
		t.Errorf("the prompt does not point at the manifest the daemon wrote (§4.3 v0.7.2)\n%s", got)
	}
	if !strings.Contains(got, "git apply") {
		t.Errorf("the prompt does not say how to apply a diff artifact\n%s", got)
	}
	if strings.Contains(got, "colab artifact get") {
		t.Errorf("the prompt still re-downloads what `rebind_prepare` already fetched (NN5)\n%s", got)
	}

	// Order is the instruction, not decoration: diffs applied out of order
	// conflict (E14-06).
	i, j := strings.Index(got, a.String()), strings.Index(got, b.String())
	if i < 0 || j < 0 {
		t.Fatalf("artifact ids missing from the prompt\n%s", got)
	}
	if i > j {
		t.Errorf("artifacts listed out of submission order (E14-06)\n%s", got)
	}
	if !strings.Contains(got, "1. ") || !strings.Contains(got, "2. ") {
		t.Errorf("the prompt has no ordered list\n%s", got)
	}

	// E14-06's own sentence, and the cold-start fact that goes with it: the
	// runtime session died with the machine.
	if !strings.Contains(got, "제출 순서대로") {
		t.Errorf("E14-06's sentence is missing\n%s", got)
	}
	if !strings.Contains(got, "콜드 스타트") {
		t.Errorf("the prompt does not say this turn is a cold start (E14-06)\n%s", got)
	}
	if !strings.Contains(got, "workdir 의 현재 상태") {
		t.Errorf("the prompt does not ask for the workdir check (§8.4)\n%s", got)
	}
}

// TestRebindPromptWithNoDiffsNamesNoDirectory: with nothing downloaded there is
// no rebind directory to point at, and a placeholder the daemon cannot resolve
// to anything useful is worse than none.
func TestRebindPromptWithNoDiffsNamesNoDirectory(t *testing.T) {
	got := RebindPrompt(nil)
	if strings.Contains(got, RebindDirPlaceholder) {
		t.Errorf("empty-artifact prompt names the rebind directory\n%s", got)
	}
	if !strings.Contains(got, "현재 상태를 먼저 확인") {
		t.Errorf("empty-artifact prompt lost the workdir check\n%s", got)
	}
}
