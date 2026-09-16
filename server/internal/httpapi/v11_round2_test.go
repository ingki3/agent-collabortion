package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/eventschema"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roles"
)

// V-1 (#246 NN1~NN3) — the v1.1 first-round review's open items on the
// empty-turn card and the role gate.

// TestV11EmptyTurnMessageClauseAlone is NN1: the "메시지 0" clause of
// tasks.emptyTurn pinned on its own. `colab message post` leaves a message
// row AND a §4 status row, so TestV11EmptyTurnCard's message-only turn was
// held by the status clause too — deleting the message clause changed
// nothing. Here the message row exists with NO status row (the message was
// written by the platform on the task's behalf, or the §4 row was lost), and
// the card must still not be written.
func TestV11EmptyTurnMessageClauseAlone(t *testing.T) {
	f := newP2Fixture(t)
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	f.fake.Advance(1) // the row must sit at or after the attempt's dispatch
	if _, err := f.pool.Exec(t.Context(), `
		INSERT INTO message (session_id, author_type, author_id, content, mentions, source_task_id, kind, created_at)
		VALUES ($1, 'agent', $2, '메시지만 남긴 턴', '{}', $3, 'text', $4)`,
		mustUUID(t, f.sessionID), f.rUUID, taskID, f.fake.Now()); err != nil {
		t.Fatal(err)
	}
	var statusRows int
	if err := f.pool.QueryRow(t.Context(), `SELECT count(*) FROM task_event WHERE task_id = $1 AND class = 'status'`, taskID).Scan(&statusRows); err != nil {
		t.Fatal(err)
	}
	if statusRows != 0 {
		t.Fatalf("premise broken: %d status rows before finish, want 0 — this test is about the message clause ALONE", statusRows)
	}
	f.finishTurn(t, taskID, contracts.Finish{Outcome: "completed", StopReason: "end_turn"})
	if n := f.emptyTurnCards(t, taskID, 1); n != 0 {
		t.Fatalf("a turn that posted a message (and nothing else the feed saw) got %d empty-turn cards, want 0 — the message clause must hold on its own (V-1 NN1)", n)
	}
}

// TestCommandVerbsComplete is NN2: every ColabCommand of colab-cli.md §2.5
// (roles.All, parsed from the contract) has a §4 verb, and the verb is one
// task_event.schema.json allows — so a refused command of ANY kind leaves
// its row on the feed, reads included.
func TestCommandVerbsComplete(t *testing.T) {
	all := roles.All()
	if len(all) != 13 {
		t.Fatalf("roles.All = %d commands, want the 13 of colab-cli.md §2.5", len(all))
	}
	for _, cmd := range all {
		verb, ok := commandVerbs[cmd]
		if !ok || verb == "" {
			t.Errorf("commandVerbs[%s] missing — a refusal of it would answer 403 with no feed row", cmd)
			continue
		}
		// The row the gate writes for a refusal, checked against the schema
		// exactly as tasks.InsertServerEvent will (S-52).
		if err := eventschema.ValidateServerEvent("status", verb, string(cmd), "rejected",
			map[string]any{"command": roles.CLIName(cmd), "rejected_reason": "command_not_allowed"}); err != nil {
			t.Errorf("commandVerbs[%s] = %q: the refusal row does not match task_event.schema.json — %v", cmd, verb, err)
		}
	}
	for cmd := range commandVerbs {
		if !cmd.Valid() {
			t.Errorf("commandVerbs names %q, which is not a ColabCommand", cmd)
		}
	}
}

// TestCommandAllowedReadsRoleOncePerRequest is NN3: the gate reads the
// agent's role from the row once per request and keeps it on the Principal.
// Proven from the cache side — a cached role decides the verdict even when
// the row says otherwise — and from the row side: the cache is filled by
// the first call, and a fresh request reads the row again (a role change
// takes effect on the next call, which is why it is not in the token).
func TestCommandAllowedReadsRoleOncePerRequest(t *testing.T) {
	f := newP2Fixture(t)
	f.setRole(t, f.rUUID, "reviewer")
	tok, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.runTask(t, taskID)
	sc, err := f.srv.Tokens.Verify(t.Context(), f.pool, tok)
	if err != nil {
		t.Fatal(err)
	}
	// 1. A request whose Principal already carries a role: the row is not
	// consulted. `lead` may delegate; the row (reviewer) may not.
	lead := "lead"
	r := withPrincipal(httptest.NewRequest("POST", "/x", nil), &Principal{Task: sc, agentRole: &lead})
	if p := f.srv.commandAllowed(r, gen.LaneDelegate); p != nil {
		t.Fatalf("with a cached role of lead, lane delegate = %v, want allowed — the cache decides within a request", p)
	}
	// 2. A fresh request: the first call reads the row (reviewer → 403) and
	// fills the cache; the second call sees the cache.
	pr := &Principal{Task: sc}
	r = withPrincipal(httptest.NewRequest("POST", "/x", nil), pr)
	if p := f.srv.commandAllowed(r, gen.LaneDelegate); p == nil || p.Code != "command_not_allowed" {
		t.Fatalf("reviewer lane delegate = %v, want 403 command_not_allowed", p)
	}
	if pr.agentRole == nil || *pr.agentRole != "reviewer" {
		t.Fatalf("Principal.agentRole after the first gate = %v, want reviewer cached", pr.agentRole)
	}
	// Flip the row: within THIS request the cached verdict stands …
	f.setRole(t, f.rUUID, "lead")
	if p := f.srv.commandAllowed(r, gen.LaneDelegate); p == nil {
		t.Fatalf("second gate in the same request read the row again — want the cached reviewer verdict (one read per request)")
	}
	// … and the next request reads the row again.
	r = withPrincipal(httptest.NewRequest("POST", "/x", nil), &Principal{Task: sc})
	if p := f.srv.commandAllowed(r, gen.LaneDelegate); p != nil {
		t.Fatalf("a new request after the role change = %v, want allowed — the role is per request, never in the token", p)
	}
}

// withPrincipal is what authenticate does for a verified credential.
func withPrincipal(r *http.Request, p *Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKey{}, p))
}
