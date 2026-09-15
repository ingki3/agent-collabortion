# 스파이크 5 — 추적 중 `CLAUDE.md` 마커 append + `skip-worktree`

| 항목 | 내용 |
|---|---|
| 근거 | PLAN §4 스파이크 5(P4 착수 전), §3 P4 D 스트림·DoD, `plan/P3_TASKS.md` 관례. PRD §8.4 M6·M3(전달 경로·숨기기 표·"복원은 마커 구간만"), FR-4.1, `contracts/harness.md` §1(`brief_transport`)·§3(`_meta`·`settingSources`)·§10(브리프와 workdir) |
| 실행일 | 2026-09-07 KST 02:46~02:55. 실기 런 **26회**(런타임 2종 × 레이아웃 2종 × 전달 방식 5종; 지시문을 고쳐 다시 돌린 4회는 `out/runs.superseded.jsonl` 로 뺐다) + 런타임 없는 git 부수 효과 probe 8묶음 |
| 런타임 | Claude Code CLI 2.1.258 + 어댑터 `@agentclientprotocol/claude-agent-acp` **0.74.0** · **Hermes 0.20.6**. 모델 `claude-haiku-4-5-20251001`, 실제 로그인 |
| 스택 | **없다.** 데몬·서버·Postgres 를 띄우지 않았다 — 이 스파이크의 §10 데몬 코드는 P4 T-D9 가 만들고, 4질문은 전부 "런타임 ↔ workdir ↔ git" 사이에서 결정된다. `plan/spikes/spike05/acp.py` 가 `harness.md` §2·§3 시퀀스를 그대로 흉내 내 런타임과 직접 stdio JSON-RPC 로 말한다. 다른 워커 스택과 겹치는 포트·컨테이너가 하나도 없다 |
| 실험 대상 저장소 | **이 저장소가 아니다.** 런마다 `/private/tmp/colab-spike05/<run_id>/repo` 에 커밋 3개짜리 임시 저장소를 새로 만든다 — `CLAUDE.md`·`AGENTS.md` 를 **추적하는** 상태(M3 의 조건)에 가공의 위젯 카탈로그 과제(X-2: 이 저장소의 파일명·시나리오 이름을 쓰지 않는다) |
| 도구 | `plan/spikes/spike05/` — `acp.py`(ACP 배관) · `spike05.py`(한 케이스) · `run_all.sh`(매트릭스) · `run_controls.sh`(대조군) · `gitprobe.sh`(런타임 없는 git 부수 효과) · `measure.py`(표 생성) |
| 원시 로그 | `plan/spikes/logs/spike05_runs.jsonl`(런별 판정치 + 두 턴 전문 + git 출력), `plan/spikes/logs/spike05_gitprobe.txt`(부수 효과 probe), `plan/spikes/logs/spike05_runs_superseded.jsonl`(§0.4 로 폐기한 4회) |
| 분리한 코드 PR | **없다.** 구현 코드·`contracts/` 를 한 줄도 고치지 않았다 |

---

## 총평

**판정: 실패 — 우회로 간다.** PLAN §4 스파이크 5 의 통과 기준 셋 중 **"런타임 인지"와 "복원 무손실"은 성립하고, `git status` 클린은 성립하지만 그것이 위험을 가린다.** 떨어뜨리는 것은 계약이 예상하지 않았던 네 번째 사실이다:

> **`skip-worktree` 는 에이전트의 정당한 편집을 조용히 삼킨다.** 두 런타임 모두 지시 파일 편집에 성공하지만(12/12), 이어서 실행한 `git add -A && git commit` 은 **`nothing to commit, working tree clean`** 을 돌려주고 커밋은 일어나지 않는다(커밋 0/12). 에이전트는 커밋했다고 믿고 턴을 끝낸다. 코드 파일과 지시 파일을 같이 고친 커밋에서는 **코드 파일만 들어가고 지시 파일 편집만 빠지며, 그 뒤 `git status` 는 클린**이라 눈으로도 알 수 없다(§3.3).

