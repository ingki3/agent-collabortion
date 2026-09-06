package httpapi

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/runtimes"
	"github.com/ingki3/agent-collabortion/server/internal/testdb"
)

// TestP4RebindPromptReachesTheBundle is S-53 (found reviewing #162's NN5).
//
// `rebindSession` (openapi) says a `worktree` rebind "새 workdir을 만들고 첫
// 프롬프트에 'diff 아티팩트 N개를 제출 순서대로 적용한 뒤 이어가라'를
// 넣는다(E14-06)". P4 built that sentence — `runtimes.RebindPrompt` — into
// `RebindPlan.Prompt` and then dropped it on the floor: nothing stored it and
// `queue.buildBundle` never looked. The golden table stayed green because it
// pins the DECISION fields (`PromptSaysApplyArtifacts`, `PromptArtifactOrder`),
// not the bundle, so a rebound agent got a cold-start prompt, an empty workdir
// and no word about the diffs sitting in `{{COLAB_REBIND_DIR}}`.
//
// Three properties, in the order they can break:
//  1. the rebind writes the prompt and the next bundle carries it once;
//  2. a REQUEUE of that attempt keeps it — this is why it is not consumed when
//     the bundle is built. The scenario that produces a rebind is a machine
//     going away, and the machine it moved to can stall too (E5-03);
//  3. a `completed` finish clears it: the diffs are applied, and a prompt that
//     kept saying "apply them" would have the agent replay them every turn.
func TestP4RebindPromptReachesTheBundle(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	wsID := mustUUID(t, f.wsID)
	sessionID := mustUUID(t, f.sessionID)

	// A task for the agent to pick up after the move.
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 이어서 해라"})
	taskID := firstTaskOf(t, f, sessionID)

	// The session ran under `worktree` on a machine that has gone away, and it
	// submitted two diff artifacts before it did.
	if _, err := f.pool.Exec(ctx, `
		UPDATE session SET isolation = '{"kind":"worktree","remote_url":"git@github.com:acme/app.git"}',
		       status = 'paused', paused_reason = 'runtime_offline' WHERE id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	addDiffArtifact(t, f, sessionID, "step-1")
	addDiffArtifact(t, f, sessionID, "step-2")

	// The machine it is moving to: online, same repository.
	target := testdb.AddRuntime(t, f.pool, wsID, "mac-2", f.fake.Now())
	if _, err := f.pool.Exec(ctx, `
		UPDATE runtime SET repos = '[{"path":"/Users/x/app","remote_url":"git@github.com:acme/app.git"}]'
		WHERE id = $1`, target); err != nil {
		t.Fatal(err)
	}
	// The old runtime keeps the session's queued task pinned until the rebind
	// clears it, so park the task where a rebind finds it.
	if _, err := f.pool.Exec(ctx, `UPDATE task SET status = 'queued', runtime_id = NULL WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}

	plan, err := f.srv.Runtimes.Rebind(ctx, wsID, sessionID, target, true)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if !plan.PromptSaysApplyArtifacts || len(plan.PromptArtifactOrder) != 2 {
		t.Fatalf("plan did not build the E14-06 prompt: %+v", plan)
	}

	// (1) stored, and in the bundle.
	var stored string
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(rebind_prompt, '') FROM session WHERE id = $1`, sessionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "" {
		t.Fatal("the rebind stored no prompt — E14-06's instruction has no way to reach the agent (S-53)")
	}
	prompt := claimPromptOn(t, f, target, taskID)
	// The order the PLAN chose is submission order (`created_at, id`); the
	// bundle must list them the same way, or "제출 순서대로" is a lie.
	assertRebindBlock(t, prompt, plan.PromptArtifactOrder...)

	// (2) the attempt is requeued (the new machine stalls too). The instruction
	// must still be there, or the diffs are never applied and nothing says so.
	requeueTask(t, f, taskID)
	prompt2 := claimPromptOn(t, f, target, taskID)
	assertRebindBlock(t, prompt2, plan.PromptArtifactOrder...)

	// (3) the turn completes: the diffs are in the workdir now.
	f.endTurn(t, taskID)
	if err := f.pool.QueryRow(ctx, `SELECT COALESCE(rebind_prompt, '') FROM session WHERE id = $1`, sessionID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "" {
		t.Errorf("rebind prompt survived a completed turn: %q — every later turn would be told to "+
			"replay the same diffs", stored)
	}
	f.post(t, map[string]any{"content": router.MentionLink("Lead", f.leadUUID) + " 다음 단계"})
	next := latestTaskOf(t, f, sessionID)
	if next != taskID {
		if p := claimPromptOn(t, f, target, next); strings.Contains(p, "<rebind>") {
			t.Errorf("a later turn still carries <rebind>:\n%s", p)
		}
	}
}

// claimPromptOn is claimPrompt against a NAMED runtime: after a rebind the
// session runs on the new machine, and the fixture's helper always asks the
// first one.
func claimPromptOn(t *testing.T, f *p2Fixture, runtimeID, taskID uuid.UUID) string {
	t.Helper()
	bundles, err := f.srv.Queue.Claim(t.Context(), runtimeID.String(), 5, f.fake.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bundles {
		if b.Task.ID == taskID.String() {
			return b.Prompt
		}
	}
	t.Fatalf("the queue handed out no bundle for task %s (got %d)", taskID, len(bundles))
	return ""
}

func assertRebindBlock(t *testing.T, prompt string, arts ...uuid.UUID) {
	t.Helper()
	if n := strings.Count(prompt, "<rebind>"); n != 1 {
		t.Fatalf("<rebind> appears %d times, want 1 (S-53)\n--- prompt ---\n%s", n, prompt)
	}
	if n := strings.Count(prompt, runtimes.RebindDirPlaceholder); n != 1 {
		t.Errorf("`%s` appears %d times in the bundle, want 1 — the daemon substitutes it and a "+
			"leftover one is a failed(config) finish (harness §10)\n%s",
			runtimes.RebindDirPlaceholder, n, prompt)
	}
	last := -1
	for i, a := range arts {
		at := strings.Index(prompt, a.String())
		if at < 0 {
			t.Fatalf("artifact %d (%s) missing from the bundle prompt\n%s", i+1, a, prompt)
		}
		if at < last {
			t.Errorf("artifacts out of submission order in the bundle (E14-06)\n%s", prompt)
		}
		last = at
	}
	if i, j := strings.Index(prompt, "<rebind>"), strings.Index(prompt, "<history"); i >= 0 && j >= 0 && i > j {
		t.Errorf("<rebind> comes after the history — nothing in the prompt is true until the "+
			"workdir has the previous machine's work\n%s", prompt)
	}
}

func addDiffArtifact(t *testing.T, f *p2Fixture, sessionID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		INSERT INTO artifact (session_id, name, type, version, storage_ref, created_at)
		VALUES ($1, $2, 'diff', 1, $2, $3) RETURNING id`, sessionID, name, f.fake.Now()).Scan(&id); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}
	return id
}

func firstTaskOf(t *testing.T, f *p2Fixture, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id FROM task WHERE session_id = $1 ORDER BY created_at LIMIT 1`, sessionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func latestTaskOf(t *testing.T, f *p2Fixture, sessionID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT id FROM task WHERE session_id = $1 ORDER BY created_at DESC LIMIT 1`, sessionID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// requeueTask is E5-03's outcome without the three-minute wait: the attempt is
// gone and the task is queued again with the next attempt number.
func requeueTask(t *testing.T, f *p2Fixture, taskID uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(t.Context(), `
		UPDATE task SET status = 'queued', attempt = attempt + 1, runtime_id = NULL,
		       dispatched_at = NULL, started_at = NULL, heartbeat_at = NULL WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}
}
