package loop

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/config"
	"github.com/ingki3/agent-collabortion/daemon/internal/orphan"
	"github.com/ingki3/agent-collabortion/daemon/internal/toolwrap"
)

// Real Hermes, one turn, through the whole daemon path: the §10 wrapper is
// written, the brief and prompt name it, and the agent runs it from a shell
// tool whose environment the adapter sanitised. G5 (b) measured
// `colab: command not found` here.
//
//	COLAB_SMOKE=1 go test ./internal/loop -run SmokeHermesToolWrapper -v
func TestSmokeHermesToolWrapper(t *testing.T) {
	if os.Getenv("COLAB_SMOKE") != "1" {
		t.Skip("set COLAB_SMOKE=1 to run the real-adapter smoke")
	}
	if _, err := exec.LookPath("hermes"); err != nil {
		t.Skip("hermes CLI not on this machine")
	}
	colab := toolwrap.ResolveBin("")
	if _, err := exec.LookPath(colab); err != nil {
		t.Skipf("colab CLI not on this machine (%v)", err)
	}

	b := bundle("smoke-toolwrap")
	b.Profile.RuntimeKind = contracts.RuntimeHermes
	b.Brief.Transport = contracts.BriefInstructionFile
	b.Brief.Text = "[2] Workspace rules and colab CLI\n" +
		"- The platform CLI is `colab`. Run it from the shell exactly as written here.\n"
	b.Prompt = "Run `colab --version` in the shell, then reply with VERSION=<the exact stdout>. Do nothing else."

	srv := &memServer{queue: []contracts.TaskBundle{b}}
	root := t.TempDir()
	srv.root = root
	d := &Daemon{
		Cfg:               config.Config{ServerURL: "mem", RuntimeID: "rt", DaemonToken: "cdt", WorkdirRoot: root, Capacity: 1, ColabBin: colab},
		Server:            srv,
		Version:           "smoke",
		Orphans:           orphan.Store{Root: root, KillAfter: 5 * time.Second},
		Log:               t.Logf,
		HeartbeatInterval: 5 * time.Second,
		ClaimWait:         time.Second,
		KillAfter:         5 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- d.Run(runCtx) }()
	waitFor(t, 5*time.Minute, func() bool { return srv.finished() == 1 })
	stop()
	<-done

	srv.mu.Lock()
	f := srv.finishes[0]
	var text string
	for _, e := range srv.events {
		if e.Class == "message" && e.Verb == "say" {
			if s, ok := e.Payload["text"].(string); ok {
				text += s
			}
		}
	}
	srv.mu.Unlock()
	t.Logf("outcome=%s stop=%s text=%q", f.Outcome, f.StopReason, text)
	if f.Outcome != "completed" {
		t.Fatalf("outcome %s (%s)", f.Outcome, f.StopReason)
	}
	if strings.Contains(text, "command not found") {
		t.Fatalf("the wrapper did not reach the shell tool: %q", text)
	}
	if !regexp.MustCompile(`\d+\.\d+\.\d+`).MatchString(text) {
		t.Fatalf("no colab version in the reply — the agent could not run the CLI: %q", text)
	}
	if _, err := os.Stat(toolwrap.Dir(root, b.Task.ID, 1)); !os.IsNotExist(err) {
		t.Fatal("wrapper survived finish")
	}
}
