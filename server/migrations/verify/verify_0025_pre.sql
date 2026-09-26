-- verify_0025_pre.sql — 0025(방·미션 이관) **적용 직전**에 돌린다. 옛 스키마(0024)의
-- 수와 session 행 사본을 `verify_0025` 스키마에 떠 둔다. 적용 뒤 verify_0025.sql 이 이것과 대조한다.
--
-- 이 디렉터리는 마이그레이션이 아니다(`migrations.FS` 는 *.sql 한 단계만 읽는다).
-- 절차(PRD §10 R4 · plan/research/V19_impl.md §2.5):
--   pg_dump -Fc → psql -f verify_0025_pre.sql → 서버 기동(0025 적용) 또는 cmd/migrate
--   → psql -f verify_0025.sql → 모든 행의 n 이 0 이면 통과, 하나라도 아니면 덤프 복원.
--   통과 뒤 `DROP SCHEMA verify_0025 CASCADE`.
-- 쓰는 곳: server/internal/db/migrate_0025_test.go · e2e/p5/87_migrate_0025.sh.
DROP SCHEMA IF EXISTS verify_0025 CASCADE;
CREATE SCHEMA verify_0025;

CREATE TABLE verify_0025.counts AS
SELECT 'session'             AS tbl, count(*) AS n FROM session
UNION ALL SELECT 'session_participant', count(*) FROM session_participant
UNION ALL SELECT 'message',             count(*) FROM message
UNION ALL SELECT 'lane',                count(*) FROM lane
UNION ALL SELECT 'task',                count(*) FROM task
UNION ALL SELECT 'task_event',          count(*) FROM task_event
UNION ALL SELECT 'artifact',            count(*) FROM artifact
UNION ALL SELECT 'decision',            count(*) FROM decision
UNION ALL SELECT 'hitl_request',        count(*) FROM hitl_request
UNION ALL SELECT 'inbox_item',          count(*) FROM inbox_item
UNION ALL SELECT 'workdir',             count(*) FROM workdir
UNION ALL SELECT 'session_hop',         count(*) FROM session_hop
UNION ALL SELECT 'session_context',     count(*) FROM session_context
UNION ALL SELECT 'activity_log',        count(*) FROM activity_log;

-- 값 보존 대조용 사본. 0025 가 방에서 지우는 열이 전부 여기 남는다.
CREATE TABLE verify_0025.session AS SELECT * FROM session;
