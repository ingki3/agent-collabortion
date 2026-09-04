// Command colab is the CLI agents use to talk back to the platform
// (PRD FR-7.4): session · message · lane · hitl · artifact · decision ·
// status · review. Authenticated with COLAB_TASK_TOKEN. Also exposed as an
// MCP server (PLAN.md §2 stream C).
//
// P0-a skeleton: only `colab version`. Commands land per phase after G2
// according to contracts/colab-cli.md.
package main

import (
	"fmt"
	"os"

	"github.com/ingki3/agent-collabortion/contracts"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("colab %s (contracts %s)\n", version, contracts.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "colab: skeleton — commands are defined in contracts/colab-cli.md after G2. try: colab version")
	os.Exit(2)
}