PRD §8.4 M6 가 막으려던 것은 "(b) `git status` 에 잡혀 에이전트가 커밋해버린다"였다. `skip-worktree` 는 그것을 막지만 **반대 방향으로 같은 크기의 유실**을 만든다 — 에이전트가 커밋해야 할 것을 커밋하지 못한다. 그리고 자력으로 빠져나올 길이 없다: `git add <path>` 는 sparse-checkout 을 말하는 **엉뚱한 메시지**로 실패하고, `git stash` 는 `No local changes to save`, `git switch`·`git merge` 는 **`Your local changes to the following files would be overwritten` 로 중단**된다(§3.2). P4 시나리오 B 는 에이전트가 브랜치에 커밋하는 시나리오다.

여기에 두 번째 사실이 겹친다. **Claude Code 는 v1 계약의 `settingSources: []` 아래에서 `CLAUDE.md` 를 아예 읽지 않는다**(마커 0/4, 저장소 원본 규칙도 0/4). 스파이크 5 의 질문 자체가 claude_code 에는 **발생하지 않는다** — 계약 §10 이 이미 "`CLAUDE.md` 를 만들거나 고치지 않는다"로 정해 두었고 실측이 그 결정을 뒷받침한다. 읽히게 하려면 `settingSources: ["project"]` 가 필요한데(그때는 4/4 읽는다) 그것은 G1 F2 로 닫아 둔 격리를 다시 여는 일이다.

따라서 이 문제가 **실제로 존재하는 자리는 Hermes 하나**다(`brief_transport: instruction_file`, `AGENTS.md`). 그리고 거기서 실측한 우회는 이렇다.

| 방식 | 읽힘 | 에이전트 커밋 | `git status` | 판정 |
|---|---|---|---|---|
| 마커 append + `skip-worktree` (PLAN 기본안) | ✅ | ❌ **조용히 실패** | 클린(가짜) | **채택 불가** |
| 마커 append, 숨기지 않음(대조) | ✅ | ✅ | `M AGENTS.md` 노출 | 브리프가 커밋에 섞일 위험(§6.2 실측 4/4) |
| 우회 A: 별도 파일 + `@import` 한 줄 | Claude Code ✅ / **Hermes ❌** | ❌(마커가 남아 `skip-worktree` 가 그대로 필요) | 클린(가짜) | **Hermes 에서 무효** |
| **우회 B: 미추적 브리프 파일 + 턴 프롬프트가 가리킴** | ✅ | ✅ | **진짜 클린** | **권고** |

**권고 한 줄: `skip-worktree` 를 채택하지 않는다. 추적 중인 지시 파일에는 손대지 않고, 브리프를 미추적 파일(`.git/info/exclude`)에 두고 턴 프롬프트가 그 절대 경로를 가리키는 우회 B 로 간다 — `contracts/harness.md` §10 과 PRD §8.4 M3 표의 "추적 중" 행을 바꾸는 계약 PR 이 필요하다.**

---

## 0. 방법

### 0.1 한 런의 모양

```
임시 저장소 생성   : /private/tmp/colab-spike05/<run_id>/repo, 커밋 3개, CLAUDE.md·AGENTS.md 추적
(worktree 판)      : git worktree add -b colab/<session>/<agent> ../wt   → workdir = wt
데몬이 하는 일     : 지시 파일에 마커 구간 append (daemon/internal/brief 와 같은 규칙)
                     + git update-index --skip-worktree <file>
관측 1             : git ls-files -v / git status --porcelain / (worktree 판) 원본 체크아웃 쪽도
런타임 턴 1 (읽기)  : "도구를 쓰지 마라. 지시에 있는 SESSION_CONFIRM_CODE 와 PROJECT_RULE_CODE 를 답하라"
런타임 턴 2 (편집)  : "<file> 끝에 한 줄 덧붙이고, git add -A && git commit, 그 출력을 그대로 보고하라"
관측 2             : 파일 실물 / git status / git log / git show HEAD:<file>
데몬 복원          : --no-skip-worktree → 마커 구간만 제거
관측 3             : git status --porcelain / git diff / 원본 규칙 잔존 / 에이전트 편집 잔존
```

`SESSION_CONFIRM_CODE` 는 마커 구간 안에만, `PROJECT_RULE_CODE`(`ORIG-RULE-4471`) 는 저장소 원본 부분에만 있다. 두 값을 따로 물어 **"마커를 읽었는가"와 "저장소 원본 규칙을 읽었는가"를 분리해서 잰다** — M6 이 막으려던 "(a) 원본 규칙이 사라진다"가 실제로 성립하는지도 같은 턴에서 확인된다.

