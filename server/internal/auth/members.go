package auth

// 멤버 역할 변경·제거 — openapi updateMemberRole · removeMember (S14 멤버 탭, S-72).
//
// 판정은 순수 함수(PlanRoleChange · PlanRemoval)에 두고 DB 는 그 입력(역할·
// 소유자 수·Director 인 진행 중 세션 수)만 읽는다 — 권한 4상태와 409 두 조건을
// DB 없이 표로 재기 위해서다(runtimes.PlanRuntimeDelete 와 같은 모양).
//
// 계약 문장(openapi):
//   updateMemberRole  권한: owner · admin. owner 강등은 owner 만(SCREEN §2.3).
//                     마지막 owner 는 강등할 수 없다(409).
//   removeMember      권한: owner · admin. 마지막 owner 는 제거할 수 없다(409).
//                     그 멤버가 Director 인 활성 세션이 있으면 409(먼저 Director 를 교체).
//
// 계약이 말하지 않아 여기서 정한 것(PR 본문에 적는다 — Lead 가 풀 수 있다):
//   - 소유자로 **올리는** 것도 소유자만 한다. "owner 강등은 owner 만" 의 취지가
//     관리자가 소유자 층을 건드리지 못하게 하는 것이라면, 관리자가 자기를
//     소유자로 올리는 길을 열어 두는 순간 그 규칙은 비어 버린다.
//   - 소유자를 **내보내는** 것도 소유자만 한다(강등보다 약한 동작이 아니다).
//   - "활성 세션" 은 runtimes.blockingSessions 와 같은 집합 — draft · active ·
//     paused · completing(끝나지 않은 세션 전부). 사라진 Director 를 가진
//     draft 도 시작할 사람이 없기는 마찬가지다.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// Problem codes the screens can branch on (web/lib/mock/handlers.ts uses the
// same three).
const (
	CodeOwnerOnly        = "owner_only"
	CodeLastOwner        = "last_owner"
	CodeMemberIsDirector = "member_is_director"
)

// ErrMemberNotFound — no member with that id in this workspace. The handler
// turns it into the 404 sentence (the id may exist in another workspace; the
// caller is not told which).
var ErrMemberNotFound = errors.New("auth: member not found in workspace")

// RoleChangeCase is what PlanRoleChange decides on.
type RoleChangeCase struct {
	CallerRole string // owner | admin (member never reaches here — the handler's admin check)
	TargetRole string // the member's current role
	NewRole    string // requested role
	OwnerCount int    // owners in the workspace right now, target included
}

// PlanRoleChange returns nil when the change may proceed, else the Problem.
func PlanRoleChange(c RoleChangeCase) *apperr.Problem {
	touchesOwner := c.TargetRole == "owner" || c.NewRole == "owner"
	if touchesOwner && c.CallerRole != "owner" {
		return apperr.Forbidden(CodeOwnerOnly, "소유자 역할을 주거나 거두는 것은 소유자만 할 수 있습니다")
	}
	if c.TargetRole == "owner" && c.NewRole != "owner" && c.OwnerCount <= 1 {
		return apperr.Conflict(CodeLastOwner, "마지막 소유자는 강등할 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요")
	}
	return nil
}

// RemovalCase is what PlanRemoval decides on.
type RemovalCase struct {
	CallerRole       string
	TargetRole       string
	OwnerCount       int
	DirectorSessions int // unfinished sessions whose Director is the target user
}

// PlanRemoval returns nil when the member may be removed, else the Problem.
func PlanRemoval(c RemovalCase) *apperr.Problem {
	if c.TargetRole == "owner" && c.CallerRole != "owner" {
		return apperr.Forbidden(CodeOwnerOnly, "소유자를 내보내는 것은 소유자만 할 수 있습니다")
	}
	if c.TargetRole == "owner" && c.OwnerCount <= 1 {
		return apperr.Conflict(CodeLastOwner, "마지막 소유자는 내보낼 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요")
	}
	if c.DirectorSessions > 0 {
		return apperr.Conflict(CodeMemberIsDirector,
			fmt.Sprintf("이 멤버가 Director 인 진행 중 세션이 %d개 있습니다 — 먼저 그 세션의 Director 를 교체해 주세요", c.DirectorSessions))
	}
	return nil
}

// activeSessionStatuses is the "활성 세션" set — the same rows
// runtimes.blockingSessions refuses to orphan.
const activeSessionStatuses = `('draft', 'active', 'paused', 'completing')`

