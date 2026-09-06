# e2e/p3 — P3 통합 E2E (T-I3, G6 판정 자료)

실제 런타임(Claude Code CLI + `@agentclientprotocol/claude-agent-acp` 0.74.0, Hermes 0.20.6 — 둘 다 로그인 필요)으로
**HITL 왕복 · 중복 0 · 예산 · deputy·취소 · 시나리오 C·D** 를 끝까지 돌린다.
**CI 에서 실행하지 않는다.** 결과 해석과 판정은 `plan/G6_REPORT.md`(판정은 Lead 의 `plan/G6_DECISION.md`).

| 스크립트 | 무엇을 재나 | 산출물(`out/`, git 제외) |
|---|---|---|
| `up.sh` / `down.sh` | 전용 스택. **포트를 다른 단계와 분리한다**: Postgres `colab-pg-g6`(:5442) · server :8100 · web :3020 — P1(:8080/:3000/:5435) · P2(:8090/:3010/:5436) · G5(:5437) · 스파이크 4c(:8095/:5441) 과 같이 돌 수 있게. 매 실행 `make build` | `server.log` `web.log` `*.pid` |
| `40_cli_hitl_smoke.sh` | (T-C4 PR #126) `colab hitl` 3종을 두 도구 표면으로 — **목 서버**. 목은 openapi 를 읽지 않는다: 실서버로 돌리면 404 다(결함 K-7·C-4, 보고서 §2) | `capture.jsonl` `mcp.jsonl` |
| `41_hitl_roundtrip.sh` | (a) HITL 왕복 — 턴 종료 → `waiting_human`(프로세스 0 · 슬롯 미점유) → **웹 인박스**에서 답 → 새 attempt(resume 우선) → `<hitl_answer>` 가 프롬프트에. 강제 콜드 스타트 · 거절(E7-17) · 두 도구 표면 프로브 | `41-checks.tsv` `41.json` `41-prompt-*.txt` |
| `42_partial_exec_dup0.sh` | (b) 부분 실행 → 데몬 **SIGKILL** → 3분 만료 재큐잉 → 재개. **재게시 0 · 중복 편집 0**(실기 1회; sim 100회는 CI) | `42-checks.tsv` `42.jsonl` `42-summary.tsv` |
| `43_budget_pause_override.sh` | (c) 예산 — 턴 중 초과 → `paused(budget)` + 시스템 HITL → 상향 승인 → 같은 lane·workdir 재개 → E9-08. 거절 유지(E9-03) · 추정치 드레인(E9-05) | `43-checks.tsv` `43.json` |
| `44_deputy_and_cancel.sh` | (d) deputy 위임 시점(E7-09·10)·멤버 권한(E7-11)·취소 권한(E10-05·06). 시각은 `backdate_hitl` 로 옮긴다 | `44-checks.tsv` `44.json` |
| `45_scenario_c.sh` | (e) 시나리오 C — 실행 중 메시지 / "중단하고 다시 지시" / "중단", 그리고 결정 기록이 콜드 스타트를 넘는가 | `45-checks.tsv` `45.json` |
| `46_scenario_d.sh` | (f) 시나리오 D 재확인 — hermes 실패 → 같은 머신 claude_code 폴백(workdir·아티팩트 유지), 대안 없음 → 알림 | `46-checks.tsv` `46.json` |
| `fixtures/g_user_approval_card.sh` | (g) `e2e/p2/33_` 을 G6 스택으로 재실행 + 그 `user_approval` 이 **정식 HITL 카드**로 도는지 웹 확인 | `g-checks.tsv` `g.json` `g-33.log` |
| `lib.sh` | `e2e/p2/lib.sh` 재사용 + 포트 분리 + P3 헬퍼(HITL 질의·`backdate_hitl`·attempt 별 프롬프트/브리프·웹 헬퍼) |
| `fixtures/prompt_of.py` · `brief_of.py` | claim 탭 JSONL 에서 **attempt 별** 턴 프롬프트 / 브리프 [1]~[8] 을 꺼낸다 |
| `fixtures/measure_dup0.py` | 중복 0 판정기 — `plan/spikes/spike04c/measure.py` 를 옮기고 psql 마지막 줄 빈 칸 버그만 고쳤다 |
| `fixtures/daemon_heartbeat.sh` | **데몬 대역** — daemon-protocol §4.2 heartbeat 에 `usage` 를 실어 보낸다(결함 D-17·S-44 우회) |

```
bash e2e/p3/up.sh                       # 스택 (WITH_WEB=0 이면 웹 생략)
bash e2e/p3/41_hitl_roundtrip.sh
bash e2e/p3/42_partial_exec_dup0.sh
bash e2e/p3/43_budget_pause_override.sh
bash e2e/p3/44_deputy_and_cancel.sh
bash e2e/p3/45_scenario_c.sh
bash e2e/p3/46_scenario_d.sh
bash e2e/p3/fixtures/g_user_approval_card.sh
bash e2e/p3/down.sh
```

41·43·45 는 각각 자기 데몬을 띄우므로 **동시에 돌려도 된다**(워크스페이스·포트·workdir 가 다르다).
42 는 3분 heartbeat 만료를 기다리므로 혼자 가장 오래 걸린다.

## 재현할 때 걸리는 것들

- **픽스처 함정(X-2)**: 세션 `goal`·지시문에 이 저장소의 파일·스크립트 이름을 쓰지 마라 — 에이전트가 저장소에서
  그것을 찾아 스스로 실행한다(G3_DECISION §2). 과제는 전부 저장소 밖의 가상 제품이다. 지시문에 "저장소를
  뒤지지 마라" 를 넣은 이유도 같다(G6 1회차에 도구가 404 로 실패하자 에이전트가 `git log` 를 뒤졌다).
- **kill 은 SIGKILL**: SIGTERM 은 데몬의 정상 종료 경로라 running task 를 `finish(cancelled)` 로 닫아 재큐잉이
  아예 없다(SPIKE_04c §0.2). 그리고 `lane.runtime_session_ref` 는 `finish` 에서만 저장되므로 **warm-up 턴**이
  없으면 다음 attempt 는 콜드 스타트다.
- **개입은 턴이 살아 있는 동안**: `restartLane`·`cancelLane` 은 끝난 lane 에 409 다. 45_ 가 세 자극을 한 자리에서
  내는 이유이고, 43_ 이 세 heartbeat 를 한 자리에서 내는 이유다.
- **프로세스 종료는 pid·pgid·포트만**(`P2_TASKS.md §0-10`). 이 머신에는 다른 워커의 데몬이 떠 있다.
- **비활성 버튼 판정은 CSS `:disabled`**: `get attr … disabled` 는 boolean 속성이 있을 때 빈 문자열을 준다.
- **활동 피드는 `task_event`** 다(class=`status`, payload.note), 세션 `message` 가 아니다.
- `out/` 은 gitignore — attempt 토큰(래퍼 파일)·쿠키·턴 프롬프트가 들어 있다.

비용: 41_ 은 에이전트 턴 8 남짓, 42_ 은 6, 43_ 은 5, 44_ 은 3, 45_ 은 8, 46_ 은 4 (전부 haiku).
