#!/usr/bin/env bash
# e2e/p3/41_cli_hitl_real.sh — C-4 회귀: HITL 3종이 **실서버**에 실제로 닿는지 한 번 잰다.
#
# 왜 이 스크립트가 있는가. T-C4(PR #126)의 40_ 스모크는 목 서버만 봤고, 그 목은 CLI 와 같은
# 잘못된 표(colab-cli.md v0.5 §2.4 의 `POST /v1/tasks/{T}/hitl`)에서 나왔다. 둘이 같은 오답을
# 공유하니 전부 초록이었고, 실서버에서는 **404** 였다(T-I3 발견, C-4 · #126 리뷰 NN1).
# 그러므로 이 파일은 목을 쓰지 않는다: 진짜 서버 · 진짜 라우터 · 진짜 TaskToken 이다.
#
# 에이전트 런타임은 띄우지 않는다 — 필요한 것은 **데몬이 하는 HTTP 왕복**뿐이라
# daemon-protocol.md §2·§4 를 curl 로 직접 친다(pair → claim → phase → finish).
# 그래서 모델 호출 0회, 결정적, 30초 안에 끝난다.
#
#   1. 전용 스택(Postgres :5443 + server :8097) — 다른 워커 스택에 손대지 않는다(P3_TASKS §0-13).
#   2. pair → probe → 에이전트·세션 → claim 으로 진짜 ctk_ 토큰을 받는다.
#   3. `colab hitl ask` 1회 → **201** + turn_end_required, 서버 DB 에 hitl_request 1행.
#   4. 같은 task 두 번째 요청 → **409 hitl_already_open** (E7-04).
#   5. finish(completed) → 서버가 task 를 **waiting_human** 으로 옮긴다 (FR-7.1 N4, E7-01).
#
# 사용: bash e2e/p3/41_cli_hitl_real.sh          (끝나면 스택은 down)
#       KEEP_STACK=1 bash e2e/p3/41_cli_hitl_real.sh   (스택을 남긴다)
# 산출물: e2e/p3/out-real/{run.log,hitl-201.json,hitl-409.json,checks.tsv,claim.json}

export SERVER_URL="${SERVER_URL:-http://localhost:8097}"
export PG_PORT="${PG_PORT:-5443}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-c4}"
P3_DIR="$(cd "$(dirname "$0")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P3_DIR/out-real}"
# p2/lib.sh 는 p1/lib.sh 를 부르고 API·psqlq·setsid_run 헬퍼를 준다. 웹은 쓰지 않는다.
source "$P3_DIR/../p2/lib.sh"

