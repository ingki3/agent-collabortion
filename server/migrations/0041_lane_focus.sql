-- lane_focus — 지금 하는 일 (T-FOCUS, PRD FR-3.1.5 · openapi v0.3.8 Lane.focus)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 에이전트가 `colab status set working --note` 로 선언한 한 문장(source = agent), 또는
-- 선언 전 서버가 트리거로 만든 대신 문장(source = derived). 도는 턴이 없으면 셋 다 NULL
-- (턴이 끝나면 서버가 비운다 — 끝난 뒤엔 게시된 메시지가 말한다).
--
--   focus_text          사람에게 하는 한 문장, 120자 이하(서버가 자른다).
--   focus_at            선언(또는 대신 문장을 만든) 시각.
--   focus_source        agent | derived.
--   focus_published_at  마지막으로 「지금」 줄이 lane.updated 로 흘러간 시각 — 60초 안의
--                       연속 선언은 마지막 하나만 흘린다(openapi setTaskStatus). 새 턴의 대신
--                       문장이 NULL 로 되돌린다 — 턴의 첫 선언은 바로 흐른다.
ALTER TABLE lane ADD COLUMN focus_text text;
ALTER TABLE lane ADD COLUMN focus_at timestamptz;
ALTER TABLE lane ADD COLUMN focus_source text;
ALTER TABLE lane ADD COLUMN focus_published_at timestamptz;

ALTER TABLE lane ADD CONSTRAINT lane_focus_shape CHECK (
    (focus_text IS NULL AND focus_at IS NULL AND focus_source IS NULL)
    OR (focus_text IS NOT NULL AND focus_at IS NOT NULL AND focus_source IN ('agent', 'derived')
        AND char_length(focus_text) BETWEEN 1 AND 120)
);
