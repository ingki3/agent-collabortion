# 멀티 에이전트 프레임워크는 맥락을 어떻게 유지·관리·회상하는가 — Colab 설계용 조사

조사일 2026-09-28 · 선행 보고: `scratchpad/context-memory-research.md`(3층 제안·§6 질문), `scratchpad/graphify-research.md`
방법: 공식 문서·README 를 읽고, 가능한 곳은 `gh api` 와 얕은 클론으로 **소스를 직접** 읽었다. Orca 는 로컬 `orca` 1.4.215 의 읽기 전용 명령(`--help`, `skills get`, `run-list`, `task-list`, `dispatch-show --preamble`, `search --index-status`)과 공개 저장소 소스를 봤다. 워커 시작·정지나 메시지 전송은 하지 않았다.
표기: **[미검증]** 은 문서 문구나 2차 자료뿐이고 소스·실측으로 확인하지 못한 것이다.

---

## 0. 결론 먼저

1. **3층 제안(가 수명주기 · 나 상태 원장 · 다 회상)은 조사한 거의 모든 시스템과 방향이 같다.** 다만 세 군데를 고친다.
   - **(가)를 더 세게.** "재개 세션에 델타만 싣기"로 끝내지 말고 **"재개는 조건부, 기본은 원장으로 새 세션"** 으로 간다. 근거는 네 가지다.
     - Anthropic Agent SDK 문서: "Don't rely on session resume … pass them into a fresh session's prompt."
     - Managed Agents: 세션은 창 밖의 이벤트 로그이고 컨텍스트는 매번 새로 조립한다.
     - Claude Code 자체: 1시간 넘게 쉬었고 100k 토큰을 넘는 세션은 "요약에서 재개"를 제안한다.
     - multica: 재개 실패 시 "기록이 정본, 작업 기억만 잃었다"는 연속성 공지를 넣는다.
   - **(가)에 "밀지 말고 끌어 읽게"를 앞당긴다.**
     - multica 는 프롬프트를 일부러 짧게 둔다(트리거 + 이슈 id + "먼저 `issue get` 하라"). 댓글은 `--roots-only --summary`(200룬) 다음 `--thread --tail 30` 의 두 단계로 읽게 한다.
     - Colab 의 `<mission_messages>` 전부 싣기는 원장(2단계)을 기다리지 말고 1단계에서 **"미션 머리글 색인 + 최근 N"** 으로 줄여도 된다.
   - **(다)의 벡터는 기본이 아니라 옵션으로.**
     - 코딩·오케스트레이션 도구는 모두 **어휘 검색 + 에이전트가 부르는 도구**만 쓴다. Claude Code 는 RAG 를 버렸고, Orca 는 SQLite FTS5, multica 는 CLI 끌어 읽기, graphify 는 IDF+트라이그램이다.
     - 벡터를 쓰는 쪽은 개인화 메모리 제품(Mem0·Zep·Letta archival)이다.
     - 그래서 트라이그램/FTS 로 먼저 문항을 재고, 모자랄 때만 pgvector 를 붙인다.
2. **상태 원장(나)의 모양은 Magentic-One 원장이 가장 가깝다. 여기에 Letta 블록 상한, Graphiti 무효화, Anthropic feature-list JSON 을 섞는다.**
   - **task ledger** 는 사실을 4분류(주어지거나 확인된 것 / 찾을 것 / 도출할 것 / 추정)하고 계획을 붙인다.
   - **progress ledger** 는 매 턴 `is_request_satisfied / is_in_loop / is_progress_being_made / next_speaker / instruction` JSON 이다.
   - **정체 3회면 사실을 다시 쓰고 재계획한 뒤, 모든 참가자의 대화를 비우고 원장 한 메시지로 재시작**한다. 원장이 곧 압축본이다.
   - Colab 원장에 `kind=plan`·`kind=progress` 두 칸을 더하고, "정체 → 원장으로 cold start" 를 수명주기와 묶는다.
3. **위임 인계는 모두 같은 모양으로 수렴한다. 넘길 때는 자기완결 명세, 받을 때는 짧은 요약 + 산출물 참조다.**
   - Orca: 명세 5칸(Target/Change/Constraints/Ownership/Acceptance), `worker_done` 은 3문장, "coordinator reads the body first and only opens artifacts if it needs more detail".
   - Anthropic research: 1~2k 토큰 요약 + artifacts bypass lead.
   - LangGraph `output_mode=last_message`, OpenHands `delegate` 는 final response 텍스트만, Roo Boomerang 은 `attempt_completion` 요약만 돌려준다.
   - Colab 의 보고 메시지(conversation + detail)는 이 모양을 이미 갖췄다. 빠진 것은 **보고 본문 규격**(한 일 / 찾은 것 / 남은 것 + 참조 id)과 **Lead 가 detail 을 기본으로 안 여는 규율**이다.
4. **캐시 규율에서 새로 확인한 것은 두 가지다.**
   - **(a) 압축·도구 결과 지우기는 드물게, 크게 한다.** OpenHands README 원문은 "condensation destroys the prompt cache, but doing so regularly keeps the cost … low" 이고, SWE-agent 에는 `polling`, API 에는 `clear_at_least` 가 있다.
   - **(b) 캐시 TTL 5분.** SDK·API 키 경로는 기본이 5분이다. Lead 의 깨움 간격이 5분을 넘으면 재개 세션 전체가 **캐시 쓰기**로 다시 청구된다. Colab 실측에서 cache_read 가 크다는 것은 대체로 적중했다는 뜻이지만, cache_creation 비중도 0단계에서 같이 봐야 한다.
5. **graphify 에서 가져올 새것은 "결과 표시가 붙은 작업 기억"이다.** `save-result --outcome useful|dead_end|corrected` 로 남기고, `reflect` 가 LLM 없이 결정적으로 LESSONS.md 를 만든다. 뒷받침이 2건 이상이어야 "선호"가 되고, 반감기 30일로 감쇠하며, 인용한 코드 지문이 바뀌면 "재확인" 으로 격하한다. Colab 원장의 `kind=lesson` 에 그대로 쓸 수 있다. 그래프 자체는 여전히 도입하지 않는다(선행 보고 결론 유지).

---

## 1. 대상별 조사

각 항목은 같은 틀로 적는다. 겨냥하는 문제 → 맥락 모델(층, 저장하는 것, 쓰는 주체, 압축 시점, 회상 방법) → 공유·인계 → 비용 통제 → 성숙도·라이선스 → Colab 이 베낄 것·피할 것.

### 1.1 graphify (Graphify-Labs/graphify) — Director 지정

- **문제:** 에이전트가 원본을 grep 하거나 통째로 읽어 토큰을 쓰는 것을 그래프 질의로 바꾼다. 선행 보고의 정체(코드는 tree-sitter, 문서는 LLM 추출, `graph.json`, MCP)는 그대로다.
- **에이전트 맥락 주입(새로 확인):**
  - **상시 안내문.** `graphify/always_on/` 의 템플릿을 CLAUDE.md·AGENTS.md·`.cursor/rules/graphify.mdc`(alwaysApply)에 써 넣는다. 요지는 "`graphify query/path/explain` 먼저, GRAPH_REPORT.md 는 넓은 구조용, 편집 뒤 `graphify update .`" 이다.
  - **PreToolUse 훅.** `graphify hook-guard <search|read>`(`graphify/cli.py:814`)가 Bash·Grep·Read·Glob 직전에 `additionalContext` 로 권유를 넣는다. `--strict` 면 세션의 첫 원본 읽기만 `deny` 로 한 번 막고 이후엔 권유로 내려간다. 최근 질의가 있으면 차단을 끈다.
  - **질의 토큰 예산.** `serve.py:1087` `_subgraph_to_text(token_budget=2000)` 은 약 3자를 1토큰으로 세고, 씨앗 노드는 자르지 않으며, 넘치면 고지한다.
  - **스킬 분할.** SKILL.md 를 핵심 약 615줄과 필요할 때만 읽는 `references/` 로 나눠 상시 컨텍스트를 약 47% 줄였다.
