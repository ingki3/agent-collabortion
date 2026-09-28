package acp

import "github.com/ingki3/agent-collabortion/contracts"

// Hermes reports its ACP `usage` in a different shape from Claude Code, and
// reading it as if it were the same one inflated every hermes attempt's bill
// (T-HERMESCACHE, plan/research/context-memory/05-hermes-cache.md).
//
// Measured on the wire (hermes 0.20.6, `hermes acp`, two prompts in one
// session, 2026-09-28):
//
//	turn 1  inputTokens 26248  cachedReadTokens     0  outputTokens 5  totalTokens 26253
//	turn 2  inputTokens 52535  cachedReadTokens 26244  outputTokens 9  totalTokens 52544
//
// Two facts follow from those four rows.
//
//  1. **`inputTokens` INCLUDES the cached tokens.** `totalTokens` equals
//     `inputTokens + outputTokens` exactly in both rows, so the cache read is
//     not a sibling of the input — it is a part of it. The adapter fills the
//     field from Hermes' `prompt_tokens`, which is defined as
//     `input + cache_read + cache_write` (agent/usage_pricing.py
//     CanonicalUsage.prompt_tokens; acp_adapter/server.py builds Usage with
//     `input_tokens=result["prompt_tokens"]`).
//
//     Our contract's `input` is the NON-cached input: the server prices
//     `input × input-rate + cache_read × read-rate` (server/internal/cost), so
//     passing the inclusive number charges every cached token twice — once at
//     the full input rate and once at the cache-read rate. On the game room's
//     40 hermes attempts that is $1,544 recorded against ~$352 correctly
//     priced (4.4×), and it is why hermes "had no cache" in the 0단계
//     baseline: its cache reads were hiding inside `input`.
//
//  2. **`inputTokens` is CUMULATIVE over the adapter session**, not per turn:
//     turn 2's 52,535 is turn 1's 26,248 plus turn 2's own 26,287. The adapter
//     process lives exactly one attempt, so the value it reports IS the
//     attempt's total (harness §7 `cumulative: true`) — but it must be TAKEN,
//     not ADDED to what an earlier prompt in the same attempt reported. The
//     refusal-retry path (D-13) prompts twice in one attempt, and adding a
//     cumulative number there counted the first turn twice.
//
// `cachedWriteTokens` never arrives: the adapter only passes
// `cached_read_tokens` even though Hermes tracks `cache_write_tokens`
// internally (agent/turn_finalizer.py) — an upstream gap, recorded in the
// report, not something the daemon can invent.
//
// Claude Code is deliberately untouched: its numbers were not measured for
// either property, and the 0단계 cost fit reproduces its bill exactly as read
// today.

// hermesUsage normalizes one hermes `session/prompt` usage into the contract's
// shape: `input` without the cached tokens, `cache_read`/`cache_write` beside
// it. Clamped at zero — a report whose cache exceeds its prompt total would
// otherwise produce a negative input that the server's CHECK rejects.
func hermesUsage(u *PromptUsage) contracts.Usage {
	if u == nil {
		return contracts.Usage{}
	}
	input := u.InputTokens - u.CachedReadTokens - u.CachedWriteTokens
	if input < 0 {
		input = 0
	}
	return contracts.Usage{
		InputTokens:      input,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CachedReadTokens,
		CacheWriteTokens: u.CachedWriteTokens,
	}
}
