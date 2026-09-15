package acp_test

import (
	"context"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// D-19 — a cancel the SERVER issued because of budget is `paused_budget`,
// not `cancelled` (daemon-protocol §4.4).
//
// The two are different things to the server: `cancelled` ends the task,
// `paused_budget` pauses the SESSION so a Director who raises the cap resumes
// the same lane and the same workdir (FR-7.3 M9). Every cancel path in
// runner.go asked "was I cancelled?" before it asked why, so a
// `cancel {reason: "budget"}` came back as `cancelled` + failure_kind
// `cancelled` — the session ended and the money was spent for nothing. The
// reason is on the wire (§4.3); this reads it.
//
// Three paths reach the verdict and all three are covered: the prompt
// answering `stopReason: cancelled` (both rows below), the prompt failing
// with an error while the cancel intent is set, and classify() on a
// transport error (TestBudgetCancelThroughClassify).
func TestBudgetCancelReportsPausedBudget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script acpfake.Script
	}{
		{
			// The adapter answers the cancel: stopReason cancelled.
			name: "prompt answers cancelled",
			script: acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
				{Chunk: "spending"},
				{SleepMs: 400},
				{Chunk: "never"},
			}}}},
		},
		{
			// The adapter parks until session/cancel arrives — the §5
			// procedure's normal shape for a tool that is still running.
			name: "adapter parks until session/cancel",
			script: acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
				{Chunk: "spending"},
				{Hang: true},
			}}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, tc.script, bundle(contracts.RuntimeClaudeCode), nil)
			var res acp.Result
			done := make(chan struct{})
			go func() { res = f.run(); close(done) }()
			waitFor(t, func() bool { return f.sink.nPreviews() > 0 })
			f.runner.Cancel(context.Background(), acp.CancelRequest{Reason: "budget"})
			<-done

			if res.Outcome != "paused_budget" {
				t.Fatalf("outcome = %q, want paused_budget — the server's `cancel "+
					"{reason: budget}` is a session PAUSE (§4.4); reporting `cancelled` ends "+
					"the task and the Director's budget raise has nothing to resume (D-19)", res.Outcome)
			}
			if res.StopReason != "budget" {
				t.Errorf("stop_reason = %q, want budget", res.StopReason)
			}
			if res.Failure != nil {
				t.Errorf("failure = %+v, want none — §4.4: `paused_budget` carries NO "+
					"failure_kind, going over budget is policy, not an error", res.Failure)
			}
		})
	}
}

// The other reasons are unchanged: only `budget` becomes a pause.
func TestNonBudgetCancelStaysCancelled(t *testing.T) {
	for _, reason := range []string{"director", "kill_switch", "loop", "session_paused", ""} {
		t.Run("reason="+reason, func(t *testing.T) {
			s := acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{
				{Chunk: "working"}, {SleepMs: 400}, {Chunk: "never"},
			}}}}
			f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), nil)
			var res acp.Result
			done := make(chan struct{})
			go func() { res = f.run(); close(done) }()
			waitFor(t, func() bool { return f.sink.nPreviews() > 0 })
			f.runner.Cancel(context.Background(), acp.CancelRequest{Reason: reason})
			<-done
			if res.Outcome != "cancelled" || res.Failure == nil || res.Failure.Kind != contracts.FailCancelled {
				t.Fatalf("reason %q gave %+v, want cancelled + failure_kind cancelled", reason, res)
			}
		})
	}
}

// classify(): the transport dies while a budget cancel is in flight. Same
// verdict — the daemon knows why it was stopping.
func TestBudgetCancelThroughClassify(t *testing.T) {
	s := acpfake.Script{StayAlive: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "Bash", Kind: "execute", Command: "sleep 100"}},
		{HangForever: true},
	}}}}
	clk := clock.NewFake(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	f := newFixture(t, s, bundle(contracts.RuntimeClaudeCode), func(a *acp.Attempt) { a.Clock = clk })
	ctx, cancel := context.WithCancel(context.Background())
	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.runner.Run(ctx); close(done) }()
	waitFor(t, func() bool { return len(f.sink.find("tool", "run_shell", "started")) == 1 })
	go f.runner.Cancel(context.Background(), acp.CancelRequest{AfterCurrentTool: true, Reason: "budget"})
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	if res.Outcome != "paused_budget" || res.Failure != nil {
		t.Fatalf("result %+v — want paused_budget with no failure_kind (D-19)", res)
	}
}
