# 데몬 ↔ 서버 프로토콜

| 항목 | 내용 |
|---|---|
| 버전 | v0.6 — `dispatched` 5분 타임아웃은 재큐잉이 아니라 종료다(§4.1). v0.5는 probe 최상위 `colab_cli`(§3), `preview.message_id` 의 주체를 서버로 명시(§4.2). v0.4 는 프로파일 폴백의 주체를 서버로 명시(§4.4). v0.3 은 G3 재확인 C-1: heartbeat `preview` **모양 확정**(객체)과 "부가 정보는 heartbeat를 실패시키지 않는다" 규칙. v0.2는 명령 소비 조건·heartbeat 만료 범위 |
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

**`colab_cli` 는 최상위다 (v0.5).** 에이전트는 colab CLI 로 서버에 말하므로(colab-cli.md §1) CLI 가 없으면 세션은 조용히 아무 말도 못 하는 상태가 된다 — 데몬 로그에만 남기면 사람이 원인을 못 찾는다. 이 값은 **머신 속성**이라 런타임별 `capabilities[]` 가 아니라 probe 최상위에 한 번 싣는다: 런타임이 둘이어도 바이너리는 하나이고, 런타임이 0개인 머신에서도 보고돼야 한다. 데몬은 probe 마다 `colab --version` 을 실행해 채우고, 실행 실패·미설치는 `{present: false, version: ""}` 로 통일한다(원인은 데몬 로그에 남긴다). 서버는 `present == false` 인 머신을 S12/S11 카드에 경고로 드러낸다.

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
- 이 런타임에 **고정된 세션**(`session.runtime_id`)의 `queued` task만 준다(E11-09). `none` 격리에 `runtime_id`가 비었으면 첫 claim한 런타임으로 고정한다(E11-10).
- 세션이 `paused`면 주지 않는다(E5-04). `task.not_before`가 미래면 주지 않는다(`rate_limited`).
- 동시성 상한 4층(FR-6.3)을 서버가 계산한다. `worktree` 격리에서 같은 에이전트의 다른 lane이 `running`이면 주지 않는다(E2-12).
- 큐는 `Queue` 인터페이스(§7) 뒤에 있다. v1 구현은 Postgres `SELECT … FOR UPDATE SKIP LOCKED`.
- claim 즉시 `queued → dispatched`, `dispatched_at` 기록. 5분 안에 `preparing` 보고가 없으면 **`failed(timeout)`으로 끝난다 — 재큐잉하지 않는다**(E5-02, PRD FR-7.1). 서버는 그 attempt의 task token을 폐기하고(좀비 데몬이 나중에 보고하지 못하게) Director가 보도록 드러낸다.

  **v0.6 정정.** 이 줄은 v0.5까지 "→ 재큐잉(E5-02)"이라고 적혀 있었다. 그런데 인용한 EVAL E5-02는 `failed(timeout)`만 말하고 재큐잉을 말하지 않으며, PRD FR-7.1도 "`dispatched` 5분 초과 → `failed(timeout)`"이고 **재시도 대상 목록(`runtime_offline`·네트워크·프로세스 stall)에 `timeout`이 없다.** 근거를 인용하면서 반대로 옮긴 것이라 계약을 고친다.

  **왜 `running`(E5-03, 3분)은 재큐잉인데 이쪽은 아닌가.** `dispatched`는 데몬이 **이미 claim해서 소유권과 토큰을 가진** 상태다. 그 데몬이 모델 작업 이전 단계인 `preparing`조차 보고하지 못했다면 고장은 반복된다 — 세션은 런타임에 고정돼 있으므로(§4.1) 재큐잉은 같은 고장난 런타임에게 되돌려 주는 것이고, claim ↔ timeout 을 오가며 attempt만 태운다. 반대로 `running` 침묵은 프로세스가 실제로 시작된 뒤의 일이라 재시도에 승산이 있고, 그 경로는 런타임을 `offline`으로 표시해 즉시 재배정 루프를 막는다.

**TaskBundle**

```json
{
  "task": { "id", "attempt", "lane_id", "session_id", "agent_id", "trigger_message_id",
            "restarted_from_task_id?", "delegated_from_task_id?", "budget_usd?", "budget_override_usd?" },
  "task_token": "ctk_…",
  "profile": { "runtime_kind", "model", "options", "env", "args", "tools", "adapter_pin" },
  "workdir": { "kind": "worktree|dir", "path?", "repo_path?", "branch?", "reuse": true|false },
  "brief": { "transport": "acp_meta_system_prompt|instruction_file", "text": "<[1]~[8]>" },
  "prompt": "<턴 프롬프트 — 서버가 만든다. 재개면 <resumed> 구간 포함>",
  "resume": { "runtime_session_ref": <harness.md §6> } | null,
  "limits": { "budget_usd", "stall_seconds": 180 },
  "posted_message_ids": [ … ]      // attempt ≥ 2일 때, 이미 게시한 메시지(FR-7.1)
}
```

데몬은 번들 밖의 것을 알 필요가 없다 — 세션 히스토리도 프롬프트 안에 들어 있다.

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
| `gc` | `{workdir_ids: [...]}` 또는 `{policy: {...}}` | §6 |
| `rebind_prepare` | `{session_id, artifacts: [{id, order, url}]}` | 새 workdir 준비 후 아티팩트 순서 적용은 **프롬프트가 지시**(FR-9.2). 데몬은 다운로드만 |

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
    last_seq, workdir: {path, git: {branch, dirty, ahead}?} }
  → 200 {ok}
