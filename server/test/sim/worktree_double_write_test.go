//go:build p4golden

// Worktree double-write simulator — the `worktree` half of FR-9.1 (데몬 크래시와
// 고아 프로세스, M11), EVAL E11-05·E11-06 re-measured under `worktree`
// isolation, and the P4 gate's "kill -9 뒤 이중 쓰기 0" line (plan/P4_TASKS §3).
//
// THE SCENARIO. A lane is running under `worktree` isolation: its workdir is a
// real checkout with a `colab/<session>/<agent>` branch. The daemon is killed
// with SIGKILL, so it cannot clean up. The runtime process it spawned survives
// and keeps editing that checkout. The server's heartbeat sweep re-queues the
// task; the restarted daemon claims it and — under FR-6.4/C3 — binds the SAME
// worktree, because a `worktree` workdir belongs to the AGENT and is reused
// across that agent's lanes.
//
// That is the whole danger, and it is worse than P1's `none` case. Under
// `none` a second process in the same directory corrupts our own scratch
// files; under `worktree` it corrupts the user's repository, and git's index
// lock turns concurrent writes into failures that look like the agent's fault.
//
// WHAT MUST HOLD (FR-9.1's four bullets, read against a checkout):
//
//  1. The daemon recorded `pgid` + task id on disk BEFORE spawning
//     (daemon-protocol §5 표: `<workdir_root>/.colab/attempts/<task>.<attempt>.json`).
//  2. On restart it reads that record and kills the orphan BEFORE claiming.
//     "Before" is the entire property: claim-then-clean leaves a window in
//     which two processes hold the same checkout, and E11-05 measures that
//     window as ZERO.
//  3. The server revokes the re-queued task's `COLAB_TASK_TOKEN`, so an
//     orphan that outlives step 2 cannot post (FR-9.1 마지막 방어선).
//  4. The orphan's edits are NOT reverted (E11-06) — the resume prompt's
//     "workdir 상태를 먼저 확인하라" handles them. A daemon that "cleans" the
//     worktree with `git checkout .` destroys the work it was recovering.
//
// WHAT THIS HARNESS MUST NOT DO. It must not decide any of that itself. A
// simulator that tracks its own "is a process alive" flag proves only that the
// simulator can count — the same shape as a test that passes while production
// writes twice. Every verdict below comes from a hook the implementation
// wires; the local stand-in only replays "spawn, get killed, come back".
//
// WHY THE FAKE IS LOCAL. `daemon/acpfake` is a public package as of PR #121,
// but it speaks ACP — it does not model a daemon crash, and P4a may not change
// implementation code to make it do so. `runAttempt` here is the one function
// to swap if T-D9 grows a crash harness.
//
// WHERE THE HOOKS LIVE (T-D9/T-S9 ask). This file is in the SERVER module, and
// four of its hooks — `startWorktreeAttempt`, `killDaemon`, `restartDaemon`,
// `concurrentWriters` — are daemon behaviour that Go will not let this module
// import from `daemon/internal/…`. The P1 orphan work already has a home for
// it (`daemon/internal/orphan`), so the shape Lead should expect is: the
// daemon side exposes a test-usable entry point (a public package, or a small
// `cmd` the wiring drives as a subprocess) and the wiring PR binds these hooks
// to THAT. Re-implementing pgid bookkeeping on the server to satisfy the hooks
// would make this simulator measure the simulator — the exact failure the
// paragraph above rules out. If neither is possible before T-I4, the honest
// outcome is to leave the sim tagged and measure the property in the e2e
// script instead; that is Lead's call, not a thing the wiring PR should decide
// by quietly filling in a stub.
package sim

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// What the implementation must expose.
// ---------------------------------------------------------------------------

// worktreeLane is one lane running under `worktree` isolation.
type worktreeLane struct {
	SessionID uuid.UUID
	AgentID   uuid.UUID
	TaskID    uuid.UUID
	Attempt   int
	// Path is the checkout the lane works in.
	Path string
	// Branch is `colab/<session>/<agent>`.
	Branch string
}

// spawnRecord is the on-disk note FR-9.1 requires before a spawn.
type spawnRecord struct {
	PGID      int
	TaskID    uuid.UUID
	Attempt   int
	Path      string
	WrittenAt int64
	// WrittenBeforeSpawn is the ordering FR-9.1 names. A record written after
	// the fork is useless for exactly the crash it exists to survive.
	WrittenBeforeSpawn bool
}

// startWorktreeAttempt is wired by T-D9: prepare/reuse the worktree, record the
// pgid, spawn. It returns the lane as dispatched plus the record on disk.
var startWorktreeAttempt func(sessionID, agentID, taskID uuid.UUID, attempt int) (worktreeLane, spawnRecord)

