# 하네스 인터페이스 — 데몬 ↔ 런타임 (ACP)

| 항목 | 내용 |
|---|---|
| 버전 | v0.7 — §11 `cost_usd` 부재 규칙(생략 + `estimated:true`)과 가격표 소유(워크스페이스=서버). v0.6은 §2.1 시스템 최소에 `USER`(G4 차단 결함: OAuth 갱신 시 키체인이 `USER`를 쓴다). v0.5는 §9 `supported_options`(T-W2가 찾은 빈칸: 프로파일 옵션 능력을 광고할 키가 v0.4.1 대조에서 빠졌다). v0.3 은 PR #20(데몬 P1) 구현·리뷰에서 드러난 5건 반영(usage 시점, Hermes 모델 접두어, Hermes 본문 오류 접두어 규칙, disallowedTools 도출, 250ms 비주입). v0.2는 스파이크 1b 반영 |
| 소유 | D + Lead. 변경은 Director 승인 PR로만 (`contracts/README.md`) |
| 근거 | PRD §8.2 (하네스), §8.4 (브리프), FR-7.1 (재시도), FR-3.4·§8.2.2 (취소), FR-5.4 (재개). **G1 판정 `plan/G1_DECISION.md` 와 스파이크 보고서 `plan/spikes/SPIKE_01..06.md`, `SPIKE_01b.md`** — 이 문서의 수치·옵션 키는 전부 실측에서 왔다 |
| 미결 | 없음 (§12 참조). 서브에이전트 가시성은 v1 피드 요구로 미광고 |

이 문서는 **데몬이 런타임 프로세스와 어떻게 말하는가**를 정한다. 데몬 ↔ 서버는 `daemon-protocol.md`, 이벤트 형식은 `task_event.schema.json`, 에이전트 → 서버는 `colab-cli.md`.

## 1. 런타임 바인딩

프로파일(`agent_profile`)이 런타임을 고른다. v1은 두 종류, 전송은 ACP 하나.

