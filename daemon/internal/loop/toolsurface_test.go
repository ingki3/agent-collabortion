package loop

import (
	"context"
	"encoding/json"
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

// The brief and prompt the server hands out, shortened to the CLI lines that
// matter (server/internal/queue/bundle.go).
const (
	briefText  = "[2] Workspace rules and colab CLI\n- Post every reply with `colab message post --body \"<text>\"` (or the colab_message_post MCP tool).\n- Read more history with `colab session messages`.\n"
	promptText = "Respond to the trigger. Post your reply with `colab message post`.\n"
)

func hermesBundle(id string) contracts.TaskBundle {
	b := bundle(id)
	b.Profile.RuntimeKind = contracts.RuntimeHermes
	b.Brief.Transport = contracts.BriefInstructionFile
	b.Brief.Text = briefText
	b.Prompt = promptText
	return b
}

func hermesScript() acpfake.Script {
	return acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}
}

// harness §10 (b): a runtime with no `mcpCapabilities` gets a per-attempt
// wrapper executable, and the brief AND the turn prompt name it by absolute
// path. The wrapper is gone once the attempt finishes.
func TestCLIWrapperAttempt(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{hermesBundle("t-h")}}
	d, root := newDaemon(t, srv, hermesScript())
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(hermesScript(), record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}
	wrapper := toolwrap.Path(root, "t-h", 1)
	var mode os.FileMode
	var script, agents string
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase != "preparing" {
			return
		}
		if st, err := os.Stat(wrapper); err == nil {
			mode = st.Mode().Perm()
			b, _ := os.ReadFile(wrapper)
			script = string(b)
		}
		b, _ := os.ReadFile(filepath.Join(req.WorkdirPath, brief.FileName))
		agents = string(b)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done

	if script == "" {
		t.Fatalf("no wrapper at %s while the attempt was preparing", wrapper)
	}
	if mode != 0o700 {
		t.Fatalf("wrapper mode %v want 0700", mode)
	}
	if !strings.Contains(script, "export COLAB_TASK_TOKEN='ctk_x'") || !strings.Contains(script, `"$@"`) {
		t.Fatalf("wrapper does not carry the attempt token to the real CLI:\n%s", script)
	}
	// [2] of the brief points at the wrapper, and no bare `colab ` command is
	// left for the agent to run in a sanitised environment.
	if !strings.Contains(agents, "`"+wrapper+" message post") || !strings.Contains(agents, "`"+wrapper+" session messages`") {
		t.Fatalf("brief not rewritten:\n%s", agents)
	}
	if strings.Contains(agents, "`colab ") {
		t.Fatalf("bare colab command left in the brief:\n%s", agents)
	}
	// Prose and the MCP tool name are untouched.
	if !strings.Contains(agents, "Workspace rules and colab CLI") || !strings.Contains(agents, "colab_message_post MCP tool") {
		t.Fatalf("rewrite hit prose:\n%s", agents)
	}
	// The turn prompt too (v0.8.1) — the agent reads it in the same breath.
	prompt := promptOf(t, record)
	if !strings.Contains(prompt, "`"+wrapper+" message post`") || strings.Contains(prompt, "`colab ") {
		t.Fatalf("prompt not rewritten: %q", prompt)
	}
	// finish → the wrapper (and its token) is gone.
	if _, err := os.Stat(toolwrap.Dir(root, "t-h", 1)); !os.IsNotExist(err) {
		t.Fatalf("wrapper survived finish: %v", err)
	}
}

// The cancelled path drops the wrapper as well — the token dies with the
// attempt whichever way it ends.
func TestCLIWrapperRemovedOnCancel(t *testing.T) {
	srv := &memServer{queue: []contracts.TaskBundle{hermesBundle("t-hc")}}
	s := hermesScript()
	s.StayAlive = true
	s.Turns = []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "working"}, {Hang: true}}}}
	d, root := newDaemon(t, srv, s)
	srv.hbCmds = []contracts.Command{{Type: contracts.CmdCancel, TaskID: "t-hc", Attempt: 1, Reason: "director"}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done
	srv.mu.Lock()
	outcome := srv.finishes[0].Outcome
	srv.mu.Unlock()
	if outcome != "cancelled" {
		t.Fatalf("outcome %q", outcome)
	}
	if _, err := os.Stat(toolwrap.Dir(root, "t-hc", 1)); !os.IsNotExist(err) {
		t.Fatal("wrapper survived a cancelled attempt")
	}
}

// An mcp runtime gets no wrapper and no rewrite: it reaches the platform
// through the colab MCP server, and `colab` is on its PATH anyway.
func TestMCPSurfaceNoWrapperNoRewrite(t *testing.T) {
	b := bundle("t-c")
	b.Brief.Text, b.Prompt = briefText, promptText
	srv := &memServer{queue: []contracts.TaskBundle{b}}
	d, root := newDaemon(t, srv, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}})
	record := filepath.Join(t.TempDir(), "record.jsonl")
	d.SpawnConfig = func(contracts.TaskBundle, string) acp.Config {
		cmd, args, env := acpfake.Command(acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "ok"}}}}}, record)
		return acp.Config{Command: cmd, Args: args, Env: env, KillAfter: time.Second}
	}
	var wrapperSeen bool
	srv.phaseHook = func(req api.PhaseRequest) {
		if req.Phase == "preparing" {
			_, err := os.Stat(toolwrap.Root(root))
			wrapperSeen = err == nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return srv.finished() == 1 })
	cancel()
	<-done
	if wrapperSeen {
		t.Fatal("wrapper written for an mcp runtime")
	}
	if p := promptOf(t, record); p != promptText {
		t.Fatalf("prompt rewritten for an mcp runtime: %q", p)
	}
}

// Daemon start sweeps wrappers left by a dead daemon, in the same place as
// the orphan pgid records and before the first claim.
func TestSweepWrappersOnStart(t *testing.T) {
	srv := &memServer{}
	d, root := newDaemon(t, srv, acpfake.Script{})
	stale := toolwrap.Path(root, "old-task", 3)
	if _, err := toolwrap.Write(root, "old-task", 3, "colab", []string{"COLAB_TASK_TOKEN=ctk_dead"}); err != nil {
		t.Fatal(err)
	}
	srv.claimHook = func() {
		if _, err := os.Stat(stale); err == nil {
			t.Error("stale wrapper still on disk at the first claim")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 10*time.Second, func() bool { return d.claims() > 0 })
	cancel()
	<-done
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale wrapper survived the start-up sweep")
	}
}

func (d *Daemon) claims() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Claimed
}

func promptOf(t *testing.T, record string) string {
	t.Helper()
	recs, err := acpfake.ReadRecords(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.Method != acp.MethodSessionPrompt {
			continue
		}
		var p acp.PromptParams
		if err := json.Unmarshal(r.Params, &p); err != nil {
			t.Fatal(err)
		}
		if len(p.Prompt) > 0 {
			return p.Prompt[0].Text
		}
	}
	t.Fatal("no session/prompt recorded")
	return ""
}
