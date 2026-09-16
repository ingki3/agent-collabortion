-- 0023_drop_member_notification_settings.sql — 죽은 열 삭제 (T-S20: S-75, PR #209 리뷰 NN3)
--
-- 0002 가 만든 member.notification_settings 는 0021 이 알림 설정의 정본을
-- app_user.notification_settings 로 옮긴 뒤 어디에서도 읽거나 쓰지 않는다
-- (server/ 소스 grep 0건 — auth/notifications.go 는 app_user 만 본다). 두 열이
-- 남아 있으면 이 표를 다음에 보는 사람이 "어느 쪽이 정본인가" 를 또 묻는다.
-- Lead 결정(V11_TASKS T-S20)으로 지운다. 기존 값은 옮기지 않는다 — 0021 의
-- 주석대로 그 열은 한 번도 쓰인 적이 없어 기본값뿐이다.
ALTER TABLE member DROP COLUMN IF EXISTS notification_settings;
