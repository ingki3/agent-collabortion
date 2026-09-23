#!/usr/bin/env bash
# e2e/p5/88_room_gate.sh — T-R1b1 실서버 스모크: 방 단위 게이트 (PRD v0.19 FR-2.4 · FR-2A.3 · FR-2.1.1 ·
# FR-3.1.1 · FR-3.5 · §3.1 · §12.1-5·9) — **데몬 없이**, 데몬 역할(claim·phase·heartbeat·finish)은 curl 로 흉내
# (70_·79_·80_ 의 레시피).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 15s
#
# 재는 것 (판정 표 out/88-checks.tsv). 절마다 워크스페이스·컴퓨터를 새로 짝지어 claim 이 서로의 task 를 집지 않게 한다.
#   A. 방 예산 멈춤 → 방장 승인: 번들 limits.budget_usd = 방 잔여 · heartbeat usage 가 방 예산을 넘으면
#      room.blocked_reason=budget(blocked_detail 수·works_stopped) · 옛 getSession 은 paused(budget) 그대로 ·
#      확인 요청 approver_spec=room_owner · 방의 새 task 는 나가지 않는다 · unblockRoom 409 not_manual ·
#      승인(budget_override_usd) 한 번에 방이 풀리고 기다리던 task 가 나간다.
#   B·E. 루프 방 멈춤 + 부방장 위임 시각: 시간당 상한 1 로 에이전트 멘션 2번 → room.blocked_reason=loop ·
#      옛 getSession paused(loop) · 받은 요청 room_paused(방장) · 경고 loop_limit. 부방장 없으면 워크스페이스
#      owner 최고참이, 부방장을 두면 부방장이 기한 절반부터 답한다(그 전엔 403 deputy_not_yet + can_respond_from,
#      일반 멤버는 403 + null). 승인 → 방이 풀리고 세션 active.
#   H. 경계(Lead Q2): Director 가 멈춘 미션(표식 없는 paused)은 방 멈춤·해제에 끌려가지 않는다.
#   C. manual 멈춤·해제: 멤버 403 · 방장 200(blocked_by_user) · 실행 중 턴에 cancel 명령 · 새 task 안 나감 ·
#      시스템 메시지·activity_log · 409 already_blocked · 해제 200 → task 나감 · 409 not_blocked.
#   D. 전역 동시 상한: 에이전트 max_concurrent_tasks=1 이 방을 가로지른다 → 두 번째 방의 task queued_reason
#      agent_global(task·lane 응답에도) → 첫 턴이 끝나면 나가고 queued_reason 이 지워진다.
#   F. isolation_confirm: 저장소가 있는 컴퓨터로 none 방의 첫 실행 → 보류(task 0 · runtime_id null · 요청 1 ·
#      받은 요청 isolation_confirm · purpose isolation) → 승인 → worktree + 그 저장소 · 컴퓨터 고정 ·
#      「이 방은 …에서 돕니다」 · 번들 workdir.kind worktree.
#   W. 경로 없는 worktree 방의 첫 실행(T-S-wt · SCREEN §4.5 ⓘ): room_defaults worktree 로 createRoom(kind 만) →
#      저장소 없는 컴퓨터는 집지 않고 queued_reason runtime · 저장소 둘 → 방장에게 choice(purpose isolation) →
#      고른 저장소로 고정·「…의 〈저장소〉 에서 워크트리로 돕니다」·번들 worktree · 저장소 하나 → 묻지 않고 바로 나간다.
#   G. 귀속(FR-3.1.1) previewTriggers: 실행 중 lane 의 에이전트 멘션 → work_source running_lane · 스레드 답글 →
#      thread · 그 밖(방당 미션 1, R1b1 호환 규칙) → chosen · 게시된 메시지·task 의 work_id = 그 미션.
#
# 스택(T-R1b1): server :8130 · pg :5479 · 컨테이너 colab-pg-r1b1-5479. 다른 워커 스택과 겹치지 않는다(§0-13).
# 사용: SERVER_URL=http://localhost:8130 PG_PORT=5479 PG_CONTAINER=colab-pg-r1b1-5479 bash e2e/p5/up.sh
#       bash e2e/p5/88_room_gate.sh
#       SERVER_URL=http://localhost:8130 PG_PORT=5479 PG_CONTAINER=colab-pg-r1b1-5479 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8130}"
export PG_PORT="${PG_PORT:-5479}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-r1b1-5479}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/88-checks.tsv"; : > "$CHECKS"
API="$SERVER_URL/api/v1"
C_DIR="$OUT/88-c-dir.txt"; C_DEP="$OUT/88-c-dep.txt"; C_O2="$OUT/88-c-o2.txt"; C_MEM="$OUT/88-c-mem.txt"
rm -f "$C_DIR" "$C_DEP" "$C_O2" "$C_MEM"
COOKIE="$C_DIR"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-R1b1 ports"
CAPS='[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]'

