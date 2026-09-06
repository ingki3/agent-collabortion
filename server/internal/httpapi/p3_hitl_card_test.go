package httpapi

import (
	"testing"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
)

// S-45 — a system-issued HITL puts a card on the timeline, like an agent's.
//
// SCREEN §4.5 places the HITL card in the CENTRE timeline: the Director reads
// the session, not the request table. The agent path (createHitlRequest) posted
// that card and stored its id in hitl_request.message_id; the three
// system-issued paths — the budget pause (in-turn and the post-turn one S-44
// added), the completion/budget approval and the loop pause — inserted the
// request row and the inbox item and nothing else, so T-I3 measured a session
// with an open platform request and zero HITL cards (43_).
//
// Each row below drives ONE production path and asserts the same two things:
// exactly one `hitl` message, and hitl_request.message_id pointing at it.

// hitlCards is every `hitl` message of the session with its author.
func (f *p2Fixture) hitlCards(t *testing.T) []struct {
	ID         uuid.UUID
	AuthorType string
	Content    string
} {
	t.Helper()
	rows, err := f.pool.Query(t.Context(), `
		SELECT id, author_type::text, content FROM message
		WHERE session_id = $1 AND kind = 'hitl' ORDER BY created_at, id`, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []struct {
		ID         uuid.UUID
		AuthorType string
		Content    string
	}
	for rows.Next() {
		var r struct {
			ID         uuid.UUID
			AuthorType string
			Content    string
		}
		if err := rows.Scan(&r.ID, &r.AuthorType, &r.Content); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// cardOf is the request's link back to its card (openapi HitlRequest.message_id).
func (f *p2Fixture) cardOf(t *testing.T, purpose string) *uuid.UUID {
	t.Helper()
	var id *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `
		SELECT message_id FROM hitl_request WHERE session_id = $1 AND purpose = $2`,
		f.sessionID, purpose).Scan(&id); err != nil {
		t.Fatalf("no %s hitl_request: %v", purpose, err)
	}
	return id
}

// assertOneCard is the shared assertion: one card, authored as expected, linked
// from the request.
func (f *p2Fixture) assertOneCard(t *testing.T, purpose, wantAuthor string) {
	t.Helper()
	cards := f.hitlCards(t)
	if len(cards) != 1 {
		t.Fatalf("%s: hitl messages = %d, want exactly 1 — SCREEN §4.5 puts the HITL card in the centre timeline", purpose, len(cards))
	}
	if cards[0].AuthorType != wantAuthor {
		t.Fatalf("%s: card author_type = %q, want %q", purpose, cards[0].AuthorType, wantAuthor)
	}
	link := f.cardOf(t, purpose)
	if link == nil {
		t.Fatalf("%s: hitl_request.message_id is NULL — the request has no card to open (openapi HitlRequest.message_id)", purpose)
	}
	if *link != cards[0].ID {
		t.Fatalf("%s: message_id = %s, want the card %s", purpose, *link, cards[0].ID)
	}
}

// TestP3HitlCardAgentPath is the baseline the three system paths are compared
// against: it already held, and it is here so a change to the shared helper
// that breaks the agent's card fails loudly too.
func TestP3HitlCardAgentPath(t *testing.T) {
	f := newP2Fixture(t)
	tok, _ := f.agentToken(t, f.sessionID, f.rUUID, "R")
	out := f.hitlOn(t, tok, map[string]any{
		"type": "choice", "question": "어느 쪽으로 갈까요?",
		"options": []string{"A", "B"}, "proposed_default": "A",
	}, 201)

	f.assertOneCard(t, "agent", "agent")
	cards := f.hitlCards(t)
	// The card body carries enough to answer without opening the request
	// (SCREEN §4.6).
	for _, want := range []string{"[HITL:choice]", "어느 쪽으로 갈까요?", "선택지: A · B", "에이전트 제안: A"} {
		if !contains(cards[0].Content, want) {
			t.Fatalf("card body = %q, missing %q", cards[0].Content, want)
		}
	}
	req, _ := out["hitl_request"].(map[string]any)
	if str(req, "message_id") != cards[0].ID.String() {
		t.Fatalf("createHitlRequest response message_id = %v, want %s", req["message_id"], cards[0].ID)
	}
}

// TestP3SystemHitlCardBudget is the budget pause. It goes through the POST-TURN
// path S-44 added (usage priced only at `finish`), which is the same insert the
// in-turn heartbeat branch reaches — one card either way.
func TestP3SystemHitlCardBudget(t *testing.T) {
	f := newP2Fixture(t)
	if _, err := f.pool.Exec(t.Context(), `UPDATE agent SET budget_per_task = 1 WHERE id = $1`, f.rUUID); err != nil {
		t.Fatal(err)
	}
	_, taskID := f.agentToken(t, f.sessionID, f.rUUID, "R")
	f.finishTurn(t, taskID, contracts.Finish{
		Outcome: "completed", StopReason: "end_turn",
		Usage: contracts.Usage{InputTokens: 1000, OutputTokens: 1000, CostUSD: 1.01},
	})

	f.assertOneCard(t, "budget", "system")
	cards := f.hitlCards(t)
	if !contains(cards[0].Content, "예산") {
		t.Fatalf("budget card body = %q, want the question the Director answers", cards[0].Content)
	}
	// The card threads to the task that crossed the line, so the lane card and
	// the timeline card point at the same run (FR-7.3 s-13).
	var src *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT source_task_id FROM message WHERE id = $1`, cards[0].ID).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src == nil || *src != taskID {
		t.Fatalf("card source_task_id = %v, want %s", src, taskID)
	}
}

// TestP3SystemHitlCardCompletionApproval is E6-01's "⬜ Director 승인": the
// platform asks whether the session may close. It carries no task.
func TestP3SystemHitlCardCompletionApproval(t *testing.T) {
	f := newP2Fixture(t)
	f.issueCompletionApproval(t)

	f.assertOneCard(t, string(sessions.CondUserApproval), "system")
	cards := f.hitlCards(t)
	if !contains(cards[0].Content, "종료 조건") {
		t.Fatalf("approval card body = %q, want the completion question", cards[0].Content)
	}
	var src *uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `SELECT source_task_id FROM message WHERE id = $1`, cards[0].ID).Scan(&src); err != nil {
		t.Fatal(err)
	}
	if src != nil {
		t.Fatalf("card source_task_id = %v, want NULL — the platform issued it, no task did (FR-2.2)", src)
	}
}

// TestP3SystemHitlCardLoopPause is FR-3.5: the session stops mid-conversation,
// which is the one pause a reader meets in the timeline itself.
func TestP3SystemHitlCardLoopPause(t *testing.T) {
	f := newP2Fixture(t)
	ctx := t.Context()
	f.api.must(200, "PATCH", f.p+"/workspaces/"+f.wsID+"/settings", map[string]any{
		"loop_limits": map[string]any{"max_pair_roundtrips": 1},
	})
	for i := 0; i < 3; i++ {
		for _, pair := range [][2]string{{f.lead, f.r}, {f.r, f.lead}} {
			if _, err := f.pool.Exec(ctx, `
				INSERT INTO session_hop (session_id, from_agent_id, to_agent_id, rule, created_at)
				VALUES ($1, $2, $3, 2, $4)`, f.sessionID, pair[0], pair[1], t0); err != nil {
				t.Fatal(err)
			}
		}
	}
	author := router.Author{Type: "agent", AgentID: &f.rUUID}
	if _, err := f.srv.Router.Post(ctx, mustUUID(t, f.sessionID), author, gen.MessageCreate{
		Content: router.MentionLink("Lead", f.leadUUID) + " 또",
	}); err != nil {
		t.Fatal(err)
	}

	f.assertOneCard(t, "loop", "system")
	if !contains(f.hitlCards(t)[0].Content, "루프 상한") {
		t.Fatalf("loop card body = %q, want the limit it names", f.hitlCards(t)[0].Content)
	}
}
