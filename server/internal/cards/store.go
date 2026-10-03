package cards

// store.go is the database half: the task_card row (migration 0046), its
// contract shape (openapi TaskCard · CardBoard), and the writes every path
// shares — the result (agent's or automatic), cancellation, the summary a
// mission's completion approval carries. The router and the handlers call
// these; lanedone's CARD GATE calls Gate.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/nullj"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// ErrNotFound is a card that does not exist.
var ErrNotFound = errors.New("cards: card not found")

// Row is one task_card row with the names the contract shows.
type Row struct {
	ID, RoomID           uuid.UUID
	WorkID               *uuid.UUID
	Number, Version      int
	Status               string
	DelegatorID          uuid.UUID
	DelegatorName        string
	DelegatorTaskID      *uuid.UUID
	AssigneeID           uuid.UUID
	AssigneeName         string
	LaneID               uuid.UUID
	ParentCardID         *uuid.UUID
	Goal                 string
	Criteria             []Criterion
	Boundaries           string
	Refs                 []Ref
	OutputFormat         *string
	BudgetUSD            *float64
	ReviseReason         *string
	DelegateMessageID    *uuid.UUID
	Result               json.RawMessage // CardResult shape, nil = none
	Judgement            json.RawMessage // CardJudgement shape, nil = none
	FollowUps            int
	Versions             json.RawMessage
	CreatedAt, UpdatedAt time.Time
}

// Label is 「C-n」.
func (r *Row) Label() string { return Label(r.Number) }

const selectCard = `
	SELECT c.id, c.room_id, c.work_id, c.number, c.version, c.status,
	       c.delegator_agent_id, da.name, c.delegator_task_id, c.assignee_agent_id, aa.name,
	       c.lane_id, c.parent_card_id, c.goal, c.criteria, c.boundaries, c.refs, c.output_format,
	       c.budget_usd::float8, c.revise_reason, c.delegate_message_id, c.result, c.judgement,
	       c.follow_ups, c.versions, c.created_at, c.updated_at
	FROM task_card c JOIN agent da ON da.id = c.delegator_agent_id JOIN agent aa ON aa.id = c.assignee_agent_id`

func scan(row pgx.Row) (*Row, error) {
	var r Row
	var crit, refs, result, judgement, versions []byte
	err := row.Scan(&r.ID, &r.RoomID, &r.WorkID, &r.Number, &r.Version, &r.Status,
		&r.DelegatorID, &r.DelegatorName, &r.DelegatorTaskID, &r.AssigneeID, &r.AssigneeName,
		&r.LaneID, &r.ParentCardID, &r.Goal, &crit, &r.Boundaries, &refs, &r.OutputFormat,
		&r.BudgetUSD, &r.ReviseReason, &r.DelegateMessageID, &result, &judgement,
		&r.FollowUps, &versions, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("cards: scan: %w", err)
	}
	_ = json.Unmarshal(crit, &r.Criteria)
	_ = json.Unmarshal(refs, &r.Refs)
	if r.Refs == nil {
		r.Refs = []Ref{}
	}
	if len(result) > 0 && string(result) != "null" {
		r.Result = result
	}
	if len(judgement) > 0 && string(judgement) != "null" {
		r.Judgement = judgement
	}
	r.Versions = versions
	return &r, nil
}

// Get loads a card (no lock).
func Get(ctx context.Context, q db.DBTX, id uuid.UUID) (*Row, error) {
	return scan(q.QueryRow(ctx, selectCard+` WHERE c.id = $1`, id))
}

// Lock loads a card FOR UPDATE. Lock order everywhere is task → lane → card
// (lanedone.MarkDone holds the task and the lane when it reaches the gate).
func Lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Row, error) {
	return scan(tx.QueryRow(ctx, selectCard+` WHERE c.id = $1 FOR UPDATE OF c`, id))
}

// OfLane is the card that made a lane, nil for a lane with none.
func OfLane(ctx context.Context, q db.DBTX, laneID uuid.UUID) (*Row, error) {
	r, err := scan(q.QueryRow(ctx, selectCard+` WHERE c.lane_id = $1`, laneID))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	return r, err
}

