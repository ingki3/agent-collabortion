# e2e/p2 — P2 통합 E2E (T-I2, G4 판정 자료)

실제 런타임(Claude Code + `@agentclientprotocol/claude-agent-acp@0.74.0`, 로그인 필요)으로 **시나리오 A 를 단일 런타임으로** 끝까지 돌린다.
**CI 에서 실행하지 않는다.** 결과 해석과 판정은 `plan/G4_REPORT.md`.

| 스크립트 | 내용 | 산출물(`out/`, git 제외) |
|---|---|---|
| `up.sh` / `down.sh` | 전용 스택 기동/종료. **포트를 P1 과 분리한다**: Postgres `colab-pg-g4`(:5436) · server :8090 · web :3010 — 다른 워크스페이스의 P1 스택(:8080/:3000/:5435)과 같이 돌 수 있게. 매 실행 `make build` 하고 **빌드 시각을 찍는다** | `server.log` `web.log` `*.pid` |
| `10_scenario_a_api.sh` | 시나리오 A **API/CLI 경로**: 위임 3 → lane 3 병렬 → 합류 1회 → 종합 → Writer 초안 → `artifact submit`. 판정 31항목(Lead 깨어난 횟수 3, 동시 실행, E1-15·E1-21, 201·Content-Length, 진행률) | `a-checks.tsv` `scenario-a.json` `a-join-prompt.txt` `claim-tap.jsonl` |
| `11_scenario_a_web.sh` | 같은 시나리오를 **웹(agent-browser)** 으로 — EVAL_USER U2·U4·U5·U15 여정 | `w-steps.tsv` `w-summary.json`, `web/__screenshots__/p2-a-*.png` |
| `12_mock_vs_real.sh` | `web/e2e/p2-mock.sh` 를 **BASE_URL 만 바꿔** 실서버에 돌리고 목과 행별로 대조. 갈리는 행이 결함 후보다 | `mock-run.txt` `real-run.txt` `mock-vs-real.tsv` |
| `20_regression_p1.sh` | `e2e/p1/01~07` 을 이 스택에 그대로(README 순서: 01 → 03 → 02 → 05 → 06 → 04 → 07) | `p1/*.log` `regression.tsv` |
| `lib.sh` | `e2e/p1/lib.sh` 재사용 + 포트 분리 + P2 헬퍼(에이전트·세션 생성, lane/합류/동시성 질의) |
| `fixtures/claimtap.py` | 데몬↔서버 사이의 기록용 프록시. claim 응답(`TaskBundle`)을 남긴다 — **E1-21 은 서버가 데몬에 보내는 턴 프롬프트를 봐야만 증명된다**(디스크에 남지 않는다). 구현 무수정 |
| `fixtures/scenario_a_agents.sh` | Lead·Researcher·Writer instruction. 10_ 과 11_ 이 같은 것을 쓴다 |
| `fixtures/prompt_of_task.py` | 탭 JSONL 에서 특정 task 의 턴 프롬프트를 꺼낸다 |

**픽스처 주의(P1 과 같다)**: 세션 `goal`·브리프에 이 저장소의 파일·스크립트 이름을 쓰지 마라 — 에이전트가 그것을 찾아 스스로 실행한다(G3_DECISION §2 X-2). 과제는 저장소 밖의 무해한 주제로. `runtime_id` 는 명시하고 워크스페이스 이름은 ASCII 로.

비용: `10_` 은 에이전트 턴 7(haiku), `11_` 도 7 안팎, `12_` 는 0~1, `20_` 은 `N` + 10 남짓.
