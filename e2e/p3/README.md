# e2e/p3 — P3 스모크

| 스크립트 | 무엇을 재나 | 스택 |
|---|---|---|
| `40_cli_hitl_smoke.sh` | `colab hitl ask · approve-request · request-info` 를 **에이전트가 실제로 닿는 두 도구 표면**으로 한 번씩 — Claude Code 는 `colab mcp serve`(MCP), Hermes 는 `<workdir_root>/.colab/bin/<task>.<attempt>/colab` 래퍼를 `env -i` 로(harness.md §10 `cli_wrapper`). E7-01·04·05·06·20·21 과 C-3(버전), 요청 **경로** | **목 서버**(`mock_hitl_server.py`) |
| `41_cli_hitl_real.sh` | 같은 명령이 **실서버**에 닿는가 — 진짜 라우터·진짜 `ctk_` TaskToken 으로 `hitl ask` 1회 → 201 · DB 행 · 409 · `turn_end` 후 `waiting_human`. C-4 회귀 | **전용 스택**(Postgres :5443 + server :8097, 웹 없음) |

```
bash e2e/p3/40_cli_hitl_smoke.sh    # 산출물 e2e/p3/out/ (gitignore)
bash e2e/p3/41_cli_hitl_real.sh     # 산출물 e2e/p3/out-real/ (gitignore) — 끝나면 스택 down
KEEP_STACK=1 bash e2e/p3/41_cli_hitl_real.sh   # 스택을 남긴다
```

`mock_hitl_server.py` 는 `contracts/openapi.yaml` `createHitlRequest` 하나만 구현한다 —
경로(`POST /api/v1/sessions/{S}/hitl-requests`), 201 형태, 두 번째 요청의 `409 hitl_already_open`,
`proposed_default` 없는 422. **목은 openapi 에서 베끼고 CLI 에서 베끼지 않는다.**
T-C4 때는 목도 CLI 도 같은 잘못된 표(`/tasks/{T}/hitl`)에서 나와 스모크가 전부 초록이었는데
실서버에서는 404 였다(C-4, T-I3 발견). 목을 고칠 일이 있으면 **41_ 도 같이 돌려라** — 목이
받아 주는 경로와 서버가 받는 경로가 갈라지는 순간을 잡는 것은 41_ 뿐이다.

`41_` 은 에이전트 런타임을 띄우지 않는다. 필요한 것은 데몬의 HTTP 왕복뿐이라
`daemon-protocol.md` §2·§4 를 curl 로 직접 친다(pair → probe → claim → phase → finish) —
모델 호출 0회, 30초. 스택 포트는 다른 워커와 겹치지 않게 전용이다(`P3_TASKS §0-13`);
`SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 로 덮어쓸 수 있다.

40_ 은 `e2e/p1`·`e2e/p2` 와 달리 **전체 스택을 띄우지 않는다** — CLI 하나만 보는 스모크다.

프로세스 종료는 pid 로만 한다(`P2_TASKS.md §0-10`). 래퍼 파일은 attempt 토큰을 담으므로 `out/` 은 gitignore.
