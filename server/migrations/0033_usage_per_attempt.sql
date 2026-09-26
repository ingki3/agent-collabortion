-- usage_per_attempt — task_usage 를 (task_id, attempt) 단위로 (T-S-usage)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- task_usage 는 task 하나에 한 행이었고 heartbeat·finish 가 그 행을 덮어썼다.
-- 데몬이 보내는 값은 "이 attempt 의 누적" 이라 같은 attempt 안에서는 덮어쓰기가
-- 맞지만, 재시도·재개·콜드 스타트·프로파일 폴백으로 같은 task 가 새 attempt 를
-- 받으면 새 attempt 의 누적이 이전 attempt 의 비용을 지웠다. 실측(2026-09-24):
-- attempt 1 이 $2.733 을 쓴 뒤 예산 pause → 승인 → attempt 2 가 $1.318 로 끝나자
-- 미션 비용이 11.477 → 10.062 로 내려갔다 — 예산 강제가 실제보다 적게 센다.
--
-- 이제 한 행은 한 attempt 이고, task 의 비용은 그 task 의 행들의 합이다. 합계를
-- 읽는 SQL(sum(cost_usd) … JOIN task) 은 그대로 attempt 합이 된다. task 하나에 한
-- 줄을 기대하는 자리(GetUsage · 비용 보고서의 task 칸 · 예산 상태의 task 사용액)는
-- task_usage_total 을 읽는다.
--
-- 기존 행은 그 task 의 현재 attempt 로 둔다 — 덮어쓰기로 이미 사라진 이전 attempt
-- 의 값은 되살릴 근거가 없다.
ALTER TABLE task_usage ADD COLUMN attempt integer;
UPDATE task_usage u SET attempt = t.attempt FROM task t WHERE t.id = u.task_id;
ALTER TABLE task_usage ALTER COLUMN attempt SET NOT NULL;
ALTER TABLE task_usage ADD CONSTRAINT task_usage_attempt_check CHECK (attempt >= 1);
ALTER TABLE task_usage DROP CONSTRAINT task_usage_pkey;
ALTER TABLE task_usage ADD PRIMARY KEY (task_id, attempt);

-- task 하나의 사용량 = attempt 합. estimated 는 어느 attempt 든 추정이면 추정이다
-- (측정과 추정이 섞인 합은 추정).
CREATE VIEW task_usage_total AS
SELECT task_id,
       sum(input_tokens)::bigint  AS input_tokens,
       sum(output_tokens)::bigint AS output_tokens,
       sum(cache_read)::bigint    AS cache_read,
       sum(cost_usd)              AS cost_usd,
       bool_or(estimated)         AS estimated,
       max(updated_at)            AS updated_at,
       count(*)::int              AS attempts
FROM task_usage
GROUP BY task_id;