// memberRow is the target as the planners see it, read under the workspace
// row lock.
type memberRow struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Role       string
	OwnerCount int
}

// afterLockMember runs between lockMember's read and the planner's write —
// nil in production. It is the seam TestUpdateMemberRoleConcurrentDemotion
// (S-75, PR #209 리뷰 NN2) widens the window with: without a delay here the
// two demotions in one process never overlap and the test proves nothing
// about the lock.
var afterLockMember func()

// lockMember locks the workspace row (so two concurrent demotions cannot both
// see "two owners") and reads the target member plus the owner count.
func lockMember(ctx context.Context, tx pgx.Tx, wsID, memberID uuid.UUID) (*memberRow, error) {
	var ws uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM workspace WHERE id = $1 FOR UPDATE`, wsID).Scan(&ws); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("workspace")
		}
		return nil, err
	}
	var m memberRow
	err := tx.QueryRow(ctx, `
		SELECT m.id, m.user_id, m.role::text,
		       (SELECT count(*) FROM member o WHERE o.workspace_id = m.workspace_id AND o.role = 'owner')
		FROM member m WHERE m.workspace_id = $1 AND m.id = $2`, wsID, memberID).
		Scan(&m.ID, &m.UserID, &m.Role, &m.OwnerCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMemberNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// UpdateMemberRole is openapi updateMemberRole. callerRole is the caller's
// own membership role (already known to be owner or admin). The change is
// recorded in activity_log as member.role_changed (PRD §7 — a role change is
// something someone later asks "who did that" about).
func (s *Service) UpdateMemberRole(ctx context.Context, wsID, memberID, callerUserID uuid.UUID, callerRole, newRole string) (*gen.Member, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	m, err := lockMember(ctx, tx, wsID, memberID)
	if err != nil {
		return nil, err
	}
	if afterLockMember != nil {
		afterLockMember()
	}
	if p := PlanRoleChange(RoleChangeCase{CallerRole: callerRole, TargetRole: m.Role, NewRole: newRole, OwnerCount: m.OwnerCount}); p != nil {
		return nil, p
	}
	if m.Role != newRole {
		if _, err := tx.Exec(ctx, `UPDATE member SET role = $3 WHERE workspace_id = $1 AND id = $2`, wsID, memberID, newRole); err != nil {
			return nil, fmt.Errorf("auth: update member role: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO activity_log (workspace_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
			VALUES ($1, 'user', $2, 'member.role_changed', 'member', $3, jsonb_build_object('user_id', $4::uuid, 'from', $5::text, 'to', $6::text), $7)`,
			wsID, callerUserID, memberID, m.UserID, m.Role, newRole, s.Clock.Now()); err != nil {
			return nil, fmt.Errorf("auth: log role change: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	out, err := s.member(ctx, wsID, m.UserID)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, ErrMemberNotFound
	}
	return out, nil
}

// RemoveMember is openapi removeMember. The member row goes away (inbox items
// and subscriptions cascade with it — 0001 · 0002); the user account stays.
// Recorded in activity_log as member.removed.
func (s *Service) RemoveMember(ctx context.Context, wsID, memberID, callerUserID uuid.UUID, callerRole string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	m, err := lockMember(ctx, tx, wsID, memberID)
	if err != nil {
		return err
	}
	var directing int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM session WHERE workspace_id = $1 AND director_user_id = $2 AND status IN `+activeSessionStatuses,
		wsID, m.UserID).Scan(&directing); err != nil {
		return err
	}
	if p := PlanRemoval(RemovalCase{CallerRole: callerRole, TargetRole: m.Role, OwnerCount: m.OwnerCount, DirectorSessions: directing}); p != nil {
		return p
	}
	if _, err := tx.Exec(ctx, `DELETE FROM member WHERE workspace_id = $1 AND id = $2`, wsID, memberID); err != nil {
		return fmt.Errorf("auth: remove member: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO activity_log (workspace_id, actor_type, actor_id, action, object_type, object_id, payload, created_at)
		VALUES ($1, 'user', $2, 'member.removed', 'member', $3, jsonb_build_object('user_id', $4::uuid, 'role', $5::text), $6)`,
		wsID, callerUserID, memberID, m.UserID, m.Role, s.Clock.Now()); err != nil {
		return fmt.Errorf("auth: log removal: %w", err)
	}
	return tx.Commit(ctx)
}
