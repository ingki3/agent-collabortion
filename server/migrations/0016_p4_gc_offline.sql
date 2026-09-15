-- 0016_p4_gc_offline.sql — P4: GC 판정에 필요한 git 상태 분리와 스윕 멱등 (T-S9)
--
-- 왜 `dirty` 하나로는 모자란가. §6 보고는 `git: {branch, merged, dirty,
-- commits_ahead}` 를 따로 주는데 서버는 그것을 `dirty = dirty OR (미병합 AND
-- 커밋>0)` 한 칸으로 접어 저장하고 있었다. FR-6.4 의 두 초록불 —
-- (병합됨 AND 클린) 또는 (커밋 0 AND 클린) — 은 세 값을 따로 봐야 판정되고,
-- E13-12(미병합 커밋 → "병합해 달라")와 E13-13(미커밋 변경 → "커밋하거나
-- 버려 달라")은 Director 에게 요구하는 다음 행동이 달라 사유도 한 문자열로
-- 합칠 수 없다.
--
-- 열 이름은 Lead 계약 결정(T-S9 ask 2)이 정한 openapi `Workdir` 의 새 필드명을
-- 그대로 쓴다: `merged` · `commits_ahead` · `gc_blocked_reason`. 기존 `dirty` 는
-- 뜻을 바꾸지 않고 그대로 둔다(계약 설명이 "미병합 커밋 또는 미커밋 변경").
ALTER TABLE workdir
    ADD COLUMN merged            boolean,                                          -- §6 git.merged. NULL = 아직 보고 없음
    ADD COLUMN commits_ahead     integer NOT NULL DEFAULT 0 CHECK (commits_ahead >= 0), -- §6 git.commits_ahead
    ADD COLUMN tree_dirty        boolean,                                          -- §6 git.dirty (작업 트리만 — `dirty` 는 둘의 OR)
    -- unmerged_commits | uncommitted_changes (계약 enum). 둘 다면 미커밋 쪽을
    -- 적는다 — 커밋되지 않은 변경이 복구 불가능한 쪽이고, 알림 본문이
    -- commits_ahead 를 함께 실어 나머지 절반을 알린다.
    ADD COLUMN gc_blocked_reason text,
    -- 스윕은 주기 작업이다. 이 칸이 없으면 차단된 workdir 하나가 매 틱마다
    -- 같은 알림을 다시 낸다(E14-10 이 오프라인 스윕에 대해 말하는 것과 같은 함정).
    ADD COLUMN gc_notified_at    timestamptz;

-- GC 스윕은 "종료된 세션의 active workdir" 만 본다.
CREATE INDEX workdir_gc_sweep ON workdir (status) WHERE status = 'active';

-- §9 보안 · openapi listTaskEvents: 워크스페이스 `task_event_masking` 이 켜지면
-- 페이로드의 원본 내용(파일 편집 diff · 셸 명령과 출력)을 요약으로 대체하고
-- `masked: true` 로 표시한다. 설정 칸은 0001 부터 있었지만 아무도 읽지 않았다.
--
-- 왜 페이로드 안이 아니라 열인가. `contracts/task_event.schema.json` 의 payload
-- 는 class 마다 닫혀 있고(`additionalProperties: false`), `masked` 를 가진 것은
-- `tool` 하나뿐이다. `message` 이벤트도 마스킹 대상이라 표시할 곳이 필요하고,
-- 계약 `TaskEvent.masked` 는 최상위 필드다.
ALTER TABLE task_event ADD COLUMN masked boolean NOT NULL DEFAULT false;
