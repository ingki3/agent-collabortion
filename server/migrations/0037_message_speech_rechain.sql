-- message_speech_rechain — 보고에 대한 보고는 없다 (T-AGENTFIX B6, PRD FR-3.1.3 표 8행 보완)
--
-- 실측(게임 제작 방 14:05·14:30): Lead 의 턴은 Writer 의 보고로 깨어났고, 그 턴에서
-- Lead 가 Writer 에게 한 새 지시가 표 8행(요청자에게 → 보고)에 걸려 「보고(→ Writer)」로
-- 저장됐다. 규칙 보완: 턴을 깨운 메시지가 **보고**이고 지금 작성자가 **그 보고의 받는 쪽**
-- (= 그 보고의 요청자)이면, 그 턴의 말은 보고가 아니라 **요청**이다(받는 쪽은 그대로 보고한
-- 쪽 한 명, responds_to 는 비운다). 보고 안에서 본문 멘션 칩으로만 불린 에이전트(받는 쪽이
-- 아니다)가 부탁받은 결과를 돌려주는 말은 **원래 보고**로 남는다(review #343 블로커 1).
--
-- 새 행은 messages.Classify 가 쓰는 순간 트리거의 저장된 speech·addressees 를 보고 판정한다.
-- 이미 쓰인 행(실사용 DB 포함)은 아래에서 **전부 다시 계산**한다 — 0035 의 채우기와 같은 표에
-- 이 한 줄만 더한 것이다. 몇 번을 돌려도 같은 답이 나오게(멱등) 입력은 저장된 speech 가 아니라
-- 판정 전제뿐이다: 보고 전제를 갖춘 메시지 X 의 트리거 T 도 보고 전제를 갖추고 X 의 작성자가
-- T 의 요청자이면 X 는 T 에 매달린 고리다. 그런 사슬(보고 → 받은 쪽의 다음 말 → …)에서 홀수
-- 번째 고리가 요청이 된다. 트리거는 늘 먼저 쓰였으므로 사슬에 순환이 없다.
--
-- 규모(review #343 블로커 2): 한 문장짜리 재귀 CTE 는 행 추정이 폭주해(1e24) 20만 행에서
-- Hash Anti Join 이 백만 배치를 잡고 백엔드가 OOM 킬됐다. 그래서 판정 전제를 임시 테이블로
-- 물리화하고 인덱스·ANALYZE 뒤 사슬을 계산한다 — 플래너가 실제 행 수를 본다. 임시 테이블은
-- ON COMMIT DROP 이라 한 트랜잭션 안에서 돌아야 한다(db.applyOne 이 그렇게 돌린다).
--
-- 규칙의 정본은 Go(messages.Classify)다. convo_speech_test 의 파리티 테스트가 이 파일의
-- 채우기(`-- backfill:` 아래 전부)를 그대로 돌려 Store 와 한 글자도 다르지 않은지 본다.

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

-- 'report?' = 표 8행의 전제를 갖춘 메시지. 고리: 자식의 트리거가 'report?' 이고 자식의
-- 작성자가 그 트리거의 요청자(= 보고였다면 그 보고의 받는 쪽 한 명)일 때. 사슬의 첫 고리는
-- 보고, 그 답은 요청, 그 답은 다시 보고 … (깊이의 홀짝).
CREATE TEMP TABLE speech_report_link ON COMMIT DROP AS
SELECT d.id, d.trigger_id, d.trigger_author_id,
       (td.id IS NOT NULL) AS linked
FROM speech_decided d
LEFT JOIN speech_decided td
       ON td.id = d.trigger_id AND td.speech = 'report?'
      AND td.trigger_author_type = 'agent' AND td.trigger_author_id = d.author_id
WHERE d.speech = 'report?';

CREATE UNIQUE INDEX ON speech_report_link (id);
CREATE INDEX ON speech_report_link (trigger_id) WHERE linked;
ANALYZE speech_report_link;

CREATE TEMP TABLE speech_chain ON COMMIT DROP AS
WITH RECURSIVE chain AS (
    SELECT l.id, 0 AS depth FROM speech_report_link l WHERE NOT l.linked
    UNION ALL
    SELECT l.id, c.depth + 1
    FROM chain c JOIN speech_report_link l ON l.trigger_id = c.id AND l.linked
    WHERE c.depth < 100000
)
SELECT id, depth FROM chain;

CREATE UNIQUE INDEX ON speech_chain (id);
ANALYZE speech_chain;

WITH final AS (
    SELECT d.*,
           CASE WHEN d.speech <> 'report?' THEN d.speech
                WHEN c.depth % 2 = 0 THEN 'report'
                ELSE 'request'
           END AS final_speech
    FROM speech_decided d LEFT JOIN speech_chain c ON c.id = d.id
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

ANALYZE message;