- **작업 기억(work memory):**
  - ① `graphify save-result --question --answer --nodes --outcome useful|dead_end|corrected [--correction]` 을 **에이전트가 직접** 호출하고, 결과는 `graphify-out/memory/*.md` 에 쌓인다(`ingest.py:278`). 다음 추출 때 그래프로 되먹여진다.
  - ② `graphify reflect` 가 이것을 **LLM 없이 결정적으로** `reflections/LESSONS.md` 5절(선호 출처 / 잠정 / 논쟁 / 막다른 길 / 정정)로 증류한다(`reflect.py`).
    - 점수에 부호가 있고 반감기 30일로 감쇠한다.
    - 서로 다른 결과가 **2건 이상** 뒷받침해야 "선호"가 된다.
    - 그래프에서 사라진 노드는 빠진다.
  - ③ `.graphify_learning.json` 오버레이가 query·explain 결과에 "Lesson:" 힌트를 붙인다. 인용 코드의 지문이 바뀌면 "code changed — re-verify" 로 격하한다. **교훈이 탐색 순위를 바꾸게 하는 것은 자기강화 루프가 걱정돼 일부러 보류했다.**
  - 세션 시작 때 `reflect --if-stale` 을 돌리고 LESSONS.md 를 읽힌다.
- **대화 기억:** 제품 차원의 대화 수집은 없다. 오디오·영상은 faster-whisper 로 전사해 문서로 넣는다. "회의까지 상시 갱신"은 상용 플랫폼 광고 문구뿐이다 **[미검증]**.
- **공유:** 레포 안 파일 또는 MCP HTTP 서버 하나를 같이 쓴다. 에이전트별 신원·권한은 없다.
- **비용:** 질의 예산, 코드는 AST 만, reflect 에 LLM 을 쓰지 않는다.
- **성숙도:** Apache-2.0(+MIT 파일), ★약 12만, 2026-04 생성, 최신 v0.9.70(2026-09-27). 사실상 1인 주도다.
- **Colab:**
  - **베낄 것:** 결과 표시(useful/dead_end/corrected)가 붙은 항목, 2건 이상 뒷받침 규칙, 시간 감쇠, 출처 지문이 바뀌면 격하 → 원장 `kind=lesson`. 그리고 "강제 1회 후 권유" 훅 패턴 → Lead 역할 규율(Q2)의 완화형.
  - **피할 것:** 그래프 추출(비결정·Python 사이드카).

### 1.2 Orca (stablyai/orca) — Director 지정, 이 프로젝트가 쓰는 도구

- **문제:** 여러 코딩 에이전트 CLI(Claude Code·Codex·OpenCode 등)를 git worktree 마다 병렬로 돌리고, 코디네이터가 감독하는 ADE 다.
- **성숙도:** MIT, TypeScript(Electron), ★약 8만, 2026-03 생성, 매일 push. 로컬 버전 1.4.215.
- **맥락 모델: 공유 메모리가 없다. 맥락은 전부 "명시적으로 전달되는 짧은 문서"다.**
  - **Run** 은 코디네이터 inbox 겸 이름공간이다. `run-create --objective`.
  - **Task spec** 은 반드시 자기완결이어야 하고, 스킬이 5칸을 강제한다: **Target / Change / Constraints / Ownership / Observable acceptance.** 바이트 상한도 있다. `ORCHESTRATION_WORKER_START_TASK_SPEC_MAX_BYTES` 는 터미널 붙여넣기 예산에서 프리앰블 예약분을 뺀 값이다(`src/shared/orchestration-worker-start-prompt-budget.ts`).
  - **Dispatch preamble**(`src/main/runtime/orchestration/preamble.ts` `buildDispatchPreamble`)은 고정 헤더 뒤에 `=== TASK ===` + spec 을 붙인다. 헤더에는 코디네이터 handle, task/dispatch id, 명령 예시가 들어간다.
    - 설계 주석: *"Behavioral rules … live as inline comments above the relevant CLI example … LLM readers anchor on examples and skim trailing prose."* 규칙은 쓰는 자리에 둔다.
    - **BASE DRIFT 절**은 worktree 가 base 보다 뒤처졌을 때만 넣는다(뒤처진 커밋 수 + 최근 제목 5개). *"polluting it for fresh worktrees would train workers to ignore it."* 드문 신호는 드물 때만 넣는다.
    - 하위 위임 절은 깊이 한도상 가능할 때만 넣는다("못 할 일을 광고하면 턴을 태운다").
  - **메시지:** 내구성 있는 mailbox(FIFO Delivery 최대 50건, ack 전까지 재생)다. `send`/`check`/`ask`(블로킹, timeout 뒤 같은 id 로 resume)/`reply`/`escalation`/`heartbeat`(5분). 주소는 `dispatch:<id>`, `@all`, `@claude`, `@worktree:<id>` 등이다. 코디네이터의 후속 지시는 끼어들지 않는다. 워커가 **자연스러운 체크포인트마다**(새 파일 시작 전, 테스트 뒤, worker_done 직전) `check` 한다.
  - **완료 보고 `worker_done`:** 정확히 한 번 보낸다. `--body` 는 **3문장**(한 일 / 찾은 것 / 남은 것), `--outcome succeeded|failed` 는 명시한다("실패를 산문에만 숨기지 마라"), `--files-modified`·`--report-path` 는 실제 값만 붙인다. 프리앰블 주석: *"the coordinator reads the body first and only opens artifacts if it needs more detail."*
  - **권한·펜싱:** 수명주기 권한은 활성 Dispatch 에만 있다. 재시도 뒤 늦게 온 옛 보고는 dispatchId 로 거른다. `consumer_fenced` 는 "너는 교체됐다"의 유일한 신호다.
  - **코디네이터의 외부 기억:** `task-list --ready --brief`(spec 을 160자로 자름)를 "external memory" 로 쓰라고 한다. 결정은 `gate-create/resolve` 로 DAG 에 기록한다.
  - **worktree 메타:** `worktree set --comment`(짧고 최신인 한 줄 상태), 보드 상태, 이슈 링크.
  - **세션 전문 검색 `orca search`:** 호스트의 에이전트 transcript 를 **SQLite FTS5**(`tokenize="unicode61 tokenchars '_.-/+'"`, `session-search-schema.ts`)로 색인한다.
    - 사용자·어시스턴트 글은 전문을 색인하고, 도구 출력은 행당 3,072자까지만 색인한다.
    - 오타 보정·식별자 분리·질의 플래너를 쓴다. 벡터는 없다.
    - 로컬 색인 16,901 파일(읽기 전용 `--index-status` 로 확인).
- **cold start 와 재사용:** 워커는 새 터미널·새 에이전트에서 프리앰블 + spec 만 받고 코디네이터 이력은 받지 않는다. 같은 에이전트를 즉시 후속 작업에 재사용할 수는 있다(`worker-start --terminal`, "fresh preamble + TASK block").
- **비용 통제:** 이력 비상속(격리), spec 바이트 상한, 3문장 요약, 중첩 깊이 상한, `--brief`.
- **Colab:**
  - **베낄 것:**
    - ① 위임 명세 5칸을 lane 위임 메시지 규격으로(브리프 [3]).
    - ② 보고 3문장 + outcome enum + 참조 id. "Lead 는 본문을 먼저 읽고 detail·아티팩트는 필요할 때만" 을 명문화한다.
    - ③ 조건부 절(드리프트처럼 드문 신호는 드물 때만) → 턴 프롬프트의 `<rebind>`·`<resumed>` 류와 같은 원칙을 재확인한다.
    - ④ 규칙은 예시 옆 주석으로 → 브리프 [2] 문구 설계.
    - ⑤ 어휘 FTS 만으로 된 세션 검색 → (다)를 어휘 먼저로.
  - **피할 것:** Orca 에는 **공유 상태 면이 없다**. 코디네이터 대화가 곧 상태이고, 이 프로젝트 Lead 가 겪는 누적 문제를 Orca 는 풀어 주지 않는다. Colab 은 원장이 필요하다.

### 1.3 multica (multica-ai/multica) — Director 지정, PRD 가 멘션 라우팅을 빌린 곳

