# e2e/p4 — P4 스모크·판정 스크립트

`e2e/p3/README.md` 의 규약을 그대로 쓴다. 다른 점만 적는다.

- **번호는 Lead 가 준다**(P3_TASKS §0-17 의 P4 판): 60~ 스트림 실서버 스모크,
  이후 T-I4 의 G7 판정 스크립트는 Lead 가 따로 배정한다. 같은 번호를 다시 쓰지 않는다.
  산출물 파일명도 스크립트 번호를 따른다(`out/60_*`).
- **대상 저장소는 이 저장소가 아니다**(P4_TASKS §0-18). worktree 를 다루는 스크립트는
  `mktemp -d` 로 임시 git 저장소를 만들어 쓰고 끝나면 지운다 — 자기 저장소를 대상으로 삼으면
  G3 판정의 X-2(에이전트가 이 저장소를 헤집는다)의 worktree 판이 된다.
- **스택은 스크립트마다 격리**(P3_TASKS §0-13). 다른 워커의 스택이 같은 머신에 떠 있다.
  `SERVER_URL`·`PG_PORT`·`PG_CONTAINER` 를 미리 export 하면 덮어쓸 수 있다.
- `out/` 은 `.gitignore` 다 — claim 응답에 진짜 `ctk_` 토큰이 들어 있다.

| 스크립트 | 무엇을 재는가 | 스택 |
|---|---|---|
| `60_cli_artifact_diff.sh` | `colab artifact submit --type diff` (T-C6): 생성된 diff 가 실서버 multipart 를 그대로 통과하는가, 메타 두 자리, `git apply` 재적용, 같은 이름 version 2, 빈 diff 거절 | Postgres `colab-pg-c6` :5449 + server :8104 (웹·런타임 없음, 모델 호출 0회) |
