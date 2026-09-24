#!/usr/bin/env bash
# e2e/p5/85_capacity_daemon.sh — T-D14 실기 스모크: D-28 capacity 창(데몬 몫).
#
# 실기 데몬(claude_code, capacity 1)에 **두 턴을 몰아** 넣고 동시 실행이 1 인지 잰다.
# T-I6 REPORT §6 실측: claim 루프의 free 가 d.running 만 세어 준비 중인 attempt 가 안 보였고, 짧은 턴이
# 몰리면 capacity 3 에 4 동시. 고친 뒤(PR T-D14)는 claim 부터 finish 보고까지 한 자리를 쥔다.
#
# 잰다:
#   C.1 두 task 가 모두 completed.
#   C.2 서버 쪽 겹침 0 — 두 attempt 의 [dispatched_at, finished_at) 이 겹치지 않는다(task_attempt).
#   C.3 데몬 로그 순서 — 첫 `finish outcome` 줄 전에 `claim` 줄이 정확히 1 개(두 번째 claim 은 finish 뒤).
#   C.4 (BEFORE=1 일 때만) origin/dev 데몬 바이너리로 같은 시나리오를 먼저 돌려 대조 — 겹침 ≥ 1 이 결함의 증거.
#
# 스택(T-D14 배정): server :8122 · pg :5466 · 컨테이너 colab-pg-d14.
# 사용: bash e2e/p5/85_capacity_daemon.sh            # 스택이 없으면 up.sh 를 같은 포트로 띄운다
#       BEFORE=1 bash e2e/p5/85_capacity_daemon.sh   # dev 바이너리 before + HEAD after (haiku 4턴)
#       SERVER_URL=http://localhost:8122 PG_PORT=5466 PG_CONTAINER=colab-pg-d14 bash e2e/p5/down.sh
# 비용: haiku 2턴(BEFORE=1 이면 4턴). 로그인된 claude_code 가 필요하다(RUNTIME=real 고정 — 페이크는 유닛이 잰다:
# daemon/internal/loop/d28_capacity_test.go).
export SERVER_URL="${SERVER_URL:-http://localhost:8122}"
export WEB_URL="${WEB_URL:-http://localhost:3024}"
export PG_PORT="${PG_PORT:-5466}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-d14}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$HERE/out/d14}"
source "$HERE/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/85-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/85-cookies.txt"; rm -f "$COOKIE"
MODEL="${LEAD_MODEL:-claude-haiku-4-5-20251001}"
# 작업 폴더는 이 저장소 밖(P4_TASKS §0-18).
WORK="${COLAB_D14_WORK:-/private/tmp/colab-d14}/$RUN"; mkdir -p "$WORK"

PIDS=()
cleanup() { local p; for p in "${PIDS[@]:-}"; do [ -n "$p" ] && daemon_stop "$p"; done; return 0; }
trap cleanup EXIT

curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 || bash "$HERE/up.sh" || die "stack did not come up"
wait_for() { local n=0; while [ $n -lt $(( $1 * 10 )) ]; do if eval "$2"; then return 0; fi; sleep 0.1; n=$((n+1)); done; return 1; }

# ───────────────────────────── 0 ─────────────────────────────────────────────
step "0. 바이너리 · 계정"
mkdir -p "$BIN"
(cd "$E2E_ROOT/daemon" && go build -o "$BIN/daemon" ./cmd/daemon) || die "daemon build"
(cd "$E2E_ROOT/cli" && go build -o "$BIN/colab" ./cmd/colab) || die "colab build"
if [ "${BEFORE:-0}" = 1 ]; then
  # origin/dev 의 데몬을 저장소 밖에서 빌드(T-S9a 레시피: git archive → go build, 워크트리를 안 건드린다).
  DEVSRC="$WORK/devsrc"; mkdir -p "$DEVSRC"
  git fetch -q origin dev
  git archive origin/dev | tar -x -C "$DEVSRC"
  (cd "$DEVSRC/daemon" && GOWORK="$DEVSRC/go.work" go build -o "$BIN/daemon-dev" ./cmd/daemon) || die "dev daemon build"
  ok "dev daemon: $(git rev-parse --short origin/dev)"
fi
signup "d14-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "D14 $RUN")"
INS="You are an assistant. Follow the session goal literally. $P4_RULES"
AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg m "$MODEL" --arg i "$INS" '{name:"Ping",role:"custom",role_description:"짧게 답한다",
  instructions:$i,profiles:[{name:"default",runtime_kind:"claude_code",model:$m,is_default:true}]}')" | jq -r .id)"
GOAL="Reply with the single word PONG and nothing else, then end your turn. Do not use any tool. $P4_RULES"

