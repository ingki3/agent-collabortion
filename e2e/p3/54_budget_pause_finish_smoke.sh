#!/usr/bin/env bash
# e2e/p3/54_budget_pause_finish_smoke.sh — T-S9a 실서버 스모크: S-50 "예산으로 paused 된
# task 의 finish 가 500" + S-51 "턴 종료와 경합한 취소".
#
# G6 2판 §9.5 가 실측한 모양을 그대로 재현한다 — 서버가 턴 중에 예산 초과를 잡아 task 를
# paused(budget) 로 두고 §8.2.2 cancel 명령을 걸면, 데몬이 attempt 를 닫고 보내는
# finish{outcome: paused_budget} 이 500 이었다(task_paused_detail_check, SQLSTATE 23514).
# 고치기 전 origin/dev 에서 이 스크립트는 arm A 의 finish 에서 FAIL 로 결함을 재현하고
# (attempt 기록·runtime_session_ref 유실 → 승인 뒤 재개가 콜드 스타트), 고친 뒤에는 FAIL 0 이다.
#
#   arm A  예산 초과(턴 중) → paused(budget) → finish 200 → attempt 기록 · lane.runtime_session_ref
#          → HITL 승인 → attempt 2 번들에 resume(같은 ref) → resumed=true      (S-50 (a), E9-01·02)
#   arm B  paused(budget) 인 task 를 세션 취소로 끝낸다 → 200 · paused_detail NULL  (S-50 (b), 0006)
#   arm C  취소 명령이 걸린 뒤 턴이 스스로 끝난다(finish completed) → task completed ·
#          피드에 "취소 요청이 턴 종료와 경합해 적용되지 않음" · 명령 소비            (S-51)
#
# 전용 스택(다른 워커와 겹치지 않게 — P2_TASKS §0-13, 이 워커 포트 :8102 · pg :5447):
#   docker run -d --name colab-pg-s9a -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab \
#     -e POSTGRES_DB=colab -p 5447:5432 postgres:16-alpine
#   COLAB_DB_URL="postgres://colab:colab@localhost:5447/colab?sslmode=disable" go run ./server/cmd/migrate
#   COLAB_DB_URL=... COLAB_SERVER_ADDR=:8102 COLAB_SERVER_URL=http://127.0.0.1:8102 ./bin/server
# 종료는 pid 로만 (§0-10).
#
# 사용: bash e2e/p3/54_budget_pause_finish_smoke.sh
set -uo pipefail
RUNID=$(uuidgen | tr "[:upper:]" "[:lower:]" | cut -c1-8)
H=http://127.0.0.1:8102
S=$H/api/v1
PG=colab-pg-s9a
J="${TMPDIR:-/tmp}/s50-jar-$RUNID.txt"; rm -f "$J"; trap 'rm -f "$J"' EXIT
FAILED=0
ok(){ printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad(){ printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED+1)); }
step(){ printf '\n\033[1m== %s\033[0m\n' "$*"; }
api(){ curl -sS -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }
Q(){ docker exec $PG psql -U colab -d colab -tAc "$1" | tr -d ' '; }

step "0. 계정·워크스페이스·데몬 페어링"
api -X POST "$S/auth/signup" -d '{"display_name":"Dir","email":"s50-'$RUNID'@example.com","password":"password123"}' >/dev/null
WS=$(api -X POST "$S/workspaces" -d '{"name":"S50"}' | jq -r .id)
AG=$(api -X POST "$S/workspaces/$WS/agents" -d '{"name":"R","role":"researcher","role_description":"d","instructions":"i","budget_per_task":0.002,"profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-sonnet-5"}]}' | jq -r .id)
CODE=$(api -X POST "$S/workspaces/$WS/runtimes/pairings" -d '{"name":"mac-s50"}' | jq -r .pairing_token)
RT=$(curl -sS -X POST "$H/v1/daemon/pair" -H 'Content-Type: application/json' \
      -d "{\"pairing_code\":\"$CODE\",\"hostname\":\"mac-s50\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")
RID=$(echo "$RT" | jq -r .runtime_id); DTOK=$(echo "$RT" | jq -r .daemon_token)
[ "$RID" != null ] && ok "runtime $RID paired" || { bad "pair: $RT"; exit 1; }
curl -sS -X POST "$H/v1/daemon/runtimes/$RID/probe" -H "Authorization: Bearer $DTOK" \
  -H 'Content-Type: application/json' -d '{"runtimes":[{"kind":"claude_code","available":true,"version":"1.0.0","capabilities":{"usage":true,"resume":true}}]}' >/dev/null

