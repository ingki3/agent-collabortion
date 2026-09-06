-- 0006_p2_routing.sql — P2 라우터·lane·루프 상한·종료 조건 (T-S2)
--
-- 0001~0005 는 건드리지 않는다. 여기서 더하는 것은 P1 이 저장할 자리가 없어
-- 판정만 하고 버렸던 상태들이다.

-- ---------------------------------------------------------------------------
-- FR-3.5 루프 상한: 어느 상한이 걸렸는지
-- ---------------------------------------------------------------------------
-- pause_reason enum 에는 `loop` 하나뿐이라 chain_depth·hops_per_hour·
-- pair_roundtrips 를 구분할 수 없다. Director 는 "loop" 만으로는 무엇을 조정해야
-- 할지 알 수 없으므로(EVAL E4-01·03·06·09 는 서로 다른 행이다) 이유를 따로 적는다.
-- enum 을 늘리지 않는 이유: pause_reason 은 "왜 멈췄나"의 분류이고 이것은 그
-- 분류 안의 세부라, 값을 섞으면 UI 가 다섯 가지 사유를 여덟 가지로 읽는다.
ALTER TABLE session ADD COLUMN pause_detail text;
ALTER TABLE session ADD CONSTRAINT session_pause_detail_check
    CHECK (pause_detail IS NULL OR paused_reason IS NOT NULL);

ALTER TABLE task ADD COLUMN paused_detail text;
ALTER TABLE task ADD CONSTRAINT task_paused_detail_check
    CHECK (paused_detail IS NULL OR paused_reason IS NOT NULL);

-- 세션의 에이전트 간 트리거 이력. 상한 세 개가 전부 이 표를 읽는다.
--   chain_depth      마지막 사람 hop 이후의 깊이
--   hops_per_hour    from_agent_id IS NOT NULL 인 행의 1시간 롤링 카운트
--   pair_roundtrips  같은 두 에이전트가 연속으로 주고받은 횟수
-- message·task 를 조인해 세지 않는 이유: 트리거가 만들어지지 '않은' hop(상한에
-- 걸려 task 가 없는 경우)도 이력에 남아야 다음 판정이 맞는다.
CREATE TABLE session_hop (
    id            bigserial PRIMARY KEY,
    session_id    uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    from_agent_id uuid REFERENCES agent(id),          -- NULL = 사람이 썼다(리셋 지점)
    to_agent_id   uuid NOT NULL REFERENCES agent(id),
    message_id    uuid REFERENCES message(id) ON DELETE SET NULL,
    rule          integer NOT NULL,                    -- 어느 FR-3.3 규칙이 만든 hop인가
    allowed       boolean NOT NULL DEFAULT true,       -- false = 상한에 걸려 task 미생성
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX session_hop_session ON session_hop (session_id, id);
CREATE INDEX session_hop_window ON session_hop (session_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- FR-6.5 합류: 그룹당 한 번만 발화한다
-- ---------------------------------------------------------------------------
-- 합류 그룹 = 같은 delegated_from_task_id 에서 나온 자식 lane 집합. 발화 여부를
-- 위임 task 에 적는다 — 규칙 8 의 억제 기간(E1-17)이 바로 이 값을 읽는다.
ALTER TABLE task ADD COLUMN join_fired_at timestamptz;

-- ---------------------------------------------------------------------------
-- FR-3.3 규칙 7: 5분 지연 assignee 폴백
-- ---------------------------------------------------------------------------
-- deferred task 가 어느 task 를 대신해 기다리는지. 주 에이전트가 응답하면 이
-- 열을 따라가 취소한다(E1-13).
ALTER TABLE task ADD COLUMN fallback_for_task_id uuid REFERENCES task(id) ON DELETE CASCADE;
CREATE INDEX task_fallback_for ON task (fallback_for_task_id) WHERE status = 'deferred';
-- deferred → queued 승격 스윕(E1-14)이 읽는다.
CREATE INDEX task_deferred_due ON task (not_before) WHERE status = 'deferred';

-- ---------------------------------------------------------------------------
-- FR-2.2 종료 조건: 충족된 원자는 거절을 넘어 살아남는다
-- ---------------------------------------------------------------------------
-- E6-04: Director 가 거절해도 artifact_submitted 플래그는 유지된다 — 아티팩트는
-- 여전히 존재하기 때문이다. 매번 다시 계산하면 그 사실을 표현할 수 없다.
ALTER TABLE session ADD COLUMN completion_met jsonb NOT NULL DEFAULT '{}';
