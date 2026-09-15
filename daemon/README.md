# colab-daemon — 사용자 컴퓨터에서 도는 데몬 (스트림 D)

서버와 말하는 규칙은 `contracts/daemon-protocol.md`, 런타임(Claude Code · Hermes)과 말하는 규칙은
`contracts/harness.md` 다. 이 문서는 **운영자가 손대는 것**만 적는다 — 명령줄과 `daemon.json` 의 키.

## 명령

```sh
colab-daemon pair <code> --server <url>   # S12 「컴퓨터 연결」이 준 코드로 페어링, 곧바로 probe
colab-daemon run                          # 고아 정리 → probe → claim 루프 (포그라운드)
colab-daemon probe [--turn]               # probe 본문을 서버 없이 출력 (진단)
colab-daemon repos add|remove <path>      # worktree 격리에 쓸 git 저장소 등록/해제
colab-daemon repos list                   # 등록된 저장소를 probe 가 광고하는 모양으로 출력
colab-daemon version
```

설치는 서버가 서비스하는 `curl <서버>/install.sh | sh` 로 한다(S12 안내). 바이너리 둘(`colab-daemon`, `colab`)이
같은 디렉터리에 놓이고 데몬은 옆의 `colab` 을 에이전트의 MCP 서버·CLI 로 쓴다.

### `repos add` — 저장소 등록 (D-20)

`worktree` 격리 세션은 이 컴퓨터의 git 저장소에 `git worktree add` 로 작업 폴더를 만든다. 어느 저장소를 쓸 수
있는지는 데몬이 probe 의 `repos[]`(daemon-protocol §3)로 광고하고, 웹의 세션 만들기(S6)가 그 목록으로 컴퓨터를
고르며, 다른 컴퓨터로 세션을 옮길 때(S17 재연결)도 `remote_url` 이 같은 저장소가 있는 컴퓨터만 후보가 된다.

```sh
colab-daemon repos add ~/dev/app       # 저장소 안 어느 경로든 된다 — 최상위 경로가 절대 경로로 저장된다
colab-daemon repos list
colab-daemon repos remove ~/dev/app
```

- git 작업 트리 안이 아니면 거부한다. 같은 저장소를 두 번 넣어도 한 번만 저장된다.
- 이미 `worktree` 세션을 돌린 저장소는 등록하지 않아도 자동으로 광고된다. 이 명령은 **아직 한 번도 안 쓴** 저장소를
  위한 것이다 — 그것이 없으면 새 컴퓨터는 영영 세션을 넘겨받을 후보가 되지 못한다.
- 돌고 있는 데몬은 **다음 probe** 에 반영한다(시작 시 · 하루 1회 · 서버의 probe 명령). 재시작할 필요는 없다 —
  probe 직전에 `daemon.json` 을 다시 읽는다.

## `daemon.json`

기본 위치 `~/.colab/daemon.json`(`$COLAB_DAEMON_CONFIG` 로 바꿀 수 있다). 페어링이 쓰는 `server_url` ·
`runtime_id` · `daemon_token` 은 손대지 않는다. 운영자가 고치는 키:

| 키 | 뜻 | 기본 |
|---|---|---|
| `workdir_root` | 작업 폴더의 기준. lane 폴더 `sessions/<세션>/<lane>`, worktree, 그리고 `.colab/`(pgid 기록 · 로그 · 래퍼 · 테스트 채팅 임시 폴더)이 이 아래 생긴다 | `~/.colab/work` |
| `capacity` | 동시에 돌릴 attempt 수(데몬 상한, FR-6.3) | `10` |
| `repos` | `repos add` 가 채우는 저장소 목록 (위) | 없음 |
| `colab_bin` | 에이전트에게 주는 `colab` 실행 파일 | 데몬 옆의 `colab`, 없으면 PATH |
| `stderr_dir` | attempt 마다 런타임 stderr 를 남기는 곳 | `<workdir_root>/.colab/logs` |
| `log_level` | `run` 진행 로그의 상세 수준 — 아래 | `info` |
| `usage_midturn` | claude_code 원시 스트림(턴 중 usage + 긴 도구 입력 중 활동 신호) 끄기 스위치 — 아래 | 켜짐 |

### `log_level` — 진행 로그 (D-24)

`colab-daemon run` 은 진행 로그를 **stdout** 에 쓴다(probe 는 stdout 이 JSON 이라 stderr). 수준은 둘이다.

- `info`(기본): attempt 하나에 열 줄 안팎 — claim · workdir · phase preparing/running · stall 워처가 무엇을 세는지 ·
  turn(결과·사유·usage) · finish · workdir 보고. 명령 수신(cancel · gc · rebind_prepare), 서버 오류(같은 오류가
  반복되면 1 · 10 · 100 · 1000번째만, 회복 시 몇 번 실패했는지 한 줄).
