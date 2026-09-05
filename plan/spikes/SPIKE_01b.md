# 스파이크 1b — `@agentclientprotocol/claude-agent-acp@0.74.0` 재확인 + 데몬 세션 격리

| 항목 | 내용 |
|---|---|
| 근거 | PLAN P0-b 스파이크 1b(반나절 상자). SPIKE_01 §4·§6-4(모델 선택·`_meta.systemPrompt` 재확인), SPIKE_02 §2-3(F2 격리·F5 서브에이전트 누수), SPIKE_01 §3(F3 한도 오류 분류) |
| 실행일 | 2026-09-05 KST 11:30~11:36 (실기 3회 실행, 총 haiku 21턴, 한도 미도달 — 5시간 창 사용률 0.59→0.63) |
| 어댑터 | **`@agentclientprotocol/claude-agent-acp` 0.74.0** (`npx -y …@0.74.0`) — deps `@anthropic-ai/claude-agent-sdk` 0.3.257, `@agentclientprotocol/sdk` 1.4.0. `agentInfo.name` = `@agentclientprotocol/claude-agent-acp`, `protocolVersion` 1 |
| 런타임 | Claude Code CLI 2.1.258, 로그인 claude.ai Max(OAuth), 모델 haiku(`claude-haiku-4-5-20251001`) |
| 도구 | `daemon/cmd/acpprobe -scenario spike1b` (E1~E4) · `-scenario spike1b-load` (E2 보강). 소스 확인은 `node_modules/@agentclientprotocol/claude-agent-acp/dist/acp-agent.js` 등 (줄 번호는 0.74.0 dist 기준) |
| 원시 로그 | `plan/spikes/logs/spike1b_claude_20260905T023003Z.{jsonl,summary.json,stderr.txt}` (E1~E4), `spike1b-load_claude_20260905T023428Z.*` (E2b 1차), `spike1b-load_claude_20260905T023525Z.*` (E2b 2차, 깨끗한 이력 변형 포함) |

**총평: 5항목 모두 판정 완료, 0.74.0으로 프로파일을 고정해도 된다.** 다만 세 가지가 하네스 계약에 그대로 들어가야 한다 — (1) 모델은 `session/set_config_option`으로 고르고 **`session/load` 뒤에는 반드시 다시 건다**(리로드 시 기본 모델로 되돌아감), (2) `_meta.systemPrompt`는 `session/new`·`session/load` **양쪽에 매번** 넣는다, (3) 격리는 `settingSources: []` + `strictMcpConfig: true` 두 키가 모두 필요하다. 어댑터 소스 증거는 `session/update`의 `_meta["_claude/rateLimit"]`가 한도 상태를 **구조화해서 매 턴** 보내므로 F3의 문구 파싱은 보조 수단으로 내려간다.

## 1. 실험 방법 (공통)

- 클라이언트는 `fs`/`terminal`을 광고하지 않는다(SPIKE_01과 동일). 권한 정책은 `allow_once` 우선. 실험 디렉토리는 실험별 빈 하위 디렉토리(`e1/`·`e2/`·`e3/`·`e4/`·`e2b/`), `.claude/`·`CLAUDE.md` 없음.
- **판정 근거는 모델의 자기 보고가 아니라 와이어다.** 모델은 `session/prompt` 응답 `_meta.quota.model_usage[].model`(SDK `result.modelUsage`, 서브에이전트 포함 회계용 값)로, 툴·MCP·hooks는 `_meta.claudeCode.emitRawSDKMessages: true`로 켠 원시 SDK 스트림(`_claude/sdkMessage` 확장 알림)의 `system/init`(`tools`, `mcp_servers`, `skills`, `agents`)과 `system/hook_started`로, 서브에이전트 차단은 같은 스트림의 `tool_use`/`tool_result`(`parent_tool_use_id`, `is_error`)로 판정했다.
- probe 변경(이 PR): `session/set_config_option`·`configOptions` 파싱, `OnNotification`(확장 알림 캡처), `RPCError.Data`→`errorKind` 파싱, `TurnResult.Models`, 기본 런타임을 0.74.0 고정 패키지로(`ClaudeAdapterPkg`), 구 패키지는 `-runtime claude-zed`.

## 2. E1 — 모델 선택 (F: `session/new` 응답 `models` null)

