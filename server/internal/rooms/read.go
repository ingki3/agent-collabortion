package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ingki3/agent-collabortion/contracts/clock"
	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/artifacts"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
	"github.com/ingki3/agent-collabortion/server/internal/router"
	"github.com/ingki3/agent-collabortion/server/internal/sessions"
	"github.com/ingki3/agent-collabortion/server/internal/tasks"
)

// Policy is contracts RoomReadPolicy (workspace_settings.room_read).
type Policy struct {
	MaxRoomsPerTurn int `json:"max_rooms_per_turn"`
	MaxTokens       int `json:"max_tokens"`
}

// DefaultPolicy is FR-4.5's defaults, used for a key the settings row lacks.
var DefaultPolicy = Policy{MaxRoomsPerTurn: 3, MaxTokens: 4000}

// DefaultTail is readRoom's `tail` default (openapi).
const DefaultTail = 30

type Service struct {
	DB        *pgxpool.Pool
	Clock     clock.Clock
	Hub       *realtime.Hub
	Router    *router.Service
	Artifacts *artifacts.Service
}

// Reader is the calling attempt, from its task token.
type Reader struct {
	TaskID  uuid.UUID
	Attempt int
	AgentID uuid.UUID
	RoomID  uuid.UUID // the room the task runs in
}

// reader is Reader plus what the record and the sentences need.
type reader struct {
	Reader
	WorkspaceID    uuid.UUID
	Originator     *uuid.UUID
	OriginatorName string
	AgentName      string
}

func loadReader(ctx context.Context, q db.DBTX, r Reader) (*reader, error) {
	out := &reader{Reader: r}
	var name *string
	err := q.QueryRow(ctx, `
		SELECT rm.workspace_id, t.originator_user_id, u.display_name, a.name
		FROM task t JOIN room rm ON rm.id = t.session_id JOIN agent a ON a.id = t.agent_id
		LEFT JOIN app_user u ON u.id = t.originator_user_id
		WHERE t.id = $1`, r.TaskID).Scan(&out.WorkspaceID, &out.Originator, &name, &out.AgentName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tasks.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: reader: %w", err)
	}
	if name != nil {
		out.OriginatorName = *name
	}
	return out, nil
}

// target is one candidate room with the facts about it.
type target struct {
	ID           uuid.UUID
	Name         string
	Description  string
	LastActivity *time.Time
	Facts        Facts
}

// factsSQL reads the FR-4.5 facts for rooms of the reader's workspace. $1
// workspace, $2 originator (may be NULL), $3 agent, $4 current room. A room
// of another workspace is simply not a row — the same as one that does not
// exist, which Judge answers originator_not_participant.
const factsSQL = `
	SELECT r.id, r.name, r.description,
	       (SELECT max(m.created_at) FROM message m WHERE m.session_id = r.id),
	       (SELECT CASE WHEN p.left_at IS NULL THEN 1 ELSE 2 END
	          FROM room_participant p WHERE p.room_id = r.id AND p.user_id = $2),
	       EXISTS (SELECT 1 FROM room_participant p WHERE p.room_id = r.id AND p.agent_id = $3 AND p.left_at IS NULL),
	       EXISTS (SELECT 1 FROM room_link k WHERE k.room_id = $4 AND k.target_room_id = r.id)
	FROM room r
	WHERE r.workspace_id = $1`

func scanTarget(row pgx.Row, hasOriginator bool) (*target, error) {
	var t target
	var standing *int
	if err := row.Scan(&t.ID, &t.Name, &t.Description, &t.LastActivity, &standing,
		&t.Facts.AgentParticipant, &t.Facts.Linked); err != nil {
		return nil, err
	}
	t.Facts.HasOriginator = hasOriginator
	if standing != nil {
		t.Facts.Originator = Membership(*standing)
	}
	return &t, nil
}

