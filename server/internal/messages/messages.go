// Package messages reads and maps message rows (FR-3.1). Writing goes through
// router.Service.Post so every message passes the routing rules.
package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/nullj"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

var ErrNotFound = errors.New("messages: not found")

// Row is one message joined with its author's display data.
type Row struct {
	ID           uuid.UUID
	SessionID    uuid.UUID
	AuthorType   string
	AuthorID     *uuid.UUID
	AuthorName   *string
	AuthorAvatar *string
	AuthorRole   *string
	ParentID     *uuid.UUID
	Content      string
	Mentions     []gen.Mention
	SourceTaskID *uuid.UUID
	LaneID       *uuid.UUID
	Kind         string
	State        string
	ReplyCount   int
	CreatedAt    time.Time
	EditedAt     *time.Time
	// WorkID is the mission the message belongs to (openapi 0.2.0
	// Message.work_id, FR-3.1.1), nil for one outside any mission. The web's
	// mission chip filters by it — a row without it reads as 「미션 없음」.
	WorkID *uuid.UUID
	// Detail is the agent message's 작업 내용 layer (openapi v0.3.1
	// Message.detail, PRD FR-3.1.2), nil for a person's or the system's.
	// Only the message's own readers carry it — routing, the inbox, alerts,
	// search previews and the mission summary read Content alone.
	Detail *string
	// Speech · Addressees · RespondsTo · DelegatedLane are openapi v0.3.2
	// (D24, PRD FR-3.1.3) — decided by the server when the message is written
	// (speech.go) so every client reads the same answer. Speech is empty only
	// for rows written before the migration that the backfill could not see.
	Speech        string
	Addressees    []Addressee
	RespondsTo    *uuid.UUID
	DelegatedLane *uuid.UUID
	// HitlRequestID is the request a `hitl` card belongs to (openapi
	// Message.hitl_request_id), nil for every other kind.
	HitlRequestID *uuid.UUID
}

const selectMessage = `
	SELECT m.id, m.session_id, m.author_type, m.author_id,
	       COALESCE(u.display_name, a.name), COALESCE(u.avatar_url, a.avatar_url), a.role,
	       m.parent_id, m.content, m.mentions, m.source_task_id, t.lane_id, m.kind, m.state,
	       (SELECT count(*) FROM message r WHERE r.parent_id = m.id), m.created_at, m.edited_at,
	       m.work_id, m.detail, m.speech, m.addressees, m.responds_to_message_id, m.delegated_lane_id,
	       -- openapi Message.hitl_request_id ("kind=hitl일 때") — T-APPROVAL: the
	       -- web pairs a card with its request by it when the request is not in
	       -- its list yet. Only hitl cards look (the rest read NULL).
	       CASE WHEN m.kind = 'hitl' THEN (SELECT h.id FROM hitl_request h WHERE h.message_id = m.id ORDER BY h.created_at LIMIT 1) END
	FROM message m
	LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id
	LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
	LEFT JOIN task t ON t.id = m.source_task_id`

func scan(row pgx.Row) (*Row, error) {
	var m Row
	var mentions, addressees []byte
	var role, speech *string
	err := row.Scan(&m.ID, &m.SessionID, &m.AuthorType, &m.AuthorID, &m.AuthorName, &m.AuthorAvatar, &role,
		&m.ParentID, &m.Content, &mentions, &m.SourceTaskID, &m.LaneID, &m.Kind, &m.State, &m.ReplyCount, &m.CreatedAt, &m.EditedAt,
		&m.WorkID, &m.Detail, &speech, &addressees, &m.RespondsTo, &m.DelegatedLane, &m.HitlRequestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("messages: scan: %w", err)
	}
	m.AuthorRole = role
	if len(mentions) > 0 {
		_ = json.Unmarshal(mentions, &m.Mentions)
	}
	if m.Mentions == nil {
		m.Mentions = []gen.Mention{}
	}
	if speech != nil {
		m.Speech = *speech
	}
	if len(addressees) > 0 {
		_ = json.Unmarshal(addressees, &m.Addressees)
	}
	if m.Addressees == nil {
		m.Addressees = []Addressee{}
	}
	return &m, nil
}

func Get(ctx context.Context, q db.DBTX, id uuid.UUID) (*Row, error) {
	return scan(q.QueryRow(ctx, selectMessage+` WHERE m.id = $1`, id))
}

