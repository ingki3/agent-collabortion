package tasks

import (
	"fmt"
	"os"
	"testing"
)

// TestMain is S-52's assertion for this package: cancel, the kill switch, the
// S-51 cancel/turn-end race, the unpriced-model note and the budget re-check
// failure all write their feed rows here.
func TestMain(m *testing.M) {
	ResetServerEventViolations()
	code := m.Run()
	if v := ServerEventViolations(); len(v) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "\n%d server-written task_event rows do not match "+
			"contracts/task_event.schema.json (S-52):\n", len(v))
		for _, e := range v {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		code = 1
	}
	os.Exit(code)
}

// TestServerEventShapesAreClosed states the two rules the thirteen call sites
// broke, as rules rather than as a list of call sites: `status` is what the
// server records when it handles a colab CLI command, and it closes its payload
// to {command,args,result_ref,rejected_reason}; `runtime` is the server talking
// about itself, and its free-text slot is `detail`.
//
// The `args` object is where a human sentence goes on a `status` row — the
// schema leaves it open — which is why the fix did not need a contract change.
func TestServerEventShapesAreClosed(t *testing.T) {
	before := len(ServerEventViolations())
	for _, tc := range []struct {
		name                            string
		class, verb, objectRef, outcome string
		payload                         map[string]any
		ok                              bool
	}{
		{name: "a cancel records the command and carries its sentence in args",
			class: "status", verb: "cancel", objectRef: "director", outcome: "ok",
			payload: map[string]any{"command": "lane cancel",
				"args": map[string]any{"note": "사람이 중단함", "reason": "director"}}, ok: true},
		{name: "a top-level note is not a status payload key",
			class: "status", verb: "cancel", objectRef: "director", outcome: "ok",
			payload: map[string]any{"command": "lane cancel", "note": "사람이 중단함"}},
		{name: "status without a command is not a platform operation",
			class: "status", verb: "cancel", objectRef: "director", outcome: "ok",
			payload: map[string]any{"args": map[string]any{"note": "x"}}},
		{name: "note is not a verb",
			class: "status", verb: "note", objectRef: "cost.unpriced", outcome: "info",
			payload: map[string]any{"command": "x"}},
		{name: "pause is not a verb",
			class: "runtime", verb: "pause", objectRef: "budget", outcome: "info",
			payload: map[string]any{"detail": "x"}},
		{name: "error is not an outcome",
			class: "runtime", verb: "error", objectRef: "budget.enforce_failed", outcome: "error",
			payload: map[string]any{"detail": "x"}},
		{name: "a server diagnostic speaks through detail",
			class: "runtime", verb: "report", objectRef: "cost.unpriced", outcome: "info",
			payload: map[string]any{"detail": "가격표에 없는 모델"}, ok: true},
		{name: "a rejected platform command names its reason",
			class: "status", verb: "hitl", objectRef: "hitl.rejected", outcome: "rejected",
			payload: map[string]any{"command": "hitl ask", "rejected_reason": "hitl_already_open",
				"args": map[string]any{"question": "?"}}, ok: true},
	} {
		err := InsertServerEventValidateOnly(tc.class, tc.verb, tc.objectRef, tc.outcome, tc.payload)
		if tc.ok && err != nil {
			t.Errorf("%s: rejected: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: accepted — contracts/task_event.schema.json says otherwise", tc.name)
		}
	}
	// This test writes violations on purpose; trim back to what the suite had
	// before it, so TestMain still measures the PRODUCTION call sites.
	TrimServerEventViolations(before)
}
