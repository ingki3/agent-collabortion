-- approval_quiet — 승인만 남은 일은 에이전트끼리 새 일을 만들지 않는다
-- (T-QUIET, PRD v0.19.13 FR-2A.2.3 · openapi v0.3.9 QueuedReason.approval_pending)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 실측(2026-09-28, 「게임 제작 방」): 게임이 완성되고 Lead 가 「팀 쪽에 열린 일은 0」이라고
-- 보고한 뒤에도 Lead·Writer·Researcher 가 30분 동안 메시지 17건으로 가이드 각주의 숫자를
-- 서로 고쳤다. 에이전트끼리 계속 서로 깨우니 도는 할 일이 끊이지 않아 FR-2A.2.1 의 보류가
-- 영영 풀리지 않았고, 승인 요청은 한 번도 뜨지 않았다.
--
-- 1) queued_reason 에 approval_pending — 승인 대기 일에서 에이전트가 쓴 메시지가 만든 할 일은
--    이 사유로 큐에 남고 claim 이 건너뛴다. FR-2A.2.1 의 「실행·대기 중」 에도 세지 않는다.
ALTER TYPE queued_reason ADD VALUE IF NOT EXISTS 'approval_pending';

-- 2) work.approval_quiet — 그 일의 승인 대기 상태.
--
--    NULL       승인 대기가 아니다(또는 승인 외 조건이 다시 미충족 — FR-2A.2.3 ④).
--    'quiet'    승인 대기 — 에이전트 메시지의 트리거는 보류된다.
--    'released' 사람이 풀었다(수정 요청 ② · 사람 지시 ③). 에이전트 간 협업은 다시 돈다.
--               그 일의 에이전트가 사람에게 보고하거나(report → user), 일이 멈췄는데
--               승인 요청이 열려 있으면 다시 'quiet'(Lead 판정 2026-09-28).
--
--    승인 대기 판정은 서버 한 곳(sessions.ApplyWorkEvent — 종료 조건에서 user_approval 만
--    남았는가)이 쓴다. 읽는 쪽(라우터·위임·claim)은 이 칸만 본다.
ALTER TABLE work ADD COLUMN approval_quiet text
    CHECK (approval_quiet IN ('quiet', 'released'));
ALTER TABLE work ADD COLUMN approval_quiet_at timestamptz;

-- 2a) 배포 순간 이미 승인 대기인 일 — 승인 요청이 열려 있거나(FR-2A.2.2) 작업 중이라 보류된
--     (FR-2A.2.1) 열린 일. 새 규칙은 다음 에이전트 메시지부터 걸린다. 이미 큐에 있는 할 일은
--     건드리지 않는다(누가 만든 것인지 되짚지 않는다 — 다음 게시부터).
UPDATE work wk SET approval_quiet = 'quiet', approval_quiet_at = now()
 WHERE wk.status = 'active'
   AND (wk.approval_held_at IS NOT NULL
        OR EXISTS (SELECT 1 FROM hitl_request h
                    WHERE h.work_id = wk.id AND h.source = 'system'
                      AND h.purpose = 'user_approval' AND h.status = 'open'));
