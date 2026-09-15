#!/usr/bin/env bash
# e2e/p5/79_delete_session.sh — T-S17 실서버 스모크: deleteSession(openapi 0.1.3, FR-2.7)
# — **데몬 없이**, 데몬 역할(claim·phase·finish·§6 보고)은 curl 로 흉내(70_ 의 레시피).
#
# 재는 것 (판정 표 out/79-checks.tsv):
#   A. 완료 세션 하나를 만든다: 세션 → claim → phase → 아티팩트(task 토큰, large object) → finish(usage)
#      → completeSession → completed. 비용 by_session 에 있고, 지표 표본에 든다.
#   B. 권한: 멤버 403 director_required · 진행 중 세션 409 session_active(계약 문장) · Director 204
#      → 두 번째 404 · getSession 404 · listSessions 에 없음 · getWorkspaceCost 에서 빠짐 · 지표 표본에서 빠짐
#      · activity_log session.deleted 1행(그 세션의 다른 활동 0) · SSE session.deleted {session_id}
#      · 아티팩트 large object 0(0008 트리거) · 자식 행 0
#   C. worktree 세션: §6 보고(merged=false · commits_ahead=1) → cancel → 삭제 409 workdir_unmerged
#      + Problem.workdirs[0].id = 그 행 → §6 보고(merged=true · clean) → 204 → claim 응답에 gc {workdirs:[{id,path}]}
#      → §6 영수증(gc.id, 없는 행) 200 → 명령 consumed_by=workdir_report · 피드 0 · workdir 행 0
#      → admin 도 204(다른 완료 세션).
#
# 스택(T-S17 배정): server :8115 · pg :5459 · 컨테이너 colab-pg-s17. 다른 워커 스택과 겹치지 않는다(§0-13).
# 사용: SERVER_URL=http://localhost:8115 PG_PORT=5459 PG_CONTAINER=colab-pg-s17 bash e2e/p5/up.sh
#       bash e2e/p5/79_delete_session.sh
#       SERVER_URL=http://localhost:8115 PG_PORT=5459 PG_CONTAINER=colab-pg-s17 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8115}"
export PG_PORT="${PG_PORT:-5459}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-s17}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/79-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/79-cookies-dir.txt"; rm -f "$COOKIE"
COOKIE_MEM="$OUT/79-cookies-mem.txt"; rm -f "$COOKIE_MEM"
COOKIE_ADM="$OUT/79-cookies-adm.txt"; rm -f "$COOKIE_ADM"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-S17 ports"
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
# as COOKIEFILE METHOD PATH [JSON] → 다른 계정으로 api
as() { local c="$1"; shift; COOKIE="$c" api "$@"; }
count_children() { # SESSION → 자식 행 합계(세션을 가리키는 표 전부)
  psqlq "select (select count(*) from session_participant where session_id='$1')
            + (select count(*) from lane where session_id='$1') + (select count(*) from task where session_id='$1')
            + (select count(*) from message where session_id='$1') + (select count(*) from artifact where session_id='$1')
            + (select count(*) from hitl_request where session_id='$1') + (select count(*) from decision where session_id='$1')
            + (select count(*) from inbox_item where session_id='$1') + (select count(*) from workdir where session_id='$1')
            + (select count(*) from session_hop where session_id='$1') + (select count(*) from task_token where session_id='$1')
            + (select count(*) from task_usage u join task t on t.id=u.task_id where t.session_id='$1')"
}

step "0. 계정 3(Director=owner · member · admin) · 워크스페이스 · 에이전트 · 페어링(curl) · probe"
signup "s17-dir-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "S17 $RUN")"
invite() { api_ok POST "/workspaces/$WS/invites" "$(jq -nc --arg r "$1" '{role:$r}')" | jq -r .token; }
INV_MEM="$(invite member)"; INV_ADM="$(invite admin)"
COOKIE="$COOKIE_MEM" signup "s17-mem-$RUN@example.com" password123 "Mem" >/dev/null
as "$COOKIE_MEM" POST "/invites/$INV_MEM/accept" | api_code | grep -q '^20' || die "member invite accept"
COOKIE="$COOKIE_ADM" signup "s17-adm-$RUN@example.com" password123 "Adm" >/dev/null
as "$COOKIE_ADM" POST "/invites/$INV_ADM/accept" | api_code | grep -q '^20' || die "admin invite accept"
AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc '{name:"Lead",role:"lead",role_description:"팀을 이끈다",
  instructions:"짧게, 한국어로 답한다. 저장소나 다른 디렉토리를 뒤지지 마라.",profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id)"
