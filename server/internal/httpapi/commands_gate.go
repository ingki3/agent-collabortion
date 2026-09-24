package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/roles"
)

// FR-1.9.1 (v1.1, K-19): the server's copy of the role gate. The daemon trims
// the MCP tool list and the CLI refuses before sending (exit 3), so under
// normal use this never fires — it is the defence against a runtime that
// reaches the API some other way (a curl in a shell, a wrapper the daemon
// did not build), and it reads the same table as the other two
// (roles.AllowedCommands ← colab-cli.md §2.5).
//
// People are not gated here: the table is about what an AGENT may say to
// the platform, and a person's rights are the membership rules already on
// each handler.

// commandVerbs is colab-cli.md §4's `status` verb for EVERY command
// (TestCommandVerbsComplete holds it to roles.All). The three reads have no
// verb of their own in task_event.schema.json — the feed records platform
// OPERATIONS — and the §2.5 table allows them to every role today, so a
// refused read does not happen; but a refusal with no feed row is a refusal
// nobody can see, and the day §2.5 restricts a read that is exactly what it
// would become (PR #246 리뷰 NN2). `read` is the schema's own verb for a
// read; the command name rides in object_ref and payload.command as for the
// rest. `work propose` (v0.8) has no verb of its own either and the schema
// is closed (a new verb would be a contract change): it rides on `hitl`,
// because what it does is exactly that — put a question to the room's
// people, who decide whether the mission opens (FR-2A.1).
var commandVerbs = map[gen.ColabCommand]string{
	gen.ColabCommandMessagePost: "post_message", gen.ColabCommandStatusSet: "set_status", gen.ColabCommandDecisionRecord: "record_decision",
	gen.ColabCommandLaneDelegate: "delegate", gen.ColabCommandArtifactSubmit: "submit_artifact",
	gen.ColabCommandReviewApprove: "review", gen.ColabCommandReviewReject: "review",
	gen.ColabCommandHitlAsk: "hitl", gen.ColabCommandHitlApproveRequest: "hitl", gen.ColabCommandHitlRequestInfo: "hitl",
	gen.ColabCommandSessionGet: "read", gen.ColabCommandSessionMessages: "read", gen.ColabCommandArtifactGet: "read",
	gen.ColabCommandRoomList: "read", gen.ColabCommandRoomRead: "read",
	gen.ColabCommandWorkPropose: "hitl",
}

// commandAllowed answers nil when the caller may run cmd. For a task token
// whose agent's role does not have cmd it is `403 command_not_allowed` with
// the sentence colab-cli.md §2.5 fixes ("이 역할(<role>)은 <명령> 를 쓸 수
// 없습니다" — the role enum as the screens show it, the command as the agent
// typed it) and, per §4, a `status` row on the attempt's feed with
// rejected_reason = command_not_allowed. The Problem carries the enum name
// in `command` for a client that wants the key rather than the sentence.
func (s *Server) commandAllowed(r *http.Request, cmd gen.ColabCommand) *Problem {
	pr := principalOf(r)
	sc := pr.Task
	if sc == nil {
		return nil
	}
	role, err := s.agentRole(r)
	if err != nil {
		return apperr.Internal(err)
	}
	if roles.Allows(gen.AgentRole(role), cmd) {
		return nil
	}
	if verb, ok := commandVerbs[cmd]; ok {
		// Its own transaction: the handler has not opened one, and the 403 is
		// the answer whether or not the note lands.
		if err := s.writeServerEvent(r.Context(), sc.TaskID, sc.Attempt, "status", verb, string(cmd), "rejected",
			map[string]any{"command": roles.CLIName(cmd), "rejected_reason": "command_not_allowed"}, s.Clock.Now()); err != nil {
			s.Log.Warn("record refused command", "err", err, "task", sc.TaskID, "command", cmd)
		}
	}
	p := apperr.Forbidden("command_not_allowed", fmt.Sprintf("이 역할(%s)은 %s 를 쓸 수 없습니다", role, roles.CLIName(cmd)))
	p.Extra = map[string]any{"command": string(cmd), "role": role}
	return p
}

// agentRole is the calling agent's role, read from the agent row ONCE per
// request and kept on the Principal (PR #246 리뷰 NN3): the gate runs on all
// 13 operations and getCliContext reads the same row, so a handler that
// does both used to pay two round trips. It is deliberately NOT carried in
// the task token — a role changed mid-attempt must take effect on the next
// call, and a token is minted once per attempt.
//
// Only meaningful for a task principal; a person has no agent role.
func (s *Server) agentRole(r *http.Request) (string, error) {
	pr := principalOf(r)
	if pr.Task == nil {
		return "", errors.New("httpapi: agentRole on a non-task principal")
	}
	if pr.agentRole != nil {
		return *pr.agentRole, nil
	}
	var role string
	if err := s.DB.QueryRow(r.Context(), `SELECT role::text FROM agent WHERE id = $1`, pr.Task.AgentID).Scan(&role); err != nil {
		return "", err
	}
	pr.agentRole = &role
	return role, nil
}

// hitlCommand is the ColabCommand a createHitlRequest body stands for
// (colab-cli.md §2.4): `hitl ask` covers question and choice.
func hitlCommand(kind string) gen.ColabCommand {
	switch kind {
	case hitl.KindApproval:
		return gen.ColabCommandHitlApproveRequest
	case hitl.KindInfo:
		return gen.ColabCommandHitlRequestInfo
	}
	return gen.ColabCommandHitlAsk
}
