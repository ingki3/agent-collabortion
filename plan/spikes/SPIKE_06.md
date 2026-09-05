# 스파이크 6 — Claude Code CLI 플래그 실재 확인

| 항목 | 내용 |
|---|---|
| 근거 | PLAN.md §4 #6, PRD.md §8.2.4 CLI 폴백 스펙, §12 오픈 이슈 6, 부록 C |
| 실행일 | 2026-09-05 (KST 11:13) |
| 대상 | Claude Code CLI **2.1.258** (`claude --version`), 로그인 claude.ai(Max) |
| 방법 | ① `claude --help` 전문 저장 ② 파서 수용 테스트(`claude <flag> --bogus` → 첫 미지 옵션이 `--bogus`면 flag 수용) ③ 실제 `-p` 호출로 기능 확인(haiku) ④ 공식 CLI 레퍼런스(부록 C) 대조 |
| 원시 로그 | `plan/spikes/logs/spike6_claude-help_2.1.258.txt` |

**판정: 통과** — `--effort`, `--max-budget-usd`, `--append-system-prompt-file` 셋 다 실재하고 동작한다. PRD §8.2.4 폴백 명령줄은 그대로 유효. 부록 C 대조표는 아래.

## 1. 대조표

| 플래그 (PRD §8.2.4) | `--help` 2.1.258 | 파서 수용 | 기능 확인 | 공식 문서(부록 C CLI reference) |
|---|---|---|---|---|
| `--effort <level>` | ✓ `low, medium, high, xhigh, max` | ✓ | ✓ `--effort low` 정상 응답 | ✓ 문서는 `ultracode`도 추가로 나열 |
| `--max-budget-usd <amount>` | ✓ "(only works with --print)" | ✓ | ✓ `0.00001`로 주면 `is_error:true, subtype:"error_max_budget_usd"` — **예산 초과가 구조화된 오류로 옴** | ✓ |
| `--append-system-prompt-file` | **△ 목록에 별도 줄 없음** — `--bare` 설명문에 `--append-system-prompt[-file]`로만 언급 | ✓ | ✓ 파일에 "The secret word is marmalade" → 답 `marmalade` | ✓ 문서에는 정식 항목 |
| `--append-system-prompt <prompt>` | ✓ | ✓ | (미실행) | ✓ |
| `--system-prompt-file` | △ 위와 같음 | (미실행) | (미실행) | ✓ |
| `--disallowedTools` | ✓ | — | 스파이크 2에서 SDK 경로로 확인 | ✓ "bare tool name removes the matching tools from Claude's context" |
| `--permission-mode` | ✓ | — | — | ✓ `default, acceptEdits, plan, auto, dontAsk, bypassPermissions, manual` |
| `--resume`, `--session-id`, `--settings`, `--strict-mcp-config`, `--max-turns`, `--input-format`, `--output-format` | ✓ | — | — | ✓ |
| `--bare` | ✓ | — | — | ✓ — PRD "CLAUDE.md를 읽지 않으므로 쓰지 않는다"와 일치(문서: skips … CLAUDE.md) |

## 2. 발견·모순

1. `--append-system-prompt-file`·`--system-prompt-file`은 **`--help` 목록에 숨겨져 있으나** 파서가 받고 문서에도 있다. `--help` grep으로 존재를 판단하는 probe는 오판한다 — probe는 파서 수용 테스트 또는 문서 기준으로 한다.
2. `--effort` 값 집합이 문서(`ultracode` 포함)와 `--help`(`max`까지)가 다르다. 프로파일 편집기의 effort 선택지는 `low~max` 5단계로 두고 `ultracode`는 넣지 않는다.
3. `--max-budget-usd` 초과 시 `result` JSON의 `subtype: "error_max_budget_usd"` — PRD §8.2.4/FR-7.3 예산 강제 구현에서 이 값을 그대로 `budget_exceeded` 이벤트로 매핑하면 된다(추가 파싱 불필요). 최소 청구 단위가 있어 아주 작은 예산은 첫 호출 뒤에 걸린다(0.0099 USD 소비 후 중단).
4. 부록 C에는 플래그 표가 없고 문서 링크만 있다 — 위 표가 그 대조표다. PRD 수정은 하지 않았다(금지 사항).

## 3. 권고

- §8.2.4 Claude Code 폴백 명령줄 **변경 없음**.
- 데몬 `probe()`의 CLI 플래그 검증은 `claude --help` 파싱이 아니라 **버전 고정 + 파서 수용 테스트**로 한다.
