//go:build p3golden

// Partial-execution simulator — EVAL E8-04 and E8-05, the `sim` verification
// tier of PLAN §5 and the "중복 0" half of the G6 DoD.
//
// THE SCENARIO (E8-04). An attempt posts M messages and applies N file edits,
// then the daemon is killed. The server re-queues the task, a second attempt
// starts on the same workdir, and the agent — which cannot remember what it
// already did — tries to do all of it again. Two mechanisms must make that
// harmless:
//
//	messages : the `task_id + seq` idempotency key (0001_init.sql:338,
//	           colab-cli.md §1 UUIDv5(task:<id>:<seq>)) plus the resume
//	           prompt's `posted_message_ids` (FR-7.1 M5 3, daemon-protocol
//	           §4.1).
//	edits    : the prompt instruction to inspect the workdir first (§8.4),
//	           since no key can de-duplicate a file write.
//
// E8-05 runs it 100 times and demands ZERO duplicates of either kind — PRD §11
// says "< 1%" and PLAN §5 tightens that to 0 in CI, because a 1% allowance on
// a deterministic mechanism only hides a bug.
//
// WHY THE FAKE IS LOCAL AND NOT `acpfake`
//
// The task brief names `daemon/internal/acpfake`. Go forbids importing another
// module's `internal/` tree (measured: "use of internal package
// github.com/ingki3/agent-collabortion/daemon/internal/acpfake not allowed"),
// and P3a may not modify implementation code to move it. So the runtime side
// is a local stand-in whose ONLY job is to replay "M posts + N edits, then
// die, then do it all again". It deliberately does NOT decide anything: every
// duplicate verdict comes from the server hooks below. If Lead moves acpfake
// to a public path, `replayAttempt` is the one function to swap.
//
// WHAT THIS HARNESS MUST NOT DO. It must not implement the idempotency key
// itself. A simulator that hashes (task, seq) locally and reports "0
// duplicates" proves only that the simulator can count — which is exactly the
// shape of a test that passes while production posts twice. Both hooks are
// therefore server-side.
package sim

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// ---------------------------------------------------------------------------
// What the implementation must expose.
// ---------------------------------------------------------------------------

// postAttempt is one `colab message post` as the CLI would send it: the
// content, and the seq the CLI derived its Idempotency-Key from (colab-cli.md
// §1 — the seq continues ACROSS attempts within a task, it does not restart).
type postAttempt struct {
	TaskID  uuid.UUID
	Seq     int
	Content string
}

// postResult is what the server answered.
type postResult struct {
	MessageID uuid.UUID
	// Created is false when the key replayed an existing message.
	Created bool
	// Replayed mirrors the `Idempotent-Replayed` response header.
	Replayed bool
	Status   int
}

// postMessage is wired by T-S5 to the real message endpoint (openapi
// postMessage). The simulator calls it; it never decides duplication itself.
var postMessage func(p postAttempt) postResult

// resumeContext is what the server hands attempt ≥ 2 (daemon-protocol §4.1).
type resumeContext struct {
	Attempt int
	Workdir string
	// PostedMessageIDs is the list the prompt tells the agent not to repeat.
	PostedMessageIDs []uuid.UUID
	// PromptSaysInspectWorkdir is §8.4's "workdir의 현재 상태를 먼저 확인하라".
	PromptSaysInspectWorkdir bool
	// LastSeq is what `/cli/context` reports, so attempt 2 continues the seq
	// series instead of restarting it (colab-cli.md §1).
	LastSeq int
}

// requeueAfterKill is wired by T-S5: kill the attempt, let the heartbeat sweep
// re-queue it, and return the context the next attempt receives.
var requeueAfterKill func(taskID uuid.UUID) resumeContext

// editRecord is one file edit the agent applied.
type editRecord struct {
	Path string
	// Marker is the content the edit inserts; a duplicated edit inserts it
	// twice, which is how the count is taken.
	Marker string
}

// applyEdit is wired by T-S5/T-D5 to the workdir under test. It returns how
// many times the marker is now present in the file.
var applyEdit func(workdir string, e editRecord) int

func requireWiring(t *testing.T) {
	t.Helper()
	switch {
	case postMessage == nil:
		t.Fatalf("unimplemented: message posting with the task_id+seq idempotency key " +
			"(openapi postMessage, colab-cli.md §1). T-S5 must wire `postMessage` — " +
			"see the P3a hand-off report 'required API'")
	case requeueAfterKill == nil:
		t.Fatalf("unimplemented: kill → heartbeat sweep → re-queue with the resume context " +
			"(FR-7.1 M5, daemon-protocol §4.1). T-S5 must wire `requeueAfterKill` — " +
			"see the P3a hand-off report 'required API'")
	case applyEdit == nil:
		t.Fatalf("unimplemented: workdir edit application (E8-04 (4)). T-S5/T-D5 must wire " +
			"`applyEdit` — see the P3a hand-off report 'required API'")
	}
}

// ---------------------------------------------------------------------------
// The stand-in agent.
// ---------------------------------------------------------------------------

