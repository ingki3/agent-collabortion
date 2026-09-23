#!/usr/bin/env bash
# e2e/p5/89_rooms.sh — T-R1b3 실서버 스모크: 방 API (openapi 0.2.x — PRD v0.19 FR-2 · FR-2.2 · FR-4.5 링크 · FR-5.3 · FR-8 · §12.1-4)
# — **데몬 없이** curl 로. 방에는 에이전트 턴이 없어도 되는 동작만 있다.
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 20s
#
# 재는 것 (판정 표 out/89-checks.tsv):
#   A. 방 만들기 — 컴퓨터 0대에서도 201 · 이름 한 칸 · 만든 사람이 방장 · my_capabilities · 빈 이름 422 · room_defaults 상속
#   B. 초대 — invited 방은 초대 안 된 멤버에게 404·목록 밖·옛 /sessions/{id}/messages·lanes 도 404 · 사람 초대 201 · room_invited(open_room) · 에이전트 초대는
#      부른 사람의 FR-1.9 로(403 not_invitable → owner 가 201 + warnings) · 이미 있음 409 · 워크스페이스 밖 422
#   C. 나가기 — 방장 409 is_owner · 열린 미션 Director 409 is_director · 본인 204 → 404 · 다시 초대하면 같은 행 ·
#      참여자가 남을 초대 403 · 방장 넘기기 · 부방장
#   D. 링크 — 대상 방 참여자 아님 403 not_participant_of_target(없는 방도 같은 답) · 201 · 양쪽 시스템 메시지 ·
#      중복 409 · 대상 방 쪽 방장이 풀기 204
#   E. 보관/해제 — 보관 200 · 보관된 방에 초대 409 room_archived · 기본 목록 밖 · include_archived · 해제 200 ·
#      진행 중 할 일이 있는 방 409 tasks_active
#   F. 삭제 — 진행 중 미션 409 works_active · 부방장 403 · 방장 204 → 404 · activity_log room.deleted 한 줄
#   G. 안 읽음 — 남의 메시지만 · 목록과 getRoom 같은 수 · markRoomRead → 0 · 뒤로 안 간다
#   H. SSE — room_id 거르기(다른 방 프레임 0) · invited 방 프레임이 초대 안 된 사람 스트림에 0 · room.unread 는 본인만 ·
#      participant.joined/left · room_link.updated · room.updated · room.deleted + session.deleted · envelope room_id
#   I. 활동 로그 — owner·admin 만 · room 거르기 · 감사 열람 기록
#   J. 옛 표면 — getSession·listSessions 200 그대로
#
# 스택(T-R1b3 배정): server :8135 · pg :5484 · 컨테이너 colab-pg-r1b3-e2e.
# 사용: export SERVER_URL=http://localhost:8135 PG_PORT=5484 PG_CONTAINER=colab-pg-r1b3-e2e
#       bash e2e/p5/up.sh && bash e2e/p5/89_rooms.sh ; bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8135}"
export PG_PORT="${PG_PORT:-5484}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-r1b3-e2e}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/89-checks.tsv"; : > "$CHECKS"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-R1b3 ports"

