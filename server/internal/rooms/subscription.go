package rooms

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ingki3/agent-collabortion/server/internal/db"
	"github.com/ingki3/agent-collabortion/server/internal/httpapi/gen"
)

// Room notification subscription (openapi 0.2.9 setRoomSubscription ·
// Room.my_subscription, FR-8 · SCREEN §4.17). Stored in the old session
// subscription row (session_subscription — session_id is the room's id since
// 0025), values RoomSubscriptionLevel. A mission's own subscription
// (work_subscription) overrides the room's for that mission; there is no
// delivery path reading either yet, so the rule lives in the contract and here.

// SubscriptionLevel is Room.my_subscription: the stored level, or — with no
// row — the person's default (NotificationSettings.default_subscription)
// where the two value sets meet. `completion_only` has no room counterpart
// and reads as `all` (the migration moves an old row the same way).
func SubscriptionLevel(stored *string, personalDefault string) gen.RoomSubscriptionLevel {
	if stored != nil && gen.RoomSubscriptionLevel(*stored).Valid() {
		return gen.RoomSubscriptionLevel(*stored)
	}
	if personalDefault == string(gen.SubscriptionLevelHitlOnly) {
		return gen.RoomSubscriptionLevelHitlOnly
	}
	return gen.RoomSubscriptionLevelAll
}

// MySubscription reads the caller's level for the room.
func MySubscription(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID) (gen.RoomSubscriptionLevel, error) {
	var stored, def *string
	err := q.QueryRow(ctx, `
		SELECT s.level, u.notification_settings->>'default_subscription'
		  FROM app_user u
		  LEFT JOIN session_subscription s ON s.session_id = $1 AND s.user_id = u.id
		 WHERE u.id = $2`, roomID, userID).Scan(&stored, &def)
	if err != nil {
		return "", fmt.Errorf("rooms: subscription: %w", err)
	}
	return SubscriptionLevel(stored, derefStr(def)), nil
}

// SetSubscription stores the caller's level (upsert — one row per person).
func SetSubscription(ctx context.Context, q db.DBTX, roomID, userID uuid.UUID, level gen.RoomSubscriptionLevel, now time.Time) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO session_subscription (session_id, user_id, level, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (session_id, user_id) DO UPDATE SET level = EXCLUDED.level, updated_at = EXCLUDED.updated_at`,
		roomID, userID, string(level), now); err != nil {
		return fmt.Errorf("rooms: set subscription: %w", err)
	}
	return nil
}
