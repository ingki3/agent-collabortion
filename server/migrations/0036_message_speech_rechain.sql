-- message_speech_rechain — 보고에 대한 보고는 없다 (T-AGENTFIX B6, PRD FR-3.1.3 표 8행 보완)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 실측(게임 제작 방 14:05·14:30): Lead 의 턴은 Writer 의 보고로 깨어났고, 그 턴에서
-- Lead 가 Writer 에게 한 새 지시가 표 8행(요청자에게 → 보고)에 걸려 「보고(→ Writer)」로
-- 저장됐다. 규칙 보완: 턴을 깨운 메시지가 이미 **보고**였다면 그 턴의 말은 보고가 아니라
-- **요청**이다(받는 쪽은 그대로 보고한 쪽 한 명, responds_to 는 비운다).
--
-- 새 행은 messages.Classify 가 쓰는 순간 트리거의 저장된 speech 를 보고 판정한다. 이미
-- 쓰인 행(실사용 DB 포함)은 아래에서 **전부 다시 계산**한다 — 앞 마이그레이션의 채우기와
-- 같은 표에 이 한 줄만 더한 것이다. 몇 번을 돌려도 같은 답이 나오게(멱등) 입력은 저장된
-- speech 가 아니라 판정 전제뿐이다: 보고 전제를 갖춘 메시지의 사슬(보고 → 그 보고로 깨운
-- 턴의 말 → …)에서 홀수 번째 고리가 요청이 된다. 트리거는 늘 먼저 쓰였으므로 사슬에
-- 순환이 없다.
--
-- 규칙의 정본은 Go(messages.Classify)다. convo_speech_test 의 파리티 테스트가 이 파일의
-- 채우기를 그대로 돌려 Store 와 한 글자도 다르지 않은지 본다.
WITH RECURSIVE mention_to AS (
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
), chain AS (
    -- 'report?' = 표 8행의 전제를 갖춘 메시지. 그 트리거도 'report?' 이면 사슬이 된다.
    -- 사슬의 첫 고리(트리거가 보고 전제를 못 갖춘 것)는 보고, 그 답은 요청, 그 답은
    -- 다시 보고 … — 트리거는 늘 먼저 쓰였으므로 순환이 없다.
    SELECT d.id, 0 AS depth
    FROM decided d
    LEFT JOIN decided td ON td.id = d.trigger_id AND td.speech = 'report?'
    WHERE d.speech = 'report?' AND td.id IS NULL
    UNION ALL
    SELECT d.id, c.depth + 1
    FROM decided d JOIN chain c ON d.trigger_id = c.id
    WHERE d.speech = 'report?' AND c.depth < 100000
), final AS (
    SELECT d.*,
           CASE WHEN d.speech <> 'report?' THEN d.speech
                WHEN c.depth % 2 = 0 THEN 'report'
                ELSE 'request'
           END AS final_speech
    FROM decided d LEFT JOIN chain c ON c.id = d.id
)
UPDATE message m SET
    speech = d.final_speech,
    addressees = CASE d.final_speech
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
        -- 보고 — 요청자 한 명(표 8행). 보고에 대한 답(요청)도 그 한 명에게.
        WHEN 'report' THEN d.requester
        WHEN 'request' THEN CASE WHEN d.speech = 'report?' THEN d.requester ELSE d.base END
        ELSE d.base
    END,
    responds_to_message_id = CASE WHEN d.final_speech = 'report' THEN d.trigger_id END,
    delegated_lane_id = CASE WHEN d.final_speech = 'delegate' THEN d.lane_id END
FROM final d
WHERE m.id = d.id;
