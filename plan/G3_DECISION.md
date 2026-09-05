# G3 판정 — P1 수직 슬라이스 (P1 → P2)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G3** — "수직 슬라이스 DoD + kill -9 + 고아 정리 + 20회 중앙값". 통과 못 하면 **P2를 시작하지 않는다** |
| 근거 | `plan/G3_REPORT.md`(Integrator T-I1, PR #30, Hermes APPROVE), `e2e/p1/` 재현 스크립트, PR #18·#20·#21·#22 + 수정 #24~#28 |
| 작성 | Lead 초안, 2026-09-06 |
| 상태 | **조건부 통과 — 재확인 대기.** 기반 다섯은 실기로 통과했고(DoD 1·2), 사람 경로(S12)와 취소 경로 결함은 수정 PR이 돌고 있다. 수정 머지 후 e2e/p1 `03`·`04`·`06`만 재실행해 §3을 채운 뒤 Director 확인 |

## 1. DoD 판정 (실제 런타임: Claude Code 2.1.258 + 어댑터 0.74.0, haiku)

| # | DoD | 판정 | 수치 |
|---|---|---|---|
| 1 | 멘션 → 2초 내 claim → 실행 → 답글 웹 도착, **20회** 첫 출력 중앙값 ≤ 10초 | **통과** ¹ | claim 6ms(max 12ms), 첫 출력 3.05s, 답글 3.43s, 20/20 |
| 2 | 데몬 `kill -9` → heartbeat 만료 재큐잉·고아 정리·폐기 토큰 거부·중복 게시 0 | **통과** ² | 186s에 attempt 2, 폐기 토큰 `colab message post` → 401 exit 4·저장 0, 고아 게시 0, 메시지 각 1건, workdir 보존 |
| 3 | 취소 절차가 프로세스 트리를 남기지 않는다 | **부분** | 프로세스 그룹 4→0 ✓. 그러나 서버 `cancelLane` 501(S-2), 데몬 SIGTERM 경로가 `failed(other)`로 보고 → 재큐잉(D-1) |
| 4 | 초대 링크로 두 번째 멤버 입장 | **통과(API)** | 멤버 +1, `accepted`, S4 건너뜀. 브라우저 S3는 U1 4단계 실패로 미도달 |
| 5 | 신규 머신 S12 페어링 실측 | **API 통과 / 웹 실패** | `daemon pair` → ready 7.7s, 서버 SSE 정상. **웹 S12 패널이 `대기 중`에 머묾**(W-1 페어링 이중 생성, W-2 갱신 미반영) → U1 4단계 FAIL, F1 최대 이탈 지점 |

¹ 20회는 S-1 **우회(로컬 CHECK 완화) 상태**에서 측정했고, 수정(#26) 후에는 kill -9의 attempt 2 finish만 재확인했다(Lead 지시로 20회 재실행 생략 — 우회는 CHECK 키 하나라 지연 측정에 영향 없음). Hermes 리뷰 N3.
² E11-05의 "살아남은 고아를 kill"하는 분기는 실기에서 고아가 이미 죽어 있어 **acpfake 테스트로만** 검증됐다(N4). E11-09(다른 런타임 claim 거부)·E12-02(`allow_once` 부재 → `reject_once`)는 이 보고의 e2e가 아니라 PR #22·#20의 unit/harness 테스트로 충족된다(N1) — EVAL §부록의 "G3 = E11 전부"는 unit 포함으로 읽는다.

**판정 논리.** G3의 목적은 "기반이 흔들리면 뒤가 전부 흔들린다"를 막는 것이다(PLAN §6.2). 기반 다섯 — ACP 하네스·큐·실시간·CLI 토큰·고아 정리 — 은 실기에서 동작했다(1·2). 실패한 것은 기반 위의 **경로** 둘이다: 사람이 쓰는 S12 화면(W)과 서버 취소 경로(S)·데몬 종료 순서(D). 셋 다 국소 결함이고 계약 변경이 필요 없다. 따라서 "P2를 시작하지 않는다"가 아니라 **수정 후 3개 시나리오 재확인을 조건으로 통과**로 본다. 단 재확인 전에 P2 fan-out은 하지 않는다.

## 2. 통합에서 드러난 결함과 처리

| ID | 스트림 | 내용 | 처리 |
|---|---|---|---|
| S-1 | S | `lane.runtime_session_ref` CHECK 키(`kind`) ≠ 계약(`runtime_kind`) → 모든 finish 500 | **#26 머지**, 보고서에서 수정 후 확인 |
| S-3 | S | claim이 타 워크스페이스 런타임에 task를 줌(보안) | **#28 머지** |
| S-2 | S | `cancelLane` 501 — T-S1이 lane 동작을 P2로 미룬 것과 PLAN P1 DoD 3의 충돌. **Lead 판단: DoD가 우선** — P1에 최소 cancelLane(취소 명령 발행 + finish cancelled) | fix/p1-g3-server |
| S-4 | S | 같은 호스트 재페어링 500(같은 tx 안 재시도) | fix/p1-g3-server, E11-12 |
| S-5 | S | 초대 URL 오리진이 서버(:8080) | fix/p1-g3-server, `COLAB_WEB_URL` |
| D-1 | D | SIGTERM 종료 시 ctx 취소가 취소 절차보다 먼저 prompt를 끊어 `failed(other)` | fix 예정(동시 worker 2 제한으로 S·W 뒤), E10-13 |
| W-1 | W | PairingPanel이 마운트마다 createPairing 2회(StrictMode) | fix/p1-g3-web |
| W-2 | W | S12 패널이 `ready`를 반영 안 함 | fix/p1-g3-web, E17-09 |
| W-3 | W | e2e 문구·건너뛰기 링크 | fix/p1-g3-web |
| X-1 | 운영 | 공용 Postgres를 테스트 `DROP SCHEMA`가 지움 | E2E 전용 컨테이너(5435) — 완료 |

**EVAL 추가 행**(Integrator 제안, 채택): E8-13(non-nil runtime_session_ref 저장·재개), E11-11(claim 워크스페이스 한정), E11-12(재페어링 접미어), E10-13(SIGTERM 정상 종료 = cancelled), E17-09(S12 패널 10초 내 준비 완료). `EVAL.md` v0.2로 반영.

## 3. 재확인 (수정 머지 후)

| 시나리오 | 스크립트 | 기대 | 결과 |
|---|---|---|---|
| 취소 | `e2e/p1/03_cancel.sh` | `cancelLane` 202 → finish `cancelled`, lane `failed(cancelled)`, 재큐잉 없음, 프로세스 0 | (대기) |
| S12 웹 | `e2e/p1/06_s12_pairing_realtime.sh` (2) | 페어링 1건, 패널 10초 내 `준비 완료` | (대기) |
| U1 전체 | `e2e/p1/04_u1_browser.sh` | 1~13단계 + U13 브라우저 | (대기) |

## 4. 게이트 예산 (PLAN §6.2 G3: `blocked` 10 / PR 30)

`blocked`(question) 3건(S 계약 4건, Integrator 2건), PR 13건(#18~#30). 리뷰 반려 PR당 최대 1회(상한 3). **예산 안.** 토큰 단위 `u`: worker 세션 사용률을 Orca가 노출하지 않아 이번에도 재지 못함 — 대신 관측: 4 worker 동시 fan-out이 5시간 창을 20~30분에 소진(3회 정지). **P2는 동시 2개.**

## 5. P2 착수 조건

§3 세 행이 채워지고 Director가 이 판정을 확인하면 `plan/P2_TASKS.md`로 fan-out(P2a Reviewer 골든 테스트 → P2b). `plan/P2_BACKLOG.md`의 이월 항목을 흡수.
