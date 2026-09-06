package httpapi

import (
	"fmt"
	"os"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// TestMain is S-52's assertion, placed where it costs nothing and catches
// everything: this package's suite drives cancels, budget pauses, HITL
// requests and refusals, GC refusals, command TTL expiry, status changes,
// message posts, summaries and the daemon's own event batches, and every one of
// those writes server-side task_event rows through tasks.InsertServerEvent.
//
// Validating there is log-and-store in production (dropping a feed note the
// server mis-shaped would turn a cosmetic bug into lost information), so the
// place the mis-shape can still be caught for free is here. Before this, the
// server answered 422 to a DAEMON batch with an unknown payload key while
// writing thirteen of its own rows that broke the same rule.
func TestMain(m *testing.M) {
	tasks.ResetServerEventViolations()
	code := m.Run()
	if v := tasks.ServerEventViolations(); len(v) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "\n%d server-written task_event rows do not match "+
			"contracts/task_event.schema.json (S-52):\n", len(v))
		for _, e := range v {
			fmt.Fprintf(os.Stderr, "  - %s\n", e)
		}
		code = 1
	}
	os.Exit(code)
}

// TestServerEventValidationIsWired proves the check above can fail: a package
// whose assertion is "the counter is zero" is worth nothing if nothing ever
// increments it.
func TestServerEventValidationIsWired(t *testing.T) {
	before := len(tasks.ServerEventViolations())
	// `status` closes its payload to {command,args,result_ref,rejected_reason}
	// and requires `command` — this is exactly the shape thirteen call sites
	// had before S-52.
	f := newP2Fixture(t)
	f.post(t, map[string]any{"content": "no mention, no task"})
	if err := tasks.InsertServerEventValidateOnly("status", "note", "cost.unpriced", "info",
		map[string]any{"note": "사람이 읽는 문장", "model": "x"}); err == nil {
		t.Fatal("a status payload with a top-level `note`, an unknown `model` and no `command` " +
			"validated — the S-52 check is not wired to the schema")
	}
	got := tasks.ServerEventViolations()
	if len(got) != before+1 {
		t.Fatalf("violations recorded = %d, want %d", len(got), before+1)
	}
	// Trim only what this test added: resetting would throw away what every
	// other test in the package found before it ran.
	tasks.TrimServerEventViolations(before)
}