// replayAttempt is the runtime side: an agent that always tries to do the
// whole job. On attempt 2 it re-reads its instructions, so it skips the
// messages `posted` lists and the edits already present in the workdir — but
// ONLY if the server actually told it. When the resume context is empty, it
// repeats everything, which is precisely the case the mechanisms must survive.
//
// It returns the posts it attempted and the edits it applied.
func replayAttempt(taskID uuid.UUID, workdir string, msgs, edits int, ctx resumeContext) ([]postResult, []int) {
	posted := map[uuid.UUID]bool{}
	for _, id := range ctx.PostedMessageIDs {
		posted[id] = true
	}

	seq := ctx.LastSeq
	results := make([]postResult, 0, msgs)
	for i := 0; i < msgs; i++ {
		seq++
		results = append(results, postMessage(postAttempt{
			TaskID: taskID, Seq: seq,
			Content: fmt.Sprintf("결과 보고 %d", i+1),
		}))
	}

	counts := make([]int, 0, edits)
	for i := 0; i < edits; i++ {
		e := editRecord{Path: fmt.Sprintf("notes-%d.md", i+1), Marker: fmt.Sprintf("<edit-%d>", i+1)}
		// An agent told to inspect the workdir first sees its own earlier work
		// and does not re-apply it. Without that instruction it just writes.
		if ctx.PromptSaysInspectWorkdir && ctx.Attempt > 1 {
			counts = append(counts, applyEdit(workdir, editRecord{Path: e.Path, Marker: ""}))
			continue
		}
		counts = append(counts, applyEdit(workdir, e))
	}
	return results, counts
}

// ---------------------------------------------------------------------------
// E8-04 — one round, examined in detail
// ---------------------------------------------------------------------------

func TestPartialExecutionSingleRoundGolden(t *testing.T) {
	requireWiring(t)

	const msgs, edits = 2, 2
	taskID := uuid.New()

	// Attempt 1: post 2, edit 2, then the daemon dies.
	first, firstEdits := replayAttempt(taskID, "/w/lane-1", msgs, edits, resumeContext{Attempt: 1})
	created := 0
	firstIDs := make([]uuid.UUID, 0, msgs)
	for _, r := range first {
		if r.Created {
			created++
			firstIDs = append(firstIDs, r.MessageID)
		}
	}
	if created != msgs {
		t.Fatalf("attempt 1 created %d messages, want %d", created, msgs)
	}
	for i, n := range firstEdits {
		if n != 1 {
			t.Fatalf("attempt 1 edit %d applied %d times, want 1", i+1, n)
		}
	}

	ctx := requeueAfterKill(taskID)

	// (1) the same workdir
	if ctx.Workdir != "/w/lane-1" {
		t.Errorf("attempt 2 workdir = %q, want the same /w/lane-1 (E8-04 (1), FR-7.1 M5 1)", ctx.Workdir)
	}
	if ctx.Attempt != 2 {
		t.Errorf("attempt = %d, want 2", ctx.Attempt)
	}
	// (2) the list of what was already posted
	if len(ctx.PostedMessageIDs) != msgs {
		t.Errorf("posted_message_ids = %v, want the %d messages of attempt 1 (E8-04 (2))",
			ctx.PostedMessageIDs, msgs)
	}
	if !ctx.PromptSaysInspectWorkdir {
		t.Error("the prompt must tell the agent to inspect the workdir first — it is the ONLY " +
			"defence for edits, which no idempotency key can de-duplicate (E8-04 (4), §8.4)")
	}
	if ctx.LastSeq < msgs {
		t.Errorf("last_seq = %d, want ≥ %d — the seq series continues across attempts, or attempt 2 "+
			"reuses attempt 1's keys for DIFFERENT content and the server 422s (colab-cli.md §1)",
			ctx.LastSeq, msgs)
	}

	// Attempt 2: the agent tries the whole job again.
	second, secondEdits := replayAttempt(taskID, ctx.Workdir, msgs, edits, ctx)

	dupMessages := 0
	for _, r := range second {
		if r.Created && !containsID(firstIDs, r.MessageID) {
			dupMessages++
		}
	}
	if dupMessages != 0 {
		t.Errorf("duplicate messages = %d, want 0 (E8-04 (3)) — the posted list plus the "+
			"task_id+seq key must absorb the repeat", dupMessages)
	}
	for i, n := range secondEdits {
		if n != 1 {
			t.Errorf("edit %d present %d times after the resume, want 1 (E8-04 (4))", i+1, n)
		}
	}
}

// ---------------------------------------------------------------------------
// E8-05 — 100 rounds, zero duplicates
// ---------------------------------------------------------------------------

func TestPartialExecutionHundredRoundsGolden(t *testing.T) {
	requireWiring(t)

	const rounds = 100
	var dupMessages, dupEdits int

	for i := 0; i < rounds; i++ {
		// Vary the interruption point: 1..3 messages and 1..3 edits, so the
		// kill lands in a different place each round rather than replaying
		// one lucky shape a hundred times.
		msgs := 1 + i%3
		edits := 1 + (i/3)%3
		taskID := uuid.New()
		workdir := fmt.Sprintf("/w/lane-%d", i)

		first, _ := replayAttempt(taskID, workdir, msgs, edits, resumeContext{Attempt: 1})
		firstIDs := make([]uuid.UUID, 0, msgs)
		for _, r := range first {
			if r.Created {
				firstIDs = append(firstIDs, r.MessageID)
			}
		}

		ctx := requeueAfterKill(taskID)
		second, secondEdits := replayAttempt(taskID, ctx.Workdir, msgs, edits, ctx)

		for _, r := range second {
			if r.Created && !containsID(firstIDs, r.MessageID) {
				dupMessages++
			}
		}
		for _, n := range secondEdits {
			if n > 1 {
				dupEdits += n - 1
			}
		}
	}

	if dupMessages != 0 {
		t.Errorf("duplicate messages over %d rounds = %d, want 0 — PRD §11 allows < 1%%, PLAN §5 "+
			"makes it 0 in CI because a tolerance on a deterministic key only hides a defect",
			rounds, dupMessages)
	}
	if dupEdits != 0 {
		t.Errorf("duplicate edits over %d rounds = %d, want 0 (E8-05)", rounds, dupEdits)
	}
}

func containsID(list []uuid.UUID, want uuid.UUID) bool {
	for _, id := range list {
		if id == want {
			return true
		}
	}
	return false
}
