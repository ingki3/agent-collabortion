package contracts

import "time"

// Go mirror of contracts/daemon-protocol.md, contracts/harness.md and
// contracts/task_event.schema.json. Wire format is JSON with these tags.
// Enum values are the single source of truth shared with server/migrations.

// RuntimeKind — v1 runtimes (PRD FR-1.6). "antigravity" exists in the DB enum
// for v1.1 but is not a valid harness target.
type RuntimeKind string

const (
	RuntimeClaudeCode RuntimeKind = "claude_code"
	RuntimeHermes     RuntimeKind = "hermes"
)

// Transport — v1 is ACP only (G1: CLI adapter excluded).
type Transport string

const (
	TransportACP Transport = "acp"
	TransportCLI Transport = "cli" // reserved, v1.1
)

// BriefTransport — how the brief reaches the runtime (harness.md §1, §10).
type BriefTransport string

const (
	BriefACPMetaSystemPrompt BriefTransport = "acp_meta_system_prompt" // claude_code: _meta.systemPrompt.append
	BriefInstructionFile     BriefTransport = "instruction_file"       // hermes: AGENTS.md marker block
)

// AdapterPin — G1 F1. The old @zed-industries/claude-code-acp is frozen.
const (
	ClaudeAgentACPPackage = "@agentclientprotocol/claude-agent-acp"
	ClaudeAgentACPPin     = "0.74.0"
	ACPProtocolVersion    = 1
)

// FailureKind — task.failure_kind (harness.md §8, FR-7.1). Must match the
// failure_kind enum in server/migrations/0001_init.sql.
type FailureKind string

const (
	FailAuth           FailureKind = "auth"
	FailQuota          FailureKind = "quota"
	FailRateLimited    FailureKind = "rate_limited" // G1 F3: retry at not_before
	FailConfig         FailureKind = "config"
	FailNetwork        FailureKind = "network"
	FailRuntimeOffline FailureKind = "runtime_offline"
	FailStall          FailureKind = "stall"
	FailTimeout        FailureKind = "timeout"
	FailCancelled      FailureKind = "cancelled"
	FailOther          FailureKind = "other"
)

// Retryable reports whether FR-7.1 allows an automatic retry.
func (k FailureKind) Retryable() bool {
	switch k {
	case FailRateLimited, FailNetwork, FailRuntimeOffline, FailStall, FailTimeout, FailOther:
		return true
	}
	return false
}

// Timings shared by server and daemon (daemon-protocol.md §4). Tests override
// via contracts/clock, never by editing these.
const (
	HeartbeatInterval = 15 * time.Second
	HeartbeatExpiry   = 3 * time.Minute
	DispatchedTimeout = 5 * time.Minute
	StallTimeout      = 3 * time.Minute
	CancelDrainWait   = 30 * time.Second // FR-3.4: wait for an in-flight edit/shell before cancelling
	CancelPromptWait  = 10 * time.Second
	KillAfterTerm     = 10 * time.Second
	HermesQuietWait   = 250 * time.Millisecond // PRD §8.2.5
	ClaimMaxWait      = 30 * time.Second
	RateLimitFallback = 30 * time.Minute // not_before when the reset time cannot be parsed
)

// RuntimeSessionRef — lane.runtime_session_ref (harness.md §6).
type RuntimeSessionRef struct {
	RuntimeKind    RuntimeKind       `json:"runtime_kind"`
	AdapterVersion string            `json:"adapter_version,omitempty"`
	SessionID      string            `json:"session_id"`
	CWD            string            `json:"cwd"`
	CreatedAt      time.Time         `json:"created_at"`
	Provenance     *HermesProvenance `json:"provenance,omitempty"`
}

// HermesProvenance — _meta.hermes.sessionProvenance (spike 4a).
type HermesProvenance struct {
	ACPSessionID         string `json:"acpSessionId"`
	RootHermesSessionID  string `json:"rootHermesSessionId,omitempty"`
	CurrentHermesSession string `json:"currentHermesSessionId,omitempty"`
	SessionKind          string `json:"sessionKind,omitempty"`
	CompressionDepth     int    `json:"compressionDepth,omitempty"`
}

// Capability — one runtime as reported by probe (harness.md §9).
type Capability struct {
	Kind             RuntimeKind    `json:"kind"`
	Version          string         `json:"version"`
	AdapterVersion   string         `json:"adapter_version,omitempty"`
	LoggedIn         bool           `json:"logged_in"`
	Models           []string       `json:"models"`
	ProtocolVersion  int            `json:"protocol_version"`
	Resume           bool           `json:"resume"`
	Usage            bool           `json:"usage"`
	ToolDisallow     bool           `json:"tool_disallow"`
	BriefTransport   BriefTransport `json:"brief_transport"`
	AllowOnceMissing bool           `json:"allow_once_missing"`
}

// Repo — probe repos[] (FR-9, FR-9.2 rebinding by remote_url).
type Repo struct {
	Path      string `json:"path"`
	RemoteURL string `json:"remote_url"`
	Branch    string `json:"branch"`
	Clean     bool   `json:"clean"`
}