as() { local c="$1"; shift; COOKIE="$c" api "$@"; }
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
# bundle_of CLAIM_JSON SESSION [AGENT] → 그 세션(·에이전트)의 번들 한 개
bundle_of() { jq -c --arg s "$2" --arg a "${3:-}" '[.tasks[]|select(.task.session_id==$s and ($a=="" or .task.agent_id==$a))][0] // empty' <<<"$1"; }
n_of() { jq --arg s "$2" '[.tasks[]|select(.task.session_id==$s)]|length' <<<"$1"; }
running() { daemon_api "tasks/$1/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null; }
finish() { daemon_api "tasks/$1/attempts/1/finish" '{"outcome":"completed","stop_reason":"end_turn","transport":"acp","last_seq":0,"usage":{"input_tokens":10,"output_tokens":5,"cost_usd":0.001,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null; }
agent_post() { # SESSION TOKEN CONTENT → 응답 본문
  curl -sS -X POST "$API/sessions/$1/messages" -H "Authorization: Bearer $2" -H "Idempotency-Key: $(uuid)" -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg c "$3" '{content:$c}')"
}
room_col() { psqlq "select coalesce(($2)::text,'-') from room where id='$1'"; }
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" '{name:$n,role:"lead",role_description:"d",instructions:"짧게",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
# fresh NAME → WS·RID·DTOK·LEAD·R (새 워크스페이스 + curl 페어링 + 에이전트 둘)
fresh() {
  WS="$(create_workspace "R1b1 $1 $RUN")"
  IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-$1" "$CAPS" "/tmp/colab-r1b1-$RUN")"
  export DTOK RID
  LEAD="$(mk_agent Lead)"; R="$(mk_agent R)"
}
# mk_session TITLE ASSIGNEE [PINNED:yes|no] [EXTRA_JSON] → 세션(=방) id. 참여자 Lead·R.
mk_session() {
  local extra="${4:-}"; [ -n "$extra" ] || extra='{}'
  api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "$1" --arg a "$2" --arg l "$LEAD" --arg r "$R" --arg rt "$RID" --arg pin "${3:-yes}" --argjson x "$extra" \
    '{title:$t,goal:"짧은 인사말 한 줄",isolation:{kind:"none"},participants:[{agent_id:$l},{agent_id:$r}],assignee_agent_id:$a}
     + (if $pin=="yes" then {runtime_id:$rt} else {} end) + $x')" | jq -r .id
}
join_ws() { # COOKIEFILE EMAIL NAME ROLE → user id (가입 + 이 워크스페이스 멤버로)
  local uid; uid="$(COOKIE="$1" signup "$2" password123 "$3")"
  psqlq "insert into member (workspace_id, user_id, role, created_at) values ('$WS', '$uid', '$4', now() + interval '1 minute')" >/dev/null
  printf '%s' "$uid"
}

step "0. 방장 Dir 가입"
DIR_ID="$(signup "r1b1-dir-$RUN@example.com" password123 "Dir")"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. 방 예산 멈춤 → 방장 승인 (FR-2A.3 방의 상한 · K-10)"
fresh a
SA="$(mk_session "A 방 예산 $RUN" "$LEAD" yes '{"limits":{"budget_usd":0.01}}')"
CL="$(claim)"; B="$(bundle_of "$CL" "$SA")"; [ -n "$B" ] || die "A: claim 에 $SA 의 task 가 없다: $CL"
TA="$(jq -r .task.id <<<"$B")"
chk A.1 0.01 "$(jq -r '.limits.budget_usd // "-"' <<<"$B")" "번들 limits.budget_usd = min(미션 잔여, 방 잔여) — 미션 한도 없음 → 방 잔여"
running "$TA"
daemon_api "tasks/$TA/attempts/1/heartbeat" '{"usage":{"input_tokens":1000,"output_tokens":1000,"cost_usd":0.05,"estimated":false,"model":"claude-sonnet-5"},"last_seq":0}' > "$OUT/88-A-hb.json"
chk A.2 budget "$(room_col "$SA" blocked_reason)" "heartbeat usage \$0.05 > 방 예산 \$0.01 → room.blocked_reason=budget"
chk A.3 "0.01/0.05/1" "$(psqlq "select (blocked_detail->>'budget_usd')||'/'||(blocked_detail->>'cost_usd')||'/'||(blocked_detail->>'works_stopped') from room where id='$SA'")" "blocked_detail — 한도·쓴 돈·멈춘 미션 수(수는 칸으로)"
chk A.4 "paused/budget" "$(api_ok GET "/sessions/$SA" | jq -r '.status+"/"+(.paused_reason//"-")')" "옛 getSession 은 paused(budget) 그대로(미러, Lead Q2)"
HA="$(psqlq "select id from hitl_request where session_id='$SA' and purpose='budget' and status='open'")"
chk A.5 "room_owner/t/t" "$(psqlq "select approver_spec||'/'||(task_id is null)::text::char||'/'||(work_id is null)::text::char from hitl_request where id='$HA'")" "확인 요청은 방의 것 — approver_spec room_owner · task 없음 · 미션 없음"
chk A.6 1 "$(psqlq "select count(*) from inbox_item i join member m on m.id=i.member_id where i.ref_id='$HA' and m.user_id='$DIR_ID'")" "방장 받은 요청 1"
post_message "$SA" "$(mention R "$R") 이것도 부탁" >/dev/null
chk A.7 0 "$(n_of "$(claim)" "$SA")" "멈춘 방의 새 task 는 나가지 않는다(claim 게이트 한 줄)"
chk A.8 "409/not_manual" "$(R_="$(api POST "/rooms/$SA/unblock")"; echo "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)")" "unblockRoom 은 manual 만 — 예산 멈춤은 409 not_manual"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HA" '{"approved":true,"budget_override_usd":1}')"
chk A.9 200 "$CODE" "방장 승인(budget_override_usd 1) → 200"
chk A.10 "-/active" "$(room_col "$SA" blocked_reason)/$(api_ok GET "/sessions/$SA" | jq -r .status)" "승인 한 번에 방이 풀리고 세션 active"
chk A.11 1 "$(claim | jq --arg s "$SA" --arg a "$R" '[.tasks[]|select(.task.session_id==$s and .task.agent_id==$a)]|length')" "기다리던 R task 가 나간다"

