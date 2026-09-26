-- 0025_v19_rooms.sql — 방·미션 스키마 이관 (PRD v0.19 §7 · §10 R1 · T-R1a)
--
-- 되돌릴 수 없는 유일한 단계다. 되돌리는 길은 적용 전 `pg_dump` 뿐이다(§10 R4).
-- 검증은 `server/migrations/verify/verify_0025.sql`(행 수 대조, 전부 0 이어야 한다)과
-- e2e/p5/87_migrate_0025.sh.
--
-- 설계 (plan/research/V19_impl.md §2):
--   • 이관은 "행 이동"이 아니라 "열 이동"이다. `session` 을 `room` 으로 RENAME 하면
--     자식 표의 FK·인덱스·CHECK 가 OID 로 따라오고 자식 데이터는 한 행도 움직이지 않는다.
--     자식 표의 `session_id` 열 이름은 그대로 두고 **방 id 로 읽는다**(§12.1-3, R4 까지).
--   • goal 쪽 열은 새 `work` 표로 나가고 방에서는 지운다 — 같은 사실이 두 표에 있으면
--     어느 쪽이 정본인지 코드가 갈린다. 방 1개당 미션 1개(R1a 동안 UNIQUE 로 강제).
--   • `work.id` 는 새 uuid 다(옛 session id 와 다른 값). 방 id 가 옛 session id 를
--     그대로 잇고(계약 RoomId), 미션 id 를 같은 값으로 두면 "방 id 를 미션 id 로 잘못
--     넘긴" 코드가 조용히 맞아떨어진다 — R1b 에서 두 번째 미션이 생기는 순간에야 틀린다.
--     값이 다르면 그 실수가 R1a 에서 바로 404/0행으로 드러난다. 서버 SQL 은 전부
--     `work.room_id` 로 잇는다.

-- ---------------------------------------------------------------------------
-- 0. 값 집합 (contracts/openapi.yaml 0.2.0)
-- ---------------------------------------------------------------------------
CREATE TYPE room_status             AS ENUM ('active', 'archived');                                -- RoomStatus
CREATE TYPE room_visibility         AS ENUM ('workspace', 'invited');                              -- RoomVisibility
CREATE TYPE room_blocked_reason     AS ENUM ('budget', 'runtime_offline', 'loop', 'manual');       -- RoomBlockedReason
CREATE TYPE room_role               AS ENUM ('owner', 'deputy', 'member');                         -- RoomRole
CREATE TYPE work_proposal_status    AS ENUM ('open', 'accepted', 'rejected');                      -- WorkProposalStatus
CREATE TYPE queued_reason           AS ENUM ('room_lanes', 'agent_global', 'runtime', 'workspace'); -- QueuedReason
CREATE TYPE room_read_denied_reason AS ENUM ('originator_not_participant', 'originator_left',
                                             'agent_not_allowed', 'no_originator');                -- RoomReadDeniedReason

-- ---------------------------------------------------------------------------
-- 1. session → room (개명)
-- ---------------------------------------------------------------------------
ALTER TABLE session RENAME TO room;