- `debug`: 위에 더해 task_event **한 줄씩**(도구 호출 전부), 빈 long-poll, §4.3 재발행 중복. 한 턴에 수백 줄이라
  진단할 때만 켠다.

```sh
COLAB_DAEMON_LOG=debug colab-daemon run     # 환경 변수가 daemon.json 의 log_level 보다 우선한다
```

stall 이 났을 때 로그에 남는 두 줄:

```
<task>.<attempt> stall watch armed limit=3m0s counts=session/update,request_permission,raw:_claude/sdkMessage(on)
<task>.<attempt> stall fired idle=3m0s limit=3m0s tool_in_progress=edit(essay.md) counted=raw:stream_event=873,session/update:tool_call=1,… last=raw:stream_event
```

첫 줄은 워처가 **무엇을 활동으로 세는지**, 둘째 줄은 발화 순간까지 무엇을 몇 번 봤는지다. `counted=nothing` 이면
프로세스가 죽은 것이고, 원시 스트림만 잔뜩 세었다면 모델이 긴 도구 입력을 만들고 있었던 것이다(S-66).

### `usage_midturn` — claude_code 원시 스트림

claude_code 어댑터의 원시 SDK 스트림(`_claude/sdkMessage`)은 두 가지에 쓰인다: (1) 턴 **중간** usage 를
heartbeat 에 실어 서버의 예산 검사(FR-7.3)가 턴이 끝나기 전에 동작하게 하고, (2) 모델이 긴 도구 입력(수십 KB 의
Write)을 생성하는 동안 — 이때 어댑터는 `session/update` 를 하나도 보내지 않는다 — stall 워처에게 "살아 있다"는
신호가 된다(harness §7 v0.8.9). 실측(2026-09-13, sonnet-5): 17 KB Write 동안 `session/update` 공백 101초, 원시 스트림 최대 간격 2초.

기본은 **켜짐**이고 예산이 없는 세션에서도 켜진다. `"usage_midturn": false` 로 끄면 (1)(2) 둘 다 꺼진다 — 예산은
턴 끝에서만 강제되고, 3분 넘게 걸리는 긴 도구 입력은 stall 로 잘린다. 비용은 로컬 stdio 파이프의 메시지 4배 ·
바이트 2배(PR #145 실측)이고 서버 트래픽은 늘지 않는다. hermes 에는 이런 스트림이 없다(harness §7).

### 역할별 colab 명령 (K-19, harness §10 v0.8.10)

번들 `task.allowed_commands`(daemon-protocol §4.1 v0.8.2)는 역할이 쓸 수 있는 colab 명령의 부분집합이다(colab-cli §2.5,
서버가 정한다). 데몬은 같은 목록을 세 곳에 놓는다 — colab MCP 서버 argv `mcp serve --allow a,b,…`(claude_code; CLI 가
그 툴만 등록), hermes 래퍼의 `export COLAB_ALLOWED_COMMANDS=a,b,…`(CLI 가 exit 3 으로 거부), 브리프 [2] 끝의 두 줄(허용
명령 목록 + "이 역할은 … 을 쓰지 않는다" — 막힌 명령은 명령 이름이 아니라 사람 말로, `internal/commands`). 비어 있으면
전부(옛 서버·lead·custom)이고 아무것도 바뀌지 않는다. 턴이 끝나면 raw system/init 의 콜랩 툴 목록을 로그에 남긴다
(`colab tools registered: …`) — `--allow` 가 툴 목록까지 닿았는지 보는 자리.

## 디렉터리 (`<workdir_root>/.colab/`)

| 경로 | 무엇 | 정리 |
|---|---|---|
| `attempts/<task>.<attempt>.json` | 살아 있는 프로세스 그룹 기록(FR-9.1) | 정상 종료 시 삭제, 시작 시 고아 정리 |
| `bin/<task>.<attempt>/colab` | hermes 용 CLI 래퍼(harness §10) — 토큰이 들어 있다 | finish 시 삭제, 시작 시 일괄 삭제 |
| `workdirs/` | §6 보고에 쓰는 작업 폴더 신원 사이드카 | gc 시 삭제 |
| `logs/` | attempt 별 런타임 stderr | 남는다 |
| `rebind/<session>/` | 재연결 시 내려받은 아티팩트 | — |
| `testchat/<test_chat_id>/` | 테스트 채팅(S10, daemon-protocol §4.5) 임시 폴더 — 토큰 없는 턴이 여기서 돈다 | 채팅을 닫으면 서버 gc 로 삭제, 시작 시 24h 넘은 것 삭제 |
