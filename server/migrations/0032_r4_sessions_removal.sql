-- r4_sessions_removal — 옛 세션 주소 삭제(openapi v0.3.0 D22, R4)가 남긴 저장 값 정리
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- ① 인박스 항목 타입 session_completed · session_paused 는 계약 enum 에서 빠졌다
--    (work_completed · work_paused 가 대신한다). 서버는 더 만들지 않지만, 이미 쌓인 행을
--    listInbox 가 그대로 돌려주면 웹이 모르는 타입을 받는다. 같은 사건의 work_* 항목이
--    R1b2 부터 나란히 쌓였으므로 옛 행만 지운다. DB enum 값은 남긴다(Postgres 는 enum
--    값을 지우지 못하고, 쓰는 코드가 없다).
-- ② SSE 보관 행(stream_event, 10분 보관)의 session.* 는 재연결 backfill 로 다시
--    나가지 않게 지운다.
--
-- allowed_commands 는 DB 에 저장되지 않는다 — 에이전트 역할에서 매번 계산한다
-- (roles.AllowedCommands ← colab-cli.md §2.5). session_get → room_get 은 코드·계약
-- 변경만으로 끝난다.
DELETE FROM inbox_item WHERE type IN ('session_completed', 'session_paused');
DELETE FROM stream_event WHERE type IN ('session.updated', 'session.deleted', 'session.completion_progress');