# ───────────────────────────── E + B ────────────────────────────────────────
step "E. 루프 방 멈춤 (FR-3.5 · §3.1 — 루프 상한은 방 단위)"
fresh e
psqlq "update workspace_settings set loop_limits = '{\"max_hops_per_hour\": 1}' where workspace_id='$WS'" >/dev/null
SE="$(mk_session "E 루프 $RUN" "$LEAD")"
CL="$(claim)"; B="$(bundle_of "$CL" "$SE" "$LEAD")"; [ -n "$B" ] || die "E: claim 에 Lead task 가 없다: $CL"
TE="$(jq -r .task.id <<<"$B")"; TOKE="$(jq -r .task_token <<<"$B")"; running "$TE"
agent_post "$SE" "$TOKE" "$(mention R "$R") 하나" > "$OUT/88-E-1.json"
agent_post "$SE" "$TOKE" "$(mention R "$R") 둘" > "$OUT/88-E-2.json"
chk E.1 loop_limit "$(jq -r '[.warnings[]|select(.code=="loop_limit")][0].code // "-"' "$OUT/88-E-2.json")" "두 번째 에이전트 멘션 → 경고 loop_limit"
chk E.2 loop "$(room_col "$SE" blocked_reason)" "room.blocked_reason=loop(방이 멈춘다)"
chk E.3 "paused/loop" "$(api_ok GET "/sessions/$SE" | jq -r '.status+"/"+(.paused_reason//"-")')" "옛 getSession 은 paused(loop) 그대로"
HE="$(psqlq "select id from hitl_request where session_id='$SE' and purpose='loop' and status='open'")"
chk E.4 room_owner "$(psqlq "select approver_spec from hitl_request where id='$HE'")" "루프 확인 요청 approver_spec room_owner"
chk E.5 1 "$(psqlq "select count(*) from inbox_item i join member m on m.id=i.member_id where i.ref_id='$HE' and i.type='room_paused' and m.user_id='$DIR_ID'")" "방장 받은 요청 room_paused"

