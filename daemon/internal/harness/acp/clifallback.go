package acp

import (
	"strconv"

	"github.com/ingki3/agent-collabortion/contracts"
)

// The CLI fallback argv (PRD §8.2.4, EVAL E9-06).
//
// WHY THIS EXISTS WHEN v1 NEVER TAKES THE PATH
//
// G1 fixed ACP as the only transport for v1 (`harness.md` §1: "`cli`는 타입에만
// 두고 구현하지 않는다"), so nothing in the daemon spawns these argv today.
// What E9-06 pins is not the spawning but the DOUBLE ENFORCEMENT rule
// (FR-7.3): when a runtime has a budget cap of its own, our accumulation is
// not the only line of defence, because ours lags by a turn — cost arrives in
// the `session/prompt` response, after the money is spent. A fallback path
// built later without the flag would silently drop the second line, and the
// only place that omission is visible is the argv.
//
// So this is the §8.2.4 table encoded, with the one rule that has a test.
// Hermes has no budget flag: `hermes -z` takes `--usage-file`, which REPORTS
// the cost afterwards, and reporting is not capping — the row is empty rather
// than filled with a flag that does not exist.

// CLIPath and ACPPath name which of the two §8.2.3 paths an invocation is.
const (
	ACPPath = "acp"
	CLIPath = "cli"
)

// CLIInvocation is one runtime's fallback command line.
type CLIInvocation struct {
	// Path is "cli" — the field exists so a caller can tell a fallback
	// invocation from the ACP one it replaced.
	Path    string
	Command string
	Args    []string
}

// CLIFallbackOptions is what the caller knows about the attempt.
type CLIFallbackOptions struct {
	Model string
	// BudgetUSD ≤ 0 omits the cap entirely rather than sending 0, which
	// Claude Code reads as "no budget at all is allowed".
	BudgetUSD float64
	Effort    string
	MaxTurns  int
	SessionID string // --resume
	MCPConfig string
	Settings  string
	// UsageFile is the hermes `--usage-file` sink.
	UsageFile string
	// Workdir is the hermes `--in` lane directory.
	Workdir string
}

// CLIFallback builds the §8.2.4 argv for a runtime.
func CLIFallback(kind contracts.RuntimeKind, o CLIFallbackOptions) CLIInvocation {
	switch kind {
	case contracts.RuntimeClaudeCode:
		args := []string{
			"-p",
			"--input-format", "stream-json",
			"--output-format", "stream-json",
			"--verbose",
			"--permission-mode", "bypassPermissions",
			"--disallowedTools", "AskUserQuestion",
		}
		if o.MCPConfig != "" {
			args = append(args, "--mcp-config", o.MCPConfig, "--strict-mcp-config")
		}
		if o.Model != "" {
			args = append(args, "--model", o.Model)
		}
		if o.Effort != "" {
			args = append(args, "--effort", o.Effort)
		}
		if o.MaxTurns > 0 {
			args = append(args, "--max-turns", strconv.Itoa(o.MaxTurns))
		}
		if o.BudgetUSD > 0 {
			args = append(args, "--max-budget-usd", formatUSD(o.BudgetUSD))
		}
		if o.SessionID != "" {
			args = append(args, "--resume", o.SessionID)
		}
		if o.Settings != "" {
			args = append(args, "--settings", o.Settings)
		}
		return CLIInvocation{Path: CLIPath, Command: "claude", Args: args}
	case contracts.RuntimeHermes:
		args := []string{"-z"}
		if o.Model != "" {
			args = append(args, "--model", o.Model)
		}
		args = append(args, "--yolo")
		if o.UsageFile != "" {
			args = append(args, "--usage-file", o.UsageFile)
		}
		if o.Workdir != "" {
			args = append(args, "--in", o.Workdir)
		}
		return CLIInvocation{Path: CLIPath, Command: "hermes", Args: args}
	}
	return CLIInvocation{Path: CLIPath}
}

// formatUSD renders a dollar amount the way a flag takes it: 1.5, not 1.500000
// and not 1.5000000000000002.
func formatUSD(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