### 0.2 실험 대상 저장소는 이 저장소가 아니다

런마다 새 임시 저장소를 만든다(`spike05.py: make_repo`). 과제는 가공의 위젯 카탈로그이고, 이 저장소의 파일명·시나리오 이름을 goal 이나 지시문에 쓰지 않았다(§0-5 X-2). 지시문에는 §0-16 의 기본 문구 두 개("이 디렉토리나 다른 디렉토리를 뒤지지 마라", "도구가 실패해도 재시도하거나 다른 방법을 찾지 마라")를 넣었다.

### 0.3 데몬·서버를 띄우지 않았다

`§10` 의 데몬 코드는 P4 T-D9 가 만든다(지시). 그래서 `plan/spikes/spike05/acp.py` 가 `harness.md` §2 시퀀스(`initialize` → `session/new`(claude_code 는 §3 `_meta`) → 모델 선택 → `session/prompt`)와 §4 권한 응답(`allow_once` **kind** 로 선택)만 그대로 흉내 낸다. 마커 append/제거는 `daemon/internal/brief/brief.go` 의 `Block`·`stripBlock` 과 **같은 규칙**을 Python 으로 옮겼다 — 그래서 여기서 나온 결함은 그 코드에 그대로 옮겨진다.

### 0.4 실측 중 고친 것 두 가지 (재현하려는 사람을 위해)

- **`session/set_model` 의 파라미터 이름은 `modelId` 다.** 계약 §1 은 `session/set_model "<provider>:<model>"` 라고만 적혀 있어 처음에 `{"model": ...}` 로 보냈고 Hermes 가 `-32602 Field required: modelId` 로 거절했다(구현 `daemon/internal/harness/acp/wire.go:240` 은 맞게 보내고 있다). **거절돼도 세션은 살아 있고 기본 모델로 턴이 돈다** — 조용한 모델 드리프트다. `plan/spikes/spike04c/hermes_wire.py` 도 같은 오타를 갖고 있다(그 스파이크의 판정에는 영향 없음 — 유실 감지만 봤다).
- **우회 B 의 읽기 지시문에서 "도구를 쓰지 마라"를 뺐다.** 브리프가 파일에 있는데 도구를 금지하면 모순이라 Hermes 1회가 "도구 없이는 읽을 수 없다"고 답했다. 그 4회는 `out/runs.superseded.jsonl` 로 뺐고 지시문을 고쳐 다시 4회 돌렸다. 표의 우회 B 행은 재실행분이다.

---

## 1. 케이스 × 런타임 표

| 런타임 | 레이아웃 | settingSources | 전달 방식 | n | (1) 마커 읽음 | (1b) 원본 규칙 읽음 | 도구 0 | (2) 편집됨 | (2) 커밋됨 | 커밋에 마커 섞임 | (3) 복원 후 마커 0 | (3) 원본 무손상 | (3) 편집 보존 | (3) status 클린 |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `claude_code` | plain | `empty` | 마커 append + skip-worktree | 2 | 0/2 | 0/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `claude_code` | plain | `project` | 마커 append + skip-worktree | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `hermes` | plain | `—` | 마커 append + skip-worktree | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `claude_code` | worktree | `empty` | 마커 append + skip-worktree | 2 | 0/2 | 0/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `claude_code` | worktree | `project` | 마커 append + skip-worktree | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `hermes` | worktree | `—` | 마커 append + skip-worktree | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `claude_code` | plain | `project` | 우회 A: 별도 파일 + @import | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `hermes` | plain | `—` | 우회 A: 별도 파일 + @import | 2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `hermes` | plain | `—` | 대조: 마커 append, 숨기지 않음 | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 0/2 |
| `claude_code` | plain | `project` | 대조: 마커 append, 숨기지 않음 | 2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 2/2 | 1/2 | 0/2 |
| `claude_code` | plain | `empty` | 대조: _meta.systemPrompt(계약 v1) | 2 | 2/2 | 0/2 | 2/2 | 2/2 | 2/2 | 0/2 | 2/2 | 2/2 | 2/2 | 2/2 |
| `claude_code` | plain | `empty` | 우회 B: 미추적 파일 + 턴 프롬프트 | 2 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 0/2 | 2/2 | 2/2 | 2/2 | 2/2 |
| `hermes` | plain | `—` | 우회 B: 미추적 파일 + 턴 프롬프트 | 2 | 2/2 | 2/2 | 0/2 | 2/2 | 2/2 | 0/2 | 2/2 | 2/2 | 2/2 | 2/2 |

