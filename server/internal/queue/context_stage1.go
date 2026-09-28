package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
)

// 맥락 1단계 (harness v0.9.14 · daemon-protocol v0.10.3, Director 승인
// 2026-09-28): ① the brief loses [6]·[7] to the turn prompt, ② a resumed turn
// carries only the messages after its anchor, ③ the server decides whether to
// resume by the session's size. This file holds the pieces buildBundle calls.

// renderRoomArtifacts is harness §10's `<room_artifacts count=N>` — the old
// brief [6] artifact part, unchanged: the surface's header line and one line
// per name at its latest version. Empty when the room has none.
func renderRoomArtifacts(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, surf Surface) (string, error) {
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT ON (name) id::text, name, type, version, COALESCE(description, '')
		FROM artifact WHERE session_id = $1
		ORDER BY name, version DESC`, roomID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	n := 0
	for rows.Next() {
		var id, name, typ, desc string
		var version int
		if err := rows.Scan(&id, &name, &typ, &version, &desc); err != nil {
			return "", err
		}
		// T-AGENTFIX B4: the id is on the line — `artifact get` takes an id.
		fmt.Fprintf(&b, "- %s (%s, v%d, id %s)", name, typ, version, id)
		if desc != "" {
			fmt.Fprintf(&b, " — %s", preview(desc, 120))
		}
		b.WriteString("\n")
		n++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return fmt.Sprintf("<room_artifacts count=%d>\n%s%s</room_artifacts>\n\n", n, surf.ArtifactsHeader, b.String()), nil
}

// renderReusedContext is harness §10's `<reused_context>` — the old brief
// [6] 이전 세션 요약 (FR-4.4), unchanged. Empty when nothing is attached or
// every attached session's summary is empty.
func renderReusedContext(ctx context.Context, tx pgx.Tx, roomID uuid.UUID, surf Surface) (string, error) {
	reuse, err := reusedSessionSummaries(ctx, tx, roomID, surf)
	if err != nil || reuse == "" {
		return "", err
	}
	return "<reused_context>\n" + ensureTrailingNewline(strings.TrimRight(reuse, "\n")) + "</reused_context>\n\n", nil
}

// contextAnchor is harness §10's 기준점: the room's latest message at the
// moment a bundle was built. The time is kept beside the id so the comparison
// still holds after the message is deleted.
type contextAnchor struct {
	ID uuid.UUID
	At time.Time
}

// recordContextAnchor writes this attempt's anchor (harness §10 「서버는
// 번들을 지을 때마다 그 attempt 의 기준점을 기록한다」). The finish that stores
// this attempt's runtime_session_ref on the lane copies it there
// (tasks.Service.Finish), so the lane's ref and anchor are always one pair.
// A room with no messages has no anchor and the column stays NULL.
func recordContextAnchor(ctx context.Context, tx pgx.Tx, roomID, taskID uuid.UUID, attempt int) (*contextAnchor, error) {
	var a contextAnchor
	err := tx.QueryRow(ctx, `
		SELECT id, created_at FROM message WHERE session_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, roomID).Scan(&a.ID, &a.At)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("queue: context anchor: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_attempt SET context_anchor_message_id = $3, context_anchor_at = $4
		WHERE task_id = $1 AND attempt = $2`, taskID, attempt, a.ID, a.At); err != nil {
		return nil, fmt.Errorf("queue: context anchor: %w", err)
	}
	return &a, nil
}

// laneContextAnchor is the anchor of the attempt that last wrote the lane's
// runtime_session_ref — nil when that attempt predates the rollout (harness
// §10: 기준점이 기록되지 않은 세션 → no delta).
func laneContextAnchor(ctx context.Context, tx pgx.Tx, laneID uuid.UUID) (*contextAnchor, error) {
	var id *uuid.UUID
	var at *time.Time
	if err := tx.QueryRow(ctx, `SELECT context_anchor_message_id, context_anchor_at FROM lane WHERE id = $1`, laneID).Scan(&id, &at); err != nil {
		return nil, fmt.Errorf("queue: lane anchor: %w", err)
	}
	if id == nil || at == nil {
		return nil, nil
	}
	return &contextAnchor{ID: *id, At: *at}, nil
}

// after reports whether m sits after the anchor in the (created_at, id)
// order every message list uses.
func (a contextAnchor) after(m *messages.Row) bool {
	if !m.CreatedAt.Equal(a.At) {
		return m.CreatedAt.After(a.At)
	}
	return strings.Compare(m.ID.String(), a.ID.String()) > 0
}

// runtimeKnowsPromptCold is daemon-protocol §3 v0.10.3: the runtime's last
// probe advertised `daemon_features: ["prompt_cold"]`. An older daemon would
// send a delta prompt to the new session it opens after a failed resume, so
// it gets no delta at all.
func runtimeKnowsPromptCold(ctx context.Context, tx pgx.Tx, runtimeID uuid.UUID) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `SELECT $2 = ANY(daemon_features) FROM runtime WHERE id = $1`, runtimeID, contracts.DaemonFeaturePromptCold).Scan(&ok)
	if isNoRows(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("queue: daemon features: %w", err)
	}
	return ok, nil
}

// sessionStartTokens is harness §6 v0.9.14's 세션 크기: the first mid-turn
// usage sample (task_context_metric.samples[0] = [s, cache_read, cache_write,
// input]) of the most recently FINISHED turn on the runtime session the lane
// would resume. ok is false when there is no such sample — a runtime without
// mid-turn usage (hermes), an attempt from before the metric table, or a read
// that failed — and the cap is then not applied.
//
// The read runs under a savepoint: a failure here must not poison the claim
// transaction (the metric table is measurement first).
func sessionStartTokens(ctx context.Context, tx pgx.Tx, laneID uuid.UUID, sessionID string) (tokens int64, ok bool) {
	if sessionID == "" {
		return 0, false
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT session_cap`); err != nil {
		return 0, false
	}
	var raw []byte
	err := tx.QueryRow(ctx, `
		SELECT samples->0 FROM task_context_metric
		WHERE lane_id = $1 AND finish_session_id = $2 AND finished_at IS NOT NULL
		ORDER BY finished_at DESC LIMIT 1`, laneID, sessionID).Scan(&raw)
	if err != nil {
		_, _ = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT session_cap`)
		return 0, false
	}
	_, _ = tx.Exec(ctx, `RELEASE SAVEPOINT session_cap`)
	var s []float64
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil || len(s) < 4 {
		return 0, false
	}
	return int64(s[1] + s[2] + s[3]), true
}

// overSessionCap is harness §6's decision: resume only while the session's
// start size is at most contracts.ResumeSessionMaxTokens.
func overSessionCap(tokens int64, ok bool) bool {
	return ok && tokens > contracts.ResumeSessionMaxTokens
}

// deltaHeadLine is harness §10's fixed line at the head of a delta prompt.
func deltaHeadLine(anchor uuid.UUID) string {
	return fmt.Sprintf("Your runtime session already holds this room's messages up to %s from earlier turns; <history> and <mission_messages> below carry only the messages after it.\n", anchor)
}
