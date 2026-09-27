-- message_parts — 부분 메시지 (T-PARTS, PRD FR-3.1.4 · openapi v0.3.6 D26)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 부분마다 메시지 한 행, 한 게시의 행들은 같은 group_id 로 묶는다(D26). 행이 하나씩이라
-- 라우팅·lane 해소·speech 판정·↩·스레드·안 읽음·받은 요청·미션 귀속은 지금 규칙을 행마다
-- 그대로 쓴다. 화면만 같은 group_id 행을 말풍선 하나로 그린다.
--
--   group_id     한 게시(postMessageGroup)의 묶음 id. NULL = 보통 메시지(옛 행 전부).
--   group_index  묶음 안 순서(0부터) = 요청 parts[] 의 순서.
--   group_size   묶음의 부분 수(2~6) — 화면이 다 도착했는지 안다.
--
-- CHECK 짝: 셋은 함께 차거나 함께 빈다. 부분 수 2~6(계약 minItems·maxItems)과
-- 0 <= index < size 는 서버가 422 로 먼저 거르고, CHECK 는 다른 INSERT 경로의 바닥이다.
-- (group_id, group_index) 는 유일 — 한 묶음에 같은 자리 두 행이 없다.
ALTER TABLE message ADD COLUMN group_id uuid;
ALTER TABLE message ADD COLUMN group_index int;
ALTER TABLE message ADD COLUMN group_size int;

ALTER TABLE message ADD CONSTRAINT message_group_shape CHECK (
    (group_id IS NULL AND group_index IS NULL AND group_size IS NULL)
    OR (group_id IS NOT NULL AND group_index IS NOT NULL AND group_size IS NOT NULL
        AND group_size BETWEEN 2 AND 6 AND group_index >= 0 AND group_index < group_size)
);

-- listMessages ?group= 과 번들의 「같은 메시지의 다른 부분」 줄이 묶음으로 읽는다.
CREATE UNIQUE INDEX message_group_part ON message (group_id, group_index) WHERE group_id IS NOT NULL;
