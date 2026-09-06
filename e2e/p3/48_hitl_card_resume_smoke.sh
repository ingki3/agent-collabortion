#!/usr/bin/env bash
# e2e/p3/48_hitl_card_resume_smoke.sh — T-S7 실서버 스모크: S-45 + S-46.
#
#   S-45  시스템 발행 HITL 이 타임라인 카드(kind=hitl)를 남기고 message_id 를 채운다.
#         고치기 전 origin/dev 에서는 hitl 메시지 0 · message_id NULL 이다(T-I3 43_).
#   S-46  세션 예산 정지가 park 한 task 를 재개가 queued 로 되돌리고 claim 이 나온다.
#         고치기 전에는 세션만 active 가 되고 task 는 영영 paused 였다.
#
# 전용 스택(다른 워커와 겹치지 않게 — P2_TASKS §0-13):
#   docker run -d --name colab-pg-s7live -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab \
#     -e POSTGRES_DB=colab -p 5445:5432 postgres:16-alpine
#   COLAB_DB_URL="postgres://colab:colab@localhost:5445/colab?sslmode=disable" go run ./server/cmd/migrate
#   COLAB_DB_URL=... COLAB_SERVER_ADDR=:8099 COLAB_SERVER_URL=http://127.0.0.1:8099 ./bin/server
# 종료는 pid 로만 (§0-10).
#
# 사용: bash e2e/p3/48_hitl_card_resume_smoke.sh
set -uo pipefail
RUNID=$(uuidgen | tr "[:upper:]" "[:lower:]" | cut -c1-8)
S=http://127.0.0.1:8099/api/v1
D=http://127.0.0.1:8099/v1/daemon
J="${TMPDIR:-/tmp}/s45-jar-$RUNID.txt"; rm -f "$J"; trap 'rm -f "$J"' EXIT
FAILED=0
ok(){ printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad(){ printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED+1)); }
step(){ printf '\n\033[1m== %s\033[0m\n' "$*"; }
api(){ curl -sS -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }
Q(){ docker exec colab-pg-s7live psql -U colab -d colab -tAc "$1" | tr -d ' '; }

step "1. 계정·워크스페이스·에이전트 2개·세션(예산 \$1)"
api -X POST "$S/auth/signup" -d '{"display_name":"Dir","email":"s45-'$RUNID'@example.com","password":"password123"}' >/dev/null
WS=$(api -X POST "$S/workspaces" -d '{"name":"S45"}' | jq -r .id)
mkag(){ api -X POST "$S/workspaces/$WS/agents" -d "{\"name\":\"$1\",\"role\":\"$2\",\"role_description\":\"d\",\"instructions\":\"i\",\"profiles\":[{\"name\":\"default\",\"runtime_kind\":\"claude_code\",\"model\":\"claude-sonnet-5\"}]}" | jq -r .id; }
R=$(mkag R researcher); W=$(mkag W writer)
PAIR=$(api -X POST "$S/workspaces/$WS/runtimes/pairings" -d '{"name":"mac-s45"}')
CODE=$(echo "$PAIR" | jq -r .pairing_token)
RT=$(curl -sS -X POST "$D/pair" -H 'Content-Type: application/json' \
      -d "{\"pairing_code\":\"$CODE\",\"hostname\":\"mac-s45\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")
RID=$(echo "$RT" | jq -r .runtime_id); DTOK=$(echo "$RT" | jq -r .daemon_token)
[ "$RID" != null ] && ok "runtime $RID paired" || { bad "pair: $RT"; exit 1; }
curl -sS -X POST "$D/runtimes/$RID/probe" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d '{"runtimes":[{"kind":"claude_code","available":true,"version":"1.0.0","capabilities":{"usage":true,"resume":true}}]}' >/dev/null
# 에이전트 단위 예산은 없다 — 세션 잔여가 유일한 상한이라 초과는 SESSION 범위다(D-16).
SESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"S\",\"goal\":\"g\",\"isolation\":{\"kind\":\"none\"},\"assignee_agent_id\":\"$R\",\"participants\":[{\"agent_id\":\"$R\"},{\"agent_id\":\"$W\"}],\"runtime_id\":\"$RID\",\"limits\":{\"budget_usd\":1}}" | jq -r .id)
LIM=$(Q "SELECT limits->>'budget_usd' FROM session WHERE id='$SESS'")
[ "$LIM" = 1 ] && ok "session $SESS · limits.budget_usd = \$1" || bad "limits = $LIM"

step "2. 두 task 를 claim — W 는 running 인 채로 둔다(정지가 취소할 턴)"
mktask(){ api -X POST "$S/sessions/$SESS/messages" -d "{\"content\":\"[@$1](mention://agent/$2) 부탁합니다\"}" | jq -r '.triggers[0].task_id'; }
TR=$(mktask R "$R"); TW=$(mktask W "$W")
CLAIM=$(curl -sS -X POST "$D/runtimes/$RID/claim" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d '{"capacity":5}')
att(){ echo "$CLAIM" | jq -r ".tasks[] | select(.task.id==\"$1\") | .task.attempt"; }
AR=$(att "$TR"); AW=$(att "$TW")
[ -n "$AR" ] && [ -n "$AW" ] && ok "claim → R task $TR · W task $TW" || bad "claim: $CLAIM"
PH(){ curl -sS -X POST "$D/tasks/$1/attempts/$2/phase" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d "{\"phase\":\"$3\",\"workdir_path\":\"/tmp/s45\"}" >/dev/null; }
for t in "$TR:$AR" "$TW:$AW"; do PH "${t%%:*}" "${t##*:}" preparing; PH "${t%%:*}" "${t##*:}" running; done
ok "phase preparing → running (둘 다)"