### 에이전트가 본 커밋 결과 (turn 2 STEP2)

| 런타임 | settingSources | 전달 방식 | 에이전트가 보고한 커밋 결과 |
|---|---|---|---|
| `claude_code` | `empty` | 마커 append + skip-worktree | `nothing to commit, working tree clean` |
| `claude_code` | `project` | 마커 append + skip-worktree | `nothing to commit, working tree clean` |
| `hermes` | `—` | 마커 append + skip-worktree | `nothing to commit, working tree clean` |
| `hermes` | `—` | 마커 append + skip-worktree | `On branch main` |
| `hermes` | `—` | 마커 append + skip-worktree | `On branch colab/4ecc93e1/cataloger nothing to commit, working tree clean` |
| `claude_code` | `project` | 우회 A: 별도 파일 + @import | `nothing to commit, working tree clean` |
| `hermes` | `—` | 우회 A: 별도 파일 + @import | `On branch main\nnothing to commit, working tree clean` |
| `hermes` | `—` | 대조: 마커 append, 숨기지 않음 | `1 file changed, 16 insertions(+)` |
| `hermes` | `—` | 대조: 마커 append, 숨기지 않음 | `[main 145fac6] docs: agent note` |
| `claude_code` | `project` | 대조: 마커 append, 숨기지 않음 | `1 file changed, 16 insertions(+)` |
| `claude_code` | `empty` | 대조: _meta.systemPrompt(계약 v1) | `1 file changed, 1 insertion(+)` |
| `claude_code` | `empty` | 우회 B: 미추적 파일 + 턴 프롬프트 | `1 file changed, 1 insertion(+)` |
| `hermes` | `—` | 우회 B: 미추적 파일 + 턴 프롬프트 | `[main 503b7a2] docs: agent note` |
| `hermes` | `—` | 우회 B: 미추적 파일 + 턴 프롬프트 | `1 file changed, 1 insertion(+)` |

읽기 판정은 **도구 호출 0** 인 응답만 인정한다(우회 B 는 파일을 읽으라는 것이 지시 자체라 예외 — 그 행의 "도구 0" 이 0/2 인 것이 정상이다). `(1b) 원본 규칙 읽음` 은 저장소가 원래 갖고 있던 `PROJECT_RULE_CODE` 를 답했는가다.

---

## 2. 질문 (1) — 런타임이 마커 구간을 읽는가

### 2.1 Claude Code: **계약 v1 아래에서는 안 읽는다 — `CLAUDE.md` 를 통째로 안 읽는다**

`_meta.claudeCode.options.settingSources: []`(harness §3, G1 F2 격리) 아래에서 마커 0/4 · **원본 프로젝트 규칙도 0/4**. 응답은 네 번 다 `CODE=NONE / RULE=NONE` 이었다. `["project"]` 로 바꾸면 마커 4/4 · 원본 4/4, 도구 호출 0 으로 읽는다.

| `settingSources` | 마커 읽음 | 원본 규칙 읽음 | 뜻 |
|---|---|---|---|
| `[]` (계약 v1) | 0/4 | 0/4 | `CLAUDE.md` 가 SDK 컨텍스트에 아예 안 들어간다 |
| `["project"]` | 4/4 | 4/4 | 읽힌다. 대신 workdir `.claude/settings.json`·프로젝트 훅·스킬이 함께 열린다(1b E3) |

`contracts/harness.md` §3 표가 이미 이렇게 적어 두었고(*"지시 파일 프로파일이라면 `["project"]`(CLAUDE.md를 읽으려면 필요). v1 Claude Code는 `_meta` 경로라 `[]`"*), §10 이 *"`CLAUDE.md`를 만들거나 고치지 않는다"* 로 못박아 두었다. **실측이 그 두 문장을 확인한다.** 대조군(`_meta.systemPrompt.append`)은 `settingSources: []` 그대로 마커 2/2 · 원본 0/2 — 브리프는 도착하고 저장소 규칙은 안 보인다.

