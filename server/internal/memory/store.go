// Package memory is the mission state ledger (PRD FR-4.6 v0.19.18, openapi
// v0.3.12 `memory` tag, 맥락 2단계): structured items an agent writes on
// purpose — facts, assignments, open questions, lessons, the Lead's plan and
// progress — that the turn prompt's <mission_ledger> carries instead of the
// whole mission transcript.
//
// Four design rules (plan/research/context-memory/07-oss-memory-tools.md):
//
//  1. No automatic judgement. The server never decides that two items
//     contradict or should merge (Mem0 measured that and dropped it). The one
//     server-side merge is exact: a lesson whose trimmed content equals an
//     active lesson of the same mission raises that lesson's support_count.
//  2. No deletion. supersede and retire change the state columns only
//     (status · superseded_by · invalidated_at); the row stays forever.
//  3. A limit refuses, it does not cut: content over 300 characters is 422.
//  4. Write contention is settled by role: plan · progress are the lead's
//     alone (403 memory_kind_forbidden), so the cells most likely to race have
//     one writer.
//
// store.go is the database half (migration memory_item); render.go is the
// turn prompt's block. The HTTP translation lives in httpapi/handlers_memory.go.
package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/nullable"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// ContentMax is MemoryItemInput.content's maxLength, in characters.
const ContentMax = 300

// PromoteAt is the support_count from which a lesson reaches the turn prompt
// (PRD FR-4.6 3 — a guess met once does not harden into a lesson).
const PromoteAt = 2

// LessonHalfLife is how long a lesson stays in the turn prompt after it was
// written (PRD FR-4.6 3, 「30일 지나면 렌더에서 빠진다」). The row stays.
const LessonHalfLife = 30 * 24 * time.Hour

// Sentences the problems carry (openapi error codes of v0.3.12).
const (
	KindForbiddenSentence  = "계획(plan)과 진행 판단(progress)은 리드만 원장에 쓸 수 있습니다"
	NotActiveSentence      = "이미 대체되었거나 철회된 항목입니다 — 지금 값은 colab memory get 으로 확인하세요"
	KindImmutableSentence  = "대체는 대상 항목의 종류와 미션을 그대로 물려받습니다 — kind·work_id 는 보내지 마세요"
	ContentRequiredMessage = "내용을 적어 주세요"
	ContentTooLongMessage  = "원장 항목은 300자까지입니다 — 잘라 내지 않습니다, 줄여서 다시 보내세요"
	ReasonRequiredMessage  = "철회 사유를 적어 주세요"
	SourceInvalidMessage   = "근거 메시지는 이 방의 메시지여야 합니다"
)

// ErrNotFound is an item that does not exist.
var ErrNotFound = errors.New("memory: item not found")

// Item is one memory_item row with the author's name resolved.
type Item struct {
	ID, WorkID      uuid.UUID
	Kind            string
	Content         string
	Certainty       *string
	Outcome         *string
	SupportCount    int
	Status          string
	Supersedes      *uuid.UUID
	SupersededBy    *uuid.UUID
	InvalidatedAt   *time.Time
	SourceMessageID []uuid.UUID
	// RetireReason is retireMemory's reason (openapi v0.3.13 MemoryItem.retire_reason).
	RetireReason *string
	// SupportAuthors are the writers that reinforced a lesson — one count
	// each (openapi v0.3.13: only a writer that has not contributed counts).
	SupportAuthors []uuid.UUID
	// LastReinforcedAt is when a lesson was last reinforced by a new writer
	// (its created_at until then) — the 30-day render rule reads it.
	LastReinforcedAt *time.Time
	AuthorAgentID    *uuid.UUID
	AuthorUserID     *uuid.UUID
	AuthorName       string
	CreatedAt        time.Time
}

// Promoted is openapi MemoryItem.promoted: a lesson with support_count ≥ 2;
// every other kind is always true.
func (it *Item) Promoted() bool {
	return it.Kind != string(gen.MemoryKindLesson) || it.SupportCount >= PromoteAt
}

// Author is who writes an item: an agent through its task token, or a person.
type Author struct {
	AgentID *uuid.UUID
	TaskID  *uuid.UUID
	UserID  *uuid.UUID
	// Role is the agent's role (FR-1.1); "" for a person.
	Role string
}

// ID is the writer's id as the lesson support list counts it: the agent,
// or the person.
func (a Author) ID() uuid.UUID {
	switch {
	case a.UserID != nil:
		return *a.UserID
	case a.AgentID != nil:
		return *a.AgentID
	}
	return uuid.Nil
}

