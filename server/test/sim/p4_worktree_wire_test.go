//go:build p4golden

// ADAPTER ONLY (P2_TASKS §0-8) — T-S9's half of the wiring for
// worktree_double_write_test.go.
//
// It binds the ONE server hook of that file. The other six are daemon
// behaviour and T-D9 bound them to `daemon/worktreesim` in
// p4_daemon_adapter_test.go; between the two files every hook is now live and
// the table runs with `go test -tags p4golden ./test/sim/`.
//
//	requeuedAttempt → tasks.Service.ExpireStale + Queue.Claim
//	                  (cmd/server.scheduler's 10s tick in production)
//
// WHY THIS ONE CANNOT BE A STAND-IN. `worktreesim.Harness.Requeue` exists and
// does the same thing in memory — T-D9 wrote it so the daemon-side mirror of
// these rows could run before this hook existed, and its doc says so. Leaving
// it bound here would make the table measure the harness's own bookkeeping:
// the property under test is that the SERVER's heartbeat sweep gives attempt 2
// the same checkout (FR-6.4/C3) and revokes attempt 1's token (FR-9.1's last
// line of defence), and a simulator that tracks its own flags proves only that
// the simulator can count.
//
// So every value returned below comes from Postgres after the real sweep ran:
// the attempt from `task.attempt`, the path and branch from the `workdir` row
// the lane binds, and `tokenRevoked` from `task_token.revoked_at`. The harness
// is then TOLD (`Requeue`), because it owns the fake runtime process's view of
// its own token — `orphanWrite`'s POST status is the harness answering "this
// token is dead", and that is the E11-04 half of the same rule. The server
// decides; the harness is informed.
package sim

import (
	"context"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

func init() {
	requeuedAttempt = adaptRequeuedAttempt
}

func adaptRequeuedAttempt(taskID uuid.UUID) (worktreeLane, bool) {
	ctx := context.Background()
	h := simCurrent()
	lane, ok := h.Requeue(taskID.String())
	if !ok {
		return worktreeLane{}, false
	}
	if err := ensureTask(taskID); err != nil {
		panic("sim: seed task: " + err.Error())
	}
	// The server has to know which checkout this lane holds, or "attempt 2 gets
	// the same workdir" is a claim about a row that does not exist. Under
	// `worktree` the workdir belongs to the AGENT (C3), which is the whole
	// reason the double write is possible here.
	if _, err := workdirs.Record(ctx, pool, workdirs.Report{
		Kind: "worktree", Path: lane.Path, SessionID: seedIDs.session,
		AgentID: &seedIDs.agent, Branch: &lane.Branch,
	}, fake.Now()); err != nil {
		panic("sim: record workdir: " + err.Error())
	}

	// The daemon died, so no heartbeat arrives. The SERVER's sweep
	// (daemon-protocol §7) is what notices — the simulator does not move the
	// row itself.
	if _, err := pool.Exec(ctx, `UPDATE task SET heartbeat_at = $2 WHERE id = $1`,
		taskID, fake.Now().Add(-2*contracts.HeartbeatExpiry)); err != nil {
		panic("sim: kill: " + err.Error())
	}
	if _, err := srv.Queue.ExpireStale(ctx, fake.Now()); err != nil {
		panic("sim: sweep: " + err.Error())
	}

	attempt, _, workdir, branch, revoked, err := tasks.RequeuedAttempt(ctx, pool, taskID)
	if err != nil {
		panic("sim: requeued attempt: " + err.Error())
	}
	return worktreeLane{
		SessionID: seedIDs.session, AgentID: seedIDs.agent, TaskID: taskID,
		Attempt: attempt, Path: workdir, Branch: branch,
	}, revoked
}
