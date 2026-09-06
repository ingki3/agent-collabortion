# P2 작업 분해 — 협업 코어 (G4 → G5)

| 항목 | 내용 |
|---|---|
| 상태 | **G5 진행 중(2026-09-06).** G4 확정(Director 위임), K-5 결정(PRD v0.15). P3-prep 완료: 데몬 #93(D-6)·서버 #95(S-16·S-20). **G5 차단 결함 D-7**(Hermes 도구 표면 — 계약 harness v0.8/v0.8.1 `tool_surface`·`cli_wrapper` 래퍼) **PR #97로 해소**(한 라운드 APPROVE, 실기 스모크: 위생화 셸에서 래퍼로 `colab --version` 완료). T-I2 2부 보고서 **PR #100**: D-7 수정 뒤 재실행으로 30_ **57/0**, 총 PASS 134 / FAIL 5 — **G5 충족(템플릿 3분 Director 실측 대기)**. 남은 FAIL 5 의 결함 S-24~S-31 은 **서버 hotfix PR #103 으로 전부 해소**(계약 PR #101: 승인 op P2·blocked_q 멘션·기상 인용). PR #100 은 Hermes 재계산 뒤 머지. 다음: T-I2 가 #103 코드로 31_·33_ 재측정 → `plan/G5_DECISION.md`(템플릿 3분은 Director 실측 대기). 구현 스트림 전부 완료(P2a #52 · D #54 · S #62·#65 · C #61 · W #67) + 통합 hotfix 8건(#70·#71·#75·#76·#78·#79·#83·#85). T-I2 1부: 1판 #73 → 2판 #81 → 3판 #87 → 보완 #89. **API 32/32 · 웹 19/0/N/A 1/DoD 밖 1 · 골든 72/72 · 07 110/110.** 판정: `plan/G4_DECISION.md`. 확인되면 K-5 결정 → T-I2 2부(G5) |
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
10. **프로세스 종료는 pid 파일 또는 포트 지정만 (2026-09-06).** 이 머신은 워커 여럿이 각자 서버·데몬·웹을 띄운다. 한 워커의 경로 무관 `pkill -f bin/server`가 다른 워커(T-I2 G4 스택 :8090)의 서버를 죽였다. `kill $(cat out/server.pid)` 또는 `lsof -ti :PORT | xargs kill`만 쓴다 — e2e up/down 스크립트가 pid 파일을 쓰는 이유다.
9. **웹 리뷰 체크 항목 (2026-09-06, PR #73 리뷰어 자기 정정).** mock이 생성 타입으로 강제돼 *모양*이 안전하다는 것과 별개로, (1) 웹이 서버로 **보내는 문자열**(멘션 링크 등)과 (2) mock이 **통과시키는 값**(lane 해소 번호 등)은 계약·EVAL 원문과 기계 대조한다. #67에서 둘 다 놓쳐 실서버에서 `@all`이 assignee를 깨웠다(E1-05 위반).
8. **골든 테스트 파일의 어댑터 예외 (2026-09-06 결정).** "구현 PR은 테스트 파일을 건드리지 못한다"(PLAN §10.3)의 목적은 **구현이 기대값을 자기에게 맞게 낮추는 것**을 막는 데 있지, 다리 역할을 하는 코드를 잠그는 데 있지 않다. 골든 파일의 **어댑터 함수**(`decideForCase` 등, 파일 주석이 "It is an ADAPTER, not an implementation"이라 밝힌 것)는 구현자가 고칠 수 있다 — 새 `Input` 모양을 아는 사람은 그것을 정하는 구현자뿐이고, Reviewer가 먼저 고치려면 아직 없는 시그니처를 알아야 하는 순환에 빠진다. 조건 셋:
   - 어댑터 변경은 **독립 커밋**으로 분리한다(구현·마이그레이션·다른 테스트를 섞지 않는다).
   - **기대값은 한 글자도 바꾸지 않는다** — 케이스 테이블·`want`·케이스 이름·행 번호 주석 전부. 손댈 수 있는 것은 어댑터 **본문**과 `//go:build` 태그 줄뿐이다. Lead가 머지 전에 골든 파일 diff를 기계로 확인하고, Reviewer 리뷰 항목에도 넣는다.
   - 표가 틀렸다고 판단되면 **고치지 말고 근거와 함께 보고**한다. PRD가 이기고 판정은 Lead가 한다.
   - 라우팅·판정 로직은 전부 구현 쪽에 남긴다. 어댑터가 판정을 조금이라도 하면 골든 표가 구현이 아니라 어댑터를 검증하게 된다.

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
  - **프로파일 폴백**(T-D2에서 이동): 재시도 가능한 `failure_kind`(network·stall·runtime_offline·rate_limited)일 때 `agent_profile.fallback_profile_id`로 갈아타 재큐잉 — 같은 workdir(`reuse:true`), `attempt` 증가, `runtime_kind`가 바뀌면 `resume` 비움(E8-08). 대안이 없으면 `queued` + Director 알림, **다른 머신으로 넘기지 않음**(E8-09).
  - 백로그 흡수: **S-1**(규칙 3 — 위 규칙 세트에 포함), **S-9**(서버 발행 seq 동시 계산 — **네 자리**), **S-10**(AcceptInvite TOCTOU), **S-11**(파라미터 범위 → 422),
    **S-12**(설정 operation 구현 시 authz — 설정을 이번에 켠다면).
DoD: 골든 테스트(자기 범위) 초록 + 기존 테스트 회귀 0 + CI 초록. e2e/p1/07_adversarial.sh 37항목 유지.
금지: contracts/·daemon/·cli/·web/ 수정, 0001~0005 수정(0006부터).
```

#### T-S3 · 서버 후속: 아티팩트 제출·리뷰 (T-S2 다음 PR)

T-S2 에서 **하지 않은** 것이고 "범위 밖"이 아니라 "바로 다음"이다.

```
작업: submitArtifact · reviewArtifact · getArtifact · downloadArtifact · listArtifacts (FR-4.3)
스트림: S / 브랜치 feat/server-artifacts
왜 지금인가:
  - **G5 를 막는다.** 시나리오 A 8단계가 artifact_submitted 로 시작한다. G4 는 통과한다.
  - **PR #61(T-C2, CLI)이 이미 이 엔드포인트를 부른다.** 서버가 없으면 `colab` 5개 명령 중
    3개(artifact submit·get, review approve)가 501 을 받는다.
  - 종료 조건 E6-05·06(`agent_approval`)의 판정 로직은 T-S2 가 이미 넣었다
    (sessions.ApplyEvent). 빠진 것은 그 이벤트를 발생시킬 **호출 경로**뿐이다.
출력: 파일 저장(버전 관리 — 같은 이름 재제출 시 v2), artifact_review 행,
      submit → sessions.ApplyEvent{Kind: "artifact_submit"},
      review approve → sessions.ApplyEvent{Kind: "review_approve"} 연결.
DoD: E6-05·06 이 실 경로로 초록, e2e 에서 CLI 3개 명령이 501 을 받지 않는다.
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
  - ~~프로파일 폴백~~ → **T-S2로 이동**(2026-09-06 결정, `daemon-protocol.md` v0.4 §4.4): 세션이 런타임에 고정되므로 서버 재큐잉이 같은 머신을 보장하고, 재시도 회계·토큰·비용이 전부 서버 소유다. 데몬이 할 일은 **`failure_kind`를 정확히 보고**하는 것뿐이며 그 분류를 acpfake 테스트로 굳힌다.
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

**e2e에서만 확인할 수 있는 항목 (PR #54 리뷰에서 이월).** acpfake 계약 테스트가 원리적으로 증명할 수 없는 것들이라 여기서 받는다 — fake는 구현의 가정을 공유하므로 "데몬이 보냈다"까지만 증명하고 "런타임이 존중한다"는 증명하지 못한다.

| # | 항목 | 왜 fake로는 불가능한가 |
|---|---|---|
| **S3** | 실제 `disallowedTools`가 런타임에서 **효력**이 있는가 | `acpfake`가 이 PR에서 필터를 해석하도록 함께 바뀌었다 — fake와 구현이 같은 가정을 공유한다. 실제 어댑터가 무시하면 테스트는 통과한 채 `tool_disallow=true`를 오보한다 |
| **S4** | Hermes 250ms 정적 대기가 **실기의** 늦은 청크를 잡는가 | fake는 지연을 스크립트로 흉내낸다 |

나머지 스모크 항목(S1 세션 보존·S2 어댑터 버전 핀·S5 `colab --version`)은 **2026-09-06 Lead가 실기로 확인해 닫았다**(PR #54 코멘트: 두 런타임 PONG 완료, `resume`·`usage` 실측 true, `tool_disallow`가 claude true / hermes false로 갈림, `adapter_version = 0.74.0` = 핀).

---

## 4. 배포 전 필수 (P2 안에 반드시)

| # | 항목 | 왜 지금 |
|---|---|---|
| **S-6** | SSE 응답에 `Cache-Control: no-cache, no-transform` | `compress:false`는 Next만 막는다. 배포의 nginx·CDN이 `text/event-stream`을 버퍼링하면 G3에서 고친 W-2가 **그대로 재발**한다. 서버가 스스로 말해야 한 곳에서 막힌다 |
| `make dev`의 `COLAB_WEB_URL` | Makefile이 아직 넘기지 않아 초대 링크 기본값이 서버 오리진 | S-5 잔여 |

---

## 5. 순서와 게이트

1. ~~**P2a**(Hermes 단독) → 머지(빌드 태그로 초록 유지).~~ **완료 2026-09-06, PR #52.** 골든 71행(E1 20·E2 13·E4 9·E5 18·E6 11), EVAL 전 행 포함을 기계 대조로 확인. 태그 없는 빌드는 초록 유지. 작성자가 Hermes라 리뷰는 Lead가 기계 대조로 대신했다(PR #52 코멘트에 근거 기록).
   - 부산물: 골든 테이블을 쓰다 **명세 공백 4건**이 드러나 EVAL v0.3·PRD v0.14로 반영(PR #53). E1-21 억제와 합류 전달은 한 쌍 · E2-14 새 lane 토글 자동 해제 · E2-15 중단 후 재지시의 lane · E4-10 재개 시 카운터. 자동 발행 `user_approval`의 취소 조건은 P3로 미룸(K-4).
   - 계약 공백 2건도 함께 메웠다(PR #51, `daemon-protocol.md` v0.5): probe 최상위 `colab_cli`, `preview.message_id`는 서버가 채운다.
2. **T-S2 + T-D2** 동시 2개 → 리뷰·머지. S가 먼저 머지되어야 W의 previewTriggers 교체가 가능하다.
   - **T-D2 완료 2026-09-06, PR #54.** Hermes APPROVE. 리뷰어가 회귀 2건(`Preview` 인자, `Resume` 상수화)을 직접 주입해 테스트가 실제로 무는 것까지 확인했다. 삭제 3144줄 중 3084줄이 `acpprobe`이고, 함께 사라진 테스트 8개가 전부 `harness/acp`에 행동 기준으로 대체됐음을 표로 대조했다. **DoD의 실기 스모크는 Lead가 직접 돌려 닫았다.** 비차단 지적 NN1(`Sink.Preview`의 `message_id` 인자 제거 — 옳은 값을 만들 수 없다면 틀린 값을 넣을 자리도 없어야 한다)·NN3(스모크의 로그인 미비는 `Skipf`)은 머지 전에 반영했고, 가드가 여전히 무는 것을 Lead가 상수를 되돌려 확인했다.
   - **T-S2 완료 2026-09-06, PR #62** — 골든 71행 초록, 태그 제거로 **상시 CI 편입**. 두 라운드: 1차 반려는 **그림자 훅 6개**(프로덕션 호출 0 — 프로덕션을 망가뜨려도 표가 초록, 파생 상태 사다리가 둘) + Lead 추가 결함 `paused_detail` 키·모양·테이블 이름 셋 다 계약과 어긋남 + 서버 이벤트 seq 한 자리 미보호. 2차는 리뷰어가 같은 방법으로 다시 세어 해소 확인, `LastFailureKindSQL` ORDER BY 경계 4건을 Postgres에서 직접 평가, advisory lock 순서를 전 호출자에서 추적. 파생 상태 열린 질문은 Lead 판정 — **사다리는 PRD 표 순서대로, 끈적한 `error`는 입력 정의가 막는다**(가장 최근 task 기준, 두 화면이 SQL 조각 하나를 공유). 부산물: 계약 PR #60(dispatched 5분 타임아웃은 재큐잉이 아니라 종료 — 계약만 반대였고 인용한 EVAL 행이 그 반대를 말하지 않았다), §0-8 골든 어댑터 예외(PR #56).
   - T-D2가 끝나 슬롯이 비어 **T-C2를 앞당겨 착수**했다(`task_e97458fa87bf`). CLI는 httptest로 계약을 검증하므로 서버 머지를 기다릴 필요가 없다 — 기다려야 하는 것은 W의 `previewTriggers`뿐이다.
3. **T-C2 + T-W2** 동시 2개 → 리뷰·머지.
   - **T-C2 완료 2026-09-06, PR #61.** 세 라운드 — 각 라운드가 앞 라운드의 수정이 만든 것을 잡았다: `artifact get` 16 MiB **조용한 절단** → 수정이 **모든 타임아웃을 버려 무한 대기** → 헤더 상한(transport) + 본문 유휴 상한(idleReader)으로 분리. 교훈: 상한 하나가 두 일(자르기·끝내기)을 하고 있었다. 부산물: 계약 PR #59(`colab-cli.md` ↔ openapi 불일치 5건 — review 는 아티팩트 스코프, artifact submit 은 multipart 4필드, decision record 는 summary·rationale 둘뿐 — 그리고 **`end_turn` → `turn_end_required`**: ACP stopReason 과 이름이 충돌해 P1 `kind`↔`runtime_kind` 와 같은 부류였다).
   - **T-W2 착수**(`task_211be9c19151`) — T-S2 머지를 기다리지 않고 슬롯이 비자마자 넣었다. 웹은 계약(생성 타입 + mock)을 상대로 만들고 서버 머지는 종단 검증(T-I2)에만 필요하다. 부산물: 계약 PR #63(`x-render-class` 평가 순서 first-match·실패 강조·"서버가 파생" 문구 정정 — 와이어 필드가 없고 서버도 계산하지 않았다).
   - **T-W2 완료 2026-09-06, PR #67 — 한 라운드 APPROVE.** 계약 빈칸 2건(#63 피드 평가 순서·"서버가 파생" 문구 정정, #66 `supported_options`)을 추측 없이 물어 계약이 고쳐진 뒤 반영. 리뷰어가 판정 5건을 코드 줄로 대조, 회귀 주입 2/2. mock이 생성 타입으로 강제돼 P1의 모양 불일치 부류가 컴파일에서 막힌다. 브라우저 실기(U1)는 헤드리스 하이드레이션 환경 문제로 못 함 → **T-I2가 대신 확인**. P3 기록: 배너 UUID 폴백(NN1), 컷 1 플래그 출처(NN2).
   - **T-S3 완료 2026-09-06, PR #65 — 두 라운드.** 반려 1건: large object 정리 경로 부재(세션 CASCADE 뒤 바이트가 `pg_largeobject`에 영구 잔존) → 0008 트리거(`pglo:` 가드, `undefined_object`만 삼킴) + 두 국면 회귀 테스트. Lead가 트리거를 빼고 FAIL 확인, 리뷰어가 SQLSTATE 42704 를 라이브 DB에서 대조. 백로그 S-14(다운로드 커넥션 점유 상한 — P3 웹 다운로드 전 필수)·S-15(리뷰 행 덮어쓰기). **후속 소PR 대기**: "이미 없는 oid" 케이스 테스트 1건(`feat/server-artifacts-nn1`) — T-I2는 기다리지 않는다.
4. **T-I2 1부** → **G4 판정**(P2a 초록 + 시나리오 A 단일). 미달이면 Hermes 어댑터를 P3로 이월.
   - **첫 실행에서 G4 차단 결함 격리(2026-09-06)**: 모든 `claude_code` task가 `failure_kind=auth` — 데몬 env 허용목록에 `USER`가 없어 macOS 키체인이 만료 OAuth를 갱신하지 못했다. **데몬은 계약을 정확히 따랐고 계약이 틀렸다**(harness §2.1 "인증은 HOME이면 된다"). P1에서 안 드러난 이유는 액세스 토큰이 살아 있어 갱신이 불필요했기 때문. 계약 v0.6(PR #70) + hotfix(PR #71, 데몬 `systemEnvKeys`에 `USER` · CLI `--version` 플래그 — 후자는 모든 probe가 `colab_cli.present=false`로 오보하던 C 결함). Integrator는 프로파일 env 우회로 측정을 계속했고, hotfix 머지 후 **우회 없이 재실행**한 수치로 판정한다.
   **착수 2026-09-06**(`task_0bec4deae960`, 워크트리 p1-integration, 브랜치 test/g4-scenario-a).
   - **1부 자료 머지(PR #73).** API/CLI 경로 **통과** — Lead 기상 3(시작 1 + 합류 2; Writer 통보는 규칙 8 억제로 task 0), lane 3 동시 running 3, 합류 그룹당 1회·자식 3/3(E1-21), artifact 201·1439 B = Content-Length, 진행률 1/2, 31 chk 중 PASS 29(FAIL 2 = S-4 workdir 행·S-5 colab_cli, 코어 밖). 리뷰어가 수치를 DB에서 독립 재계산해 일치. 웹 경로 **실패** — S-1 listDecisions `{items:[]}`(계약은 배열)로 S7 전체 사망, S-2·S-3·S-6 P2 op 501. 결함 10건(S 8·W 2) → **T-S4**(`task_8b5e8280f8e5`, 서버·웹 PR 분리). 회귀: P1 01·02·03·05·06 통과, 07 82 중 1(S-8 recordDecision 쿠키 201 — 결정 기록 위조 가능). **G4 판정은 T-S4 머지 후 웹 절반 재실행(2판)으로.**
   - **T-S4 완료 2026-09-06 — PR #75(서버)·#76(웹), 둘 다 한 라운드 APPROVE.** 리뷰어가 전수 대조 표를 openapi에서 독립 집계(배열 list op 9 일치, TaskToken 전용 write op는 워커 6 → 실제 5: `submitArtifact`는 양쪽 허용), 런타임 후보 규칙 SCREEN §4.4 1:1, 템플릿 3종 FR-1.4·SCREEN §4.1 일치, workdir 기록 시점이 daemon-protocol의 두 보고 경로와 대응, 회귀 주입 3/3. 남긴 것: S-16(`listParticipants` P2 501), W-3′(mock 재진입 규칙). 사고: 워커의 경로 무관 `pkill -f`가 T-I2 스택 서버를 죽임 → §0-10.
   - **2판 1차(2026-09-06)** — 마법사 W1~W4b 통과(S-1·S-2 해소 확인), 세션 화면에서 3건 실패. Lead가 실서버 payload·DB·코드로 갈랐다: W5(동시 running 1) = DB는 Researcher 3개가 14초 겹쳤으나 **서버가 lane→running 전이에 `lane.updated`를 발행하지 않음**(S) · W6(brief 0) = `lane.brief` 컬럼 부재, delegate의 brief 폐기(S) · W7(칩 빈값) = AgentChip에 `data-agent-id` 없음(W 테스트 훅). → **hotfix-2 PR #78(서버: 0010 `lane.brief`, status 전이 15곳 전부 발행 — 순환 import를 `LanePublish` 훅으로 끊고 `tasks.publish`가 task·lane 이벤트를 함께 냄, claim 뒤 running 프레임을 실 SSE 본문에서 읽는 테스트)·#79(웹: `data-agent-id`)**, 둘 다 한 라운드 APPROVE(리뷰어가 전이 자리를 코드에서 독립 집계, 훅 배선 누락을 주입으로 검증). 백로그 S-17(nil 훅 로그)·S-18(20 vs 15 정의).
   - **2판 2차(PR #81, 머지)** — W5(동시 running, 화면 최대 4)·W6(브리프 3)·W9(`@all` 트리거 없음) **통과** — hotfix #76·#78·#79가 실기에서 닫힘. 새 실패 3건은 전부 **S7 실시간 전파**(W13c "새로고침하면 보인다" PASS가 증거): **S-13** `SystemPost`가 `message.created`를 발행하지 않음(합류 카드 실시간 0) · **S-14** `participant.updated`·`artifact.created`·`decision.created`·`session.completion_progress`·`cost.updated` 발행 자리 0(웹은 13종 전부 처리). W14(task 이력 피드)는 `listLaneTasks`가 **P3** op라 N/A로 재분류. up.sh를 `next build + next start`로 바꿔 헤드리스 하이드레이션 문제 해소. → **hotfix-3 PR #85 머지** — 6종 발행(메시지 INSERT 5곳 전수, `participant.updated`는 `listParticipants`와 같은 함수로 파생 상태, 비용 롤업은 잠금 순서 역전 때문에 커밋 뒤 별도 tx). 부수 발견: `artifact.created` 발행 자리 부재, `session.cost_usd`를 아무도 쓰지 않아 모든 세션 $0. 리뷰어 회귀 주입 2/2. 백로그 S-19. **3판(PR #87, 머지)** — 웹 여정 **PASS 19 · FAIL 0 · N/A 2**(W14 P3 op, W16 비용). 2판 실패 W7·W10·W13 전부 통과, 마법사 제출자=Writer 지정(W4c)과 제출 후 1/2(W13b) 웹에서 성립, 실시간 도착은 리뷰어가 스크립트 코드로 증명(`ab open` 1회, reload 0). W16은 N/A가 아니라 **D 결함**(D-6: 데몬이 `cost_usd 0`을 `estimated:false`로) — 계약 harness v0.7로 규칙 확정, S-20. **G4 판정 조건 1**(리뷰어 §4): `10_scenario_a_api.sh` 현 스택 1회 재실행 → FAIL 0 기록(1판 FAIL 2는 #75로 닫혔으나 산출물이 1판 그대로). 보완 PR 뒤 `plan/G4_DECISION.md`. 리뷰어 의견: 웹 절반은 **부분**("새로고침하면 보인다"는 여정 DoD를 못 채우지만 웹은 이벤트 13종을 받을 코드가 있고 남은 것은 서버 publish 5줄). 별개 W 결함 **W-4**(마법사가 `artifact_submitted` 제출자를 지정 못 함 → 시나리오 A "Writer가 제출"을 웹에서 못 만듦)는 **PR #83**으로 닫힘 — 페이로드는 계약 `CompletionAtom` 문언대로 `{who:"assignee"}` 또는 `{agent_id}`(Lead 지시문의 `{who:"agent", agent_id}`는 계약 위반이라 워커가 물어 바로잡음). W-3′(mock 재진입 규칙 3)도 같은 PR. 백로그 W-5(mock 규칙의 vitest 가드).
   - 리뷰어 자기 정정: W-1(`@all` 링크 형식)·W-2(mock 기대값)는 #67 리뷰가 놓친 것 — mock의 *모양*만 보고 웹이 *만드는 문자열*과 mock의 *기대값*은 계약·EVAL과 대조하지 않았다. → §0-9. 판정 수치: Lead 기상 = 정확히 3(위임 1 + 합류 1 + 통보 1), lane 3 동시 running 구간, 다운로드 바이트 = Content-Length, 웹(agent-browser)과 API/CLI 양쪽.
5. **T-I2 2부** → **G5 판정**(시나리오 A 8단계 + Hermes + 템플릿 3분). **착수 2026-09-06.** 주의: `respondHitlRequest`는 x-phase P3(501)이므로 `user_approval` 승인은 P2의 승인 경로(`completeSession`/director 이벤트)로 판정하고 HITL 응답 UI는 P3로 남긴다. 템플릿 3분은 agent-browser 측정 + Director 실측 항목으로 분리.

리뷰는 P1과 같다: worker_done → Hermes `REVIEW PR <n>` → `/tmp/review<n>.md`를 Lead가 게시 → APPROVE면 CI·범위 확인 후 머지. **반려 3회면 Director 상신.**
