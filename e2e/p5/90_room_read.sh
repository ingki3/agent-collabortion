#!/usr/bin/env bash
# e2e/p5/90_room_read.sh — T-R1c 실서버 스모크: **다른 방 읽기**(PRD v0.19 FR-4.5 · §9 · V19-B NN7 · V19-C)
#   listReadableRooms(GET /cli/rooms) · readRoom(GET /cli/rooms/{id}/read) · listRoomReads(S23)
# — **데몬 없이**, 데몬 역할(claim·phase·finish)은 curl 로 흉내(70_·79_·81_ 의 레시피).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 30s
#
# 방 다섯(서연 = Director·방장, Lead·R·W 에이전트, 다른 사람 = 민수):
#   A 작업   Lead·R·W  — 읽는 쪽(현재 방). A → D 참고 링크(방장이 건다 — 링크 API 는 R1b3, 여기서는 psql)
#   B 인프라 Lead      — Lead 가 참여자 → 읽힘
#   C 비밀   Lead      — 민수의 방(사람 행은 민수뿐), 서연은 참여한 적 없다 → originator_not_participant(존재 숨김)
#   D 참고   R         — Lead 는 참여자가 아니지만 A 가 링크 → via_link 로 읽힘
#   E 남의방 R         — 링크도 참여도 없음 → agent_not_allowed
#
# 재는 것 (판정 표 out/90-checks.tsv):
#   A. 참여 방 읽기 허용·기록 양쪽 — 목록 {인프라, 참고}(C·E·A 없음) · read 200 · room_read_log 1행 · 읽힌 방 타임라인 시스템 메시지 ·
#      activity_log room.read 양쪽 · 읽은 task 의 task_event(status/read, args.note) · SSE room_read.recorded 양쪽(stream_event) ·
#      S23 out/in(originator_user = 서연)
#   B. 링크 경유 — 참고 read 200 · 목록 via_link true
#   C. 요청자 비참여 403 — 비밀 403 originator_not_participant · 없는 방 id 와 **구별 불가**(같은 코드·사유·문장) · 목록 숨김 ·
#      S23 denied other_room null · activity room.read.denied · 남의방 403 agent_not_allowed
#   D. originator 떠남(V19-C 읽는 순간 판정) — 서연이 인프라에서 나감(left_at, 나가기 API 는 R1b3 → psql) → 다음 read 403 originator_left +
#      room_name · 목록에서 사라짐 · 사람 쪽 문장 「서연이 인프라 방을 떠나 @Lead의 참고 읽기가 막혔습니다」 · S23 에만 방 이름
#   E. originator 승계(NN7) — 위임 자식(W·R) · blocked 질문 기상 · 합류 기상 · 재시도(attempt 2) · 재지시(restartLane) ·
#      HITL 재개 — 전부 task.originator_user_id = 서연, 그리고 **그 턴의 토큰으로 read 200**
#   F. no_originator — 사람 없는 사슬(originator NULL): 403 no_originator · 목록 빈 배열 · **방장(서연)으로 대체하지 않는다**
#   H. (T-R3a) 실제 colab 바이너리 — room list · room read(truncated 그대로 · 거부 exit 3 + denied_reason) · work propose · room get({room, work, participants}) · session get 삭제(R4)
#   G. 상한 잘림 — 설정 room_read {max_rooms_per_turn:1, max_tokens:500} → 긴 방 read truncated true · 로그 truncated ·
#      같은 턴 두 번째 방은 truncated + 빈 내용 + 기록 0 · 같은 방 다시 읽기는 칸을 안 먹는다
#
# 스택(T-R1c 배정): server :8131 · pg :5481 · 컨테이너 colab-pg-r1c-5481.
# 사용: SERVER_URL=http://localhost:8131 PG_PORT=5481 PG_CONTAINER=colab-pg-r1c-5481 bash e2e/p5/up.sh
#       bash e2e/p5/90_room_read.sh
#       SERVER_URL=http://localhost:8131 PG_PORT=5481 PG_CONTAINER=colab-pg-r1c-5481 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8131}"
export PG_PORT="${PG_PORT:-5481}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-r1c-5481}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/90-checks.tsv"; : > "$CHECKS"
COOKIE_DIR="$OUT/90-cookies-dir.txt"; rm -f "$COOKIE_DIR"; COOKIE="$COOKIE_DIR"
COOKIE_MS="$OUT/90-cookies-minsu.txt"; rm -f "$COOKIE_MS"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-R1c ports"