as() { COOKIE="$OUT/89-cookie-$1.txt"; }
rm -f "$OUT"/89-cookie-*.txt
# call METHOD PATH [JSON] [curl args…] → 전역 CODE · BODY
call() { local out; out="$(api "$@")"; CODE="$(api_code <<<"$out")"; BODY="$(api_body <<<"$out")"; }
code_of() { jq -r '.code // "-"' <<<"$BODY"; }
SSE_PIDS=()
cleanup() { local p; for p in "${SSE_PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT
sse() { # WHO FILE [QUERY] — 그 사람의 쿠키로 스트림을 연다
  local who="$1" f="$2" q="${3:-}"
  : > "$f"
  curl -sN -b "$OUT/89-cookie-$who.txt" "$API/workspaces/$WS/stream${q}" > "$f" 2>/dev/null &
  SSE_PIDS+=("$!")
}
frames() { # FILE TYPE [ROOM] → 그 타입 프레임 수(ROOM 이면 envelope room_id 까지)
  grep '^data: ' "$1" | sed 's/^data: //' | jq -r --arg t "$2" --arg r "${3:-}" 'select(.type==$t and ($r=="" or .room_id==$r)) | .id' | wc -l | tr -d ' '
}
frames_room() { # FILE ROOM → 그 방 프레임 수(타입 무관)
  grep '^data: ' "$1" | sed 's/^data: //' | jq -r --arg r "$2" 'select(.room_id==$r) | .id' | wc -l | tr -d ' '
}
pid_of() { # WHO ROOM USER_ID → participant id
  as "$1"; api_ok GET "/rooms/$2/participants" | jq -r --arg u "$3" '.items[]|select(.user.id==$u)|.id'
}

# ───────────────────────────── 0 ─────────────────────────────────────────────
step "0. 계정 넷(dir=owner · mem · oth · adm=admin) · 워크스페이스 · 에이전트 Lead(respond_to owner)"
as dir; DIR_UID="$(signup "r1b3-dir-$RUN@example.com" password123 "Dir")"
WS="$(create_workspace "R1b3 $RUN")"
AG="$(create_agent "$WS" "Lead" "claude-haiku-4-5-20251001")"
join() { # who role → <WHO>_UID
  local who="$1" role="$2" tok m up
  up="$(tr a-z A-Z <<<"$who")"
  as dir; tok="$(api_ok POST "/workspaces/$WS/invites" "$(jq -nc --arg r "$role" '{role:$r}')" | jq -r .token)"
  as "$who"; signup "r1b3-$who-$RUN@example.com" password123 "$who" >/dev/null
  m="$(api_ok POST "/invites/$tok/accept" '')"
  printf -v "${up}_UID" '%s' "$(jq -r .user.id <<<"$m")"
}
join mem member; join oth member; join adm admin
chk 0.1 0 "$(psqlq "select count(*) from runtime where workspace_id='$WS'")" "연결된 컴퓨터 0대"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. createRoom — 이름 한 칸, 컴퓨터 0대에서도 201"
as mem; call POST "/workspaces/$WS/rooms" '{"name":"89 설계"}' -H "Idempotency-Key: $(uuid)"
chk A.1 201 "$CODE" "컴퓨터 0대 → 201 (옛 createSession 의 409 no_runtime 없음)"
RA="$(jq -r .id <<<"$BODY")"
chk A.2 "$MEM_UID/owner/workspace/null" "$(jq -r '[.owner_user_id,.my_room_role,.visibility,(.runtime_id|tostring)]|join("/")' <<<"$BODY")" "만든 사람이 방장 · 기본 공개 · 컴퓨터는 첫 실행 때"
chk A.3 "post,invite,configure,link,block,archive,delete,transfer_owner,summarize" "$(jq -r '.my_capabilities|join(",")' <<<"$BODY")" "방장의 my_capabilities"
chk A.4 "3/5" "$(jq -r '"\(.limits.max_concurrent_works)/\(.limits.max_parallel_lanes)"' <<<"$BODY")" "limits 기본값(max_concurrent_works 3 · max_parallel_lanes 5)"
call POST "/workspaces/$WS/rooms" '{"name":"   "}'
chk A.5 "422/name" "$CODE/$(jq -r '.errors[0].field' <<<"$BODY")" "빈 이름 → 422"
as dir; api_ok PATCH "/workspaces/$WS/settings" '{"room_defaults":{"visibility":"invited"}}' >/dev/null
as oth; call POST "/workspaces/$WS/rooms" '{"name":"89 참고"}'
RB="$(jq -r .id <<<"$BODY")"
chk A.6 invited "$(jq -r .visibility <<<"$BODY")" "room_defaults.visibility 를 상속"
as dir; api_ok PATCH "/workspaces/$WS/settings" '{"room_defaults":{"visibility":"workspace"}}' >/dev/null

# SSE 를 연다: oth 는 워크스페이스 전체, mem 은 RB 만(room_id), dir 는 session_id 별칭으로 RA 만.
sse oth "$OUT/89-sse-oth.log"
sse mem "$OUT/89-sse-mem-rb.log" "?room_id=$RB"
sse dir "$OUT/89-sse-dir-ra.log" "?session_id=$RA"
sleep 1

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 초대 — invited 방 · 사람 · 에이전트(FR-1.9)"
as mem; api_ok PATCH "/rooms/$RA" '{"visibility":"invited"}' >/dev/null
SUMM="$(api_ok POST "/rooms/$RA/summaries" "$(jq -nc --arg s "$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ)" '{since:$s}')" | jq -r .id)"   # invited 가 된 뒤 이 방에 프레임을 하나 더
chk B.0 "summary/1" "$(psqlq "select kind||'/'||(summary_range ? 'message_ids')::int from message where id='$SUMM'")" "「여기까지 정리」 → summary 메시지 + 범위 기록"
as oth; call GET "/rooms/$RA"
chk B.1 404 "$CODE" "invited 방 — 초대 안 된 멤버에게 404(존재 숨김)"
chk B.2 0 "$(api_ok GET "/workspaces/$WS/rooms?participating=false" | jq -r --arg r "$RA" '[.items[]|select(.id==$r)]|length')" "목록(참여 안 한 방 포함)에도 없다"
# 옛 /sessions/* 별칭도 같은 방이다(리뷰 #291 R1-1 — 방 카드만 숨고 대화가 열려 있던 역전).
call GET "/sessions/$RA/messages"
chk B.2a "404/0" "$CODE/$(grep -c "$SUMM" <<<"$BODY")" "옛 /sessions/{id}/messages 도 404 — 본문이 안 나간다"
chk B.2b 404 "$(api GET "/sessions/$RA/lanes" | api_code)" "옛 /sessions/{id}/lanes 도 404(500 아님 — NN4)"
as dir; call GET "/rooms/$RA"
chk B.3 "200/null/false" "$CODE/$(jq -r '(.my_room_role|tostring)+"/"+((.my_capabilities|index("post"))!=null|tostring)' <<<"$BODY")" "ws owner 는 감사 열람(게시 버튼 없음)"
chk B.4 1 "$(psqlq "select count(*) from activity_log where session_id='$RA' and action='room.audit_viewed'")" "감사 열람이 activity_log 에"
as mem; call POST "/rooms/$RA/participants" "$(jq -nc --arg u "$OTH_UID" '{user_id:$u}')"
chk B.5 "201/user/member" "$CODE/$(jq -r '.kind+"/"+.room_role' <<<"$BODY")" "사람 초대 → 201"
as oth; INB="$(api_ok GET "/inbox?workspace_id=$WS" | jq -c --arg r "$RA" '[.items[]|select(.type=="room_invited" and .room_id==$r)][0]')"
chk B.6 "info/open_room" "$(jq -r '.severity+"/"+(.actions|join(","))' <<<"$INB")" "room_invited 카드(info · open_room)"
chk B.7 200 "$(api GET "/rooms/$RA" | api_code)" "초대 뒤 열람"
as mem; call POST "/rooms/$RA/participants" "$(jq -nc --arg u "$OTH_UID" '{user_id:$u}')"
chk B.8 "409/already_participant" "$CODE/$(code_of)" "이미 있음 → 409"
call POST "/rooms/$RA/participants" "$(jq -nc --arg u "$(uuid)" '{user_id:$u}')"
chk B.9 "422/not_member" "$CODE/$(jq -r '.errors[0].code' <<<"$BODY")" "워크스페이스 밖 사람 → 422"
call POST "/rooms/$RA/participants" "$(jq -nc --arg a "$AG" '{agent_id:$a}')"
chk B.10 "403/not_invitable" "$CODE/$(code_of)" "에이전트 초대 — 방장이라도 respond_to(owner) 밖이면 403"
as dir; call POST "/rooms/$RA/participants" "$(jq -nc --arg a "$AG" '{agent_id:$a}')"
chk B.11 "201/agent/0" "$CODE/$(jq -r '.kind+"/"+(.warnings|length|tostring)' <<<"$BODY")" "에이전트 주인(ws owner)이 초대 → 201 · warnings[] (컴퓨터 없음 → 0)"
AG_PID="$(jq -r .id <<<"$BODY")"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 나가기 — is_owner · is_director · 본인 · 재초대 · 넘기기 · 부방장"
MEM_P="$(pid_of mem "$RA" "$MEM_UID")"; OTH_P="$(pid_of mem "$RA" "$OTH_UID")"
as mem; call DELETE "/rooms/$RA/participants/$MEM_P"
chk C.1 "409/is_owner" "$CODE/$(code_of)" "방장은 먼저 넘긴다"
# 열린 미션의 Director — 미션 API 는 R1b2 몫이라 행을 직접 심는다.
W1="$(psqlq "insert into work (room_id, title, goal, director_user_id, status, created_by, started_at) values ('$RA', '89 미션', 'g', '$OTH_UID', 'active', '$MEM_UID', now()) returning id" | head -1)"
as oth; call DELETE "/rooms/$RA/participants/$OTH_P"
chk C.2 "409/is_director" "$CODE/$(code_of)" "열린 미션의 Director 는 먼저 넘긴다"
psqlq "update work set director_user_id='$MEM_UID' where id='$W1'" >/dev/null
call DELETE "/rooms/$RA/participants/$OTH_P"
chk C.3 204 "$CODE" "본인 나가기 → 204"
chk C.4 404 "$(api GET "/rooms/$RA" | api_code)" "나간 뒤 invited 방은 다시 404"
chk C.4a 404 "$(api GET "/sessions/$RA/messages" | api_code)" "나간 뒤 옛 /sessions/{id}/messages 도 404"
as mem; api_ok POST "/rooms/$RA/participants" "$(jq -nc --arg u "$OTH_UID" '{user_id:$u}')" >/dev/null
chk C.5 "$OTH_P" "$(pid_of mem "$RA" "$OTH_UID")" "다시 초대 → 같은 행(left_at 해제)"
as oth; call POST "/rooms/$RA/participants" "$(jq -nc --arg u "$ADM_UID" '{user_id:$u}')"
chk C.6 "403/room_steward_required" "$CODE/$(code_of)" "참여자는 초대 못 한다"
call DELETE "/rooms/$RA/participants/$AG_PID"
chk C.7 "403/room_steward_required" "$CODE/$(code_of)" "참여자는 남을 내보내지 못한다"
as mem; call PUT "/rooms/$RA/deputy" "$(jq -nc --arg u "$OTH_UID" '{user_id:$u}')"
chk C.8 "200/$OTH_UID" "$CODE/$(jq -r .deputy_owner_user_id <<<"$BODY")" "부방장 지정"
as oth; chk C.9 "deputy/true/false" "$(api_ok GET "/rooms/$RA" | jq -r '.my_room_role+"/"+((.my_capabilities|index("invite"))!=null|tostring)+"/"+((.my_capabilities|index("delete"))!=null|tostring)')" "부방장: 초대 ✅ · 삭제 ❌"
as mem; call PUT "/rooms/$RA/owner" "$(jq -nc --arg u "$ADM_UID" '{user_id:$u}')"
chk C.10 "422/not_participant" "$CODE/$(jq -r '.errors[0].code' <<<"$BODY")" "참여자 아닌 사람에게 방장 → 422"
call PUT "/rooms/$RA/owner" "$(jq -nc --arg u "$OTH_UID" '{user_id:$u}')"
chk C.11 "200/$OTH_UID/null/member" "$CODE/$(jq -r '[.owner_user_id,(.deputy_owner_user_id|tostring),.my_room_role]|join("/")' <<<"$BODY")" "방장 넘기기 — 부방장이 방장이 되면 부방장 자리는 빈다 · 옛 방장은 member"
as oth; api_ok PUT "/rooms/$RA/owner" "$(jq -nc --arg u "$MEM_UID" '{user_id:$u}')" >/dev/null
chk C.12 2 "$(psqlq "select count(*) from activity_log where session_id='$RA' and action='room.owner_transferred'")" "방장 넘기기 activity_log 2줄"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 참고 방 링크 — 권한 · 양쪽 시스템 메시지"
as mem; call POST "/rooms/$RA/links" "$(jq -nc --arg t "$RB" '{target_room_id:$t}')"
chk D.1 "403/not_participant_of_target" "$CODE/$(code_of)" "대상 방 참여자가 아니면 403"
call POST "/rooms/$RA/links" "$(jq -nc --arg t "$(uuid)" '{target_room_id:$t}')"
chk D.2 "403/not_participant_of_target" "$CODE/$(code_of)" "없는 방도 같은 답(존재 숨김)"
as oth; api_ok POST "/rooms/$RB/participants" "$(jq -nc --arg u "$MEM_UID" '{user_id:$u}')" >/dev/null
SYS0="$(psqlq "select count(*) from message where session_id in ('$RA','$RB') and kind='system'")"
as mem; call POST "/rooms/$RA/links" "$(jq -nc --arg t "$RB" '{target_room_id:$t}')" -H "Idempotency-Key: $(uuid)"
chk D.3 "201/89 참고" "$CODE/$(jq -r .target_room.name <<<"$BODY")" "링크 → 201"
LINK="$(jq -r .id <<<"$BODY")"
chk D.4 "$((SYS0 + 2))" "$(psqlq "select count(*) from message where session_id in ('$RA','$RB') and kind='system'")" "양쪽 방에 시스템 메시지 1줄씩"
call POST "/rooms/$RA/links" "$(jq -nc --arg t "$RB" '{target_room_id:$t}')"
chk D.5 "409/already_linked" "$CODE/$(code_of)" "중복 → 409"
chk D.6 1 "$(api_ok GET "/rooms/$RA/links" | jq '.items|length')" "listRoomLinks 1"
as oth; call DELETE "/rooms/$RB/links/$LINK"
chk D.7 204 "$CODE" "대상 방 쪽 방장이 풀기 → 204"
as mem; chk D.8 0 "$(api_ok GET "/rooms/$RA/links" | jq '.items|length')" "풀린 뒤 0"
as adm; chk D.9 "1/1" "$(api_ok GET "/workspaces/$WS/activity-log?action=room_link.created" | jq '.items|length')/$(api_ok GET "/workspaces/$WS/activity-log?action=room_link.deleted" | jq '.items|length')" "activity_log room_link.created · deleted"

# ───────────────────────────── E ─────────────────────────────────────────────
step "E. 보관 · 해제 · tasks_active"
psqlq "update work set status='cancelled', finished_at=now() where id='$W1'" >/dev/null
as mem; call POST "/rooms/$RA/archive"
chk E.1 "200/archived" "$CODE/$(jq -r .status <<<"$BODY")" "보관 → 200"
call POST "/rooms/$RA/participants" "$(jq -nc --arg u "$ADM_UID" '{user_id:$u}')"
chk E.2 "409/room_archived" "$CODE/$(code_of)" "보관된 방에 초대 → 409 room_archived"
chk E.3 0 "$(api_ok GET "/workspaces/$WS/rooms" | jq -r --arg r "$RA" '[.items[]|select(.id==$r)]|length')" "기본 목록에서 빠진다"
chk E.4 1 "$(api_ok GET "/workspaces/$WS/rooms?include_archived=true" | jq -r --arg r "$RA" '[.items[]|select(.id==$r)]|length')" "include_archived 에는 있다"
call POST "/rooms/$RA/unarchive"
chk E.5 "200/active" "$CODE/$(jq -r .status <<<"$BODY")" "해제 → 200"
# 진행 중 할 일이 있는 방: 옛 세션(방 + 미션 + queued task)을 만든다 — 컴퓨터가 있어야 한다.
as dir; IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-89" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-89-$RUN")"
S="$(create_session "$WS" "$AG" "89 세션" "목표")"
call POST "/rooms/$S/archive"
chk E.6 "409/tasks_active" "$CODE/$(code_of)" "진행 중 할 일 → 409 tasks_active"

