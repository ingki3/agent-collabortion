// Golden table for the CANCEL PROCEDURE — EVAL E10-01, E10-02, E10-03, E10-14.
//
// 이월됨: server/internal/tasks/cancel_golden_test.go (P3a, PR #108) 의
// `cancelTurn` 행. 그 표는 서버 모듈에 있는데 절차를 실행하는 것은 데몬이라
// 서버 골든이 데몬 코드를 직접 부를 수 없다. plan/P3_TASKS.md T-D5 의 방법 (i)
// 대로 **기대값을 한 글자도 바꾸지 않고** 이 파일로 옮기고, 어댑터만 데몬 API 로
// 바꿨다(§0-8). 서버 쪽 파일은 이 스트림이 손대지 않는다(같은 지시문 금지 줄).
//
// The comments below are the original file's, kept verbatim, because they are
// the reason the table looks the way it does:
//
//   - The cancel ORDER is the contract, not an implementation detail: wait for
//     an in-flight edit (≤30s) → answer the pending permission request →
//     `session/cancel` → drain → only then signal the process group. Killing
//     first corrupts the runtime's own history and can leave a half-written
//     file (§8.2.2, harness §5).
//
// WHAT THE ADAPTER HAD TO DECIDE (and why it is not judging)
//
// `cancelCase.PermissionPending` — "a session/request_permission awaiting an
// answer" — cannot arise against a client that answers on arrival, and harness
// §5 says so itself: "권한 대기 중 취소는 실기에서 발동하지 않아(권한이 먼저
// allow됨) 가짜 에이전트 테스트로 커버." The fake creates it: the request
// arrives inside the §5 step-1 window, where the runner now PARKS it (it will
// not authorise a new tool after a human pressed 중단) and answers it
// `cancelled` at step 2. Realising that case therefore needs a tool in flight
// as well; every assertion below still measures the real procedure.
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

// cancelCase is the runtime state when the human presses 중단.
type cancelCase struct {
	// LastEvent is the most recent task_event: "edit_started" (no completion
	// yet), "edit_completed", "shell_started", or "" for an idle turn.
	LastEvent string
	// PermissionPending is a `session/request_permission` awaiting an answer.
	PermissionPending bool
	// CompletionAfter is how long the in-flight tool takes to report done.
	// Longer than 30s exercises E10-02.
	CompletionAfter time.Duration
}

// cancelProcedure records what the daemon did, in order.
type cancelProcedure struct {
	// Steps is the ordered list of actions actually taken. The contract's
	// order (harness §5) is:
	//   wait_tool_completion? → answer_permission? → session_cancel → drain
	//   → signal_process_group
	Steps []string

	// HeldFor is how long the cancel waited for the in-flight tool.
	HeldFor time.Duration
	// ForcedAfterTimeout is E10-02: the 30-second cap expired.
	ForcedAfterTimeout bool
	// FeedNote is the activity-feed line a forced cancel leaves.
	FeedNote string

	// PermissionOutcome is what the pending permission request was answered
	// with (harness §4: `cancelled`, not allow/reject).
	PermissionOutcome string

	// ProcessTreeRemaining must be 0 (E10-03, E11-07).
	ProcessTreeRemaining int
	// ImmediateKill is true when the process was signalled before the drain.
	ImmediateKill bool
}