-- ---------------------------------------------------------------------------
-- 2. work — 옛 session 의 goal 쪽 칸
-- ---------------------------------------------------------------------------
-- status·paused_reason 은 옛 enum 을 그대로 쓴다(WorkStatus = v0.18 session_status 의
-- 상태 머신). limits·autonomy 는 방에 남고(R1a 에서 읽는 곳은 전부 방이다), 미션의
-- 칸은 "비우면 방을 따른다"(WorkLimits) — 이관분은 비워 둔다. 복사하면 같은 값이
-- 두 곳에 생기고, 방 한도를 고쳐도 미션 한도가 옛 값으로 남는다.
CREATE TABLE work (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id              uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    title                text NOT NULL,
    goal                 text NOT NULL,
    acceptance_criteria  text[] NOT NULL DEFAULT '{}',
    director_user_id     uuid NOT NULL REFERENCES app_user(id),
    deputy_user_id       uuid REFERENCES app_user(id),
    assignee_agent_id    uuid REFERENCES agent(id),
    completion_condition jsonb NOT NULL DEFAULT '{"op": "and", "conditions": [{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"}]}',
    completion_met       jsonb NOT NULL DEFAULT '{}',
    limits               jsonb NOT NULL DEFAULT '{}',
    autonomy             autonomy_level,
    status               session_status NOT NULL DEFAULT 'draft',
    paused_reason        pause_reason,
    paused_detail        jsonb,
    cost_usd             numeric(12, 4) NOT NULL DEFAULT 0,
    created_by           uuid NOT NULL REFERENCES app_user(id),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    finished_at          timestamptz,
    CONSTRAINT work_paused_check        CHECK ((status = 'paused') = (paused_reason IS NOT NULL)),
    CONSTRAINT work_paused_detail_check CHECK (paused_detail IS NULL OR paused_reason IS NOT NULL)
);
-- R1a 불변식: 방 하나에 미션 하나. 옛 `/sessions/*` 가 방 ⋈ 미션을 1:1 로 합성하고
-- 서버 SQL 이 `JOIN work ON work.room_id = room.id` 로 잇는 것이 이 인덱스 위에 서 있다
-- (두 번째 미션이 생기면 조인이 행을 불린다). R1b 가 미션 여러 개를 열 때 지운다.
CREATE UNIQUE INDEX work_room_single ON work (room_id);
CREATE INDEX work_status ON work (status);

INSERT INTO work (room_id, title, goal, acceptance_criteria, director_user_id, deputy_user_id,
                  assignee_agent_id, completion_condition, completion_met, status, paused_reason,
                  paused_detail, cost_usd, created_by, created_at, updated_at, started_at, finished_at)
SELECT id, title, goal, acceptance_criteria, director_user_id, deputy_director_user_id,
       assignee_agent_id, completion_condition, completion_met, status, paused_reason,
       paused_detail, cost_usd, created_by, created_at, updated_at, started_at, finished_at
FROM room;

-- ---------------------------------------------------------------------------
-- 3. 방 고유 칸 (PRD §7 room 행)
-- ---------------------------------------------------------------------------
ALTER TABLE room
    ADD COLUMN name                     text,
    ADD COLUMN description              text NOT NULL DEFAULT '',
    ADD COLUMN owner_user_id            uuid REFERENCES app_user(id),
    ADD COLUMN deputy_owner_user_id     uuid REFERENCES app_user(id),
    ADD COLUMN visibility               room_visibility NOT NULL DEFAULT 'workspace',
    ADD COLUMN default_director_user_id uuid REFERENCES app_user(id),
    ADD COLUMN room_status              room_status NOT NULL DEFAULT 'active',
    ADD COLUMN blocked_reason           room_blocked_reason,
    ADD COLUMN blocked_detail           jsonb;

-- §10 이관 규칙: 방 이름 = 세션 제목, 방 설명 = goal 첫 줄, 방장 = 만든 사람,
-- 완료·취소된 세션은 보관된 방.
UPDATE room SET name                     = title,
                description              = split_part(goal, E'\n', 1),
                owner_user_id            = created_by,
                default_director_user_id = director_user_id,
                room_status              = CASE WHEN status IN ('completed', 'cancelled')
                                                THEN 'archived'::room_status ELSE 'active'::room_status END;

-- 방 한도에 동시 미션 상한(RoomLimits.max_concurrent_works, 기본 3). 이미 적힌 값은 두고
-- 빠진 행에만 넣는다. 옛 `Session.limits` 는 형식 있는 구조체라 이 키는 응답에 새지 않는다.
UPDATE room SET limits = limits || '{"max_concurrent_works": 3}'::jsonb
WHERE NOT limits ? 'max_concurrent_works';
ALTER TABLE room ALTER COLUMN limits SET DEFAULT '{"max_parallel_lanes": 5, "max_concurrent_works": 3}';

