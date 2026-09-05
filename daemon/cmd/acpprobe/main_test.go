package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/ingki3/agent-collabortion/daemon/internal/acpprobe"
)

// sdkUsageLimitPrefixes is USAGE_LIMIT_ERROR_PREFIXES from
// @anthropic-ai/claude-agent-sdk 0.3.257 sdk.d.ts:8559 — the list
// claude-agent-acp 0.74.0 uses (session-failure-extension.js
// isSyntheticUsageLimitMessage) to recognise a usage-limit turn. The adapter
// forwards the text as `-32603 Internal error: <text>` when the client does
// not advertise the AIR sessionFailure capability (acp-agent.js
// internalErrorForClient), so the harness classifies on the text.
var sdkUsageLimitPrefixes = []string{
	"You've hit your",
	"You've reached your",
	"You're out of usage credits",
	"Your org is out of usage · add funds to continue",
	"Your org is out of usage · contact your admin",
	"Your seat type doesn't include usage credits",
	"Your seat type doesn't include usage",
	"Your usage allocation has been disabled by your admin",
	"Your group's usage limit is set to $0",
	"Fable 5 requires usage credits",
	"You're out of extra usage",
	"Your seat type doesn't include extra usage",
}

func TestRateLimitRegexCoversSDKPrefixes(t *testing.T) {
	for _, p := range sdkUsageLimitPrefixes {
		msg := "session/prompt: rpc error -32603: Internal error: " + p
		if !rateLimitRe.MatchString(msg) {
			t.Errorf("prefix not classified as limit: %q", p)
		}
	}
	// The wording observed in SPIKE_01 (2026-09-05, claude.ai Max).
	observed := "session/prompt: rpc error -32603: Internal error: You've hit your limit · resets 11am (Asia/Seoul)"
	if !rateLimitRe.MatchString(observed) {
		t.Fatalf("observed limit text not matched")
	}
	m := resetTimeRe.FindStringSubmatch(observed)
	if len(m) < 2 || m[1] != "11am (Asia/Seoul)" {
		t.Fatalf("reset time not parsed: %v", m)
	}
	for _, ok := range []string{"session/prompt: rpc error -32603: Internal error: Session not found", "done", "tool denied"} {
		if rateLimitRe.MatchString(ok) {
			t.Errorf("false positive: %q", ok)
		}
	}
}

func TestIsLimitErrorUsesErrorKindData(t *testing.T) {
	// claude-agent-acp 0.74.0 attaches {errorKind} (errorKindData) from the
	// SDK assistant error; message text may be generic.
	rpc := &acpprobe.RPCError{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"errorKind":"rate_limit"}`)}
	err := fmt.Errorf("session/prompt: rpc error %d: %w", rpc.Code, rpc)
	if !isLimitError(err) {
		t.Fatalf("errorKind=rate_limit not classified")
	}
	other := &acpprobe.RPCError{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"errorKind":"server_error"}`)}
	if isLimitError(fmt.Errorf("x: %w", other)) {
		t.Fatalf("server_error misclassified as limit")
	}
	var got *acpprobe.RPCError
	if !errors.As(err, &got) || got.ErrorKind() != "rate_limit" {
		t.Fatalf("errors.As lost the RPCError")
	}
}
