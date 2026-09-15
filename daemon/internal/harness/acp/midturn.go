package acp

import "encoding/json"

// Mid-turn usage from the claude_code raw SDK stream (harness §7 v0.8.5, D-17).
//
// WHY THIS FILE EXISTS
//
// FR-7.3 wants the budget enforced DURING a turn. The only in-turn signal the
// daemon can give the server is the heartbeat's `usage` (daemon-protocol
// §4.2), and until v0.8.5 that number was always ZERO: `recordUsage` ran once,
// from the `session/prompt` response, i.e. after the turn was over. The
// server's in-turn check is gated on `usage.input_tokens > 0 || … > 0`, so it
// never fired once — the whole "턴 중 강제" half of FR-7.3 was dead code on
// both sides (T-I3 measured it; S-44 added the finish-time floor under it).
//
// WHAT THE ADAPTER ACTUALLY GIVES (measured, claude-agent-acp 0.74.0 + Claude
// Code 2.1.258, one 12.2s turn with three tool calls):
//
//	session/update usage_update   {used, size} + _meta._claude/rateLimit …… no tokens
//	session/prompt response       full turn usage, tokens only …………………… turn END
//	_claude/sdkMessage            per-request usage, 17 notifications ……… DURING the turn
//	                              (first at 3.17s of 12.2s), and a final
//	                              `result` carrying total_cost_usd
//
// So the tokens exist mid-turn, but only on the adapter's raw SDK passthrough,
// which is off unless the client asks for it with
// `_meta.claudeCode.emitRawSDKMessages: true` (harness §3 v0.8.5). Hermes
// 0.20.6 has no equivalent at all — measured 0 in-turn usage notifications on
// every path — so it keeps the zero mid-turn number and the pre-finish
// heartbeat is the whole of its in-turn signal.
//
// THE DEDUP RULE (harness §7 v0.8.5)
//
// Summing everything that carries a `usage` block gives a WRONG total: the
// adapter emits each `assistant` message TWICE, and the copy's output_tokens
// is the value at the moment the message started, not the final one. Measured
// on the turn above, the naive sum came out at 2× the real input tokens and
// 1/54 of the real output tokens. The contract's rule is therefore split by
// field and by source, and neither source is ever double-counted:
//
//	input / cache_read / cache_write ← stream_event `message_start`
//	output                          ← stream_event `message_delta`
//
// Verified against the same turn: 10+8+8+8 = 34 input, 13615+21799+22603+22919
// = 80936 cache read, 8184+804+316+158 = 9462 cache write, 298+172+102+81 =
// 653 output — every one of them equal to the `session/prompt` response total
// (34 / 80936 / 9462 / 653). p3_midturn_test.go pins those four numbers.
//
// (`message_delta.usage` on this adapter also repeats the input and cache
// numbers, so summing message_delta ALONE is exact too. The contract names
// message_start for them, so that is what this code reads; the alternative is
// noted for whoever bumps the pin.)
//
// The mid-turn number is an APPROXIMATION and is treated as one: it is only
// ever added to what heartbeats report, and the moment the turn's own
// authoritative total arrives in the `session/prompt` response, `recordUsage`
// throws the approximation away and folds the real total in instead. A drift
// between the two can therefore never accumulate across turns.

// sdkTokens is one raw SDK usage block. Only the four fields the harness
// normalises are read; the SDK's own extras (thinking tokens, service tier,
// per-iteration breakdown) are not part of contracts.Usage.
type sdkTokens struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	CacheReadTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
}

// sdkStreamEvent is `message.event` of a `stream_event` sdkMessage — one
// Anthropic streaming event as the SDK saw it.
type sdkStreamEvent struct {
	Type string `json:"type"` // message_start | message_delta | content_block_* | …
	// message_start carries the whole request's opening usage under `message`.
	Message *struct {
		Usage *sdkTokens `json:"usage"`
	} `json:"message,omitempty"`
	// message_delta carries the request's final usage directly.
	Usage *sdkTokens `json:"usage,omitempty"`
}

// turnTokens is the running mid-turn approximation for the CURRENT turn.
// It is not a contracts.Usage because it deliberately has no cost and no
// model: mid-turn the daemon knows how many tokens were burned and nothing
// about what they cost (the price arrives with `result` at turn end).
type turnTokens struct {
	in, out, cacheRead, cacheWrite int64
}

func (t turnTokens) any() bool {
	return t.in > 0 || t.out > 0 || t.cacheRead > 0 || t.cacheWrite > 0
}

// foldSDKStream applies the §7 v0.8.5 dedup rule to one `stream_event`
// message and reports what it contributed. Anything else contributes nothing.
func foldSDKStream(raw json.RawMessage) turnTokens {
	var ev sdkStreamEvent
	if len(raw) == 0 || json.Unmarshal(raw, &ev) != nil {
		return turnTokens{}
	}
	switch ev.Type {
	case "message_start":
		if ev.Message == nil || ev.Message.Usage == nil {
			return turnTokens{}
		}
		u := ev.Message.Usage
		// Output is NOT taken here: at message_start it is the handful of
		// tokens produced so far, and message_delta reports the final count
		// for the same request.
		return turnTokens{in: u.InputTokens, cacheRead: u.CacheReadTokens, cacheWrite: u.CacheCreationTokens}
	case "message_delta":
		if ev.Usage == nil {
			return turnTokens{}
		}
		return turnTokens{out: ev.Usage.OutputTokens}
	}
	return turnTokens{}
}
