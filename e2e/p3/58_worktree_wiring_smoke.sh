#!/usr/bin/env bash
# 58 — T-D10 실기 스모크: worktree 격리 배선 (G7 1판 차단 ①·② 의 데몬 몫)
#
# 근거: contracts/daemon-protocol.md v0.7.3 §4.1(workdir.path 절대 + 데몬 방어)·
#       §4.4(finish workdir.git)·§6(session_id·agent_id·git·bytes 필수, gc 영수증),
#       plan/G7_REPORT.md §2 차단 ①·②, PRD FR-6.4·FR-9.1
#
# 이 판이 재는 것은 **데몬 바이너리**다. 유닛이 잴 수 없는 것 두 가지 때문이다:
#   (1) 번들이 상대 경로를 실어 왔을 때 `git worktree add` 가 실제로 어디에
#       체크아웃하고, 런타임 프로세스가 실제로 어느 디렉터리에서 도는가
#   (2) §6 workdir 보고와 gc 영수증이 **와이어 위에서** 무엇을 담고 가는가
#       (G7 에서 이 두 통로는 양쪽 다 자기 규칙대로 옳고 사이에서만 틀렸다)
#
# 실험 대상 저장소는 **이 저장소가 아니다**(P4_TASKS §0-18): $WORK/repo.
# 서버·Postgres 를 띄우지 않는다 — 데몬 프로토콜만 말하는 목 서버.
# 런타임은 acpfake(= `hermes` 이름으로 PATH 에 놓은 테스트 바이너리)라
# 모델 호출이 없다. 프로세스 종료는 pid·포트만(§0-10).
#
#   bash e2e/p3/58_worktree_wiring_smoke.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${COLAB_D10_WORK:-/private/tmp/colab-p4-d10/$(date +%Y%m%d-%H%M%S)}"
OUT="$ROOT/e2e/p3/out"
PORT="${COLAB_D10_PORT:-8098}"
mkdir -p "$WORK/bin" "$OUT"
LOG="$OUT/58_smoke.log"
: > "$LOG"

SESSION="9f2b4c1e-1111-4000-8000-000000000058"
AGENT="1a3c5e70-1111-4000-8000-0000000000a5"
LANE="5b6d8f90-1111-4000-8000-0000000000b5"

wait_for() { # wait_for <seconds> <shell test>
  local n=0
  while [ $n -lt $(( $1 * 10 )) ]; do
    if eval "$2"; then return 0; fi
    sleep 0.1; n=$((n+1))
  done
  return 1
}
say() { printf '\n=== %s ===\n' "$*" | tee -a "$LOG"; }
fail=0
check() { # check <name> <condition-ok?>
  if [ "$2" = "0" ]; then printf 'PASS  %s\n' "$1" | tee -a "$LOG"
  else printf 'FAIL  %s\n' "$1" | tee -a "$LOG"; fail=$((fail+1)); fi
}
jq_state() { python3 "$WORK/state.py" "$WORK/server.jsonl" "$@"; }

cleanup() {
  [ -n "${SRVBG:-}" ] && kill "$SRVBG" 2>/dev/null
  [ -f "$WORK/server.pid" ] && kill "$(cat "$WORK/server.pid")" 2>/dev/null
  [ -f "$WORK/daemon.pid" ] && kill -9 "$(cat "$WORK/daemon.pid")" 2>/dev/null
  return 0
}
trap cleanup EXIT

# 목 서버 기록을 읽는 작은 도우미 (jq 의존을 만들지 않는다).
cat > "$WORK/state.py" <<'PYEOF'
import json, sys
kind = sys.argv[2]
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
out = [r["body"] for r in rows if r["kind"] == kind]
print(json.dumps(out[-1] if (out and len(sys.argv) > 3 and sys.argv[3] == "last") else out,
                 ensure_ascii=False, indent=None))
PYEOF

