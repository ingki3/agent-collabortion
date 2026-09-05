# 하네스 인터페이스 — 데몬 ↔ 런타임 (ACP)

| 항목 | 내용 |
|---|---|
| 버전 | v0.1 (G2 후보) |
| 소유 | D + Lead. 변경은 Director 승인 PR로만 (`contracts/README.md`) |
| 근거 | PRD §8.2 (하네스), §8.4 (브리프), FR-7.1 (재시도), FR-3.4·§8.2.2 (취소), FR-5.4 (재개). **G1 판정 `plan/G1_DECISION.md` 와 스파이크 보고서 `plan/spikes/SPIKE_01..06.md`** — 이 문서의 수치·옵션 키는 전부 실측에서 왔다 |
| 미결 | §12 — 스파이크 1b(P0-b) 결과로 닫는다 |

이 문서는 **데몬이 런타임 프로세스와 어떻게 말하는가**를 정한다. 데몬 ↔ 서버는 `daemon-protocol.md`, 이벤트 형식은 `task_event.schema.json`, 에이전트 → 서버는 `colab-cli.md`.

## 1. 런타임 바인딩

프로파일(`agent_profile`)이 런타임을 고른다. v1은 두 종류, 전송은 ACP 하나.

| `runtime_kind` | 어댑터 명령 | 어댑터 고정 | 모델 선택 | 브리프 전달 (`brief_transport`) |
|---|---|---|---|---|
| `claude_code` | `npx -y @agentclientprotocol/claude-agent-acp@<pin>` | **`0.74.0`** (G1 F1 — 구 `@zed-industries/claude-code-acp`는 동결, 쓰지 않는다) | `session/set_config_option` (스파이크 1b에서 확정; 실패 시 `session/set_model` 폴백) | `acp_meta_system_prompt` — `session/new._meta.systemPrompt = {append: <brief>}` (스파이크 3, **append 모드만**) |
| `hermes` | `hermes acp` | Hermes ≥ 0.20.6 | `session/set_model "anthropic:<model>"` | `instruction_file` — workdir `AGENTS.md` 마커 구간 (§8.4) |

- `transport`는 v1에서 항상 `acp`. `cli`는 타입에만 두고(v1.1) 구현하지 않는다 — G1.
- probe(`daemon-protocol.md` §3)가 어댑터 버전·`hermes --version`을 보고하고, 핀과 다르면 프로파일을 `error(config)`로 표시한다. **버전 고정이 드리프트 방어다** — `_meta.*` 확장은 스펙이 아니라 어댑터 구현이라 버전 간에 움직인다(스파이크 1 §4).

## 2. 프로세스 수명

```
spawn (pgid, cwd=workdir, env=§2.1)
  → initialize {protocolVersion: 1, clientCapabilities: {fs: {readTextFile: false, writeTextFile: false}, terminal: false}}
  → 응답 protocolVersion ≠ 1 → failure_kind=config (재시도 없음)
  → session/new {cwd, mcpServers: [colab MCP], _meta: §3}      (신규)
    또는 session/load {sessionId, cwd, mcpServers, _meta: §3}  (재개, §6)
  → session/prompt {sessionId, prompt: [{type:"text", text: <턴 프롬프트>}]}
  → session/update 스트림 → task_event 정규화 (§7)
  → session/prompt 응답 {stopReason} = 턴 종료 (§2.2)
  → 종료: 프로세스 그룹 SIGTERM → 10초 → SIGKILL. pgid 기록 삭제 (FR-9.1)
```

- 클라이언트 능력을 광고하지 않는다(`fs`·`terminal` false). 런타임이 자기 툴(Read/Write/Bash)을 쓰고, 권한 협상만 `session/request_permission`으로 온다(스파이크 1 §2).
- 한 프로세스 = 한 task attempt. 턴이 끝나면 프로세스를 내린다(FR-5.4 "프로세스가 답을 기다리며 살아 있지 않는다").

### 2.1 환경

런타임 프로세스에 주는 환경은 **허용 목록**이다. 사용자 셸 환경을 물려주지 않는다.

| 변수 | 값 |
|---|---|
| `COLAB_TASK_TOKEN` | `daemon-protocol.md` §5 — 이 attempt 전용 |
| `COLAB_SERVER_URL`, `COLAB_TASK_ID`, `COLAB_LANE_ID`, `COLAB_SESSION_ID`, `COLAB_AGENT_NAME` | colab CLI/MCP가 쓴다 |
| `PATH`, `HOME`, `LANG`, `TMPDIR` | 시스템 최소 |
| 런타임 인증 | Claude Code: `~/.claude` OAuth를 그대로(HOME). Hermes: `~/.hermes`. 프로파일 `env(jsonb)`는 **여기 더해진다** — 사용자가 명시한 것만 |

