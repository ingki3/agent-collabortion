// Command acpfake is the scripted fake ACP agent (daemon/acpfake) as a
// standalone binary, for the CI E2E (e2e/p5, T-I5): put it on PATH as
// `hermes` (or behind an `npx` wrapper for claude_code) and the daemon spawns
// it with its own code path — no model, no login. The script comes from
// ACPFAKE_SCRIPT exactly as for the test-binary form; ACPFAKE=1 is implied.
//
// It is test wiring, not a runtime: nothing in the daemon references it.
package main

import (
	"os"

	"github.com/ingki3/agent-collabortion/daemon/acpfake"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		os.Stdout.WriteString("acpfake 0.0.0\n")
		return
	}
	_ = os.Setenv("ACPFAKE", "1")
	if os.Getenv("ACPFAKE_SCRIPT") == "" {
		_ = os.Setenv("ACPFAKE_SCRIPT", "{}")
	}
	acpfake.MaybeMain()
}
