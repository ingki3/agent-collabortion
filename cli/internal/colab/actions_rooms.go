package colab

import (
	"context"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// The room commands of colab-cli.md v0.8 §2.4a (PRD v0.19 FR-4.5 · FR-2A.1).
// `room get` · `room messages` are the old `session get` · `session messages`
// under the room name (a room's id IS the session id) — same request, same
// JSON, same gate (session_get · session_messages), until R4 retires the old
// names. `room list` · `room read` · `work propose` are new commands with
// their own ColabCommand.

// RoomGetArgs — `colab room get [--room R]` / colab_room_get.
type RoomGetArgs struct {
	Room string `json:"room,omitempty"`
}

// RoomGet is SessionGet (GET /sessions/{S}).
func RoomGet(ctx context.Context, c *client.Client, a RoomGetArgs) (map[string]any, error) {
	return SessionGet(ctx, c, SessionGetArgs{Session: a.Room})
}

// RoomMessagesArgs — `colab room messages [--since --limit --thread --work]`.
type RoomMessagesArgs struct {
	Room   string `json:"room,omitempty"`
	Since  string `json:"since,omitempty"`
	Limit  *int   `json:"limit,omitempty"`
	Thread string `json:"thread,omitempty"`
	Work   string `json:"work,omitempty"` // only this mission's messages (listMessages work_id)
}

// RoomMessages is SessionMessages (GET /sessions/{S}/messages) plus --work.
func RoomMessages(ctx context.Context, c *client.Client, a RoomMessagesArgs) (*SessionMessagesResult, error) {
	return SessionMessages(ctx, c, SessionMessagesArgs{Session: a.Room, Since: a.Since, Limit: a.Limit, Thread: a.Thread, Work: a.Work})
}

// RoomListArgs — `colab room list [--query <말>]` / colab_room_list.
type RoomListArgs struct {
	Query string `json:"query,omitempty"`
}

// RoomList — GET /cli/rooms. Only the rooms this turn may read (FR-4.5); the
// answer is the server's `{items: [ReadableRoom]}` as sent.
func RoomList(ctx context.Context, c *client.Client, a RoomListArgs) (map[string]any, error) {
	if err := c.Allow(ctx, client.CmdRoomList); err != nil {
		return nil, err
	}
	return c.ListReadableRooms(ctx, a.Query)
}

// RoomReadArgs — `colab room read --room <id> [--tail N] [--query <말>]`.
type RoomReadArgs struct {
	Room  string `json:"room"`
	Tail  *int   `json:"tail,omitempty"` // 1..100 (nil = server default 30)
	Query string `json:"query,omitempty"`
}

// RoomRead — GET /cli/rooms/{id}/read. The RoomReadResult as sent, so
// `truncated` is in the output exactly as the server decided it (§2.4a). A
// refusal is exit 3 room_read_denied with `denied_reason` top-level in the
// error (client.problemError). Read-only: nothing is recorded by the CLI —
// the server logs the read in both rooms.
func RoomRead(ctx context.Context, c *client.Client, a RoomReadArgs) (map[string]any, error) {
	if strings.TrimSpace(a.Room) == "" {
		return nil, client.Usage("--room is required")
	}
	tail := 0
	if a.Tail != nil {
		if *a.Tail < 1 || *a.Tail > 100 {
			return nil, client.Usage("--tail must be 1..100 (got %d)", *a.Tail)
		}
		tail = *a.Tail
	}
	if err := c.Allow(ctx, client.CmdRoomRead); err != nil {
		return nil, err
	}
	return c.ReadRoom(ctx, strings.TrimSpace(a.Room), tail, a.Query)
}

// WorkProposeArgs — `colab work propose --goal <목표> --why <근거>`.
type WorkProposeArgs struct {
	Goal           string `json:"goal"`
	Why            string `json:"why"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// WorkProposeResult — the proposal id first (§2.4a "제안 id"), then the
// WorkProposal as sent.
type WorkProposeResult struct {
	ProposalID string         `json:"proposal_id"`
	Proposal   map[string]any `json:"proposal"`
	Replayed   bool           `json:"replayed"`
}

// WorkPropose — POST /rooms/{S}/work-proposals. An agent cannot open a
// mission; a person confirms the proposal (FR-2A.1).
func WorkPropose(ctx context.Context, c *client.Client, a WorkProposeArgs) (*WorkProposeResult, error) {
	goal, why := strings.TrimSpace(a.Goal), strings.TrimSpace(a.Why)
	if goal == "" {
		return nil, client.Usage("--goal is required")
	}
	if why == "" {
		return nil, client.Usage("--why is required")
	}
	if err := c.Allow(ctx, client.CmdWorkPropose); err != nil {
		return nil, err
	}
	rid, err := c.SessionID(ctx, "") // always this turn's room: the server refuses any other
	if err != nil {
		return nil, err
	}
	p, replayed, err := c.CreateWorkProposal(ctx, rid, client.WorkProposalCreate{Goal: goal, Rationale: why}, a.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	id, _ := p["id"].(string)
	return &WorkProposeResult{ProposalID: id, Proposal: p, Replayed: replayed}, nil
}