claim() { daemon_api "runtimes/$RID/claim" '{"capacity":10,"wait_ms":0}'; }
as() { local c="$1"; shift; COOKIE="$c" api "$@"; }
tok_api() { # TOKEN METHOD PATH [JSON] → 본문 + 마지막 줄 코드
  local tok="$1" method="$2" path="$3" body="${4:-}"
  if [ -n "$body" ]; then
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuid)" -X "$method" "$API$path" --data "$body"
  else
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $tok" -X "$method" "$API$path"
  fi
}
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" --arg r "$2" '{name:$n,role:$r,role_description:"d",
  instructions:"짧게, 한국어로 답한다.",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
mk_room() { # TITLE AGENT_ID... → 방 id(createRoom→참여자→createWork, lib create_room_work). 첫 에이전트가 담당.
  local title="$1"; shift
  local parts; parts="$(printf '%s\n' "$@" | jq -R '{agent_id:.}' | jq -sc .)"
  create_room_work "$WS" "$(jq -nc --arg t "$title" --arg rt "$RID" --arg a "$1" --argjson p "$parts" \
    '{title:$t,goal:($t+" 방의 목표"),isolation:{kind:"none"},participants:$p,assignee_agent_id:$a,runtime_id:$rt,
      completion_condition:{op:"and",conditions:[{type:"manual"}]}}')"
}
mention() { # SESSION AGENT_ID NAME TEXT → Director 가 @멘션
  api_ok POST "/rooms/$1/messages" "$(with_work "$1" "$(jq -nc --arg a "$2" --arg n "$3" --arg t "$4" '{content:("[@"+$n+"](mention://agent/"+$a+") "+$t)}')")" -H "Idempotency-Key: $(uuid)" >/dev/null
}
# take TASK_ID → 그 task 의 번들을 받아 phase running. 표준출력: attempt<TAB>token
# claim 은 줄 선 task 를 **전부** 내준다 — 찾는 것 말고 받은 번들은 버리지 않고 $BUNDLES 에 두었다가 쓴다
# (버리면 그 task 는 dispatched 로 남아 다시는 claim 에 안 나온다).
BUNDLES="$OUT/90-bundles.jsonl"; : > "$BUNDLES"
take() {
  local tid="$1" i b=""
  for i in 1 2 3 4 5 6; do
    b="$(jq -c --arg t "$tid" 'select(.task.id==$t)' "$BUNDLES" | head -1)"
    [ -n "$b" ] && break
    claim | jq -c '.tasks[]' >> "$BUNDLES"
    sleep 0.2
  done
  [ -n "$b" ] || { bad "claim 에 task $tid 가 없다"; return 1; }
  jq -c --arg t "$tid" 'select(.task.id!=$t)' "$BUNDLES" > "$BUNDLES.tmp"; mv "$BUNDLES.tmp" "$BUNDLES"
  local at; at="$(jq -r .task.attempt <<<"$b")"
  daemon_api "tasks/$tid/attempts/$at/phase" '{"phase":"running","pgid":4242}' >/dev/null
  printf '%s\t%s' "$at" "$(jq -r .task_token <<<"$b")"
}
finish() { # TASK ATTEMPT [OUTCOME] [FAILURE_KIND]
  local body
  if [ "${3:-completed}" = completed ]; then
    body="$(jq -nc '{outcome:"completed",stop_reason:"end_turn",transport:"acp",last_seq:0,usage:{input_tokens:100,output_tokens:10,estimated:false,model:"claude-sonnet-5"}}')"
  else
    body="$(jq -nc --arg k "$4" '{outcome:"failed",failure_kind:$k,transport:"acp",last_seq:0}')"
  fi
  daemon_api "tasks/$1/attempts/$2/finish" "$body" >/dev/null
}
queued_task() { psqlq "select id from task where session_id='$1' and agent_id='$2' and status='queued' order by created_at desc limit 1"; }
orig_of() { psqlq "select coalesce(originator_user_id::text,'NULL') from task where id='$1'"; }
readc() { tok_api "$1" GET "/cli/rooms/$2/read${3:-}"; }           # 본문 + 코드
read_code() { readc "$@" | api_code; }
read_body() { readc "$@" | api_body; }
readable() { tok_api "$1" GET /cli/rooms | api_body | jq -r '[.items[].name]|sort|join(",")'; }
reads() { COOKIE="$COOKIE_DIR" api_ok GET "/rooms/$1/reads${2:-}"; }
code_reason() { local b; b="$(cat)"; printf '%s/%s/%s' "$(api_code <<<"$b")" "$(api_body <<<"$b" | jq -r .code)" "$(api_body <<<"$b" | jq -r '.denied_reason // "-"')"; }