step "B. 방장 부재 위임 시각 (FR-2A.3 · SCR-A G-10)"
O2_ID="$(join_ws "$C_O2" "r1b1-o2-$RUN@example.com" O2 owner)"
DEP_ID="$(join_ws "$C_DEP" "r1b1-dep-$RUN@example.com" Dep member)"
MEM_ID="$(join_ws "$C_MEM" "r1b1-mem-$RUN@example.com" Mem member)"
CREATED="$(psqlq "select to_char((created_at + (due_at - created_at)/2) at time zone 'UTC', 'YYYY-MM-DD\"T\"HH24:MI') from hitl_request where id='$HE'")"
R_="$(as "$C_O2" POST "/hitl-requests/$HE/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)")"
chk B.1 "403/deputy_not_yet/$CREATED" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r '.code+"/"+((.can_respond_from // "-")[0:16])')" "부방장 없음 → owner 최고참(O2): 기한 절반 전 403 deputy_not_yet + can_respond_from(절반 시각)"
chk B.2 1 "$(api_body <<<"$R_" | jq -r .detail | grep -c '방장 응답 대기 중' || true)" "문장은 방장을 기다린다고 말한다"
R_="$(as "$C_MEM" POST "/hitl-requests/$HE/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)")"
chk B.3 "403/null" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r '.can_respond_from // "null"')" "일반 멤버는 403 + can_respond_from null(끝내 권한 없음)"
psqlq "update room set deputy_owner_user_id='$DEP_ID' where id='$SE'" >/dev/null
R_="$(as "$C_DEP" GET "/hitl-requests/$HE")"
chk B.4 "false/$CREATED" "$(api_body <<<"$R_" | jq -r '(.can_respond|tostring)+"/"+((.can_respond_from // "-")[0:16])')" "부방장을 두면 부방장이 위임자 — 카드 can_respond false + 절반 시각"
backdate_hitl "$HE" $((13*3600))
R_="$(as "$C_O2" POST "/hitl-requests/$HE/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)")"
chk B.5 403 "$(api_code <<<"$R_")" "부방장이 있으면 O2 는 절반이 지나도 403"
R_="$(as "$C_DEP" POST "/hitl-requests/$HE/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)")"
chk B.6 200 "$(api_code <<<"$R_")" "절반 경과 뒤 부방장 승인 200"
chk E.6 "-/active" "$(room_col "$SE" blocked_reason)/$(api_ok GET "/sessions/$SE" | jq -r .status)" "승인이 방을 푼다(루프 해제는 승인 HITL 의 결과)"
chk E.7 2 "$(psqlq "select count(*) from session_hop where session_id='$SE' and from_agent_id is null")" "사람 hop = 시작 1 + 승인 1 (깊이·짝 카운터를 사람 아래에서 다시, 시간당은 그대로)"

