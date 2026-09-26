-- 0026_v19_room_read.sql — 다른 방 읽기 분량 상한 (PRD v0.19 FR-4.5 · T-R1c)
--
-- FR-4.5 「양·비용」: 한 턴에 읽을 수 있는 방 수·분량에 상한 — 워크스페이스 설정
-- `room_read.max_rooms_per_turn`(기본 3)·`max_tokens`(기본 4,000). 계약
-- WorkspaceSettings.room_read(RoomReadPolicy)가 이 칸이다. 다른 jsonb 설정 묶음과
-- 같은 모양으로 두어 PATCH 가 키 단위 `||` 병합을 그대로 쓴다.
ALTER TABLE workspace_settings
    ADD COLUMN room_read jsonb NOT NULL DEFAULT '{"max_rooms_per_turn": 3, "max_tokens": 4000}';
