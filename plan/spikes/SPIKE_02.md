# 스파이크 2 — ACP 경로에서 `AskUserQuestion` 차단 수단

| 항목 | 내용 |
|---|---|
| 근거 | PLAN.md §4 #2, PRD.md §8.2.5(Claude Code 헤드리스 `AskUserQuestion` 빈 답), §12 오픈 이슈 2(M11), EVAL.md E12 |
| 실행일 | 2026-09-05 (KST 09:14) |
| 어댑터 | `@zed-industries/claude-code-acp` 0.16.2 (claude-agent-sdk 0.2.44) · Claude Code CLI 2.1.258 · 모델 haiku |
| 도구 | `daemon/cmd/acpprobe -scenario spike2` |
| 원시 로그 | `plan/spikes/logs/spike2_claude_20260905T001358Z.{jsonl,summary.json}` |

**판정: 통과** — 설정 파일(`permissions.deny`)로 차단한 툴 호출은 에이전트에게 오류로 돌아온다. 어댑터는 `AskUserQuestion`을 기본으로 툴 목록에서 제거하므로 호출 자체가 불가능하다. `tool_disallow: true`.

## 1. 수단별 결과

네 실험 모두 새 프로세스·새 세션에서 1턴. 프롬프트는 (a) "반드시 AskUserQuestion을 호출하라, 못 하면 `TOOL_UNAVAILABLE`과 보유 툴 목록을 말하라", (b)(c)(d) "Bash로 `date`를 실행하고 결과를 그대로 답하라. 실패하면 `BLOCKED:` + 받은 오류 원문".

| # | 수단 | 결과 | 에이전트에게 무엇이 돌아왔나 |
|---|---|---|---|
| a | **어댑터 기본값** — 소스 `acp-agent.js:836` `disallowedTools = ["AskUserQuestion"]` 하드코딩 | `tool_call` 0건, 권한 요청 0건, `end_turn` | "I don't have access to an AskUserQuestion tool" + 보유 툴 목록(Task, Bash, Glob, Grep, Read, Edit, Write, WebFetch, TodoWrite, Skill, mcp__* …). **툴이 모델의 툴 목록에서 빠져** 호출이 일어나지 않는다 — 오류가 아니라 부재 |
| b | **설정 파일** — workdir `.claude/settings.json` `{"permissions":{"deny":["Bash(date:*)","Bash(date)"]}}` | `tool_call` → `tool_call_update status=failed`, 권한 요청 0건 | **"BLOCKED: Permission to use Bash with command date has been denied."** — 거부가 툴 결과(오류)로 에이전트에게 전달됨. `session/request_permission`은 오지 않음(deny가 권한 협상 앞에서 끊음) |
| c | **`_meta.claudeCode.options.disallowedTools: ["Bash"]`** (어댑터가 `_meta.claudeCode.options`를 SDK 옵션으로 그대로 통과, `acp-agent.js:770`) | 주 에이전트에서 Bash 제거됨. 그러나 에이전트가 **`Task` 서브에이전트를 띄워 그 안에서 `date` 실행 성공** (`Run date command` tool_call, 결과 "Sat Sep 5 20:23:41 UTC 2026") | 오류 없음 — **서브에이전트에는 disallowedTools가 전파되지 않는 누수** |
| d | 대조군(차단 없음) | `date` 실행, 권한 요청 0건 | "Sat Sep 5 09:14:35 KST 2026" — 읽기 전용 셸은 Claude Code가 자동 승인 |
| — | **환경변수** | 해당 없음 | `claude --help`·공식 CLI 레퍼런스에 툴 차단용 환경변수 없음. `--disallowedTools` 플래그만 존재(CLI 폴백 경로용) |

## 2. 해석

- PRD §8.2.5의 함정(헤드리스에서 빈 답 반환)은 ACP 경로에서 **어댑터 기본값만으로 이미 막혀 있다**. 별도 조치 없이도 `AskUserQuestion` 호출은 발생하지 않는다.
- "차단된 호출이 오류로 돌아오는가"(§4 통과 기준)는 수단 (b)로 확인: deny 규칙은 툴 결과 오류로 돌아온다. 수단 (a)(c)는 오류가 아니라 **툴 부재**로 동작한다 — 에이전트가 "그 툴이 없다"고 인지하므로 빈 답 문제는 없다.
- (c)의 서브에이전트 누수는 `AskUserQuestion`에는 해당하지 않는다 — 어댑터가 서브에이전트에도 같은 SDK 옵션을 쓰는지는 확인하지 못했고, `AskUserQuestion`은 서브에이전트가 부를 이유가 없는 툴이다. 그러나 **"어떤 툴을 막았다"를 서브에이전트까지 보장하려면 deny 규칙(b)이 필요**하다.
- 부수 발견: 어댑터가 `settingSources: ["user","project","local"]`로 사용자 전역 설정을 읽어 **사용자의 MCP 서버(mcp__pencil, mcp__stitch …)와 hooks가 세션에 실렸다**. 데몬 세션은 `_meta.claudeCode.options.strictMcpConfig` 또는 `settingSources` 제한으로 격리해야 한다(P0-b 하네스 인터페이스에 반영).

## 3. 권고 (P0-b 하네스 인터페이스)

1. `tool_disallow: true`. 1차 수단은 **`_meta.claudeCode.options.disallowedTools`** — 파일을 쓰지 않아 워크트리 오염(§8.4 M6)이 없다. 어댑터 기본 차단과 중복되지만 어댑터 버전이 바뀌어도 우리 쪽에서 보장된다.
2. 서브에이전트까지 강제해야 하는 툴은 `permissions.deny`를 **`--settings` 인라인 JSON에 해당하는 SDK 옵션**(`_meta.claudeCode.options.settingSources` + `permissions`)으로 전달할 수 있는지 P0-b에서 확인. 확인 전까지는 workdir `.claude/settings.json`을 쓰지 않는다(오염).
3. 브리프 "쓰지 마라" + 피드 경고 경로(실패 분기)는 **불필요**.
