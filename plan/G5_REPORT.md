# G5 판정 자료 — 시나리오 A 8단계 + Hermes + 템플릿 3분 (T-I2 2부)

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G5** — "시나리오 A **8단계** + **Hermes** + **템플릿 3분 Director 실측**". 예산이 가장 큰 게이트다 |
| 앞 게이트 | **G4 통과**(`plan/G4_DECISION.md`, Director 확인·K-5 결정 PR #91). 시나리오 A 를 Claude Code 단일 런타임으로 — API 32/32 · 웹 19/0 |
| 작성 | Integrator (T-I2 2부), 2026-09-06 |
| 스택 | dev `6b4fe78`(문서 #99 까지) 를 머지한 `d61d3f5` 에서 **재빌드**. `bin/server`·`bin/daemon`·`bin/colab` 빌드 시각 **2026-09-06 19:48:20 KST**. 1판은 dev `6d9d20d`(18:59:22 빌드) 였고, D-7 hotfix **#97** 이 그 뒤에 들어왔다. 서버 `:8090` · 웹 `:3010`(`next build` + `next start`) · Postgres `colab-pg-g4 :5436` |
| 런타임 | Claude Code 2.1.258 + 어댑터 0.74.0(핀) · **Hermes 0.20.6**. 모델은 비용 때문에 haiku(`claude-haiku-4-5-20251001`) |
| 재현 | `bash e2e/p2/up.sh` 뒤 `30_scenario_a_hermes.sh` · `31_blocked_roundtrip.sh` · `32_loop_limit.sh` · `33_approval_completed.sh` · `34_template_3min.sh` |
| 총계 | 다섯 스크립트 **PASS 134 · FAIL 5** — 30_ **57/0** · 31_ 26/2 · 32_ 13/1 · 33_ 24/2 · 34_ 14/0 |
| D-7 | **수정됨 (#97).** hermes `tool_surface=cli_wrapper` 판정 + attempt 별 래퍼 실행 파일 + 브리프·턴 프롬프트 치환. 30_ 재실행에서 **FAIL 0**(§3) |
| 결론 | **G5 충족 — 템플릿 3분 Director 실측 대기.** 다섯 항목이 전부 실기에서 섰다. 남은 FAIL 5건은 표시·프롬프트·정리 결함(S-26·S-27·S-28·S-29)과 승인 경로의 P3 이월(S-25)이고 **DoD 를 막지 않는다**. 판정이 남은 칸은 (e) 의 **3분 수치 하나**뿐이다(§6.3) |

> **읽는 법.** 각 절은 EVAL 행 번호를 그대로 쓴다. `우회`라고 적힌 것은 정식 API 경로가 없어
> DB 나 다른 op 로 돌아간 측정이다 — 그 자리에는 반드시 결함 번호가 붙어 있다.
> 결함은 `S`(서버) `W`(웹) `D`(데몬/하네스) `C`(CLI) `계약` 으로 스트림을 귀속했다.

---

## 0. 방법

- 지시문은 G4 와 **같은 파일**(`e2e/p2/fixtures/scenario_a_agents.sh`)을 쓴다. 런타임만 바꿔 비교가
  프롬프트 차이로 흐려지지 않게 했다. blocked 왕복만 별도 지시문(`fixtures/blocked_agents.sh`)이다.
- 수치는 전부 **서버 DB 의 단일 클럭**에서 센다(`e2e/p2/lib.sh` 의 관측 질의). 화면 판정은
  `agent-browser` 로 실제 DOM 을 읽는다.
- 서버가 데몬에 보내는 `TaskBundle` 은 claim 탭(`fixtures/claimtap.py`)이 기록한다 — 턴 프롬프트·
  브리프·`resume` 를 사후에 확인할 수 있다.
- 과제는 저장소 밖의 무해한 주제(가상의 제품 X)다. 세션 goal·브리프에 저장소 파일명을 쓰지 않는다
  (G3_DECISION §2 X-2: 쓰면 에이전트가 그것을 찾아 실행한다). 워크스페이스 이름은 ASCII.
- 프로세스 종료는 pid 파일·포트 지정만(§0-10). 이 머신에는 다른 워커의 서버가 함께 떠 있다.

### K-5 결정의 적용 (PRD v0.15 · EVAL E1-22, PR #91)

합류가 발화한 뒤 자식이 위임자를 멘션하면 **규칙 8 억제가 풀리고 일반 라우팅**이 된다. 위임자가
한 줄마다 깨어나지 않는 것은 새 규칙이 아니라 **FR-3.4 의 lane 단위 병합**이 막는다 —
첫 멘션이 위임자 task 하나를 만들고, 위임자가 그 task 를 마치기 전의 후속 멘션은
`coalesced_message_ids` 로 병합된다.

**이번 실행에서 이 경로는 나오지 않았다.** 31_ 의 세션 `077b9688` 에서 Researcher 가 `@Lead` 를
두 번 멘션했지만(10:39:18·10:39:22) 둘 다 합류 발화(10:39:24) **전**이라 E1-21(억제 + 합류 전달)에
해당했다 — 실측: 그 두 메시지로 생긴 Lead task **0개**, 둘 다 합류 페이로드에 실림.
재진입한 자식이 합류 **뒤에** 위임자를 멘션하는 데까지는 시나리오가 가지 않았으므로,
K-5 는 판정 기준으로만 썼고 실측은 P2a 골든 E1-22 가 맡는다.

---

## 1. DoD 항목별 판정

| # | DoD (PLAN §6.2) | 판정 | 근거 |
|---|---|---|---|
| a | 시나리오 A **8단계 끝까지** — 위임 3 → lane 3 병렬 → 합류 1회 → 종합 → Writer 초안 → `artifact_submitted` → **승인** → `completed` + 요약 자동 게시 | **통과 (단서 2)** | 1~6단계는 G4(Claude Code 단일, API 32/32)에 이어 이번 30_ 에서 **Hermes 를 섞어서도** 다시 섰다. 7·8단계(승인 → `completed` + 요약)는 `33_approval_completed.sh` **PASS 24 / FAIL 2** — `active → completing → completed`·`session_summary` **1개**·인박스 알림 성립. 단서 둘: 승인을 **P2 의 경로**(`completeSession`)로 쟀다(**S-25** — `respondHitlRequest` 는 x-phase P3, 채워진 원자는 `manual`), 그리고 완료해도 workdir 가 안 지워진다(**S-29**). 둘 다 8단계의 흐름을 막지 않는다. §2 |
| b | **Hermes 프로파일**로 같은 시나리오 + 폴백 전환(E8-08) + 대안 없음(E8-09) | **통과** | `30_scenario_a_hermes.sh` **PASS 57 / FAIL 0**(93초). Researcher 를 hermes 로 두고 위임 3 → **hermes lane 3개 동시 running** → 합류 2회(그룹당 시스템 메시지 1) → 종합 → Writer 초안 → `artifact_submitted` → 진행률 `1/2`. hermes 자식 메시지 3/3, 실패 attempt 0. 도구 표면은 **래퍼 절대 경로 호출 6건**으로 실증했고 턴 뒤 정리 0(§3.2). 폴백 E8-08·E8-09 전부 통과(§3.3) |
| c | **blocked 왕복** E3-05·E3-06·E3-07 + 웹에서 질문 카드 | **통과 (순서 단서 있음)** | `31_blocked_roundtrip.sh` **PASS 26 / FAIL 2**. 세 행 전부 성립하고 재진입이 **같은 런타임 세션을 이어받았다**(`runtime.resume outcome=resumed`). 미달 2건은 표시·프롬프트 결함(S-27·S-28)이고 왕복 자체를 막지 않는다. **단서**: 위임자가 즉시 기상 통보에 바로 답하면 합류가 아예 발화하지 않는다(**S-31**, §4.3.1) — EVAL 이 적은 순서에서는 통과하지만 순서에 의존한다. §4 |
| d | **루프 상한** E4-03 — 워크스페이스 설정으로 낮춰 `paused(loop)` + `paused_detail.loop.limit` | **통과** | `32_loop_limit.sh` **PASS 13 / FAIL 1**. 설정 op 는 P2 에 있다(HTTP 200). 상한 2 에서 관측 왕복 3 → `paused(loop)` · `limit=pair_roundtrips` · `count=3` · `agents` 2명 · 넘긴 트리거로 task 0 · Director HITL `source=system`. 미달 1건은 **S-26**(부분 갱신이 같은 객체의 다른 키를 지운다) §5 |
| e | **템플릿 3분** — 팀 템플릿에서 팀 생성 → 세션 시작 | **경로 통과 · 시간은 Director 실측 대기** | `34_template_3min.sh` **PASS 14 / FAIL 0**. 자동 조작 하한 **10초**(로그인→템플릿 0s, 템플릿→세션 9s, 첫 task 까지 10s). 사람 실측 절차와 예상 소요는 §6 |

**판정 논리.** G5 가 묻는 것은 "협업 코어가 **여러 런타임 위에서, 사람이 끼는 지점까지 포함해**
끝까지 도는가"다. 다섯 항목이 전부 실기에서 섰고 수치는 DB 에서 다시 셀 수 있다 —
Hermes 를 섞은 시나리오가 위임부터 아티팩트 제출까지, blocked 왕복이 질문·합류·재진입·resume 까지,
루프 상한이 정지·상세·알림까지, 승인이 `completed` 와 요약까지, 템플릿이 팀 생성부터 세션 시작까지.

**한 판 만에 되지 않았다는 것이 이 게이트의 내용이다.** 1판은 (b) 가 통째로 막혀 있었고
(D-7 — 데몬이 Hermes 에게 도구를 건네는 자리), 그 자리는 라우터도 lane 도 합류도 아니었다.
`acpfake` 는 원리적으로 그것을 잡을 수 없었고(구현과 같은 가정을 공유한다), probe 는 초록이었다.
통합이 그것을 드러냈고 #97 이 계약에 `tool_surface` 칸을 만들어 닫았다 —
**G4 가 적어 둔 "통합에서만 드러나는 부류"의 이번 사례**다.

남은 FAIL 5건은 표시(S-27)·프롬프트(S-28)·정리(S-29)·설정 병합(S-26)과 승인 입구의 P3 이월(S-25)이고,
어느 것도 DoD 문장을 막지 않는다. 판정이 남은 칸은 (e) 의 **3분 수치 하나**이며 그것은 사람이 잰다.
순서 의존 하나(S-31)는 §4.3.1 에 증거와 함께 적어 뒀다 — 통과 판정을 뒤집지는 않지만
FR-6.5 의 묶음이 조용히 사라지는 경로라 그냥 두지 않는 편이 낫다.

---

## 2. (a) 종료 조건 → 승인 → `completed` (E6-01 · E6-03)

재현: `bash e2e/p2/33_approval_completed.sh` · 산출물 `out/approval.json`·`out/a3-checks.tsv` · 50초.

### 2.1 E6-01 — 지정 에이전트 제출이 조건을 채우고 플랫폼이 HITL 을 낸다

| 확인 | 값 |
|---|---|
| 제출 전 진행률 · `satisfied` | `0/2` · `false` |
| 제출자 | **Writer**(지정 에이전트) — 45초 만에 제출 |
| `artifact_submitted` · `user_approval` | `true` · `false` |
| 제출 후 진행률 | **`1/2`**, `human_gate=true` |
| 세션 상태 | **`active` 유지** (E6-01 문언대로) |
| 플랫폼 발행 HITL | `type=approval` **1건**, `source=system`, **`task_id` NULL**, `approver_spec=director`, `status=open` |

`source=system` + `task_id` 비움은 "**플랫폼이** 발행한다"(FR-2.2 · openapi §7)의 관측 가능한 형태다.
에이전트 턴이 낸 HITL 과 구분되는 유일한 표식이므로 셋을 따로 확인했다.

### 2.2 승인 경로 — `respondHitlRequest` 는 P3 다 (S-25)

`POST /hitl-requests/{id}/response` → **HTTP 501**. openapi 의 x-phase 가 P3 이므로 이 스택에서 옳다.
문제는 그 결과로 **`user_approval` 원자를 충족시키는 HTTP 입구가 P2 에 하나도 없다**는 것이다:

- `sessions/completion.go` 에 `director_approve` 이벤트가 구현돼 있고 `met[CondUserApproval]=true` 를 세운다.
- 그런데 그 이벤트를 부르는 핸들러가 없다. 부르는 것은 `artifact_submit`·`review_*`·`director_end`·`budget_exhausted` 뿐이다.

그래서 지시대로 **P2 의 승인 경로**(`completeSession`)로 E6-03 의 기대값을 옮겨 적용했다.
`completeSession` 은 FR-2.2 의 `manual`(E6-08)이고 "종료 조건과 무관하게 사람이 끝낸다"이므로
**전이·요약·정리**를 재는 데는 같은 경로다. 다만 채워지는 원자가 다르다 —
관측된 `completion_met` = `{"manual": true, "artifact_submitted": true}`, `user_approval` 은 **여전히 false**.

### 2.3 E6-03 — `active → completing → completed`

| 확인 | 기대 | 실측 |
|---|---|---|
| 세션 상태 | `completed` | **`completed`** |
| `finished_at` | 찍힘 | 찍힘 |
| `paused_reason`·`paused_detail` | 비워짐 | 둘 다 NULL |
| `session_summary` 메시지 | **1개** | **1개**, `author_type=system`, 213 B |
| 남은 `queued`/`deferred` task | 0 | **0**(완료 시 취소) |
| Director 인박스 `session_completed` | 1 | **1** |
| 격리 `none` workdir 즉시 삭제 | 0개 남음 | **1개 남음 — FAIL (S-29)** |

요약 본문은 현재 `## 세션 요약 … 목표 … ### 실행 lane 1개, 완료된 task 1개, 비용 $0.00` 이다.
**본문 품질은 P4** 이므로 여기서는 "전이가 서고 요약 메시지가 정확히 한 개 생긴다"만 봤다(지시대로).

**S-29.** 완료 시 서버가 낸 `gc` 명령 **0건**(`daemon_command where type='gc'`). `sessions/complete.go` 의
`completed` 분기가 세션·task·인박스는 정리하지만 workdir GC 훅이 없다. daemon-protocol §6 은
"**GC 판정은 서버**가 하고 서버가 `gc {workdir_ids}` 를 내면 데몬이 지운다" 이므로 빠진 쪽은 서버다.

---

## 3. (b) Hermes 프로파일 — **통과** (D-7 수정 뒤)

재현: `bash e2e/p2/30_scenario_a_hermes.sh` · 산출물 `out/hermes.json`·`out/h-checks.tsv`·
`out/h-caps.json`(능력 광고)·`out/h-wrapper-calls.txt`(래퍼 호출 증거)·`out/daemon-h.log`.
**PASS 57 / FAIL 0**, 시나리오 88초. 연속 두 번 같은 값이 나왔다.

### 3.1 probe — 능력 광고는 실기와 맞는다 (harness §9)

Researcher 프로파일만 `runtime_kind: hermes` 로 두고 나머지는 G4 와 같다.

| 확인 | 기대(harness §1·§9) | 실측 |
|---|---|---|
| hermes 능력 행 | 있음 | 있음 (`hermes 0.20.6`) |
| `logged_in` | true | **true** |
| `brief_transport` | `instruction_file` | **`instruction_file`** |
| `resume` | true (`session/load`) | **true** |
| `usage` | true (G1 F6) | **true** |
| `tool_disallow` | false (claude 와 갈린다) | **false** |
| `supported_options` | v1 은 비어 있음 | **0개** |
| `protocol_version` | 1 | **1** |
| 버전 | ≥ 0.20.6 | **0.20.6** |
| `colab_cli` | 런타임이 아니라 probe 최상위 | 최상위, `present=true` |
| 프로파일 `model` | 접두어 **없이** 저장 | `claude-haiku-4-5-20251001` |
| 광고 모델 id | 전부 `provider:model` | 43개 전부 접두어 있음(`anthropic:`·`openai-codex:`·`gemini:`…) |

`adapter_version` 은 hermes 행에서 **빈 문자열**이다. §1 이 hermes 에는 별도 어댑터 핀을 두지 않고
`hermes ≥ 0.20.6` 만 요구하므로 계약대로다(claude_code 는 `0.74.0` 핀이 실린다).

### 3.2 D-7 — 무엇이 막혀 있었고 #97 이 무엇을 바꿨나 **(수정됨)**

**1판(dev `6d9d20d`)의 증상.** 세 lane 이 병렬로 뜨고 셋 다 `outcome=completed stop=end_turn` 인데
**세션에는 아무것도 남지 않았다** — Researcher 메시지 0, `status set` 0, 합류 0, Lead 기상 1,
진행률 `0/2`, 실패 attempt 0. 하네스 눈에는 성공한 턴이었다.

원인은 두 표면이 **동시에** 끊긴 것이었다. 에이전트가 스스로 조사한 결과가 task_event 에 남았다:

| | 1판 상태 | 근거 |
|---|---|---|
| 브리프(`AGENTS.md`) | 있다 | `brief.transport=instruction_file`, `[1] Agent Identity` 로 시작 |
| `COLAB_*` 환경변수 | 있다 | 에이전트의 `env \| grep -i colab` 에 토큰 포함 7개 전부 |
| colab **MCP 도구** | **없다** | 도구 목록에 `colab_*` 0개. 직접 ACP 프로브: `initialize` 응답에 `mcpCapabilities` 가 아예 없고, `mcpServers` 를 실은 `session/new` 는 200 을 주면서 **조용히 버린다** |
| `colab` **실행 파일** | **없다** | `colab message post` → `command not found`. `echo $PATH` 에 저장소 `bin/` 이 없다 |

Claude Code 는 MCP 서버를 `colab_bin` **절대경로**로 띄우므로 PATH 가 필요 없었다 —
그래서 브리프 [2] 가 내내 `colab message post` 를 쓰라고 적어 온 표면을 아무도 실행해 본 적이 없었다.
도구도 명령도 없자 에이전트는 토큰을 들고 `curl $COLAB_SERVER_URL/api/v1/messages` 같은
**없는 경로를 지어내 두드렸다** — 막힘이 "아무 일도 안 일어남"으로 끝나지 않았다.

**#97 이 한 일.** harness v0.8/v0.8.1 로 계약에 **`tool_surface`** 칸이 생기고, 데몬이 `initialize` 의
`mcpCapabilities` 유무로 그것을 **실측**해 광고한다. `cli_wrapper` 로 판정된 런타임에는 attempt 마다
`<workdir_root>/.colab/bin/<task_id>.<attempt>/colab`(0700) 래퍼를 쓰고, **브리프 마커 구간과 턴
프롬프트의 명령 위치 `colab `** 을 그 절대 경로로 치환한다. 래퍼는 attempt 토큰을 담으므로 `finish`
에서 지운다.

**재측정 (dev `6b4fe78` 머지, `d61d3f5` 빌드 19:48:20).**

| 확인 | 기대 | 실측 |
|---|---|---|
| hermes `tool_surface` | `cli_wrapper` (§10 v0.8) | **`cli_wrapper`** |
| claude_code `tool_surface` | `mcp` — 두 런타임이 갈린다 | **`mcp`** |
| 래퍼가 실제로 **불렸는가** | 절대 경로 호출 ≥ 1 | **3건** — hermes lane 3개 각각 하나씩, 전부 `<workdir_root>/.colab/bin/<task>.<attempt>/colab` |
| `colab: command not found` | 0 | **0** |
| 턴 뒤 래퍼 정리 | 0개 남음 | **0** (토큰이 남지 않는다) |
| hermes 턴이 세션에 남긴 것 | 있음 | 메시지·`status set` 모두 있음 |

래퍼 경로는 `out/h-wrapper-calls.txt` 에 그대로 있다(예:
`…/e2e/p2/out/work-h/.colab/bin/152b3a64-….1/colab`). 데몬 로그는 정상 경로에서 래퍼 경로를 찍지
않으므로(오류·표면 불일치 때만) **에이전트의 도구 호출 자체가 증거**다 — `out/h-toolsurface.txt` 도
함께 남긴다.

**시나리오는 끝까지 돈다.**

| 확인 | 기대 | 실측 |
|---|---|---|
| Researcher lane | 위임 3 = lane 3 | **3** |
| hermes lane 동시 running | 3 (FR-6.3) | **3** |
| 합류 그룹 발화 | 2 (J1 Researcher 3 · J2 Writer 1) | **2**, 그룹당 시스템 메시지 **1** |
| hermes 자식 메시지 | 3 (E12-04 늦은 청크 유실 0) | **3** |
| hermes lane 종료 | 3개 전부 `done` | **3** |
| 실패 attempt | 0 | **0** |
| Writer 아티팩트 | 제출 | 제출 |
| 진행률 · 세션 | `1/2` · `active` | `1/2` · `active` |
| Lead 기상 | K-5 로 판정(아래) | **3** |

**K-5(E1-22)를 판정에 넣었다.** 기본은 3(시작 1 + 합류 2)이다. 자식이 `status set done` **뒤에**
한 줄을 더 올리면 그 시점에는 그 자식의 합류 그룹이 이미 발화한 뒤라 규칙 8 억제가 풀려 있고,
멘션은 일반 라우팅으로 위임자를 깨운다 — 결함이 아니라 K-5 결정이고, 위임자가 자식 발언 한 줄마다
깨어나지 않게 막는 것은 FR-3.4 의 lane 단위 병합이다. 그래서 단언을 상수 3 이 아니라
**`3 + 합류 뒤 자식 멘션이 만든 task 수`** 로 두고, 그 task 수가 **합류 뒤 자식 멘션 수를 넘지
않는지**(병합이 실제로 묶는지)를 함께 잰다.

- 최종 실행: 합류 뒤 자식 멘션 **0건** → Lead task **0개** → 총 기상 **3**.
- 그 직전 실행(세션 `f351c81d`)에서는 Writer 가 `done` **뒤에** `@Lead` 를 한 줄 올렸고,
  그때 J2 는 이미 발화한 상태라 그 멘션이 Lead 를 한 번 더 깨웠다(총 4, `coalesced_message_ids` 1건).
  **K-5 대로 옳은 동작**이다 — 상수 3 을 그대로 뒀다면 제품 결함으로 오보했을 자리다.

**남은 계약 빈칸.** `acpfake` 는 `mcpServers` 를 존중하도록 함께 구현돼 있어 이 부류를 원리적으로
잡을 수 없다 — `P2_TASKS §3` 이 e2e 로 넘긴 S3·S4 와 같다. #97 이 `tool_surface` 를 실측·광고로
만들었으므로 이제 **probe 가 초록인데 에이전트가 말을 못 하는** 상태는 광고에서 드러난다.

### 3.3 E8-08 · E8-09 — 프로파일 폴백 (전부 통과)

폴백은 hermes 를 **실패하는 프로파일**로만 쓰므로 D-7 과 독립이다. 고의로 모델 이름을 틀린
hermes 프로파일(`claude-haiku-4-5-TYPO`)을 기본으로 두고, 대체 프로파일을 claude_code 로 뒀다.

**우회 1건 (S-24).** `agent_profile.fallback_profile_id` 를 세울 정식 경로가 없어 DB 에 직접 썼다
(`lib.sh link_fallback`):

- `createAgent` 의 `AgentProfileCreate` 는 계약상 `fallback_profile`(이름)과 `fallback_profile_id` 를
  받는데(openapi), `agents.go` 의 INSERT 열 목록에 둘 다 없다 — **조용히 버린다**.
- `updateAgentProfile` 은 openapi x-phase **P2** 인데 `unimplemented.go` 에 남아 **501**.
- `createAgentProfile` 도 **501**.

연결만 DB 로 만들고 나머지는 전부 정식 경로로 측정했다.

| 확인 | 기대 (§4.4 · E8-08) | 실측 |
|---|---|---|
| attempt 1 실패 | 재시도 가능한 `failure_kind` | 실패, **`other`** (§8 상 2~3회 재시도) |
| 재큐잉 | `attempt` 증가 | **2 이상** |
| 프로파일 전환 | 대체 프로파일로 | task·lane 둘 다 **`spare`(claude_code)** 로 |
| **workdir** | 같은 것을 재사용(`workdir.reuse`) | **경로 동일**, workdir 행 **1개**(새로 만들지 않았다) |
| `runtime_kind` 변경 시 `resume` | 비운다 → 콜드 스타트 | 폴백 뒤 `runtime_session_ref.runtime_kind` = **claude_code**(새 세션) |
| 전환 결과 | 일이 끝난다 | task **`completed`** |
| 머신 | 같은 머신 (E8-09) | 세션 `runtime_id` 그대로 |

**E8-09 — 대안이 없을 때** (hermes 오타 프로파일 하나만 가진 에이전트):

| 확인 | 기대 | 실측 |
|---|---|---|
| Director 알림 | 있음 | 인박스 `run_failed` **1건** |
| 알림 중복 | task 당 1건(재시도 3회여도) | **1건** |
| 재큐잉 | `queued` 로 대기 | attempt **2건 이상** 기록 (재큐잉이 있었다는 증거) |
| 프로파일 | 전환할 대안이 없다 | **바뀌지 않음** |
| 다른 머신 | **넘기지 않는다** | attempt 의 `runtime_id` **1개** |

E8-09 의 "`queued` 대기"는 재시도 **사이의 상태**라 폴링으로는 놓친다(1차 실행에서 실제로 놓쳤다).
서버는 재시도 가능한 실패를 `queued` 로 되돌려야만 다음 attempt 를 만들 수 있으므로,
**attempt 2건 이상**을 그 증거로 삼았다.

### 3.4 (b) 요약

`30_scenario_a_hermes.sh` **PASS 57 / FAIL 0** (시나리오 88초). 연속 두 실행이 같은 값이다.
probe·계약 대조 15건(도구 표면 2건 포함), 시나리오 12건, 하네스 규칙 11건, 폴백 15건이 전부 통과했다.
1판의 FAIL 6건은 **전부 D-7 의 하류**였고 #97 로 사라졌다.

---

## 4. (c) blocked 왕복 — E3-05 · E3-06 · E3-07

재현: `bash e2e/p2/31_blocked_roundtrip.sh` · 산출물 `out/blocked.json`·`out/b-checks.tsv` ·
스크린샷 `web/__screenshots__/p2-b-01-question-card.png` · **PASS 26 / FAIL 2**, 58초 (세션 `077b9688`).

시나리오: Lead 가 3항목을 위임하고 그중 하나의 브리프에 `AMBIGUOUS` 표식을 둔다. 그 lane 의
Researcher 는 `colab status set blocked --note "경쟁 제품의 범위가 불명확합니다…"` 만 부르고 턴을 끝낸다.

### 4.1 E3-05 — 질문 카드 + 위임자 즉시 기상

| 확인 | 기대 | 실측 |
|---|---|---|
| 자식 lane 상태 | `blocked` | **`blocked`** (세션 시작 21초 뒤) |
| `lane.blocked_message_id` | 설정 | 설정 |
| 그 메시지의 `kind` | `blocked_q` | **`blocked_q`** |
| 작성자 | 자식 에이전트 | **Researcher** |
| `source_task_id` | 그 lane 의 task | 일치 |
| `lane.blocked_note` | 카드 본문 캐시 | 본문과 **일치** |
| workdir | 보존(프로세스만 종료) | **남아 있음** |
| 위임자 기상 | **즉시** 시스템 메시지 task 1개 | **1개** — 그 시점 종료된 형제 lane **0개**(형제 상태와 무관하게 성립한다는 것이 E3-05 의 요구다) |
| 프롬프트 문구 | "질문 알림이며 합류가 아니다" | `"…이것은 질문 알림이며 합류가 아닙니다 — 답만 하고 턴을 끝내세요."` |
| **카드 인용** | 시스템 메시지가 카드를 인용 | **FAIL — S-28** |

**S-28.** `router/status.go` 의 `wake()` 는 접두어 문자열만 `SystemPost` 한다. 위임자가 받는
시스템 메시지에는 **카드 id 도, 질문 본문도 없다**. 질문은 히스토리에 있으니 읽히기는 하지만,
위임자가 그 카드에 **답글을 달려면 id 가 필요한데** 트리거에는 없다.
합류 메시지는 질문 본문을 싣는데(§4.2) 즉시 기상 쪽만 안 싣는 것은 일관성도 깨진다.

### 4.2 E3-06 — 합류는 blocked 를 종료로 치고 질문을 다시 싣는다

형제 둘이 `done` 이 된 순간 합류가 발화했다(`join_fired_at` 1건, 시스템 메시지 **정확히 1개**).
그 본문:

```
위임한 작업이 모두 끝났습니다.
- Researcher: done
- Researcher: blocked — 질문: 경쟁 제품의 범위가 불명확합니다. 국내만인가요, 해외 포함인가요?
- Researcher: done

답을 기다리는 자식 1개가 있습니다. 답하기 전에 작업을 종료하지 마세요.
```

E3-06 의 세 요구(발화·질문 재포함·"답을 기다리는 자식 1개")가 문언 그대로 있다.

### 4.3 E3-07 — 답글 → 같은 lane 재진입 → resume

**정식 경로로 측정했다.** Lead 에이전트가 `colab_session_messages` 로 `kind=blocked_q` 카드를 찾아
`reply_to` 로 답글을 달았다(우회 폴백은 발동하지 않았다).

| 확인 | 기대 | 실측 |
|---|---|---|
| 해소 규칙 | 규칙 1 → 같은 lane (새 lane 없음) | Researcher lane **3개 그대로** |
| `reentry_count` | +1 | **0 → 1** |
| lane 상태 | `blocked → running` | blocked 를 벗어남 |
| 그 lane 의 task | 하나 더 | **2개** |
| 턴 프롬프트 | 그 lane 의 `runtime_session_ref` 로 resume 시도 | TaskBundle 에 `resume` 실림, `runtime_kind=claude_code` |
| 재진입 프롬프트 트리거 | 답글 본문 | 실림 |
| 실제 재개 결과 | `resumed` 또는 `cold_start` (§6 상 둘 다 정상) | **`runtime.resume outcome=resumed`** — 같은 ACP 세션을 이어받았다 |

**여기서 한 가지가 드러났다(S-28 두 번째 절반).** 앞선 실행에서 Lead 는 지시대로 **멘션 없이**
스레드 답글만 달았고, 그때 자식 lane 은 `blocked` 그대로 남았다(`reentry_count` 0, 새 task 0).
FR-3.3 규칙 4 — "에이전트의 멘션 없는 메시지는 아무도 트리거하지 않는다" — 가 먼저 걸리기 때문이다.
lane 해소(규칙 1)는 **누구를 깨울지 정해진 뒤**의 이야기라 트리거가 없으면 도달하지 않는다.
사람이 같은 답글을 달면 규칙 5 로 살아난다. 즉 **위임자 에이전트는 자식을 함께 멘션해야** 한다.
그런데 기상 프롬프트는 "답만 하고 턴을 끝내세요"라고만 하고 그 말을 하지 않는다 —
카드 id 부재(§4.1)와 합쳐지면 위임자가 스스로 규칙을 알아내야 푸는 길이 된다.
지시문을 그에 맞춰 고쳐 정식 경로로 다시 쟀고, 그것이 위 표다.

### 4.3.1 S-31 — 답을 받은 자식이 **마지막**으로 끝나면 합류가 영영 안 온다

E3-05 → E3-06 → E3-07 은 EVAL 이 적은 순서이고, 위 표는 그 순서로 잰 것이다. 순서를 바꿔 보다가
합류가 통째로 사라지는 경로를 만났다.

별도 실행(세션 `f80b092b`, 로그 `out/run-31-s28.log`·`out/b-checks-s28.tsv`)에서 Lead 가
**즉시 기상 통보를 받자마자** 답을 달았다. 그 실행의 판정은 **PASS 20 / FAIL 8** 이고,
FAIL 5건이 전부 "합류가 없다"에서 나왔다:

```
10:28:43  L2 blocked (질문 카드)              10:28:43  위임자 즉시 기상
10:28:56  Lead 답글 + @Researcher → L2 재진입
10:28:58  L1 done      10:29:02  L3 done      10:29:21  L2 done  ← 그룹이 이때 완성된다
→ join_fired_at = NULL. 시스템 메시지는 "요청하신 작업이 끝났습니다"(재진입 통보) 하나뿐.
```

`router/status.go` 의 `afterLaneDone` 이 원인이다:

```go
if reentry > 0 {
    return s.notifyReentry(…)   // ← 여기서 끝난다. maybeFireJoin 을 부르지 않는다
}
```

재진입 여부와 **그룹이 완성됐는가**는 별개인데 한 분기가 둘을 함께 처리한다. 그래서
"답을 받은 자식이 그룹의 마지막으로 끝나는" 순서에서는 위임자가 **합류 묶음을 영영 못 받는다**.
세션이 멈추지는 않았다 — 재진입 통보로 Lead 가 깨어나 히스토리만 보고 종합했다 — 하지만
FR-6.5 가 약속한 "자식 결과를 한 번에 묶어 전달"이 사라졌고, 이것은 조용한 손실이다.

위임자가 질문에 **빨리** 답할수록 밟기 쉬운 순서라 실제 사용에서 드문 경우가 아니다.
(E3-05 가 "즉시 기상"을 두는 이유가 바로 빨리 답하라는 것이다.)

위 §4.1~§4.3 의 수치는 EVAL 이 적은 순서 — 질문 알림은 알림으로 두고 **합류 묶음이 실어 온
질문에 답하는** 순서 — 로 잰 것이다. 지시문(`fixtures/blocked_agents.sh` STEP 2·2a)이 그 순서를
고정한다. 두 순서 모두 사람이 시킬 수 있는 것이므로, 둘 다 도는 것이 맞다.

### 4.4 웹 — S7 피드의 질문 카드

| 확인 | 기대(COMPONENTS §2.2 K3) | 실측 |
|---|---|---|
| 피드에 `blocked_q` 카드 | 렌더 | **1장** (`[data-kind="blocked_q"]`) |
| 질문 본문 | 보임 | 보임 |
| lane 보드 | blocked 카드 | **1장** |
| 배지 | `질문 → @위임자` | **`질문`** — FAIL (S-27) |

**S-27.** 웹은 배지의 위임자 이름을 `message.mentions` 에서 찾는다
(`MessageCard.tsx`: `m.kind === "blocked_q" ? m.mentions.find(agent)…`). 서버가 `blocked_q` 카드를
**멘션 없이** 삽입하므로(`router/status.go`) 이름을 채울 수 없고 웹은 폴백 라벨 `질문` 을 쓴다.
웹 코드는 두 경우를 다 다루고 있으므로 고칠 쪽은 **서버가 카드에 위임자 멘션을 싣는 것**이다.
(카드에 멘션이 실리면 CLI·히스토리에서도 "누구에게 묻는 질문인지"가 읽힌다.)

---

## 5. (d) 루프 상한 — E4-03

재현: `bash e2e/p2/32_loop_limit.sh` · 산출물 `out/loop.json`·`out/l-checks.tsv` · 64초.

**설정 op 는 P2 에 있다.** `PATCH /workspaces/{id}/settings` → **HTTP 200**(501 아님).
`max_pair_roundtrips` 를 **2** 로 낮추고 Lead↔Researcher 가 서로만 멘션하게 두었다.

`pairRoundtrips = (연속 같은 쌍 트리거 수 + 1) / 2` 이므로 상한 2 는 5번째 에이전트 트리거에서 걸린다.

| 확인 | 기대(E4-03) | 실측 |
|---|---|---|
| 세션 상태 | `paused` | **`paused`** |
| `paused_reason` | `loop` | **`loop`** |
| `paused_detail.loop.limit` | `pair_roundtrips` | **`pair_roundtrips`** |
| `paused_detail.loop.count` | 상한 초과 | **3** (> 2) |
| `paused_detail.loop.agents` | 도는 두 명 | **2명** |
| 상한을 넘긴 트리거 | **task 생성 안 됨** | 마지막 에이전트 메시지 뒤 새 task **0** |
| 진행 중이던 task | 정지 | **0** (§8.2.2 대로 취소) |
| Director 알림 | FR-3.5, `source: system` | HITL 1건, **`source=system`** |

에이전트 메시지 5 · task 5 · 소요 64초. `paused_detail` 원문:

```json
{"loop": {"count": 3, "limit": "pair_roundtrips",
          "agents": ["1f73384d-…", "0d6a6b63-…"]},
 "reason": "loop", "paused_at": "2026-09-06T10:21:18.899983Z"}
```

E4-03 문언은 기본값 5(=10 트리거)이고 여기서는 같은 규칙을 상한 2 로 재현했다 — 규칙이 상한을
읽는다는 것까지 함께 재기 위해서다. **기본값 5 로도 같은 코드 경로**이고, 그쪽은 P2a 골든
(`router/loop_golden_test.go`)이 이미 덮는다.

**S-26 (미달 1건).** 같은 요청에서 `max_chain_depth` 가 **`null` 로 지워졌다**.
`handlers_settings.go` 의 `mergeJSON` 은 이름과 달리 merge 하지 않고 부분 객체를 그대로
jsonb 열에 **덮어쓴다**. 라우터는 `DefaultLimits()` 에서 시작해 있는 키만 덮으므로 상한이 0 이
되지는 않지만, **관리자가 설정해 둔 값이 조용히 기본값으로 되돌아가고** S14 화면과
`getWorkspaceSettings` 는 `null` 을 보여준다. 세 상한 중 하나만 만지는 것이 정상 사용이므로
자주 밟는다.

---

## 6. (e) 템플릿에서 팀 생성 → 세션 시작

재현: `bash e2e/p2/34_template_3min.sh` · 산출물 `out/template.json`·`out/t-checks.tsv` ·
스크린샷 `web/__screenshots__/p2-t-01…06.png`.

**판정 칸은 "Director 실측 대기" 다.** DoD 의 3분은 사람이 재는 수치이므로 이 스크립트는
(1) 경로가 실제로 있는지 화면에서 확인하고 (2) 기계 소요를 재서 사람 실측의 **하한**을 준다.

### 6.1 경로 확인 — PASS 14 / FAIL 0

| 확인 | 실측 |
|---|---|
| S9 팀 템플릿 3종(FR-1.4) | **3장** — 리서치·개발·콘텐츠 |
| 프로파일 자동 매핑 | 템플릿 에이전트 **9개 전부 `mapped`**, `unmapped` 0 |
| 템플릿 한 번으로 일괄 생성 | 에이전트 **3명**, 화면과 DB 일치 |
| 기본 프로파일 | 3명 전부 보유(프로파일 없는 에이전트 **0**) |
| `definition_source` | 템플릿 키 `research_team` 기록 (FR-1.7) |
| S6 마법사가 그 에이전트를 참여자 후보로 | 보임 |
| 런타임 후보 | 연결한 컴퓨터가 보임 |
| 마법사 '시작' | 세션 생성 · S7 열림 |
| 세션이 실제로 돔 | 첫 task 가 **실행됨** |

### 6.2 기계 소요 (사람 실측의 하한)

| 구간 | 초 |
|---|---|
| 로그인 완료 → 템플릿 적용 완료 | **0** (템플릿 목록은 정적, 적용은 트랜잭션 1회) |
| 템플릿 적용 → 세션 생성(S7 열림) | **9** |
| **합계 (팀 생성 → 세션 시작)** | **10** |
| 세션 시작 → 첫 task 실행 | **10** |

### 6.3 Director 실측 절차

**전제(측정 구간 밖).** 계정·워크스페이스가 있고, 컴퓨터 한 대가 이미 페어링돼 **probe 가 끝나
런타임 후보로 뜬다.** 페어링부터 재면 `npx` 첫 설치가 분 단위로 들어와 3분 판정이 네트워크
속도 측정이 된다. 그리고 이 전제는 실제로 중요하다 — §6.4 참조.

스톱워치 **시작** = `/agents` 에서 **[팀 템플릿]** 을 누르는 순간.

1. 팀 템플릿 목록에서 **리서치 팀** 카드의 매핑 결과를 확인 → **[이 템플릿으로 만들기]**
2. 에이전트 3명이 목록에 뜨는 것 확인
3. **[새 세션]** → 제목·목표 입력 → 다음
4. Director/부재자 → 격리(없음) → **런타임 후보 선택** → 다음
5. 참여자 3명 체크 + **assignee 지정** → 다음
6. 종료 조건 확인(기본 = 아티팩트 제출 AND 승인), 제출자 지정 → 다음
7. 한도·autonomy 확인 → **[시작]**

스톱워치 **정지** = S7 이 열리고 lane 보드에 첫 카드가 뜨는 순간.

**예상 소요.** 기계 하한 10초 + 사람이 읽고 고르는 시간. 2~6 단계는 각각 읽을 것이 있는 화면이라
단계당 20~40초로 보면 **약 2분 ~ 2분 30초**. 3분 안에 들어올 것으로 보지만, 판정은 Director 가 한다.

### 6.4 S-30 — 매핑이 실패하면 프로파일 없는 에이전트가 남는다

1차 실행에서 매핑이 **9개 전부 실패**했다(`unmapped=9`, 사유 "감지된 런타임이 없습니다").
원인은 테스트 쪽이었다 — `daemon run --no-turn` 이 재시작 때 `capabilities.models` 를 빈 배열로
덮는다(G3_REPORT §2 이미 아는 것). 턴을 도는 `run` 으로 바꾸니 `mapped=9` 가 됐다.

그런데 그 과정에서 제품 쪽 구멍이 보였다. `applyAgentTemplate` 은 매핑에 실패해도 에이전트를
만들고 **프로파일은 만들지 않는다**(`templates.go`: `if kind == "" { unmapped… ; continue }`).
openapi 도 "매핑 불가 에이전트도 등록하되 `unmapped[]` 에 사유"라고 그렇게 적었다.
문제는 **그 뒤를 이을 경로가 P2 에 없다**는 것이다 — `createAgentProfile`·`updateAgentProfile` 둘 다
501(S-24). 프로파일 없는 에이전트는 세션에서 쓸 수 없으므로, 런타임을 연결하기 전에 템플릿을
누른 사람은 **되돌릴 수 없는 3명**을 얻는다. 3분 DoD 를 가장 흔하게 깨는 경로이기도 하다
(처음 쓰는 사람이 컴퓨터 연결보다 팀 만들기를 먼저 누르는 것은 자연스럽다).

---

## 7. 결함 목록 (스트림 귀속)

번호는 `plan/P2_BACKLOG.md` 의 S-21·S-22·S-23(PR #95 리뷰, 비용 롤업)과 겹치지 않게 **S-24 부터** 붙였다.

| ID | 스트림 | 내용 | DoD 영향 | 근거 |
|---|---|---|---|---|
| **D-7** | D + 계약 | **~~hermes 런타임에 colab 도구 표면이 닿지 않는다~~** — MCP 는 어댑터가 무시하고(`mcpCapabilities` 미광고, `session/new` 는 200), CLI 는 `colab` 이 PATH 에 없었다. `COLAB_*` env 는 가고 있었으므로 끊긴 것은 **도구와 실행 파일**이었다 | ~~(b) 차단~~ → **수정됨 (#97)**: `tool_surface` 실측·광고 + attempt 래퍼 + 브리프·턴 프롬프트 치환. 30_ 재실행 **57/0** | §3.2 — 1판 근거는 세션 `0661186a`·`6b49f0ad` 의 task_event 와 직접 ACP 프로브, 수정 확인은 `out/h-wrapper-calls.txt` |
| **S-24** | S | `createAgent` 가 `AgentProfileCreate.fallback_profile(_id)` 를 조용히 버린다(INSERT 열 목록에 없음). `createAgentProfile`·`updateAgentProfile` 은 x-phase P2 인데 501 | (b) E8-08 **우회 필요** · (e) S-30 의 원인 | §3.3, `agents.go:135`, `unimplemented.go` |
| **S-25** | S | `user_approval` 원자를 충족시키는 HTTP 입구가 P2 에 없다. `director_approve` 이벤트는 구현돼 있으나 호출자가 없고 `respondHitlRequest` 는 P3(501) | (a) **승인 경로 대체** | §2.2, `sessions/completion.go` |
| **S-26** | S | `updateWorkspaceSettings` 의 부분 갱신이 같은 객체의 다른 키를 지운다(`mergeJSON` 이 merge 가 아니라 replace) | (d) FAIL 1 | §5, `handlers_settings.go:202` |
| **S-27** | S (표시는 W) | `blocked_q` 질문 카드에 위임자 멘션이 없어 K3 배지가 `질문 → @위임자` 가 아니라 `질문` 으로 떨어진다 | (c) FAIL 1 | §4.4 |
| **S-28** | S + 계약 | 위임자 기상 시스템 메시지가 **카드를 인용하지 않는다**(E3-05 (3)). 더해서 위임자가 멘션 없이 답글만 달면 규칙 4 로 자식이 안 깨어나는데 프롬프트가 그 말을 하지 않는다 | (c) FAIL 1 · 왕복은 성립 | §4.1·§4.3, `router/status.go wake()` |
| **S-29** | S | 세션 완료 시 격리 `none` workdir 가 지워지지 않는다(E6-03). 서버가 `gc` 명령을 0건 낸다 | (a) FAIL 1 | §2.3, `sessions/complete.go` |
| **S-30** | S + 계약 | 템플릿 매핑 실패 시 **프로파일 없는 에이전트**가 남는데 P2 에 프로파일을 붙일 op 가 없다 | (e) 잠재 — 이번 실행은 통과 | §6.4, `agents/templates.go` |
| **S-31** | S | **재진입한 lane 이 자기 합류 그룹의 마지막으로 끝나면 합류가 발화하지 않는다** — `afterLaneDone` 이 `reentry > 0` 에서 `notifyReentry` 로 빠지고 `maybeFireJoin` 을 부르지 않는다 | (c) 순서 의존 — FR-6.5 묶음이 조용히 사라진다 | §4.3.1, 세션 `f80b092b` |

**G4 에서 넘어온 것 중 이번에 다시 본 것.** `D-6`(비용 추정)·`S-16`(`listParticipants` 501)은
이 브랜치 시점(dev `6d9d20d`) 기준으로 아직 열려 있었다. 다른 워커가 그 뒤 PR #93·#95 로
닫은 것으로 보이므로 이 보고서의 스택에는 반영돼 있지 않다.

---

## 8. 통합에서만 보인 것 (§10.7 되먹임)

- **"광고는 초록인데 실제로는 못 쓴다"가 이번의 새 부류였다.** hermes 는 probe 항목을 전부
  통과했다 — 로그인·재개·usage·브리프 전송까지. 그런데 도구가 없었다. 능력 광고가 **런타임이
  할 수 있는 것**만 말하고 **플랫폼이 그 런타임과 말할 수 있는가**는 말하지 않았기 때문이다.
  #97 이 `tool_surface` 를 실측 항목으로 만들어 그 칸을 메웠다 — 이제 같은 상태는 광고에서 드러난다.
- **한 런타임이 두 표면을 다 쓸 수 있다고 아무도 확인하지 않았다.** Claude Code 는 MCP 만으로
  충분해서 CLI 를 이름으로 부를 일이 없었고, 그래서 `colab` 이 PATH 에 없다는 사실이 P1·G3·G4 를
  전부 통과했다. 브리프 [2] 는 그동안 내내 `colab message post` 를 쓰라고 적고 있었다 —
  **문서가 약속한 표면을 아무도 실행해 본 적이 없었던 것**이다.
- **표면이 없으면 에이전트가 API 를 지어낸다.** 도구도 명령도 없자 hermes 에이전트는 토큰을 들고
  `curl $COLAB_SERVER_URL/api/v1/messages` 같은 없는 경로를 두드렸다(§3.2). 막힌 것이 조용히
  "아무 일도 안 일어남"으로 끝나지 않는다는 뜻이다.
- **fake 가 증명할 수 없는 것을 e2e 가 정확히 그 자리에서 잡았다.** `acpfake` 는 `mcpServers` 를
  존중하도록 함께 구현돼 있어 통과했다. P2_TASKS §3 이 S3·S4 를 e2e 로 넘긴 판단이 값을 했다.
- **"규칙이 옳은데 사람에게 그 말을 안 한다"가 두 번 나왔다**(S-28 두 절반, S-30).
  규칙 4 도 unmapped 도 각각 문서대로다. 문제는 그 규칙에 걸린 사람에게 다음 한 걸음을
  알려주는 문장이 없다는 것이다. 프롬프트와 응답 본문은 계약의 일부로 다뤄야 한다.
- **스크립트를 돌리는 중에 그 스크립트를 고치면 죽는다.** bash 는 파일을 조금씩 읽는다.
  이번 실행에서 한 번 실측을 잃었다. 재실행 전에만 고친다.
- **`daemon run --no-turn` 은 능력 광고를 지운다**(G3_REPORT §2 에 이미 있다). 이번에는 그것이
  템플릿 매핑을 통째로 실패시켜 다른 결함처럼 보였다. e2e 에서 능력 광고가 필요한 경로는
  반드시 `daemon_start_p2`(턴 도는 run)를 쓴다 — `lib.sh` 주석에 적어 두었다.

---

## 9. 다음

1. ~~**D-7**~~ — **닫혔다(#97).** 30_ 재실행 57/0, 연속 두 번 같은 값. §3.2.
2. **템플릿 3분 Director 실측** — G5 에서 판정이 남은 유일한 칸이다. 절차는 §6.3.
3. **S-24** — 프로파일 op(생성·수정)를 열면 E8-08 우회가 사라지고 S-30 도 같이 닫힌다.
4. **S-29** — 완료 시 workdir GC. E6-03 의 마지막 칸이다.
5. **S-25** — `user_approval` 입구. P3 로 두는 것이 결정이라면 EVAL E6-01·E6-03 의 P2 판정 문언을
   `completeSession` 기준으로 정정해야 한다(지금은 문서와 구현이 다른 것을 가리킨다).
6. **S-31** — `afterLaneDone` 에서 재진입 통보와 합류 판정을 분리한다. 조용한 손실이라 우선순위가 낮지 않다.
7. **S-26 · S-27 · S-28 · S-30** — 각각 한 곳 수정. DoD 를 막지는 않는다.
