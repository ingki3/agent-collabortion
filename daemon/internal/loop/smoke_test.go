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

// Real Claude Code, one turn with several tool calls, through the whole daemon
// path — the D-17 실기 스모크. It answers the two questions the fake cannot:
// does usage really reach a heartbeat WHILE the turn is running, and is the
// last heartbeat before `finish` carrying the turn's measured total?
//
//	COLAB_SMOKE=1 go test ./internal/loop -run SmokeClaudeMidturnUsage -v
func TestSmokeClaudeMidturnUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind contracts.RuntimeKind
		bin  string
		// §9 v0.8.5: only claude_code has an in-turn channel, and only
		// claude_code reports a cost (result.total_cost_usd).
		wantMidturn  bool
		wantMeasured bool
	}{
		{"claude_code", contracts.RuntimeClaudeCode, "npx", true, true},
		{"hermes", contracts.RuntimeHermes, "hermes", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) { smokeMidturn(t, tc.kind, tc.bin, tc.wantMidturn, tc.wantMeasured) })
	}
}

func smokeMidturn(t *testing.T, kind contracts.RuntimeKind, bin string, wantMidturn, wantMeasured bool) {
	if os.Getenv("COLAB_SMOKE") != "1" {
		t.Skip("set COLAB_SMOKE=1 to run the real-adapter smoke")
	}
	if _, err := exec.LookPath(bin); err != nil {
		t.Skipf("%s not on this machine", bin)
	}
	b := bundle("smoke-midturn-" + string(kind))
	b.Profile.RuntimeKind = kind
	b.Profile.Model = "haiku"
	if kind == contracts.RuntimeHermes {
		b.Profile.Model = ""
		b.Brief.Transport = contracts.BriefInstructionFile
	}
	b.Prompt = "Do these three steps one at a time, using a tool for each: " +
		"1) write a file named step1.txt containing the word ONE, " +
		"2) write a file named step2.txt containing the word TWO, " +
		"3) run `ls` in the current directory. Then reply with exactly DONE."

	srv := &memServer{queue: []contracts.TaskBundle{b}}
	root := t.TempDir()
	srv.root = root
	d := &Daemon{
		Cfg:               config.Config{ServerURL: "mem", RuntimeID: "rt", DaemonToken: "cdt", WorkdirRoot: root, Capacity: 1},
		Server:            srv,
		Version:           "smoke",
		Orphans:           orphan.Store{Root: root, KillAfter: 5 * time.Second},
		Log:               t.Logf,
		HeartbeatInterval: 2 * time.Second,
		ClaimWait:         time.Second,
		KillAfter:         5 * time.Second,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- d.Run(runCtx) }()
	waitFor(t, 7*time.Minute, func() bool { return srv.finished() == 1 })
	stop()
	<-done

	srv.mu.Lock()
	defer srv.mu.Unlock()
	f := srv.finishes[0]
	t0 := srv.hbLog[0].at
	var midturn, beforeFinish int
	for i, hb := range srv.hbLog {
		nonzero := hb.usage.InputTokens > 0 || hb.usage.OutputTokens > 0
		if nonzero {
			beforeFinish++
			// "During the turn" means: the runtime was still working, i.e.
			// another tool event came after this heartbeat.
			if hb.at.Before(srv.lastToolAt) {
				midturn++
			}
		}
		t.Logf("heartbeat %d at +%.1fs: in=%d out=%d cache_read=%d cost=%.4f estimated=%v",
			i, hb.at.Sub(t0).Seconds(), hb.usage.InputTokens, hb.usage.OutputTokens,
			hb.usage.CacheReadTokens, hb.usage.CostUSD, hb.usage.Estimated)
	}
	t.Logf("heartbeats=%d with usage=%d (mid-turn=%d); last tool at +%.1fs, finish at +%.1fs, outcome=%s usage=%+v",
		len(srv.hbLog), beforeFinish, midturn, srv.lastToolAt.Sub(t0).Seconds(), srv.finishAt.Sub(t0).Seconds(), f.Outcome, f.Usage)
	if f.Outcome != "completed" {
		t.Fatalf("outcome %s (%s)", f.Outcome, f.StopReason)
	}
	if wantMidturn && midturn == 0 {
		t.Errorf("no heartbeat carried usage while the turn was still running — the raw SDK stream "+
			"is the only in-turn source (harness §7 v0.8.5) and it produced nothing (heartbeats: %d)", len(srv.hbLog))
	}
	if !wantMidturn && midturn > 0 {
		t.Errorf("%d in-turn heartbeats on a runtime §9 advertises usage_midturn=false — the "+
			"advertisement and the behaviour must not disagree", midturn)
	}
	last := srv.hbLog[len(srv.hbLog)-1]
	if !last.at.Before(srv.finishAt) || last.usage.InputTokens == 0 {
		t.Errorf("last heartbeat (at +%.1fs, in=%d) is not the pre-finish measured one (finish +%.1fs)",
			last.at.Sub(t0).Seconds(), last.usage.InputTokens, srv.finishAt.Sub(t0).Seconds())
	}
	if f.Usage.Estimated == wantMeasured {
		t.Errorf("finish usage estimated=%v — claude_code reports result.total_cost_usd (measured, "+
			"§7 v0.8.5) and hermes reports no cost at all (estimated). usage %+v", f.Usage.Estimated, f.Usage)
	}
}