step "0. 계정(서연=Director·방장, 민수=멤버) · 워크스페이스 · 에이전트 Lead·R·W · 페어링(curl)"
signup "r1c-dir-$RUN@example.com" password123 "서연" >/dev/null
SEOYEON="$(psqlq "select id from app_user where email='r1c-dir-$RUN@example.com'")"
WS="$(create_workspace "R1c $RUN")"
INV="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}' | jq -r .token)"
COOKIE="$COOKIE_MS" api_ok POST /auth/signup "$(jq -nc --arg e "r1c-ms-$RUN@example.com" --arg t "$INV" '{display_name:"민수",email:$e,password:"password123",invite_token:$t}')" >/dev/null
COOKIE="$COOKIE_DIR"
LEAD="$(mk_agent Lead lead)"; R="$(mk_agent R writer)"; W="$(mk_agent W writer)"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-r1c" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-r1c-$RUN")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"

A="$(mk_room 작업 "$LEAD" "$R" "$W")"
B="$(mk_room 인프라 "$LEAD")"
D="$(mk_room 참고 "$R")"
E="$(mk_room 남의방 "$R")"
# 비밀: Lead 가 참여한 민수의 방. 에이전트는 만든 사람만 초대할 수 있어(not_invitable) 서연이 만들고, 사람 행을
# 민수로 바꾼다 — 방 초대·나가기 API 는 R1b3 몫이라 psql.
C="$(mk_room 비밀 "$LEAD")"
MINSU="$(psqlq "select id from app_user where email='r1c-ms-$RUN@example.com'")"
psqlq "delete from room_participant where room_id='$C' and user_id is not null; insert into room_participant (room_id, user_id, role) values ('$C', '$MINSU', 'owner')" >/dev/null
psqlq "insert into room_link (room_id, target_room_id, created_by) values ('$A', '$D', '$SEOYEON')" >/dev/null
# 나머지 방의 시작 task 는 이 판정과 무관하다 — Lead 의 동시 실행 칸(max_concurrent_tasks 3)을 먹지 않게 닫는다.
psqlq "update task set status='cancelled', finished_at=now() where session_id in ('$B','$C','$D','$E') and status='queued'" >/dev/null
chk 0.2 "0" "$(psqlq "select count(*) from room_participant where room_id='$C' and user_id='$SEOYEON'")" "서연은 비밀 방에 참여한 적 없다"
chk 0.3 "1" "$(psqlq "select count(*) from room_link where room_id='$A' and target_room_id='$D'")" "작업 → 참고 링크"

