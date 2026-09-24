package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// FR-7.2 (v1.1, K-18) — a turn that ends on `end_turn` with no message, no
// platform operation and no file edit leaves ONE information card
// (status/turn_end/"empty_turn", outcome info). Three attempts, each doing one
// of the things that make a turn non-empty, and one doing nothing; then the
// server's own rows (a cancel note) proving they do not count as "doing
// something", a repeated finish proving the card is written once, and a turn
// the server stopped (cancelled) proving the judgment is `end_turn` only.

// emptyTurnCards counts the FR-7.2 rows of one attempt.
func (f *p2Fixture) emptyTurnCards(t *testing.T, taskID uuid.UUID, attempt int) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(), `
		SELECT count(*) FROM task_event
		WHERE task_id = $1 AND attempt = $2 AND class = 'status' AND verb = 'turn_end'
		  AND object_ref = to_jsonb($3::text) AND outcome = 'info'
		  AND payload->>'command' = 'turn_end' AND payload->'args'->>'note' = '아무것도 하지 않고 턴을 끝냈습니다'`,
		taskID, attempt, tasks.EmptyTurnObjectRef).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestV11EmptyTurnCard(t *testing.T) {
	f := newP2Fixture(t)
	endTurn := contracts.Finish{Outcome: "completed", StopReason: "end_turn"}
	insertEvent := func(taskID uuid.UUID, class, verb string, payload string) {
		t.Helper()
		if _, err := f.pool.Exec(t.Context(), `
			INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
			VALUES ($1, 1, (SELECT COALESCE(max(seq), 0) + 1 FROM task_event WHERE task_id = $1 AND attempt = 1 AND seq < 1000),
			        $2, $3, to_jsonb('x'::text), 'ok', $4::jsonb, $5)`,
			taskID, class, verb, payload, f.fake.Now()); err != nil {
			t.Fatal(err)
		}
	}

	// 1. message only — `colab message post` through the task token: the
	// message row AND the §4 status row; either alone would already count.
	tok, msgOnly := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, msgOnly)
	agent := &client{t: t, srv: f.api.srv, bearer: tok}
	if st, out, _ := agent.do("POST", f.p+"/rooms/"+f.sessionID+"/messages", map[string]any{"content": "한 마디"}, "Idempotency-Key", uuid.NewString()); st != 201 {
		t.Fatalf("post = %d %v", st, out)
	}
	f.finishTurn(t, msgOnly, endTurn)
	if n := f.emptyTurnCards(t, msgOnly, 1); n != 0 {
		t.Errorf("message-only turn got %d empty-turn cards, want 0", n)
	}

	// 2. edit only — a daemon-reported `tool edit_file`, nothing else.
	_, editOnly := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.runTask(t, editOnly)
	insertEvent(editOnly, "tool", "edit_file", `{"tool_call_id":"c1","kind":"edit","path":"a.md","lines_added":1,"lines_removed":0}`)
	f.finishTurn(t, editOnly, endTurn)
	if n := f.emptyTurnCards(t, editOnly, 1); n != 0 {
		t.Errorf("edit-only turn got %d empty-turn cards, want 0", n)
	}

	// 3. nothing — the card, exactly once, and the S-52 shape (TestMain counts
	// violations; the literal is in tasks/emptyturn.go for S-54).
	_, nothing := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, nothing)
	// The daemon's own rows (a `say` with no text posted, a `runtime start`,
	// a `read`) are not the three things the card is about.
	insertEvent(nothing, "runtime", "start", `{"runtime_kind":"claude_code"}`)
	insertEvent(nothing, "message", "think", `{"kind":"thought","text":"…","chars":1}`)
	insertEvent(nothing, "tool", "read", `{"tool_call_id":"c2","kind":"read","path":"a.md"}`)
	f.finishTurn(t, nothing, endTurn)
	if n := f.emptyTurnCards(t, nothing, 1); n != 1 {
		t.Fatalf("empty turn got %d cards, want exactly 1", n)
	}
	// A repeated finish (the daemon re-sends) does not stack a second card.
	if _, err := f.srv.finishAndEnforce(t.Context(), nothing, 1, endTurn); err != nil {
		t.Fatal(err)
	}
	if n := f.emptyTurnCards(t, nothing, 1); n != 1 {
		t.Errorf("after a repeated finish: %d cards, want 1", n)
	}
	// The card is on the feed the web reads (listTaskEvents), as an info row.
	events := f.api.must(200, "GET", f.p+"/tasks/"+nothing.String()+"/events", nil)
	found := false
	for _, raw := range events["items"].([]any) {
		e := raw.(map[string]any)
		if str(e, "verb") == "turn_end" && str(e, "outcome") == "info" {
			found = true
		}
	}
	if !found {
		t.Errorf("empty-turn card not on the task's event feed: %v", events["items"])
	}

	// 4. a status row the SERVER wrote — a Director cancel note — is not the
	// agent doing something. The turn still ends on its own (S-51 race shape:
	// the cancel arrives, the turn completes anyway) and is judged empty.
	_, cancelled := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.runTask(t, cancelled)
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO task_event (task_id, attempt, seq, class, verb, object_ref, outcome, payload, created_at)
		VALUES ($1, 1, 1073741824, 'status', 'cancel', to_jsonb('director'::text), 'ok', '{"command":"cancel"}', $2)`,
		cancelled, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	f.finishTurn(t, cancelled, endTurn)
	if n := f.emptyTurnCards(t, cancelled, 1); n != 1 {
		t.Errorf("server-written status row hid the empty turn: %d cards, want 1", n)
	}

	// 5. not `end_turn` — a completed turn the runtime cut short (max_tokens)
	// and a turn the server cancelled are not "did nothing".
	_, cut := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, cut)
	f.finishTurn(t, cut, contracts.Finish{Outcome: "completed", StopReason: "max_tokens"})
	if n := f.emptyTurnCards(t, cut, 1); n != 0 {
		t.Errorf("max_tokens turn got %d empty-turn cards, want 0", n)
	}
	_, stopped := f.agentToken(t, f.sessionID, f.wUUID, "W")
	f.runTask(t, stopped)
	f.finishTurn(t, stopped, contracts.Finish{Outcome: "cancelled", StopReason: "cancelled"})
	if n := f.emptyTurnCards(t, stopped, 1); n != 0 {
		t.Errorf("cancelled turn got %d empty-turn cards, want 0", n)
	}

	// The observation row counts what was written: 5 completed attempts
	// (msgOnly · editOnly · nothing · cancelled-note · max_tokens), 2 empty → 0.4.
	obs := f.api.must(200, "GET", f.p+"/workspaces/"+f.wsID+"/observations", nil)
	for _, raw := range obs["rows"].([]any) {
		r := raw.(map[string]any)
		if str(r, "key") == "empty_turn_rate" && (r["n"].(float64) != 5 || r["value"].(float64) != 0.4) {
			t.Errorf("empty_turn_rate = n %v value %v, want 5 · 0.4", r["n"], r["value"])
		}
	}
}
