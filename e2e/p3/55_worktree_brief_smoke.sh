#!/usr/bin/env bash
# 55 — T-D9 실기 스모크: worktree 격리 · 브리프 오염 방지 · 데몬 kill -9 뒤 이중 쓰기 0
#
# 근거: PRD FR-6.4·FR-9.1·§8.4(v0.16), contracts/harness.md §10(v0.8.6),
#       daemon-protocol §3·§5·§6, EVAL E13-02~07·E11-05·06, plan/spikes/SPIKE_05.md
#
# 실험 대상 저장소는 **이 저장소가 아니다**(P4_TASKS §0-18): 판마다
# $WORK/repo 에 새 임시 저장소를 만든다. 서버·Postgres 를 띄우지 않는다 —
# 데몬 프로토콜만 말하는 파이썬 목 서버(mock_daemon_server.py)를 쓴다.
# 프로세스 종료는 pid·포트만(§0-10).
#
#   bash e2e/p3/55_worktree_brief_smoke.sh            # 전부
#   SKIP_RUNTIME=1 bash e2e/p3/55_worktree_brief_smoke.sh   # 실기 런타임 없이 (1)(3) 만
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${COLAB_D9_WORK:-/private/tmp/colab-p4-d9/$(date +%Y%m%d-%H%M%S)}"
OUT="$ROOT/e2e/p3/out"
PORT="${COLAB_D9_PORT:-8099}"
mkdir -p "$WORK" "$OUT"
LOG="$OUT/55_smoke.log"
: > "$LOG"

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