> 그래서 **스파이크 5 의 원래 질문("추적 중인 `CLAUDE.md`")은 claude_code 에서는 발생하지 않는다.** PLAN §4 가 이 스파이크를 적을 때는 §8.4 M6 의 "v1 기본 가정은 지시 파일" 이 살아 있었고, 그 뒤 스파이크 1b·3 이 `_meta` 경로를 확정하면서 전제가 바뀌었다. 남은 자리는 Hermes 하나다.

### 2.2 Hermes: **읽는다 — 추적 여부·`skip-worktree` 와 무관하게**

`AGENTS.md` 마커 4/4(평면 2 + worktree 2), 원본 규칙 4/4, 도구 호출 0. `skip-worktree` 가 걸린 상태에서도 런타임의 파일 읽기는 정상이다 — `skip-worktree` 는 **index 의 비트**이지 파일 시스템 권한이 아니다. `contracts/harness.md` §10 의 `instruction_file` 경로가 실기에서 성립한다.

---

## 3. 질문 (2) — 에이전트가 지시 파일을 정당하게 편집하면

### 3.1 편집은 된다. 커밋이 **조용히** 안 된다

`skip-worktree` 12런 전부: 파일 편집 **12/12 성공**, `git add -A && git commit` 으로 만들어진 커밋 **0/12**. 에이전트가 보고한 커밋 결과는 두 런타임 모두 한 줄로 같다.

```
STEP2=nothing to commit, working tree clean
STEP3=EMPTY
```

에이전트는 자기가 커밋했다고 믿고 턴을 끝낸다. **오류가 아니라 성공처럼 보이는 무동작**이라 데몬도 피드도 이상을 감지할 신호가 없다.

Hermes 는 여기에 한 겹이 더 있다 — 지시 파일 쓰기에 **전용 권한 게이트**가 붙는다(실측 문구):

```
Write to protected agent-instruction file(s): AGENTS.md.
These files steer future agent behavior; approval is always required (not bypassed by auto-approve).
```

이 스파이크는 §4 대로 `allow_once` 로 승인했다. 프로파일이 이 요청을 거부하도록 설정돼 있으면 에이전트는 **편집조차 못 한다**.

### 3.2 자력으로 빠져나올 길이 없다 (`gitprobe.sh` P1~P3, 런타임 없음)

| 명령 | 결과 | rc |
|---|---|---|
| `git status --porcelain` / `git diff` / `git diff HEAD` | **빈 출력** — 편집이 보이지 않는다 | 0 |
| `git add -A` | 조용히 무동작 | 0 |
| `git add <file>` | `The following paths … matched paths that exist outside of your sparse-checkout definition, so will not be updated in the index` — **sparse-checkout 을 쓰지도 않았는데 sparse-checkout 을 말한다** | 1 |
| `git add --force <file>` | 같은 메시지 | 1 |
| `git commit -m …` / `git commit -a -m …` | `On branch main / nothing to commit, working tree clean` | 1 |
| `git stash push` | `No local changes to save` — 마커도 편집도 그대로 남는다 | 0 |
| `git switch <other>` (그 파일이 저쪽에서 바뀐 경우) | `error: Your local changes to the following files would be overwritten by checkout … Aborting` | 1 |
| `git merge <other>` (같은 조건) | `error: … would be overwritten by merge … Aborting` | 1 |
| `git reset --hard HEAD` | 성공하지만 **그 파일은 건드리지 않는다** — 마커·편집 그대로 | 0 |

`switch`·`merge` 가 막힌 상태에서 `commit` 도 `stash` 도 듣지 않으므로 **에이전트는 스스로 이 교착을 풀 수 없다.** P4 시나리오 B 는 에이전트가 `colab/<session>/<agent>` 브랜치에 커밋하고, QA 가 그 diff 를 아티팩트로 받는 시나리오다.

### 3.3 실제로 유실이 어떻게 보이는가 (P7)

코드 파일과 지시 파일을 같이 고치고 한 번에 커밋하면:

```
$ git status --short
 M widgets.md                          ← 지시 파일 편집은 여기에 없다
$ git add -A && git commit -m "feat: widget + rule note"
[main 45ed5ea] feat: widget + rule note
 1 file changed, 1 insertion(+)        ← 코드 파일만
$ git status --short
                                       ← 클린. 다 커밋된 것처럼 보인다
```

지시 파일의 편집은 **작업 트리에는 남아 있지만 커밋에는 없고, `git status` 도 클린**이다. 세션이 끝나고 데몬이 마커를 제거하면 그때 비로소 `M <file>` 로 나타난다 — 아무도 보고 있지 않은 시점에.

