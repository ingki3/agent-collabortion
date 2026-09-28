package acp_test

// T-HERMESCACHE — hermes' ACP `usage` is a different shape from Claude Code's,
// and reading it as the same one billed every cached token at the full input
// rate (plan/research/context-memory/05-hermes-cache.md).
//
// Wire measurement (hermes 0.20.6, two prompts in one adapter session,
// 2026-09-28) — the numbers the fake below replays:
//
//	turn 1  inputTokens 26248  cachedReadTokens     0  outputTokens 5  totalTokens 26253
//	turn 2  inputTokens 52535  cachedReadTokens 26244  outputTokens 9  totalTokens 52544
//
//	(a) totalTokens == inputTokens + outputTokens in both rows → the cache read
//	    is INSIDE inputTokens, not beside it.
//	(b) turn 2's inputTokens is turn 1's plus turn 2's own → CUMULATIVE over
//	    the adapter session.

import (
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// The measured second-turn reading: 52,535 prompt tokens of which 26,244 were
// cache reads.
var hermesWireUsage = acp.PromptUsage{
	InputTokens: 52535, CachedReadTokens: 26244, OutputTokens: 9, TotalTokens: 52544,
}

func hermesScript(u acp.PromptUsage) acpfake.Script {
	return acpfake.Script{Kind: "hermes", Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "DONE"}}, Usage: &u,
	}}}
}

// A hermes turn reports `input` WITHOUT the cached tokens, and the cache read
// beside it. Before this, `input` carried the inclusive 52,535 and the server
// priced 26,244 tokens twice — once at the input rate and once at the
// cache-read rate ($5/M + $0.5/M instead of $0.5/M).
func TestHermesUsageSeparatesCacheFromInput(t *testing.T) {
	f := newFixture(t, hermesScript(hermesWireUsage), bundle(contracts.RuntimeHermes), nil)
	if res := f.run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	got := f.runner.Usage()
	// 52,535 − 26,244 = 26,291 genuinely new input tokens.
	wantUsage(t, got, 26291, 9, 26244, 0, "hermes finish")
	if got.InputTokens+got.CacheReadTokens+got.CacheWriteTokens != hermesWireUsage.InputTokens {
		t.Errorf("input(%d) + cache_read(%d) + cache_write(%d) must rebuild the reported prompt total %d",
			got.InputTokens, got.CacheReadTokens, got.CacheWriteTokens, hermesWireUsage.InputTokens)
	}
}

// Claude Code's numbers are NOT touched: its `inputTokens` excludes the cache
// and its per-turn values are added. The separation above must not leak into
// that path.
func TestClaudeUsageKeepsAdditiveShape(t *testing.T) {
	u := acp.PromptUsage{InputTokens: 1000, CachedReadTokens: 9000, CachedWriteTokens: 500, OutputTokens: 77}
	f := newFixture(t, acpfake.Script{Kind: "claude", Turns: []acpfake.Turn{{
		Steps: []acpfake.Step{{Chunk: "DONE"}}, Usage: &u,
	}}}, bundle(contracts.RuntimeClaudeCode), nil)
	if res := f.run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	wantUsage(t, f.runner.Usage(), 1000, 77, 9000, 500, "claude finish")
}

// Two prompts in ONE attempt (the D-13 refusal retry): hermes reports the
// adapter session's running total each time, so the attempt's usage is the
// LAST reading, not the sum. Adding them counted the refused turn twice — and
// that turn is the expensive one, since a resume prompt carries the whole
// history.
func TestHermesRefusalRetryDoesNotDoubleCountTheSession(t *testing.T) {
	refusal := acp.PromptUsage{InputTokens: 26248, CachedReadTokens: 0, OutputTokens: 5, TotalTokens: 26253}
	// The resume SUCCEEDS (provenance matches) and the first prompt on the
	// resumed session comes back a bare `refusal` with no tool activity —
	// D-13's signal that hermes lost the conversation — so the runner cold
	// starts and prompts a second time inside the same attempt.
	s := acpfake.Script{
		Kind: "hermes", KnownSessions: []string{"old"},
		Turns: []acpfake.Turn{
			{StopReason: "refusal", Usage: &refusal},
			{Steps: []acpfake.Step{{Chunk: "DONE"}}, Usage: &hermesWireUsage},
		},
	}
	f := newFixture(t, s, bundle(contracts.RuntimeHermes), func(a *acp.Attempt) {
		a.Bundle.Resume = &contracts.RuntimeSessionRef{
			RuntimeKind: contracts.RuntimeHermes, SessionID: "old", CWD: a.Workdir,
		}
	})
	res := f.run()
	if res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	if res.ResumeOutcome != "cold_start" {
		t.Fatalf("resume outcome = %q, want cold_start — the two-prompt path is what this test measures (D-13)", res.ResumeOutcome)
	}
	// The retry's reading (52,535 / 26,244) stands on its own. Summed with the
	// refused turn, the attempt would read 78,783 prompt tokens for an adapter
	// session that burned 52,535.
	wantUsage(t, f.runner.Usage(), 26291, 9, 26244, 0, "hermes refusal-retry attempt")
}

// A malformed report whose cache exceeds the prompt total must not produce a
// negative input: task_usage's CHECK (input_tokens >= 0) would reject the row
// and the attempt would lose its whole usage.
func TestHermesUsageClampsAtZero(t *testing.T) {
	u := acp.PromptUsage{InputTokens: 100, CachedReadTokens: 900, CachedWriteTokens: 50, OutputTokens: 3}
	f := newFixture(t, hermesScript(u), bundle(contracts.RuntimeHermes), nil)
	if res := f.run(); res.Outcome != "completed" {
		t.Fatalf("result %+v", res)
	}
	wantUsage(t, f.runner.Usage(), 0, 3, 900, 50, "hermes clamped")
}