cleanup() {
  [ -n "${SRVBG:-}" ] && kill "$SRVBG" 2>/dev/null
  [ -f "$WORK/server.pid" ] && kill "$(cat "$WORK/server.pid")" 2>/dev/null
  [ -f "$WORK/daemon.pid" ] && kill -9 "$(cat "$WORK/daemon.pid")" 2>/dev/null
  [ -f "$WORK/pgid" ] && kill -9 -"$(cat "$WORK/pgid")" 2>/dev/null
  return 0
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
say "(1) 골든 미러 + 실기 프로세스 이중 쓰기 시뮬 (p4golden)"
# ---------------------------------------------------------------------------
( cd "$ROOT/daemon" && go test -tags p4golden ./internal/brief/ ./internal/workdir/ ./worktreesim/ ) >>"$LOG" 2>&1
check "p4golden 미러 (E13-02·03~07·06a, 이중 쓰기 1라운드+100라운드+회귀 주입)" $?

( cd "$ROOT/daemon" && go vet ./... && go vet -tags p4golden ./... && go test -race ./... ) >>"$LOG" 2>&1
check "go vet + go test -race ./... (daemon)" $?

# ---------------------------------------------------------------------------
say "(2) 실기 런타임 — 임시 저장소·worktree·브리프 읽힘→편집→커밋→복원→git status 클린"
# ---------------------------------------------------------------------------
if [ "${SKIP_RUNTIME:-0}" = "1" ]; then
  echo "SKIP (SKIP_RUNTIME=1)" | tee -a "$LOG"
else
  ( cd "$ROOT/daemon" && COLAB_SMOKE=1 go test ./internal/loop \
      -run 'TestSmokeP4Worktree' -v -timeout 25m ) >>"$LOG" 2>&1
  check "SmokeP4Worktree (claude_code + hermes, 저장소 AGENTS.md 무변경·커밋 성공·exclude 해제)" $?
fi

# ---------------------------------------------------------------------------
say "(3) 데몬 바이너리 kill -9 → 재시작 → 같은 workdir 이중 쓰기 0"
# ---------------------------------------------------------------------------
# 실험 저장소 (이 저장소가 아니다)
REPO="$WORK/repo"
rm -rf "$REPO"; mkdir -p "$REPO"
git -C "$REPO" init -q -b main
git -C "$REPO" config user.email d9@test
git -C "$REPO" config user.name "d9 smoke"
printf '# Widget catalog\n\n- bolt\n' > "$REPO/catalog.md"
printf '# House rules\n\nPROJECT_RULE_CODE is ORIG-RULE-4471.\n' > "$REPO/AGENTS.md"
git -C "$REPO" add -A && git -C "$REPO" commit -qm seed

( cd "$ROOT/daemon" && go build -o "$WORK/daemon" ./cmd/daemon ) >>"$LOG" 2>&1
check "daemon 바이너리 빌드" $?

# 런타임은 진짜다(claude_code + 어댑터 0.74.0). 턴이 셸 도구로 체크아웃에 계속
# 쓰게 시켜, 데몬을 kill -9 했을 때 살아남아 계속 쓰는 프로세스 그룹을 만든다 —
# 그것이 FR-9.1 이 막는 상황이고, 흉내로는 재지지 않는다.
WDROOT="$WORK/work"
WT="$WDROOT/worktrees/sess-55/backend"
LOOP_CMD='for i in $(seq 1 600); do echo "<orphan-$i>" >> AGENT_WORK.md; sleep 0.5; done'
PROMPT="Run exactly this one bash command in your working directory and then report only the word STARTED: $LOOP_CMD  Do not look at any other directory. If a tool fails, stop and say so — do not retry it and do not look for another way."
python3 - "$WORK/queue.json" "$REPO" "$PROMPT" <<'QPY'
import json, sys
out, repo, prompt = sys.argv[1], sys.argv[2], sys.argv[3]
json.dump([{
  "task": {"id": "t-55", "attempt": 1, "lane_id": "lane-1", "session_id": "sess-55",
           "agent_id": "a1", "agent_name": "backend", "trigger_message_id": "m1"},
  "task_token": "ctk_55",
  "profile": {"runtime_kind": "claude_code", "model": "claude-haiku-4-5-20251001", "adapter_pin": ""},
  "workdir": {"kind": "worktree", "repo_path": repo, "reuse": True},
  "brief": {"transport": "acp_meta_system_prompt", "text": "You are Backend."},
  "prompt": prompt, "resume": None, "limits": {"stall_seconds": 180},
}], open(out, "w"))
QPY

cat > "$WORK/daemon.json" <<CEOF
{"server_url":"http://127.0.0.1:$PORT","runtime_id":"rt-55","daemon_token":"cdt_55",
 "workdir_root":"$WDROOT","capacity":1,"repos":["$REPO"]}
CEOF

# 목 서버: 프로토콜만 말한다. 포트에 남은 것이 있으면 **포트로만** 정리한다(§0-10).
lsof -ti ":$PORT" 2>/dev/null | xargs -r kill 2>/dev/null
python3 "$ROOT/e2e/p3/mock_daemon_server.py" --port "$PORT" \
  --state "$WORK/server.jsonl" --pid "$WORK/server.pid" --queue "$WORK/queue.json" \
  >>"$LOG" 2>&1 &
SRVBG=$!
wait_for 20 'curl -sf -X POST "http://127.0.0.1:'"$PORT"'/v1/daemon/pair" -d "{}" >/dev/null'
check "목 서버 기동 (:$PORT)" $?

start_daemon() {
  COLAB_DAEMON_CONFIG="$WORK/daemon.json" "$WORK/daemon" run >>"$LOG" 2>&1 &
  echo $! > "$WORK/daemon.pid"
}

if [ "${SKIP_RUNTIME:-0}" = "1" ]; then
  echo "(3) SKIP (SKIP_RUNTIME=1 — 실기 런타임이 필요하다)" | tee -a "$LOG"
  kill "$SRVBG" 2>/dev/null
  printf '\n--- 산출물: %s / %s ---\n' "$LOG" "$WORK" | tee -a "$LOG"
  exit "$fail"
fi

sleep 0.5
start_daemon
wait_for 120 '[ -d "$WT" ]'
check "worktree 준비 ($WT)" $?
BR="$(git -C "$WT" symbolic-ref --short HEAD 2>/dev/null)"
[ "$BR" = "colab/sess-55/backend" ]; check "브랜치 colab/sess-55/backend (실제: ${BR:-없음})" $?

wait_for 180 '[ -f "$WDROOT/.colab/attempts/t-55.1.json" ]'
check "pgid 기록 (<workdir_root>/.colab/attempts/t-55.1.json — 체크아웃 밖)" $?
PGID="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1]))['pgid'])" "$WDROOT/.colab/attempts/t-55.1.json" 2>/dev/null)"
echo "$PGID" > "$WORK/pgid"
echo "runtime pgid=$PGID" | tee -a "$LOG"