step "H. 경계 — 표식 없는 paused(Director 멈춤)는 방 멈춤·해제에 끌려가지 않는다 (Lead Q2)"
SH="$(mk_session "H 경계 $RUN" "$LEAD")"
CL="$(claim)"; B="$(bundle_of "$CL" "$SH" "$LEAD")"; [ -n "$B" ] || die "H: claim 에 Lead task 가 없다: $CL"
TH="$(jq -r .task.id <<<"$B")"; TOKH="$(jq -r .task_token <<<"$B")"; running "$TH"
api_ok POST "/sessions/$SH/pause" '{}' >/dev/null
agent_post "$SH" "$TOKH" "$(mention R "$R") 하나" >/dev/null
agent_post "$SH" "$TOKH" "$(mention R "$R") 둘" >/dev/null
chk H.1 "loop/director/f" "$(room_col "$SH" blocked_reason)/$(psqlq "select paused_reason::text||'/'||coalesce((paused_detail->>'room_blocked'),'f')::char from work where room_id='$SH'")" "방은 loop 로 멈추고 Director 가 멈춘 미션은 표식 없이 paused(director) 그대로"
HH="$(psqlq "select id from hitl_request where session_id='$SH' and purpose='loop' and status='open'")"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HH" '{"approved":true}')"
chk H.2 "200/-/paused/director" "$CODE/$(room_col "$SH" blocked_reason)/$(api_ok GET "/sessions/$SH" | jq -r '.status+"/"+(.paused_reason//"-")')" "방이 풀려도 미션은 paused(director) — 방이 멈춘 것만 방 해제로 돌아온다"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. manual 멈춤·해제 (FR-2.4 · blockRoom · unblockRoom)"
fresh c
MEMC_ID="$(join_ws "$C_MEM" "r1b1-memc-$RUN@example.com" MemC member)"
SC="$(mk_session "C 수동 $RUN" "$LEAD")"
CL="$(claim)"; B="$(bundle_of "$CL" "$SC" "$LEAD")"; [ -n "$B" ] || die "C: claim 에 Lead task 가 없다: $CL"
TC="$(jq -r .task.id <<<"$B")"; running "$TC"
R_="$(as "$C_MEM" POST "/rooms/$SC/block")"
chk C.1 "403/not_room_manager" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)" "일반 멤버는 멈출 수 없다"
R_="$(api POST "/rooms/$SC/block")"; api_body <<<"$R_" | jq . > "$OUT/88-C-block.json"
chk C.2 "200/manual/Dir/1" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r '.blocked_reason+"/"+.blocked_detail.blocked_by_user.display_name+"/"+(.blocked_detail.works_stopped|tostring)')" "방장이 멈춤 → Room(blocked_reason manual · blocked_by_user · works_stopped)"
chk C.3 1 "$(claim | jq --arg t "$TC" '[.commands[]|select(.type=="cancel" and .task_id==$t)]|length')" "실행 중 턴에 cancel 명령(FR-3.4 「중단」, §8.2.2)"
chk C.4 "1/1" "$(psqlq "select count(*) from message where session_id='$SC' and kind='system' and content like '%이 방을 멈췄습니다%'")/$(psqlq "select count(*) from activity_log where session_id='$SC' and action='room.blocked'")" "타임라인 시스템 메시지 1 · activity_log room.blocked 1"
post_message "$SC" "$(mention R "$R") 멈춘 동안" >/dev/null
chk C.5 0 "$(n_of "$(claim)" "$SC")" "멈춘 방의 새 task 는 나가지 않는다"
R_="$(api POST "/rooms/$SC/block")"
chk C.6 "409/already_blocked" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)" "두 번째 멈춤 409 already_blocked"
R_="$(as "$C_MEM" POST "/rooms/$SC/unblock")"
chk C.7 403 "$(api_code <<<"$R_")" "일반 멤버는 풀 수 없다"
R_="$(api POST "/rooms/$SC/unblock")"
chk C.8 "200/null" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r '.blocked_reason // "null"')" "방장 해제 200"
chk C.9 1 "$(n_of "$(claim)" "$SC")" "해제 뒤 기다리던 task 가 나간다"
chk C.10 1 "$(psqlq "select count(*) from activity_log where session_id='$SC' and action='room.unblocked'")" "activity_log room.unblocked 1"
R_="$(api POST "/rooms/$SC/unblock")"
chk C.11 "409/not_blocked" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)" "멈추지 않은 방 해제 409 not_blocked"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 전역 동시 상한 — max_concurrent_tasks 는 방을 가로지른다 (§12.1-5 · queued_reason)"
fresh d
psqlq "update agent set max_concurrent_tasks = 1 where id='$R'" >/dev/null
SD1="$(mk_session "D 방1 $RUN" "$R")"
CL="$(claim)"; B="$(bundle_of "$CL" "$SD1" "$R")"; [ -n "$B" ] || die "D: claim 에 방1 R task 가 없다: $CL"
TD1="$(jq -r .task.id <<<"$B")"; running "$TD1"
SD2="$(mk_session "D 방2 $RUN" "$R")"
TD2="$(psqlq "select id from task where session_id='$SD2' order by created_at limit 1")"
chk D.1 0 "$(n_of "$(claim)" "$SD2")" "R 이 방1에서 일하는 동안 방2의 R task 는 나가지 않는다(상한 1, 전역)"
chk D.2 agent_global "$(psqlq "select coalesce(queued_reason::text,'-') from task where id='$TD2'")" "task.queued_reason = agent_global(다른 방에서 작업 중)"
chk D.3 agent_global "$(api_ok GET "/tasks/$TD2" | jq -r '.queued_reason // "-"')" "getTask 응답에도 queued_reason"
chk D.4 agent_global "$(api_ok GET "/sessions/$SD2/lanes" | jq -r '.[0].queued_reason // "-"')" "listLanes 의 lane 에도 queued_reason(첫 대기 task 의 것)"
finish "$TD1"
chk D.5 1 "$(n_of "$(claim)" "$SD2")" "방1 턴이 끝나면 방2의 R task 가 나간다"
chk D.6 "dispatched/-" "$(psqlq "select status::text||'/'||coalesce(queued_reason::text,'-') from task where id='$TD2'")" "나가면 queued_reason 이 지워진다"

