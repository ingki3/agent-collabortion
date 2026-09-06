package tasks

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/ingki3/agent-collabortion/server/internal/eventschema"
)

// S-52. The server writes task_event rows of its own — cancel notes, budget
// pauses, GC refusals, drift warnings — through InsertServerEvent and
// InsertServerEventOnce. Until now nothing checked them against
// contracts/task_event.schema.json, and thirteen call sites had drifted: a
// `note` key on a `status` payload the schema closes to
// {command,args,result_ref,rejected_reason}, a verb `note` and a verb `pause`
// that are in no enum, an outcome `error` that is in no enum.
//
// The asymmetry is what made it a defect rather than a wart: since S-41 (#162)
// the server answers 422 to a DAEMON batch carrying an unknown payload key,
// while writing rows that break the same rule. A reader of the feed — the web
// renderer, an e2e assertion, the next person to add a column — cannot tell
// which rows obey the contract.
//
// Production behaviour is log-and-store, not reject. A feed note exists to be
// read by a human deciding whether to intervene ("보여주지 않았으면 일어나지
// 않은 것이다", FR-7.2); dropping one because the server mis-shaped it turns a
// cosmetic bug into lost information, and the write is usually inside a
// transaction doing something far more important (a cancel, a completion).
// The violation is instead COUNTED, and tests assert the count is zero — which
// is where a mis-shaped row can still be fixed for free.
var (
	serverEventMu         sync.Mutex
	serverEventViolations []ServerEventViolation
)

// ServerEventViolation is one server-written row that does not match the
// schema, kept for the assertions in *_test.go.
type ServerEventViolation struct {
	Class, Verb, ObjectRef, Outcome string
	Err                             error
}

func (v ServerEventViolation) String() string {
	return fmt.Sprintf("%s/%s %s (outcome=%s): %v", v.Class, v.Verb, v.ObjectRef, v.Outcome, v.Err)
}

// checkServerEvent validates one server-written row. It never returns an error:
// see the log-and-store note above.
func checkServerEvent(class, verb, objectRef, outcome string, payload map[string]any) {
	err := eventschema.ValidateServerEvent(class, verb, objectRef, outcome, payload)
	if err == nil {
		return
	}
	serverEventMu.Lock()
	// Bounded: a mis-shaped call site fires on every request, and this slice
	// lives for the process. The first ones are the ones a test reports.
	if len(serverEventViolations) < 256 {
		serverEventViolations = append(serverEventViolations,
			ServerEventViolation{Class: class, Verb: verb, ObjectRef: objectRef, Outcome: outcome, Err: err})
	}
	serverEventMu.Unlock()
	slog.Warn("server task_event does not match contracts/task_event.schema.json (S-52)",
		"class", class, "verb", verb, "object_ref", objectRef, "outcome", outcome, "err", err)
}

// ServerEventViolations returns the schema violations recorded so far. Tests
// assert it is empty; nothing in production reads it.
func ServerEventViolations() []ServerEventViolation {
	serverEventMu.Lock()
	defer serverEventMu.Unlock()
	return append([]ServerEventViolation(nil), serverEventViolations...)
}

// ResetServerEventViolations clears the record. Only a TestMain calls it, at
// the start of a run — a test that clears it in the middle throws away what the
// tests before it found, which is how the first version of this check reported
// a clean suite over a deliberately broken call site.
func ResetServerEventViolations() {
	serverEventMu.Lock()
	serverEventViolations = nil
	serverEventMu.Unlock()
}

// TrimServerEventViolations drops everything after the first n. A test that
// writes a bad row ON PURPOSE takes the count before, and trims back to it
// after, so its own noise does not reach the package-wide assertion and the
// findings of every other test survive.
func TrimServerEventViolations(n int) {
	serverEventMu.Lock()
	if n >= 0 && n < len(serverEventViolations) {
		serverEventViolations = serverEventViolations[:n]
	}
	serverEventMu.Unlock()
}

// InsertServerEventValidateOnly runs the S-52 check without writing anything.
// It exists so a test in another package can prove the check is wired to the
// schema — an assertion that a counter is zero proves nothing on its own.
func InsertServerEventValidateOnly(class, verb, objectRef, outcome string, payload map[string]any) error {
	checkServerEvent(class, verb, objectRef, outcome, payload)
	return eventschema.ValidateServerEvent(class, verb, objectRef, outcome, payload)
}
