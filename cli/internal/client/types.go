package client

import "encoding/json"

// Problem — openapi.yaml components.schemas.Problem (RFC 9457 + code + errors).
type Problem struct {
	Type   string `json:"type,omitempty"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code,omitempty"`
	// DeniedReason · RoomName are readRoom's 403 room_read_denied extension
	// members (openapi readRoom, colab-cli.md §2.4a): why the other room
	// could not be read, and — for originator_left only — which room.
	DeniedReason string `json:"denied_reason,omitempty"`
	RoomName     string `json:"room_name,omitempty"`
	Errors       []struct {
		Field   string `json:"field"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// CliContext — GET /cli/context (openapi.yaml CliContext).
type CliContext struct {
	TaskID                     string        `json:"task_id"`
	LaneID                     string        `json:"lane_id"`
	SessionID                  string        `json:"session_id"`
	AgentID                    string        `json:"agent_id"`
	AgentName                  string        `json:"agent_name,omitempty"`
	WorkspaceID                string        `json:"workspace_id"`
	Attempt                    int           `json:"attempt"`
	LastSeq                    int           `json:"last_seq"` // last client seq this task used (any attempt); CLI continues at +1
	DelegatedFromTaskID        *string       `json:"delegated_from_task_id"`
	SuppressedDelegatorAgentID *string       `json:"suppressed_delegator_agent_id"`
	OpenHitlRequestID          *string       `json:"open_hitl_request_id"`
	Participants               []Participant `json:"participants"`
	// Humans is the room's people (openapi v0.3.5): an agent may mention them
	// (`mention://user/<id>`), looked up after Participants (colab-cli v0.9.4).
	// A pre-v0.3.5 server omits it.
	Humans []Human `json:"humans,omitempty"`
	// AllowedCommands is the role's command subset (v1.1 K-19, colab-cli.md
	// §2.5). A pre-v1.1 server omits it (nil) and an empty list means no
	// restriction — both allow everything (AllowedCommandSet).
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	ExpiresAt       string   `json:"expires_at"`
}

// Participant — CliContext.participants[].
type Participant struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"`
	MentionLink string `json:"mention_link"`
}

// Human — CliContext.humans[] (openapi v0.3.5).
type Human struct {
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	MentionLink string `json:"mention_link"`
}

// MessageCreate — POST /rooms/{R}/messages body.
type MessageCreate struct {
	Content string `json:"content"`
	// Detail is the work layer (openapi v0.3.1 MessageCreate.detail, PRD
	// FR-3.1.2): findings · full drafts · tables. Omitted when nil — the
	// server's minLength is 1, so an empty detail is never sent.
	Detail   *string `json:"detail,omitempty"`
	ParentID *string `json:"parent_id,omitempty"`
	// AttachmentIDs is openapi v0.3.7 MessageCreate.attachment_ids (PRD
	// FR-3.7, colab-cli v0.9.6 `--attach`): artifacts of the same room shown
	// under the message. Omitted when empty.
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
}

// MessagePartCreate · MessageGroupCreate · MessageGroupPostResult — openapi
// v0.3.6 postMessageGroup (colab-cli v0.9.5 --parts-file / MCP parts).
type MessagePartCreate struct {
	To      []string `json:"to"`
	Content string   `json:"content"`
	Detail  *string  `json:"detail,omitempty"`
	// AttachmentIDs is v0.3.7 MessagePartCreate.attachment_ids: this part's
	// files, by the same rules as MessageCreate's.
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
}

type MessageGroupCreate struct {
	Parts    []MessagePartCreate `json:"parts"`
	ParentID *string             `json:"parent_id,omitempty"`
}

type MessageGroupPostResult struct {
	GroupID string              `json:"group_id"`
	Parts   []MessagePostResult `json:"parts"`
}