---

## 4. 질문 (3) — 복원

`--no-skip-worktree` → 마커 구간만 제거. 12/12:

| 항목 | 결과 |
|---|---|
| 마커 잔여 0 | **12/12** |
| 원본 규칙 무손상(줄 단위 전수 대조) | **12/12** |
| 에이전트의 정당한 편집 보존 | **12/12** |
| `git status` 클린 | **0/12 — `M <file>`** |

**`git status` 가 클린하지 않은 것이 정상이다.** 에이전트가 지시 파일을 고쳤으니 남는 것이 맞고, 그 diff 는 마커 한 줄 없이 에이전트 편집만 담는다:

```diff
--- a/CLAUDE.md
+++ b/CLAUDE.md
@@ -5,3 +5,4 @@ Owned by the catalog team. Keep entries alphabetical.
 - PROJECT_RULE_CODE = ORIG-RULE-4471
 - Every catalog entry needs a `price` field.
 - Do not touch `legacy/` without a ticket.
+<!-- agent-note: NOTE-B43A -->
```

에이전트가 지시 파일을 **안 고친** 갈래(`gitprobe.sh` P5-noedit)에서는 복원 후 `git status` 가 완전히 클린하다. 따라서 PLAN §3 P4 DoD 의 *"세션 실행 후 `git status` 클린"* 은 **"마커 잔여 0"으로 읽어야 한다** — 에이전트의 정당한 편집이 있으면 클린일 수 없고, 클린이면 오히려 그 편집을 지운 것이다.

### 4.1 신규: 마커 구간이 **파일 끝**에 있으면 복원이 에이전트 편집을 지운다 (1/20)

`brief.writeMarkerBlock` 은 마커 구간을 **파일 끝에** 붙인다. 에이전트에게 "파일 끝에 한 줄 덧붙이라"고 하면 그 줄이 **마커 구간 안**으로 들어갈 수 있고, 그러면 "마커 구간만 제거"가 그 줄까지 지운다. 20런 중 1회 실측(`claude_code-plain-nohide-2-f49dbe`):

```
<!-- colab:brief:start -->
…
Goal: keep the widget catalog tidy.
<!-- agent-note: NOTE-E651 -->      ← 에이전트 편집이 구간 안으로 들어갔다
<!-- colab:brief:end -->
```

복원 후 파일에서 `NOTE-E651` 은 사라졌다. `skip-worktree` 와 무관한 **복원 규칙 자체의 구멍**이다(PRD §8.4 *"통째 복원은 그 수정을 지운다"* 가 막으려던 바로 그 유실이 마커-only 복원에서도 난다). 우회 B 를 채택하면 지시 파일에 마커를 안 넣으므로 함께 사라지지만, 마커를 남기는 어떤 안을 고르든 **구간을 파일 맨 앞에 두거나, 복원 시 구간 안의 비-브리프 줄을 구간 밖으로 옮겨야** 한다.

---

## 5. 질문 (4) — worktree 격리

**`skip-worktree` 는 worktree 마다다.** index 가 worktree 마다 따로다:

| 관측 | worktree(`colab/<session>/<agent>`) | 원본 체크아웃 |
|---|---|---|
| `git ls-files -v CLAUDE.md` | `S CLAUDE.md` | `H CLAUDE.md` |
| `git status --porcelain`(마커 append 후) | 빈 출력 | 빈 출력(그쪽 파일은 안 건드렸다) |
| index 경로 | `.git/worktrees/<name>/index` | `.git/index` |

- 원본 체크아웃 쪽에서 같은 파일을 고쳐도 worktree 의 `S` 비트는 그대로다(격리 성립).
- `git worktree remove` 하면 그 index 가 통째로 사라지므로 **비트도 함께 없어진다** — 복원 전에 데몬이 죽어도 원본 저장소에 `skip-worktree` 잔재가 남지 않는다(GC 관점에서 유일한 좋은 소식).
- **런타임 결과는 평면 체크아웃과 완전히 같다**: 읽기 Hermes 2/2 · claude_code(`project`) 2/2 · claude_code(`[]`) 0/2, 커밋 0/6, 복원 6/6. worktree 라고 나아지는 것은 없다.

---

