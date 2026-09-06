# P3 작업 분해 — HITL · 재개 · 예산 (G5 → G6)

| 항목 | 내용 |
|---|---|
| 상태 | **P3b 진행(2026-09-07).** 스파이크 4c 통과(#117). T-P3a 골든 #108·#113·sim 대역 #120. **T-D5 데몬 PR #121 머지**(Hermes APPROVE: §5 취소 절차·§4.4 예산·재개 D-11~13·gc v0.7·acpfake 공개·골든 이월 6케이스 diff 0). 계약: #110·#116·#119(daemon-protocol v0.7)·#122(harness v0.8.4 refusal 예외)·#123(colab-cli v0.5 choice·request-info v1). **T-S5(서버) 실측 검증 중 ‖ T-C4(CLI) 구현 중**. 다음: T-W3 → T-I3 |
| 근거 | `PLAN.md` §3 P3·§6.2 G6·§8(열린 결정 5·6), `EVAL.md` E7~E10·E8-13·E16-C, `PRD.md` FR-5.1~5.4·FR-7.3·FR-3.4·FR-1.9·§8.2.2·§8.4, `contracts/harness.md` §5·§6, `contracts/daemon-protocol.md` §4.3·§6, `plan/P2_BACKLOG.md` |
| 목표 | **사람이 개입하는 모든 경로.** 세션이 멈추고 다시 이어지되 **작업을 잃지 않는다.** 시나리오 C(Director 중간 개입)·D(프로파일 전환 — G5에서 이미 통과, G6에서 재확인) |
| 게이트 | **G6**: 시나리오 C·D + **중복 0** + (G4에서 이월된 Hermes 조건은 G5에서 닫힘). **컷 3 판정** — 미통과면 P4를 시작하지 않고 P3를 마감(PLAN §6.2) |
| 예산 (PLAN §6.2) | `blocked` 15 / PR 40. PR당 리뷰 반려 상한 3 |
| 동시성 | worker 동시 2개, `--agent claude --model opus`(골든 작성은 `--agent hermes`) |

---

## 0. 공통 규칙

`plan/P2_TASKS.md §0` 1~10을 그대로 적용한다(브랜치 기준 origin/dev, `contracts/` 수정 금지 → `ask`, 생성물 같은 PR, X-2 픽스처 함정, 한도 대응, worker_done 형식, 골든 어댑터 예외 §0-8, 웹 리뷰 체크 §0-9, 프로세스 종료는 pid·포트만 §0-10). P2에서 더해진 것:

11. **결함 ID는 `plan/P2_BACKLOG.md`가 SSOT다.** 보고서·PR에서 새 결함을 번호 매기지 말고 "신규(스트림, 내용)"로 적어라 — Lead가 번호를 준다. G5에서 보고서 번호와 백로그 번호가 충돌해 재번호했다.
12. **회귀 주입은 빌드를 깨지 않게** — 줄을 지우면 변수가 미사용이 돼 증명이 안 된다. `return false && <원래 식>`·`_ = x` 형태로(PR #103 레시피).
13. **실서버 검증은 격리 스택 2개** — 테스트 DB(전수 `go test`가 스키마를 드롭한다)와 실서버용 DB·포트를 따로. 다른 워커 스택이 같은 머신에 떠 있다.
14. **일반 유닛 테스트 기대값은 결함 수정으로 바뀔 수 있다**(골든과 다르다). 바꿨으면 PR 본문에 "e2e도 같은 것을 재고 있으면 고쳐라"를 적어 통합 워커에게 넘긴다.
15. **계약 `x-phase`는 `P3 + # 주석`** — 문자열 값이면 web 생성 타입 CI가 깨진다.

---

## 1. P3-pre — 착수 전 두 갈래 (동시)

### T-D4 · 스파이크 4c — 콜드 스타트 정성 평가 (PLAN §4 4c, P3 착수 전 게이트)

```
작업: 스파이크 4c — 브리프 + 히스토리 + 결정 기록만으로 콜드 스타트한 에이전트가 작업을 이어가는가
스트림: D / 브랜치 spike/4c-cold-start (origin/dev)
입력: PRD §8.4(턴 프롬프트: <resumed> 구간, "이미 게시한 메시지 목록", workdir 상태 확인 지시),
      FR-5.4, contracts/harness.md §6(재개·resume_rejected → cold_start), EVAL E8-02·E8-03·E8-04,
      plan/spikes/ 의 이전 스파이크 보고 형식(SPIKE_01b.md 등), e2e/p2/lib.sh(스택 레시피)
방법: 실제 런타임(Claude Code haiku, Hermes)으로 격리 none 세션 — 저장소 밖 무해한 과제(X-2).
  (1) attempt 1: 에이전트가 파일 2개를 절반 편집하고 메시지 2개를 게시한 뒤 데몬을 pid 로 kill.
  (2) 서버가 재큐잉 → attempt 2. 두 경로를 각각 5회: (a) resume 성공(runtime_session_ref 있음),
      (b) 강제 콜드 스타트(lane 의 ref 를 비우거나 Hermes state.db 제거 → E8-03).
  (3) 판정: 이어갔는가(작업 완료 여부·같은 파일을 이어서 편집했는가), 같은 메시지 재게시 0,
      같은 편집 중복 적용 0, "workdir 상태 먼저 확인" 지시를 따랐는가(task_event 로).
  콜드 스타트 프롬프트에 무엇이 부족했는지(히스토리 상한 E8-12, 결정 기록, 직전 활동 피드 요약)를
  정성으로 적는다 — PLAN 이 정한 폴백은 "직전 활동 피드 요약 주입"이고 그것은 §8.4 계약 변경 PR 이다.
출력: plan/spikes/SPIKE_04c.md(판정 표 + 프롬프트 샘플 + 부족 항목) — PR 하나. 코드 변경 없음.
      데몬 코드에 손대야 재현이 되면 그 diff 는 별도 브랜치·별도 PR 로 분리하고 보고서에 근거만.
DoD: (a)·(b) 각 5회 표. "통과/실패 + 권고(계약 변경 필요 여부)" 한 줄 결론. Lead 가 계약 변경 여부를 결정한다.
금지: contracts/·server/·cli/·web/ 수정. 저장소 파일명을 세션 goal 에 쓰지 않기.
```

### T-P3a · 골든·시뮬레이터 먼저 — Reviewer(Hermes)가 쓴다

P2a와 같은 이유(PLAN §10.5, `P2_TASKS.md §1`): 결정적 로직은 구현자가 테스트를 같이 쓰면 자기 해석을 복사한다.

```
작업: P3a — HITL·재개·예산·취소의 골든 테이블 + 부분 실행 시뮬레이터
역할: Reviewer(Hermes). 구현하지 않는다. 테스트만 쓴다.
브랜치: test/p3-golden (origin/dev 기준)
입력: PRD FR-5.1·5.2·5.4(HITL 타입·동작·재개 모델), FR-7.3(예산), FR-3.4(취소·재지시), FR-1.9(킬 스위치),
      §8.2.2(취소 절차), §8.4(턴 프롬프트·<resumed>), FR-2.2(시스템 발행 HITL),
      EVAL E7(19행)·E8(13행)·E9(7행)·E10(13행), contracts/openapi.yaml(x-phase P3 op 전부 —
      respondHitlRequest 나머지·listInbox·pauseSession/resumeSession·restartTask·cancelTask·
      updateBudgetOverride 등 이름은 계약을 따른다), contracts/daemon-protocol.md §4.3 명령,
      server/internal/{router,tasks,sessions,hitl} 현재 구조, P2 골든의 어댑터 관례(§0-8)
출력: PR 하나, 빌드 태그 //go:build p3golden
  - server/internal/hitl/hitl_golden_test.go — E7 전부(타입별 default 필수, task 당 1개, turn_end 전이,
    approver_spec·deputy 12h·overdue·autonomy 별 24h 처리, 거절 재개, fail-closed).
  - server/internal/tasks/resume_golden_test.go — E8-01·02·06·07·08·09·10·11·12·13
    (재개=새 attempt·resume 우선·콜드 스타트 프롬프트 구성·재지시=새 task·재시도=같은 task).
  - server/internal/sessions/budget_golden_test.go — E9 전부(paused(budget)·override·거절 유지·
    세션 잔여·추정치 하드 컷 없음·4단위 집계).
  - server/internal/tasks/cancel_golden_test.go — E10 전부(편집 완료 대기 30초·권한 응답 순서·
    failed(cancelled)·권한·deputy 즉시·킬 스위치 3상태·originator 권한).
  - **부분 실행 시뮬레이터** test/sim/partial_exec_test.go(위치는 서버 모듈 안, 태그 같음) —
    E8-04·05: "편집 N + 게시 M 후 강제 종료 → 재큐잉 → 재개" 를 acpfake 로 100회, 중복 메시지 0·중복 편집 0 을
    task_id+seq 멱등키와 프롬프트 지시로 세는 하네스. 구현이 붙기 전에는 의도한 실패.
  - PR 본문에 EVAL 행 ↔ 테스트 함수 대응표. EVAL 에 없는 케이스는 EVAL 제안 행으로.
DoD: go vet -tags p3golden ./... && go test -tags p3golden ./... 로 의도한 실패만(컴파일 통과).
     태그 없이 go test ./... 초록.
금지: 구현 코드 수정, contracts/ 수정.
```

---

## 2. P3b — 구현 (동시 2개, S → D → C → W 순)

### T-S5 · 서버: HITL 전체 · 재개·재시도 · paused 5종 · 예산 강제 · 취소·재지시

```
작업: P3 서버 — x-phase P3 op 전부 + 골든 E7~E10 통과
스트림: S / 브랜치 feat/server-p3
입력: test/p3-golden(먼저 읽어라 — 이것이 명세다), PRD FR-5.1~5.4·FR-7.3·FR-3.4·FR-1.9·§8.2.2·§8.4,
      openapi x-phase P3 op, daemon-protocol §4.3·§6, EVAL E7~E10·E8-13
출력: PR 하나 이상(골든의 p3golden 태그를 자기 범위만큼 걷어낸다)
  - HITL: 툴 호출 → pending_hitl → turn_end 에 waiting_human, task 당 1개, 타임라인 카드 게시,
    인박스 Needs Action 질의 + 심각도 7종(listInbox), deputy(기한 절반 후 응답·취소는 즉시), overdue,
    autonomy 별 24h 처리(question 자동 답·approval 절대 자동 진행 없음), respondHitlRequest 나머지
    (에이전트 발행 응답 → 재큐잉 새 attempt, 예산 상향 → task.budget_override, 킬 스위치 disabled 보류).
    P2 의 user_approval 경로(#103)는 유지.
  - 재개 = 새 attempt: lane.runtime_session_ref resume 우선 → resume_rejected 보고 시 콜드 스타트 프롬프트
    (브리프 + 히스토리 상한 E8-12 + 결정 기록 + "이미 게시한 메시지 목록" + workdir 확인 지시).
    스파이크 4c 결론이 "활동 피드 요약 주입"이면 계약 PR 뒤에 반영.
  - 재시도 공통 경로(같은 task·attempt+1·같은 workdir), 재지시 = 새 task(restarted_from_task_id, 새 지시만).
  - 세션 paused 사유 5종 + 재개(드레인 vs 취소), paused 중 queued dispatch 금지.
  - 예산: usage 누적 → 턴 중 초과 시 §8.2.2 취소 명령 → task paused(budget) + 시스템 HITL(task_id 채움),
    세션 잔여 예산, 추정치 런타임은 하드 컷 대신 paused + 드레인, override 후 같은 lane·workdir 재개.
  - 취소: 편집 완료 대기 30초, 권한 응답 → session/cancel → 드레인 순서(명령으로 데몬에), lane failed(cancelled),
    권한(멤버 403·deputy 즉시), 킬 스위치 3상태(E10-07~09), originator 권한(E10-12).
  - 백로그 흡수: **S-35**(daemon_command.delivered_at 기록 — 명령이 응답에 실릴 때), **S-32·S-33·S-34**,
    **S-21·S-22·S-23**(비용), **K-4**(자동 발행 user_approval 의 취소 조건 — Lead 결정: 조건이 다시 미충족이 되면
    open HITL 을 `cancelled` 로 닫고 인박스에서 내린다; 계약 문언은 Lead 가 PR 로), S-17·S-18·S-19.
DoD: 골든 E7~E10 초록 + 시뮬레이터 100회 중복 0 + 기존 골든·e2e/p1/07 회귀 0 + CI 초록.
     PR 본문에 op ↔ 코드 줄 전수 대조표, 회귀 주입 결과.
금지: contracts/·daemon/·cli/·web/ 수정, 0001~0012 수정(0013부터).
```

### T-D5 · 데몬: 예산 취소 · resume 거부 감지 · `<resumed>` 처리 · gc 핸들러

```
작업: P3 데몬 — usage 누적·예산 초과 §8.2.2 취소, resume_rejected 보고, 취소 명령 순서, gc 핸들러
스트림: D / 브랜치 feat/daemon-p3
입력: harness.md §5(취소 절차)·§6(재개), daemon-protocol §4.3(명령)·§6(GC), PRD §8.2.2·§8.2.5, EVAL E8-01~03·E8-11,
      E9-01·E9-04·E9-06, E10-01~03·E10-13, 스파이크 4c 보고
출력: PR 하나
  - usage_update 누적 → 서버가 낸 cancel(budget) 명령 수신 시 §8.2.2 절차(편집 완료 대기 30초 → 권한 응답 →
    session/cancel → 드레인) — E10-01~03 과 같은 코드 경로.
  - resume: session/load 결과 null·provenance 불일치 → resume_rejected 보고 + cold_start 이벤트(E8-02·03),
    preparing 재개 시 workdir 재사용(E8-11). CLI 폴백 경로면 --max-budget-usd 동봉(E9-06).
  - gc 명령 핸들러(**D-4** 의 GC 부분): workdir_ids 삭제 → §6 보고(행 deleted). worktree 격리는 P4.
  - 백로그 흡수: **D-8**(cliRe 플래그), **D-2**(probe 실측), **D-5**(supported_options 표).
DoD: acpfake 로 E8·E9·E10 데몬 행 초록, 실기 스모크 1회(Claude Code) 로그 첨부, CI 초록.
금지: contracts/·server/·cli/·web/ 수정.
```

### T-C4 · CLI: `hitl ask` · `approve-request` · `request-info`

```
작업: colab hitl ask --question --default / --choices, approve-request --summary, request-info — "턴을 끝내라" 반환
스트림: C / 브랜치 feat/cli-p3
입력: contracts/colab-cli.md(HITL 절), openapi createHitlRequest, EVAL E7-01·04·05·06, harness §10(cli_wrapper 에서도 동작)
출력: PR 하나 — 등록 성공 시 "등록됨, 이 턴을 끝내라" + turn_end_required, 두 번째 요청은 서버 거부 메시지 그대로,
      question·choice 는 default 필수(클라이언트에서도 검사), MCP 도구 표면(colab_hitl_*)과 동일 동작. C-3(버전 ldflags) 흡수.
DoD: 유닛 + e2e 에서 Hermes(cli_wrapper)·Claude Code(mcp) 양쪽으로 등록 1회.
```

### T-W3 · 웹: S8 인박스 · HITL 카드 · S7 상단 액션 · lane 카드 액션 · paused 배너 · 권한 변형

```
작업: P3 웹 — S8 인박스 전체(7종 + deputy 노출 시점), HITL 카드(타임라인·인박스 공용 하위 컴포넌트 — PLAN §8 결정 6),
      S7 상단 액션(일시정지·재개·종료·참여자·Director 교체), lane 카드 "중단하고 다시 지시"/"중단", paused 배너 5종,
      S7-P·S7-D 변형(권한 비활성 + 툴팁), task 이력 펼침
스트림: W / 브랜치 feat/web-p3
입력: SCREEN S7·S8·§8.2(Q2·Q3·Q5·Q6 — Lead 결정: 표의 권고안 그대로. v1 세션 삭제 없음·보관됨 칩·푸시 v2·데스크톱 우선 + 인박스 응답만 모바일),
      COMPONENTS §2.3·2.4, EVAL_USER(해당 여정), openapi P3 op, mock 규칙 §0-9
출력: PR 하나 이상. mock 은 골든 E7 값과 기계 대조(§0-9). W-5 흡수(mock 규칙 vitest 가드).
DoD: typecheck·vitest 초록, p3-mock 스모크, 스크린샷 4장(인박스·HITL 카드·paused 배너·S7-D).
금지: 서버·계약 수정. 계약 빈칸은 ask.
```

---

## 3. T-I3 · 통합 — G6 판정 자료

```
작업: 시나리오 C(Director 개입) · D(재확인) · HITL 왕복 · 중복 0 · 예산 · deputy → plan/G6_REPORT.md
스트림: Integrator / 브랜치 test/g6-scenarios
입력: EVAL E16-C·D, E7-18, E8-04·05(시뮬레이터는 CI, 여기서는 실기 1회), E9-01~03, E7-09·10(clock 주입), P3 hotfix 전부
출력: e2e/p3/*.sh + 보고서. 수치는 DB 단일 클럭. 결함은 스트림 귀속으로 "신규" 표기(번호는 Lead).
DoD(PLAN §3 P3): HITL 왕복(턴 종료·슬롯 미점유·답변·새 attempt·resume 기억 / 콜드 스타트 이어감), 중복 0(실기 1회 + CI sim 100회),
     예산 paused(budget) → 상향 → 같은 lane·workdir 재개 + budget_per_task 불변, deputy 12h 전 비활성 + "HH:MM부터"·취소 즉시,
     시나리오 C: Director 메시지가 실행 중 턴을 절대 죽이지 않음, 시나리오 A 의 user_approval 이 정식 HITL 카드로.
```

G6 판정은 `plan/G6_DECISION.md`(Lead). **컷 3**: 미통과면 P4를 열지 않는다.

---

## 4. 순서와 의존

1. **지금**: T-D4(스파이크 4c) ‖ T-P3a(골든·시뮬레이터). 4c 결론이 계약 변경을 요구하면 Lead가 §8.4 계약 PR(Director 게이트 1회 — 위임 하에 Lead 결정·기록).
2. T-P3a 머지 후: T-S5 ‖ T-D5 → T-C4 ‖ T-W3.
3. T-I3 → hotfix 라운드 → G6.
4. 병행 결정: Reviewer의 colab Hermes 프로파일 전환(PLAN §10.1 "G5 후")은 **T-I3 때 시범**(리뷰 1건을 colab 세션으로) — Orca 리뷰 흐름은 유지.
