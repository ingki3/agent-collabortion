//go:build p4golden

// P4 golden MIRROR — worktree double write (FR-9.1, E11-05·06, E8-04 (4)).
//
// The table of record is `server/test/sim/worktree_double_write_test.go`
// (PR #152). Four of its hooks — startWorktreeAttempt, killDaemon,
// restartDaemon, concurrentWriters — plus orphanWrite and markerCount are
// daemon behaviour that the server module cannot import; the wiring PR binds
// them to THIS package (that file's own "WHERE THE HOOKS LIVE" note, Lead
// confirmed 2026-09-07). `requeuedAttempt` is T-S9's, so the server-side file
// stays red until T-S9 lands; these rows run now, against real processes.
//
// Case names, assertions and failure messages are copied verbatim so a
// reviewer can diff the two files mechanically.
package worktreesim

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// newID stands in for the server-side table's uuid.New(): the rows only need
// task ids that do not collide, and the daemon module carries no dependency
// for that.
var idSeq atomic.Int64

func newID() string { return fmt.Sprintf("task-%d", idSeq.Add(1)) }

func newHarness(t *testing.T) *Harness {
	t.Helper()
	h, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func indexOf(list []string, want string) int {
	for i, s := range list {
		if s == want {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// One round, examined in detail
// ---------------------------------------------------------------------------

func TestWorktreeCrashRecoverySingleRoundGoldenMirror(t *testing.T) {
	h := newHarness(t)
	sessionID, agentID, taskID := "S", "backend", newID()

	lane, rec, err := h.StartAttempt(sessionID, agentID, taskID, 1)
	if err != nil {
		t.Fatal(err)
	}

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
	if lane.Branch != "colab/S/backend" {
		t.Errorf("lane branch = %q, want colab/S/backend (FR-6.4)", lane.Branch)
	}

	// attempt 1 does some work, then the daemon dies hard.
	if n, _ := h.Write(lane.Path, "<edit-1>"); n == 0 {
		t.Fatal("attempt 1 wrote nothing")
	}
	pgid := h.KillDaemon()
	if pgid == 0 {
		t.Fatal("the runtime process did not survive the daemon — then this scenario is not " +
			"being exercised at all (FR-9.1: '자체 프로세스 그룹으로 띄운 런타임은 살아남는다')")
	}

	// The server sweeps and re-queues. Under C3 the same agent gets the same
	// worktree back.
	next, tokenRevoked := h.Requeue(taskID)
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
	_, status := h.Write(lane.Path, "<orphan>")
	if status != 401 && status != 403 {
		t.Errorf("orphan post status = %d, want 401/403 — a revoked token is the guard that "+
			"holds even if recovery is late (FR-9.1, E11-04)", status)
	}

	// Recovery: kill first, claim second.
	steps, killed, err := h.Restart()
	if err != nil {
		t.Fatal(err)
	}
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
	if n := h.Writers(lane.Path); n > 1 {
		t.Errorf("%d live processes hold %s, want ≤ 1 (E11-05 '같은 workdir에 프로세스 2개 "+
			"동시 존재 시간 = 0')", n, lane.Path)
	}

	// The orphan's edits stay. Cleaning them is the tempting "tidy" step and
	// it deletes the work the recovery exists to preserve.
	if h.MarkerCount(lane.Path, "<orphan>") == 0 {
		t.Error("the orphan's workdir changes were reverted — FR-9.1: '고아가 남긴 workdir " +
			"변경은 지우지 않는다', and the resume prompt's \"현재 상태를 먼저 확인하라\" is " +
			"what handles them (E11-06)")
	}

	// And attempt 2's own edit lands exactly once.
	if n, _ := h.Write(next.Path, "<edit-2>"); n == 0 {
		t.Fatal("attempt 2 wrote nothing — its runtime never came up")
	}
	if n := h.MarkerCount(next.Path, "<edit-2>"); n != 1 {
		t.Errorf("attempt 2's edit present %d times, want 1", n)
	}
}

// ---------------------------------------------------------------------------
// 100 rounds, zero double writes
// ---------------------------------------------------------------------------

func TestWorktreeDoubleWriteHundredRoundsGoldenMirror(t *testing.T) {
	if testing.Short() {
		t.Skip("100 rounds spawn real processes")
	}
	const rounds = 100
	var overlaps, dupEdits, lateClaims int

	for i := 0; i < rounds; i++ {
		h := newHarness(t)
		sessionID, agentID, taskID := "S", fmt.Sprintf("agent%d", i), newID()
		lane, _, err := h.StartAttempt(sessionID, agentID, taskID, 1)
		if err != nil {
			t.Fatal(err)
		}

		// Vary how far attempt 1 got before the crash, so the kill lands in a
		// different place each round instead of replaying one lucky shape.
		edits := 1 + i%3
		for e := 0; e < edits; e++ {
			h.Write(lane.Path, fmt.Sprintf("<edit-%d>", e+1))
		}

		h.KillDaemon()
		next, _ := h.Requeue(taskID)
		steps, _, err := h.Restart()
		if err != nil {
			t.Fatal(err)
		}

		if k, c := indexOf(steps, "kill_orphans"), indexOf(steps, "claim"); k < 0 || c < 0 || k > c {
			lateClaims++
		}
		if h.Writers(next.Path) > 1 {
			overlaps++
		}

		// Attempt 2 redoes the same edits — it cannot remember what it did.
		for e := 0; e < edits; e++ {
			marker := fmt.Sprintf("<edit-%d>", e+1)
			before := h.MarkerCount(next.Path, marker)
			h.Write(next.Path, marker)
			if n := h.MarkerCount(next.Path, marker) - before; n > 1 {
				dupEdits += n - 1
			}
		}
		h.Close()
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

// REGRESSION INJECTION (P2_TASKS §0-12, one per item). A daemon that claims
// before it sweeps must make the row above FAIL — otherwise "0 overlaps" is
// only evidence that nothing was looked at.
func TestClaimBeforeSweepIsCaught(t *testing.T) {
	h := newHarness(t)
	taskID := newID()
	lane, _, err := h.StartAttempt("S", "backend", taskID, 1)
	if err != nil {
		t.Fatal(err)
	}
	h.Write(lane.Path, "<edit-1>")
	h.KillDaemon()
	next, _ := h.Requeue(taskID)

	steps, _, err := h.RestartClaimFirst()
	if err != nil {
		t.Fatal(err)
	}
	k, c := indexOf(steps, "kill_orphans"), indexOf(steps, "claim")
	if k < c {
		t.Fatalf("the injection did not reverse the order: %v", steps)
	}
	if n := h.Writers(next.Path); n < 2 {
		t.Fatalf("peak live writers in one checkout = %d, want ≥ 2 — the simulator cannot see "+
			"the window it exists to measure, so a green run above proves nothing", n)
	}
}
