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

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
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
}

const selectMessage = `
	SELECT m.id, m.session_id, m.author_type, m.author_id,
	       COALESCE(u.display_name, a.name), COALESCE(u.avatar_url, a.avatar_url), a.role,
	       m.parent_id, m.content, m.mentions, m.source_task_id, t.lane_id, m.kind, m.state,
	       (SELECT count(*) FROM message r WHERE r.parent_id = m.id), m.created_at, m.edited_at
	FROM message m
	LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id
	LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
	LEFT JOIN task t ON t.id = m.source_task_id`

func scan(row pgx.Row) (*Row, error) {
	var m Row
	var mentions []byte
	var role *string
	err := row.Scan(&m.ID, &m.SessionID, &m.AuthorType, &m.AuthorID, &m.AuthorName, &m.AuthorAvatar, &role,
		&m.ParentID, &m.Content, &mentions, &m.SourceTaskID, &m.LaneID, &m.Kind, &m.State, &m.ReplyCount, &m.CreatedAt, &m.EditedAt)
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
}

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

// ToAPI maps a row to the contract Message.
func ToAPI(m *Row) gen.Message {
	out := gen.Message{
		Id:           m.ID,
		SessionId:    m.SessionID,
		AuthorType:   gen.AuthorType(m.AuthorType),
		AuthorId:     tasks.NullUUID(m.AuthorID),
		ParentId:     tasks.NullUUID(m.ParentID),
		Content:      m.Content,
		Mentions:     m.Mentions,
		SourceTaskId: tasks.NullUUID(m.SourceTaskID),
		LaneId:       tasks.NullUUID(m.LaneID),
		Kind:         gen.MessageKind(m.Kind),
		State:        gen.MessageState(m.State),
		ReplyCount:   &m.ReplyCount,
		CreatedAt:    m.CreatedAt,
		EditedAt:     tasks.NullTime(m.EditedAt),
	}
	isNote := strings.HasPrefix(m.Content, "/note")
	out.IsNote = &isNote
	if m.AuthorName != nil {
		out.Author = &struct {
			AvatarUrl nullable.Nullable[string] `json:"avatar_url,omitempty"`
			Name      *string                   `json:"name,omitempty"`
			Role      *gen.AgentRole            `json:"role,omitempty"`
		}{AvatarUrl: tasks.NullString(m.AuthorAvatar), Name: m.AuthorName}
		if m.AuthorRole != nil {
			r := gen.AgentRole(*m.AuthorRole)
			out.Author.Role = &r
		}
	}
	return out
}
