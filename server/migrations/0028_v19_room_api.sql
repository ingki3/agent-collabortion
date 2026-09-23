-- v19_room_api — 방 API 가 쓰는 저장 자리 (PRD v0.19 FR-2 · FR-4.5 · FR-8 · T-R1b3)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 0025 가 방·참여자·링크 표를 세웠고, 이 파일은 방 API 가 쓰는데 자리가 없던 것만 더한다.

-- ---------------------------------------------------------------------------
-- 1. 서브 미션 알림 구독 (openapi setLaneSubscription, FR-8 — 스레드 단위)
-- ---------------------------------------------------------------------------
-- 행이 없으면 방·미션 구독을 따른다. 끄는 것도 행이다(enabled = false) — 지우면
-- "따른다"로 돌아가고, 방 구독이 all 이면 끈 스레드가 다시 울린다.
CREATE TABLE lane_subscription (
    lane_id    uuid NOT NULL REFERENCES lane(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    enabled    boolean NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (lane_id, user_id)
);

-- ---------------------------------------------------------------------------
-- 2. 워크스페이스 설정 — 새 방의 기본값 (계약 RoomDefaults). room_read 는 R1c(#290) 몫.
-- ---------------------------------------------------------------------------
-- room_defaults 는 비워 두면 계약 기본값을 따른다(createRoom 이 채운다). 격리 기본값은
-- 옛 default_isolation 열이 계속 정본이다 — room_defaults.isolation_kind 가 있으면 그쪽이 이긴다.
ALTER TABLE workspace_settings
    ADD COLUMN room_defaults jsonb NOT NULL DEFAULT '{}';

-- ---------------------------------------------------------------------------
-- 3. 「여기까지 정리」 범위 (openapi summarizeRoom, FR-2.5 [V19-C])
-- ---------------------------------------------------------------------------
-- 요약 메시지(kind = summary)가 어느 범위를 읽었는지 — {since?, from_message_id?,
-- to_message_id?, message_ids[]}. 미션이 끝날 때 쓰는 자동 요약(FR-2A.4)은 NULL 이다.
-- 이 칸으로 둘을 가른다: 자동 요약의 "한 번만" 검사가 사람이 누른 범위 요약을 세면
-- 미션 완료 요약이 조용히 빠진다.
ALTER TABLE message ADD COLUMN summary_range jsonb;
ALTER TABLE message ADD CONSTRAINT message_summary_range_kind CHECK (summary_range IS NULL OR kind = 'summary');

-- ---------------------------------------------------------------------------
-- 4. 받은 요청 — v0.2.0 새 타입 · 받는 근거 (계약 InboxItemType · InboxItem.recipient_basis)
-- ---------------------------------------------------------------------------
-- 값만 더한다(쓰는 쪽은 코드). room_paused · isolation_confirm 은 r1b1_room_gate 가 이미
-- 넣었다(나머지 v0.2.0 값은 "그 값을 쓰는 PR 이 넣는다" — 여기). work_* 는 미션 스트림이
-- 발행하지만 enum 은 한 번에 계약과 맞춘다.
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'work_proposed';
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'work_paused';
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'work_completed';
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'room_invited';
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'workdir_quota';

ALTER TABLE inbox_item ADD COLUMN recipient_basis text
    CHECK (recipient_basis IN ('director', 'deputy', 'room_owner', 'room_deputy', 'workspace_owner'));

-- ---------------------------------------------------------------------------
-- 5. 방 목록 한 쿼리가 쓰는 인덱스
-- ---------------------------------------------------------------------------
-- 지금 참여 중인 사람 행(안 읽음·my_room_role·참여자 줄)과 방 활동 로그.
CREATE INDEX room_participant_live ON room_participant (room_id) WHERE left_at IS NULL;

-- ---------------------------------------------------------------------------
-- 6. 한 사람에게만 가는 SSE 프레임 (StreamEvent `room.unread` — "내 다른 탭·기기에도")
-- ---------------------------------------------------------------------------
-- NULL 이면 워크스페이스의 볼 수 있는 사람 모두. 값이 있으면 그 사람의 스트림에만
-- 흐르고 재연결 백필도 그 사람에게만 한다 — 남의 안 읽음 수가 옆 사람 배지에 뜨지 않게.
ALTER TABLE stream_event ADD COLUMN user_id uuid;

-- ---------------------------------------------------------------------------
-- 7. 감사 열람 (FR-5.3 P-Q) — ws owner·admin 이 참여하지 않은 invited 방을 본 기록은
--    같은 사람·같은 방·같은 날(UTC) 한 줄. 목록 스크롤·재조회마다 쌓이지 않게 하는
--    판정을 DB 가 쥔다(동시 조회 두 개도 한 줄) — 쓰는 쪽은 ON CONFLICT DO NOTHING.
-- ---------------------------------------------------------------------------
CREATE UNIQUE INDEX activity_log_audit_viewed_daily
    ON activity_log (actor_id, session_id, ((created_at AT TIME ZONE 'UTC')::date))
    WHERE action = 'room.audit_viewed';
