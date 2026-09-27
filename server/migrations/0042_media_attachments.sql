-- media_attachments — 미디어 미리보기·파일 붙이기 (PRD v0.19.10 FR-4.3.1 · FR-3.7, openapi v0.3.7)
--
-- 1) artifact.content_type_judged
--    v0.3.7 부터 artifact.content_type 은 **서버가 판정한** 값이다(첫 바이트 + 확장자,
--    artifacts.DetectContentType). 이 줄 이전 행의 content_type 은 올린 쪽이 보낸 값
--    (CLI 는 모르는 확장자를 application/octet-stream 으로, 브라우저는 OS 추측값으로)이라
--    믿을 수 없다. 판정했는지를 이 칸이 말한다.
--
--    기존 행은 여기서 다시 판정하지 않는다: 본문은 large object 라 SQL 로 첫 바이트를 읽어
--    Go 의 판정표(http.DetectContentType)와 같은 답을 내려면 판정표를 SQL 로 다시 써야 하고,
--    두 벌이 된 규칙은 언젠가 갈라진다. 대신 **읽을 때 한 번** Go 가 판정해 이 칸을 true 로
--    바꾼다(artifacts.Service.judgeLegacy — Get·List 가 부른다). 한 번 판정된 행은 다시 읽지
--    않는다. 판정 전 행은 미리보기 대상이 아니다(Previewable 은 판정된 값만 본다).
--
-- 2) message_attachment
--    메시지가 가리키는 첨부(openapi MessageCreate.attachment_ids → Message.attachments).
--    artifact_id 는 **그 버전의 행**이다 — 같은 이름이 나중에 v2 가 되어도 이 메시지의
--    첨부는 게시 때의 버전 그대로(Message.attachments 「버전 그대로」). position 은 보낸 순서.
--    같은 방 검증은 쓰는 쪽(router)이 한다(422 attachment_not_in_room) — 방이 지워지면
--    메시지·아티팩트가 함께 지워지므로 CASCADE 둘 다 방 안에서만 일어난다.

ALTER TABLE artifact ADD COLUMN content_type_judged boolean NOT NULL DEFAULT false;

CREATE TABLE message_attachment (
    message_id  uuid    NOT NULL REFERENCES message(id) ON DELETE CASCADE,
    artifact_id uuid    NOT NULL REFERENCES artifact(id) ON DELETE CASCADE,
    position    integer NOT NULL CHECK (position >= 0 AND position < 10),
    PRIMARY KEY (message_id, artifact_id),
    UNIQUE (message_id, position)
);

CREATE INDEX message_attachment_artifact ON message_attachment (artifact_id);
