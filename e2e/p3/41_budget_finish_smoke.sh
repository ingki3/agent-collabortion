#!/usr/bin/env bash
# e2e/p3/41_budget_finish_smoke.sh — T-S6 실서버 스모크: S-44 "finish 에서도 예산 강제".
#
# T-I3 이 실측한 모양을 그대로 재현한다 — 에이전트 budget_per_task $0.002, 턴 비용
# $0.0599, 하트비트에는 usage 가 한 번도 실리지 않는다(D-17 데몬). 고치기 전 origin/dev
# 에서 이 스크립트는 FAIL 5 로 결함을 재현하고(task completed · lane done · HITL 0 ·
# 인박스 0 · 다음 task 가 그대로 dispatch), 고친 뒤에는 FAIL 0 이다.
#
# 전용 스택(다른 워커와 겹치지 않게 — P2_TASKS §0-13):
#   docker run -d --name colab-pg-s6live -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab \
#     -e POSTGRES_DB=colab -p 5444:5432 postgres:16-alpine
#   COLAB_DB_URL="postgres://colab:colab@localhost:5444/colab?sslmode=disable" go run ./server/cmd/migrate
#   COLAB_DB_URL=... COLAB_SERVER_ADDR=:8098 COLAB_SERVER_URL=http://127.0.0.1:8098 ./bin/server
# 종료는 pid 로만 (§0-10).
#
# 사용: bash e2e/p3/41_budget_finish_smoke.sh
set -uo pipefail
RUNID=$(uuidgen | tr "[:upper:]" "[:lower:]" | cut -c1-8)
ROOT="$(cd "$(dirname "$0")" && pwd)"
S=http://127.0.0.1:8098/api/v1
J="${TMPDIR:-/tmp}/s44-jar-$RUNID.txt"; rm -f "$J"; trap 'rm -f "$J"' EXIT
FAILED=0
ok(){ printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad(){ printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED+1)); }
step(){ printf '\n\033[1m== %s\033[0m\n' "$*"; }
api(){ curl -sS -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }

step "1. 계정·워크스페이스·에이전트·세션"
api -X POST "$S/auth/signup" -d '{"display_name":"Dir","email":"s44-'$RUNID'@example.com","password":"password123"}' >/dev/null
WS=$(api -X POST "$S/workspaces" -d '{"name":"S44"}' | jq -r .id)
AG=$(api -X POST "$S/workspaces/$WS/agents" -d '{"name":"R","role":"researcher","role_description":"d","instructions":"i","budget_per_task":0.002,"profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-sonnet-5"}]}' | jq -r .id)
PAIR=$(api -X POST "$S/workspaces/$WS/runtimes/pairings" -d '{"name":"mac-s44"}')
CODE=$(echo "$PAIR" | jq -r .pairing_token)
RT=$(curl -sS -X POST http://127.0.0.1:8098/v1/daemon/pair -H 'Content-Type: application/json' \
      -d "{\"pairing_code\":\"$CODE\",\"hostname\":\"mac-s44\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")
RID=$(echo "$RT" | jq -r .runtime_id); DTOK=$(echo "$RT" | jq -r .daemon_token)
[ "$RID" != null ] && ok "runtime $RID paired" || { bad "pair: $RT $PAIR"; exit 1; }
curl -sS -X POST "http://127.0.0.1:8098/v1/daemon/runtimes/$RID/probe" -H "Authorization: Bearer $DTOK" \
  -H 'Content-Type: application/json' -d '{"runtimes":[{"kind":"claude_code","available":true,"version":"1.0.0","capabilities":{"usage":true,"resume":true}}]}' >/dev/null
SESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"S\",\"goal\":\"g\",\"isolation\":{\"kind\":\"none\"},\"assignee_agent_id\":\"$AG\",\"participants\":[{\"agent_id\":\"$AG\"}],\"runtime_id\":\"$RID\"}" | jq -r .id)
ok "session $SESS · agent budget_per_task \$0.002"

step "2. 멘션 → task, 데몬 claim"
POST=$(api -X POST "$S/sessions/$SESS/messages" -d "{\"content\":\"[@R](mention://agent/$AG) 부탁합니다\"}")
TASK=$(echo "$POST" | jq -r '.triggers[0].task_id')
CLAIM=$(curl -sS -X POST "http://127.0.0.1:8098/v1/daemon/runtimes/$RID/claim" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d '{"capacity":5}')
echo "$CLAIM" | jq -r '.tasks[0].task.id' | grep -q "$TASK" && ok "claim → task $TASK" || bad "claim: $CLAIM"
ATT=$(echo "$CLAIM" | jq -r '.tasks[0].task.attempt')

