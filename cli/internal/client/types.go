package client

import "encoding/json"

// Problem — openapi.yaml components.schemas.Problem (RFC 9457 + code + errors).
type Problem struct {
	Type   string `json:"type,omitempty"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code,omitempty"`
	Errors []struct {
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
	DelegatedFromTaskID        *string       `json:"delegated_from_task_id"`
	SuppressedDelegatorAgentID *string       `json:"suppressed_delegator_agent_id"`
	OpenHitlRequestID          *string       `json:"open_hitl_request_id"`
	Participants               []Participant `json:"participants"`
	ExpiresAt                  string        `json:"expires_at"`
}

// Participant — CliContext.participants[].
type Participant struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"`
	MentionLink string `json:"mention_link"`
}

// MessageCreate — POST /sessions/{S}/messages body.
type MessageCreate struct {
	Content  string  `json:"content"`
	ParentID *string `json:"parent_id,omitempty"`
}

// Message — openapi.yaml Message. Fields the CLI surfaces are typed; the
// rest is kept in Raw so nothing the server sends is lost on --json output.
type Message struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	AuthorType   string          `json:"author_type"`
	AuthorID     *string         `json:"author_id"`
	Author       *MessageAuthor  `json:"author,omitempty"`
	ParentID     *string         `json:"parent_id"`
	Content      string          `json:"content"`
	Mentions     json.RawMessage `json:"mentions,omitempty"`
	SourceTaskID *string         `json:"source_task_id"`
	LaneID       *string         `json:"lane_id,omitempty"`
	Kind         string          `json:"kind"`
	State        string          `json:"state"`
	ReplyCount   int             `json:"reply_count,omitempty"`
	IsNote       bool            `json:"is_note,omitempty"`
	CreatedAt    string          `json:"created_at"`
	EditedAt     *string         `json:"edited_at,omitempty"`
}

type MessageAuthor struct {
	Name      string  `json:"name"`
	AvatarURL *string `json:"avatar_url,omitempty"`
	Role      string  `json:"role,omitempty"`
}

// MessagePage — GET /sessions/{S}/messages.
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

// Warning — MessagePostResult.warnings[].
type Warning struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	AgentID *string `json:"agent_id,omitempty"`
}

// MessagePostResult — POST /sessions/{S}/messages 201 body (openapi.yaml).
// `Triggered`/`Suppressed` are the colab-cli.md §2.2 names; a server that
// emits them directly is accepted, otherwise they are derived from
// triggers[]/warnings[] (see colab.PostResult).
type MessagePostResult struct {
	Message       Message   `json:"message"`
	Triggers      []Trigger `json:"triggers"`
	Warnings      []Warning `json:"warnings"`
	SessionPaused *string   `json:"session_paused,omitempty"`
	Triggered     []string  `json:"triggered,omitempty"`
	Suppressed    []string  `json:"suppressed,omitempty"`
}
