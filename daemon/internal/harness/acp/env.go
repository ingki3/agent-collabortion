package acp

import (
	"os"
	"sort"
	"strings"

	"github.com/ingki3/agent-collabortion/contracts"
)

// systemEnvKeys is the "시스템 최소" part of the harness §2.1 allow-list.
var systemEnvKeys = []string{"PATH", "HOME", "LANG", "TMPDIR"}

// TaskEnv is what the daemon knows about one attempt for §2.1.
type TaskEnv struct {
	TaskToken string
	ServerURL string
	TaskID    string
	LaneID    string
	SessionID string
	AgentName string
}

// Env builds the runtime process environment (harness §2.1): an allow-list
// of system variables from the daemon's own environment, the COLAB_* set for
// this attempt, runtime-specific defaults, and the profile env on top. The
// user shell environment is NOT inherited.
func Env(kind contracts.RuntimeKind, t TaskEnv, profileEnv map[string]string) []string {
	m := map[string]string{}
	for _, k := range systemEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			m[k] = v
		}
	}
	if t.TaskToken != "" {
		m["COLAB_TASK_TOKEN"] = t.TaskToken
	}
	m["COLAB_SERVER_URL"] = t.ServerURL
	m["COLAB_TASK_ID"] = t.TaskID
	m["COLAB_LANE_ID"] = t.LaneID
	m["COLAB_SESSION_ID"] = t.SessionID
	m["COLAB_AGENT_NAME"] = t.AgentName
	if kind == contracts.RuntimeHermes {
		// Permission requests must reach session/request_permission (§4);
		// yolo mode would auto-approve inside Hermes.
		m["HERMES_YOLO_MODE"] = "0"
	}
	for k, v := range profileEnv {
		m[k] = v
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
// profile adapter_pin; empty → contracts.ClaudeAgentACPPin.
func Command(kind contracts.RuntimeKind, pin string, args []string) (string, []string) {
	switch kind {
	case contracts.RuntimeClaudeCode:
		if pin == "" {
			pin = contracts.ClaudeAgentACPPin
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
