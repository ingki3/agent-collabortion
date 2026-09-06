#!/usr/bin/env bash
# plan/spikes/spike04c/lib.sh — 스파이크 4c(콜드 스타트 정성 평가) 전용 스택.
# e2e/p2/lib.sh 의 레시피를 따르되 **포트·컨테이너·workdir 를 전부 분리**한다
# (P2_TASKS §0-13: 다른 워커 스택이 같은 머신에 떠 있다). 웹은 띄우지 않는다 — 판정이 전부 DB 다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
export SERVER_URL="${SERVER_URL:-http://localhost:8095}"
export PG_PORT="${PG_PORT:-5441}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-s4c}"
API="$SERVER_URL/api/v1"
OUT="${SPIKE_OUT:-$ROOT/plan/spikes/spike04c/out}"
BIN="$ROOT/bin"
mkdir -p "$OUT"
COOKIE="${COOKIE:-$OUT/cookies.txt}"
MODEL="${MODEL:-claude-haiku-4-5-20251001}"

log()  { printf '%s %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
step() { printf '\n▶ %s\n' "$*" >&2; }
ok()   { printf '  ✓ %s\n' "$*" >&2; }
bad()  { printf '  ✗ %s\n' "$*" >&2; }
die()  { bad "$*"; exit 1; }

psqlq() { docker exec -i "$PG_CONTAINER" psql -U colab -d colab -tA -F $'\t' -c "$1"; }
api() {
  local method="$1" path="$2" body="${3:-}"; shift 2; [ $# -gt 0 ] && shift
  if [ -n "$body" ]; then
    curl -sS -w '\n%{http_code}' -b "$COOKIE" -c "$COOKIE" -H 'Content-Type: application/json' -X "$method" "$API$path" --data "$body" "$@"
  else
    curl -sS -w '\n%{http_code}' -b "$COOKIE" -c "$COOKIE" -X "$method" "$API$path" "$@"
  fi
}
api_body() { sed '$d'; }
api_code() { tail -n1; }
api_ok() {
  local out code; out="$(api "$@")"; code="$(api_code <<<"$out")"
  case "$code" in 2*) api_body <<<"$out";; *) bad "$1 $2 → HTTP $code: $(api_body <<<"$out")"; return 1;; esac
}
uuid() { python3 -c 'import uuid; print(uuid.uuid4())'; }
setsid_run() {
  local logf="$1"; shift
  python3 - "$logf" "$@" <<'PY'
import os, sys, subprocess
logf, cmd = sys.argv[1], sys.argv[2:]
f = open(logf, "ab")
p = subprocess.Popen(cmd, stdout=f, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, start_new_session=True, env=os.environ)
print(p.pid)
PY
}
signup() { api_ok POST /auth/signup "$(jq -nc --arg e "$1" --arg p "$2" --arg n "$3" '{email:$e,password:$p,display_name:$n}')" | jq -r '.user.id // .id'; }
create_workspace() { api_ok POST /workspaces "$(jq -nc --arg n "$1" '{name:$n}')" | jq -r .id; }
create_pairing() { api_ok POST "/workspaces/$1/runtimes/pairings" '{"name":"spike4c"}' | jq -r '[.id,.pairing_token]|@tsv'; }
pairing_status() { api GET "/workspaces/$1/runtimes/pairings/$2" | api_body | jq -r .status; }
wait_pairing() { local i; for ((i=0;i<${3:-300};i++)); do [ "$(pairing_status "$1" "$2")" = ready ] && return 0; sleep 1; done; return 1; }
post_message() { api_ok POST "/sessions/$1/messages" "$(jq -nc --arg c "$2" '{content:$c}')" -H "Idempotency-Key: $(uuid)"; }
mention() { printf '[@%s](mention://agent/%s)' "$1" "$2"; }