# burst LABEL DAEMON_BIN → 페어링 + capacity 1 데몬 + 세션 2개 동시 생성 + 두 attempt 종료 대기, 겹침·로그 순서를 잰다.
burst() {
  # bash 3.2 + set -u: 한 `local` 줄 안에서 앞 변수를 참조하면 unbound — 줄을 나눈다.
  local label="$1" bin="$2"
  local cfg="$WORK/daemon-$label.json" root="$WORK/root-$label" dlog="$OUT/85-daemon-$label.log"
  mkdir -p "$root"; : > "$dlog"
  local PID_ PCODE
  IFS=$'\t' read -r PID_ PCODE <<<"$(create_pairing "$WS")"
  rm -f "$cfg"
  COLAB_DAEMON_CONFIG="$cfg" "$bin" pair "$PCODE" --server "$SERVER_URL" --workdir-root "$root" 2>&1 | tail -1 >&2
  jq --arg b "$BIN/colab" '.capacity=1 | .colab_bin=$b' "$cfg" > "$cfg.tmp" && mv "$cfg.tmp" "$cfg"
  local rid; rid="$(jq -r .runtime_id "$cfg")"
  ( cd "$WORK" && export PATH="$BIN:$(stable_path)" COLAB_DAEMON_CONFIG="$cfg"; setsid_run "$dlog" "$bin" run ) > "$OUT/daemon-85-$label.pid"
  PIDS+=("$OUT/daemon-85-$label.pid")
  wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $dlog)"
  # 두 세션을 연달아 — 두 task 가 같은 claim 창에 queued 로 있게 한다.
  local s1 s2
  s1="$(create_room_work "$WS" "$(jq -nc --arg g "$GOAL" --arg a "$AG" --arg rt "$rid" \
    '{title:"D14 burst 1",goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt,
      completion_condition:{op:"and",conditions:[{type:"manual"}]}}')")"
  s2="$(create_room_work "$WS" "$(jq -nc --arg g "$GOAL" --arg a "$AG" --arg rt "$rid" \
    '{title:"D14 burst 2",goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt,
      completion_condition:{op:"and",conditions:[{type:"manual"}]}}')")"
  ok "$label: sessions $s1 $s2 (runtime $rid)"
  wait_for "${T_TURN:-600}" '[ "$(psqlq "select count(*) from task_attempt a join task t on t.id=a.task_id where t.session_id in ('"'"'$s1'"'"','"'"'$s2'"'"') and a.outcome is not null")" = 2 ]' \
    || bad "$label: two turns did not finish in time (see $dlog)"
  psqlq "select t.session_id, a.attempt, a.outcome, a.dispatched_at, a.started_at, a.finished_at from task_attempt a join task t on t.id=a.task_id where t.session_id in ('$s1','$s2') order by a.dispatched_at" \
    | tee "$OUT/85-attempts-$label.tsv" >&2
  local outcomes overlap claims_before_finish
  outcomes="$(psqlq "select string_agg(coalesce(a.outcome,'-'),',' order by a.dispatched_at) from task_attempt a join task t on t.id=a.task_id where t.session_id in ('$s1','$s2')")"
  # 겹침: 한 attempt 의 dispatched_at 이 다른 attempt 의 [dispatched_at, finished_at) 안에 있는 쌍의 수.
  overlap="$(psqlq "select count(*) from task_attempt x join task tx on tx.id=x.task_id, task_attempt y join task ty on ty.id=y.task_id
    where tx.session_id in ('$s1','$s2') and ty.session_id in ('$s1','$s2') and x.task_id <> y.task_id
      and y.dispatched_at >= x.dispatched_at and y.dispatched_at < coalesce(x.finished_at, now())")"
  claims_before_finish="$(awk '/ finish outcome=/{exit} / claim kind=/{n++} END{print n+0}' "$dlog")"
  cp "$dlog" "$OUT/85-daemon-$label.final.log" 2>/dev/null || true
  daemon_stop "$OUT/daemon-85-$label.pid"
  echo "$outcomes|$overlap|$claims_before_finish"
}

# ───────────────────────────── B (선택) ─────────────────────────────────────
if [ "${BEFORE:-0}" = 1 ]; then
  step "B. origin/dev 데몬(before) · capacity 1 · 두 턴"
  IFS='|' read -r B_OUT B_OVER B_CLAIMS <<<"$(burst before "$BIN/daemon-dev")"
  printf 'C.4\tNOTE\t-\t%s\t%s\n' "overlap=$B_OVER claims_before_finish=$B_CLAIMS outcomes=$B_OUT" "before(origin/dev): 겹침·첫 finish 전 claim 수 — 결함이면 겹침 ≥ 1 / claim 2" >> "$CHECKS"
  ok "C.4  (관측) before: outcomes=$B_OUT overlap=$B_OVER claims_before_finish=$B_CLAIMS"
fi

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. HEAD 데몬(after) · capacity 1 · 두 턴 → 동시 1"
IFS='|' read -r A_OUT A_OVER A_CLAIMS <<<"$(burst after "$BIN/daemon")"
chk C.1 "completed,completed" "$A_OUT" "두 task 모두 completed"
chk C.2 0 "$A_OVER" "서버 쪽 겹침 0 — 두 번째 attempt 의 dispatched_at 이 첫 attempt 의 finished_at 뒤"
chk C.3 1 "$A_CLAIMS" "데몬 로그: 첫 finish 전 claim 1개(두 번째 claim 은 finish 보고 뒤 — D-28)"

step "결과: $CHECKS"
cat "$CHECKS" >&2
[ "$FAILS" = 0 ] && ok "85 all pass" || die "85: $FAILS fail"
