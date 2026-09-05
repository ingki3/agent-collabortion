// Package acpprobe is a minimal ACP (Agent Client Protocol) client over
// JSON-RPC/stdio, written for the P0-a spikes (PLAN.md §4 #1·2·3·4a). It is a
// probe, not the production harness: it does the least needed to drive
// claude-code-acp and `hermes acp` and to measure what PLAN §4 asks for.
//
// Wire format: newline-delimited JSON-RPC 2.0. Client → agent requests use
// the AGENT_METHODS names; agent → client requests (session/request_permission)
// are answered by the client; session/update is a notification.
package acpprobe

import "encoding/json"

// ProtocolVersion is the ACP major version this probe speaks. Both adapters
// under test (claude-code-acp 0.16.x, hermes 0.20.x) advertise 1.
const ProtocolVersion = 1

// Agent methods (client → agent). Names from @agentclientprotocol/sdk
// schema/meta.json v0.11.x / v0.14.x.
const (
	MethodInitialize      = "initialize"
	MethodSessionNew      = "session/new"
	MethodSessionLoad     = "session/load"
	MethodSessionResume   = "session/resume" // UNSTABLE in the spec; both adapters implement it
	MethodSessionPrompt   = "session/prompt"
	MethodSessionCancel   = "session/cancel"
	MethodSessionSetModel = "session/set_model"
	MethodSessionSetMode  = "session/set_mode"
	// MethodSessionSetConfigOption is the ACP 1.x replacement for set_model:
	// claude-agent-acp 0.74.0 no longer returns `models` from session/new, it
	// returns `configOptions` (ids: mode, model, effort, …).
	MethodSessionSetConfigOption = "session/set_config_option"
)

// ExtNotificationSDKMessage is claude-agent-acp's raw SDK message stream,
// enabled per session with `_meta.claudeCode.emitRawSDKMessages` (true or a
// filter list). Params: {sessionId, message: <SDKMessage>}.
const ExtNotificationSDKMessage = "_claude/sdkMessage"

// Client methods (agent → client).
const (
	MethodRequestPermission = "session/request_permission"
	MethodSessionUpdate     = "session/update"
	MethodFSRead            = "fs/read_text_file"
	MethodFSWrite           = "fs/write_text_file"
	MethodTerminalCreate    = "terminal/create"
)

// message is the union JSON-RPC envelope. Exactly one of the shapes is
// populated per line; we decode into this and dispatch on which fields exist.
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

// ErrorKind returns data.errorKind if the agent attached one (claude-agent-acp
// 0.74.0 sets it from the SDK's assistant error: rate_limit, overloaded,
// authentication_failed, billing_error, …). Empty when absent.
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

// InitializeParams — we advertise no fs/terminal capability on purpose: with
// them absent, claude-code-acp lets Claude Code use its own Read/Write/Bash
// tools, which go through canUseTool → session/request_permission, which is
// exactly the path spike 1 measures.
type InitializeParams struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

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
	AgentCapabilities json.RawMessage `json:"agentCapabilities"`
	AgentInfo         *Implementation `json:"agentInfo,omitempty"`
	AuthMethods       json.RawMessage `json:"authMethods,omitempty"`
}

// ---- session/new · load · resume -----------------------------------------

type NewSessionParams struct {
	Cwd        string         `json:"cwd"`
	MCPServers []any          `json:"mcpServers"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

type LoadSessionParams struct {
	Cwd        string         `json:"cwd"`
	SessionID  string         `json:"sessionId"`
	MCPServers []any          `json:"mcpServers"`
	Meta       map[string]any `json:"_meta,omitempty"`
}

// SessionResult is the common shape of session/new, session/load and
// session/resume responses. sessionId is only present on session/new.
type SessionResult struct {
	SessionID     string          `json:"sessionId,omitempty"`
	Models        *ModelState     `json:"models,omitempty"`
	Modes         json.RawMessage `json:"modes,omitempty"`
	ConfigOptions []ConfigOption  `json:"configOptions,omitempty"`
	Meta          json.RawMessage `json:"_meta,omitempty"`
}

// ConfigOption is one entry of session/new·load `configOptions` (ACP 1.x).
// currentValue is a string for select options and a bool for boolean ones.
type ConfigOption struct {
	ID           string          `json:"id"`
	Name         string          `json:"name,omitempty"`
	Category     string          `json:"category,omitempty"`
	Type         string          `json:"type,omitempty"`
	CurrentValue json.RawMessage `json:"currentValue,omitempty"`
	Options      json.RawMessage `json:"options,omitempty"`
}

// ConfigOptionValue returns the string currentValue of option id ("" if absent
// or not a string).
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

type SetConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     any    `json:"value"`
}

type SetConfigOptionResult struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
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
	Usage      json.RawMessage `json:"usage,omitempty"`
	Meta       json.RawMessage `json:"_meta,omitempty"`
}

// ModelsUsed extracts `_meta.quota.model_usage[].model` (claude-agent-acp
// ≥0.7x) — the accounting-grade record of which model(s) actually served the
// turn, subagents included. This is the evidence for "which model ran", not
// the agent's own answer.
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

type SetModelParams struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// ---- session/request_permission ------------------------------------------

type RequestPermissionParams struct {
	SessionID string             `json:"sessionId"`
	ToolCall  json.RawMessage    `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
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

// UpdateKind is the discriminator of a session/update payload.
type UpdateKind struct {
	SessionUpdate string `json:"sessionUpdate"`
}
