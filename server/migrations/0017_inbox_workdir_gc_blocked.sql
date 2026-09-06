-- 0017_inbox_workdir_gc_blocked.sql — 인박스 항목 `workdir_gc_blocked` (T-S9 ask 1, Lead 결정)
--
-- FR-6.4 E13-12·13 은 "삭제하지 않고 **Director 에게 알린다**" 인데
-- `inbox_item_type` 에 맞는 값이 없었다. 계약 `InboxItemType` 에도 같은 값이
-- 추가된다(Lead 계약 PR).
--
-- 이 파일에 이 문장 하나만 있는 이유는 0013 과 같다: 마이그레이션은 파일 하나가
-- 한 트랜잭션이고, 새 enum 값은 그것을 추가한 트랜잭션 안에서 쓸 수 없다.
-- 값을 쓰는 쪽은 코드(workdirs.Service.notifyBlocked)다.
ALTER TYPE inbox_item_type ADD VALUE IF NOT EXISTS 'workdir_gc_blocked';
