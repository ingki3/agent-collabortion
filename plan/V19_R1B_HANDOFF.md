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

## R1 이후 남은 후속 (2026-09-24)

- ~~**#304 NN1**~~ **닫힘(#309)** — `TestWorktreeFirstClaimRace` 가 이중 방어를 둘 다 떼어도 초록(패자의 UPDATE 가 READ COMMITTED 재평가로 0행이 되어 스스로 물러남). 구현은 #302 리뷰 프로브로 안전 확인됨 — 자물쇠만 #302 리뷰 프로브(p2Fixture + runtime 복제 + SettleFill 앞 지연)로 교체할 것.
- ~~**#304 범위 밖**~~ **닫힘(#309)** — `decision.auto` 열이 있는데 `DecisionAPI` 가 싣지 않는다(보고만).
- **#303 NN1** — 종료 조건 편집기 어휘 「보고서 제출」·「담당 에이전트」 vs SCREEN 「아티팩트 제출」·「제출자」 → R1.5 문구 라운드.
- **#309 NN1** — 받은 요청 항목이, 사람이 invited 방에서 내보내진 **뒤에도** 방 이름을 싣는다(`InboxItem.room` + `room_invited` 카드 본문). `listInbox` 에서 읽는 순간 `Decide(ActView)` 로 걸러 `room: null`·본문 이름 가림(FR-4.5 「읽는 시점에 다시 검사」와 같은 취지).
- **#309 NN2** — `TestR2WorktreeFirstClaimRace` 주석 "둘 다 뗀 변조를 잡는다"가 사실과 다르다(방어가 셋 — 셋을 다 떼야 잡힘). 주석 정정 또는 이음새를 FillWorktree UPDATE 직후·커밋 전으로.
- **계약 문장** — setRoomSubscription 권한은 **방 참여자만**(Lead 판정, 구독·안 읽음은 room_participant 행 단위). openapi 설명 「방을 볼 수 있는 사람」을 고칠 것.
- **#311 서버 발견 4건**(T-R2-W4a): ① 방 예산 멈춤이 `room_paused` 가 아니라 `hitl_request` 로 들어간다(budget.go:442 — 루프만 room_paused) ② room_paused·isolation_confirm 항목 `recipient_basis` 가 비어 있다 ③ inbox.Actions 가 open_workdirs·delete_workdir 를 낸다(계약 0.2.10 에서 enum 에 편입) ④ isolation_confirm 카드에 트리거한 사람·인용 칸이 없다(0.2.10 card.actor_name·quote). + #311 NN3 room_invited 초대한 사람(card.actor_name).
- **#311 NN1** — 새 메시지 직후 내비 안 읽음 합계(디바운스 reload)와 목록 카드(즉시 +1)가 잠깐 갈린다. **NN2** — SCREEN 필터 칩 「액션 필요」→「조치 필요」로 맞출 것(R1.5).
- **#310 NN1** — 「여기까지 정리」 직접 고르기가 타임라인에서 집는 방식이 아니라 다이얼로그 입력 두 칸.
- ~~#309 NN1·NN2, #311 ①~④·NN3, 계약 문장(방 구독)~~ **닫힘(#312·#313)**. ~~runtime_offline 방 게이트~~ **닫힘(#314·#315)**.
- ~~**#314 NN1** — `TestMayRebind` 에 `blockedAt == nil` 방장 케이스 한 줄. **NN2** — e2e 63_ 실데몬 회차 1회(단언만 바꿨고 안 돌렸다).~~ **닫힘(#324)** — 63_ 실기 결과는 #324 본문.
- ~~**#323 NN1** — `loadRoomBrief` 미션 SELECT 가 `wk.room_id` 를 안 본다(task.work_id 가 남의 방 미션이면 [4] 에 그려짐). **NN2** — [7] LIMIT 20 과 ③ OFFSET 20 에 보조 정렬이 없어 같은 created_at 결정이 경계에서 중복·누락.~~ **닫힘(#324)** — `AND wk.room_id = $2`, 두 쿼리 `created_at DESC, id DESC`.
- ~~**#322 NN1** — `roles.all` 을 따로 적은 슬라이스 대신 생성된 enum 에서.~~ **닫힘(#324)** — `scripts/gen_enum_values.sh` → `gen.ColabCommandValues`(openapi 순서 = colab-cli §2 문서 순서 = 웹 `lib/commands.ts` 순서). 서버가 내보내는 `allowed_commands` 순서가 바뀌었다: `artifact_get` 이 8번째 → 3번째(e2e 81~84 기대값 갱신). ~~83_ 실데몬~~ 결과는 #324 본문.
- **#324 발견** — ① 데몬 `daemon/internal/commands/commands.go` 의 `all` 이 아직 옛 순서(`artifact_get` 8번째)를 따로 적고 있다 — 서버와 같은 순서로(또는 번들 값을 그대로) 맞출 것. ② `task.work_id` 가 남의 방 미션이면 [4]·②는 이제 막히지만 번들 `task.work_id`·`COLAB_WORK_ID`·예산(`remainingBudget … t.work_id`)은 그 값을 그대로 쓴다(DB 변조 전제라 보고만). ③ e2e 83 D.4 의 CLI `--allow` 탐지식(`mcp serve --allow x --list`)이 낡아, 툴 목록에서 `colab_lane_delegate` 가 빠졌는데도 판정 대신 NOTE 로 떨어진다.
  - #324 리뷰 판정: ② 는 **서버 경로로 도달 불가**(task.work_id 쓰기 4곳 전수 — 메시지 귀속·위임 상속·openWork·legacy 미션 모두 같은 방) → 방어 심화 백로그 「task.work_id 무결성 CHECK 또는 claim 시 검증」. **NN2** — `gen.go` 의 `//go:generate` 에 `scripts/gen_enum_values.sh` 를 더해 CI 재생성 diff 로도 잡기(지금은 `TestAllCommandsIsTheClosedEnum` 한 겹).
- **E12-11 판정 대기(Director)** — [5] Roster `status:`(working/idle)가 매 턴 계산돼 [1]~[5] 바이트 동일이 roster status 도입(v0.9.0)부터 깨져 있다(#323 리뷰가 dev 에서 재현). 제안: status 를 [5] 에서 빼고 턴 프롬프트 한 줄(`<roster_status>`)로 — harness §10 계약 변경. 그때까지 자물쇠는 정규식으로 status 를 가리고 비교.
- ~~#317 NN1·NN2 · CI 10분 시간 초과~~ **닫힘(#318)** — 웹 자물쇠 여러 줄 JSX, 서버 자물쇠 상수 추적, go test -timeout 20m.
- **R4 로 미룸(Lead 판정 2026-09-24)**: 계약 description 속 「세션」 산문(openapi 약 86곳·daemon-protocol 23곳·colab-cli 6곳, #317 본문 목록). 대부분 R4 까지 별칭으로 사는 옛 `/sessions/*` op 의 설명이라 지금도 사실이고, 서버 테스트 일부가 계약 문장을 글자 단위로 읽는다 — 별칭을 지우는 R4 에서 op 과 함께 고친다.
- **백로그**: e2e 82 W3·W3b(S7 빈 턴 행, agent-browser 로컬 전용) 가 dev 에서도 68/2 — 기존 결함(#317 보고).