| `runtime_kind` | 어댑터 명령 | 어댑터 고정 | 모델 선택 | 브리프 전달 (`brief_transport`) |
|---|---|---|---|---|
| `claude_code` | `npx -y @agentclientprotocol/claude-agent-acp@<pin>` | **`0.74.0`** (G1 F1 — 구 `@zed-industries/claude-code-acp`는 동결, 쓰지 않는다) | `session/set_config_option {configId: "model", value}` — **`session/new` 뒤와 모든 `session/load` 뒤에** 호출한다. load 시 기본 모델로 되돌아간다(1b E1). 응답 `configOptions[id="model"].currentValue`로 확인. `session/set_model`은 0.74.0에 없다 | `acp_meta_system_prompt` — `_meta.systemPrompt = {append: <brief>}`를 **`session/new`·`session/load` 양쪽에 매번**(1b E2: 세션에 저장되지 않는다). **append 모드만** |
| `hermes` | `hermes acp` | Hermes ≥ 0.20.6 | `session/set_model "<provider>:<model>"` — 프로파일 `model`은 **provider 접두어 없이** 저장하고(Claude Code와 같은 값 집합), 데몬이 `anthropic:`을 붙인다. 프로파일에 `:`가 이미 있으면 그대로(v0.3, PR #20 결함 2) | `instruction_file` — workdir `AGENTS.md` 마커 구간 (§8.4) |

- `transport`는 v1에서 항상 `acp`. `cli`는 타입에만 두고(v1.1) 구현하지 않는다 — G1.
- probe(`daemon-protocol.md` §3)가 어댑터 버전·`hermes --version`을 보고하고, 핀과 다르면 프로파일을 `error(config)`로 표시한다. **버전 고정이 드리프트 방어다** — `_meta.*` 확장은 스펙이 아니라 어댑터 구현이라 버전 간에 움직인다(스파이크 1 §4).

## 2. 프로세스 수명

```
spawn (pgid, cwd=workdir, env=§2.1)
  → initialize {protocolVersion: 1, clientCapabilities: {fs: {readTextFile: false, writeTextFile: false}, terminal: false}}
  → 응답 protocolVersion ≠ 1 → failure_kind=config (재시도 없음)
  → session/new {cwd, mcpServers: [colab MCP], _meta: §3}      (신규)
    또는 session/load {sessionId, cwd, mcpServers, _meta: §3}  (재개, §6)
  → session/set_config_option {sessionId, configId: "model", value: <profile.model>}   (claude_code: new/load 뒤 항상)
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
| `COLAB_SERVER_URL`(오리진), `COLAB_TASK_ID`, **`COLAB_TASK_ATTEMPT`**, `COLAB_LANE_ID`, `COLAB_SESSION_ID`, `COLAB_AGENT_NAME` | colab CLI/MCP가 쓴다(`colab-cli.md` §1). attempt는 멱등키 파생에 필요 |
| `PATH`, `HOME`, `LANG`, `TMPDIR`, **`USER`** | 시스템 최소. **`USER`는 v0.6 추가** — macOS에서 Claude Code가 만료된 OAuth를 갱신할 때 키체인 조회가 `USER`를 쓴다. 없으면 `Failed to authenticate: OAuth session expired and could not be refreshed`로 모든 task가 `failure_kind=auth`. P1에서는 액세스 토큰이 살아 있어 갱신이 필요 없었기에 드러나지 않았다(G4 첫 실행에서 격리: `env -i PATH HOME LANG TMPDIR claude -p PONG` 실패, `USER` 추가 시 성공) |
| 런타임 인증 | Claude Code: `~/.claude` OAuth를 그대로(HOME). Hermes: `~/.hermes`. 프로파일 `env(jsonb)`는 **여기 더해진다** — 사용자가 명시한 것만 |

### 2.2 턴 종료 판정

| 런타임 | 판정 |
|---|---|
| `claude_code` | `session/prompt` 응답 수신 = 종료 |
| `hermes` | `session/prompt` 응답 수신 후 **250ms 정적 대기** — 마지막 `agent_message_chunk`가 응답 뒤에 올 수 있다(PRD §8.2.5) |

`stopReason` 매핑: `end_turn` → 정상, `cancelled` → 취소 완료, `max_tokens`·`max_turn_requests` → 정상(피드에 사유), `refusal` → 정상 종료 + 피드 경고(유실 판정에는 **쓰지 않는다** — G1 F7).

## 3. `_meta` — 어댑터 확장 (claude_code)

`session/new`와 `session/load` **양쪽에 매번** 같은 `_meta`를 넣는다 — 어댑터는 `_meta`를 세션에 저장하지 않는다(1b E2, `acp-agent.js` `createSession`). 어댑터는 `claudeCode.options`의 키를 검증 없이 SDK로 통과시키므로 오타는 조용히 무시된다 — 효과는 계약 테스트(§11)로 확인한다.

```json
{
  "systemPrompt": { "append": "<브리프 [1]~[8], §8.4 순서 고정>" },
  "claudeCode": {
    "options": {
      "settingSources": [],
      "strictMcpConfig": true,
      "disallowedTools": ["AskUserQuestion", "<프로파일 tools 허용 목록 밖의 툴>"],
      "settings": { "permissions": { "deny": ["<서브에이전트까지 막을 규칙, 예: Bash(rm -rf:*)>"] } },
      "permissionMode": "default"
    }
  }
}
```

| 키 | 왜 | 실측 |
|---|---|---|
| `systemPrompt.append` | 브리프 전달 경로 1(PRD §8.4). **대체(문자열) 모드는 쓰지 않는다** — Claude Code 기본 프롬프트(툴 규약)를 잃는다 | 스파이크 3 3/3, 1b E2 new 3/3·load 적용 확인. **브리프 없는 턴이 한 번 끼면 이후 턴이 그 답을 따라간다**(이력 오염) — resume에서 브리프를 빠뜨리는 것은 회귀 |
| `settingSources: []` | **G1 F2** — 기본 `["user","project","local"]`은 `~/.claude/settings.json`(hooks)·`~/.claude.json`(사용자 MCP)·`~/.claude/skills`를 읽는다. `[]` = SDK isolation. workdir `.claude/settings.json`도 안 읽으므로 오염 걱정 없음. 지시 파일 프로파일이라면 `["project"]`(CLAUDE.md를 읽으려면 필요) — v1 Claude Code는 `_meta` 경로라 `[]` | 1b E3: mcp__ 툴 0, hooks 0, 사용자 스킬 0 |
| `strictMcpConfig: true` | `mcpServers` 파라미터로 넘긴 것 외 모든 MCP 차단 — `settingSources`만으로는 **claude.ai 원격 커넥터(Drive·Calendar·Gmail)가 남는다** | 1b E3: `mcp_servers: []`. **두 키가 모두 있어야 한다** |
| `disallowedTools` | 모델 툴 목록에서 제거(주 에이전트 UX). `AskUserQuestion`은 어댑터가 이미 빼지만 버전이 바뀌어도 우리 쪽에서 보장 | 스파이크 2. **서브에이전트에는 전파되지 않는다** |
| `settings.permissions.deny` | **서브에이전트까지 강제**하는 권한 규칙(SDK `--settings` 인라인 계층, 파일 없음). `Task` 안의 Bash도 "Permission … denied"로 돌아온다 | 1b E4 1/1. workdir `.claude/settings.json`은 **쓰지 않는다**(§8.4 M6) |
| `permissionMode: default` | 권한 요청이 `session/request_permission`으로 오게. `bypassPermissions`는 쓰지 않는다 — 요청이 안 오면 피드에 남길 수 없다 | — |

역할 분담: **`disallowedTools` = 목록에서 제거, `permissions.deny` = 호출을 거부.** 프로파일 `tools` 허용 목록은 둘 다에 반영한다.

서브에이전트 가시성: 0.74.0은 서브에이전트의 툴 호출을 ACP `tool_call`로 올리지 않는다. 피드에 보이려면 `clientCapabilities._meta["subagent-transcript"]: true`를 광고해야 한다 — v1은 **광고하지 않는다**(피드 요구사항 밖, 원시 알림 폭증). v1.1 검토.

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
| `usage_update` | `usage` | `report` | — | 실측(P1 D)상 `usage_update`에는 토큰이 없고 `{used, size}`(컨텍스트 창)와 **`_meta["_claude/rateLimit"]`**(`status`, `resetsAt`, `rateLimitType`, `utilization`)만 온다 → `payload.rate_limit`로 올린다(매 턴, 리셋 시각을 미리 보여주는 근거). **토큰·비용은 `session/prompt` 응답 `usage`에서 턴 종료 시 1회** `usage.report {input, output, cache_read, cache_write, cost_usd?, cumulative:true}` (v0.3, PR #20 결함 1). **`cost_usd`의 뜻(v0.7)**: 런타임이 비용을 주면 그 값 + `estimated: false`. 주지 않으면 **`cost_usd`를 생략하고 `estimated: true`** — `0`을 실측처럼 보내지 않는다(G4 3판 W16: Claude Code는 토큰만 주므로 데몬이 `cost_usd: 0, estimated: false`를 보내 세션 비용이 확정 $0으로 보였다). **가격표는 워크스페이스 소유**(PRD §8.2.6 "워크스페이스 가격표로 계산")이므로 추정은 **서버**가 롤업 시 토큰 × 단가로 채우고 배지(FR-7.3)를 단다. 데몬은 단가를 모른다 |
| `session/prompt` 응답 `_meta.quota.model_usage[].model` | `runtime` | `turn_end` | — | 실제 실행 모델. 프로파일 모델과 다르면 `payload.model_drift: true` + 피드 경고(1b E1 — load 후 기본 모델로 되돌아가는 회귀 감시) |
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
| **`rate_limited`** | 우선순위(1b E5): (1) 직전 `usage_update._meta["_claude/rateLimit"].status == "rejected"` → `reset_at = resetsAt`(epoch초) (2) `session/prompt` 오류 `-32603` + `data.errorKind ∈ {rate_limit, overloaded}` (3) 오류 메시지가 SDK `USAGE_LIMIT_ERROR_PREFIXES` 12개(`protocol.go` `UsageLimitPrefixes`) 중 하나로 시작 → 리셋 시각 파싱(`resets 11am (Asia/Seoul)` 형식), 없으면 `quota`로 사람 에스컬레이션. 프로세스는 살아 있다 | **리셋 시각까지 `queued`** (`task.not_before` = `reset_at`, 없으면 +30분). 같은 런타임의 다른 task도 같은 `not_before`. `-32603`이지만 위 셋에 안 걸리면 `other` |
| `config` | `protocolVersion ≠ 1`, 어댑터 핀 불일치, CLI 없음, 모델 없음 | 없음 |
| `network` | 서버 연결 실패 | 2~3회 |
| `runtime_offline` | heartbeat 3분 무응답(서버 판정) | 재큐잉 + 토큰 폐기 |
| `stall` | §7 | 2~3회, 대체 프로파일 있으면 전환 |
| `timeout` | `dispatched` 5분 초과(서버 판정) | 재큐잉 |
| `cancelled` | §5 | 없음 |
| `other` | 그 외 프로세스 비정상 종료(`UnexpectedExit`) | 2~3회 |

Hermes 보조 신호: stderr의 프로바이더 오류 문구 스니핑 → `other`/`auth`로 분류(PRD §8.2.5). **v0.3 추가(PR #20 결함 3)**: Hermes는 프로바이더 오류를 **`stopReason: end_turn`의 본문**으로 돌려준다(실측 "API call failed after 1 retries: HTTP 429 …"). 따라서 **툴 호출 0 + 본문 전체가 Hermes 오류 형식으로 시작**할 때만 턴을 실패로 분류한다 — 정확한 판별자는 접두어 정규식 `^API call failed after \d+ retries: ` (본문 첫 줄, 앞뒤 공백 제외, 그 외 텍스트가 앞에 있으면 **아니다**). 그 뒤의 `HTTP 429`·`rate limit` → `rate_limited`(리셋 시각 없음 → +30분), `HTTP 401`·`403`·`authentication` → `auth`, 그 외 → `other`. 에이전트가 "빌드 실패 원인: API call failed … 429"처럼 **보고 문장 안에** 같은 문구를 쓰는 경우는 접두어가 아니므로 오탐하지 않는다(PR #20 리뷰 R4의 우려). PRD §8.2.5의 `refusal && 활동 0` 규칙과 같은 자리에서 판정하고, 이 본문은 메시지로 게시하지 않는다.

`disallowedTools` 도출(PR #20 결함 4): "프로파일 `tools` 허용 목록 밖의 툴"을 계산하려면 런타임의 전체 툴 표가 필요하다. 데몬이 Claude Code 툴 표(`KnownClaudeTools`, 어댑터 핀과 함께 관리)를 갖고 차집합을 `disallowedTools`와 `permissions.deny` 양쪽에 넣는다. 표에 없는 새 툴은 막히지 않는다 — 어댑터 핀을 올릴 때 표를 갱신한다.

250ms 정적 대기(§2.2)는 어댑터 동작 대기라 **클럭 주입 대상이 아니다**(PR #20 결함 5). stall·취소 대기·`not_before`는 클럭 주입.

## 9. 능력 광고 (probe)

페어링 시와 하루 한 번, 런타임마다 한 턴("PONG"만 답하라)을 돌려 실측한다. 결과는 `runtime.capabilities[]`:

```json
{ "kind": "claude_code", "version": "2.1.258", "adapter_version": "0.74.0", "logged_in": true,
  "models": ["…"], "protocol_version": 1,
  "resume": true, "usage": true, "tool_disallow": true,
  "brief_transport": "acp_meta_system_prompt", "allow_once_missing": false,
  "supported_options": { "effort": ["low", "medium", "high", "xhigh"] } }
```

`supported_options`(v0.5)는 이 런타임이 받는 프로파일 옵션과 허용 값이다. 어댑터는 `claudeCode.options`의 키를 검증 없이 통과시키므로(§2) 실측할 수 없다 — 데몬이 `(kind, adapter_version)`로 **아는 범위**를 표로 광고한다. 모르면 비워 두고, 비어 있으면 "광고 없음"이다(옵션 없음이 아니다). Hermes는 v1에서 비어 있다.

Hermes: `usage: true`(G1 F6), `resume: true`(`session/load`), `brief_transport: "instruction_file"`.

이 객체는 **런타임 하나**를 설명한다. 머신 전체의 속성(예: colab CLI 설치 여부)은 여기가 아니라 probe 최상위에 있다 — `daemon-protocol.md` §3 `colab_cli`.

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

## 12. 미결 — 스파이크 1b로 닫힘 (`plan/spikes/SPIKE_01b.md`)

| # | 질문 | 결과 | 계약 위치 |
|---|---|---|---|
| 1 | 0.74.0 모델 선택 방법 | `session/set_config_option {configId:"model"}` — **new 뒤와 모든 load 뒤에.** `set_model` 없음. `options.model` 경로는 `currentValue`가 거짓말을 하므로 쓰지 않는다 | §1, §2 |
| 2 | `_meta.systemPrompt`가 load에서도 필요한가 | **필요.** 세션에 저장되지 않는다. 깨끗한 이력에서 load 시 적용 1/1 | §3 |
| 3 | `settingSources: []` + `strictMcpConfig`로 사용자 MCP·hooks가 빠지는가 | **둘 다 있어야** 전부 빠진다. `settingSources`만으로는 claude.ai 커넥터가 남는다 | §3 |
| 4 | `permissions.deny`를 파일 없이 넘겨 서브에이전트까지 막을 수 있는가 | **된다.** `options.settings.permissions.deny`가 Task 안 Bash까지 거부 1/1 | §3 |
| + | 한도 오류 신호 | `_claude/rateLimit` 구조화 신호가 1차, `errorKind` 2차, 접두어 12개 3차 | §7, §8 |

P1 계약 테스트 추가 항목: (a) load 후 첫 턴에 브리프 식별자 질의(브리프 유지), (b) load 후 `model_usage`가 프로파일 모델(모델 재호출), (c) 원시 `system/init.tools`에 `mcp__` 0·`hook_started` 0(격리), (d) `CLAUDE_CONFIG_DIR` 분리·플러그인 격리는 macOS keychain 문제로 미시험 — P1에서.