- **문제:** 이슈 보드의 팀원으로 여러 에이전트 CLI 를 두고, 할당하거나 @멘션하면 로컬 데몬에서 실행한다. **Go 서버 + sqlc + Postgres + 데몬이라 Colab 과 모양이 가장 비슷하다.**
- **성숙도:** ★약 5.1만, 2026-01 생성, 커밋 약 5,500, v0.5.3(2026-09-24). 라이선스는 "Multica License"(Apache-2.0 + 조건: 제3자 호스팅·상용 임베드는 상용 라이선스, 브랜딩 제거 금지). **코드를 가져오면 안 되고 개념만 참고한다.**
- **프롬프트는 일부러 짧게 — 끌어 읽기(pull).** `server/internal/daemon/prompt.go` `BuildPrompt` 는 이슈 id, 트리거 댓글(작성자 종류 표시), "`multica issue get <id> --output json` 먼저" 만 넣는다.
  - 댓글 이력은 두 단계로 읽게 한다. `comment list --roots-only --summary --compact`(댓글당 200룬, `handler/comment.go:144`) 다음 필요한 스레드만 `--thread <id> --tail 30`. 한 번에 몽땅 받는 것은 금지다(MUL-5372).
  - 목록 상한 2000행 + `X-Comments-Truncated` 헤더, 본문 64KiB 상한.
- **대기 중 합치기:** 실행 시작 전 쌓인 댓글은 한 run 에 합친다(`CoalescedComments`, "모든 id 를 처리하기 전엔 끝내지 마라"). 대기 중 wakeup 은 `[WAKEUP — joined this run]` 블록으로 붙인다.
- **캐시 보존:** 매번 바뀌는 블록(권한 위임자, 연결 앱, 연속성 공지)은 브리프(messages[0])가 아니라 **사용자 메시지 뒤**에 붙인다(MUL-5377). Colab E12-11 과 같은 원칙이다.
- **세션 재개:**
  - `(agent_id, issue_id)` 마다 마지막 task 의 session_id·work_dir 를 재사용한다(`pkg/db/queries/agent.sql` `GetLastTaskSession`).
  - 재개에 실패하면 **연속성 공지**를 한 번 넣는다(`execenv/runtime_config_sections.go:568`: "이슈와 댓글 기록이 정본이다, 네 작업 기억만 사라졌으니 다시 도출하라").
  - **컨텍스트 창이 넘치면 실패(`agent_context_overflow`)로 재분류해 재개 포인터를 푼다**(`handler/daemon.go:4309`). 죽은 세션이 계속 재개되지 않게 하려는 것이다.