// Message — openapi.yaml Message. Fields the CLI surfaces are typed; the
// rest is kept in Raw so nothing the server sends is lost on --json output.
type Message struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	AuthorType string         `json:"author_type"`
	AuthorID   *string        `json:"author_id"`
	Author     *MessageAuthor `json:"author,omitempty"`
	ParentID   *string        `json:"parent_id"`
	Content    string         `json:"content"`
	// Detail is an agent message's work layer (openapi v0.3.1 Message.detail),
	// carried whole: `colab room messages --thread <id>` is where an agent
	// reads what its turn prompt showed only the first 400 characters of
	// (harness v0.9.4). null for people's and system messages.
	Detail   *string         `json:"detail,omitempty"`
	Mentions json.RawMessage `json:"mentions,omitempty"`
	// Attachments is openapi v0.3.7 Message.attachments (colab-cli v0.9.6:
	// `room messages` carries them) — each an AttachmentRef, verbatim; fetch
	// one with `artifact get <artifact_id> --out <path>`.
	Attachments json.RawMessage `json:"attachments,omitempty"`
	// Speech · Addressees · RespondsToMessageID · DelegatedLaneID are openapi
	// v0.3.2 (D24, PRD FR-3.1.3): the server says WHO said WHAT to WHOM, so
	// `room messages` shows the same answer the web timeline does instead of
	// each client re-deriving it from the body. Carried through verbatim —
	// the CLI does not interpret them.
	Speech              string          `json:"speech,omitempty"`
	Addressees          json.RawMessage `json:"addressees,omitempty"`
	RespondsToMessageID *string         `json:"responds_to_message_id,omitempty"`
	DelegatedLaneID     *string         `json:"delegated_lane_id,omitempty"`
	SourceTaskID        *string         `json:"source_task_id"`
	LaneID              *string         `json:"lane_id,omitempty"`
	Kind                string          `json:"kind"`
	State               string          `json:"state"`
	ReplyCount          int             `json:"reply_count,omitempty"`
	IsNote              bool            `json:"is_note,omitempty"`
	CreatedAt           string          `json:"created_at"`
	EditedAt            *string         `json:"edited_at,omitempty"`
	// GroupID · GroupIndex · GroupSize are openapi v0.3.6 (D26, PRD
	// FR-3.1.4): the part message this row is one part of. `room messages`
	// shows them (colab-cli v0.9.5); null for an ordinary message.
	GroupID    *string `json:"group_id"`
	GroupIndex *int    `json:"group_index"`
	GroupSize  *int    `json:"group_size,omitempty"`
}

type MessageAuthor struct {
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	Role      string  `json:"role,omitempty"`
}

// MessagePage — GET /rooms/{R}/messages.
type MessagePage struct {
	Items         []Message `json:"items"`
	BeforeCursor  *string   `json:"before_cursor"`
	AfterCursor   *string   `json:"after_cursor"`
	HasMoreBefore bool      `json:"has_more_before"`
	HasMoreAfter  bool      `json:"has_more_after"`
	Total         *int      `json:"total"`
}

// Trigger — MessagePostResult.triggers[].
type Trigger struct {
	AgentID       string  `json:"agent_id"`
	TaskID        string  `json:"task_id"`
	LaneID        string  `json:"lane_id"`
	Coalesced     bool    `json:"coalesced"`
	DeferredUntil *string `json:"deferred_until,omitempty"`
}

// Warning codes — openapi.yaml MessagePostResult.warnings[].code enum
// (colab-cli.md §2.2): not_participant · suppressed_delegator ·
// loop_limit_near · agent_disabled.
const (
	WarningNotParticipant      = "not_participant"
	WarningSuppressedDelegator = "suppressed_delegator" // rule 8
	WarningLoopLimitNear       = "loop_limit_near"
	WarningAgentDisabled       = "agent_disabled"
)

// Warning — MessagePostResult.warnings[].
type Warning struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	AgentID *string `json:"agent_id,omitempty"`
}

// MessagePostResult — POST /rooms/{R}/messages 201 body (openapi.yaml).
// `Triggered`/`Suppressed` are the colab-cli.md §2.2 convenience names; a
// server that emits them directly is accepted, otherwise they are derived
// from triggers[]/warnings[] (see colab.summarize).
type MessagePostResult struct {
	Message       Message   `json:"message"`
	Triggers      []Trigger `json:"triggers"`
	Warnings      []Warning `json:"warnings"`
	SessionPaused *string   `json:"session_paused,omitempty"`
	Triggered     []string  `json:"triggered,omitempty"`
	Suppressed    []string  `json:"suppressed,omitempty"`
}
