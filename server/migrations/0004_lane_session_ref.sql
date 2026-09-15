-- 0004_lane_session_ref — lane.runtime_session_ref CHECK을 계약 키에 맞춘다 (harness.md §6, protocol.go RuntimeSessionRef)
--
-- 결함(통합 실기): 0001의 CHECK는 jsonb에 'kind' + 'session_id'를 요구했지만 계약은
--   {runtime_kind, adapter_version?, session_id, cwd, created_at, provenance?}
-- 이다. 데몬이 계약대로 보낸 finish(runtime_session_ref 포함)가 lane UPDATE에서 CHECK 위반 →
-- 500 → 모든 실제 attempt가 finish 못 하고 heartbeat 만료로 재큐잉·실패했다.
--
-- 0001~0003은 수정하지 않는다(적용된 마이그레이션은 편집 금지). 기존 CHECK를 드롭하고
-- 'runtime_kind' + 'session_id'를 요구하는 CHECK로 재생성한다. 이름은 0001의 인라인 CHECK에
-- Postgres가 붙인 기본 이름(lane_runtime_session_ref_check)이지만, 이름에 기대지 않고
-- runtime_session_ref를 참조하는 CHECK를 모두 찾아 드롭한다.

DO $$
DECLARE
    c record;
BEGIN
    FOR c IN
        SELECT conname
        FROM pg_constraint
        WHERE conrelid = 'lane'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%runtime_session_ref%'
    LOOP
        EXECUTE format('ALTER TABLE lane DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE lane ADD CONSTRAINT lane_runtime_session_ref_check
    CHECK (runtime_session_ref IS NULL
           OR (runtime_session_ref ? 'runtime_kind' AND runtime_session_ref ? 'session_id'));
