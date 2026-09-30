package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Card operations — contracts/openapi.yaml v0.3.10 tag `cards` (PRD FR-3.8):
// delegateLane's card body, submitCardResult · acceptCard · reviseCard ·
// getCard · listRoomCards. Bodies are passed through as JSON the actions
// built (the card file's own shape), results kept whole in Raw.

// CardDelegateCreate — POST /rooms/{R}/lanes body since v0.3.10.
type CardDelegateCreate struct {
	Card      map[string]any `json:"card"`
	DependsOn []string       `json:"depends_on"`
	Profile   *string        `json:"profile,omitempty"`
}

// CardDelegateResult — delegateLane 201 {lane, message, task, card}.
type CardDelegateResult struct {
	Lane    json.RawMessage `json:"lane"`
	Message *Message        `json:"message,omitempty"`
	Task    json.RawMessage `json:"task,omitempty"`
	Card    json.RawMessage `json:"card,omitempty"`
}

// DelegateCard — POST /rooms/{R}/lanes with a card.
func (c *Client) DelegateCard(ctx context.Context, roomID string, body CardDelegateCreate, key string) (*CardDelegateResult, error) {
	if body.DependsOn == nil {
		body.DependsOn = []string{}
	}
	res, err := c.Do(ctx, http.MethodPost, "/rooms/"+url.PathEscape(roomID)+"/lanes", nil, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	var out CardDelegateResult
	if err := decode(res, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CardCall is one card operation's raw 2xx body.
func (c *Client) CardCall(ctx context.Context, method, path string, q url.Values, body any, key string) (json.RawMessage, error) {
	res, err := c.Do(ctx, method, path, q, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(res.Body), nil
}

// CardSummary is the part of a CardBoard row / TaskCard the CLI reads to
// resolve `C-n` to an id.
type CardSummary struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Number int    `json:"number"`
}

// ListRoomCards — GET /rooms/{R}/cards?work_id=<id|none>.
func (c *Client) ListRoomCards(ctx context.Context, roomID, workID string) (json.RawMessage, []CardSummary, error) {
	q := url.Values{}
	if workID == "" {
		workID = "none"
	}
	q.Set("work_id", workID)
	raw, err := c.CardCall(ctx, http.MethodGet, "/rooms/"+url.PathEscape(roomID)+"/cards", q, nil, "")
	if err != nil {
		return nil, nil, err
	}
	var board struct {
		Items []CardSummary `json:"items"`
	}
	if err := json.Unmarshal(raw, &board); err != nil {
		return nil, nil, &Error{Exit: ExitUnreachable, Code: "bad_response", Title: "unparseable server response", Detail: err.Error()}
	}
	return raw, board.Items, nil
}
