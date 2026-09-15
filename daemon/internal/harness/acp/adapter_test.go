package acp

import (
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// N1 — the tool table is managed with the adapter pin: a pin bump must
// re-verify KnownClaudeTools (raw system/init.tools) and update this pin.
func TestKnownClaudeToolsPinnedWithAdapter(t *testing.T) {
	if AdapterPin != contracts.ClaudeAgentACPPin {
		t.Fatalf("AdapterPin %q != contracts pin %q", AdapterPin, contracts.ClaudeAgentACPPin)
	}
	if KnownClaudeToolsPin != AdapterPin {
		t.Fatalf("KnownClaudeTools captured from %s but adapter pinned at %s — re-verify the table", KnownClaudeToolsPin, AdapterPin)
	}
	seen := map[string]bool{}
	for _, tl := range KnownClaudeTools {
		if seen[tl] {
			t.Fatalf("duplicate tool %s", tl)
		}
		seen[tl] = true
	}
	for _, must := range []string{"AskUserQuestion", "Bash", "Edit", "Read", "Task", "Agent"} {
		if !seen[must] {
			t.Fatalf("table lacks %s", must)
		}
	}
}

// N5 — exact match (+ alias table); no substring matching either way.
func TestModelMatchesExact(t *testing.T) {
	yes := [][2]string{
		{"sonnet", "sonnet"},
		{"Claude-Sonnet-5", "claude-sonnet-5"},
		{"anthropic:claude-haiku-4-5-20251001", "claude-haiku-4-5-20251001"},
		{"claude-haiku-4-5-20251001", "anthropic:claude-haiku-4-5-20251001"},
		{"haiku", "claude-haiku-4-5-20251001"}, // spike 1b: alias → reported id
		{"claude-haiku-4-5-20251001", "haiku"},
		{"", ""},
	}
	no := [][2]string{
		{"sonnet", "claude-sonnet-5-fast"},
		{"sonnet", "sonnet-1m"},
		{"claude-sonnet-5", "claude-sonnet-5-20260101"},
		{"claude-sonnet-5", "sonnet-5"},
		{"haiku", "claude-fable-5-1"}, // spike 1b: default model after load = drift
		{"sonnet", ""},
		{"", "sonnet"},
		{"claude-haiku-4-5", "claude-haiku-4-5-20251001"}, // two concrete ids never fuzz
	}
	for _, c := range yes {
		if !ModelMatches(c[0], c[1]) {
			t.Errorf("ModelMatches(%q,%q) = false", c[0], c[1])
		}
	}
	for _, c := range no {
		if ModelMatches(c[0], c[1]) {
			t.Errorf("ModelMatches(%q,%q) = true", c[0], c[1])
		}
	}
}

// R4 / harness §8 v0.3 — only the Hermes error prefix on the first line,
// with no tool activity, is evidence; quoted or bare patterns are not.
func TestSniffHermesTextPrefixOnly(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	type c struct {
		text  string
		tools int
		kind  contracts.FailureKind // "" → not sniffed
	}
	cases := []c{
		{"API call failed after 1 retries: HTTP 429: This request would exceed your account's rate limit.", 0, contracts.FailRateLimited},
		{"\n  API call failed after 3 retries: rate limit exceeded\nsecond line", 0, contracts.FailRateLimited},
		{"API call failed after 2 retries: HTTP 401: authentication_error", 0, contracts.FailAuth},
		{"API call failed after 2 retries: HTTP 403 forbidden", 0, contracts.FailAuth},
		{"API call failed after 1 retries: HTTP 503 overloaded", 0, contracts.FailOther},
		{"API call failed after 1 retries: connection reset", 0, contracts.FailOther},
		// not evidence:
		{"빌드 실패 원인: API call failed after 1 retries: HTTP 429", 0, ""},
		{"Review: the daemon logged \"API call failed after 1 retries: HTTP 429\" and retried.", 0, ""},
		{"HTTP 429 Too Many Requests", 0, ""},
		{"rate limit hit, please retry", 0, ""},
		{"unauthorized: invalid api key", 0, ""},
		{"API call failed: HTTP 429", 0, ""},                 // no "after N retries:"
		{"API call failed after 1 retries: HTTP 429", 1, ""}, // tool activity → real turn
		{"PONG", 0, ""},
		{"", 0, ""},
	}
	for _, x := range cases {
		f, ok := SniffHermesText(x.text, x.tools, now)
		if x.kind == "" {
			if ok {
				t.Errorf("%q sniffed as %s", x.text, f.Kind)
			}
			continue
		}
		if !ok || f.Kind != x.kind {
			t.Errorf("%q → ok=%v kind=%s want %s", x.text, ok, f.Kind, x.kind)
			continue
		}
		if x.kind == contracts.FailRateLimited && (f.NotBefore == nil || !f.NotBefore.Equal(now.Add(contracts.RateLimitFallback))) {
			t.Errorf("%q not_before %v", x.text, f.NotBefore)
		}
		if strings.Contains(f.Detail, "\n") {
			t.Errorf("detail not first line: %q", f.Detail)
		}
	}
}

// R2 — the colab MCP entry carries exactly the attempt's COLAB_* env.
func TestColabMCPServerFromEnv(t *testing.T) {
	env := Env(contracts.RuntimeClaudeCode, TaskEnv{TaskToken: "ctk_1", ServerURL: "http://s", TaskID: "t", Attempt: 3, LaneID: "l", SessionID: "s", AgentName: "Lead"}, map[string]string{"MY_KEY": "v"})
	s := ColabMCPServer("", env)
	if s.Name != "colab" || s.Command != "colab" || strings.Join(s.Args, " ") != "mcp serve" {
		t.Fatalf("%+v", s)
	}
	got := map[string]string{}
	for _, e := range s.Env {
		got[e.Name] = e.Value
	}
	want := map[string]string{"COLAB_TASK_TOKEN": "ctk_1", "COLAB_SERVER_URL": "http://s", "COLAB_TASK_ID": "t", "COLAB_TASK_ATTEMPT": "3", "COLAB_LANE_ID": "l", "COLAB_SESSION_ID": "s", "COLAB_AGENT_NAME": "Lead"}
	if len(got) != len(want) {
		t.Fatalf("env %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s=%q want %q", k, got[k], v)
		}
	}
	if s2 := ColabMCPServer("/opt/colab/bin/colab", env); s2.Command != "/opt/colab/bin/colab" {
		t.Fatalf("%+v", s2)
	}
}
