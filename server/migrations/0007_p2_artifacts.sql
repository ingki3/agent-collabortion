-- 0007_p2_artifacts.sql — 아티팩트 제출·리뷰의 저장 자리 (T-S3)
--
-- 0001~0006 은 건드리지 않는다. 0001 의 `artifact` 는 P1 이 "표만" 만들어 둔
-- 것이라 openapi `Artifact` 스키마가 요구하는 칸이 넷 비어 있다:
-- size_bytes · content_type · description · 사람이 올렸을 때의 제출자.
--
-- 왜 본문을 large object 로 두는가: 서버는 Postgres 말고 아무 저장소도 갖고
-- 있지 않고(compose 에도 볼륨이 없다), downloadArtifact 는 50 MB 를 메모리에
-- 올리지 않고 흘려보내야 한다(계약: Content-Length + 스트리밍). bytea 는 읽을
-- 때 통째로 detoast 되고, 파일시스템은 배포·e2e 마다 볼륨을 새로 정해야 한다.
-- large object 는 트랜잭션 안에서 만들어지고 롤백되며 io.Reader 로 읽힌다.
-- storage_ref 는 `pglo:<oid>` 로 적어 어떤 저장소인지 값이 스스로 말하게 한다.

ALTER TABLE artifact ADD COLUMN description          text;
ALTER TABLE artifact ADD COLUMN size_bytes           bigint NOT NULL DEFAULT 0 CHECK (size_bytes >= 0);
ALTER TABLE artifact ADD COLUMN content_type         text;
-- FR-4.3 은 "사람이 자료를 올릴 때"도 같은 엔드포인트다(openapi submitArtifact
-- 의 UserSession 시큐리티). task 가 없는 제출이 있으므로 제출자를 따로 적는다.
ALTER TABLE artifact ADD COLUMN submitted_by_user_id uuid REFERENCES app_user(id);
-- submitted_by_task_id 는 ON DELETE SET NULL 이라 task 가 사라지면 "누가
-- 냈는지"가 같이 사라진다. openapi Artifact.submitted_by.agent_id 는 그 뒤에도
-- 답을 내야 하므로 에이전트를 직접 적는다.
ALTER TABLE artifact ADD COLUMN submitted_by_agent_id uuid REFERENCES agent(id);

-- 같은 이름의 최신 버전 조회(FR-4.3 v2·latest_only)와 제출 순 목록이 전부
-- 이 순서를 읽는다. UNIQUE (session_id, name, version) 은 0001 에 이미 있다.
CREATE INDEX artifact_session_name_version ON artifact (session_id, name, version DESC);