D(){ curl -sS -X POST "$H/v1/daemon/$1" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d "$2"; }
# newSession <name> → SESS
newSession(){
  api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"$1\",\"goal\":\"g\",\"isolation\":{\"kind\":\"none\"},\"assignee_agent_id\":\"$AG\",\"participants\":[{\"agent_id\":\"$AG\"}],\"runtime_id\":\"$RID\"}" | jq -r .id
}
# runOne <sess> → "<task> <attempt>"  (멘션 → claim → preparing → running)
runOne(){
  local sess=$1 task att
  task=$(api -X POST "$S/sessions/$sess/messages" -d "{\"content\":\"[@R](mention://agent/$AG) 부탁합니다\"}" | jq -r '.triggers[0].task_id')
  att=$(D "runtimes/$RID/claim" '{"capacity":5}' | jq -r --arg t "$task" '.tasks[]|select(.task.id==$t)|.task.attempt')
  D "tasks/$task/attempts/$att/phase" '{"phase":"preparing","workdir_path":"/tmp/s50"}' >/dev/null
  D "tasks/$task/attempts/$att/phase" '{"phase":"running","workdir_path":"/tmp/s50"}' >/dev/null
  echo "$task $att"
}

# ───────────────────────────── arm A — S-50 (a) ─────────────────────────────
step "A1. 예산 초과를 턴 중 하트비트로 (실측 \$0.05 > budget_per_task \$0.002)"
SA=$(newSession "A"); read -r TA AA <<<"$(runOne "$SA")"
D "tasks/$TA/attempts/$AA/heartbeat" '{"usage":{"input_tokens":12000,"output_tokens":3000,"cost_usd":0.05,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null
TS=$(Q "SELECT status||'('||COALESCE(paused_reason::text,'')||')' FROM task WHERE id='$TA'")
[ "$TS" = "paused(budget)" ] && ok "task = $TS — 전제(E9-01)" || bad "task = $TS, want paused(budget)"
CC=$(Q "SELECT count(*) FROM daemon_command WHERE task_id='$TA' AND type='cancel' AND payload->>'reason'='budget'")
[ "$CC" = 1 ] && ok "budget cancel 명령 1 (§8.2.2) — 이 명령이 결함의 방아쇠였다" || bad "cancel = $CC"
PD=$(Q "SELECT paused_detail IS NOT NULL FROM task WHERE id='$TA'")
[ "$PD" = t ] && ok "paused_detail 채워짐 — 0006 CHECK 의 짝" || bad "paused_detail = $PD"

step "A2. 데몬이 §5 로 attempt 를 닫고 finish{paused_budget} — 여기가 500 이었다"
FIN=$(D "tasks/$TA/attempts/$AA/finish" '{"outcome":"paused_budget","stop_reason":"budget","runtime_session_ref":{"runtime_kind":"claude_code","session_id":"acp-s50-A","cwd":"/tmp/s50","created_at":"2026-09-07T00:00:00Z"},"usage":{"input_tokens":12000,"output_tokens":3000,"cost_usd":0.05,"estimated":false,"model":"claude-sonnet-5"}}')
[ "$(echo "$FIN" | jq -r .status)" = paused ] && ok "finish → 200 · status=paused" || bad "finish: $FIN"
TS=$(Q "SELECT status||'('||COALESCE(paused_reason::text,'')||')' FROM task WHERE id='$TA'")
[ "$TS" = "paused(budget)" ] && ok "task = $TS — cancelled 로 승격되지 않는다 (E9-01)" || bad "task = $TS"
AR=$(Q "SELECT outcome||'/'||COALESCE(stop_reason,'-')||'/'||(finished_at IS NOT NULL)::text FROM task_attempt WHERE task_id='$TA' AND attempt=$AA")
[ "$AR" = "paused_budget/budget/true" ] && ok "task_attempt = $AR (outcome·stop_reason·finished_at)" || bad "task_attempt = $AR"
REF=$(Q "SELECT l.runtime_session_ref->>'session_id' FROM lane l JOIN task t ON t.lane_id=l.id WHERE t.id='$TA'")
[ "$REF" = "acp-s50-A" ] && ok "lane.runtime_session_ref = $REF — 재개 자원(Finish 가 유일한 기록자)" || bad "ref = $REF"
LS=$(Q "SELECT l.status FROM lane l JOIN task t ON t.lane_id=l.id WHERE t.id='$TA'")
[ "$LS" = paused ] && ok "lane = paused — 승인 전까지 게이트" || bad "lane = $LS"

