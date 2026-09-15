-- 0001_init.sql — Colab schema v0 (PLAN.md §3 P0-a "스키마 v0")
--
-- Source of truth: PRD.md §7 (data model), FR-2.3 (session states), FR-7.1 (task
-- states), FR-6.2 (lane states), FR-5.4 (hitl states), FR-1.8.1 (test_chat),
-- FR-8 / SCREEN.md §4.6 (inbox types & severities).
--
-- v1.1 fields are included now so no later migration is needed (PLAN P0-a):
-- message.state, task.budget_override, lane.runtime_session_ref, ...
--
-- Conventions
--   * ids are uuid (gen_random_uuid(), built into Postgres 13+).
--   * every closed value set is a Postgres ENUM whose labels match the PRD exactly.
--   * created_at/updated_at are timestamptz; updated_at is maintained by the app.
--   * "user" is a reserved word, so the people table is app_user.

-- ---------------------------------------------------------------------------
-- Enumerations
-- ---------------------------------------------------------------------------

CREATE TYPE member_role       AS ENUM ('owner', 'admin', 'member');                        -- §7 member.role
CREATE TYPE runtime_status    AS ENUM ('online', 'offline');                                -- FR-9 (not enumerated in PRD)
CREATE TYPE runtime_kind      AS ENUM ('claude_code', 'antigravity', 'hermes');             -- FR-1.6
CREATE TYPE agent_role        AS ENUM ('lead', 'researcher', 'writer', 'engineer', 'reviewer', 'custom'); -- FR-1.1
CREATE TYPE agent_status      AS ENUM ('idle', 'working', 'waiting_human', 'error', 'offline', 'disabled'); -- FR-1.3 (derived, never stored)
CREATE TYPE respond_to        AS ENUM ('owner', 'allowlist', 'workspace', 'nobody');        -- FR-1.9
CREATE TYPE isolation_kind    AS ENUM ('worktree', 'container', 'none');                    -- FR-2.1
CREATE TYPE autonomy_level    AS ENUM ('supervised', 'guided', 'autonomous');               -- FR-2.1
CREATE TYPE session_status    AS ENUM ('draft', 'active', 'paused', 'completing', 'completed', 'cancelled'); -- FR-2.3
CREATE TYPE pause_reason      AS ENUM ('budget', 'time', 'loop', 'runtime_offline', 'director'); -- FR-2.3, FR-7.3, FR-9.2
CREATE TYPE context_type      AS ENUM ('doc', 'url', 'file', 'session');                    -- §7 session_context.type
CREATE TYPE workdir_kind      AS ENUM ('worktree', 'container', 'dir');                     -- §7 workdir.kind
CREATE TYPE workdir_status    AS ENUM ('active', 'retained', 'deleted');                    -- §7 workdir.status
CREATE TYPE author_type       AS ENUM ('user', 'agent', 'system');                          -- FR-3.1
CREATE TYPE message_kind      AS ENUM ('text', 'hitl', 'blocked_q', 'system', 'summary');   -- §7 message.kind
CREATE TYPE message_state     AS ENUM ('posted', 'pending_approval');                       -- §7 message.state (v1.1 supervised)
CREATE TYPE lane_status       AS ENUM ('queued', 'running', 'waiting_human', 'blocked', 'paused', 'done', 'failed'); -- FR-6.2
CREATE TYPE task_status       AS ENUM ('deferred', 'queued', 'dispatched', 'preparing', 'running',
                                       'waiting_human', 'paused', 'completed', 'failed', 'cancelled'); -- FR-7.1
CREATE TYPE failure_kind      AS ENUM ('auth', 'quota', 'config', 'network', 'runtime_offline', 'stall',
                                       'timeout', 'cancelled', 'other');                    -- FR-7.1 retry classes + FR-3.4/FR-7.1 timeout·cancelled
