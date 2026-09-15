// Package realtime is the one SSE fan-out (openapi.md D1, G2 Q5): every
// persisted change is appended to stream_event (10-minute backfill window)
// and pushed to the subscribers of its workspace/session.
//
// Daemon → server → web; the daemon never talks to the browser
// (daemon-protocol §8).
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/db"
)

// Retention is the backfill window; an older Last-Event-ID gets `resync`.
const Retention = 10 * time.Minute

// Event is one SSE frame (StreamEvent in openapi.yaml).
type Event struct {
	ID          int64           `json:"-"`
	Type        string          `json:"type"`
	At          time.Time       `json:"at"`
	WorkspaceID uuid.UUID       `json:"workspace_id"`
	SessionID   *uuid.UUID      `json:"session_id"`
	Ephemeral   bool            `json:"ephemeral,omitempty"`
	Payload     json.RawMessage `json:"payload"`
}

// MarshalJSON adds the string cursor id the contract requires.
func (e Event) MarshalJSON() ([]byte, error) {
	type alias Event
	return json.Marshal(struct {
		ID string `json:"id"`
		alias
	}{fmt.Sprint(e.ID), alias(e)})
}

// Hub persists and fans out events. Safe for concurrent use.
type Hub struct {
	DB    db.DBTX
	Clock clock.Clock

	mu   sync.Mutex
	subs map[*Subscription]struct{}
}

func New(q db.DBTX, c clock.Clock) *Hub {
	return &Hub{DB: q, Clock: c, subs: map[*Subscription]struct{}{}}
}

// Subscription receives events of one workspace, optionally narrowed to sessions.
type Subscription struct {
	hub      *Hub
	ws       uuid.UUID
	sessions map[uuid.UUID]bool
	C        chan Event
	once     sync.Once
}

func (h *Hub) Subscribe(ws uuid.UUID, sessions []uuid.UUID) *Subscription {
	s := &Subscription{hub: h, ws: ws, C: make(chan Event, 256)}
	if len(sessions) > 0 {
		s.sessions = map[uuid.UUID]bool{}
		for _, id := range sessions {
			s.sessions[id] = true
		}
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (s *Subscription) Close() {
	s.once.Do(func() {
		s.hub.mu.Lock()
		delete(s.hub.subs, s)
		s.hub.mu.Unlock()
	})
}

func (s *Subscription) wants(e Event) bool {
	if e.WorkspaceID != s.ws {
		return false
	}
	if s.sessions == nil || e.SessionID == nil {
		return true
	}
	return s.sessions[*e.SessionID]
}

// Publish persists the event (unless ephemeral) and delivers it. q may be a
// transaction: the row is written with the caller's tx while the in-memory
// delivery happens immediately (a rolled-back tx can leave one stale push —
// clients re-read via REST, which is the D1 contract anyway).
func (h *Hub) Publish(ctx context.Context, q db.DBTX, ws uuid.UUID, session *uuid.UUID, typ string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("realtime: marshal %s: %w", typ, err)
	}
	e := Event{Type: typ, At: h.Clock.Now(), WorkspaceID: ws, SessionID: session, Payload: raw}
	if q == nil {
		q = h.DB
	}
	if err := q.QueryRow(ctx, `
		INSERT INTO stream_event (workspace_id, session_id, type, payload, created_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, ws, session, typ, raw, e.At).Scan(&e.ID); err != nil {
		return fmt.Errorf("realtime: persist %s: %w", typ, err)
	}
	h.deliver(e)
	return nil
}

// PublishEphemeral delivers without persisting (message.delta, agent.typing).
func (h *Hub) PublishEphemeral(ws uuid.UUID, session *uuid.UUID, typ string, payload any) {
	raw, _ := json.Marshal(payload)
	h.deliver(Event{Type: typ, At: h.Clock.Now(), WorkspaceID: ws, SessionID: session, Ephemeral: true, Payload: raw})
}

func (h *Hub) deliver(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if !s.wants(e) {
			continue
		}
		select {
		case s.C <- e:
		default: // slow consumer: drop; its next reconnect backfills or resyncs
		}
	}
}

// Backfill returns events of ws after cursor lastID. resync is true when the
// cursor is older than the retention window (or unknown), in which case the
// client must re-read state via REST.
func (h *Hub) Backfill(ctx context.Context, ws uuid.UUID, lastID int64, sessions []uuid.UUID) (events []Event, resync bool, err error) {
	var createdAt time.Time
	err = h.DB.QueryRow(ctx, `SELECT created_at FROM stream_event WHERE id = $1`, lastID).Scan(&createdAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unknown cursor: fine only if nothing newer exists (client is current).
		var newer int
		if err := h.DB.QueryRow(ctx, `SELECT count(*) FROM stream_event WHERE id > $1`, lastID).Scan(&newer); err != nil {
			return nil, false, err
		}
		return nil, newer > 0, nil
	}
	if err != nil {
		return nil, false, err
	}
	if h.Clock.Since(createdAt) > Retention {
		return nil, true, nil
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, type, created_at, workspace_id, session_id, payload
		FROM stream_event WHERE workspace_id = $1 AND id > $2 ORDER BY id`, ws, lastID)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	filter := map[uuid.UUID]bool{}
	for _, id := range sessions {
		filter[id] = true
	}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.Type, &e.At, &e.WorkspaceID, &e.SessionID, &e.Payload); err != nil {
			return nil, false, err
		}
		if len(filter) > 0 && e.SessionID != nil && !filter[*e.SessionID] {
			continue
		}
		events = append(events, e)
	}
	return events, false, rows.Err()
}

// Purge drops rows older than the retention window (scheduler).
func (h *Hub) Purge(ctx context.Context) error {
	_, err := h.DB.Exec(ctx, `DELETE FROM stream_event WHERE created_at < $1`, h.Clock.Now().Add(-Retention))
	return err
}
