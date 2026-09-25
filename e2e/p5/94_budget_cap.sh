#!/usr/bin/env bash
# e2e/p5/94_budget_cap.sh — T-BUDGETCAP 실서버 스모크(Director 요청 2026-09-25: 실사용 방이 상한 없이 80분에 $92.77).
# 기본값은 바꾸지 않는다(상한 없음). 화면이 새로 거는 세 길이 **이미 있는 예산 멈춤 흐름(HITL)** 에 닿는지 잰다 — 데몬 없이 curl 로
# (데몬 역할 claim·phase·heartbeat 는 91_ 의 레시피).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 10s
#
#   0. 기본값: 새 방·새 미션 모두 상한 없음(budget_usd null).
#   A. 방 비용 줄 [상한 걸기] = updateRoom limits.budget_usd → 번들 limits.budget_usd = 방 잔여 → heartbeat usage 가 넘으면
#      room.blocked_reason=budget + 예산 HITL(purpose budget) → 승인(budget_override_usd) 으로 풀린다.
#   B. 미션 비용 줄 [상한 걸기] = updateWork limits.budget_usd → 넘으면 그 미션만 paused(budget) + 예산 HITL · 방은 막히지 않는다.
#   C. S14 「새 미션의 기본 예산 상한」(budget_policy.default_session_budget_usd) → 예산 칸을 비운 새 미션에 걸리고(서버가 채움)
#      넘으면 paused(budget) · 명시적 null 은 상한 없음 그대로 · S14 「새 방의 기본 예산 상한」(room_defaults.limits.budget_usd) → 새 방에 걸린다.
#
# 스택(T-BUDGETCAP): server :8141 · pg :5496 · 컨테이너 colab-pg-budgetcap.
# 사용: SERVER_URL=http://localhost:8141 PG_PORT=5496 PG_CONTAINER=colab-pg-budgetcap bash e2e/p5/up.sh
#       bash e2e/p5/94_budget_cap.sh
#       SERVER_URL=http://localhost:8141 PG_PORT=5496 PG_CONTAINER=colab-pg-budgetcap bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8141}"
export PG_PORT="${PG_PORT:-5496}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-budgetcap}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/94-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/94-c-dir.txt"; rm -f "$COOKIE"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-BUDGETCAP ports"
CAPS='[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]'

call() { local o; o="$(api "$@")"; CODE="$(api_code <<<"$o")"; BODY="$(api_body <<<"$o")"; }
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":8,"wait_ms":0}'; }
att() { psqlq "select attempt from task where id='$1'"; }
running() { daemon_api "tasks/$1/attempts/$(att "$1")/phase" '{"phase":"running","pgid":4242}' >/dev/null; }
spend() { daemon_api "tasks/$1/attempts/$(att "$1")/heartbeat" "$(jq -nc --argjson c "$2" '{usage:{input_tokens:1000,output_tokens:1000,cost_usd:$c,estimated:false,model:"claude-sonnet-5"},last_seq:0}')" > "$OUT/94-hb-$1.json"; }
post() { api_ok POST "/rooms/$1/messages" "$2" -H "Idempotency-Key: $(uuid)"; }
mk_room() { api_ok POST "/workspaces/$WS/rooms" "$(jq -nc --arg n "$1 $RUN" '{name:$n}')" | jq -r .id; }
join_r() { api_ok POST "/rooms/$1/participants" "$(jq -nc --arg a "$R" '{agent_id:$a}')" >/dev/null; }
# of_room CLAIM ROOM [WORK] → 그 방(·미션) 번들 하나
of_room() { jq -c --arg r "$2" --arg w "${3:-}" '[.tasks[]|select(.task.session_id==$r and ($w=="" or .task.work_id==$w))][0] // empty' <<<"$1"; }
budget_of() { jq -r '.limits.budget_usd // "-"'; }