**판정: 통과 — `session/set_config_option {configId:"model", value:"haiku"}`로 선택되고 `_meta.quota.model_usage`로 haiku가 확인됐다. 단 `session/load` 뒤에는 기본 모델(`claude-fable-5-1`)로 되돌아가므로 매 load 후 재호출이 필수.**

| 단계 | 요청 | 응답/증거 |
|---|---|---|
| `session/new` (모델 지정 없음) | `_meta` 없음 | `models` 필드 **없음**(`new_models_field_present: false`). 대신 `configOptions[id="model"]`: `currentValue: "claude-fable-5-1[1m]"`, `options: default · opus[1m] · claude-fable-5-1[1m] · sonnet · haiku` |
| `session/set_config_option` | `{sessionId, configId:"model", value:"haiku"}` | `configOptions.model.currentValue: "haiku"`. 어댑터는 값이 옵션에 없으면 `resolveModelPreference`로 별칭을 해석(`acp-agent.js:4353-4433`) |
| 턴 1 (PONG) | — | `_meta.quota.model_usage: [claude-haiku-4-5-20251001]`, 원시 `system/init.model`도 haiku |
| 프로세스 kill → 새 프로세스 `session/load` | `_meta` 없음 | 응답 `configOptions.model.currentValue: "claude-fable-5-1[1m]"` ← **되돌아감** |
| 턴 2 (PONG) | — | `model_usage: [claude-fable-5-1]` — 실제로 기본 모델로 실행됨 |
| `set_config_option` 재호출 → 턴 3 | 위와 동일 | `currentValue: "haiku"`, `model_usage: [claude-haiku-4-5-20251001]` |
| 대안: `session/new` + `_meta.claudeCode.options.model: "haiku"` | SDK 옵션 통과(`acp-agent.js:5548 ...userProvidedOptions`) | 실행은 haiku(`model_usage`)이나 **응답 `configOptions.model.currentValue`는 `claude-fable-5-1[1m]`로 잘못 보고**. load 뒤에는 이 방식도 되돌아감(SPIKE_01 비교 실행 `newpkg_spike1_…` 로그: new 후 3턴 haiku → load 후 11턴 fable) |

원인(소스): `getAvailableModels`(`acp-agent.js:6821-`)는 우선순위 `ANTHROPIC_MODEL` env → settings.model → **resume 세션의 라이브 모델(`readResumedLiveModel`, `query.getContextUsage()`)** → `models[0]`. resume 시 CLI가 복원한 라이브 모델이 기본 모델이므로 어댑터는 그것을 "진실"로 보고하고 setModel을 걸지 않는다(어댑터 결함이 아니라 CLI 2.1.258이 SDK `setModel`로 바꾼 모델을 transcript에 남기지 않는 것으로 보임 — 어느 쪽이든 하네스는 재호출로 대응).

**계약 값:**
- `session/new` 뒤: `session/set_config_option {"sessionId", "configId":"model", "value":"<alias|modelId>"}` — 응답 `configOptions[id="model"].currentValue`가 원하는 값인지 확인.
- `session/load` 뒤: **무조건 같은 호출을 다시** 한다. 응답의 `currentValue`만 믿고 건너뛰지 말 것(`options.model` 경로는 값이 거짓말을 한다).
- 관측: 턴마다 `_meta.quota.model_usage[].model`을 `task_event`에 싣고, 프로파일 모델과 다르면 경고(드리프트 감지).
- `session/set_model`은 0.74.0에 없다(SPIKE_01 probe가 `models: null`이라 호출을 건너뛴 이유). `session/new` 응답 스키마는 `{sessionId, modes, configOptions}`.

## 3. E2 — `_meta.systemPrompt {append}` 유지 여부

**판정: 통과 — `session/new`에서 3/3 동작. `session/load` 시에도 `_meta.systemPrompt`를 넣어야 유지된다(넣지 않으면 사라짐). 넣으면 깨끗한 이력에서도 적용됨(1/1).**

