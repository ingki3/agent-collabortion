package client

import (
	"context"
	"encoding/json"
	"net/url"
)

// Mission ledger operations — contracts/openapi.yaml v0.3.12 tag `memory`
// (PRD FR-4.6): listMemory · noteMemory · supersedeMemory · retireMemory.
// Bodies are the JSON the actions built, results kept whole (MemoryItem and
// friends are passed through to the agent unchanged).

// MemoryCall is one ledger operation's raw 2xx body. key is the optional
// Idempotency-Key (openapi IdempotencyKeyOptional on noteMemory ·
// supersedeMemory), sent only when given — the card commands' rule.
func (c *Client) MemoryCall(ctx context.Context, method, path string, q url.Values, body any, key string) (json.RawMessage, error) {
	res, err := c.Do(ctx, method, path, q, body, idemHeader(key))
	if err != nil {
		return nil, err
	}
	return json.RawMessage(res.Body), nil
}