- **기억 층:**
  - ① 이슈 레코드(설명·댓글·metadata key-value)가 정본이다.
  - ② 공급자 세션 재개.
  - ③ Skills(워크스페이스 플레이북, CLAUDE.md·skills 로 주입).
  - ④ Hermes 전용 `memories/` 영속. 에이전트·머신 단위이고 동시 쓰기는 마지막 쓴 쪽이 이기며 90일 GC 다(`execenv/hermes_memory.go`).
  - **Codex 내장 기억은 강제로 끈다**(`execenv/codex_memory.go`, 워크스페이스를 넘어 샌 사고 #3130). 원칙은 "모든 기억 채널은 명시적이고 사람이 보고 고칠 수 있어야 한다."
- **인계:**
  - 재할당하면 handoff note("할당자의 범위 지시로 다뤄라, 댓글처럼 답하지 마라")가 붙는다.
  - Squad 리더 브리핑(`handler/squad_briefing.go`) 규칙: **"직접 하지 말고 @멘션으로 위임하라. 위임 댓글은 짧게, 이슈를 다시 요약하지 마라(모두 이미 읽는다)."** 자기 트리거·셀프 루프 가드, 하위 이슈 완료 콜백도 있다.
- **작업 공간:** bare repo 캐시에서 task 마다 worktree, 대화당 브랜치 하나를 이어간다.
- **비용:** 사용량 분석과 동시 실행 상한 20. **달러 예산 강제는 찾지 못했다** (grep 기준 **[미검증]**).
- **Colab:**
  - **베낄 것:**
    - ① 연속성 공지 문장(세션 교체·재개 실패 공통).
    - ② overflow → 재개 포인터 해제(lane 의 runtime_session 을 비우고 다음은 cold start).
    - ③ 두 단계 읽기(`room messages --top-only --summary` → `--thread --tail`).
    - ④ Lead 문구 "이슈를 다시 요약하지 마라".
    - ⑤ 공급자 내장 기억 끄기 점검(Claude Code auto memory 가 ACP 세션에서 켜져 있는지).
  - **피할 것:** 라이선스 때문에 코드 차용 금지. 그리고 `(agent, issue)` 무기한 재개는 Colab 의 390k 문제와 같은 모양이다.

### 1.4 Anthropic — Claude Code · Claude API · 엔지니어링 글

- **Claude Code:**
  - **CLAUDE.md 계층**(managed → 사용자 → 프로젝트 → local, 하위 디렉터리는 지연 로드, `@import` 4홉)은 시작 때 한 번 읽는다.
  - **auto memory** 는 `MEMORY.md` 한 줄 색인이고 **앞 200줄 또는 25KB 만** 로드한다. 세부는 토픽 파일로 두고 필요할 때 읽는다. 이 레포의 메모리 색인도 같은 구조다.
  - **compaction** 은 오래된 도구 출력을 먼저 지우고 그다음 요약한다.
    - 요약에 남는 것: 요청·의도, 핵심 개념, 파일·코드 조각, 에러와 해결, 남은 작업, 현재 작업.
    - 압축 뒤에는 시스템 프롬프트·CLAUDE.md·메모리·MCP 도구를 다시 로드하고, 최근 수정 파일 5개를 다시 읽는다.
    - `Compact Instructions` 절, `/compact <지시>`, PreCompact 훅, `SessionStart(compact)` 훅으로 재주입할 수 있다.
  - **재개:** 전체 대화를 다시 보낸다. **1시간 넘게 쉬고 100k 를 넘긴 세션은 "요약에서 재개"를 제안한다.** 캐시 TTL 은 SDK·API 키 경로 기본 5분이고 `promptCacheTtl=1h` 로 늘릴 수 있다.
  - **서브에이전트:** 새 컨텍스트에서 자기 프롬프트 + 위임 메시지 + CLAUDE.md + git status 만 받고, **요약만** 돌려준다.
  - **agent teams**(실험): 공유 task 목록 + 에이전트별 메일박스. 팀원은 Lead 이력 없이 spawn 프롬프트만 받는다. 재개하면 팀원이 복원되지 않는다.
  - **Agent SDK 문서:** *"Don't rely on session resume. Capture the results you need … as application state and pass them into a fresh session's prompt. This is often more robust."*
- **Claude API:**
  - `clear_tool_uses_20250919`: 기본 trigger 100k, 최근 3개 유지, `clear_at_least` 로 한 번에 크게.
  - `clear_thinking_20251015`
  - 서버 compaction `compact_20260112`: trigger 기본 150k, `instructions` 로 요약 프롬프트를 대체한다. 시스템 프롬프트 끝에 `cache_control` 을 두면 압축 뒤에도 시스템 캐시가 유지된다.
  - memory tool `memory_20250818`: `/memories` 파일 명령. 권장 패턴은 progress log + feature checklist 이고 "ASSUME INTERRUPTION" 을 전제한다.
- **엔지니어링 글:**
  - **multi-agent research:** Lead 가 계획을 memory 에 저장하고(200k 에서 잘리므로), 새 서브에이전트에 careful handoff 로 넘긴다. **서브에이전트는 결과를 외부 저장소에 쓰고 가벼운 참조만 반환한다.** 위임문 4요소는 목표·출력 형식·도구와 출처·경계다. 토큰 사용량이 성능 분산의 80% 를 설명하고, 멀티에이전트는 채팅의 약 15배를 쓴다.
  - **context engineering:** just-in-time 조회(식별자만 들고 있다가 필요할 때 읽기), tool-result clearing 이 가장 가벼운 압축, NOTES.md, 서브에이전트는 1~2k 토큰 요약.
  - **long-running harness:** initializer 가 `init.sh`·`claude-progress.txt`·feature list JSON(`passes` 칸)을 만든다. 원문 *"the model is less likely to inappropriately change or overwrite JSON"*. 매 세션 루틴은 pwd → git log/progress → 최우선 미완료 하나.
  - **harness design (2026-03):** reset 은 깨끗하지만 "handoff artifact 가 충분한 상태를 담아야" 한다. Opus 4.5 이후 context anxiety 가 줄어 SDK 자동 compaction 으로 한 세션을 이어갔다. 에이전트 사이는 **파일**로 소통하고 구현 전에 "sprint contract" 를 합의한다.
  - **Managed Agents (2026-04):** 세션은 **컨텍스트 창 밖의 append-only 이벤트 로그**다. 하네스가 `getEvents()` 로 필요한 구간만 잘라 **매번 컨텍스트를 새로 조립**한다. 이유는 "압축·잘라내기는 되돌릴 수 없는 결정" 이기 때문이다. Memory 는 `/mnt/memory/` 파일 + 쓰기 감사 로그 **[2차 출처]**.
- **Colab:**
  - **베낄 것:** 이벤트 로그(Postgres)가 정본이고 런타임 세션은 쓰고 버리는 캐시. 원장은 JSON 모양이고 상태 칸은 서버가 소유. 서브에이전트 요약 1~2k. 시스템 프롬프트 층(= Colab 브리프 [1]~[5], `_meta.systemPrompt.append`)은 압축 뒤에도 살아남는다.
  - **피할 것:** 1M 창에 기대 자동 압축을 기다리는 것(약 83% 전에는 안 걸린다). `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` 를 settings env 로 넣으면 무시된다는 보고가 있다 **[미검증]**.

### 1.5 Magentic-One / AutoGen / Microsoft Agent Framework

- **GroupChat:** 모든 참가자가 브로드캐스트된 같은 스레드를 받고, 각자 `model_context` 에 복사본을 둔다.
  - `model_context` 종류: `Unbounded`, `Buffered`(최근 N), **`HeadAndTail`**(앞 k + 뒤 n: 과제 설명과 최신 내용 보존), `TokenLimited`.
  - Memory 프로토콜 `add/query/update_context` 구현: `ListMemory`, `ChromaDBVectorMemory`, `Mem0Memory`. Teachability(0.2)는 가르침을 벡터 메모로 저장한다 **[미검증]**.
- **Magentic-One 원장**(소스 `_magentic_one/_prompts.py`, `_magentic_one_orchestrator.py` 확인):
  - **Task ledger(외부 루프):** fact sheet 를 `GIVEN OR VERIFIED FACTS / FACTS TO LOOK UP / FACTS TO DERIVE / EDUCATED GUESSES` 로 만들고, 팀 구성을 보고 bullet plan 을 짠다. "task + team + facts + plan" 한 메시지로 브로드캐스트한다.
  - **Progress ledger(매 턴):** 엄격한 JSON `is_request_satisfied / is_in_loop / is_progress_being_made / next_speaker / instruction_or_question`, 항목마다 `{reason, answer}`.
  - **정체:** 진전이 없거나 루프면 `n_stalls+1`, 진전이 있으면 −1 이다. `max_stalls=3` 에 닿으면 fact sheet 를 갱신하고(추정 하나 이상 갱신을 강제), 무엇이 잘못됐는지 설명한 뒤 새 plan 을 받는다. 그다음 **모든 참가자에 `GroupChatReset` 을 보내 스레드를 비우고 새 ledger 한 메시지로 재시작**한다.
- **성숙도:** AutoGen 은 MIT 이고 **유지보수 모드**다. 후계인 Microsoft Agent Framework 는 `AgentThread` + `ChatMessageStore` + 호출 전 주입하는 `AIContextProvider` 구조다.
- **Colab:**
  - **베낄 것:** 두 원장, 정체 카운터, "원장으로 재시작". Colab 에는 이미 연쇄 깊이·루프 감지(FR-3.5, v0.19.12 보고 복귀 시 깊이 되돌림)가 있으니 progress ledger 가 그 근거 칸이 된다. HeadAndTail 은 브리프(머리·캐시)와 최근(꼬리) 구조를 재확인해 준다.
  - **피할 것:** 모두가 같은 스레드 전체를 받는 GroupChat 브로드캐스트.

### 1.6 LangGraph / LangChain / LangMem

- **공유 state + reducer:** typed state 하나를 모든 노드가 읽고 쓴다(`messages` 는 `add_messages`).
- **Checkpointer:** `thread_id` 별 super-step 스냅숏, PostgresSaver. time travel·fork, HITL 재개.
- **Store:** 스레드를 넘는 장기 기억. namespace 튜플 `(org, user, "memories")` 에 put/search 하고, index 를 설정하면 semantic 검색이 된다.
- **LangMem:**
  - semantic(profile = 최신 하나 / collection = 누적), episodic, procedural(프롬프트 자체를 고침).
  - 쓰기 경로는 hot path(대화 중 도구)와 background manager 두 가지다.
  - `SummarizationNode` 는 `RunningSummary{summary, summarized_message_ids, last_summarized_message_id}` 로 증분 요약한다(소스 확인).
- **인계:**
  - supervisor 기본값은 **full history 전달**이다. `output_mode=full_history|last_message` 로 워커 중간 단계를 돌려놓을지 고른다. `create_forward_message_tool` 은 워커 답을 그대로 최종 출력으로 보낸다.
  - swarm 은 단일 `messages` + `active_agent` 이고, checkpointer 가 없으면 누가 활성이었는지 잊는다.
- **성숙도:** MIT, 운영 사례가 많다.
- **Colab:**
  - **베낄 것:** `last_summarized_message_id` 같은 **"어디까지 정리했나" 포인터**(= lane 의 `last_seen_message_id`, 원장 `room_version`). namespace 트리(org/room/work/lane)로 권한과 수명을 표현. profile(현재 값 하나) vs collection(누적) 구분 = 원장 `key` 대체 vs 일반 항목.
  - **피할 것:** 인계 기본값을 full history 로 두는 것.

### 1.7 OpenAI Agents SDK (+ Swarm, Codex CLI)

- **Sessions:** `get_items/add_items/pop_item/clear`. SQLite·Redis·SQLAlchemy(Postgres)·서버 보관 `OpenAIConversationsSession`. `OpenAIResponsesCompactionSession` 은 기록을 지우고 다시 쓰는 자동 압축이다.
- **handoff:**
  - 기본값은 새 에이전트가 **이전 대화 전체**를 본다.
  - `input_filter` 로 바꿀 수 있고, 원본은 세션에 남는다. `remove_all_tools` 는 구조화 tool item 만 지우고 메시지에 복사된 결과는 못 지운다.
  - `nest_handoff_history`(베타)는 이전 기록을 `<CONVERSATION HISTORY>` 요약 한 조각으로 접는다.
  - `input_type` 으로 인계 사유 같은 구조화 인자를 LLM 이 넘긴다.
- **Codex CLI:**
  - AGENTS.md 연결(32KiB 상한), `/compact`, `model_auto_compact_token_limit`.
  - 요약 프롬프트는 "handoff summary for another LLM that will resume the task" **[2차 분석, 미검증]**.
  - 압축 뒤 AGENTS.md 를 다시 읽지 않는 결함 보고가 있다(#5772).
- **Colab:**
  - **베낄 것:** 인계에 구조화 인자(사유·우선순위). Colab 의 lane 위임에 `purpose` 칸으로 이미 있다. 요약은 "다음 LLM 이 이어받을 handoff" 로 프롬프트를 고정.
  - **피할 것:** 기본 전체 상속, 매번 다시 쓰는 압축 세션(캐시 파괴). 그리고 **불변 지시를 첫 사용자 메시지에 두면 압축 때 요약에 섞여 사라진다** → Colab 은 브리프를 시스템 프롬프트 층에 두는 현 구조를 유지한다.

### 1.8 CrewAI

- **예전:** short-term(RAG) / long-term(SQLite 에 task 결과·품질 점수) / entity / contextual / external(Mem0).
- **현행 단일 `Memory`:** 저장 때 LLM 이 scope(`/project/alpha`, `/agent/researcher` 트리)·카테고리·중요도를 추론한다.
  - 유사도가 0.85 이상이면 LLM 이 keep/update/delete/insert 를 정한다.
  - recall 은 `shallow`(벡터 + 최신성·중요도 가중, LLM 없음) / `deep`(다단계, 기본). 저장소는 LanceDB.
- **인계:** `Task(context=[t1,t2])` 는 **앞 task 의 출력 텍스트만** 붙인다. hierarchical 에서는 manager 가 위임·검수한다.
- **성숙도:** MIT. 메모리 API 가 자주 바뀌었다.
- **Colab:**
  - **베낄 것:** scope 트리와 private·read-only 조각, "task 출력만 다음 task 로".
  - **피할 것:** 저장마다 LLM(숨은 비용·비결정).

### 1.9 MetaGPT

- `Environment` 가 공유 메시지 풀이다. Role 은 `Message(cause_by=Action, send_to=…)` 로 publish 하고, `_watch([Action])` 로 **관심 있는 Action 이 만든 메시지만 관찰**한다(pub/sub).
- 인계 산출물은 대화가 아니라 **SOP 구조화 문서**(PRD → 설계 → task 목록 → 코드)다.
- **성숙도:** MIT, 연구 성격.
- **Colab:**
  - **베낄 것:** "역할이 구독하는 메시지 종류만 문맥에" → 턴 프롬프트 `<history>` 를 역할별로 거르는 여지(예: Designer 에게 셸 로그 detail 제외). 문서형 인계 = 아티팩트.
  - **피할 것:** 고정 SOP 파이프라인(Colab 은 대화형 방).

### 1.10 OpenHands

- **append-only 이벤트 로그 + "묘비" `Condensation` 이벤트.** 지우지 않고 잊음을 기록하며, `View` 가 적용해 LLM 입력을 만든다. tool-call 짝과 배치 원자성을 View 속성으로 보호한다(`openhands-sdk/.../context/condenser/`, `view/properties/tool_call_matching.py`).
- **기본 `LLMSummarizingCondenser`:** 기본 에이전트는 `max_size=80, keep_first=4` 이고, **넘치면 앞 절반을 요약 하나로** 바꾸며 뒤 절반은 원문으로 둔다. 트리거는 soft(한도)와 hard(요청·context-exceeded → 전체 요약, 5회 재시도).
  - README: *"condensation destroys the prompt cache, but doing so regularly keeps the cost of rebuilding the prompt cache low."*
- **V0 컨덴서:** `observation_masking`(창 5 밖 관찰 `<MASKED>`), `amortized_forgetting`, `recent_events`, `llm_attention`, `structured_summary`, `pipeline`. 요약 프롬프트는 `USER_CONTEXT / TASK_TRACKING(task ID 보존) / COMPLETED / PENDING / CODE_STATE` 절을 강제한다.
- **위임:**
  - V0 `AgentDelegateAction` 은 자식에게 `inputs['task']` 만 넘긴다(부모 이력 없음). metrics 는 공유한다.
  - SDK `DelegateTool` 은 `spawn`(최대 5) → `delegate {id: task}` 를 병렬로 돌리고 **final response 텍스트만** 모은다. 자식 비용은 부모 stats 에 교체 방식으로 롤업한다(이중 집계 방지).
- **repo 지식:** skills(`.agents/skills`, 트리거형 knowledge)를 system suffix 로 붙인다.
- **성숙도:** MIT, ★약 8.9만.
- **Colab:**
  - **베낄 것:** 묘비 이벤트 = FR-2.5 「여기까지 정리」를 "요약 이벤트 + 덮은 범위" 행으로 일반화. 요약 절 강제(id 보존). "드물게 크게".
  - **피할 것:** hard reset 을 일상 경로로 쓰는 것.

### 1.11 Letta (MemGPT)

- **core memory block:** 라벨이 붙은 항상 컨텍스트 안의 텍스트. 문자 `limit`(기본 10만, persona/human 은 2만), `read_only`. 블록이 별도 행이라 **여러 에이전트에 붙이면 공유 블록**이 된다.
- **recall:** 전체 메시지 + `conversation_search`. **archival:** 벡터.
- **쓰기:** 에이전트 자신이 `core_memory_append/replace`, `memory_rethink` 등으로 쓴다.
- **압축:** `context_window × 0.9` 초과 시 경량 summarizer 가 요약한다.
- **sleep-time:** N턴마다 백그라운드 에이전트가 `last_processed_message_id` 이후만 읽고 공유 블록을 다시 쓴다(`sleeptime_multi_agent_v4.py`).
- **현황:** V1 서버는 `archive` 브랜치로 은퇴했다. 현행 letta-code 는 git-backed 파일시스템 "MemFS" + 백그라운드 "dreaming" 통합 + 선택적 적용 전 리뷰 **[문서만]**.
- **Colab:**
  - **베낄 것:** 블록 상한 + read_only 구분(일부 kind 는 Lead·사람만 쓰기), sleep-time 포인터 방식, "적용 전 리뷰" = 정리자 제안 모드.
  - **피할 것:** 공유 블록을 에이전트가 통째로 다시 쓰는 것(ACE 의 context collapse, 쓰기 경합).

### 1.12 Mem0

- **2026 현재 ADD-only**(`ADDITIVE_EXTRACTION_PROMPT`, "Your sole operation is ADD"). 옛 UPDATE/DELETE 판정과 **그래프 메모리는 제거**됐다(PR #4805, issue #6591).
- 사실이 바뀌면 둘 다 보존하고 시간 맥락으로 판단한다. md5 로 중복을 제거하고, 엔티티를 링크한다(코사인 0.95 이상).
- 검색 점수 = (semantic + BM25 시그모이드 + entity_boost×0.5)/max(`utils/scoring.py`).
- 스코프 `user_id/agent_id/run_id` 중 하나가 필수이고, metadata 로 덮어쓸 수 없다.
- **성숙도:** Apache-2.0, ★약 6.6만.
- **Colab:**
  - **베낄 것:** 스코프 키를 필수 필터로 강제(방·미션 id), 어휘 + 의미 + 엔티티 부스트.
  - **피할 것:** ADD-only 무한 누적을 "현재 상태"로 쓰는 것. 원장은 명시적 대체(supersede)가 필요하다.

### 1.13 Zep / Graphiti

- Episode(원문) → Entity 노드 + `RELATES_TO` 사실 엣지. 엣지는 **이중 시간**(`valid_at/invalid_at` 세계 시간, `created_at/expired_at` 시스템 시간)이다.
- 모순된 옛 엣지는 **삭제하지 않고 무효화**한다(`resolve_extracted_edges`).
- 검색: cosine·BM25·BFS + RRF·MMR·cross-encoder. Community 요약.
- 백엔드는 Neo4j·FalkorDB·Kuzu·Neptune 이고 **Postgres 드라이버가 없다**. 수집 때마다 LLM 을 여러 번 부른다.
- **성숙도:** Apache-2.0, ★약 3.1만.
- **Colab:**
  - **베낄 것:** `valid_from/invalidated_at/superseded_by` 칸과 무효화(삭제 금지).
  - **피할 것:** 그래프 DB, 메시지마다 추출.

### 1.14 Cognee · A-MEM (간단히)

- **Cognee**(Apache-2.0, ★약 3.1만):
  - `add → cognify`(분류·청크·엔티티 추출·그래프+임베딩·요약) → 검색. `memify`·`improve`(세션 Q&A 를 영구 그래프로, 락과 멱등 기록 행).
  - "Postgres 단일 인스턴스로 가능" 이라고 한다 **[문서만]**.
  - 개념은 원장 백필 파이프라인과 비슷하지만 LLM 비용이 무겁다.
- **A-MEM**(MIT, 연구용·정체):
  - Zettelkasten 노트(content·keywords·tags·links). 새 노트가 들어올 때마다 LLM 이 이웃 노트를 다시 쓴다(evolve).
  - 비결정·비용·감사 곤란 → **피할 것.** 단 "항목끼리 링크" 는 원장 `source_*`·`supersedes` 로 충분하다.

### 1.15 Cline / Roo Code · Aider · SWE-agent · Devin (코딩 에이전트 보조 사례)

- **Cline:**
  - Memory Bank 는 **내장이 아니라 Rules 관례**다(projectbrief/activeContext/progress… md 를 "update memory bank" 요청 때 전면 재작성).
  - `new_task` 는 컨텍스트 사용량 약 50% 에서 요약·파일 상태·다음 단계를 담아 사람 승인 뒤 새 task 를 연다.
  - Focus Chain 은 todo 를 6메시지마다 재주입하고, 이 목록은 요약을 거쳐도 살아남는다.
- **Roo Code**(2026-05 아카이브):
  - Boomerang: 서브태스크에는 `new_task` 메시지만, 부모에는 `attempt_completion` 요약만 간다.
  - **Orchestrator 모드는 일부러 파일 읽기 권한이 없다**(컨텍스트 오염 방지).
- **Aider:**
  - repo map: tree-sitter 참조 그래프 PageRank → 상위 식별자·시그니처만, `--map-tokens` 1k.
  - 이력은 `min(max(ctx/16,1024),8192)` 토큰을 넘으면 약한 모델로 **머리만 재귀 요약하고 꼬리는 원문**(`aider/history.py`).
- **SWE-agent:** `LastNObservations` 로 오래된 관찰을 생략한다. 소스 주석이 캐시를 깬다고 인정하고, `polling` 으로 여러 턴에 한 번씩 크게 자른다. mini-swe-agent 는 압축 없이 선형이다.
- **Devin:** Knowledge(트리거 설명이 맞을 때 조회, pin 하면 상시), Playbook. 핸드오프 내부는 비공개 **[미검증]**.
- **Colab:**
  - **베낄 것:** Roo Orchestrator 의 "읽기 권한 없는 조정자"(Q2 강한 안), Cline Focus Chain(= `<mission_progress>` 를 압축 뒤에도 살아남는 자리에), Aider "머리 요약 + 꼬리 원문".
  - **피할 것:** 에이전트가 자유롭게 재작성하는 md 를 유일한 진실로 삼는 것.

---

## 2. 비교표

| 대상 | 맥락의 정본 | 항상 싣는 것 | 압축·잊기 | 회상 | 인계(넘김 / 돌려받음) | 누가 기억을 쓰나 | 캐시·비용 장치 | 라이선스 |
|---|---|---|---|---|---|---|---|---|
| **graphify** | 레포 파일 + graph.json | CLAUDE.md 상시 안내, LESSONS.md | 없음(질의 예산 2k) | 어휘(IDF+트라이그램)→BFS | — | 에이전트 `save-result` + 결정적 `reflect` | 질의 예산, AST 무LLM | Apache-2.0 |
| **Orca** | Run/Task/Dispatch DB + 메일박스 | 프리앰블 + 자기완결 spec | 없음(이력 비상속) | FTS5 세션 검색(`orca search`) | spec 5칸 / `worker_done` 3문장 + outcome + report-path | 코디네이터(spec·gate), 워커(보고) | spec 바이트 상한, 격리, `--brief` 160자 | MIT |
| **multica** | 이슈·댓글(Postgres) | 짧은 프롬프트(트리거 + id) + skills | 공급자 압축에 맡김, overflow → 실패 처리 | CLI 끌어 읽기(요약 200룬 → 스레드 tail) | handoff note, 리더 "다시 요약 마라" / 댓글 | 사람·에이전트(댓글·metadata), Hermes memories | 가변 블록을 뒤에, 합치기 | Multica(조건부) |
| **Claude Code/API** | transcript JSONL / 클라이언트 이력 | 시스템 프롬프트, CLAUDE.md, MEMORY.md 200줄 | tool clear → 요약(약 83%), 서버 compaction 150k | 에이전트형 grep/read(RAG 폐기) | 서브에이전트: 위임문 / 요약 | 에이전트(auto memory, memory tool) | 캐시 TTL, `clear_at_least`, 시스템 층 보존 | 상용 |
| **Anthropic 설계 글** | 창 밖 이벤트 로그 | 계획·progress·feature JSON | reset + handoff 산출물 또는 compaction | just-in-time 식별자 | 새 서브에이전트 / 1~2k 요약 + 아티팩트 참조 | Lead(계획), 서브에이전트(아티팩트) | 격리가 핵심 | — |
| **Magentic-One** | 공유 GroupChat | task ledger(facts 4분류 + plan) | 정체 3회 → **전원 reset + 원장 한 메시지** | 없음 | progress ledger 가 next_speaker + instruction | 오케스트레이터 LLM | 원장이 압축본 | MIT(유지보수) |
| **LangGraph** | checkpoint(thread) + Store | state | SummarizationNode(`last_summarized_id`) | Store semantic search | supervisor full / `last_message` | 개발자 코드, LangMem hot/background | trim·summarize | MIT |
| **OpenAI Agents SDK** | Session | instructions | CompactionSession(재작성), `nest_handoff_history` | 없음(세션만) | 기본 전체 / `input_filter` | 코드 | — | MIT |
| **CrewAI** | Memory(LanceDB) | contextual 주입 | LLM consolidation | 벡터 + 최신성·중요도 | `context=[task]` 출력만 | LLM 자동(저장 때마다) | shallow recall | MIT |
| **MetaGPT** | 공유 메시지 풀 | watch 한 메시지 | 없음 | 최근 k | SOP 문서 | 역할 | watch 필터 | MIT |
| **OpenHands** | append-only 이벤트 | skills suffix | **묘비 이벤트**, 앞 절반 요약(80개) | — | task 문자열 / final response | 컨덴서 LLM | "드물게 크게" | MIT |
| **Letta** | DB(블록·메시지·archival) | core blocks(문자 상한) | 90% 요약, sleep-time 재작성 | conversation_search·archival 벡터 | 에이전트 간 메시지 도구 | 에이전트 자신 + sleep-time | 블록 상한 | Apache-2.0 |
| **Mem0** | 사실 저장소 | 검색 결과 | ADD-only(대체 없음) | 벡터 + BM25 + 엔티티 | — | LLM 추출 | 조회당 소토큰 | Apache-2.0 |
| **Zep/Graphiti** | 시간 KG | 검색 결과 | 무효화(삭제 없음) | 하이브리드 + 그래프 + RRF | — | LLM 추출 | — | Apache-2.0 |
| **Cline/Roo** | 파일(md) | Rules, Focus Chain todo | 50% `new_task`, condense | 파일 읽기 | Boomerang: 메시지 / 완료 요약 | 에이전트(md 재작성) | Orchestrator 읽기 금지 | Apache-2.0 |
| **Aider** | 채팅 + repo | repo map 1k | 머리 요약, 꼬리 원문 | PageRank 맵 | — | weak model | 맵 토큰 예산 | Apache-2.0 |
| **Colab(지금)** | Postgres 메시지·결정·아티팩트 | 브리프 [1]~[5] + 턴 프롬프트(최근 50 + **미션 전부** + 결정) | 런타임 자동 압축(1M 창이라 늦음) | 필터·부분 문자열 | lane 위임 메시지 / 보고 메시지 + detail | 에이전트(결정 기록만) | 브리프 바이트 동일(캐시 접두) | — |

---

## 3. 공통 패턴 — 모두가 수렴하는 여덟 가지

1. **정본은 창 밖의 append-only 로그, 컨텍스트는 매번 조립하는 뷰.** Managed Agents, OpenHands View, LangGraph checkpoint, multica 이슈 기록이 그렇다. Colab 은 이미 정본(Postgres)을 가졌고, 문제는 런타임 세션을 "두 번째 정본"처럼 키우는 것이다.
2. **구조화된 공유 상태 면(블랙보드·원장).** Magentic-One ledger, Letta shared block, Anthropic feature JSON, Orca task-list(ready), Cline Focus Chain. 자유 산문이 아니라 **칸이 있는 작은 것**이고, 상태 칸은 서버나 조정자가 소유한다.
3. **인계 = 자기완결 명세 → 짧은 요약 + 참조.** Orca 5칸/3문장, Anthropic 4요소/1~2k, LangGraph last_message, OpenHands final response, Roo completion. **아티팩트는 조정자를 거치지 않는다.**
4. **격리: 무거운 탐색은 깨끗한 창에서.** 서브에이전트, Orca 워커, Roo 의 "읽기 금지 조정자", multica 의 "리더는 직접 하지 말라".
5. **압축은 드물게·크게, 고정 머리는 보존.** OpenHands 앞 절반, Aider 머리 요약 + 꼬리 원문, HeadAndTail, API `clear_at_least`, SWE-agent polling, 시스템 프롬프트 끝의 `cache_control`. 불변 지시는 압축되지 않는 층(시스템 프롬프트)에 둔다.
6. **어디까지 처리했나 포인터.** LangMem `last_summarized_message_id`, Letta `last_processed_message_id`, Orca FIFO Delivery ack, multica 합치기(모든 id 처리). 델타 렌더와 백그라운드 정리 모두 이 포인터로 결정적이 된다.
7. **사실은 덮어쓰지 않고 무효화·대체한다.** Graphiti 이중 시간, Mem0 ADD-only, graphify 격하. 단, "현재 상태"를 보여 주려면 **명시적 대체 사슬**이 필요하다(Mem0 는 그걸 검색에 떠넘겨 약하다).
8. **기억 채널은 명시적이고 사람이 볼 수 있게.** multica 가 Codex 내장 기억을 끈 이유, Letta 적용 전 리뷰, graphify 의 md 기록. LLM 자동 추출(CrewAI·A-MEM·Graphiti 수집)은 비용·비결정·감사 문제로 **제안** 역할에 그친다.

한 가지는 **갈린다: 벡터 검색.** 코딩·오케스트레이션 도구(Claude Code, Orca, multica, graphify, Aider)는 어휘·구조 검색과 에이전트 도구만 쓴다. 개인화 메모리 제품(Mem0, Zep, Letta archival, CrewAI)은 벡터가 중심이다. Colab 의 회상 대상은 "id·수치·이름이 중요한 작업 대화"라 전자에 가깝다.

---

## 4. §6 질문에 대한 프레임워크의 답

| # | 질문 | 프레임워크가 보여 주는 것 | 권고 |
|---|---|---|---|
| 1 | 원장 쓰기 주체: 에이전트 직접 vs 자동 정리자 | 에이전트 직접: Letta, graphify `save-result`, Claude memory tool. 조정자 전용: Magentic-One 원장, Orca spec·gate. LLM 자동: CrewAI·Mem0·Graphiti. **자동 + 리뷰**: letta-code "적용 전 리뷰". multica 는 "명시·가시" 원칙 | **유지 + 세분.** `fact/lesson/artifact_ref/open_question` 은 모든 에이전트, `plan/progress/owner/constraint` 는 Lead·사람만(Letta read_only 처럼). 정리자는 **제안만**. 채택은 Lead·사람 |
| 2 | Lead 역할 규율: 권고 vs 막기 | Roo Orchestrator 는 **읽기 권한 자체가 없다**. multica 리더 문구 "직접 하지 말라". graphify "강제 1회 후 권유". Anthropic 은 격리 권고 | **2단계.** ① 브리프 [3] 문구 + 보고 규격(1단계), ② 0단계 계측 뒤 Lead 셸 비중이 줄지 않으면 역할 허용 명령(K-19)으로 브라우저·미디어 계열을 막는다. 단번에 막지는 않는다 |
| 3 | 임베딩 경로(외부 API vs 자체 호스팅) | 코딩 도구는 모두 **벡터 없이** 운영한다(Orca FTS5, Claude Code RAG 폐기, multica 끌어 읽기). graphify 자체 벤치에서도 하이브리드와 차이가 2~4점 | **보류를 기본으로.** 3단계를 "트라이그램/FTS + 원장 검색"으로 시작하고, 문항 recall@5 가 목표에 못 미칠 때만 임베딩을 결정한다. 질문 자체를 뒤로 미룰 수 있다 |
| 4 | 재개 세션 교체(계획적 cold start) 허용 | Agent SDK "resume 에 기대지 마라", Managed Agents "매번 조립", Claude Code "1h·100k → 요약에서 재개", multica overflow → 포인터 해제 + 연속성 공지, Magentic reset, Cline 50% `new_task` | **허용 권고, 조건 명시.** 세션 누적 used > 임계(예 150k~200k) **또는** 유휴 > 캐시 TTL **또는** 정체 3회 **또는** overflow → 다음 task 는 cold start + 원장 + handoff 요약 + 연속성 공지 한 줄 |
| 5 | 「여기까지 정리」·결정 기록을 원장으로 합칠까 | OpenHands 묘비 이벤트(요약 = 덮은 범위가 있는 이벤트), Magentic 원장이 곧 압축본, Letta 블록 | **합친다(화면 하나, 행은 둘).** 결정은 FR-4.2 행을 유지하고 원장 `kind=decision` 이 참조한다. 「여기까지 정리」는 `kind=summary` + `covers_until_message_id`(묘비). 렌더·화면은 원장 하나 |
| 6 | 문항 정답 달기 주체 | 직접 답하는 프레임워크는 없음. graphify outcome 표시·LongMemEval 방식 | 에이전트 초안(질문 + 후보 id) → Director 검수. 초안 작성자는 문항 대상 방에 참여하지 않은 cold 에이전트 |

---

## 5. Colab 수정 권고 — 3층 유지, 설계 차용 명시

### 5.1 (가) 수명주기 — "조건부 재개 + 끌어 읽기"로 강화

1. **재개 판정을 서버가 한다**(`PlanAttempt` 입력 칸 추가).
   - resume 은 **모두** 참일 때만 한다: 직전 세션 used < 임계, 마지막 턴 이후 경과 < 캐시 TTL(기본 5분이면 사실상 짧다, 1h TTL 여부 확인), 정체 카운트 < 3, 직전 attempt 가 overflow 가 아님.
   - 아니면 cold start 다. 이때 싣는 것은 **원장 전체 + handoff 요약(직전 lane 의 마지막 보고 3문장) + 연속성 공지 한 줄**(multica 문장 차용: "방 기록과 원장이 정본이다. 런타임 기억만 새로 시작했다. `colab room messages` 로 필요한 것을 다시 읽어라").
2. **overflow 는 실패로**(multica). 데몬이 context overflow 를 보고하면 lane 의 runtime session 포인터를 비운다.
3. **resume 이면 델타**(원안 유지): `last_seen_message_id` 이후 메시지 + `<memory_changes>`.
4. **끌어 읽기를 1단계로 당긴다.**
   - `<mission_messages>` 전부 → "미션 머리글 색인"(top-only, 메시지당 한 줄 약 120자 + id) + 최근 N.
   - 전문은 `colab room messages --thread`. CLI 에 `--summary`(요약 모드)를 더해 multica 의 두 단계 읽기를 만든다.
   - 문항 점수로 하락이 없는지 확인한다.
5. **압축은 드물게·크게.** 데몬이 세션 교체를 쓰면 런타임 자동 압축에 의존할 이유가 줄어든다. `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` 는 안전망으로만 쓰고, 실제로 먹는지는 실측한다 **[미검증]**.
6. **Lead 격리.**
   - 브리프 [3] 에 multica·Anthropic 문구: "직접 검증·제작하지 말고 담당에게 위임하라. 위임문은 대상·바꿀 것·제약·소유 범위·완료 증거 5칸으로(Orca). 방 기록을 다시 요약하지 마라."
   - 이어서 Q2 의 2단계.

### 5.2 (나) 상태 원장 — 칸 보강

원안 `memory_item` 에 다음을 더한다.

| 추가 | 출처 | 뜻 |
|---|---|---|
| `kind=plan` (미션당 active 1개, key 고정) | Magentic task ledger, Anthropic 계획 저장 | Lead 가 쓰는 bullet 계획 |
| `kind=fact` 의 `certainty: given\|to_verify\|derived\|guess` | Magentic facts 4분류 | 확인된 사실과 추정을 구분 → 회상 정확도 |
| `kind=progress` (Lead 판단 턴마다 JSON: satisfied/in_loop/progress/next/instruction + reason) | Magentic progress ledger | 정체 카운터·연쇄 깊이 판단 근거, 사람 화면 "지금" 줄 |
| `kind=lesson` 의 `outcome: useful\|dead_end\|corrected`, 뒷받침 수, 감쇠 | graphify reflect | "해 봤더니 안 된 것"을 신뢰도와 함께 |
| `kind=summary` + `covers_until_message_id` | OpenHands 묘비, FR-2.5 | 「여기까지 정리」 흡수 |
| `writable_by` (kind 별 역할 표) | Letta read_only, Orca 권한 | plan/progress/owner/constraint 는 Lead·사람만 |
| 렌더 상한(kind 별 글자 수) + 넘치면 "…외 N건 — `colab memory get`" | Letta 블록 limit, Claude MEMORY.md 200줄·25KB | 원장 자체의 비대화 방지 |
| 상태 칸은 서버 소유(`status`, `passes` 류) | Anthropic feature JSON | 에이전트가 산문으로 상태를 뒤집지 못하게 |

- 쓰기는 **항목 단위 연산만** 허용한다(ACE, Letta 재작성 회피). 무효화는 `superseded/retracted` + `invalidated_at`(Graphiti) 로 하고 삭제는 없다.
- 정체 처리: progress 가 연속 3회 "진전 없음"이면 서버가 Director 인박스에 항목을 올리고, 다음 Lead task 는 cold start + 원장으로 연다(Magentic reset 의 Colab 판).

### 5.3 인계·보고 규격 (원안에 없던 것, 새로 추가)

- **lane 보고 content 규격:** "한 일 / 찾은 것 / 남은 것" 3문장 + `outcome`(succeeded/failed/blocked) + 참조(아티팩트 id, detail 존재 표시). 긴 결과는 detail·아티팩트에 둔다(Orca `worker_done`, Anthropic 참조 반환).
- **Lead 턴 프롬프트에서 워커 보고 detail 은 기본 미리보기 400자**(현행 유지). Lead 가 detail 을 여는 횟수를 0단계 지표에 넣는다.
- 위임 메시지 규격 5칸(Orca)은 브리프 [3] 예시로 넣는다. 규칙은 예시 옆 주석으로 둔다(Orca 설계 주석).

### 5.4 (다) 회상 — 어휘 먼저

- 3단계를 **"트라이그램/FTS 하이브리드(메시지·detail 청크·원장·결정) + 에이전트 도구 `colab room recall`"** 로 시작한다(Orca FTS5 설계 참고: 도구 출력은 상한까지만 색인, 식별자 분리, 오타 보정).
- pgvector 는 문항 결과가 모자랄 때 붙이는 **선택 단계**로 둔다. Q3 는 그때 묻는다.
- 원장 항목의 `source_message_ids` 가 회상의 1순위 경로다. 원장 → 원문.

### 5.5 수정된 순서

**0 계측**(호출당 컨텍스트 분해 + cache_creation 비중 + 유휴 간격 분포 + 문항 v1)
→ **1 수명주기**(조건부 재개·overflow 해제·연속성 공지·미션 머리글 색인·Lead 위임 문구·보고 규격)
→ **2 원장**(plan/progress/fact 확실도/lesson/summary 흡수)
→ **3 어휘 회상**
→ **3b 벡터(조건부)**
→ **4 정리자 제안·sleep-time**(`last_processed` 포인터, 미션 종료·N건마다, 제안만).

**피할 것 요약:** 인계 기본값을 전체 상속으로 두기, 저장마다 LLM 추출, 그래프 DB, 공급자 내장 불투명 기억, 매 턴 조금씩 잘라 캐시 깨기, 에이전트가 자유 재작성하는 md 를 유일한 진실로 삼기, multica 코드 차용(라이선스).

---

## 6. 확인하지 못한 것

- **캐시 TTL 효과:** Colab 에서 claude_code(ACP 어댑터)의 실제 캐시 TTL 이 5분인지 1시간인지, Lead 깨움 간격이 이를 넘는지 확인하지 않았다. cache_creation 토큰을 실사용 DB 에서 분해하지 않았다(0단계).
- **자동 압축 임계 제어:** `CLAUDE_AUTOCOMPACT_PCT_OVERRIDE` 가 ACP 경유 세션에 먹는지, ACP 로 `/compact` 를 보낼 수 있는지 확인 못 했다.
- **graphify:** 상용 플랫폼의 기억·회의 기능은 README 문구뿐이다.
- **multica:** 달러 예산 강제 부재는 grep 기준이다. Skills 주입 경로는 주석·README 로 추정했다.
- **Letta·Cognee·Anthropic Memory:** letta-code MemFS/dreaming, Cognee "Postgres 단일", Managed Agents Memory 감사 로그는 문서·2차 출처만 봤다.
- **2차 분석 출처:** Codex 요약 프롬프트 문구, Devin 핸드오프 내부는 2차 분석이거나 비공개다.
- **LangGraph time travel 세부**와 **CrewAI knowledge sources 세부**는 이번에 재확인하지 않았다.
- **Orca 경계:** 공개 저장소 소스와 로컬 CLI 1.4.215 를 대조하지 않았다(버전 차이 가능). `orca search` 의 한국어 토큰화(unicode61)는 실측하지 않았다.
- **숫자:** ★ 수·릴리스 날짜는 조사 시점 GitHub API 값이다.

---

## 출처

**Director 지정**
- graphify: https://github.com/Graphify-Labs/graphify (`graphify/always_on/`, `graphify/cli.py`, `serve.py`, `ingest.py`, `reflect.py`, `hooks.py`, CHANGELOG)
- Orca: https://github.com/stablyai/orca (`src/main/runtime/orchestration/preamble.ts`, `src/shared/orchestration-worker-start-prompt-budget.ts`, `skill-guides/orchestration.md`·`references/*`, `docs/reference/agent-session-search-contract.md`, `docs/reference/agent-session-search-query-tuning.md`, `src/main/ai-vault-search/session-search-schema.ts`), https://www.onorca.dev/ , 로컬 `orca skills get orchestration`(1.4.215)
- multica: https://github.com/multica-ai/multica (`server/internal/daemon/prompt.go`, `handler/comment.go`, `handler/daemon.go`, `handler/squad_briefing.go`, `execenv/runtime_config_sections.go`, `execenv/hermes_memory.go`, `execenv/codex_memory.go`, `pkg/db/queries/agent.sql`, `LICENSE`)

**Anthropic**
- https://code.claude.com/docs/en/memory · /context-window · /hooks · /sessions · /prompt-caching · /sub-agents · /agent-teams · /agent-sdk/sessions
- https://platform.claude.com/docs/en/build-with-claude/context-editing · /compaction · /compaction-threshold · /agents-and-tools/tool-use/memory-tool
- https://www.anthropic.com/engineering/multi-agent-research-system · /effective-context-engineering-for-ai-agents · /effective-harnesses-for-long-running-agents · /harness-design-long-running-apps · /managed-agents
- Claude Code 자동 압축 이슈: https://github.com/anthropics/claude-code/issues/31806

**AutoGen·LangGraph·OpenAI SDK·ADK**
- AutoGen·Magentic-One: https://github.com/microsoft/autogen (`autogen-agentchat/.../_magentic_one/_prompts.py`, `_magentic_one_orchestrator.py`, `autogen-core/.../model_context/`), https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/memory.html , https://learn.microsoft.com/en-us/agent-framework/get-started/memory
- LangGraph: https://docs.langchain.com/oss/python/langgraph/persistence , https://github.com/langchain-ai/langgraph-supervisor-py , https://github.com/langchain-ai/langgraph-swarm-py , https://langchain-ai.github.io/langmem/concepts/conceptual_guide/ , `langmem/short_term/summarization.py`
- OpenAI: https://openai.github.io/openai-agents-python/handoffs/ , https://openai.github.io/openai-agents-python/sessions/ , https://learn.chatgpt.com/docs/agent-configuration/agents-md
- Google ADK: https://adk.dev/sessions/state/ , https://adk.dev/context/compaction/

**그 밖의 프레임워크**
- CrewAI: https://docs.crewai.com/en/concepts/memory
- MetaGPT: https://docs.deepwisdom.ai/main/en/guide/tutorials/multi_agent_101.html , https://github.com/geekan/MetaGPT
- OpenHands: https://github.com/OpenHands/software-agent-sdk (`openhands-sdk/openhands/sdk/context/condenser/`, `context/view/`, `openhands-tools/.../delegate/impl.py`), https://github.com/OpenHands/OpenHands/tree/0.59.0/openhands/memory/condenser

**메모리 전용**
- Letta: https://github.com/letta-ai/letta/tree/archive , https://docs.letta.com/letta-code/memory
- Mem0: https://github.com/mem0ai/mem0 , https://docs.mem0.ai/migration/platform-v2-to-v3 , https://github.com/mem0ai/mem0/issues/6591
- Zep/Graphiti: https://github.com/getzep/graphiti
- Cognee: https://github.com/topoteretes/cognee
- A-MEM: https://github.com/agiresearch/A-mem

**코딩 에이전트**
- Cline: https://docs.cline.bot/prompting/cline-memory-bank , https://cline.bot/blog/unlocking-persistent-memory-how-clines-new_task-tool-eliminates-context-window-limitations
- Roo: https://roocodeinc.github.io/Roo-Code/features/boomerang-tasks
- Aider: https://aider.chat/docs/repomap.html , `aider/history.py`
- SWE-agent: https://swe-agent.com/latest/reference/history_processor_config/ , https://mini-swe-agent.com/latest/
- Devin: https://docs.devin.ai/product-guides/knowledge