step "0. 방장 Dir 가입, 워크스페이스·컴퓨터(curl 페어링)·에이전트 R — 기본은 상한 없음"
DIR_ID="$(signup "bc-dir-$RUN@example.com" password123 "Dir")"
WS="$(create_workspace "BudgetCap $RUN")"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-bc" "$CAPS" "/tmp/colab-bc-$RUN")"
export DTOK RID
R="$(api_ok POST "/workspaces/$WS/agents" '{"name":"R","role":"researcher","role_description":"d","instructions":"짧게","profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-sonnet-5","is_default":true}]}' | jq -r .id)"
ROOMA="$(mk_room "94 방 상한")"; join_r "$ROOMA"
chk 0.1 "-" "$(api_ok GET "/rooms/$ROOMA" | budget_of)" "새 방 — 상한 없음(기본값 그대로)"
W0="$(api_ok POST "/rooms/$ROOMA/works" '{"goal":"기본 미션"}')"
chk 0.2 "-" "$(budget_of <<<"$W0")" "새 미션 — 상한 없음(워크스페이스 기본 없음)"
api_ok POST "/works/$(jq -r .id <<<"$W0")/cancel" '{}' >/dev/null

step "A. 방 비용 줄 [상한 걸기] → updateRoom → 초과 → 방 멈춤 + 예산 HITL → 승인으로 풀림"
call PATCH "/rooms/$ROOMA" '{"limits":{"budget_usd":0.02}}'
chk A.1 "200/0.02" "$CODE/$(budget_of <<<"$BODY")" "updateRoom limits.budget_usd = 0.02(화면의 「상한 걸기」가 보내는 몸)"
post "$ROOMA" "$(jq -nc --arg c "$(mention R "$R") 방 상한 시험" '{content:$c}')" >/dev/null
CL="$(claim)"; BA="$(of_room "$CL" "$ROOMA")"; [ -n "$BA" ] || die "A: claim 에 방 A task 가 없다: $CL"
TA="$(jq -r .task.id <<<"$BA")"; running "$TA"
chk A.2 0.02 "$(budget_of <<<"$BA")" "번들 limits.budget_usd = 방 잔여(미션 없음)"
spend "$TA" 0.05
chk A.3 budget "$(psqlq "select coalesce(blocked_reason::text,'-') from room where id='$ROOMA'")" "쓴 돈 \$0.05 > 방 상한 \$0.02 → room.blocked_reason=budget"
HA="$(psqlq "select id from hitl_request where session_id='$ROOMA' and purpose='budget' and status='open'")"
chk A.4 1 "$(psqlq "select count(*) from hitl_request where session_id='$ROOMA' and purpose='budget' and status='open'")" "예산 HITL(purpose budget) 하나 — 이미 있는 멈춤 흐름"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HA" '{"approved":true,"budget_override_usd":1}')"
chk A.5 "200/-/1" "$CODE/$(psqlq "select coalesce(blocked_reason::text,'-') from room where id='$ROOMA'")/$(api_ok GET "/rooms/$ROOMA" | budget_of)" "승인(상한 \$1) → 방이 풀리고 상한이 올라간다"

