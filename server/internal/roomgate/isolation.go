package roomgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/hitl"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
	"github.com/ingki3/agent-collabortion/server/internal/messages"
	"github.com/ingki3/agent-collabortion/server/internal/realtime"
)

// FR-2.1.1 — what a room fixes for good on its first run, and the one choice
// it asks for before it does.
//
// A room is made from a name alone, so its isolation is the workspace default
// (`none`) and its computer is whichever claims first. That first claim pins
// the room to that computer for good — the working folders live there. When
// that computer has a repository, `none` means every agent edits the SAME
// checkout, and a chat that happens to run first would settle that forever.
// So the first dispatch waits: the room owner is asked whether to split into
// worktrees, and until they answer nothing of the room runs and nothing is
// pinned.

// PurposeIsolation is hitl_request.purpose for the question (migration r1b1_room_gate, openapi
// 0.2.2 HitlRequest.purpose `isolation`).
const PurposeIsolation = "isolation"

// Pending is room.isolation_pending: the question in flight and what an answer
// applies to — the computer that asked (it becomes the room's computer either
// way) and the repository a `worktree` answer would use.
type Pending struct {
	HitlRequestID uuid.UUID `json:"hitl_request_id"`
	RuntimeID     uuid.UUID `json:"runtime_id"`
	RepoPath      string    `json:"repo_path"`
}

// AskIsolation raises the question for a room the claim found about to make
// its first run on `runtimeID`, a computer with a repository at `repoPath`.
// The caller holds the room row (FOR UPDATE) and has checked the premise:
// isolation `none`, no computer yet, nothing pending.
//
// production caller: queue.Postgres.Claim.
func AskIsolation(ctx context.Context, tx pgx.Tx, hub *realtime.Hub, roomID, runtimeID uuid.UUID, repoPath string, now time.Time) error {
	var wsID, owner uuid.UUID
	var computer string
	if err := tx.QueryRow(ctx, `
		SELECT s.workspace_id, s.owner_user_id, r.name FROM room s JOIN runtime r ON r.id = $2 WHERE s.id = $1`,
		roomID, runtimeID).Scan(&wsID, &owner, &computer); err != nil {
		return fmt.Errorf("roomgate: isolation premise: %w", err)
	}
	question := IsolationQuestion(computer, repoPath)
	var hitlID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO hitl_request (session_id, task_id, source, type, question, approver_spec, purpose, due_at, created_at)
		VALUES ($1, NULL, 'system', 'approval', $2, $3, $4, $5, $6) RETURNING id`,
		roomID, question, hitl.SpecRoomOwner, PurposeIsolation, now.Add(hitl.DefaultDueIn), now).Scan(&hitlID); err != nil {
		return fmt.Errorf("roomgate: isolation request: %w", err)
	}
	pending, err := json.Marshal(Pending{HitlRequestID: hitlID, RuntimeID: runtimeID, RepoPath: repoPath})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE room SET isolation_pending = $2, updated_at = $3 WHERE id = $1`, roomID, pending, now); err != nil {
		return fmt.Errorf("roomgate: isolation pending: %w", err)
	}
	msgID, err := messages.PostHitlCard(ctx, hub, tx, wsID, roomID, messages.HitlCard{Type: hitl.KindApproval, Question: question}, now)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE hitl_request SET message_id = $2 WHERE id = $1`, hitlID, msgID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
		SELECT m.id, $1::inbox_item_type, $2::inbox_severity, $3, $4, $5
		FROM member m WHERE m.workspace_id = $6 AND m.user_id = $7`,
		inbox.TypeIsolationConfirm, inbox.Severity(inbox.TypeIsolationConfirm), roomID, hitlID, now, wsID, owner); err != nil {
		return fmt.Errorf("roomgate: isolation inbox: %w", err)
	}
	return nil
}

// IsolationQuestion is the request the room owner reads (FR-2.1.1). The
// default it proposes is worktree: approving splits the folders, rejecting
// keeps one shared folder.
func IsolationQuestion(computer, repoPath string) string {
	question := fmt.Sprintf("이 방의 첫 실행이 저장소가 있는 컴퓨터 %s 에서 시작됩니다 (%s). "+
		"지금 설정으로는 에이전트들이 같은 폴더를 함께 고치게 됩니다. 워크트리로 나눌까요? "+
		"승인하면 에이전트마다 워크트리를 따로 쓰고, 거절하면 한 폴더를 함께 씁니다", computer, repoPath)
	return question
}

