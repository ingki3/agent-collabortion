package probe

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

func TestMain(m *testing.M) {
	acpfake.MaybeMain()
	os.Exit(m.Run())
}

// PONG turn against the fake: version/models/usage/resume folded into the
// capability; isolation raw init requested for claude_code.
func TestPongFoldsCapability(t *testing.T) {
	// KnownSessions makes the second process load the PONG session back —
	// that load IS the resume measurement (backlog D-2).
	script := acpfake.Script{AgentVersion: "0.74.0", Models: []string{"claude-sonnet-5", "claude-haiku-4-5"}, KnownSessions: []string{"sess-1"}, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}, ModelUsage: true, Usage: &contractsUsage}}}
	o := Options{DaemonVersion: "t", Turn: true, Timeout: 20 * time.Second, Command: func(k contracts.RuntimeKind) (string, []string, []string, bool) {
		c, a, e := acpfake.Command(script, "")
		return c, a, e, true
	}}
	cap := contracts.Capability{Kind: contracts.RuntimeClaudeCode}
	res := Pong(context.Background(), contracts.RuntimeClaudeCode, o, &cap)
	if res.Result.Outcome != "completed" || strings.TrimSpace(res.Result.Text) != "PONG" {
		t.Fatalf("%+v", res.Result)
	}
	if !cap.LoggedIn || !cap.Usage || !cap.Resume || cap.AdapterVersion != "0.74.0" || len(cap.Models) != 2 {
		t.Fatalf("cap %+v", cap)
	}
	if cap.ProtocolVersion != contracts.ACPProtocolVersion || !cap.ToolDisallow {
		t.Fatalf("measured protocol/tool_disallow missing: %+v", cap)
	}
	if res.Result.RawInit == nil {
		t.Fatal("raw init not requested for claude_code probe")
	}
}

func TestPongAuthFailureMeansNotLoggedIn(t *testing.T) {
	script := acpfake.Script{Turns: []acpfake.Turn{{Error: &rpcErr}}}
	o := Options{Turn: true, Timeout: 20 * time.Second, Command: func(k contracts.RuntimeKind) (string, []string, []string, bool) {
		c, a, e := acpfake.Command(script, "")
		return c, a, e, true
	}}
	cap := contracts.Capability{Kind: contracts.RuntimeClaudeCode, LoggedIn: true}
	Pong(context.Background(), contracts.RuntimeClaudeCode, o, &cap)
	if cap.LoggedIn {
		t.Fatal("auth failure should clear logged_in")
	}
}

// R3 — adapter_version is measured, never the pin: a mismatched adapter is
// reported with its real version (and the turn is a config failure).
func TestPongReportsMeasuredAdapterVersion(t *testing.T) {
	script := acpfake.Script{AgentVersion: "0.73.0", Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}}
	o := Options{Turn: true, Timeout: 20 * time.Second, Command: func(k contracts.RuntimeKind) (string, []string, []string, bool) {
		c, a, e := acpfake.Command(script, "")
		return c, a, e, true
	}}
	cap := contracts.Capability{Kind: contracts.RuntimeClaudeCode}
	res := Pong(context.Background(), contracts.RuntimeClaudeCode, o, &cap)
	if res.Result.Outcome != "failed" || res.Result.Failure == nil || res.Result.Failure.Kind != contracts.FailConfig {
		t.Fatalf("%+v", res.Result)
	}
	if cap.AdapterVersion != "0.73.0" {
		t.Fatalf("adapter_version %q (must be the measured value, not the pin)", cap.AdapterVersion)
	}
}

// R3 — a static probe (no turn) leaves adapter_version empty.
func TestStaticDetectLeavesAdapterVersionEmpty(t *testing.T) {
	cap, ok := Detect(context.Background(), contracts.RuntimeClaudeCode, Options{})
	if !ok {
		t.Skip("claude CLI not installed")
	}
	if cap.AdapterVersion != "" {
		t.Fatalf("adapter_version %q reported without measurement", cap.AdapterVersion)
	}
}

