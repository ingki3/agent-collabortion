# P2 백로그 — P1 리뷰·통합에서 이월된 항목

| 항목 | 내용 |
|---|---|
| 목적 | P1 PR 리뷰(Hermes)와 Integrator가 남긴 비차단 지적·후속을 한곳에 모은다. `plan/P2_TASKS.md`를 만들 때 스트림별 작업에 흡수한다 |
| 출처 | PR #18·#20·#21·#22·#25·#26·#28 리뷰 코멘트, `plan/G3_REPORT.md`(작성 중) |

## S (서버)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| ~~S-1~~ | 라우터 규칙 3(`@all`·사람만 멘션 → 트리거 없음) — 지금은 규칙 6으로 떨어져 assignee task 생성. 웹 미리보기 칩("트리거 없음")과 서버가 반대로 말함 | PR #22 N1 | P2b 라우터 전체에 포함(E1-05·06) |
| S-2 | `session.runtime_id` ↔ `workspace_id` 일치를 DB에서 강제 — 복합 FK `session(workspace_id, runtime_id) → runtime(workspace_id, id)`(0005). `rebindSession`(FR-9.2)이 세 번째 고정 경로가 되므로 같은 가드 필수 | PR #28 NN2 | P2 착수 시 |
| S-3 | 고정 UPDATE 가드 회귀 테스트(순수 SQL로 재현 어려움 — 주석으로 대체 가능) | PR #28 NN1 | 낮음 |
| S-4 | `ServerSeqBase` 위 seq 유일성은 `max(seq)+1`로 고침(완료). 동시 커밋 창도 T-S2 에서 닫았다 — `tasks.InsertServerEvent` 가 `pg_advisory_xact_lock((task, attempt))` 아래에서 seq 를 계산한다. 유실도 500 도 없다 | PR #22 N4 | 낮음 |
| S-5 | TaskEvent `object_ref`·`payload` 계약 정렬 완료(v0.4). `sentence` 렌더 폴백이 payload를 쓰는지 재확인 | PR #22 R2 | P2b 피드 5클래스 |

## D (데몬)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| D-1 | probe가 `colab --version`을 확인 — CLI가 없으면 MCP·셸 경로 둘 다 없는데 조용히 실패 | PR #20 NN1 | P2 초반 |
| D-2 | probe의 `resume`·`usage`·`tool_disallow`를 상수가 아니라 실측으로(E12-06 `usage=false` 경로) | PR #20 NN2 | P2 초반 |
| D-3 | `acpprobe`(스파이크 cmd) 제거 — `harness/acp`로 승격 완료 | PR #20 결함 6 | 정리 |
| D-4 | worktree·GC·`rebind_prepare`·예산 강제는 P3·P4 | PR #20 결함 7 | 단계대로 |
| D-5 | probe `capabilities[].supported_options` 채우기 — `(kind, adapter_version)` 표. claude_code 0.74.0: `effort` 허용 값. Hermes: 비움 | T-W2 계약 빈칸(harness v0.5) | **T-I2 전.** 비어 있으면 웹이 옵션 편집을 비활성으로 둔다 — 빈 채로 두면 S10이 사실상 죽는다 |

## W (웹)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| ~~W-1~~ | **해결 — T-W2.** `RuntimeCard` 가 새 키 7종을 읽고, **없는 능력은 결과와 함께** 말한다(`usage:false` → 비용이 추정치, `resume:false` → 재진입이 늘 콜드 스타트). probe 최상위 `colab_cli.present:false` 는 경고다 | PR #27 | — |
| ~~W-2~~ | **해결 — T-W2.** `TaskEventWire` 캐스팅 제거 + `object_ref` 를 문자열로만 읽는다(계약 v0.4) | PR #22 R2 | — |
| ~~W-3~~ | **해결 — T-W2.** `new_lane` 토글 + **전송 후 자동 해제**. 해제되지 않으면 이후 모든 멘션이 lane 을 새로 만들어 해소 규칙 3 이 죽으므로 컴포넌트 테스트로 고정했다 | PR #21 N2 | — |
| W-4 | `install_commands`의 서버 호스트(:8080 직접 vs :3000 프록시) 실서버 기준 확정 | PR #21 N6 | Integrator 결과로 |
| ~~W-5~~ | **해결 — T-W2.** PRD FR-1.3 4행대로 **`running` 만 `working`** 이다. `dispatched`·`preparing` 은 아직 턴이 시작되지 않았고, 그것을 working 으로 세면 데몬이 claim 만 하고 멈춰도 칩이 "작업 중"이라 침묵과 실행을 구분할 수 없다. 웹의 파생 함수와 목 저장소 둘 다 고쳤다 | PR #21 N7 | — |
| ~~W-6~~ | **해결 — T-W2.** 작성창이 `previewTriggers` 를 부르고 로컬 규칙 계산(`classifyMentions`)을 지웠다 — 규칙 1~8 과 lane 해소는 서버 상태를 봐야 해서 로컬로 흉내 내면 서버와 반대로 말한다(S-1 이 그랬다) | PR #21 R2 | — |

