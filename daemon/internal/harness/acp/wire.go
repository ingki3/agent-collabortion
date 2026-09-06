// Package acp is the production ACP (Agent Client Protocol) harness — the
// promotion of internal/acpprobe (P0-a spikes) into what contracts/harness.md
// v0.2 specifies: process lifetime (§2), _meta (§3), permission policy (§4),
// cancel (§5), resume (§6), event normalisation (§7), error classification
// (§8). Wire format is newline-delimited JSON-RPC 2.0 over stdio.
package acp

import "encoding/json"

// Agent methods (client → agent).
const (
	MethodInitialize             = "initialize"
	MethodSessionNew             = "session/new"
	MethodSessionLoad            = "session/load"
	MethodSessionPrompt          = "session/prompt"
	MethodSessionCancel          = "session/cancel"
	MethodSessionSetModel        = "session/set_model"
	MethodSessionSetConfigOption = "session/set_config_option"
)

// Client methods (agent → client).
const (
	MethodRequestPermission = "session/request_permission"
	MethodSessionUpdate     = "session/update"
)

// ExtNotificationSDKMessage is claude-agent-acp's raw SDK stream, enabled per
// session with `_meta.claudeCode.emitRawSDKMessages: true` (probe/smoke only).
const ExtNotificationSDKMessage = "_claude/sdkMessage"

// message is the union JSON-RPC envelope.
type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
}

// RPCError is a JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if len(e.Data) > 0 && string(e.Data) != "null" {
		return e.Message + " data=" + string(e.Data)
	}
	return e.Message
}

// ErrorKind returns data.errorKind (claude-agent-acp 0.74.0: rate_limit,
// overloaded, authentication_failed, billing_error, …). Empty when absent.
func (e *RPCError) ErrorKind() string {
	if len(e.Data) == 0 {
		return ""
	}
	var d struct {
		ErrorKind string `json:"errorKind"`
	}
	_ = json.Unmarshal(e.Data, &d)
	return d.ErrorKind
}

// ---- initialize -----------------------------------------------------------

type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// ClientCapabilities — harness §2: fs and terminal are never advertised.
type ClientCapabilities struct {
	FS       FSCapabilities `json:"fs"`
	Terminal bool           `json:"terminal"`
}

type FSCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version,omitempty"`
}

type InitializeResult struct {
	ProtocolVersion   int             `json:"protocolVersion"`
	AgentCapabilities json.RawMessage `json:"agentCapabilities,omitempty"`
	AgentInfo         *Implementation `json:"agentInfo,omitempty"`
	AuthMethods       json.RawMessage `json:"authMethods,omitempty"`
}

// AgentCaps is the part of `agentCapabilities` the harness acts on. PRD
// §8.2.1: a capability is judged by what the session advertised, never by
// the runtime name — the same protocol keys are spoken by different
// binaries and by different versions of the same one.
type AgentCaps struct {
	// LoadSession is the resume capability (harness §6, probe §9 `resume`).
	LoadSession bool `json:"loadSession"`
	// MCP is which MCP transports the agent accepts.
	MCP MCPCapabilities `json:"mcpCapabilities"`
}

// MCPCapabilities is `agentCapabilities.mcpCapabilities`. stdio is the ACP
// baseline every agent accepts and is therefore not advertised; http and sse
// are (PRD §8.2.3 "런타임 `mcpCapabilities`에 맞춰 stdio/http 필터").
type MCPCapabilities struct {
	HTTP bool `json:"http"`
	SSE  bool `json:"sse"`
}

// Caps decodes agentCapabilities. An absent or unparsable block means
// "nothing advertised" — the caller then keeps only the stdio baseline.
func (r *InitializeResult) Caps() AgentCaps {
	var c AgentCaps
	if r == nil || len(r.AgentCapabilities) == 0 {
		return c
	}
	_ = json.Unmarshal(r.AgentCapabilities, &c)
	return c
}

// ---- session/new · load ---------------------------------------------------

