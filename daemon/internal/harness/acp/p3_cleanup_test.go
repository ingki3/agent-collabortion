// P3 후속 정리 — 백로그 D-15.
//
// The cancel-order golden (p3_cancel_golden_test.go) reads the §5 lines and
// checks their ORDER; it cannot check that the line is there at all, because a
// missing step reads as index -1 and every "signal after drain" assertion is
// vacuously true. That is the hole D-15 names: `Run` used to close the
// process with nothing said, so a close that skipped the procedure and one
// that walked it looked identical in the feed. The golden's expectations are
// untouched — this file adds the row it could not carry.
package acp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// cancelSteps returns the §5 step names the attempt left, in order.
func cancelSteps(f *fixture) []string {
	var out []string
	for _, e := range f.sink.all() {
		if e.Class != "runtime" || e.Verb != "cancel" {
			continue
		}
		detail, _ := e.Payload["detail"].(string)
		if fields := strings.Fields(detail); len(fields) >= 3 && fields[0] == "§5" {
			out = append(out, fields[2])
		}
	}
	return out
}

func countStep(steps []string, want string) int {
	n := 0
	for _, s := range steps {
		if s == want {
			n++
		}
	}
	return n
}

// D-15 — signalling the process group and saying so are one action. The line
// is emitted inside closeProcess's sync.Once, so neither of the two paths that
// end the process (the §5 procedure and Run's own close) can take it silently,
// and it is still reported exactly once.
func TestSignalStepRidesWithTheCloseItself(t *testing.T) {
	f := newFixture(t, acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "working"}, {Hang: true}},
	}}}, bundle(contracts.RuntimeClaudeCode), nil)

	done := make(chan struct{})
	go func() { f.run(); close(done) }()
	waitFor(t, func() bool { return len(f.sink.find("runtime", "start", "started")) == 1 })
	f.runner.Cancel(context.Background(), acp.CancelRequest{Reason: "director"})
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the attempt never ended after the cancel")
	}

	steps := cancelSteps(f)
	if n := countStep(steps, "signal_process_group"); n != 1 {
		t.Fatalf("steps = %v, want exactly one signal_process_group — the process IS signalled here, "+
			"so a feed without the line (or with two) no longer describes what happened (D-15)", steps)
	}
	drain := indexOfStep(steps, "drain")
	signal := indexOfStep(steps, "signal_process_group")
	if drain >= 0 && signal < drain {
		t.Errorf("steps = %v — the signal is last, after the drain (harness §5, §8.2.2)", steps)
	}
}

// The control: an attempt nobody cancelled ends at the same closeProcess, and
// must NOT leave a §5 cancel line. Binding the two would otherwise put a
// cancel in the activity feed of every completed task.
func TestACompletedAttemptLeavesNoCancelStep(t *testing.T) {
	f := newFixture(t, acpfake.Script{Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "PONG"}},
	}}}, bundle(contracts.RuntimeClaudeCode), nil)

	if res := f.run(); res.Outcome != "completed" {
		t.Fatalf("outcome = %q, want completed", res.Outcome)
	}
	if steps := cancelSteps(f); len(steps) != 0 {
		t.Errorf("steps = %v, want none — nobody cancelled this attempt (D-15)", steps)
	}
}
