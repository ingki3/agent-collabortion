// Package mcp is a minimal stdio MCP server (JSON-RPC 2.0, newline-delimited)
// exposing the colab commands as tools with the same names as the command
// paths joined by underscores (contracts/colab-cli.md §3):
// colab_session_get · colab_session_messages · colab_message_post ·
// colab_status_set · colab_lane_delegate · colab_decision_record ·
// colab_artifact_submit · colab_artifact_get · colab_review_approve ·
// colab_review_reject · colab_hitl_ask · colab_hitl_approve_request ·
// colab_hitl_request_info.
//
// Every tool calls the same internal/colab action the CLI subcommand calls,
// so a tool and its command produce byte-identical JSON.
//
// No SDK dependency: the daemon injects this server as the only MCP server
// (harness.md §3, strictMcpConfig) and the surface is a handful of tools, so
// a hand-rolled JSON-RPC loop keeps the CLI a single static binary.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
	"github.com/ingki3/agent-collabortion/cli/internal/colab"
)

// ProtocolVersion is the MCP revision this server negotiates.
const ProtocolVersion = "2025-06-18"

// ServerName is reported in initialize.
const ServerName = "colab"

// Tool is one tools/list entry.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Tools is the tool table (order is stable for tools/list): P1 reads and
// message post, then the P2 write commands of colab-cli.md v0.4 §2.2·2.3,
// then the P3 HITL commands of v0.5 §2.4.
var Tools = []Tool{
	{
		Name:        "colab_session_get",
		Description: "Read this session: goal, acceptance_criteria, completion_progress, participants (roster with derived status), isolation, director. Same as `colab session get`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session":{"type":"string","description":"session id (default: the task's session)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_session_messages",
		Description: "Read session messages (author, body, thread, time). Use when the history in your prompt is truncated. Same as `colab session messages [--since --limit --thread]`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session":{"type":"string"},"since":{"type":"string","description":"only messages newer than this cursor / message id (sent as the after= query parameter)"},"limit":{"type":"integer","minimum":1,"maximum":200,"description":"1..200; omit for the server default (50)"},"thread":{"type":"string","description":"thread root message id: returns root + replies"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_message_post",
		Description: "Post a message to the session. Routing is server-side: an agent message triggers other agents ONLY when it mentions them (`mention`); the delegator's mention is suppressed until rejoin. Returns message_id, triggered[], suppressed[]. Same as `colab message post --body [--reply-to --mention]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["body"],"properties":{"body":{"type":"string","minLength":1,"description":"markdown text"},"reply_to":{"type":"string","description":"parent message id (thread)"},"mention":{"type":"array","items":{"type":"string"},"description":"agent names to mention, e.g. [\"@Reviewer\"]"},"session":{"type":"string"},"idempotency_key":{"type":"string","description":"reuse a previous result's idempotency_key to retry the same post after a network error (default: UUIDv5 of task:<task_id>:<seq>)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_status_set",
		Description: "Set this task's status. `blocked` is how you ask YOUR DELEGATOR a question: the server marks the lane blocked, posts the question card on the lane thread and wakes the delegator (Director inbox when there is none) — `note` is the question and is required. `done` declares this turn's work finished. The result's `turn_end_required: true` means you must end your turn immediately. Same as `colab status set working|blocked|done [--note]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["status"],"properties":{"status":{"type":"string","enum":["working","blocked","done"]},"note":{"type":"string","description":"feed note; REQUIRED for blocked — it is the question the delegator answers"},"task":{"type":"string","description":"task id (default: this task)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_lane_delegate",
		Description: "Delegate work to another agent: always creates a NEW lane whose delegated_from_task_id is this task (the rejoin group). The target must ALREADY be a session participant — you cannot create one. A non-participant fails with code `not_participant`; ask the Director to add them with colab_hitl_ask. Same as `colab lane delegate --agent --brief`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["agent","brief"],"properties":{"agent":{"type":"string","description":"target participant name, e.g. \"Reviewer\" or \"@Reviewer\""},"brief":{"type":"string","minLength":1,"description":"the delegation brief; goes into the delegate's turn prompt verbatim"},"depends_on":{"type":"array","items":{"type":"string"},"description":"lane ids this lane waits for (v1 stores them; DAG execution is v1.1)"},"profile":{"type":"string","description":"profile name (default: the participant's registered profile)"},"session":{"type":"string"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_decision_record",
		Description: "Record a decision (source=agent) so it appears in the session's decision log and in later turn briefs. The record is exactly two fields: summary (what was decided) and rationale (why). Same as `colab decision record --summary --rationale`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string","minLength":1,"description":"what was decided"},"rationale":{"type":"string","description":"why"},"session":{"type":"string"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_artifact_submit",
		Description: "Submit a file as a session artifact. Re-submitting the same name creates version+1. This is the input to the `artifact_submitted` completion condition — the result's completion_progress says whether it is now met. Max 50 MB. Same as `colab artifact submit --type --file`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["type","file"],"properties":{"type":{"type":"string","minLength":1,"description":"open set: file · diff · branch · doc · report …"},"file":{"type":"string","description":"path to the file to upload (max 50 MB)"},"name":{"type":"string","description":"artifact name; defaults to the file's base name"},"description":{"type":"string"},"session":{"type":"string"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_artifact_get",
		Description: "Read an artifact's metadata, and with `out` also download its body to that path. This is the ONLY way to read another lane's work — worktree paths are never exposed. Same as `colab artifact get <id> [--out]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["artifact"],"properties":{"artifact":{"type":"string","description":"artifact id"},"out":{"type":"string","description":"write the body here (a file path, or an existing directory)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_review_approve",
		Description: "Approve an artifact. This is the input to the `agent_approval` completion condition. If the condition designates a different reviewer the call fails with code `not_reviewer` and nothing is stored. Same as `colab review approve --artifact [--note]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["artifact"],"properties":{"artifact":{"type":"string","description":"artifact id"},"note":{"type":"string","description":"comments recorded with the review"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_review_reject",
		Description: "Reject an artifact. `reason` is required and the server posts it as a reply on the artifact's lane thread, which re-enters the submitting lane, and records a decision. Same as `colab review reject --artifact --reason`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["artifact","reason"],"properties":{"artifact":{"type":"string","description":"artifact id"},"reason":{"type":"string","minLength":1,"description":"why it is rejected; posted on the artifact thread"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_hitl_ask",
		Description: "Ask the DIRECTOR (a human) a question and STOP. Use this when you cannot proceed without a human decision — not for questions your delegator can answer (that is colab_status_set with status=blocked). `default` is REQUIRED: it is the answer you propose so the human can just accept it. Pass `choices` (2+) to make it a multiple-choice question, and then `default` must be one of them. The result is `turn_end_required: true` — register the request and END YOUR TURN immediately; the answer arrives as a new turn. A task can have only ONE open request: a second call fails with code `hitl_already_open`. Same as `colab hitl ask --question --default [--choices --context]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["question","default"],"properties":{"question":{"type":"string","minLength":1,"description":"the question for the Director"},"default":{"type":"string","minLength":1,"description":"REQUIRED — the answer you propose (FR-5.1); with choices it must be one of them"},"choices":{"type":"array","minItems":2,"items":{"type":"string"},"description":"2+ options; makes this a choice-type request"},"context":{"type":"string","description":"background the human needs to answer"},"task":{"type":"string","description":"task id (default: this task)"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_hitl_approve_request",
		Description: "Ask a human to APPROVE something and STOP — for an irreversible or out-of-scope step you must not take on your own. There is no default and it NEVER auto-proceeds, even after the due date passes (FR-5.4): without an answer the work stays stopped. The result is `turn_end_required: true` — END YOUR TURN. A rejection is a normal outcome and comes back with its reason in your next turn. One open request per task; a second call fails with code `hitl_already_open`. Same as `colab hitl approve-request --summary [--artifact]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string","minLength":1,"description":"what you are asking approval for"},"artifact":{"type":"string","description":"artifact id this approval is about"},"task":{"type":"string","description":"task id (default: this task)"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_hitl_request_info",
		Description: "Ask a human for INFORMATION you cannot obtain yourself (a credential holder's answer, an offline document, a fact only they know) and STOP. No default, and it never auto-proceeds. The result is `turn_end_required: true` — END YOUR TURN. One open request per task; a second call fails with code `hitl_already_open`. Same as `colab hitl request-info --what [--why]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["what"],"properties":{"what":{"type":"string","minLength":1,"description":"the information you need"},"why":{"type":"string","description":"why you need it"},"task":{"type":"string","description":"task id (default: this task)"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Server handles one stdio connection.
type Server struct {
	c       *client.Client
	version string
	out     io.Writer
	mu      sync.Mutex
}

// Serve runs the JSON-RPC loop until in is closed or ctx is done. Every line
// on in is one message; every response is one line on out.
func Serve(ctx context.Context, c *client.Client, in io.Reader, out io.Writer, version string) error {
	s := &Server{c: c, version: version, out: out}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 16<<20)
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.handleLine(ctx, []byte(line))
	}
	return sc.Err()
}

func (s *Server) handleLine(ctx context.Context, line []byte) {
	// Batches are not part of the 2025-06-18 revision; treat as invalid.
	if line[0] == '[' {
		s.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeInvalidRequest, Message: "batch requests are not supported"}})
		return
	}
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.write(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "parse error: " + err.Error()}})
		return
	}
	if len(req.ID) == 0 || string(req.ID) == "null" {
		// Notification: nothing to answer.
		return
	}
	res, rerr := s.Handle(ctx, req.Method, req.Params)
	if rerr != nil {
		s.write(response{JSONRPC: "2.0", ID: req.ID, Error: rerr})
		return
	}
	s.write(response{JSONRPC: "2.0", ID: req.ID, Result: res})
}