type NewSessionParams struct {
	Cwd        string         `json:"cwd"`
	MCPServers []MCPServer    `json:"mcpServers"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

type LoadSessionParams struct {
	Cwd        string         `json:"cwd"`
	SessionID  string         `json:"sessionId"`
	MCPServers []MCPServer    `json:"mcpServers"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// SessionResult is the common shape of session/new and session/load responses.
type SessionResult struct {
	SessionID     string          `json:"sessionId,omitempty"`
	Models        *ModelState     `json:"models,omitempty"`
	Modes         json.RawMessage `json:"modes,omitempty"`
	ConfigOptions []ConfigOption  `json:"configOptions,omitempty"`
	Meta          json.RawMessage `json:"_meta,omitempty"`
}

// HermesProvenance extracts _meta.hermes.sessionProvenance (harness §6).
func (s *SessionResult) HermesProvenance() (acpSessionID, rootHermesSessionID, sessionKind string, depth int, ok bool) {
	if s == nil || len(s.Meta) == 0 {
		return "", "", "", 0, false
	}
	var m struct {
		Hermes struct {
			SessionProvenance *struct {
				ACPSessionID        string `json:"acpSessionId"`
				RootHermesSessionID string `json:"rootHermesSessionId"`
				SessionKind         string `json:"sessionKind"`
				CompressionDepth    int    `json:"compressionDepth"`
			} `json:"sessionProvenance"`
		} `json:"hermes"`
	}
	if json.Unmarshal(s.Meta, &m) != nil || m.Hermes.SessionProvenance == nil {
		return "", "", "", 0, false
	}
	p := m.Hermes.SessionProvenance
	return p.ACPSessionID, p.RootHermesSessionID, p.SessionKind, p.CompressionDepth, true
}

// ConfigOption is one entry of `configOptions` (ACP 1.x).
type ConfigOption struct {
	ID           string          `json:"id"`
	Name         string          `json:"name,omitempty"`
	Category     string          `json:"category,omitempty"`
	Type         string          `json:"type,omitempty"`
	CurrentValue json.RawMessage `json:"currentValue,omitempty"`
	Options      json.RawMessage `json:"options,omitempty"`
}

// ConfigOptionValue returns the string currentValue of option id.
func ConfigOptionValue(opts []ConfigOption, id string) string {
	for _, o := range opts {
		if o.ID == id {
			var s string
			_ = json.Unmarshal(o.CurrentValue, &s)
			return s
		}
	}
	return ""
}

// ConfigOptionValues returns the `value` list of a select option (model ids).
func ConfigOptionValues(opts []ConfigOption, id string) []string {
	for _, o := range opts {
		if o.ID != id {
			continue
		}
		var vals []struct {
			Value string `json:"value"`
		}
		_ = json.Unmarshal(o.Options, &vals)
		out := make([]string, 0, len(vals))
		for _, v := range vals {
			out = append(out, v.Value)
		}
		return out
	}
	return nil
}

type SetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
}

type SetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

type SetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

type ModelState struct {
	CurrentModelID  string      `json:"currentModelId"`
	AvailableModels []ModelInfo `json:"availableModels"`
}

