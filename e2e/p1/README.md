# e2e/p1 — P1 통합 E2E (T-I1, G3 판정 자료)

실제 런타임(Claude Code + `@agentclientprotocol/claude-agent-acp@0.74.0`, 로그인 필요)으로 P1 수직 슬라이스를 검증한다.
**CI 에서 실행하지 않는다.** 결과 해석과 판정은 `plan/G3_REPORT.md`.

| 스크립트 | 내용 | 산출물(`out/`, git 제외) |
|---|---|---|
| `up.sh` / `down.sh` | `make dev` 구성(Postgres·server :8080·web :3000)을 백그라운드로 기동/종료. Postgres 는 E2E 전용 `colab-pg-e2e`(5435) | `server.log` `web.log` `*.pid` |
| `01_vertical_slice.sh` | (a) 가입→워크스페이스→페어링→`daemon pair`→probe→Lead(haiku)→세션→`@Lead 인사해줘`×N, claim·첫 출력·답글 지연 중앙값 (E17-01·02) | `a-latency.tsv` `a-summary.json` `a-ids.txt` `daemon-a.log` |
| `02_kill9.sh` | (b) running 중 데몬 `kill -9` → 3분 → 재큐잉·토큰 폐기·401·고아 sweep·중복 0 (E11-03~06, E8-04) | `b-summary.json` |
| `03_cancel.sh` | (c) 취소 두 경로: **(A) 사람 경로 `cancelLane`(202)** → finish `cancelled`·lane `failed`·재큐잉 0·프로세스 트리 0, **(B) 데몬 SIGTERM 정상 종료**(E10-13) → finish `cancelled` (E11-07, E10-03·04) | `c-summary.json` `c-run.log` |
| `04_u1_browser.sh` | (d) agent-browser 로 EVAL_USER U1 1~13 + U13 실서버, 단계별 "보이는 것" 판정 | `d-steps.tsv` `d-summary.json`, `web/__screenshots__/p1-u1-*.png` |
| `05_invite_api.sh` | DoD 4 초대→두 번째 멤버 (API) | `e-summary.json` |
| `06_s12_pairing_realtime.sh` | S12: 서버 SSE `pairing.updated` + 웹 패널이 10초 안에 `준비 완료`(E17-09), 페어링 1건 (에이전트 턴 없음) | `f-summary.json` `f-sse.txt`, `p1-s12-*.png` |
| `workaround-0001-check.sh` | (역사) 0004 머지 전 로컬 DB 우회. 0004 적용 후엔 불필요 |
| `lib.sh` | 공통: API(curl+jq), DB(docker exec psql), 데몬 기동, 지연 계산(서버 DB 클럭) |

순서: `up.sh` → `01` → `03` → `02` → `05` → `06` → `04` → `down.sh`. `01` 이 만든 `out/a-ids.txt`·쿠키를 `02`·`03`·`05`·`06` 이 쓴다.
비용: 에이전트 턴은 haiku 프로파일(`LEAD_MODEL`). (a) 21턴, (b) 4~5턴, (c) 3턴, (d) 2~3턴(웹이 고른 `models[0]`).

**픽스처 주의**: 세션 `goal`·지시 문구에 이 저장소의 시나리오 이름(`E11-07`, `03_cancel.sh` 등)을 쓰지 마라. 에이전트가 저장소에서 그 스크립트를 찾아 스스로 실행해 세션이 재귀 생성되고, 중첩 실행의 `kill -TERM` 이 데몬을 죽인다(2026-09-06 실측, G3_DECISION §2 X-2). `goal` 은 저장소를 가리키지 않는 중립 문장으로.

`up.sh` 는 서버에 `COLAB_WEB_URL=$WEB_URL` 을 넘긴다 — 초대 링크가 사람이 여는 웹 오리진(:3000)을 가리키게 하기 위해서다(S-5). `make dev` 는 아직 이 값을 넘기지 않는다.