func (s *Server) write(r response) {
	b, err := json.Marshal(r)
	if err != nil {
		b = []byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"encode: ` + err.Error() + `"}}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// One write per message: stdio consumers read line by line.
	s.out.Write(append(b, '\n'))
}

// Handle dispatches one request method. Exposed for tests.
func (s *Server) Handle(ctx context.Context, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": ServerName, "version": s.version},
			"instructions":    "Tools mirror the colab CLI (contracts/colab-cli.md). Post a message with colab_message_post; mention agents to trigger them.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": Tools}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params: " + err.Error()}
		}
		return s.callTool(ctx, p.Name, p.Arguments)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + method}
}

// callTool runs the tool. Command failures (exit 2..5) are tool results with
// isError=true carrying the same error JSON the CLI prints — not protocol
// errors — so the model can read `code`/`detail` and react.
func (s *Server) callTool(ctx context.Context, name string, args json.RawMessage) (any, *rpcError) {
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var (
		v   any
		err error
	)
	switch name {
	case "colab_session_get":
		var a colab.SessionGetArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.SessionGet(ctx, s.c, a)
	case "colab_session_messages":
		var a colab.SessionMessagesArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.SessionMessages(ctx, s.c, a)
	case "colab_message_post":
		var a struct {
			colab.MessagePostArgs
			MentionRaw json.RawMessage `json:"mention,omitempty"`
		}
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		a.Mention = parseMention(a.MentionRaw)
		v, err = colab.MessagePost(ctx, s.c, a.MessagePostArgs)
	case "colab_status_set":
		var a colab.StatusSetArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.StatusSet(ctx, s.c, a)
	case "colab_lane_delegate":
		var a struct {
			colab.LaneDelegateArgs
			DependsOnRaw json.RawMessage `json:"depends_on,omitempty"`
		}
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		a.DependsOn = parseMention(a.DependsOnRaw) // same array-or-CSV shape
		v, err = colab.LaneDelegate(ctx, s.c, a.LaneDelegateArgs)
	case "colab_decision_record":
		var a colab.DecisionRecordArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.DecisionRecord(ctx, s.c, a)
	case "colab_artifact_submit":
		var a colab.ArtifactSubmitArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.ArtifactSubmit(ctx, s.c, a)
	case "colab_artifact_get":
		var a colab.ArtifactGetArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.ArtifactGet(ctx, s.c, a)
	case "colab_review_approve":
		var a colab.ReviewArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.ReviewApprove(ctx, s.c, a)
	case "colab_review_reject":
		var a colab.ReviewArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.ReviewReject(ctx, s.c, a)
	case "colab_hitl_ask":
		var a struct {
			colab.HitlAskArgs
			ChoicesRaw json.RawMessage `json:"choices,omitempty"`
		}
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		a.Choices = parseMention(a.ChoicesRaw) // same array-or-CSV shape
		v, err = colab.HitlAsk(ctx, s.c, a.HitlAskArgs)
	case "colab_hitl_approve_request":
		var a colab.HitlApproveRequestArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.HitlApproveRequest(ctx, s.c, a)
	case "colab_hitl_request_info":
		var a struct {
			colab.HitlRequestInfoArgs
			Question string `json:"question,omitempty"` // alias of what, as on the CLI
		}
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		if a.What == "" {
			a.What = a.Question
		}
		v, err = colab.HitlRequestInfo(ctx, s.c, a.HitlRequestInfoArgs)
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q", name)}
	}
	if err != nil {
		ej := colab.ErrorJSON(err)
		return map[string]any{
			"isError":           true,
			"content":           []map[string]any{{"type": "text", "text": string(colab.MarshalIndent(ej))}},
			"structuredContent": ej,
		}, nil
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(colab.MarshalIndent(v))}},
		"structuredContent": v,
	}, nil
}

// parseMention accepts ["@A","@B"], "@A,@B" or null. colab_lane_delegate's
// depends_on takes the same shape.
func parseMention(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil && one != "" {
		return strings.Split(one, ",")
	}
	return nil
}
