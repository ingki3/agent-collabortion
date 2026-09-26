-- workdir_mission_folders — 작업 폴더를 방 → 미션 → 에이전트로 (T-FOLDERS, daemon-protocol v0.10.0 §6.1)
--
-- 경로를 서버가 짓는다(모든 kind). 그래서 `none` 폴더도 첫 attempt 부터 행이 있고, 그 행은
-- lane 이 아니라 (방, 미션, 에이전트) 하나다 — 같은 미션의 같은 에이전트 lane 은 한 폴더를
-- 함께 쓴다(D3 A). 미션 공용 `_shared` 는 에이전트도 lane 도 없는 행이다(role = 'shared').
--
--   work_id  이 폴더가 속한 미션. NULL = 미션 밖 `_room` · worktree 체크아웃 · 옛 배치 행.
--            미션이 지워지면 NULL 로 남는다(행과 폴더는 GC 가 정한다).
--   role     agent | shared. 옛 행은 전부 agent(기본값) — 옛 배치는 옮기지 않는다(D6 A).
--
-- CHECK 완화: `agent_id IS NOT NULL OR lane_id IS NOT NULL` 는 공용 행을 막는다. 공용 행만
-- 둘 다 비울 수 있고, agent 행은 여전히 둘 중 하나가 있어야 한다.
--
-- 인덱스: 번들 조립(EnsureBundleRow)이 (방, 미션, 에이전트) 로 행을 찾고, S13·미션 닫기
-- 확인이 `?work_id=` 로 센다.

ALTER TABLE workdir
    ADD COLUMN work_id uuid REFERENCES work(id) ON DELETE SET NULL,
    ADD COLUMN role    text NOT NULL DEFAULT 'agent' CHECK (role IN ('agent', 'shared'));

ALTER TABLE workdir DROP CONSTRAINT workdir_check;
ALTER TABLE workdir ADD CONSTRAINT workdir_owner_check
    CHECK (role = 'shared' OR agent_id IS NOT NULL OR lane_id IS NOT NULL);

CREATE INDEX workdir_room_work_agent ON workdir (session_id, work_id, agent_id) WHERE status <> 'deleted';
CREATE INDEX workdir_work ON workdir (work_id) WHERE work_id IS NOT NULL;