# Lead 의 A 첫 task(방 시작 — originator = 서연)
T1="$(psqlq "select id from task where session_id='$A' and agent_id='$LEAD' order by created_at limit 1")"
chk 0.4 "$SEOYEON" "$(orig_of "$T1")" "방을 연 사람이 첫 task 의 originator"
IFS=$'\t' read -r AT1 TOK1 <<<"$(take "$T1")"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. 참여 방 읽기 — 허용 · 기록 양쪽"
chk A.1 "인프라,참고" "$(readable "$TOK1")" "목록: 참여 방 + 링크 방만(비밀·남의방·지금 방 없음)"
tok_api "$TOK1" GET /cli/rooms | api_body > "$OUT/90-list.json"
chk A.2 "true/false" "$(jq -r '.items[]|select(.name=="인프라")|"\(.agent_is_participant)/\(.via_link)"' "$OUT/90-list.json")" "인프라: 참여 · 링크 아님"
SE0="$(psqlq "select count(*) from stream_event where workspace_id='$WS' and type='room_read.recorded'")"
readc "$TOK1" "$B" > "$OUT/90-read-infra.txt"
chk A.3 200 "$(api_code < "$OUT/90-read-infra.txt")" "read 인프라 → 200"
api_body < "$OUT/90-read-infra.txt" > "$OUT/90-read-infra.json"
# R4: 방을 createRoom→updateRoom→addRoomParticipant→createWork 로 만든다 — 방 타임라인에 시스템 메시지 3건
# (「방 설정을 바꿨습니다」·「Lead이(가) 방에 참여했습니다」·「미션을 열었습니다」). 옛 createSession 은 1건(방 시작)이었다.
chk A.4 "인프라/false/3" "$(jq -r '"\(.room.name)/\(.truncated)/\(.messages|length)"' "$OUT/90-read-infra.json")" "요약 없음 · 잘리지 않음 · 최근 메시지 3건(방 만들기 흐름의 시스템 메시지)"
chk A.5 "1" "$(psqlq "select count(*) from room_read_log where room_id='$A' and target_room_id='$B' and allowed and reader_task_id='$T1' and originator_user_id='$SEOYEON' and recent_n=3")" "room_read_log 1행(읽은 방 A → 읽힌 방 B, originator 서연)"
chk A.6 "서연의 요청으로 @Lead이(가) 이 방을 읽었습니다(최근 3건)." "$(psqlq "select content from message where session_id='$B' and kind='system' order by created_at desc limit 1")" "읽힌 방 타임라인 시스템 메시지(SCREEN §8.1 문장 2)"
chk A.7 "0" "$(psqlq "select count(*) from message where session_id='$A' and content like '%이 방을 읽었습니다%'")" "읽은 방에는 그 문장이 없다"
chk A.8 "out:$A/in:$B" "$(psqlq "select string_agg(payload->>'direction'||':'||session_id, '/' order by payload->>'direction' desc) from activity_log where action='room.read' and workspace_id='$WS'")" "activity_log room.read 양쪽"
chk A.9 "1" "$(psqlq "select count(*) from task_event where task_id='$T1' and class='status' and verb='read' and outcome='ok' and payload->>'command'='room read' and payload->'args'->>'note'='「인프라」 방을 읽었습니다(최근 3건)'")" "읽은 task 의 피드(status/read, args.note)"
chk A.10 "$((SE0+2))" "$(psqlq "select count(*) from stream_event where workspace_id='$WS' and type='room_read.recorded'")" "SSE room_read.recorded 2건(양쪽 방)"
chk A.11 "out:$A,in:$B" "$(psqlq "select string_agg((payload->>'direction')||':'||(payload->>'room_id'), ',' order by id) from stream_event where workspace_id='$WS' and type='room_read.recorded'")" "SSE 각 방에 제 방향으로"
reads "$A" "?direction=out" > "$OUT/90-reads-A-out.json"
reads "$B" "?direction=in" > "$OUT/90-reads-B-in.json"
chk A.12 "1/인프라/서연/true" "$(jq -r '"\(.items|length)/\(.items[0].other_room.name)/\(.items[0].originator_user.display_name)/\(.items[0].scope.recent_n==3)"' "$OUT/90-reads-A-out.json")" "S23(A) 읽음: 인프라, 요청자 서연"
chk A.13 "1/작업/Lead" "$(jq -r '"\(.items|length)/\(.items[0].other_room.name)/\(.items[0].agent.name)"' "$OUT/90-reads-B-in.json")" "S23(B) 읽힘: 작업 방의 Lead"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 링크 경유"
chk B.1 "false/true" "$(jq -r '.items[]|select(.name=="참고")|"\(.agent_is_participant)/\(.via_link)"' "$OUT/90-list.json")" "참고: 참여 아님 · 링크"
chk B.2 200 "$(read_code "$TOK1" "$D")" "read 참고(링크) → 200"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 요청자 비참여 · 에이전트 불허 — 403, 존재 숨김"
readc "$TOK1" "$C" > "$OUT/90-read-secret.txt"
GHOST="$(uuid)"
readc "$TOK1" "$GHOST" > "$OUT/90-read-ghost.txt"
chk C.1 "403/room_read_denied/originator_not_participant" "$(code_reason < "$OUT/90-read-secret.txt")" "비밀(서연 비참여) → 403"
chk C.2 "$(code_reason < "$OUT/90-read-secret.txt")|$(api_body < "$OUT/90-read-secret.txt" | jq -r .detail)" \
  "$(code_reason < "$OUT/90-read-ghost.txt")|$(api_body < "$OUT/90-read-ghost.txt" | jq -r .detail)" "없는 방과 구별 불가(코드·사유·문장)"
