package realtime

import (
	"sync"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// The frame type is the contract's closed enum (openapi StreamEvent.type). A
// type outside it is a frame no client listens for — the web drops what is
// not in STREAM_EVENT_TYPES — and, since v0.3.0 (R4, D22), the old
// `session.updated` · `session.deleted` · `session.completion_progress` are
// exactly such types: the rooms and missions send the same events as room.* ·
// work.*.
//
// Publishing still goes through (dropping a frame the server mis-named would
// turn a naming bug into a lost update); the type is recorded so the httpapi
// suite's TestMain fails on it — the S-52 pattern (tasks.ServerEventViolations)
// for frames.

const maxTypeViolations = 100

var typeViolations struct {
	mu   sync.Mutex
	list []string
}

func checkType(typ string) {
	if gen.StreamEventType(typ).Valid() {
		return
	}
	typeViolations.mu.Lock()
	defer typeViolations.mu.Unlock()
	if len(typeViolations.list) < maxTypeViolations {
		typeViolations.list = append(typeViolations.list, typ)
	}
}

// TypeViolations lists the published frame types outside the contract enum.
func TypeViolations() []string {
	typeViolations.mu.Lock()
	defer typeViolations.mu.Unlock()
	return append([]string(nil), typeViolations.list...)
}

// ResetTypeViolations clears the list (a test suite's start).
func ResetTypeViolations() {
	typeViolations.mu.Lock()
	defer typeViolations.mu.Unlock()
	typeViolations.list = nil
}

// TrimTypeViolations drops what was recorded after the first n (a test that
// provokes one on purpose removes only its own).
func TrimTypeViolations(n int) {
	typeViolations.mu.Lock()
	defer typeViolations.mu.Unlock()
	if n < len(typeViolations.list) {
		typeViolations.list = typeViolations.list[:n]
	}
}
