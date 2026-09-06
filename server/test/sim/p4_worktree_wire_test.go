//go:build p4golden

// Wiring for the worktree double-write simulator.
//
// THE FILE KEEPS ITS `p4golden` TAG. Six of the seven hooks are daemon
// behaviour — `startWorktreeAttempt`, `killDaemon`, `restartDaemon`,
// `orphanWrite`, `markerCount`, `concurrentWriters` — and Go forbids importing
// `daemon/internal/…` from the server module. The harness file says so itself
// and rules out the shortcut: re-implementing pgid bookkeeping on the server to
// satisfy the hooks would make the simulator measure the simulator. T-D9
// exposes a test-usable entry point (a public package, or a small `cmd` this
// wiring drives as a subprocess) and binds them to THAT, then removes the tag.
//
// T-S9 owns exactly one hook, and it is bound here:
//
//	requeuedAttempt → tasks.RequeuedAttempt   the heartbeat-expiry re-queue
//	                                          (tasks.Service.ExpireStale, from
//	                                          cmd/server.scheduler) — the next
//	                                          attempt's lane and workdir, plus the
//	                                          previous attempt's revoked token
//
// Until T-D9 lands, `requireWorktreeWiring` stops at the first nil daemon hook,
// so binding this one changes nothing observable yet. It is bound anyway so
// that T-D9's PR has nothing of the server's left to do — the same hand-off
// shape `cliFallbackArgs` had in the E9 table (PR #121).
package sim

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// simPool is the database the wiring reads through. TestMain provisions it for
// this package (see the package's existing harness); a nil pool means the
// package skipped, and the daemon hooks would have stopped the run first
// anyway.
var simPool *pgxpool.Pool

func init() {
	requeuedAttempt = adaptRequeuedAttempt
}

func adaptRequeuedAttempt(taskID uuid.UUID) (worktreeLane, bool) {
	if simPool == nil {
		return worktreeLane{}, false
	}
	attempt, _, workdir, branch, revoked, err := tasks.RequeuedAttempt(context.Background(), simPool, taskID)
	if err != nil {
		return worktreeLane{}, false
	}
	return worktreeLane{TaskID: taskID, Attempt: attempt, Path: workdir, Branch: branch}, revoked
}

var _ = testing.Short