step "A3. Director 상향 승인 → 새 attempt 가 resume 을 받는다 (E9-02 'resume 우선')"
HID=$(Q "SELECT id FROM hitl_request WHERE session_id='$SA' AND purpose='budget'")
api -X POST "$S/hitl-requests/$HID/response" -d '{"approved":true,"budget_override_usd":3}' >/dev/null
TS=$(Q "SELECT status FROM task WHERE id='$TA'")
[ "$TS" = queued ] && ok "task = queued (같은 task, 새 attempt)" || bad "task = $TS"
C2=$(D "runtimes/$RID/claim" '{"capacity":5}')
RES=$(echo "$C2" | jq -r --arg t "$TA" '.tasks[]|select(.task.id==$t)|.resume.session_id')
AT2=$(echo "$C2" | jq -r --arg t "$TA" '.tasks[]|select(.task.id==$t)|.task.attempt')
[ "$RES" = "acp-s50-A" ] && ok "attempt $AT2 번들 resume.session_id = $RES — 콜드 스타트가 아니다" || bad "resume = $RES (claim=$(echo "$C2" | jq -c '[.tasks[]?.task.id]'))"
D "tasks/$TA/attempts/$AT2/phase" '{"phase":"running","workdir_path":"/tmp/s50"}' >/dev/null
D "tasks/$TA/attempts/$AT2/finish" '{"outcome":"completed","stop_reason":"end_turn","resume_outcome":"resumed","runtime_session_ref":{"runtime_kind":"claude_code","session_id":"acp-s50-A","cwd":"/tmp/s50","created_at":"2026-09-07T00:00:00Z"}}' >/dev/null
RSM=$(Q "SELECT resumed FROM task_attempt WHERE task_id='$TA' AND attempt=$AT2")
[ "$RSM" = t ] && ok "task_attempt(attempt $AT2).resumed = true" || bad "resumed = $RSM"

# ───────────────────────────── arm B — S-50 (b) ─────────────────────────────
step "B. paused(budget) 인 task 를 세션 취소로 끝낸다 — paused_detail 도 함께 지워야 200"
SB=$(newSession "B"); read -r TB AB <<<"$(runOne "$SB")"
D "tasks/$TB/attempts/$AB/heartbeat" '{"usage":{"input_tokens":12000,"output_tokens":3000,"cost_usd":0.05,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null
TS=$(Q "SELECT status||'('||COALESCE(paused_reason::text,'')||')' FROM task WHERE id='$TB'")
[ "$TS" = "paused(budget)" ] && ok "전제: task = $TS" || bad "task = $TS"
CODE_B=$(api -o /dev/null -w '%{http_code}' -X POST "$S/sessions/$SB/cancel" -d '{"reason":"여기까지"}')
[ "$CODE_B" = 200 ] && ok "세션 취소 = 200 (고치기 전에는 23514 로 500)" || bad "세션 취소 = $CODE_B"
TB2=$(Q "SELECT status||'/'||COALESCE(failure_kind::text,'-')||'/'||COALESCE(paused_reason::text,'NULL')||'/'||COALESCE(paused_detail::text,'NULL') FROM task WHERE id='$TB'")
[ "$TB2" = "cancelled/cancelled/NULL/NULL" ] && ok "task = $TB2 — paused_reason·paused_detail 둘 다 NULL (0006)" || bad "task = $TB2"

# ───────────────────────────── arm C — S-51 ─────────────────────────────────
step "C. 취소가 턴 종료와 경합한다 — 완료는 완료로 두되 피드가 그렇게 말한다"
SC=$(newSession "C"); read -r TC AC <<<"$(runOne "$SC")"
LID=$(Q "SELECT lane_id FROM task WHERE id='$TC'")
CODE_C=$(api -o /dev/null -w '%{http_code}' -X POST "$S/lanes/$LID/cancel" -d '{}')
[ "$CODE_C" = 202 ] && ok "lane 중단 = 202 (명령이 걸렸다)" || bad "lane 중단 = $CODE_C"
D "tasks/$TC/attempts/$AC/finish" '{"outcome":"completed","stop_reason":"end_turn"}' >/dev/null
TS=$(Q "SELECT status FROM task WHERE id='$TC'")
[ "$TS" = completed ] && ok "task = completed — 일은 실제로 끝났다" || bad "task = $TS"
NOTE=$(Q "SELECT count(*) FROM task_event WHERE task_id='$TC' AND class='status' AND verb='cancel' AND object_ref=to_jsonb('cancel_raced_turn_end'::text)")
[ "$NOTE" = 1 ] && ok "피드에 경합 note 1 — '사람이 중단함' 과 '완료' 가 어긋나지 않는다" || bad "note = $NOTE"
UNC=$(Q "SELECT count(*) FROM daemon_command WHERE task_id='$TC' AND type='cancel' AND consumed_at IS NULL")
[ "$UNC" = 0 ] && ok "미소비 cancel 명령 0 (§4.3)" || bad "미소비 = $UNC"

step "결과"
[ "$FAILED" = 0 ] && printf '\033[32mPASS\033[0m — FAIL 0\n' || printf '\033[31m%d FAIL\033[0m\n' "$FAILED"
exit $([ "$FAILED" = 0 ] && echo 0 || echo 1)
