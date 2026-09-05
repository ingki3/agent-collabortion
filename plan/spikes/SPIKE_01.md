# 스파이크 1 — `claude-code-acp` 성숙도

| 항목 | 내용 |
|---|---|
| 근거 | PLAN.md §4 #1(시간 상자 3일 → 이번 실행은 3시간), PRD.md §8.2.2·§12 오픈 이슈 1, EVAL.md E12-01·02·03·08 |
| 실행일 | 2026-09-05 KST 09:13~09:16 (1차, 한도 도달로 중단) · 11:10~11:14 (2차 이어서) |
| 어댑터 | **`@zed-industries/claude-code-acp` 0.16.2** (npm latest, 2026-02-17) — deps `@anthropic-ai/claude-agent-sdk` 0.2.44, `@agentclientprotocol/sdk` 0.14.1. 실행 `npx -y @zed-industries/claude-code-acp` |
| 런타임 | Claude Code CLI 2.1.258, 로그인 claude.ai Max(OAuth), 모델 **haiku** (`session/set_model`) |
| 도구 | `daemon/cmd/acpprobe -scenario spike1` (Go ACP 클라이언트 `daemon/internal/acpprobe`) |
| 원시 로그 | `plan/spikes/logs/spike1_claude_20260905T001331Z.*` (1차), `spike1_claude_20260905T021040Z.*` (2차), 비교 실행 `newpkg_spike1_claude_20260905T021240Z.*` |

**판정: 통과 — 단, 패키지 이름이 바뀌었다.** 30턴·resume 11회·취소 10회·권한 요청 26건에서 크래시 0, resume 성공 11/11(100%), `allow_once` 부재 0/26(0%), 프로토콜 버전 1 고정 가능. **CLI 어댑터를 v1에 넣지 않는다.** 다만 지시받은 패키지 `@zed-industries/claude-code-acp`는 2026-02 이후 갱신이 없고, 프로젝트는 **`@agentclientprotocol/claude-agent-acp`** 로 이름을 바꿔 0.74.0(2026-09-04)까지 활발히 릴리스 중이다. 프로파일은 새 패키지 이름으로 고정해야 한다(§4).

## 1. 규모 축소 명시

PLAN §4는 100턴·resume 20·취소 20·권한 50이다. 코디네이터 지시로 **30턴·resume 10·취소 10·권한 ≥ 20**으로 줄였고, 시간 상자는 3일이 아니라 실험 시작 후 3시간(재개 후 2시간)이다. 1차 실행이 턴 14에서 claude.ai 사용 한도(`You've hit your limit · resets 11am (Asia/Seoul)`)에 걸려 중단됐고, 11:00 리셋 후 **같은 세션을 `session/load`로 이어서** 남은 17턴을 돌렸다(처음부터 다시 돌리지 않음).

## 2. 실험 방법

- 클라이언트 능력 `fs`/`terminal` 미광고 → Claude Code가 자체 Read/Write/Bash 툴을 쓰고, 어댑터 `canUseTool` → `session/request_permission`으로 권한 협상이 온다.
- 작업 턴: 매 턴 **새 경로**를 Write/Bash 리다이렉션/mkdir/cp로 만들게 해 `default` 권한 모드에서 권한 요청을 유도. (1차 실행에서 확인: Claude Code는 읽기 전용 셸(`ls|wc`, `date`, `sleep`)을 자동 승인하고, 한 번 승인한 파일의 재기록도 다시 묻지 않는다 — 그래서 1차 13턴 중 권한 요청은 6건뿐. 2차는 프롬프트를 바꿔 17턴 20건.)
- 권한 응답: `kind=="allow_once"` 옵션을 골라 응답(optionId 하드코딩 없음), 없으면 `reject_once` + 카운트. 취소 중이면 `cancelled`.
- resume: 프로세스 그룹 SIGTERM → 새 프로세스 → `initialize` → `session/load{sessionId}` → 리플레이 수신 → 확인 턴("resumed"라고만 답하라).
- 취소 두 종류 교대: `cancel_text`(긴 텍스트 생성 중 2.5초 뒤 `session/cancel`), `cancel_perm`(`python3 -c sleep(45)` 권한 요청이 대기 중일 때 4초 뒤 취소 → 대기 중 권한 요청에 `cancelled` 응답 후 `session/cancel`, PRD §8.2.2 순서).
- 크래시: 프로세스 예기치 않은 종료, 요청 대기 중 종료(`UnexpectedExit`), RPC 전송 실패.

## 3. 원시 수치

| 지표 | 1차 (09:13) | 2차 (11:10, 같은 세션 이어서) | **합계** | 기준 |
|---|---|---|---|---|
| 작업 턴 (end_turn) | 13 / 13 | 17 / 17 | **30 / 30** | — |
| 크래시 (프로세스 종료·UnexpectedExit) | 0 | 0 | **0** | 0 |
| resume(`session/load`) 시도 → 성공 | 4 → 4 (※) | 7 → 7 | **11 → 11 (100%)** | ≥ 95% |
| 취소 시도 → `stopReason: cancelled` | 4 → 4 | 6 → 6 | **10 → 10 (100%)** | — |
| 권한 요청 | 6 | 20 | **26** | ≥ 20 |
| 권한 옵션 kind 조합 | allow_always·allow_once·reject_once ×6 | 동일 ×20 | 26/26 동일 | — |
| **`allow_once` 부재** | 0 | 0 | **0 / 26 (0%)** | < 5% |
| 취소 중 권한 요청에 `cancelled` 응답 | 0 | 0 | 0 (권한이 취소보다 먼저 allow됨) | — |
| 턴 소요 중앙값 | 2.8s | 3.2s | — | — |
| `session/load` 소요 (리플레이 포함) | 4.6~5.5s | 4.4~5.5s | 리플레이 청크 10 → 111 누적 | — |
| 초기화 `protocolVersion` | 1 | 1 | **1 (요청 1 = 응답 1)** | 고정 가능 |