// D-2 — `resume` is measured, not assumed: a second process must really load
// the session back (harness §6). The two ways it can fail are the two the
// capability exists to warn about.
func TestPongMeasuresResume(t *testing.T) {
	cases := []struct {
		name   string
		script acpfake.Script
		kind   contracts.RuntimeKind
		want   bool
	}{
		{"claude loads it back", acpfake.Script{KnownSessions: []string{"sess-1"}}, contracts.RuntimeClaudeCode, true},
		{"claude lost the session", acpfake.Script{}, contracts.RuntimeClaudeCode, false},
		{"hermes loads it back", acpfake.Script{Kind: "hermes", KnownSessions: []string{"sess-1"}}, contracts.RuntimeHermes, true},
		{"hermes answers null", acpfake.Script{Kind: "hermes"}, contracts.RuntimeHermes, false},
		{"hermes rotates the provenance", acpfake.Script{Kind: "hermes", KnownSessions: []string{"sess-1"},
			LoadProvenance: &acpfake.Provenance{ACPSessionID: "other", RootHermesSessionID: "other"}}, contracts.RuntimeHermes, false},
		{"loadSession not advertised", acpfake.Script{KnownSessions: []string{"sess-1"}, NoLoadSession: true}, contracts.RuntimeClaudeCode, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := tc.script
			script.Turns = []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}, Usage: &contractsUsage}}
			o := Options{DaemonVersion: "t", Turn: true, Timeout: 30 * time.Second, Command: func(k contracts.RuntimeKind) (string, []string, []string, bool) {
				c, a, e := acpfake.Command(script, "")
				return c, a, e, true
			}}
			cap := contracts.Capability{Kind: tc.kind}
			Pong(context.Background(), tc.kind, o, &cap)
			if cap.Resume != tc.want {
				t.Fatalf("resume=%v want %v", cap.Resume, tc.want)
			}
		})
	}
}

// E12-06 / D-2 — a runtime that reports no usage advertises usage=false, so
// the cost card degrades to "추정" (PRD §8.2.6) instead of the daemon
// promising a number nobody measured. Hermes gets no raw system/init, so
// tool_disallow is false for it — also a measurement, not an assumption.
func TestPongMeasuresUsageAndToolDisallow(t *testing.T) {
	cases := []struct {
		name             string
		kind             contracts.RuntimeKind
		usage            *acp.PromptUsage
		wantUsage, wantD bool
	}{
		{"claude with usage", contracts.RuntimeClaudeCode, &contractsUsage, true, true},
		{"claude without usage", contracts.RuntimeClaudeCode, nil, false, true},
		{"hermes has no raw init", contracts.RuntimeHermes, &contractsUsage, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := acpfake.Script{KnownSessions: []string{"sess-1"}, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}, Usage: tc.usage}}}
			if tc.kind == contracts.RuntimeHermes {
				script.Kind = "hermes"
			}
			o := Options{DaemonVersion: "t", Turn: true, Timeout: 30 * time.Second, Command: func(k contracts.RuntimeKind) (string, []string, []string, bool) {
				c, a, e := acpfake.Command(script, "")
				return c, a, e, true
			}}
			cap := contracts.Capability{Kind: tc.kind}
			Pong(context.Background(), tc.kind, o, &cap)
			if cap.Usage != tc.wantUsage || cap.ToolDisallow != tc.wantD {
				t.Fatalf("usage=%v (want %v) tool_disallow=%v (want %v)", cap.Usage, tc.wantUsage, cap.ToolDisallow, tc.wantD)
			}
		})
	}
}

// D-1 — the colab CLI is how every agent reaches the platform (the MCP server
// the daemon registers and the shell path are the same binary). A missing or
// broken one is advertised on the probe and logged; it is never a silent
// tool failure in the middle of somebody's turn.
func TestColabCLIPresenceIsReported(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "colab")
	if err := os.WriteFile(good, []byte("#!/bin/sh\necho 'colab 0.4.2'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "colab-broken")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		bin         string
		wantPresent bool
		wantVersion string
	}{
		{"installed", good, true, "0.4.2"},
		{"not installed", filepath.Join(dir, "definitely-absent"), false, ""},
		{"present but failing", broken, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logs []string
			got := Colab(context.Background(), tc.bin, Options{Log: func(s string) { logs = append(logs, s) }})
			if got.Present != tc.wantPresent || got.Version != tc.wantVersion {
				t.Fatalf("colab %+v want present=%v version=%q", got, tc.wantPresent, tc.wantVersion)
			}
			if !tc.wantPresent && len(logs) == 0 {
				t.Fatal("colab CLI failure was swallowed — nothing logged (D-1)")
			}
		})
	}
}
