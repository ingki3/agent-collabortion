package httpapi

import (
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

// commandVerbs is colab-cli.md §4's `status` verb for each command that has
// one. The three reads have no verb in task_event.schema.json (the feed
// records platform OPERATIONS) and the §2.5 table allows them to every role,
// so a refused read cannot happen; if it ever did, it would answer 403
// without a feed row.
var commandVerbs = map[gen.ColabCommand]string{
	gen.MessagePost: "post_message", gen.StatusSet: "set_status", gen.DecisionRecord: "record_decision",
	gen.LaneDelegate: "delegate", gen.ArtifactSubmit: "submit_artifact",
	gen.ReviewApprove: "review", gen.ReviewReject: "review",
	gen.HitlAsk: "hitl", gen.HitlApproveRequest: "hitl", gen.HitlRequestInfo: "hitl",
}

// commandAllowed answers nil when the caller may run cmd. For a task token
// whose agent's role does not have cmd it is `403 command_not_allowed` with
// the sentence colab-cli.md §2.5 fixes ("이 역할(<role>)은 <명령> 를 쓸 수
// 없습니다" — the role enum as the screens show it, the command as the agent
// typed it) and, per §4, a `status` row on the attempt's feed with
// rejected_reason = command_not_allowed. The Problem carries the enum name
// in `command` for a client that wants the key rather than the sentence.
func (s *Server) commandAllowed(r *http.Request, cmd gen.ColabCommand) *Problem {
	sc := principalOf(r).Task
	if sc == nil {
		return nil
	}
	var role string
	if err := s.DB.QueryRow(r.Context(), `SELECT role::text FROM agent WHERE id = $1`, sc.AgentID).Scan(&role); err != nil {
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

// hitlCommand is the ColabCommand a createHitlRequest body stands for
// (colab-cli.md §2.4): `hitl ask` covers question and choice.
func hitlCommand(kind string) gen.ColabCommand {
	switch kind {
	case hitl.KindApproval:
		return gen.HitlApproveRequest
	case hitl.KindInfo:
		return gen.HitlRequestInfo
	}
	return gen.HitlAsk
}