※ 1차의 resume 5~10회째는 `session/load` 자체와 리플레이는 성공했으나 확인 턴이 한도 오류(`-32603 Internal error: You've hit your limit`)로 실패 — 어댑터 결함이 아니라 계정 한도이므로 시도에서 제외했다. 로그 `spike1_claude_20260905T001331Z.summary.json` `resumes[4..9]`.

**한도 오류의 형태**: `session/prompt`가 JSON-RPC 오류 `-32603`로 돌아오고 프로세스는 살아 있다. 크래시가 아니다. 하네스는 이 메시지를 `rate_limited`(재시도 가능, 리셋 시각 파싱)로 분류해야 한다 — 1차 실행의 probe는 이 문구를 못 잡아 30턴을 모두 소진하며 같은 오류를 반복했다(probe 수정 완료).

## 4. 어댑터 버전·유지보수 상태

| 항목 | `@zed-industries/claude-code-acp` (지시된 패키지) | `@agentclientprotocol/claude-agent-acp` (후속) |
|---|---|---|
| npm latest | 0.16.2, **2026-02-17** (그 뒤 릴리스 없음) | **0.74.0, 2026-09-04** — 2026-03-26부터 68개 릴리스 |
| GitHub | `zed-industries/claude-code-acp` → **`agentclientprotocol/claude-agent-acp`로 리다이렉트**, 마지막 push 2026-09-04, open issues+PRs 164, ★2,478 | 같은 저장소 |
| deps | claude-agent-sdk 0.2.44 · ACP SDK 0.14.1 | claude-agent-sdk **0.3.257** · ACP SDK **1.4.0** |
| `agentInfo` | `Claude Code` | `Claude Agent` |
| 비교 실행(이 스파이크, 10턴·resume 3·취소 3) | — | 크래시 0, resume 3/3, 취소 3/3, 권한 11건 `allow_once` 11/11, 프로토콜 1 |
| 차이 | `session/new` 응답에 `models` 있음 → `session/set_model` 가능 | `session/new` 응답 `models: null` → probe의 모델 선택이 안 돼 기본 모델(claude-fable-5-1)로 실행됨. 모델 선택은 `session/set_config_option`(ACP 1.x)로 옮겨간 것으로 보임 — **P0-b에서 확인** |

즉 "지시된 패키지"는 사실상 동결, 프로젝트는 살아 있다. **프로토콜 버전(1)은 두 패키지 모두 같고 우리 클라이언트가 수정 없이 둘 다 구동했다** — 하네스 인터페이스 관점에서는 안정적이나, 어댑터 확장(`_meta.*`, 모델 선택)은 버전 간에 움직인다.

## 5. EVAL 대응

| EVAL | 결과 |
|---|---|
| E12-01 optionId 하드코딩 없이 `allow_once` 선택 | 26/26 (+11/11) — 어댑터 optionId는 `allow`이지만 클라이언트는 kind로만 고른다(단위 테스트 `TestDefaultPolicyPicksAllowOnceByKindNotID`) |
| E12-02 `allow_once` 부재 → `reject_once` | 실기에서는 발생 0. 가짜 에이전트 테스트로 경로 검증(`TestAllowOnceMissingFallsBackToReject`) |
| E12-03 부재 ≥3회 → CLI 전환 권고 | 발생 0 |
| E12-08 프로토콜 버전 불일치 | 클라이언트가 `protocolVersion != 1`이면 오류로 분류(`Initialize`) — 실기 불일치 없음 |

## 6. 권고

1. **CLI 어댑터(§8.2.4 Claude Code 폴백)를 v1 구현 범위에 넣지 않는다.** §6 컷 1은 예약하지 않는다. 폴백 스펙 문서는 유지(스파이크 6 확인 완료).
2. 프로파일 어댑터 고정값을 **`@agentclientprotocol/claude-agent-acp@<버전>`** 으로 한다. `@zed-industries/claude-code-acp`는 동결 패키지라 쓰지 않는다. PRD §8.2.3·§12의 패키지 이름은 갱신 대상(문서 수정은 이 PR에서 하지 않음).
3. P0-b 하네스 인터페이스에 넣을 것: `rate_limited` 오류 분류(리셋 시각 파싱), `session/load` 리플레이 청크는 `task_event`로 올리지 않고 버림(누적 111청크), 취소 시 대기 중 권한 요청 `cancelled` 응답(구현했으나 실기에서 발동 0 — 가짜 에이전트 테스트로 커버).
4. 0.74.0의 모델 선택 방식(`session/set_config_option`)과 `_meta.systemPrompt`(스파이크 3) 유지 여부를 **P0-b 착수 시 새 패키지로 재확인** — 반나절.
5. 100턴 전체 규모는 P1 E2E 야간 실행(§5)에서 채운다.