// killDaemon is wired by T-D9: SIGKILL the daemon, leaving the runtime process
// group alive. It returns the pgid still running.
var killDaemon func() int

// orphanWrite is wired by T-D9/T-S9: the surviving process writes to the
// checkout and tries to post. It returns how many bytes landed in the file and
// the HTTP status the post got — FR-9.1's third bullet says that status must
// be a refusal once the task is re-queued.
var orphanWrite func(path, marker string) (fileWrites int, postStatus int)

// restartDaemon is wired by T-D9: bring the daemon back. It returns, in order,
// what it did — the ordering is the property under test, so a bare boolean
// would not distinguish "killed then claimed" from "claimed then killed".
var restartDaemon func() (steps []string, orphansKilled int)

// concurrentWriters is wired by T-D9: how many live processes currently hold
// the given checkout, measured from the OS (pgid liveness), not from our own
// bookkeeping.
var concurrentWriters func(path string) int

// markerCount is wired by T-D9/T-S9: how many times a marker appears in the
// file. Two occurrences of one edit is a duplicated write.
var markerCount func(path, marker string) int

// requeuedAttempt is wired by T-S9: the heartbeat sweep result — the next
// attempt's lane (same workdir under C3) and whether the previous attempt's
// task token was revoked.
var requeuedAttempt func(taskID uuid.UUID) (lane worktreeLane, tokenRevoked bool)

func requireWorktreeWiring(t *testing.T) {
	t.Helper()
	switch {
	case startWorktreeAttempt == nil:
		t.Fatalf("unimplemented: worktree attempt start with the pgid record (FR-9.1, " +
			"daemon-protocol §5). T-D9 must wire `startWorktreeAttempt` — see the P4a hand-off " +
			"report 'required API'")
	case killDaemon == nil || restartDaemon == nil:
		t.Fatalf("unimplemented: daemon crash and recovery (FR-9.1, E11-05). T-D9 must wire " +
			"`killDaemon`/`restartDaemon` — see the P4a hand-off report 'required API'")
	case orphanWrite == nil || markerCount == nil || concurrentWriters == nil:
		t.Fatalf("unimplemented: workdir observation (E11-05·06). T-D9 must wire " +
			"`orphanWrite`/`markerCount`/`concurrentWriters` — see the P4a hand-off report")
	case requeuedAttempt == nil:
		t.Fatalf("unimplemented: heartbeat sweep re-queue with token revocation (FR-9.1, " +
			"E11-03). T-S9 must wire `requeuedAttempt` — see the P4a hand-off report")
	}
}

// runAttempt is the runtime side: an agent that edits the checkout. It knows
// nothing and decides nothing; it writes its marker and returns.
func runAttempt(lane worktreeLane, marker string) int {
	n, _ := orphanWrite(lane.Path, marker)
	return n
}

// ---------------------------------------------------------------------------
// One round, examined in detail
// ---------------------------------------------------------------------------

