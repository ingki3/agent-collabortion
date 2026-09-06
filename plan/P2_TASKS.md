# P2 작업 분해 — 협업 코어 (G4 → G5)

| 항목 | 내용 |
|---|---|
| 상태 | **초안. Director의 G3 확인 뒤 fan-out한다**(`plan/G3_DECISION.md` §5) |
| 근거 | `PLAN.md` §3 P2·§6.2 G4·G5, `EVAL.md` E1~E6·E15, `EVAL_USER.md` U2·U4·U5·U8·U10·U11·U15, `plan/P2_BACKLOG.md`(P1 이월 전부) |
| 목표 | **시나리오 A(`none` 격리)가 8단계 끝까지 통과한다.** Lead가 3항목을 위임 → lane 3개 병렬 → 합류 **정확히 1회** → 종합 → Writer 초안 → `artifact_submitted` → 승인 → `completed` |
| 게이트 | **G4**(중간): P2a 골든 테스트 + 시나리오 A **단일 런타임** 통과 · **G5**: 시나리오 A 8단계 + Hermes + 템플릿 3분 Director 실측 |
| 예산 (PLAN §6.2) | G4 `blocked` 8 / PR 20 · G5 `blocked` 15 / PR 40(**가장 큰 단계**). PR당 리뷰 반려 상한 3 |
| 동시성 | **worker 동시 2개**(P1에서 4개가 5시간 한도를 20~30분에 소진). `worker-start … --agent claude --model opus` |

---

## 0. 공통 규칙 (모든 작업)

P1에서 반복해 밟은 함정이라 맨 앞에 둔다.

1. **브랜치**: Orca 워크트리 브랜치는 main 기준이다. 시작 전 `git fetch origin && git checkout -b <branch> origin/dev`. `contracts/`·`server/`가 없으면 갈아타지 않은 것이다.
2. **PR**: `GH_PROMPT_DISABLED=1 gh pr create --repo ingki3/agent-collabortion --base dev --head <branch> --title … --body-file …`.
3. **`contracts/` 수정 금지.** 계약이 모자라거나 틀렸으면 **임의 해석하지 말고** `orca orchestration ask`로 Lead에게. 계약 변경은 Director 승인 PR로만 들어간다 — P1에서 계약이 v0.1→v0.5로 다섯 번 바뀌었고 **전부 구현·리뷰가 드러낸 결함**이었다. 이것이 정상 경로다.
4. **생성물**: 계약이 바뀌면 `cd server && go generate ./internal/httpapi/gen`, `cd web && npm run gen:api`를 **같은 PR에서** 돌린다. CI `contracts` 잡이 드리프트를 막는다.
5. **E2E 픽스처 함정(X-2)**: 세션 `goal`·지시문에 이 저장소의 파일명·시나리오 이름을 쓰지 마라. 에이전트가 저장소에서 그 스크립트를 찾아 **스스로 실행**한다(2026-09-06 실측: 세션 11개 재귀 생성, 중첩 실행이 데몬을 죽임).
6. **한도**: `Fable limit`이면 터미널에 `/model opus` → 확인 `1`. `session limit`이면 리셋 시각 이후 재개. 어느 쪽이든 **지금까지 결과를 PR에 push하고** `worker_done --outcome failed`에 리셋 시각을 적어라 — 기다리지 마라.
7. **완료**: `orca orchestration send --type worker_done …`에 PR URL·테스트 결과·**계약 결함**·범위 밖으로 남긴 것.

---

## 1. P2a — 골든 테스트 먼저 (Reviewer/Hermes가 쓴다)

