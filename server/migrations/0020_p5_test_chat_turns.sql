-- 0020_p5_test_chat_turns.sql — 테스트 채팅 턴의 진행 상태 (T-S12, daemon-protocol v0.8 §4.5)
--
-- `test_chat` 행은 0001 부터 있었지만(turns jsonb · runtime_session_ref · transport)
-- 한 사용자 턴이 **데몬에게 나가 있는 동안**의 상태를 둘 자리가 없었다. §4.5 는
-- 테스트 채팅의 한 턴을 "토큰 없는 attempt" 하나로 정의한다 — claim 이 번들을
-- 만들고, phase·events·heartbeat·finish 가 `task.id = test_chat.id` ·
-- `attempt = 사용자 턴 번호` 로 온다. 그러려면 task 행이 갖던 것과 같은 진행
-- 칸(dispatched_at · heartbeat_at · 진행 상태)이 이 행에도 있어야 한다:
--
--   turn_status   idle | queued | dispatched | preparing | running
--                 idle 은 "진행 중 턴 없음"(postTestChatTurn 이 다음 턴을 받는 조건),
--                 queued 는 claim 대상, 나머지는 §4.1·§4.2 의 attempt 상태 그대로.
--   turn_no       진행 중(또는 마지막) 사용자 턴 번호 = 번들의 task.attempt.
--   turn_text     그 턴에 도착한 message.say 본문(턴 단위로 합친 것). finish 에서
--                 agent 턴으로 확정되고 비워진다. task_event 는 저장하지 않는다(§4.5).
--   turn_usage    heartbeat·usage.report 가 준 그 턴의 누적 usage(finish.usage 가 비면
--                 이것으로 채운다 — 세션 task 의 S-19 와 같은 이유).
--   turn_last_seq (task_id, attempt, seq) 멱등의 근거 — 이미 본 seq 는 무시한다.
--   workdir_path  서버가 조립한 `<workdir_root>/.colab/testchat/<id>` — gc 명령이 싣는다.
--   estimated     비용에 추정치가 섞였는가(openapi TestChat.estimated).
--
-- 재큐잉 칸(attempt 상한·not_before)은 없다: §4.5 는 만료를 재큐잉 없이 그 턴의
-- error 로 닫는다.
ALTER TABLE test_chat
    ADD COLUMN IF NOT EXISTS turn_status   text NOT NULL DEFAULT 'idle'
        CHECK (turn_status IN ('idle', 'queued', 'dispatched', 'preparing', 'running')),
    ADD COLUMN IF NOT EXISTS turn_no       integer NOT NULL DEFAULT 0 CHECK (turn_no >= 0),
    ADD COLUMN IF NOT EXISTS dispatched_at timestamptz,
    ADD COLUMN IF NOT EXISTS started_at    timestamptz,
    ADD COLUMN IF NOT EXISTS heartbeat_at  timestamptz,
    ADD COLUMN IF NOT EXISTS turn_text     text NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS turn_usage    jsonb,
    ADD COLUMN IF NOT EXISTS turn_last_seq integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS workdir_path  text,
    ADD COLUMN IF NOT EXISTS estimated     boolean NOT NULL DEFAULT false;

-- claim 은 "이 런타임에 고정된 채팅의 queued 턴" 만 본다(§4.5 동시성·claim).
CREATE INDEX IF NOT EXISTS test_chat_queued ON test_chat (runtime_id, updated_at) WHERE turn_status = 'queued';
-- 만료 스윕(dispatched 5분 · running 3분)이 진행 중 턴만 훑는다.
CREATE INDEX IF NOT EXISTS test_chat_in_flight ON test_chat (turn_status) WHERE turn_status <> 'idle';

COMMENT ON COLUMN test_chat.turn_status IS
  'daemon-protocol v0.8 §4.5 — 진행 중 사용자 턴의 attempt 상태. idle 이면 다음 턴을 받는다(postTestChatTurn 409 의 조건).';
COMMENT ON COLUMN test_chat.turn_no IS
  '진행 중(또는 마지막) 사용자 턴 번호 = TaskBundle.task.attempt (§4.5).';
