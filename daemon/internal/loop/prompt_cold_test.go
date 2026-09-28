package loop

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/brief"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

// harness §10 v0.9.14 · daemon-protocol v0.10.3 §4.1: `prompt_cold` gets the
// same treatment as `prompt` — wrapper rewrite → placeholder substitution →
// pointer line — so a hermes lane whose resume fails reads the wrapper path
// and its brief file in the whole turn prompt it is handed.
//
// 회귀 주입: loop.go 의 PromptCold 치환 셋 중 하나를 지우면 FAIL.
func TestPromptColdGetsRewriteSubstitutionAndPointer(t *testing.T) {
	b := hermesBundle("t-pc")
	b.Resume = &contracts.RuntimeSessionRef{RuntimeKind: contracts.RuntimeHermes, SessionID: "gone", CWD: "/x", CreatedAt: time.Now()}
	b.Prompt = "DELTA `colab message post --body x`\n"
	b.PromptCold = "COLD {{COLAB_REBIND_DIR}} then `colab message post --body x`\n"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, root := newDaemon(t, srv, hermesScript())
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		// No known sessions: session/load answers null → cold start.
		cmd, args, env := acpfake.Command(hermesScript(), record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	if f := srv.finishes[0]; f.Outcome != "completed" || f.ResumeOutcome != "cold_start" {
		t.Fatalf("finish %s/%s %q resume=%q", f.Outcome, f.FailureKind, f.StopReason, f.ResumeOutcome)
	}
	prompt := promptOf(t, record)
	if !strings.Contains(prompt, "COLD ") || strings.Contains(prompt, "DELTA") {
		t.Fatalf("a new session after resume_rejected got the wrong prompt:\n%s", prompt)
	}
	if strings.Contains(prompt, "{{COLAB_") {
		t.Fatalf("placeholder left in prompt_cold:\n%s", prompt)
	}
	if !strings.Contains(prompt, toolwrap.Path(root, "t-pc", 1)+" message post") {
		t.Fatalf("prompt_cold was not rewritten to the wrapper:\n%s", prompt)
	}
	first, _, _ := strings.Cut(prompt, "\n")
	if !strings.Contains(first, "COLAB_BRIEF") || !strings.HasPrefix(first, brief.PromptPointerPrefix) {
		t.Fatalf("pointer line is not first in prompt_cold: %q", first)
	}
}

// §10 v0.8.7 on prompt_cold: a placeholder this daemon cannot fill in the
// cold prompt fails the attempt before the runtime starts, like one in
// `prompt`.
func TestUnsubstitutedPlaceholderInPromptColdFails(t *testing.T) {
	b := bundle("t-pcph")
	b.Resume = &contracts.RuntimeSessionRef{RuntimeKind: contracts.RuntimeClaudeCode, SessionID: "s", CWD: "/x", CreatedAt: time.Now()}
	b.PromptCold = "cold {{COLAB_UNKNOWN_DIR}}"
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, _ := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done
	if f := srv.finishes[0]; f.Outcome != "failed" || f.FailureKind != contracts.FailConfig || !strings.Contains(f.StopReason, "{{COLAB_UNKNOWN_DIR}}") {
		t.Fatalf("finish = %s/%s %q, want failed/config naming the placeholder", f.Outcome, f.FailureKind, f.StopReason)
	}
}
