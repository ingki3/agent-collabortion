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

// TaskScope resolves (task_id, attempt) for the Idempotency-Key.
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

// IdempotencyKey formats colab-cli.md §2.2: <task_id>:<attempt>:<client_seq>.
func IdempotencyKey(taskID string, attempt, seq int) string {
	return fmt.Sprintf("%s:%d:%d", taskID, attempt, seq)
}

// PostMessage — POST /sessions/{S}/messages with the Idempotency-Key. If key
// is empty one is generated from (task, attempt, next client_seq). Returns the
// key actually used so a caller can retry with the same one.
func (c *Client) PostMessage(ctx context.Context, sessionID string, body MessageCreate, key string) (*MessagePostResult, string, bool, error) {
	if body.Content == "" {
		return nil, "", false, Usage("--body is required")
	}
	if key == "" {
		task, attempt, err := c.TaskScope(ctx)
		if err != nil {
			return nil, "", false, err
		}
		seq, err := c.NextSeq(task, attempt)
		if err != nil {
			return nil, "", false, err
		}
		key = IdempotencyKey(task, attempt, seq)
	}
	h := http.Header{}
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