```

- `waiting_human`·`blocked`는 데몬이 정하지 않는다. `turn_end`가 왔을 때 서버가 `pending_hitl`(FR-7.1 HITL 전이) 또는 `status set blocked` 호출 여부로 정하므로, 데몬은 `outcome: "completed"` + `stop_reason`을 보내고 **서버가 최종 상태를 정한다**. 위 열거는 서버 응답의 최종 상태이지 데몬 판단이 아니다.
- `finish`는 attempt 단위로 멱등. 두 번 와도 첫 결과가 남는다.
- **프로파일 폴백은 서버가 결정한다 (v0.4).** 데몬은 실패를 `failure_kind`로 정확히 보고할 뿐, 대체 프로파일로 스스로 갈아타지 않는다. 이유: (a) 세션이 `runtime_id`에 고정되므로(FR-2.1 M10) 서버의 재큐잉은 **같은 머신을 구조적으로 보장**한다 — FR-7.1의 "같은 머신 안에 대체 프로파일이 있으면 전환"이 저절로 성립한다. (b) 재시도 회계(`attempt`·상한 2~3회)·토큰 발급·비용 집계가 전부 서버 소유라, 데몬이 in-process로 갈아타면 그 셋이 흐려진다. (c) 서버는 `agent_profile.fallback_profile_id`를 이미 갖고 있고 데몬은 알 필요가 없다.

  서버가 폴백할 때: 같은 workdir(`workdir.reuse: true`), `attempt` 증가, **`runtime_kind`가 바뀌면 `resume`을 비운다**(런타임 세션은 이어받을 수 없다 — E8-08). 같은 머신에 쓸 수 있는 대체 프로파일이 없으면 `queued`로 두고 Director에게 알린다. **다른 머신으로 넘기지 않는다**(E8-09).

  따라서 `TaskBundle`에 대체 프로파일 목록은 두지 않는다.
- `paused_budget`: 데몬이 `usage_update` 누적으로 `limits.budget_usd`를 넘겨 취소 절차를 밟은 경우(FR-7.3). `failure_kind` 없음.

## 5. 토큰 폐기와 고아 (FR-9.1)

| 시점 | 서버 | 데몬 |
|---|---|---|
| 재큐잉(heartbeat 만료·timeout·재시도) | 그 attempt의 `ctk_` **즉시 폐기**. 이후 그 토큰의 `colab` 호출은 `401 token_revoked`(E11-04) | 다음 claim/heartbeat 응답에서 `revoke` 명령 수신 |
| 취소·완료·`waiting_human` 전이 | 폐기 | — |
| 데몬 재시작 | — | claim **전에** 디스크의 `pgid` 기록을 읽어 살아 있는 프로세스 그룹을 SIGTERM/SIGKILL(E11-05). 기록 형식: `<workdir_root>/.colab/attempts/<task_id>.<attempt>.json {pgid, started_at}` — 정상 종료 시 삭제 |

**방향은 서버 → 데몬이다**(PLAN 리뷰 #02 m9). 데몬이 토큰을 폐기 요청하는 경로는 없다 — 재큐잉을 서버가 하므로.

## 6. workdir와 GC (FR-6.4)

```
POST /v1/daemon/runtimes/{runtime_id}/workdirs   {workdirs: [{id?, kind, path, session_id, agent_id?, lane_id?, bytes, last_used_at, git: {branch, merged, dirty, commits_ahead}?}]}
```

- 데몬은 workdir 목록을 probe와 함께, 그리고 lane 종료 시 보고한다. S13이 이 데이터를 보여준다.
- GC 판정은 **서버**가 한다(보존 기한·용량 상한·미병합/미커밋 차단 — E13-09~13). 서버가 `gc {workdir_ids}` 명령을 내리면 데몬이 삭제하고 결과를 보고한다. 데몬은 스스로 지우지 않는다.
- `worktree` 삭제는 `git worktree remove`만, 브랜치는 남긴다(E13-10).
- 디스크 상한 도달은 probe의 `disk`로 서버가 판정해 새 세션 생성을 막는다(E13-16).

## 7. 큐 인터페이스 (서버 내부)

Postgres SKIP LOCKED를 Redis로 바꿔도 이 프로토콜은 안 바뀐다(PLAN §7-3). 서버 코드는 아래 인터페이스만 본다.

```go
type Queue interface {
    Claim(ctx, runtimeID string, capacity int, now time.Time) ([]TaskBundle, error) // not_before ≤ now, 세션 active, 동시성 상한 적용
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
| claim이 `paused` 세션·미래 `not_before`·다른 런타임 고정 세션을 주지 않음 | E5-04, E11-09 |
| `none` 첫 claim이 `runtime_id` 고정 | E11-10 |
| `dispatched` 5분 → `failed(timeout)`, **재큐잉 없음** + 토큰 폐기 (클럭 주입) | E5-02 |
| heartbeat 3분 무응답 → 재큐잉 + 토큰 폐기 → 그 토큰의 `colab message post` 401 | E5-03, E11-03·04 |
| events `(task,attempt,seq)` 멱등, 재전송 시 중복 0 | E8-04 |
| `finish` 멱등 | — |
| `revoke` 명령 최소 한 번 전달 | E11-05 |
| `worktree` 같은 에이전트 lane 순차 claim | E2-12 |
