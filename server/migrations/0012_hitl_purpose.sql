-- 0012_hitl_purpose.sql — 시스템 발행 HITL 의 용도 (S-25)
--
-- 계약 HitlRequest 는 `purpose`(agent · user_approval · budget · time · loop)를
-- 내보내라고 하는데 테이블에 칸이 없어서 서버가 그 값을 만든 적이 없다. 그동안은
-- 응답 op 자체가 501 이라 아무도 밟지 않았다.
--
-- respondHitlRequest 를 P2 로 여는 순간(계약 PR #101: 플랫폼 발행 approval —
-- 종료 조건 user_approval 의 승인·거절만) 이 칸이 필요해진다. `source = system`
-- 과 `type = approval` 은 종료 조건 승인·예산 초과·루프 상한 셋이 전부 공유하고,
-- 셋 다 task_id 를 비운 채 들어온다 — 질문 본문 말고는 구분할 표식이 없었다.
-- 문자열 비교로 승인 경로를 여는 것은 규칙이 아니라 우연이다.
--
-- 기존 행: 에이전트 발행은 'agent'. 시스템 발행 approval 은 세션이 멈춰 있으면
-- 그 이유(budget · loop), 아니면 종료 조건 승인이다 — 발행 시점의 판정과 같다.
ALTER TABLE hitl_request ADD COLUMN purpose text;

ALTER TABLE hitl_request ADD CONSTRAINT hitl_request_purpose_ck
    CHECK (purpose IS NULL OR purpose IN ('agent', 'user_approval', 'budget', 'time', 'loop'));

UPDATE hitl_request h SET purpose = 'agent' WHERE h.source = 'agent';

UPDATE hitl_request h SET purpose = CASE
        WHEN s.paused_reason::text IN ('budget', 'loop') THEN s.paused_reason::text
        ELSE 'user_approval'
    END
FROM session s
WHERE s.id = h.session_id AND h.source = 'system' AND h.type = 'approval';
