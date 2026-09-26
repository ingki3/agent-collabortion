-- r2_inbox_actor — 받은 요청 카드의 「누가」·「무슨 말로」 (openapi 0.2.10 InboxItem.card.actor_name · quote, T-S-inbox)
--
-- 번호는 PR 을 올리는 순간 origin/dev 의 마지막 + 1 로 이름만 바뀐다(Lead 규칙) —
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않는다.
--
-- isolation_confirm 은 첫 턴을 일으킨 사람과 그 트리거 메시지, room_invited 는 초대한
-- 사람. 둘 다 항목을 만드는 순간에만 알 수 있다 — 초대한 사람은 어디에도 남지 않고,
-- 격리 질문이 나간 뒤 방의 첫 task 는 바뀔 수 있다. 이름·본문은 읽을 때 조인한다
-- (사람이 이름을 바꾸면 카드도 따라간다). 메시지가 지워지면 인용만 사라진다.
ALTER TABLE inbox_item ADD COLUMN actor_user_id uuid REFERENCES app_user(id) ON DELETE SET NULL;
ALTER TABLE inbox_item ADD COLUMN quote_message_id uuid REFERENCES message(id) ON DELETE SET NULL;
