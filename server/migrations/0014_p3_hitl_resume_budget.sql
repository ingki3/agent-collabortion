-- 0014_p3_hitl_resume_budget.sql — P3 (T-S5): HITL 전체 · 재개 · 예산 · 취소
--
-- 0001~0012 는 건드리지 않는다(P3_TASKS §2 T-S5 금지). 여기서 더하는 것은 계약
-- (openapi `HitlRequest` · `Decision`)이 이미 내보내라고 적었는데 적을 칸이 없어
-- 서버가 한 번도 만든 적 없는 값들이다.

-- ---------------------------------------------------------------------------
-- 1. HITL 요청의 빈칸 (openapi HitlRequest)
-- ---------------------------------------------------------------------------
-- context             `--context` / `--why` / approval 의 부연. 인박스 카드 본문이라
--                     없으면 Director 가 세션을 열어야 답할 수 있다(SCREEN §4.6).
-- artifact_id         approval 대상 아티팩트(`hitl approve-request --artifact`).
-- budget_override_usd 예산 HITL 승인으로 기록된 상향값. task.budget_override 와
--                     둘 다 필요하다 — 하나는 강제 시점에 읽는 유효 한도(E9-08),
--                     다른 하나는 "그때 얼마를 승인했는가"라는 요청의 기록이다.
-- message_id          타임라인에 게시한 hitl 카드(계약 createHitlRequest 201).
-- requeue_held        E10-08. 킬 스위치로 disabled 된 에이전트의 HITL 에 사람이
--                     답하면 답은 기록하되 재큐잉을 보류한다. 보류가 명시 상태가
--                     아니면 다시 활성화할 때 풀어 줄 대상을 찾을 수 없다.
ALTER TABLE hitl_request ADD COLUMN context             text;
ALTER TABLE hitl_request ADD COLUMN artifact_id         uuid REFERENCES artifact(id) ON DELETE SET NULL;
ALTER TABLE hitl_request ADD COLUMN budget_override_usd numeric(12, 4) CHECK (budget_override_usd IS NULL OR budget_override_usd >= 0);
ALTER TABLE hitl_request ADD COLUMN message_id          uuid REFERENCES message(id) ON DELETE SET NULL;
ALTER TABLE hitl_request ADD COLUMN requeue_held        boolean NOT NULL DEFAULT false;

CREATE INDEX hitl_request_requeue_held ON hitl_request (task_id) WHERE requeue_held;

-- 0001 의 이름 없는 CHECK (status = 'open' OR answered_at IS NOT NULL) 은 0013 의
-- `cancelled` 를 막는다 — 취소는 답이 아니라 answered_at 이 없다. 생성된 이름을
-- 알 수 없으므로 정의로 찾아 바꾼다.
DO $$
DECLARE c text;
BEGIN
    SELECT conname INTO c FROM pg_constraint
     WHERE conrelid = 'hitl_request'::regclass AND contype = 'c'
       AND pg_get_constraintdef(oid) LIKE '%answered_at%';
    IF c IS NOT NULL THEN
        EXECUTE format('ALTER TABLE hitl_request DROP CONSTRAINT %I', c);
    END IF;
END $$;

ALTER TABLE hitl_request ADD CONSTRAINT hitl_request_answered_ck
    CHECK (status IN ('open', 'cancelled') OR answered_at IS NOT NULL);

-- 열린 요청은 task 당 하나(0001 의 부분 유니크 인덱스)이고 `cancelled` 는 열린
-- 상태가 아니므로 그 인덱스는 그대로 맞다 — 취소된 요청은 다음 요청을 막지 않는다.

-- ---------------------------------------------------------------------------
-- 2. decision.auto (openapi Decision.auto, E7-12)
-- ---------------------------------------------------------------------------
-- "Director 가 투자자라고 답했다"와 "아무도 답하지 않아 에이전트 제안으로
-- 진행했다"를 결정 기록에서 구분하는 칸. 계약에 필드가 있고 서버는 늘 비웠다.
ALTER TABLE decision ADD COLUMN auto boolean NOT NULL DEFAULT false;
