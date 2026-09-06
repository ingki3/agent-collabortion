# G4 판정 — P2 중간 게이트: 시나리오 A · Claude Code 단일 런타임 (G4 → G5)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G4** — "P2a 통과 + 시나리오 A **Claude Code 단일 런타임** 통과". 미달이면 Hermes 어댑터를 P3로 이월하고 G5를 열지 않는다 |
| 근거 | `plan/G4_REPORT.md`(Integrator T-I2 1부, **1판 PR #73 · 2판 #81 · 3판 #87 · 보완 #89**, 각각 Hermes APPROVE — 수치를 `out/`·DB에서 독립 재계산), `e2e/p2/` 재현 스크립트(`up.sh`·`10_scenario_a_api.sh`·`11_scenario_a_web.sh`·`12_mock_vs_real.sh`·`20_regression_p1.sh`), 구현 스트림 PR #54(D)·#61(C)·#62(S)·#65(S)·#67(W), 통합이 드러낸 결함의 수정 PR #70·#71·#75·#76·#78·#79·#83·#85, 계약 PR #51·#53·#56·#59·#60·#63·#66·#70·#88 |
| 작성 | Lead 2026-09-06 |
| 상태 | **✅ G4 통과 — 확정(2026-09-06).** Director가 "알아서 진행"으로 확인·진행을 Lead에 위임했다. **K-5 결정**: 규칙 8 억제는 합류 발화 전까지, 합류 뒤 기상은 FR-3.4 병합이 묶는다(PRD v0.15·EVAL E1-22 — 코드 변경 없음, 골든 E1-17 유지). **G5(T-I2 2부) 착수.** P2a 골든 72행 상시 CI 초록(리뷰어 재실행 72/72). 시나리오 A **API/CLI 경로 32/32**(보완 재실행, FAIL 0) · **웹 경로 PASS 19 · FAIL 0 · N/A 1 · DoD 밖 1**. 리뷰어가 건 통과 조건(API 재실행 기록) 충족(#89). 확인되면 **T-I2 2부(G5: 시나리오 A 8단계 + Hermes 프로파일 + 템플릿 3분 Director 실측)** 를 연다(§5) |

## 1. DoD 판정 (실제 런타임: Claude Code 2.1.258 + 어댑터 0.74.0, haiku; 스택 dev `c7d299e`)

| # | DoD | 판정 | 수치 |
|---|---|---|---|
| 1 | **P2a 통과** — 골든 테이블 E1·E2·E4·E5·E6 71행이 구현을 통과하고 상시 CI에 든다 | **통과** | 72행(E5-15 순서 검증 1행 추가) **72/72**, `p2golden` 태그 제거로 상시 CI 편입(#62). 리뷰어가 #87 브랜치 서버로 `-run Golden` 재실행 72/72 |
| 2 | 시나리오 A — Lead가 3항목 위임 → Researcher lane 3 **병렬** → 합류 **정확히 1회** → 종합 → Writer 초안 → `artifact submit` | **통과** ¹ | **API/CLI**(`10_scenario_a_api.sh`, 보완 재실행): 체크 **32/32**. Lead 기상 **3**(시작 1 + 합류 2 — Writer의 `@Lead` 통보는 규칙 8로 억제, task 0), Researcher lane **3**·동시 겹침 **3**, 합류 그룹 2(그룹당 시스템 메시지 **1**), 합류 프롬프트에 자식 메시지 **3/3**(E1-21), `submitArtifact` 201·다운로드 **1755 B = Content-Length**, 진행률 **1/2**(`artifact_submitted` 충족, 세션 `active`), workdir 행 = lane 수 5, `colab_cli.present` true, `failure_kind=auth` 0. 82초, 에이전트 턴 7 |
| 3 | 같은 시나리오를 **웹(agent-browser)** 으로 U2·U4·U5 여정대로 | **통과** ² | `11_scenario_a_web.sh` 3판: **PASS 19 · FAIL 0 · N/A 1(W14, `listLaneTasks`는 x-phase P3) · DoD 밖 1(W16 비용 → D-6)**. 마법사 7단계(런타임 후보 1, 참여자 3, **제출자 = Writer 지정**) → 세션 생성 → S7: lane 카드 동시 running **4**(Lead 1 + Researcher 3), 카드별 브리프 3, Researcher 칩 `working`(실시간), Lead task 없는 동안 Lead `idle`, 작성창 미리보기 = 서버 `previewTriggers`, `@all` → "트리거 없음 — 기록만"(E1-05), **합류 카드 실시간 1 · 아티팩트 행 실시간 1**(새로고침 0 — 리뷰어가 스크립트 코드로 증명), 진행률 0/2 → 1/2, HITL 발행 1 |
| 4 | P1 회귀 없음 | **통과** | P2 스택에서 `e2e/p1/01·02·03·05·06` 통과(claim 중앙값 0.008s, 첫 출력 3.3s, kill -9 중복 0, 취소, 초대, S12 0.3s), `04` 부분(P2 op 결함과 동일 항목 → 해소), **`07` 경계 110/110** |

¹ 1판(#73)은 29/31 — FAIL 2가 S-4(workdir 행 미기록)·S-5(`colab_cli` 미노출)였고 #75로 닫혔다. 보완 재실행(#89)에서 "workdir 행 got 5 / want 3"이 한 번 갈렸는데 단언이 좁았던 것이다(격리 `none`은 lane당 workdir 하나 → lane 5 = 행 5). 단언을 둘로 나눠 32행이 됐다. 제품 결함이 아니다.

² 1판은 S7이 첫 렌더에서 죽어(S-1 `listDecisions` 계약 위반) 여정이 시작조차 못 했고, 2판은 마법사·보드는 통과했으나 실시간 전파 3건(S-13·S-14)이 실패했다. 3판은 hotfix #78(lane.updated 전이 15곳)·#85(S7 이벤트 6종 발행)·#83(마법사 제출자 지정) 반영 빌드다. 웹 워커가 못 풀었던 헤드리스 하이드레이션 문제는 Integrator가 `next build + next start`로 해소했다(dev 서버의 ENOENT 8회가 원인).

**판정 논리.** G4의 목적은 "협업 코어가 실제 런타임 위에서 돌아가는가"다(PLAN §3 P2, §6.2). 코어 다섯 — 라우팅 규칙·lane 해소·합류 1회·종료 조건 판정·실시간 전파 — 이 API와 웹 양쪽에서 끝까지 돌았고, 그 수치는 Integrator가 아닌 리뷰어가 DB에서 다시 세어 일치했다. 1·2판에서 실패한 것은 전부 **통합에서만 드러나는 부류**(계약 모양 위반, P2 op 501, 이벤트 미발행)였고, 판정을 미루는 대신 hotfix 라운드 셋으로 닫은 뒤 **우회 없는 정식 실행**으로 다시 쟀다. 열린 결함(§2)은 전부 DoD 밖이며 G5·P3 전 처리 시점이 정해져 있다.

## 2. 통합이 드러낸 결함과 처리

| ID | 스트림 | 내용 | 처리 |
|---|---|---|---|
| D-1 | D+계약 | env 허용목록에 `USER`가 없어 만료 OAuth 갱신 실패 → 모든 task `failed(auth)`. **계약이 틀렸고 데몬은 계약대로였다** | **#70(harness v0.6)·#71** |
| C-1 | C | `colab --version` exit 2 → 모든 probe `colab_cli.present=false` | **#71** |
| S-1 | S | `listDecisions`가 `{items:[]}`(계약은 배열) → S7 전체 사망 | **#75** |
| S-2·S-3·S-6 | S | `listRuntimeCandidates`·`agent-templates`·`listLanes` x-phase P2인데 501 | **#75** |
| S-4·S-5·S-7·S-8 | S | workdir 행 미기록 · `colab_cli` 미저장 · `agent_disabled` 경고 누락 · `recordDecision` 사람 쿠키 201(결정 기록 위조 가능) | **#75** |
| W-1·W-2 | W | `@all` 링크 `mention://all`(PRD는 `all/all`) → 규칙 6으로 assignee 기상 · mock 기대값이 EVAL과 반대 | **#76** |
| S-9·S-10 | S | `lane.brief` 미저장 · lane→running 전이에 `lane.updated` 미발행(보드가 병렬 시작을 못 봄) | **#78** |
| W-3 | W | AgentChip에 `data-agent-id` 없음(e2e 식별 불가) | **#79** |
| W-4 | W | 마법사가 `artifact_submitted` 제출자를 지정 못 함(시나리오 A "Writer가 제출" 불가) | **#83** — 페이로드는 계약 문언대로 `{who:"assignee"}` 또는 `{agent_id}` |
| S-13·S-14 | S | 시스템 메시지 `message.created` 미발행 · `participant.updated`·`artifact.created`·`decision.created`·`completion_progress`·`cost.updated` 발행 자리 0 | **#85** — 덤으로 `session.cost_usd`를 아무도 쓰지 않던 결함 |
| **D-6** | D+계약 | 런타임이 `cost_usd`를 안 주면 데몬이 `0 + estimated:false` → 비용이 확정 $0으로 보임(FR-7.3 추정 배지 불가, 예산 강제 불발) | **열림.** 계약 v0.7(#88)로 규칙 확정(생략 + `estimated:true`, 가격표는 서버). 구현 D-6·S-20 — **P3 예산 항목 전** |
| S-16 | S | `listParticipants`(P2) 501 — 웹은 세션 상세를 써서 무영향 | 열림, 다음 서버 작업 |
| K-5 | 계약 | 규칙 8 억제가 lane 상태에 묶여 `done` 뒤 한 줄이 위임자를 다시 깨움 — E1-17 문언 vs FR-6.5 "정확히 한 번" | **열림 — G5 전 결정** |
| 기타 | — | S-11·S-12·S-17·S-18·S-19·W-5·D-2·C-3 | 백로그, G4 밖 |

## 3. 운영에서 배운 것 (PLAN §10.7 되먹임)

- **골든을 먼저 쓴 것이 값을 했다.** T-S2 첫 PR은 표를 통과했지만 훅 13개 중 6개가 프로덕션이 부르지 않는 그림자였고, 표를 쓴 리뷰어가 "프로덕션 호출 0"으로 잡았다. §0-8(어댑터 예외)·"훅 옆에 프로덕션 호출처 주석"이 그 답이다.
- **계약 질문 12건이 전부 계약 수정으로 끝났다.** 워커가 추측 대신 물은 것(폴백 주체, `colab_cli` 위치, `end_turn` 이름 충돌, 타임아웃 규칙, 렌더 순서, `supported_options`, `CompletionAtom`의 `who` vs `agent_id`…)은 하나도 헛되지 않았다. Lead 지시문이 계약과 어긋난 경우(2건)도 워커가 계약을 들어 바로잡았다.
- **리뷰 라운드가 앞 라운드의 수정이 만든 결함을 잡았다.** CLI `artifact get`: 조용한 절단 → 무한 대기 → 유휴 상한 분리. 반려 후 재확인을 생략하면 그대로 머지됐을 것이다.
- **통합에서만 드러나는 부류가 이번에도 넷 이상**(계약 모양·501·이벤트 미발행·env). 스트림 단위 초록으로는 안 보인다 — T-I2를 게이트마다 두는 이유.
- **Orca 운영**: 반려 재작업은 dispatch가 닫힌 뒤라 메일이 막힌다(터미널 send + push 감시), 경로 무관 `pkill`이 남의 서버를 죽인다(§0-10), 웹 브랜치는 리뷰어가 읽을 워크트리를 Lead가 만들어야 한다.

## 4. 확인 요청

Director가 확인할 것은 하나다 — **§1의 판정을 받아들이고 G5(T-I2 2부)를 여는가.** 판정 근거는 전부 PR과 `e2e/p2/out/`에 있고, `bash e2e/p2/up.sh && bash e2e/p2/10_scenario_a_api.sh`로 재현된다(에이전트 턴 7, 약 90초).

## 5. 확인 후 순서

1. **K-5 결정**(규칙 8 억제 해제 시점) — G5 시나리오가 이 규칙 위에서 돈다.
2. **T-I2 2부(G5)**: 시나리오 A를 **Hermes 프로파일**로 + 폴백 전환(E8-08), blocked 왕복(E3-05~07), 루프 상한(E4-03), 종료 조건 → 승인 → `completed`(E6-01·03), **템플릿에서 팀 생성 3분 Director 실측**.
3. 그 전에 닫을 것: D-6·S-20(비용 추정 — P3 예산 전), S-16.