### 2.2 턴 종료 판정

| 런타임 | 판정 |
|---|---|
| `claude_code` | `session/prompt` 응답 수신 = 종료 |
| `hermes` | `session/prompt` 응답 수신 후 **250ms 정적 대기** — 마지막 `agent_message_chunk`가 응답 뒤에 올 수 있다(PRD §8.2.5) |

`stopReason` 매핑: `end_turn` → 정상, `cancelled` → 취소 완료, `max_tokens`·`max_turn_requests` → 정상(피드에 사유), `refusal` → 정상 종료 + 피드 경고(유실 판정에는 **쓰지 않는다** — G1 F7).

## 3. `_meta` — 어댑터 확장 (claude_code)

`session/new`와 `session/load` **양쪽**에 같은 `_meta`를 넣는다(load에서 유지되는지는 1b가 확인, §12).

```json
{
  "systemPrompt": { "append": "<브리프 [1]~[8], §8.4 순서 고정>" },
  "claudeCode": {
    "options": {
      "settingSources": [],
      "strictMcpConfig": true,
      "disallowedTools": ["AskUserQuestion"],
      "permissionMode": "default"
    }
  }
}
```

| 키 | 왜 |
|---|---|
| `systemPrompt.append` | 브리프 전달 경로 1(PRD §8.4). **대체(문자열) 모드는 쓰지 않는다** — Claude Code 기본 프롬프트(툴 규약)를 잃는다(스파이크 3 §2) |
| `settingSources: []` + `strictMcpConfig: true` | **G1 F2** — 기본값은 사용자 전역 설정을 읽어 Director 개인 MCP 서버·hooks가 에이전트 세션에 실린다. 데몬 세션은 colab MCP만 |
| `disallowedTools` | `AskUserQuestion`은 어댑터가 이미 빼지만 버전이 바뀌어도 우리 쪽에서 보장(스파이크 2 §3). 프로파일 `tools` 허용 목록에 없는 툴도 여기로 |
| `permissionMode: default` | 권한 요청이 `session/request_permission`으로 오게. `bypassPermissions`는 쓰지 않는다 — 요청이 안 오면 피드에 남길 수 없다 |

**서브에이전트 누수(G1 F5)**: `disallowedTools`는 `Task` 서브에이전트에 전파되지 않는다. 서브에이전트까지 막아야 하는 툴은 `permissions.deny`를 SDK 옵션으로 넘길 수 있는지 1b가 확인한다. **확인 전에는 workdir에 `.claude/settings.json`을 쓰지 않는다**(워크트리 오염, §8.4 M6).

Hermes는 `_meta`를 버린다(스파이크 3 §1). Hermes의 툴 제한은 `hermes acp`의 자체 설정으로 — P1 하네스 작업에서 확인.

## 4. 권한 응답 정책

`session/request_permission {toolCall, options[]}`에 데몬이 자동 응답한다. 사람의 승인은 ACP 권한이 아니라 **앱 층의 HITL(`colab hitl approve-request`)** 이다 — 둘을 섞지 않는다.

| 상황 | 응답 | 기록 |
|---|---|---|
| 옵션 중 `kind == "allow_once"` 있음 | 그 `optionId`로 `selected`. **optionId 값을 하드코딩하지 않는다**(어댑터마다 다름; 실측 `allow`) | task_event `tool.permission` outcome=allowed |
| `allow_once` 없음 | `kind == "reject_once"`로 `selected` | outcome=rejected + 피드 경고. 같은 런타임에서 3회 누적 → `runtime.capabilities.allow_once_missing = true` 보고(E12-03) |
| 취소 진행 중 | `outcome: "cancelled"` | — |
| 프로파일 `tools` 허용 목록 밖의 툴 | 위 규칙 대신 `reject_once` | outcome=rejected(policy) |

실측: 26/26 + 11/11에서 `allow_once` 부재 0(스파이크 1). 부재 경로는 가짜 에이전트 계약 테스트로 커버한다(§11).

## 5. 취소 절차 (PRD §8.2.2, FR-3.4)

서버가 `cancel {after_current_tool: bool}`을 내린다(`daemon-protocol.md` §4.3). 데몬 절차:

1. `after_current_tool`이고 마지막 `tool_call`이 파일 편집·셸이며 완료 이벤트가 없으면 **완료를 최대 30초 기다린다**. 넘기면 그대로 진행하고 피드에 "30초 초과로 강제 취소".
2. 대기 중인 `session/request_permission`이 있으면 `outcome: "cancelled"`로 응답한다.
3. `session/cancel {sessionId}`.
4. `session/prompt` 응답(`stopReason: cancelled`)을 최대 10초 기다린다(드레인).
5. 프로세스 그룹 SIGTERM → 10초 → SIGKILL. 프로세스 트리 잔존 0 (E10-03, E11-07).

