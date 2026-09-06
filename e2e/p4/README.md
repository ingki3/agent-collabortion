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
| `60_cli_artifact_diff.sh` | `colab artifact submit --type diff` (T-C6): 생성된 diff 가 실서버 multipart 를 그대로 통과하는가, 메타 두 자리, `git apply` 재적용(**바이너리 파일 포함**), 같은 이름 version 2, 빈 diff 거절 | Postgres `colab-pg-c6` :5449 + server :8104 (웹·런타임 없음, 모델 호출 0회) |

## 생성된 diff 아티팩트를 읽는 쪽이 알아야 할 것 (T-C6)

`colab artifact submit --type diff` 가 `--file` 없이 만들어 올리는 본문에 대한 약속이다. 재바인딩
재적용(E14-06)과 시나리오 B 리뷰어가 이 규칙만 보고 동작한다.

- **`git apply` 대상이지 `git am` 대상이 아니다.** 본문은 `From`/`Subject` 없는 순수 unified diff라
  `git am` 은 `Patch format detection failed` 로 거절한다. 적용은 언제나 `git apply`(먼저
  `git apply --check`)로 한다. 맨 앞 `# colab-diff: …` 한 줄은 첫 패치 헤더 앞이라 `git apply` 가
  건너뛴다.
- **바이너리 파일이 들어 있다.** `git diff --binary` 로 만들기 때문에 아이콘·폰트·`.dat` 변경은
  `Binary files … differ` 요약이 아니라 `GIT binary patch` 페이로드로 실린다(요약만 있으면
  `git apply` 가 텍스트 hunk 까지 포함해 패치를 통째로 거부한다 — PR #160 R1). 본문 바이트는 git 이
  쓴 그대로 올라간다: base85 블록 끝의 빈 줄까지 보존해야 `corrupt binary patch` 가 안 난다.
- **기준은 merge-base 다.** `--base <ref>`(기본은 저장소 기본 브랜치)의 tip 이 아니라 fork 지점부터
  잰다. base 브랜치가 fork 이후 전진해도 그 커밋을 되돌리는 역전 hunk 가 패치에 들어가지 않는다.
- **기본 이름은 브랜치의 마지막 세그먼트**다: `colab/<session>/frontend` → `frontend.diff`(detached
  HEAD 면 에이전트 이름, 그마저 없으면 `workdir`). 데몬이 attempt 를 `colab/<session>/<agent>` 에
  올리므로 한 세션 안에서 같은 lane 의 재제출은 같은 이름 `version`+1 이 된다(FR-4.3, E16-B 5→6).
  **세션 도중 브랜치를 바꾸면 기본 이름도 바뀌어** 다음 제출이 version 1 의 새 아티팩트가 된다 —
  버전을 이어 붙이려면 `--name` 을 직접 준다.
- untracked 파일은 본문에 없다(결과 JSON 의 `diff.untracked_not_included` 로만 알려 준다). 빈 diff 는
  요청 전에 exit 2 `empty_diff`.
