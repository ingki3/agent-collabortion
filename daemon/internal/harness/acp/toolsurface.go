package acp

import "github.com/ingki3/agent-collabortion/contracts"

// Tool surface values (harness §10 v0.8, contracts.Capability.ToolSurface) —
// HOW the agent talks back to the platform, which is not the same question as
// how the brief reaches it (brief_transport).
const (
	// ToolSurfaceMCP: the runtime honours session/new.mcpServers, so the
	// colab MCP server is the agent's channel (ColabMCPServer).
	ToolSurfaceMCP = "mcp"
	// ToolSurfaceCLIWrapper: the runtime ignores mcpServers, so the daemon
	// writes a per-attempt wrapper executable and puts its absolute path in
	// the text it hands the agent (internal/toolwrap).
	ToolSurfaceCLIWrapper = "cli_wrapper"
)

// ToolSurface is the §10 judgement, MEASURED: an initialize response that
// carries `agentCapabilities.mcpCapabilities` speaks MCP; one that does not
// (Hermes, G5) silently drops the mcpServers we send.
func (c AgentCaps) ToolSurface() string {
	if c.MCPAdvertised {
		return ToolSurfaceMCP
	}
	return ToolSurfaceCLIWrapper
}

// DefaultToolSurface is the §10 table, used ONLY before initialize has
// happened: the wrapper file and the CLI-path rewrite must be decided while
// the attempt is still being prepared. The measured value from the probe turn
// (or from the previous attempt on this daemon) supersedes it — PRD §8.2.1
// judges a capability by what the session advertised, and this is the
// placeholder that holds until there is such a measurement.
func DefaultToolSurface(kind contracts.RuntimeKind) string {
	if kind == contracts.RuntimeHermes {
		return ToolSurfaceCLIWrapper
	}
	return ToolSurfaceMCP
}
