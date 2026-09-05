package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// Context calls GET /cli/context once and caches it (colab-cli.md §1
// "전처리"). A revoked token surfaces here as 401 token_revoked → exit 4.
//
// NOTE (review N3): the contract phrases this as "called once at CLI start",
// but P1 calls it lazily — only when a path parameter is missing from the
// env or when the seq state needs last_seq (attempt boundary). That is
// equivalent for P1 because every command's real request surfaces a revoked
// token as 401 anyway. P2/P3 commands that depend on
// open_hitl_request_id / suppressed_delegator_agent_id must call Context
// explicitly before acting rather than assume it was fetched.
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

// SessionID resolves the session: explicit arg → COLAB_SESSION_ID → /cli/context.
func (c *Client) SessionID(ctx context.Context, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if c.cfg.SessionID != "" {
		return c.cfg.SessionID, nil
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return "", err
	}
	if cc.SessionID == "" {
		return "", &Error{Exit: ExitUnreachable, Code: "bad_response", Title: "cli context has no session_id"}
	}
	return cc.SessionID, nil
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

// GetSession — GET /sessions/{S}. Returned as a generic map so every field
// the server sends reaches --json output unchanged.
func (c *Client) GetSession(ctx context.Context, sessionID string) (map[string]any, error) {
	var out map[string]any
	if _, err := c.GetJSON(ctx, "/sessions/"+url.PathEscape(sessionID), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MessagesQuery — colab session messages flags.
type MessagesQuery struct {
	Since  string // → after=<cursor|id>
	Limit  int    // → limit (1..200)
	Thread string // → thread=<root id>
}

// ListMessages — GET /sessions/{S}/messages.
func (c *Client) ListMessages(ctx context.Context, sessionID string, q MessagesQuery) (*MessagePage, error) {
	// 0 means "not given" here; an explicit --limit 0 is rejected one layer
	// up (colab.SessionMessages) where "given" is known.
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
	if q.Thread != "" {
		v.Set("thread", q.Thread)
		v.Set("include_replies", "true")
	}
	var page MessagePage
	if _, err := c.GetJSON(ctx, "/sessions/"+url.PathEscape(sessionID)+"/messages", v, &page); err != nil {
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

// PostMessage — POST /sessions/{S}/messages with the Idempotency-Key. If key
// is empty one is derived from (task, next seq) — see NextSeq — and that seq
// is sent alongside as X-Colab-Client-Seq (v0.3). An explicit key has no
// known seq, so the header is omitted and the server falls back to its
// UUIDv5 probe. Returns the key actually used so a caller can retry with the
// same one.
func (c *Client) PostMessage(ctx context.Context, sessionID string, body MessageCreate, key string) (*MessagePostResult, string, bool, error) {
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
	res, err := c.Do(ctx, http.MethodPost, "/sessions/"+url.PathEscape(sessionID)+"/messages", nil, body, h)
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
