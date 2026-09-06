# 스파이크 4a — 런타임 resume 능력: Claude Code 컨텍스트 유지, Hermes 유실 감지

| 항목 | 내용 |
|---|---|
| 근거 | PLAN.md §4 #4a, PRD.md §8.2.5(Hermes 함정), §12 리스크·오픈 이슈 4, EVAL.md E8-03 |
| 실행일 | 2026-09-05 (KST 11:10~11:13) |
| 런타임 | (a) `@zed-industries/claude-code-acp` 0.16.2 + Claude Code 2.1.258, 모델 haiku. (b) `hermes acp` 0.20.6, 모델 `anthropic:claude-haiku-4-5-20251001`(`session/set_model`) |
| 도구 | `acpprobe -scenario spike4a -n 10`, `acpprobe -runtime hermes -scenario hermes-loss -n 4` |
| 도구 주석 | `acpprobe`(스파이크 전용 CLI)는 P2 에서 삭제됐다 — `daemon/internal/harness/acp` 로 승격 완료(백로그 D-3). 이 문서의 명령줄은 당시 실행 기록이며 지금은 재현되지 않는다. 재현이 필요하면 `daemon/internal/probe`(PONG 턴)와 `daemon/internal/acpfake`(계약 테스트)를 쓴다 |
| 원시 로그 | `plan/spikes/logs/spike4a_claude_20260905T021040Z.*`, `plan/spikes/logs/hermes-loss_hermes_20260905T021041Z.*` |

**판정: 통과** — (a) Claude Code ACP `session/load` 후 컨텍스트 유지 **10/10 (100%, 기준 90%)**. (b) Hermes `state.db`에서 세션을 지운 뒤 `session/resume`은 오류 없이 새 세션을 만들지만, 응답 `_meta.hermes.sessionProvenance.acpSessionId`가 요청한 id와 달라 **3/3 (100%) 감지**. 대조군(삭제 안 함) 1/1은 id 일치·기억 유지.

## (a) Claude Code — 프로세스 재시작 + `session/load` 뒤 컨텍스트 유지

방법(반복 10회, 매회 새 세션): ① 새 프로세스·`session/new` → 턴 1 "memo_i.txt에 고유어 `<word>-<n>`을 Write" ② **프로세스 그룹 kill**(데몬 재시작 상황) ③ 새 프로세스 → `session/load{sessionId}` ④ 턴 2 "툴·파일 읽기 없이, 직전에 무엇을 했나? 파일명과 단어를 말하라" → 응답에 고유어 포함 여부.

| 회 | 고유어 | `session/load` 리플레이 청크 | 턴 2 응답(요지) | 유지 |
|---|---|---|---|---|
| 1 | pomegranate-1007 | 7 | memo_1.txt · pomegranate-1007 | ✓ |
| 2 | quasar-1014 | 6 | memo_2.txt · quasar-1014 | ✓ |
| 3 | lighthouse-1021 | 20 | memo_3.txt · lighthouse-1021 | ✓ |
| 4 | saffron-1028 | 7 | memo_4.txt · saffron-1028 | ✓ |
| 5 | meridian-1035 | 6 | memo_5.txt · meridian-1035 | ✓ |
| 6 | tungsten-1042 | 21 | memo_6.txt · tungsten-1042 | ✓ |
| 7 | obsidian-1049 | 22 | memo_7.txt · obsidian-1049 | ✓ |
| 8 | cinnamon-1056 | 25 | memo_8.txt · cinnamon-1056 | ✓ |
| 9 | harbinger-1063 | 7 | memo_9.txt · harbinger-1063 | ✓ |
| 10 | zeppelin-1070 | 7 | memo_10.txt · zeppelin-1070 | ✓ |

- 10/10. 턴 2는 모두 툴 호출 0(지시대로), `end_turn`.
- `session/load`는 어댑터가 `~/.claude/projects/<cwd-encoded>/<sessionId>.jsonl`을 찾아 히스토리를 `user_message_chunk`/`agent_message_chunk`로 리플레이한 뒤 응답한다(`acp-agent.js:171`). 파일이 없으면 `"Session not found"` 오류 — **Claude Code 쪽 유실은 오류로 구분된다**(PRD §8.2.5 `resume_rejected` 분류 가능).
- 스파이크 1에서도 같은 세션에 대해 `session/load` 11회 연속 성공(리플레이 청크 10→111 누적).