실측: 취소 10/10 + 3/3 `stopReason: cancelled`, 권한 대기 중 취소는 실기에서 발동하지 않아(권한이 먼저 allow됨) 가짜 에이전트 테스트로 커버.

## 6. 재개 (FR-5.4, FR-7.1)

`lane.runtime_session_ref`:

```json
{
  "runtime_kind": "claude_code" | "hermes",
  "adapter_version": "0.74.0",
  "session_id": "<ACP sessionId>",
  "cwd": "<workdir>",
  "created_at": "<RFC3339>",
  "provenance": { "acpSessionId": "…", "rootHermesSessionId": "…", "sessionKind": "…" }
}
```

| 런타임 | 절차 | 유실 판정 → 콜드 스타트 |
|---|---|---|
| `claude_code` | `session/load {sessionId, cwd, mcpServers, _meta}` → 리플레이 `user_message_chunk`/`agent_message_chunk` 수신(**버린다** — task_event로 올리지 않는다, G1 F4: 11회에 111청크) → 응답 | JSON-RPC 오류 `"Session not found"` → `resume_rejected` |
| `hermes` | `session/load {sessionId, cwd}` → 응답 | (a) 결과 `null` → `resume_rejected` (b) `_meta.hermes.sessionProvenance.acpSessionId ≠ 요청 id` → `resume_rejected` (c) 같으면 유지. `session/resume`(UNSTABLE)은 **쓰지 않는다** |

- 실측: Claude Code 컨텍스트 유지 10/10, Hermes 유실 감지 3/3·오탐 0(스파이크 4a).
- `resume_rejected`는 실패가 아니다. 데몬이 콜드 스타트(`session/new` + 브리프 + 히스토리 + 결정 기록)로 이어가고 `task_event` `runtime.resume outcome=cold_start`를 올린다(E8-02).
- provenance의 `rootHermesSessionId`가 같고 `acpSessionId`만 다르면 압축 회전(`reason: compression`)일 수 있다 — 유실로 치지 않고 새 id를 저장한다(스파이크 4a 권고 2).
- 재개 턴 프롬프트는 서버가 만든다(`<resumed>` 구간, PRD §8.4). 데몬은 받은 프롬프트를 그대로 보낸다.

## 7. 이벤트 정규화

`session/update` → `task_event` (`task_event.schema.json`). 한 task attempt 안에서 `seq`는 1부터 단조 증가, 서버가 `(task_id, seq)`로 멱등 처리.

| ACP `sessionUpdate` | class | verb | object_ref | outcome |
|---|---|---|---|---|
| `agent_message_chunk` | `message` | `say` | — | 청크를 **턴 단위로 합쳐** 하나의 이벤트(스트리밍은 `daemon-protocol.md` §4.2의 partial 플래그) |
| `agent_thought_chunk` | `message` | `think` | — | 합쳐서 하나. 원본 레일에만 |
| `tool_call` (status pending/in_progress) | `tool` | `kind`에서: `edit`→`edit_file`, `execute`→`run_shell`, `read`→`read`, `search`/`fetch`→`search`, 그 외 `use_tool` | `locations[0].path` 또는 `title` | `started` |
| `tool_call_update` (completed/failed) | `tool` | 같은 verb, 같은 `toolCallId` | 같음 | `ok` / `failed` + `content` 요약(diff는 +/- 줄 수, 셸은 종료 코드) |
| `session/request_permission` | `tool` | `permission` | toolCall.title | `allowed` / `rejected` / `cancelled` (§4) |
| `usage_update` | `usage` | `report` | — | `{input, output, cache_read, cache_write, cost_usd?}` 누적 |
| `plan` | `plan` | `update` | — | 항목 수·완료 수 |
| `session/load` 리플레이 | — | — | — | **버림** |
| 어댑터/프로세스 오류 | `runtime` | `error` | — | `failure_kind` (§8) |

- `tool.edit_file` 페이로드에는 경로·+/-줄 수·`workspace_settings.task_event_masking`이 켜져 있으면 요약만.
- `stall` 판정: `running` 상태에서 `session/update`도 권한 요청도 없이 **3분** → `failure_kind=stall`, 재시도(FR-7.1).

## 8. 오류 분류 (`task.failure_kind`)

