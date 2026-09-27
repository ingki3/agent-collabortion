-- message_speech_upstream — 보고를 받은 뒤의 말은 멘션으로 가른다 (T-SPEECHFIX, PRD v0.19.8 FR-3.1.3)
--
-- 실측(Director 지적 2026-09-27, 게임 제작 방): Lead 가 Developer 의 보고(→ Lead)로 깨어나
-- 멘션 없이 「Simplist 님, v9 올렸습니다…」를 게시했는데, 0037 의 규칙(보고에 대한 보고는
-- 없다)이 그 말을 「요청 → @Developer」로 저장했다. v0.19.8 은 보고를 받고 깨어난 턴(트리거가
-- 보고이고 지금 작성자가 그 보고의 받는 쪽)의 말을 멘션으로 가른다:
--
--   에이전트를 멘션 → 요청(멘션된 에이전트)
--   사람만 멘션     → 보고(멘션된 사람, 윗선 지시가 그 사람의 것이면 responds_to = 윗선 지시)
--   멘션 없음       → 보고(윗선 요청자 한 명, responds_to = 윗선 지시)
--   윗선을 못 찾음  → 대화(11번, 받는 쪽은 base)
--
-- 윗선 지시 = 트리거 보고의 responds_to 를 쓴 task 의 트리거 메시지. 그것도 보고면 같은
-- 규칙으로 올라간다(최대 5단, 시스템·자기 자신에서 멈춘다) — messages.walkUpstream 과 같다.
--
-- 판정은 조상에게 기댄다: 한 말의 결과는 트리거의 **결과**(speech·addressees·responds_to)와
-- 윗선을 찾아 올라가며 읽는 메시지들의 결과에 달렸다. 그 메시지들은 모두 트리거 사슬의 조상
-- (보고의 responds_to 는 그 보고의 트리거이거나 그 트리거에서 찾은 윗선 — 역시 조상)이다.
-- 그래서 트리거 사슬의 **깊이 순**으로 한 층씩 채우면 매 층에서 조상은 이미 확정돼 있다.
-- 입력은 판정 전제뿐(저장된 speech 를 읽지 않는다)이라 몇 번을 돌려도 같은 답이다(멱등).
--
-- 규모(0037 의 교훈, review #343 블로커 2): 한 문장짜리 재귀 CTE 는 행 추정이 폭주해 20만
-- 행에서 OOM 이 났다. 그래서 전제·결과·깊이를 임시 테이블로 물리화하고 인덱스·ANALYZE 뒤
-- 층마다 작은 UPDATE 를 돈다. 임시 테이블·함수는 이 트랜잭션(db.applyOne) 안에서만 산다.
--
-- 규칙의 정본은 Go(messages.Classify · walkUpstream)다. convo_speech_test 의 파리티 테스트가
-- 이 파일의 채우기(`-- backfill:` 아래 전부)를 그대로 돌려 Store 와 한 글자도 다르지 않은지 본다.
--
-- speech_decided 는 0037 의 첫 표와 같다(같은 전제, 같은 순서) — 'report?' = 표 8행 전제.

-- backfill:
CREATE TEMP TABLE speech_decided ON COMMIT DROP AS
WITH mention_to AS (
    SELECT m.id AS message_id,
           COALESCE(jsonb_agg(jsonb_build_object(
               'kind', x.kind,
               'id', CASE WHEN x.kind = 'all' THEN NULL ELSE to_jsonb(x.id) END,
               'name', COALESCE(x.display_name, '')
           ) ORDER BY x.ord) FILTER (WHERE x.kind IS NOT NULL), '[]'::jsonb) AS addressees,
           count(*) FILTER (WHERE x.kind = 'agent') AS agent_count
    FROM message m
    LEFT JOIN LATERAL (
        SELECT DISTINCT ON (e->>'kind', e->>'id')
               e->>'kind' AS kind, e->>'id' AS id, e->>'display_name' AS display_name, ord
        FROM jsonb_array_elements(m.mentions) WITH ORDINALITY AS t(e, ord)
        WHERE e->>'kind' = 'all' OR e->>'id' IS DISTINCT FROM m.author_id::text
        ORDER BY e->>'kind', e->>'id', ord
    ) x ON true
    GROUP BY m.id
), delegation AS (
    SELECT DISTINCT ON (t.trigger_message_id)
           t.trigger_message_id AS message_id, l.id AS lane_id, l.agent_id, a.name AS agent_name
    FROM lane l
    JOIN task t ON t.lane_id = l.id AND t.trigger_message_id IS NOT NULL
    JOIN agent a ON a.id = l.agent_id
    JOIN message m ON m.id = t.trigger_message_id
    WHERE l.delegated_from_task_id IS NOT NULL
      AND l.delegated_from_task_id = m.source_task_id
    ORDER BY t.trigger_message_id, t.created_at
), trig AS (
    SELECT m.id AS message_id, tm.id AS trigger_id, tm.author_type::text AS author_type, tm.author_id,
           COALESCE(u.display_name, a.name, '') AS author_name
    FROM message m
    JOIN task t ON t.id = m.source_task_id
    JOIN message tm ON tm.id = t.trigger_message_id
    LEFT JOIN app_user u ON tm.author_type = 'user' AND u.id = tm.author_id
    LEFT JOIN agent a ON tm.author_type = 'agent' AND a.id = tm.author_id
    WHERE m.author_type = 'agent'
), waiting AS (
    -- 표 3행 후반. 위임자가 있으면 그 에이전트, 없으면 사슬을 시작한 사람
    -- (openapi Lane.waiting_for 「위임자 이름 또는 Director」).
    SELECT m.id AS message_id,
           CASE WHEN COALESCE(da.name, '') <> '' THEN jsonb_build_array(jsonb_build_object(
                    'kind', 'agent', 'id', to_jsonb(d.agent_id), 'name', da.name))
                WHEN COALESCE(ou.display_name, '') <> '' THEN jsonb_build_array(jsonb_build_object(
                    'kind', 'user', 'id', to_jsonb(t.originator_user_id), 'name', ou.display_name))
                ELSE '[]'::jsonb END AS waiting_to
    FROM message m
    JOIN task t ON t.id = m.source_task_id
    JOIN lane l ON l.id = t.lane_id
    LEFT JOIN task d ON d.id = l.delegated_from_task_id
    LEFT JOIN agent da ON da.id = d.agent_id
    LEFT JOIN app_user ou ON ou.id = t.originator_user_id
    WHERE m.kind = 'blocked_q'
), premise AS (
    SELECT m.id,
           m.kind::text AS kind, m.author_type::text AS author_type, m.author_id, m.content,
           mt.addressees AS mention_to, mt.agent_count,
           p.kind::text AS parent_kind, p.author_type::text AS parent_author_type, p.author_id AS parent_author_id,
           COALESCE(pu.display_name, pa.name, '') AS parent_author_name,
           d.lane_id, d.agent_id AS delegate_agent_id, d.agent_name AS delegate_agent_name,
           g.trigger_id, g.author_type AS trigger_author_type, g.author_id AS trigger_author_id, g.author_name AS trigger_author_name,
           COALESCE(w.waiting_to, '[]'::jsonb) AS waiting_to
    FROM message m
    JOIN mention_to mt ON mt.message_id = m.id
    LEFT JOIN message p ON p.id = m.parent_id
    LEFT JOIN app_user pu ON p.author_type = 'user' AND pu.id = p.author_id
    LEFT JOIN agent pa ON p.author_type = 'agent' AND pa.id = p.author_id
    LEFT JOIN delegation d ON d.message_id = m.id
    LEFT JOIN trig g ON g.message_id = m.id
    LEFT JOIN waiting w ON w.message_id = m.id
), parent_to AS (
    SELECT id,
           CASE WHEN parent_author_id IS NOT NULL AND parent_author_type <> 'system'
                 AND parent_author_id IS DISTINCT FROM author_id
                THEN jsonb_build_array(jsonb_build_object(
                    'kind', CASE WHEN parent_author_type = 'agent' THEN 'agent' ELSE 'user' END,
                    'id', to_jsonb(parent_author_id), 'name', parent_author_name))
                ELSE '[]'::jsonb END AS reply_to,
           CASE WHEN trigger_author_id IS NOT NULL AND trigger_author_type <> 'system'
                 AND trigger_author_id IS DISTINCT FROM author_id
                THEN jsonb_build_array(jsonb_build_object(
                    'kind', CASE WHEN trigger_author_type = 'agent' THEN 'agent' ELSE 'user' END,
                    'id', to_jsonb(trigger_author_id), 'name', trigger_author_name))
                ELSE '[]'::jsonb END AS requester
    FROM premise
), based AS (
    -- Classify 의 base: 멘션, 비면 스레드 상대. 보고 판정의 전제이자 대화의 받는 쪽이다.
    SELECT pr.id,
           CASE WHEN jsonb_array_length(pr.mention_to) > 0 THEN pr.mention_to ELSE pt.reply_to END AS base
    FROM premise pr JOIN parent_to pt ON pt.id = pr.id
), decided AS (
    SELECT pr.*, pt.reply_to, pt.requester, b.base,
           CASE
               WHEN pr.kind = 'system' THEN 'system'
               WHEN pr.kind = 'hitl' THEN 'hitl'
               WHEN pr.kind = 'blocked_q' THEN 'question'
               WHEN pr.kind = 'summary' THEN 'summary'
               WHEN pr.parent_kind = 'blocked_q' THEN 'answer'
               WHEN pr.content LIKE '/note%' THEN 'note'
               WHEN pr.author_type = 'agent' AND pr.lane_id IS NOT NULL THEN 'delegate'
               WHEN pr.author_type = 'agent' AND pt.requester <> '[]'::jsonb
                    AND (jsonb_array_length(b.base) = 0 OR b.base @> pt.requester)
                   THEN 'report?'
               WHEN pr.agent_count > 0 AND pr.author_type = 'user' THEN 'instruct'
               WHEN pr.agent_count > 0 AND pr.author_type = 'agent' THEN 'request'
               ELSE 'chat'
           END AS speech
    FROM premise pr JOIN parent_to pt ON pt.id = pr.id JOIN based b ON b.id = pr.id
)
SELECT * FROM decided;

CREATE UNIQUE INDEX ON speech_decided (id);
CREATE INDEX ON speech_decided (trigger_id);
ANALYZE speech_decided;

-- 모든 행의 결과. 먼저 「보고를 받고 깨어난 턴」을 모르는 채의 답(표 그대로)을 채우고,
-- 아래 층별 루프가 그 턴의 말만 고친다.
--   cand  = Classify 의 에이전트 갈래까지 오는 말(위임·메모·답·질문 등이 아닌 에이전트 말)
--   depth = 트리거 사슬의 깊이(사람·시스템 말 = 0)
CREATE TEMP TABLE speech_final ON COMMIT DROP AS
SELECT d.id, d.trigger_id, d.author_type, d.author_id,
       COALESCE(au.display_name, aa.name, '') AS author_name,
       d.mention_to, d.agent_count, d.base,
       (d.author_type = 'agent' AND d.trigger_id IS NOT NULL
        AND d.speech IN ('report?', 'request', 'chat')) AS cand,
       0 AS depth,
       CASE WHEN d.speech = 'report?' THEN 'report' ELSE d.speech END AS speech,
       CASE d.speech
           WHEN 'system' THEN '[]'::jsonb
           WHEN 'hitl' THEN '[]'::jsonb
           WHEN 'summary' THEN '[]'::jsonb
           WHEN 'note' THEN '[]'::jsonb
           WHEN 'question' THEN CASE WHEN jsonb_array_length(d.mention_to) > 0 THEN d.mention_to ELSE d.waiting_to END
           -- 답 — 질문자 먼저, 그다음 멘션(같은 kind·id 는 한 번만).
           WHEN 'answer' THEN (
               SELECT COALESCE(jsonb_agg(z.e ORDER BY z.ord), '[]'::jsonb)
               FROM (
                   SELECT DISTINCT ON (u.e->>'kind', u.e->>'id') u.e, u.ord
                   FROM (
                       SELECT e, ord FROM jsonb_array_elements(d.reply_to) WITH ORDINALITY AS t(e, ord)
                       UNION ALL
                       SELECT e, ord + 1000000 FROM jsonb_array_elements(d.mention_to) WITH ORDINALITY AS t(e, ord)
                   ) u
                   ORDER BY u.e->>'kind', u.e->>'id', u.ord
               ) z
           )
           WHEN 'delegate' THEN jsonb_build_array(jsonb_build_object(
               'kind', 'agent', 'id', to_jsonb(d.delegate_agent_id), 'name', d.delegate_agent_name))
           -- 보고 — 요청자 한 명(표 8행).
           WHEN 'report?' THEN d.requester
           ELSE d.base
       END AS addressees,
       CASE WHEN d.speech = 'report?' THEN d.trigger_id END AS resp,
       CASE WHEN d.speech = 'delegate' THEN d.lane_id END AS lane
FROM speech_decided d
LEFT JOIN app_user au ON d.author_type = 'user' AND au.id = d.author_id
LEFT JOIN agent aa ON d.author_type = 'agent' AND aa.id = d.author_id;

CREATE UNIQUE INDEX ON speech_final (id);
CREATE INDEX ON speech_final (trigger_id);
ANALYZE speech_final;

-- 깊이: 트리거 사슬만 따라가는 작은 재귀(재귀 항에 anti join 이 없다). 트리거는 늘 먼저
-- 쓰였으므로 순환이 없다.
CREATE TEMP TABLE speech_depth ON COMMIT DROP AS
WITH RECURSIVE chain AS (
    SELECT f.id, 0 AS depth FROM speech_final f WHERE f.trigger_id IS NULL
    UNION ALL
    SELECT f.id, c.depth + 1
    FROM chain c JOIN speech_final f ON f.trigger_id = c.id
    WHERE c.depth < 1000000
)
SELECT id, depth FROM chain;

CREATE UNIQUE INDEX ON speech_depth (id);
ANALYZE speech_depth;

UPDATE speech_final f SET depth = s.depth FROM speech_depth s WHERE s.id = f.id AND s.depth <> 0;
CREATE INDEX ON speech_final (depth) WHERE cand;
ANALYZE speech_final;

-- 윗선 지시(messages.walkUpstream 과 같은 걸음): 보고 c 의 responds_to(에이전트가 쓴 말) →
-- 그 말을 쓴 턴의 트리거 u. u 가 시스템이거나 자기 자신이 쓴 말이면 못 찾음, 보고면 u 에서
-- 한 번 더(최대 5단). 읽는 행은 모두 조상이라 이미 확정된 결과다.
CREATE FUNCTION pg_temp.speech_upstream(start uuid, me uuid) RETURNS uuid
LANGUAGE plpgsql STABLE AS $$
DECLARE
    cur uuid := start;
    r uuid;
    u uuid;
    u_type text;
    u_author uuid;
    u_speech text;
BEGIN
    FOR step IN 1..5 LOOP
        SELECT f.resp INTO r FROM speech_final f WHERE f.id = cur;
        IF r IS NULL THEN
            RETURN NULL;
        END IF;
        SELECT f.trigger_id INTO u FROM speech_final f WHERE f.id = r AND f.author_type = 'agent';
        IF u IS NULL THEN
            RETURN NULL;
        END IF;
        SELECT f.author_type, f.author_id, f.speech INTO u_type, u_author, u_speech
        FROM speech_final f WHERE f.id = u;
        IF NOT FOUND OR u_type = 'system' OR u_author IS NULL OR u_author = me THEN
            RETURN NULL;
        END IF;
        IF u_speech <> 'report' THEN
            RETURN u;
        END IF;
        cur := u;
    END LOOP;
    RETURN NULL;
END
$$;

-- 층별: 이 층의 말 중 「트리거가 보고이고 작성자가 그 보고의 받는 쪽」인 것만 멘션으로 가른다.
DO $$
DECLARE
    d integer;
    top integer;
BEGIN
    SELECT COALESCE(max(depth), 0) INTO top FROM speech_final WHERE cand;
    FOR d IN 1..top LOOP
        WITH woken AS (
            SELECT x.id, x.author_id, x.trigger_id, x.agent_count, x.mention_to, x.base
            FROM speech_final x
            JOIN speech_final t ON t.id = x.trigger_id
            WHERE x.depth = d AND x.cand
              AND t.speech = 'report'
              AND t.addressees @> jsonb_build_array(jsonb_build_object('kind', 'agent', 'id', to_jsonb(x.author_id)))
        ), up AS (
            SELECT w.*, CASE WHEN w.agent_count = 0 THEN pg_temp.speech_upstream(w.trigger_id, w.author_id) END AS up_id
            FROM woken w
        ), decided AS (
            SELECT up.id, up.up_id, up.mention_to, up.agent_count, up.base,
                   CASE WHEN u.id IS NULL THEN NULL ELSE jsonb_build_object(
                       'kind', CASE WHEN u.author_type = 'agent' THEN 'agent' ELSE 'user' END,
                       'id', to_jsonb(u.author_id), 'name', u.author_name) END AS up_to
            FROM up LEFT JOIN speech_final u ON u.id = up.up_id
        )
        UPDATE speech_final f SET
            speech = CASE
                WHEN n.agent_count > 0 THEN 'request'
                WHEN jsonb_array_length(n.mention_to) > 0 THEN 'report'
                WHEN n.up_to IS NOT NULL THEN 'report'
                ELSE 'chat' END,
            addressees = CASE
                WHEN jsonb_array_length(n.mention_to) > 0 THEN n.mention_to
                WHEN n.up_to IS NOT NULL THEN jsonb_build_array(n.up_to)
                ELSE n.base END,
            resp = CASE
                WHEN n.agent_count > 0 THEN NULL
                WHEN jsonb_array_length(n.mention_to) > 0 THEN
                    CASE WHEN n.up_to IS NOT NULL AND n.mention_to @> jsonb_build_array(
                             jsonb_build_object('kind', n.up_to->'kind', 'id', n.up_to->'id'))
                         THEN n.up_id END
                WHEN n.up_to IS NOT NULL THEN n.up_id
                ELSE NULL END
        FROM decided n
        WHERE f.id = n.id;
    END LOOP;
END
$$;

UPDATE message m SET
    speech = f.speech,
    addressees = f.addressees,
    responds_to_message_id = f.resp,
    delegated_lane_id = f.lane
FROM speech_final f
WHERE m.id = f.id
  AND (m.speech, m.addressees, m.responds_to_message_id, m.delegated_lane_id)
      IS DISTINCT FROM (f.speech, f.addressees, f.resp, f.lane);

DROP FUNCTION pg_temp.speech_upstream(uuid, uuid);

ANALYZE message;
