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
| ~~D-5~~ | probe `capabilities[].supported_options` 채우기 — `(kind, adapter_version)` 표. claude_code 0.74.0: `effort` 허용 값. Hermes: 비움 | T-W2 계약 빈칸(harness v0.5) | **T-I2 전.** 비어 있으면 웹이 옵션 편집을 비활성으로 둔다 — 빈 채로 두면 S10이 사실상 죽는다 | **해결 — PR #121**
| ~~D-6~~ | 데몬 `recordUsage`가 `cost_usd`를 한 번도 대입하지 않고 `0 + estimated:false`를 보낸다(ACP `Usage`에 비용 필드 없음). harness v0.7: 비용을 모르면 **`cost_usd` 생략 + `estimated:true`**. `Estimated`가 `pr.Usage == nil`일 때만 켜지는 것을 고쳐라 | G4 3판 W16 (PR #87 리뷰 §2) | P3 비용 화면(E9-07) 전 | **해결 — PR #93.**
| ~~D-7~~ | **G5 (b) 차단** — Hermes ACP 어댑터가 `mcpServers`를 무시하고(initialize에 `mcpCapabilities` 없음) 셸 도구를 위생화된 env로 띄워 `COLAB_*`·`PATH`가 안 내려간다 → Hermes 에이전트가 플랫폼에 말할 수단이 없다(메시지 0, status 0). harness v0.8: attempt별 래퍼 `<workdir_root>/.colab/bin/<task>.<attempt>/colab` + 브리프 [2] 절대 경로 + `tool_surface` 광고 | G5 T-I2 2부 escalation | **G5 전** | **해결 — PR #97**(`internal/toolwrap`, 실기 스모크로 위생화 셸에서 래퍼 동작 확인).
| ~~D-8~~ | `toolwrap.cliRe`가 명령 위치의 `colab <소문자 서브커맨드>`만 잡고 `colab --flag`(예 `` `colab --version` ``)는 치환하지 않는다. 지금 브리프·프롬프트의 명령은 전부 서브커맨드라 실해 없음; `([a-z]|-)`로 넓히거나 계약에 "서브커맨드 또는 플래그" 명시 | PR #97 리뷰 NN2 | 낮음 | **해결 — PR #121**
| ~~D-9~~ | `daemon/internal/acpfake` 가 내부 패키지라 server 모듈의 시뮬레이터(`server/test/sim`, P3a #108)가 임포트 못 한다 — 테스트 로컬 `replayAttempt` 로 대체 중. 공개 패키지(`daemon/acpfake`)로 이동하면 시뮬레이터가 실제 ACP 대역으로 돈다 | P3a PR #108 질문 1 | T-D5 | **해결 — PR #121**
| D-10 | `isSessionGone` 의 `not found` 부분일치가 넓다(`cwd not found` 류도 유실로) — 보수적 방향이라 무해; 어댑터 코드 안정화 뒤 `-32002` 만 | PR #111 리뷰 NN1 | 낮음 |
| ~~D-11~~ | 유실 이벤트(`runtime.resume outcome=cold_start`)에 원 rpc 코드·메시지 한 칸 — S7 피드에서 "왜 콜드 스타트인지" | PR #111 리뷰 NN2 | T-D5 | **해결 — PR #121**
| ~~D-12~~ | hermes `sessionProvenance: {}`·빈 `acpSessionId` 는 reason 이 `provenance_mismatch` 로 남는다(결과는 같은 cold_start) — `no_provenance` 로 | PR #115 리뷰 NN1 | T-D5 | **해결 — PR #121**
| ~~D-13~~ | resume 직후 첫 prompt 가 `stopReason=refusal` 로 편집·게시 0 으로 끝나면 task 가 `completed` 가 된다(§2.2 가 refusal 을 성공으로 읽음) — Lead 결정: 콜드 스타트 1회 재시도, 재시도도 refusal 이면 `failed(other)` + 사유 이벤트 | 스파이크 4c §3 | T-D5 | **해결 — PR #121**
| ~~D-14~~ | daemon-protocol v0.7 이후 `api.Command` 래퍼를 `contracts.Command` 로 접는 후속 정리(T-D5 가 남김) | PR #121 | 다음 데몬 작업 | **해결 — PR #129**
| ~~D-15~~ | `closeProcess()` 를 `emitStep(5)` 없이 부르는 경로가 생기면 취소 순서 골든이 못 잡는다 — `closeProcess` 안에서 5단계 이벤트를 내게 묶기 | PR #121 리뷰 NN1 | 낮음 | **해결 — PR #129**
| ~~D-16~~ | `budgetLimit` 우선순위가 `Task.BudgetOverrideUSD > Limits.BudgetUSD > Task.BudgetUSD` — Lead 결정: 유효 예산 = **min(override 또는 task 예산, 세션 잔여)**. harness §4.4 문언 보강(Lead) + 데몬 반영 | PR #121 리뷰 NN3 | 다음 데몬 작업 + 계약 | **해결 — PR #129**

## W (웹)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| ~~W-1~~ | **해결 — T-W2.** `RuntimeCard` 가 새 키 7종을 읽고, **없는 능력은 결과와 함께** 말한다(`usage:false` → 비용이 추정치, `resume:false` → 재진입이 늘 콜드 스타트). probe 최상위 `colab_cli.present:false` 는 경고다 | PR #27 | — |
| ~~W-2~~ | **해결 — T-W2.** `TaskEventWire` 캐스팅 제거 + `object_ref` 를 문자열로만 읽는다(계약 v0.4) | PR #22 R2 | — |
| ~~W-3~~ | **해결 — T-W2.** `new_lane` 토글 + **전송 후 자동 해제**. 해제되지 않으면 이후 모든 멘션이 lane 을 새로 만들어 해소 규칙 3 이 죽으므로 컴포넌트 테스트로 고정했다 | PR #21 N2 | — |
| W-4 | `install_commands`의 서버 호스트(:8080 직접 vs :3000 프록시) 실서버 기준 확정 | PR #21 N6 | Integrator 결과로 |
| ~~W-5~~ | **해결 — T-W2.** PRD FR-1.3 4행대로 **`running` 만 `working`** 이다. `dispatched`·`preparing` 은 아직 턴이 시작되지 않았고, 그것을 working 으로 세면 데몬이 claim 만 하고 멈춰도 칩이 "작업 중"이라 침묵과 실행을 구분할 수 없다. 웹의 파생 함수와 목 저장소 둘 다 고쳤다 | PR #21 N7 | — |
| ~~W-6~~ | **해결 — T-W2.** 작성창이 `previewTriggers` 를 부르고 로컬 규칙 계산(`classifyMentions`)을 지웠다 — 규칙 1~8 과 lane 해소는 서버 상태를 봐야 해서 로컬로 흉내 내면 서버와 반대로 말한다(S-1 이 그랬다) | PR #21 R2 | — |
| W-3′ | mock previewTriggers가 `done/blocked` lane **재진입**을 `resolution 4 + lane_id + reentry:true`로 준다(`handlers.ts:571-573`). PRD lane 규칙·EVAL E2-04·05는 재진입을 **규칙 3**으로 두고 4는 "그 외 → 새 lane". §0-9(b) 부류 — mock 응답·p2-mock 기대값·재진입 테스트 함께 | PR #76 Lead 확인 | 다음 웹 작업 |
| W-5 | mock의 lane 해소 규칙(`handlers.ts` resolveLane류)을 지키는 것이 `web/e2e/p2-mock.sh`뿐이고 그 스모크는 CI 밖(mock 서버 필요)이다. `done` lane 있는 세션에서 preview → `resolution 3 · reentry true`를 vitest 1건으로 — W-2·W-3′ 부류가 다시 슬며시 바뀌어도 CI가 모른다 | PR #83 리뷰 NN1 | 다음 웹 작업 |

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
| ~~S-16~~ | `listParticipants`(x-phase **P2**)가 아직 501. 웹은 세션 상세의 `participants`를 써서 G4 2판을 막지 않지만, T-S2가 "P2 op 전부"를 받고 남긴 마지막 하나 | T-S4(PR #75) 남김 | 다음 서버 작업 | **해결 — PR #95.**
| S-17 | `tasks.Service.LanePublish` 훅이 `nil`이면 **조용히** 발행을 건너뛴다(`tasks/service.go:585`). 프로덕션 배선은 `httpapi/server.go:89` 한 곳이고 `TestClaimPublishesLaneRunning`이 누락을 잡지만, 다른 바이너리 조립(워커·CLI 임베드)에서는 조용히 빠질 수 있다 → `nil`이면 `slog.Warn("tasks: LanePublish unwired")` 한 줄 | PR #78 리뷰 NN1 | 낮음 |
| S-18 | PR #78 본문의 "lane.updated 발행 20곳"은 status 전이(15: UPDATE 12 + INSERT 3)에 카드 변경 자리(phase 보고 등 5)를 더한 정의다. 다음 대조가 15와 20을 다시 맞추지 않도록 정의를 코드 주석(`tasks.publish`)에 명시 | PR #78 리뷰 NN2 | 문서 |
| S-19 | 비용 롤업(`tasks.Finish` 커밋 뒤 별도 tx, `SUM(task_usage)`)이 실패하면 데몬 재시도가 `finished != nil` 멱등 경로로 빠져 `costed`가 안 켜지고 그 attempt의 롤업이 다시 돌지 않는다 — 세션의 마지막 finish면 `session.cost_usd`가 영구 뒤처짐. DB 장애 외 도달 불가라 비차단. 후보: 멱등 경로에서도 롤업(SUM이라 무해) 또는 `getSessionCost`가 SUM을 직접 읽기. (S-17의 nil 훅 로그는 `ParticipantPublish`에도 적용) | PR #85 리뷰 NN1·NN2 | 낮음 |
| ~~S-20~~ | 비용 롤업(#85)이 `estimated` 행을 **워크스페이스 가격표 × 토큰**으로 채우고 세션 비용에 추정 배지를 단다(harness v0.7: 가격표는 워크스페이스 소유, 추정은 서버). 가격표 스키마·기본값은 PRD §8.2.6 | G4 3판 W16 | P3 비용 화면 전, D-6과 함께 | **해결 — PR #95**(`internal/cost`, `pricing_overrides` 우선, 0011 `task_usage.model`).
| S-21 | `cost.Defaults`에 Claude 계열 단가만 있다. Hermes가 비용을 안 주는 경로면 모델 미상 → 배지만 켜지고 $0. G5 Hermes 실기 전에 그 모델들 단가 또는 "Hermes는 자체 보고" 확인 | PR #95 리뷰 NN1 | G5 |
| S-22 | `cost.Load`가 settings 행 없음·override JSON 불량을 모두 빈 표로 삼킨다 — finish를 500으로 안 만드는 의도는 맞으나 관리자가 알 길이 없다. 로그 한 줄 | PR #95 리뷰 NN2 | 낮음 |
| S-23 | `rollUpCost`의 `repriceEstimates`가 매 finish마다 세션 전체 `task_usage`를 훑는다. 지금은 무해; P4 GC 전에 `WHERE estimated AND (cost_usd = 0 OR updated_at < settings.updated_at)` 류로 좁힐 것 | PR #95 리뷰 NN3 | P4 전 |
| ~~S-24~~ | `createAgent` 가 `AgentProfileCreate.fallback_profile(_id)` 를 INSERT 에서 조용히 버린다; `createAgentProfile`·`updateAgentProfile` 은 x-phase P2 인데 501 → E8-08 폴백 연결을 DB 로 우회 | G5 보고서 §3.3 (PR #100) | G5 hotfix(서버, 진행 중) | **해결 — PR #103**
| ~~S-25~~ | 종료 조건 `user_approval` 을 채울 HTTP 입구가 없다 — `director_approve` 이벤트는 있으나 호출자 없음. **Lead 결정: 계약 PR #101 로 `respondHitlRequest` 의 플랫폼 발행 approval 승인·거절을 P2 로** | G5 §2.2 | G5 hotfix | **해결 — PR #103**
| ~~S-26~~ | `updateWorkspaceSettings` 의 `mergeJSON` 이 merge 가 아니라 replace — 한 키만 보내면 같은 객체의 다른 키가 null | G5 §5 | G5 hotfix | **해결 — PR #103**
| ~~S-27~~ | `blocked_q` 카드에 위임자 멘션이 없어 K3 배지가 `질문 → @위임자` 대신 `질문` | G5 §4.4, 계약 #101 | G5 hotfix | **해결 — PR #103**
| ~~S-28~~ | 위임자 즉시 기상 시스템 메시지가 카드 id·본문을 인용하지 않고(E3-05 (3)), 답글에 자식 멘션이 필요하다는 안내가 없다(규칙 4) | G5 §4.1·§4.3, 계약 #101 | G5 hotfix | **해결 — PR #103**
| ~~S-29~~ | 세션 완료 시 서버가 `gc` 명령을 내지 않아 격리 `none` workdir 가 남는다(E6-03) | G5 §2.3 | G5 hotfix | **해결 — PR #103**
| ~~S-30~~ | 템플릿 매핑 실패 시 프로파일 없는 에이전트가 남고 P2 에 프로파일을 붙일 op 가 없다(S-24 와 함께 닫힘) | G5 §6.4 | G5 hotfix | **해결 — PR #103**
| ~~S-31~~ | `afterLaneDone` 이 `reentry > 0` 이면 `notifyReentry` 로 빠져 `maybeFireJoin` 을 안 부른다 — 재진입 lane 이 그룹의 마지막으로 끝나면 합류(FR-6.5)가 영영 발화하지 않는다 | G5 §4.3.1 (세션 f80b092b) | G5 hotfix — 조용한 손실, 우선 1순위 | **해결 — PR #103**
| S-32 | `updateWorkspaceSettings` 의 '명시 null = unset' 주석과 실제가 다르다 — 생성 타입이 `*int omitempty` 라 null 이 생략과 같아 키를 지울 수 없다(오동작 아님). `nullable.Nullable` 로 바꾸거나 주석 정정 | PR #103 리뷰 NN1 | 낮음 |
| S-33 | 승인(S-25) 후 `session_completed` 인박스 행 단언이 테스트에 없다 — `listInbox` 가 P3 라 HTTP 관측 불가지만 `inbox` 테이블 count 로 고정 가능 | PR #103 리뷰 NN2 | 낮음 |
| S-34 | `gcWorkdirs` 가 `runtime_id IS NULL` 이면 조용히 반환 — `none` 격리는 도달 불가지만 로그 한 줄 | PR #103 리뷰 NN3 | P4 GC |
| S-35 | `daemon_command.delivered_at` 을 프로덕션 코드가 어디서도 채우지 않는다(대입 0곳) — 명령은 claim·events·heartbeat 응답으로 전달되고 데몬이 받지만(재측정에서 gc 가 `command gc ignored (P4)` 로 찍힘) 서버 기록은 영원히 NULL 이라 '최소 한 번' 전달(E11-05)·재전달 판단을 증명할 수 없다. 보고서 §10.3 의 '유휴 데몬 전달 불가'는 틀린 진단(#105 리뷰 NN1) | G5 재측정 §10.3 | P3 첫 서버 작업 |
| S-36 | 재개 프롬프트의 "이미 게시한 메시지" 가 UUID 뿐이라 에이전트가 대조할 수 없다(히스토리 줄에 id 없음) — 재게시 0 은 workdir 덕. `id — 앞 80자` 로 렌더 + 히스토리 줄에 id | 스파이크 4c §5-1 (PR #117) | T-S5 |
| S-37 | 브리프에 §8.4 의 [3] coordination·[6] Context·[7] Decision Log 가 없다(`bundle.go` 주석 "P2" 인데 P2 에서 안 만들어짐) — decision 테이블·기록 op 는 있는데 읽는 쪽이 없다. HITL 답변·승인 여부는 `<resumed>` 에 | 스파이크 4c §5-2 | T-S5 (시나리오 C 전) |
| S-38 | `historyLimit = 30` 인데 EVAL E8-12 는 50 — 상수를 50 으로, 설정화는 P4 | 스파이크 4c §5-4 | T-S5 |
| S-39 | `lane.runtime_session_ref` 가 finish 에서만 저장돼 크래시한 attempt 는 resume 자원이 없다(다음 attempt 는 항상 콜드 스타트). 실측상 콜드 스타트 성적이 같아 고치지 않음 — 세션 생성 직후 ref 를 heartbeat 에 싣는 것은 §4.2 계약 변경 | 스파이크 4c §5-5·§0.2 | P4, 알고 있기 |
| S-40 | 사소: 턴 프롬프트 영어 · `failure_kind` 원문 노출 · `none` 격리에서 `git status` 문구 | 스파이크 4c §5-6 | 낮음 |
| S-41 | 서버가 `contracts/task_event.schema.json` 을 어긴 task_event(닫힌 enum·additionalProperties:false 위반)를 **200 으로 받는다** — T-D5 첫 구현이 5곳 어겼는데 아무도 몰랐다(데몬은 memSink 검사로 자기 방어). 서버 ingest 에 스키마 검증(422 + 피드) | PR #121 자기 정정 | T-S5 후속 또는 P4 |
| S-42 | 취소 골든(`tasks/cancel_golden_test.go`)의 5단계(`signal_process_group`) 순서 단언이 단계 **부재**를 참으로 둔다(index -1 → `signal < drain` 공허, `ImmediateKill=false`) — 1~4단계는 부재를 FAIL 로 잡는데 5단계만 구멍. 데몬 사본은 §0-8 로 못 고쳐 별도 테스트(#129)로 막았다. 골든 저자(Reviewer)가 원본 수정 | PR #129 (T-D6 발견) | 리뷰어 후속 |
| S-43 | `listInbox` 항목의 `SessionRef.status` 가 빈 문자열 — openapi required 인데 서버가 id·title 만 채운다 | T-W3 PR #130 관찰 | #124 재작업에 포함 지시 |

## C (CLI)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| ~~C-1~~ | `/cli/context` 호출 시점("시작 시 1회" vs 필요 시) 문서와 구현 정렬 | PR #18 N3 | **해소(T-C4)**. 계약이 v0.4 §1 에서 "필요할 때 호출하고 프로세스 안에서 캐시(프로세스당 최대 1회)"로 정리됐고 구현이 그대로다 — 회귀 `TestCliContextFetchedAtMostOncePerProcess`·`TestArtifactGetDoesNotFetchCliContext`, HITL 경로는 `TestHitlIsOneRequest`(E7-04 를 낡은 컨텍스트 캐시로 대신 판정하지 않는다) |
| ~~C-2~~ | `colab-cli.md` §2.1 `--tail` ↔ `--limit` 표기 통일 | PR #18 N5 | **해소(T-C4)**. 계약 v0.4 §2.1 이 `--limit` 로 통일됐고 CLI 에 `--tail` 은 없다(도움말 포함). `TestUsageTextAdvertisesOnlyRealFlags` 가 도움말과 실제 플래그의 어긋남을 계속 잡는다 |
| ~~C-3~~ | CLI 버전이 `var version = "dev"`이고 빌드 어디에도 `-ldflags -X main.version`이 없다. probe의 `versionRe`가 출력에서 먼저 걸리는 **contracts 버전**(0.1.0)을 `colab_cli.version`으로 싣는다 — `present` 판정은 정상이나 S11 카드가 "colab CLI 0.1.0"을 보인다. 배포 빌드(Makefile)에 ldflags 를 넣고 CLI 버전이 왼쪽에 오게 | PR #71 리뷰 NN1 + hotfix 워커 | **해소(T-C4)**. Makefile `COLAB_VERSION ?= 0.3.0` → `go build -ldflags "-X main.version=$(COLAB_VERSION)"`, **그리고 기본값도 `0.3.0-dev`** — `go build` 만 한 바이너리도 probe 에 x.y.z 를 준다(`"dev"` 가 x.y.z 가 아니었던 것이 근본 원인이다). CLI 버전은 여전히 출력 왼쪽. 회귀 `TestVersionFirstMatchIsTheCLIVersion` 이 probe 와 같은 정규식으로 첫 매치를 검사한다 |

## 계약·문서

| # | 항목 | 출처 |
|---|---|---|
| K-1 | EVAL 제안 행: E8-13 "finish가 non-nil runtime_session_ref를 저장하고 다음 claim resume에 실린다", E11-11 "claim은 세션 워크스페이스의 런타임에만 준다" | Integrator |
| K-2 | PRD §7 스키마 ↔ 계약 키 표기 통일(`runtime_session_ref` 키 이름 `runtime_kind`) | PR #26 |
| K-3 | `harness.md` §2.2 `preparing` heartbeat 비대상, §4.3 명령 소비 표 — 데몬 쪽 문서(`daemon/README`)에 반영 | PR #22 |
| K-4 | **P3로 미룸** — 자동 발행된 `user_approval`의 **취소 조건**. FR-2.2는 "나머지 조건이 모두 충족되면 자동 발행"만 정하고, 발행 뒤 조건이 다시 미충족이 되면(예: 아티팩트 철회) 이미 발행된 HITL이 어떻게 되는지 정의가 없다 — Director 인박스에 유효하지 않은 승인 요청이 남는다. P3 HITL 전이를 설계할 때 정한다. EVAL 행(E6-12 후보)은 그때 추가 | P2a Hermes |
| K-5 | **G5 전 결정** — 규칙 8 억제가 **lane 상태**에 묶여 있어, 자식 lane 이 `done` 된 뒤 같은 lane 이 한 줄 더 쓰면(재진입) 위임자가 다시 깨어난다. E1-17 문언("억제 기간은 합류 발화 전까지")대로이지만 FR-6.5 "합류는 정확히 한 번"과 맞물리면 위임자가 합류 뒤 자식 한 줄마다 깨어난다. 억제 해제 시점을 "합류 발화"가 아니라 "위임자가 그 lane 에 다시 지시"로 둘지 결정 필요 | G4_REPORT §5 관찰 1 (PR #73 리뷰 NN3) | G5 전 |
| K-6 | 인박스 `mention` 항목의 `actions` 가 COMPONENTS §2.4 표와 다르다 — 서버 쪽이 맞아 보이므로 문서(COMPONENTS) 수정 후보. Lead 판정 | T-W3 PR #130 관찰 | 문서 |

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