| 실험 | `_meta` | 응답 (질문: 이름·비밀어 두 줄) |
|---|---|---|
| new ×3 | `{"systemPrompt":{"append":"<brief>"}}` | `Zorblax-7 / pomegranate` ×3 (`passed: 3`) |
| 3번 세션 kill → load **without** meta → 질문 | 없음 | `Claude / I don't have a secret word.` — 이력에 Zorblax 답이 있어도 **사라짐** |
| 같은 세션 다시 load **with** meta → 질문 | append | `Claude / I don't have a secret word.` — 실패처럼 보이나 **이력 오염**: 직전 턴(위 행)의 "Claude" 답이 이력 마지막에 있어 haiku가 그것을 따랐다 |
| E2b(A) 깨끗한 이력: new **without** meta → PONG → kill → load **with** meta → 질문 | append (load에만) | **`Zorblax-7 / pomegranate`** — load 시점의 `_meta.systemPrompt`가 실제 적용됨 (`spike1b-load_…T023525Z` `clean_new_without_meta_then_load_with_meta.load_ok: true`) |
| E2b(B) new with meta → 질문 → kill → load with meta → "직전 답을 그대로 반복" → 질문 | append | 이력 복원 OK(반복 성공) + `Zorblax-7 / pomegranate`. 문자열(대체) 형태로 load해도 동일 |

소스: `createSession`이 `session/new`·`session/load` 모두에서 `params._meta.systemPrompt`를 읽어 SDK `systemPrompt`를 만든다(`acp-agent.js:5421-5437`, load 경로 `getOrCreateSession` → `createSession({... _meta: params._meta}, {resume})` `:5320-5334`). 세션에 저장되지 않으므로 **load마다 다시 보내야 한다**. 객체 형태는 `{...customPrompt, type:"preset", preset:"claude_code"}`로 잠기므로 `append` 외 SDK 프리셋 옵션(`excludeDynamicSections` 등)도 그대로 통과.

**계약 값:** `session/new`와 `session/load` 양쪽 `params._meta.systemPrompt = {"append": "<brief [1]~[8]>"}`. 문자열 형태(기본 프롬프트 대체)는 쓰지 않는다(SPIKE_03). E12-09(중복 주입 0)는 유지되나, "브리프 없는 턴이 한 번이라도 끼면 이후 턴이 그 답을 따라간다"는 이력 오염을 봤으므로 **resume 경로에서 브리프를 빠뜨리는 것은 곧 회귀**로 취급(P1 계약 테스트: load 후 첫 턴에 브리프 식별자 질의).

## 4. E3 — 격리 (F2: 사용자 전역 MCP·hooks·스킬이 세션에 실림)

**판정: 통과 — `_meta.claudeCode.options = {"settingSources": [], "strictMcpConfig": true}`로 사용자 MCP 툴·MCP 서버·hooks·사용자 스킬이 모두 빠진다. `settingSources`만으로는 claude.ai 원격 커넥터가 남는다.**

세 세션(모두 haiku, 빈 workdir, 질문 "보유 툴을 한 줄에 하나씩"):

| 변형 | `options` | `system/init` 증거 | 에이전트 답변 |
|---|---|---|---|
| a 대조군 | 없음 (어댑터 기본 `settingSources: ["user","project","local"]`, `acp-agent.js:5545`) | tools 37 — **`mcp__pencil__*` 5개 포함**; `mcp_servers`: stitch(pending)·pencil(connected)·claude.ai Google Drive/Calendar/Gmail(needs-auth); `hook_started` **`SessionStart:startup` 1회**(사용자 `~/.claude/settings.json`의 Orca hook); skills 34(사용자 17 + 내장 17); slash 67 | 툴 목록에 `mcp__pencil__browser` 등 5개 **있음** |
| b `settingSources: ["project"]` | strictMcpConfig 없음 | tools 32, **mcp__ 0**; `mcp_servers`: claude.ai Google Drive/Calendar/Gmail **3개 남음**(계정 원격 커넥터, `~/.claude.json`의 pencil/stitch는 빠짐); hooks **0**; skills 17(내장만); slash 50 | mcp 없음 |
| c `settingSources: [], strictMcpConfig: true` | — | tools 32, mcp__ 0; **`mcp_servers: []`**; hooks 0; skills 17; slash 50 | mcp 없음 |

