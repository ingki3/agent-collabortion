package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Context calls GET /cli/context on first need and caches it for the life of
// the process — at most one round trip, never one per command (colab-cli.md
// v0.4 §1, backlog C-1). An unconditional preflight would double every
// command's request count for nothing: a revoked token surfaces as 401 on
// each command's own request anyway (FR-9.1, E11-04).
//
// A command that needs a context value — the room or task id when the env
// does not carry one, the participant roster (`lane delegate`), last_seq at
// an attempt boundary, or open_hitl_request_id /
// suppressed_delegator_agent_id — calls this and gets the cached answer.
func (c *Client) Context(ctx context.Context) (*CliContext, error) {
	if c.ctx != nil {
		return c.ctx, nil
	}
	var cc CliContext
	if _, err := c.GetJSON(ctx, "/cli/context", nil, &cc); err != nil {
		return nil, err
	}
	c.ctx = &cc
	return c.ctx, nil
}

// RoomID resolves the turn's room: explicit arg → COLAB_ROOM_ID (else its
// old name COLAB_SESSION_ID, same value) → /cli/context session_id (the
// context still names the room's id by its old column name).
func (c *Client) RoomID(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if c.cfg.RoomID != "" {
		return c.cfg.RoomID, nil
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return "", err
	}
	if cc.SessionID == "" {
		return "", &Error{Exit: ExitUnreachable, Code: "bad_response", Title: "cli context has no session_id (room id)"}
	}
	return cc.SessionID, nil
}

// WorkID is the mission this turn belongs to (COLAB_WORK_ID), "" outside a
// mission. /cli/context has no work id, so the env is the only source.
func (c *Client) WorkID() string { return c.cfg.WorkID }

// ThreadID is the thread this turn was asked in (COLAB_THREAD_ID), "" for a
// turn that started at the top level. It is a thread of the turn's OWN room,
// so it is returned only for that room.
func (c *Client) ThreadID(roomID string) string {
	if c.cfg.RoomID != "" && roomID != c.cfg.RoomID {
		return ""
	}
	return c.cfg.ThreadID
}

// TaskScope resolves (task_id, attempt): env first, else /cli/context. The
// attempt is not part of the key; NextSeq uses it to detect attempt
// boundaries in the persisted seq state.
func (c *Client) TaskScope(ctx context.Context) (string, int, error) {
	if c.cfg.TaskID != "" && c.cfg.Attempt > 0 {
		return c.cfg.TaskID, c.cfg.Attempt, nil
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return "", 0, err
	}
	task := c.cfg.TaskID
	if task == "" {
		task = cc.TaskID
	}
	attempt := c.cfg.Attempt
	if attempt == 0 {
		attempt = cc.Attempt
	}
	if task == "" || attempt == 0 {
		return "", 0, &Error{Exit: ExitUnreachable, Code: "bad_response", Title: "cli context has no task_id/attempt"}
	}
	return task, attempt, nil
}

