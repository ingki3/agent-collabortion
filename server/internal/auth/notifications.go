package auth

// 알림 설정(개인) — openapi getNotificationSettings · updateNotificationSettings
// (S14 알림 탭, FR-8 미션 구독 기본값). 저장 자리는 app_user.notification_settings
// (0021 — 왜 member 열이 아닌지는 그 파일 머리).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ingki3/agent-collabortion/server/internal/apperr"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// DefaultNotificationSettings is the openapi default (email true · push false ·
// default_subscription all) — also the 0021 column default.
func DefaultNotificationSettings() gen.NotificationSettings {
	return gen.NotificationSettings{Email: true, Push: false, DefaultSubscription: gen.SubscriptionLevelAll}
}

// NotificationPatch is a partial update: a nil field keeps the stored value.
// The screen sends all three; a CLI or a later screen may send one.
type NotificationPatch struct {
	Email               *bool
	Push                *bool
	DefaultSubscription *string
}

// ValidateSubscriptionLevel is the 422 for a level outside the enum.
func ValidateSubscriptionLevel(level string) *apperr.Problem {
	switch gen.SubscriptionLevel(level) {
	case gen.SubscriptionLevelAll, gen.SubscriptionLevelHitlOnly, gen.SubscriptionLevelCompletionOnly:
		return nil
	}
	return apperr.Validation(apperr.Field("default_subscription", "enum", "구독 기본값은 전부 · 사람 확인만 · 종료만 중 하나여야 합니다"))
}

// ApplyNotificationPatch is the pure merge: stored + patch → next.
func ApplyNotificationPatch(cur gen.NotificationSettings, p NotificationPatch) gen.NotificationSettings {
	if p.Email != nil {
		cur.Email = *p.Email
	}
	if p.Push != nil {
		cur.Push = *p.Push
	}
	if p.DefaultSubscription != nil {
		cur.DefaultSubscription = gen.SubscriptionLevel(*p.DefaultSubscription)
	}
	return cur
}

// decodeNotificationSettings reads the jsonb column, filling anything a
// pre-0021 row (or a hand-edited one) left out with the default.
func decodeNotificationSettings(raw []byte) gen.NotificationSettings {
	out := DefaultNotificationSettings()
	var in struct {
		Email               *bool   `json:"email"`
		Push                *bool   `json:"push"`
		DefaultSubscription *string `json:"default_subscription"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return out
	}
	p := NotificationPatch{Email: in.Email, Push: in.Push}
	if in.DefaultSubscription != nil && ValidateSubscriptionLevel(*in.DefaultSubscription) == nil {
		p.DefaultSubscription = in.DefaultSubscription
	}
	return ApplyNotificationPatch(out, p)
}

// NotificationSettings returns the user's settings (the default when never set).
func (s *Service) NotificationSettings(ctx context.Context, userID uuid.UUID) (*gen.NotificationSettings, error) {
	var raw []byte
	err := s.DB.QueryRow(ctx, `SELECT notification_settings FROM app_user WHERE id = $1`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("user")
	}
	if err != nil {
		return nil, err
	}
	out := decodeNotificationSettings(raw)
	return &out, nil
}

// UpdateNotificationSettings merges the patch into the stored settings and
// returns the result. Only the user's own row — the handler resolves userID
// from the principal, never from the request.
func (s *Service) UpdateNotificationSettings(ctx context.Context, userID uuid.UUID, p NotificationPatch) (*gen.NotificationSettings, error) {
	if p.DefaultSubscription != nil {
		if prob := ValidateSubscriptionLevel(*p.DefaultSubscription); prob != nil {
			return nil, prob
		}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var raw []byte
	err = tx.QueryRow(ctx, `SELECT notification_settings FROM app_user WHERE id = $1 FOR UPDATE`, userID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, apperr.NotFound("user")
	}
	if err != nil {
		return nil, err
	}
	next := ApplyNotificationPatch(decodeNotificationSettings(raw), p)
	buf, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE app_user SET notification_settings = $2 WHERE id = $1`, userID, buf); err != nil {
		return nil, fmt.Errorf("auth: update notification settings: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &next, nil
}