- `settingSources`(SDK 옵션, `sdk.d.ts:2061` "`[]` = SDK isolation mode")는 `~/.claude/settings.json`(hooks·permissions)·`~/.claude.json`의 사용자 MCP·`~/.claude/skills`를 끊는다. **`project`를 넣어야 CLAUDE.md가 읽힌다**(SDK 주석) — 브리프를 지시 파일로 줄 프로파일은 `["project"]`, `_meta.systemPrompt`로 줄 Claude Code 프로파일은 `[]`도 가능. 단 `[]`이면 workdir `.claude/settings.json`도 안 읽으므로 워크트리 오염 걱정이 없다.
- `strictMcpConfig`(`sdk.d.ts:2110`, CLI `--strict-mcp-config`)는 `mcpServers` 옵션으로 넘긴 것 외 모든 MCP(프로젝트 `.mcp.json`, 사용자, 플러그인, 서브에이전트 frontmatter MCP)를 끊는다. b에서 남은 claude.ai 커넥터가 c에서 사라진 것이 이것.
- 남는 것: 내장 스킬 17·내장 슬래시 50·에이전트 정의(`claude`, `Explore`, `general-purpose`, `Plan`, `statusline-setup` — `~/.claude/agents`는 없으므로 내장/플러그인)는 세 변형 모두 동일. 사용자 플러그인 격리는 이번에 대상이 없어 미확인(`CLAUDE_CONFIG_DIR` 분리는 macOS keychain 자격증명 문제가 있어 시험하지 않음 — P1 항목).

**계약 값 (Claude Code 프로파일 `session/new`·`session/load` 공통):**
```json
"_meta": {
  "systemPrompt": {"append": "<brief>"},
  "claudeCode": {
    "options": {
      "settingSources": [],
      "strictMcpConfig": true,
      "disallowedTools": ["AskUserQuestion", "..."],
      "settings": {"permissions": {"deny": ["..."]}}
    }
  }
}
```
(`mcpServers`는 ACP 표준 파라미터로 따로. 데몬이 붙일 MCP가 있으면 `params.mcpServers`에 넣는다 — `strictMcpConfig`는 그것만 허용.)

## 5. E4 — 서브에이전트 차단 (F5: `disallowedTools`가 Task 서브에이전트에 안 퍼짐)

**판정: 통과 — `_meta.claudeCode.options.settings = {"permissions":{"deny":["Bash(date:*)","Bash(date)"]}}`(파일 없음, SDK `--settings` 인라인 계층)가 Task 서브에이전트 안의 Bash까지 막는다. 1/1.**

프롬프트: "Task로 general-purpose 서브에이전트 하나를 띄워 `date`를 Bash로 실행해 결과를 그대로 보고하라. 막히면 `BLOCKED: `+오류 원문."

| 증거 | 값 |
|---|---|
| 원시 `tool_use` | `ToolSearch` → `Agent{subagent_type:"general-purpose"}` → **`Bash{command:"date"}` with `parent_tool_use_id` = Agent 호출 id** (서브에이전트 안) |
| 원시 `tool_result` (서브에이전트) | `is_error: true`, **"Permission to use Bash with command date has been denied."** |
| 주 에이전트 | Bash 직접 호출 0, `session/request_permission` 0건 (deny가 권한 협상 앞에서 끊음 — SPIKE_02 (b)와 동일 형태) |
| 최종 답 | `BLOCKED: Permission to use Bash with command `date` has been denied.` |
| 모델 | 주·서브 모두 haiku (`model_usage`) |

소스: `userProvidedOptions.settings`가 SDK `settings`(문자열 경로 또는 객체, `sdk.d.ts:2010-2026`, "flag settings" 계층 = CLI `--settings`)로 그대로 전달(`acp-agent.js:5504-5522, 5549`). 서브에이전트는 같은 CLI 프로세스의 permission 계층을 쓰므로 deny가 적용된다. **`disallowedTools`(툴 목록 제거)는 서브에이전트에 안 퍼지고(SPIKE_02 c), `settings.permissions.deny`(권한 규칙)는 퍼진다** — 둘의 역할이 다르다.

부수 발견: 0.74.0은 서브에이전트의 툴 호출을 ACP `tool_call`로 올리지 않는다(이번 실행의 ACP `tool_call`은 `ToolSearch`·`Task` 2건뿐; 0.16.2는 SPIKE_02에서 "Run date command"가 올라왔음). 서브에이전트 내부를 피드에 보이려면 클라이언트가 `clientCapabilities._meta["subagent-transcript"]: true`(또는 `options.forwardSubagentText: true`)를 광고해야 한다(`acp-agent.js:218-221, 5444-5446`). v1 피드 요구사항에 따라 결정.

