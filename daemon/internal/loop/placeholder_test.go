package loop

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/api"
	"github.com/ingki3/agent-collabortion/daemon/internal/brief"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

// The prompt the server writes for the first turn after a rebinding (§4.3
// rebind_prepare, FR-9.2 E14-06). It names the directory it cannot know by
// placeholder and the CLI by name — both are the daemon's to fill in.
const rebindPromptText = "재바인딩됨. `{{COLAB_REBIND_DIR}}/manifest.json` 순서대로 " +
	"`{{COLAB_REBIND_DIR}}` 의 diff 를 적용한 뒤 `colab message post --body \"<text>\"` 로 보고하라.\n"

const rebindBriefText = "[2] Workspace rules and colab CLI\n" +
	"- 이전 세션의 산출물은 `{{COLAB_REBIND_DIR}}` 에 있다.\n" +
	"- Post every reply with `colab message post --body \"<text>\"`.\n"

// harness §10 v0.8.7: after `rebind_prepare` has put the artifacts on disk,
// the FIRST attempt's real prompt carries the absolute path, not the
// placeholder — and the path it carries is the one the files are actually in.
//
// Order matters and is asserted here, because §10 fixes it (wrapper rewrite →
// placeholder substitution → pointer line): the wrapper path and the rebind
// path both land, and the brief pointer is still the first line of the
// prompt.
func TestRebindDirPlaceholderInFirstAttemptPrompt(t *testing.T) {
	b := hermesBundle("t-rb")
	b.Task.SessionID = "sess-rb"
	b.Brief.Text = rebindBriefText
	b.Prompt = rebindPromptText

	srv := &memServer{queue: []contracts.TaskBundle{b}, downloadBody: map[string]string{
		"/v1/artifacts/a1/content": "diff-one",
	}}
	d, root := newDaemon(t, srv, hermesScript())
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(hermesScript(), record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	// 1. The server prepares the rebinding: the artifacts land under the
	//    workdir root, outside any checkout. (Run re-runs init itself.)
	d.init()
	d.rebindPrepare(context.Background(), contracts.Command{
		Type: contracts.CmdRebindPrepare, SessionID: "sess-rb",
		Artifacts: []contracts.ArtifactRef{{ID: "a1", Order: 1, URL: "/v1/artifacts/a1/content"}},
	})
	dir := RebindDir(root, "sess-rb")
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("rebind_prepare left no manifest: %v", err)
	}

	// 2. The first attempt of the rebound session runs.
	var briefBody string
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase != "preparing" {
			return
		}
		body, _ := os.ReadFile(filepath.Join(req.WorkdirPath, brief.FileName))
		briefBody = string(body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	// First: the attempt ran at all. An unsubstituted placeholder fails the
	// attempt as `config` before the runtime is spawned, so this is where a
	// missing substitution shows up as itself rather than as "no prompt".
	if f := srv.finishes[0]; f.Outcome != "completed" {
		t.Fatalf("finish = %s/%s %q, want completed — the first attempt after a rebinding did not run", f.Outcome, f.FailureKind, f.StopReason)
	}
	prompt := promptOf(t, record)
	if strings.Contains(prompt, "{{COLAB_") {
		t.Fatalf("placeholder survived into the real prompt: %q", prompt)
	}
	if !strings.Contains(prompt, dir+"/manifest.json") || !strings.Contains(prompt, "`"+dir+"` 의 diff") {
		t.Fatalf("prompt does not name the rebind dir %s:\n%s", dir, prompt)
	}
	// The path the agent is told to read is the path the bytes are in — the
	// whole point of substituting rather than passing the placeholder on.
	body, err := os.ReadFile(filepath.Join(dir, "001-a1"))
	if err != nil || string(body) != "diff-one" {
		t.Fatalf("%s/001-a1 = %q (%v), want the downloaded artifact", dir, body, err)
	}
	// §10 order: the wrapper rewrite ran first (its path is in), the pointer
	// line is still the first line, and the brief got the same treatment.
	wrapper := toolwrap.Path(root, "t-rb", 1)
	if !strings.Contains(prompt, wrapper+" message post") {
		t.Fatalf("wrapper rewrite did not survive the substitution:\n%s", prompt)
	}
	first, _, _ := strings.Cut(prompt, "\n")
	if !strings.Contains(first, brief.FileName) {
		t.Fatalf("pointer line is not first (§8.4 v0.16): %q", first)
	}
	if strings.Contains(briefBody, "{{COLAB_") || !strings.Contains(briefBody, dir) {
		t.Fatalf("brief not substituted:\n%s", briefBody)
	}
}

// §10 v0.8.7: a placeholder this daemon cannot fill NEVER reaches the agent.
// The attempt fails as `config` before the runtime is even spawned, and the
// placeholder's own name is in the feed — otherwise whoever wrote that text
// sees "config error" and has nothing to go on.
func TestUnsubstitutedPlaceholderFailsTheAttempt(t *testing.T) {
	b := bundle("t-ph")
	b.Prompt = "Apply the diffs in {{COLAB_UNKNOWN_DIR}} then report."
	b.Brief.Text = "brief with {{COLAB_ANOTHER_ONE}}"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, _ := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}})
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	f := srv.finishes[0]
	if f.Outcome != "failed" || f.FailureKind != contracts.FailConfig {
		t.Fatalf("finish = %s/%s, want failed/config", f.Outcome, f.FailureKind)
	}
	if !strings.Contains(f.StopReason, "{{COLAB_UNKNOWN_DIR}}") || !strings.Contains(f.StopReason, "{{COLAB_ANOTHER_ONE}}") {
		t.Fatalf("stop_reason %q does not name both placeholders", f.StopReason)
	}
	// The reason is in the feed, not only in the finish body.
	var detail string
	srv.mu.Lock()
	for _, e := range srv.events {
		if e.Class == "runtime" && e.Verb == "error" && e.Outcome == "failed" {
			if s, ok := e.Payload["detail"].(string); ok {
				detail = s
			}
		}
	}
	srv.mu.Unlock()
	if !strings.Contains(detail, "{{COLAB_UNKNOWN_DIR}}") {
		t.Fatalf("no runtime/error event naming the placeholder; detail = %q", detail)
	}
	// The runtime never started: no money spent on a broken path.
	if _, err := os.Stat(record); err == nil {
		if recs, rerr := acpfake.ReadRecords(record); rerr == nil && len(recs) > 0 {
			t.Fatalf("the runtime was spawned anyway: %d ACP calls", len(recs))
		}
	}
}

