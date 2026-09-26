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
--
-- 2a) 인덱스 전에 이미 쌓인 중복을 닫는다 — 실사용 DB 에는 위 다섯 건이 그대로 open
--     이라, 정리 없이 인덱스를 만들면 unique 위반으로 migrate 가 실패하고 서버가
--     뜨지 않는다. 미션마다 가장 최근(created_at 최대, 같으면 id 큰 것) 하나만 남기고
--     나머지는 `cancelled` — 같은 미션의 새 승인 요청으로 대체됨(중복 정리). 사람이
--     답한 게 아니므로 answered_* · decision 은 만들지 않는다(K-4 closeOrphanSystemHitl
--     과 같은 닫기). hitl_request 에 사유 칸은 없다. 닫힌 요청의 받은 요청 항목은
--     같은 경로대로 지운다 — 열린 채 남으면 인박스에 답할 수 없는 줄이 남는다.
WITH dup AS (
    SELECT id FROM (
        SELECT id, row_number() OVER (PARTITION BY work_id ORDER BY created_at DESC, id DESC) AS rn
        FROM hitl_request
        WHERE work_id IS NOT NULL AND source = 'system' AND purpose = 'user_approval' AND status = 'open'
    ) d WHERE rn > 1
), closed AS (
    UPDATE hitl_request SET status = 'cancelled' WHERE id IN (SELECT id FROM dup) RETURNING id
)
DELETE FROM inbox_item
 WHERE type = 'hitl_request' AND ref_id IN (SELECT id FROM closed);

CREATE UNIQUE INDEX hitl_request_one_open_user_approval_per_work
    ON hitl_request (work_id)
    WHERE work_id IS NOT NULL AND source = 'system' AND purpose = 'user_approval' AND status = 'open';
