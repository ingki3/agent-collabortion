-- 0022_p5_hop_cause.sql — chain_depth 를 인과 사슬 깊이로 (T-S16: S-78)
--
-- 0006 의 session_hop 은 chain_depth 를 "마지막 사람 hop 이후의 행 수" 로
-- 셌다. PR #213(S-76) 이 위임과 합류 통보도 hop 으로 넣자 형제 위임(Lead→A,
-- Lead→B, Lead→C)과 "다 끝났습니다" 통보까지 전부 깊이가 되어, F1 형 세션
-- (위임 3 · 합류 · 멘션 · 재진입 · 위임 · 합류)이 사람 메시지 한 번 뒤 8번째
-- hop 에서 paused(loop) 가 났다(Hermes 실측 chainDepth 9). PRD FR-3.5 의
-- 깊이는 "사람의 메시지에서 시작해 멘션이 연쇄된 **깊이**" 고 Lead → 실무자
-- → 리뷰어 → Lead 가 4 다 — 폭이 아니라 깊이다.
--
-- 그래서 hop 마다 **원인 hop** 을 적는다: 이 hop 을 일으킨 턴(트리거 메시지를
-- 쓴 task)을 깨운 hop 의 id. 깊이 = 원인의 깊이 + 1, 사람이 쓴 hop 은 1.
--   · 형제 위임은 원인이 같아 같은 깊이
--   · 합류·질문·재진입 통보로 깨어나는 위임자(요청자)는 원인을 자기 task 의
--     원인으로 받아 **자기 깊이**로 돌아온다(자식보다 1 작다)
--   · NULL = 원인 없음(사람이 썼거나, 재개가 hop 을 지웠거나, 원인 hop 이
--     없는 task) — 셈은 router/loop.go chainDepth 가 한다
ALTER TABLE session_hop ADD COLUMN IF NOT EXISTS cause_hop_id bigint;

COMMENT ON COLUMN session_hop.cause_hop_id IS
  'S-78: 이 hop 을 일으킨 턴을 깨운 hop 의 id(같은 세션). chain_depth = 원인의 깊이 + 1, 사람 hop 은 1. NULL = 원인 없음.';