STAMP="$(date +%s)"
COOKIE="$OUT/cookies.txt"; rm -f "$COOKIE"
CHK="$OUT/checks.tsv"; printf 'id\twhat\tverdict\tvalue\n' > "$CHK"
pass=0; fail=0
chk() { # id 설명 기대 실제
  if [ "$3" = "$4" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %-54s %s\n' "$2" "$4" >&2; printf '%s\t%s\tPASS\t%s\n' "$1" "$2" "$4" >> "$CHK"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %-54s got=%s want=%s\n' "$2" "$4" "$3" >&2; printf '%s\t%s\tFAIL\tgot=%s want=%s\n' "$1" "$2" "$4" "$3" >> "$CHK"; fi
}
# dcurl METHOD PATH [JSON] → body, 마지막 줄 HTTP 코드 (데몬 토큰 · /v1/daemon 는 openapi 밖)
dcurl() {
  local m="$1" p="$2" b="${3:-}"
  if [ -n "$b" ]; then
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -X "$m" "${SERVER_URL%/}$p" --data "$b"
  else
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $DTOK" -X "$m" "${SERVER_URL%/}$p"
  fi
}

cleanup() {
  if [ -z "${KEEP_STACK:-}" ]; then
    [ -f "$OUT/server.pid" ] && { kill -TERM -- "-$(cat "$OUT/server.pid")" 2>/dev/null || true; }
    docker stop "$PG_CONTAINER" >/dev/null 2>&1 || true
  fi
  return 0
}
trap cleanup EXIT

step "0. 전용 스택 — Postgres($PG_CONTAINER :$PG_PORT) + server $SERVER_URL (웹 없음)"
docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
  || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
for i in $(seq 1 30); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable"
(cd "$E2E_ROOT" && COLAB_DB_URL="$DB_URL" go run ./server/cmd/migrate 2>&1 | tail -1 >&2)
(cd "$E2E_ROOT" && make build >"$OUT/build.log" 2>&1) || die "make build 실패 (see $OUT/build.log)"
if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up"; else
  COLAB_DB_URL="$DB_URL" COLAB_SERVER_URL="$SERVER_URL" COLAB_SERVER_ADDR=":${SERVER_URL##*:}" \
    setsid_run "$OUT/server.log" "$BIN/server" > "$OUT/server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/server.log)"
  ok "server pid $(cat "$OUT/server.pid")"
fi

step "1. 계정 · 워크스페이스 · 페어링"
signup "c4+$STAMP@example.com" "password123" "Director" >/dev/null
WS="$(create_workspace "C4 hitl path $STAMP")"
read -r PAIRING PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
# daemon-protocol §2. bin/daemon 을 띄우지 않고 그 왕복만 흉내낸다 — 이 스모크가 재는 것은
# 서버의 HITL 경로이지 데몬이 아니다.
PAIR_OUT="$(curl -sS -H 'Content-Type: application/json' -X POST "${SERVER_URL%/}/v1/daemon/pair" \
  --data "$(jq -nc --arg c "$PTOK" '{pairing_code:$c,hostname:"e2e-c4",os:"darwin",daemon_version:"e2e"}')")"
RUNTIME="$(jq -r .runtime_id <<<"$PAIR_OUT")"; DTOK="$(jq -r .daemon_token <<<"$PAIR_OUT")"
[ -n "$RUNTIME" ] && [ "$RUNTIME" != null ] || die "pair 실패: $PAIR_OUT"
dcurl POST "/v1/daemon/runtimes/$RUNTIME/probe" "$(jq -nc \
  '{daemon_version:"e2e",hostname:"e2e-c4",capabilities:[{runtime_kind:"claude_code",present:true}],
    repos:[],workdir_root:"/tmp/c4",disk:{used_bytes:0},colab_cli:{present:true,version:"e2e"}}')" >/dev/null
ok "runtime $RUNTIME (pairing $PAIRING)"

step "2. 에이전트 · 세션 (격리 none, 이 런타임에 고정)"
AGENT="$(create_agent_p2 "$WS" Researcher researcher "$LEAD_MODEL" '조사한다' '조사 담당')"
SESSION="$(create_session_p2 "$WS" "C4 HITL 경로" "$SCENARIO_GOAL" "$AGENT" "$RUNTIME" "$AGENT" "$AGENT")"
ok "session $SESSION · agent $AGENT"

step "3. claim — 진짜 TaskToken 을 받는다 (daemon-protocol §4.1)"
CLAIM=""
for i in $(seq 1 20); do
  CLAIM="$(dcurl POST "/v1/daemon/runtimes/$RUNTIME/claim" '{"capacity":1,"wait_ms":2000}' | sed '$d')"
  [ "$(jq -r '.tasks|length' <<<"$CLAIM")" -gt 0 ] && break
  sleep 1
done
echo "$CLAIM" > "$OUT/claim.json"
[ "$(jq -r '.tasks|length' <<<"$CLAIM")" -gt 0 ] || die "claim 이 task 를 주지 않았다: $CLAIM"
TASK="$(jq -r '.tasks[0].task.id' <<<"$CLAIM")"
ATTEMPT="$(jq -r '.tasks[0].task.attempt' <<<"$CLAIM")"
TOKEN="$(jq -r '.tasks[0].task_token' <<<"$CLAIM")"
LANE="$(jq -r '.tasks[0].task.lane_id' <<<"$CLAIM")"
ok "task $TASK attempt $ATTEMPT token ${TOKEN:0:12}…"
dcurl POST "/v1/daemon/tasks/$TASK/attempts/$ATTEMPT/phase" '{"phase":"preparing","pgid":0,"workdir_path":"/tmp/c4"}' >/dev/null
dcurl POST "/v1/daemon/tasks/$TASK/attempts/$ATTEMPT/phase" '{"phase":"running","pgid":0,"workdir_path":"/tmp/c4"}'   >/dev/null

