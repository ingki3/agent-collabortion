# e2e/p1 — P1 통합 E2E (T-I1, G3 판정 자료)

실제 런타임(Claude Code + `@agentclientprotocol/claude-agent-acp@0.74.0`, 로그인 필요)으로 P1 수직 슬라이스를 검증한다.
**CI 에서 실행하지 않는다.** 결과 해석과 판정은 `plan/G3_REPORT.md`.

| 스크립트 | 내용 | 산출물(`out/`, git 제외) |
|---|---|---|
| `up.sh` / `down.sh` | `make dev` 구성(Postgres·server :8080·web :3000)을 백그라운드로 기동/종료. Postgres 는 E2E 전용 `colab-pg-e2e`(5435) | `server.log` `web.log` `*.pid` |
| `01_vertical_slice.sh` | (a) 가입→워크스페이스→페어링→`daemon pair`→probe→Lead(haiku)→세션→`@Lead 인사해줘`×N, claim·첫 출력·답글 지연 중앙값 (E17-01·02) | `a-latency.tsv` `a-summary.json` `a-ids.txt` `daemon-a.log` |
| `02_kill9.sh` | (b) running 중 데몬 `kill -9` → 3분 → 재큐잉·토큰 폐기·401·고아 sweep·중복 0 (E11-03~06, E8-04) | `b-summary.json` |
| `03_cancel.sh` | (c) 취소: `cancelLane` 시도(501) + 데몬 SIGTERM 취소 경로 → 프로세스 트리 0 (E11-07) | `c-summary.json` |
| `04_u1_browser.sh` | (d) agent-browser 로 EVAL_USER U1 1~13 + U13 실서버, 단계별 "보이는 것" 판정 | `d-steps.tsv` `d-summary.json`, `web/__screenshots__/p1-u1-*.png` |
| `05_invite_api.sh` | DoD 4 초대→두 번째 멤버 (API) | `e-summary.json` |
| `06_s12_pairing_realtime.sh` | S12 실패 원인 분리: 서버 SSE `pairing.updated` vs 웹 패널 갱신 (에이전트 턴 없음) | `f-summary.json` `f-sse.txt`, `p1-s12-*.png` |
| `workaround-0001-check.sh` | (역사) 0004 머지 전 로컬 DB 우회. 0004 적용 후엔 불필요 |
| `lib.sh` | 공통: API(curl+jq), DB(docker exec psql), 데몬 기동, 지연 계산(서버 DB 클럭) |

순서: `up.sh` → `01` → `03` → `02` → `05` → `06` → `04` → `down.sh`. `01` 이 만든 `out/a-ids.txt`·쿠키를 `02`·`03`·`05`·`06` 이 쓴다.
비용: 에이전트 턴은 haiku 프로파일(`LEAD_MODEL`). (a) 21턴, (b) 4~5턴, (c) 2턴, (d) 2~3턴(웹이 고른 `models[0]`).
