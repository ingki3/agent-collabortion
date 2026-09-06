# e2e/p3 — P3 스모크

| 스크립트 | 무엇을 재나 | 스택 |
|---|---|---|
| `40_cli_hitl_smoke.sh` | `colab hitl ask · approve-request · request-info` 를 **에이전트가 실제로 닿는 두 도구 표면**으로 한 번씩 — Claude Code 는 `colab mcp serve`(MCP), Hermes 는 `<workdir_root>/.colab/bin/<task>.<attempt>/colab` 래퍼를 `env -i` 로(harness.md §10 `cli_wrapper`). E7-01·04·05·06·20·21 과 C-3(버전) | **목 서버**(`mock_hitl_server.py`) |

```
bash e2e/p3/40_cli_hitl_smoke.sh    # 산출물 e2e/p3/out/ (gitignore)
```

`mock_hitl_server.py` 는 `contracts/openapi.yaml` `createHitlRequest` 하나만 구현한다 —
201 형태, 두 번째 요청의 `409 hitl_already_open`, `proposed_default` 없는 422.
**서버 T-S5(PR #124)가 머지되면** 목 대신 실서버로 돌린다: `SERVER_URL` 과 `TOKEN`(실 attempt 토큰),
`TASK_ID` 를 실제 값으로 바꾸고 `reset_mock` 을 task 마다 새로 만드는 것으로 대체하면 같은 단언이 그대로 선다.
스크립트는 `e2e/p1`·`e2e/p2` 와 달리 **전체 스택을 띄우지 않는다** — CLI 하나만 보는 스모크다.

프로세스 종료는 pid 로만 한다(`P2_TASKS.md §0-10`). 래퍼 파일은 attempt 토큰을 담으므로 `out/` 은 gitignore.
