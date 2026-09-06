package main

// P3 HITL command tests (contracts/colab-cli.md §2.4) at the process
// boundary: argument parsing, exit codes, and the JSON an agent parses.
//
// TestP3CommandAndMCPToolAgree is the DoD's MCP round trip for HITL: each
// command is run twice against the same fake — once through run() and once as
// the same-named MCP tool over stdio — and the two documents must be equal,
// which is what "the tool surface uses the same shared code" means in
// practice (harness.md §10: Claude Code reaches these through MCP, Hermes
// through the cli_wrapper, and both must get the same answer).

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/client/clienttest"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// E7-01 at the CLI boundary.
func TestCLIHitlAsk(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()),
		"hitl", "ask", "--question", "독자?", "--default", "투자자", "--context", "브리프에 없다")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["turn_end_required"] != true {
		t.Fatalf("turn_end_required = %v, want true", v["turn_end_required"])
	}
	if v["instruction"] != colab.TurnEndInstruction {
		t.Fatalf("instruction = %v, want %q", v["instruction"], colab.TurnEndInstruction)
	}
	if v["hitl_id"] != clienttest.HitlID || v["type"] != "question" {
		t.Fatalf("v = %v", v)
	}
	// The instruction is spelled `turn_end_required`, never shortened to
	// ACP's `end_turn` — the two mean opposite directions (Lead, PR #59).
	if _, wrong := v["end_turn"]; wrong {
		t.Fatalf("the flag must be named turn_end_required only: %v", v)
	}
	if s.HitlCalls[0].Body["type"] != "question" {
		t.Fatalf("body = %v", s.HitlCalls[0].Body)
	}
}

func TestCLIHitlAskChoice(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()),
		"hitl", "ask", "--question", "어느 쪽?", "--default", "B", "--choices", "A,B", "--choices", "C")
	if code != client.ExitOK {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if v["type"] != "choice" {
		t.Fatalf("type = %v", v["type"])
	}
	opts, _ := s.HitlCalls[0].Body["options"].([]any)
	if len(opts) != 3 {
		t.Fatalf("options = %v (repeatable and comma-separated)", opts)
	}
}

func TestCLIHitlApproveRequestAndRequestInfo(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()), "hitl", "approve-request", "--summary", "배포?")
	if code != client.ExitOK || v["type"] != "approval" || v["turn_end_required"] != true {
		t.Fatalf("approve-request: code=%d v=%v", code, v)
	}

	s2 := clienttest.New(t)
	code, v, _ = exec(t, s2.Env(t.TempDir()), "hitl", "request-info", "--what", "API 키", "--why", "재현 불가")
	if code != client.ExitOK || v["type"] != "info" {
		t.Fatalf("request-info: code=%d v=%v", code, v)
	}
	if s2.HitlCalls[0].Body["what"] != "API 키" || s2.HitlCalls[0].Body["why"] != "재현 불가" {
		t.Fatalf("body = %v", s2.HitlCalls[0].Body)
	}
}

// `--question` is the alias of `--what` the task brief names; the wire field
// is `what` either way (openapi HitlCreateInfo).
func TestCLIHitlRequestInfoQuestionAlias(t *testing.T) {
	s := clienttest.New(t)
	code, v, _ := exec(t, s.Env(t.TempDir()), "hitl", "request-info", "--question", "API 키")
	if code != client.ExitOK || v["type"] != "info" {
		t.Fatalf("code=%d v=%v", code, v)
	}
	if s.HitlCalls[0].Body["what"] != "API 키" {
		t.Fatalf("body = %v, want --question folded into `what`", s.HitlCalls[0].Body)
	}
}