# ---------------------------------------------------------------------------
say "(0) 준비 — 임시 저장소 · 데몬 바이너리 · acpfake 를 hermes 로"
# ---------------------------------------------------------------------------
REPO="$WORK/repo"
rm -rf "$REPO"; mkdir -p "$REPO"
git -C "$REPO" init -q -b main
git -C "$REPO" config user.email d10@test
git -C "$REPO" config user.name "d10 smoke"
printf '# Widget catalog\n\n- bolt\n' > "$REPO/catalog.md"
git -C "$REPO" add -A && git -C "$REPO" commit -qm seed
check "임시 저장소 ($REPO — 이 저장소가 아니다)" $?

( cd "$ROOT/daemon" && go build -o "$WORK/daemon" ./cmd/daemon ) >>"$LOG" 2>&1
check "daemon 바이너리 빌드" $?
# acpfake 는 TestMain 에서 ACPFAKE=1 이면 스스로 ACP 서버가 된다. 그 테스트
# 바이너리를 `hermes` 라는 이름으로 PATH 앞에 두면, 데몬은 자기 코드 그대로
# `hermes acp` 를 spawn 한다 — 실기 배선을 재면서 모델 호출은 없다.
( cd "$ROOT/daemon" && go test -c -o "$WORK/bin/hermes" ./internal/loop ) >>"$LOG" 2>&1
check "acpfake 를 hermes 로 빌드 ($WORK/bin/hermes)" $?

WDROOT="$WORK/work"
mkdir -p "$WDROOT"
# 번들은 **G7 당시 서버가 보내던 모양**이다: workdir.path 가 상대
# (`<session-slug>/<agent-slug>`). v0.7.3 서버는 절대 경로를 싣지만, 데몬의
# 방어 규칙은 옛 서버·잘못된 경로에도 이 판을 살려야 한다.
python3 - "$WORK/queue.json" "$REPO" "$SESSION" "$AGENT" "$LANE" <<'QPY'
import json, sys
out, repo, session, agent, lane = sys.argv[1:6]
script = {"kind": "hermes", "no_mcp_capabilities": True,
          "turns": [{"steps": [{"chunk": "ok"}]}]}
json.dump([{
  "task": {"id": "t-58", "attempt": 1, "lane_id": lane, "session_id": session,
           "agent_id": agent, "agent_name": "backend", "trigger_message_id": "m1"},
  "task_token": "ctk_58",
  "profile": {"runtime_kind": "hermes", "model": "sonnet", "adapter_pin": "",
              "env": {"ACPFAKE": "1", "ACPFAKE_SCRIPT": json.dumps(script),
                      # 상대 경로다: 페이크가 자기 CWD 에 남긴다 →
                      # 런타임이 실제로 어느 디렉터리에서 돌았는지의 증거.
                      "ACPFAKE_RECORD": "acpfake-cwd.jsonl"}},
  "workdir": {"kind": "worktree", "repo_path": repo, "path": "sess-slug/backend", "reuse": True},
  "brief": {"transport": "instruction_file", "text": "You are Backend."},
  "prompt": "Implement the widget.", "resume": None, "limits": {"stall_seconds": 180},
}], open(out, "w"))
QPY

cat > "$WORK/daemon.json" <<CEOF
{"server_url":"http://127.0.0.1:$PORT","runtime_id":"rt-58","daemon_token":"cdt_58",
 "workdir_root":"$WDROOT","capacity":1,"repos":["$REPO"]}
CEOF

lsof -ti ":$PORT" 2>/dev/null | xargs -r kill 2>/dev/null
python3 "$ROOT/e2e/p3/mock_daemon_server.py" --port "$PORT" \
  --state "$WORK/server.jsonl" --pid "$WORK/server.pid" --queue "$WORK/queue.json" \
  --commands "$WORK/commands.json" >>"$LOG" 2>&1 &
SRVBG=$!
wait_for 20 'curl -sf -X POST "http://127.0.0.1:'"$PORT"'/v1/daemon/pair" -d "{}" >/dev/null'
check "목 서버 기동 (:$PORT)" $?

