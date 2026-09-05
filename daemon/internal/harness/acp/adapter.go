package acp

import "github.com/ingki3/agent-collabortion/contracts"

// AdapterPin is the claude-agent-acp version this package was verified
// against (harness §1, G1 F1). Everything below that depends on adapter
// behaviour lives in this file so a pin bump reviews it in one place;
// TestKnownClaudeToolsPinnedWithAdapter fails until KnownClaudeToolsPin is
// re-verified against the new adapter.
const AdapterPin = contracts.ClaudeAgentACPPin

// KnownClaudeToolsPin is the adapter version KnownClaudeTools was captured
// from (raw system/init.tools, spike 1b). Must equal AdapterPin.
const KnownClaudeToolsPin = "0.74.0"

// KnownClaudeTools is the Claude Code tool set the profile allow-list is
// diffed against to build disallowedTools + permissions.deny (harness §3,
// §8 "disallowedTools 도출"). Tools missing from the table are not blocked
// by the diff — the §4 runtime permission check is the second line.
var KnownClaudeTools = []string{
	"Agent", "Task", "Bash", "Edit", "Write", "Read", "Glob", "Grep",
	"MultiEdit", "NotebookEdit", "WebFetch", "WebSearch", "TodoWrite",
	"BashOutput", "KillShell", "Skill", "ExitPlanMode", "AskUserQuestion",
}

// ModelAliases maps the short model aliases Claude Code accepts in
// session/set_config_option{model} to the ids the same adapter reports in
// _meta.quota.model_usage (spike 1b: "haiku" → claude-haiku-4-5-20251001).
// ModelMatches uses exact comparison plus this table — never substrings —
// so model_drift (harness §7, 1b E1) is not masked by loose matching.
var ModelAliases = map[string][]string{
	"haiku":  {"claude-haiku-4-5", "claude-haiku-4-5-20251001"},
	"sonnet": {"claude-sonnet-5", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929"},
	"opus":   {"claude-opus-5", "claude-opus-4-1", "claude-opus-4-1-20250805"},
	"fable":  {"claude-fable-5-1"},
}
