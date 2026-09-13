# G9 판정 자료 — 출시 판정: 시나리오 A·B·C·D CI 초록 + §11 대시보드 (초안, T-I5)

| 항목 | 내용 |
|---|---|
| 상태 | **초안(2026-09-14, T-I5 PR #206).** 판정은 Lead 의 `plan/G9_DECISION.md`. G8(F1 실측)은 별도 — `plan/G8_PLAN.md` |
| 게이트 | `PLAN.md` §6.2 **G9**: 시나리오 A·B·C·D E2E **CI 에서** 초록 + §11 대시보드에서 지표 10개를 그대로 읽을 수 있음 + 컷 발동 여부 최종 판정 |
| 빌드 | dev `85e30e5`(#204 T-D12 S-66 고침 · #200 T-S12 지표 · #203 harness v0.8.9 포함) 위에 PR #206 |
| 방법 | 페이크 런타임(`e2e/p5/README.md` "왜 페이크인가"): 서버·데몬·CLI·DB·웹은 실물, 모델 자리만 acpfake 대본. 실기 대조는 §5(S-66) 와 G4~G7 의 실기 판정 |

## 1. CI 초록 증거

- **run**: https://github.com/ingki3/agent-collabortion/actions/runs/34765757987 (PR #206, head `896ca04`) — `e2e` job **success**, 4m55s(상한 15분). 다른 job(contracts·go·web)도 초록.
- job 안 표(`ci-summary.tsv`, 아티팩트 `e2e-p5-out`):

| 스크립트 | 결과 | PASS/FAIL | 시간 | 재는 것 |
|---|---|---|---|---|
| `72_scenario_a` | PASS | 46/0 | 7s | 위임 3 → lane 3 병렬 → 합류 1회 → Writer HITL(default) → 답 → resume attempt 2 → 아티팩트 ≥ 3000B → `user_approval` HITL → 승인 → completed → 요약 1 |
| `73_scenario_b` | PASS | 48/0 | 10s | worktree · 브랜치 · diff 아티팩트 2 · QA 번들 남의 경로 0 · 반려 → 기존 lane 재진입(resume=true) · v2 · QA 재기동 · completed · §8.4 위생 · 편집/셸 카드 |
| `74_scenario_c` | PASS | 42/0 | 35s | 실행 중 메시지 → queued(kill 0·취소 0) · restartLane(attempt 1·restarted_from·<resumed> 0) · cancelLane(failed·"사람이 중단함"·프로세스 0) · 결정 기록이 콜드 스타트 브리프 [7] 에 |
| `75_scenario_d` | PASS | 22/0 | 6s | hermes 프로파일 실패(other) → claude_code 폴백 · workdir 재사용 · 아티팩트 같은 workdir · 대안 없음 → 재큐잉 + run_failed 알림 |
| `76_perf` | PASS | 9/0 | 126s | §2 |
| `77_security` | PASS | 43/0 (N/A 2) | 37s | §3 |
| `78_web_s7` | PASS | 4/0 (N/A 1) | 0s | next build + 앱 셸 200 + `/api/v1` 프록시 200 (DOM·스크린샷은 로컬 agent-browser: 7/0, `web/__screenshots__/p5-78-s7.png`) |

로컬(맥, docker Postgres) 같은 명령 `bash e2e/p5/ci.sh`: 7/7 PASS, 239s.

**페이크가 재지 않는 것**(정직하게): 모델의 판단·실기 어댑터의 이벤트 모양·resume 신뢰도·비용. 시나리오 A 는 실기로도 돌렸다(§5, `RUNTIME=real` 46/0 · 120s).

## 2. 성능 (PRD §9)

`76_perf.sh` — 페이크 런타임(모델 0회). 시각은 서버 DB 단일 클럭.

### 2.1 지연 100회 (게시 → claim · 첫 출력), 격리 none, 데몬 1대

| 지표 | 목표 | CI(ubuntu-latest) | 로컬(맥) |
|---|---|---|---|
| 게시 → claim **p50** | < 2s | **0.005s** (p95 0.017) | 0.008s (p95 0.008) |
| 게시 → 첫 출력 p50 | < 10s | **0.034s** (p95 0.094) | 0.038s (p95 0.047) |
| 게시 → 답글 p50 | — | 0.057s | 0.071s |

페이크 턴이라 "첫 출력" 은 어댑터 spawn + 프롬프트 왕복만 잰다(실기는 npx 콜드 스타트 + 모델 ≈ 5~15s, G4 실측). §9 의 상한은 **플랫폼 몫**에 대해 여유가 두 자릿수다.

### 2.2 동시 task 50 (워크스페이스 1 · 데몬 5대 × capacity 10 · 세션 50 동시 생성 · 턴 4s)

| 지표 | CI | 로컬 |
|---|---|---|
| 동시 running 최대 | **50 / 50** | 50 / 50 |
| 런타임 분산 | 5 / 5 | 5 / 5 |
| 부하 중 claim p50 / p95 | 0.003 / 0.004s | 0.004 / 0.004s |
| 완료(생성→finish) p50 / p95 | 4.10 / 4.13s (턴 4s 포함) | 4.12 / 4.13s |
| API `GET /sessions/{id}` p50 / p95 (부하 중 40회) | 0.004 / 0.041s | 0.006 / 0.020s |
| DB 커넥션 최대(client backend) | 5 (idle 5) | 10 (idle 7) |
| 재큐잉(attempt > 1) | **0** | 0 |
| 이중 게시(task 당 에이전트 메시지 > 1) | **0** | 0 |
| 세션 50 생성 | 1.5s | 1.5s |

발견(결함 아님, 설계 확인): 한 에이전트가 50 세션을 맡으면 **에이전트 상한(`max_concurrent_tasks` 기본 3)** 이 먼저 걸린다(첫 실행 동시 3). §9 의 "워크스페이스당 50·데몬당 10" 은 런타임·워크스페이스 층이고, 에이전트 층은 FR-6.3 의 별개 상한이다 — 76_ 은 그 값을 50 으로 올려 잰다.

## 3. 보안 (PRD §9 보안 행)

`77_security.sh` 43/0 · N/A 2. 사람 셋(Director=owner · 초대 멤버 · 외부인) + 에이전트 4.

| 항목 | 판정 | 근거 |
|---|---|---|
| originator 체인 보존 | ✅ | 멤버가 건 task 의 `originator_user_id` = 멤버, 그 task 가 위임한 자식 task 도 멤버(Director 로 상승 없음) |
| task 토큰으로 사람 op | ✅ 전부 401/403 | completeSession · pauseSession · cancelLane · updateWorkspaceSettings · createSession |
| 멤버 쿠키 cancelLane | ✅ 403 (E10-05) | |
| 멤버가 `respond_to=owner` 에이전트 초대 | ✅ 403, Director 는 201 (FR-1.9 originator 사다리) | |
| 토큰 범위 — 다른 세션 postMessage/listMessages/submitArtifact | ✅ 403/404 | |
| 토큰 범위 — 다른 task setTaskStatus | ✅ 403 | |
| **finish 뒤** 토큰 | ✅ 401 | attempt 전용 |
| **revoke(취소) 뒤** 토큰 | ✅ 401 (E11-04) | getCliContext · postMessage |
| 마스킹 — 설정 권한 | ✅ 멤버 403 · owner 200 | |
| 마스킹 — summary(출력)·command(인자)·diff 본문 | ✅ 저장 안 됨, `[마스킹됨 · N자]` + 경로·첫 토큰 유지, OFF 대조군은 저장 | |
| 마스킹 — `payload.title` | ⚠️ **N/A(신규 결함)** | title 에 셸 명령 전체가 그대로 남는다. 실기 어댑터(claude-agent-acp)도 title = 명령 전체(`"title": "ls -la \| grep manual"`, G6 DB 실측) — 마스킹이 인자를 지우는 의미가 없어진다 |
| 데몬 토큰으로 사람 op | ✅ 401/403 | getSession · completeSession · postMessage · listInbox · createAgent (대조군: 자기 런타임 claim 200) |
| SSE 격리 | ✅ | 외부인의 A 스트림 403 · B 스트림에 A 세션 id·워크스페이스 id·메시지 본문 0건 (A 스트림 대조군에는 흐른다) |
| 위임↔합류 사이클과 루프 상한 | ⚠️ **N/A(신규 결함)** | 위임자가 합류 통보로 깨어나 다시 위임하는 사이클 8회에 세션이 active 그대로 — `max_pair_roundtrips=5` 를 넘어도 `paused(loop)` 가 없다. 첫 실행(상한 없이)에서 **70초에 529회**. `delegateLane` 과 합류 wake(`router/status.go`) 가 `CheckLoopLimits`(postMessage 경로) 를 타지 않는다 |

## 4. §11 대시보드 실값 (`getWorkspaceMetrics`, #200)

실측 스택에서 그대로 읽었다(워크스페이스 단위). 두 워크스페이스 — 페이크 시나리오 A(CI 와 같은 대본) · 실기 시나리오 A(§5 run1).

| key | 목표 | 페이크 A | 실기 A run1 | n |
|---|---|---|---|---|
| f1_minutes | < 15 | 0.10 | **2.0** | 1 |
| auto_complete_rate | > 0.6 | 1.0 | 1.0 | 1 |
| hitl_response_minutes | < 30 | 0.04 | 0.18 | 2 |
| delegation_autonomous_rate | > 0.7 | 0.75 | 0.75 | 4 |
| parallel_wallclock_reduction | > 0.4 | **−4.97** | 0.12 | 1 |
| task_success_rate_by_runtime | cc > 0.95 · 기타 > 0.85 | claude_code 1.0 (n7) · hermes null | claude_code 1.0 (n7) · hermes null | |
| duplicate_after_resume_rate | < 0.01 | 0 | 0 | 1 |
| resume_success_rate | > 0.9 | 1.0 | 1.0 | 3 |
| blocked_response_minutes | < 5 | null | null | 0 |
| weekly_active_sessions | > 5 | 1 | 1 | 1 |

읽을 때 주의: 열 개가 **모두 읽힌다**(G9 조건 충족). `parallel_wallclock_reduction` 은 페이크에서 음수 — 정의(1 − 전체/합)에서 "전체" 에 사람 대기(HITL 답·승인)가 들어가는데 턴이 0.2s 면 대기가 지배한다. 실기에서는 0.12(목표 0.4 미달 — 시나리오 A 는 Writer 가 순차라 원래 낮다). `delegation_autonomous_rate` 0.75 는 Writer 의 HITL 질문 1건(4건 중) — 시나리오가 그렇게 만든 값이다.

## 5. S-66 전후 대조 (실기, 로컬 맥, claude_code haiku)

배경: S-66(집필 단계 3분 stall) 은 T-D12(#204) 가 원인을 "긴 tool 입력 생성 중 session/update 0" 으로 확정하고 고쳤다(`onRawSDK → noteActivity`, 원시 스트림 항상 ON). 여기서는 **같은 조건**(Lead 위임 3 + Researcher 3 + Writer 집필)을 `72_scenario_a.sh RUNTIME=real` 로 돌려 고침 전후를 대조한다. 전 = `git archive fb97913^`(#203 까지) 로 빌드한 데몬(`DAEMON_BIN`), 후 = dev(#204).

실기 8회(haiku 6 · sonnet 2). 전부 `72_scenario_a.sh RUNTIME=real`, 판정 46/0. **stall 0/8 — 전에서도 후에서도 재현되지 않았다.** 표는 Writer attempt 2(집필 턴)만; 전체 표는 `e2e/p5/out-real/<run>/s66-table.tsv`(로컬, gitignore).

| run | 데몬 | 모델 | 요구 길이 | 실제 아티팩트 | 집필 턴 | 서버 이벤트 최대 공백 | stall |
|---|---|---|---|---|---|---|---|
| run1 | dev #204 | haiku | 3000자 | 5.2 KB | 34.9s | 17.9s | 0 |
| run2 | dev #204 | haiku | 3000자 | 9.3 KB | 59.7s | 30.9s | 0 |
| run3 | dev #204 | haiku | 3000자 | 8.3 KB | 60.1s | 35.0s | 0 |
| pre-3000 | **#204 이전**(`fb97913^`) | haiku | 3000자 | 5.3 KB | 41.6s | 22.9s | 0 |
| pre-15000 | #204 이전 | haiku | 15000자 | 11.1 KB | 76.9s | 46.2s | 0 |
| dev-15000 | dev #204 | haiku | 15000자 | 8.4 KB | 49.3s | 29.2s | 0 |
| pre-sonnet | **#204 이전** | sonnet | 20000자 | **45.4 KB** | **437.2s** | **159.6s** | 0 (임계 180s 에 20s 못 미침) |
| dev-sonnet | dev #204 | sonnet | 20000자 | 26.4 KB | 141.0s | 115.8s | 0 |

- "서버 이벤트 최대 공백" 은 그 attempt 의 `task_event` 사이 최대 간격 — #204 이전 데몬이 세던 활동(session/update)의 무음 상한 근사다. #204 뒤 데몬은 원시 스트림(`raw:_claude/sdkMessage(on)`, 로그 `stall watch armed … counts=`)도 세므로 이 공백과 무관하게 stall 이 나지 않는다.
- **재현이 안 된 이유(조건 차이)**: (1) **모델** — haiku 는 15000자를 시켜도 11 KB 까지만 쓰고 ~150 B/s 로 1분 안에 끝난다. (2) **한 Write 의 크기** — sonnet 은 45 KB 를 **두 번의 Write(20 KB 씩, 119s·160s) + Edit 여러 번**으로 나눠 썼다. #204 이전 데몬의 stall 은 **한 tool 입력의 무음 생성 > 180s** 라 한 Write 가 ≈ 30 KB 를 넘어야 난다(T-D12 실측 170 B/s). Director 의 원래 사건(2026-09-08)은 큰 모델이 긴 글을 한 Write 로 쓴 경우로 읽힌다. (3) 다중 에이전트 수는 무관 — 8회 모두 Lead+Researcher×3+Writer 였고 집필 턴 외에는 공백이 35s 를 넘지 않았다.
- **전후 대조가 말해 주는 것**: pre-sonnet 의 160s 공백은 임계에 20s 남은 값이다 — 같은 조건에서 모델이 조금만 더 길게 한 번에 쓰면 #204 이전은 stall, 이후는 원시 델타로 활동이 이어져 안전하다(#204 유닛 `s66_stall_test.go` 가 그 경계를 페이크 클럭으로 잰다). 실기로 stall 자체를 보려면 한 Write ≥ 30 KB 를 강제해야 하는데(예: "한 파일에 한 번의 Write 로 40000자") 모델이 지시를 나눠 쓰는 경향이 있어 결정적이지 않다 — 필요하면 Director 의 원 세션 조건(모델·goal)으로 1회.
- 로그: `e2e/p5/out-real/*/daemon-72.log`(dev 는 `stall watch armed limit=3m0s counts=session/update,request_permission,raw:_claude/sdkMessage(on)` 8줄/세션, `stall fired` 0), `72-real.log`, `72-artifact-dl.bin`.

#204 가 바꾼 `e2e/p3/50_` C3d 패턴("쓸 수 있는 예산")도 재실행했다(격리 스택 :8110/:5454, 실기 haiku): **116/1 → C3f 는 스크립트의 `grep -c || echo 0` 두 줄 함정**(p4 README 에 적힌 것)이라 그 줄을 고쳤다(`{ grep -c … || true; } | head -1`). 서버 500 은 0건. <!-- 50_RERUN -->

## 6. 열린 결함 · 관찰 (번호 없음 — Lead 가 준다, §0-11)

| # | 스트림 | 내용 | 근거 |
|---|---|---|---|
| 신규 1 | 서버 | **위임↔합류 사이클이 FR-3.5 루프 상한을 타지 않는다.** `delegateLane`(router/delegate.go)·합류 wake(router/status.go `wake`)는 `CheckLoopLimits` 밖. 위임자가 합류 통보에 다시 위임하면 무한 — 70초에 529 task, 세션 active | `77_` S1x, `e2e/p5/out/77_security.log` |
| 신규 2 | 서버 | **마스킹이 `task_event.payload.title` 을 지우지 않는다.** 실기 어댑터의 title = 셸 명령 전체 → 인자 마스킹이 무효 | `77_` S3d2, `events/mask.go` |
| 관찰 1 | 서버·CLI | `colab status set done` 은 lane 을 즉시 `done` 으로 만든다 — 그 뒤에도 도는 턴(에이전트가 done 뒤에 일을 더 하면)은 Director 가 "중단" 할 수 없다(`409 lane_not_cancellable`, task 는 running). 지시문 관례("done 은 마지막 호출")로 덮여 있지만 S7 의 중단 버튼이 running 턴에 비활성이 되는 자리 | `77_` 첫 실행, README 함정 |
| 관찰 2 | 서버 | `parallel_wallclock_reduction` 정의가 사람 대기(HITL)를 "전체" 에 넣는다 — HITL 이 있는 세션은 병렬 효과와 무관하게 낮거나 음수 | §4 |
| 관찰 3 | 웹 | S7 의 요약 메시지(`kind=summary`)가 마크다운 원문(`##`·`-`)으로 보인다 | `web/__screenshots__/p5-78-s7.png` |
| 관찰 4 | 데몬 | acpfake 는 `daemon/acpfake` 에 `exec` 스텝을 더했다(PR #204 의 `raw_deltas` 와 같은 파일, 인접 줄 — 리베이스로 병합됨). 테스트 하네스 배선이며 데몬 코드는 참조하지 않는다 | `daemon/acpfake/exec_test.go` |

## 7. 남은 것

- G8(F1 실측 5명·달력) — `plan/G8_PLAN.md` Lead 확정 → Director 실측.
- 신규 1·2 의 번호·수정(Lead). 신규 1 은 무한 루프라 **배포 전** 후보.
- S-64(설치가 main 을 클론) 는 G8 전 배포본에서 닫혀야 한다(P2_BACKLOG "배포 전").