### S 추가 (G3 수정 리뷰에서)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| ~~S-6~~ | ~~SSE 응답에 `Cache-Control: no-cache, no-transform`~~ **해결 — T-S2**(`handlers_sessions.go` 스트림 헤더) | PR #34 NN1 | — |
| ~~S-7~~ | ~~`createWorkspace` 슬러그 유일 제약 재시도가 같은 트랜잭션 안 → 같은 이름 두 번째 워크스페이스가 `25P02` 500~~ **해결 — PR #43**(savepoint + 이름 해시 stem). 전수 조사도 그 PR에서 끝났다(재시도하던 곳은 `auth.go`·`runtimes.go` 둘뿐). 실기 확인: `plan/G3_DECISION.md` §3-1 | PR #34 NN2 → G3_DECISION S-6 | — |
| S-8 | 취소 흡수(`cancelRequested`)가 명령의 **존재**만 보고 `consumed_at`을 안 본다. 24h TTL 소비 후에도 흡수가 남을 수 있다 | PR #33 NN3 | 낮음 |
| ~~S-9~~ | **진단 정정 + 해결 — T-S2.** 유니크 제약은 0002(214-215)가 이미 `(task_id, attempt, seq)` 로 바꿨다 — 아래 진단의 전제가 낡았다. 남은 위험은 서버 발행 이벤트의 **동시 `max(seq)+1` 계산**이고, 자리는 셋이 아니라 **넷**이다(`NotePreviewDrift`·director 취소 노트·`router.Post` 상태 이벤트·`httpapi/commands.go` 명령 24h 만료 노트). `ON CONFLICT DO NOTHING` 은 충돌을 오류에서 **조용한 유실**로 바꾼 것이라 해결이 아니었다 — 네 자리를 `tasks.InsertServerEvent` 하나로 모으고 `pg_advisory_xact_lock((task, attempt))` 아래에서 seq 를 계산한다. 피드는 사람이 개입 여부를 판단하는 화면이라 노트 유실이 500 보다 나쁘다(FR-7.2). ~~서버 발행 task_event의 seq 계산이 attempt 스코프(`max(seq)+1 WHERE task_id AND attempt`)인데 유니크 제약은 `(task_id, seq)`(0001) → attempt 2의 첫 서버 이벤트가 attempt 1과 충돌. 피해는 피드 노트 1건 유실(heartbeat·취소는 안전) ~~ | PR #43 NN1 | — |
| ~~S-10~~ | ~~`auth.AcceptInvite` 동시 수락 TOCTOU → 500~~ **해결 — T-S2**(`ON CONFLICT (workspace_id, user_id) DO NOTHING`. 두 번 수락은 오류가 아니다) | PR #43 전수 조사 | — |
| ~~S-11~~ | **해결 — T-S2**(`validateLimit`, 범위 밖은 422. `07_adversarial.sh` D8 기대도 갱신). ~~요청 파라미터의 스키마 제약이 강제되지 않는다. `limit`은 계약상 `minimum:1 maximum:200`인데 서버는 `-1`·`0`·`999999`를 200으로 받고 **조용히 기본값 50으로 강제**한다(타입 오류만 422). 네 저장소(messages·agents·sessions·events)가 모두 clamp하므로 **자원 고갈 위험은 없다** — 계약↔구현 불일치이고, 500을 요청한 클라이언트가 50을 받고도 모른다 ~~ | Lead 적대적 검증 D8 | — |
| S-13 | `createProfile`·`updateProfile`이 `options`를 런타임 `supported_options` 밖이면 **422**(openapi L1053 규칙 — 광고할 키가 없어 지금까지 구현 불가였다). 빈 광고 = 허용 없음 | T-W2 계약 빈칸(harness v0.5) | T-S3 뒤 또는 T-I2 전 |
| ~~S-12~~ | **해결 — T-S2.** 두 operation 을 켜면서 owner·admin 게이트를 함께 넣었고, 루프 상한 0(상한을 조용히 끄는 값)도 422 로 막는다. `07_adversarial.sh` D2 기대를 403/404 로 좁혔다. ~~P2에서 authz를 반드시 넣을 것. 지금은 501이라 남의 워크스페이스 설정도 바뀌지 않지만, 501은 인가가 아니라 미구현이다. `e2e/p1/07_adversarial.sh` D2의 기대를 그때 `403/404`로 좁힌다 ~~ | Lead 적대적 검증 D2 | — |
| S-14 | **다운로드가 응답 끝까지 DB 트랜잭션(=풀 커넥션)을 잡는다.** large object 는 트랜잭션 안에서만 읽히므로 `artifacts.Open` → `io.Copy` 구조 자체는 대안이 없고 설계는 맞다. 문제는 그 옆이다 — `http.Server` 에 `WriteTimeout` 이 없고(`cmd/server/main.go` 는 `ReadHeaderTimeout` 만 준다) 풀도 기본 크기라, 느린 클라이언트 N 개가 커넥션 N 개를 무기한 점유한다. **P3 웹 다운로드를 켜기 전에** `WriteTimeout` 또는 다운로드 전용 컨텍스트 데드라인을 반드시 넣는다 | PR #65 NN1 | P3 전 필수 |
| S-15 | **`artifact_review` 의 `ON CONFLICT (artifact_id) DO UPDATE` 가 재리뷰로 이전 판정을 덮어쓴다.** 거절 사유는 `decision_id` 로 결정 기록에 남아 E6-04("아티팩트는 사라지지 않는다")는 지켜지지만, 행 자체에는 이력이 없다 — 같은 아티팩트를 reject 했다가 approve 하면 reject 가 행에서 사라진다. P3 리뷰 UI 가 이력(누가 언제 무엇을 뒤집었나)을 그리려면 PK 를 `(artifact_id, reviewed_at)` 류로 바꾸거나 별도 이력 테이블이 필요하다 | PR #65 NN3 | P3 리뷰 UI 설계 시 |

