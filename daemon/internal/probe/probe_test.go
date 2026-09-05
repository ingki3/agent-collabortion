package probe

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/daemon/internal/acpfake"
)

func TestMain(m *testing.M) {
	acpfake.MaybeMain()
	os.Exit(m.Run())
}

// PONG turn against the fake: version/models/usage/resume folded into the
// capability; isolation raw init requested for claude_code.
func TestPongFoldsCapability(t *testing.T) {
	script := acpfake.Script{AgentVersion: "0.74.0", Models: []string{"claude-sonnet-5", "claude-haiku-4-5"}, Turns: []acpfake.Turn{{Steps: []acpfake.Step{{Chunk: "PONG"}}, ModelUsage: true, Usage: &contractsUsage}}}
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
