-- verify_0025.sql — 0025 적용 **직후** 대조. 모든 행의 n 이 0 이어야 한다.
-- 먼저 verify_0025_pre.sql 이 옛 스키마에서 돌아 있어야 한다(같은 디렉터리의 머리말 절차).
--
-- 한 행 = 한 검사(chk, n). n 은 "어긋난 수"다. 이관 직후에만 뜻이 있다 — 그 뒤 새로 생기는
-- 미션 밖 메시지·lane·task 는 work_id 가 비는 것이 정상이다(FR-2A.1).
WITH pre AS (SELECT tbl, n FROM verify_0025.counts),
     p   AS (SELECT * FROM verify_0025.session)
SELECT chk, n FROM (
    -- A. 방 수 = 옛 세션 수, 미션 수 = 방 수, 방마다 미션 하나
              SELECT 1 AS ord, 'rooms - pre sessions' AS chk, (SELECT count(*) FROM room) - (SELECT n FROM pre WHERE tbl = 'session') AS n
    UNION ALL SELECT 2, 'works - rooms',        (SELECT count(*) FROM work) - (SELECT count(*) FROM room)
    UNION ALL SELECT 3, 'rooms without one work', (SELECT count(*) FROM room r WHERE (SELECT count(*) FROM work w WHERE w.room_id = r.id) <> 1)
    UNION ALL SELECT 4, 'rooms not in pre',     (SELECT count(*) FROM room r WHERE NOT EXISTS (SELECT 1 FROM p WHERE p.id = r.id))
    -- B. 자식 표 행 수 불변
    UNION ALL SELECT 10, 'message - pre',       (SELECT count(*) FROM message)         - (SELECT n FROM pre WHERE tbl = 'message')
    UNION ALL SELECT 11, 'lane - pre',          (SELECT count(*) FROM lane)            - (SELECT n FROM pre WHERE tbl = 'lane')
    UNION ALL SELECT 12, 'task - pre',          (SELECT count(*) FROM task)            - (SELECT n FROM pre WHERE tbl = 'task')
    UNION ALL SELECT 13, 'task_event - pre',    (SELECT count(*) FROM task_event)      - (SELECT n FROM pre WHERE tbl = 'task_event')
    UNION ALL SELECT 14, 'artifact - pre',      (SELECT count(*) FROM artifact)        - (SELECT n FROM pre WHERE tbl = 'artifact')
    UNION ALL SELECT 15, 'decision - pre',      (SELECT count(*) FROM decision)        - (SELECT n FROM pre WHERE tbl = 'decision')
    UNION ALL SELECT 16, 'hitl_request - pre',  (SELECT count(*) FROM hitl_request)    - (SELECT n FROM pre WHERE tbl = 'hitl_request')
    UNION ALL SELECT 17, 'inbox_item - pre',    (SELECT count(*) FROM inbox_item)      - (SELECT n FROM pre WHERE tbl = 'inbox_item')
    UNION ALL SELECT 18, 'workdir - pre',       (SELECT count(*) FROM workdir)         - (SELECT n FROM pre WHERE tbl = 'workdir')
    UNION ALL SELECT 19, 'session_hop - pre',   (SELECT count(*) FROM session_hop)     - (SELECT n FROM pre WHERE tbl = 'session_hop')
    UNION ALL SELECT 20, 'session_context - pre', (SELECT count(*) FROM session_context) - (SELECT n FROM pre WHERE tbl = 'session_context')
    UNION ALL SELECT 21, 'activity_log - pre',  (SELECT count(*) FROM activity_log)    - (SELECT n FROM pre WHERE tbl = 'activity_log')
    -- C. 이관 행의 work_id 가 그 방의 미션으로 채워졌다
    UNION ALL SELECT 30, 'message work_id wrong',      (SELECT count(*) FROM message c      LEFT JOIN work w ON w.room_id = c.session_id WHERE c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 31, 'lane work_id wrong',         (SELECT count(*) FROM lane c         LEFT JOIN work w ON w.room_id = c.session_id WHERE c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 32, 'task work_id wrong',         (SELECT count(*) FROM task c         LEFT JOIN work w ON w.room_id = c.session_id WHERE c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 33, 'artifact work_id wrong',     (SELECT count(*) FROM artifact c     LEFT JOIN work w ON w.room_id = c.session_id WHERE c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 34, 'decision work_id wrong',     (SELECT count(*) FROM decision c     LEFT JOIN work w ON w.room_id = c.session_id WHERE c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 35, 'hitl_request work_id wrong', (SELECT count(*) FROM hitl_request c LEFT JOIN work w ON w.room_id = c.session_id WHERE c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 36, 'inbox_item work_id wrong',   (SELECT count(*) FROM inbox_item c   LEFT JOIN work w ON w.room_id = c.session_id WHERE c.session_id IS NOT NULL AND c.work_id IS DISTINCT FROM w.id)
    UNION ALL SELECT 37, 'task queued_reason set',     (SELECT count(*) FROM task WHERE queued_reason IS NOT NULL)
    -- D. 값 보존: 미션의 goal 쪽 칸 = 옛 세션, 방에 남은 칸 = 옛 세션
    UNION ALL SELECT 40, 'work drift', (SELECT count(*) FROM work w JOIN p ON p.id = w.room_id
        WHERE (w.title, w.goal, w.acceptance_criteria, w.director_user_id, w.deputy_user_id, w.assignee_agent_id,
               w.completion_condition, w.completion_met, w.status, w.paused_reason, w.paused_detail, w.cost_usd,
               w.created_by, w.created_at, w.updated_at, w.started_at, w.finished_at)
          IS DISTINCT FROM
              (p.title, p.goal, p.acceptance_criteria, p.director_user_id, p.deputy_director_user_id, p.assignee_agent_id,
               p.completion_condition, p.completion_met, p.status, p.paused_reason, p.paused_detail, p.cost_usd,
               p.created_by, p.created_at, p.updated_at, p.started_at, p.finished_at))
    UNION ALL SELECT 41, 'work limits/autonomy not empty', (SELECT count(*) FROM work WHERE limits <> '{}'::jsonb OR autonomy IS NOT NULL)
    UNION ALL SELECT 42, 'room drift', (SELECT count(*) FROM room r JOIN p ON p.id = r.id
        WHERE (r.workspace_id, r.runtime_id, r.isolation, r.autonomy, r.context_reuse_override, r.rebind_prompt,
               r.created_by, r.created_at, r.updated_at, r.limits - 'max_concurrent_works')
          IS DISTINCT FROM
              (p.workspace_id, p.runtime_id, p.isolation, p.autonomy, p.context_reuse_override, p.rebind_prompt,
               p.created_by, p.created_at, p.updated_at, p.limits - 'max_concurrent_works'))
    -- E. §10 이관 규칙: 이름 = 제목, 설명 = goal 첫 줄, 방장 = 만든 사람, 기본 Director = Director,
    --    끝난 세션은 보관된 방, 새 칸은 기본값
    UNION ALL SELECT 50, 'room name/description/owner rule', (SELECT count(*) FROM room r JOIN p ON p.id = r.id
        WHERE r.name IS DISTINCT FROM p.title OR r.description IS DISTINCT FROM split_part(p.goal, E'\n', 1)
           OR r.owner_user_id IS DISTINCT FROM p.created_by OR r.default_director_user_id IS DISTINCT FROM p.director_user_id)
    UNION ALL SELECT 51, 'room status rule', (SELECT count(*) FROM room r JOIN p ON p.id = r.id
        WHERE r.status IS DISTINCT FROM (CASE WHEN p.status IN ('completed', 'cancelled') THEN 'archived' ELSE 'active' END)::room_status)
    UNION ALL SELECT 52, 'room new columns not default', (SELECT count(*) FROM room
        WHERE visibility <> 'workspace' OR deputy_owner_user_id IS NOT NULL OR blocked_reason IS NOT NULL OR blocked_detail IS NOT NULL)
    UNION ALL SELECT 53, 'room limits max_concurrent_works missing', (SELECT count(*) FROM room WHERE NOT limits ? 'max_concurrent_works')
    -- F. 참여자: 에이전트 행 = 옛 표, 방마다 방장 한 명, Director·deputy 는 참여자, 한쪽만
    UNION ALL SELECT 60, 'agent participants - pre', (SELECT count(*) FROM room_participant WHERE agent_id IS NOT NULL) - (SELECT n FROM pre WHERE tbl = 'session_participant')
    UNION ALL SELECT 61, 'agent participant drift', (SELECT count(*) FROM session_participant sp
        WHERE NOT EXISTS (SELECT 1 FROM room_participant rp WHERE rp.room_id = sp.session_id AND rp.agent_id = sp.agent_id
                            AND rp.profile_id = sp.profile_id AND rp.joined_at = sp.joined_at AND rp.role = 'member'))
    UNION ALL SELECT 62, 'rooms without exactly one owner', (SELECT count(*) FROM room r
        WHERE (SELECT count(*) FROM room_participant rp WHERE rp.room_id = r.id AND rp.role = 'owner') <> 1)
    UNION ALL SELECT 63, 'owner row is not the owner', (SELECT count(*) FROM room_participant rp JOIN room r ON r.id = rp.room_id
        WHERE rp.role = 'owner' AND rp.user_id IS DISTINCT FROM r.owner_user_id)
    UNION ALL SELECT 64, 'director/deputy not a participant', (SELECT count(*) FROM work w,
        LATERAL (VALUES (w.director_user_id), (w.deputy_user_id)) u(user_id)
        WHERE u.user_id IS NOT NULL
          AND NOT EXISTS (SELECT 1 FROM room_participant rp WHERE rp.room_id = w.room_id AND rp.user_id = u.user_id))
    UNION ALL SELECT 65, 'extra human participants', (SELECT count(*) FROM room_participant rp JOIN work w ON w.room_id = rp.room_id
        JOIN room r ON r.id = rp.room_id
        WHERE rp.user_id IS NOT NULL AND rp.user_id NOT IN (r.owner_user_id, w.director_user_id)
          AND rp.user_id IS DISTINCT FROM w.deputy_user_id)
    UNION ALL SELECT 66, 'participant both/neither side', (SELECT count(*) FROM room_participant WHERE (agent_id IS NULL) = (user_id IS NULL))
    -- G. 새 표는 비어 있다
    UNION ALL SELECT 70, 'room_link rows',     (SELECT count(*) FROM room_link)
    UNION ALL SELECT 71, 'work_proposal rows', (SELECT count(*) FROM work_proposal)
    UNION ALL SELECT 72, 'room_read_log rows', (SELECT count(*) FROM room_read_log)
) v
ORDER BY ord;