// E7-05 · E7-20 and the rest of the argument surface: exit 2, nothing sent.
func TestCLIHitlUsageExit2(t *testing.T) {
	for _, args := range [][]string{
		{"hitl"},
		{"hitl", "nope"},
		{"hitl", "ask"},                      // no --question, no --default
		{"hitl", "ask", "--question", "독자?"}, // E7-05: no --default
		{"hitl", "ask", "--default", "투자자"},  // no --question
		{"hitl", "ask", "--question", "어느?", "--choices", "A,B"},                   // E7-20: choice, no --default
		{"hitl", "ask", "--question", "어느?", "--default", "A", "--choices", "A"},   // < 2 options
		{"hitl", "ask", "--question", "어느?", "--default", "Z", "--choices", "A,B"}, // default outside options
		{"hitl", "ask", "--question", "독자?", "--default", "투자자", "extra"},          // stray argument
		{"hitl", "approve-request"}, // no --summary
		{"hitl", "request-info"},    // no --what
	} {
		s := clienttest.New(t)
		code, _, _ := exec(t, s.Env(t.TempDir()), args...)
		if code != client.ExitUsage {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if len(s.HitlCalls) != 0 {
			t.Errorf("%v: sent %d requests, want none", args, len(s.HitlCalls))
		}
	}
}

// E7-04 at the CLI boundary: the second open request on the task is exit 3
// carrying the server's own code and message.
func TestCLIHitlSecondRequestExit3(t *testing.T) {
	s := clienttest.New(t)
	env := s.Env(t.TempDir())
	if code, v, _ := exec(t, env, "hitl", "ask", "--question", "독자?", "--default", "투자자"); code != client.ExitOK {
		t.Fatalf("first: code=%d v=%v", code, v)
	}
	code, v, stderr := exec(t, env, "hitl", "ask", "--question", "또?", "--default", "예")
	if code != client.ExitRefused {
		t.Fatalf("code = %d, want 3", code)
	}
	if errCode(v) != colab.ServerHitlAlreadyOpen {
		t.Fatalf("code = %q, v = %v", errCode(v), v)
	}
	e, _ := v["error"].(map[string]any)
	if detail, _ := e["detail"].(string); !strings.Contains(detail, clienttest.HitlID) {
		t.Fatalf("detail = %q, want the server's message with the open request id", detail)
	}
	if !strings.Contains(stderr, colab.ServerHitlAlreadyOpen) {
		t.Fatalf("stderr should carry the one-line reason, got %q", stderr)
	}
}

// C-3 regression. The daemon probe takes the FIRST \d+\.\d+\.\d+ in
// `colab --version` (daemon/internal/probe.CLIVersion) and stores it as
// colab_cli.version. While the default version was "dev" that match was the
// contracts version, so S11 showed the contract set's number as the CLI's.
func TestVersionFirstMatchIsTheCLIVersion(t *testing.T) {
	probeRe := regexp.MustCompile(`\d+\.\d+\.\d+`) // probe/probe.go versionRe
	var out, errb strings.Builder
	if code := run([]string{"--version"}, func(string) string { return "" }, nil, &out, &errb); code != 0 {
		t.Fatalf("exit %d", code)
	}
	line := out.String()
	got := probeRe.FindString(line)
	want := probeRe.FindString(version)
	if want == "" {
		t.Fatalf("version %q is not x.y.z — the probe would fall through to the contracts version", version)
	}
	if got != want {
		t.Fatalf("probe would report %q from %q; want the CLI version %q", got, strings.TrimSpace(line), want)
	}
}

// ─────────────────── command ↔ MCP tool round trip (DoD) ───────────────────

func TestP3CommandAndMCPToolAgree(t *testing.T) {
	cases := []struct {
		name     string
		argv     []string
		tool     string
		args     map[string]any
		setup    func(*clienttest.Server)
		wantExit int
	}{
		{
			name: "hitl ask question (E7-01)", tool: "colab_hitl_ask",
			argv: []string{"hitl", "ask", "--question", "독자?", "--default", "투자자", "--context", "c"},
			args: map[string]any{"question": "독자?", "default": "투자자", "context": "c"},
		},
		{
			name: "hitl ask choice", tool: "colab_hitl_ask",
			argv: []string{"hitl", "ask", "--question", "어느?", "--default", "B", "--choices", "A,B"},
			args: map[string]any{"question": "어느?", "default": "B", "choices": []string{"A", "B"}},
		},
		{
			name: "hitl ask no default (E7-05)", tool: "colab_hitl_ask",
			argv:     []string{"hitl", "ask", "--question", "독자?"},
			args:     map[string]any{"question": "독자?"},
			wantExit: client.ExitUsage,
		},
		{
			name: "hitl ask choice no default (E7-20)", tool: "colab_hitl_ask",
			argv:     []string{"hitl", "ask", "--question", "어느?", "--choices", "A,B"},
			args:     map[string]any{"question": "어느?", "choices": []string{"A", "B"}},
			wantExit: client.ExitUsage,
		},
		{
			name: "hitl approve-request (E7-06)", tool: "colab_hitl_approve_request",
			argv: []string{"hitl", "approve-request", "--summary", "배포?", "--artifact", clienttest.ArtifactID},
			args: map[string]any{"summary": "배포?", "artifact": clienttest.ArtifactID},
		},
		{
			name: "hitl request-info (E7-21)", tool: "colab_hitl_request_info",
			argv: []string{"hitl", "request-info", "--what", "API 키", "--why", "재현 불가"},
			args: map[string]any{"what": "API 키", "why": "재현 불가"},
		},
		{
			name: "hitl already open (E7-04)", tool: "colab_hitl_ask",
			argv:     []string{"hitl", "ask", "--question", "또?", "--default", "예"},
			args:     map[string]any{"question": "또?", "default": "예"},
			setup:    func(s *clienttest.Server) { s.OpenHitlID = clienttest.HitlID },
			wantExit: client.ExitRefused,
		},
		{
			name: "revoked token (E11-04)", tool: "colab_hitl_ask",
			argv:     []string{"hitl", "ask", "--question", "독자?", "--default", "투자자"},
			args:     map[string]any{"question": "독자?", "default": "투자자"},
			setup:    func(s *clienttest.Server) { s.Revoked = true },
			wantExit: client.ExitNoToken,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sCLI := clienttest.New(t)
			if tc.setup != nil {
				tc.setup(sCLI)
			}
			code, cli, _ := exec(t, sCLI.Env(t.TempDir()), tc.argv...)
			if code != tc.wantExit {
				t.Fatalf("CLI exit = %d, want %d: %v", code, tc.wantExit, cli)
			}

			sMCP := clienttest.New(t)
			if tc.setup != nil {
				tc.setup(sMCP)
			}
			tool, isErr := mcpCall(t, sMCP.Env(t.TempDir()), tc.tool, tc.args)
			if isErr != (tc.wantExit != client.ExitOK) {
				t.Fatalf("tool isError = %v, want %v", isErr, tc.wantExit != client.ExitOK)
			}
			if tc.wantExit != client.ExitOK {
				e, _ := tool["error"].(map[string]any)
				if e == nil || e["exit"] != float64(tc.wantExit) {
					t.Fatalf("tool error = %v, want exit %d", tool, tc.wantExit)
				}
			}
			scrubTimestamps(cli)
			scrubTimestamps(tool)
			if len(cli) == 0 || len(tool) == 0 {
				t.Fatalf("nothing left to compare — cli=%v tool=%v", cli, tool)
			}
			if !equalJSON(t, cli, tool) {
				a, _ := json.Marshal(cli)
				b, _ := json.Marshal(tool)
				t.Fatalf("command and tool disagree\n  cmd:  %s\n  tool: %s", a, b)
			}
			// Same server call, not just the same printed result.
			if len(sCLI.HitlCalls) != len(sMCP.HitlCalls) {
				t.Fatalf("hitl calls: CLI %d, tool %d", len(sCLI.HitlCalls), len(sMCP.HitlCalls))
			}
			for i := range sCLI.HitlCalls {
				if !equalJSON(t, sCLI.HitlCalls[i].Body, sMCP.HitlCalls[i].Body) {
					a, _ := json.Marshal(sCLI.HitlCalls[i].Body)
					b, _ := json.Marshal(sMCP.HitlCalls[i].Body)
					t.Fatalf("request bodies differ\n  cmd:  %s\n  tool: %s", a, b)
				}
			}
		})
	}
}
