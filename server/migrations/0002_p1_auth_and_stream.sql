-- 0002_p1_auth_and_stream.sql — P1 storage the API draft assumed but schema v0
-- did not have (G2 decision Q1) plus the daemon-protocol fields (task tokens,
-- attempt-scoped task_event seq, not_before, daemon command queue).
--
-- 0001_init.sql is never edited (PLAN §10.3). Everything here is additive except
-- one deliberate rename-in-place: task_event's (task_id, seq) uniqueness becomes
-- (task_id, attempt, seq) because daemon-protocol.md §4.2 restarts seq at 1 for
-- every attempt. The index keeps its 0001 name so the P0-a schema test still
-- finds it.
--
-- session.isolation gains an optional "remote_url" key (Q2) — jsonb, no DDL.

-- ---------------------------------------------------------------------------
-- Auth (S1/S2): password + user sessions
-- ---------------------------------------------------------------------------

ALTER TABLE app_user ADD COLUMN password_hash text;   -- argon2id PHC string; NULL = no password login (SSO v1.1)

CREATE TABLE user_session (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    token_hash   text NOT NULL UNIQUE,                     -- sha256(cookie value)
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz,
    revoked_at   timestamptz
);

CREATE INDEX user_session_user ON user_session (user_id);

-- ---------------------------------------------------------------------------
-- Invites (S3)
-- ---------------------------------------------------------------------------

CREATE TABLE workspace_invite (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    email        text,                                     -- NULL = anyone with the link
    role         member_role NOT NULL DEFAULT 'member' CHECK (role <> 'owner'),
    token        text NOT NULL UNIQUE,                     -- the link itself is the credential
    invited_by   uuid NOT NULL REFERENCES app_user(id),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    accepted_at  timestamptz,
    accepted_by  uuid REFERENCES app_user(id),
    revoked_at   timestamptz
);

CREATE INDEX workspace_invite_workspace ON workspace_invite (workspace_id, created_at DESC);
CREATE INDEX workspace_invite_pending_email ON workspace_invite (lower(email))
    WHERE email IS NOT NULL AND accepted_at IS NULL AND revoked_at IS NULL;

-- ---------------------------------------------------------------------------
-- Runtime pairing (S12) + daemon token (daemon-protocol §1, §2)
-- ---------------------------------------------------------------------------

CREATE TYPE pairing_status AS ENUM ('waiting', 'connected', 'probing', 'ready', 'expired');

CREATE TABLE runtime_pairing (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name         text,                                     -- suggested runtime name
    code_hash    text NOT NULL UNIQUE,                     -- sha256(pairing_code); code shown once
    status       pairing_status NOT NULL DEFAULT 'waiting',
    runtime_id   uuid REFERENCES runtime(id) ON DELETE SET NULL,
    created_by   uuid NOT NULL REFERENCES app_user(id),
    expires_at   timestamptz NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    connected_at timestamptz,
    ready_at     timestamptz
);

CREATE INDEX runtime_pairing_workspace ON runtime_pairing (workspace_id, created_at DESC);

ALTER TABLE runtime ADD COLUMN daemon_token_hash text UNIQUE;  -- sha256("cdt_…"); NULL until paired
ALTER TABLE runtime ADD COLUMN offline_since     timestamptz;  -- FR-9.2 grace starts here

-- ---------------------------------------------------------------------------
-- Notifications (FR-8): per-session subscription + member defaults (Q4)
-- ---------------------------------------------------------------------------

CREATE TYPE subscription_level AS ENUM ('all', 'hitl_only', 'completion_only');

CREATE TABLE session_subscription (
    session_id uuid NOT NULL REFERENCES session(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
    level      subscription_level NOT NULL DEFAULT 'all',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, user_id)
);

ALTER TABLE member ADD COLUMN notification_settings jsonb NOT NULL
    DEFAULT '{"email": true, "push": false, "default_subscription": "all"}';

-- ---------------------------------------------------------------------------
-- Artifact review (FR-2.2 agent_approval) — table only; operations are P2
-- ---------------------------------------------------------------------------

CREATE TYPE review_verdict AS ENUM ('approve', 'reject');