chk C.3 "no" "$(api_body < "$OUT/90-read-secret.txt" | grep -q 비밀 && echo yes || echo no)" "응답에 방 이름 없음"
chk C.4 "403/room_read_denied/agent_not_allowed" "$(readc "$TOK1" "$E" | code_reason)" "남의방(링크·참여 없음) → 403 agent_not_allowed"
reads "$A" "?direction=denied" > "$OUT/90-reads-A-denied.json"
chk C.5 "3/0" "$(jq -r '"\(.items|length)/\([.items[]|select(.other_room!=null)]|length)"' "$OUT/90-reads-A-denied.json")" "S23(A) 거부 3행 · other_room 전부 null"
chk C.6 "3" "$(psqlq "select count(*) from activity_log where session_id='$A' and action='room.read.denied' and object_id is null and payload->>'note'='@Lead이(가) 읽을 수 없는 방을 조회했습니다'")" "활동: 「〈에이전트〉가 읽을 수 없는 방을 조회했습니다」(방 이름 없이)"
chk C.7 "0" "$(psqlq "select count(*) from message where session_id in ('$C','$E') and content like '%이 방을 읽었습니다%'")" "거부된 방에는 아무것도 남지 않는다"
chk C.8 "3" "$(psqlq "select count(*) from task_event where task_id='$T1' and class='status' and verb='read' and outcome='rejected' and payload->>'rejected_reason'='room_read_denied' and object_ref=to_jsonb('room_read'::text)")" "피드: rejected 3행(대상 id 없이)"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. originator 떠남 — 읽는 순간 다시 판정(V19-C)"
psqlq "update room_participant set left_at=now() where room_id='$B' and user_id='$SEOYEON'" >/dev/null
readc "$TOK1" "$B" > "$OUT/90-read-left.txt"
chk D.1 "403/room_read_denied/originator_left" "$(code_reason < "$OUT/90-read-left.txt")" "서연이 나간 뒤 같은 토큰의 read → 403 originator_left"
chk D.2 "인프라" "$(api_body < "$OUT/90-read-left.txt" | jq -r .room_name)" "이 사유만 방 이름을 드러낸다"
chk D.3 "참고" "$(readable "$TOK1")" "목록에서도 사라진다"
chk D.4 "1" "$(psqlq "select count(*) from activity_log where session_id='$A' and action='room.read.denied' and object_id='$B' and payload->>'note'='서연이 인프라 방을 떠나 @Lead의 참고 읽기가 막혔습니다'")" "사람 쪽 문장(PRD FR-4.5, 읽으려 한 방의 활동)"
chk D.5 "originator_left/인프라" "$(reads "$A" "?direction=denied" | jq -r '.items[0]|"\(.denied_reason)/\(.other_room.name)"')" "S23 거부 행: 이 사유만 other_room"
psqlq "update room_participant set left_at=null where room_id='$B' and user_id='$SEOYEON'" >/dev/null
chk D.6 200 "$(read_code "$TOK1" "$B")" "다시 참여하면 다음 read 는 200(판정은 dispatch 가 아니라 읽는 순간)"

# ───────────────────────────── E ─────────────────────────────────────────────
step "E. originator 승계(NN7) — 위임 자식 · blocked 기상 · 합류 기상 · 재시도 · 재지시 · HITL 재개"
LR="$(tok_api "$TOK1" POST "/rooms/$A/lanes" "$(jq -nc --arg a "$R" '{agent_id:$a,brief:"조사해 주세요"}')")"
LW="$(tok_api "$TOK1" POST "/rooms/$A/lanes" "$(jq -nc --arg a "$W" '{agent_id:$a,brief:"초안을 써 주세요"}')")"
chk E.1 "201/201" "$(api_code <<<"$LR")/$(api_code <<<"$LW")" "Lead 가 R·W 에게 위임"
TR="$(api_body <<<"$LR" | jq -r .task.id)"; TW="$(api_body <<<"$LW" | jq -r .task.id)"
chk E.2 "$SEOYEON/$SEOYEON" "$(orig_of "$TR")/$(orig_of "$TW")" "위임 자식 task 가 서연을 물려받는다"
finish "$T1" "$AT1"
IFS=$'\t' read -r ATR TOKR <<<"$(take "$TR")"
IFS=$'\t' read -r ATW TOKW <<<"$(take "$TW")"
chk E.3 200 "$(read_code "$TOKR" "$D")" "위임 자식(R) 턴이 참고(자기 참여 방) 를 읽는다"

