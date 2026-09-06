package acp_test

import (
	"encoding/json"
	"testing"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/acpfake"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// harness §10 v0.8 — the judgement is the PRESENCE of `mcpCapabilities`, not
// what it says. An agent that omits the key does not speak MCP at all and
// drops session/new.mcpServers silently (Hermes, G5 (b)).
func TestCapsMCPAdvertised(t *testing.T) {
	cases := []struct {
		name    string
		caps    string
		surface string
	}{
		{"empty object advertised", `{"loadSession":true,"mcpCapabilities":{}}`, acp.ToolSurfaceMCP},
		{"http advertised", `{"mcpCapabilities":{"http":true}}`, acp.ToolSurfaceMCP},
		{"all false still advertised", `{"mcpCapabilities":{"http":false,"sse":false}}`, acp.ToolSurfaceMCP},
		{"key absent", `{"loadSession":true}`, acp.ToolSurfaceCLIWrapper},
		{"key null", `{"mcpCapabilities":null}`, acp.ToolSurfaceCLIWrapper},
		{"no agentCapabilities at all", ``, acp.ToolSurfaceCLIWrapper},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := &acp.InitializeResult{ProtocolVersion: contracts.ACPProtocolVersion}
			if c.caps != "" {
				res.AgentCapabilities = json.RawMessage(c.caps)
			}
			if got := res.Caps().ToolSurface(); got != c.surface {
				t.Fatalf("tool_surface %q want %q", got, c.surface)
			}
		})
	}
}

// The pre-spawn table (§10): used only until a turn has measured something.
func TestDefaultToolSurface(t *testing.T) {
	if got := acp.DefaultToolSurface(contracts.RuntimeClaudeCode); got != acp.ToolSurfaceMCP {
		t.Fatalf("claude_code %q", got)
	}
	if got := acp.DefaultToolSurface(contracts.RuntimeHermes); got != acp.ToolSurfaceCLIWrapper {
		t.Fatalf("hermes %q", got)
	}
}

// Measured end to end through the fake: the same runner, one script that
// advertises mcpCapabilities and one that does not.
func TestRunnerMeasuresToolSurface(t *testing.T) {
	t.Run("mcp", func(t *testing.T) {
		f := newFixture(t, acpfake.Script{Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}}, bundle(contracts.RuntimeClaudeCode), nil)
		res := f.run()
		if res.ToolSurface != acp.ToolSurfaceMCP {
			t.Fatalf("tool_surface %q want mcp", res.ToolSurface)
		}
		if !res.Caps.MCPAdvertised {
			t.Fatal("mcpCapabilities not seen")
		}
	})
	t.Run("cli_wrapper", func(t *testing.T) {
		s := acpfake.Script{Kind: "hermes", NoMCPCapabilities: true, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}}}}
		f := newFixture(t, s, bundle(contracts.RuntimeHermes), nil)
		res := f.run()
		if res.ToolSurface != acp.ToolSurfaceCLIWrapper {
			t.Fatalf("tool_surface %q want cli_wrapper", res.ToolSurface)
		}
		if res.Caps.MCPAdvertised {
			t.Fatal("mcpCapabilities reported without the key")
		}
	})
}