func loadTarget(ctx context.Context, q db.DBTX, rd *reader, id uuid.UUID) (*target, error) {
	t, err := scanTarget(q.QueryRow(ctx, factsSQL+` AND r.id = $5`,
		rd.WorkspaceID, rd.Originator, rd.AgentID, rd.RoomID, id), rd.Originator != nil)
	if errors.Is(err, pgx.ErrNoRows) {
		// Does not exist (or another workspace): the facts of a room nobody
		// is in. Judge must see the same thing it sees for a hidden room.
		return &target{ID: id, Facts: Facts{HasOriginator: rd.Originator != nil}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rooms: target: %w", err)
	}
	return t, nil
}

// LoadPolicy reads workspace_settings.room_read; a missing key takes the
// FR-4.5 default.
func LoadPolicy(ctx context.Context, q db.DBTX, wsID uuid.UUID) (Policy, error) {
	p := DefaultPolicy
	var raw []byte
	err := q.QueryRow(ctx, `SELECT room_read FROM workspace_settings WHERE workspace_id = $1`, wsID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, fmt.Errorf("rooms: policy: %w", err)
	}
	var got struct {
		MaxRoomsPerTurn *int `json:"max_rooms_per_turn"`
		MaxTokens       *int `json:"max_tokens"`
	}
	_ = json.Unmarshal(raw, &got)
	if got.MaxRoomsPerTurn != nil && *got.MaxRoomsPerTurn > 0 {
		p.MaxRoomsPerTurn = *got.MaxRoomsPerTurn
	}
	if got.MaxTokens != nil && *got.MaxTokens > 0 {
		p.MaxTokens = *got.MaxTokens
	}
	return p, nil
}

// Readable is one `colab room list` row.
type Readable struct {
	ID                 uuid.UUID
	Name               string
	Description        string
	LastActivityAt     *time.Time
	AgentIsParticipant bool
	ViaLink            bool
}

// ListReadable is listReadableRooms: the rooms Judge allows right now, the
// current room excluded ("다른 방"). A turn with no originator reads nothing,
// so it lists nothing — the list must not show what read would refuse.
func (s *Service) ListReadable(ctx context.Context, r Reader, query string) ([]Readable, error) {
	rd, err := loadReader(ctx, s.DB, r)
	if err != nil {
		return nil, err
	}
	out := []Readable{}
	if rd.Originator == nil {
		return out, nil
	}
	// Only rooms the originator has a row in can pass condition 1; the rest
	// are not read at all. Judge still decides every row that is.
	sql := factsSQL + ` AND r.id <> $4 AND EXISTS (SELECT 1 FROM room_participant p WHERE p.room_id = r.id AND p.user_id = $2)`
	args := []any{rd.WorkspaceID, rd.Originator, rd.AgentID, rd.RoomID}
	if q := strings.TrimSpace(query); q != "" {
		args = append(args, "%"+likeEscape(q)+"%")
		sql += fmt.Sprintf(` AND (r.name ILIKE $%d OR r.description ILIKE $%d)`, len(args), len(args))
	}
	rows, err := s.DB.Query(ctx, sql+` ORDER BY r.name, r.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("rooms: list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanTarget(rows, true)
		if err != nil {
			return nil, fmt.Errorf("rooms: list scan: %w", err)
		}
		v := Judge(t.Facts)
		if !v.Allowed {
			continue
		}
		out = append(out, Readable{ID: t.ID, Name: t.Name, Description: t.Description, LastActivityAt: t.LastActivity,
			AgentIsParticipant: t.Facts.AgentParticipant, ViaLink: v.ViaLink})
	}
	return out, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Result is readRoom's body before it is mapped to the contract.
type Result struct {
	RoomID      uuid.UUID
	Name        string
	Description string
	Summary     *string
	Messages    []*messages.Row
	Decisions   []sessions.DecisionRow
	Artifacts   []*artifacts.Row
	Truncated   bool
	// Capped: the turn had already read max_rooms_per_turn other rooms, so
	// nothing was read (Truncated is true too).
	Capped bool
}

// Denial is a refused read. RoomName is set only for originator_left.
type Denial struct {
	Reason         Reason
	RoomName       string
	OriginatorName string
	AgentName      string
}

// ErrSelf is a read of the room the task is running in — that is not
// "another room", and it would put one log row on both sides of S23.
var ErrSelf = errors.New("rooms: the current room")

// Read is readRoom. Exactly one of the results is non-nil on success.
func (s *Service) Read(ctx context.Context, r Reader, targetID uuid.UUID, tail int, query string) (*Result, *Denial, error) {
	if targetID == r.RoomID {
		return nil, nil, ErrSelf
	}
	if tail <= 0 {
		tail = DefaultTail
	}
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// One read of this attempt at a time: the per-turn room count is a
	// read-then-write. An advisory lock rather than the task row, which the
	// daemon's heartbeat and finish contend for.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"room_read:"+r.TaskID.String()); err != nil {
		return nil, nil, fmt.Errorf("rooms: lock: %w", err)
	}
	rd, err := loadReader(ctx, tx, r)
	if err != nil {
		return nil, nil, err
	}
	t, err := loadTarget(ctx, tx, rd, targetID)
	if err != nil {
		return nil, nil, err
	}
	v := Judge(t.Facts)
	if !v.Allowed {
		d := &Denial{Reason: v.Reason, OriginatorName: rd.OriginatorName, AgentName: rd.AgentName}
		if v.RevealRoom {
			d.RoomName = t.Name
		}
		if err := s.recordDenied(ctx, tx, rd, t, v, d, now); err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return nil, d, nil
	}

	pol, err := LoadPolicy(ctx, tx, rd.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	res := &Result{RoomID: t.ID, Name: t.Name, Description: t.Description,
		Messages: []*messages.Row{}, Decisions: []sessions.DecisionRow{}, Artifacts: []*artifacts.Row{}}
	others, again, err := roomsReadThisTurn(ctx, tx, r, targetID)
	if err != nil {
		return nil, nil, err
	}
	if !again && others >= pol.MaxRoomsPerTurn {
		// FR-4.5 「넘으면 잘라 주고 그 사실을 알린다」: nothing is read, so
		// nothing is recorded as read in either room — the agent's own feed
		// says why the answer is empty.
		res.Truncated, res.Capped = true, true
		note := fmt.Sprintf("이번 턴에 읽을 수 있는 방 %d개를 이미 읽어 「%s」 방은 읽지 않았습니다", pol.MaxRoomsPerTurn, t.Name)
		if err := tasks.InsertServerEvent(ctx, tx, r.TaskID, r.Attempt, "status", "read", t.ID.String(), "ok",
			map[string]any{"command": "room read", "args": map[string]any{"note": note}}, now); err != nil {
			return nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, err
		}
		return res, nil, nil
	}

	if err := s.fill(ctx, tx, res, tail, query, pol.MaxTokens); err != nil {
		return nil, nil, err
	}
	if err := s.recordAllowed(ctx, tx, rd, t, res, now); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return res, nil, nil
}

// roomsReadThisTurn counts the OTHER rooms this attempt has read, and whether
// it has already read this one (a second look costs no slot). The turn starts
// at the attempt's dispatch; an attempt with no row yet counts from the task's
// first read.
func roomsReadThisTurn(ctx context.Context, q db.DBTX, r Reader, targetID uuid.UUID) (int, bool, error) {
	var n int
	var again bool
	err := q.QueryRow(ctx, `
		SELECT count(DISTINCT target_room_id) FILTER (WHERE target_room_id <> $3),
		       COALESCE(bool_or(target_room_id = $3), false)
		FROM room_read_log
		WHERE reader_task_id = $1 AND allowed
		  AND created_at >= COALESCE((SELECT dispatched_at FROM task_attempt WHERE task_id = $1 AND attempt = $2), '-infinity')`,
		r.TaskID, r.Attempt, targetID).Scan(&n, &again)
	if err != nil {
		return 0, false, fmt.Errorf("rooms: turn count: %w", err)
	}
	return n, again, nil
}

// fill reads the target room within max_tokens. The budget is spent in the
// order an agent needs it: the latest summary, then decisions (newest first),
// then the artifact list (names are cheap), then the message tail (newest
// first). Whatever does not fit is dropped whole — a half message reads as a
// different message — and Truncated says so. The summary alone is cut, on a
// line boundary, because it is one block written to be read in part.
func (s *Service) fill(ctx context.Context, q db.DBTX, res *Result, tail int, query string, budget int) error {
	query = strings.ToLower(strings.TrimSpace(query))
	match := func(parts ...string) bool {
		if query == "" {
			return true
		}
		for _, p := range parts {
			if strings.Contains(strings.ToLower(p), query) {
				return true
			}
		}
		return false
	}

	var summary string
	err := q.QueryRow(ctx, `
		SELECT content FROM message WHERE session_id = $1 AND kind = 'summary' AND state = 'posted'
		ORDER BY created_at DESC, id DESC LIMIT 1`, res.RoomID).Scan(&summary)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return fmt.Errorf("rooms: summary: %w", err)
	default:
		cut, trimmed := llm.TrimToTokens(summary, budget)
		budget -= llm.EstimateTokens(cut)
		res.Truncated = res.Truncated || trimmed
		if cut != "" {
			res.Summary = &cut
		}
	}

	rows, err := q.Query(ctx, `
		SELECT id, summary, rationale, source::text, ref_id, created_at
		FROM decision WHERE session_id = $1 ORDER BY created_at DESC, id DESC`, res.RoomID)
	if err != nil {
		return fmt.Errorf("rooms: decisions: %w", err)
	}
	var decs []sessions.DecisionRow
	for rows.Next() {
		var d sessions.DecisionRow
		if err := rows.Scan(&d.ID, &d.Summary, &d.Rationale, &d.Source, &d.RefID, &d.CreatedAt); err != nil {
			rows.Close()
			return err
		}
		decs = append(decs, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range decs {
		rat := ""
		if d.Rationale != nil {
			rat = *d.Rationale
		}
		if !match(d.Summary, rat) {
			continue
		}
		cost := llm.EstimateTokens(d.Summary) + llm.EstimateTokens(rat)
		if cost > budget {
			res.Truncated = true
			break
		}
		budget -= cost
		res.Decisions = append(res.Decisions, d)
	}
	reverse(res.Decisions)

	arts, err := s.Artifacts.List(ctx, res.RoomID, artifacts.ListOptions{LatestOnly: true})
	if err != nil {
		return fmt.Errorf("rooms: artifacts: %w", err)
	}
	for i := len(arts) - 1; i >= 0; i-- {
		a := arts[i]
		desc := ""
		if a.Description != nil {
			desc = *a.Description
		}
		if !match(a.Name, desc) {
			continue
		}
		cost := llm.EstimateTokens(a.Name) + llm.EstimateTokens(desc) + 1
		if cost > budget {
			res.Truncated = true
			break
		}
		budget -= cost
		res.Artifacts = append(res.Artifacts, a)
	}
	reverse(res.Artifacts)

	limit := tail
	if query != "" {
		limit = 200 // filter a wider window, then keep the tail of what matched
	}
	msgs, _, _, _, err := messages.List(ctx, q, res.RoomID, messages.ListOptions{
		IncludeReplies: true, Kinds: []string{"text", "hitl", "blocked_q", "system"}, Limit: limit})
	if err != nil {
		return fmt.Errorf("rooms: messages: %w", err)
	}
	for i := len(msgs) - 1; i >= 0 && len(res.Messages) < tail; i-- {
		m := msgs[i]
		if !match(m.Content) {
			continue
		}
		cost := llm.EstimateTokens(m.Content) + 1
		if cost > budget {
			res.Truncated = true
			break
		}
		budget -= cost
		res.Messages = append(res.Messages, m)
	}
	reverse(res.Messages)
	return nil
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// scopeText is 「요약 + 최근 N건」 — what was read, in the words of SCREEN
// §8.1 system message 2.
func scopeText(res *Result) string {
	if res.Summary != nil {
		return fmt.Sprintf("요약 + 최근 %d건", len(res.Messages))
	}
	return fmt.Sprintf("최근 %d건", len(res.Messages))
}

// recordAllowed leaves the read in both rooms (FR-4.5 「읽은 사실을 남긴다」):
// the room_read_log row both S23 sides read, the reading task's feed, an
// activity line in each room, the read room's timeline line, and one
// room_read.recorded frame per room.
func (s *Service) recordAllowed(ctx context.Context, tx pgx.Tx, rd *reader, t *target, res *Result, now time.Time) error {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO room_read_log (room_id, reader_task_id, reader_agent_id, originator_user_id, target_room_id,
		                           allowed, summary_bytes, recent_n, truncated, created_at)
		VALUES ($1, $2, $3, $4, $5, true, $6, $7, $8, $9) RETURNING id`,
		rd.RoomID, rd.TaskID, rd.AgentID, rd.Originator, t.ID,
		summaryBytes(res), len(res.Messages), res.Truncated, now).Scan(&id); err != nil {
		return fmt.Errorf("rooms: log: %w", err)
	}
	scope := scopeText(res)
	note := fmt.Sprintf("「%s」 방을 읽었습니다(%s)", t.Name, scope)
	if res.Truncated {
		note += " — 분량 상한으로 일부만 읽었습니다"
	}
	if err := tasks.InsertServerEvent(ctx, tx, rd.TaskID, rd.Attempt, "status", "read", t.ID.String(), "ok",
		map[string]any{"command": "room read", "result_ref": id.String(), "args": map[string]any{"note": note}}, now); err != nil {
		return err
	}
	for _, side := range []struct {
		room, other uuid.UUID
		dir         string
	}{{rd.RoomID, t.ID, "out"}, {t.ID, rd.RoomID, "in"}} {
		if err := activity(ctx, tx, rd, side.room, "room.read", &side.other, map[string]any{
			"direction": side.dir, "room_read_id": id, "task_id": rd.TaskID, "originator_user_id": rd.Originator,
			"recent_n": len(res.Messages), "summary": res.Summary != nil, "truncated": res.Truncated,
		}, now); err != nil {
			return err
		}
	}
	// SCREEN §8.1 system message 2: the READ side's people must see that
	// their room's context left it.
	line := fmt.Sprintf("%s의 요청으로 %s 이 방을 읽었습니다(%s).",
		rd.OriginatorName, apperr.Josa("@"+rd.AgentName, "이", "가"), scope)
	if _, err := s.Router.SystemPost(ctx, tx, t.ID, line); err != nil {
		return err
	}
	return s.publish(ctx, tx, rd, id, []pubSide{{rd.RoomID, "out"}, {t.ID, "in"}})
}

func summaryBytes(res *Result) int {
	if res.Summary == nil {
		return 0
	}
	return len(*res.Summary)
}

// recordDenied is FR-4.5 「거부도 말한다」: the attempt's feed and the READING
// room's activity say an unreadable room was asked for — without naming it,
// except for originator_left, whose sentence names the room so the person
// who left can see what their leaving blocked (SCREEN §4.13, G-7).
func (s *Service) recordDenied(ctx context.Context, tx pgx.Tx, rd *reader, t *target, v Verdict, d *Denial, now time.Time) error {
	// The target id is kept only when the room exists in this workspace; the
	// API hides it anyway (other_room: null) unless the reason reveals it.
	var targetCol *uuid.UUID
	if t.Name != "" {
		id := t.ID
		targetCol = &id
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO room_read_log (room_id, reader_task_id, reader_agent_id, originator_user_id, target_room_id,
		                           allowed, denied_reason, created_at)
		VALUES ($1, $2, $3, $4, $5, false, $6, $7) RETURNING id`,
		rd.RoomID, rd.TaskID, rd.AgentID, rd.Originator, targetCol, string(v.Reason), now).Scan(&id); err != nil {
		return fmt.Errorf("rooms: denied log: %w", err)
	}
	note := DeniedActivityText(d)
	objectRef := "room_read"
	var object *uuid.UUID
	if v.RevealRoom {
		objectRef, object = t.ID.String(), targetCol
	}
	if err := tasks.InsertServerEvent(ctx, tx, rd.TaskID, rd.Attempt, "status", "read", objectRef, "rejected",
		map[string]any{"command": "room read", "rejected_reason": "room_read_denied",
			"args": map[string]any{"note": note, "denied_reason": string(v.Reason)}}, now); err != nil {
		return err
	}
	if err := activity(ctx, tx, rd, rd.RoomID, "room.read.denied", object, map[string]any{
		"direction": "denied", "room_read_id": id, "task_id": rd.TaskID, "originator_user_id": rd.Originator,
		"denied_reason": string(v.Reason), "note": note,
	}, now); err != nil {
		return err
	}
	return s.publish(ctx, tx, rd, id, []pubSide{{rd.RoomID, "denied"}})
}

// DeniedActivityText is the reading room's activity line for a refusal
// (PRD FR-4.5). The originator_left sentence is the PRD's own, word for word.
func DeniedActivityText(d *Denial) string {
	if d.Reason == OriginatorLeft {
		return fmt.Sprintf("%s %s 방을 떠나 @%s의 참고 읽기가 막혔습니다",
			apperr.Josa(d.OriginatorName, "이", "가"), d.RoomName, d.AgentName)
	}
	return fmt.Sprintf("%s 읽을 수 없는 방을 조회했습니다", apperr.Josa("@"+d.AgentName, "이", "가"))
}

func activity(ctx context.Context, tx pgx.Tx, rd *reader, room uuid.UUID, action string, object *uuid.UUID, payload map[string]any, now time.Time) error {
	objType := any(nil)
	if object != nil {
		objType = "room"
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, session_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
		VALUES ($1, $2, 'agent', $3, $4, $5, $6, $7, $8)`,
		rd.WorkspaceID, room, rd.AgentID, action, objType, object, payload, now); err != nil {
		return fmt.Errorf("rooms: activity %s: %w", action, err)
	}
	return nil
}

type pubSide struct {
	room uuid.UUID
	dir  string
}

// publish sends room_read.recorded to each side's room with the entry as that
// side's S23 shows it.
func (s *Service) publish(ctx context.Context, tx pgx.Tx, rd *reader, id uuid.UUID, sides []pubSide) error {
	if s.Hub == nil {
		return nil
	}
	for _, side := range sides {
		entry, err := loadEntry(ctx, tx, id, side.room, side.dir)
		if err != nil {
			return err
		}
		room := side.room
		if err := s.Hub.Publish(ctx, tx, rd.WorkspaceID, &room, "room_read.recorded", map[string]any{
			"room_id": side.room, "direction": side.dir, "entry": entry,
		}); err != nil {
			return err
		}
	}
	return nil
}