# ───────────────────────────── F ─────────────────────────────────────────────
step "F. isolation_confirm — 저장소 있는 컴퓨터로 첫 실행 보류 → 승인 (FR-2.1.1)"
fresh f
REPO="/tmp/colab-r1b1-$RUN/repo"
daemon_api "runtimes/$RID/probe" "$(jq -nc --arg root "/tmp/colab-r1b1-$RUN" --arg repo "$REPO" --argjson caps "$CAPS" \
  '{daemon_version:"0.1.0",hostname:"mac-f",capabilities:$caps,repos:[{path:$repo,remote_url:"",branch:"main",clean:true}],workdir_root:$root,disk:{used_bytes:0},colab_cli:{present:true,version:"0.1.0"}}')" >/dev/null
SF="$(mk_session "F 격리 확인 $RUN" "$LEAD" no)"
chk F.1 0 "$(n_of "$(claim)" "$SF")" "none 방의 첫 실행이 저장소 있는 컴퓨터로 → 보류(task 0)"
chk F.2 "-/t" "$(room_col "$SF" runtime_id)/$(psqlq "select (isolation_pending is not null)::text::char from room where id='$SF'")" "runtime_id 미고정 · 격리 확인 대기"
HF="$(psqlq "select id from hitl_request where session_id='$SF' and purpose='isolation' and status='open'")"
chk F.3 "room_owner/approval/isolation" "$(api_ok GET "/hitl-requests/$HF" | jq -r '.approver_spec+"/"+.type+"/"+(.purpose//"-")')" "확인 요청 room_owner · approval · purpose isolation(openapi 0.2.2)"
chk F.4 "isolation_confirm/approve" "$(api_ok GET "/inbox?workspace_id=$WS" | jq -r --arg h "$HF" '[.items[]|select(.ref_id==$h)][0]|.type+"/"+(.actions[0]//"-")')" "받은 요청 isolation_confirm(승인·거절 버튼)"
claim >/dev/null
chk F.5 1 "$(psqlq "select count(*) from hitl_request where session_id='$SF' and purpose='isolation'")" "다음 claim 이 두 번 묻지 않는다"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HF" '{"approved":true}')"
chk F.6 200 "$CODE" "방장 승인 → 200"
chk F.7 "worktree/$REPO/$RID" "$(psqlq "select (isolation->>'kind')||'/'||(isolation->>'repo_path')||'/'||runtime_id from room where id='$SF'")" "승인 = worktree + 물어본 저장소 · 그 컴퓨터로 고정"
chk F.8 1 "$(psqlq "select count(*) from message where session_id='$SF' and kind='system' and content like '이 방은 mac-f에서 돕니다%'")" "「이 방은 mac-f에서 돕니다 …」 시스템 메시지"
CL="$(claim)"; B="$(bundle_of "$CL" "$SF")"
chk F.9 "worktree/$REPO" "$(jq -r '(.workdir.kind // "-")+"/"+(.workdir.repo_path // "-")' <<<"$B")" "첫 실행이 나간다 — 번들 workdir 이 worktree(그 저장소)"

