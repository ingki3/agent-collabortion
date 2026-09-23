# R1b 인계 — room ⋈ work 가 1:1 이 아니게 될 때 틀어지는 자리

출처: PR #286(T-R1a) Hermes 리뷰 §2·§7(2026-09-23). R1a 는 방당 미션 1개(`work_room_single` 제약)를 전제로 SQL 을 치환했다. R1b 에서 한 방에 미션이 여럿이 되는 순간 아래 자리가 틀어진다 — **`work_room_single` 을 지우는 PR 은 이 목록을 전부 닫아야 한다.**

## 우선 처리

- **`workdirs/sweep.go:118` `JOIN work wk`** — GC 는 파일을 지우므로 가장 먼저(리뷰 NN2).
- **`budget.go:65`** — 틀린 예산으로 턴을 조용히 거부한다(리뷰 NN1).
- `e2e/p5/87_migrate_0025.sh` 의 `setsid` — macOS 에 없어 로컬 재현 불가(리뷰 NN3).

## 2. (2) R1b 에서 틀어질 자리 — **42곳, 목록**

`JOIN work wk ON wk.room_id = s.id` 는 **`work_room_single`(방당 미션 1) UNIQUE 위에 서 있다**. 워커도 두 곳에 주석으로 적었다(`sessions/room.go:18`, `workdirs/sweep.go:52`).

**DB 로 실증했다** — 그 UNIQUE 인덱스를 떼고 같은 방에 미션 2개를 넣으니:

```
-- 미션 1개: /tmp/wd1 → 1행
-- 미션 2개: /tmp/wd1 → 2행    ← 같은 workdir 가 중복 판정된다
-- 인덱스 복원 후: 1행         ← 되돌림 확인
```

R1b 가 이 제약을 지우는 순간 **조용히 값이 틀어지는** 유형이 있다. 비테스트 42곳을 분류했다.

**(a) 집계 — 값이 미션 수만큼 불어난다 (8곳, 가장 위험)**
```
internal/metrics/metrics.go:198, :207, :229
internal/auth/members.go:196
internal/runtimes/offline.go:141
internal/runtimes/runtimes.go:350
internal/httpapi/budget.go:65          ← 예산이 두 배로 세어지면 턴이 잘못 막힌다
internal/queue/postgres.go:94
```
이 유형은 **에러 없이 틀린 답**을 낸다. `budget.go` 는 사람 눈에 안 보이게 턴을 거부할 수 있다.

**(b) `FOR UPDATE` — 잠그는 행이 늘어난다 (10곳)**
```
internal/artifacts/artifacts.go:106 · httpapi/handlers_sessions_p3.go:99,157,335,487
httpapi/handlers_hitl.go:420 · sessions/delete.go:84 · sessions/complete.go:86
router/service.go:116 · router/delegate.go:65
```
`FOR UPDATE OF s, wk` 가 방의 **모든** 미션 행을 잠가 교착·대기가 는다.

**(c) 단건 조회 — 임의의 한 미션을 집는다 (16곳)**
```
httpapi/handlers_sessions_p3.go:39,680 · handlers_hitl_p3.go:42 · principal.go:206
handlers_hitl.go:130 · handlers_p4.go:190 · handlers_sessions_p5.go:35 · handlers_lanes.go:30
sessions/sessions.go:417,570,710 · workdirs/api.go:77 · queue/bundle.go:57
router/preview.go:36 · router/delegate.go:57 · router/status.go:61
```
`QueryRow` 라 **ORDER BY 없이 아무 행이나** 온다 — 어느 미션의 Director·autonomy 인지 비결정적이 된다.

**(d) 목록/기타 (8곳)** — `tasks/fallback.go:114`, `runtimes/offline.go:807`, `runtimes/runtimes.go:382`, `httpapi/handlers_inbox.go:69`, `handlers_p3_agent_cost.go:150`, `workdirs/sweep.go:118`, `workdirs/api.go:108,207`.

**의미가 바뀐 치환은 발견하지 못했다.** `firstLine()`(`sessions/room.go:22`)이 SQL 의 `split_part(goal, E'\n', 1)` 과 **같은 절단**이라 신규 생성 방과 이관된 방의 설명이 같은 규칙을 따른다 — 이관본과 신규본이 갈리는 흔한 함정을 피했다.

---


---

## R1b1(T-R1b1) 이 남기는 것 — R1b2·R1b3 가 받을 것 (2026-09-23)

- **임시 호환 규칙 「legacy single-work room」**(`router.legacySingleWorkRoom`, Lead Q1 승인) — FR-3.1.1 규칙 3 과 4 사이에 "방에 미션이 **정확히 하나**면(상태 무관) 그 미션" 을 끼워 둔다. 옛 `/sessions/*` 클라이언트가 `work_id` 없이 게시해도 세션=미션 동작(멈춘·완료된 세션의 메시지가 dispatch 되지 않음, 브리프·비용 귀속)이 유지된다. preview 의 `work_source` 는 계약 enum 이 닫혀 있어 `chosen` 으로 싣는다. **R1b2 가 방에 두 번째 미션을 열 수 있게 하는 순간 이 전제를 "옛 경로(createSession · 0025 이관)로 만든 방" 으로 좁혀야 한다** — 같은 함수가 `routingAssignee`(규칙 6 의 assignee)와 `loadHitlRow`·`loadHitlSession`(미션 없는 시스템 요청의 Director)의 폴백이기도 하다.
- **방 멈춤의 미러**(`roomgate`, Lead Q2 승인) — `budget`·`loop` 로 방이 막히면 그 방의 active 미션을 같은 사유로 `paused` 에 두고 `work.paused_detail.room_blocked = true` 표식을 단다(옛 Session 모양 유지). 해제는 표식 있는 것만 되살린다. **`loop` 는 계약 `WorkPauseReason` 에 없다 — R1b2 의 Work 응답은 표식 있는 paused 를 방 사유로 투영해야 한다**(`paused_reason: loop` 를 미션에 싣지 말 것).
- **아직 `work_id` 를 쓰지 않는 새 행**: `artifact`·`decision`·대부분의 `inbox_item`·종료 조건 승인 HITL(`sessions/complete.go` — `loadHitlRow` 가 호환 규칙으로 메운다). R1b1 마이그레이션(`*_r1b1_room_gate.sql`)이 R1b1 이전 행은 전부 메웠다.
- **미션 시간 상한**: 기존 강제 경로가 없어(P3 이후 `time_extension` 501) R1b1 도 만들지 않았다 — 예산만 미션·방 두 층이다.
- 위 42곳 중 R1b1 이 1:N 에서도 옳게 고친 줄은 PR 본문 체크리스트에 있다. 남은 줄은 그대로 R1b2·R1b3 몫이다.
