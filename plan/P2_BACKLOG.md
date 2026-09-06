# P2 백로그 — P1 리뷰·통합에서 이월된 항목

| 항목 | 내용 |
|---|---|
| 목적 | P1 PR 리뷰(Hermes)와 Integrator가 남긴 비차단 지적·후속을 한곳에 모은다. `plan/P2_TASKS.md`를 만들 때 스트림별 작업에 흡수한다 |
| 출처 | PR #18·#20·#21·#22·#25·#26·#28 리뷰 코멘트, `plan/G3_REPORT.md`(작성 중) |

## S (서버)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| S-1 | 라우터 규칙 3(`@all`·사람만 멘션 → 트리거 없음) — 지금은 규칙 6으로 떨어져 assignee task 생성. 웹 미리보기 칩("트리거 없음")과 서버가 반대로 말함 | PR #22 N1 | P2b 라우터 전체에 포함(E1-05·06) |
| S-2 | `session.runtime_id` ↔ `workspace_id` 일치를 DB에서 강제 — 복합 FK `session(workspace_id, runtime_id) → runtime(workspace_id, id)`(0005). `rebindSession`(FR-9.2)이 세 번째 고정 경로가 되므로 같은 가드 필수 | PR #28 NN2 | P2 착수 시 |
| S-3 | 고정 UPDATE 가드 회귀 테스트(순수 SQL로 재현 어려움 — 주석으로 대체 가능) | PR #28 NN1 | 낮음 |
| S-4 | `ServerSeqBase` 위 seq 유일성은 `max(seq)+1`로 고침(완료). 동시 커밋 창은 남음 — 트랜잭션 advisory lock 검토 | PR #22 N4 | 낮음 |
| S-5 | TaskEvent `object_ref`·`payload` 계약 정렬 완료(v0.4). `sentence` 렌더 폴백이 payload를 쓰는지 재확인 | PR #22 R2 | P2b 피드 5클래스 |

## D (데몬)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| D-1 | probe가 `colab --version`을 확인 — CLI가 없으면 MCP·셸 경로 둘 다 없는데 조용히 실패 | PR #20 NN1 | P2 초반 |
| D-2 | probe의 `resume`·`usage`·`tool_disallow`를 상수가 아니라 실측으로(E12-06 `usage=false` 경로) | PR #20 NN2 | P2 초반 |
| D-3 | `acpprobe`(스파이크 cmd) 제거 — `harness/acp`로 승격 완료 | PR #20 결함 6 | 정리 |
| D-4 | worktree·GC·`rebind_prepare`·예산 강제는 P3·P4 | PR #20 결함 7 | 단계대로 |

## W (웹)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| W-1 | S11 런타임 카드가 계약 v0.4.1 `RuntimeCapability` 새 키(adapter_version·protocol_version·resume·usage·tool_disallow·brief_transport·allow_once_missing)를 읽도록 | PR #27 | P2b S11 전체 |
| W-2 | `TaskEventWire` 캐스팅 제거 — 이제 생성 타입에 `payload`가 있다 | PR #22 R2 | 3줄 |
| W-3 | `new_lane` 토글(t-2) | PR #21 N2 | P2b 작성창 |
| W-4 | `install_commands`의 서버 호스트(:8080 직접 vs :3000 프록시) 실서버 기준 확정 | PR #21 N6 | Integrator 결과로 |
| W-5 | `working`에 드는 task 상태 집합(파생 상태 FR-1.3) — `dispatched`·`preparing`도 working인지 | PR #21 N7 | PRD 확인 후 |
| W-6 | 트리거 미리보기는 `previewTriggers`(P2 op)로 교체, 로컬 계산 제거 | PR #21 R2 | P2b |

### S 추가 (G3 수정 리뷰에서)