-- goal 쪽 열을 방에서 지운다. 여기에 걸린 CHECK(0001 의 status↔paused_reason,
-- 0006 의 session_paused_detail_check)와 인덱스 session_workspace_status 는 열과 함께
-- 사라지고, 같은 CHECK 가 위 work 에 서 있다.
ALTER TABLE room
    DROP COLUMN title,
    DROP COLUMN goal,
    DROP COLUMN acceptance_criteria,
    DROP COLUMN director_user_id,
    DROP COLUMN deputy_director_user_id,
    DROP COLUMN assignee_agent_id,
    DROP COLUMN completion_condition,
    DROP COLUMN completion_met,
    DROP COLUMN status,
    DROP COLUMN paused_reason,
    DROP COLUMN paused_detail,
    DROP COLUMN cost_usd,
    DROP COLUMN started_at,
    DROP COLUMN finished_at;

ALTER TABLE room RENAME COLUMN room_status TO status;
ALTER TABLE room
    ALTER COLUMN name SET NOT NULL,
    ALTER COLUMN owner_user_id SET NOT NULL,
    ADD CONSTRAINT room_blocked_detail_check CHECK (blocked_detail IS NULL OR blocked_reason IS NOT NULL);

CREATE INDEX room_workspace_created ON room (workspace_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- 4. room_participant — 사람과 에이전트가 한 표 (PRD §7)
-- ---------------------------------------------------------------------------
-- 정본은 이 표다. 옛 session_participant 는 R4 까지 이관 원본으로 남지만 **쓰기를
-- 막는다**(아래 트리거) — 두 표에 나눠 쓰면 어느 쪽이 참여자 목록인지 코드가 갈린다.
-- 에이전트 행은 profile_id 를 가진다(옛 표의 NOT NULL 을 그대로 잇는다).
CREATE TABLE room_participant (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id              uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    agent_id             uuid REFERENCES agent(id),
    profile_id           uuid REFERENCES agent_profile(id),
    user_id              uuid REFERENCES app_user(id),
    role                 room_role NOT NULL DEFAULT 'member',
    joined_at            timestamptz NOT NULL DEFAULT now(),
    left_at              timestamptz,
    last_read_message_id uuid REFERENCES message(id) ON DELETE SET NULL,
    CONSTRAINT room_participant_one_side CHECK ((agent_id IS NULL) <> (user_id IS NULL)),
    CONSTRAINT room_participant_agent_profile CHECK (agent_id IS NULL OR profile_id IS NOT NULL),
    -- 역할은 사람 행에만 뜻이 있다(RoomRole) — 에이전트 행은 member.
    CONSTRAINT room_participant_agent_member CHECK (agent_id IS NULL OR role = 'member')
);
CREATE UNIQUE INDEX room_participant_agent ON room_participant (room_id, agent_id) WHERE agent_id IS NOT NULL;
CREATE UNIQUE INDEX room_participant_user  ON room_participant (room_id, user_id)  WHERE user_id IS NOT NULL;
CREATE INDEX room_participant_user_rooms ON room_participant (user_id) WHERE user_id IS NOT NULL;

-- 에이전트: 옛 표 그대로.
INSERT INTO room_participant (room_id, agent_id, profile_id, role, joined_at)
SELECT session_id, agent_id, profile_id, 'member', joined_at FROM session_participant;

-- 사람: 방장(= 만든 사람) owner, Director·deputy 는 member. 방장이 곧 Director 인
-- 흔한 경우는 한 행(owner)이다.
INSERT INTO room_participant (room_id, user_id, role, joined_at)
SELECT r.id, r.owner_user_id, 'owner', r.created_at FROM room r;
INSERT INTO room_participant (room_id, user_id, role, joined_at)
SELECT w.room_id, u.user_id, 'member', r.created_at
FROM work w JOIN room r ON r.id = w.room_id,
     LATERAL (VALUES (w.director_user_id), (w.deputy_user_id)) u(user_id)
WHERE u.user_id IS NOT NULL
ON CONFLICT DO NOTHING;

CREATE FUNCTION session_participant_frozen() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'session_participant is frozen since 0025 — write room_participant';
END;
$$;
CREATE TRIGGER session_participant_frozen_trg
    BEFORE INSERT OR UPDATE ON session_participant
    FOR EACH ROW EXECUTE FUNCTION session_participant_frozen();

-- ---------------------------------------------------------------------------
-- 5. 참고 방 · 미션 제안 · 다른 방 읽기 기록 (PRD §7, FR-4.5 · FR-2A.1)
-- ---------------------------------------------------------------------------
CREATE TABLE room_link (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id        uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    target_room_id uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    created_by     uuid NOT NULL REFERENCES app_user(id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (room_id, target_room_id),
    CHECK (room_id <> target_room_id)
);
CREATE INDEX room_link_target ON room_link (target_room_id);

CREATE TABLE work_proposal (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id             uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    proposed_by_task_id uuid REFERENCES task(id) ON DELETE SET NULL,
    goal                text NOT NULL,
    rationale           text NOT NULL,
    trigger_message_id  uuid REFERENCES message(id) ON DELETE SET NULL,
    status              work_proposal_status NOT NULL DEFAULT 'open',
    decided_by          uuid REFERENCES app_user(id),
    decided_at          timestamptz,
    reject_reason       text,
    work_id             uuid REFERENCES work(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'open') = (decided_at IS NULL))
);
CREATE INDEX work_proposal_room ON work_proposal (room_id, created_at DESC);

-- 읽은 쪽(room_id)과 읽힌 쪽(target_room_id) 양쪽 화면(S23)이 이 한 표를 읽는다.
-- task_event 는 닫힌 스키마라 여기 둔다(PRD §7).
CREATE TABLE room_read_log (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id            uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    reader_task_id     uuid REFERENCES task(id) ON DELETE SET NULL,
    reader_agent_id    uuid NOT NULL REFERENCES agent(id),
    originator_user_id uuid REFERENCES app_user(id),
    target_room_id     uuid REFERENCES room(id) ON DELETE SET NULL,
    allowed            boolean NOT NULL,
    denied_reason      room_read_denied_reason,
    summary_bytes      integer NOT NULL DEFAULT 0,
    recent_n           integer NOT NULL DEFAULT 0,
    truncated          boolean NOT NULL DEFAULT false,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CHECK (allowed = (denied_reason IS NULL))
);
CREATE INDEX room_read_log_room ON room_read_log (room_id, created_at DESC);
CREATE INDEX room_read_log_target ON room_read_log (target_room_id, created_at DESC) WHERE target_room_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 6. 자식 표의 work_id (nullable — 미션에 속하지 않는 실행이 있다, FR-2A.1)
-- ---------------------------------------------------------------------------
ALTER TABLE message      ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE lane         ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE task         ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE artifact     ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE decision     ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
ALTER TABLE hitl_request ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL;
-- 인박스는 미션·서브 미션 구독 칸을 미리 둔다(§10 R2 [V19-C]).
ALTER TABLE inbox_item   ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL,
                         ADD COLUMN lane_id uuid REFERENCES lane(id) ON DELETE SET NULL;

-- 이관분은 전부 그 방의 유일한 미션에 붙는다.
UPDATE message      c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;
UPDATE lane         c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;
UPDATE task         c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;
UPDATE artifact     c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;
UPDATE decision     c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;
UPDATE hitl_request c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;
UPDATE inbox_item   c SET work_id = w.id FROM work w WHERE w.room_id = c.session_id;

CREATE INDEX message_work      ON message (work_id)      WHERE work_id IS NOT NULL;
CREATE INDEX lane_work         ON lane (work_id)         WHERE work_id IS NOT NULL;
CREATE INDEX task_work         ON task (work_id)         WHERE work_id IS NOT NULL;
CREATE INDEX artifact_work     ON artifact (work_id)     WHERE work_id IS NOT NULL;
CREATE INDEX decision_work     ON decision (work_id)     WHERE work_id IS NOT NULL;
CREATE INDEX hitl_request_work ON hitl_request (work_id) WHERE work_id IS NOT NULL;
CREATE INDEX inbox_item_work   ON inbox_item (work_id)   WHERE work_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- 7. task.queued_reason (PRD §3.1) — 큐에 걸린 이유. 채우는 쪽은 R1b.
-- ---------------------------------------------------------------------------
ALTER TABLE task ADD COLUMN queued_reason queued_reason;
