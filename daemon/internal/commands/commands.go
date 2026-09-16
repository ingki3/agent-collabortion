// Package commands is the daemon's copy of the colab command set (colab-cli.md
// §2 · §2.5 · §3) for FR-1.9.1 / K-19 (harness §10 v0.8.10): the bundle's
// `task.allowed_commands` (daemon-protocol §4.1 v0.8.2) is a role's subset of
// this set, and the daemon has to put the SAME list in three places —
//
//   - the colab MCP server's argv (`colab mcp serve --allow a,b,…`), so the
//     server registers only those tools (mcp);
//   - the hermes wrapper's environment (`COLAB_ALLOWED_COMMANDS=a,b,…`), so the
//     CLI refuses the rest with exit 3 (cli_wrapper);
//   - brief [2], which names the allowed commands and, in one line, what this
//     role does not use — an instruction that names a command the surface no
//     longer has makes the agent try it and get refused (§10).
//
// An empty list means "everything" (an older server, or lead/custom) and
// changes nothing: no flag, no variable, no brief lines.
//
// The table below is not authoritative — the server decides the list from
// the role (server/internal/roles) and the CLI enforces it. What lives here is
// the NAME MAPPING (enum → CLI spelling → person's word), and commands_test.go
// checks the set against the contract enum (openapi ColabCommand), the §3
// tool-name list and the web's COMMAND_LABEL table (13/13, read from
// web/lib/wording.ts at test time), so a 14th command cannot appear on one
// side only.
package commands

import (
	"strings"
	"unicode/utf8"
)

// EnvVar is what the hermes wrapper exports (harness §10 v0.8.10,
// colab-cli.md §2.5): the comma-joined allowed list.
const EnvVar = "COLAB_ALLOWED_COMMANDS"

// AllowFlag is the `colab mcp serve` flag that carries the list (harness §10
// v0.8.10). The CLI half is T-C7; a CLI without the flag ignores the extra
// argv today (`colab mcp serve` checks only args[1]).
const AllowFlag = "--allow"

// all is every ColabCommand in colab-cli.md §2 order — the order the brief
// lists them in, and the order Denied is emitted in.
var all = []string{
	"session_get", "session_messages", "message_post", "status_set", "decision_record",
	"lane_delegate", "artifact_submit", "artifact_get", "review_approve", "review_reject",
	"hitl_ask", "hitl_approve_request", "hitl_request_info",
}

// cliNames is the command as the agent types it (colab-cli.md §2): the two
// hyphenated HITL sub-commands, else the enum with its underscore as a space.
var cliNames = map[string]string{
	"hitl_approve_request": "hitl approve-request",
	"hitl_request_info":    "hitl request-info",
}

// labels is the person's word for each command (COMPONENTS §8.4 — the same
// table the web shows on the role card, web/lib/wording.ts COMMAND_LABEL).
// The brief's "does not use" line speaks in these, not in command names: the
// tool is gone from the surface, so the agent needs the idea, not a spelling
// it would then try.
var labels = map[string]string{
	"session_get":          "세션 읽기",
	"session_messages":     "메시지 읽기",
	"artifact_get":         "산출물 읽기",
	"message_post":         "메시지 게시",
	"status_set":           "상태 알리기",
	"decision_record":      "결정 기록",
	"lane_delegate":        "위임",
	"artifact_submit":      "산출물 제출",
	"review_approve":       "검토 승인",
	"review_reject":        "검토 반려",
	"hitl_ask":             "사람에게 질문",
	"hitl_approve_request": "완료 승인 요청",
	"hitl_request_info":    "사람에게 정보 요청",
}

// known is `all` as a set — the ONE definition of "a command this daemon
// knows". `labels` is a rendering of that set, not its definition (PR #250
// 리뷰 NN3): deleting a label must make Label fall back to the CLI spelling,
// never make the command unknown.
var known = func() map[string]bool {
	m := make(map[string]bool, len(all))
	for _, c := range all {
		m[c] = true
	}
	return m
}()

// All is the closed set in §2 order.
func All() []string { return append([]string(nil), all...) }

// Known reports whether cmd is in All().
func Known(cmd string) bool { return known[cmd] }

// List is the wire form shared by the flag and the variable: comma-joined,
// in the order the bundle gave, "" for an empty list.
func List(allowed []string) string { return strings.Join(allowed, ",") }

// Args is what goes after `mcp serve`: nil for an empty list (an older server
// or a role with everything — no flag, so an older CLI sees the P1 argv), else
// {"--allow", "a,b,…"}.
func Args(allowed []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	return []string{AllowFlag, List(allowed)}
}

// EnvEntry is the wrapper's export as one "K=V" entry, "" for an empty list.
func EnvEntry(allowed []string) string {
	if len(allowed) == 0 {
		return ""
	}
	return EnvVar + "=" + List(allowed)
}

// CLIName is the §2 spelling: `lane delegate` for `lane_delegate`, `hitl
// approve-request` for `hitl_approve_request`. A name this daemon does not
// know (a newer server) gets the generic rule.
func CLIName(cmd string) string {
	if n, ok := cliNames[cmd]; ok {
		return n
	}
	return strings.ReplaceAll(cmd, "_", " ")
}

// ToolName is the §3 MCP tool name: `colab_` + enum.
func ToolName(cmd string) string { return "colab_" + cmd }

// Label is the person's word; an unknown command falls back to its CLI
// spelling so the line is never empty.
func Label(cmd string) string {
	if l, ok := labels[cmd]; ok {
		return l
	}
	return CLIName(cmd)
}

// Denied is All minus allowed, in §2 order. nil when allowed is empty: an
// empty list means everything, not nothing (daemon-protocol §4.1 v0.8.2).
func Denied(allowed []string) []string {
	if len(allowed) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, c := range allowed {
		have[c] = true
	}
	var out []string
	for _, c := range all {
		if !have[c] {
			out = append(out, c)
		}
	}
	return out
}

// ObjectParticle picks 을/를 for the word before it (Hangul final consonant
// → 을). Non-Hangul endings get 을 — the labels are all Hangul, so this is
// only for a newer command whose label fell back to its CLI spelling.
func ObjectParticle(word string) string {
	r, _ := utf8.DecodeLastRuneInString(word)
	if r >= 0xAC00 && r <= 0xD7A3 {
		if (r-0xAC00)%28 == 0 {
			return "를"
		}
		return "을"
	}
	return "을"
}
