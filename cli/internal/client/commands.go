package client

import (
	"context"
	"fmt"
	"strings"
)

// Command is a colab command name as openapi.yaml `ColabCommand` spells it
// (colab-cli.md §2.5): the CLI path joined by underscores, which is also the
// MCP tool name without its `colab_` prefix (§3).
type Command string

// The thirteen ColabCommand values, in colab-cli.md §2 order. commands_test.go
// checks this list against the openapi.yaml enum.
const (
	CmdSessionGet         Command = "session_get"
	CmdSessionMessages    Command = "session_messages"
	CmdArtifactGet        Command = "artifact_get"
	CmdMessagePost        Command = "message_post"
	CmdStatusSet          Command = "status_set"
	CmdDecisionRecord     Command = "decision_record"
	CmdLaneDelegate       Command = "lane_delegate"
	CmdArtifactSubmit     Command = "artifact_submit"
	CmdReviewApprove      Command = "review_approve"
	CmdReviewReject       Command = "review_reject"
	CmdHitlAsk            Command = "hitl_ask"
	CmdHitlApproveRequest Command = "hitl_approve_request"
	CmdHitlRequestInfo    Command = "hitl_request_info"
)

// AllCommands is the closed ColabCommand set.
var AllCommands = []Command{
	CmdSessionGet, CmdSessionMessages, CmdArtifactGet, CmdMessagePost, CmdStatusSet, CmdDecisionRecord,
	CmdLaneDelegate, CmdArtifactSubmit, CmdReviewApprove, CmdReviewReject,
	CmdHitlAsk, CmdHitlApproveRequest, CmdHitlRequestInfo,
}

// IsCommand reports whether s is one of AllCommands.
func IsCommand(s string) bool {
	for _, c := range AllCommands {
		if string(c) == s {
			return true
		}
	}
	return false
}

// CLIName is the command as the agent typed it, for the refusal sentence:
// `lane delegate` for lane_delegate. The two HITL sub-commands are the
// hyphenated ones (colab-cli.md §2.4). Same rule as the server's
// roles.CLIName, so the CLI's exit 3 and the server's 403 read alike.
func (c Command) CLIName() string {
	switch c {
	case CmdHitlApproveRequest:
		return "hitl approve-request"
	case CmdHitlRequestInfo:
		return "hitl request-info"
	}
	return strings.ReplaceAll(string(c), "_", " ")
}

// ToolName is the MCP tool for the command (colab-cli.md §3).
func (c Command) ToolName() string { return "colab_" + string(c) }

// SplitCommands parses the comma-separated form of COLAB_ALLOWED_COMMANDS
// and `--allow`: trimmed, empties dropped, order kept, never nil.
func SplitCommands(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ErrCodeCommandNotAllowed is the exit 3 code of a command outside the
// role's subset (colab-cli.md §2.5), the same code the server's 403 carries.
const ErrCodeCommandNotAllowed = "command_not_allowed"

// NotAllowedSentence is the human sentence of the refusal — colab-cli.md
// §2.5 "이 역할(<role>)은 <명령>을 쓸 수 없습니다", spelled exactly as the
// server's 403 spells it (server/internal/httpapi/commands_gate.go) so an
// agent sees one sentence whichever layer refused. When the role is not
// known (a wrapper handed the list over and no context was fetched) the
// parenthesis is dropped rather than filled with a guess.
func NotAllowedSentence(role string, cmd Command) string {
	if role == "" {
		return fmt.Sprintf("이 역할은 %s 를 쓸 수 없습니다", cmd.CLIName())
	}
	return fmt.Sprintf(notAllowedFormat, role, cmd.CLIName())
}

// notAllowedFormat is byte-for-byte the server's fmt string
// (commands_gate_test in this package reads it out of the server source).
const notAllowedFormat = "이 역할(%s)은 %s 를 쓸 수 없습니다"

// AllowedCommands is the list the gate reads, with where it came from:
// Config.AllowedCommands (COLAB_ALLOWED_COMMANDS / --allow) when given,
// otherwise getCliContext.allowed_commands — fetched once and cached like
// every other context read. nil means "no list": a pre-v1.1 server, or a
// list that was given empty — both allow everything.
func (c *Client) AllowedCommands(ctx context.Context) ([]string, error) {
	if c.cfg.AllowedCommands != nil {
		return nonEmpty(c.cfg.AllowedCommands), nil
	}
	cc, err := c.Context(ctx)
	if err != nil {
		return nil, err
	}
	return nonEmpty(cc.AllowedCommands), nil
}

func nonEmpty(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	return list
}

// Allow is the K-19 gate (colab-cli.md §2.5): nil when cmd may run, else the
// exit 3 `command_not_allowed` error — built without any request to the
// server for cmd itself. With a wrapper-given list nothing is sent at all;
// otherwise the one cached /cli/context read is the only traffic. A list
// that is absent or empty allows every command.
func (c *Client) Allow(ctx context.Context, cmd Command) error {
	list, err := c.AllowedCommands(ctx)
	if err != nil {
		return err
	}
	if list == nil {
		return nil
	}
	for _, a := range list {
		if a == string(cmd) {
			return nil
		}
	}
	role := ""
	if cc := c.ctx; cc != nil { // only if already fetched — never a round trip just for the sentence
		role = cc.OwnRole()
	}
	return NotAllowed(role, cmd, list)
}

// NotAllowed is the exit 3 `command_not_allowed` error: the sentence in
// Detail and, for --json, role · command · allowed as top-level keys of the
// error object.
func NotAllowed(role string, cmd Command, allowed []string) *Error {
	if allowed == nil {
		allowed = []string{}
	}
	return &Error{
		Exit: ExitRefused, Code: ErrCodeCommandNotAllowed,
		Title:  "command not allowed for this role",
		Detail: NotAllowedSentence(role, cmd),
		Extra:  map[string]any{"role": role, "command": string(cmd), "allowed": allowed},
	}
}

// OwnRole is the calling agent's role as the roster reports it: CliContext
// has no top-level role field, but the agent is one of its own session's
// participants and each participant row carries `role`.
func (cc *CliContext) OwnRole() string {
	for _, p := range cc.Participants {
		if p.AgentID == cc.AgentID {
			return p.Role
		}
	}
	return ""
}
