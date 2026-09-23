package rooms

import (
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// TestActiveTaskStatusesTable is #299 review NN1: the SET behind
// ActiveTaskCountSQL, one line per task status (FR-7.1). The count tests
// only saw the statuses their fixtures happened to plant; a status dropped
// from the literal — or a new enum value nobody placed — would pass them
// and draw 「보관」 enabled on a room the server then refuses (or the reverse).
func TestActiveTaskStatusesTable(t *testing.T) {
	want := []struct {
		status gen.TaskStatus
		active bool
	}{
		{gen.TaskStatusDeferred, true},     // waits for its trigger — still the room's work
		{gen.TaskStatusQueued, true},       // in line
		{gen.TaskStatusDispatched, true},   // handed to a computer
		{gen.TaskStatusPreparing, true},    // folder being prepared
		{gen.TaskStatusRunning, true},      // turn in progress
		{gen.TaskStatusWaitingHuman, true}, // process gone, the task is not
		{gen.TaskStatusPaused, true},       // budget/time/loop — resumes into the room
		{gen.TaskStatusCompleted, false},   // terminal
		{gen.TaskStatusFailed, false},      // terminal
		{gen.TaskStatusCancelled, false},   // terminal
	}
	got := map[string]bool{}
	for _, s := range strings.Split(strings.Trim(activeTaskStatuses, "()"), ",") {
		got[strings.Trim(strings.TrimSpace(s), "'")] = true
	}
	seen := map[string]bool{}
	for _, w := range want {
		if !w.status.Valid() {
			t.Fatalf("%q is not a contract TaskStatus", w.status)
		}
		seen[string(w.status)] = true
		if got[string(w.status)] != w.active {
			t.Errorf("%-13s active = %v, want %v", w.status, got[string(w.status)], w.active)
		}
	}
	for s := range got {
		if !seen[s] {
			t.Errorf("activeTaskStatuses has %q, which this table does not place", s)
		}
	}
	if len(want) != 10 {
		t.Fatalf("table has %d statuses — task_status (0001) has 10; place every new one here", len(want))
	}
}