# ───────────────────────────── F ─────────────────────────────────────────────
step "F. 삭제 — works_active · 부방장 403 · 방장 204"
call DELETE "/rooms/$S"
chk F.1 "409/works_active" "$CODE/$(code_of)" "진행 중 미션 → 409 works_active"
as mem; api_ok PUT "/rooms/$RA/deputy" "$(jq -nc --arg u "$OTH_UID" '{user_id:$u}')" >/dev/null
as oth; call DELETE "/rooms/$RA"
chk F.2 "403/room_owner_required" "$CODE/$(code_of)" "부방장은 삭제 못 한다"

# ───────────────────────────── G ─────────────────────────────────────────────
step "G. 안 읽음 — RB 에서 mem"
as mem; N="$(api_ok GET "/rooms/$RB" | jq -r .unread_count)"
chk_ge G.1 1 "$N" "남이 만든 시스템 메시지가 안 읽음으로"
chk G.2 "$N" "$(api_ok GET "/workspaces/$WS/rooms" | jq -r --arg r "$RB" '.items[]|select(.id==$r)|.unread_count')" "목록과 getRoom 이 같은 수"
LAST="$(psqlq "select id from message where session_id='$RB' order by created_at desc, id desc limit 1")"
FIRST="$(psqlq "select id from message where session_id='$RB' order by created_at, id limit 1")"
chk G.3 0 "$(api_ok POST "/rooms/$RB/read" "$(jq -nc --arg m "$LAST" '{last_read_message_id:$m}')" | jq -r .unread_count)" "markRoomRead → 0"
chk G.4 0 "$(api_ok POST "/rooms/$RB/read" "$(jq -nc --arg m "$FIRST" '{last_read_message_id:$m}')" | jq -r .unread_count)" "앞 메시지로 되돌려도 0(앞으로만)"
as oth; chk_ge G.5 1 "$(api_ok GET "/rooms/$RB" | jq -r .unread_count)" "남의 안 읽음은 그대로(oth 는 자기 메시지가 아닌 것만)"

