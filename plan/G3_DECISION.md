# G3 판정 — P1 수직 슬라이스 (P1 → P2)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G3** — "수직 슬라이스 DoD + kill -9 + 고아 정리 + 20회 중앙값". 통과 못 하면 **P2를 시작하지 않는다** |
| 근거 | `plan/G3_REPORT.md`(Integrator T-I1, PR #30, Hermes APPROVE), `e2e/p1/` 재현 스크립트, PR #18·#20·#21·#22 + 수정 #24~#28, **재확인 수정 #33(S)·#34(W)·#38(D)** |
| 작성 | Lead 초안 2026-09-06, **§1·§2·§3 재확인 갱신: Integrator 2026-09-06** |
| 상태 | **재확인 완료 (2026-09-06 09:15~09:24).** 수정 #33·#34·#38 머지 후 `03`·`04`·`06`을 재실행해 §3을 채웠다. DoD 1~5가 모두 실기 통과다 — 취소는 서버 `cancelLane`으로 발동되고(202→1초), S12 웹 패널은 0.2초에 `준비 완료`, U1은 19 PASS/1 FAIL/1 N/A. 남은 FAIL 1건은 **재확인에서 새로 드러난 서버 결함 S-6**(같은 이름 워크스페이스 생성 500)이고, 재확인 중 **C-1**(heartbeat `preview` 드리프트)도 발견했다(§2). 게이트 통과 선언과 P2 착수는 이 두 건의 처리 방침과 함께 Lead·Director 판단 |

## 1. DoD 판정 (실제 런타임: Claude Code 2.1.258 + 어댑터 0.74.0, haiku)

| # | DoD | 판정 | 수치 |
|---|---|---|---|
| 1 | 멘션 → 2초 내 claim → 실행 → 답글 웹 도착, **20회** 첫 출력 중앙값 ≤ 10초 | **통과** ¹ | claim 6ms(max 12ms), 첫 출력 3.05s, 답글 3.43s, 20/20 |
| 2 | 데몬 `kill -9` → heartbeat 만료 재큐잉·고아 정리·폐기 토큰 거부·중복 게시 0 | **통과** ² | 186s에 attempt 2, 폐기 토큰 `colab message post` → 401 exit 4·저장 0, 고아 게시 0, 메시지 각 1건, workdir 보존 |
| 3 | 취소 절차가 프로세스 트리를 남기지 않는다 | **통과** ³ | (A) 사람 경로: `POST /lanes/{id}/cancel` **202** → **1초**에 task `cancelled`(`failure_kind=cancelled`)·lane `failed`·취소 뒤 새 task 0·`queued/running` 0·프로세스 그룹 **4→0**·`claude-agent-acp` 0·pgid 파일 삭제·피드 `"사람이 중단함"` 1건·`cancel-done` 게시 0. (B) 데몬 SIGTERM: 1초에 종료, finish `cancelled`(`failed(other)` 아님), `runtime.error failed` **0건**(E10-03), 재큐잉 0, 프로세스 그룹 4→0 |
| 4 | 초대 링크로 두 번째 멤버 입장 | **통과(API·브라우저)** | 멤버 +1, `accepted`, S4 건너뜀. 재확인에서 **브라우저 S3까지 도달**: `invite.url`이 웹 오리진 `http://localhost:3000/invite/...`(S-5 수정 확인, `COLAB_WEB_URL`) → "마케팅팀에 초대되었습니다" → 가입 → S5, 멤버 2명, member 내비에 Settings 없음 |
| 5 | 신규 머신 S12 페어링 실측 | **통과(API·웹)** | 서버 SSE `pairing.updated` 2프레임·`runtime.updated` 2프레임. **웹 S12 단독 화면: 페어링 1건(이중 생성 없음), `daemon pair` → 패널 `준비 완료` 0.2초**(E17-09 기준 10초), 표시된 설치 명령 2행 불변. U1 온보딩 인라인 S12도 **7.3초에 준비 완료** + "Claude Code 감지됨 · 로그인됨 · 모델 5개" → U1 4단계 PASS(F1 최대 이탈 지점 해소) |

¹ 20회는 S-1 **우회(로컬 CHECK 완화) 상태**에서 측정했고, 수정(#26) 후에는 kill -9의 attempt 2 finish만 재확인했다(Lead 지시로 20회 재실행 생략 — 우회는 CHECK 키 하나라 지연 측정에 영향 없음). Hermes 리뷰 N3.
² E11-05의 "살아남은 고아를 kill"하는 분기는 실기에서 고아가 이미 죽어 있어 **acpfake 테스트로만** 검증됐다(N4). E11-09(다른 런타임 claim 거부)·E12-02(`allow_once` 부재 → `reject_once`)는 이 보고의 e2e가 아니라 PR #22·#20의 unit/harness 테스트로 충족된다(N1) — EVAL §부록의 "G3 = E11 전부"는 unit 포함으로 읽는다.

³ 재확인 실측(`e2e/p1/out/c-summary.json`, 2026-09-06 09:15~09:16). 03 스크립트는 이번에 `cancelLane` 501 전제를 버리고 **202를 기대**하도록 고쳤고, SIGTERM 경로(E10-13)를 별도 단계 (B)로 분리했다.

**판정 논리.** G3의 목적은 "기반이 흔들리면 뒤가 전부 흔들린다"를 막는 것이다(PLAN §6.2). 기반 다섯 — ACP 하네스·큐·실시간·CLI 토큰·고아 정리 — 은 실기에서 동작했다(1·2). 초안에서 실패했던 기반 위의 **경로** 둘 — 사람이 쓰는 S12 화면(W)과 서버 취소 경로(S)·데몬 종료 순서(D) — 은 #33·#34·#38 머지 뒤 §3에서 전부 실기 통과했다.

**재확인이 새로 드러낸 것.** 경로가 뚫리자 그 뒤에 가려져 있던 결함 둘이 보였다. **S-6**은 U1 1단계에서 사람이 처음 만나는 500이고(F1 이탈), **C-1**은 스트리밍 중 heartbeat가 통째로 422라 살아 있는 attempt가 3분 뒤 재큐잉될 수 있다(DoD 2가 막으려던 바로 그 중복 실행). 둘 다 국소 수정이고 계약 변경이 필요 없다 — S-6은 #33이 `runtimes.go`에 넣은 savepoint를 `auth.go`에도 넣는 것이고, C-1은 양쪽 필드 모양을 계약에 못 박는 것이다. G3 통과 선언을 이 둘의 처리 뒤로 미룰지, 통과시키고 P2 첫 작업으로 넣을지는 Lead·Director 판단.

## 2. 통합에서 드러난 결함과 처리

| ID | 스트림 | 내용 | 처리 |
|---|---|---|---|
| S-1 | S | `lane.runtime_session_ref` CHECK 키(`kind`) ≠ 계약(`runtime_kind`) → 모든 finish 500 | **#26 머지**, 보고서에서 수정 후 확인 |
| S-3 | S | claim이 타 워크스페이스 런타임에 task를 줌(보안) | **#28 머지** |
| S-2 | S | `cancelLane` 501 — T-S1이 lane 동작을 P2로 미룬 것과 PLAN P1 DoD 3의 충돌. **Lead 판단: DoD가 우선** — P1에 최소 cancelLane(취소 명령 발행 + finish cancelled) | **머지 #33** — §3에서 202→1초 cancelled 확인 |
| S-4 | S | 같은 호스트 재페어링 500(같은 tx 안 재시도) | **머지 #33**(savepoint), E11-12 |
| S-5 | S | 초대 URL 오리진이 서버(:8080) | **머지 #33** — §3 U13에서 `http://localhost:3000/invite/...` 확인. **잔여**: `make dev`·`e2e/p1/up.sh`가 `COLAB_WEB_URL`을 넘기지 않아 기본값이 여전히 서버 오리진이다 → up.sh는 이번에 넣었고, Makefile은 이 작업 범위 밖 |
| D-1 | D | SIGTERM 종료 시 ctx 취소가 취소 절차보다 먼저 prompt를 끊어 `failed(other)` | **머지 #38** — §3에서 finish `cancelled`·`runtime.error failed` 0건 확인, E10-13 |
| W-1 | W | PairingPanel이 마운트마다 createPairing 2회(StrictMode) | **머지 #34** — §3에서 페어링 1건 확인 |
| W-2 | W | S12 패널이 `ready`를 반영 안 함 (진짜 원인은 Next 응답 압축이 SSE를 버퍼링) | **머지 #34**(`compress: false`) — §3에서 0.2초 `준비 완료`, E17-09 |
| W-3 | W | e2e 문구·건너뛰기 링크 | **머지 #34** — §3 U1-2에서 건너뛰기 링크 확인 |
| **S-6** | S | **신규(재확인).** `auth.CreateWorkspace`의 slug 접미어 재시도가 **같은 tx 안**에서 돌아, 첫 유일제약 충돌 뒤 `25P02 current transaction is aborted` → **500**. 한글만인 이름은 `Slugify`가 전부 `ws`로 접어 **두 번째 '마케팅팀'부터 항상 실패**한다. U1 1단계에서 사람이 처음 만나는 500(F1 이탈). #33이 `runtimes.go`에 넣은 savepoint를 `auth.go:283`은 못 받았다 | **미해결** — `server/internal/auth/auth.go:299~318`을 S-4와 같은 savepoint 재시도로. 재현: `e2e/p1/04_u1_browser.sh` U1-3a |
| **C-1** | 계약/S·D | **신규(재확인).** heartbeat `preview` 모양 드리프트 — 데몬은 `preview: string`(`daemon/internal/api/api.go:67`), 서버는 `preview: {text, message_id}`(`server/internal/httpapi/daemon.go:260`). 부분 출력이 있는 동안 heartbeat가 **통째로 422**라 (a) `task.heartbeat_at`가 갱신되지 않아 3분 뒤 **살아 있는 attempt가 재큐잉**되고(T-I1 서버 로그 `expired stale attempts requeued:1` 2회), (b) S7 실시간 부분 출력(`message.delta`)이 한 번도 못 나간다. `daemon-protocol.md` §4.2의 본문 스케치에는 `preview` 필드 자체가 없어 계약이 모양을 못 박지 않았다 | **미해결** — 계약에 모양 확정 후 한쪽 정렬. 증거: `e2e/p1/out/daemon-a.log` 422 34건 |
| X-1 | 운영 | 공용 Postgres를 테스트 `DROP SCHEMA`가 지움 | E2E 전용 컨테이너(5435) — 완료 |
| X-2 | 운영 | e2e 픽스처 함정 — 세션 `goal`에 `취소 시나리오(E11-07)`처럼 저장소의 시나리오 이름을 쓰면 haiku Lead가 `e2e/p1/03_cancel.sh`를 찾아 **스스로 실행**한다. 세션이 재귀 생성되고 중첩 실행이 `kill -TERM`으로 데몬을 죽여 판정이 전부 오염됐다(2026-09-06 09:01~09:11, 세션 11개) | 완료 — `goal`을 저장소를 가리키지 않는 중립 문장으로 바꾸고 스크립트에 주석으로 남김 |

**EVAL 추가 행**(Integrator 제안, 채택): E8-13(non-nil runtime_session_ref 저장·재개), E11-11(claim 워크스페이스 한정), E11-12(재페어링 접미어), E10-13(SIGTERM 정상 종료 = cancelled), E17-09(S12 패널 10초 내 준비 완료). `EVAL.md` v0.2로 반영.

## 3. 재확인 (수정 머지 후) — 2026-09-06 09:15~09:24, dev @ b0ed563

실행 환경: E2E 전용 Postgres `colab-pg-e2e`(5435), server :8080(`COLAB_WEB_URL=http://localhost:3000`), web :3000(next dev), 실제 Claude Code 2.1.258 + 어댑터 0.74.0, 에이전트 모델 haiku.

| 시나리오 | 스크립트 | 기대 | 결과 |
|---|---|---|---|
| 취소 | `e2e/p1/03_cancel.sh` | `cancelLane` 202 → finish `cancelled`, lane `failed(cancelled)`, 재큐잉 없음, 프로세스 0 | **통과** — 202, **1초**에 task `cancelled`(`failure_kind=cancelled`), lane `failed`, 취소 뒤 새 task 0·`queued/running` 0, 프로세스 그룹 4→0, `claude-agent-acp` 0, pgid 파일 삭제, 피드 `"사람이 중단함"` 1건, `cancel-done` 게시 0, 데몬 생존. **추가 (B) 데몬 SIGTERM(E10-13)**: 1초 종료, finish `cancelled`, `runtime.error failed` **0건**, 재큐잉 0, 프로세스 4→0 |
| S12 웹 | `e2e/p1/06_s12_pairing_realtime.sh` (2) | 페어링 1건, 패널 10초 내 `준비 완료` | **통과** — 화면 1회 열기에 페어링 **1건**, `daemon pair` → 패널 `준비 완료` **0.2초**(E17-09 기준 10초), 표시 명령 2행 불변, 페어링한 코드의 DB 상태 `ready`. 서버 SSE `pairing.updated` 2 / `runtime.updated` 2. 스크린샷 `web/__screenshots__/p1-s12-01~03-*.png` 갱신 |
| U1 전체 | `e2e/p1/04_u1_browser.sh` | 1~13단계 + U13 브라우저 | **부분(19 PASS / 1 FAIL / 1 N/A)** — FAIL은 **U1-3a**(워크스페이스 이름 `마케팅팀` → 서버 500, **신규 S-6**). 이름만 유일하게 바꿔 이어간 나머지는 전부 PASS: **4단계 7.3초 `준비 완료` + "Claude Code 감지됨 · 로그인됨 · 모델 5개"**(F1 최대 이탈 지점 해소), 6~12 S6 기본값 6행, 13 goal 시스템 메시지·참여자 칩, 13b 초기 답글 35초, 14 멘션 칩, 15 답글 17초, 15b 활동 피드 레일. N/A는 5단계(에이전트 템플릿 3장 = P2). **U13**: `invite.url`이 웹 오리진(:3000), S3 "마케팅팀에 초대되었습니다" → 가입 → S5, 멤버 2명, member 내비 Settings 0. 스크린샷 `p1-u1-01~15-*.png`·`p1-u13-01~02-*.png` 갱신 |

**단계별 판정 원본**: `e2e/p1/out/d-steps.tsv`. 요약 JSON: `c-summary.json`·`f-summary.json`·`d-summary.json`(`out/`는 gitignore — 이 표가 커밋본).

## 4. 게이트 예산 (PLAN §6.2 G3: `blocked` 10 / PR 30)

`blocked`(question) 3건(S 계약 4건, Integrator 2건), PR **22건**(#18~#39 — 재확인 수정 #33·#34·#38과 계약 #35·#37, 재생성 #36 포함). 리뷰 반려 PR당 최대 1회(상한 3). **예산 안**(상한 30, 남은 여유 8). 토큰 단위 `u`: worker 세션 사용률을 Orca가 노출하지 않아 이번에도 재지 못함 — 대신 관측: 4 worker 동시 fan-out이 5시간 창을 20~30분에 소진(3회 정지). **P2는 동시 2개.**

## 5. P2 착수 조건

§3 세 행이 채워졌다(2026-09-06). Director가 이 판정을 확인하면 `plan/P2_TASKS.md`로 fan-out(P2a Reviewer 골든 테스트 → P2b). `plan/P2_BACKLOG.md`의 이월 항목을 흡수.

**남은 판단 1건**: §2의 신규 결함 **S-6**·**C-1**을 P2 fan-out 전에 처리할지, P2 첫 작업으로 넣을지. Integrator 의견 — **S-6은 먼저**(U1 1단계에서 사람이 만나는 500이라 이후 어떤 사람 경로 검증도 이 우회를 깔고 가야 한다), **C-1은 계약 확정이 먼저**라 P2 계약 스트림에 얹는 편이 낫다.
