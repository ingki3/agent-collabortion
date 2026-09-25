-- work_approval_held — 작업 중 승인 보류 + **미션당 열린 완료 승인 하나**
-- (T-APPROVAL, openapi v0.3.3 CompletionProgress held_reason)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 1) approval_held_at — 종료 조건이 user_approval 만 남기고 충족됐어도 그 미션에
--    실행·대기 중인 할 일이 있으면 서버는 승인 요청(HITL)을 열지 않고 이 칸에 보류
--    시각을 적는다. 마지막 할 일이 끝나면(finish·취소·실패) 다시 판정해 요청을 열고
--    칸을 비운다.
--
--    표식이 따로 있어야 하는 이유: 「user_approval 만 빠졌고 열린 요청이 없다」는
--    Director 가 거절한 뒤(E6-04 — 거절은 아무것도 다시 부르지 않는다)와 똑같이
--    생겼다. 표식 없이 다시 판정하면 거절 직후 승인 요청이 또 뜬다.
ALTER TABLE work ADD COLUMN approval_held_at timestamptz;

-- 2) 미션당 열린 완료 승인은 **하나**다(T-APPROVAL E).
--
--    실측(실사용 「게임 제작 방」): 아티팩트가 제출될 때마다 새 요청이 열려 13:41 ·
--    13:45 · 14:04 · 14:08 · 14:21 · 14:30 여섯 건이 열렸고 그중 다섯이 동시에 open
--    이었다. 받은 요청 화면에 같은 질문이 다섯 줄 쌓인다 — 어느 것에 답해도 같은
--    미션이 닫히는데, 답한 뒤에도 넷이 남는다.
--
--    판정 층(sessions.ApplyEvent 의 `advanced`)과 발행 층(ApplyWorkEvent 의 열린 요청
--    조회)이 이미 막지만, **바닥에서도 막는다** — 경합(두 제출이 같은 순간에 도착)은
--    조회로는 못 막고, 앞으로 생길 다른 발행 경로가 이 규칙을 모를 수 있다.
--    `0001` 의 `hitl_request_one_open_per_task` 가 task 범위에 한 것과 같은 모양이고,
--    범위만 미션이다. 방 층 요청(work_id NULL)은 대상이 아니다.
CREATE UNIQUE INDEX hitl_request_one_open_user_approval_per_work
    ON hitl_request (work_id)
    WHERE work_id IS NOT NULL AND source = 'system' AND purpose = 'user_approval' AND status = 'open';