chk E.4 200 "$(tok_api "$TOKR" POST "/tasks/$TR/status" '{"status":"blocked","note":"범위가 어디까지인가요?"}' | api_code)" "R: status blocked → 위임자 즉시 기상"
TQ="$(queued_task "$A" "$LEAD")"
chk E.5 "$SEOYEON" "$(orig_of "$TQ")" "blocked 질문 기상 task(router wake) — 서연 승계"
finish "$TR" "$ATR"
IFS=$'\t' read -r ATQ TOKQ <<<"$(take "$TQ")"
chk E.6 200 "$(read_code "$TOKQ" "$D")" "질문 기상 턴이 참고를 읽는다"
finish "$TQ" "$ATQ"

chk E.7 200 "$(tok_api "$TOKW" POST "/tasks/$TW/status" '{"status":"done"}' | api_code)" "W: status done → 합류(R 은 blocked = 끝남)"
TJ="$(queued_task "$A" "$LEAD")"
chk E.8 "yes/$SEOYEON" "$( [ -n "$TJ" ] && [ "$TJ" != "$TQ" ] && echo yes || echo no )/$(orig_of "$TJ")" "합류 기상 task — 새 task, 서연 승계(status.go 합류 INSERT)"
chk E.9 "1" "$(psqlq "select count(*) from task t join message m on m.id=t.trigger_message_id where t.id='$TJ' and m.content like '위임한 작업이 모두 끝났습니다%'")" "그 task 의 트리거가 합류 메시지"
finish "$TW" "$ATW"
IFS=$'\t' read -r ATJ TOKJ <<<"$(take "$TJ")"
chk E.10 200 "$(read_code "$TOKJ" "$D")" "합류로 깨어난 Lead 가 참고를 읽는다(V19_impl §4 위험 4 ③)"

# 재시도: 같은 task 의 attempt 2 — 같은 행이라 originator 가 그대로
finish "$TJ" "$ATJ" failed network
IFS=$'\t' read -r ATJ2 TOKJ2 <<<"$(take "$TJ")"
chk E.11 "2/$SEOYEON/200" "$ATJ2/$(orig_of "$TJ")/$(read_code "$TOKJ2" "$D")" "재시도(attempt 2) 턴도 읽는다"

# HITL 재개: 질문 → 턴 끝 → Director 답 → 재큐잉(새 attempt)
HQ="$(tok_api "$TOKJ2" POST "/rooms/$A/hitl-requests" '{"type":"question","question":"어느 쪽으로 갈까요?","proposed_default":"A 안"}')"
chk E.12 201 "$(api_code <<<"$HQ")" "Lead: hitl ask"
HID="$(api_body <<<"$HQ" | jq -r '.id // .hitl_request.id')"
finish "$TJ" "$ATJ2"
chk E.13 waiting_human "$(psqlq "select status from task where id='$TJ'")" "턴 끝 → waiting_human"
chk E.14 200 "$(api POST "/hitl-requests/$HID/response" '{"answer":"B 안으로"}' -H "Idempotency-Key: $(uuid)" | api_code)" "서연이 답한다"
IFS=$'\t' read -r ATJ3 TOKJ3 <<<"$(take "$TJ")"
chk E.15 "3/$SEOYEON/200" "$ATJ3/$(orig_of "$TJ")/$(read_code "$TOKJ3" "$D")" "HITL 재개 턴도 읽는다"
finish "$TJ" "$ATJ3"

# 재지시: Director 가 lane 을 다시 지시 → 새 task(restarted_from_task_id), originator = 다시 지시한 사람
LANE_J="$(psqlq "select lane_id from task where id='$TJ'")"
psqlq "update lane set status='failed' where id='$LANE_J'" >/dev/null
RS="$(api POST "/lanes/$LANE_J/restart" '{"content":"처음부터 다시 해 주세요"}' -H "Idempotency-Key: $(uuid)")"
chk E.16 202 "$(api_code <<<"$RS")" "restartLane → 202"
TRS="$(api_body <<<"$RS" | jq -r .task.id)"
IFS=$'\t' read -r ATRS TOKRS <<<"$(take "$TRS")"
chk E.17 "$SEOYEON/200" "$(orig_of "$TRS")/$(read_code "$TOKRS" "$D")" "재지시 task — 다시 지시한 서연, 읽는다"
finish "$TRS" "$ATRS"