# ───────────────────────────── W ─────────────────────────────────────────────
step "W. 경로 없는 worktree 방의 첫 실행 (T-S-wt — 조용히 queued 로 멈추지 않는다)"
fresh w
probe_repos() { daemon_api "runtimes/$RID/probe" "$(jq -nc --arg root "/tmp/colab-r1b1-$RUN" --argjson repos "$1" --argjson caps "$CAPS" \
  '{daemon_version:"0.1.0",hostname:"mac-w",capabilities:$caps,repos:[$repos[]|{path:.,remote_url:"",branch:"main",clean:true}],workdir_root:$root,disk:{used_bytes:0},colab_cli:{present:true,version:"0.1.0"}}')" >/dev/null; }
# mk_room NAME → createRoom(이름 한 칸) + Lead 초대 + Lead 멘션 한 줄 → 방 id
mk_room() {
  local id; id="$(api_ok POST "/workspaces/$WS/rooms" "$(jq -nc --arg n "$1" '{name:$n}')" -H "Idempotency-Key: $(uuid)" | jq -r .id)"
  api_ok POST "/rooms/$id/participants" "$(jq -nc --arg a "$LEAD" '{agent_id:$a}')" >/dev/null
  post_message "$id" "$(mention Lead "$LEAD") 인사 한 줄" >/dev/null
  printf '%s' "$id"
}
api_ok PATCH "/workspaces/$WS/settings" '{"room_defaults":{"isolation_kind":"worktree"}}' >/dev/null
SW="$(mk_room "W 워크트리 $RUN")"
chk W.1 "worktree/-/-" "$(psqlq "select (isolation->>'kind')||'/'||coalesce(isolation->>'repo_path','-')||'/'||coalesce(runtime_id::text,'-') from room where id='$SW'")" "전제: 격리 kind 만 상속 · 경로·컴퓨터 없음"
chk W.2 0 "$(n_of "$(claim)" "$SW")" "저장소 없는 컴퓨터는 집지 않는다"
LW="$(psqlq "select lane_id from task where session_id='$SW' and status='queued' limit 1")"
chk W.3 "runtime/-" "$(api_ok GET "/sessions/$SW/lanes" | jq -r --arg l "$LW" '[.[]|select(.id==$l)][0].queued_reason // "-"')/$(room_col "$SW" runtime_id)" "기다리는 이유 queued_reason runtime(「저장소가 있는 컴퓨터를 기다립니다」) · 고정 안 됨"
RA="/tmp/colab-r1b1-$RUN/repo-a"; RB="/tmp/colab-r1b1-$RUN/repo-b"
probe_repos "$(jq -nc --arg a "$RA" --arg b "$RB" '[$a,$b]')"
chk W.4 0 "$(n_of "$(claim)" "$SW")" "저장소 둘 → 방장이 고를 때까지 보류"
HW="$(psqlq "select id from hitl_request where session_id='$SW' and purpose='isolation' and status='open'")"
chk W.5 "room_owner/choice/isolation/2" "$(api_ok GET "/hitl-requests/$HW" | jq -r '.approver_spec+"/"+.type+"/"+(.purpose//"-")+"/"+(.options|length|tostring)')" "어느 저장소로 나눌까요 — choice · 보기 = 저장소 둘"
chk W.6 isolation_confirm "$(api_ok GET "/inbox?workspace_id=$WS" | jq -r --arg h "$HW" '[.items[]|select(.ref_id==$h)][0].type // "-"')" "받은 요청 isolation_confirm(같은 자리)"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HW" "$(jq -nc --arg b "$RB" '{answer:$b}')")"
chk W.7 200 "$CODE" "방장이 repo-b 를 고른다"
CL="$(claim)"; B="$(bundle_of "$CL" "$SW")"
chk W.8 "worktree/$RB/$RID" "$(jq -r '(.workdir.kind // "-")+"/"+(.workdir.repo_path // "-")' <<<"$B")/$(room_col "$SW" runtime_id)" "고른 저장소로 첫 실행이 나간다 · 컴퓨터 고정"
chk W.9 1 "$(psqlq "select count(*) from message where session_id='$SW' and kind='system' and content like '이 방은 mac-w의 $RB 에서 워크트리로 돕니다%'")" "「이 방은 mac-w의 …/repo-b 에서 워크트리로 돕니다」"
probe_repos "$(jq -nc --arg a "$RA" '[$a]')"
SW1="$(mk_room "W 저장소 하나 $RUN")"
CL="$(claim)"; B="$(bundle_of "$CL" "$SW1")"
chk W.10 "worktree/$RA/0" "$(jq -r '(.workdir.kind // "-")+"/"+(.workdir.repo_path // "-")' <<<"$B")/$(psqlq "select count(*) from hitl_request where session_id='$SW1' and purpose='isolation'")" "저장소 하나 → 묻지 않고 그 저장소로 첫 실행(같은 claim)"