// Probe — POST /v1/daemon/runtimes/{id}/probe body (daemon-protocol.md §3).
type Probe struct {
	DaemonVersion string       `json:"daemon_version"`
	Hostname      string       `json:"hostname"`
	Capabilities  []Capability `json:"capabilities"`
	Repos         []Repo       `json:"repos"`
	WorkdirRoot   string       `json:"workdir_root"`
	Disk          Disk         `json:"disk"`
}

type Disk struct {
	UsedBytes  int64 `json:"used_bytes"`
	QuotaBytes int64 `json:"quota_bytes,omitempty"`
}

// TaskBundle — one claimed task attempt (daemon-protocol.md §4.1).
type TaskBundle struct {
	Task             BundleTask         `json:"task"`
	TaskToken        string             `json:"task_token"`
	Profile          BundleProfile      `json:"profile"`
	Workdir          BundleWorkdir      `json:"workdir"`
	Brief            BundleBrief        `json:"brief"`
	Prompt           string             `json:"prompt"`
	Resume           *RuntimeSessionRef `json:"resume"`
	Limits           BundleLimits       `json:"limits"`
	PostedMessageIDs []string           `json:"posted_message_ids,omitempty"`
}

type BundleTask struct {
	ID                  string   `json:"id"`
	Attempt             int      `json:"attempt"`
	LaneID              string   `json:"lane_id"`
	SessionID           string   `json:"session_id"`
	AgentID             string   `json:"agent_id"`
	AgentName           string   `json:"agent_name"`
	TriggerMessageID    string   `json:"trigger_message_id"`
	RestartedFromTaskID string   `json:"restarted_from_task_id,omitempty"`
	DelegatedFromTaskID string   `json:"delegated_from_task_id,omitempty"`
	BudgetUSD           *float64 `json:"budget_usd,omitempty"`
	BudgetOverrideUSD   *float64 `json:"budget_override_usd,omitempty"`
}

type BundleProfile struct {
	RuntimeKind RuntimeKind       `json:"runtime_kind"`
	Model       string            `json:"model"`
	Options     map[string]any    `json:"options,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Tools       []string          `json:"tools,omitempty"` // allow-list; empty = runtime default
	AdapterPin  string            `json:"adapter_pin,omitempty"`
}

type BundleWorkdir struct {
	Kind     string `json:"kind"` // worktree | dir
	Path     string `json:"path,omitempty"`
	RepoPath string `json:"repo_path,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Reuse    bool   `json:"reuse"`
}

type BundleBrief struct {
	Transport BriefTransport `json:"transport"`
	Text      string         `json:"text"`
}

type BundleLimits struct {
	BudgetUSD    *float64 `json:"budget_usd,omitempty"`
	StallSeconds int      `json:"stall_seconds"`
}

// CommandType — server → daemon (daemon-protocol.md §4.3).
type CommandType string

const (
	CmdCancel        CommandType = "cancel"
	CmdRevoke        CommandType = "revoke"
	CmdProbe         CommandType = "probe"
	CmdGC            CommandType = "gc"
	CmdRebindPrepare CommandType = "rebind_prepare"
)

type Command struct {
	Type             CommandType   `json:"type"`
	TaskID           string        `json:"task_id,omitempty"`
	Attempt          int           `json:"attempt,omitempty"`
	AfterCurrentTool bool          `json:"after_current_tool,omitempty"`
	Reason           string        `json:"reason,omitempty"` // director | budget | kill_switch | loop | session_paused
	WorkdirIDs       []string      `json:"workdir_ids,omitempty"`
	SessionID        string        `json:"session_id,omitempty"`
	Artifacts        []ArtifactRef `json:"artifacts,omitempty"`
}

type ArtifactRef struct {
	ID    string `json:"id"`
	Order int    `json:"order"`
	URL   string `json:"url"`
}

// Finish — POST …/finish body (daemon-protocol.md §4.4).
type Finish struct {
	Outcome           string             `json:"outcome"` // completed | failed | cancelled | paused_budget
	StopReason        string             `json:"stop_reason,omitempty"`
	FailureKind       FailureKind        `json:"failure_kind,omitempty"`
	NotBefore         *time.Time         `json:"not_before,omitempty"`
	Usage             Usage              `json:"usage"`
	RuntimeSessionRef *RuntimeSessionRef `json:"runtime_session_ref,omitempty"`
	ResumeOutcome     string             `json:"resume_outcome,omitempty"` // resumed | cold_start
	LastSeq           int                `json:"last_seq"`
}

type Usage struct {
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64   `json:"cache_write_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd"`
	Estimated        bool    `json:"estimated"`
}

// TaskEvent — contracts/task_event.schema.json. Payload is class-specific and
// validated against the schema at the server boundary.
type TaskEvent struct {
	TaskID    string         `json:"task_id"`
	Attempt   int            `json:"attempt"`
	Seq       int            `json:"seq"`
	TS        time.Time      `json:"ts"`
	Class     string         `json:"class"` // message | tool | usage | plan | runtime | status
	Verb      string         `json:"verb"`
	ObjectRef string         `json:"object_ref,omitempty"`
	Outcome   string         `json:"outcome"`
	Partial   bool           `json:"partial,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}