## 6. 우회 실측

### 6.1 우회 A — 별도 파일 + `@import` 한 줄: **Hermes 에서 무효**

`CLAUDE.local.md`/`AGENTS.local.md` 를 미추적으로 두고 `.git/info/exclude` 에 등록한 뒤, 추적 파일의 마커 구간에는 `@<파일>` 한 줄만 남기는 안.

| 런타임 | 마커(=import) 로 브리프가 읽혔는가 |
|---|---|
| `claude_code` (`settingSources: ["project"]`) | **2/2** — `@CLAUDE.local.md` import 가 동작한다 |
| `hermes` | **0/2** — `@AGENTS.local.md` 를 그냥 텍스트로 본다. 원본 규칙은 2/2 로 읽는다 |

그리고 **추적 파일에 한 줄이라도 남으면 `skip-worktree` 가 여전히 필요하므로 §3 의 부작용이 그대로다**(커밋 0/4). 두 가지 이유로 탈락.

### 6.2 대조 — 마커를 append 하되 숨기지 않으면: **에이전트가 브리프를 커밋한다**

PRD §8.4 M6 의 "(b) `git status` 에 잡혀 에이전트가 커밋해버린다"를 실측했다. 4/4 전부 커밋이 성사됐고 **4/4 전부 마커 구간 16줄이 통째로 저장소 히스토리에 들어갔다**:

```
$ git show --stat --oneline HEAD
145fac6 docs: agent note
 AGENTS.md | 16 ++++++++++++++++
 1 file changed, 16 insertions(+)
```

즉 숨기지 않는 안은 **채택할 수 없다**. M6 의 걱정은 정당했다 — 문제는 그 대책(`skip-worktree`)이 반대편에 같은 크기의 유실을 만든다는 것이다.

### 6.3 우회 B — 미추적 브리프 파일 + 턴 프롬프트가 가리킴: **전부 통과**

추적 중인 지시 파일을 **한 바이트도 건드리지 않는다.** 브리프를 `<workdir>/COLAB_BRIEF.md` 에 쓰고 `.git/info/exclude` 에 등록하며, 턴 프롬프트 첫 줄이 그 파일을 읽으라고 지시한다.

| 항목 | `claude_code` | `hermes` |
|---|---|---|
| 브리프 읽힘 | 2/2 | 2/2 |
| 저장소 원본 규칙도 살아 있음 | — (`[]` 라 원래 안 읽는다) | 2/2 |
| 에이전트 편집 | 2/2 | 2/2 |
| **에이전트 커밋 성공** | **2/2** | **2/2** |
| 커밋에 브리프 섞임 | 0/2 | 0/2 |
| 세션 후 `git status` | **클린 2/2** | **클린 2/2** |

에이전트가 지시 파일을 고치고 커밋하는 것이 **평범하게 동작한다** — `1 file changed, 1 insertion(+)`. `skip-worktree` 도, 마커 제거도, 복원도 필요 없다(브리프 파일만 지우면 끝).

비용 두 가지: (a) 브리프가 **턴 프롬프트에 의존**하므로 `[1]~[5]` 바이트 동일(E12-11)은 프롬프트 쪽에서 지켜야 하고, (b) 에이전트가 그 파일을 읽는 **도구 호출 1회**가 매 턴 앞에 붙는다(실측 4/4 모두 첫 도구가 그 파일 read).

---

## 7. 권고

### 7.1 `skip-worktree` 채택 여부 — **채택하지 않는다**

읽기와 복원은 통과하지만 **에이전트의 정당한 편집을 조용히 삼키고, 브랜치 이동·병합을 자력으로 풀 수 없게 막는다.** P4 시나리오 B(코드 작업 + 커밋 + diff 아티팩트)와 정면으로 충돌한다.

### 7.2 별도 파일 우회 필요 여부 — **필요하다. 우회 B(미추적 브리프 파일 + 턴 프롬프트)**

우회 A(`@import`)는 Hermes 가 import 를 모르고, 추적 파일에 한 줄이라도 남기면 `skip-worktree` 부작용이 그대로라 탈락.

### 7.3 계약 변경 (Lead 판정 사항)

