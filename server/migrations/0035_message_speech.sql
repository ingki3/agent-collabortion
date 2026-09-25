-- message_speech — 말의 종류와 받는 쪽 (T-CONVO, PRD FR-3.1.3 · openapi v0.3.2 D24)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- 판정은 서버가 **쓰는 순간** 한다(D24): router.Delegate 는 자기가 위임을 쓰는 줄 알고,
-- 에이전트 게시는 자기 task 의 trigger_message_id 를 안다. 화면이 나중에 되짚으면
-- 「본문이 lane brief 로 끝나는가」 같은 짐작이 되고 클라이언트마다 답이 갈린다.
--
-- speech 는 NOT NULL 이 아니다 — 이 마이그레이션 전에 쓰인 행을 아래에서 채우지만,
-- 채우기가 규칙의 일부를 못 보는 경우(삭제된 task 등)가 있어 NULL 을 남겨 두고
-- 읽는 쪽이 그때만 'chat' 으로 읽는다(messages.ToAPI). 새 행은 서버가 항상 채운다.
ALTER TABLE message ADD COLUMN speech text;
ALTER TABLE message ADD COLUMN addressees jsonb NOT NULL DEFAULT '[]';
ALTER TABLE message ADD COLUMN responds_to_message_id uuid REFERENCES message(id) ON DELETE SET NULL;
ALTER TABLE message ADD COLUMN delegated_lane_id uuid REFERENCES lane(id) ON DELETE SET NULL;

ALTER TABLE message ADD CONSTRAINT message_speech_enum CHECK (
    speech IS NULL OR speech IN
    ('system', 'hitl', 'question', 'summary', 'answer', 'delegate', 'report', 'instruct', 'request', 'note', 'chat')
);
-- 두 칸은 그 종류일 때만 찬다(계약: 그 밖 null).
ALTER TABLE message ADD CONSTRAINT message_responds_to_shape CHECK (
    responds_to_message_id IS NULL OR speech = 'report'
);
ALTER TABLE message ADD CONSTRAINT message_delegated_lane_shape CHECK (
    delegated_lane_id IS NULL OR speech = 'delegate'
);

CREATE INDEX message_responds_to ON message (responds_to_message_id) WHERE responds_to_message_id IS NOT NULL;

-- ── 기존 행 채우기 ────────────────────────────────────────────────────────────
-- 실사용 DB 에 이미 STO 방 메시지가 있다(Lead). 비워 두면 그 방만 머리 없는 타임라인이
-- 되므로 같은 규칙을 SQL 로 한 번 돌린다. 규칙의 정본은 Go(messages.Classify)이고
-- 여기는 그 표를 한 번 적용하는 것뿐이다 — 새 규칙을 만들지 않는다.
--
--   mention_to  멘션(작성자 자신 제외, @all 은 all). `detail` 속 멘션은 보지 않는다(D23 과 같은 이유).
--   delegation  위임 — 그 task 가 만든 lane 중 이 메시지를 트리거로 가진 것
--               (router.Delegate: lane.delegated_from_task_id = 호출 task, task.trigger_message_id = 위임 메시지).
--   trig        보고 — 이 메시지를 쓴 턴을 깨운 메시지와 그 작성자(요청자).
--   parent      답 — 스레드 루트가 질문 카드인가.
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
), premise AS (
    SELECT m.id,
           m.kind::text AS kind, m.author_type::text AS author_type, m.author_id, m.content,
           mt.addressees AS mention_to, mt.agent_count,
           p.kind::text AS parent_kind, p.author_type::text AS parent_author_type, p.author_id AS parent_author_id,
           COALESCE(pu.display_name, pa.name, '') AS parent_author_name,
           d.lane_id, d.agent_id AS delegate_agent_id, d.agent_name AS delegate_agent_name,
           g.trigger_id, g.author_type AS trigger_author_type, g.author_id AS trigger_author_id, g.author_name AS trigger_author_name
    FROM message m
    JOIN mention_to mt ON mt.message_id = m.id
    LEFT JOIN message p ON p.id = m.parent_id
    LEFT JOIN app_user pu ON p.author_type = 'user' AND pu.id = p.author_id
    LEFT JOIN agent pa ON p.author_type = 'agent' AND pa.id = p.author_id
    LEFT JOIN delegation d ON d.message_id = m.id
    LEFT JOIN trig g ON g.message_id = m.id
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
), decided AS (
    SELECT pr.*, pt.reply_to, pt.requester,
           CASE
               WHEN pr.kind = 'system' THEN 'system'
               WHEN pr.kind = 'hitl' THEN 'hitl'
               WHEN pr.kind = 'blocked_q' THEN 'question'
               WHEN pr.kind = 'summary' THEN 'summary'
               WHEN pr.parent_kind = 'blocked_q' THEN 'answer'
               WHEN pr.content LIKE '/note%' THEN 'note'
               WHEN pr.author_type = 'agent' AND pr.lane_id IS NOT NULL THEN 'delegate'
               WHEN pr.author_type = 'agent' AND pt.requester <> '[]'::jsonb
                    AND (jsonb_array_length(pr.mention_to) = 0 OR pr.mention_to @> pt.requester)
                   THEN 'report'
               WHEN pr.agent_count > 0 AND pr.author_type = 'user' THEN 'instruct'
               WHEN pr.agent_count > 0 AND pr.author_type = 'agent' THEN 'request'
               ELSE 'chat'
           END AS speech
    FROM premise pr JOIN parent_to pt ON pt.id = pr.id
)
UPDATE message m SET
    speech = d.speech,
    addressees = CASE d.speech
        WHEN 'system' THEN '[]'::jsonb
        WHEN 'hitl' THEN '[]'::jsonb
        WHEN 'summary' THEN '[]'::jsonb
        WHEN 'note' THEN '[]'::jsonb
        WHEN 'question' THEN d.mention_to
        WHEN 'answer' THEN CASE WHEN jsonb_array_length(d.mention_to) > 0 THEN d.mention_to ELSE d.reply_to END
        WHEN 'delegate' THEN jsonb_build_array(jsonb_build_object(
            'kind', 'agent', 'id', to_jsonb(d.delegate_agent_id), 'name', d.delegate_agent_name))
        WHEN 'report' THEN CASE WHEN jsonb_array_length(d.mention_to) > 0 THEN d.mention_to
                                WHEN jsonb_array_length(d.reply_to) > 0 THEN d.reply_to
                                ELSE d.requester END
        ELSE CASE WHEN jsonb_array_length(d.mention_to) > 0 THEN d.mention_to ELSE d.reply_to END
    END,
    responds_to_message_id = CASE WHEN d.speech = 'report' THEN d.trigger_id END,
    delegated_lane_id = CASE WHEN d.speech = 'delegate' THEN d.lane_id END
FROM decided d
WHERE m.id = d.id;