CREATE TABLE artifact_review (
    artifact_id       uuid PRIMARY KEY REFERENCES artifact(id) ON DELETE CASCADE,
    verdict           review_verdict NOT NULL,
    comments          text,
    reviewer_agent_id uuid NOT NULL REFERENCES agent(id),
    reviewer_task_id  uuid REFERENCES task(id) ON DELETE SET NULL,
    decision_id       uuid REFERENCES decision(id) ON DELETE SET NULL,
    reviewed_at       timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Idempotency keys (openapi.md §1 — 24h retention)
-- ---------------------------------------------------------------------------

CREATE TABLE idempotency_key (
    scope        text NOT NULL,                            -- 'user:<uuid>' | 'task:<uuid>' — a key never crosses principals
    key          text NOT NULL,
    request_hash text NOT NULL,                            -- sha256(method + path + body)
    status       integer NOT NULL,
    response     jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (scope, key)
);

CREATE INDEX idempotency_key_expires ON idempotency_key (expires_at);

-- ---------------------------------------------------------------------------
-- SSE backfill log (openapi.md D1 / Q5 — 10 minute window, then `resync`)
-- ---------------------------------------------------------------------------

CREATE TABLE stream_event (
    id           bigserial PRIMARY KEY,                    -- the SSE id / Last-Event-ID cursor
    workspace_id uuid NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    session_id   uuid,                                     -- NULL = workspace-level event
    type         text NOT NULL,                            -- StreamEvent.type
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stream_event_workspace ON stream_event (workspace_id, id);
CREATE INDEX stream_event_created ON stream_event (created_at);

-- ---------------------------------------------------------------------------
-- Task tokens (colab-cli.md §1, daemon-protocol §5) — hash only
-- ---------------------------------------------------------------------------

CREATE TABLE task_token (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id       uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    attempt       integer NOT NULL CHECK (attempt >= 1),
    token_hash    text NOT NULL UNIQUE,                    -- sha256("ctk_…")
    lane_id       uuid NOT NULL,
    session_id    uuid NOT NULL,
    agent_id      uuid NOT NULL,
    runtime_id    uuid REFERENCES runtime(id) ON DELETE SET NULL,
    issued_at     timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    revoke_reason text,                                    -- requeue | cancelled | completed | waiting_human
    UNIQUE (task_id, attempt)
);

-- ---------------------------------------------------------------------------
-- Server → daemon command queue (daemon-protocol §4.3): delivered on the next
-- claim / events / heartbeat response of that runtime, at least once.
-- ---------------------------------------------------------------------------

CREATE TABLE daemon_command (
    id           bigserial PRIMARY KEY,
    runtime_id   uuid NOT NULL REFERENCES runtime(id) ON DELETE CASCADE,
    type         text NOT NULL,                            -- cancel | revoke | probe | gc | rebind_prepare
    payload      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz
);

CREATE INDEX daemon_command_pending ON daemon_command (runtime_id, id) WHERE delivered_at IS NULL;

-- ---------------------------------------------------------------------------
-- Task attempts (history behind Task.attempts[] and FR-7.1 retry bookkeeping)
-- ---------------------------------------------------------------------------

CREATE TABLE task_attempt (
    task_id       uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    attempt       integer NOT NULL CHECK (attempt >= 1),
    runtime_id    uuid REFERENCES runtime(id) ON DELETE SET NULL,
    dispatched_at timestamptz,
    started_at    timestamptz,
    finished_at   timestamptz,
    outcome       text,                                    -- completed | failed | cancelled | timeout | runtime_offline | …
    failure_kind  failure_kind,
    resumed       boolean,
    stop_reason   text,
    PRIMARY KEY (task_id, attempt)
);

-- ---------------------------------------------------------------------------
-- task: daemon-protocol fields
-- ---------------------------------------------------------------------------

ALTER TABLE task ADD COLUMN not_before  timestamptz;          -- rate_limited retry (G1 F3)
ALTER TABLE task ADD COLUMN stop_reason text;                 -- finish.stop_reason of the last attempt

CREATE INDEX task_dispatched_at ON task (dispatched_at) WHERE status = 'dispatched';

-- ---------------------------------------------------------------------------
-- task_event: attempt-scoped seq + normalized payload (task_event.schema.json)
-- ---------------------------------------------------------------------------

ALTER TABLE task_event ADD COLUMN attempt integer NOT NULL DEFAULT 1 CHECK (attempt >= 1);
ALTER TABLE task_event ADD COLUMN ts      timestamptz;        -- daemon wall clock (display only; order by seq)
ALTER TABLE task_event ADD COLUMN payload jsonb;              -- class-specific $defs payload
ALTER TABLE task_event DROP CONSTRAINT task_event_task_id_seq_key;
CREATE UNIQUE INDEX task_event_task_id_seq_key ON task_event (task_id, attempt, seq);

-- ---------------------------------------------------------------------------
-- failure_kind: contracts/protocol.go has rate_limited (G1 F3); 0001 does not.
-- ---------------------------------------------------------------------------

ALTER TYPE failure_kind ADD VALUE IF NOT EXISTS 'rate_limited' AFTER 'quota';