# ───────────────────────────── F ─────────────────────────────────────────────
step "F. no_originator — 사람 없는 사슬은 방장으로 대체하지 않는다"
mention "$A" "$LEAD" Lead "한 번 더"
TN="$(queued_task "$A" "$LEAD")"
psqlq "update task set originator_user_id=null where id='$TN'" >/dev/null
IFS=$'\t' read -r ATN TOKN <<<"$(take "$TN")"
chk F.1 "403/room_read_denied/no_originator" "$(readc "$TOKN" "$D" | code_reason)" "originator 없는 턴 → 403 no_originator"
chk F.2 "이 턴은 사람의 요청에서 시작하지 않아 다른 방을 읽을 수 없습니다" "$(read_body "$TOKN" "$D" | jq -r .detail)" "PRD FR-4.5 [V19-B] 문장"
chk F.3 "" "$(readable "$TOKN")" "목록 빈 배열"
# 사람 없는 사슬의 합류: W 에게 위임(자식도 NULL) → done → 기상 task 도 NULL
LN="$(tok_api "$TOKN" POST "/rooms/$A/lanes" "$(jq -nc --arg a "$W" '{agent_id:$a,brief:"사람 없는 위임"}')")"
TNW="$(api_body <<<"$LN" | jq -r .task.id)"
chk F.4 "NULL" "$(orig_of "$TNW")" "위임 자식도 없다"
finish "$TN" "$ATN"
IFS=$'\t' read -r ATNW TOKNW <<<"$(take "$TNW")"
tok_api "$TOKNW" POST "/tasks/$TNW/status" '{"status":"done"}' >/dev/null
TNJ="$(queued_task "$A" "$LEAD")"
chk F.5 "NULL" "$(orig_of "$TNJ")" "합류 기상 task 도 NULL — 방장·Director(서연)로 대체하지 않는다"
finish "$TNW" "$ATNW"
# 그 기상 task 를 닫는다 — 줄에 남아 있으면 G 의 멘션이 거기에 합쳐져(FR-3.4) originator 없는 턴이 된다.
psqlq "update task set status='cancelled', finished_at=now() where id='$TNJ'" >/dev/null

# ───────────────────────────── G ─────────────────────────────────────────────
step "G. 상한 잘림 — max_tokens · max_rooms_per_turn"
chk G.1 200 "$(api PATCH "/workspaces/$WS/settings" '{"room_read":{"max_rooms_per_turn":1,"max_tokens":500}}' | api_code)" "설정 room_read 1방 · 500토큰"
chk G.2 "1/500" "$(api_ok GET "/workspaces/$WS/settings" | jq -r '"\(.room_read.max_rooms_per_turn)/\(.room_read.max_tokens)"')" "설정이 읽힌다"
chk G.3 422 "$(api PATCH "/workspaces/$WS/settings" '{"room_read":{"max_tokens":10}}' | api_code)" "max_tokens 10 → 422(최소 500)"
LONG="$(python3 -c 'print("긴 문장입니다. " * 60)')"
for i in 1 2 3 4; do api_ok POST "/rooms/$D/messages" "$(jq -nc --arg c "$LONG" '{content:$c}')" -H "Idempotency-Key: $(uuid)" >/dev/null; done
mention "$A" "$LEAD" Lead "참고 방을 읽어 주세요"
TG="$(queued_task "$A" "$LEAD")"
IFS=$'\t' read -r ATG TOKG <<<"$(take "$TG")"
read_body "$TOKG" "$D" > "$OUT/90-read-capped-tokens.json"
chk G.4 "true" "$(jq -r .truncated "$OUT/90-read-capped-tokens.json")" "긴 방 → truncated"
N_READ="$(jq -r '.messages|length' "$OUT/90-read-capped-tokens.json")"
chk G.5 "yes" "$( [ "$N_READ" -ge 1 ] && [ "$N_READ" -lt 5 ] && echo yes || echo no )" "메시지 일부만($N_READ / 5)"
chk G.6 "true/$N_READ" "$(psqlq "select truncated||'/'||recent_n from room_read_log where reader_task_id='$TG'")" "로그에 truncated · recent_n"
read_body "$TOKG" "$B" > "$OUT/90-read-capped-rooms.json"
chk G.7 "true/0/0" "$(jq -r '"\(.truncated)/\(.messages|length)/\(.decisions|length)"' "$OUT/90-read-capped-rooms.json")" "같은 턴 두 번째 방 → truncated · 빈 내용"
chk G.8 "0" "$(psqlq "select count(*) from room_read_log where reader_task_id='$TG' and target_room_id='$B'")" "읽지 않은 방은 기록도 없다"
chk G.9 "1" "$(psqlq "select count(*) from task_event where task_id='$TG' and verb='read' and payload->'args'->>'note' like '이번 턴에 읽을 수 있는 방 1개를 이미 읽어%'")" "피드가 이유를 말한다"
chk G.10 "200/false" "$(readc "$TOKG" "$D" "?tail=1" | { b="$(cat)"; printf '%s/%s' "$(api_code <<<"$b")" "$(api_body <<<"$b" | jq -r .truncated)"; })" "같은 방 다시(tail 1) — 칸을 안 먹는다"
finish "$TG" "$ATG"