**왜 먼저인가.** 라우터 12규칙은 결정적이고 PRD에 표로 있다. 구현자가 테스트를 같이 쓰면 자기 해석을 테스트에 복사한다. P1에서 Hermes가 스트림별 구현의 계약 위반을 전부 잡아낸 것이 근거다(PR #18·#20·#21·#22 모두 1차 반려).

```
작업: P2a — 결정적 로직의 골든 테이블 테스트 작성
역할: Reviewer(Hermes). **구현하지 않는다.** 테스트만 쓴다.
브랜치: test/p2-golden (origin/dev 기준)
입력: PRD FR-3.3(규칙 1~8 + lane 해소 4규칙 + 병합), FR-3.4, FR-3.5(루프 상한 3종),
      FR-2.2(종료 조건 4종 AND/OR), FR-2.3·FR-6.2·FR-7.1(상태 머신), FR-1.3(파생 상태),
      EVAL.md E1(20행)·E2(13행)·E4(9행)·E5(18행)·E6(11행), server/internal/router·tasks 현재 구조
출력: PR 하나
  - server/internal/router/golden_test.go — E1·E2 전부. 표 하나에 (메시지 유형 × 작성자 × 멘션 ×
    스레드 위치 × lane 상태) → (트리거 대상, lane, 병합 여부). 규칙 우선순위(E1-19·20)와
    규칙 8의 억제 범위·기간(E1-15~17)을 반드시 포함.
  - server/internal/tasks/state_golden_test.go — E5 전부(세션 6·task 10·lane 7 상태, 불법 전이 거부,
    파생 상태 6단계 우선순위 E5-11~18).
  - server/internal/sessions/completion_golden_test.go — E6 전부(4종 AND/OR, 시스템 발행 HITL,
    agent_approval 단독, criteria_met 단독 거부).
  - server/internal/router/loop_golden_test.go — E4 전부(깊이·시간당·왕복, 리셋 조건, 내용 기반 억제 없음).
  **빌드 태그 `//go:build p2golden`** 를 달아 dev CI를 초록으로 유지한다. P2b의 각 작업이
  자기 범위의 태그를 걷어내며 초록으로 만든다(태그를 남긴 채 머지하는 것이 이 단계의 정상 상태다).
  - PR 본문에 EVAL 행 ↔ 테스트 함수 대응표. **EVAL에 없는 케이스를 발견하면 EVAL 제안 행으로** 적어라.
DoD: `cd server && go vet -tags p2golden ./... && go test -tags p2golden ./... 2>&1 | grep FAIL` 로
     **의도한 실패**만 나온다(컴파일은 통과). 태그 없이 `go test ./...`는 초록.
금지: 구현 코드 수정, contracts/ 수정.
```

**G4의 절반이 이 PR이다.** 나머지 절반은 시나리오 A 단일 런타임 통과(T-I2 1부).

---

## 2. P2b — 구현 (동시 2개씩, S → D → C → W 순으로 시작)

### T-S2 · 서버: 라우터 전체 · lane · 합류 · blocked · 종료 판정

```
작업: P2 서버 — FR-3.3 규칙 1~8 + lane 해소 4규칙 + 병합, lane/합류/blocked, 루프 상한, 종료 조건 판정, 동시성·권한 게이트, 결정 기록
스트림: S / 브랜치 feat/server-p2
입력: PRD FR-3.3·3.4·3.5·3.6, FR-6.1·6.2·6.2.1·6.3·6.5, FR-2.2·2.3, FR-1.9, FR-4.2,
      contracts/openapi.yaml(x-phase P2 operation 전부), contracts/daemon-protocol.md,
      test/p2-golden 브랜치의 골든 테스트(먼저 읽어라 — 이것이 명세다), EVAL E1~E6·E15-02·06
출력: PR 하나 (골든 테스트의 p2golden 태그를 이 범위만큼 걷어낸다)
  - 라우터 규칙 1·3·4·5·7·8 추가(2·6은 P1에 있음). 규칙 7의 5분 지연 폴백은 주입 클럭.
  - lane 해소 4규칙 + "새 lane으로 보내기" 토글 + 병합(lane 단위, coalesced_message_ids).
  - 합류 그룹(delegated_from_task_id) + **1회 발화** + 재진입 통보(서버가 작성자를 멘션).
  - blocked 경로: status set blocked → 질문 카드 게시 + lane.blocked_message_id + **위임자 즉시 깨움**,
    위임자 없으면 Director 인박스(인박스 자체는 P3지만 lane_blocked 행은 지금 만든다).
  - 루프 상한 3종 → 세션 paused(사유 구분) + 상한 설정.
  - 종료 조건 판정 4종 AND/OR → active→completing→completed. user_approval 발행은 인박스 항목 +
    승인 API까지(정식 HITL 전이는 P3). **요약 생성은 P4.**
  - 동시성 상한 4층, 호출 권한 게이트(respond_to, 세션 참여=허용), 결정 기록 저장/조회.
  - previewTriggers(FR-3.6) 구현 — 웹의 로컬 계산을 걷어낼 수 있게.
  - 백로그 흡수: **S-1**(규칙 3 — 위 규칙 세트에 포함), **S-9**(서버 seq attempt 스코프 충돌,
    두 자리 함께), **S-10**(AcceptInvite TOCTOU), **S-11**(파라미터 범위 → 422),
    **S-12**(설정 operation 구현 시 authz — 설정을 이번에 켠다면).
DoD: 골든 테스트(자기 범위) 초록 + 기존 테스트 회귀 0 + CI 초록. e2e/p1/07_adversarial.sh 37항목 유지.
금지: contracts/·daemon/·cli/·web/ 수정, 0001~0005 수정(0006부터).
```

### T-D2 · 데몬: Hermes 어댑터 · 프로파일 폴백 · 브리프 완성 · probe 실측

```
작업: P2 데몬 — Hermes ACP 어댑터, 같은 머신 프로파일 폴백, 브리프 [6][7][8], probe 실측값
스트림: D / 브랜치 feat/daemon-p2
입력: contracts/harness.md v0.3 전부(§1 Hermes 모델 접두어·§2.2 250ms·§8 본문 접두어 규칙),
      PRD §8.2.5(Hermes 함정), §8.4 브리프 [1]~[8], FR-1.6(프로파일), EVAL E12-04·05·06, E8-08·09
출력: PR 하나
  - Hermes 어댑터를 실기 경로로: mcpCapabilities 필터, 유실 감지(session/load null·provenance),
    250ms 정적 대기, 프로바이더 오류 접두어 규칙, usage_update.
  - 프로파일 다중 + **같은 머신 안에서만** 폴백(E8-08: workdir·아티팩트 유지 / E8-09: 대안 없으면 queued).
  - 브리프 [6] 컨텍스트 요약 · [7] 결정 기록 · [8] 지시 우선순위(서버가 주는 값을 붙이기만).
  - 백로그 흡수: **D-1**(probe가 `colab --version` 확인), **D-2**(probe의 resume·usage·tool_disallow를
    상수가 아니라 실측), **D-3**(acpprobe 제거 — harness/acp로 승격 완료),
    **데몬 message_id**(heartbeat preview.message_id를 채워 S7이 델타를 말풍선에 이어 붙이게).
DoD: acpfake 계약 테스트 + Hermes 실기 스모크(COLAB_SMOKE=1) 통과, `-race` 초록, CI 초록.
금지: contracts/·server/·cli/·web/ 수정.
```

### T-C2 · CLI: 5개 명령

```
작업: P2 colab CLI — lane delegate · status set · decision record · artifact submit/get · review approve/reject
스트림: C / 브랜치 feat/cli-p2
입력: contracts/colab-cli.md v0.3 §2.2·2.3, contracts/openapi.yaml x-colab-cli, EVAL E15-02, E3-05, E6-06
출력: PR 하나 — 5개 명령 + 같은 이름의 MCP 툴 + 종료 코드 규약 유지.
  `status set blocked`는 반환 `turn_end_required: true`를 그대로 노출한다.
  `lane delegate` 비참여자 → exit 3 + "hitl ask로 Director에게 요청하라" 안내.
  백로그 흡수: **C-1**(/cli/context 호출 시점 문서·구현 정렬), **C-2**(--tail↔--limit 표기).
DoD: httptest 계약 테스트 + MCP 왕복, `-race` 초록, CI 초록.
금지: contracts/·server/·daemon/·web/ 수정.
```

### T-W2 · 웹: S7 좌·우열 · lane 보드 · 질문 카드 · 피드 5클래스 · S6 · S9/S10 · 템플릿

```
작업: P2 웹 — 세션 화면 완성과 에이전트 관리
스트림: W / 브랜치 feat/web-p2
입력: SCREEN.md §4.4(S6 마법사 7단계)·§4.5(S7 좌·우열, lane 카드 7상태, 질문 카드)·§4.7(S9·S10),
      COMPONENTS.md(Lane Card·HITL Card·Inbox Item·Condition Row·Profile Row·Activity Feed),
      contracts/task_event.schema.json x-render-class(피드 5클래스), EVAL_USER U2·U4·U5·U8·U11·U15
출력: PR 하나
  - S7 좌열: 참여자 칩(파생 상태) + lane 보드 7상태 카드(+ 재진입 횟수, blocked 질문 배지).
  - S7 우열: goal·종료 조건 진행률·아티팩트·결정 기록·비용.
  - 작성창: **previewTriggers(서버)** 로 교체(로컬 계산 제거), × 억제, **new_lane 토글**(전송 후 자동 해제).
  - 질문 카드(blocked_q) + 스레드 답글.
  - 활동 피드 **5클래스 렌더러**(컷 1 대상 — 발동 시 2클래스 + 오류).
  - S6 마법사 전체(none 격리), S9·S10(프로파일 편집기·킬 스위치), **팀 템플릿 3종**(G5의 3분 실측 대상).
  - 백로그 흡수: **W-1**(S11이 RuntimeCapability 새 키를 읽는다), **W-2**(TaskEventWire 캐스팅 제거 — 완료),
    **W-3**(new_lane 토글), **W-5**(working에 드는 task 상태 확정), **W-6**(previewTriggers 교체).
DoD: typecheck·test·build 초록, 컴포넌트 테스트 신규 5개 이상, CI 초록.
금지: contracts/·server/·daemon/·cli/ 수정, 디자인 토큰 값 변경.
```

---

## 3. T-I2 · Integrator: 시나리오 A E2E

```
작업: P2 통합 — 시나리오 A 8단계 + Hermes + 템플릿 3분, plan/G5_REPORT.md
역할: Integrator. 구현 코드 수정 금지.
1부(G4): 시나리오 A를 **Claude Code 단일 런타임**으로 — 위임 3 → lane 3 병렬 → **합류 1회**
   (Lead 깨어난 횟수 = 위임 1 + 합류 1 + 통보 1) → 종합 → Writer 초안.
2부(G5): 같은 시나리오를 **Hermes 프로파일**로, 폴백 전환(E8-08)까지. blocked 왕복(E3-05~07),
   루프 상한(E4-03), 종료 조건 → 승인 → completed(E6-01·03), 템플릿에서 팀 생성 **Director 실측 3분**.
출력: e2e/p2/ 스크립트 + plan/G5_REPORT.md(DoD 항목별 판정·수치·재현 명령·결함의 스트림 귀속).
     `e2e/p1/07_adversarial.sh`에 P2 operation 행을 **추가**한다(경계는 늘 때마다 늘린다).
```

---

## 4. 배포 전 필수 (P2 안에 반드시)

| # | 항목 | 왜 지금 |
|---|---|---|
| **S-6** | SSE 응답에 `Cache-Control: no-cache, no-transform` | `compress:false`는 Next만 막는다. 배포의 nginx·CDN이 `text/event-stream`을 버퍼링하면 G3에서 고친 W-2가 **그대로 재발**한다. 서버가 스스로 말해야 한 곳에서 막힌다 |
| `make dev`의 `COLAB_WEB_URL` | Makefile이 아직 넘기지 않아 초대 링크 기본값이 서버 오리진 | S-5 잔여 |

---

## 5. 순서와 게이트

1. **P2a**(Hermes 단독) → 머지(빌드 태그로 초록 유지).
2. **T-S2 + T-D2** 동시 2개 → 리뷰·머지. S가 먼저 머지되어야 W의 previewTriggers 교체가 가능하다.
3. **T-C2 + T-W2** 동시 2개 → 리뷰·머지.
4. **T-I2 1부** → **G4 판정**(P2a 초록 + 시나리오 A 단일). 미달이면 Hermes 어댑터를 P3로 이월.
5. **T-I2 2부** → **G5 판정**(시나리오 A 8단계 + Hermes + 템플릿 3분).

리뷰는 P1과 같다: worker_done → Hermes `REVIEW PR <n>` → `/tmp/review<n>.md`를 Lead가 게시 → APPROVE면 CI·범위 확인 후 머지. **반려 3회면 Director 상신.**
