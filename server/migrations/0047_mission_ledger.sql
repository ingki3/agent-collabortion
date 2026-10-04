-- 미션 상태 원장 (T-LEDGER, PRD FR-4.6 · openapi v0.3.12 · harness v0.9.18, Director 결정 2026-10-05, 맥락 2단계)
--
-- 번호는 머지 순서로 정해진다(Lead 규칙) — dev 의 마지막이 0046(T-CARD-S)이라 이 PR 은 0047 이다.
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- memory_item: 미션의 원장 항목. 쓰기는 새 행 추가뿐이다 — 기존 행의 내용(kind·content·certainty·
-- outcome·source·작성자·시각)은 고쳐 쓰지 않고, 상태 칸 셋(status · superseded_by · invalidated_at)만
-- 바뀐다(대체·철회, PRD FR-4.6 1). 행은 지우지 않는다 — 미션이 지워질 때만 함께 간다.
--
-- 예외 하나: lesson 의 support_count 는 같은 내용이 다시 기록될 때 그 행에서 +1 한다(새 행을 만들지
-- 않는다 — T-LEDGER 브리프, 「같은 내용을 가리키는 독립 항목 수」 PRD FR-4.6 표).
CREATE TABLE memory_item (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    work_id             uuid NOT NULL REFERENCES work(id) ON DELETE CASCADE,
    kind                text NOT NULL
                        CHECK (kind IN ('fact', 'assignment', 'open_question', 'lesson', 'plan', 'progress')),
    content             text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 300),
    certainty           text CHECK (certainty IS NULL OR (kind = 'fact' AND certainty IN ('given', 'to_verify', 'derived', 'guess'))),
    outcome             text CHECK (outcome IS NULL OR (kind = 'lesson' AND outcome IN ('dead_end', 'corrected', 'useful'))),
    -- openapi MemoryItem.support_count: lesson 만 1 이상, 그 밖은 0.
    support_count       integer NOT NULL DEFAULT 0
                        CHECK ((kind = 'lesson' AND support_count >= 1) OR (kind <> 'lesson' AND support_count = 0)),
    status              text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'superseded', 'retired')),
    supersedes          uuid REFERENCES memory_item(id),
    -- 대체는 새 행의 id 를 옛 행에 먼저 적고 새 행을 넣는다(한 트랜잭션) — 그래서 지연 검사.
    superseded_by       uuid REFERENCES memory_item(id) DEFERRABLE INITIALLY DEFERRED,
    invalidated_at      timestamptz,
    -- retireMemory 의 사유. MemoryItem 응답에는 칸이 없다(내용은 고쳐 쓰지 않는다 — 사유는 따로 남긴다).
    retire_reason       text,
    source_message_ids  uuid[] NOT NULL DEFAULT '{}',
    -- created_by: 에이전트(task token) 또는 사람. 둘 다 비면 지워진 작성자.
    created_by_agent_id uuid REFERENCES agent(id) ON DELETE SET NULL,
    created_by_user_id  uuid REFERENCES app_user(id) ON DELETE SET NULL,
    created_by_task_id  uuid REFERENCES task(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT memory_item_author CHECK (num_nonnulls(created_by_agent_id, created_by_user_id) <= 1),
    CONSTRAINT memory_item_status_shape CHECK (
        (status = 'active' AND superseded_by IS NULL AND invalidated_at IS NULL)
        OR (status = 'superseded' AND superseded_by IS NOT NULL AND invalidated_at IS NOT NULL)
        OR (status = 'retired' AND superseded_by IS NULL AND invalidated_at IS NOT NULL))
);
CREATE INDEX memory_item_work_kind_status ON memory_item (work_id, kind, status);
-- plan 은 미션당 active 1개(PRD FR-4.6 표) — 코드가 대체로 지키고 이 인덱스가 뒷받침한다.
CREATE UNIQUE INDEX memory_item_one_active_plan ON memory_item (work_id) WHERE kind = 'plan' AND status = 'active';
