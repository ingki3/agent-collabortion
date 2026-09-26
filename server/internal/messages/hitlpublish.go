package messages

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// T-APPROVAL (Director 2026-09-25): a HITL request the web cannot pair with its
// timeline card is drawn as a plain system line with a 「답글」 link — no
// buttons. S7 pairs them through its `hitls` list, which it fills from
// listHitlRequests on load and from `hitl.created`/`hitl.updated` after that.
// Only the agent path (createHitl) and the mission time limit published
// `hitl.created`; the completion approval, the budget pause, the loop stop and
// the isolation question inserted the request and its card and said nothing
// about the request, so the card that arrived by `message.created` had no
// request to be drawn with until a reload.
//
// The frame's payload is the contract's HitlRequest, which only httpapi knows
// how to build (hitlAPI: can_respond, purpose, the Director chain). The
// packages that raise system requests (sessions, router, roomgate, httpapi)
// cannot import httpapi, so the server registers the builder here against
// ITS hub — keyed by hub, not a package global, because tests run several
// servers in one process and a frame must reach the hub of the server whose
// transaction wrote the row.

// HitlPublishFunc publishes `hitl.created` / `hitl.updated` for one request,
// reading it (and its room's workspace) through q — the caller's transaction,
// where the row may not be committed yet.
type HitlPublishFunc func(ctx context.Context, q db.DBTX, hitlID uuid.UUID, event string)

var hitlPublishers sync.Map // *realtime.Hub → HitlPublishFunc

// RegisterHitlPublisher wires the builder for one hub.
//
// production caller: httpapi.NewServer.
func RegisterHitlPublisher(hub *realtime.Hub, fn HitlPublishFunc) {
	if hub == nil || fn == nil {
		return
	}
	hitlPublishers.Store(hub, fn)
}

// PublishHitl sends `event` (`hitl.created` or `hitl.updated`) for a request.
// A hub with no registered builder is said out loud once per event type: that
// is exactly the shape of the bug this exists for.
func PublishHitl(ctx context.Context, hub *realtime.Hub, q db.DBTX, hitlID uuid.UUID, event string) {
	if hub == nil {
		return
	}
	fn, ok := hitlPublishers.Load(hub)
	if !ok {
		slog.Warn("messages: no hitl publisher registered for this hub — "+event+" not published", "hitl", hitlID)
		return
	}
	fn.(HitlPublishFunc)(ctx, q, hitlID, event)
}

// AttachHitlCard is the one tail every SYSTEM-issued request shares: post the
// timeline card, link it back (hitl_request.message_id — without it S7 cannot
// pair the card with its request) and publish `hitl.created` with the link in
// it. Publishing before the link is written would hand the web a request whose
// message_id is null, which is the same plain-line card again.
//
// production callers: sessions.ApplyWorkEvent (completion approval · budget),
// httpapi.attachHitlCard (budget pause · mission time limit),
// router.pauseForLoop, roomgate.AskIsolation · AskRepo.
func AttachHitlCard(ctx context.Context, hub *realtime.Hub, q db.DBTX, wsID, roomID, hitlID uuid.UUID, c HitlCard, now time.Time) (uuid.UUID, error) {
	msgID, err := PostHitlCard(ctx, hub, q, wsID, roomID, c, now)
	if err != nil {
		return uuid.Nil, err
	}
	if _, err := q.Exec(ctx, `UPDATE hitl_request SET message_id = $2 WHERE id = $1`, hitlID, msgID); err != nil {
		return uuid.Nil, fmt.Errorf("messages: hitl card link: %w", err)
	}
	PublishHitl(ctx, hub, q, hitlID, "hitl.created")
	return msgID, nil
}
