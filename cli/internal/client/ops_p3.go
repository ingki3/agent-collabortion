package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// P3 operations — contracts/openapi.yaml x-colab-cli, x-phase P3.

// CreateHitlRequest — POST /sessions/{S}/hitl-requests (createHitlRequest).
// The path is session-scoped and carries no task: the TaskToken names the
// task, so the request the server registers is always this attempt's own
// (colab-cli.md v0.5.1 §2.4 — the v0.5 table's `POST /v1/tasks/{T}/hitl` was
// a path openapi never had, and the real server answered it 404; C-4, found
// by T-I3).
//
// The task's second open request is the server's 409 hitl_already_open, which
// reaches the agent as exit 3 with the server's own wording (E7-04): the CLI
// does not pre-check /cli/context.open_hitl_request_id, because a cached
// context from earlier in the process would answer for a state that has since
// changed.
func (c *Client) CreateHitlRequest(ctx context.Context, sessionID string, body HitlCreate, key string) (*HitlCreateResult, error) {
	res, err := c.Do(ctx, http.MethodPost, "/sessions/"+url.PathEscape(sessionID)+"/hitl-requests", nil, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	var out HitlCreateResult
	if err := decode(res, &out); err != nil {
		return nil, err
	}
	out.Raw = json.RawMessage(res.Body)
	return &out, nil
}