// mayWrite is FR-4.6 2: plan · progress are the lead's and the people's.
func (a Author) mayWrite(kind string) bool {
	if kind != string(gen.MemoryKindPlan) && kind != string(gen.MemoryKindProgress) {
		return true
	}
	return a.UserID != nil || a.Role == string(gen.Lead)
}

// KindAllowed reports whether a writer with this agent role ("" = person)
// may write kind — the 403 memory_kind_forbidden rule, for the CLI-side
// mirror and the tests.
func KindAllowed(role, kind string) bool {
	a := Author{Role: role}
	if role == "" {
		id := uuid.Nil
		a.UserID = &id
	}
	return a.mayWrite(kind)
}

// NoteIn is noteMemory's body as the rules see it.
type NoteIn struct {
	WorkID, RoomID uuid.UUID
	Kind           string
	Content        string
	Certainty      *string
	Outcome        *string
	Sources        []uuid.UUID
	Author         Author
}

// SupersedeIn is supersedeMemory's body.
type SupersedeIn struct {
	Content   string
	Certainty *string
	Sources   []uuid.UUID
	Author    Author
}

const selectItem = `
	SELECT m.id, m.work_id, m.kind, m.content, m.certainty, m.outcome, m.support_count, m.status,
	       m.supersedes, m.superseded_by, m.invalidated_at, m.source_message_ids,
	       m.retire_reason, m.support_author_ids, m.last_reinforced_at,
	       m.created_by_agent_id, m.created_by_user_id,
	       COALESCE(a.name, u.display_name, ''), m.created_at
	FROM memory_item m
	LEFT JOIN agent a ON a.id = m.created_by_agent_id
	LEFT JOIN app_user u ON u.id = m.created_by_user_id`

func scan(row pgx.Row) (*Item, error) {
	var it Item
	err := row.Scan(&it.ID, &it.WorkID, &it.Kind, &it.Content, &it.Certainty, &it.Outcome, &it.SupportCount, &it.Status,
		&it.Supersedes, &it.SupersededBy, &it.InvalidatedAt, &it.SourceMessageID,
		&it.RetireReason, &it.SupportAuthors, &it.LastReinforcedAt,
		&it.AuthorAgentID, &it.AuthorUserID, &it.AuthorName, &it.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("memory: scan: %w", err)
	}
	if it.SourceMessageID == nil {
		it.SourceMessageID = []uuid.UUID{}
	}
	return &it, nil
}

// Get reads one item.
func Get(ctx context.Context, q db.DBTX, id uuid.UUID) (*Item, error) {
	return scan(q.QueryRow(ctx, selectItem+` WHERE m.id = $1`, id))
}

// List is listMemory: the mission's items, oldest first. kind "" is every
// kind; status "" or "active" is active only, "all" is every status.
func List(ctx context.Context, q db.DBTX, workID uuid.UUID, kind, status string) ([]*Item, error) {
	if status == "" {
		status = string(gen.MemoryStatusActive)
	}
	rows, err := q.Query(ctx, selectItem+`
		WHERE m.work_id = $1 AND ($2 = '' OR m.kind = $2) AND ($3 = 'all' OR m.status = $3)
		ORDER BY m.created_at, m.id`, workID, kind, status)
	if err != nil {
		return nil, fmt.Errorf("memory: list: %w", err)
	}
	defer rows.Close()
	out := []*Item{}
	for rows.Next() {
		it, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// cleanContent is the content rule: trimmed, 1..300 characters (runes, not
// bytes), refused — not cut — when longer (design rule 3).
func cleanContent(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if c == "" {
		return "", apperr.Validation(apperr.Field("content", "required", ContentRequiredMessage))
	}
	if utf8.RuneCountInString(c) > ContentMax {
		return "", apperr.Validation(apperr.Field("content", "too_long", ContentTooLongMessage))
	}
	return c, nil
}

// lockWork serialises the ledger writes of one mission: the lesson merge and
// the one-active-plan rule both read-then-write.
func lockWork(ctx context.Context, tx pgx.Tx, workID uuid.UUID) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('memory_item:' || $1::text, 0))`, workID); err != nil {
		return fmt.Errorf("memory: lock: %w", err)
	}
	return nil
}

// checkSources is 「source_message_ids 는 같은 방의 메시지」: every id must be
// a message of the mission's room (422 otherwise).
func checkSources(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return []uuid.UUID{}, nil
	}
	seen := map[uuid.UUID]bool{}
	uniq := []uuid.UUID{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) > 10 {
		return nil, apperr.Validation(apperr.Field("source_message_ids", "too_many", "근거 메시지는 10개까지입니다"))
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM message WHERE id = ANY($1) AND session_id = $2`, uniq, roomID).Scan(&n); err != nil {
		return nil, fmt.Errorf("memory: sources: %w", err)
	}
	if n != len(uniq) {
		return nil, apperr.Validation(apperr.Field("source_message_ids", "not_in_room", SourceInvalidMessage))
	}
	return uniq, nil
}