ROOT="/tmp/colab-s17-$RUN"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-s17" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "$ROOT")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"
chk 0.2 "admin/member" "$(psqlq "select string_agg(role::text, '/' order by role::text) from member where workspace_id='$WS' and role<>'owner'")" "멤버·관리자 합류"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. 완료 세션 만들기 — claim → phase → 아티팩트 → finish → completeSession"
mk_session() { # TITLE ISOLATION_JSON → session id
  api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "$1" --arg a "$AG" --arg rt "$RID" --argjson iso "$2" \
    '{title:$t,goal:"저장소 밖에서 짧은 인사말 한 줄을 쓴다",isolation:$iso,participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt}')" | jq -r .id
}
run_turn() { # SESSION → 그 세션의 초기 task 를 claim → phase running → finish completed(usage). 표준출력: task_id<TAB>task_token
  local s="$1" cl b tid tok
  cl="$(claim)"
  b="$(jq -c --arg s "$s" '.tasks[]|select(.task.session_id==$s)' <<<"$cl")"
  [ -n "$b" ] || die "claim 에 세션 $s 의 task 가 없다: $cl"
  tid="$(jq -r .task.id <<<"$b")"; tok="$(jq -r .task_token <<<"$b")"
  daemon_api "tasks/$tid/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null
  printf '%s\t%s' "$tid" "$tok"
}
finish_turn() { # TASK
  daemon_api "tasks/$1/attempts/1/finish" '{"outcome":"completed","stop_reason":"end_turn","transport":"acp","last_seq":0,
    "usage":{"input_tokens":2000,"output_tokens":500,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null
}
S_DONE="$(mk_session "삭제 대상(완료) $RUN" '{"kind":"none"}')"
IFS=$'\t' read -r T_DONE TT_DONE <<<"$(run_turn "$S_DONE")"
[ -n "$T_DONE" ] || die "완료 세션의 턴을 claim 하지 못했다"
printf 'diff --git a/hello.txt b/hello.txt\n+안녕\n' > "$OUT/79-artifact.diff"
ART="$(curl -sS -X POST "$API/sessions/$S_DONE/artifacts" -H "Authorization: Bearer $TT_DONE" -H "Idempotency-Key: $(uuid)" \
        -F name=report -F type=diff -F "file=@$OUT/79-artifact.diff")"
ART_ID="$(jq -r '.artifact.id // .id // empty' <<<"$ART")"
chk A.1 1 "$(psqlq "select count(*) from artifact where id='$ART_ID' and session_id='$S_DONE'")" "아티팩트 저장(task 토큰)"
LO="$(psqlq "select substr(storage_ref,6) from artifact where id='$ART_ID'")"
chk A.2 1 "$(psqlq "select count(*) from pg_largeobject_metadata where oid=${LO:-0}")" "본문은 large object pglo:$LO"
finish_turn "$T_DONE"
api_ok POST "/sessions/$S_DONE/complete" '{"confirm":true}' >/dev/null
chk A.3 completed "$(psqlq "select status from session where id='$S_DONE'")" "completeSession → completed"
COST0="$(api_ok GET "/workspaces/$WS/cost")"; echo "$COST0" | jq . > "$OUT/79-cost-before.json"
chk A.4 1 "$(jq -r --arg s "$S_DONE" '[.by_session[]|select(.id==$s)]|length' <<<"$COST0")" "getWorkspaceCost.by_session 에 세션이 있다"
MET0="$(api_ok GET "/workspaces/$WS/metrics")"; echo "$MET0" | jq . > "$OUT/79-metrics-before.json"
chk A.5 1 "$(jq -r '.metrics[]|select(.key=="auto_complete_rate")|.n' <<<"$MET0")" "지표 auto_complete_rate 표본 n=1"
chk_ge A.6 5 "$(count_children "$S_DONE")" "자식 행(참여자·줄기·할 일·메시지·아티팩트·토큰·비용)이 있다"
# 이 경로(curl 데몬)는 activity_log 를 남기지 않으므로 그 세션의 활동 한 줄을 심어 "함께 사라진다"를 잰다.
psqlq "insert into activity_log (workspace_id, session_id, actor_type, action, object_type, object_id) values ('$WS','$S_DONE','system','session.started','session','$S_DONE')" >/dev/null
ACT0="$(psqlq "select count(*) from activity_log where session_id='$S_DONE'")"
chk A.7 1 "$ACT0" "그 세션의 활동 기록 1행(심음)"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 권한 · 상태 · 삭제 뒤 관측"
S_ACT="$(mk_session "진행 중 $RUN" '{"kind":"none"}')"
R="$(as "$COOKIE_MEM" DELETE "/sessions/$S_DONE")"
chk B.1 "403/director_or_admin_required" "$(api_code <<<"$R")/$(api_body <<<"$R" | jq -r .code)" "멤버(Director 아님) → 403"
R="$(api DELETE "/sessions/$S_ACT")"
chk B.2 "409/session_active" "$(api_code <<<"$R")/$(api_body <<<"$R" | jq -r .code)" "active 세션 → 409 session_active"
chk B.3 "진행 중인 세션은 먼저 종료하세요" "$(api_body <<<"$R" | jq -r .detail)" "409 detail = 계약 문장"
SSE="$OUT/79-sse.log"; : > "$SSE"
curl -sN -b "$COOKIE" "$API/workspaces/$WS/stream" > "$SSE" 2>/dev/null &
SSE_PID=$!; echo "$SSE_PID" > "$OUT/79-sse.pid"; sleep 1
chk B.4 204 "$(api DELETE "/sessions/$S_DONE" | api_code)" "Director → 204"
chk B.5 404 "$(api DELETE "/sessions/$S_DONE" | api_code)" "두 번째 호출 → 404 (멱등 아님)"
chk B.6 404 "$(api GET "/sessions/$S_DONE" | api_code)" "getSession → 404"
chk B.7 0 "$(api_ok GET "/workspaces/$WS/sessions" | jq -r --arg s "$S_DONE" '[.items[]|select(.id==$s)]|length')" "listSessions 에 없다"
COST1="$(api_ok GET "/workspaces/$WS/cost")"; echo "$COST1" | jq . > "$OUT/79-cost-after.json"
chk B.8 0 "$(jq -r --arg s "$S_DONE" '[.by_session[]|select(.id==$s)]|length' <<<"$COST1")" "getWorkspaceCost.by_session 에서 빠졌다"
chk B.9 0 "$(jq -r '.total_usd' <<<"$COST1")" "total_usd 0 (그 세션이 유일한 비용이었다)"
MET1="$(api_ok GET "/workspaces/$WS/metrics")"; echo "$MET1" | jq . > "$OUT/79-metrics-after.json"
chk B.10 0 "$(jq -r '.metrics[]|select(.key=="auto_complete_rate")|.n' <<<"$MET1")" "지표 auto_complete_rate 표본 n=0"
chk B.11 0 "$(count_children "$S_DONE")" "자식 행 0 (CASCADE)"
chk B.12 0 "$(psqlq "select count(*) from pg_largeobject_metadata where oid=${LO:-0}")" "아티팩트 large object 0 (0008 트리거)"
# session_id 의 FK 는 SET NULL 이라 "그 세션의 행 0" 만으론 못 잡는다 — 워크스페이스의 session_id NULL 행(고아)도 센다.
chk B.13 "1/0/0" "$(psqlq "select (select count(*) from activity_log where action='session.deleted' and object_id='$S_DONE')||'/'||(select count(*) from activity_log where session_id='$S_DONE')||'/'||(select count(*) from activity_log where workspace_id='$WS' and session_id is null and action<>'session.deleted')")" "activity_log: session.deleted 1행 · 그 세션의 행 0 · 고아(session_id NULL) 0 (있었던 행 $ACT0)"
chk B.14 "삭제 대상(완료) $RUN/$S_DONE" "$(psqlq "select payload->>'title' ||'/'|| (payload->>'session_id') from activity_log where action='session.deleted' and object_id='$S_DONE'")" "session.deleted payload {title, session_id}"
sleep 1; kill "$SSE_PID" 2>/dev/null || true; rm -f "$OUT/79-sse.pid"
chk B.15 1 "$(grep -c '^event: session.deleted' "$SSE" || true)" "SSE session.deleted 1 프레임"
chk B.16 "$S_DONE" "$(grep -A1 '^event: session.deleted' "$SSE" | grep '^data:' | sed 's/^data: //' | jq -r .payload.session_id)" "프레임 payload.session_id"
chk B.17 1 "$(psqlq "select count(*) from stream_event where type='session.deleted' and session_id='$S_DONE'")" "stream_event 에 남아 백필 가능"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. worktree 세션 — 미병합 409 → 병합 뒤 204 → gc 명령 → 없는 행 영수증 조용히 소비"
# 이 컴퓨터의 probe 는 repos 가 비어 있어 worktree 세션의 턴은 claim 되지 않는다(S-62 진단) — 턴 없이
# §6 보고만으로 workdir 행을 만들고 세션은 cancel 로 끝낸다. 삭제가 읽는 것은 행과 상태다.
S_WT="$(mk_session "삭제 대상(워크트리) $RUN" "$(jq -nc --arg r "$ROOT/repo" '{kind:"worktree",repo_path:$r}')")"
WT_PATH="$ROOT/worktrees/$S_WT/lead"
report() { # SESSION MERGED DIRTY AHEAD [GC_JSON] → §6 보고 코드
  local gc="${5:-null}"
  daemon_api_code "runtimes/$RID/workdirs" "$(jq -nc --arg s "$1" --arg a "$AG" --arg p "$WT_PATH" --argjson m "$2" --argjson d "$3" --argjson n "$4" --argjson gc "$gc" \
    '{workdirs:[{kind:"worktree",path:$p,session_id:$s,agent_id:$a,bytes:1024,git:{branch:"colab/lead",merged:$m,dirty:$d,commits_ahead:$n}} + (if $gc==null then {} else {gc:$gc} end)]}')"
}
chk C.1 200 "$(report "$S_WT" false false 1)" "§6 보고: 미병합 커밋 1"
WD="$(psqlq "select id from workdir where session_id='$S_WT'")"
chk C.2 "worktree/f/1" "$(psqlq "select kind::text||'/'||case when merged then 't' else 'f' end||'/'||commits_ahead from workdir where id='${WD:-00000000-0000-0000-0000-000000000000}'")" "workdir 행(merged=false, ahead=1)"
api_ok POST "/sessions/$S_WT/cancel" '{"reason":"여기까지"}' >/dev/null
chk C.3 cancelled "$(psqlq "select status from session where id='$S_WT'")" "cancelSession → cancelled"
R="$(api DELETE "/sessions/$S_WT")"; api_body <<<"$R" | jq . > "$OUT/79-409-unmerged.json"
chk C.4 "409/workdir_unmerged" "$(api_code <<<"$R")/$(api_body <<<"$R" | jq -r .code)" "미병합 worktree → 409 workdir_unmerged"
chk C.5 "$WD/worktree/$WT_PATH" "$(api_body <<<"$R" | jq -r '.workdirs[0].id+"/"+.workdirs[0].kind+"/"+.workdirs[0].path_or_ref')" "Problem.workdirs[0] = 그 행"
chk C.6 1 "$(psqlq "select count(*) from session where id='$S_WT'")" "세션은 남아 있다"
chk C.7 200 "$(report "$S_WT" true false 0)" "§6 보고: 병합됨 · 클린"
chk C.8 204 "$(api DELETE "/sessions/$S_WT" | api_code)" "삭제 → 204"
chk C.9 0 "$(psqlq "select count(*) from workdir where id='${WD:-00000000-0000-0000-0000-000000000000}'")" "workdir 행 0 (행을 먼저 지운다)"
CL="$(claim)"; echo "$CL" | jq . > "$OUT/79-claim-gc.json"
GC="$(jq -c --arg s "$S_WT" '[.commands[]|select(.type=="gc" and .session_id==$s)][0]' <<<"$CL")"
chk C.10 "$WD/$WT_PATH" "$(jq -r '.workdirs[0].id+"/"+.workdirs[0].path' <<<"$GC")" "claim 응답의 gc {workdirs:[{id,path}]}"
EV0="$(psqlq "select count(*) from task_event")"
# 영수증 — 데몬 실제 모양: session_id 는 명령의 것, id 는 gc 안에, 상위 id 없음. 서버엔 그 행도 세션도 없다.
chk C.11 200 "$(report "$S_WT" true false 0 "$(jq -nc --arg id "$WD" '{status:"deleted",id:$id}')")" "없는 행을 가리키는 §6 영수증 → 200"
chk C.12 workdir_report "$(psqlq "select coalesce(consumed_by,'-') from daemon_command where type='gc' and session_id='$S_WT'")" "gc 명령 소비(consumed_by=workdir_report)"
chk C.13 "$EV0" "$(psqlq "select count(*) from task_event")" "피드(task_event)에 남기지 않았다"
chk C.14 0 "$(psqlq "select count(*) from workdir where session_id='$S_WT'")" "영수증이 workdir 행을 되살리지 않았다"
chk C.15 "[]" "$(claim | jq -c --arg s "$S_WT" '[.commands[]|select(.session_id==$s)]')" "다음 claim 에 그 세션의 명령 없음"
S_ADM="$(mk_session "관리자가 지운다 $RUN" '{"kind":"none"}')"
IFS=$'\t' read -r T_ADM _ <<<"$(run_turn "$S_ADM")"; [ -n "$T_ADM" ] && finish_turn "$T_ADM"
api_ok POST "/sessions/$S_ADM/cancel" '{"reason":"끝"}' >/dev/null
chk C.16 204 "$(as "$COOKIE_ADM" DELETE "/sessions/$S_ADM" | api_code)" "admin(Director 아님) → 204"
chk C.17 1 "$(psqlq "select count(*) from session where id='$S_ACT'")" "진행 중 세션은 그대로"

step "결과: $CHECKS"
printf '%s\n' "PASS $(grep -c $'\tPASS\t' "$CHECKS") · FAIL $FAILS" | tee "$OUT/79-summary.txt"
[ "$FAILS" = 0 ]