func TestSubstitutePlaceholdersAndLeftovers(t *testing.T) {
	vals := map[string]string{PlaceholderRebindDir: "/w/.colab/rebind/s1"}
	got := substitutePlaceholders("a {{COLAB_REBIND_DIR}} b {{COLAB_REBIND_DIR}}", vals)
	if got != "a /w/.colab/rebind/s1 b /w/.colab/rebind/s1" {
		t.Fatalf("substitute = %q", got)
	}
	if l := leftoverPlaceholders(got); l != nil {
		t.Fatalf("leftovers on a fully substituted text: %v", l)
	}
	// Sorted, de-duplicated, across both texts.
	l := leftoverPlaceholders("{{COLAB_B}} {{COLAB_A}}", "{{COLAB_A}}")
	if len(l) != 2 || l[0] != "{{COLAB_A}}" || l[1] != "{{COLAB_B}}" {
		t.Fatalf("leftovers = %v", l)
	}
	// An empty value is not a substitution: "" would give the agent a path
	// like "/manifest.json".
	if got := substitutePlaceholders("x {{COLAB_REBIND_DIR}}", map[string]string{PlaceholderRebindDir: ""}); got != "x {{COLAB_REBIND_DIR}}" {
		t.Fatalf("empty value substituted: %q", got)
	}
}