func validKind(k string) bool { return gen.MemoryKind(k).Valid() }

func validCertainty(c string) bool {
	switch gen.MemoryCertainty(c) {
	case gen.MemoryCertaintyGiven, gen.MemoryCertaintyToVerify, gen.MemoryCertaintyDerived, gen.MemoryCertaintyGuess:
		return true
	}
	return false
}

func validOutcome(o string) bool {
	switch gen.MemoryOutcome(o) {
	case gen.DeadEnd, gen.Corrected, gen.Useful:
		return true
	}
	return false
}

// Note is noteMemory. It answers the item and whether a NEW row was made —
// false when a lesson merged into an existing one (support_count +1).
//
//   - kind outside the enum → 422; plan · progress from a non-lead → 403
//     memory_kind_forbidden.
//   - certainty is kept for a fact only, outcome for a lesson only — given
//     with another kind they are dropped, not refused (openapi「무시」).
//   - a lesson whose trimmed content (exact, case-sensitive) equals an
//     active lesson of the same mission never adds a row: it answers that
//     lesson, and raises its support_count (and last_reinforced_at) only
//     when the writer has not contributed to it yet (openapi v0.3.13 — the
//     same writer saying it twice is not independent support).
//   - a plan supersedes the mission's active plan (one active plan).
func Note(ctx context.Context, tx pgx.Tx, in NoteIn, now time.Time) (*Item, bool, error) {
	if !validKind(in.Kind) {
		return nil, false, apperr.Validation(apperr.Field("kind", "invalid", "kind 는 fact · assignment · open_question · lesson · plan · progress 중 하나입니다"))
	}
	if !in.Author.mayWrite(in.Kind) {
		return nil, false, apperr.Forbidden("memory_kind_forbidden", KindForbiddenSentence)
	}
	content, err := cleanContent(in.Content)
	if err != nil {
		return nil, false, err
	}
	var certainty, outcome *string
	if in.Kind == string(gen.MemoryKindFact) && in.Certainty != nil {
		if !validCertainty(*in.Certainty) {
			return nil, false, apperr.Validation(apperr.Field("certainty", "invalid", "certainty 는 given · to_verify · derived · guess 중 하나입니다"))
		}
		certainty = in.Certainty
	}
	if in.Kind == string(gen.MemoryKindLesson) && in.Outcome != nil {
		if !validOutcome(*in.Outcome) {
			return nil, false, apperr.Validation(apperr.Field("outcome", "invalid", "outcome 은 dead_end · corrected · useful 중 하나입니다"))
		}
		outcome = in.Outcome
	}
	sources, err := checkSources(ctx, tx, in.RoomID, in.Sources)
	if err != nil {
		return nil, false, err
	}
	if err := lockWork(ctx, tx, in.WorkID); err != nil {
		return nil, false, err
	}
	support := 0
	if in.Kind == string(gen.MemoryKindLesson) {
		support = 1
		var dup uuid.UUID
		var already bool
		err := tx.QueryRow(ctx, `
			SELECT id, $3::uuid = ANY(support_author_ids) FROM memory_item
			WHERE work_id = $1 AND kind = 'lesson' AND status = 'active' AND btrim(content) = $2
			ORDER BY created_at, id LIMIT 1`, in.WorkID, content, in.Author.ID()).Scan(&dup, &already)
		if err == nil {
			if !already {
				if _, err := tx.Exec(ctx, `
					UPDATE memory_item SET support_count = support_count + 1,
					       support_author_ids = array_append(support_author_ids, $2), last_reinforced_at = $3
					WHERE id = $1`, dup, in.Author.ID(), now); err != nil {
					return nil, false, fmt.Errorf("memory: lesson support: %w", err)
				}
			}
			it, err := Get(ctx, tx, dup)
			return it, false, err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("memory: lesson dup: %w", err)
		}
	}
	id := uuid.New()
	var supersedes *uuid.UUID
	if in.Kind == string(gen.MemoryKindPlan) {
		var prev uuid.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM memory_item WHERE work_id = $1 AND kind = 'plan' AND status = 'active'`, in.WorkID).Scan(&prev)
		switch {
		case err == nil:
			if err := markSuperseded(ctx, tx, prev, id, now); err != nil {
				return nil, false, err
			}
			supersedes = &prev
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, false, fmt.Errorf("memory: active plan: %w", err)
		}
	}
	var supporters []uuid.UUID
	if support > 0 {
		supporters = []uuid.UUID{in.Author.ID()}
	}
	if err := insert(ctx, tx, id, in.WorkID, in.Kind, content, certainty, outcome, support, supporters, supersedes, sources, in.Author, now); err != nil {
		return nil, false, err
	}
	it, err := Get(ctx, tx, id)
	return it, true, err
}

// markSuperseded is the ONE write a supersede makes to the old row: its
// state columns. Nothing it said is touched.
func markSuperseded(ctx context.Context, tx pgx.Tx, old, by uuid.UUID, now time.Time) error {
	if _, err := tx.Exec(ctx, `
		UPDATE memory_item SET status = 'superseded', superseded_by = $2, invalidated_at = $3
		WHERE id = $1`, old, by, now); err != nil {
		return fmt.Errorf("memory: mark superseded: %w", err)
	}
	return nil
}

func insert(ctx context.Context, tx pgx.Tx, id, workID uuid.UUID, kind, content string, certainty, outcome *string,
	support int, supporters []uuid.UUID, supersedes *uuid.UUID, sources []uuid.UUID, a Author, now time.Time) error {
	if supporters == nil {
		supporters = []uuid.UUID{}
	}
	// last_reinforced_at starts at created_at, for a lesson only.
	var reinforced *time.Time
	if kind == string(gen.MemoryKindLesson) {
		reinforced = &now
	}
	agentID := a.AgentID
	if a.UserID != nil {
		agentID = nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO memory_item (id, work_id, kind, content, certainty, outcome, support_count, supersedes,
		                         source_message_ids, created_by_agent_id, created_by_user_id, created_by_task_id, created_at,
		                         support_author_ids, last_reinforced_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		id, workID, kind, content, certainty, outcome, support, supersedes,
		sources, agentID, a.UserID, a.TaskID, now, supporters, reinforced); err != nil {
		return fmt.Errorf("memory: insert: %w", err)
	}
	return nil
}

// Supersede is supersedeMemory: a NEW row carries the new content and
// inherits the target's mission, kind, outcome, certainty (unless a fact's
// new certainty is given) and — for a lesson — its support_count (a
// supersede says the value changed; it is not a new lesson). The target
// gets only its state columns written. Returns (new, old).
func Supersede(ctx context.Context, tx pgx.Tx, targetID uuid.UUID, in SupersedeIn, roomID uuid.UUID, now time.Time) (*Item, *Item, error) {
	content, err := cleanContent(in.Content)
	if err != nil {
		return nil, nil, err
	}
	target, err := Get(ctx, tx, targetID)
	if err != nil {
		return nil, nil, err
	}
	if !in.Author.mayWrite(target.Kind) {
		return nil, nil, apperr.Forbidden("memory_kind_forbidden", KindForbiddenSentence)
	}
	certainty := target.Certainty
	if target.Kind == string(gen.MemoryKindFact) && in.Certainty != nil {
		if !validCertainty(*in.Certainty) {
			return nil, nil, apperr.Validation(apperr.Field("certainty", "invalid", "certainty 는 given · to_verify · derived · guess 중 하나입니다"))
		}
		certainty = in.Certainty
	}
	sources, err := checkSources(ctx, tx, roomID, in.Sources)
	if err != nil {
		return nil, nil, err
	}
	if err := lockWork(ctx, tx, target.WorkID); err != nil {
		return nil, nil, err
	}
	// Re-read under the lock: two supersedes of one item must not both win.
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM memory_item WHERE id = $1 FOR UPDATE`, targetID).Scan(&status); err != nil {
		return nil, nil, fmt.Errorf("memory: lock target: %w", err)
	}
	if status != string(gen.MemoryStatusActive) {
		return nil, nil, apperr.Conflict("memory_not_active", NotActiveSentence)
	}
	id := uuid.New()
	if err := markSuperseded(ctx, tx, targetID, id, now); err != nil {
		return nil, nil, err
	}
	if err := insert(ctx, tx, id, target.WorkID, target.Kind, content, certainty, target.Outcome,
		target.SupportCount, target.SupportAuthors, &targetID, sources, in.Author, now); err != nil {
		return nil, nil, err
	}
	item, err := Get(ctx, tx, id)
	if err != nil {
		return nil, nil, err
	}
	old, err := Get(ctx, tx, targetID)
	return item, old, err
}

