// Real Claude Code adapter, the P3 resume and cancel paths (task DoD).
//
//	COLAB_SMOKE=1 go test ./internal/harness/acp -run SmokeP3 -v
//
// The fake covers the contract; this covers the ADAPTER, which is where every
// P3 resume defect actually came from — spike 4c found two of them precisely
// because the fake had been taught the contract's wording instead of the
// adapter's (`-32000 "Session not found"` vs `-32002 "Resource not found"`).
package acp_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

func smokeGate(t *testing.T) {
	t.Helper()
	if os.Getenv("COLAB_SMOKE") != "1" {
		t.Skip("set COLAB_SMOKE=1 to run the real-adapter smoke")
	}
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on this machine")
	}
}

func smokeAttempt(t *testing.T, b contracts.TaskBundle, mut func(*acp.Attempt)) (*fixture, acp.Result) {
	t.Helper()
	dir := t.TempDir()
	cmd, args := acp.Command(contracts.RuntimeClaudeCode, "", nil)
	env := acp.Env(contracts.RuntimeClaudeCode, acp.TaskEnv{AgentName: "smoke"}, nil)
	f := &fixture{t: t, sink: &memSink{}, dir: dir}
	a := acp.Attempt{
		Bundle: b, Workdir: dir,
		Cmd:  acp.Config{Command: cmd, Args: args, Env: env, KillAfter: 10 * time.Second},
		Sink: f.sink, DaemonVersion: "smoke", SetupTimeout: 4 * time.Minute,
	}
	if mut != nil {
		mut(&a)
	}
	f.runner = acp.New(a)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	res := f.runner.Run(ctx)
	f.sink.assertSchema(t)
	t.Logf("outcome=%s stop=%s resume=%s ref=%+v text=%q", res.Outcome, res.StopReason, res.ResumeOutcome, res.SessionRef, strings.TrimSpace(res.Text))
	return f, res
}

func smokeBundle(model string) contracts.TaskBundle {
	b := bundle(contracts.RuntimeClaudeCode)
	b.Profile.Model = model
	b.Prompt = "Reply with exactly: TURN-ONE. Do not use any tool."
	return b
}

// 1) a turn ends and leaves a session ref  2) the next attempt RESUMES it and
// still has the first turn's context  3) a ref the runtime no longer has cold
// starts, with the adapter's own rpc error on the feed (D-11, E8-01·02).
func TestSmokeP3ResumeThenForcedColdStart(t *testing.T) {
	smokeGate(t)
	b := smokeBundle("haiku")
	_, first := smokeAttempt(t, b, nil)
	if first.Outcome != "completed" || first.SessionRef == nil || first.SessionRef.SessionID == "" {
		t.Fatalf("turn 1 %+v — the ref is what the next attempt resumes from (E8-13)", first)
	}

	b2 := smokeBundle("haiku")
	b2.Task.Attempt = 2
	b2.Resume = first.SessionRef
	b2.Prompt = "What exactly did you reply in your previous message? Answer with that word only."
	f2, second := smokeAttempt(t, b2, nil)
	if second.ResumeOutcome != "resumed" {
		t.Fatalf("attempt 2 resume outcome = %q, want resumed (E8-01)", second.ResumeOutcome)
	}
	if !strings.Contains(strings.ToUpper(second.Text), "TURN-ONE") {
		t.Errorf("resumed turn lost the context: %q", second.Text)
	}
	if n := len(f2.sink.find("message", "say", "ok")); n != 1 {
		t.Errorf("replayed chunks leaked into the feed: %d message events (§6, G1 F4)", n)
	}

	b3 := smokeBundle("haiku")
	b3.Task.Attempt = 3
	gone := *first.SessionRef
	gone.SessionID = "00000000-0000-4000-8000-000000000000"
	b3.Resume = &gone
	f3, third := smokeAttempt(t, b3, nil)
	if third.ResumeOutcome != "cold_start" {
		t.Fatalf("a session the runtime never had must cold start, not fail (E8-02): %+v", third)
	}
	ev := f3.sink.find("runtime", "resume", "cold_start")
	if len(ev) != 1 {
		t.Fatalf("cold start events %+v", f3.sink.find("runtime", "resume", ""))
	}
	t.Logf("cold_start payload: %+v", ev[0].Payload)
	if s, _ := ev[0].Payload["detail"].(string); !strings.Contains(s, "-32002") {
		t.Errorf("cold start detail = %q — the adapter's own code and wording is the evidence (D-11)", s)
	}
}

// The §5 order against the real adapter: session/cancel and drain before the
// signal, finish outcome cancelled, process tree clean (E10-03, E10-13).
func TestSmokeP3CancelOrder(t *testing.T) {
	smokeGate(t)
	b := smokeBundle("haiku")
	b.Prompt = "Run `sleep 120` with the Bash tool, then reply DONE."
	dir := t.TempDir()
	cmd, args := acp.Command(contracts.RuntimeClaudeCode, "", nil)
	env := acp.Env(contracts.RuntimeClaudeCode, acp.TaskEnv{AgentName: "smoke"}, nil)
	f := &fixture{t: t, sink: &memSink{}, dir: dir}
	f.runner = acp.New(acp.Attempt{
		Bundle: b, Workdir: dir,
		Cmd:  acp.Config{Command: cmd, Args: args, Env: env, KillAfter: 10 * time.Second},
		Sink: f.sink, DaemonVersion: "smoke", SetupTimeout: 4 * time.Minute,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.runner.Run(ctx); close(done) }()
	defer f.sink.assertSchema(t)
	waitFor(t, func() bool { return len(f.sink.find("tool", "run_shell", "started")) > 0 })
	f.runner.Cancel(context.Background(), acp.CancelRequest{Reason: "director"})
	<-done

	var steps []string
	for _, e := range f.sink.find("runtime", "cancel", "info") {
		d, _ := e.Payload["detail"].(string)
		if fields := strings.Fields(d); len(fields) >= 3 && fields[0] == "§5" {
			steps = append(steps, fields[2])
		}
	}
	t.Logf("§5 steps: %v outcome=%s", steps, res.Outcome)
	if indexOfStep(steps, "session_cancel") < 0 || indexOfStep(steps, "drain") < 0 {
		t.Fatalf("steps = %v, want the §5 sequence", steps)
	}
	if indexOfStep(steps, "signal_process_group") < indexOfStep(steps, "drain") {
		t.Fatalf("steps = %v — the group is signalled only after the drain (§8.2.2)", steps)
	}
	if res.Outcome != "cancelled" {
		t.Fatalf("outcome = %q, want cancelled", res.Outcome)
	}
	assertGroupGone(t, res.PGID)
}
