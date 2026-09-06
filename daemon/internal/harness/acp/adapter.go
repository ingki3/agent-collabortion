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

// supportedOptions is the harness §9 `supported_options` table: the profile
// options a runtime accepts, and their allowed values, keyed by
// (kind, adapter_version).
//
// It is a TABLE and not a measurement because it cannot be measured: §3 says
// the adapter passes `claudeCode.options` to the SDK without validating them,
// so an unknown key and a supported one are indistinguishable on the wire —
// a typo is silently ignored. So the daemon advertises what it knows for the
// version it is pinned to, and nothing for a version it has not seen.
//
// The distinction §9 draws matters to the web (backlog D-5): an EMPTY map
// means "no advertisement", not "no options". S10 leaves option editing
// disabled when the map is empty, so filling it in for a version we have not
// verified would be worse than leaving it out — the user would get a control
// that writes a value the runtime discards.
var supportedOptions = map[contracts.RuntimeKind]map[string]map[string][]string{
	contracts.RuntimeClaudeCode: {
		// 0.74.0 — G1 F1 pin. `effort` is the one profile option the adapter
		// forwards to the SDK with a closed value set.
		"0.74.0": {"effort": {"low", "medium", "high", "xhigh"}},
	},
	// Hermes: empty in v1 (§9). `hermes acp` drops `_meta` entirely (§3), so
	// there is no profile-option channel at all yet.
	contracts.RuntimeHermes: {},
}

// SupportedOptions returns the §9 advertisement for one runtime, or nil when
// the daemon has not verified that (kind, adapter_version) pair.
func SupportedOptions(kind contracts.RuntimeKind, adapterVersion string) map[string][]string {
	byVersion, ok := supportedOptions[kind]
	if !ok || adapterVersion == "" {
		return nil
	}
	opts, ok := byVersion[adapterVersion]
	if !ok {
		return nil
	}
	out := make(map[string][]string, len(opts))
	for k, v := range opts {
		out[k] = append([]string(nil), v...)
	}
	return out
}