| 값 | 판정 | 재시도 |
|---|---|---|
| `auth` | 어댑터 stderr/오류에 login·unauthorized, Claude Code "Login expired" | **없음** → 에이전트 `error` |
| `quota` | 조직 쿼터·결제 실패 | 없음 |
| **`rate_limited`** | `session/prompt` JSON-RPC `-32603` 본문에 `hit your limit`·`resets <time>` (G1 F3). 프로세스는 살아 있다 | **리셋 시각까지 `queued`** (`task.not_before` = 파싱한 시각, 못 파싱하면 +30분). 같은 런타임의 다른 task도 같은 `not_before` |
| `config` | `protocolVersion ≠ 1`, 어댑터 핀 불일치, CLI 없음, 모델 없음 | 없음 |
| `network` | 서버 연결 실패 | 2~3회 |
| `runtime_offline` | heartbeat 3분 무응답(서버 판정) | 재큐잉 + 토큰 폐기 |
| `stall` | §7 | 2~3회, 대체 프로파일 있으면 전환 |
| `timeout` | `dispatched` 5분 초과(서버 판정) | 재큐잉 |
| `cancelled` | §5 | 없음 |
| `other` | 그 외 프로세스 비정상 종료(`UnexpectedExit`) | 2~3회 |

Hermes 보조 신호: stderr의 프로바이더 오류 문구 스니핑 → `other`/`auth`로 분류(PRD §8.2.5).

## 9. 능력 광고 (probe)

페어링 시와 하루 한 번, 런타임마다 한 턴("PONG"만 답하라)을 돌려 실측한다. 결과는 `runtime.capabilities[]`:

```json
{ "kind": "claude_code", "version": "2.1.258", "adapter_version": "0.74.0", "logged_in": true,
  "models": ["…"], "protocol_version": 1,
  "resume": true, "usage": true, "tool_disallow": true,
  "brief_transport": "acp_meta_system_prompt", "allow_once_missing": false }
```

Hermes: `usage: true`(G1 F6), `resume: true`(`session/load`), `brief_transport: "instruction_file"`.

## 10. 브리프와 workdir (PRD §8.4)

| 런타임 | 브리프 | workdir 파일 |
|---|---|---|
| `claude_code` | `_meta.systemPrompt.append` — **파일을 쓰지 않는다** | 없음. `CLAUDE.md`를 만들거나 고치지 않는다 |
| `hermes` | `AGENTS.md` 마커 구간 `<!-- colab:brief:start -->` … `<!-- colab:brief:end -->` | 추적 파일이면 `skip-worktree`, 미추적이면 `.git/info/exclude`(§8.4 M3). lane 종료 시 마커 구간만 제거 |

브리프 [1]~[8]의 순서는 고정이고 [1]~[5]는 같은 세션 안에서 바이트 동일(캐시 친화, E12-11).

## 11. 계약 테스트 (P1 D 작업, EVAL §E12)

가짜 ACP 서버(`daemon/internal/acpfake`)로:

| 테스트 | EVAL |
|---|---|
| `allow_once`를 kind로 고름, optionId 무관 | E12-01 |
| `allow_once` 부재 → `reject_once` + 카운트 3회 → 능력 플래그 | E12-02·03 |
| 취소 중 권한 요청 → `cancelled` 응답, 순서 §5 | E10-03 |
| Hermes 응답 뒤 청크 → 250ms 대기로 수신 | E12-04 |
| `session/load` null → 콜드 스타트, provenance 불일치 → 콜드 스타트, 일치 → 유지 | E8-02·03 |
| 리플레이 청크 버림 | — |
| `-32603 hit your limit` → `rate_limited` + `not_before` 파싱 | — |
| `protocolVersion 2` → `config` | E12-08 |
| `_meta` 하나만(중복 주입 0), Hermes에는 `_meta` 없음 | E12-09 |
| 브리프 [1]~[5] 바이트 동일 | E12-11 |

실기 계약 테스트(야간, 실제 어댑터): 스파이크 1의 30턴 시나리오를 100턴으로.

## 12. 미결 — 스파이크 1b가 닫는다

| # | 질문 | 임시값 |
|---|---|---|
| 1 | 0.74.0 모델 선택 방법 | `session/set_config_option`, 실패 시 `session/set_model` |
| 2 | `_meta.systemPrompt`가 0.74.0에서 유지되는가, `session/load`에서도 필요한가 | 양쪽에 넣는다 |
| 3 | `settingSources: []` + `strictMcpConfig`로 사용자 MCP·hooks가 빠지는가 | 빠진다고 가정 |
| 4 | `permissions.deny`를 파일 없이 옵션으로 넘겨 서브에이전트까지 막을 수 있는가 | 못 하면 프로파일 `tools`는 주 에이전트에만 적용됨을 문서화 |
