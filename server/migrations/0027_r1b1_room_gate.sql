-- r1b1_room_gate — 방 단위 게이트 (PRD v0.19 FR-2.4 · FR-2A.3 · FR-2.1.1 · FR-3.1.1 · T-R1b1)
--
-- 0025 가 칸을 만들었고(room.blocked_reason · task.queued_reason · *.work_id), 이 파일은
-- R1b1 이 그 칸을 채우는 데 필요한 나머지다.

-- ---------------------------------------------------------------------------
-- 1. 인박스 항목 타입 (openapi 0.2.0 InboxItemType) — 방 전체 멈춤과 격리 확인.
--    나머지 v0.2.0 값(work_*·room_invited·workdir_quota)은 그 값을 쓰는 PR 이 넣는다.
-- ---------------------------------------------------------------------------
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'room_paused';
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'isolation_confirm';

-- ---------------------------------------------------------------------------
-- 2. 시스템 HITL 의 용도에 격리 확인(FR-2.1.1, openapi 0.2.2 HitlRequest.purpose `isolation`).
--    purpose 는 source=system + approval 을 가르는 판정 기준이라(0012) 값이 없으면
--    승인(worktree)·거절(none) 분기를 탈 수 없다.
-- ---------------------------------------------------------------------------
ALTER TABLE hitl_request DROP CONSTRAINT hitl_request_purpose_ck;
ALTER TABLE hitl_request ADD CONSTRAINT hitl_request_purpose_ck
    CHECK (purpose IS NULL OR purpose IN ('agent', 'user_approval', 'budget', 'time', 'loop', 'isolation'));

-- ---------------------------------------------------------------------------
-- 3. 격리 확인 대기 (FR-2.1.1). 값이 있는 동안 그 방의 첫 dispatch 를 보류하고
--    runtime_id 를 고정하지 않는다. {hitl_request_id, runtime_id, repo_path} —
--    답이 오면 그 컴퓨터로 고정하고 승인이면 그 저장소로 worktree 가 된다.
-- ---------------------------------------------------------------------------
ALTER TABLE room ADD COLUMN isolation_pending jsonb;

-- ---------------------------------------------------------------------------
-- 4. work_id 메우기. 0025 뒤 R1b1 전까지 새 행을 쓴 코드는 work_id 를 몰랐다(R1a 열린
--    항목). 그 구간의 방은 전부 미션이 하나(work_room_single)이고 미션 밖 실행을 만드는
--    길이 없었으므로 그 미션이 정답이다. 이 파일 뒤로는 R1b1 코드가 귀속을 정한다.
-- ---------------------------------------------------------------------------
UPDATE message      c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;
UPDATE lane         c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;
UPDATE task         c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;
UPDATE artifact     c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;
UPDATE decision     c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;
UPDATE hitl_request c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;
UPDATE inbox_item   c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id AND c.work_id IS NULL;

-- ---------------------------------------------------------------------------
-- 5. 루프 상한의 조회 창 (NN3 · V19_impl §4 위험 3). 깊이는 "마지막 사람 hop 이후 전부"
--    를 읽는다 — 그 hop 을 찾는 인덱스.
-- ---------------------------------------------------------------------------
CREATE INDEX session_hop_human ON session_hop (session_id, id) WHERE from_agent_id IS NULL;

-- ---------------------------------------------------------------------------
-- 6. approver_spec 에 room_owner (openapi 0.2.0 — 미션 밖 task · 방 상한 · 격리 확인,
--    부재 위임 FR-2A.3). 0001 의 CHECK 는 이름 없이 만들어져 PG 가 붙인 이름을 쓴다.
-- ---------------------------------------------------------------------------
ALTER TABLE hitl_request DROP CONSTRAINT hitl_request_approver_spec_check;
ALTER TABLE hitl_request ADD CONSTRAINT hitl_request_approver_spec_check
    CHECK (approver_spec IN ('director', 'room_owner', 'any_member')
           OR approver_spec ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$');
