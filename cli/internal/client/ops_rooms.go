package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Room operations — contracts/colab-cli.md v0.8 §2.4a (openapi x-colab-cli
// `room list` · `room read` · `work propose`, PRD v0.19 FR-4.5 · FR-2A.1).
// The answers are passed through as the server sent them (map), so every
// field — `truncated` above all — reaches --json output unchanged.

// ListReadableRooms — GET /cli/rooms (listReadableRooms): only the rooms this
// turn may read, judged by the server at the moment of the call.
func (c *Client) ListReadableRooms(ctx context.Context, query string) (map[string]any, error) {
	v := url.Values{}
	if query != "" {
		v.Set("q", query)
	}
	var out map[string]any
	if _, err := c.GetJSON(ctx, "/cli/rooms", v, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ReadRoom — GET /cli/rooms/{id}/read (readRoom). tail 0 = server default.
func (c *Client) ReadRoom(ctx context.Context, roomID string, tail int, query string) (map[string]any, error) {
	v := url.Values{}
	if tail > 0 {
		v.Set("tail", strconv.Itoa(tail))
	}
	if query != "" {
		v.Set("query", query)
	}
	var out map[string]any
	if _, err := c.GetJSON(ctx, "/cli/rooms/"+url.PathEscape(roomID)+"/read", v, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// WorkProposalCreate — openapi WorkProposalCreate.
type WorkProposalCreate struct {
	Goal      string `json:"goal"`
	Rationale string `json:"rationale"`
}

// CreateWorkProposal — POST /rooms/{S}/work-proposals (createWorkProposal).
// The Idempotency-Key is optional, sent only when given (as the P2 writes).
func (c *Client) CreateWorkProposal(ctx context.Context, roomID string, body WorkProposalCreate, key string) (map[string]any, bool, error) {
	res, err := c.Do(ctx, http.MethodPost, "/rooms/"+url.PathEscape(roomID)+"/work-proposals", nil, body, idemHeader(key))
	if err != nil {
		return nil, false, err
	}
	var out map[string]any
	if err := decode(res, &out); err != nil {
		return nil, res.Replayed, err
	}
	return out, res.Replayed, nil
}