**계약 값:** `_meta.claudeCode.options.settings.permissions.deny: [<rule>...]` — 파일을 쓰지 않는다. `disallowedTools`는 "모델 툴 목록에서 제거"(주 에이전트 UX용)로 병행. `AskUserQuestion`은 어댑터가 form-elicitation 미광고 시 자동 제거(`:5461`), 우리 클라이언트는 광고하지 않으므로 여전히 부재.

## 6. E5 — 한도 오류 분류 (F3: `-32603` 문구 파싱), 소스 확인만

**판정: 통과(조건부) — 0.74.0에서도 클라이언트가 AIR 세션-실패 능력을 광고하지 않는 한 `-32603 "Internal error: <원문>"`으로 오고 원문은 SDK `USAGE_LIMIT_ERROR_PREFIXES` 12개 접두어 중 하나로 시작한다. probe 정규식이 그중 5개를 못 잡던 것을 고쳤고, 더 좋은 신호(`error.data.errorKind`, `_meta["_claude/rateLimit"]`)가 있으므로 하네스는 그것을 1차로 쓴다.**

경로(소스):
1. SDK가 한도에 걸리면 합성 assistant 메시지를 보내고 텍스트가 `USAGE_LIMIT_ERROR_PREFIXES`(`claude-agent-sdk/sdk.d.ts:8559`)로 시작: `"You've hit your"`, `"You've reached your"`, `"You're out of usage credits"`, `"Your org is out of usage · add funds to continue"`, `"Your org is out of usage · contact your admin"`, `"Your seat type doesn't include usage credits"`, `"Your seat type doesn't include usage"`, `"Your usage allocation has been disabled by your admin"`, `"Your group's usage limit is set to $0"`, `"Fable 5 requires usage credits"`, `"You're out of extra usage"`, `"Your seat type doesn't include extra usage"`. 어댑터는 `isSyntheticUsageLimitMessage`(`session-failure-extension.js:112-121`)로 인식.
2. `result`가 `is_error`면 `failActiveWithSessionFailure(providerFailureCategory(lastAssistantError, wasUsageLimit), internalErrorForClient(errorKindData(lastAssistantError), message.result), …)`(`acp-agent.js:3310, 3346`). `internalErrorForClient`(`:1640`)는 **클라이언트가 AIR `sessionFailure`를 광고하지 않으면** `RequestError.internalError(data, rawDetail)` → JSON-RPC `{code:-32603, message:"Internal error: <message.result>", data:{errorKind}}`(`@agentclientprotocol/sdk/dist/jsonrpc.js:1020`). SPIKE_01에서 본 `Internal error: You've hit your limit · resets 11am (Asia/Seoul)` 형태 그대로. `data.errorKind`는 SDK `SDKAssistantMessageError`(`rate_limit | overloaded | authentication_failed | billing_error | account_on_hold | invalid_request | model_not_found | server_error | max_output_tokens | unknown`)로, API 429는 `rate_limit`, 합성 사용량 한도 메시지는 errorKind 없이 문구만 올 수 있다.
3. 분류 정책(어댑터 자체 기준, `session-failure-extension.js:320-345`): 사용량 한도 → `quota_exhausted`(category `limit`, actions `[]`), `rate_limit` → `rate_limited`(actions `["retry"]`), `overloaded` → 재시도.
4. **더 나은 신호 — 매 턴 구조화 상태**: SDK `rate_limit_event`를 어댑터가 `session/update {sessionUpdate:"usage_update", used, size, _meta:{"_claude/rateLimit": SDKRateLimitInfo}}`로 올린다(`acp-agent.js:3878-3890`). 이번 실행에서 12회 수신: `{"status":"allowed","resetsAt":1788591000,"rateLimitType":"five_hour","utilization":0.59~0.63,"unifiedWindows":{"five_hour":{...}}}`. 타입(`sdk.d.ts:4855-4873`): `status: allowed | allowed_warning | rejected`, `resetsAt`(epoch초), `rateLimitType: five_hour | seven_day | …`, `overageStatus`, `errorCode?: 'credits_required'`. **하네스는 `status: "rejected"` + `resetsAt`으로 `rate_limited`와 리셋 시각을 정규식 없이 얻는다.**
5. 대안: `clientCapabilities._meta.air = {"version": 1, "capabilities": ["sessionFailure"]}`를 광고하면 오류 대신 `stopReason: "end_turn"` + `_meta.air.sessionFailure {kind, category, actions, title}`가 온다(`air-extension.js:3-8, 48-58`). 오류 코드가 사라지므로 SPIKE_01 규격(`-32603`)과 양립하지 않는다 — v1은 광고하지 않는다.

