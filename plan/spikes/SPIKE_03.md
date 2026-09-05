# 스파이크 3 — `session/new`에 시스템 프롬프트를 넣을 수 있는가

| 항목 | 내용 |
|---|---|
| 근거 | PLAN.md §4 #3, PRD.md §8.4 전달 경로(M6) 1번, §12 오픈 이슈 3, EVAL.md E12-09 |
| 실행일 | 2026-09-05 (KST 09:14) |
| 어댑터 | `@zed-industries/claude-code-acp` 0.16.2 · Claude Code CLI 2.1.258 · 모델 haiku · Hermes 0.20.6은 소스 확인만 |
| 도구 | `daemon/cmd/acpprobe -scenario spike3` |
| 원시 로그 | `plan/spikes/logs/spike3_claude_20260905T001436Z.{jsonl,summary.json}` |

**판정: 통과(Claude Code 한정)** — ACP 표준 스키마에는 필드가 없고, `claude-code-acp`가 `_meta.systemPrompt`로 받는다. 브리프 없이 주입한 이름·비밀어를 에이전트가 정확히 답했다. Hermes는 필드가 없어 지시 파일이 유일 경로.

## 1. 스키마·소스 확인

| 대상 | 시스템 프롬프트 필드 | 근거 |
|---|---|---|
| ACP 스펙 `session/new` (schema v0.11.x / SDK 0.14.1) | **없음**. 파라미터는 `cwd`, `mcpServers`, `_meta`뿐 | `acp/schema.py:1645 NewSessionRequest` — `field_meta`, `cwd`, `mcp_servers` |
| ACP 스펙 `initialize` | 없음 | — |
| `claude-code-acp` 0.16.2 | **`_meta.systemPrompt`** — 문자열이면 기본 프리셋을 **대체**, `{append: "…"}`면 `claude_code` 프리셋 뒤에 **추가** | `acp-agent.js:756-767` |
| `claude-code-acp` 0.16.2 (우회) | `_meta.claudeCode.options`가 SDK 옵션으로 통과되므로 `options.systemPrompt`도 이론상 가능 — 단 어댑터가 `systemPrompt`를 먼저 계산해 `...userProvidedOptions`로 덮이는 순서라 실험하지 않음 | `acp-agent.js:770-788` |
| `hermes acp` 0.20.6 | **없음**. `new_session(cwd, mcp_servers, **kwargs)`가 `_meta`를 버린다. 시스템 프롬프트는 Hermes가 `SOUL.md`·`AGENTS.md`·자체 프롬프트 빌더로 구성 | `acp_adapter/server.py:1591-1610` |

## 2. 주입 실험 (Claude Code)

브리프 파일(CLAUDE.md) 없는 빈 디렉토리. 주입 문구: "Your name is Zorblax-7 … secret word 'pomegranate'". 프롬프트: "이름과 비밀어를 두 줄로".

| 실험 | `_meta` | 응답 |
|---|---|---|
| 대조군 | 없음 | `Claude` / `I don't have a secret word.` |
| 문자열(대체) | `{"systemPrompt": "<brief>"}` | **`Zorblax-7` / `pomegranate`** |
| 추가 | `{"systemPrompt": {"append": "<brief>"}}` | **`Zorblax-7` / `pomegranate`** |

3/3 예상대로. 대체 모드는 Claude Code 기본 시스템 프롬프트(툴 사용 규약 등)를 잃으므로 **`append`를 쓴다**.

## 3. 권고

1. PRD §8.4 전달 경로 1번은 **Claude Code(ACP)에서 유효** — `_meta.systemPrompt.append`로 브리프 [1]~[8]을 넣을 수 있다. 어댑터 전용 확장이므로 하네스 프로파일에 `brief_transport: "acp_meta_system_prompt" | "instruction_file"`로 두고, Claude Code 프로파일만 전자.
2. **Hermes는 지시 파일(`AGENTS.md`)이 유일 경로** — §8.4 2번 기본 가정 유지.
3. E12-09(경로 하나만, 중복 주입 0)는 프로파일 값으로 강제한다. 단, `_meta.systemPrompt`는 `session/new`·`session/load` 양쪽에 넣어야 resume 뒤에도 유지되는지 **P1 계약 테스트 항목**으로 남긴다(이번엔 `session/new`만 실험).
4. 어댑터 버전 드리프트 주의: 이 필드는 스펙이 아니라 어댑터 구현이다. 프로파일에 어댑터 버전을 고정하고(스파이크 1 권고와 동일) probe에서 `_meta.systemPrompt` 지원 여부를 한 턴으로 검증한다.