step "B. 미션 비용 줄 [상한 걸기] → updateWork → 초과 → 그 미션만 paused(budget) + 예산 HITL"
ROOMB="$(mk_room "94 미션 상한")"; join_r "$ROOMB"
WB="$(api_ok POST "/rooms/$ROOMB/works" '{"goal":"미션 상한 시험"}' | jq -r .id)"
chk B.1 "-" "$(api_ok GET "/works/$WB" | budget_of)" "열 때는 상한 없음"
call PATCH "/works/$WB" '{"limits":{"budget_usd":0.01}}'
chk B.2 "200/0.01" "$CODE/$(budget_of <<<"$BODY")" "updateWork limits.budget_usd = 0.01(미션 칸의 「상한 걸기」)"
post "$ROOMB" "$(jq -nc --arg c "$(mention R "$R") 미션 상한 시험" --arg w "$WB" '{content:$c,work_id:$w}')" >/dev/null
CL="$(claim)"; BB="$(of_room "$CL" "$ROOMB" "$WB")"; [ -n "$BB" ] || die "B: claim 에 미션 B task 가 없다: $CL"
TB="$(jq -r .task.id <<<"$BB")"; running "$TB"
chk B.3 0.01 "$(budget_of <<<"$BB")" "번들 limits.budget_usd = 미션 잔여"
spend "$TB" 0.05
chk B.4 "paused/budget" "$(api_ok GET "/works/$WB" | jq -r '.status+"/"+(.paused_reason//"-")')" "미션 상한 초과 → paused(budget)"
chk B.5 "-" "$(psqlq "select coalesce(blocked_reason::text,'-') from room where id='$ROOMB'")" "방은 막히지 않는다(방 상한이 아니다)"
chk B.6 1 "$(psqlq "select count(*) from hitl_request where work_id='$WB' and purpose='budget' and status='open'")" "미션 예산 HITL 하나"

step "C. S14 기본값 — 새 미션(budget_policy.default_session_budget_usd) · 새 방(room_defaults.limits.budget_usd)"
call PATCH "/workspaces/$WS/settings" '{"budget_policy":{"default_session_budget_usd":0.01},"room_defaults":{"limits":{"budget_usd":30}}}'
chk C.1 200 "$CODE" "S14 저장(두 기본값)"
ROOMC="$(mk_room "94 기본값")"; join_r "$ROOMC"
chk C.2 30 "$(api_ok GET "/rooms/$ROOMC" | budget_of)" "새 방은 room_defaults.limits.budget_usd 를 상속"
chk C.3 "-" "$(api_ok GET "/rooms/$ROOMB" | budget_of)" "  … 이미 있는 방은 그대로"
WC="$(api_ok POST "/rooms/$ROOMC/works" '{"goal":"기본 상한 미션"}' | jq -r .id)"
chk C.4 0.01 "$(api_ok GET "/works/$WC" | budget_of)" "예산 칸을 비운 새 미션 → 서버가 워크스페이스 기본 \$0.01 을 채운다"
WN="$(api_ok POST "/rooms/$ROOMC/works" '{"goal":"명시적 없음","limits":{"budget_usd":null}}' | jq -r .id)"
chk C.5 "-" "$(api_ok GET "/works/$WN" | budget_of)" "명시적 null → 상한 없음(방을 따름) 그대로"
api_ok POST "/works/$WN/cancel" '{}' >/dev/null
post "$ROOMC" "$(jq -nc --arg c "$(mention R "$R") 기본 상한 시험" --arg w "$WC" '{content:$c,work_id:$w}')" >/dev/null
CL="$(claim)"; BC="$(of_room "$CL" "$ROOMC" "$WC")"; [ -n "$BC" ] || die "C: claim 에 미션 C task 가 없다: $CL"
TC="$(jq -r .task.id <<<"$BC")"; running "$TC"; spend "$TC" 0.05
chk C.6 "paused/budget" "$(api_ok GET "/works/$WC" | jq -r '.status+"/"+(.paused_reason//"-")')" "기본 상한 초과 → paused(budget) — 같은 멈춤 흐름"
chk C.7 1 "$(psqlq "select count(*) from hitl_request where work_id='$WC' and purpose='budget' and status='open'")" "  … 예산 HITL 하나"

step "요약"
PASS_N="$(awk -F'\t' '$2=="PASS"' "$CHECKS" | wc -l | tr -d ' ')"; FAIL_N="$(awk -F'\t' '$2=="FAIL"' "$CHECKS" | wc -l | tr -d ' ')"
log "94_: PASS $PASS_N · FAIL $FAIL_N (out/94-checks.tsv)"
[ "$FAIL_N" = 0 ]
