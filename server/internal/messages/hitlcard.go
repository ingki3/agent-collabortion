package messages

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// The timeline card of a HITL request (SCREEN §4.5: the HITL card is a message
// in the CENTRE timeline, not a panel of its own).
//
// S-45: the agent path (httpapi.createHitl) posted this card and stored its id
// in hitl_request.message_id, and the three SYSTEM-issued paths — the budget
// pause (httpapi.applyBudgetPause, in-turn and the post-turn one S-44 added),
// the completion/budget approval (sessions.ApplyCompletionEvent) and the loop
// pause (router.pauseForLoop) — inserted the request and nothing else. The
// request existed, the inbox card existed, and the session timeline showed
// zero HITL cards (T-I3 measured 43_ with 0). One helper now, so a fourth
// system-issued request cannot be added without its card.
//
// Publishing is SystemPost's rule: a system message IS a timeline message, so
// it reaches S7 as `message.created` instead of only on reload (G4 2판 W10).
type HitlCard struct {
	// Type is the hitl_request.type (`question` · `choice` · `approval` ·
	// `info`), rendered in the card's header.
	Type            string
	Question        string
	Context         string
	Options         []string
	ProposedDefault string
	// AuthorAgentID is set for a request an agent raised. It is nil for the
	// platform's own requests, which are authored by `system` — the message
	// table's CHECK ties author_type = 'system' to a NULL author_id.
	AuthorAgentID *uuid.UUID
	// SourceTaskID threads the card to the task that raised it, where there is
	// one. A session-scoped budget or completion request has none.
	SourceTaskID *uuid.UUID
}

// CardBody is the card's text (SCREEN §4.6: enough to answer without opening
// the session).
func (c HitlCard) CardBody() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[HITL:%s] %s", c.Type, c.Question)
	if c.Context != "" {
		fmt.Fprintf(&b, "\n%s", c.Context)
	}
	if len(c.Options) > 0 {
		fmt.Fprintf(&b, "\n선택지: %s", strings.Join(c.Options, " · "))
	}
	if c.ProposedDefault != "" {
		fmt.Fprintf(&b, "\n에이전트 제안: %s", c.ProposedDefault)
	}
	return b.String()
}

// PostHitlCard inserts the card and publishes `message.created`. The caller
// stores the returned id in hitl_request.message_id — openapi HitlRequest
// declares that field, and a null there is what S7 reads as "no card".
//
// production callers: httpapi.createHitl (source=agent),
// httpapi.applyBudgetPause, sessions.ApplyCompletionEvent, router.pauseForLoop
// (source=system).
func PostHitlCard(ctx context.Context, hub *realtime.Hub, q db.DBTX, wsID, sessionID uuid.UUID, c HitlCard, now time.Time) (uuid.UUID, error) {
	authorType := "system"
	var authorID *uuid.UUID
	if c.AuthorAgentID != nil && *c.AuthorAgentID != uuid.Nil {
		authorType, authorID = "agent", c.AuthorAgentID
	}
	var id uuid.UUID
	if err := q.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, source_task_id, created_at)
		VALUES ($1, $2::author_type, $3, $4, 'hitl', $5, $6) RETURNING id`,
		sessionID, authorType, authorID, c.CardBody(), c.SourceTaskID, now).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("messages: hitl card: %w", err)
	}
	// A publish failure is not the caller's failure — the card is committed
	// either way and the client re-reads via REST (realtime D1).
	_ = Publish(ctx, hub, q, wsID, sessionID, id)
	return id, nil
}
