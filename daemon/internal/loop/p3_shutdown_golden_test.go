// Golden row for daemon shutdown — EVAL E10-13 (a G3 regression row).
//
// 이월됨: server/internal/tasks/cancel_golden_test.go 의 `daemonSigterm` 행
// (P3a, PR #108). 실행 주체가 데몬이라 서버 모듈의 골든이 부를 수 없어
// plan/P3_TASKS.md T-D5 방법 (i) 대로 기대값을 한 글자도 바꾸지 않고 옮겼다.
// 어댑터만 데몬 API 다(§0-8). 서버 파일은 이 스트림이 손대지 않는다.
package loop

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
)

type shutdownResult struct {
	// Steps must be the same §8.2.2 sequence as a human cancel.
	Steps []string
	// FinishOutcome is what the daemon reports (daemon-protocol §4.4).
	FinishOutcome string
	// Requeued must be false: SIGTERM is an orderly stop, and re-queueing
	// hands the task straight back to a daemon that is going away.
	Requeued             bool
	ProcessTreeRemaining int
}

func indexOfStepP3(steps []string, want string) int {
	for i, s := range steps {
		if s == want {
			return i
		}
	}
	return -1
}

// ADAPTER — production callers: Daemon.Run's ctx cancellation → Daemon.stop →
// cancelRun → acp.Runner.Cancel. Nothing here judges; it cancels a real
// running attempt and reads the reported events and the finish body.
func daemonSigterm(t *testing.T) shutdownResult {
	t.Helper()
	srv := &memServer{queue: []contracts.TaskBundle{bundle("t-sigterm")}}
	script := acpfake.Script{StayAlive: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{
		{ToolCall: &acpfake.ToolCallStep{ID: "t1", Title: "Bash", Kind: "execute", Command: "sleep 100"}},
		{Hang: true},
	}}}}
	d, _ := newDaemon(t, srv, script)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	waitFor(t, 20*time.Second, func() bool { return len(srv.findEvents("tool", "run_shell", "started")) == 1 })
	cancel() // SIGTERM
	<-done

	r := shutdownResult{}
	for _, e := range srv.findEvents("runtime", "cancel", "") {
		if step, _ := e.Payload["step"].(string); step != "" {
			r.Steps = append(r.Steps, step)
		}
	}
	if n := srv.finished(); n == 1 {
		f := srv.finishes[0]
		r.FinishOutcome = f.Outcome
		// A daemon never re-queues anything itself (daemon-protocol §4.4 —
		// re-queueing is the server's). What it CAN do is report a retryable
		// `failure_kind`, which is the server's instruction to re-queue. So
		// "requeued" on this side is "we asked for a retry".
		r.Requeued = f.FailureKind != ""
	}
	if len(srv.phases) > 0 {
		if err := syscall.Kill(-srv.phases[0].PGID, 0); err == nil {
			r.ProcessTreeRemaining = 1
		}
	}
	return r
}

func TestDaemonShutdownCancelGolden(t *testing.T) {
	t.Run(caseNameP3("E10-13", "sigterm_cancels_the_running_attempt_through_the_same_procedure"), func(t *testing.T) {
		r := daemonSigterm(t)
		if indexOfStepP3(r.Steps, "session_cancel") < 0 || indexOfStepP3(r.Steps, "drain") < 0 {
			t.Errorf("steps = %v, want the §8.2.2 sequence — a shutdown that skips it leaves the "+
				"runtime session inconsistent", r.Steps)
		}
		if r.FinishOutcome != "cancelled" {
			t.Errorf("finish outcome = %q, want cancelled (daemon-protocol §4.4)", r.FinishOutcome)
		}
		if r.Requeued {
			t.Error("a cancelled attempt is not re-queued (E10-13)")
		}
		if r.ProcessTreeRemaining != 0 {
			t.Errorf("process tree remaining = %d, want 0", r.ProcessTreeRemaining)
		}
	})
}

// caseNameP3 keeps the P3a golden's subtest names byte-identical.
func caseNameP3(eval, name string) string {
	out := make([]byte, 0, len(eval))
	for i := 0; i < len(eval); i++ {
		if eval[i] == '-' {
			out = append(out, '_')
			continue
		}
		out = append(out, eval[i])
	}
	return string(out) + "_" + name
}