// ErrNoPending: the room is not waiting on this question (answered already, or
// never asked).
var ErrNoPending = errors.New("roomgate: no isolation question pending")

// AnswerIsolation applies the room owner's answer to the pending question
// `hitlID`: approve → `worktree` on the repository that asked, reject →
// `none` stays. Either way the room is pinned to the computer that asked —
// the answer was about THAT computer — and the timeline says so (FR-2.1.1
// 「이 방은 〈컴퓨터〉에서 돕니다」). It returns ErrNoPending when the room has
// moved on (a second answer, or a pending question of a different id).
//
// production caller: httpapi.answerAgentHitl (purpose `isolation`).
func AnswerIsolation(ctx context.Context, tx pgx.Tx, hub *realtime.Hub, roomID, hitlID uuid.UUID, approved bool, now time.Time) error {
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT isolation_pending FROM room WHERE id = $1 FOR UPDATE`, roomID).Scan(&raw); err != nil {
		return fmt.Errorf("roomgate: isolation answer: %w", err)
	}
	if len(raw) == 0 {
		return ErrNoPending
	}
	var p Pending
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("roomgate: isolation pending: %w", err)
	}
	if p.HitlRequestID != hitlID {
		return ErrNoPending
	}
	if approved {
		iso, err := json.Marshal(map[string]any{"kind": "worktree", "repo_path": p.RepoPath})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE room SET isolation = $2 WHERE id = $1`, roomID, iso); err != nil {
			return fmt.Errorf("roomgate: isolation worktree: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `
		UPDATE room SET isolation_pending = NULL, runtime_id = $2, updated_at = $3
		WHERE id = $1 AND runtime_id IS NULL
		  AND workspace_id = (SELECT workspace_id FROM runtime WHERE id = $2)`, roomID, p.RuntimeID, now)
	if err != nil {
		return fmt.Errorf("roomgate: isolation pin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Pinned by another path meanwhile (a rebind) — the question is
		// moot, but it must not keep holding the room.
		_, err := tx.Exec(ctx, `UPDATE room SET isolation_pending = NULL, updated_at = $2 WHERE id = $1`, roomID, now)
		return err
	}
	return AnnounceComputer(ctx, tx, hub, roomID, p.RuntimeID, now)
}

// AnnounceComputer is FR-2.1.1's second notice: the moment the room's
// computer is fixed, the timeline says so — isolation and computer cannot be
// changed from here on, because the working folders are there.
//
// production callers: queue.Postgres.Claim (first dispatch pins the room) and
// AnswerIsolation.
func AnnounceComputer(ctx context.Context, q db.DBTX, hub *realtime.Hub, roomID, runtimeID uuid.UUID, now time.Time) error {
	var wsID uuid.UUID
	var computer string
	if err := q.QueryRow(ctx, `SELECT s.workspace_id, r.name FROM room s JOIN runtime r ON r.id = $2 WHERE s.id = $1`, roomID, runtimeID).
		Scan(&wsID, &computer); err != nil {
		return fmt.Errorf("roomgate: announce computer: %w", err)
	}
	body := ComputerFixedText(computer)
	var id uuid.UUID
	if err := q.QueryRow(ctx, `
		INSERT INTO message (session_id, author_type, author_id, content, kind, created_at)
		VALUES ($1, 'system', NULL, $2, 'system', $3) RETURNING id`, roomID, body, now).Scan(&id); err != nil {
		return fmt.Errorf("roomgate: announce computer: %w", err)
	}
	return messages.Publish(ctx, hub, q, wsID, roomID, id)
}

// ComputerFixedText is PRD FR-2.1.1's sentence, with the computer's name.
func ComputerFixedText(computer string) string {
	body := "이 방은 " + computer + "에서 돕니다. 격리 방식과 컴퓨터는 이제 바꿀 수 없습니다 — 작업 폴더가 거기 묶여 있습니다."
	return body
}
