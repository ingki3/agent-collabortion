-- 0024_artifact_review_history.sql — 아티팩트 리뷰를 append-only 로 (T-S20: S-15, PR #65 리뷰 NN3)
--
-- 0002 의 artifact_review 는 artifact_id 가 PK 라 재리뷰가 `ON CONFLICT DO UPDATE`
-- 로 이전 판정을 덮어썼다 — 같은 아티팩트를 reject 했다가 approve 하면 reject 가
-- 행에서 사라진다. 거절 사유는 decision 행에 남아 E6-04("아티팩트는 사라지지
-- 않는다")는 지켜졌지만, 리뷰 화면이 이력(누가 언제 무엇을 뒤집었나)을 그릴
-- 자리가 없었다.
--
-- 행마다 id 를 주고 아티팩트당 여러 행을 허용한다. "최신 판정" 은 뷰
-- artifact_review_latest — openapi Artifact.review 는 이 뷰의 행이라 계약은
-- 바뀌지 않는다. id 는 삽입 순서를 갖는 정수(identity)라 같은 마이크로초에
-- 적힌 두 판정도 나중 것이 이긴다(uuid 였다면 무작위로 갈린다). 기존 행은
-- 그대로 첫 이력이 된다.
ALTER TABLE artifact_review DROP CONSTRAINT artifact_review_pkey;
ALTER TABLE artifact_review ADD COLUMN id bigint GENERATED ALWAYS AS IDENTITY;
ALTER TABLE artifact_review ADD PRIMARY KEY (id);
CREATE INDEX artifact_review_artifact_reviewed ON artifact_review (artifact_id, reviewed_at DESC, id DESC);

CREATE VIEW artifact_review_latest AS
    SELECT DISTINCT ON (artifact_id) *
    FROM artifact_review
    ORDER BY artifact_id, reviewed_at DESC, id DESC;

COMMENT ON TABLE artifact_review IS
  'FR-2.2 agent_approval 의 판정 이력 — append-only(S-15). 최신 판정은 artifact_review_latest.';
COMMENT ON VIEW artifact_review_latest IS
  '아티팩트당 마지막 판정 한 행 — openapi Artifact.review 가 읽는 자리.';
