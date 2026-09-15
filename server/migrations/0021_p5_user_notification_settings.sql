-- 0021_p5_user_notification_settings.sql — 알림 설정의 저장 자리 (T-S14: S-72)
--
-- openapi `getNotificationSettings` · `updateNotificationSettings` 는
-- `/me/notification-settings` 다 — 워크스페이스 매개변수가 없고 권한 줄이
-- "로그인한 사용자(본인)" 이며 SCREEN §4.10 도 알림 탭의 권한을 "개인" 으로
-- 둔다. 그런데 0002 가 만든 자리는 `member.notification_settings` 로
-- **워크스페이스 멤버십마다** 하나다(Q4 "member defaults"). 그 열은 어디에서도
-- 읽거나 쓰지 않았고, 사용자 하나가 워크스페이스 둘에 속하면 같은 op 가
-- 어느 행을 뜻하는지 정할 길이 없다. 계약이 말하는 단위(사용자)에 열을 둔다.
--
-- 기본값은 openapi NotificationSettings 의 default(email true · push false)
-- 와 SubscriptionLevel 의 첫 값(all) — 0002 의 member 열 기본값과 같다.
-- member.notification_settings 는 지우지 않는다(0002 는 이미 적용된 파일이라
-- 편집하지 않고, 열 삭제는 Lead 결정).
ALTER TABLE app_user ADD COLUMN IF NOT EXISTS notification_settings jsonb NOT NULL
    DEFAULT '{"email": true, "push": false, "default_subscription": "all"}';

COMMENT ON COLUMN app_user.notification_settings IS
  'openapi NotificationSettings — /me/notification-settings (S14 알림 탭, 개인). FR-8 세션 구독 기본값(default_subscription: all | hitl_only | completion_only).';