// OpenOnLane is the lane's card when it is in_progress (Lead 판정 Q3: only
// in_progress is 「열린」 — a trigger that lands on a submitted, accepted or
// cancelled card's lane is a normal task and does not reopen the card; only
// revise does). uuid.Nil when none.
func OpenOnLane(ctx context.Context, q db.DBTX, laneID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRow(ctx, `SELECT id FROM task_card WHERE lane_id = $1 AND status = 'in_progress'`, laneID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	return id, err
}

// NextNumber is the next display number in a mission (outside any mission:
// in the room). The caller holds the room row lock (router.Delegate), and the
// task_card_number unique index backs it.
func NextNumber(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, workID *uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT COALESCE(max(number), 0) + 1 FROM task_card WHERE room_id = $1 AND work_id IS NOT DISTINCT FROM $2`, roomID, workID).Scan(&n)
	return n, err
}

// New is what Create writes.
type New struct {
	RoomID            uuid.UUID
	WorkID            *uuid.UUID
	Number            int
	DelegatorID       uuid.UUID
	DelegatorTaskID   uuid.UUID
	LaneID            uuid.UUID
	ParentCardID      *uuid.UUID
	Draft             Draft
	DelegateMessageID uuid.UUID
	Now               time.Time
}

// Create inserts a card at version 1.
func Create(ctx context.Context, tx pgx.Tx, n New) (uuid.UUID, error) {
	crit, _ := json.Marshal(Numbered(n.Draft.Criteria))
	refs := n.Draft.Refs
	if refs == nil {
		refs = []Ref{}
	}
	rj, _ := json.Marshal(refs)
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO task_card (room_id, work_id, number, delegator_agent_id, delegator_task_id, assignee_agent_id, lane_id,
		                       parent_card_id, goal, criteria, boundaries, refs, output_format, budget_usd, delegate_message_id,
		                       created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16) RETURNING id`,
		n.RoomID, n.WorkID, n.Number, n.DelegatorID, n.DelegatorTaskID, n.Draft.AssigneeID, n.LaneID,
		n.ParentCardID, trim(n.Draft.Goal), crit, trim(n.Draft.Boundaries), rj, n.Draft.OutputFormat, n.Draft.BudgetUSD,
		n.DelegateMessageID, n.Now).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("cards: create: %w", err)
	}
	return id, nil
}

// ── the contract shape ────────────────────────────────────────────────────

// refLabels fills CardRef.label/missing from the room's rows.
func refLabels(ctx context.Context, q db.DBTX, roomID uuid.UUID, refs []Ref) []gen.CardRef {
	out := make([]gen.CardRef, 0, len(refs))
	for _, r := range refs {
		var label *string
		switch r.Kind {
		case "artifact":
			_ = q.QueryRow(ctx, `SELECT name || ' v' || version FROM artifact WHERE id = $1 AND session_id = $2`, r.ID, roomID).Scan(&label)
		case "decision":
			_ = q.QueryRow(ctx, `SELECT left(summary, 40) FROM decision WHERE id = $1 AND session_id = $2`, r.ID, roomID).Scan(&label)
		case "message":
			_ = q.QueryRow(ctx, `
				SELECT COALESCE(u.display_name, a.name, '시스템') || ' · ' || to_char(m.created_at AT TIME ZONE 'UTC', 'MM-DD HH24:MI')
				FROM message m LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id
				LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
				WHERE m.id = $1 AND m.session_id = $2`, r.ID, roomID).Scan(&label)
		}
		cr := gen.CardRef{Id: r.ID, Kind: gen.CardRefKind(r.Kind), Missing: label == nil}
		if label != nil {
			cr.Label = *label
		} else {
			cr.Label = "(deleted)"
		}
		out = append(out, cr)
	}
	return out
}

