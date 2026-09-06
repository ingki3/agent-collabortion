package tasks

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/contracts"
	"github.com/ingki3/agent-collabortion/server/internal/inbox"
)

// Profile fallback (daemon-protocol.md v0.4 §4.4). The DAEMON reports a
// failure_kind and nothing else; switching to an alternate profile is the
// SERVER's decision, for three reasons:
//
//   - the session is pinned to a runtime (FR-2.1 M10), so a server re-queue
//     structurally lands on the same machine — FR-7.1's "같은 머신 안에 대체
//     프로파일이 있으면 전환" then holds for free;
//   - attempt accounting, token issue and cost aggregation are all server-owned,
//     and an in-process swap inside the daemon blurs all three;
//   - agent_profile.fallback_profile_id already lives here.

// FallbackDecision is what the server does with a failed attempt that has a
// retryable kind.
type FallbackDecision struct {
	// ProfileID is the profile the next attempt runs on. Equal to the current
	// one when there is nothing to fall back to.
	ProfileID uuid.UUID
	Switched  bool
	// ClearResume is set when the runtime KIND changed: a Claude Code session
	// ref cannot be resumed by Hermes, so the next attempt cold-starts in the
	// same workdir (E8-08).
	ClearResume bool
	// NotifyDirector is E8-09: nothing on this machine can run the work, so the
	// task waits in `queued` and a human is told. It is NEVER handed to another
	// machine — the workdir and the runtime session live here.
	NotifyDirector bool
}

// PlanProfileFallback picks the next attempt's profile. It is pure so the
// decision is testable without a runtime; the caller supplies what the DB knows.
func PlanProfileFallback(current, fallback *uuid.UUID, currentKind, fallbackKind string, reason contracts.FailureKind) FallbackDecision {
	d := FallbackDecision{}
	if current != nil {
		d.ProfileID = *current
	}
	if !reason.Retryable() {
		// auth·quota·config: a different profile on the same machine has the
		// same broken credentials or config. Retrying is theatre (E8-10).
		return d
	}
	if fallback == nil {
		d.NotifyDirector = true
		return d
	}
	d.ProfileID, d.Switched = *fallback, true
	d.ClearResume = currentKind != fallbackKind
	return d
}

// ApplyProfileFallback re-queues taskID onto its profile's fallback, keeping
// the workdir (workdir.reuse: true is the daemon's default for a re-queue) and
// starting a new attempt. It returns whether a switch happened.
func (s *Service) ApplyProfileFallback(ctx context.Context, tx pgx.Tx, t *Row, reason contracts.FailureKind, now time.Time) (FallbackDecision, error) {
	var fallbackID *uuid.UUID
	var currentKind string
	err := tx.QueryRow(ctx, `SELECT p.fallback_profile_id, p.runtime_kind::text FROM agent_profile p WHERE p.id = $1`, t.ProfileID).
		Scan(&fallbackID, &currentKind)
	if errors.Is(err, pgx.ErrNoRows) {
		return FallbackDecision{ProfileID: t.ProfileID}, nil
	}
	if err != nil {
		return FallbackDecision{}, err
	}
	fallbackKind := currentKind
	if fallbackID != nil {
		if err := tx.QueryRow(ctx, `SELECT runtime_kind::text FROM agent_profile WHERE id = $1`, *fallbackID).Scan(&fallbackKind); err != nil {
			return FallbackDecision{}, err
		}
	}
	cur := t.ProfileID
	d := PlanProfileFallback(&cur, fallbackID, currentKind, fallbackKind, reason)
	if !d.Switched {
		return d, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE task SET profile_id = $2, updated_at = $3 WHERE id = $1`, t.ID, d.ProfileID, now); err != nil {
		return d, fmt.Errorf("tasks: profile fallback: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE lane SET profile_id = $2, updated_at = $3 WHERE id = $1`, t.LaneID, d.ProfileID, now); err != nil {
		return d, err
	}
	if d.ClearResume {
		// E8-08: the runtime changed, so there is no session to resume. The
		// workdir stays — that is where the half-finished work is.
		if _, err := tx.Exec(ctx, `UPDATE lane SET runtime_session_ref = NULL, updated_at = $2 WHERE id = $1`, t.LaneID, now); err != nil {
			return d, err
		}
	}
	t.ProfileID = d.ProfileID
	return d, nil
}

// noteNoFallback is E8-09's "Director 알림". Once per task: a three-attempt
// retry would otherwise post the same notice three times, and the Director
// learns nothing new the second time.
func noteNoFallback(ctx context.Context, tx pgx.Tx, t *Row, now time.Time) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO inbox_item (member_id, type, severity, session_id, ref_id, created_at)
		SELECT m.id, $4::inbox_item_type, $5::inbox_severity, s.id, $1, $2
		FROM session s JOIN member m ON m.workspace_id = s.workspace_id AND m.user_id = s.director_user_id
		WHERE s.id = $3
		  AND NOT EXISTS (SELECT 1 FROM inbox_item i WHERE i.ref_id = $1 AND i.type = $4::inbox_item_type)`,
		t.ID, now, t.SessionID, inbox.TypeRunFailed, inbox.Severity(inbox.TypeRunFailed))
	return err
}
