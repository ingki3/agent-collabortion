package acp

import (
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
)

// §2.1 — allow-list only: no user shell variables leak, COLAB_* present,
// profile env added, Hermes yolo off.
func TestEnvAllowList(t *testing.T) {
	t.Setenv("SECRET_SHELL_VAR", "leak")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("PATH", "/usr/bin")
	env := Env(contracts.RuntimeHermes, TaskEnv{TaskToken: "ctk_1", ServerURL: "http://s", TaskID: "t", LaneID: "l", SessionID: "s", AgentName: "Lead"}, map[string]string{"MY_KEY": "v"})
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"SECRET_SHELL_VAR", "CLAUDECODE"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("%s leaked: %v", bad, env)
		}
	}
	for k, v := range map[string]string{"COLAB_TASK_TOKEN": "ctk_1", "COLAB_SERVER_URL": "http://s", "COLAB_TASK_ID": "t", "COLAB_LANE_ID": "l", "COLAB_SESSION_ID": "s", "COLAB_AGENT_NAME": "Lead", "PATH": "/usr/bin", "MY_KEY": "v", "HERMES_YOLO_MODE": "0"} {
		if EnvValue(env, k) != v {
			t.Fatalf("%s=%q want %q (%v)", k, EnvValue(env, k), v, env)
		}
	}
	if EnvValue(Env(contracts.RuntimeClaudeCode, TaskEnv{}, nil), "HERMES_YOLO_MODE") != "" {
		t.Fatal("hermes var set for claude")
	}
}

func TestCommandPins(t *testing.T) {
	c, a := Command(contracts.RuntimeClaudeCode, "", nil)
	if c != "npx" || strings.Join(a, " ") != "-y @agentclientprotocol/claude-agent-acp@0.74.0" {
		t.Fatalf("%s %v", c, a)
	}
	c, a = Command(contracts.RuntimeHermes, "", []string{"--x"})
	if c != "hermes" || strings.Join(a, " ") != "acp --x" {
		t.Fatalf("%s %v", c, a)
	}
}

func TestParseResetTime(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC) // 12:00 KST
	got, ok := ParseResetTime("You've hit your limit · resets 11am (Asia/Seoul)", now)
	if !ok || !got.Equal(time.Date(2026, 9, 6, 2, 0, 0, 0, time.UTC)) { // next day 11:00 KST
		t.Fatalf("%v %v", got, ok)
	}
	got, ok = ParseResetTime("resets 3:30pm (UTC)", now)
	if !ok || !got.Equal(time.Date(2026, 9, 5, 15, 30, 0, 0, time.UTC)) {
		t.Fatalf("%v %v", got, ok)
	}
	if _, ok := ParseResetTime("no time here", now); ok {
		t.Fatal("false positive")
	}
}

func TestClassifyAuthAndPrefixes(t *testing.T) {
	for _, p := range contracts.UsageLimitPrefixes {
		f := Classify(ClassifyInput{Err: &RPCError{Code: -32603, Message: "Internal error: " + p}, Now: time.Now()})
		if f.Kind != contracts.FailRateLimited && f.Kind != contracts.FailQuota {
			t.Fatalf("prefix %q → %s", p, f.Kind)
		}
	}
	f := Classify(ClassifyInput{Err: ErrProcessExited, Stderr: "Error: Login expired · Please run /login"})
	if f.Kind != contracts.FailAuth {
		t.Fatalf("auth → %s", f.Kind)
	}
	if f := Classify(ClassifyInput{Err: ErrProtocolVersion}); f.Kind != contracts.FailConfig {
		t.Fatalf("protocol → %s", f.Kind)
	}
	if f := Classify(ClassifyInput{Err: ErrProcessExited}); f.Kind != contracts.FailOther || !strings.Contains(f.Detail, "UnexpectedExit") {
		t.Fatalf("exit → %+v", f)
	}
}