CREATE TYPE hitl_source       AS ENUM ('agent', 'system');                                  -- §7 hitl_request.source
CREATE TYPE hitl_type         AS ENUM ('question', 'choice', 'approval', 'info');           -- FR-5.1
CREATE TYPE hitl_status       AS ENUM ('open', 'answered', 'auto_answered');                -- FR-5.4
CREATE TYPE decision_source   AS ENUM ('hitl', 'agent');                                    -- §7 decision.source
CREATE TYPE inbox_item_type   AS ENUM ('hitl_request', 'lane_blocked', 'session_completed', 'session_paused',
                                       'run_failed', 'runtime_offline', 'mention');         -- FR-8, SCREEN §4.6
CREATE TYPE inbox_severity    AS ENUM ('action_required', 'attention', 'info');             -- SCREEN §4.6
CREATE TYPE test_chat_status  AS ENUM ('open', 'closed');                                   -- FR-1.8.1 (not enumerated in PRD)

-- ---------------------------------------------------------------------------
-- People and workspaces
-- ---------------------------------------------------------------------------

CREATE TABLE app_user (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email        text NOT NULL UNIQUE,
    display_name text NOT NULL,
    avatar_url   text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE workspace (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name       text NOT NULL,
    slug       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE member (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    role         member_role NOT NULL DEFAULT 'member',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id)
);

CREATE TABLE workspace_settings (
    workspace_id           uuid PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    -- FR-3.5: {max_chain_depth: 8, max_hops_per_hour: 60, max_pair_roundtrips: 5}
    loop_limits            jsonb NOT NULL DEFAULT '{"max_chain_depth": 8, "max_hops_per_hour": 60, "max_pair_roundtrips": 5}',
    -- FR-7.3: default per-task budget, pricing table for estimated usage, etc.
    budget_policy          jsonb NOT NULL DEFAULT '{}',
    -- FR-4.4: {max_summary_tokens: 2000, include_artifacts: "links"}
    context_reuse          jsonb NOT NULL DEFAULT '{"max_summary_tokens": 2000, "include_artifacts": "links"}',
    default_isolation      isolation_kind NOT NULL DEFAULT 'none',
    -- FR-9: daemon global concurrency (default 10), per-runtime caps
    runtime_policy         jsonb NOT NULL DEFAULT '{"max_concurrent_tasks": 10}',
    workdir_retention_days integer NOT NULL DEFAULT 14 CHECK (workdir_retention_days >= 0),   -- FR-6.4
    workdir_disk_quota_gb  integer CHECK (workdir_disk_quota_gb IS NULL OR workdir_disk_quota_gb > 0), -- FR-6.4
    runtime_offline_grace  interval NOT NULL DEFAULT interval '7 days',                     -- FR-9.2
    -- §9 보안: task_event payload masking (summary only) — not in §7, added
    task_event_masking     boolean NOT NULL DEFAULT false,
    updated_at             timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Runtimes (daemons)
-- ---------------------------------------------------------------------------

CREATE TABLE runtime (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id   uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name           text NOT NULL,
    host           text,
    status         runtime_status NOT NULL DEFAULT 'offline',
    daemon_version text,
    last_seen_at   timestamptz,
    -- probe(): [{kind, version, models[], logged_in}]
    capabilities   jsonb NOT NULL DEFAULT '[]',
    -- FR-9: [{path, remote_url, branch, clean}] — remote_url is the "same repo" key for FR-9.2 rebinding
    repos          jsonb NOT NULL DEFAULT '[]',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

-- ---------------------------------------------------------------------------
-- Agents and profiles
-- ---------------------------------------------------------------------------

CREATE TABLE agent (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id         uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name                 text NOT NULL,
    role                 agent_role NOT NULL,
    role_description     text NOT NULL,
    instructions         text NOT NULL,
    -- FR-1.1: allowed tools / skills / MCP servers
    tools                jsonb NOT NULL DEFAULT '[]',
    owner_id             uuid NOT NULL REFERENCES app_user(id),
    respond_to           respond_to NOT NULL DEFAULT 'owner',
    respond_to_allowlist uuid[] NOT NULL DEFAULT '{}',                -- app_user ids (FR-1.9 allowlist)
    avatar_url           text,
    budget_per_task      numeric(12, 4) CHECK (budget_per_task IS NULL OR budget_per_task >= 0),  -- USD
    max_concurrent_tasks integer NOT NULL DEFAULT 3 CHECK (max_concurrent_tasks > 0),
    -- FR-1.8: definition vs instance
    definition_source    text,
    definition_version   text,
    -- FR-1.3: status is derived from task state and is NOT stored (see agent_status type).
    archived_at          timestamptz,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, name)
);

CREATE TABLE agent_profile (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id            uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    name                text NOT NULL,
    runtime_kind        runtime_kind NOT NULL,
    model               text NOT NULL,
    options             jsonb NOT NULL DEFAULT '{}',
    env                 jsonb NOT NULL DEFAULT '{}',
    args                text[] NOT NULL DEFAULT '{}',
    is_default          boolean NOT NULL DEFAULT false,
    fallback_profile_id uuid REFERENCES agent_profile(id) ON DELETE SET NULL,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (agent_id, name),
    CHECK (fallback_profile_id IS NULL OR fallback_profile_id <> id)
);

-- exactly one default profile per agent
CREATE UNIQUE INDEX agent_profile_one_default ON agent_profile (agent_id) WHERE is_default;

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------

CREATE TABLE session (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id            uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title                   text NOT NULL,
    goal                    text NOT NULL,
    acceptance_criteria     text[] NOT NULL DEFAULT '{}',
    director_user_id        uuid NOT NULL REFERENCES app_user(id),
    deputy_director_user_id uuid REFERENCES app_user(id),
    assignee_agent_id       uuid REFERENCES agent(id),
    -- C4: execution machine. Nullable only while isolation = none and no task has been
    -- dispatched yet; fixed at first dispatch (M10).
    runtime_id              uuid REFERENCES runtime(id),
    -- {kind: worktree|container|none, repo_path?, image?}
    isolation               jsonb NOT NULL,
    -- FR-2.2: AND/OR tree of {type: artifact_submitted|agent_approval|user_approval|criteria_met|manual, ...}
    completion_condition    jsonb NOT NULL DEFAULT '{"op": "and", "conditions": [{"type": "artifact_submitted", "who": "assignee"}, {"type": "user_approval"}]}',
    -- FR-2.1: {budget_usd?, budget_tokens?, time_limit?, max_tasks?, max_parallel_lanes: 5}
    limits                  jsonb NOT NULL DEFAULT '{"max_parallel_lanes": 5}',
    autonomy                autonomy_level NOT NULL DEFAULT 'guided',
    context_reuse_override  jsonb,
    status                  session_status NOT NULL DEFAULT 'draft',
    paused_reason           pause_reason,                                        -- set while status = paused
    cost_usd                numeric(12, 4) NOT NULL DEFAULT 0,
    created_by              uuid NOT NULL REFERENCES app_user(id),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    started_at              timestamptz,
    finished_at             timestamptz,
    CHECK (isolation ? 'kind'),
    CHECK ((status = 'paused') = (paused_reason IS NOT NULL))
);

CREATE INDEX session_workspace_status ON session (workspace_id, status, created_at DESC);
CREATE INDEX session_runtime ON session (runtime_id) WHERE runtime_id IS NOT NULL;

CREATE TABLE session_participant (
    session_id uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    agent_id   uuid NOT NULL REFERENCES agent(id),
    profile_id uuid NOT NULL REFERENCES agent_profile(id),
    joined_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, agent_id)
);

CREATE TABLE session_context (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    type       context_type NOT NULL,
    ref        text NOT NULL,          -- url, storage ref, or session id
    summary    text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX session_context_session ON session_context (session_id);

-- ---------------------------------------------------------------------------
-- Lanes, workdirs, tasks
-- ---------------------------------------------------------------------------
-- lane ↔ workdir ↔ task ↔ message reference each other; FKs that point forward
-- are added after all tables exist (see "Deferred foreign keys").

CREATE TABLE lane (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id             uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    parent_lane_id         uuid REFERENCES lane(id),
    agent_id               uuid NOT NULL REFERENCES agent(id),
    profile_id             uuid NOT NULL REFERENCES agent_profile(id),
    depends_on             uuid[] NOT NULL DEFAULT '{}',     -- lane ids (DAG, FR-6.2)
    workdir_id             uuid,                              -- FK below (C3)
    delegated_from_task_id uuid,                              -- FK below; join-group key (FR-6.5)
    -- C1 / 리뷰#04-4: the only basis for resume. {kind, session_id, provenance}
    runtime_session_ref    jsonb,
    status                 lane_status NOT NULL DEFAULT 'queued',
    blocked_note           text,                              -- last value cache (FR-6.2.1)
    blocked_message_id     uuid,                              -- FK below; thread root of the question card
    reentry_count          integer NOT NULL DEFAULT 0 CHECK (reentry_count >= 0),
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    finished_at            timestamptz,
    CHECK (runtime_session_ref IS NULL OR (runtime_session_ref ? 'kind' AND runtime_session_ref ? 'session_id'))
);

CREATE INDEX lane_session_status ON lane (session_id, status);
CREATE INDEX lane_session_agent_recent ON lane (session_id, agent_id, created_at DESC);  -- lane resolution rule 3
CREATE INDEX lane_delegated_from ON lane (delegated_from_task_id) WHERE delegated_from_task_id IS NOT NULL;

CREATE TABLE workdir (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    -- C3: worktree isolation → agent_id only (shared by the agent's lanes);
    --     container/none   → lane_id (one per lane)
    agent_id     uuid REFERENCES agent(id),
    lane_id      uuid REFERENCES lane(id) ON DELETE SET NULL,
    kind         workdir_kind NOT NULL,
    path_or_ref  text NOT NULL,
    branch       text,
    status       workdir_status NOT NULL DEFAULT 'active',
    disk_bytes   bigint NOT NULL DEFAULT 0 CHECK (disk_bytes >= 0),
    last_used_at timestamptz,
    retain_until timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (agent_id IS NOT NULL OR lane_id IS NOT NULL)
);

CREATE INDEX workdir_session ON workdir (session_id);
CREATE INDEX workdir_gc ON workdir (retain_until) WHERE status = 'retained';

ALTER TABLE lane ADD CONSTRAINT lane_workdir_fk FOREIGN KEY (workdir_id) REFERENCES workdir(id) ON DELETE SET NULL;

CREATE TABLE task (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    lane_id                uuid NOT NULL REFERENCES lane(id) ON DELETE CASCADE,
    -- denormalized from lane/session for queue queries (queue is claimed per runtime,
    -- and queued tasks of a paused session must not dispatch — FR-2.3 C3′)
    session_id             uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    runtime_id             uuid REFERENCES runtime(id),
    agent_id               uuid NOT NULL REFERENCES agent(id),
    profile_id             uuid NOT NULL REFERENCES agent_profile(id),
    trigger_message_id     uuid,                              -- FK below
    delegated_from_task_id uuid REFERENCES task(id),
    restarted_from_task_id uuid REFERENCES task(id),          -- B: re-instruction is a new task (FR-3.4)
    originator_user_id     uuid REFERENCES app_user(id),      -- top-of-chain human (FR-1.9)
    coalesced_message_ids  uuid[] NOT NULL DEFAULT '{}',      -- FR-3.4 merge per lane
    attempt                integer NOT NULL DEFAULT 1 CHECK (attempt >= 1),
    max_attempts           integer NOT NULL DEFAULT 3 CHECK (max_attempts >= 1),
    pending_hitl           boolean NOT NULL DEFAULT false,    -- N4: set on tool call, transition at turn_end
    budget_override        numeric(12, 4) CHECK (budget_override IS NULL OR budget_override >= 0), -- C2′ (USD)
    status                 task_status NOT NULL DEFAULT 'queued',
    paused_reason          pause_reason,                      -- paused(budget) etc. (FR-7.3)
    failure_kind           failure_kind,                      -- set when status = failed
    heartbeat_at           timestamptz,                       -- FR-7.1: 15s heartbeat while running
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),
    dispatched_at          timestamptz,
    started_at             timestamptz,
    finished_at            timestamptz,
    CHECK ((status = 'paused') = (paused_reason IS NOT NULL)),
    CHECK (status = 'failed' OR failure_kind IS NULL)
);

-- queue: SKIP LOCKED claim per runtime, oldest first
CREATE INDEX task_queue ON task (status, runtime_id, created_at);
-- heartbeat sweep (FR-7.1: 3 min silence → runtime offline)
CREATE INDEX task_heartbeat_running ON task (heartbeat_at) WHERE status = 'running';
CREATE INDEX task_lane ON task (lane_id, created_at DESC);
CREATE INDEX task_session ON task (session_id, created_at DESC);
CREATE INDEX task_delegated_from ON task (delegated_from_task_id) WHERE delegated_from_task_id IS NOT NULL;

ALTER TABLE lane ADD CONSTRAINT lane_delegated_from_task_fk
    FOREIGN KEY (delegated_from_task_id) REFERENCES task(id) ON DELETE SET NULL;

CREATE TABLE task_event (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    seq           integer NOT NULL CHECK (seq >= 0),
    -- FR-7.2 render class; closed set fixed by contracts/task_event.schema.json (P0-b)
    class         text NOT NULL,
    verb          text,
    object_ref    jsonb,
    outcome       text,
    tool          text,
    input         jsonb,
    output        jsonb,
    usage         jsonb,
    superseded_by uuid REFERENCES task_event(id),             -- in-place update keeps history
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, seq)                                       -- idempotency key task_id + seq (FR-7.1 M5)
);

CREATE TABLE task_usage (
    task_id       uuid PRIMARY KEY REFERENCES task(id) ON DELETE CASCADE,
    input_tokens  bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cache_read    bigint NOT NULL DEFAULT 0 CHECK (cache_read >= 0),
    cost_usd      numeric(12, 6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    estimated     boolean NOT NULL DEFAULT false,             -- FR-7.3 usage=false runtimes
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Messages
-- ---------------------------------------------------------------------------

CREATE TABLE message (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id     uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    author_type    author_type NOT NULL,
    author_id      uuid,                                       -- app_user.id or agent.id; NULL for system
    parent_id      uuid REFERENCES message(id),                -- thread root
    content        text NOT NULL,
    mentions       jsonb NOT NULL DEFAULT '[]',                -- [{kind: agent|user|all, id}] (FR-3.2)
    source_task_id uuid REFERENCES task(id) ON DELETE SET NULL,
    kind           message_kind NOT NULL DEFAULT 'text',
    state          message_state NOT NULL DEFAULT 'posted',    -- M10: v1.1 supervised mode
    created_at     timestamptz NOT NULL DEFAULT now(),
    edited_at      timestamptz,
    CHECK ((author_type = 'system') = (author_id IS NULL))
);

CREATE INDEX message_session_created ON message (session_id, created_at);
CREATE INDEX message_parent ON message (parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX message_source_task ON message (source_task_id) WHERE source_task_id IS NOT NULL;

-- Deferred foreign keys (message ↔ task ↔ lane cycle)
ALTER TABLE task ADD CONSTRAINT task_trigger_message_fk
    FOREIGN KEY (trigger_message_id) REFERENCES message(id) ON DELETE SET NULL;
ALTER TABLE lane ADD CONSTRAINT lane_blocked_message_fk
    FOREIGN KEY (blocked_message_id) REFERENCES message(id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- HITL, artifacts, decisions
-- ---------------------------------------------------------------------------

CREATE TABLE hitl_request (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id       uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    task_id          uuid REFERENCES task(id) ON DELETE SET NULL,   -- nullable: source = system (M2)
    source           hitl_source NOT NULL DEFAULT 'agent',
    type             hitl_type NOT NULL,
    question         text NOT NULL,
    options          text[] NOT NULL DEFAULT '{}',                  -- choice
    proposed_default text,                                          -- required for question/choice (FR-5.1)
    -- director | any_member | <app_user uuid> (FR-5.4; unsupported specs rejected on write)
    approver_spec    text NOT NULL DEFAULT 'director',
    due_at           timestamptz NOT NULL,
    overdue          boolean NOT NULL DEFAULT false,                 -- M7: stays open, flagged
    status           hitl_status NOT NULL DEFAULT 'open',
    approved         boolean,                                        -- approval type
    answer           text,
    answered_by      uuid REFERENCES app_user(id),
    answered_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK (approver_spec IN ('director', 'any_member')
           OR approver_spec ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
    CHECK (type NOT IN ('question', 'choice') OR proposed_default IS NOT NULL),
    CHECK (source = 'system' OR task_id IS NOT NULL),
    CHECK (status = 'open' OR answered_at IS NOT NULL)
);

CREATE INDEX hitl_request_session ON hitl_request (session_id, created_at DESC);
CREATE INDEX hitl_request_open ON hitl_request (due_at) WHERE status = 'open';           -- inbox "needs action", overdue sweep
-- FR-7.1: at most one open HITL per task
CREATE UNIQUE INDEX hitl_request_one_open_per_task ON hitl_request (task_id) WHERE status = 'open' AND task_id IS NOT NULL;

CREATE TABLE artifact (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id           uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    name                 text NOT NULL,
    version              integer NOT NULL DEFAULT 1 CHECK (version >= 1),   -- same name resubmitted → v2 (FR-4.3)
    type                 text NOT NULL,                                       -- file | diff | branch | ... (open set)
    storage_ref          text NOT NULL,
    submitted_by_task_id uuid REFERENCES task(id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, name, version)
);

CREATE TABLE decision (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    summary    text NOT NULL,
    rationale  text,
    source     decision_source NOT NULL,
    ref_id     uuid,                                            -- hitl_request.id or task.id
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX decision_session ON decision (session_id, created_at);

-- ---------------------------------------------------------------------------
-- Inbox and audit
-- ---------------------------------------------------------------------------

CREATE TABLE inbox_item (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    member_id  uuid NOT NULL REFERENCES member(id) ON DELETE CASCADE,
    type       inbox_item_type NOT NULL,
    severity   inbox_severity NOT NULL,
    session_id uuid REFERENCES session(id) ON DELETE CASCADE,
    ref_id     uuid,                                            -- hitl_request / message / task / runtime id by type
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX inbox_item_member_unread ON inbox_item (member_id, severity, created_at DESC) WHERE read_at IS NULL;
CREATE INDEX inbox_item_member ON inbox_item (member_id, created_at DESC);

CREATE TABLE activity_log (
    id           bigserial PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    session_id   uuid REFERENCES session(id) ON DELETE SET NULL,
    actor_type   author_type NOT NULL,
    actor_id     uuid,
    action       text NOT NULL,                                 -- e.g. session.director_changed, agent.respond_to_changed
    object_type  text,
    object_id    uuid,
    payload      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX activity_log_workspace ON activity_log (workspace_id, created_at DESC);
CREATE INDEX activity_log_session ON activity_log (session_id, created_at DESC) WHERE session_id IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Test chat (FR-1.8.1) — not a session: no lane, no task, no CLI token.
-- Turns are stored inline; usage is added to workspace cost only.
-- ---------------------------------------------------------------------------

CREATE TABLE test_chat (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id        uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id            uuid NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    profile_id          uuid NOT NULL REFERENCES agent_profile(id),
    user_id             uuid NOT NULL REFERENCES app_user(id),
    runtime_id          uuid REFERENCES runtime(id),
    status              test_chat_status NOT NULL DEFAULT 'open',
    transport           text,                                   -- acp | cli (shown to the user)
    runtime_session_ref jsonb,
    turns               jsonb NOT NULL DEFAULT '[]',            -- [{role, content, at, usage?}]
    input_tokens        bigint NOT NULL DEFAULT 0,
    output_tokens       bigint NOT NULL DEFAULT 0,
    cost_usd            numeric(12, 6) NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    closed_at           timestamptz
);

CREATE INDEX test_chat_agent ON test_chat (agent_id, created_at DESC);