## C (CLI)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| C-1 | `/cli/context` 호출 시점("시작 시 1회" vs 필요 시) 문서와 구현 정렬 | PR #18 N3 | 문서 |
| C-2 | `colab-cli.md` §2.1 `--tail` ↔ `--limit` 표기 통일 | PR #18 N5 | 문서 |

## 계약·문서

| # | 항목 | 출처 |
|---|---|---|
| K-1 | EVAL 제안 행: E8-13 "finish가 non-nil runtime_session_ref를 저장하고 다음 claim resume에 실린다", E11-11 "claim은 세션 워크스페이스의 런타임에만 준다" | Integrator |
| K-2 | PRD §7 스키마 ↔ 계약 키 표기 통일(`runtime_session_ref` 키 이름 `runtime_kind`) | PR #26 |
| K-3 | `harness.md` §2.2 `preparing` heartbeat 비대상, §4.3 명령 소비 표 — 데몬 쪽 문서(`daemon/README`)에 반영 | PR #22 |
| K-4 | **P3로 미룸** — 자동 발행된 `user_approval`의 **취소 조건**. FR-2.2는 "나머지 조건이 모두 충족되면 자동 발행"만 정하고, 발행 뒤 조건이 다시 미충족이 되면(예: 아티팩트 철회) 이미 발행된 HITL이 어떻게 되는지 정의가 없다 — Director 인박스에 유효하지 않은 승인 요청이 남는다. P3 HITL 전이를 설계할 때 정한다. EVAL 행(E6-12 후보)은 그때 추가 | P2a Hermes |

## 테스트 자산 (P1에서 만든 것)

| 항목 | 내용 |
|---|---|
| `e2e/p1/01`~`06` | 실제 런타임 수직 슬라이스·kill -9·취소·U1 브라우저·초대·S12 |
| **`e2e/p1/07_adversarial.sh`** | 경계 항목 D1~D10(TaskToken 범위·워크스페이스 경계·501 표면·멱등키·미인증·SSE 인가·데몬 토큰·잘못된 입력·**아티팩트 제출/리뷰 경계**). **에이전트 턴 0** — 서버가 떠 있으면 언제든 돌릴 수 있다. P2에서 operation이 늘 때마다 여기에 행을 더한다 |
| CI `contracts` 잡 | openapi strict lint · task_event JSON Schema · 프로즈 키 스캔 · 서버·웹 생성물 드리프트 게이트 |

## 운영 (PLAN §10.7 되먹임)

- Hermes Reviewer가 잡은 결함 중 **통합에서만 드러나는 것**(payload 위치, CHECK 키, 워크스페이스 claim, **SSE 응답 압축**)이 넷 — 스트림 단위 테스트가 목 데이터로 초록이어도 계약 양쪽을 실기로 잇는 테스트가 필요. P2a 골든 테스트에 "계약 왕복" 항목 추가.
- 코디네이터 `/login`이 worker 세션을 전부 무효화 — 재로그인은 fan-out 사이에만.
- 한도: 4 worker 동시는 5시간 창을 20~30분에 소진. P2는 **동시 2개**로. worker는 `--model opus`(Fable 한도가 먼저 소진됨, 2026-09-06 Director 결정).
- Hermes의 `gh`·`git worktree` 호출이 승인 게이트에서 멈춘다 — 리뷰 결과 파일을 Lead가 게시하는 방식 유지, 임시 워크트리는 Lead가 정리.