# 데몬이 attempt 프로세스에 넣는 환경 (harness.md §2.1 / colab-cli.md §1).
colab_env() {
  env COLAB_TASK_TOKEN="$TOKEN" COLAB_SERVER_URL="$SERVER_URL" \
      COLAB_TASK_ID="$TASK" COLAB_TASK_ATTEMPT="$ATTEMPT" COLAB_LANE_ID="$LANE" \
      COLAB_SESSION_ID="$SESSION" COLAB_AGENT_NAME=Researcher "$@"
}

step "4. colab hitl ask — 실서버 201 (C-4 가 여기서 404 였다)"
set +e
colab_env "$BIN/colab" hitl ask --question "독자는 누구인가?" --default "투자자" --context "브리프에 없다" \
  > "$OUT/hitl-201.json" 2>"$OUT/hitl-201.err"
CODE=$?
set -e
chk C4-1 "hitl ask 종료 코드" 0 "$CODE"
[ "$CODE" = 0 ] || { echo "--- stderr ---" >&2; cat "$OUT/hitl-201.err" >&2; }
chk C4-2 "turn_end_required" true "$(jq -r '.turn_end_required // "-"' "$OUT/hitl-201.json" 2>/dev/null)"
HITL_ID="$(jq -r '.hitl_id // "-"' "$OUT/hitl-201.json" 2>/dev/null)"
# 서버 DB 가 진짜로 행을 하나 만들었는가 — 201 을 흉내낼 수 있는 목은 여기에 없다.
chk C4-3 "hitl_request 1행(open, source=agent)" "1" \
  "$(psqlq "select count(*) from hitl_request where session_id='$SESSION' and task_id='$TASK' and status='open' and source='agent'")"
chk C4-4 "그 행의 id 가 CLI 가 출력한 hitl_id" "$HITL_ID" \
  "$(psqlq "select id from hitl_request where session_id='$SESSION' and task_id='$TASK' limit 1")"
chk C4-5 "type=question · proposed_default 저장" "question|투자자" \
  "$(psqlq "select type || '|' || coalesce(proposed_default,'-') from hitl_request where id='$HITL_ID'" 2>/dev/null)"
# 등록 직후에는 아직 running 이다 — waiting_human 은 turn_end 가 와야 한다(FR-7.1 N4).
chk C4-6 "등록 직후 task 는 아직 running" running "$(psqlq "select status from task where id='$TASK'")"

step "5. 같은 task 두 번째 요청 → 409 hitl_already_open (E7-04)"
set +e
colab_env "$BIN/colab" hitl approve-request --summary "배포해도 되나" > "$OUT/hitl-409.json" 2>"$OUT/hitl-409.err"
CODE2=$?
set -e
chk C4-7 "두 번째 요청 종료 코드 3" 3 "$CODE2"
chk C4-8 "서버 코드 hitl_already_open" hitl_already_open "$(jq -r '.error.code // "-"' "$OUT/hitl-409.json" 2>/dev/null)"

step "6. finish(completed) → 서버가 waiting_human 으로 옮긴다 (E7-01)"
dcurl POST "/v1/daemon/tasks/$TASK/attempts/$ATTEMPT/finish" \
  '{"outcome":"completed","stop_reason":"end_turn","usage":{"input_tokens":0,"output_tokens":0,"cost_usd":0,"estimated":true},"last_seq":0}' \
  | sed '$d' > "$OUT/finish.json"
for i in $(seq 1 20); do
  ST="$(psqlq "select status from task where id='$TASK'")"; [ "$ST" = waiting_human ] && break; sleep 0.5
done
chk C4-9 "turn_end 후 task = waiting_human" waiting_human "$ST"

printf '\n'
column -t -s $'\t' "$CHK" >&2
if [ "$fail" = 0 ]; then printf '\033[32mREAL SMOKE PASS\033[0m — %d/%d (server %s)\n' "$pass" "$((pass+fail))" "$SERVER_URL"
else printf '\033[31mREAL SMOKE FAIL\033[0m — %d건 실패 (%d 통과)\n' "$fail" "$pass"; fi
exit "$fail"
