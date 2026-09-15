# G1 판정 — 스파이크 1·2·3·4a·6

| 항목 | 내용 |
|---|---|
| 게이트 | PLAN.md §6.2 **G1** (P0-a → P0-b) |
| 근거 | `plan/spikes/SPIKE_01.md` `SPIKE_02.md` `SPIKE_03.md` `SPIKE_04a.md` `SPIKE_06.md` (PR #10), 원시 로그 `plan/spikes/logs/` |
| 작성 | Lead 초안, 2026-09-05 |
| 상태 | **Director 확인 대기.** PLAN §10.1에 따라 게이트 판정은 Director의 것이다. Director가 "쭉 마무리해라"로 위임했으므로 이 초안으로 P0-b를 **시작**하되, Director가 뒤집으면 P0-b 계약을 그에 맞춰 고친다 — 계약은 문서와 타입이라 되돌리는 비용이 작다 |

## 판정

| # | 스파이크 | 판정 | 결정 |
|---|---|---|---|
| 1 | `claude-code-acp` 성숙도 | **통과** — 30턴 크래시 0, resume 11/11, 취소 10/10, 권한 26건 `allow_once` 부재 0%, 프로토콜 v1 고정 | **CLI 어댑터를 v1에 넣지 않는다. 컷 1 예약 없음.** PRD §10 조건부 항목 → 제외 확정 |
| 2 | ACP 툴 차단 | **통과** — 어댑터가 `AskUserQuestion`을 기본 제거, `permissions.deny`는 에이전트에 오류로 반환 | `tool_disallow: true`. 1차 수단 `_meta.claudeCode.options.disallowedTools`(파일 안 씀 → 워크트리 오염 0). 브리프 "쓰지 마라" 경로 불필요 |
| 3 | `session/new` 시스템 프롬프트 | **통과(Claude Code 한정)** — ACP 스펙에는 없고 어댑터 `_meta.systemPrompt`로 3/3 주입 | 프로파일 `brief_transport`: Claude Code = `acp_meta_system_prompt`(**append 모드**), Hermes = `instruction_file`. PRD §8.4 경로 1은 Claude Code에만 |
| 4a | 런타임 resume 능력 | **통과** — Claude Code `session/load` 후 컨텍스트 유지 10/10, Hermes state.db 삭제 후 provenance 불일치로 감지 3/3, 오탐 0 | 두 런타임 `resume: true`. Hermes는 `session/load` → 결과 `null`이면 유실, 아니면 `_meta.hermes.sessionProvenance.acpSessionId == 요청 id` 확인. `session/resume`(UNSTABLE) 안 씀 |
| 6 | Claude Code CLI 플래그 | **통과** — 세 플래그 실재·동작 | §8.2.4 폴백 명령줄 유지. probe는 `--help` 파싱이 아니라 버전 고정 + 파서 수용 테스트 |

**규모 축소**: PLAN §4의 100턴·resume 20·취소 20·권한 50을 30·11·10·26으로 줄였다(한도·비용). 통과 기준의 비율 지표(부재율·성공률)는 표본이 줄어도 판정이 뒤집힐 여지가 없을 만큼 여유가 크다(0%, 100%). 100턴 전체는 P1 야간 E2E에서 채운다.

## 계약(P0-b)에 들어갈 새 사실

스파이크가 PRD·PLAN에 없던 것을 찾았다. 전부 P0-b 하네스 인터페이스에 반영한다.

| # | 발견 | 반영 |
|---|---|---|
| F1 | **지시한 패키지 `@zed-industries/claude-code-acp`는 2026-02 이후 동결.** 후속 **`@agentclientprotocol/claude-agent-acp`** 0.74.0(2026-09-04)이 활발. 프로토콜 v1은 같고 우리 클라이언트가 수정 없이 둘 다 구동 | 프로파일 어댑터를 `@agentclientprotocol/claude-agent-acp@0.74.0`으로 고정. 새 패키지에서 `session/new` 응답 `models: null` → 모델 선택은 `session/set_config_option`으로 이동한 듯 — **P0-b 착수 시 반나절 재확인**(스파이크 1b) |
| F2 | **어댑터가 `settingSources: ["user","project","local"]`로 사용자 전역 설정을 읽어 사용자의 MCP 서버·hooks가 세션에 실렸다** | 데몬 세션은 `_meta.claudeCode.options.settingSources` 제한 + `strictMcpConfig`로 격리. 하네스 계약 필수 항목. 이것 없이는 에이전트가 Director 개인 도구를 쓴다 |
| F3 | 계정 한도는 `session/prompt` JSON-RPC 오류 `-32603 "You've hit your limit … resets 11am"`으로 오고 프로세스는 살아 있다 | 하네스 오류 분류에 **`rate_limited`**(리셋 시각 파싱, 재시도 가능) 추가. FR-7.1 재시도 표에 없던 분류 — PRD 후속 |
| F4 | `session/load` 리플레이 청크가 누적된다(11회 resume에 111청크) | 리플레이 청크는 `task_event`로 올리지 않고 버린다 |
| F5 | `disallowedTools`는 `Task` 서브에이전트에 전파되지 않는다 | 서브에이전트까지 막아야 하는 툴은 `permissions.deny`를 SDK 옵션으로 전달할 수 있는지 P0-b 확인. 확인 전엔 workdir `.claude/settings.json`을 쓰지 않는다(오염) |
| F6 | Hermes는 `session/load`도 제공하고 `usage_update`도 온다 | PLAN §7 가정 2 해소. Hermes `usage=true` |
| F7 | PRD §8.2.5 `stopReason=="refusal" && 활동 0` 규칙은 유실 시 발동하지 않았다(정상 `end_turn`) | 보조 규칙으로 강등. 1차는 provenance |
| F8 | `--max-budget-usd` 초과가 `subtype: "error_max_budget_usd"`로 구조화되어 온다 | CLI 폴백 경로의 `budget_exceeded` 매핑 — 지금은 v1 밖(스파이크 1 통과) |
| F9 | `--effort` 값 집합이 `--help`(low~max 5단계)와 문서(`ultracode` 포함)가 다르다 | 프로파일 편집기 effort 5단계, `ultracode` 제외 |

## 문서 후속 (이 판정과 같은 PR)

- PRD §8.2.3·§12 스파이크 1: 패키지 이름 → `@agentclientprotocol/claude-agent-acp`. §7.1 재시도 분류에 `rate_limited`. §8.2.5 refusal 규칙 → 보조. §10 조건부 항목(CLI 어댑터) → v1 제외 확정.
- PLAN §7 가정 1·2 → 해소. §6.2 G1 행 → 통과, 컷 1 예약 없음. §4 스파이크 1b(P0-b 재확인) 추가.

## P0-b 착수 조건

G2까지 만들 것(PLAN §3 P0-b): 하네스 인터페이스(위 F1~F7 반영), 데몬↔서버 프로토콜(claim·heartbeat·event push·토큰 폐기 서버→데몬·큐 인터페이스), `task_event` 스키마, OpenAPI 초안, `colab` CLI 계약, 주입 가능한 클럭. **스파이크 1b(반나절)** 를 P0-b 첫 작업으로.