| # | 문서 | 지금 | 제안 | 근거 |
|---|---|---|---|---|
| A | `contracts/harness.md` §10 `hermes` 행 workdir 칸 | *"추적 파일이면 `skip-worktree`, 미추적이면 `.git/info/exclude`. lane 종료 시 마커 구간만 제거"* | 추적 여부와 무관하게 **`<workdir>/COLAB_BRIEF.md`(미추적, `.git/info/exclude`)** 에 브리프 전문을 쓰고 턴 프롬프트가 절대 경로로 가리킨다. lane 종료 시 파일 삭제 + exclude 등록 해제. 추적 중인 `AGENTS.md` 는 **읽지도 쓰지도 않는다** | §3 (커밋 0/12) · §6.3 (커밋 4/4) |
| B | PRD §8.4 M3 표 | "추적 중 → `skip-worktree`" 행 | 그 행을 **삭제**하고 "원본 상태와 무관하게 별도 미추적 파일" 한 행으로. "복원은 마커 구간만 제거" 문장은 A 를 받으면 불필요해진다 | 같음 |
| C | PRD §8.4 턴 프롬프트 절 | 브리프 경로 언급 없음 | `instruction_file` 런타임의 턴 프롬프트 **맨 앞**에 "먼저 `<abs>/COLAB_BRIEF.md` 를 읽어라" 한 줄을 고정한다(§8.4 [1]~[5] 바이트 동일 규칙은 그 파일 안에서 유지) | §6.3 (b) |
| D | `contracts/harness.md` §1 `hermes` 행 모델 선택 | `session/set_model "<provider>:<model>"` | 파라미터 이름 `modelId` 를 명시. **틀린 이름은 `-32602` 로 거절되지만 세션은 살아 남아 기본 모델로 턴이 돈다**(조용한 모델 드리프트) | §0.4 |
| E | `contracts/harness.md` §10 `claude_code` 행 | *"`CLAUDE.md` 를 만들거나 고치지 않는다"* | **그대로 둔다.** 실측이 근거를 보탠다: `settingSources: []` 에서는 `CLAUDE.md` 가 읽히지도 않는다(0/4). 지시 파일 경로로 바꾸려면 `["project"]` 가 필요하고 그것은 G1 F2 격리를 다시 여는 일이다 | §2.1 |

A 를 받지 않고 마커를 지시 파일에 남기는 어떤 안을 고르더라도, **§4.1(마커 구간이 파일 끝이면 복원이 에이전트 편집을 지운다)** 은 별도로 고쳐야 한다 — 구간을 파일 맨 앞에 두거나, 복원 시 구간 안의 비-브리프 줄을 밖으로 옮긴다.

### 7.4 P4 T-D9 로 넘기는 것

- `daemon/internal/brief/brief.go` 의 `Prepare`/`Remove` 를 A 안대로(별도 미추적 파일 + `.git/info/exclude` 등록·해제) 바꾼다. 지금 코드는 `AGENTS.md` 마커 append 다.
- Hermes 의 **지시 파일 전용 권한 게이트**(§3.1)를 데몬이 인지해야 한다 — A 안을 받으면 `COLAB_BRIEF.md` 는 지시 파일 이름이 아니므로 게이트에 걸리지 않을 가능성이 크지만, 실측으로 확인할 항목이다.
- worktree GC 는 `worktree remove` 가 index 를 함께 지우므로 `skip-worktree` 잔재 정리가 필요 없다(§5).

---

## 8. 재현

```bash
# 실험 대상 저장소는 $SPIKE05_WORK 아래 새로 만들어진다(기본 /private/tmp/colab-spike05).
# 이 저장소는 건드리지 않는다. 서버·데몬·Postgres 를 띄우지 않으므로 포트 충돌이 없다.
bash plan/spikes/spike05/run_all.sh        # 실기 매트릭스 20런  (~7분, haiku)
bash plan/spikes/spike05/run_controls.sh   # 대조군 nohide·meta 6런
bash plan/spikes/spike05/gitprobe.sh       # 런타임 없는 git 부수 효과 P1~P8 (~2초)
python3 plan/spikes/spike05/measure.py     # 표 생성
```

산출물은 `plan/spikes/spike05/out/`(`.gitignore` — `plan/spikes/*/out/`). 보고서에 인용한 원시 로그는 `plan/spikes/logs/spike05_runs.jsonl`·`plan/spikes/logs/spike05_gitprobe.txt`·`plan/spikes/logs/spike05_runs_superseded.jsonl` 로 커밋했다.