// ToAPI renders a card for a caller (actions depend on who asks).
// withVersions is getCard's `versions` (the SSE frames and list omit it).
func ToAPI(ctx context.Context, q db.DBTX, r *Row, j Judge, withVersions bool) (gen.TaskCard, error) {
	out := gen.TaskCard{
		Id: r.ID, RoomId: r.RoomID, WorkId: nullj.NullUUID(r.WorkID), Number: r.Number, Label: r.Label(),
		Version: r.Version, Status: gen.CardStatus(r.Status), LaneId: r.LaneID,
		ParentCardId: nullj.NullUUID(r.ParentCardID), Goal: r.Goal, Boundaries: r.Boundaries,
		OutputFormat: nullj.NullString(r.OutputFormat), ReviseReason: nullj.NullString(r.ReviseReason),
		DelegateMessageId: nullj.NullUUID(r.DelegateMessageID), CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	out.Delegator.AgentId, out.Delegator.Name = r.DelegatorID, r.DelegatorName
	out.Assignee.AgentId, out.Assignee.Name = r.AssigneeID, r.AssigneeName
	out.BudgetUsd = nullable.NewNullNullable[float32]()
	if r.BudgetUSD != nil {
		out.BudgetUsd = nullable.NewNullableWithValue(float32(*r.BudgetUSD))
	}
	for _, c := range r.Criteria {
		out.Criteria = append(out.Criteria, struct {
			Method gen.CardCriterionMethod `json:"method"`
			N      int                     `json:"n"`
			Text   string                  `json:"text"`
		}{Method: gen.CardCriterionMethod(c.Method), N: c.N, Text: c.Text})
	}
	out.Refs = refLabels(ctx, q, r.RoomID, r.Refs)
	out.Result = nullable.NewNullNullable[gen.CardResult]()
	if r.Result != nil {
		var cr gen.CardResult
		if err := json.Unmarshal(r.Result, &cr); err != nil {
			return out, fmt.Errorf("cards: result shape: %w", err)
		}
		out.Result = nullable.NewNullableWithValue(cr)
	}
	out.Judgement = nullable.NewNullNullable[gen.CardJudgement]()
	if r.Judgement != nil {
		var cj gen.CardJudgement
		if err := json.Unmarshal(r.Judgement, &cj); err != nil {
			return out, fmt.Errorf("cards: judgement shape: %w", err)
		}
		out.Judgement = nullable.NewNullableWithValue(cj)
	}
	fu := r.FollowUps
	out.FollowUps = &fu
	w, err := JudgesOf(ctx, q, r)
	if err != nil {
		return out, err
	}
	acts := Actions(r.Status, j, w)
	out.Actions = make([]gen.TaskCardActions, 0, len(acts))
	for _, a := range acts {
		out.Actions = append(out.Actions, gen.TaskCardActions(a))
	}
	if withVersions {
		vs := []map[string]any{}
		if len(r.Versions) > 0 {
			_ = json.Unmarshal(r.Versions, &vs)
		}
		out.Versions = &vs
	}
	return out, nil
}

// JudgesOf is who may judge the card: its delegator, and the mission's
// Director and deputy (outside a mission: the room's owner and deputies).
func JudgesOf(ctx context.Context, q db.DBTX, r *Row) (Judges, error) {
	w := Judges{Delegator: r.DelegatorID}
	var rows pgx.Rows
	var err error
	if r.WorkID != nil {
		rows, err = q.Query(ctx, `
			SELECT director_user_id FROM work WHERE id = $1
			UNION SELECT deputy_user_id FROM work WHERE id = $1 AND deputy_user_id IS NOT NULL`, *r.WorkID)
	} else {
		rows, err = q.Query(ctx, `
			SELECT owner_user_id FROM room WHERE id = $1 AND owner_user_id IS NOT NULL
			UNION SELECT user_id FROM room_participant WHERE room_id = $1 AND user_id IS NOT NULL AND role = 'deputy' AND left_at IS NULL`, r.RoomID)
	}
	if err != nil {
		return w, fmt.Errorf("cards: judges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return w, err
		}
		w.People = append(w.People, id)
	}
	return w, rows.Err()
}

// Publish sends card.created / card.updated (openapi StreamEvent v0.3.10 —
// TaskCard without versions). A frame the web misses is a card frozen on the
// screen; a failure here is logged by the hub, not the caller's.
func Publish(ctx context.Context, hub *realtime.Hub, q db.DBTX, cardID uuid.UUID, typ string) {
	if hub == nil {
		return
	}
	r, err := Get(ctx, q, cardID)
	if err != nil {
		return
	}
	api, err := ToAPI(ctx, q, r, Judge{}, false)
	if err != nil {
		return
	}
	var ws uuid.UUID
	if err := q.QueryRow(ctx, `SELECT workspace_id FROM room WHERE id = $1`, r.RoomID).Scan(&ws); err != nil {
		return
	}
	room := r.RoomID
	_ = hub.Publish(ctx, q, ws, &room, typ, api)
}

// Board is listRoomCards — the mission's cards in number order (all == every
// card in the room).
func Board(ctx context.Context, q db.DBTX, roomID uuid.UUID, workID *uuid.UUID, all bool) (gen.CardBoard, error) {
	out := gen.CardBoard{WorkId: nullj.NullUUID(workID)}
	sql := selectCard + ` WHERE c.room_id = $1 AND ($3 OR c.work_id IS NOT DISTINCT FROM $2) ORDER BY c.work_id NULLS FIRST, c.number`
	rows, err := q.Query(ctx, sql, roomID, workID, all)
	if err != nil {
		return out, fmt.Errorf("cards: board: %w", err)
	}
	var rs []*Row
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			rows.Close()
			return out, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	type item = struct {
		Assignee struct {
			AgentId openapi_types.UUID `json:"agent_id"`
			Name    string             `json:"name"`
		} `json:"assignee"`
		AutoResult      *bool                                 `json:"auto_result,omitempty"`
		CostUsd         nullable.Nullable[float32]            `json:"cost_usd"`
		Goal            string                                `json:"goal"`
		Id              openapi_types.UUID                    `json:"id"`
		Label           string                                `json:"label"`
		LaneId          openapi_types.UUID                    `json:"lane_id"`
		LatestMessageId nullable.Nullable[openapi_types.UUID] `json:"latest_message_id,omitempty"`
		Met             nullable.Nullable[int]                `json:"met"`
		Number          int                                   `json:"number"`
		ParentCardId    nullable.Nullable[openapi_types.UUID] `json:"parent_card_id"`
		Status          gen.CardStatus                        `json:"status"`
		TotalCriteria   int                                   `json:"total_criteria"`
		Version         int                                   `json:"version"`
	}
	out.Items = make([]item, 0, len(rs))
	for _, r := range rs {
		it := item{Goal: r.Goal, Id: r.ID, Label: r.Label(), LaneId: r.LaneID, Number: r.Number,
			ParentCardId: nullj.NullUUID(r.ParentCardID), Status: gen.CardStatus(r.Status),
			TotalCriteria: len(r.Criteria), Version: r.Version}
		it.Assignee.AgentId, it.Assignee.Name = r.AssigneeID, r.AssigneeName
		it.Met = nullable.NewNullNullable[int]()
		latest := r.DelegateMessageID
		if res := ParseResult(r.Result); res != nil {
			it.Met = nullable.NewNullableWithValue(res.MetCount)
			auto := res.Auto
			it.AutoResult = &auto
			if res.MessageID != nil {
				if id, err := uuid.Parse(*res.MessageID); err == nil {
					latest = &id
				}
			}
		}
		it.LatestMessageId = nullj.NullUUID(latest)
		it.CostUsd = nullable.NewNullNullable[float32]()
		if c, err := laneCost(ctx, q, r.LaneID); err == nil && c != nil {
			it.CostUsd = nullable.NewNullableWithValue(float32(*c))
		}
		out.Items = append(out.Items, it)
		out.Total++
		if r.Status == ResultSubmitted {
			out.PendingJudgement++
		}
	}
	return out, nil
}

func laneCost(ctx context.Context, q db.DBTX, laneID uuid.UUID) (*float64, error) {
	var c *float64
	err := q.QueryRow(ctx, `SELECT sum(u.cost_usd)::float8 FROM task t JOIN task_usage_total u ON u.task_id = t.id WHERE t.lane_id = $1 AND t.kind = 'card'`, laneID).Scan(&c)
	return c, err
}

// ParseResult reads a stored result, nil when there is none.
func ParseResult(raw json.RawMessage) *Result {
	if len(raw) == 0 {
		return nil
	}
	var r Result
	if json.Unmarshal(raw, &r) != nil {
		return nil
	}
	return &r
}

// ── writes ────────────────────────────────────────────────────────────────

// snapshot is one version's whole shape for `versions` (#397 리뷰 B2).
func snapshot(ctx context.Context, q db.DBTX, r *Row) map[string]any {
	var result, judgement any
	if r.Result != nil {
		_ = json.Unmarshal(r.Result, &result)
	}
	if r.Judgement != nil {
		_ = json.Unmarshal(r.Judgement, &judgement)
	}
	return map[string]any{
		"version": r.Version, "goal": r.Goal, "criteria": r.Criteria, "boundaries": r.Boundaries,
		"refs": refLabels(ctx, q, r.RoomID, r.Refs), "output_format": r.OutputFormat, "budget_usd": r.BudgetUSD,
		"revise_reason": r.ReviseReason, "delegate_message_id": r.DelegateMessageID,
		"result": result, "judgement": judgement,
	}
}

// Judgement is one CardJudgement.
// reason is revise_requested's, comment is accepted's (v0.3.11) — the other
// is null.
func judgementJSON(action string, byKind string, byID uuid.UUID, byName string, at time.Time, reason, comment *string) []byte {
	b, _ := json.Marshal(map[string]any{
		"action": action, "by": map[string]any{"kind": byKind, "id": byID, "name": byName},
		"at": at.UTC().Format(time.RFC3339Nano), "reason": reason, "comment": comment,
	})
	return b
}

// StoreResult writes a result on the card's current version (status
// result_submitted) and posts the result card bubble (speech report,
// card_role result, responds_to the version's delegation bubble). The caller
// holds the card lock and has checked the transition. It returns the bubble.
func StoreResult(ctx context.Context, tx pgx.Tx, r *Row, res Result, authorTask *uuid.UUID, now time.Time) (uuid.UUID, error) {
	cost, dur := versionCostDuration(ctx, tx, r)
	res.CostUSD, res.DurationS = cost, dur
	res.SubmittedAt = now.UTC().Format(time.RFC3339Nano)
	content := ResultBubble(r, res)
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, mentions, source_task_id, kind, created_at, work_id,
		                     card_id, card_role, card_version)
		VALUES ($1, 'agent', $2, $3, '[]', $4, 'text', $5, $6, $7, 'result', $8) RETURNING id`,
		r.RoomID, r.AssigneeID, content, authorTask, now, r.WorkID, r.ID, r.Version).Scan(&msgID); err != nil {
		return uuid.Nil, fmt.Errorf("cards: result bubble: %w", err)
	}
	to := messages.Addressee{Kind: "agent", ID: &r.DelegatorID, Name: r.DelegatorName}
	if err := messages.Store(ctx, tx, msgID, messages.StoreOpts{CardReportTo: &to, CardRespondsTo: r.DelegateMessageID}); err != nil {
		return uuid.Nil, err
	}
	mid := msgID.String()
	res.MessageID = &mid
	raw, _ := json.Marshal(res)
	if _, err := tx.Exec(ctx, `UPDATE task_card SET status = 'result_submitted', result = $2, judgement = NULL, updated_at = $3 WHERE id = $1`,
		r.ID, raw, now); err != nil {
		return uuid.Nil, fmt.Errorf("cards: store result: %w", err)
	}
	return msgID, nil
}

// versionCostDuration is CardResult.cost_usd · duration_s — the card tasks of
// the current version (created at or after the version's delegation bubble).
func versionCostDuration(ctx context.Context, q db.DBTX, r *Row) (*float64, *int) {
	var cost *float64
	var start *time.Time
	_ = q.QueryRow(ctx, `
		SELECT sum(u.cost_usd)::float8, min(t.started_at)
		FROM task t LEFT JOIN task_usage_total u ON u.task_id = t.id
		WHERE t.card_id = $1 AND t.created_at >= COALESCE((SELECT created_at FROM message WHERE id = $2), '-infinity')`,
		r.ID, r.DelegateMessageID).Scan(&cost, &start)
	if start == nil {
		return cost, nil
	}
	d := int(time.Since(*start).Seconds())
	if d < 0 {
		d = 0
	}
	return cost, &d
}

// Revise opens a new version: the old one goes to `versions` whole, the patch
// applies, status in_progress, follow-ups reset. The caller holds the lock,
// has checked the transition and the patched draft, and posts the new
// delegation bubble (SetDelegateMessage).
func Revise(ctx context.Context, tx pgx.Tx, r *Row, d Draft, reason string, byKind string, byID uuid.UUID, byName string, now time.Time) error {
	snap := snapshot(ctx, tx, r)
	// The judgement that asked for this version is the old version's last word.
	// #406 리뷰 NN2(알려진 한계, 스킵): 사람이 수락된 카드를 수락 취소(revise)하면 그 판의
	// accepted 판정(comment 포함)은 이 revise_requested 판정으로 덮여 versions 에 남지 않는다 —
	// 판정 이력을 쌓으려면 versions[].judgements 같은 계약 변경이 필요하다.
	snap["judgement"] = json.RawMessage(judgementJSON("revise_requested", byKind, byID, byName, now, &reason, nil))
	var versions []any
	if len(r.Versions) > 0 {
		_ = json.Unmarshal(r.Versions, &versions)
	}
	versions = append(versions, snap)
	vj, _ := json.Marshal(versions)
	crit, _ := json.Marshal(Numbered(d.Criteria))
	refs := d.Refs
	if refs == nil {
		refs = []Ref{}
	}
	rj, _ := json.Marshal(refs)
	_, err := tx.Exec(ctx, `
		UPDATE task_card SET version = version + 1, status = 'in_progress', goal = $2, criteria = $3, boundaries = $4, refs = $5,
		       output_format = $6, budget_usd = $7, revise_reason = $8, result = NULL, judgement = NULL, follow_ups = 0,
		       versions = $9, updated_at = $10
		WHERE id = $1`, r.ID, trim(d.Goal), crit, trim(d.Boundaries), rj, d.OutputFormat, d.BudgetUSD, reason, vj, now)
	if err != nil {
		return fmt.Errorf("cards: revise: %w", err)
	}
	return nil
}

// SetDelegateMessage points the current version at its delegation bubble.
func SetDelegateMessage(ctx context.Context, tx pgx.Tx, cardID, msgID uuid.UUID) error {
	_, err := tx.Exec(ctx, `UPDATE task_card SET delegate_message_id = $2 WHERE id = $1`, cardID, msgID)
	return err
}

// Accept closes the card with a judgement and its comment (v0.3.11 — the
// caller has checked it is not blank).
func Accept(ctx context.Context, tx pgx.Tx, r *Row, byKind string, byID uuid.UUID, byName string, comment string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE task_card SET status = 'accepted', judgement = $2, updated_at = $3 WHERE id = $1`,
		r.ID, judgementJSON("accepted", byKind, byID, byName, now, nil, &comment), now)
	return err
}