// ListOptions mirrors listMessages query parameters.
type ListOptions struct {
	Thread         *uuid.UUID
	IncludeReplies bool
	Kinds          []string
	Before         *uuid.UUID // older than this message
	After          *uuid.UUID // newer than this message
	Limit          int
	// openapi v0.2.0 (T-S-wt): the mission chip. WorkID keeps one mission's
	// messages, NoWork those of no mission (`work_id IS NULL`). Both are
	// WHERE clauses, so a page is `Limit` messages AFTER the filter — never a
	// page of 50 that the filter then empties.
	WorkID *uuid.UUID
	NoWork bool
	// Around centres the page on one message (the unread anchor, a quote):
	// AroundHalf before it, the message itself, AroundHalf after — the same
	// (created_at, id) order as the before/after cursors. Limit is ignored.
	Around *uuid.UUID
}

// AroundHalf is listMessages' around_message_id window on each side
// (openapi: 「위아래 25건」).
const AroundHalf = 25

// List returns messages in chronological order plus paging flags.
func List(ctx context.Context, q db.DBTX, sessionID uuid.UUID, o ListOptions) (items []*Row, hasBefore, hasAfter bool, total *int, err error) {
	if o.Limit <= 0 || o.Limit > 200 {
		o.Limit = 50
	}
	where := []string{"m.session_id = $1"}
	args := []any{sessionID}
	if o.Thread != nil {
		args = append(args, *o.Thread)
		where = append(where, fmt.Sprintf("(m.id = $%d OR m.parent_id = $%d)", len(args), len(args)))
	} else if !o.IncludeReplies {
		where = append(where, "m.parent_id IS NULL")
	}
	if len(o.Kinds) > 0 {
		args = append(args, o.Kinds)
		where = append(where, fmt.Sprintf("m.kind::text = ANY($%d)", len(args)))
	}
	if o.WorkID != nil {
		args = append(args, *o.WorkID)
		where = append(where, fmt.Sprintf("m.work_id = $%d", len(args)))
	} else if o.NoWork {
		where = append(where, "m.work_id IS NULL")
	}
	if o.Around != nil {
		return listAround(ctx, q, where, args, *o.Around)
	}
	if o.Before != nil {
		args = append(args, *o.Before)
		where = append(where, fmt.Sprintf("(m.created_at, m.id) < (SELECT created_at, id FROM message WHERE id = $%d)", len(args)))
	}
	if o.After != nil {
		args = append(args, *o.After)
		where = append(where, fmt.Sprintf("(m.created_at, m.id) > (SELECT created_at, id FROM message WHERE id = $%d)", len(args)))
	}
	order := "DESC"
	if o.After != nil {
		order = "ASC"
	}
	args = append(args, o.Limit+1)
	sql := selectMessage + " WHERE " + strings.Join(where, " AND ") +
		fmt.Sprintf(" ORDER BY m.created_at %s, m.id %s LIMIT $%d", order, order, len(args))
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, false, false, nil, fmt.Errorf("messages: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, false, false, nil, err
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, false, nil, err
	}
	more := len(items) > o.Limit
	if more {
		items = items[:o.Limit]
	}
	if order == "DESC" {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
		hasBefore = more
		hasAfter = o.Before != nil
	} else {
		hasAfter = more
		hasBefore = true
	}
	if o.Thread != nil {
		var n int
		if err := q.QueryRow(ctx, `SELECT count(*) FROM message WHERE id = $1 OR parent_id = $1`, *o.Thread).Scan(&n); err == nil {
			total = &n
		}
	}
	if items == nil {
		items = []*Row{}
	}
	return items, hasBefore, hasAfter, total, nil
}

// listAround is List's around_message_id page: the filtered messages up to
// AroundHalf older than the anchor, the anchor itself when the filter keeps
// it, and up to AroundHalf newer — chronological, with the flags saying
// whether the before/after cursors have more.
func listAround(ctx context.Context, q db.DBTX, where []string, args []any, anchor uuid.UUID) (items []*Row, hasBefore, hasAfter bool, total *int, err error) {
	args = append(args, anchor)
	a := len(args)
	page := func(cmp, order string, limit int) ([]*Row, error) {
		sql := selectMessage + " WHERE " + strings.Join(where, " AND ") +
			fmt.Sprintf(" AND (m.created_at, m.id) %s (SELECT created_at, id FROM message WHERE id = $%d)", cmp, a) +
			fmt.Sprintf(" ORDER BY m.created_at %s, m.id %s LIMIT %d", order, order, limit)
		rows, err := q.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("messages: list around: %w", err)
		}
		defer rows.Close()
		var out []*Row
		for rows.Next() {
			m, err := scan(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, m)
		}
		return out, rows.Err()
	}
	older, err := page("<", "DESC", AroundHalf+1)
	if err != nil {
		return nil, false, false, nil, err
	}
	self, err := page("=", "ASC", 1)
	if err != nil {
		return nil, false, false, nil, err
	}
	newer, err := page(">", "ASC", AroundHalf+1)
	if err != nil {
		return nil, false, false, nil, err
	}
	if hasBefore = len(older) > AroundHalf; hasBefore {
		older = older[:AroundHalf]
	}
	if hasAfter = len(newer) > AroundHalf; hasAfter {
		newer = newer[:AroundHalf]
	}
	items = make([]*Row, 0, len(older)+1+len(newer))
	for i := len(older) - 1; i >= 0; i-- {
		items = append(items, older[i])
	}
	items = append(items, self...)
	items = append(items, newer...)
	return items, hasBefore, hasAfter, nil, nil
}