# ---------------------------------------------------------------------------
say "(1) D-21 — 상대 경로 번들 lane 1회"
# ---------------------------------------------------------------------------
# 데몬을 **저장소 안이 아닌 곳**에서 띄운다. 옛 코드는 상대 경로를 자기 CWD 로
# 절대화했으므로, CWD 를 여기로 두면 그 경로가 로그에 그대로 드러난다.
# `exec` so the pid file holds the DAEMON, not the subshell around it — a
# stale daemon from a previous판 claims the task and reports a workdir under
# the OLD root, which reads like the fix failed (measured, 2026-09-07).
( cd "$WORK" && exec env COLAB_DAEMON_CONFIG="$WORK/daemon.json" PATH="$WORK/bin:$PATH" \
  "$WORK/daemon" run >>"$LOG" 2>&1 ) &
echo $! > "$WORK/daemon.pid"

WT="$WDROOT/sess-slug/backend"
wait_for 60 '[ -d "$WT" ]'
check "체크아웃이 workdir_root 아래 절대 경로에 생겼다 ($WT) — §4.1 (a)(b)" $?
[ ! -e "$REPO/sess-slug" ]
check "사용자 저장소 안에 sess-slug 가 없다 (차단 ① 의 증상)" $?
[ -z "$(git -C "$REPO" status --porcelain)" ]
check "사용자 저장소 git status 클린 (E16-B)" $?
BR="$(git -C "$WT" symbolic-ref --short HEAD 2>/dev/null)"
[ "$BR" = "colab/$SESSION/backend" ]
check "브랜치 colab/<session>/<agent> (실제: ${BR:-없음})" $?

wait_for 90 '[ -n "$(jq_state finish 2>/dev/null | tr -d "[]")" ]'
check "finish 도달" $?
python3 - "$WORK/server.jsonl" "$WT" >>"$LOG" 2>&1 <<'PYEOF'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
fin = [r["body"] for r in rows if r["kind"] == "finish"][-1]
print("finish =", json.dumps(fin, ensure_ascii=False)[:600])
ok = fin.get("outcome") == "completed"
wd = fin.get("workdir") or {}
git = wd.get("git") or {}
ok = ok and wd.get("path") == sys.argv[2]
ok = ok and all(k in git for k in ("branch", "merged", "dirty", "commits_ahead"))
sys.exit(0 if ok else 1)
PYEOF
check "finish outcome=completed + §4.4 workdir.git{branch,merged,dirty,commits_ahead}" $?

# 런타임 프로세스가 실제로 그 체크아웃에서 돌았다: 페이크가 상대 경로로 남긴
# 기록 파일이 거기 있다 (§4.1 (c) 가 지키려는 바로 그 사실).
[ -s "$WT/acpfake-cwd.jsonl" ]
check "런타임의 실제 CWD = 체크아웃 ($WT/acpfake-cwd.jsonl)" $?
[ ! -e "$WORK/acpfake-cwd.jsonl" ] && [ ! -e "$ROOT/acpfake-cwd.jsonl" ]
check "데몬 CWD 에는 아무것도 만들지 않았다" $?
grep -q 'resolved to' "$LOG"
check "데몬 로그가 번들 경로 해석을 남겼다 (조용히 고치지 않는다)" $?

# ---------------------------------------------------------------------------
say "(2) D-22 — §6 workdir 보고"
# ---------------------------------------------------------------------------
# 에이전트가 남긴 것처럼 커밋 하나 + 미커밋 파일 하나. GC 가 판정하는 두 사실.
printf 'widget\n' > "$WT/widget.md"
git -C "$WT" add widget.md && git -C "$WT" commit -qm "feature" >>"$LOG" 2>&1
printf 'scratch\n' > "$WT/scratch.md"

