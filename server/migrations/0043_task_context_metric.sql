-- task_context_metric — attempt 마다 한 행: 턴 프롬프트의 모양과 그 턴이 쓴 맥락 (T-CTX0)
--
-- 번호는 머지 순서로 정해진다(Lead 규칙) — #379 → #380(0041) → #375(0042) → 이 PR 이라
-- 0043 이다. 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- plan/research/CONTEXT_MEMORY.md 0단계: 「재개된 런타임 세션에 턴마다 히스토리가
-- 한 벌씩 쌓인다」는 가설을 판정하고 1단계(수명주기)의 전후를 재려면, attempt 마다
-- (1) 서버가 보낸 브리프·턴 프롬프트가 구역별로 얼마였는지, (2) 그 턴이 재개였는지와
-- 같은 런타임 세션의 직전 턴이 끝난 지 몇 초 뒤였는지, (3) 턴의 input·cache_read·
-- cache_write 와 도구 호출 수가 한 줄에 있어야 한다. 지금까지는 (3)의 일부만
-- task_usage 에 있었고 cache_write 는 버려졌다.
--
-- 0단계에서는 계측 전용이지만 그 상태로 머물지 않는다: **맥락 1단계 ③(harness
-- v0.9.14 §6)이 세션 상한 판정에 이 표를 읽는다**(Lead 지시 2026-09-28) — 재개 세션의
-- 누적 크기가 상한을 넘으면 다음 task 를 계획적 cold start 로 돌리는 판정의 입력이
-- 이 행들이다. 계약이 이름으로 부르는 칸은 **`samples[0]`** 이다: 그 attempt 첫 턴 중
-- usage 표본의 cache_read + cache_write + input 이 「세션 시작 크기」이고, 이것이
-- contracts.ResumeSessionMaxTokens(300,000)와 견주어진다. 턴 누적 cache_read 는
-- 쓰지 않는다(그 턴의 모든 호출이 다시 읽은 양의 합이라 세션 크기가 아니다).
-- 그러므로 아래 samples 의 자리 순서와 뜻은 한 번 쓰고 버리는 로그가 아니라 계약이다.
--
-- 쓰기는 세이브포인트 안이라 실패해도 claim·heartbeat·finish 는 그대로 간다.
CREATE TABLE task_context_metric (
    task_id          uuid NOT NULL REFERENCES task(id) ON DELETE CASCADE,
    attempt          integer NOT NULL CHECK (attempt >= 1),
    room_id          uuid NOT NULL,
    agent_id         uuid NOT NULL,
    lane_id          uuid NOT NULL,
    runtime_kind     text NOT NULL,
    -- claim 때(서버가 번들을 지을 때) 정해지는 것
    brief_bytes      integer NOT NULL,
    brief_tokens_est integer NOT NULL,
    prompt_bytes     integer NOT NULL,
    prompt_tokens_est integer NOT NULL,
    -- 구역별 크기: {"brief.1": {"bytes": n, "tokens_est": n}, "prompt.history": {...}, …}
    sections         jsonb NOT NULL,
    -- ①②③ 에 실린 줄 수: {"history": n, "history_total": n, "mission_messages": n, "room_decisions": n, "trigger_messages": n}
    counts           jsonb NOT NULL DEFAULT '{}',
    planned_resume   boolean NOT NULL,             -- 번들에 resume 을 실었나(재개 시도)
    resume_session_id text,                         -- 실은 ref 의 session_id
    -- 같은 lane 의 직전 attempt 가 끝난 뒤 이 번들까지 몇 초(없으면 NULL)
    gap_seconds      double precision,
    claimed_at       timestamptz NOT NULL,
    -- finish 때 채우는 것
    resumed          boolean,                       -- finish.resume_outcome (NULL = 재개할 세션이 없었다)
    session_depth    integer,                       -- 이 런타임 세션에서 이 턴 앞에 이어진 턴 수(콜드 = 0)
    finish_session_id text,
    input_tokens     bigint,
    output_tokens    bigint,
    cache_read       bigint,
    cache_write      bigint,
    tool_calls       integer,
    tool_kinds       jsonb,                         -- {"Terminal": n, "Read": n, …}
    finished_at      timestamptz,
    -- heartbeat 로 본 턴 중 누적: [[경과초, cache_read, cache_write, input], …] (바뀐 것만, 상한 240)
    samples          jsonb NOT NULL DEFAULT '[]',
    PRIMARY KEY (task_id, attempt)
);

CREATE INDEX task_context_metric_room ON task_context_metric (room_id, claimed_at);
CREATE INDEX task_context_metric_lane ON task_context_metric (lane_id, claimed_at);