// ToAPI maps a row to the contract Message.
func ToAPI(m *Row) gen.Message {
	out := gen.Message{
		Id:           m.ID,
		SessionId:    m.SessionID,
		AuthorType:   gen.AuthorType(m.AuthorType),
		AuthorId:     nullj.NullUUID(m.AuthorID),
		ParentId:     nullj.NullUUID(m.ParentID),
		Content:      m.Content,
		Mentions:     m.Mentions,
		SourceTaskId: nullj.NullUUID(m.SourceTaskID),
		LaneId:       nullj.NullUUID(m.LaneID),
		Kind:         gen.MessageKind(m.Kind),
		State:        gen.MessageState(m.State),
		ReplyCount:   &m.ReplyCount,
		CreatedAt:    m.CreatedAt,
		EditedAt:     nullj.NullTime(m.EditedAt),
		WorkId:       nullj.NullUUID(m.WorkID),
		Detail:       nullj.NullString(m.Detail),
		// T-APPROVAL: declared by the contract, never filled until now.
		HitlRequestId: nullj.NullUUID(m.HitlRequestID),
	}
	isNote := IsNote(m.Content)
	out.IsNote = &isNote
	// openapi v0.3.2 (D24). A row the backfill could not classify reads as
	// `chat` with no addressees — the honest 「방 전체」, never a guessed label.
	speech := gen.MessageSpeech(m.Speech)
	if m.Speech == "" {
		speech = gen.MessageSpeechChat
	}
	out.Speech = &speech
	addr := make([]struct {
		Id   nullable.Nullable[openapi_types.UUID] `json:"id,omitempty"`
		Kind gen.MessageAddresseesKind             `json:"kind"`
		Name string                                `json:"name"`
	}, 0, len(m.Addressees))
	for _, a := range m.Addressees {
		addr = append(addr, struct {
			Id   nullable.Nullable[openapi_types.UUID] `json:"id,omitempty"`
			Kind gen.MessageAddresseesKind             `json:"kind"`
			Name string                                `json:"name"`
		}{Id: nullj.NullUUID(a.ID), Kind: gen.MessageAddresseesKind(a.Kind), Name: a.Name})
	}
	out.Addressees = &addr
	out.RespondsToMessageId = nullj.NullUUID(m.RespondsTo)
	out.DelegatedLaneId = nullj.NullUUID(m.DelegatedLane)
	if m.AuthorName != nil {
		out.Author = &struct {
			AvatarUrl nullable.Nullable[string] `json:"avatar_url,omitempty"`
			Name      *string                   `json:"name,omitempty"`
			Role      *gen.AgentRole            `json:"role,omitempty"`
		}{AvatarUrl: nullj.NullString(m.AuthorAvatar), Name: m.AuthorName}
		if m.AuthorRole != nil {
			r := gen.AgentRole(*m.AuthorRole)
			out.Author.Role = &r
		}
	}
	return out
}

// Publish is the one place a stored message row becomes a `message.created`
// frame (openapi StreamEvent, S7). router.Post and router.DelegateLane
// publish inline because they already hold the mapped message; every OTHER
// insert — the session-start notice, the join bundle, a wake prefix, the
// blocked question card, the completion summary — went to the database and
// nowhere else, so S7 only saw them on reload (G4 2판 W10).
//
// q is the caller's transaction: the frame is delivered immediately and the
// stream_event row is written with the same tx as the message itself.
func Publish(ctx context.Context, hub *realtime.Hub, q db.DBTX, wsID, sessionID, msgID uuid.UUID) error {
	if hub == nil {
		return nil
	}
	m, err := Get(ctx, q, msgID)
	if err != nil {
		return err
	}
	sid := sessionID
	return hub.Publish(ctx, q, wsID, &sid, "message.created", ToAPI(m))
}