# ───────────────────────────── H ─────────────────────────────────────────────
step "H. 실제 colab 바이너리로(T-R3a, colab-cli.md v0.8 §2.4a) — room list · room read · work propose"
COLAB="${COLAB_BIN:-$BIN/colab-r3a}"
[ -n "${COLAB_BIN:-}" ] || (cd cli && go build -o "$COLAB" ./cmd/colab) || die "colab build"
api_ok PATCH "/workspaces/$WS/settings" '{"room_read":{"max_rooms_per_turn":3,"max_tokens":4000}}' >/dev/null
mention "$A" "$LEAD" Lead "CLI 로 참고 방을 읽어 주세요"
TH="$(queued_task "$A" "$LEAD")"
IFS=$'\t' read -r ATH TOKH <<<"$(take "$TH")"
colab_h() { # ARGS… → stdout JSON, 마지막 줄 exit 코드. 데몬이 주는 env 그대로.
  env -i PATH="$PATH" HOME="$HOME" COLAB_TASK_TOKEN="$TOKH" COLAB_SERVER_URL="$SERVER_URL" COLAB_TASK_ID="$TH" COLAB_TASK_ATTEMPT="$ATH" \
    COLAB_SESSION_ID="$A" COLAB_AGENT_NAME=Lead COLAB_STATE_DIR="$OUT/90-state" "$COLAB" "$@" 2>>"$OUT/90-cli.err"
  printf '\n%s' "$?"
}
H1="$(colab_h room list --json)"
chk H.1 "0/인프라,참고" "$(api_code <<<"$H1")/$(api_body <<<"$H1" | jq -r '[.items[].name]|sort|join(",")')" "colab room list → exit 0 · 목록 = listReadableRooms"
H2="$(colab_h room read --room "$D" --tail 2)"
chk H.2 "0/참고/boolean" "$(api_code <<<"$H2")/$(api_body <<<"$H2" | jq -r '.room.name+"/"+(.truncated|type)')" "colab room read --room 참고 → exit 0 · truncated 칸을 그대로 싣는다"
chk H.3 1 "$(psqlq "select count(*) from room_read_log where reader_task_id='$TH' and target_room_id='$D' and allowed")" "서버가 기록(room_read_log) — CLI 는 따로 적지 않는다"
H3="$(colab_h room read --room "$C")"
chk H.4 "3/room_read_denied/originator_not_participant" "$(api_code <<<"$H3")/$(api_body <<<"$H3" | jq -r '.error.code+"/"+.error.denied_reason')" "거부 → exit 3 + denied_reason"
H4="$(colab_h work propose --goal "참고 방 결론을 미션으로" --why "두 방에서 같은 결정이 필요하다")"
chk H.5 "0/open/1" "$(api_code <<<"$H4")/$(api_body <<<"$H4" | jq -r .proposal.status)/$(psqlq "select count(*) from work_proposal where proposed_by_task_id='$TH'")" "lead colab work propose → exit 0 · 제안 open · 행 1"
H5="$(colab_h room get)"; H6="$(colab_h session get)"
# R4(colab-cli v0.9): session get 별칭이 지워졌다 — room get 은 {room, work, participants}, session get 은 없는 명령(0 이 아닌 종료).
chk H.6 "0/$A/removed" "$(api_code <<<"$H5")/$(api_body <<<"$H5" | jq -r '.room.id // "-"')/$( [ "$(api_code <<<"$H6")" != 0 ] && echo removed || echo still-there)" "room get → {room,…} · session get 은 R4 에서 삭제"
finish "$TH" "$ATH"

printf '\n판정: %s\n' "$(awk -F'\t' '{c[$2]++} END{printf "PASS %d · FAIL %d", c["PASS"], c["FAIL"]}' "$CHECKS")" >&2
[ "$FAILS" = 0 ]