PH(){ curl -sS -X POST "http://127.0.0.1:8098/v1/daemon/tasks/$TASK/attempts/$ATT/phase" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d "{\"phase\":\"$1\",\"workdir_path\":\"/tmp/s44\"}" >/dev/null; }
PH preparing; PH running
ok "phase preparing → running"

step "3. 하트비트 usage 없이 finish 만 (D-17 데몬) — 턴 비용 \$0.0599"
FIN=$(curl -sS -X POST "http://127.0.0.1:8098/v1/daemon/tasks/$TASK/attempts/$ATT/finish" \
  -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d '{"outcome":"completed","stop_reason":"end_turn","usage":{"input_tokens":12000,"output_tokens":3000,"cost_usd":0.0599,"estimated":false,"model":"claude-sonnet-5"}}')
echo "$FIN" | jq -r .status | grep -q completed && ok "finish → completed (서버 판정)" || bad "finish: $FIN"

step "4. 판정"
Q(){ docker exec colab-pg-s6live psql -U colab -d colab -tAc "$1" | tr -d ' '; }
TS=$(Q "SELECT status FROM task WHERE id='$TASK'")
[ "$TS" = completed ] && ok "task = completed (턴은 끝났고 completed→paused 전이는 없다)" || bad "task = $TS"
LS=$(Q "SELECT l.status FROM lane l JOIN task t ON t.lane_id=l.id WHERE t.id='$TASK'")
[ "$LS" = paused ] && ok "lane = paused — 다음 dispatch 차단" || bad "lane = $LS, want paused"
COST=$(Q "SELECT cost_usd FROM session WHERE id='$SESS'")
ok "session.cost_usd = $COST"
H=$(Q "SELECT source||'/'||type||'/'||purpose||'/'||COALESCE(task_id::text,'-') FROM hitl_request WHERE session_id='$SESS' AND purpose='budget'")
[ "$H" = "system/approval/budget/$TASK" ] && ok "HITL = $H (task_id 채움, FR-7.3 s-13)" || bad "HITL = $H"
IB=$(Q "SELECT count(*) FROM inbox_item WHERE session_id='$SESS' AND type='hitl_request'")
[ "$IB" = 1 ] && ok "Director 인박스 카드 1" || bad "inbox = $IB"
CC=$(Q "SELECT count(*) FROM daemon_command WHERE task_id='$TASK' AND type='cancel'")
[ "$CC" = 0 ] && ok "cancel 명령 0 — 끝난 턴을 취소하지 않는다" || bad "cancel = $CC"

step "5. 같은 lane 의 다음 task 는 dispatch 되지 않는다"
POST2=$(api -X POST "$S/sessions/$SESS/messages" -d "{\"content\":\"[@R](mention://agent/$AG) 하나만 더\"}")
T2=$(echo "$POST2" | jq -r '.triggers[0].task_id')
C2=$(curl -sS -X POST "http://127.0.0.1:8098/v1/daemon/runtimes/$RID/claim" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d '{"capacity":5}')
echo "$C2" | jq -r '.tasks[]?.task.id' | grep -q "$T2" && bad "paused lane 의 task $T2 가 dispatch 됐다" || ok "claim = $(echo "$C2" | jq -c '[.tasks[]?.task.id]') — 새 task $T2 는 나오지 않는다"

step "6. Director 가 \$3 으로 상향 승인 → lane 재개 + override 가 다음 task 로"
HID=$(Q "SELECT id FROM hitl_request WHERE session_id='$SESS' AND purpose='budget'")
api -X POST "$S/hitl-requests/$HID/response" -d '{"approved":true,"budget_override_usd":3}' >/dev/null
LS2=$(Q "SELECT l.status FROM lane l JOIN task t ON t.lane_id=l.id WHERE t.id='$TASK'")
[ "$LS2" != paused ] && ok "lane = $LS2 (승인이 게이트를 풀었다)" || bad "lane 이 아직 paused"
AB=$(Q "SELECT budget_per_task FROM agent WHERE id='$AG'")
[ "$(printf '%s' "$AB" | sed 's/0*$//')" = "0.002" ] && ok "agent.budget_per_task = \$0.002 그대로 (C2′)" || bad "agent budget = $AB"
C3=$(curl -sS -X POST "http://127.0.0.1:8098/v1/daemon/runtimes/$RID/claim" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d '{"capacity":5}')
OV=$(echo "$C3" | jq -r --arg t "$T2" '.tasks[]?|select(.task.id==$t)|.task.budget_override_usd')
[ "$OV" = "3" ] && ok "다음 task 번들 budget_override_usd = 3 (승인이 lane 을 따라간다)" || bad "override = $OV, claim=$(echo "$C3" | jq -c '[.tasks[]?.task.id]')"

printf '\n== 결과: FAIL %d\n' "$FAILED"
exit $((FAILED>0))
