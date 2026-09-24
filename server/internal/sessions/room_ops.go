package sessions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
	"github.com/ingki3/agent-collabortion/server/internal/llm"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/workdirs"
)

// Room operations that share this package's machinery — the delete cascade
// with its worktree check and gc (delete.go), and the summary writer
// (summary.go). The room API's handlers live in httpapi; the permission table
// in internal/rooms.

// ---------------------------------------------------------------------------
// deleteRoom (FR-2.6)
// ---------------------------------------------------------------------------

// RoomWorksActiveDetail is the 409 works_active: a mission is still going.
const RoomWorksActiveDetail = "진행 중인 미션이 있어 방을 삭제할 수 없습니다 — 먼저 끝내거나 취소해 주세요"

// DeleteRoom is deleteRoom's cascade for a whole room: every mission, lane,
// task, message, HITL, artifact, decision and cost row goes; one activity_log
// line (`room.deleted`) stays, and SSE `room.deleted` tells S5 to drop the
// card.
//
// Refused while any mission is in progress (409 works_active — `draft`,
// `completed` and `cancelled` are not) and while a worktree holds unmerged or
// uncommitted work (409 workdir_unmerged, the rows in Problem.workdirs).
func (s *Service) DeleteRoom(ctx context.Context, roomID, actor uuid.UUID) error {
	now := s.Clock.Now()
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return apperr.Internal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var wsID uuid.UUID
	var runtimeID *uuid.UUID
	var name string
	err = tx.QueryRow(ctx, `SELECT workspace_id, runtime_id, name FROM room WHERE id = $1 FOR UPDATE`, roomID).Scan(&wsID, &runtimeID, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.NotFound("room")
	}
	if err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete room: %w", err))
	}
	// 1:N — the missions are counted, not joined (every row of the room).
	var active int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM work WHERE room_id = $1 AND status IN ('active', 'paused', 'completing')`, roomID).Scan(&active); err != nil {
		return apperr.Internal(err)
	}
	if active > 0 {
		p := apperr.Conflict("works_active", RoomWorksActiveDetail)
		p.Extra = map[string]any{"works_active": active}
		return p
	}
	blocking, err := workdirs.UnmergedRoomWorktrees(ctx, tx, roomID)
	if err != nil {
		return apperr.Internal(err)
	}
	if len(blocking) > 0 {
		p := apperr.Conflict("workdir_unmerged", DeleteUnmergedDetail)
		p.Extra = map[string]any{"workdirs": blocking}
		return p
	}
	if err := s.gcAllWorkdirs(ctx, tx, roomID, runtimeID); err != nil {
		return apperr.Internal(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM activity_log WHERE session_id = $1`, roomID); err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete room activity: %w", err))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM room WHERE id = $1`, roomID); err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete room: %w", err))
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, session_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
		VALUES ($1, NULL, 'user', $2, 'room.deleted', 'room', $3,
		        jsonb_build_object('name', $4::text, 'room_id', $3::uuid, 'actor', $2::uuid), $5)`,
		wsID, actor, roomID, name, now); err != nil {
		return apperr.Internal(fmt.Errorf("sessions: delete room activity line: %w", err))
	}
	if s.Hub != nil {
		rid := roomID
		if err := s.Hub.Publish(ctx, tx, wsID, &rid, "room.deleted", map[string]any{"room_id": roomID}); err != nil {
			return apperr.Internal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return apperr.Internal(err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// summarizeRoom (FR-2.5 [V19-C])
// ---------------------------------------------------------------------------

// SummaryRange is what the person picked: a start time, or a message span
// (either end may be open).
type SummaryRange struct {
	Since    *time.Time
	From, To *uuid.UUID
}

// maxRangeMessages bounds one summary's input. A range wider than this is
// summarised from its most recent messages and the range record says so
// (`truncated`) — the same range still reads the same input.
const maxRangeMessages = 300

type rangeMsg struct {
	id      uuid.UUID
	author  string
	kind    string
	content string
	at      time.Time
}

// SummarizeRange writes one `kind: summary` message into the room for the
// chosen range and records the range on it (message.summary_range:
// since · from · to · the ids read · truncated). The body comes from the same
// writer as the mission's completion summary: the platform LLM when
// configured, the rows otherwise — and a refused or failed model call falls
// back to the rows, because the person pressed a button and must get a
// summary.
func (s *Service) SummarizeRange(ctx context.Context, tx pgx.Tx, wsID, roomID, requester uuid.UUID, rng SummaryRange) (*gen.Message, error) {
	lo, hi, err := rangeBounds(ctx, tx, roomID, rng)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		SELECT m.id, COALESCE(u.display_name, a.name, ''), m.kind::text, m.content, m.created_at
		FROM message m
		LEFT JOIN app_user u ON m.author_type = 'user' AND u.id = m.author_id
		LEFT JOIN agent a ON m.author_type = 'agent' AND a.id = m.author_id
		WHERE m.session_id = $1 AND m.state = 'posted'
		  AND ($2::timestamptz IS NULL OR m.created_at >= $2)
		  AND ($3::timestamptz IS NULL OR m.created_at <= $3)
		ORDER BY m.created_at DESC, m.id DESC
		LIMIT $4`, roomID, lo, hi, maxRangeMessages+1)
	if err != nil {
		return nil, fmt.Errorf("sessions: summary range: %w", err)
	}
	var msgs []rangeMsg
	for rows.Next() {
		var m rangeMsg
		if err := rows.Scan(&m.id, &m.author, &m.kind, &m.content, &m.at); err != nil {
			rows.Close()
			return nil, err
		}
		msgs = append(msgs, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	truncated := len(msgs) > maxRangeMessages
	if truncated {
		msgs = msgs[:maxRangeMessages]
	}
	// Oldest first from here on.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	if len(msgs) == 0 {
		return nil, apperr.Validation(apperr.Field("since", "empty_range", "이 범위에는 정리할 메시지가 없습니다"))
	}
	ids := make([]uuid.UUID, len(msgs))
	for i, m := range msgs {
		ids[i] = m.id
	}
	body := s.rangeBody(ctx, roomID, msgs, truncated)
	record := map[string]any{
		"from_message_id": ids[0], "to_message_id": ids[len(ids)-1], "message_ids": ids,
		"truncated": truncated, "requested_by": requester,
	}
	if rng.Since != nil {
		record["since"] = rng.Since.UTC()
	}
	now := s.Clock.Now()
	var msgID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, summary_range, created_at)
		VALUES ($1, 'system', NULL, $2, 'summary', $3, $4) RETURNING id`, roomID, body, record, now).Scan(&msgID); err != nil {
		return nil, fmt.Errorf("sessions: summary range message: %w", err)
	}
	_ = messages.Publish(ctx, s.Hub, tx, wsID, roomID, msgID)
	row, err := messages.Get(ctx, tx, msgID)
	if err != nil {
		return nil, err
	}
	out := messages.ToAPI(row)
	return &out, nil
}

// rangeBounds turns the request into [lo, hi] timestamps, checking that the
// named messages are this room's.
func rangeBounds(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, rng SummaryRange) (lo, hi *time.Time, err error) {
	if rng.Since != nil {
		t := *rng.Since
		return &t, nil, nil
	}
	at := func(id uuid.UUID, field string) (*time.Time, error) {
		var t time.Time
		err := tx.QueryRow(ctx, `SELECT created_at FROM message WHERE id = $1 AND session_id = $2`, id, roomID).Scan(&t)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Validation(apperr.Field(field, "not_in_room", "이 방의 메시지가 아닙니다"))
		}
		return &t, err
	}
	if rng.From != nil {
		if lo, err = at(*rng.From, "from_message_id"); err != nil {
			return nil, nil, err
		}
	}
	if rng.To != nil {
		if hi, err = at(*rng.To, "to_message_id"); err != nil {
			return nil, nil, err
		}
	}
	if lo != nil && hi != nil && hi.Before(*lo) {
		return nil, nil, apperr.Validation(apperr.Field("to_message_id", "order", "끝 메시지가 시작 메시지보다 앞에 있습니다"))
	}
	return lo, hi, nil
}

// rangeBody writes the summary text. The first line always states the range
// (who can check a summary they cannot trace?), then the model's prose or the
// row digest.
func (s *Service) rangeBody(ctx context.Context, roomID uuid.UUID, msgs []rangeMsg, truncated bool) string {
	header := fmt.Sprintf("**여기까지 정리** — %s ~ %s · 메시지 %d건",
		msgs[0].at.UTC().Format("2006-01-02 15:04"), msgs[len(msgs)-1].at.UTC().Format("2006-01-02 15:04"), len(msgs))
	if truncated {
		header += fmt.Sprintf(" (범위가 길어 최근 %d건만 읽었습니다)", maxRangeMessages)
	}
	digest := rangeDigest(msgs)
	if s.LLM == nil {
		return header + "\n\n" + digest
	}
	req := llm.BuildRequest(llm.JobSessionSummary)
	req.System = rangeSystemPrompt
	req.PrefixTokens = llm.EstimateTokens(rangeSystemPrompt)
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s] %s: %s\n", m.at.UTC().Format("01-02 15:04"), orSystem(m.author), m.content)
	}
	req.Prompt = b.String()
	res, err := s.LLM.Do(ctx, req)
	plan := PlanSummary(res, err, false)
	if !plan.Post {
		s.logWarn("sessions: room range summary fell back to rows", "room", roomID, "category", plan.ErrorCategory, "err", err)
		return header + "\n\n" + digest
	}
	return header + "\n\n" + plan.Body
}

const rangeSystemPrompt = `당신은 Colab 플랫폼의 대화 정리기다.