# ───────────────────────────── G ─────────────────────────────────────────────
step "G. 메시지 미션 귀속 — previewTriggers work·work_source (FR-3.1.1)"
fresh g
SG="$(mk_session "G 귀속 $RUN" "$LEAD")"
WG="$(psqlq "select id from work where room_id='$SG'")"
CL="$(claim)"; B="$(bundle_of "$CL" "$SG" "$LEAD")"; [ -n "$B" ] || die "G: claim 에 Lead task 가 없다: $CL"
TG="$(jq -r .task.id <<<"$B")"; running "$TG"
chk G.0 running "$(psqlq "select status from lane where id=(select lane_id from task where id='$TG')")" "전제: Lead 의 lane 이 실행 중"
PV="$(api_ok POST "/sessions/$SG/messages/preview" "$(jq -nc --arg c "$(mention Lead "$LEAD") 진행 상황?" '{content:$c}')")"; echo "$PV" | jq . > "$OUT/88-G-preview-3.json"
chk G.1 "running_lane/$WG/G 귀속 $RUN" "$(jq -r '.work_source+"/"+(.work.id // "-")+"/"+(.work.title // "-")' <<<"$PV")" "규칙 3 — 실행 중 lane 의 에이전트 멘션 → running_lane, 칩에 미션 이름"
M1="$(post_message "$SG" "$(mention Lead "$LEAD") 진행 상황?" | jq -r .message.id)"
chk G.2 "$WG/$WG" "$(psqlq "select m.work_id||'/'||coalesce((select t.work_id::text from task t where t.trigger_message_id=m.id or m.id = any(t.coalesced_message_ids) limit 1),'-') from message m where m.id='$M1'")" "게시된 메시지와 그 task 의 work_id = 그 미션"
PV="$(api_ok POST "/sessions/$SG/messages/preview" "$(jq -nc --arg p "$M1" '{content:"덧붙여",parent_id:$p}')")"
chk G.3 "thread/$WG" "$(jq -r '.work_source+"/"+(.work.id // "-")' <<<"$PV")" "규칙 2 — 스레드 답글 → thread"
PV="$(api_ok POST "/sessions/$SG/messages/preview" '{"content":"/note 기록만"}')"
chk G.4 "chosen/$WG" "$(jq -r '.work_source+"/"+(.work.id // "-")' <<<"$PV")" "그 밖 — 방당 미션 1(R1b1 호환 규칙, 계약 enum 이 닫혀 chosen 으로 싣는다)"

step "결과: $CHECKS"
cat "$CHECKS" >&2
[ "$FAILS" = 0 ] && ok "88 all pass" || die "88: $FAILS fail"