# 삭제는 마지막 — SSE 를 보려면 방이 있어야 하는 판정이 앞에 있다.
as mem; call DELETE "/rooms/$RA"
chk F.3 204 "$CODE" "방장 → 204"
chk F.4 404 "$(api GET "/rooms/$RA" | api_code)" "삭제 뒤 404"
chk F.5 "1/0" "$(psqlq "select count(*) from activity_log where action='room.deleted' and object_id='$RA'")/$(psqlq "select count(*) from activity_log where session_id='$RA'")" "activity_log 에 room.deleted 한 줄만 남는다"
sleep 1

# ───────────────────────────── H ─────────────────────────────────────────────
step "H. SSE — room_id 거르기 · invited 숨김 · 본인 프레임 · 타입"
F_OTH="$OUT/89-sse-oth.log"; F_MEM="$OUT/89-sse-mem-rb.log"; F_DIR="$OUT/89-sse-dir-ra.log"
chk H.1 0 "$(grep '^data: ' "$F_MEM" | sed 's/^data: //' | jq -r --arg r "$RB" 'select(.room_id!=null and .room_id!=$r)|.id' | wc -l | tr -d ' ')" "room_id=RB 스트림에 다른 방 프레임 0"
chk_ge H.2 1 "$(frames_room "$F_MEM" "$RB")" "  … RB 프레임은 온다"
chk H.3 0 "$(grep '^data: ' "$F_DIR" | sed 's/^data: //' | jq -r --arg r "$RA" 'select(.room_id!=null and .room_id!=$r)|.id' | wc -l | tr -d ' ')" "session_id(별칭)=RA 스트림에 다른 방 프레임 0"
chk_ge H.4 1 "$(frames "$F_DIR" participant.joined "$RA")" "participant.joined"
chk_ge H.5 1 "$(frames "$F_DIR" participant.left "$RA")" "participant.left"
chk_ge H.6 1 "$(frames "$F_DIR" room.updated "$RA")" "room.updated"
chk H.7 "1/1" "$(frames "$F_DIR" room.deleted "$RA")/$(frames "$F_DIR" session.deleted "$RA")" "room.deleted + session.deleted(R4 까지 둘 다)"
chk H.8 "2/2" "$(frames "$F_DIR" room_link.updated "$RA")/$(frames "$F_MEM" room_link.updated "$RB")" "room_link.updated 가 양쪽 방에(걸기·풀기)"
# invited 가 된 뒤(커밋 뒤) RA 에서 난 프레임 — 「여기까지 정리」 요약 메시지 — 는 oth 의 워크스페이스 스트림에 없다.
# (visibility 를 바꾸는 그 트랜잭션 안의 프레임은 커밋 전에 나가 옛 공개 범위로 판정된다 — 방금까지 볼 수 있던 사람에게.)
chk H.9 1 "$(grep '^data: ' "$F_DIR" | sed 's/^data: //' | jq -r --arg id "$SUMM" 'select(.type=="message.created" and .payload.id==$id)|.id' | wc -l | tr -d ' ')" "  (대조) dir 스트림에는 그 요약 프레임이 있다"
chk H.10 0 "$(grep '^data: ' "$F_OTH" | sed 's/^data: //' | jq -r --arg id "$SUMM" 'select(.payload.id==$id)|.id' | wc -l | tr -d ' ')" "초대 전 invited 방 프레임이 oth 스트림에 0"
chk H.11 "2/0" "$(frames "$F_MEM" room.unread "$RB")/$(frames "$F_OTH" room.unread "$RB")" "room.unread 는 읽은 본인 스트림에만"
chk H.12 "$RB" "$(grep '^data: ' "$F_MEM" | sed 's/^data: //' | jq -r 'select(.type=="room.unread")|.room_id' | head -1)" "envelope room_id"