# 보고를 한 번 더 받는 통로로 **없는 경로를 가리키는 gc** 를 쓴다: 데몬은
# 명령의 행(삭제할 것이 없으니 no-op)과 함께 **디스크에 남은 workdir 전부**를
# 실어 보낸다(§4.3 "해당 workdir 보고에서 삭제 확인"). probe 명령을 세우면
# 응답마다 다시 실려 어댑터를 수백 번 spawn 한다 — 첫 판에서 실측했다.
issue() { python3 -c 'import json,sys; json.dump(json.loads(sys.argv[2]), open(sys.argv[1],"w"))' "$WORK/commands.json" "$1"; }
reports() { python3 -c 'import json,sys; print(sum(1 for l in open(sys.argv[1]) if l.strip() and json.loads(l)["kind"]=="workdirs"))' "$WORK/server.jsonl"; }

N0="$(reports)"
issue "[{\"type\":\"gc\",\"session_id\":\"$SESSION\",\"workdirs\":[{\"id\":\"wd-noop\",\"path\":\"$WDROOT/no-such-dir\"}]}]"
wait_for 60 '[ "$(reports)" -gt "'"$N0"'" ]'
check "gc(no-op) 응답으로 §6 보고 도달" $?

python3 "$ROOT/e2e/p3/58_assert.py" row "$WORK/server.jsonl" "$WT" "$SESSION" "$AGENT" >>"$LOG" 2>&1
check "§6 행에 session uuid · agent_id · git{…} · bytes (차단 ②)" $?

# ---------------------------------------------------------------------------
say "(3) D-22 — gc 영수증도 같은 통로로 도달한다 (§4.3·§6)"
# ---------------------------------------------------------------------------
# 미커밋 파일이 있으면 git 이 remove 를 거부한다(E13-13) — 거부도 영수증이다.
N1="$(reports)"
issue "[{\"type\":\"gc\",\"session_id\":\"$SESSION\",\"workdirs\":[{\"id\":\"wd-58\",\"path\":\"$WT\"}]}]"
wait_for 60 '[ "$(reports)" -gt "'"$N1"'" ]'
check "gc 영수증 도달" $?
python3 "$ROOT/e2e/p3/58_assert.py" receipt "$WORK/server.jsonl" "$WT" "$SESSION" "$AGENT" refused >>"$LOG" 2>&1
check "gc 영수증 행이 refused + 신원을 싣고 온다 (§6, E13-13)" $?
[ -d "$WT" ]
check "거부된 workdir 은 그대로 있다 (미병합·미커밋 보호)" $?

# 미커밋 파일을 치우면 **재발행된** 같은 명령이 이번엔 삭제로 끝난다.
rm -f "$WT/scratch.md" "$WT/acpfake-cwd.jsonl"
N2="$(reports)"
issue "[{\"type\":\"gc\",\"session_id\":\"$SESSION\",\"workdirs\":[{\"id\":\"wd-58\",\"path\":\"$WT\"}]}]"
wait_for 60 '[ ! -d "$WT" ] && [ "$(reports)" -gt "'"$N2"'" ]'
check "재발행된 gc 가 체크아웃을 수거했다 (§4.3 재발행)" $?
python3 "$ROOT/e2e/p3/58_assert.py" receipt "$WORK/server.jsonl" "$WT" "$SESSION" "$AGENT" deleted >>"$LOG" 2>&1
check "삭제 영수증(deleted)도 같은 신원을 싣는다" $?
git -C "$REPO" rev-parse --verify --quiet "refs/heads/colab/$SESSION/backend" >/dev/null
check "브랜치는 남았다 (E13-10)" $?
rm -f "$WORK/commands.json"

printf '\n--- 산출물: %s / %s ---\n' "$LOG" "$WORK" | tee -a "$LOG"
if [ "$fail" = "0" ]; then echo "58 SMOKE: 전부 통과" | tee -a "$LOG"; else echo "58 SMOKE: 실패 $fail 건" | tee -a "$LOG"; fi
exit "$fail"
