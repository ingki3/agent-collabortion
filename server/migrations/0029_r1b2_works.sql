-- r1b2_works — 한 방에 미션 여럿 (PRD v0.19 FR-2A · FR-3.1.1 · FR-8 · T-R1b2)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 0025 가 방·미션을 1:1 로 이관하면서 `work_room_single` 을 걸어 두었고, 서버 SQL 의
-- `JOIN work ON work.room_id = room.id` 는 그 UNIQUE 위에 서 있었다
-- (plan/V19_R1B_HANDOFF.md 42곳). 이 파일이 제약을 지우고, 코드는 같은 PR 에서
-- 미션을 id 로 집는다(옛 /sessions/* 는 아래 legacy_work_id 로).

-- ---------------------------------------------------------------------------
-- 1. 방당 미션 1개 해제 (FR-2A.5)
-- ---------------------------------------------------------------------------
DROP INDEX work_room_single;
CREATE INDEX work_room ON work (room_id, created_at);

-- ---------------------------------------------------------------------------
-- 2. 옛 경로로 만든 방의 표식 (R1b1 호환 규칙 「legacy single-work room」을 좁힌다)
-- ---------------------------------------------------------------------------
-- 옛 `/sessions/*` 는 방 ⋈ 미션 한 줄을 세션으로 합성한다. 방에 미션이 여럿이 되면
-- "그 세션의 미션" 이 무엇인지 따로 적어 두어야 한다 — 방의 미션 중 아무거나 집으면
-- (ORDER BY 없는 QueryRow) 어느 Director·상태를 보여 줄지 비결정적이 된다.
--
-- 값이 있는 방 = 옛 경로(createSession · 0025 이관)로 만든 방이고 값은 그 세션의
-- 미션이다. 새 방(createRoom)은 NULL 이다 — 그 방의 최상위 대화는 정직하게
-- "미션 없음"(FR-3.1.1 규칙 4)이고, 옛 /sessions/{id} 로는 보이지 않는다.
-- 그 미션을 지우면(deleteWork) 표식도 사라지고 방은 새 방처럼 산다.
ALTER TABLE room ADD COLUMN legacy_work_id uuid REFERENCES work(id) ON DELETE SET NULL;
-- 이 시점의 방은 전부 미션이 0개(createRoom) 또는 1개(옛 경로)다.
UPDATE room r SET legacy_work_id = w.id FROM work w WHERE w.room_id = r.id;
CREATE UNIQUE INDEX room_legacy_work ON room (legacy_work_id) WHERE legacy_work_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 3. 미션의 요약 메시지 · 「이걸 미션으로」의 원 메시지 (계약 Work)
-- ---------------------------------------------------------------------------
ALTER TABLE work
    ADD COLUMN summary_message_id     uuid REFERENCES message(id) ON DELETE SET NULL,
    ADD COLUMN opened_from_message_id uuid REFERENCES message(id) ON DELETE SET NULL;
-- 끝난 미션의 요약: 이 시점까지는 방에 미션이 하나라 방의 자동 요약(범위 요약 제외)이
-- 그 미션의 것이다.
UPDATE work w SET summary_message_id = (
    SELECT m.id FROM message m
    WHERE m.session_id = w.room_id AND m.kind = 'summary' AND m.summary_range IS NULL
    ORDER BY m.created_at DESC, m.id DESC LIMIT 1)
WHERE w.status = 'completed';

-- ---------------------------------------------------------------------------
-- 4. 미션 알림 구독 (openapi setWorkSubscription, FR-8 — 방 구독을 덮어쓴다)
-- ---------------------------------------------------------------------------
-- 행이 없으면 방 설정을 따른다(level null = 행 삭제).
CREATE TABLE work_subscription (
    work_id    uuid NOT NULL REFERENCES work(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    level      text NOT NULL CHECK (level IN ('all', 'hitl_only', 'completion_only')),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (work_id, user_id)
);

-- ---------------------------------------------------------------------------
-- 5. 미션 제안의 에이전트 (계약 WorkProposal.agent — 필수)
-- ---------------------------------------------------------------------------
-- proposed_by_task_id 는 task 가 지워지면 NULL 이 된다(ON DELETE SET NULL). 제안이
-- 누구의 것인지는 task 보다 오래 남아야 한다 — 거절 알림도 그 에이전트에게 간다.
ALTER TABLE work_proposal ADD COLUMN agent_id uuid REFERENCES agent(id);
UPDATE work_proposal p SET agent_id = t.agent_id FROM task t WHERE t.id = p.proposed_by_task_id;
CREATE INDEX work_proposal_open ON work_proposal (room_id) WHERE status = 'open';

-- ---------------------------------------------------------------------------
-- 6. work_id 메우기 (R1b1 인계 「아직 work_id 를 쓰지 않는 새 행」)
-- ---------------------------------------------------------------------------
-- R1b1 뒤로도 artifact · decision · 대부분의 inbox_item · 종료 조건 승인 hitl_request 는
-- work_id 없이 쓰였다. 이 시점의 방은 미션이 0개 또는 1개이고, 1개인 방(옛 경로)은
-- 호환 규칙이 모든 실행을 그 미션에 붙였으므로 그 미션이 정답이다. 이 파일 뒤로는
-- 코드가 행을 쓸 때 채운다 — 방에 미션이 여럿이면 "방의 유일한 미션" 이 없다.
-- message · lane · task 는 R1b1 의 귀속 규칙이 이미 채웠다(시스템 알림처럼 방의 것은 비워 둔 채로).
UPDATE artifact     c SET work_id = r.legacy_work_id FROM room r WHERE r.id = c.session_id AND c.work_id IS NULL AND r.legacy_work_id IS NOT NULL;
UPDATE decision     c SET work_id = r.legacy_work_id FROM room r WHERE r.id = c.session_id AND c.work_id IS NULL AND r.legacy_work_id IS NOT NULL;
UPDATE hitl_request c SET work_id = r.legacy_work_id FROM room r WHERE r.id = c.session_id AND c.work_id IS NULL AND r.legacy_work_id IS NOT NULL
                                                        AND c.approver_spec <> 'room_owner';
UPDATE inbox_item   c SET work_id = r.legacy_work_id FROM room r WHERE r.id = c.session_id AND c.work_id IS NULL AND r.legacy_work_id IS NOT NULL
                                                        AND c.type NOT IN ('room_paused', 'isolation_confirm');
-- 범위 요약(「여기까지 정리」, summary_range 있음)은 방의 것이라 미션에 붙이지 않는다.
-- 미션 완료 요약(summary_range 없음)만 그 미션의 것이다.
UPDATE message c SET work_id = r.legacy_work_id FROM room r
 WHERE r.id = c.session_id AND c.work_id IS NULL AND r.legacy_work_id IS NOT NULL
   AND c.kind = 'summary' AND c.summary_range IS NULL;
