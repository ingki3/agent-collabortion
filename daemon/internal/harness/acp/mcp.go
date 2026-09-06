package acp

import "strings"

// MCP transports of `mcpServers[]`. stdio carries no `type` on the wire (it
// is the ACP baseline); http and sse do, and are only accepted by an agent
// that advertised them in `agentCapabilities.mcpCapabilities`.
const (
	MCPStdio = "stdio"
	MCPHTTP  = "http"
	MCPSSE   = "sse"
)

// MCPServer is one ACP session/new·load `mcpServers[]` entry. Type empty ==
// stdio (Command/Args/Env); http and sse use URL/Headers.
type MCPServer struct {
	Type    string      `json:"type,omitempty"`
	Name    string      `json:"name"`
	Command string      `json:"command,omitempty"`
	Args    []string    `json:"args,omitempty"`
	Env     []EnvVar    `json:"env"`
	URL     string      `json:"url,omitempty"`
	Headers []MCPHeader `json:"headers,omitempty"`
}

// MCPHeader is one http/sse request header.
type MCPHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Transport is the entry's transport with the stdio default applied.
func (s MCPServer) Transport() string {
	if s.Type == "" {
		return MCPStdio
	}
	return s.Type
}

// FilterMCPServers splits the list into what this agent can accept and what
// it cannot (PRD §8.2.3, the Hermes row: the MCP list is filtered against the
// runtime `mcpCapabilities`). stdio is always kept; http and sse need the
// matching advertised flag; anything else is unknown to us and dropped.
// Dropping is never silent — the runner puts every dropped server on the
// activity feed (harness §7 runtime class).
func FilterMCPServers(servers []MCPServer, caps MCPCapabilities) (kept, dropped []MCPServer) {
	for _, s := range servers {
		ok := false
		switch s.Transport() {
		case MCPStdio:
			ok = true
		case MCPHTTP:
			ok = caps.HTTP
		case MCPSSE:
			ok = caps.SSE
		}
		if ok {
			kept = append(kept, s)
			continue
		}
		dropped = append(dropped, s)
	}
	return kept, dropped
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
