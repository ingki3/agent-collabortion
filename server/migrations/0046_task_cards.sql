-- 작업 카드 (T-CARD-S, PRD FR-3.8 · openapi v0.3.10 · harness v0.9.16, Director 결정 2026-09-30)
--
-- 번호는 머지 순서로 정해진다(Lead 규칙) — dev 의 마지막이 0045(T-CTX1)라 이 PR 은 0046 이다.
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- task_card: 위임 카드와 그 결과. 번호(number)는 미션 안에서(미션 밖 위임이면 방 안에서) 1부터 —
-- 위임은 방 행을 잠그고(router.Delegate) max+1 을 매기며, 아래 유일 인덱스가 뒷받침한다.
-- 판(version)은 수정 요청마다 +1, 지난 판은 versions(판마다 전체 모양, #397 리뷰 B2)에 남는다.
-- result·judgement 는 현재 판의 것(jsonb — openapi CardResult · CardJudgement 모양 그대로).
CREATE TABLE task_card (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id             uuid NOT NULL REFERENCES room(id) ON DELETE CASCADE,
    work_id             uuid REFERENCES work(id) ON DELETE CASCADE,
    number              integer NOT NULL CHECK (number >= 1),
    version             integer NOT NULL DEFAULT 1 CHECK (version >= 1),
    status              text NOT NULL DEFAULT 'in_progress'
                        CHECK (status IN ('in_progress', 'result_submitted', 'accepted', 'cancelled')),
    delegator_agent_id  uuid NOT NULL REFERENCES agent(id),
    delegator_task_id   uuid REFERENCES task(id) ON DELETE SET NULL,
    assignee_agent_id   uuid NOT NULL REFERENCES agent(id),
    lane_id             uuid NOT NULL REFERENCES lane(id) ON DELETE CASCADE,
    parent_card_id      uuid REFERENCES task_card(id) ON DELETE SET NULL,
    goal                text NOT NULL,
    criteria            jsonb NOT NULL,             -- [{n, text, method}]
    boundaries          text NOT NULL,
    refs                jsonb NOT NULL DEFAULT '[]', -- [{kind, id}]
    output_format       text,
    budget_usd          numeric(12, 4),
    revise_reason       text,
    delegate_message_id uuid REFERENCES message(id) ON DELETE SET NULL,
    result              jsonb,
    judgement           jsonb,
    follow_ups          integer NOT NULL DEFAULT 0 CHECK (follow_ups BETWEEN 0 AND 2),
    versions            jsonb NOT NULL DEFAULT '[]',
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX task_card_number ON task_card (room_id, work_id, number) NULLS NOT DISTINCT;
CREATE UNIQUE INDEX task_card_lane ON task_card (lane_id);
CREATE INDEX task_card_work ON task_card (room_id, work_id, status);

-- task: 종류(normal · card · question), 카드 task 의 카드, 메시지 없는 서버 트리거의 사유,
-- 그리고 위임자의 합류·재진입 완료 턴에 실을 결과 카드(<result_cards>, 서버 내부 칸 — Lead 판정 Q4).
ALTER TABLE task ADD COLUMN kind text NOT NULL DEFAULT 'normal' CHECK (kind IN ('normal', 'card', 'question'));
ALTER TABLE task ADD COLUMN card_id uuid REFERENCES task_card(id) ON DELETE SET NULL;
ALTER TABLE task ADD COLUMN trigger_reason text CHECK (trigger_reason IS NULL OR trigger_reason = 'result_card_missing');
ALTER TABLE task ADD COLUMN result_card_ids uuid[] NOT NULL DEFAULT '{}';
-- card_id 는 카드 task 에만(카드가 지워지면 SET NULL 이라 card 인데 null 인 행은 허용한다).
ALTER TABLE task ADD CONSTRAINT task_card_kind_shape CHECK (kind = 'card' OR card_id IS NULL);

-- message: 카드 말풍선(위임 카드 판마다 하나 · 결과 카드).
ALTER TABLE message ADD COLUMN card_id uuid REFERENCES task_card(id) ON DELETE SET NULL;
ALTER TABLE message ADD COLUMN card_role text CHECK (card_role IS NULL OR card_role IN ('delegation', 'result'));
ALTER TABLE message ADD COLUMN card_version integer;
ALTER TABLE message ADD CONSTRAINT message_card_shape CHECK ((card_role IS NULL) = (card_version IS NULL));

-- #396 재리뷰 NN2: 합류 복구 스윕은 창(마지막 자식 종료 시각)부터 거른다.
CREATE INDEX lane_finished_delegated ON lane (finished_at) WHERE delegated_from_task_id IS NOT NULL;
