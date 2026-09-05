package acp

import (
	"github.com/ingki3/agent-collabortion/contracts"
)

// BriefMarkerStart/End delimit the instruction-file brief (harness §10).
const (
	BriefMarkerStart = "<!-- colab:brief:start -->"
	BriefMarkerEnd   = "<!-- colab:brief:end -->"
)

// MetaOptions parametrise Meta().
type MetaOptions struct {
	// Brief is the [1]~[8] text; goes to _meta.systemPrompt.append (append
	// mode only — replacing loses the Claude Code tool conventions).
	Brief string
	// Tools is the profile allow-list; empty = runtime default.
	Tools []string
	// DenyRules are extra permissions.deny rules enforced down to subagents.
	DenyRules []string
	// RawSDKMessages enables `_claude/sdkMessage` (probe/smoke only, §12(c)).
	RawSDKMessages bool
}

// Meta builds the claude_code `_meta` for session/new AND session/load
// (harness §3 — sent every time, never stored by the adapter). Returns nil
// for other runtimes (Hermes drops _meta; E12-09 "Hermes에는 _meta 없음").
func Meta(kind contracts.RuntimeKind, o MetaOptions) map[string]any {
	if kind != contracts.RuntimeClaudeCode {
		return nil
	}
	disallowed := []string{"AskUserQuestion"}
	deny := append([]string{}, o.DenyRules...)
	if len(o.Tools) > 0 {
		allowed := map[string]bool{}
		for _, t := range o.Tools {
			allowed[t] = true
		}
		for _, t := range KnownClaudeTools {
			if t == "AskUserQuestion" || allowed[t] {
				continue
			}
			disallowed = append(disallowed, t)
			deny = append(deny, t)
		}
	}
	options := map[string]any{
		"settingSources":  []string{},
		"strictMcpConfig": true,
		"disallowedTools": disallowed,
		"settings":        map[string]any{"permissions": map[string]any{"deny": deny}},
		"permissionMode":  "default",
	}
	cc := map[string]any{"options": options}
	if o.RawSDKMessages {
		cc["emitRawSDKMessages"] = true
	}
	return map[string]any{
		"systemPrompt": map[string]any{"append": o.Brief},
		"claudeCode":   cc,
	}
}
