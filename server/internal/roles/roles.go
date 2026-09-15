// Package roles is FR-1.9.1 (v1.1, K-19): the colab commands an agent may use
// are decided by its ROLE, and the decision is enforced on the surface rather
// than asked for in the prompt — the daemon trims the MCP tool list, the CLI
// refuses before sending (exit 3 command_not_allowed), and the server answers
// 403 command_not_allowed to whatever gets through. All three read the same
// table: colab-cli.md §2.5, which this package is the server's copy of.
//
// roles_test.go parses that table out of the contract file and compares it
// with AllowedCommands row by row (T-S13b's way of pinning a sentence to the
// contract): a change to the table without a change here fails CI, and the
// other way round.
package roles

import (
	"strings"

	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// all is every ColabCommand in colab-cli.md §2 order — what `lead` and
// `custom` get, and the order every role's list is emitted in.
var all = []gen.ColabCommand{
	gen.SessionGet, gen.SessionMessages, gen.MessagePost, gen.StatusSet, gen.DecisionRecord,
	gen.LaneDelegate, gen.ArtifactSubmit, gen.ArtifactGet, gen.ReviewApprove, gen.ReviewReject,
	gen.HitlAsk, gen.HitlApproveRequest, gen.HitlRequestInfo,
}

// denied is colab-cli.md §2.5 by exception: the commands each role does NOT
// have. Roles absent here (lead · custom) have everything.
//
//   - researcher · writer · engineer: no `lane delegate` (delegation is the
//     Lead's — a worker that delegates deepens the chain, FR-3.5), no
//     `review approve/reject` (nobody approves their own output), no
//     `hitl approve-request` (asking for completion approval is the Lead's);
//   - reviewer: the same, plus no `artifact submit` (a reviewer does not
//     produce deliverables — the reason for a rejection goes in
//     `review reject --reason`) but WITH `review approve/reject`.
var denied = map[gen.AgentRole]map[gen.ColabCommand]bool{
	gen.Researcher: {gen.LaneDelegate: true, gen.ReviewApprove: true, gen.ReviewReject: true, gen.HitlApproveRequest: true},
	gen.Writer:     {gen.LaneDelegate: true, gen.ReviewApprove: true, gen.ReviewReject: true, gen.HitlApproveRequest: true},
	gen.Engineer:   {gen.LaneDelegate: true, gen.ReviewApprove: true, gen.ReviewReject: true, gen.HitlApproveRequest: true},
	gen.Reviewer:   {gen.LaneDelegate: true, gen.ArtifactSubmit: true, gen.HitlApproveRequest: true},
}

// AllowedCommands is the role's row of colab-cli.md §2.5, in §2 order. An
// unknown role gets everything — the enum is closed by the contract and the
// DB, so this is a stance for a future role rather than a hole: a role the
// table does not know is not silently muted.
func AllowedCommands(role gen.AgentRole) []gen.ColabCommand {
	d := denied[role]
	out := make([]gen.ColabCommand, 0, len(all))
	for _, c := range all {
		if !d[c] {
			out = append(out, c)
		}
	}
	return out
}

// AllowedCommandStrings is AllowedCommands as the daemon bundle carries it
// (contracts.BundleTask.AllowedCommands, daemon-protocol §4.1 v0.8.2).
func AllowedCommandStrings(role gen.AgentRole) []string {
	cmds := AllowedCommands(role)
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, string(c))
	}
	return out
}

// Allows reports whether role may use cmd.
func Allows(role gen.AgentRole, cmd gen.ColabCommand) bool {
	return !denied[role][cmd]
}

// cliNames is the command as the agent typed it (colab-cli.md §2 — the MCP
// tool is the underscore form): the two HITL sub-commands are hyphenated,
// every other name is the enum with its underscore as a space.
var cliNames = map[gen.ColabCommand]string{
	gen.HitlApproveRequest: "hitl approve-request",
	gen.HitlRequestInfo:    "hitl request-info",
}

// CLIName is what the 403's sentence names, so the refusal reads like the
// command the agent ran: `lane delegate` for `lane_delegate`.
func CLIName(cmd gen.ColabCommand) string {
	if n, ok := cliNames[cmd]; ok {
		return n
	}
	return strings.ReplaceAll(string(cmd), "_", " ")
}

// All is every command, for callers that need the closed set (tests, the
// role table check).
func All() []gen.ColabCommand { return append([]gen.ColabCommand(nil), all...) }