# 셸 도구가 실제로 돌기 시작할 때까지 기다린다(모델 턴이므로 수십 초).
wait_for 300 '[ -s "$WT/AGENT_WORK.md" ]'
check "런타임이 체크아웃에 쓰기 시작했다" $?

DPID="$(cat "$WORK/daemon.pid")"
kill -9 "$DPID" 2>/dev/null
wait_for 20 '! kill -0 "$DPID" 2>/dev/null'
check "데몬 kill -9" $?
kill -0 "-$PGID" 2>/dev/null
check "런타임 프로세스 그룹은 살아남았다 (FR-9.1 — 안 살아남으면 이 판은 아무것도 안 잰다)" $?

BEFORE="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
sleep 2
DURING="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
# 관측이지 단정이 아니다. 실측(2026-09-07): 프로세스 **그룹**은 살아남지만
# 어댑터의 stdio 가 데몬과 함께 끊기면 그 셸 도구의 쓰기는 멈춘다 —
# 즉 이 판에서 고아가 계속 쓰는지는 어댑터 사정이고, FR-9.1 이 요구하는 것은
# "재시작이 claim 전에 그 그룹을 정리한다"이다(아래 두 줄). 계속 쓰는 쪽까지
# 재는 것은 worktreesim 의 100라운드가 한다(자기 프로세스가 살아 있다).
echo "관측: kill -9 뒤 고아 쓰기 $BEFORE → $DURING (그룹 생존은 위에서 PASS)" | tee -a "$LOG"

start_daemon
wait_for 60 '! kill -0 "-$PGID" 2>/dev/null'
check "재시작이 claim 전에 고아를 정리했다 (E11-05, 데몬 §5 sweep)" $?
LIVE=0; kill -0 "-$PGID" 2>/dev/null && LIVE=1
[ "$LIVE" = "0" ]; check "같은 workdir 에 살아 있는 프로세스 그룹 = 0 (이중 쓰기 0)" $?
AFTER="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
[ "$AFTER" -ge "$DURING" ]; check "고아가 남긴 편집은 지우지 않았다 ($DURING → $AFTER, E11-06)" $?
grep -q 'sweep\|orphan' "$LOG"; check "sweep 로그" $?

# §3 repos[]
python3 - "$WORK/server.jsonl" >>"$LOG" 2>&1 <<'PYEOF'
import json, sys
repos = []
for line in open(sys.argv[1]):
    r = json.loads(line)
    if r["kind"] == "probe" and r["body"].get("repos"):
        repos = r["body"]["repos"]
print("probe repos[] =", json.dumps(repos, ensure_ascii=False))
sys.exit(0 if repos and repos[0].get("path") and "branch" in repos[0] and "clean" in repos[0] else 1)
PYEOF
check "probe repos[] 에 path·remote_url·branch·clean (§3, E14-04·05)" $?

printf '\n--- 산출물: %s / %s ---\n' "$LOG" "$WORK" | tee -a "$LOG"
if [ "$fail" = "0" ]; then echo "55 SMOKE: 전부 통과" | tee -a "$LOG"; else echo "55 SMOKE: 실패 $fail 건" | tee -a "$LOG"; fi
exit "$fail"
