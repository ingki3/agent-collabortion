// Package mcp is a minimal stdio MCP server (JSON-RPC 2.0, newline-delimited)
// exposing the colab commands as tools with the same names as the command
// paths joined by underscores (contracts/colab-cli.md §3):
// colab_room_get · colab_room_messages · colab_message_post ·
// colab_status_set · colab_lane_delegate · colab_decision_record ·
// colab_artifact_submit · colab_artifact_get · colab_review_approve ·
// colab_review_reject · colab_hitl_ask · colab_hitl_approve_request ·
// colab_hitl_request_info · (v0.8 §2.4a) colab_room_list · colab_room_read ·
// colab_work_propose. (v0.9 R4 removed the old session-named read tools.)
//
// Every tool calls the same internal/colab action the CLI subcommand calls,
// so a tool and its command produce byte-identical JSON.
//
// `colab mcp serve --allow <cmd,…>` (colab-cli.md v0.6 §2.5·§3, harness §10)
// registers only the listed commands' tools — the daemon passes the bundle's
// task.allowed_commands, the role's subset — so a model never sees a tool its
// role cannot use. Without --allow every tool is registered and the actions'
// own gate (client.Allow, fed by getCliContext.allowed_commands) still
// refuses with command_not_allowed.
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

// Tools is the tool table (order is stable for tools/list): the room reads
// and message post, then the P2 write commands of colab-cli.md v0.4 §2.2·2.3,
// then the P3 HITL commands of v0.5 §2.4.
var Tools = []Tool{
	{
		Name:        "colab_room_get",
		Description: "Read this room: {room, work, participants}. `room` is the room itself (name, description, isolation); `work` is the mission this turn belongs to — goal, acceptance_criteria, completion_progress (which conditions are met and whose turn it is), director — or null outside a mission; `participants` is the roster (people and agents: name, role description, derived status). Same as `colab room get [--room]`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"room":{"type":"string","description":"room id (default: this turn's room)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_room_messages",
		Description: "Read this room's messages (author, body, parent_id, time). Thread replies are included (parent_id = the thread root); set `top_only` for the main timeline alone. Use when the history in your prompt is truncated; `work` keeps one mission's messages. Same as `colab room messages [--since --limit --thread --work --top-only]`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"room":{"type":"string","description":"room id (default: this turn's room)"},"since":{"type":"string","description":"only messages newer than this cursor / message id (sent as the after= query parameter)"},"limit":{"type":"integer","minimum":1,"maximum":200,"description":"1..200; omit for the server default (50)"},"thread":{"type":"string","description":"thread root message id: returns root + replies"},"work":{"type":"string","description":"mission id: only that mission's messages"},"top_only":{"type":"boolean","description":"main timeline only; thread replies are included by default"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_message_post",
		Description: "Post a message to the room. Routing is server-side: an agent message triggers other agents ONLY when it mentions them (`mention`); the delegator's mention is suppressed until rejoin. Returns message_id, triggered[], suppressed[]. When your turn was asked in a thread, the reply goes to that thread by default; set `top_level` only when it belongs on the main timeline. Same as `colab message post --body [--reply-to | --top-level] [--mention]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["body"],"properties":{"body":{"type":"string","minLength":1,"description":"markdown text"},"reply_to":{"type":"string","description":"parent message id (thread); default: the thread this turn was asked in"},"top_level":{"type":"boolean","description":"post to the main timeline even when this turn was asked in a thread; not with reply_to"},"mention":{"type":"array","items":{"type":"string"},"description":"agent names to mention, e.g. [\"@Reviewer\"]"},"session":{"type":"string"},"idempotency_key":{"type":"string","description":"reuse a previous result's idempotency_key to retry the same post after a network error (default: UUIDv5 of task:<task_id>:<seq>)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_status_set",
		Description: "Set this task's status. `blocked` is how you ask YOUR DELEGATOR a question: the server marks the lane blocked, posts the question card on the lane thread and wakes the delegator (Director inbox when there is none) — `note` is the question and is required. `done` declares this turn's work finished. The result's `turn_end_required: true` means you must end your turn immediately. Same as `colab status set working|blocked|done [--note]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["status"],"properties":{"status":{"type":"string","enum":["working","blocked","done"]},"note":{"type":"string","description":"feed note; REQUIRED for blocked — it is the question the delegator answers"},"task":{"type":"string","description":"task id (default: this task)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_lane_delegate",
		Description: "Delegate work to another agent: always creates a NEW lane whose delegated_from_task_id is this task (the rejoin group). The target must ALREADY be a room participant — you cannot create one. A non-participant fails with code `not_participant`; ask the Director to add them with colab_hitl_ask. Same as `colab lane delegate --agent --brief`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["agent","brief"],"properties":{"agent":{"type":"string","description":"target participant name, e.g. \"Reviewer\" or \"@Reviewer\""},"brief":{"type":"string","minLength":1,"description":"the delegation brief; goes into the delegate's turn prompt verbatim"},"depends_on":{"type":"array","items":{"type":"string"},"description":"lane ids this lane waits for (v1 stores them; DAG execution is v1.1)"},"profile":{"type":"string","description":"profile name (default: the participant's registered profile)"},"session":{"type":"string"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_decision_record",
		Description: "Record a decision (source=agent) so it appears in the room's decision log and in later turn briefs. The record is exactly two fields: summary (what was decided) and rationale (why). Same as `colab decision record --summary --rationale`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string","minLength":1,"description":"what was decided"},"rationale":{"type":"string","description":"why"},"session":{"type":"string"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_artifact_submit",
		Description: "Submit a file as a room artifact. Re-submitting the same NAME creates version+1. This is the input to the `artifact_submitted` completion condition — the result's completion_progress says whether it is now met. Max 50 MB. With `type: \"diff\"` you may omit `file`: the CLI then builds one unified diff of YOUR OWN workdir (commits since `base`, staged and unstaged changes) — that diff is how another lane reads your work, since worktree paths are never shared. Untracked files are NOT in a diff; `git add` them first. Same as `colab artifact submit --type --file` / `--type diff [--base]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["type"],"properties":{"type":{"type":"string","minLength":1,"description":"open set: file · diff · branch · doc · report …"},"file":{"type":"string","description":"path to the file to upload (max 50 MB). REQUIRED except for type=diff, where omitting it makes the CLI build the diff of this workdir"},"base":{"type":"string","description":"type=diff only: the branch or commit to diff against (default: the repository default branch). Cannot point at another repository — the diff is always of this workdir"},"name":{"type":"string","description":"artifact name; defaults to the file's base name, or to <branch>.diff for a generated diff — keep it the same to submit a new version"},"description":{"type":"string","description":"for a diff the CLI puts \"diff <branch>@<commit> vs <base>\" on the first line and this underneath"},"session":{"type":"string"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
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
		InputSchema: json.RawMessage(`{"type":"object","required":["question","default"],"properties":{"question":{"type":"string","minLength":1,"description":"the question for the Director"},"default":{"type":"string","minLength":1,"description":"REQUIRED — the answer you propose (FR-5.1); with choices it must be one of them"},"choices":{"type":"array","minItems":2,"items":{"type":"string"},"description":"2+ options; makes this a choice-type request"},"context":{"type":"string","description":"background the human needs to answer"},"session":{"type":"string","description":"room id (default: this task's room)"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_hitl_approve_request",
		Description: "Ask a human to APPROVE something and STOP — for an irreversible or out-of-scope step you must not take on your own. There is no default and it NEVER auto-proceeds, even after the due date passes (FR-5.4): without an answer the work stays stopped. The result is `turn_end_required: true` — END YOUR TURN. A rejection is a normal outcome and comes back with its reason in your next turn. One open request per task; a second call fails with code `hitl_already_open`. Same as `colab hitl approve-request --summary [--artifact]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["summary"],"properties":{"summary":{"type":"string","minLength":1,"description":"what you are asking approval for"},"artifact":{"type":"string","description":"artifact id this approval is about"},"session":{"type":"string","description":"room id (default: this task's room)"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_hitl_request_info",
		Description: "Ask a human for INFORMATION you cannot obtain yourself (a credential holder's answer, an offline document, a fact only they know) and STOP. No default, and it never auto-proceeds. The result is `turn_end_required: true` — END YOUR TURN. One open request per task; a second call fails with code `hitl_already_open`. Same as `colab hitl request-info --what [--why]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["what"],"properties":{"what":{"type":"string","minLength":1,"description":"the information you need"},"why":{"type":"string","description":"why you need it"},"session":{"type":"string","description":"room id (default: this task's room)"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	}, {
		Name:        "colab_room_list",
		Description: "List the OTHER rooms this turn may read: only rooms that both the person who started this turn and you can access, judged by the server at the moment of the call — anything else is simply not in the list. Returns items[] with id, name, description, last_activity_at, agent_is_participant, via_link. Same as `colab room list [--query]`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"only rooms matching this text"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_room_read",
		Description: "Read another room (an id from colab_room_list): its latest summary, recent messages, decisions and artifacts. READ-ONLY and for THIS turn only — to carry something over into this room, record it with colab_decision_record. `truncated: true` means the server cut it to the read limits. A refusal fails with code `room_read_denied` and `denied_reason` (originator_not_participant · originator_left · agent_not_allowed · no_originator) — tell the person why rather than retrying. The read is logged in both rooms. Same as `colab room read --room [--tail --query]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["room"],"properties":{"room":{"type":"string","description":"the room id to read"},"tail":{"type":"integer","minimum":1,"maximum":100,"description":"recent messages, 1..100; omit for the server default (30)"},"query":{"type":"string","description":"only messages matching this text"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_work_propose",
		Description: "Propose a new MISSION for this room. You cannot open a mission yourself: the proposal goes to the room's people, and a person opens it (and becomes its Director) or declines it. Returns proposal_id. Same as `colab work propose --goal --why`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["goal","why"],"properties":{"goal":{"type":"string","minLength":1,"description":"the mission's goal"},"why":{"type":"string","minLength":1,"description":"why this should be a mission"},"idempotency_key":{"type":"string"}},"additionalProperties":false}`),
	},
}

// ToolCommand is the ColabCommand a tool runs — what --allow and the gate
// decide it by: the tool name without its colab_ prefix (colab-cli.md §3).
func ToolCommand(name string) client.Command {
	return client.Command(strings.TrimPrefix(name, "colab_"))
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
	tools   []Tool   // what tools/list answers: Tools, or the --allow subset
	allow   []string // the --allow list as given (nil = everything)
	mu      sync.Mutex
}

// Options tunes Serve.
type Options struct {
	// Allow is the `--allow` list: ColabCommand names whose tools are
	// registered. nil (flag absent) or empty registers every tool. Names
	// that are not commands are ignored — a newer server's command an older
	// CLI does not know cannot be registered anyway — and reported through
	// Unknown when it is set.
	Allow   []string
	Unknown func(name string)
}

// FilterTools is the tools/list table for an --allow list: Tools in their
// stable order, kept when the tool's command (ToolCommand: the tool name
// minus `colab_`, or the aliased command) is in allow. An empty allow keeps everything (daemon-protocol §4.1 "비면 전부").
func FilterTools(allow []string) []Tool {
	if len(allow) == 0 {
		return Tools
	}
	set := map[client.Command]bool{}
	for _, a := range allow {
		set[client.Command(a)] = true
	}
	out := make([]Tool, 0, len(Tools))
	for _, t := range Tools {
		if set[ToolCommand(t.Name)] {
			out = append(out, t)
		}
	}
	return out
}

// Serve runs the JSON-RPC loop until in is closed or ctx is done. Every line
// on in is one message; every response is one line on out.
func Serve(ctx context.Context, c *client.Client, in io.Reader, out io.Writer, version string) error {
	return ServeWith(ctx, c, in, out, version, Options{})
}

// ServeWith is Serve with Options.
func ServeWith(ctx context.Context, c *client.Client, in io.Reader, out io.Writer, version string, o Options) error {
	s := NewServer(c, out, version, o)
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

// NewServer builds a Server for one connection; Serve/ServeWith run its loop.
// Exposed for tests that drive Handle directly.
func NewServer(c *client.Client, out io.Writer, version string, o Options) *Server {
	if o.Unknown != nil {
		for _, a := range o.Allow {
			if !client.IsCommand(a) {
				o.Unknown(a)
			}
		}
	}
	return &Server{c: c, version: version, out: out, tools: FilterTools(o.Allow), allow: o.Allow}
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
		return map[string]any{"tools": s.tools}, nil
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
	if cmd := ToolCommand(name); !s.registered(name) && client.IsCommand(string(cmd)) {
		// A real tool that --allow left out: the same refusal the CLI gives
		// (client.NotAllowed), as a tool result the model can read — not a
		// protocol error, which reads like a typo. The role is named only if
		// a context was already fetched; never a round trip for the sentence.
		role := ""
		if cc := s.c.CachedContext(); cc != nil {
			role = cc.OwnRole()
		}
		return s.errorResult(client.NotAllowed(role, cmd, s.allow)), nil
	}
	var (
		v   any
		err error
	)
	switch name {
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
	case "colab_room_list":
		var a colab.RoomListArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.RoomList(ctx, s.c, a)
	case "colab_room_read":
		var a colab.RoomReadArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.RoomRead(ctx, s.c, a)
	case "colab_work_propose":
		var a colab.WorkProposeArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.WorkPropose(ctx, s.c, a)
	case "colab_room_get":
		var a colab.RoomGetArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.RoomGet(ctx, s.c, a)
	case "colab_room_messages":
		var a colab.RoomMessagesArgs
		if e := json.Unmarshal(args, &a); e != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: e.Error()}
		}
		v, err = colab.RoomMessages(ctx, s.c, a)
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: fmt.Sprintf("unknown tool %q", name)}
	}
	if err != nil {
		return s.errorResult(err), nil
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(colab.MarshalIndent(v))}},
		"structuredContent": v,
	}, nil
}

func (s *Server) registered(name string) bool {
	for _, t := range s.tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// errorResult is a command failure (exit 2..5) as a tool result with
// isError=true carrying the same error JSON the CLI prints.
func (s *Server) errorResult(err error) map[string]any {
	ej := colab.ErrorJSON(err)
	return map[string]any{
		"isError":           true,
		"content":           []map[string]any{{"type": "text", "text": string(colab.MarshalIndent(ej))}},
		"structuredContent": ej,
	}
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
