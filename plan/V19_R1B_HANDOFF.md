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