// Retire is retireMemory: status retired, invalidated_at now, the reason
// kept beside the row (MemoryItem has no field for it and the content is
// never rewritten). Same kind rule as a supersede.
func Retire(ctx context.Context, tx pgx.Tx, targetID uuid.UUID, reason string, a Author, now time.Time) (*Item, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, apperr.Validation(apperr.Field("reason", "required", ReasonRequiredMessage))
	}
	if utf8.RuneCountInString(reason) > ContentMax {
		return nil, apperr.Validation(apperr.Field("reason", "too_long", "철회 사유는 300자까지입니다"))
	}
	target, err := Get(ctx, tx, targetID)
	if err != nil {
		return nil, err
	}
	if !a.mayWrite(target.Kind) {
		return nil, apperr.Forbidden("memory_kind_forbidden", KindForbiddenSentence)
	}
	if err := lockWork(ctx, tx, target.WorkID); err != nil {
		return nil, err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE memory_item SET status = 'retired', invalidated_at = $2, retire_reason = $3
		WHERE id = $1 AND status = 'active'`, targetID, now, reason)
	if err != nil {
		return nil, fmt.Errorf("memory: retire: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, apperr.Conflict("memory_not_active", NotActiveSentence)
	}
	return Get(ctx, tx, targetID)
}

// ToAPI is openapi MemoryItem.
func ToAPI(it *Item) gen.MemoryItem {
	out := gen.MemoryItem{
		Id: it.ID, WorkId: it.WorkID, Kind: gen.MemoryKind(it.Kind), Content: it.Content,
		SupportCount: it.SupportCount, Status: gen.MemoryStatus(it.Status), CreatedAt: it.CreatedAt,
	}
	promoted := it.Promoted()
	out.Promoted = &promoted
	src := it.SourceMessageID
	out.SourceMessageIds = &src
	if it.Certainty != nil {
		out.Certainty = nullable.NewNullableWithValue(gen.MemoryCertainty(*it.Certainty))
	} else {
		out.Certainty = nullable.NewNullNullable[gen.MemoryCertainty]()
	}
	if it.Outcome != nil {
		out.Outcome = nullable.NewNullableWithValue(gen.MemoryOutcome(*it.Outcome))
	} else {
		out.Outcome = nullable.NewNullNullable[gen.MemoryOutcome]()
	}
	out.Supersedes = nullUUID(it.Supersedes)
	out.SupersededBy = nullUUID(it.SupersededBy)
	if it.InvalidatedAt != nil {
		out.InvalidatedAt = nullable.NewNullableWithValue(*it.InvalidatedAt)
	} else {
		out.InvalidatedAt = nullable.NewNullNullable[time.Time]()
	}
	if it.RetireReason != nil {
		out.RetireReason = nullable.NewNullableWithValue(*it.RetireReason)
	} else {
		out.RetireReason = nullable.NewNullNullable[string]()
	}
	if r := it.reinforcedAt(); it.Kind == string(gen.MemoryKindLesson) && r != nil {
		// The contract types it as a string (no format): the same RFC 3339
		// text time.Time marshals created_at to.
		out.LastReinforcedAt = nullable.NewNullableWithValue(r.Format(time.RFC3339Nano))
	} else {
		out.LastReinforcedAt = nullable.NewNullNullable[string]()
	}
	out.CreatedBy.Name = it.AuthorName
	switch {
	case it.AuthorUserID != nil:
		out.CreatedBy.Kind, out.CreatedBy.Id = gen.MemoryItemCreatedByKindUser, *it.AuthorUserID
	case it.AuthorAgentID != nil:
		out.CreatedBy.Kind, out.CreatedBy.Id = gen.MemoryItemCreatedByKindAgent, *it.AuthorAgentID
	default:
		// The author was deleted (ON DELETE SET NULL): the kind is required,
		// and an agent wrote nearly every item.
		out.CreatedBy.Kind = gen.MemoryItemCreatedByKindAgent
	}
	return out
}

// reinforcedAt is last_reinforced_at, or created_at for a row written
// before the column was filled.
func (it *Item) reinforcedAt() *time.Time {
	if it.LastReinforcedAt != nil {
		return it.LastReinforcedAt
	}
	return &it.CreatedAt
}

func nullUUID(id *uuid.UUID) nullable.Nullable[uuid.UUID] {
	if id == nil {
		return nullable.NewNullNullable[uuid.UUID]()
	}
	return nullable.NewNullableWithValue(*id)
}