# ───────────────────────────── I ─────────────────────────────────────────────
step "I. 활동 로그"
as mem; chk I.1 403 "$(api GET "/workspaces/$WS/activity-log" | api_code)" "멤버 → 403"
as adm; AL="$(api_ok GET "/workspaces/$WS/activity-log?room_id=$RA")"
chk I.2 "room.deleted" "$(jq -r '[.items[].action]|join(",")' <<<"$AL")" "삭제된 방으로 거르면 room.deleted 한 줄"
chk I.3 "user/$MEM_UID" "$(api_ok GET "/workspaces/$WS/activity-log?action=room.deleted" | jq -r '.items[0].actor.kind+"/"+.items[0].actor.id')" "actor"

# ───────────────────────────── J ─────────────────────────────────────────────
step "J. 옛 표면"
as dir; chk J.1 200 "$(api GET "/sessions/$S" | api_code)" "getSession 200"
chk J.2 1 "$(api_ok GET "/workspaces/$WS/sessions" | jq -r --arg s "$S" '[.items[]|select(.id==$s)]|length')" "listSessions 에 그 세션"
chk J.3 0 "$(api_ok GET "/workspaces/$WS/sessions" | jq -r --arg r "$RB" '[.items[]|select(.id==$r)]|length')" "미션 없는 방은 세션 목록에 없다"

cleanup
PASS="$(awk -F'\t' '$2=="PASS"' "$CHECKS" | wc -l | tr -d ' ')"; FAIL="$(awk -F'\t' '$2=="FAIL"' "$CHECKS" | wc -l | tr -d ' ')"
printf '\n== 89_rooms: PASS %s · FAIL %s (%s) ==\n' "$PASS" "$FAIL" "$CHECKS" >&2
exit "$FAIL"
