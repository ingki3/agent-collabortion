# e2e/p5 — P5 스모크·판정 스크립트

`e2e/p4/README.md` 의 규약을 그대로 잇는다(→ p3 → p2 → p1). 다른 점만 적는다.

- **번호는 Lead 가 준다**(P5_TASKS §0: `70_` 부터). 같은 번호를 다시 쓰지 않는다.
  산출물 파일명도 스크립트 번호를 따른다(`out/70-*`).
- **스택은 스크립트마다 격리**(P3_TASKS §0-13). `SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 를
  미리 export 하면 덮어쓸 수 있다. `up.sh` 는 기본으로 **서버만** 띄운다(`WITH_WEB=1` 이면 웹도).
- `out/` 은 `.gitignore` 다 — claim 응답·데몬 토큰·쿠키가 들어 있다.
- **판정 표**는 `lib.sh chk ID EXPECT ACTUAL NOTE` 로 `out/<번호>-checks.tsv` 에 쌓인다
  (p2 의 `chk ID 설명 기대 실제` 와 인자 순서가 다르다 — 이 디렉터리 안에서만 쓴다).

| 스크립트 | 무엇을 재는가 | 스택 |
|---|---|---|
| `70_testchat.sh` | **테스트 채팅**(FR-1.8.1, daemon-protocol v0.8 §4.5) 서버 쪽 전부 — 데몬 **없이** 데몬 역할을 curl 로 흉내: createTestChat → 턴 202/409 → claim 이 주는 §4.5 번들(kind·id·attempt·토큰 없음·[2] 없음·workdir·첫 턴 머리 한 줄) → phase → heartbeat(preview → SSE `test_chat.delta`) → events(message.say 합침, task_event 0) → finish(transport·usage 추정·ref) → SSE `test_chat.turn` → getTestChat(agent 턴·토큰·transport·비용) → 턴 2 의 `resume` → failed(auth) 의 §8.4 문장 → 워크스페이스 `test_chat_usd` → close 의 gc(test_chat_id, session_id 없음)·410·§6 영수증으로 소비 → 진행 중 턴 close 의 cancel+gc. 끝에 **getWorkspaceMetrics**(10개·순서·표본 0 = null·422). 57 판정 | Postgres `colab-pg-s12` :5451 + server :8107 (웹·데몬·모델 호출 0회) |

## 재현

```bash
bash e2e/p5/up.sh                      # colab-pg-s12 :5451 + server :8107 (bin/server 를 다시 빌드)
bash e2e/p5/70_testchat.sh             # out/70-checks.tsv · 70-bundle-{1,2}.json · 70-sse.log · 70-testchat.json · 70-metrics.json
bash e2e/p5/down.sh                    # pid·pgid 로만 종료(§0-10). Postgres 컨테이너는 남긴다
```

## 이 판에서 밟은 함정

- **bash 3.2 는 `$( … "…\"…\"…" )` 안의 이스케이프한 따옴표에서 인자를 가른다.** `chk ID 200 "$(daemon_api_code … "{\"phase\":…}")" "설명"` 이
  `got` 와 `note` 양쪽에 HTTP 코드를 넣었다(422 로 보였지만 실제 서버 응답은 200). 데몬에 보낼 JSON 은 `jq -nc` 로 **먼저 변수에**
  만들고 그 변수만 넘긴다(70_ 의 `PH1`·`PH2`).
- 70_ 은 데몬을 띄우지 않으므로 **데몬 몫**(§4.5 토큰 없는 환경·`mcpServers` 미탑재·래퍼 미생성·`.colab/testchat/` 아래만 `rm -rf`·
  24h 지난 디렉터리 정리)은 여기서 재지 않는다 — T-D12 의 자리다.
- `task_event 저장 0` 판정(B.5)은 DB 전역 count 다. 전용 스택이라 0 이지만, 다른 스크립트와 DB 를 공유하면 먼저 재라.