| # | 항목 | 출처 | 비고 |
|---|---|---|---|
| S-6 | **SSE 응답에 `Cache-Control: no-cache, no-transform`** — `compress:false`는 Next만 막는다. 배포의 nginx·CDN이 `text/event-stream`을 gzip 버퍼링하면 W-2가 그대로 재발한다. 서버가 스스로 말해야 한 곳에서 막힌다 | PR #34 NN1 | **배포 전 필수.** `handlers_sessions.go` 스트림 응답 헤더 |
| ~~S-7~~ | ~~`createWorkspace` 슬러그 유일 제약 재시도가 같은 트랜잭션 안 → 같은 이름 두 번째 워크스페이스가 `25P02` 500~~ **해결 — PR #43**(savepoint + 이름 해시 stem). 전수 조사도 그 PR에서 끝났다(재시도하던 곳은 `auth.go`·`runtimes.go` 둘뿐). 실기 확인: `plan/G3_DECISION.md` §3-1 | PR #34 NN2 → G3_DECISION S-6 | — |
| S-8 | 취소 흡수(`cancelRequested`)가 명령의 **존재**만 보고 `consumed_at`을 안 본다. 24h TTL 소비 후에도 흡수가 남을 수 있다 | PR #33 NN3 | 낮음 |
| S-9 | **서버 발행 task_event의 seq 계산이 attempt 스코프**(`max(seq)+1 WHERE task_id AND attempt`)인데 유니크 제약은 `(task_id, seq)`(0001) → attempt 2의 첫 서버 이벤트가 attempt 1과 충돌. 피해는 피드 노트 1건 유실(heartbeat·취소는 안전) | PR #43 NN1 | 두 자리 함께: `tasks/service.go` `NotePreviewDrift`·director 취소 노트. `ON CONFLICT (task_id, seq) DO NOTHING` 또는 seq에서 attempt 조건 제거 |
| S-10 | `auth.AcceptInvite` 동시 수락 TOCTOU → 500 | PR #43 전수 조사 | savepoint 또는 `ON CONFLICT` |
| S-11 | **요청 파라미터의 스키마 제약이 강제되지 않는다.** `limit`은 계약상 `minimum:1 maximum:200`인데 서버는 `-1`·`0`·`999999`를 200으로 받고 **조용히 기본값 50으로 강제**한다(타입 오류만 422). 네 저장소(messages·agents·sessions·events)가 모두 clamp하므로 **자원 고갈 위험은 없다** — 계약↔구현 불일치이고, 500을 요청한 클라이언트가 50을 받고도 모른다 | Lead 적대적 검증 D8 | oapi-codegen 검증 미들웨어 또는 핸들러에서 범위 밖이면 422. P2에서 파라미터가 늘기 전에 |
| S-12 | **P2에서 `updateWorkspaceSettings`·`getWorkspaceSettings`를 구현할 때 authz를 반드시 넣을 것.** 지금은 501이라 남의 워크스페이스 설정도 바뀌지 않지만, 501은 인가가 아니라 미구현이다. `e2e/p1/07_adversarial.sh` D2의 기대를 그때 `403/404`로 좁힌다 | Lead 적대적 검증 D2 | owner·admin만(SCREEN §2.3) |

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

## 테스트 자산 (P1에서 만든 것)

| 항목 | 내용 |
|---|---|
| `e2e/p1/01`~`06` | 실제 런타임 수직 슬라이스·kill -9·취소·U1 브라우저·초대·S12 |
| **`e2e/p1/07_adversarial.sh`** | 경계 37항목(TaskToken 범위·워크스페이스 경계·501 표면·멱등키·미인증·SSE 인가·데몬 토큰·잘못된 입력). **에이전트 턴 0** — 서버가 떠 있으면 언제든 돌릴 수 있다. P2에서 operation이 늘 때마다 여기에 행을 더한다 |
| CI `contracts` 잡 | openapi strict lint · task_event JSON Schema · 프로즈 키 스캔 · 서버·웹 생성물 드리프트 게이트 |

## 운영 (PLAN §10.7 되먹임)

- Hermes Reviewer가 잡은 결함 중 **통합에서만 드러나는 것**(payload 위치, CHECK 키, 워크스페이스 claim, **SSE 응답 압축**)이 넷 — 스트림 단위 테스트가 목 데이터로 초록이어도 계약 양쪽을 실기로 잇는 테스트가 필요. P2a 골든 테스트에 "계약 왕복" 항목 추가.
- 코디네이터 `/login`이 worker 세션을 전부 무효화 — 재로그인은 fan-out 사이에만.
- 한도: 4 worker 동시는 5시간 창을 20~30분에 소진. P2는 **동시 2개**로. worker는 `--model opus`(Fable 한도가 먼저 소진됨, 2026-09-06 Director 결정).
- Hermes의 `gh`·`git worktree` 호출이 승인 게이트에서 멈춘다 — 리뷰 결과 파일을 Lead가 게시하는 방식 유지, 임시 워크트리는 Lead가 정리.
