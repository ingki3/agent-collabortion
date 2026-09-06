package client

import "encoding/json"

// P2 wire types — contracts/openapi.yaml operations tagged x-colab-cli with
// x-phase: P2 (delegateLane · setTaskStatus · recordDecision ·
// submitArtifact · getArtifact · downloadArtifact · reviewArtifact).
//
// Every result keeps the server's whole body in Raw so `--json` never drops a
// field the contract adds later; the typed fields are only the ones the CLI
// itself has to read.

// LaneDelegateCreate — POST /sessions/{S}/lanes body (delegateLane).
// `agent_id` is a uuid: the CLI resolves `--agent <name>` against the
// /cli/context participant roster (FR-1.5, E15-02).
type LaneDelegateCreate struct {
	AgentID   string   `json:"agent_id"`
	Brief     string   `json:"brief"`
	DependsOn []string `json:"depends_on,omitempty"`
	Profile   *string  `json:"profile,omitempty"`
}

// LaneDelegateResult — delegateLane 201 {lane, message, task?}.
type LaneDelegateResult struct {
	Lane    json.RawMessage `json:"lane"`
	Message *Message        `json:"message,omitempty"`
	Task    json.RawMessage `json:"task,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

// TaskStatus values — setTaskStatus request `status` enum.
const (
	StatusWorking = "working"
	StatusBlocked = "blocked"
	StatusDone    = "done"
)

// TaskStatusCreate — POST /tasks/{T}/status body.
type TaskStatusCreate struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// TaskStatusResult — setTaskStatus 200 {task, lane, turn_end_required,
// question_message_id?}.
//
// The flag has exactly one name across the contracts: `turn_end_required`
// ("end your turn", an instruction). ACP's `end_turn` is a different thing —
// a stopReason stating a turn already ended — and the two must never share a
// name in a codebase that handles both (Lead ruling on PR #59).
type TaskStatusResult struct {
	Task              json.RawMessage `json:"task,omitempty"`
	Lane              json.RawMessage `json:"lane,omitempty"`
	TurnEndRequired   bool            `json:"turn_end_required"`
	QuestionMessageID *string         `json:"question_message_id,omitempty"`
	Raw               json.RawMessage `json:"-"`
}

// DecisionCreate — POST /sessions/{S}/decisions body (recordDecision).
type DecisionCreate struct {
	Summary   string `json:"summary"`
	Rationale string `json:"rationale,omitempty"`
}

// ArtifactUpload — the multipart/form-data parts of submitArtifact.
type ArtifactUpload struct {
	Name        string
	Type        string
	Description string
	FileName    string // filename= of the `file` part
	ContentType string // per-part Content-Type (default application/octet-stream)
	Data        []byte
}

// ArtifactSubmitResult — submitArtifact 201 {artifact, completion_progress?}.
type ArtifactSubmitResult struct {
	Artifact           json.RawMessage `json:"artifact"`
	CompletionProgress json.RawMessage `json:"completion_progress,omitempty"`
	Raw                json.RawMessage `json:"-"`
}

// ArtifactContent — downloadArtifact 200 (application/octet-stream).
type ArtifactContent struct {
	Data        []byte
	ContentType string
	FileName    string // from Content-Disposition, when the server sends one
}

// Review verdicts — reviewArtifact request `verdict` enum.
const (
	VerdictApprove = "approve"
	VerdictReject  = "reject"
)

// ReviewCreate — POST /artifacts/{id}/review body (reviewArtifact).
type ReviewCreate struct {
	Verdict  string `json:"verdict"`
	Comments string `json:"comments,omitempty"`
}

// ReviewResult — reviewArtifact 200 {review, completion_progress, message?}.
type ReviewResult struct {
	Review             json.RawMessage `json:"review"`
	CompletionProgress json.RawMessage `json:"completion_progress"`
	Message            *Message        `json:"message,omitempty"`
	Raw                json.RawMessage `json:"-"`
}
