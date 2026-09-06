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
| `61_scenario_b.sh` | **시나리오 B 전체**(E16-B 1~8단계): worktree 격리·브랜치·diff 아티팩트·QA 가 아티팩트만 본다(E13-08)·재진입·`agent_approval` 종료·§8.4 위생(E13-03~06)·요약·활동 피드. **QA 는 hermes**(브리프 `instruction_file` 경로가 있어야 오염을 잴 수 있다) | T-I4 스택 + 탭 :8115 |
| `62_double_write.sh` | 데몬 `kill -9` → 재시작 → **이중 쓰기 0**(E11-05·06·E8-04 (4)) + `daemon/worktreesim` 100라운드 | T-I4 스택 (hermes 1대) |
| `63_offline_rebind.sh` | 오프라인 유예 7일 → `paused(runtime_offline)` → 후보 판정(remote URL) → 재바인딩 → `rebind_prepare` → 첫 턴 프롬프트 → diff 재적용(E14-02~06·08·10) · 실서버 `listArtifacts` 순서 | T-I4 스택 (런타임 3대) + 탭 :8116 |
| `64_gc.sh` | workdir GC(E13-09~19): 미병합/미커밋 차단 + 인박스, 병합·클린 삭제 + 브랜치 보존, active 세션 보존, 쿼터 | T-I4 스택 |
| `65_summary_refusal.sh` | 세션 요약 실패 경로(E6-11·12, §8.5): refusal · 전송 오류 · 정상 · 키 없음 폴백 | 전용 server :8106(같은 DB) + `mock_anthropic.py` :8117 |

## T-I4 전용 스택 · 재현

```bash
bash e2e/p4/up.sh                      # colab-pg-i4 :5450 + server :8105 + web :3018
bash e2e/p4/61_scenario_b.sh           # 이하 하나씩 (동시에 돌려도 되지만 워크스페이스가 따로다)
bash e2e/p4/62_double_write.sh
bash e2e/p4/63_offline_rebind.sh
bash e2e/p4/64_gc.sh
bash e2e/p4/65_summary_refusal.sh
bash e2e/p4/down.sh                    # pid·pgid 로만 종료. Postgres 컨테이너는 남긴다
```

판정 표는 `out/<번호>-checks.tsv`, 요약은 `plan/G7_REPORT.md`.

## 이 판에서 밟은 함정 (다음 사람을 위해)

- **돌고 있는 bash 스크립트를 편집하지 마라.** bash 는 파일을 바이트 오프셋으로 다시 읽어 `syntax error` 로 죽는다.
- `set -euo pipefail` 에서 **무매치 `grep` 은 파이프라인을 죽인다** — `{ grep … || true; }` 로 감싼다.
  `grep -c` 는 무매치면 `0` 을 찍고 exit 1 이라 `|| echo 0` 이 줄을 **두 개** 만든다(`cnt()` 헬퍼 참고).
- **bash 3.2 는 명령 치환 안의 `case … pattern)` 을 파싱하지 못한다** (p3 `lib.sh in_set` 주석과 같은 함정).
- **fnm 의 multishell PATH 는 셸이 끝나면 사라진다** → 데몬이 물려받으면 `npx` 를 못 찾아 모든 attempt 가
  `failed(config)`. `lib.sh stable_path` 가 데몬에 넘기는 PATH 만 안정 경로로 바꾼다.
- **브리프 파일은 턴이 도는 동안에만 디스크에 있다**(삭제는 attempt 종료의 `defer`). 위생을 재려면 그 세션의
  `queued|dispatched|preparing|running` task 가 0 이 될 때까지 기다리고, "덧붙이지 않는가" 를 재려면 반대로
  **턴이 도는 동안** 세야 한다.
- **`worktree` 격리 세션은 이 빌드에서 그대로는 돌지 않는다**(G7_REPORT §1 차단 ①). 스크립트들은 세션을 만든 뒤
  데몬을 띄우기 전에 `lib.sh seed_worktree_workdirs` 로 workdir 절대 경로를 심어 우회한다 — 결함이 고쳐지면 지운다.

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