// GetRoom — GET /rooms/{R} (getRoom). Returned as a generic map so every
// field the server sends reaches --json output unchanged.
func (c *Client) GetRoom(ctx context.Context, roomID string) (map[string]any, error) {
	var out map[string]any
	if _, err := c.GetJSON(ctx, "/rooms/"+url.PathEscape(roomID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetWork — GET /works/{W} (getWork): goal · acceptance_criteria ·
// completion_progress · director, as sent.
func (c *Client) GetWork(ctx context.Context, workID string) (map[string]any, error) {
	var out map[string]any
	if _, err := c.GetJSON(ctx, "/works/"+url.PathEscape(workID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListRoomParticipants — GET /rooms/{R}/participants (listRoomParticipants):
// people and agents in one list, each agent row with its room-scoped
// derived status. Returns items[] as sent (never nil).
func (c *Client) ListRoomParticipants(ctx context.Context, roomID string) ([]map[string]any, error) {
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if _, err := c.GetJSON(ctx, "/rooms/"+url.PathEscape(roomID)+"/participants", nil, &out); err != nil {
		return nil, err
	}
	if out.Items == nil {
		out.Items = []map[string]any{}
	}
	return out.Items, nil
}

// MessagesQuery — colab room messages flags.
type MessagesQuery struct {
	Since  string // → after=<cursor|id>
	Limit  int    // → limit (1..200)
	Thread string // → thread=<root id>
	Work   string // → work_id=<mission id> (colab room messages --work, v0.8)
	// TopOnly → include_replies=false (colab room messages --top-only,
	// v0.9.1). Replies are the default: agents answer in threads, so a
	// top-level-only read misses most of what they said to each other.
	TopOnly bool
}

// ListMessages — GET /rooms/{R}/messages (listMessages).
func (c *Client) ListMessages(ctx context.Context, roomID string, q MessagesQuery) (*MessagePage, error) {
	// 0 means "not given" here; an explicit --limit 0 is rejected one layer
	// up (colab.RoomMessages) where "given" is known.
	if q.Limit < 0 || q.Limit > 200 {
		return nil, Usage("--limit must be 1..200 (got %d)", q.Limit)
	}
	v := url.Values{}
	if q.Since != "" {
		v.Set("after", q.Since)
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Work != "" {
		v.Set("work_id", q.Work)
	}
	if q.Thread != "" {
		v.Set("thread", q.Thread)
	}
	// --thread already means root + replies; otherwise v0.9.1 defaults to
	// replies included and --top-only asks for the main timeline alone.
	v.Set("include_replies", strconv.FormatBool(q.Thread != "" || !q.TopOnly))
	var page MessagePage
	if _, err := c.GetJSON(ctx, "/rooms/"+url.PathEscape(roomID)+"/messages", v, &page); err != nil {
		return nil, err
	}
	return &page, nil
}

// IdempotencyKey derives the colab-cli.md §1 (v0.2) key:
// UUIDv5(IdempotencyNamespace, "task:<task_id>:<seq>"). The attempt is
// deliberately not part of the name — the same (task, seq) from a later
// attempt is a network re-send and must replay (E8-04).
func IdempotencyKey(taskID string, seq int) string {
	u, err := UUIDv5(IdempotencyNamespace, fmt.Sprintf("task:%s:%d", taskID, seq))
	if err != nil {
		panic(err) // IdempotencyNamespace is a constant; cannot fail
	}
	return u
}

// HeaderClientSeq carries the client seq the Idempotency-Key was derived from
// (colab-cli.md §1 v0.3, openapi.yaml `ClientSeq`). The server stores it as
// idempotency_key.client_seq and answers CliContext.last_seq = max(client_seq),
// so a hole in the seq (failed post, then retry) never causes a key reuse.
const HeaderClientSeq = "X-Colab-Client-Seq"

// PostMessage — POST /rooms/{R}/messages with the Idempotency-Key. If key
// is empty one is derived from (task, next seq) — see NextSeq — and that seq
// is sent alongside as X-Colab-Client-Seq (v0.3). An explicit key has no
// known seq, so the header is omitted and the server falls back to its
// UUIDv5 probe. Returns the key actually used so a caller can retry with the
// same one.
func (c *Client) PostMessage(ctx context.Context, roomID string, body MessageCreate, key string) (*MessagePostResult, string, bool, error) {
	if body.Content == "" {
		return nil, "", false, Usage("--body is required")
	}
	h := http.Header{}
	if key == "" {
		task, attempt, err := c.TaskScope(ctx)
		if err != nil {
			return nil, "", false, err
		}
		seq, err := c.NextSeq(ctx, task, attempt)
		if err != nil {
			return nil, "", false, err
		}
		key = IdempotencyKey(task, seq)
		h.Set(HeaderClientSeq, strconv.Itoa(seq))
	}
	h.Set("Idempotency-Key", key)
	res, err := c.Do(ctx, http.MethodPost, "/rooms/"+url.PathEscape(roomID)+"/messages", nil, body, h)
	if err != nil {
		return nil, key, false, err
	}
	var out MessagePostResult
	if err := decode(res, &out); err != nil {
		return nil, key, res.Replayed, err
	}
	return &out, key, res.Replayed, nil
}

// CachedContext returns the /cli/context result if it was already fetched.
func (c *Client) CachedContext() *CliContext { return c.ctx }
