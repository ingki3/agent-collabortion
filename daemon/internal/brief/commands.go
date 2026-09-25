package brief

import (
	"regexp"
	"strings"

	"github.com/ingki3/agent-collabortion/daemon/internal/commands"
	"github.com/ingki3/agent-collabortion/daemon/internal/harness/acp"
)

// sectionRe matches a section header line of the server's brief ("[2]
// Workspace rules and colab CLI") or of Assemble's ("## [2] Workspace Rules").
var sectionRe = regexp.MustCompile(`^(## )?\[(\d)\] `)

// RestrictCommands is harness §10 v0.8.10 for brief [2] (K-19): the colab
// command section names only the commands the bundle allows, and says in one
// line what this role does not use. The lines go at the END of [2], so the
// server's [2] text stays byte-identical ahead of them and [1]~[5] remain
// stable within a session (E12-11 — the allowed list is a property of the
// role, constant for the agent's lifetime).
//
// Defensively, a [2] line that names a denied command in command position
// (`colab lane delegate`, or the tool name colab_lane_delegate) is dropped:
// today's server [2] only names message_post/room_messages/room_get,
// which every role has, so nothing is dropped in practice — the rule is
// there for the day a server line says otherwise.
//
// An empty allowed list means everything (daemon-protocol §4.1 v0.8.2) and
// leaves the text alone; so does a brief with no [2] header (a test-shaped
// brief) — there is no colab section to restrict.
//
// It runs BEFORE the cli_wrapper rewrite (toolwrap.RewriteCLI): the command
// names it writes are `colab …` in command position and must reach a hermes
// agent as the wrapper's absolute path like every other one.
//
// surface is the attempt's harness §10 tool_surface (acp.ToolSurfaceMCP ·
// acp.ToolSurfaceCLIWrapper). v0.9.6: the allowed line speaks the surface's
// words — MCP tool names for `mcp` (that shell has no `colab`, and a line that
// spells the shell command is what sent a claude_code Lead to run it and fail),
// the shell command for `cli_wrapper`.
func RestrictCommands(text string, allowed []string, surface string) string {
	if len(allowed) == 0 || text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	start := -1
	for i, l := range lines {
		if m := sectionRe.FindStringSubmatch(l); m != nil && m[2] == "2" {
			start = i
			break
		}
	}
	if start < 0 {
		return text
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if sectionRe.MatchString(lines[i]) {
			end = i
			break
		}
	}
	denied := commands.Denied(allowed)
	var body []string
	for _, l := range lines[start+1 : end] {
		if namesDenied(l, denied) {
			continue
		}
		body = append(body, l)
	}
	// Keep the section's trailing blank lines (the separator before the next
	// header) after the new lines.
	tail := 0
	for tail < len(body) && strings.TrimSpace(body[len(body)-1-tail]) == "" {
		tail++
	}
	body = append(body[:len(body)-tail], append(commandLines(allowed, surface), body[len(body)-tail:]...)...)
	out := append([]string{}, lines[:start+1]...)
	out = append(out, body...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n")
}

// namesDenied reports whether the line uses a denied command in command
// position: "`colab <cli name>" or the MCP tool name.
func namesDenied(line string, denied []string) bool {
	for _, d := range denied {
		if strings.Contains(line, "`colab "+commands.CLIName(d)) || strings.Contains(line, commands.ToolName(d)) {
			return true
		}
	}
	return false
}

// commandLines composes the two lines. The allowed commands are written as
// the agent calls them — the MCP tool name on the `mcp` surface, the CLI
// spelling on `cli_wrapper` (what the agent runs, and what the wrapper
// rewrite recognises); the denied ones in the person's words (COMPONENTS
// §8.4 — the daemon wording lock, internal/wording, reads this function), and
// only when there are any: a role with everything gets no "does not use"
// line. Denied commands are deliberately NOT named as commands — the tool is
// gone from the surface, and a spelling the agent could type is exactly the
// noise §10 removes.
func commandLines(allowed []string, surface string) []string {
	names := make([]string, 0, len(allowed))
	for _, c := range allowed {
		if surface == acp.ToolSurfaceMCP {
			names = append(names, "`"+commands.ToolName(c)+"`")
		} else {
			names = append(names, "`colab "+commands.CLIName(c)+"`")
		}
	}
	var out []string
	if surface == acp.ToolSurfaceMCP {
		out = []string{"- 이 역할이 쓸 수 있는 colab 툴: " + strings.Join(names, ", ") + "."}
	} else {
		out = []string{"- 이 역할이 쓸 수 있는 colab 명령: " + strings.Join(names, ", ") + "."}
	}
	denied := commands.Denied(allowed)
	if len(denied) == 0 {
		return out
	}
	words := make([]string, 0, len(denied))
	for _, c := range denied {
		words = append(words, commands.Label(c))
	}
	last := words[len(words)-1]
	return append(out, "- 이 역할은 "+strings.Join(words, " · ")+commands.ObjectParticle(last)+" 쓰지 않는다.")
}
