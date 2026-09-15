-- 0015_gc_command_targets.sql — gc 명령이 가리키는 workdir 집합 (daemon-protocol v0.7 §4.3·§6)
--
-- v0.7 부터 gc 명령의 정본 페이로드는 `workdirs: [{id, path}]` 이고(서버가 경로를
-- 싣는다), v0.6 데몬을 위해 `workdir_ids: [...]` 도 함께 남는다. 명령의 대상 집합을
-- 읽는 곳이 두 군데(보고 소비 tokens.ConsumeGCCommands · 행 이동 workdirs.ApplyGCReports)
-- 라서, 두 모양을 푸는 규칙을 SQL 한 곳에 둔다 — 한쪽만 새 모양을 알면 명령이
-- 영원히 소비되지 않거나 멀쩡한 workdir 이 닫힌다.
--
-- jsonb_array_elements 는 배열이 아닌 값에 에러를 내므로 CASE 로 감싼다. payload 는
-- 데몬이 아니라 서버가 쓴 값이지만, 옛 행·손상된 행에서 보고 처리 전체가 500 이
-- 되는 것이 GC 하나를 놓치는 것보다 나쁘다.
CREATE OR REPLACE FUNCTION gc_command_workdir_ids(payload jsonb)
RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT COALESCE(array_agg(id), '{}'::text[])
    FROM (
        SELECT e->>'id' AS id
        FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(payload->'workdirs') = 'array'
                 THEN payload->'workdirs' ELSE '[]'::jsonb END) e
        UNION
        SELECT w
        FROM jsonb_array_elements_text(
            CASE WHEN jsonb_typeof(payload->'workdir_ids') = 'array'
                 THEN payload->'workdir_ids' ELSE '[]'::jsonb END) w
    ) t
    WHERE id IS NOT NULL
$$;
