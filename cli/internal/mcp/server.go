// Package mcp is a minimal stdio MCP server (JSON-RPC 2.0, newline-delimited)
// exposing the colab commands as tools with the same names as the command
// paths joined by underscores (contracts/colab-cli.md §3):
// colab_session_get · colab_session_messages · colab_message_post.
//
// No SDK dependency: the daemon injects this server as the only MCP server
// (harness.md §3, strictMcpConfig) and the surface is three tools, so a
// hand-rolled JSON-RPC loop keeps the CLI a single static binary.
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

// Tools is the P1 tool table (order is stable for tools/list).
var Tools = []Tool{
	{
		Name:        "colab_session_get",
		Description: "Read this session: goal, acceptance_criteria, completion_progress, participants (roster with derived status), isolation, director. Same as `colab session get`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session":{"type":"string","description":"session id (default: the task's session)"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_session_messages",
		Description: "Read session messages (author, body, thread, time). Use when the history in your prompt is truncated. Same as `colab session messages [--since --limit --thread]`.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"session":{"type":"string"},"since":{"type":"string","description":"only messages after this cursor / message id"},"limit":{"type":"integer","minimum":1,"maximum":200},"thread":{"type":"string","description":"thread root message id: returns root + replies"}},"additionalProperties":false}`),
	},
	{
		Name:        "colab_message_post",
		Description: "Post a message to the session. Routing is server-side: an agent message triggers other agents ONLY when it mentions them (`mention`); the delegator's mention is suppressed until rejoin. Returns message_id, triggered[], suppressed[]. Same as `colab message post --body [--reply-to --mention]`.",
		InputSchema: json.RawMessage(`{"type":"object","required":["body"],"properties":{"body":{"type":"string","minLength":1,"description":"markdown text"},"reply_to":{"type":"string","description":"parent message id (thread)"},"mention":{"type":"array","items":{"type":"string"},"description":"agent names to mention, e.g. [\"@Reviewer\"]"},"session":{"type":"string"},"idempotency_key":{"type":"string","description":"reuse to retry the same post after a network error"}},"additionalProperties":false}`),
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

// parseMention accepts ["@A","@B"], "@A,@B" or null.
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
