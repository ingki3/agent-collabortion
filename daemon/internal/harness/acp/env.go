package acp

import (
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ingki3/agent-collabortion/contracts"
)

// systemEnvKeys is the "시스템 최소" part of the harness §2.1 allow-list.
// USER (v0.6) is here because refreshing an expired OAuth session looks the
// user up in the macOS keychain through it — without it every task fails
// with failure_kind=auth (G4 blocker).
var systemEnvKeys = []string{"PATH", "HOME", "LANG", "TMPDIR", "USER"}

// ReservedEnvPrefix marks variables the daemon owns (harness §2.1): the
// profile env may not set or override them — COLAB_SERVER_URL would
// redirect the attempt token to an arbitrary host (FR-9.1, PR #20 R5).
const ReservedEnvPrefix = "COLAB_"

// TaskEnv is what the daemon knows about one attempt for §2.1.
type TaskEnv struct {
	TaskToken string
	ServerURL string
	TaskID    string
	Attempt   int
	LaneID    string
	SessionID string
	AgentName string
}

// colabVars is the COLAB_* set for this attempt (colab-cli.md §1).
func (t TaskEnv) colabVars() map[string]string {
	m := map[string]string{
		"COLAB_SERVER_URL":   t.ServerURL,
		"COLAB_TASK_ID":      t.TaskID,
		"COLAB_TASK_ATTEMPT": strconv.Itoa(t.Attempt),
		"COLAB_LANE_ID":      t.LaneID,
		"COLAB_SESSION_ID":   t.SessionID,
		"COLAB_AGENT_NAME":   t.AgentName,
	}
	if t.TaskToken != "" {
		m["COLAB_TASK_TOKEN"] = t.TaskToken
	}
	return m
}

// Env builds the runtime process environment (harness §2.1): the profile
// env first (user-specified additions only), then the allow-listed system
// variables from the daemon's own environment, the COLAB_* set for this
// attempt and runtime-specific defaults on top — so the daemon-owned values
// always win and COLAB_* can never be overridden (R5). Profile keys under
// ReservedEnvPrefix are dropped. The user shell environment is NOT inherited.
func Env(kind contracts.RuntimeKind, t TaskEnv, profileEnv map[string]string) []string {
	m := map[string]string{}
	for k, v := range profileEnv {
		if strings.HasPrefix(k, ReservedEnvPrefix) {
			continue
		}
		m[k] = v
	}
	for _, k := range systemEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	for k, v := range t.colabVars() {
		m[k] = v
	}
	if kind == contracts.RuntimeHermes {
		// Permission requests must reach session/request_permission (§4);
		// yolo mode would auto-approve inside Hermes.
		m["HERMES_YOLO_MODE"] = "0"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

// Command returns the adapter command for a runtime (harness §1). pin is the
// profile adapter_pin; empty → AdapterPin.
func Command(kind contracts.RuntimeKind, pin string, args []string) (string, []string) {
	switch kind {
	case contracts.RuntimeClaudeCode:
		if pin == "" {
			pin = AdapterPin
		}
		return "npx", append([]string{"-y", contracts.ClaudeAgentACPPackage + "@" + pin}, args...)
	case contracts.RuntimeHermes:
		return "hermes", append([]string{"acp"}, args...)
	}
	return "", nil
}

// EnvValue reads KEY from an env slice ("" when absent).
func EnvValue(env []string, key string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			return kv[len(key)+1:]
		}
	}
	return ""
}