probe 변경: `rateLimitRe`에 `you've (hit|reached) your|usage credits|usage allocation|doesn't include (extra )?usage` 추가, `isLimitError`가 `errors.As → RPCError.ErrorKind()=="rate_limit"`도 본다. 단위 테스트 `TestRateLimitRegexCoversSDKPrefixes`(12개 접두어 + 관측 문구 + 리셋 시각 파싱 `11am (Asia/Seoul)`)·`TestIsLimitErrorUsesErrorKindData`.

**계약 값 (하네스 `rate_limited` 분류, 우선순위):**
1. 직전 `usage_update._meta["_claude/rateLimit"].status == "rejected"` → `rate_limited`, `reset_at = resetsAt`(epoch).
2. `session/prompt` 오류 `code == -32603` 이고 `data.errorKind == "rate_limit" | "overloaded"` → `rate_limited`(재시도), `resetsAt`은 1번 값.
3. 오류 메시지가 `USAGE_LIMIT_ERROR_PREFIXES` 중 하나로 시작 → `rate_limited`(리셋 시각 있으면 파싱, 없으면 `quota_exhausted`로 사람 에스컬레이션). 12개 접두어를 계약 상수로 복사하고 어댑터 버전 고정과 함께 관리.
4. 그 외 `-32603` → `agent_error`. 프로세스는 살아 있으므로 세션은 유지(SPIKE_01 §3).

## 7. 그 외 관측

- 크래시 0, `UnexpectedExit` 0. stderr의 `Claude Code process exited with code 143`·`cancellation failed during teardown`은 probe의 계획된 SIGTERM(retire) 뒤 어댑터 정리 로그로 결함 아님.
- `session/load` 리플레이는 이번에도 `user_message_chunk`/`agent_message_chunk`로 온다(2~4청크) — SPIKE_01 권고대로 `task_event`에 올리지 않고 버린다.
- `emitRawSDKMessages: true`는 스트리밍 부분 메시지까지 모두 올려 세션당 원시 알림 32~45건. 데몬은 켜지 않는다(필터 배열 `[{type:"system",subtype:"init"}]` 형태는 지원되나 필요 시에만).
- `session/new`가 `_meta.claudeCode.options`의 키를 **검증 없이** SDK로 통과시킨다(`...userProvidedOptions`). 오타는 조용히 무시되므로 계약 테스트에서 효과(툴 목록·hook 0)를 확인해야 한다.

## 8. 권고 (P0-b 하네스 계약에 넣을 것)

1. 프로파일 고정: `@agentclientprotocol/claude-agent-acp@0.74.0`, 프로토콜 1. probe 기본값도 이 값(`ClaudeAdapterPkg`).
2. 모델: `session/set_config_option{model}`을 `session/new` 뒤와 **모든 `session/load` 뒤**에 호출. `_meta.quota.model_usage`로 드리프트 감시.
3. 브리프: `_meta.systemPrompt.append`를 `session/new`·`session/load` 양쪽에 매번.
4. 격리: `_meta.claudeCode.options.settingSources: []`(지시 파일 프로파일은 `["project"]`) + `strictMcpConfig: true`. 계약 테스트는 `system/init.tools`에 `mcp__` 0, `hook_started` 0으로 확인(원시 스트림 필터 켜서).
5. 툴 차단: 서브에이전트까지 강제할 규칙은 `_meta.claudeCode.options.settings.permissions.deny`, 모델 UX용 제거는 `disallowedTools`. workdir `.claude/settings.json`은 쓰지 않는다.
6. 한도: §6 우선순위대로. `_claude/rateLimit`을 `task_event`(또는 세션 상태)에 실어 리셋 시각을 미리 보여준다.
7. 서브에이전트 가시성(`subagent-transcript` 능력 광고 여부)은 v1 피드 요구사항으로 결정 — 기본은 광고하지 않음.
