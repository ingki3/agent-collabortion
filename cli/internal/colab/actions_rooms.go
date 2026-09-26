package colab

import (
	"context"
	"strings"

	"github.com/ingki3/agent-collabortion/cli/internal/client"
)

// The room commands of colab-cli.md §2.4a (PRD v0.19 FR-4.5 · FR-2A.1).
// v0.9 (R4): `room get` · `room messages` are the only names — the old
// `session get` · `session messages` and every old session-scoped path are gone
// (openapi v0.3.0). `room get` is three reads: getRoom + getWork (the turn's
// mission, COLAB_WORK_ID) + listRoomParticipants, so nothing the old
// getSession answer carried is lost: goal · acceptance_criteria ·
// completion_progress · director come from the Work, isolation from the
// Room, the roster with its derived status from the participants.

// RoomGetArgs — `colab room get [--room R]` / colab_room_get.
type RoomGetArgs struct {
	Room string `json:"room,omitempty"`
}

// RoomGetResult — the three answers as the server sent them. Work is null
// outside a mission (no COLAB_WORK_ID) and when --room names a room other
// than this turn's (the turn's mission is not that room's).
type RoomGetResult struct {
	Room         map[string]any   `json:"room"`
	Work         map[string]any   `json:"work"`
	Participants []map[string]any `json:"participants"`
}

// RoomGet — GET /rooms/{R} + GET /works/{W} + GET /rooms/{R}/participants.
func RoomGet(ctx context.Context, c *client.Client, a RoomGetArgs) (*RoomGetResult, error) {
	if err := c.Allow(ctx, client.CmdRoomGet); err != nil {
		return nil, err
	}
	own, err := c.RoomID(ctx, "")
	if err != nil && a.Room == "" {
		return nil, err
	}
	rid := own
	if a.Room != "" {
		rid = a.Room
	}
	room, err := c.GetRoom(ctx, rid)
	if err != nil {
		return nil, err
	}
	out := &RoomGetResult{Room: room}
	if w := c.WorkID(); w != "" && rid == own {
		if out.Work, err = c.GetWork(ctx, w); err != nil {
			return nil, err
		}
	}
	if out.Participants, err = c.ListRoomParticipants(ctx, rid); err != nil {
		return nil, err
	}
	return out, nil
}

// RoomMessagesArgs — `colab room messages [--since --limit --thread --work --top-only]`.
type RoomMessagesArgs struct {
	Room   string `json:"room,omitempty"`
	Since  string `json:"since,omitempty"`  // sent as after=<cursor|message id>
	Limit  *int   `json:"limit,omitempty"`  // 1..200 (nil = server default 50; an explicit 0 is exit 2)
	Thread string `json:"thread,omitempty"` // thread root id
	Work   string `json:"work,omitempty"`   // only this mission's messages (listMessages work_id)
	// TopOnly drops thread replies (v0.9.1: replies are included by default).
	TopOnly bool `json:"top_only,omitempty"`
}

// RoomMessagesResult adds the E8-12 included/total/truncated view.
type RoomMessagesResult struct {
	RoomID        string           `json:"room_id"`
	Items         []client.Message `json:"items"`
	Included      int              `json:"included"`
	Total         *int             `json:"total"`
	Truncated     bool             `json:"truncated"`
	BeforeCursor  *string          `json:"before_cursor"`
	AfterCursor   *string          `json:"after_cursor"`
	HasMoreBefore bool             `json:"has_more_before"`
	HasMoreAfter  bool             `json:"has_more_after"`
}

// RoomMessages — GET /rooms/{R}/messages.
func RoomMessages(ctx context.Context, c *client.Client, a RoomMessagesArgs) (*RoomMessagesResult, error) {
	limit := 0
	if a.Limit != nil {
		if *a.Limit < 1 || *a.Limit > 200 {
			return nil, client.Usage("--limit must be 1..200 (got %d)", *a.Limit)
		}
		limit = *a.Limit
	}
	if err := c.Allow(ctx, client.CmdRoomMessages); err != nil {
		return nil, err
	}
	rid, err := c.RoomID(ctx, a.Room)
	if err != nil {
		return nil, err
	}
	page, err := c.ListMessages(ctx, rid, client.MessagesQuery{Since: a.Since, Limit: limit, Thread: a.Thread, Work: a.Work, TopOnly: a.TopOnly})
	if err != nil {
		return nil, err
	}
	items := page.Items
	if items == nil {
		items = []client.Message{}
	}
	return &RoomMessagesResult{
		RoomID: rid, Items: items, Included: len(items), Total: page.Total,
		Truncated:    page.HasMoreBefore || page.HasMoreAfter || (page.Total != nil && *page.Total > len(items)),
		BeforeCursor: page.BeforeCursor, AfterCursor: page.AfterCursor,
		HasMoreBefore: page.HasMoreBefore, HasMoreAfter: page.HasMoreAfter,
	}, nil
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

// WorkPropose — POST /rooms/{R}/work-proposals. An agent cannot open a
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
	rid, err := c.RoomID(ctx, "") // always this turn's room: the server refuses any other
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
