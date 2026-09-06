# G7 판정 자료 — 시나리오 B · worktree 격리 · 재바인딩 · 요약 (T-I4)

> 판정은 Lead 가 `plan/G7_DECISION.md` 에 쓴다. 이 문서는 **실측치와 그 근거**만 담는다.
> 형식은 `plan/G6_REPORT.md` §9(2판) 을 그대로 따른다.

## 0. 방법

| 항목 | 내용 |
|---|---|
| 스택 | dev **`c375b33`**(#168 머지 뒤). Postgres `colab-pg-i4` **:5450** · server **:8105** · web **:3018** · 요약 전용 서버 **:8106**(65_ 만, 같은 DB) · claim 탭 :8115(61_)·:8116(63_) · §8.5 목 :8117. 마이그레이션 18개. `bin/server`·`bin/daemon`·`bin/colab` 빌드 **2026-09-07 06:06 KST** |
| 런타임 | **실기 2종** — Claude Code CLI 2.1.258 + `claude-agent-acp` **0.74.0**(핀) · **Hermes 0.20.6**. 모델은 전부 `claude-haiku-4-5-20251001` |
| 실험 저장소 | **이 저장소가 아니다**(P4_TASKS §0-18). 판마다 `/private/tmp/colab-p4-i4/<판>/repo…` 에 임시 git 저장소를 만들고 `CLAUDE.md`·`AGENTS.md` 를 **추적 상태로** 커밋해 둔다 — E13-03~06 이 재는 것이 그 상태다 |
| 총계 | **PASS 136 · FAIL 16 · N/A 1** — 61_ **49/4** · 62_ **21/0** · 63_ **21/7 · N/A 1** · 64_ **26/5** · 65_ **19/0** |
| 결론 | **G7 미충족.** 시나리오 B 는 **우회 위에서만** 돈다. 차단 결함 3건이 전부 `worktree` 격리의 배선(경로·git 사실·아티팩트 다운로드 권한)에 있다 — §2 |

### 0.1 스크립트

| 스크립트 | 무엇을 재나 | 스택 | PASS / FAIL |
|---|---|---|---|
| `e2e/p4/61_scenario_b.sh` | 시나리오 B 전체(E16-B 1~8단계, E13-01~08, FR-6.1·6.4·6.5, §8.4 위생, FR-2.4 요약) | 전용 데몬 + 탭 :8115 | **49 / 4** |
| `e2e/p4/62_double_write.sh` | 데몬 `kill -9` → 재시작 → 이중 쓰기 0 (E11-05·06, E8-04 (4), FR-9.1) + `worktreesim` 100라운드 | 전용 데몬(hermes) | **21 / 0** |
| `e2e/p4/63_offline_rebind.sh` | 오프라인 유예 → `paused` → 후보 → 재바인딩 → `rebind_prepare` → diff 재적용 (E14-02~06·08·10) | 런타임 3대 + 탭 :8116 | **21 / 7 · N/A 1** |
| `e2e/p4/64_gc.sh` | workdir GC (E13-09~19, FR-6.4) | 전용 데몬 | **26 / 5** |
| `e2e/p4/65_summary_refusal.sh` | 세션 요약 실패 경로 (E6-11·12, §8.5) | 전용 서버 :8106 + 목 :8117 | **19 / 0** |

### 0.2 대역(stand-in)·우회 — 항목마다 명시

**데몬 대역 curl 은 한 번도 쓰지 않았다.** 모든 턴은 실기 런타임이 돌았다. 아래가 쓴 우회의 전부다.

| # | 무엇 | 왜 | 어디에 |
|---|---|---|---|
| U1 | **workdir 행 선행 삽입** — 세션을 만든 뒤 데몬을 띄우기 전에 `<workdir_root>/worktrees/<slug>/<agent>` 를 `workdir` 행으로 넣는다 | **결함 ① 때문**: 서버가 번들에 **상대** workdir 경로를 실어 worktree 세션이 첫 턴부터 전멸한다. 서버는 그 에이전트의 workdir 행이 있으면 그 경로를 대신 싣는다(`workdirs.BundleWorkdirPaths` → `ExistingForAgent`) | 61_·62_·63_·64_ (`lib.sh seed_worktree_workdirs`) |
| U2 | **`retire_workdirs`** — 재바인딩 직후 옛 machine 의 workdir 행을 `deleted` 로 접는다 | 재바인딩은 옛 행을 `retained` 로만 바꾸는데 `BundleWorkdirPaths` 는 `status <> 'deleted'` 만 보므로 새 machine 의 번들이 사라진 컴퓨터의 경로를 가리킨다 | 63_ |
| U3 | **git 사실 주입** — 디스크에서 잰 `commits_ahead`·`tree_dirty`·`merged` 를 `workdir` 행에 직접 쓴다 | **결함 ② 때문**: 그 값이 서버에 도달하는 통로가 둘 다 막혀 있어 GC 판정의 입력이 언제나 "커밋 0·클린"이다. 없는 사실을 지어내지 않았다 — 같은 스크립트가 디스크에서 먼저 재고(P0·P0b·P0c) 그 값을 쓴다 | 64_ |
| U4 | **클럭 우회** — `runtime.offline_since` 를 8일 전, `session.finished_at` 을 30일 전으로 민다 | 서버 바이너리에 클럭 주입 경로가 없다(`clock.Real{}` 고정). 56_·57_ 와 같은 방법 | 63_·64_ |
| U5 | **쿼터 분자** — `workdir.disk_bytes` 를 2GiB 로 적는다 | E13-16 의 **입력**이지 규칙이 아니다. 실제로 1GB 를 쓰지 않는다 | 64_ |
| U6 | **§8.5 목 엔드포인트** — `ANTHROPIC_BASE_URL` 을 `mock_anthropic.py` 로 돌린다 | refusal·전송 오류는 실제 API 로 재현할 수 없다. 서버 `llm.FromEnv` 가 이 목적을 위해 이미 열어 둔 문이다("so the isolated stack can point at a stub"). 요약 결정·피드·완료는 전부 실서버 코드가 낸다 | 65_ |

### 0.3 실기 수치

| 판 | 세션 비용(실측) | attempt 수 | 소요 |
|---|---|---|---|
| 61_ 시나리오 B | **$0.5471** | 9 | 740s |
| 63_ 재바인딩 | $0.0333 | 4 | — |
| 62_·64_·65_ | 각 세션 $0.02~0.08 | — | — |

## 1. 신규 결함 (번호는 Lead 가 매긴다)

| 심각도 | 스트림 | 증상 | 재현 | 위치(읽어서 확인) |
|---|---|---|---|---|
| **차단 ①** | S(주) + D | **`worktree` 격리 세션이 첫 턴부터 전부 죽는다.** 서버가 TaskBundle 에 **상대** workdir 경로(`<session-slug>/<agent-slug>`)를 싣고, 데몬은 그것을 그대로 써서 (a) `git -C <repo> worktree add <상대경로>` 로 **사용자 저장소 안**에 체크아웃을 만들고 (b) 같은 문자열을 **자기 CWD 기준**으로 절대화해 어댑터에 없는 디렉터리를 `cmd.Dir` 로 넘긴다 → 모든 attempt 가 `failed(config)` + `spawn: fork/exec …/npx: no such file or directory`(원인을 가리는 오답 문구) | `61_scenario_b.sh` **X1·X1b·X1c** (`out/61-probe.txt`, `out/61-probe-bundle.json`, `out/61-probe-worktrees.txt`) | `server/internal/queue/bundle.go:348` — `workdirs.PlanWorktree` 를 **`Root` 없이** 부른다 → `server/internal/workdirs/gc.go:142` `path.Join("", slug, agent)`. `daemon/internal/workdir/worktree.go:137·166` — `b.Workdir.Path` 를 그대로 `filepath.Abs`. `daemon/internal/gitrepo/gitrepo.go:204` — `os.MkdirAll(filepath.Dir(<상대>))` 가 데몬 CWD 를 더럽힌다 |
| **차단 ②** | D + S | **`worktree` workdir 의 git 사실이 서버에 영영 도달하지 않는다** → GC 가 **미병합 커밋·미커밋 변경을 지운다**(FR-6.4 M4 무력화, 데이터 유실). 부수 증상: `disk_bytes` 0(쿼터 분자·S13 용량 0), gc **영수증**도 같은 통로라 workdir 행이 `deleted` 로 닫히지 않는다 | `64_gc.sh` **P1·P1b·P1c·P1d·G2d** (`out/64-git-facts.txt` = 디스크의 진짜 상태, `out/64-workdirs.txt` = 서버가 가진 값) | 통로 1 §6: `daemon/internal/workdir/worktree.go:251 ListWorktrees` 가 `SessionID` 에 **디렉터리 이름**을 넣고 `AgentID` 를 아예 안 넣는다 → `server/internal/httpapi/daemon.go:285 workdirReport` 가 uuid 파싱 실패·agent/lane 없음으로 **조용히 skip**(로그도 없다). 통로 2 §4.4: `contracts.Finish.Workdir{Path,Git}`(daemon-protocol v0.7.2 — "서버는 이 값으로 그 workdir 행의 merged·dirty·commits_ahead 를 갱신한다")를 **서버가 읽는 곳이 없다** |
| **차단 ③** | S + 계약 | **`rebind_prepare` 다운로드가 401 로 전부 실패한다** → 재바인딩 뒤 `<rebind>` 프롬프트가 가리키는 디렉터리에 **manifest 만 있고 diff 는 없다** → E14-06(순서대로 재적용)이 성립하지 않는다. 명령은 소비되지 않아 30초마다 재발행된다 | `63_offline_rebind.sh` **R5f** (`out/63-manifest.json` — 두 항목 모두 `error: server: 401 unauthorized invalid session token`, `out/daemon-63b.log`) | 데몬은 `Authorization: Bearer <daemon_token>` 으로 `GET /v1/artifacts/{id}/content` 를 부른다(`daemon/internal/api/api.go:239 Download`). openapi `downloadArtifact` 의 권한은 **"워크스페이스 멤버 · TaskToken"** 뿐 — **DaemonToken 이 없다**(contracts/openapi.yaml:2636) |
| **차단 ④** | S | **재바인딩이 세션의 저장소 경로를 새 컴퓨터 것으로 옮기지 않는다.** `isolation.repo_path` 가 사라진 machine 의 경로 그대로라, 새 machine 의 데몬이 없는 저장소에서 워크트리를 만들려 한다 → `failed(config)`. 후보 조회는 이미 `matched_repo` 를 돌려주는데 재바인딩이 그것을 쓰지 않는다 | `63_offline_rebind.sh` **R5h** — 재바인딩 뒤 `session.isolation.repo_path` = `…/repo-a`(사라진 machine), want `…/repo-b`. 그 결과 새 machine 의 attempt 가 `failed(config)`(**R7g**) — 데몬 로그: `workdir: git worktree add …/work-b/worktrees/<slug>/dev1 colab/<slug>/dev1: Preparing worktree …` | `server/internal/runtimes/offline.go` `Rebind` 의 `UPDATE session …` 이 `runtime_id`·`status`·`paused_reason`·`paused_detail`·`rebind_prompt` 만 바꾼다 |
| 중 | S | **`review reject` 답글이 그 자체로 lane 재진입을 일으키지 않는다.** openapi `reviewArtifact` 는 "`comments` 를 … lane 스레드에 답글로 게시한다(**해소 규칙 1로 재진입** — E16-B 5단계)" 라고 적혀 있으나, 그 답글은 **에이전트가 쓴 · 멘션 없는** 메시지라 라우팅 규칙 4(“에이전트의 메시지는 멘션이 없으면 아무것도 트리거하지 않는다”)에 걸린다. 사람이나 lead 가 중계해야만 Frontend 가 깨어난다 | `61_scenario_b.sh` **B5g**(반려 메시지 id 를 `task.trigger_message_id` 로 가진 Frontend task = 0) | `server/internal/httpapi/handlers_artifacts.go:420 postRejectReason` → `server/internal/router/rules.go:160` 규칙 4 |
| 낮음(관측) | S | 사라진 machine 으로 **이미 dispatch 된** task 는 재바인딩이 되살리지 않는다(`queued`·`deferred` 만 requeue). 그 task 는 heartbeat 만료로 `failed(timeout)` 이 될 때까지 새 machine 에서 아무 일도 하지 않는다 | `63_offline_rebind.sh` **R5g**(실측 상태 `dispatched`) | `server/internal/runtimes/offline.go` `UPDATE task … WHERE status IN ('queued','deferred')` |

> 백로그에 이미 열려 있는 S-52·S-53·D-20·K-12 는 이번 판에서 **재발을 관측하지 못했다**(57_ 의 전수 검사 항목은 이번 판의 대상이 아니다). 다시 보고하지 않는다.

## 2. 시나리오 B — `61_scenario_b.sh` (PASS 49 · FAIL 4)

E16-B 1~8단계를 **실기로 한 번에** 돌렸다. PM·Backend·Frontend 는 `claude_code`, **QA 는 hermes** 다 —
브리프 전송이 런타임마다 다르고(`acp_meta_system_prompt` vs `instruction_file`), §8.4 v0.16 이 막는 오염은
`instruction_file` 경로에서만 일어날 수 있으므로 hermes 참가자가 없으면 E13-03~06 은 아무것도 재지 못한다.
그래서 P4_TASKS §4 의 "Reviewer 를 hermes 프로파일로" 병행 항목을 **여기서 함께 수행**했다(별도 66_ 불필요).

| 단계 | 실측 |
|---|---|
| 1 세션 | `isolation.kind=worktree` · 종료 조건 **`agent_approval(QA)` 단독** · 사람 승인 HITL **0건**(E6-05) |
| 2 위임 | PM 한 턴에 Backend·Frontend lane **각 1개** · 워크트리 **각 1개** · 브랜치 `colab/<slug>/backend`·`…/frontend` |
| 3 제출 | `colab artifact submit --type diff` **2개**, 전부 `type=diff` (`--file` 없이 CLI 가 워크트리 diff 를 만든다) |
| 4 QA | QA 번들에 **남의 워크트리 경로 0건**(E13-08) · 번들 workdir 은 `…/qa` · 브리프 전송 `instruction_file` |
| 5 재진입 | Frontend **lane 1개 유지**(새 lane 0) · task 2개 · **같은 lane id** · 워크트리 여전히 1개(C3). **단, 재진입을 일으킨 것은 QA 의 반려가 아니라 PM 의 중계 메시지다** — §1 중 결함 |
| 6 통보 | frontend 아티팩트 **version 2**(FR-4.3) · QA 가 두 번째로 깨어남 |
| 7 승인 | 세션 `completed` · 결정 기록 `source=agent` |
| 8 위생 | Backend·Frontend·QA 세 워크트리 모두 **`COLAB_BRIEF.md` 없음 · exclude 항목 0 · 추적 중 `CLAUDE.md`/`AGENTS.md` 무변경 · `git status` 에 우리 잔여물 0**. 원본 저장소 HEAD 무변경 · `git status` 클린 |
| 요약 | `session_summary` **정확히 1개** · FR-2.4 네 절 전부 · 피드 `summary.generated_by:fallback`(키 없음) |
| 피드 | `tool/edit_file` ≥2 · `tool/run_shell` ≥1 |

FAIL 4 = X1·X1b·X1c(차단 ①) + B5g(중). **나머지 49 는 우회 U1 위에서 잰 값이다** — 우회를 걷으면
세션이 첫 턴에서 죽으므로 이 표 전체가 성립하지 않는다.

### 2.1 위생 측정의 함정 (다음 판을 위해)

브리프 파일 삭제와 `.git/info/exclude` 해제는 **attempt 종료의 `defer`** 다(`daemon/internal/loop/loop.go:582`).
세션이 `completed` 로 바뀐 직후에 재면 **아직 돌고 있는 마지막 턴의 브리프**를 오염으로 오독한다
(1차 실행에서 실제로 그랬다). 판정 전에 "그 세션의 `queued|dispatched|preparing|running` task = 0" 을 기다린다.

## 3. 이중 쓰기 0 — `62_double_write.sh` (PASS 21 · FAIL 0)

실기 hermes 턴이 워크트리에 계속 쓰는 동안 데몬을 `kill -9` 했다(pid 로만, §0-10).

| 항목 | 실측 |
|---|---|
| attempt pgid 기록 | `<workdir_root>/.colab/attempts/<task>.1.json` — **체크아웃 밖** |
| kill -9 직후 | 고아 프로세스 그룹 **생존**(관측이지 단정이 아니다 — 어댑터 stdio 가 끊기면 쓰기는 멈춘다) |
| 재시작 | 데몬 로그 `orphan <task>.1 pgid=… alive=true killed=false` 가 **배너 바로 다음 줄**, probe·claim 보다 **앞** |
| 이중 쓰기 | 같은 workdir 에 살아 있는 프로세스 그룹 **0** |
| 고아 편집 | 지우지 않았다(E11-06) |
| 브리프 | 두 번째 턴 중 `COLAB_BRIEF.md` 마커 블록 **절대 개수 1** — 덧붙이지 않는다 |
| 종료 뒤 | `COLAB_BRIEF.md` 없음 · exclude 해제 · 추적 파일 무변경 |
| `daemon/worktreesim` 100라운드 | **overlaps 0 · lateClaims 0 · dupEdits 0** (절대 개수, 델타 아님) — `out/62-worktreesim.log` |

## 4. 오프라인 · 재바인딩 — `63_offline_rebind.sh` (PASS 21 · FAIL 7 · N/A 1)

| 항목 | 실측 |
|---|---|
| E14-02 | `offline_since` 8일 → 스윕 → 세션 **`paused(runtime_offline)`** · Director 인박스 **1건** |
| E14-10 | 두 번째 스윕 뒤에도 **1건** — 멱등 |
| E14-04·05 | 후보 조회: **같은 remote·다른 경로 B = `eligible: true`**, **다른 remote C = `false`** (경로 문자열 비교가 아니다) |
| E14-03 | `rebind` **200** · `rebind_prepare` 명령 큐잉 · lane `runtime_session_ref` **비움**(콜드 스타트) |
| §4.3 위치 | 다운로드 위치 = `<workdir_root>/.colab/rebind/<session_id>/` — **체크아웃 밖** · `manifest.json` 에 **제출 순서대로** 두 아티팩트 |
| **listArtifacts 순서** | **제출순(오름차순) — `step-1, step-2`.** T-W5 가 목에서 고친 최신순 오답을 **실서버는 갖고 있지 않다** |
| 프롬프트 | `<rebind>` 구간 · `{{COLAB_REBIND_DIR}}/manifest.json` **정확히 1회** · `git apply` 지시 · **콜드 스타트** 문장 · 아티팩트가 **제출 순서**로 나열 |
| E14-08 | 활성 세션이 걸린 런타임 삭제 = **409 `runtime_has_active_sessions`** |
| **E14-06** | **미충족** — 차단 ③(다운로드 401)·차단 ④(저장소 경로 미이동). 새 machine 의 첫 attempt 는 `failed(config)` 로 끝나고 워크트리조차 만들어지지 않는다 |
| NN2(#168 리뷰) | **N/A** — 다운로드가 전부 401 이라 명령이 소비되지 않고 30초마다 재발행돼 manifest mtime 이 계속 갱신된다. 순서를 가릴 시각이 남지 않는다 |
| 관측 | 사라진 machine 으로 **이미 dispatch 된** task 는 재바인딩이 되살리지 않는다(실측 상태 `dispatched`) — §1 낮음 |

## 5. workdir GC — `64_gc.sh` (PASS 26 · FAIL 5)

**입력 통로는 막혀 있고(차단 ②), 규칙 자체는 옳다.** 디스크에서 잰 git 사실을 행에 넣고(U3) 잰 결과다.

| 항목 | 실측 |
|---|---|
| 디스크 | A = 미병합 커밋 1 + 클린 · B = 커밋 0 + 클린 · C = 커밋 0 + 미커밋 변경 (`out/64-git-facts.txt`) |
| **서버가 받은 값** | 셋 다 `merged=false · commits_ahead=0 · tree_dirty=false · disk_bytes=0` — **아무것도 도달하지 않았다**(차단 ②) |
| E13-12 | 사실 주입 뒤 A = `gc_blocked_reason=unmerged_commits` · gc 명령 **0** · 워크트리 보존 · 인박스 **1건** |
| E13-13 | C = `uncommitted_changes` · gc 명령 0 · 인박스 1건 (미커밋이 미병합보다 우선) |
| E13-10·11 | B = gc 명령 발행 → **`git worktree remove` 됨** · **브랜치 `colab/<slug>/gcclean` 보존** |
| 영수증 | B 의 workdir 행이 `deleted` 로 닫히지 **않는다** — §6 gc 영수증도 차단 ② 와 같은 통로다 |
| E13-18 | **active 세션**의 워크트리는 30일이 지나도 gc 명령 0 · 알림 0 |
| 멱등 | 두 번째 스윕 뒤에도 인박스 각 1건 |
| E13-19 | `workdir_disk_quota_gb = null` → 세션 생성 통과(null 을 0 으로 읽지 않는다) |
| E13-16 | 사용량 ≥ 상한 → 세션 생성 **409 `workdir_quota_exceeded`** |

## 6. 세션 요약 실패 경로 — `65_summary_refusal.sh` (PASS 19 · FAIL 0)

목 엔드포인트(U6)로 §8.5 의 세 가지 답을 만들고, 나머지는 전부 실서버가 결정한다.

| 항목 | 실측 |
|---|---|
| **E6-11** refusal | 피드 `summary.failed` · 카테고리 **`policy_violation`**(= `stop_details.category` 그대로) · 세션 **`completed`** · 요약 메시지 **0** |
| §8.5 순서 | 거절 본문(`I can't help with that.`)이 **요약으로 게시되지 않았다** — `Refused()` 가 `Text()` 보다 먼저다 |
| **E6-12** 전송 오류 | HTTP 500 → 카테고리 **`transport_error`** · 세션 `completed` · 요약 0 |
| 정상 | 요약 **1개**(목이 준 본문) · 피드 `summary.generated_by:platform_llm` |
| 키 없음 | 폴백 요약 **1개** · `summary.generated_by:fallback` · FR-2.4 **네 절 전부** |
| §8.5 요청 모양 | 모델 **`claude-sonnet-5`**(light job) · 안정 접두에 `cache_control` · `anthropic-beta: server-side-fallback-2026-07-01` · `stream: true` |

## 7. 컷 2 판정 근거

G7 은 **시나리오 B + `git status` 클린 + 재바인딩 + 요약**, 컷 2 판정(“B 됐는데 요약 안 되면 요약을 뺀다;
재바인딩 미통과면 v1.1”)이다. 세 항목을 따로 적는다.

| 항목 | 실측 | 판단 근거 |
|---|---|---|
| **시나리오 B + `git status` 클린** | E16-B 1~8단계 전부 실기로 통과(§2). 워크트리 위생도 전부 통과 | **다만 우회 U1 없이는 첫 턴도 못 넘는다**(차단 ①). 우회를 걷으면 0단계에서 실패 |
| **요약** | 65_ **19/0**, 61_ 의 실세션에서도 요약 1개 + `generated_by` | 통과. 컷 2 의 "요약을 뺀다" 조항을 쓸 이유가 없다 |
| **재바인딩** | 유예·paused·인박스·후보 판정·`rebind_prepare` 발행·프롬프트까지 전부 통과. **핵심인 diff 재적용(E14-06)만 미충족** | 차단 ③(다운로드 권한)·④(저장소 경로) 두 가지가 원인이고 **둘 다 배선 결함**이다 — 설계가 아니라 |

**Lead 가 판정할 때 함께 볼 것**: 차단 ①·②·③·④ 는 모두 `worktree` 격리를 **처음 실서버·실기로 관통시켜** 드러난
배선 누락이다. 골든이 못 본 이유는 세 가지 모두 **양쪽이 각자 맞는 값을 계산하고 그 사이 와이어에서 어긋나기**
때문이다 — 서버 골든은 `PlanWorktree` 를 `Root` 없이 재고, 데몬 골든은 `Root` 를 넣고 재며, 아무 표도
`TaskBundle.workdir.path` 를 데몬이 실제로 어떻게 쓰는지 재지 않는다(차단 ①). §6 보고는 데몬 쪽에서만,
서버 쪽에서만 각각 검사돼 왔다(차단 ②). `rebind_prepare` 는 데몬 유닛이 페이크 서버로 재서 401 이 날 수 없었다(차단 ③).

## 8. 되먹임 (다음 게이트를 위한 기록)

- **와이어를 재는 표가 없다.** P4 의 차단 4건 중 3건이 "양쪽 다 자기 규칙대로 옳고, 오직 둘 사이에서만 틀린" 모양이다.
  다음 골든은 **번들 한 개를 서버가 만들어 데몬이 소비하는 왕복**을 한 표로 재야 한다(경로 절대성, §6 보고의
  attribution, 다운로드 권한).
- **조용한 skip 이 결함을 숨긴다.** `httpapi.workdirReport` 는 조건을 못 채우면 로그 없이 `false` 를 돌려준다.
  P3 에서 배운 것("침묵은 명령에 대한 답이 아니다", daemon-protocol §6)이 **서버 쪽 수신부에는 아직 적용되지 않았다**.
- **실기 e2e 의 셸 함정 3종**(이번 판에서 전부 밟았다): ① 돌고 있는 bash 스크립트를 편집하면 바이트 오프셋이
  밀려 `syntax error` 로 죽는다. ② `set -euo pipefail` 에서 무매치 `grep` 은 파이프라인을 죽인다 —
  `{ grep … || true; }` 로 감싼다. ③ bash 3.2 는 명령 치환 안의 `case … pattern)` 을 파싱하지 못한다(p3 `in_set` 주석과 같은 함정).
- **fnm 의 multishell PATH 는 셸이 끝나면 사라진다.** 데몬은 자기를 띄운 셸보다 오래 살고 claude_code 어댑터는
  `npx` 를 PATH 로 찾으므로, 그 경로를 물려받으면 셸이 끝난 뒤 **모든 attempt 가 `failed(config)`** 로 죽는다.
  `e2e/p4/lib.sh stable_path` 가 데몬에 넘기는 PATH 만 `~/.fnm/aliases/default/bin` 으로 바꾼다.
- **에이전트 지시문이 판정을 바꾼다**(G6 에서 배운 것의 재확인). QA 에게 "frontend 하나만 판정하라" 를 빼면
  QA 가 backend 까지 승인해 `agent_approval` 이 5단계 전에 충족되고 재진입이 영영 일어나지 않는다.