step "3. R 의 finish 가 세션 예산 \$1 을 넘긴다 (\$1.25)"
FIN=$(curl -sS -X POST "$D/tasks/$TR/attempts/$AR/finish" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d '{"outcome":"completed","stop_reason":"end_turn","usage":{"input_tokens":20000,"output_tokens":5000,"cost_usd":1.25,"estimated":false,"model":"claude-sonnet-5"}}')
echo "$FIN" | jq -r .status | grep -q completed && ok "R finish → completed" || bad "finish: $FIN"

step "4. S-45 — 시스템 발행 HITL 의 타임라인 카드"
SS=$(Q "SELECT status||'('||COALESCE(paused_reason::text,'-')||')' FROM session WHERE id='$SESS'")
[ "$SS" = "paused(budget)" ] && ok "session = $SS (E9-04)" || bad "session = $SS, want paused(budget)"
HID=$(Q "SELECT id FROM hitl_request WHERE session_id='$SESS' AND purpose='budget'")
HT=$(Q "SELECT COALESCE(task_id::text,'-') FROM hitl_request WHERE id='$HID'")
[ "$HT" = "-" ] && ok "HITL $HID · task_id 비움 — 세션 범위(FR-7.3 s-13)" || bad "task_id = $HT"
MID=$(Q "SELECT COALESCE(message_id::text,'NULL') FROM hitl_request WHERE id='$HID'")
[ "$MID" != NULL ] && ok "hitl_request.message_id = $MID (openapi HitlRequest.message_id)" || bad "message_id = NULL — 카드가 없다 (S-45)"
CARDS=$(Q "SELECT count(*) FROM message WHERE session_id='$SESS' AND kind='hitl'")
[ "$CARDS" = 1 ] && ok "타임라인 hitl 카드 = 1 (SCREEN §4.5)" || bad "hitl 카드 = $CARDS, want 1"
AT=$(Q "SELECT author_type::text FROM message WHERE id='$MID'")
[ "$AT" = system ] && ok "카드 author_type = system" || bad "author_type = $AT"
BODY=$(Q "SELECT replace(content, E'\n', ' | ') FROM message WHERE id='$MID'")
echo "$BODY" | grep -q '예산' && ok "카드 본문 = $BODY" || bad "카드 본문 = $BODY"
# S7 이 실제로 받는 프레임 (realtime message.created).
EV=$(Q "SELECT count(*) FROM stream_event WHERE session_id='$SESS' AND type='message.created' AND payload->>'id'='$MID'")
[ "$EV" = 1 ] && ok "message.created 프레임 1 — S7 이 새로고침 없이 본다" || bad "message.created = $EV"
# 세션 메시지 API 에도 그대로 나온다.
APICARD=$(api "$S/sessions/$SESS/messages?limit=50" | jq -r "[.items[] | select(.kind==\"hitl\")] | length")
[ "$APICARD" = 1 ] && ok "listMessages 에 hitl 카드 1" || bad "listMessages hitl = $APICARD"

step "5. S-46 — 정지가 park 한 W 의 task"
WS_ST=$(Q "SELECT status||'('||COALESCE(paused_reason::text,'-')||')' FROM task WHERE id='$TW'")
[ "$WS_ST" = "paused(budget)" ] && ok "W task = $WS_ST — 예산 정지는 턴을 취소한다(§8.2.2)" || bad "W task = $WS_ST"
WL=$(Q "SELECT l.status FROM lane l JOIN task t ON t.lane_id=l.id WHERE t.id='$TW'")
[ "$WL" = paused ] && ok "W lane = paused" || bad "W lane = $WL"

step "6. 재개 — 상한을 올리고 이어간다"
RES=$(api -X POST "$S/sessions/$SESS/resume" -d '{"limits":{"budget_usd":10}}')
echo "$RES" | jq -r .status | grep -q active && ok "session = active" || bad "resume: $RES"
WS2=$(Q "SELECT status FROM task WHERE id='$TW'")
[ "$WS2" = queued ] && ok "W task = queued — 재개가 park 된 task 를 되돌린다 (S-46)" || bad "W task = $WS2, want queued"
WA=$(Q "SELECT attempt FROM task WHERE id='$TW'")
[ "$WA" = 2 ] && ok "W attempt = 2 — 같은 lane·workdir 의 새 attempt(FR-5.4)" || bad "attempt = $WA, want 2"
WL2=$(Q "SELECT l.status FROM lane l JOIN task t ON t.lane_id=l.id WHERE t.id='$TW'")
[ "$WL2" = queued ] && ok "W lane = queued" || bad "W lane = $WL2"
HS=$(Q "SELECT status||'/'||COALESCE(approved::text,'-') FROM hitl_request WHERE id='$HID'")
[ "$HS" = "answered/true" ] && ok "예산 HITL = $HS — 재개가 곧 응답(openapi resumeSession)" || bad "HITL = $HS"
C2=$(curl -sS -X POST "$D/runtimes/$RID/claim" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d '{"capacity":5}')
echo "$C2" | jq -r '.tasks[]?.task.id' | grep -q "$TW" && ok "claim 이 W task 를 다시 내준다" || bad "claim = $(echo "$C2" | jq -c '[.tasks[]?.task.id]') — 재개했는데 dispatch 0"

printf '\n'
[ "$FAILED" = 0 ] && printf '\033[32mPASS\033[0m — FAIL 0\n' || printf '\033[31m%d FAIL\033[0m\n' "$FAILED"
exit $(( FAILED > 0 ))
