# 데몬 ↔ 서버 프로토콜

| 항목 | 내용 |
|---|---|
| 버전 | **v0.10.0 — 미션 폴더(Director 승인 2026-09-26, T-FOLDERS D1~D8)**: 작업 폴더를 **방 → 미션 → 에이전트**로(§6.1 경로 규칙 표). §4.1 `workdir.path` 는 **모든 kind 에서 서버가 짓는 필수 절대 경로**다(`dir` 도 — v0.8.3 까지 `dir` 은 데몬 `Path()` 가 지었다) · `workdir.id` 는 **첫 attempt 부터 필수**(서버가 행을 먼저 만든다 — T-S21 결정 A 의 전제가 사라진다) · `workdir.shared_path?`(미션 공용 `_shared` 절대 경로, 데몬은 `mkdir -p` 만). 경로 조각은 `<slug>-<id 앞 8자리>` 를 **만들 때 한 번 정해 행에 저장**하고 이름이 바뀌어도 옮기지 않는다(D1 A; 경로 슬러그는 한글 보존, git 브랜치는 ASCII `Slug` 그대로). 같은 미션의 같은 에이전트 lane 은 한 폴더를 함께 쓴다(D3 A). `worktree` 체크아웃은 방×에이전트 `rooms/<room>/_worktrees/<agent>/`, 브랜치 `colab/<방 slug>/<agent slug>` — 첫 미션 제목이 아니라 **방 이름**(D7 C, FINDING-1 정정) + 미션 `_shared` 는 저장소 밖. §6 보고 행 `work_id?`·`role?`(`agent`|`shared`). §4.3 `gc` — 삭제 뒤 비게 된 상위 폴더 정리. GC: `none` 미션 폴더는 미션 닫힘 **즉시가 아니라** 닫힌 뒤 `last_used_at + workdir_retention_days`(D8 B). 옛 `sessions/…`·`worktrees/…` 폴더는 옮기지 않고 행의 저장 경로를 그대로 쓴다(D6 A). 옛 서버 번들(`path` 없음)이면 데몬은 예전 `Path()`(`sessions/<room>/<lane>`)로. 같은 미션 폴더 읽기·쓰기 규약은 harness v0.9.7 `<folders>`(D2 A·D4 A·D5 A — 강제 없음, 규약만). **v0.9.2 — 스레드 답글(Director 승인 2026-09-25)**: §4.1 `task.thread_root_id?` — 트리거 메시지가 스레드 답글이면 그 스레드 루트 id(병합된 트리거가 여럿이면 가장 늦게 게시된 것 기준, 최상위면 생략). 데몬은 이 값을 `COLAB_THREAD_ID`(harness §2.1)로 넘기고 CLI `message post` 가 기본 답글 위치로 쓴다 — 스레드로 물은 질문에 에이전트가 메인 타임라인에 답하던 결함(STO 방 실측). **v0.9.1 — R4(openapi v0.3.0)**: `allowed_commands` 의 `session_get`·`session_messages` → `room_get`·`room_messages`. 번들 `task.session_id` 는 `room_id` 와 같은 값으로 계속 싣는다(키 이름 바꾸기는 `session_id` 열과 함께 별도 라운드). 본문의 「세션」 → 「방」. **v0.9.0 — 방·미션(PRD v0.19 R0)**: §4.1 `task.room_id`(= 옛 `session_id`, 같은 값 — R4 까지 둘 다 싣는다) · `task.work_id?`(매인 미션, 없으면 미션 밖) · `task.queued_reason` 은 데몬에 오지 않는다(서버 큐 내부). workdir 경로 규칙은 `<room>/<agent>`(브랜치 `colab/<room slug>/<agent slug>`)로 읽는다 — 같은 uuid 라 기존 폴더는 그대로 산다. 유효 예산(§4.4) = min(task 상한, **미션 잔여, 방 잔여**). §4.3 `gc`·`rebind_prepare` 의 `session_id` 는 방 id. v0.8.3 — §4.1 `workdir.id`·§6 id 회신(K-14: 데몬 index 폐기, workdir 안 표식 파일). v0.8.2 — §4.1 `task.allowed_commands`(K-19 역할별 명령 부분집합; 데몬이 MCP 툴 목록을 자른다). v0.8.1 — §6: 세션 삭제(openapi deleteSession)로 이미 지워진 workdir 을 가리키는 §6 보고 행은 서버가 조용히 소비한다(gc 명령은 세션 삭제 전에 실린다). v0.8 — **§4.5 테스트 채팅**(FR-1.8.1, P5a): 세션 없는 1:1 대화를 **같은 claim·phase·events·heartbeat·finish 로** 돌린다 — `task.kind: "test_chat"`, `task.id` = test_chat id, `attempt` = 사용자 턴 번호, `task_token` 없음(= colab 표면 전부 끔). 종료는 `gc` 로 임시 디렉터리 삭제. `finish.transport` 추가. v0.7.4 — §6 gc 거부 피드 문장을 사용자의 말로(S-67, T-S13). v0.7.3 — T-I4(G7 1판) 차단 결함 반영: §4.1 `workdir.path` 는 **절대 경로**(서버가 probe `workdir_root` 로 조립)이고 데몬 방어 규칙 명시(①), §6 workdir 보고의 `session_id`·`agent_id` 필수 규칙과 서버의 §4.4 `Finish.Workdir` 소비 의무 명시(②). v0.7.2 — §4.4 finish `workdir.git` 이름을 §6 과 통일(`commits_ahead`·`merged`)하고 `protocol.go` `Finish.Workdir` 추가; §4.3 `rebind_prepare` 다운로드 위치 + 프롬프트 자리표시자 `{{COLAB_REBIND_DIR}}`(T-D9 PR #156 계약 결함 1·2). v0.7.1 — §4.4 유효 예산 = min(task 상한(override 우선), 세션 잔여)(PR #121 리뷰 NN3, D-16). v0.7 — §4.3 `gc` 페이로드에 서버가 경로를 싣고(`workdirs:[{id,path}]`), §6 보고 행 `gc: {status: deleted|refused, reason}` 로 결과·거부를 알린다(T-D5 계약 질문, G5 S-29·D-4). v0.6 — `dispatched` 5분 타임아웃은 재큐잉이 아니라 종료다(§4.1). v0.5는 probe 최상위 `colab_cli`(§3), `preview.message_id` 의 주체를 서버로 명시(§4.2). v0.4 는 프로파일 폴백의 주체를 서버로 명시(§4.4). v0.3 은 G3 재확인 C-1: heartbeat `preview` **모양 확정**(객체)과 "부가 정보는 heartbeat를 실패시키지 않는다" 규칙. v0.2는 명령 소비 조건·heartbeat 만료 범위 |
| 소유 | S + D. 변경은 Director 승인 PR로만 |
| 근거 | PRD §8.1(큐), FR-7.1(상태 머신·heartbeat), FR-9.1(고아·토큰 폐기), FR-9.2(오프라인 유예), FR-6.4(workdir·GC), `harness.md`(오류 분류·재개) |
| 원칙 | **데몬은 stateless, 상태는 서버.** 데몬은 서버가 준 것만 실행하고 결과를 보고한다. 모든 시각 판정(만료·유예·`not_before`)은 서버 클럭(`contracts/clock`) |

## 1. 전송·인증

- HTTPS, JSON. 데몬 → 서버 방향만 연결을 연다(사용자 머신은 인바운드가 없다). 서버 → 데몬 명령은 **long-poll 응답과 heartbeat 응답에 실어** 내려간다(§4.3).
- 인증 두 종류:

| 토큰 | 발급 | 용도 | 폐기 |
|---|---|---|---|
| **데몬 토큰** `cdt_…` | 페어링(§2) | 데몬 API 전부 | 런타임 삭제, 사용자 회수 |
| **task 토큰** `ctk_…` (`COLAB_TASK_TOKEN`) | claim 응답(§4.1)마다 attempt 전용 | 에이전트의 `colab` CLI/MCP(`colab-cli.md`) | 재큐잉·취소·완료 시 **서버가 폐기**하고 데몬에 통보(§5) |

경로 접두 `/v1/daemon/*`. OpenAPI(`openapi.yaml`)에는 넣지 않는다 — 이 문서가 스펙이다.

## 2. 페어링

```
POST /v1/daemon/pair        {pairing_code, hostname, os, daemon_version}
  → 201 {runtime_id, daemon_token}
```

- `pairing_code`는 S12(Add a computer)가 발급, 10분 유효, 1회용.
- 페어링 직후 데몬은 probe(§3)를 한 번 보낸다. S12의 "연결됨 → CLI 감지 중 → 준비 완료"는 probe 도착으로 판정(E11-08).

## 3. probe

```
POST /v1/daemon/runtimes/{runtime_id}/probe
  { daemon_version, hostname,
    capabilities: [ <harness.md §9> … ],
    repos: [ {path, remote_url, branch, clean} … ],
    workdir_root, disk: {used_bytes, quota_bytes?},
    colab_cli: {present, version} }
  → 200 {ok}
```

시점: 페어링 직후, 데몬 시작 시, 하루 1회, 서버가 `probe` 명령(§4.3)을 내릴 때. `repos[].remote_url`이 재바인딩 후보 판정의 기준(FR-9.2, E14-04·05).

**`colab_cli` 는 최상위다 (v0.5).** 에이전트는 colab CLI 로 서버에 말하므로(colab-cli.md §1) CLI 가 없으면 방은 조용히 아무 말도 못 하는 상태가 된다 — 데몬 로그에만 남기면 사람이 원인을 못 찾는다. 이 값은 **머신 속성**이라 런타임별 `capabilities[]` 가 아니라 probe 최상위에 한 번 싣는다: 런타임이 둘이어도 바이너리는 하나이고, 런타임이 0개인 머신에서도 보고돼야 한다. 데몬은 probe 마다 `colab --version` 을 실행해 채우고, 실행 실패·미설치는 `{present: false, version: ""}` 로 통일한다(원인은 데몬 로그에 남긴다). 서버는 `present == false` 인 머신을 S12/S11 카드에 경고로 드러낸다.

## 4. task 수명

```
claim ──▶ [preparing] ──▶ [running] ──heartbeat 15s──▶ finish
   │                          │
   └── 명령: cancel / revoke / probe / gc (응답에 실림)
```

### 4.1 claim (long-poll)

```
POST /v1/daemon/runtimes/{runtime_id}/claim
  { capacity: <동시 실행 여유 슬롯 수>, wait_ms: ≤ 30000 }
  → 200 { tasks: [ <TaskBundle> … ], commands: [ <Command> … ] }   (없으면 tasks: [])
```

서버 규칙:
- 이 런타임에 **고정된 방**(`session.runtime_id`)의 `queued` task만 준다(E11-09). `none` 격리에 `runtime_id`가 비었으면 첫 claim한 런타임으로 고정한다(E11-10).
- 방이 `paused`면 주지 않는다(E5-04). `task.not_before`가 미래면 주지 않는다(`rate_limited`).
- 동시성 상한 4층(FR-6.3)을 서버가 계산한다. `worktree` 격리에서 같은 에이전트의 다른 lane이 `running`이면 주지 않는다(E2-12).
- **`workdir_root` 를 보고하지 않은 런타임에는 방 task 를 주지 않는다 (v0.10.0).** 경로를 서버가 지으므로(§6.1) `none` 도 `worktree` 와 같은 거부다 — 번들을 만들지 않고 task 는 `queued` 로 두며 피드에 사람의 말로 남긴다. probe 는 v0.7.3 부터 `workdir_root` 를 싣는다.
- 큐는 `Queue` 인터페이스(§7) 뒤에 있다. v1 구현은 Postgres `SELECT … FOR UPDATE SKIP LOCKED`.
- claim 즉시 `queued → dispatched`, `dispatched_at` 기록. 5분 안에 `preparing` 보고가 없으면 **`failed(timeout)`으로 끝난다 — 재큐잉하지 않는다**(E5-02, PRD FR-7.1). 서버는 그 attempt의 task token을 폐기하고(좀비 데몬이 나중에 보고하지 못하게) Director가 보도록 드러낸다.

  **v0.6 정정.** 이 줄은 v0.5까지 "→ 재큐잉(E5-02)"이라고 적혀 있었다. 그런데 인용한 EVAL E5-02는 `failed(timeout)`만 말하고 재큐잉을 말하지 않으며, PRD FR-7.1도 "`dispatched` 5분 초과 → `failed(timeout)`"이고 **재시도 대상 목록(`runtime_offline`·네트워크·프로세스 stall)에 `timeout`이 없다.** 근거를 인용하면서 반대로 옮긴 것이라 계약을 고친다.

  **왜 `running`(E5-03, 3분)은 재큐잉인데 이쪽은 아닌가.** `dispatched`는 데몬이 **이미 claim해서 소유권과 토큰을 가진** 상태다. 그 데몬이 모델 작업 이전 단계인 `preparing`조차 보고하지 못했다면 고장은 반복된다 — 방은 런타임에 고정돼 있으므로(§4.1) 재큐잉은 같은 고장난 런타임에게 되돌려 주는 것이고, claim ↔ timeout 을 오가며 attempt만 태운다. 반대로 `running` 침묵은 프로세스가 실제로 시작된 뒤의 일이라 재시도에 승산이 있고, 그 경로는 런타임을 `offline`으로 표시해 즉시 재배정 루프를 막는다.

**TaskBundle**

```json
{
  "task": { "id", "attempt", "lane_id", "session_id", "room_id", "work_id?", "agent_id", "trigger_message_id", "thread_root_id?",   // v0.9.2: thread_root_id = 트리거 메시지(병합됐으면 가장 늦은 것)가 스레드 답글이면 그 스레드 루트, 최상위 메시지면 생략. v0.9.0: room_id == session_id(R4 까지 둘 다), work_id 는 매인 미션(없으면 생략)
            "restarted_from_task_id?", "delegated_from_task_id?", "budget_usd?", "budget_override_usd?",
            "allowed_commands?": ["room_get", …] },   // v0.8.2 K-19 — 역할별 colab 명령(colab-cli §2.5). 데몬은 MCP 툴 목록·래퍼를 이 목록으로 자른다. 비면 전부
  "task_token": "ctk_…",
  "profile": { "runtime_kind", "model", "options", "env", "args", "tools", "adapter_pin" },
  "workdir": { "id", "kind": "worktree|dir", "path", "shared_path?", "repo_path?", "branch?", "reuse": true|false },
  //  id (v0.8.3, K-14 → v0.10.0 필수): 서버 `workdir` 행의 uuid. 서버는 번들을 조립할 때 행을 먼저 만들어(없으면) id 를 싣고,
  //  데몬은 §6 보고 행에 **그 id 를 그대로 회신**한다 — 데몬의 `<root>/.colab/workdirs/` index 파일은 이제 필요 없다.
  //  **v0.10.0 — `dir` 도 첫 attempt 부터 id 가 실린다.** v0.8.3~v0.9.x 의 `dir` 첫 attempt 는 id 가 없었다(T-S21 결정 A:
  //  경로를 데몬이 지어 서버가 행을 먼저 만들 수 없었다). 이제 서버가 경로를 지으므로(§6.1) 그 전제가 없다.
  //  id 가 없는 번들은 **옛 서버**뿐이고, 그때만 데몬은 예전처럼 §6 짝 맞추기(session_id·agent_id)·index 로 간다.
  //  path 는 **모든 kind 에서 필수·절대 경로**다 (v0.7.3 T-I4 차단 ① → v0.10.0 `dir` 포함). 서버가 그 런타임의
  //  probe `workdir_root`(§3)와 §6.1 경로 규칙으로 조립해 싣는다 — 데몬이 정하면 서버가 E13-08(남의 워크트리 경로를
  //  번들에 싣지 않는다)을 판정할 수 없고, GC 명령(§4.3)·workdir 행·턴 프롬프트의 `<folders>`(harness v0.9.7)도 서버가
  //  경로를 알아야 쓸 수 있다. 상대 경로를 실으면 데몬이 자기 CWD 로 절대화해 없는 디렉터리를 런타임 cwd 로 넘기고,
  //  worktree 격리에서는 사용자 저장소 **안**에 체크아웃이 생긴다(실측: 방이 첫 턴부터 전부 failed(config)).
  //  **재진입 lane 은 행의 저장 경로 그대로**(D6 A): `lane.workdir_id` 가 가리키는 행이 있으면 그 `path_or_ref`(옛
  //  `sessions/<room>/<lane>`·`worktrees/<slug>/<agent>` 포함)를 싣는다 — 런타임 재개의 `cwd`(harness §6)가 바뀌지 않는다.
  //  **옛 서버 호환**: `dir` 번들에 path 가 없으면 데몬은 예전 `Path()` = `<workdir_root>/sessions/<room_id>/<lane_id>` 로
  //  짓는다. 이 폴백은 옛 서버 번들용으로만 남고 v0.10.0 서버는 path 를 비우지 않는다.
  //  데몬 방어: path 가 상대면 `<workdir_root>` 기준으로 해석하고, `<workdir_root>` 밖(`..`·심링크 탈출)이면 거부하며
  //  (`UnderRoot`, §6.1), 런타임 spawn 전에 디렉터리 존재를 확인해 없으면 `failure_kind=config` 로 그 경로를 문구에
  //  넣어 finish 한다(원인을 가리지 않는다). `dir` 은 없으면 데몬이 `mkdir -p` 한다(지금과 같다 — 첫 attempt).
  //  shared_path (v0.10.0): **미션 공용 폴더** `rooms/<room>/<mission>/_shared` 의 절대 경로. 미션에 매인 턴(`task.work_id`
  //  있음)이면 격리 `none`·`worktree` 모두 싣고, 미션 밖 턴·테스트 채팅이면 생략. 데몬은 **`mkdir -p` 만** 하고 내용은
  //  읽지도 지우지도 않는다(삭제는 서버의 `gc`, §4.3). 런타임 cwd 는 여전히 `path` 다. 방어는 path 와 같다(`UnderRoot`).
  //  이 키를 모르는 옛 데몬은 무시한다 — 그때 `_shared` 는 에이전트가 첫 쓰기에서 만든다(같은 root 아래라 권한 차이 없음).
  "brief": { "transport": "acp_meta_system_prompt|instruction_file", "text": "<[1]~[8]>" },
  "prompt": "<턴 프롬프트 — 서버가 만든다. 재개면 <resumed> 구간 포함>",
  "resume": { "runtime_session_ref": <harness.md §6> } | null,
  "limits": { "budget_usd", "stall_seconds": 180 },
  "posted_message_ids": [ … ]      // attempt ≥ 2일 때, 이미 게시한 메시지(FR-7.1)
}
```

데몬은 번들 밖의 것을 알 필요가 없다 — 방 히스토리도 프롬프트 안에 들어 있다.

### 4.2 진행 보고

```
POST /v1/daemon/tasks/{task_id}/attempts/{attempt}/phase   {phase: "preparing"|"running", pgid, workdir_path}
POST /v1/daemon/tasks/{task_id}/attempts/{attempt}/events  {events: [ <task_event> … ]}   → 200 {accepted_seq_max, commands: [...]}
POST /v1/daemon/tasks/{task_id}/attempts/{attempt}/heartbeat {usage: {…}, last_seq}      → 200 {commands: [...]}
```

- `events`는 배치(≤ 100개 또는 1초). `(task_id, attempt, seq)` 멱등 — 서버는 이미 받은 `seq`를 무시하고 `accepted_seq_max`를 돌려준다. 데몬은 미확인 이벤트를 재전송한다.
- 메시지 스트리밍: `message.say` 이벤트는 턴 단위로 합치되, 사람이 보는 지연을 위해 `partial: true`인 중간 이벤트를 **같은 seq 없이** 별도 채널(heartbeat의 `preview` 필드)로 보낸다. 영속되지 않는다(PRD §7 "고빈도 이벤트 비영속").

  **`preview` 모양 (v0.3, G3 C-1)** — 데몬과 서버가 서로 다른 모양을 쓰고 있었다(데몬 `string` vs 서버 `{text, message_id}`) → 부분 출력이 있는 동안 heartbeat가 통째로 `422`가 되어 **살아 있는 attempt가 3분 뒤 재큐잉**되고 `message.delta`가 한 번도 안 나갔다. 확정:

  ```json
  "preview": { "text": "<지금까지의 부분 출력>", "message_id": "<uuid, 이미 게시된 메시지를 이어 쓰는 중이면>" }
  ```

  `text`만 필수, `message_id`는 선택. 서버는 이를 SSE `message.delta`로 브로드캐스트하고 저장하지 않는다.

  **`message_id` 는 서버가 채운다. 데몬은 비운다 (v0.5).** 메시지는 에이전트가 colab CLI/MCP 로 서버에 **직접** 올리므로(colab-cli.md §1) 데몬은 그 왕복도, 서버가 만든 message id 도 볼 수 없다. 게다가 preview 는 게시 **이전**의 부분 출력이라 그 시점에는 id 가 아직 존재하지도 않는다. 그러므로 데몬이 id 를 알게 하려고 프로토콜을 늘리지 않는다 — 델타를 어느 메시지에 잇는지는 서버가 "이 attempt 가 마지막에 만든 메시지"로 판단한다. 데몬 구현은 배관만 열어 두고(아는 경우 채울 수 있게) 항상 빈 값으로 보낸다.

  **부가 정보는 heartbeat를 실패시키지 않는다.** `preview`가 없거나 모양이 달라도 서버는 `usage`·`last_seq`를 받아 `heartbeat_at`을 갱신하고 `200`을 돌려준다 — 잘못된 `preview`만 무시하고 활동 피드에 경고를 남긴다. heartbeat는 **생존 신호**이므로 부가 필드 하나로 attempt를 잃으면 안 된다(E5-03이 막으려던 상황을 스스로 만든다).
- heartbeat **15초**. 서버는 **`running` attempt**의 마지막 heartbeat로부터 **3분** 무응답이면 `runtime_offline` → 재큐잉 + 토큰 폐기(E5-03, E11-03). `preparing`은 heartbeat 만료 대상이 아니다 — `dispatched_at`부터 5분(§4.1)이 덮는다(v0.2, N5: 콜드 스타트가 긴 런타임의 준비 구간을 3분에 자르지 않기 위해).
- `waiting_human`·`blocked`·`paused`로 끝난 attempt는 heartbeat를 보내지 않는다 — 프로세스가 없다.

### 4.3 명령 (서버 → 데몬)

claim·events·heartbeat 응답의 `commands[]`:

| type | 페이로드 | 데몬 동작 |
|---|---|---|
| `cancel` | `{task_id, attempt, after_current_tool: bool, reason: "director"\|"budget"\|"kill_switch"\|"loop"\|"session_paused"}` | `harness.md` §5 절차 → `finish` outcome=`cancelled` |
| `revoke` | `{task_id, attempt}` | 그 attempt의 토큰이 폐기됐다. 프로세스가 아직 있으면 취소 절차. **고아 정리의 신호**(§5) |
| `probe` | — | §3 |
| `gc` | `{session_id, workdirs: [{id, path}]}` 또는 `{policy: {...}}` — **서버가 경로를 싣는다**(데몬은 uuid↔path 매핑을 가진 적이 없다, v0.7). `workdirs` 없이 `workdir_ids` 만 있는 옛 모양이면 데몬은 `session_id` 의 lane workdir 전부로 해석 | §6 — 삭제 또는 거부를 다음 workdir 보고 행의 `gc` 로 알린다. **v0.10.0**: 삭제 뒤 비게 된 상위 폴더 `rooms/<room>/<mission>/`·`rooms/<room>/_room/`·`rooms/<room>/_worktrees/`·`rooms/<room>/` 를 **비어 있을 때만** 지운다(안쪽부터, `rmdir` 의미 — 남은 파일이 하나라도 있으면 그대로 둔다). `<workdir_root>` 자신과 `rooms/`·옛 `sessions/`·`worktrees/` 최상위는 지우지 않는다. 페이로드 모양은 그대로 |
| `rebind_prepare` | `{session_id, artifacts: [{id, order, url}]}` | 새 workdir 준비 후 아티팩트 순서 적용은 **프롬프트가 지시**(FR-9.2). 데몬은 다운로드만 — 위치는 **체크아웃 밖** `<workdir_root>/.colab/rebind/<session_id>/NNN-<artifact_id><ext>` + `manifest.json`(order·id·파일명; v0.7.2, T-D9 계약 결함 2). 서버는 그 경로를 모르므로 재바인딩 뒤 첫 턴 프롬프트에 자리표시자 **`{{COLAB_REBIND_DIR}}`** 를 쓰고, 데몬이 `harness.md` §10 치환 규칙대로 절대 경로로 바꾼다 |

명령은 **최소 한 번** 전달된다. 데몬은 `(type, task_id, attempt)`로 멱등 처리.

**서버 쪽 규칙(v0.2, PR #22 리뷰 R3)**: 명령을 응답에 실었다고 소비하지 않는다 — 응답이 유실되면 명령이 사라지기 때문이다. 데몬 ack 왕복도 두지 않는다(프로토콜을 늘리지 않기 위해). 대신 **명령의 효과가 관측될 때까지 매 응답에 다시 싣는다**:

| type | 소비(더 이상 싣지 않음) 조건 |
|---|---|
| `cancel` | 그 attempt의 `finish`가 도착 |
| `revoke` | 그 attempt의 `finish`가 도착, 또는 발행 후 `HeartbeatExpiry`(3분) 경과 — 그 뒤 고아는 데몬 재시작 정리(§5)와 401이 막는다 |
| `probe` | 다음 probe 수신 |
| `gc` | 해당 workdir 보고(§6)에서 삭제 확인 |
| `rebind_prepare` | 새 attempt의 `phase: preparing` 보고 |
| 공통 | 발행 후 24h 경과(TTL) — 피드에 "명령 미소비 만료" 기록 |

데몬은 같은 명령을 여러 번 받을 수 있으므로 멱등 처리가 계약이다(E11-05 계약 테스트: 응답 유실 후 다음 응답에 같은 `revoke`가 다시 실림).

### 4.4 finish

```
POST /v1/daemon/tasks/{task_id}/attempts/{attempt}/finish
  { outcome: "completed"|"failed"|"cancelled"|"waiting_human"|"blocked"|"paused_budget",
    stop_reason, failure_kind?, not_before?, usage: {…},
    runtime_session_ref: <harness.md §6>, resume_outcome: "resumed"|"cold_start"|null,
    last_seq, workdir: {path, git: {branch, merged, dirty, commits_ahead}?} }
  → 200 {ok}
```

- **`workdir.git`(v0.7.2, T-D9 계약 결함 1)**: 이름은 §6 보고 행과 **같다**(`commits_ahead` — 옛 `ahead` 는 오기). `contracts/protocol.go` `Finish.Workdir`(`FinishWorkdir{Path, Git *WorkdirGit}`)이 정본. 서버는 이 값으로 그 workdir 행의 `merged`·`dirty`·`commits_ahead`(openapi Workdir, PR #155)를 갱신한다 — GC 판정(E13-10~13)의 입력이 이것이다. `git` 이 없으면(격리 `none`·`container`) 서버는 행을 건드리지 않는다.

- `waiting_human`·`blocked`는 데몬이 정하지 않는다. `turn_end`가 왔을 때 서버가 `pending_hitl`(FR-7.1 HITL 전이) 또는 `status set blocked` 호출 여부로 정하므로, 데몬은 `outcome: "completed"` + `stop_reason`을 보내고 **서버가 최종 상태를 정한다**. 위 열거는 서버 응답의 최종 상태이지 데몬 판단이 아니다.
- `finish`는 attempt 단위로 멱등. 두 번 와도 첫 결과가 남는다.
- **프로파일 폴백은 서버가 결정한다 (v0.4).** 데몬은 실패를 `failure_kind`로 정확히 보고할 뿐, 대체 프로파일로 스스로 갈아타지 않는다. 이유: (a) 방이 `runtime_id`에 고정되므로(FR-2.1 M10) 서버의 재큐잉은 **같은 머신을 구조적으로 보장**한다 — FR-7.1의 "같은 머신 안에 대체 프로파일이 있으면 전환"이 저절로 성립한다. (b) 재시도 회계(`attempt`·상한 2~3회)·토큰 발급·비용 집계가 전부 서버 소유라, 데몬이 in-process로 갈아타면 그 셋이 흐려진다. (c) 서버는 `agent_profile.fallback_profile_id`를 이미 갖고 있고 데몬은 알 필요가 없다.

  서버가 폴백할 때: 같은 workdir(`workdir.reuse: true`), `attempt` 증가, **`runtime_kind`가 바뀌면 `resume`을 비운다**(런타임 세션은 이어받을 수 없다 — E8-08). 같은 머신에 쓸 수 있는 대체 프로파일이 없으면 `queued`로 두고 Director에게 알린다. **다른 머신으로 넘기지 않는다**(E8-09).

  따라서 `TaskBundle`에 대체 프로파일 목록은 두지 않는다.
- `paused_budget`: 데몬이 `usage_update` 누적으로 **유효 예산**을 넘겨 취소 절차를 밟은 경우(FR-7.3). `failure_kind` 없음. **유효 예산(v0.7.1, D-16)** = `min(task 상한, 방 잔여)` — task 상한은 `budget_override_usd` 가 있으면 그것(승인된 상향), 없으면 `budget_usd`(에이전트 `budget_per_task`); 방 잔여는 `limits.budget_usd`(서버가 번들에 실은 방 잔여 예산). 어느 쪽이 먼저 닿든 `paused_budget` 이고, 넘긴 쪽을 `detail` 에 적는다. 우선순위(override > limits > task) 방식은 방 잔여를 넘길 수 있어 쓰지 않는다.

### 4.5 테스트 채팅 (FR-1.8.1, v0.8)

에이전트 편집 화면(S10)의 **방 없는 1:1 시험 대화**다. 새 엔드포인트를 만들지 않고 §4.1~§4.4 를 그대로 탄다 — 데몬에게 테스트 채팅의 한 사용자 턴은 "토큰 없는 attempt" 하나다.

**번들 차이** (그 외는 §4.1 TaskBundle 과 같다):

| 필드 | 값 |
|---|---|
| `task.kind` | `"test_chat"` (없거나 `"task"` 면 보통 task). `contracts/protocol.go` `BundleTask.Kind` |
| `task.id` · `task.attempt` | **`id` = `test_chat.id`**, **`attempt` = 사용자 턴 번호(1부터)**. 그래서 `(task_id, attempt, seq)` 멱등·`phase`·`events`·`heartbeat`·`finish` 의 URL 이 그대로 맞고, 턴마다 `resume` 을 이어 **한 런타임 세션으로 대화가 이어진다**(§4.4 `runtime_session_ref` 를 서버가 `test_chat.runtime_session_ref` 에 저장해 다음 턴 번들 `resume` 에 싣는다) |
| `task.lane_id` · `session_id` · `trigger_message_id` | 빈 문자열. `task.test_chat_id` 에 같은 id 를 한 번 더 싣는다(로그·래퍼 경로용) |
| `task_token` | **빈 문자열.** 데몬은 토큰이 없으면 `COLAB_*` 환경 변수를 넣지 않고, `mcpServers` 를 싣지 않고, hermes 래퍼 실행 파일(harness §10)도 만들지 않는다 — 에이전트는 메시지 게시·위임·HITL 을 **할 수 없고 순수 응답만** 한다(FR-1.8.1, E15-03). 브리프도 서버가 `[2]`(colab 명령) 없이 만든다 |
| `workdir` | `{kind: "dir", path: "<workdir_root>/.colab/testchat/<test_chat_id>", reuse: true}` — 데몬이 첫 턴에 `mkdir -p`. 저장소 체크아웃·worktree·container 를 쓰지 않는다 |
| `prompt` | 사용자 턴 본문 그대로(첫 턴은 서버가 "이것은 시험 대화다 — 플랫폼 명령은 쓸 수 없다" 한 줄을 앞에 붙인다). 방 히스토리는 없다 — 이전 턴은 `resume` 으로 이어진다 |
| `limits` | `{budget_usd: <agent.budget_per_task>, stall_seconds: 180}` |

**보고**: 데몬은 §4.2 그대로 보낸다. 서버는 테스트 채팅에 활동 피드가 없으므로 `task_event` 를 **저장하지 않고** 다음만 소비한다 — `message.say`(턴 단위로 합친 것) → 그 턴의 `agent` 응답 본문(openapi `TestChatTurn`), `usage.report`·`finish.usage` → `test_chat.input_tokens/output_tokens/cost_usd`(추정 규칙은 방과 같다; 워크스페이스 집계 `test_chat_usd`), heartbeat `preview.text` → SSE `test_chat.delta`. `finish` 가 오면 SSE `test_chat.turn`. `failed` 면 그 턴의 `error` 에 `failure_kind` 를 §8.4 문장으로 적는다(턴은 남고 채팅은 열려 있다).

**`finish.transport` (v0.8)**: 실제로 쓴 경로 `"acp"|"cli"` — `Finish.Transport`. 모든 attempt 에 실어도 되지만 서버가 쓰는 곳은 테스트 채팅(`test_chat.transport`, 화면에 "실행 경로")뿐이다.

**동시성·claim**: `test_chat.runtime_id` 는 생성 시 고정된다(비우면 그 `runtime_kind` 가 온라인인 런타임 중 하나를 서버가 고른다 — openapi createTestChat). 턴은 그 런타임의 `capacity` 한 슬롯을 방 task 와 똑같이 쓴다(FR-6.3 데몬 상한). 같은 채팅의 이전 턴이 끝나기 전에는 다음 턴을 만들지 않는다(openapi `409`). `preparing` 5분·`running` 3분 규칙(§4.1·§4.2)도 같되, **재큐잉하지 않는다** — 그 턴을 `error` 로 닫는다(시험 대화에 재시도는 잡음이다).

**닫기·취소**: `closeTestChat` 은 진행 중 턴이 있으면 `cancel {task_id: <test_chat_id>, attempt, reason: "director"}` 를 싣고, 언제나 `gc {test_chat_id, workdirs: [{id: <test_chat_id>, path}]}` 를 싣는다(`session_id` 없음). 데몬은 그 경로가 `<workdir_root>/.colab/testchat/` 아래일 때만 `rm -rf` 하고 §6 보고 행 `{id: <test_chat_id>, kind: "dir", path, test_chat_id, bytes: 0, gc: {status: "deleted"}}` 로 알린다 — `session_id` 는 비운다(§6 의 필수 규칙은 방 workdir 행에만 해당). 서버는 `test_chat_id` 가 있는 행을 `workdir` 테이블에 넣지 않고 명령 소비에만 쓴다. 방어: 데몬은 시작 시 `.colab/testchat/` 아래 **24h 넘은** 디렉터리를 지운다(서버가 죽어 gc 가 못 온 경우).

## 5. 토큰 폐기와 고아 (FR-9.1)

| 시점 | 서버 | 데몬 |
|---|---|---|
| 재큐잉(heartbeat 만료·timeout·재시도) | 그 attempt의 `ctk_` **즉시 폐기**. 이후 그 토큰의 `colab` 호출은 `401 token_revoked`(E11-04) | 다음 claim/heartbeat 응답에서 `revoke` 명령 수신 |
| 취소·완료·`waiting_human` 전이 | 폐기 | — |
| 데몬 재시작 | — | claim **전에** 디스크의 `pgid` 기록을 읽어 살아 있는 프로세스 그룹을 SIGTERM/SIGKILL(E11-05). 기록 형식: `<workdir_root>/.colab/attempts/<task_id>.<attempt>.json {pgid, started_at}` — 정상 종료 시 삭제 |

**방향은 서버 → 데몬이다**(PLAN 리뷰 #02 m9). 데몬이 토큰을 폐기 요청하는 경로는 없다 — 재큐잉을 서버가 하므로.

## 6. workdir와 GC (FR-6.4)

```
POST /v1/daemon/runtimes/{runtime_id}/workdirs   {workdirs: [{id?, kind, path, session_id, work_id?, agent_id?, lane_id?, role?, bytes, last_used_at, git: {branch, merged, dirty, commits_ahead}?, gc: {status: "deleted"|"refused", reason?}?}]}
```

- 데몬은 workdir 목록을 probe와 함께, 그리고 lane 종료 시 보고한다. S13이 이 데이터를 보여준다.
- **`work_id?`·`role?`(v0.10.0)** — 그 폴더가 속한 미션 uuid(미션 밖 `_room`·`worktree` 체크아웃·옛 폴더면 생략)와 행의 종류 `agent`(에이전트 cwd) \| `shared`(미션 공용 `_shared`, `agent_id`·`lane_id` 없음). 데몬은 번들에서 받은 값을 `.colab-workdir.json` 표식에 함께 적어 두고 그대로 회신한다. 서버는 **id 로 행을 찾으므로 둘 다 필수가 아니다** — S13 트리·GC 진단용이고, 행과 다르면 서버 행이 이긴다. 옛 데몬은 싣지 않는다.
- **`id` 는 번들이 준 값을 그대로(v0.8.3, K-14).** 번들 `workdir.id` 가 있으면 보고 행 `id` 에 그것을 싣고 서버는 id 로 행을 찾는다(session_id·agent_id 짝 맞추기는 id 가 없을 때의 폴백 — v0.10.0 부터 id 없는 행은 **옛 데몬·옛 폴더**뿐이다). probe 시 전체 보고도 데몬이 기억하는 id 로 — 재시작 뒤에는 `<path>/.colab-workdir.json` 한 줄(id 만)을 읽는다(index 디렉터리 대신 workdir 안에 표식).
- **행을 서버가 저장할 수 있게 채운다 (v0.7.3, T-I4 차단 ②).** `session_id` 는 그 workdir 을 만든 **방의 uuid** 이고(슬러그·디렉터리 이름이 아니다), `worktree` 격리에서는 `agent_id` 가 **필수**다(그 격리의 workdir 은 에이전트당 1개라 agent 없이는 어느 행인지 정해지지 않는다 — 서버는 짝을 못 맞추면 조용히 건너뛴다). `git` 블록과 `bytes` 도 매 보고에 싣는다: **GC 판정의 유일한 입력**이라 비면 서버는 "커밋 0 · 클린"으로 읽어 미병합 커밋·미커밋 변경을 지운다(FR-6.4 M4 무력화).
- **서버는 §4.4 `finish` 의 `Finish.Workdir.Git` 도 같은 행에 반영한다 (v0.7.3).** attempt 가 만든 사실이 다음 probe 를 기다리지 않고 도착해야 그 사이에 도는 GC 스윕이 옳게 판정한다.
- GC 판정은 **서버**가 한다(보존 기한·용량 상한·미병합/미커밋 차단 — E13-09~13). 서버가 `gc {session_id, workdirs:[{id, path}]}` 명령을 내리면 데몬이 삭제하고 결과를 보고한다. 데몬은 스스로 지우지 않는다.
- **gc 결과 보고(v0.7)**: 데몬은 다음 workdir 보고에서 그 행에 `gc: {status, reason?}` 를 싣는다 — `deleted`(행은 마지막으로 한 번 더 실린다; 서버가 `deleted` 로 닫고 명령을 소비) 또는 `refused`(예: `isolation_worktree_p4` — P4 전 `worktree` 삭제는 데몬이 거부한다; 서버는 피드에 **"작업 폴더 정리를 컴퓨터가 거부했습니다: <reason>"** 을 남기고(v0.7.4 — 피드 문장은 사용자 대면이라 COMPONENTS §8.4 를 따른다, S-67) 명령을 소비한다). **조용히 무시하는 경로는 없다** — 로그만 남기고 보고하지 않으면 명령이 24h 미소비 만료로 피드에 남는다(§4.3).
- `worktree` 삭제는 `git worktree remove`만, 브랜치는 남긴다(E13-10).
- **방 삭제(v0.8.1 — v0.9.1 부터 openapi `deleteRoom`)**: 서버는 그 방의 남은 workdir 에 `gc` 를 싣고 **행을 먼저 지운다**. 이후 도착하는 §6 보고 행(`gc: deleted|refused`)이 없는 workdir 을 가리키면 서버는 명령 소비로만 처리하고 피드에는 남기지 않는다(방이 없다). 미병합·미커밋 `worktree` 는 삭제 자체가 `409` 라 이 경로에 오지 않는다.
- 디스크 상한 도달은 probe의 `disk`로 서버가 판정해 새 방 생성을 막는다(E13-16).

### 6.1 경로 규칙 (v0.10.0, T-FOLDERS — Director 승인 2026-09-26)

```
<workdir_root>/rooms/<room>/
    <mission>/                       ← 미션 하나
        _shared/                     ← 미션 공용 (role=shared; none·worktree 방 모두, 미션에 매인 턴에만)
        <agent>/                     ← 에이전트 cwd (role=agent; none 격리)
    _room/<agent>/                   ← 미션 밖 턴 (none 격리)
    _worktrees/<agent>/              ← worktree 격리 체크아웃 (방×에이전트, 브랜치 colab/<방 slug>/<agent slug>)
<workdir_root>/.colab/testchat/<id>/ ← 테스트 채팅 (§4.5, 그대로)
<workdir_root>/sessions/…            ← 옛 none 배치 (옮기지 않는다)
<workdir_root>/worktrees/…           ← 옛 worktree 배치 (옮기지 않는다)
```

**경로는 서버가 짓는다 — 모든 kind.** 데몬은 번들의 `path`·`shared_path` 를 그대로 쓴다(§4.1).

| 자리 | 조각 | 예 |
|---|---|---|
| 방 `<room>` | `PathSlug(room.name)-<room_id[:8]>` | `game-studio-3f2a91c0`, `게임-제작-3f2a91c0` |
| 미션 `<mission>` | `PathSlug(work.title)-<work_id[:8]>` | `snake-prototype-8b11de02` |
| 미션 밖 | `_room` (예약 조각) | |
| 에이전트 `<agent>` | `PathSlug(agent.name)-<agent_id[:8]>` | `developer-0c7e5d19` |
| 미션 공용 | `_shared` (예약 조각) | |
| worktree 체크아웃 | `_worktrees/<agent>` (예약 조각 + 에이전트 조각) | |

- **`PathSlug` (D1 하위 결정 — 경로는 한글 보존).** NFC 정규화 → 소문자 → 유니코드 글자(`\p{L}`)·숫자(`\p{N}`)·`_` 는 남기고 나머지는 `-` 하나로(연속은 하나로 합친다) → 앞뒤 `-` 제거 → 40자(룬) 넘으면 자르고 다시 뒤 `-` 제거 → 빈 문자열이면 `x`. 파일시스템은 UTF-8 이름을 받는다. 한글 이름이 `x` 로 떨어지던 옛 `Slug` 로는 사람이 `ls` 로 알아볼 수 없었다.
- **git 브랜치는 ASCII `workdirs.Slug` 그대로** — `colab/<Slug(room.name)>/<Slug(agent.name)>`. ref 호환(도구·원격 호스팅이 비ASCII ref 를 다르게 다룬다)이 이유다. v0.9.x 서버가 슬러그 재료로 **그 에이전트의 첫 미션 제목**(`COALESCE(work.title, room.name)`)을 쓰던 것은 PRD FR-6.4(방×에이전트)와 어긋난 결함이었다(FINDING-1) — v0.10.0 은 **방 이름**. 이미 만들어진 체크아웃·브랜치는 행의 저장 경로·브랜치를 그대로 쓴다(새 방·새 에이전트부터).
- **판별자는 id 조각이다.** 슬러그는 사람이 보는 표지이고 같은 이름·빈 슬러그(`x`)도 id 로 갈린다. 예약 조각(`_room`·`_shared`·`_worktrees`)은 `-<id>` 접미가 없으므로 어떤 이름 조각(항상 `-<id>` 로 끝난다)과도 같아질 수 없다.
- **id 충돌**: 조립한 경로가 **같은 런타임의 다른 소유(방·미션·에이전트 조합) 행의 경로와 같으면** 그 경로의 id 조각을 전부 12자리(`[:12]`)로 다시 짓는다. 같은 입력이면 같은 결과(결정적)다.
- **만들 때 고정.** 경로는 행을 만들 때 한 번 지어 `workdir.path_or_ref` 에 저장하고, 이후 방·미션·에이전트 **이름 바꾸기(FR-2.1.2 등)는 경로를 다시 짓지 않는다** — 실행 중 lane 의 cwd 와 런타임 세션(`session/load {cwd}`, harness §6)이 깨진다. 화면은 경로 대신 현재 이름을 보여 준다(openapi `Workdir.work`·`session`).
- **행 단위(D3 A).** `none`: 같은 방·같은 미션·같은 에이전트의 lane 은 **한 행을 함께 쓴다**(`lane.workdir_id` 가 같은 행) — 병렬 lane 도 같은 폴더이고 순차로 바꾸지 않는다(파일 이름 규약은 harness v0.9.7 `<folders>`). 다른 미션의 lane 은 다른 폴더. 미션 밖 턴은 방이 사는 동안 에이전트당 `_room/<agent>` 하나. `_shared` 는 미션당 한 행(`role=shared`, `agent_id`·`lane_id` null). `worktree`: 체크아웃은 방×에이전트 한 행(지금과 같다 — 같은 에이전트 lane 은 순차, E2-12), 미션 `_shared` 는 저장소 **밖**이라 커밋에 섞이지 않는다(D7 C).
- **옛 배치(D6 A).** `lane.workdir_id` 가 가리키는 행이 있으면 그 저장 경로(옛 `sessions/<room>/<lane>`·`worktrees/<slug>/<agent>` 포함)를 그대로 쓰고, **새 행을 만들 때만** 이 규칙을 쓴다. 옛 폴더는 옮기지 않고(이동은 실행 중 cwd 를 깬다) 옛 GC 규칙으로 사라진다. 데몬의 목록 보고(probe 시 전체 보고)는 `rooms/`·`sessions/`·`worktrees/` 세 트리를 모두 훑는다 — 표식 `.colab-workdir.json` 이 id 를 준다.
- **데몬 방어(문장만 — 이미 있다).** `path`·`shared_path`·`gc` 경로가 `<workdir_root>` 밖이면(`..`, 심링크 탈출 포함) 데몬은 쓰지도 지우지도 않는다(`UnderRoot`) — 준비면 `failure_kind=config`, gc 면 `gc: {status: refused, reason}`.
- **읽기·쓰기 규약은 강제하지 않는다(D2 A·D5 A).** 같은 미션의 동료 폴더는 읽고, 쓰기는 자기 폴더와 `_shared` 에만 — 이것은 harness v0.9.7 브리프 [2]·턴 프롬프트 `<folders>` 의 **규약**이고, 데몬은 형제 폴더 쓰기를 막지 않는다(`permissions.deny` 로 막는 것은 hermes 에 같은 수단이 없어 런타임별로 비대칭이 된다 — 후보로만 남긴다).

**GC(서버 판정, v0.10.0)**

| 대상 | 규칙 |
|---|---|
| `none` 미션 폴더 — 에이전트 행 · `_shared` 행 | 미션이 열려 있는 동안은 지우지 않는다. **미션이 닫힌 뒤 `last_used_at + workdir_retention_days`** 가 지나면 `gc` 를 싣는다(D8 B — 닫힘 **즉시**가 아니다. 남길 것은 아티팩트로 제출하라는 안내를 미션 닫기 확인이 한다). 판정은 lane 을 타지 않고 행의 `work_id` 로 |
| `worktree` 방의 미션 `_shared` | 위와 같다(저장소 밖이고 커밋이 없어 미병합 차단 대상이 아니다) |
| `_room/<agent>` | `last_used_at + workdir_retention_days`(지금 규칙) |
| `_worktrees/<agent>` | 지금 규칙 — 병합·클린 또는 커밋 0·클린만 삭제, 브랜치는 남긴다(E13-10~13) |
| 옛 `sessions/`·`worktrees/` 행 | 지금 규칙 |
| 방 삭제(openapi `deleteRoom`) | 방의 남은 행 전부에 `gc`(§6 방 삭제 규칙 그대로). 데몬은 삭제 뒤 빈 `rooms/<room>/` 를 지운다(§4.3) |

- **재바인딩(`rebind_prepare`)**: `none` 은 새 런타임의 `workdir_root` 로 경로를 새로 짓는다(서버가 짓기 때문에 가능하다). 폴더 내용은 옮기지 않는다 — `_shared` 포함, 새 컴퓨터로 가는 것은 아티팩트뿐(FR-9.2).
- **브랜치 이름 겹침(미결, 구현 PR 에서 판정)**: 브랜치 조각은 id 가 없는 ASCII `Slug` 라 **한글 이름 방 둘이 같은 저장소·같은 에이전트**를 쓰면 `colab/x/<agent>` 로 겹칠 수 있다(v0.9.x 에도 있던 성질). 경로는 id 조각으로 갈리므로 체크아웃은 겹치지 않고 `git worktree add -b` 가 거절한다 — 구현은 이 거절을 조용히 넘기지 말고 드러낸다.

## 7. 큐 인터페이스 (서버 내부)

Postgres SKIP LOCKED를 Redis로 바꿔도 이 프로토콜은 안 바뀐다(PLAN §7-3). 서버 코드는 아래 인터페이스만 본다.

```go
type Queue interface {
    Claim(ctx, runtimeID string, capacity int, now time.Time) ([]TaskBundle, error) // not_before ≤ now, 방 active, 동시성 상한 적용
    Heartbeat(ctx, taskID string, attempt int, now time.Time) error
    Requeue(ctx, taskID string, reason FailureKind, notBefore *time.Time, now time.Time) error // 토큰 폐기 포함
    ExpireStale(ctx, now time.Time) (requeued int, err error)   // dispatched 5분, heartbeat 3분 — 스케줄러가 호출
}
```

`now`를 인자로 받는다 — 시간 의존 로직은 전부 `contracts/clock`을 경유해야 테스트에서 시계를 돌릴 수 있다(E5-02·03, E13-09~13, E14-01·02).

## 8. 실시간 (사람 화면)

사람 화면의 실시간 갱신은 이 문서 범위 밖(`openapi.yaml`의 스트림 엔드포인트). 데몬 이벤트 → 서버 저장 → 웹 브로드캐스트 순서이고, 데몬은 웹을 모른다.

## 9. 계약 테스트 (P1 S+D)

| 테스트 | EVAL |
|---|---|
| claim이 `paused` 방·미래 `not_before`·다른 런타임 고정 방을 주지 않음 | E5-04, E11-09 |
| `none` 첫 claim이 `runtime_id` 고정 | E11-10 |
| `dispatched` 5분 → `failed(timeout)`, **재큐잉 없음** + 토큰 폐기 (클럭 주입) | E5-02 |
| heartbeat 3분 무응답 → 재큐잉 + 토큰 폐기 → 그 토큰의 `colab message post` 401 | E5-03, E11-03·04 |
| events `(task,attempt,seq)` 멱등, 재전송 시 중복 0 | E8-04 |
| `finish` 멱등 | — |
| `revoke` 명령 최소 한 번 전달 | E11-05 |
| `worktree` 같은 에이전트 lane 순차 claim | E2-12 |
| (v0.10.0) `dir` 첫 attempt 번들에 `workdir.id`·절대 `path`, 같은 미션·같은 에이전트의 두 lane 이 같은 id·같은 path, 미션에 매인 턴이면 `shared_path` | — |
| (v0.10.0) 방·미션·에이전트 이름을 바꾼 뒤 다음 번들의 `path` 가 그대로 | — |
| (v0.10.0) 옛 `sessions/<room>/<lane>` 행을 가진 lane 의 재진입 번들이 그 옛 경로 | — |
| (v0.10.0) 미션 닫힘 직후에는 `gc` 없음, `last_used_at + workdir_retention_days` 경과 뒤 에이전트 행·`_shared` 행에 `gc` (클럭 주입) | E13-09 |
| (v0.10.0) `gc` 뒤 빈 상위 폴더만 삭제, 파일이 남은 상위는 그대로 | — |
