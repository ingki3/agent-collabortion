-- message_detail — 에이전트 메시지의 작업 내용 칸 (T-DETAIL, PRD FR-3.1.2 · openapi v0.3.1 D23)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 대화(content)와 작업 내용(detail)은 한 턴이 한 번에 한 말이라 같은 행의 두 칸이다
-- — 두 메시지로 나누면 스레드·답글 수·미션 귀속·트리거가 두 번 계산된다(D23).
-- 사람·시스템 메시지는 NULL. 라우팅·받은 요청·알림·검색·미션 요약은 이 칸을 읽지 않는다.
-- 상한 20만 자는 STO 실측 최대(2.8만 자)의 7배 — 서버가 422 로 먼저 거르고, CHECK 는
-- 다른 INSERT 경로가 규칙을 우회하지 못하게 하는 바닥이다.
ALTER TABLE message ADD COLUMN detail text;
ALTER TABLE message ADD CONSTRAINT message_detail_shape CHECK (
    detail IS NULL OR (author_type = 'agent' AND char_length(detail) BETWEEN 1 AND 200000)
);
