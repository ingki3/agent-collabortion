package probe

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

var contractsUsage = acp.PromptUsage{InputTokens: 3, OutputTokens: 1}
var rpcErr = acp.RPCError{Code: -32603, Message: "Internal error", Data: json.RawMessage(`{"errorKind":"authentication_failed"}`)}

// Real adapters, one PONG turn each (task DoD: COLAB_SMOKE=1 only).
//
//	COLAB_SMOKE=1 go test ./internal/probe -run Smoke -v
func TestSmokeRealAdapters(t *testing.T) {
	if os.Getenv("COLAB_SMOKE") != "1" {
		t.Skip("set COLAB_SMOKE=1 to run the real-adapter smoke")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	o := Options{DaemonVersion: "smoke", Turn: true, Log: func(s string) { t.Log(s) }}
	p := Run(ctx, o)
	b, _ := json.MarshalIndent(p, "", "  ")
	t.Logf("probe:\n%s", b)
	// D-1: the colab CLI is a machine property reported alongside the runtimes.
	t.Logf("colab_cli: %+v", Colab(ctx, "", o))
	found := map[contracts.RuntimeKind]bool{}
	for _, c := range p.Capabilities {
		found[c.Kind] = true
		if !c.LoggedIn {
			t.Errorf("%s: PONG turn did not complete (logged_in=false)", c.Kind)
		}
		if c.Kind == contracts.RuntimeClaudeCode && c.AdapterVersion != contracts.ClaudeAgentACPPin {
			t.Errorf("claude adapter %q != pin %q", c.AdapterVersion, contracts.ClaudeAgentACPPin)
		}
		if c.Kind == contracts.RuntimeClaudeCode && c.BriefTransport != contracts.BriefACPMetaSystemPrompt {
			t.Errorf("claude transport %s", c.BriefTransport)
		}
		if c.Kind == contracts.RuntimeHermes && c.BriefTransport != contracts.BriefInstructionFile {
			t.Errorf("hermes transport %s", c.BriefTransport)
		}
		// D-2: these are measurements now — a real turn must produce them.
		if c.ProtocolVersion != contracts.ACPProtocolVersion {
			t.Errorf("%s protocol_version %d (measured from initialize)", c.Kind, c.ProtocolVersion)
		}
		if !c.Resume {
			t.Errorf("%s resume=false — session/load did not bring the session back", c.Kind)
		}
		if !c.Usage {
			t.Errorf("%s usage=false — the turn reported no tokens", c.Kind)
		}
		if c.Kind == contracts.RuntimeClaudeCode && !c.ToolDisallow {
			t.Error("claude tool_disallow=false — disallowedTools did not take effect")
		}
	}
	for _, k := range []contracts.RuntimeKind{contracts.RuntimeClaudeCode, contracts.RuntimeHermes} {
		if !found[k] {
			t.Errorf("%s not detected", k)
		}
	}
	// §12 (c) on the real adapter: no mcp__ tools, 0 hooks
	cap := contracts.Capability{Kind: contracts.RuntimeClaudeCode}
	res := Pong(ctx, contracts.RuntimeClaudeCode, Options{Turn: true, Log: func(s string) { t.Log(s) }}, &cap)
	if res.Result.Outcome != "completed" || !strings.Contains(strings.ToUpper(res.Result.Text), "PONG") {
		t.Fatalf("claude PONG: %+v", res.Result)
	}
	if res.Result.RawInit == nil {
		t.Fatal("no raw system/init captured")
	}
	for _, tl := range res.Result.RawInit.Tools {
		if strings.HasPrefix(tl, "mcp__") {
			t.Errorf("mcp tool leaked into isolated session: %s", tl)
		}
	}
	if res.Result.RawInit.Hooks != 0 {
		t.Errorf("hooks fired: %d", res.Result.RawInit.Hooks)
	}
}
