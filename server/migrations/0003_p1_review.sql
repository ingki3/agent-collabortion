-- 0003_p1_review — PR #22 리뷰 반영 (contracts v0.4: openapi 0.1.1-draft, daemon-protocol v0.2, colab-cli v0.3)
--
-- R1  idempotency_key.client_seq — CLI가 보내는 X-Colab-Client-Seq. CliContext.last_seq = max(client_seq)
--     (개수가 아니라 최댓값 — seq에 구멍이 나도 키를 재사용하지 않는다).
-- R3  daemon_command.consumed_at — 명령은 응답에 실었다고 소비되지 않는다(daemon-protocol §4.3 표).
--     delivered_at은 더 이상 쓰지 않는다(호환을 위해 열은 남긴다). 효과가 관측될 때까지 매 응답에 다시 실린다.
-- N2  task_event.object_ref — task_event.schema.json과 같은 문자열. 기존 {"ref": "..."} 봉투를 문자열로 정규화.
--
-- 0001·0002는 수정하지 않는다(적용된 마이그레이션은 편집 금지).

-- ---------------------------------------------------------------------------
-- R1: client seq
-- ---------------------------------------------------------------------------

ALTER TABLE idempotency_key ADD COLUMN client_seq integer CHECK (client_seq IS NULL OR client_seq >= 1);

CREATE INDEX idempotency_key_client_seq ON idempotency_key (scope, client_seq DESC) WHERE client_seq IS NOT NULL;

-- ---------------------------------------------------------------------------
-- R3: command consumption (효과 관측 시점)
-- ---------------------------------------------------------------------------

ALTER TABLE daemon_command
    ADD COLUMN task_id     uuid,                            -- payload.task_id (cancel·revoke) — 소비 조건 대조용
    ADD COLUMN attempt     integer,                         -- payload.attempt
    ADD COLUMN session_id  uuid,                            -- payload.session_id (rebind_prepare)
    ADD COLUMN consumed_at timestamptz,
    ADD COLUMN consumed_by text;                            -- finish | revoke_expiry | probe | workdir_report | phase_preparing | ttl

UPDATE daemon_command
SET task_id    = NULLIF(payload->>'task_id', '')::uuid,
    attempt    = NULLIF(payload->>'attempt', '')::integer,
    session_id = NULLIF(payload->>'session_id', '')::uuid;

-- 이전 규칙(전달 즉시 소비)으로 배달된 명령은 그대로 소비된 것으로 본다 — 재전송으로 되살리지 않는다.
UPDATE daemon_command SET consumed_at = delivered_at, consumed_by = 'legacy_delivered' WHERE delivered_at IS NOT NULL;

DROP INDEX daemon_command_pending;
CREATE INDEX daemon_command_pending ON daemon_command (runtime_id, id) WHERE consumed_at IS NULL;
CREATE INDEX daemon_command_task ON daemon_command (task_id, attempt) WHERE consumed_at IS NULL;

-- ---------------------------------------------------------------------------
-- N2: object_ref → string
-- ---------------------------------------------------------------------------

UPDATE task_event
SET object_ref = object_ref->'ref'
WHERE object_ref IS NOT NULL AND jsonb_typeof(object_ref) = 'object' AND object_ref ? 'ref';
