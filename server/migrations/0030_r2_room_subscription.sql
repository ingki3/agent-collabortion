-- r2_room_subscription — 방 알림 구독 (openapi 0.2.9 setRoomSubscription · Room.my_subscription, FR-8 · SCREEN §4.17 · T-S-r2)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 옛 세션 구독 자리(session_subscription — 0025 가 session 을 room 으로 RENAME 해
-- session_id 는 방 id 다)를 방 구독으로 쓴다. 값 집합이 다르다: 방은
-- all · my_works · hitl_only · off(RoomSubscriptionLevel), 옛 값은
-- all · hitl_only · completion_only. setSessionSubscription 은 한 번도 구현되지
-- 않아(501) 행은 없을 것이지만, 있다면 completion_only 는 대응이 없어 all 로 옮긴다
-- (행이 없을 때 개인 기본값 completion_only 를 all 로 읽는 것과 같은 규칙).
--
-- 값은 work_subscription 과 같은 text + CHECK — 미션 구독이 있으면 그 미션은 미션
-- 구독을 따른다(계약 RoomSubscriptionLevel 설명).
ALTER TABLE session_subscription ALTER COLUMN level DROP DEFAULT;
ALTER TABLE session_subscription ALTER COLUMN level TYPE text
    USING (CASE level::text WHEN 'completion_only' THEN 'all' ELSE level::text END);
ALTER TABLE session_subscription ADD CONSTRAINT session_subscription_level_check
    CHECK (level IN ('all', 'my_works', 'hitl_only', 'off'));
