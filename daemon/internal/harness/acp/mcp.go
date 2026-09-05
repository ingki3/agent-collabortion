package acp

import "strings"

// MCPServer is the ACP session/new·load `mcpServers[]` stdio entry.
type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []EnvVar `json:"env"`
}

// EnvVar is one ACP `env` entry of an MCP server.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ColabMCPName is the one MCP server the daemon registers (colab-cli.md §3:
// tools appear as mcp__colab__* / colab_message_post …).
const ColabMCPName = "colab"

// DefaultColabBin is the colab CLI looked up on PATH when the daemon config
// does not name one.
const DefaultColabBin = "colab"

// ColabMCPServer builds the colab MCP server entry (harness §2 lifecycle
// "mcpServers: [colab MCP]", colab-cli.md §3 `colab mcp serve`) for one
// attempt: command <bin> mcp serve, with every COLAB_* variable of the
// attempt env (§2.1: TOKEN, SERVER_URL, TASK_ID, TASK_ATTEMPT, LANE_ID,
// SESSION_ID, AGENT_NAME) passed explicitly. strictMcpConfig (§3) admits
// only what is listed here, so this is the single MCP server the agent sees.
func ColabMCPServer(bin string, env []string) MCPServer {
	if bin == "" {
		bin = DefaultColabBin
	}
	s := MCPServer{Name: ColabMCPName, Command: bin, Args: []string{"mcp", "serve"}, Env: []EnvVar{}}
	for _, kv := range env {
		if !strings.HasPrefix(kv, ReservedEnvPrefix) {
			continue
		}
		k, v, _ := strings.Cut(kv, "=")
		s.Env = append(s.Env, EnvVar{Name: k, Value: v})
	}
	return s
}