## (b) Hermes — `state.db` 세션 삭제 후 resume

방법(반복 4회, 마지막 1회는 대조군): ① `hermes acp` 새 프로세스·`session/new` → 턴 1 "코드워드 `walrus-i`를 기억하라" ② 프로세스 kill ③ `sqlite3 ~/.hermes/state.db`에서 `messages`·`sessions`(source='acp') 해당 행 삭제 ④ 새 프로세스 → `session/load` 및 `session/resume` → 응답 `_meta` 비교 ⑤ 턴 2 "코드워드가 뭐였나".

| 회 | 삭제 | `session/load` | `session/resume` | provenance `acpSessionId` | 턴 2 응답 | 감지 | 근거 |
|---|---|---|---|---|---|---|---|
| 1 | ✓ | **오류 없음, 결과 `null`** | 오류 없음 | `dd8cc84b…` ≠ 요청 `350d465e…` | "I don't have a code word stored…" | ✓ | provenance 불일치 |
| 2 | ✓ | 동일 | 동일 | `e611efe4…` ≠ `85974db1…` | "I don't have a code word…" | ✓ | provenance 불일치 |
| 3 | ✓ | 동일 | 동일 | `4df8e355…` ≠ `c5ddcc23…` | 동일 | ✓ | provenance 불일치 |
| 4 (대조) | ✗ | 정상 | 정상 | `34aa91a3…` = 요청 | **`walrus-4`** | — | 일치, 기억 유지 |

- PRD §8.2.5의 함정 그대로 재현: `resume_session`은 세션이 없으면 `logger.warning("… not found, creating new")` 후 **새 세션을 만들고 정상 응답**한다(`acp_adapter/server.py:1660-1695`).
- 그러나 Hermes 0.20.x는 `session/new`·`session/load`·`session/resume` 응답에 **`_meta.hermes.sessionProvenance`** (`acpSessionId`, `currentHermesSessionId`, `rootHermesSessionId`, `sessionKind`, `compressionDepth`)를 붙인다(`acp_adapter/provenance.py`). **요청한 `sessionId` ≠ `acpSessionId`면 유실**. 이 규칙만으로 3/3, 대조군 오탐 0.
- `session/load`는 세션이 없을 때 **JSON-RPC 오류가 아니라 `result: null`**을 돌려준다(Python 핸들러가 `None` 반환). 하네스는 "load 결과가 null이면 유실"로도 잡을 수 있지만, provenance가 더 직접적이다.
- `stopReason=="refusal" && 활동 0` 규칙(PRD)은 **발동하지 않았다** — 유실 후 턴은 `end_turn`으로 정상 종료된다. 이 규칙은 보조로만 둔다.
- 삭제 후 `sessions` 행은 새 id로 재생성된다(`started_at` 새 값) — `runtime_session_ref`에 저장한 id로 DB를 직접 조회해도 감지 가능하나, 프로토콜 응답으로 충분하므로 DB 접근은 권하지 않는다.

## 권고

1. 하네스 `resume: true` 두 런타임 모두. Claude Code는 `session/load`(안정 메서드, 리플레이 포함) 사용. Hermes는 `session/load`가 유실 시 `null`을 주므로 **`session/load` → 결과 null이면 즉시 유실 판정**, 성공이면 `_meta.hermes.sessionProvenance.acpSessionId == 요청 id` 재확인. `session/resume`(UNSTABLE)은 쓰지 않는다.
2. `lane.runtime_session_ref`에 세션 id와 함께 **provenance(`rootHermesSessionId`, 생성 시각)** 를 저장해 두면 압축으로 id가 회전(`reason: "compression"`)한 경우와 유실을 구분할 수 있다.
3. 유실 판정 시 FR-5.4 콜드 스타트로 전환. E8-03 "유실 감지 100%"는 이 규칙으로 P1 하네스 계약 테스트에 넣는다.
4. **PLAN §7 가정 2 수정 제안**: Hermes는 `session/resume`뿐 아니라 `session/load`도 제공하며 `usage_update`도 온다(hermes-smoke 로그에 `usage_update` 확인). 문서 수정은 하지 않았다.
