// Command daemon runs on the user's machine: pairing/probe, task claim,
// workdir prep, brief files, ACP harness, task_event push, heartbeat,
// orphan cleanup (PLAN.md §2 stream D).
//
// P0-a skeleton: prints version and exits. Harness lands in P1 after G2;
// spike 1–3 results (G1) decide the harness interface first.
package main

import (
	"fmt"
	"os"

	"github.com/ingki3/agent-collabortion/contracts"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("colab-daemon %s (contracts %s)\n", version, contracts.Version)
		return
	}
	fmt.Fprintln(os.Stderr, "colab-daemon: skeleton — nothing to run before G2. try: daemon version")
	os.Exit(2)
}
