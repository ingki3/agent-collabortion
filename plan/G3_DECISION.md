# G3 판정 — P1 수직 슬라이스 (P1 → P2)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G3** — "수직 슬라이스 DoD + kill -9 + 고아 정리 + 20회 중앙값". 통과 못 하면 **P2를 시작하지 않는다** |
| 근거 | `plan/G3_REPORT.md`(Integrator T-I1, PR #30, Hermes APPROVE), `e2e/p1/` 재현 스크립트, PR #18·#20·#21·#22 + 수정 #24~#28, **재확인 수정 #33(S)·#34(W)·#38(D)**, **최종 재확인 수정 #41(계약)·#42(D)·#43(S)** |
| 작성 | Lead 초안 2026-09-06, **§1·§2·§3 재확인 갱신: Integrator 2026-09-06**, **§1~§5 최종 재확인 갱신: Integrator 2026-09-06 10:00** |
| 상태 | **재확인 완료 + Lead 철저 테스트 완료(§3-2) — Director 확인 대기.** 1차 재확인(09:15~09:24, #33·#34·#38)으로 §3을 채웠고, 거기서 새로 드러난 **S-6·C-1을 #41·#42·#43으로 고친 뒤 최종 재확인(09:50~10:00)** 에서 둘 다 실기로 해소를 확인했다(§3-1). **DoD 1~5 전부 실기 통과, §2에 미해결 행 0건.** 남은 것은 Director 확인이며, 확인되면 P2 fan-out을 시작한다(§5) |

## 1. DoD 판정 (실제 런타임: Claude Code 2.1.258 + 어댑터 0.74.0, haiku)

| # | DoD | 판정 | 수치 |
|---|---|---|---|
| 1 | 멘션 → 2초 내 claim → 실행 → 답글 웹 도착, **20회** 첫 출력 중앙값 ≤ 10초 | **통과** ¹ | claim 6ms(max 12ms), 첫 출력 3.05s, 답글 3.43s, 20/20 |
| 2 | 데몬 `kill -9` → heartbeat 만료 재큐잉·고아 정리·폐기 토큰 거부·중복 게시 0 | **통과** ² | 186s에 attempt 2, 폐기 토큰 `colab message post` → 401 exit 4·저장 0, 고아 게시 0, 메시지 각 1건, workdir 보존 |
| 3 | 취소 절차가 프로세스 트리를 남기지 않는다 | **통과** ³ | (A) 사람 경로: `POST /lanes/{id}/cancel` **202** → **1초**에 task `cancelled`(`failure_kind=cancelled`)·lane `failed`·취소 뒤 새 task 0·`queued/running` 0·프로세스 그룹 **4→0**·`claude-agent-acp` 0·pgid 파일 삭제·피드 `"사람이 중단함"` 1건·`cancel-done` 게시 0. (B) 데몬 SIGTERM: 1초에 종료, finish `cancelled`(`failed(other)` 아님), `runtime.error failed` **0건**(E10-03), 재큐잉 0, 프로세스 그룹 4→0 |
| 4 | 초대 링크로 두 번째 멤버 입장 | **통과(API·브라우저)** | 멤버 +1, `accepted`, S4 건너뜀. 재확인에서 **브라우저 S3까지 도달**: `invite.url`이 웹 오리진 `http://localhost:3000/invite/...`(S-5 수정 확인, `COLAB_WEB_URL`) → "마케팅팀에 초대되었습니다" → 가입 → S5, 멤버 2명, member 내비에 Settings 없음 |
| 5 | 신규 머신 S12 페어링 실측 | **통과(API·웹)** | 서버 SSE `pairing.updated` 2프레임·`runtime.updated` 2프레임. **웹 S12 단독 화면: 페어링 1건(이중 생성 없음), `daemon pair` → 패널 `준비 완료` 0.2초**(E17-09 기준 10초), 표시된 설치 명령 2행 불변. U1 온보딩 인라인 S12도 **7.3초에 준비 완료** + "Claude Code 감지됨 · 로그인됨 · 모델 5개" → U1 4단계 PASS(F1 최대 이탈 지점 해소) |

¹ 20회는 S-1 **우회(로컬 CHECK 완화) 상태**에서 측정했고, 수정(#26) 후에는 kill -9의 attempt 2 finish만 재확인했다(Lead 지시로 20회 재실행 생략 — 우회는 CHECK 키 하나라 지연 측정에 영향 없음). Hermes 리뷰 N3. **최종 재확인(#41·#42·#43 머지 뒤, dev @ `9a5680a`)에서 `01_vertical_slice.sh`를 N=3으로 한 번 더 돌렸다**: claim 중앙값 **0.007s**(max 0.013s), 첫 출력 **3.92s**, 답글 **4.69s**, 3/3 완료+답글, 페어링→`ready` 6.4초 — 20회 측정(claim 6ms, 첫 출력 3.05s, 답글 3.43s)과 **같은 자릿수**다. 서버·데몬 경로가 바뀐 뒤에도 지연이 그대로임을 확인했으므로 20회 재측정은 불필요하다(Hermes PR #40 NN3에 대한 답).
² E11-05의 "살아남은 고아를 kill"하는 분기는 실기에서 고아가 이미 죽어 있어 **acpfake 테스트로만** 검증됐다(N4). E11-09(다른 런타임 claim 거부)·E12-02(`allow_once` 부재 → `reject_once`)는 이 보고의 e2e가 아니라 PR #22·#20의 unit/harness 테스트로 충족된다(N1) — EVAL §부록의 "G3 = E11 전부"는 unit 포함으로 읽는다.

³ 재확인 실측(`e2e/p1/out/c-summary.json`, 2026-09-06 09:15~09:16). 03 스크립트는 이번에 `cancelLane` 501 전제를 버리고 **202를 기대**하도록 고쳤고, SIGTERM 경로(E10-13)를 별도 단계 (B)로 분리했다.

**판정 논리.** G3의 목적은 "기반이 흔들리면 뒤가 전부 흔들린다"를 막는 것이다(PLAN §6.2). 기반 다섯 — ACP 하네스·큐·실시간·CLI 토큰·고아 정리 — 은 실기에서 동작했다(1·2). 초안에서 실패했던 기반 위의 **경로** 둘 — 사람이 쓰는 S12 화면(W)과 서버 취소 경로(S)·데몬 종료 순서(D) — 은 #33·#34·#38 머지 뒤 §3에서 전부 실기 통과했다.

**재확인이 새로 드러낸 것, 그리고 그 처리.** 경로가 뚫리자 그 뒤에 가려져 있던 결함 둘이 보였다. **S-6**은 U1 1단계에서 사람이 처음 만나는 500이고(F1 이탈), **C-1**은 스트리밍 중 heartbeat가 통째로 422라 (a) 살아 있는 attempt가 3분 뒤 재큐잉될 수 있고(DoD 2가 막으려던 바로 그 중복 실행) (b) S7 부분 출력이 한 번도 실시간으로 못 나갔다. **Lead 판단은 "P2 fan-out 전에 처리"였고**, #41(계약 v0.3)·#42(데몬)·#43(서버)로 셋 다 머지됐다. 최종 재확인(§3-1)에서 둘 다 실기로 해소를 확인했으므로 **§2에 미해결 행은 남지 않았다** — G3 통과 선언을 미룰 이유가 없다.

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
| **S-6** | S | **신규(재확인).** `auth.CreateWorkspace`의 slug 접미어 재시도가 **같은 tx 안**에서 돌아, 첫 유일제약 충돌 뒤 `25P02 current transaction is aborted` → **500**. 한글만인 이름은 `Slugify`가 전부 `ws`로 접어 **두 번째 '마케팅팀'부터 항상 실패**한다. U1 1단계에서 사람이 처음 만나는 500(F1 이탈). #33이 `runtimes.go`에 넣은 savepoint를 `auth.go:283`은 못 받았다 | **해결 — 머지 #43.** savepoint 재시도 + ASCII 영숫자가 없는 이름은 **이름 해시 stem**(`ws-<6hex>`). 최종 재확인(§3-1): 같은 이름 워크스페이스 연속 2개 **201/201**·slug `ws-2d7bd7` / `ws-2d7bd7-2`(API), 브라우저 U1-3a 도 **201**·slug `ws-2d7bd7-3` → `ws-2d7bd7-4`, U1 1~4단계 **5 PASS / 0 FAIL**. 회귀 고정: `04_u1_browser.sh`가 U1-3a 앞에서 같은 이름 워크스페이스를 하나 먼저 만들어 **브라우저가 만드는 것이 두 번째가 되게** 한다. `plan/P2_BACKLOG.md` S-7과 같은 건 |
| **C-1** | 계약/S·D | **신규(재확인).** heartbeat `preview` 모양 드리프트 — 데몬은 `preview: string`(`daemon/internal/api/api.go:67`), 서버는 `preview: {text, message_id}`(`server/internal/httpapi/daemon.go:260`). 부분 출력이 있는 동안 heartbeat가 **통째로 422**라 (a) `task.heartbeat_at`가 갱신되지 않아 3분 뒤 **살아 있는 attempt가 재큐잉**되고(T-I1 서버 로그 `expired stale attempts requeued:1` 2회), (b) S7 실시간 부분 출력(`message.delta`)이 한 번도 못 나간다. `daemon-protocol.md` §4.2의 본문 스케치에는 `preview` 필드 자체가 없어 계약이 모양을 못 박지 않았다 | **해결 — 머지 #41(계약 v0.3)·#42(데몬)·#43(서버).** 세 겹이다: 계약이 `preview: {text, message_id?}`를 못 박고(§4.2 v0.3), 데몬이 그 모양으로 보내며(부분 출력이 없으면 키 자체 생략), 서버가 **모양이 어긋나도 200 + `heartbeat_at` 갱신**하고 잘못된 preview만 무시하며 attempt당 경고 1건을 피드에 남긴다 — 이 관대 디코드가 재발 방지의 본체다(Hermes PR #42 NN1). 최종 재확인(§3-1): heartbeat 실패·422 **0건**, `expired stale attempts requeued` **0건**, 35.3초 턴에서 SSE `message.delta` **2프레임**. **심각도 정정(Hermes PR #40 NN1)**: events 배치도 생존 신호를 갱신하므로(`events.go` "Events are liveness") (a) 재큐잉은 heartbeat가 전부 422이고 **동시에** 3분간 events 배치도 없을 때만 나는 **간헐적** 피해다(T-I1 서버 로그에서 실제로 2회 성립). 조건 없이 항상 참이던 피해는 (b)이고, 이번에 그 경로가 열린 것을 실측했다. **잔여(비차단)**: 피드 경고의 seq 계산이 attempt 스코프라 attempt 2 이상에서 경고 1건이 유실될 수 있다 — heartbeat 자체는 안전, `plan/P2_BACKLOG.md` S-9 |
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

### 3-1. 최종 재확인 (S-6·C-1 수정 머지 후) — 2026-09-06 09:50~10:00, dev @ `9a5680a`

실행 환경은 §3과 같다(E2E 전용 Postgres `colab-pg-e2e` 5435, server :8080 `COLAB_WEB_URL=http://localhost:3000`, web :3000, 실제 Claude Code 2.1.258 + 어댑터 0.74.0, haiku). §3의 세 시나리오는 다시 돌리지 않았다 — 이번 수정(#41·#42·#43)이 건드린 것은 워크스페이스 생성과 heartbeat 경로뿐이라 **그 둘만** 짧게 쳤다.

| 대상 | 스크립트 | 기대 | 결과 |
|---|---|---|---|
| **S-6** 워크스페이스 slug | `04_u1_browser.sh` `U1_STOP_AFTER=4` (+ 같은 호출의 API 대조) | 같은 이름 워크스페이스 연속 2개가 둘 다 **201**, slug 서로 다름 | **통과** — API 직접: `POST /workspaces {"name":"마케팅팀"}` × 2 → **201 / 201**, slug `ws-2d7bd7` / `ws-2d7bd7-2`. 브라우저 경로 U1 1~4단계 **5 PASS / 0 FAIL / 0 N/A**: 선행 워크스페이스(`ws-2d7bd7-3`)를 만들어 둔 뒤 온보딩에서 **같은 이름 '마케팅팀'** 을 넣어 `ws-2d7bd7-4` 로 통과 → 2단계 진입, 설치 명령 2행·상태 `대기 중`, `daemon pair` → **7.5초에 `준비 완료`** + "Claude Code 감지됨 · 로그인됨 · 모델 5개". **1차 재확인의 유일한 FAIL(U1-3a)이 사라졌다** |
| **C-1** heartbeat preview | `01_vertical_slice.sh` `N=3` (+ 새 5b 긴 답변 1회, 세션 SSE 구독) | 데몬 로그 heartbeat 422 **0건**, 서버 로그 `expired stale attempts requeued` **0건**, SSE `message.delta` **≥ 1** | **통과** — heartbeat 실패 **0건**(422 0건), `expired stale attempts` 로그 **0건**, 35.3초 턴에서 SSE `message.delta` **2프레임** 수신(내용은 에이전트의 부분 출력 텍스트). 같은 실행의 지연: claim 중앙값 0.007s(max 0.013s), 첫 출력 3.92s, 답글 4.69s, 3/3 완료+답글(각주 ¹) |

**5b를 새로 넣은 이유.** heartbeat는 15초에 한 번이고 `preview`는 어댑터의 `agent_message_chunk`(에이전트가 대화창에 **직접** 쓰는 글)만 쌓는다. 그래서 delta를 보려면 턴이 15초보다 길고 **그 사이에 에이전트가 자기 텍스트를 쓰고 있어야** 한다. 인사 턴(3~6초)은 앞 조건에서 걸리고, "길게 써서 게시해줘"는 뒤 조건에서 걸린다 — 긴 글이 MCP 도구 인자로만 흘러 preview가 빈 채 15초 heartbeat가 지나간다(실측: 21.4초 턴, 첫 `message` 이벤트 21.4초, delta 0). "대화창에 직접 길게 쓴 뒤 마지막에 한 줄만 게시"로 바꾸자 delta가 나왔다. **C-1 (b)의 관측 조건 자체가 좁다**는 뜻이라 스크립트에 못 박아 뒀다.

요약 JSON: `e2e/p1/out/a-summary.json`(`heartbeat_422` · `stale_requeue_log_lines` · `sse_message_delta` · `long_turn_seconds` 필드 신설)·`d-summary.json`(`seed_slug` · `browser_slug`). `out/`는 gitignore — 이 표가 커밋본.

## 3-2. Lead 철저 테스트 (2026-09-06 10:15~10:36, dev @ `fa483f2` → `9c6f69d`)

Director 요청으로 Integrator 재확인과 **독립적으로** 전수 검증했다. 목적이 다르다 — Integrator는 DoD를 실기로 재는 것이고, 이쪽은 **되면 안 되는 것이 정말 안 되는가**와 **자동화가 놓치는 자리**를 본다.

| 단계 | 내용 | 결과 |
|---|---|---|
| A 결정적 | Go 26패키지 `-race`, 핵심 5패키지 `-count=3`, 마이그레이션 클린·멱등, `gofmt`·`vet`, 웹 42개 | 전부 통과, 플레이크 0, skip 0 |
| B 계약 정합 | openapi strict lint, task_event JSON Schema, 생성물 드리프트(서버·웹), DB ENUM 28종 | **결함 3건 발견** |
| C 실기 E2E | `01`(N=3)·`02`·`03`·`04`·`05`·`06` 전부 재실행 | 전부 통과, U1 **20 PASS/0 FAIL/1 N/A**, 서버 5xx·패닉 0 |
| D 적대적 | 신설 `07_adversarial.sh` 37항목 | **37 PASS**, 결함 3건 발견(테스트 2·계약 1) |

**발견 6건**(PR #46·#47로 4건 수정, 2건 백로그):

| # | 내용 | 처리 |
|---|---|---|
| T-1 | `openapi.yaml`의 따옴표 없는 설명 안 쉼표가 플로우 매핑을 쪼개 `RuntimeCapability`에 유령 속성 `'Hermes 0.20.6).'`을 만들고 `version.description`을 잘랐다(PR #27이 넣음). `recommended`는 통과하고 `recommended-strict`만 잡는다 | **#46** 수정 + CI strict lint |
| T-2 | `web/lib/api/schema.d.ts`가 계약 v0.4.1 이후 재생성되지 않아 웹 타입이 서버가 보내지 않는 `transport`·`usage_reporting`·`options`를 선언하고 실제 키 7개가 없었다 | **#46** 재생성 + CI 드리프트 게이트 |
| T-3 | 낡은 타입이 가리고 있던 **웹 목 서버의 계약 불일치 7곳** — `object_ref`는 계약 v0.4에서 문자열인데 목·스토리·테스트가 객체를 썼다. 목으로 도는 웹 테스트가 서버가 만들지 않는 모양을 검증하고 있었다 | **#46** |
| T-4 | **CI가 계약을 전혀 검증하지 않았다** — T-1·2·3이 전부 초록으로 통과한 이유 | **#46** `contracts` 잡 신설(strict lint·JSON Schema·프로즈 키 스캔·양쪽 드리프트) |
| T-5 | `02_kill9.sh`가 "attempt 2가 심부름을 끝냈는가"를 단언 — 실측에서 haiku가 `sleep`을 백그라운드로 돌리고 턴을 끝내 ✗. E8-04가 요구하는 것은 **중복 0**이다 | **#47** `≤1`로 견고화 |
| T-6 | 계약의 `limit`은 `minimum:1 maximum:200`인데 서버는 `-1`·`999999`를 200으로 받아 **조용히 50으로 강제**한다. 네 저장소가 모두 clamp하므로 자원 고갈 위험은 없다 | 백로그 **S-11** |

D 단계가 확인한 경계(전부 막혀 있다): task 토큰이 남의 세션·워크스페이스·에이전트·인박스·설정을 못 읽는다 · 위조 토큰 401 · 남의 워크스페이스 읽기/게시/런타임/에이전트 차단 · 남의 SSE 스트림 구독 차단 · 미인증 401 · 데몬 API가 사람 쿠키·위조 데몬 토큰 거부 · 잘못된 UUID·깨진 JSON·빈 본문·200KB 본문·범위 밖 페이지네이션이 5xx가 되지 않음.

**판정에 미치는 영향**: DoD 1~5의 실기 결과는 바뀌지 않는다(C가 전부 재확인). B·D가 찾은 것은 **계약 정합과 검증 게이트**의 문제였고 넷은 고쳐졌다. 남은 둘은 비차단 백로그다.

## 4. 게이트 예산 (PLAN §6.2 G3: `blocked` 10 / PR 30)

`blocked`(question) 3건(S 계약 4건, Integrator 2건), PR **27건**(#18~#44 — 1차 재확인 수정 #33·#34·#38, 재확인 보고 #40, 최종 재확인 수정 #41·#42·#43, 계약 #35·#37, 재생성 #36, P2 백로그 #39·#44 포함). 이 최종 재확인 PR을 더하면 **28건**. 리뷰 반려 PR당 최대 1회(상한 3). **예산 안**(상한 30) 이지만 **남은 여유는 2건뿐** — P2 fan-out은 PR 예산을 새 게이트(G4)에서 다시 세는 전제로 시작해야 한다. 토큰 단위 `u`: worker 세션 사용률을 Orca가 노출하지 않아 이번에도 재지 못함 — 대신 관측: 4 worker 동시 fan-out이 5시간 창을 20~30분에 소진(3회 정지). **P2는 동시 2개.**

## 5. P2 착수 조건

§3 세 행이 채워졌고(09:24), §3-1 두 행까지 채워졌다(10:00). Director가 이 판정을 확인하면 `plan/P2_TASKS.md`로 fan-out(P2a Reviewer 골든 테스트 → P2b). `plan/P2_BACKLOG.md`의 이월 항목을 흡수.

**남은 판단 1건 → 해소.** §2의 신규 결함 **S-6**·**C-1**을 P2 fan-out 전에 처리할지가 남은 판단이었다. **Lead는 "먼저"를 택했고** #41·#42·#43이 머지됐으며, 최종 재확인(§3-1)에서 둘 다 실기로 해소를 확인했다. **§2에 미해결 행은 0건**이고 새로 드러난 결함도 없다 — G3 판정에 남은 열린 항목이 없다.

**P2로 넘기는 것**(전부 비차단, `plan/P2_BACKLOG.md`에 기록됨):

| 항목 | 내용 | 근거 |
|---|---|---|
| S-6(백로그) | SSE 응답에 `Cache-Control: no-cache, no-transform` — `compress:false`는 Next만 막는다. 배포의 nginx·CDN이 `text/event-stream`을 버퍼링하면 W-2가 재발한다 | Hermes PR #34 NN1. **배포 전 필수** |
| S-9(백로그) | 서버 발행 `task_event`의 seq가 attempt 스코프인데 유일 제약은 `(task_id, seq)` → attempt 2의 첫 서버 이벤트가 충돌. 피해는 피드 노트 1건 유실(heartbeat·취소는 안전) | Hermes PR #43 NN1 |
| S-10(백로그) | `auth.AcceptInvite` 동시 수락 TOCTOU → 500 | PR #43 전수 조사 |
| 데몬 `message_id` | heartbeat `preview.message_id`를 데몬이 아직 채우지 않아 S7이 델타를 기존 말풍선에 이어 붙일 수 없다(계약상 선택 필드라 위반 아님, 서버는 이미 준비돼 있음) | Hermes PR #42 NN2 |
| `make dev`의 `COLAB_WEB_URL` | Makefile이 아직 이 값을 넘기지 않아 초대 링크 기본값이 서버 오리진이다. `e2e/p1/up.sh`에만 넣어 둔 상태 | §2 S-5 잔여 |

