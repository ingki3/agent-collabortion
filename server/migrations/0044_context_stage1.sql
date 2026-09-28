-- 맥락 1단계 — 재개 델타의 기준점 · 데몬 기능 광고 (T-CTX1, harness v0.9.14 · daemon-protocol v0.10.3)
--
-- 번호는 머지 순서로 정해진다(Lead 규칙, 0043 머리와 같다) — #380(0041) → #375(0042) →
-- #382(0043) 까지가 dev 다. **Lead 정정 2026-09-28: T-QUIET(#390)이 먼저 머지되어 0044 를
-- 쓰고 이 PR 은 0045 다.** 파일 이름은 #390 이 dev 에 들어온 뒤 origin/dev 를 병합할 때
-- 0045 로 바꾼다 — 지금 미리 바꾸면 0044 가 빈 자리가 되어 db_test 의 연속성 검사가
-- 깨진다(0045 follows 0043). 최종 순서(0044 approval_quiet → 0045 context_stage1)로
-- 마이그레이션·queue·quiet 테스트가 함께 초록인 것은 #390 브랜치를 병합해 확인했다.
-- 이 파일 안이나 코드 어디에서도 번호를 부르지 않으므로 개명은 이름만 바꾸는 일이다.
--
-- ② 재개 턴 델타(harness §10): 서버는 번들을 지을 때마다 그 attempt 의 기준점 — 그 순간
-- 방의 가장 늦은 메시지(created_at, id) — 을 task_attempt 에 적는다. finish 가 lane 의
-- runtime_session_ref 를 덮을 때 같은 attempt 의 기준점을 lane 에 함께 옮긴다: lane 에
-- 있는 ref 와 기준점은 늘 「그 세션에 마지막으로 턴 프롬프트를 보낸 attempt」 한 쌍이다.
-- 기준점 메시지가 나중에 지워져도 시각이 남아 있어 비교가 성립한다(그래서 FK 가 없다).
-- 기준점이 없는 ref(이 마이그레이션 전 attempt 가 쓴 것)는 델타를 짓지 않는다.
--
-- daemon-protocol §3 v0.10.3: probe 최상위 daemon_features. 마지막 probe 의 값을 그대로
-- 둔다(비면 「아는 것 없음」 — 서버는 그 런타임에 prompt_cold 에 기대는 델타를 보내지 않는다).
ALTER TABLE task_attempt
    ADD COLUMN context_anchor_message_id uuid,
    ADD COLUMN context_anchor_at timestamptz;

ALTER TABLE lane
    ADD COLUMN context_anchor_message_id uuid,
    ADD COLUMN context_anchor_at timestamptz;

ALTER TABLE runtime
    ADD COLUMN daemon_features text[] NOT NULL DEFAULT '{}';