// ReviveOnLane brings back the lane's card that the lane's failure cancelled
// when a person restarts the lane (#400 리뷰 400b NN6): the same version,
// `in_progress`, its follow-up count reset — the restarted turn is a card
// turn again and can submit its result. A card whose mission is closed stays
// cancelled (that cancel was the mission's, not the lane's). Returns the card
// it revived, or uuid.Nil.
func ReviveOnLane(ctx context.Context, tx pgx.Tx, laneID uuid.UUID, now time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		UPDATE task_card c SET status = 'in_progress', follow_ups = 0, updated_at = $2
		WHERE c.lane_id = $1 AND c.status = 'cancelled'
		  AND NOT EXISTS (SELECT 1 FROM work w WHERE w.id = c.work_id AND w.status IN ('completed', 'cancelled'))
		RETURNING c.id`, laneID, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("cards: revive: %w", err)
	}
	return id, nil
}

// CancelWhere cancels the open cards a lane cancel or a mission close leaves
// behind (PRD FR-3.8 1 「취소(lane 취소·미션 닫힘)」) and returns them for
// card.updated. Accepted cards stay accepted.
func CancelWhere(ctx context.Context, tx pgx.Tx, col string, id uuid.UUID, now time.Time) ([]uuid.UUID, error) {
	if col != "lane_id" && col != "work_id" {
		return nil, fmt.Errorf("cards: cancel by %q", col)
	}
	rows, err := tx.Query(ctx, `UPDATE task_card SET status = 'cancelled', updated_at = $2
		WHERE `+col+` = $1 AND status IN ('in_progress', 'result_submitted') RETURNING id`, id, now)
	if err != nil {
		return nil, fmt.Errorf("cards: cancel: %w", err)
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var c uuid.UUID
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Summary is CompletionProgress.cards for a mission, nil when it has none.
func Summary(ctx context.Context, q db.DBTX, workID uuid.UUID) (*CardSummary, error) {
	rows, err := q.Query(ctx, `SELECT status, result FROM task_card WHERE work_id = $1`, workID)
	if err != nil {
		return nil, fmt.Errorf("cards: summary: %w", err)
	}
	defer rows.Close()
	var s CardSummary
	for rows.Next() {
		var st string
		var raw []byte
		if err := rows.Scan(&st, &raw); err != nil {
			return nil, err
		}
		s.Total++
		switch st {
		case Accepted:
			s.Accepted++
		case ResultSubmitted:
			s.PendingJudgement++
		case InProgress:
			s.InProgress++
		}
		if st == Accepted || st == ResultSubmitted {
			if res := ParseResult(raw); res != nil {
				for _, v := range res.Verdicts {
					if v.Verdict != "met" {
						s.WeakCriteria++
					}
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if s.Total == 0 {
		return nil, nil
	}
	return &s, nil
}

// CardSummary is CompletionProgress.cards.
type CardSummary struct {
	Total, Accepted, PendingJudgement, InProgress, WeakCriteria int
}

// Line is the one-line card summary the completion approval request carries
// (PRD FR-3.8 4 「수락 n · 판정 대기 n · 부분/미충족 기준 n」).
func (s *CardSummary) Line() string {
	if s == nil {
		return ""
	}
	return fmt.Sprintf("작업 카드 %d장 — 수락 %d · 판정 대기 %d · 진행 중 %d · 부분/미충족 기준 %d", s.Total, s.Accepted, s.PendingJudgement, s.InProgress, s.WeakCriteria)
}

// ToGen is s as the contract's anonymous struct.
func (s *CardSummary) ToGen() *struct {
	Accepted         int `json:"accepted"`
	InProgress       int `json:"in_progress"`
	PendingJudgement int `json:"pending_judgement"`
	Total            int `json:"total"`
	WeakCriteria     int `json:"weak_criteria"`
} {
	if s == nil {
		return nil
	}
	return &struct {
		Accepted         int `json:"accepted"`
		InProgress       int `json:"in_progress"`
		PendingJudgement int `json:"pending_judgement"`
		Total            int `json:"total"`
		WeakCriteria     int `json:"weak_criteria"`
	}{s.Accepted, s.InProgress, s.PendingJudgement, s.Total, s.WeakCriteria}
}

// CheckRefsExist is the refs' existence check (FR-3.8 1 「존재 검사」, same
// room only).
func CheckRefsExist(ctx context.Context, q db.DBTX, roomID uuid.UUID, refs []Ref) ([]string, error) {
	var bad []string
	for i, r := range refs {
		var n int
		var err error
		switch r.Kind {
		case "artifact":
			err = q.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE id = $1 AND session_id = $2`, r.ID, roomID).Scan(&n)
		case "decision":
			err = q.QueryRow(ctx, `SELECT count(*) FROM decision WHERE id = $1 AND session_id = $2`, r.ID, roomID).Scan(&n)
		case "message":
			err = q.QueryRow(ctx, `SELECT count(*) FROM message WHERE id = $1 AND session_id = $2`, r.ID, roomID).Scan(&n)
		default:
			continue
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			bad = append(bad, fmt.Sprintf("card.refs[%d].id", i))
		}
	}
	return bad, nil
}

// CheckEvidenceExist is the evidence refs' existence check (artifact ·
// message in the same room).
func CheckEvidenceExist(ctx context.Context, q db.DBTX, roomID uuid.UUID, in ResultIn) ([]string, error) {
	var bad []string
	for i, v := range in.Verdicts {
		for j, e := range v.Evidence {
			id, err := uuid.Parse(e.Ref)
			if err != nil {
				continue // the pure check already named it
			}
			var n int
			switch e.Kind {
			case "artifact":
				err = q.QueryRow(ctx, `SELECT count(*) FROM artifact WHERE id = $1 AND session_id = $2`, id, roomID).Scan(&n)
			case "message":
				err = q.QueryRow(ctx, `SELECT count(*) FROM message WHERE id = $1 AND session_id = $2`, id, roomID).Scan(&n)
			default:
				continue
			}
			if err != nil {
				return nil, err
			}
			if n == 0 {
				bad = append(bad, fmt.Sprintf("verdicts[%d].evidence[%d].ref", i, j))
			}
		}
	}
	return bad, nil
}

func trim(s string) string { return trimSpace(s) }