규칙:
- 입력은 한 방에서 사람이 고른 범위의 메시지 목록이다(시각, 작성자, 내용).
- 무엇이 논의되었고, 무엇이 정해졌고, 무엇이 남았는지 세 절로 짧게 정리한다.
- 입력에 없는 내용을 만들지 않는다. 사람과 에이전트의 이름은 입력 그대로 쓴다.
- 출력은 마크다운 본문만. 인사말·메타 설명·코드펜스를 넣지 않는다.`

func orSystem(author string) string {
	if author == "" {
		return "시스템"
	}
	return author
}

// rangeDigest is the no-model summary: who spoke, and the last lines.
func rangeDigest(msgs []rangeMsg) string {
	seen := map[string]bool{}
	var people []string
	for _, m := range msgs {
		if m.author != "" && !seen[m.author] {
			seen[m.author] = true
			people = append(people, m.author)
		}
	}
	var b strings.Builder
	if len(people) > 0 {
		b.WriteString("참여: " + strings.Join(people, ", ") + "\n\n")
	}
	b.WriteString("최근 내용:\n")
	start := 0
	if len(msgs) > 10 {
		start = len(msgs) - 10
	}
	for _, m := range msgs[start:] {
		line := strings.TrimSpace(m.content)
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		if r := []rune(line); len(r) > 120 {
			line = string(r[:120]) + "…"
		}
		b.WriteString("- " + orSystem(m.author) + ": " + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
