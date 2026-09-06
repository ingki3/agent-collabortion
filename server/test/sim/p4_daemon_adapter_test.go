//go:build p4golden

// ADAPTER ONLY (P2_TASKS §0-8) — T-D9's half of the wiring for
// worktree_double_write_test.go.
//
// It binds the six DAEMON hooks of that file to `daemon/worktreesim`, the
// public package T-D9 added for exactly this (that file's "WHERE THE HOOKS
// LIVE" note: "the daemon side exposes a test-usable entry point … and the
// wiring PR binds these hooks to THAT"; Lead confirmed 2026-09-07). go.work
// puts both modules in one build, so this module can import it.
//
// It does NOT touch `requeuedAttempt` — that hook is the server's heartbeat
// sweep and belongs to T-S9. Until T-S9 wires it, requireWorktreeWiring still
// fails on that one hook and the table stays red HERE; the same rows run
// green today in the daemon module
// (daemon/worktreesim/p4_double_write_golden_test.go).
//
// Nothing below decides anything. Every function forwards to the harness and
// converts types; the verdicts are in the table and the measurements are in
// the daemon. An adapter that judged would make the table measure the adapter.
//
// There is no TestMain here on purpose — wire_test.go owns the package's one.
// The harness is created on the first hook call and REPLACED at the start of
// every round (attempt 1), which closes the previous round's processes; the
// writer processes also carry their own ten-minute deadline, so a test binary
// that dies mid-round leaves nothing behind.
package sim

import (
	"os"
	"sync"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/daemon/worktreesim"
)

var (
	simMu      sync.Mutex
	simHarness *worktreesim.Harness
	simDir     string
	simIDs     = map[string]uuid.UUID{}
)

func simNewRound() *worktreesim.Harness {
	simMu.Lock()
	defer simMu.Unlock()
	if simHarness != nil {
		simHarness.Close()
		_ = os.RemoveAll(simDir)
	}
	dir, err := os.MkdirTemp("", "colab-worktree-sim-")
	if err != nil {
		panic(err)
	}
	h, err := worktreesim.New(dir)
	if err != nil {
		panic(err)
	}
	simHarness, simDir = h, dir
	return h
}

func simCurrent() *worktreesim.Harness {
	simMu.Lock()
	h := simHarness
	simMu.Unlock()
	if h == nil {
		return simNewRound()
	}
	return h
}

func simRemember(u uuid.UUID) string {
	simMu.Lock()
	simIDs[u.String()] = u
	simMu.Unlock()
	return u.String()
}

func simUUID(s string) uuid.UUID {
	simMu.Lock()
	u, ok := simIDs[s]
	simMu.Unlock()
	if ok {
		return u
	}
	parsed, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return parsed
}

func simLane(l worktreesim.Lane) worktreeLane {
	return worktreeLane{
		SessionID: simUUID(l.SessionID),
		AgentID:   simUUID(l.AgentID),
		TaskID:    simUUID(l.TaskID),
		Attempt:   l.Attempt,
		Path:      l.Path,
		Branch:    l.Branch,
	}
}

func init() {
	startWorktreeAttempt = func(sessionID, agentID, taskID uuid.UUID, attempt int) (worktreeLane, spawnRecord) {
		h := simCurrent()
		if attempt == 1 {
			h = simNewRound()
		}
		lane, rec, err := h.StartAttempt(simRemember(sessionID), simRemember(agentID), simRemember(taskID), attempt)
		if err != nil {
			panic(err)
		}
		return simLane(lane), spawnRecord{
			PGID:               rec.PGID,
			TaskID:             simUUID(rec.TaskID),
			Attempt:            rec.Attempt,
			Path:               rec.Path,
			WrittenAt:          rec.WrittenAt,
			WrittenBeforeSpawn: rec.WrittenBeforeSpawn,
		}
	}
	killDaemon = func() int { return simCurrent().KillDaemon() }
	orphanWrite = func(path, marker string) (int, int) { return simCurrent().Write(path, marker) }
	restartDaemon = func() ([]string, int) {
		steps, killed, err := simCurrent().Restart()
		if err != nil {
			panic(err)
		}
		return steps, killed
	}
	concurrentWriters = func(path string) int { return simCurrent().Writers(path) }
	markerCount = func(path, marker string) int { return simCurrent().MarkerCount(path, marker) }
}