type ModelInfo struct {
	ModelID     string `json:"modelId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ---- session/prompt · cancel ---------------------------------------------

type PromptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type PromptResult struct {
	StopReason string          `json:"stopReason"`
	Usage      *PromptUsage    `json:"usage,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

// PromptUsage is the session/prompt response `usage` (ACP 1.x).
//
// The token fields are measured (spike 1b). There is NO cost in what the
// pinned adapter sends — that is the whole of D-6: the runner used to read
// this struct's zero value as a measured $0. `CostUSD` is a pointer and
// `omitempty` so the two states stay apart: absent (nil → the attempt is an
// estimate, harness v0.7) versus a reported number, including a reported 0.
// No adapter fills it today; it is the seam a runtime that prices its own
// turns drops into, and the contract test drives it through acpfake.
type PromptUsage struct {
	InputTokens       int64    `json:"inputTokens"`
	OutputTokens      int64    `json:"outputTokens"`
	CachedReadTokens  int64    `json:"cachedReadTokens"`
	CachedWriteTokens int64    `json:"cachedWriteTokens"`
	ThoughtTokens     int64    `json:"thoughtTokens"`
	TotalTokens       int64    `json:"totalTokens"`
	CostUSD           *float64 `json:"costUSD,omitempty"`
}

// ModelsUsed extracts `_meta.quota.model_usage[].model` — which model(s)
// actually served the turn (harness §7 model_drift).
func (p PromptResult) ModelsUsed() []string {
	var m struct {
		Quota struct {
			ModelUsage []struct {
				Model string `json:"model"`
			} `json:"model_usage"`
		} `json:"quota"`
	}
	if len(p.Meta) == 0 || json.Unmarshal(p.Meta, &m) != nil {
		return nil
	}
	var out []string
	for _, u := range m.Quota.ModelUsage {
		out = append(out, u.Model)
	}
	return out
}

type CancelParams struct {
	SessionID string `json:"sessionId"`
}

// ---- session/request_permission ------------------------------------------

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCallRef        `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

// ToolCallRef is the toolCall object inside request_permission.
type ToolCallRef struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once | allow_always | reject_once | reject_always
}

type RequestPermissionResult struct {
	Outcome PermissionOutcome `json:"outcome"`
}

// PermissionOutcome is {"outcome":"selected","optionId":..} or {"outcome":"cancelled"}.
type PermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// ---- session/update --------------------------------------------------------

type SessionUpdateParams struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// Update is the decoded union of session/update payloads. Only the fields
// the harness normalises (§7) are typed; everything else stays raw.
type Update struct {
	SessionUpdate string `json:"sessionUpdate"`
	// agent_message_chunk · agent_thought_chunk · user_message_chunk
	Content json.RawMessage `json:"content,omitempty"`
	// tool_call · tool_call_update
	ToolCallID string          `json:"toolCallId,omitempty"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	Locations  []ToolLocation  `json:"locations,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage `json:"rawOutput,omitempty"`
	// plan
	Entries []PlanEntry `json:"entries,omitempty"`
	// usage_update
	Used int64           `json:"used,omitempty"`
	Size int64           `json:"size,omitempty"`
	Meta json.RawMessage `json:"_meta,omitempty"`
}

type ToolLocation struct {
	Path string `json:"path"`
	Line *int   `json:"line,omitempty"`
}

type PlanEntry struct {
	Content  string `json:"content"`
	Priority string `json:"priority,omitempty"`
	Status   string `json:"status,omitempty"` // pending | in_progress | completed
}

// ChunkText returns the text of a *_chunk update's content block.
func (u *Update) ChunkText() string {
	if len(u.Content) == 0 || u.Content[0] != '{' {
		return ""
	}
	var c struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(u.Content, &c) != nil || c.Type != "text" {
		return ""
	}
	return c.Text
}

// ToolContent is one element of tool_call(.update) `content[]`.
type ToolContent struct {
	Type    string  `json:"type"` // diff | content | terminal
	Path    string  `json:"path,omitempty"`
	OldText *string `json:"oldText,omitempty"`
	NewText string  `json:"newText,omitempty"`
	Content *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
}

// ToolContents decodes tool content (an array here, an object for chunks).
func (u *Update) ToolContents() []ToolContent {
	if len(u.Content) == 0 || u.Content[0] != '[' {
		return nil
	}
	var out []ToolContent
	_ = json.Unmarshal(u.Content, &out)
	return out
}

// RateLimitMeta is usage_update._meta["_claude/rateLimit"] (spike 1b E5).
type RateLimitMeta struct {
	Status        string  `json:"status"`   // allowed | allowed_warning | rejected
	ResetsAt      int64   `json:"resetsAt"` // epoch seconds
	RateLimitType string  `json:"rateLimitType,omitempty"`
	Utilization   float64 `json:"utilization,omitempty"`
}

// RateLimit extracts the structured limit state from a usage_update.
func (u *Update) RateLimit() *RateLimitMeta {
	if len(u.Meta) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(u.Meta, &m) != nil {
		return nil
	}
	raw, ok := m["_claude/rateLimit"]
	if !ok {
		return nil
	}
	var rl RateLimitMeta
	if json.Unmarshal(raw, &rl) != nil || rl.Status == "" {
		return nil
	}
	return &rl
}