func TestWorktreeCrashRecoverySingleRoundGolden(t *testing.T) {
	requireWorktreeWiring(t)

	sessionID, agentID, taskID := uuid.New(), uuid.New(), uuid.New()

	lane, rec := startWorktreeAttempt(sessionID, agentID, taskID, 1)

	// (1) the record exists and was written first.
	if rec.PGID == 0 || rec.TaskID != taskID {
		t.Fatalf("spawn record = %+v, want a pgid bound to task %s (FR-9.1 '데몬은 런타임 "+
			"프로세스를 띄울 때 pgid와 task id를 디스크에 기록')", rec, taskID)
	}
	if !rec.WrittenBeforeSpawn {
		t.Error("the record was written AFTER the spawn — in the crash it is written for, the " +
			"daemon may never reach that line and the orphan becomes invisible (FR-9.1)")
	}
	if rec.Path != lane.Path {
		t.Errorf("record path = %q, lane path = %q — the record has to name the checkout, or "+
			"recovery cannot tell which worktree the orphan holds", rec.Path, lane.Path)
	}
	if lane.Branch == "" {
		t.Errorf("lane branch = %q, want colab/<session>/<agent> (FR-6.4)", lane.Branch)
	}

	// attempt 1 does some work, then the daemon dies hard.
	runAttempt(lane, "<edit-1>")
	pgid := killDaemon()
	if pgid == 0 {
		t.Fatal("the runtime process did not survive the daemon — then this scenario is not " +
			"being exercised at all (FR-9.1: '자체 프로세스 그룹으로 띄운 런타임은 살아남는다')")
	}

	// The server sweeps and re-queues. Under C3 the same agent gets the same
	// worktree back.
	next, tokenRevoked := requeuedAttempt(taskID)
	if next.Path != lane.Path {
		t.Errorf("attempt 2 workdir = %q, want the same checkout %q — `worktree` binds one "+
			"workdir per AGENT (FR-6.4/C3), which is precisely why the double write is "+
			"possible here", next.Path, lane.Path)
	}
	if next.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", next.Attempt)
	}
	if !tokenRevoked {
		t.Error("the re-queue must revoke the attempt's COLAB_TASK_TOKEN — FR-9.1 calls this " +
			"the last line of defence, the one that works whether or not daemon recovery does")
	}

	// The orphan is still alive and still writing. Its file write we cannot
	// stop; its POST we must.
	_, status := orphanWrite(lane.Path, "<orphan>")
	if status != 401 && status != 403 {
		t.Errorf("orphan post status = %d, want 401/403 — a revoked token is the guard that "+
			"holds even if recovery is late (FR-9.1, E11-04)", status)
	}

	// Recovery: kill first, claim second.
	steps, killed := restartDaemon()
	if killed < 1 {
		t.Errorf("orphans killed = %d, want ≥ 1 (E11-05)", killed)
	}
	kIdx, cIdx := indexOf(steps, "kill_orphans"), indexOf(steps, "claim")
	if kIdx < 0 || cIdx < 0 {
		t.Fatalf("restart steps = %v, want both kill_orphans and claim (E11-05)", steps)
	}
	if kIdx > cIdx {
		t.Fatalf("restart order = %v — claiming before the orphan is gone opens exactly the "+
			"window E11-05 measures as zero: two processes in one checkout, and git's index "+
			"lock turns that into failures the agent gets blamed for", steps)
	}
	if n := concurrentWriters(lane.Path); n > 1 {
		t.Errorf("%d live processes hold %s, want ≤ 1 (E11-05 '같은 workdir에 프로세스 2개 "+
			"동시 존재 시간 = 0')", n, lane.Path)
	}

	// The orphan's edits stay. Cleaning them is the tempting "tidy" step and
	// it deletes the work the recovery exists to preserve.
	if markerCount(lane.Path, "<orphan>") == 0 {
		t.Error("the orphan's workdir changes were reverted — FR-9.1: '고아가 남긴 workdir " +
			"변경은 지우지 않는다', and the resume prompt's \"현재 상태를 먼저 확인하라\" is " +
			"what handles them (E11-06)")
	}

	// And attempt 2's own edit lands exactly once.
	runAttempt(next, "<edit-2>")
	if n := markerCount(next.Path, "<edit-2>"); n != 1 {
		t.Errorf("attempt 2's edit present %d times, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// 100 rounds, zero double writes
// ---------------------------------------------------------------------------

func TestWorktreeDoubleWriteHundredRoundsGolden(t *testing.T) {
	requireWorktreeWiring(t)

	const rounds = 100
	var overlaps, dupEdits, lateClaims int

	for i := 0; i < rounds; i++ {
		sessionID, agentID, taskID := uuid.New(), uuid.New(), uuid.New()
		lane, _ := startWorktreeAttempt(sessionID, agentID, taskID, 1)

		// Vary how far attempt 1 got before the crash, so the kill lands in a
		// different place each round instead of replaying one lucky shape.
		edits := 1 + i%3
		for e := 0; e < edits; e++ {
			runAttempt(lane, fmt.Sprintf("<edit-%d>", e+1))
		}

		killDaemon()
		next, _ := requeuedAttempt(taskID)
		steps, _ := restartDaemon()

		if k, c := indexOf(steps, "kill_orphans"), indexOf(steps, "claim"); k < 0 || c < 0 || k > c {
			lateClaims++
		}
		if concurrentWriters(next.Path) > 1 {
			overlaps++
		}

		// Attempt 2 redoes the same edits — it cannot remember what it did.
		for e := 0; e < edits; e++ {
			marker := fmt.Sprintf("<edit-%d>", e+1)
			runAttempt(next, marker)
			if n := markerCount(next.Path, marker); n > 1 {
				dupEdits += n - 1
			}
		}
	}

	if overlaps != 0 {
		t.Errorf("rounds with two live processes in one checkout = %d/%d, want 0 — PRD §11 "+
			"allows < 1%% for duplicate MESSAGES, but a second writer in a git checkout is not "+
			"a duplicate message, it is repository corruption (FR-9.1, E11-05)", overlaps, rounds)
	}
	if lateClaims != 0 {
		t.Errorf("rounds that claimed before cleaning orphans = %d/%d, want 0 (E11-05)",
			lateClaims, rounds)
	}
	if dupEdits != 0 {
		t.Errorf("duplicate edits over %d rounds = %d, want 0 — no idempotency key can "+
			"de-duplicate a file write; the resume prompt's workdir inspection is the only "+
			"mechanism (§8.4, E8-04 (4) read under worktree)", rounds, dupEdits)
	}
}

func indexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}