func indexOfStep(steps []string, want string) int {
	for i, s := range steps {
		if s == want {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// ADAPTER — production callers: loop.Daemon.cancelRun → acp.Runner.Cancel →
// cancelProcedure (the same entry the server `cancel` command, `revoke` and
// daemon shutdown all use). Nothing below decides anything; it builds an input
// state, runs the real procedure and reads the real task_event stream.
// ---------------------------------------------------------------------------

func cancelTurn(t *testing.T, c cancelCase) cancelProcedure {
	t.Helper()
	var steps []acpfake.Step
	toolKind := ""
	switch c.LastEvent {
	case "edit_started", "edit_completed":
		toolKind = "edit"
	case "shell_started":
		toolKind = "execute"
	}
	if c.PermissionPending && toolKind == "" {
		toolKind = "execute" // see the package comment
	}
	if toolKind != "" {
		steps = append(steps, acpfake.Step{ToolCall: &acpfake.ToolCallStep{
			ID: "tc1", Title: "in flight", Kind: toolKind, Path: "a.txt", Command: "sleep 100",
		}})
	}
	if c.LastEvent == "edit_completed" {
		steps = append(steps, acpfake.Step{ToolUpdate: &acpfake.ToolUpdateStep{ID: "tc1", Status: "completed"}})
	}
	if c.PermissionPending {
		steps = append(steps,
			acpfake.Step{SleepMs: 100},
			acpfake.Step{Permission: &acpfake.PermissionStep{ID: "p1", Title: "Bash rm", Kinds: []string{"allow_once", "reject_once"}}})
	}
	// A completion that must land INSIDE the hold is scripted late in real
	// time; the fake clock is what carries the contract's 5s/90s.
	completes := c.LastEvent != "" && c.LastEvent != "edit_completed" && !c.PermissionPending &&
		c.CompletionAfter > 0 && c.CompletionAfter < contracts.CancelDrainWait
	if completes {
		steps = append(steps, acpfake.Step{SleepMs: 1500}, acpfake.Step{ToolUpdate: &acpfake.ToolUpdateStep{ID: "tc1", Status: "completed"}})
	}
	steps = append(steps, acpfake.Step{Hang: true})

	clk := clock.NewFake(time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC))
	f := newFixture(t, acpfake.Script{Turns: []acpfake.Turn{{Steps: steps}}}, bundle(contracts.RuntimeClaudeCode),
		func(a *acp.Attempt) { a.Clock = clk })

	var res acp.Result
	done := make(chan struct{})
	go func() { res = f.run(); close(done) }()
	if toolKind != "" {
		verb := "edit_file"
		if toolKind == "execute" {
			verb = "run_shell"
		}
		waitFor(t, func() bool { return len(f.sink.find("tool", verb, "started")) == 1 })
	} else {
		waitFor(t, func() bool { return f.sink.nPreviews() >= 0 && len(f.sink.find("runtime", "start", "started")) == 1 })
	}
	cancelDone := make(chan struct{})
	go func() {
		f.runner.Cancel(context.Background(), acp.CancelRequest{AfterCurrentTool: true, Reason: "director"})
		close(cancelDone)
	}()
	waitFor(t, func() bool { return len(f.sink.find("runtime", "cancel", "started")) == 1 })
	switch {
	case c.PermissionPending:
		// Wait until the runtime really has asked, then let the 30s cap end
		// the hold — the fake is blocked on our answer and cannot complete
		// the tool it started.
		waitFor(t, func() bool {
			for _, r := range f.records() {
				if r.Method == acp.MethodRequestPermission {
					return true
				}
			}
			return false
		})
		advanceWhenWaiting(t, clk, contracts.CancelDrainWait+time.Second)
	case completes:
		advanceWhenWaiting(t, clk, c.CompletionAfter)
	case c.CompletionAfter >= contracts.CancelDrainWait:
		advanceWhenWaiting(t, clk, contracts.CancelDrainWait+time.Second)
	}
	<-cancelDone
	<-done

	p := cancelProcedure{PermissionOutcome: "", ProcessTreeRemaining: 0}
	for _, e := range f.sink.all() {
		switch {
		case e.Class == "runtime" && e.Verb == "cancel":
			step, _ := e.Payload["step"].(string)
			if step == "" {
				continue
			}
			p.Steps = append(p.Steps, step)
			if step == "wait_tool_completion" {
				if ms, ok := e.Payload["held_ms"].(int64); ok {
					p.HeldFor = time.Duration(ms) * time.Millisecond
				}
				if f, ok := e.Payload["forced"].(bool); ok && f {
					p.ForcedAfterTimeout = true
					p.FeedNote, _ = e.Payload["detail"].(string)
				}
			}
		case e.Class == "tool" && e.Verb == "permission":
			p.PermissionOutcome = e.Outcome
		}
	}
	sig := indexOfStep(p.Steps, "signal_process_group")
	drain := indexOfStep(p.Steps, "drain")
	p.ImmediateKill = sig >= 0 && drain >= 0 && sig < drain
	assertGroupGone(t, res.PGID)
	return p
}

// advanceWhenWaiting moves the injected clock once the cancel goroutine is
// parked in its select. The fake clock only fires timers that already exist.
func advanceWhenWaiting(t *testing.T, clk *clock.Fake, d time.Duration) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	clk.Advance(d)
}

func mustCancel(t *testing.T, c cancelCase) cancelProcedure {
	t.Helper()
	return cancelTurn(t, c)
}

func TestCancelProcedureGolden(t *testing.T) {
	t.Run(caseNameP3("E10-01", "an_unfinished_edit_holds_the_cancel_until_it_completes"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{LastEvent: "edit_started", CompletionAfter: 5 * time.Second})

		if p.HeldFor != 5*time.Second {
			t.Errorf("held for %s, want 5s — the cancel waits for the in-flight edit so a file is "+
				"not left half written (FR-3.4, harness §5 step 1)", p.HeldFor)
		}
		if p.ForcedAfterTimeout {
			t.Error("the edit completed inside 30s — this is not a forced cancel")
		}
		if i := indexOfStep(p.Steps, "wait_tool_completion"); i != 0 {
			t.Errorf("steps = %v, want the wait FIRST", p.Steps)
		}
	})

	t.Run(caseNameP3("E10-02", "the_hold_is_capped_at_thirty_seconds_and_is_recorded"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{LastEvent: "edit_started", CompletionAfter: 90 * time.Second})

		if p.HeldFor > 30*time.Second {
			t.Errorf("held for %s, want at most 30s — an unbounded wait turns 중단 into a button "+
				"that does nothing (FR-3.4)", p.HeldFor)
		}
		if !p.ForcedAfterTimeout {
			t.Error("past 30s the cancel proceeds regardless (E10-02)")
		}
		if p.FeedNote == "" {
			t.Error("the forced cancel is recorded in the activity feed — otherwise a truncated " +
				"edit looks like the agent's own work (E10-02)")
		}
	})

	t.Run(caseNameP3("E10-14", "an_unfinished_shell_command_holds_the_cancel_exactly_like_an_edit"), func(t *testing.T) {
		// EVAL v0.6's row. FR-3.4 and harness §5 step 1 say "파일 편집 **또는
		// 셸 명령**", but E10-01·02 only exercise the edit. A hold keyed on
		// the edit verb alone passes both of those and cancels a running
		// `rm -rf`/migration halfway — the irreversible case the rule exists
		// for.
		p := mustCancel(t, cancelCase{LastEvent: "shell_started", CompletionAfter: 5 * time.Second})

		if p.HeldFor != 5*time.Second {
			t.Errorf("held for %s, want 5s — a shell command in flight gets the same hold as an "+
				"edit (FR-3.4, harness §5 step 1, E10-14)", p.HeldFor)
		}
		if i := indexOfStep(p.Steps, "wait_tool_completion"); i != 0 {
			t.Errorf("steps = %v, want the wait FIRST for a shell command too (E10-14)", p.Steps)
		}
		if p.ForcedAfterTimeout {
			t.Error("the command completed inside 30s — this is not a forced cancel (E10-14)")
		}
	})

	t.Run(caseNameP3("E10-14", "a_shell_hold_is_capped_at_thirty_seconds_too"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{LastEvent: "shell_started", CompletionAfter: 90 * time.Second})

		if p.HeldFor > 30*time.Second {
			t.Errorf("held for %s, want at most 30s — the cap is the same for both tool kinds "+
				"(E10-14, FR-3.4)", p.HeldFor)
		}
		if !p.ForcedAfterTimeout {
			t.Error("past 30s the cancel proceeds regardless, shell or edit (E10-14)")
		}
		if p.FeedNote == "" {
			t.Error("a forced cancel is recorded in the feed whichever tool was in flight (E10-14)")
		}
	})

	t.Run(caseNameP3("E10-14", "a_completed_tool_is_not_waited_for_at_all"), func(t *testing.T) {
		// The control: the hold is for an UNFINISHED tool. Holding 30s on
		// every cancel would satisfy the two rows above and make 중단 feel
		// broken on an idle turn.
		p := mustCancel(t, cancelCase{LastEvent: "edit_completed"})
		if p.HeldFor != 0 {
			t.Errorf("held for %s, want 0 — the completion event already arrived, so there is "+
				"nothing to wait for (FR-3.4)", p.HeldFor)
		}
	})

	t.Run(caseNameP3("E10-03", "a_pending_permission_is_answered_before_session_cancel_and_the_tree_is_clean"), func(t *testing.T) {
		p := mustCancel(t, cancelCase{PermissionPending: true})

		perm := indexOfStep(p.Steps, "answer_permission")
		cancel := indexOfStep(p.Steps, "session_cancel")
		drain := indexOfStep(p.Steps, "drain")
		signal := indexOfStep(p.Steps, "signal_process_group")

		if perm < 0 || cancel < 0 || drain < 0 {
			t.Fatalf("steps = %v, want answer_permission → session_cancel → drain (harness §5)", p.Steps)
		}
		if !(perm < cancel && cancel < drain) {
			t.Errorf("steps = %v, wrong order: a pending permission left unanswered blocks the "+
				"agent loop, so session/cancel never gets processed (harness §5 steps 2-4)", p.Steps)
		}
		if signal >= 0 && signal < drain {
			t.Errorf("steps = %v — the process group is signalled only AFTER the drain; killing "+
				"mid-turn breaks the runtime's stored history (§8.2.2)", p.Steps)
		}
		if p.PermissionOutcome != "cancelled" {
			t.Errorf("permission outcome = %q, want cancelled — answering allow/reject would run "+
				"or refuse a tool we are abandoning (harness §4)", p.PermissionOutcome)
		}
		if p.ImmediateKill {
			t.Error("the process is not killed immediately (E10-03)")
		}
		if p.ProcessTreeRemaining != 0 {
			t.Errorf("process tree remaining = %d, want 0 (E10-03, E11-07)", p.ProcessTreeRemaining)
		}
	})
}
