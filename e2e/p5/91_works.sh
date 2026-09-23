#!/usr/bin/env bash
# e2e/p5/91_works.sh — T-R1b2 실서버 스모크: 미션 API + 방당 미션 여럿 (PRD v0.19 FR-2A · FR-3.1.1 · FR-5.3 ·
# FR-8 · §12.1-4) — **데몬 없이**, 데몬 역할(claim·phase·heartbeat·finish)은 curl 로 흉내(88_ 의 레시피).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 20s
#
# 재는 것 (판정 표 out/91-checks.tsv). 새 경로(createRoom)로 만든 방 하나에 미션을 여럿 연다.
#   A. 열기(FR-2A.1): goal 만 → Director = 연 사람 · assignee 없으면 종료 조건 user_approval 단독 · assignee 있으면
#      artifact_submitted(assignee) AND user_approval + 그 에이전트의 첫 task(미션 귀속) · 번들 limits 는 min(미션, 방).
#   B. 귀속(FR-3.1.1) 규칙 1~4: chosen · thread · running_lane · none(새 방은 옛 호환 규칙 없음) — 미리보기와 게시가 같다.
#   C. 미션별 예산·일시정지 독립(FR-2A.3): 미션 예산 초과 → 그 미션만 paused(budget) + 받은 요청 work_paused ·
#      방은 막히지 않는다 · 다른 미션을 Director 가 멈춰도 서로 무관 · 멈춘 미션의 새 task 는 안 나가고 다른 미션 것은 나간다 ·
#      재개는 쓴 돈보다 큰 상한이어야(422 → 200).
#   D. 종료 요약(FR-2A.4): 미션마다 요약 메시지 1개(방에 남고 work_id·summary_message_id) · work_completed ·
#      남은 미션은 그대로 · 두 번째 종료 409.
#   E. 지난 미션(FR-2A.5): listWorks?status=completed · 끝난 미션도 getWork.
#   F. 이걸 미션으로(FR-3.1.1 사후 귀속): 미션 없던 메시지·스레드·그 task·lane 이 한 번에 귀속 · 다시 하면 409 message_has_work.
#   G. 제안(FR-2A.1): 에이전트(TaskToken)만 제안 · 받은 요청 work_proposed · 열면 연 사람이 Director · 다시 409
#      already_resolved · 거절은 타임라인 시스템 메시지.
#   H. 동시 미션 상한(FR-2A.5): 방 max_concurrent_works 를 넘으면 409 max_concurrent_works + open_works[].
#   I. Director 승계(openapi 0.2.3 · §12.1-4): 미션 Director 인 멤버를 내보내면 204 · 그 방의 방장이 잇는다 ·
#      미션 타임라인 한 줄 · activity_log work.director_succeeded.
#   J. 옛 경로 방(createSession)에 둘째 미션: getSession 은 그 세션의 미션에 고정 · work_id 없는 옛 게시는 그 미션 ·
#      pauseSession 은 그 미션만 · deleteSession 은 열린 미션이 있으면 409.
#   K. 미션 시간 상한(FR-2A.3): time_limit 이 지나면 paused(time) + 확인 요청(purpose time) + work_paused ·
#      time_extension 승인으로 재개.
#
# 스택(T-R1b2): server :8140 · pg :5491 · 컨테이너 colab-pg-r1b2-5491. 다른 워커 스택과 겹치지 않는다(§0-13).
# 사용: SERVER_URL=http://localhost:8140 PG_PORT=5491 PG_CONTAINER=colab-pg-r1b2-5491 bash e2e/p5/up.sh
#       bash e2e/p5/91_works.sh
#       SERVER_URL=http://localhost:8140 PG_PORT=5491 PG_CONTAINER=colab-pg-r1b2-5491 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8140}"
export PG_PORT="${PG_PORT:-5491}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-r1b2-5491}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/91-checks.tsv"; : > "$CHECKS"
API="$SERVER_URL/api/v1"
C_DIR="$OUT/91-c-dir.txt"; C_MEM="$OUT/91-c-mem.txt"
rm -f "$C_DIR" "$C_MEM"
COOKIE="$C_DIR"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-R1b2 ports"
CAPS='[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]'

# call METHOD PATH [JSON] → CODE · BODY (현재 $COOKIE 로)
call() { local o; o="$(api "$@")"; CODE="$(api_code <<<"$o")"; BODY="$(api_body <<<"$o")"; }
as() { local c="$1"; shift; COOKIE="$c" api_ok "$@"; }
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":8,"wait_ms":0}'; }
# of_work CLAIM WORK [AGENT] → 그 미션(·에이전트) 번들 하나
of_work() { jq -c --arg w "$2" --arg a "${3:-}" '[.tasks[]|select(.task.work_id==$w and ($a=="" or .task.agent_id==$a))][0] // empty' <<<"$1"; }
n_work() { jq --arg w "$2" '[.tasks[]|select(.task.work_id==$w)]|length' <<<"$1"; }
# 현재 attempt 로(재개가 다시 큐에 넣은 task 는 attempt 2 다).
att() { psqlq "select attempt from task where id='$1'"; }
running() { daemon_api "tasks/$1/attempts/$(att "$1")/phase" '{"phase":"running","pgid":4242}' >/dev/null; }
finish() { daemon_api "tasks/$1/attempts/$(att "$1")/finish" '{"outcome":"completed","stop_reason":"end_turn","transport":"acp","last_seq":0,"usage":{"input_tokens":10,"output_tokens":5,"cost_usd":0.001,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null; }
# post ROOM JSON → MessagePostResult (사람)
post() { api_ok POST "/sessions/$1/messages" "$2" -H "Idempotency-Key: $(uuid)"; }
preview() { api_ok POST "/sessions/$1/messages/preview" "$2"; }
msg_work() { psqlq "select coalesce(work_id::text,'-') from message where id='$1'"; }
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" '{name:$n,role:"lead",role_description:"d",instructions:"짧게",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
open_work() { api_ok POST "/rooms/$1/works" "$2" | jq -r .id; }
wget_() { api_ok GET "/works/$1"; }
conds() { jq -r '[.completion_condition|..|objects|select(has("type"))|.type]|join("+")'; }

step "0. 방장 Dir · 멤버 Mem 가입, 워크스페이스·컴퓨터(curl 페어링)·에이전트 Lead·R"
DIR_ID="$(signup "r1b2-dir-$RUN@example.com" password123 "Dir")"
WS="$(create_workspace "R1b2 $RUN")"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-r1b2" "$CAPS" "/tmp/colab-r1b2-$RUN")"
export DTOK RID
LEAD="$(mk_agent Lead)"; R="$(mk_agent R)"
MEM_ID="$(COOKIE="$C_MEM" signup "r1b2-mem-$RUN@example.com" password123 "Mem")"
INV="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}' | jq -r .token)"
MEM_MID="$(as "$C_MEM" POST "/invites/$INV/accept" | jq -r .id)"
ROOM="$(api_ok POST "/workspaces/$WS/rooms" "$(jq -nc --arg n "91 미션 방 $RUN" '{name:$n}')" | jq -r .id)"
for a in "$LEAD" "$R"; do api_ok POST "/rooms/$ROOM/participants" "$(jq -nc --arg a "$a" '{agent_id:$a}')" >/dev/null; done
api_ok POST "/rooms/$ROOM/participants" "$(jq -nc --arg u "$MEM_ID" '{user_id:$u}')" >/dev/null
chk 0.1 "-" "$(psqlq "select coalesce(legacy_work_id::text,'-') from room where id='$ROOM'")" "새 경로(createRoom) 방에는 옛 세션 표식이 없다"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. 한 방에 미션 두 개 열기 (FR-2A.1)"
W1="$(open_work "$ROOM" "$(jq -nc --arg a "$LEAD" '{goal:"릴리스 노트 정리\n세부는 나중에",assignee_agent_id:$a}')")"
W2="$(open_work "$ROOM" '{"goal":"경쟁사 가격 조사","limits":{"budget_usd":0.01}}')"
J1="$(wget_ "$W1")"; J2="$(wget_ "$W2")"
chk A.1 "릴리스 노트 정리/$DIR_ID/active/director" "$(jq -r '.title+"/"+.director_user_id+"/"+.status+"/"+.my_work_role' <<<"$J1")" "제목 = goal 첫 줄 · Director = 연 사람(방 기본값 없음)"
chk A.2 "artifact_submitted+user_approval" "$(conds <<<"$J1")" "assignee 있음 → artifact_submitted(assignee) AND user_approval"
chk A.3 "user_approval" "$(conds <<<"$J2")" "assignee 없음 → user_approval 단독(FR-2A.1, S-84 교훈)"
chk A.4 2 "$(psqlq "select count(*) from work where room_id='$ROOM'")" "방 하나에 미션 둘(work_room_single 해제)"
chk A.5 2 "$(api_ok GET "/workspaces/$WS/rooms" | jq -r --arg r "$ROOM" '.items[]|select(.id==$r)|.active_work_count')" "방 목록 active_work_count 2 — 방은 한 줄"
CL="$(claim)"; B1="$(of_work "$CL" "$W1" "$LEAD")"
[ -n "$B1" ] || die "A: claim 에 W1 Lead 첫 task 가 없다: $CL"
T1="$(jq -r .task.id <<<"$B1")"; TOK1="$(jq -r .task_token <<<"$B1")"; running "$T1"
chk A.6 "$W1" "$(psqlq "select coalesce(l.work_id::text,'-') from task t join lane l on l.id=t.lane_id where t.id='$T1'")" "Lead 첫 task 의 줄기는 W1 에 매인다"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 메시지 귀속 FR-3.1.1 규칙 1~4 (미리보기 = 게시)"
PV="$(preview "$ROOM" "$(jq -nc --arg w "$W2" '{content:"W2 에 메모",work_id:$w}')")"
chk B.1 "$W2/chosen" "$(jq -r '(.work.id//"-")+"/"+.work_source' <<<"$PV")" "규칙 1 chosen(작성창 선택)"
M1="$(post "$ROOM" "$(jq -nc --arg w "$W2" '{content:"W2 에 메모",work_id:$w}')" | jq -r .message.id)"
chk B.2 "$W2" "$(msg_work "$M1")" "  … 게시된 메시지 work_id = W2"
PV="$(preview "$ROOM" "$(jq -nc --arg p "$M1" '{content:"답글",parent_id:$p}')")"
chk B.3 "$W2/thread" "$(jq -r '(.work.id//"-")+"/"+.work_source' <<<"$PV")" "규칙 2 thread(스레드의 미션)"
PV="$(preview "$ROOM" "$(jq -nc --arg c "$(mention Lead "$LEAD") 진행 상황?" '{content:$c}')")"
chk B.4 "$W1/running_lane" "$(jq -r '(.work.id//"-")+"/"+.work_source' <<<"$PV")" "규칙 3 running_lane(멘션한 에이전트가 W1 에서 실행 중)"
PV="$(preview "$ROOM" '{"content":"잡담"}')"
chk B.5 "-/none" "$(jq -r '(.work.id//"-")+"/"+.work_source' <<<"$PV")" "규칙 4 none — 새 방에는 옛 호환 규칙이 없다(T-R1b2)"
M4="$(post "$ROOM" '{"content":"잡담"}' | jq -r .message.id)"
chk B.6 "-" "$(msg_work "$M4")" "  … 게시된 잡담 work_id 없음"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 미션별 예산·일시정지 독립 (FR-2A.3)"
OUTR="$(post "$ROOM" "$(jq -nc --arg w "$W2" --arg c "$(mention R "$R") 가격표 모아 주세요" '{content:$c,work_id:$w}')")"
CL="$(claim)"; B2="$(of_work "$CL" "$W2" "$R")"; [ -n "$B2" ] || die "C: claim 에 W2 R task 가 없다: $CL"
T2="$(jq -r .task.id <<<"$B2")"; running "$T2"
chk C.1 0.01 "$(jq -r '.limits.budget_usd // "-"' <<<"$B2")" "번들 limits.budget_usd = min(미션 잔여 0.01, 방 잔여 없음)"
daemon_api "tasks/$T2/attempts/1/heartbeat" '{"usage":{"input_tokens":1000,"output_tokens":1000,"cost_usd":0.05,"estimated":false,"model":"claude-sonnet-5"},"last_seq":0}' > "$OUT/91-C-hb.json"
J2="$(wget_ "$W2")"; J1="$(wget_ "$W1")"
chk C.2 "paused/budget" "$(jq -r '.status+"/"+(.paused_reason//"-")' <<<"$J2")" "미션 예산 초과 → W2 만 paused(budget)"
chk C.3 "active" "$(jq -r .status <<<"$J1")" "W1 은 그대로 active"
chk C.4 "-" "$(psqlq "select coalesce(blocked_reason::text,'-') from room where id='$ROOM'")" "방은 막히지 않는다(방 한도가 아니다)"
chk C.5 1 "$(psqlq "select count(*) from inbox_item i join member m on m.id=i.member_id where i.type='work_paused' and i.work_id='$W2' and m.user_id='$DIR_ID'")" "받은 요청 work_paused → W2 Director"
finish "$T1"   # Lead 의 첫 턴이 끝났다 — Lead 는 비어 있다
call POST "/works/$W1/pause"
chk C.6 "200/paused/director" "$CODE/$(jq -r '.status+"/"+(.paused_reason//"-")' <<<"$BODY")" "W1 을 Director 가 멈춤 → paused(director)"
post "$ROOM" "$(jq -nc --arg w "$W1" --arg c "$(mention Lead "$LEAD") W1 이어서" '{content:$c,work_id:$w}')" >/dev/null
post "$ROOM" "$(jq -nc --arg w "$W2" --arg c "$(mention R "$R") W2 도" '{content:$c,work_id:$w}')" >/dev/null
# R 의 W2 줄기는 예산 멈춤으로 parked — 실행 중이 아니므로 규칙 3 이 없고 「미션 없음」
post "$ROOM" "$(jq -nc --arg c "$(mention R "$R") 미션 밖 잡일" '{content:$c,work_id:null}')" >/dev/null
CL="$(claim)"
chk C.7 "0/0" "$(n_work "$CL" "$W1")/$(n_work "$CL" "$W2")" "멈춘 두 미션의 새 task 는 나가지 않는다"
chk C.8 1 "$(jq --arg r "$ROOM" '[.tasks[]|select(.task.session_id==$r and (.task.work_id // "")=="")]|length' <<<"$CL")" "  … 미션 밖 task 는 방 게이트만 — 나간다"
call POST "/works/$W2/resume" '{"limits":{"budget_usd":0.03}}'
chk C.9 "422" "$CODE" "재개 상한이 쓴 돈(\$0.05)보다 작으면 422(FR-7.3)"
call POST "/works/$W2/resume" '{"limits":{"budget_usd":1}}'
chk C.10 "200/active" "$CODE/$(jq -r .status <<<"$BODY")" "상한 \$1 로 W2 재개"
chk C.11 "paused" "$(wget_ "$W1" | jq -r .status)" "  … W1 은 여전히 paused(서로 무관)"
api_ok POST "/works/$W1/resume" >/dev/null
CL="$(claim)"
chk C.12 1 "$(jq --arg w "$W1" --arg a "$LEAD" '[.tasks[]|select(.task.work_id==$w and .task.agent_id==$a)]|length' <<<"$CL")" "W1 재개 → 기다리던 W1 Lead task 가 나간다"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 종료 요약 — 미션마다 하나 (FR-2A.4)"
for t in $(psqlq "select id from task where session_id='$ROOM' and status in ('dispatched','preparing','running')"); do running "$t"; finish "$t"; done
call POST "/works/$W1/complete" '{"confirm":true}'
chk D.1 "200/completed" "$CODE/$(jq -r .status <<<"$BODY")" "W1 수동 종료(manual)"
S1="$(jq -r '.summary_message_id // "-"' <<<"$BODY")"
chk D.2 "summary/$W1" "$(psqlq "select kind||'/'||coalesce(work_id::text,'-') from message where id='$S1'")" "요약은 방의 메시지 · work_id = W1 · summary_message_id 가 가리킨다"
chk D.3 1 "$(psqlq "select count(*) from inbox_item i join member m on m.id=i.member_id where i.type='work_completed' and i.work_id='$W1' and m.user_id='$DIR_ID'")" "받은 요청 work_completed"
chk D.4 "active" "$(wget_ "$W2" | jq -r .status)" "W2 는 그대로"
call POST "/works/$W2/complete" '{"confirm":true}'
chk D.5 "200/completed" "$CODE/$(jq -r .status <<<"$BODY")" "W2 도 종료"
chk D.6 "1/1" "$(psqlq "select count(*) from message where work_id='$W1' and kind='summary' and summary_range is null")/$(psqlq "select count(*) from message where work_id='$W2' and kind='summary' and summary_range is null")" "요약은 미션당 1개 — 방에 먼저 요약이 있어도 둘째 미션 요약이 빠지지 않는다"
call POST "/works/$W1/complete" '{"confirm":true}'
chk D.7 "409" "$CODE" "끝난 미션 두 번째 종료 → 409"

# ───────────────────────────── E ─────────────────────────────────────────────
step "E. 지난 미션 (FR-2A.5)"
chk E.1 2 "$(api_ok GET "/rooms/$ROOM/works?status=completed" | jq '.items|length')" "listWorks?status=completed → 2"
chk E.2 "completed" "$(wget_ "$W1" | jq -r .status)" "끝난 미션도 getWork(읽기 전용)"

# ───────────────────────────── F ─────────────────────────────────────────────
step "F. 이걸 미션으로 — 사후 귀속 (FR-3.1.1)"
OUTF="$(post "$ROOM" "$(jq -nc --arg c "$(mention R "$R") 이 버그 좀 봐 주세요" '{content:$c,work_id:null}')")"
MF="$(jq -r .message.id <<<"$OUTF")"; TF="$(jq -r '.triggers[0].task_id' <<<"$OUTF")"
MFR="$(post "$ROOM" "$(jq -nc --arg p "$MF" '{content:"재현 절차 첨부",parent_id:$p}')" | jq -r .message.id)"
chk F.1 "-/-/-" "$(msg_work "$MF")/$(msg_work "$MFR")/$(psqlq "select coalesce(work_id::text,'-') from task where id='$TF'")" "귀속 전: 메시지·답글·task 모두 미션 없음"
W3="$(open_work "$ROOM" "$(jq -nc --arg m "$MFR" --arg a "$R" '{goal:"버그 고치기",from_message_id:$m,assignee_agent_id:$a}')")"
chk F.2 "$W3/$W3/$W3/$W3" "$(msg_work "$MF")/$(msg_work "$MFR")/$(psqlq "select t.work_id||'/'||l.work_id from task t join lane l on l.id=t.lane_id where t.id='$TF'")" "원 메시지·스레드·그 task·lane 이 한 번에 W3"
chk F.3 "$MFR" "$(wget_ "$W3" | jq -r '.opened_from_message_id // "-"')" "opened_from_message_id"
call POST "/rooms/$ROOM/works" "$(jq -nc --arg m "$MF" '{goal:"또",from_message_id:$m}')"
chk F.4 "409/message_has_work" "$CODE/$(jq -r .code <<<"$BODY")" "이미 미션에 속한 메시지 → 409 message_has_work"

# ───────────────────────────── G ─────────────────────────────────────────────
step "G. 에이전트 제안 → 사람이 연다 (FR-2A.1)"
call POST "/rooms/$ROOM/work-proposals" '{"goal":"g","rationale":"r"}'
chk G.1 "403/agent_only" "$CODE/$(jq -r .code <<<"$BODY")" "사람은 제안하지 않는다(바로 연다)"
# 살아 있는 attempt 의 토큰 — 끝난 task 의 토큰은 finish 가 폐기한다(FR-9.1).
CL="$(claim)"; BG="$(jq -c --arg r "$ROOM" '[.tasks[]|select(.task.session_id==$r)][0] // empty' <<<"$CL")"
[ -n "$BG" ] || die "G: claim 에 이 방 task 가 없다: $CL"
TOKG="$(jq -r .task_token <<<"$BG")"; AG="$(jq -r .task.agent_id <<<"$BG")"; AGN="$(jq -r .task.agent_name <<<"$BG")"
P1="$(curl -sS -X POST "$API/rooms/$ROOM/work-proposals" -H "Authorization: Bearer $TOKG" -H 'Content-Type: application/json' \
  -d '{"goal":"성능 회귀 추적","rationale":"주간 지표가 20% 나빠졌다"}' | jq -r .id)"
P2="$(curl -sS -X POST "$API/rooms/$ROOM/work-proposals" -H "Authorization: Bearer $TOKG" -H 'Content-Type: application/json' \
  -d '{"goal":"문서 정리","rationale":"중복이 많다"}' | jq -r .id)"
chk G.2 "open/$AG" "$(api_ok GET "/work-proposals/$P1" | jq -r '.status+"/"+.agent.id')" "제안(TaskToken) — 제안한 에이전트 · open"
chk G.3 1 "$(psqlq "select count(*) from inbox_item i join member m on m.id=i.member_id where i.type='work_proposed' and i.ref_id='$P1' and m.user_id='$MEM_ID'")" "받은 요청 work_proposed(방 참여자)"
RES="$(as "$C_MEM" POST "/work-proposals/$P1/resolution" '{"action":"accept"}')"
W4="$(jq -r .work.id <<<"$RES")"
chk G.4 "accepted/$MEM_ID/성능 회귀 추적" "$(jq -r '.proposal.status+"/"+.work.director_user_id+"/"+.work.goal' <<<"$RES")" "Mem 이 연다 → 연 사람이 Director"
call POST "/work-proposals/$P1/resolution" '{"action":"reject","reason":"늦었다"}'
chk G.5 "409/already_resolved/accepted" "$CODE/$(jq -r '.code+"/"+(.status//"-")' <<<"$BODY")" "이미 처리 → 409 already_resolved(무엇이 되었는지 확장에)"
api_ok POST "/work-proposals/$P2/resolution" '{"action":"reject","reason":"이번 분기 범위 밖"}' >/dev/null
chk G.6 1 "$(psqlq "select count(*) from message where session_id='$ROOM' and kind='system' and content like '%$AGN 의 미션 제안 「문서 정리」을 거절했습니다. 사유: 이번 분기 범위 밖'")" "거절 → 타임라인 시스템 메시지로 제안한 에이전트에게"

# ───────────────────────────── H ─────────────────────────────────────────────
step "H. 동시 미션 상한 (FR-2A.5)"
api_ok PATCH "/rooms/$ROOM" '{"limits":{"max_concurrent_works":2}}' >/dev/null
call POST "/rooms/$ROOM/works" '{"goal":"세 번째로 열려는 미션"}'
chk H.1 "409/max_concurrent_works" "$CODE/$(jq -r .code <<<"$BODY")" "열린 미션 2(W3·W4) = 상한 2 → 409"
chk H.2 "2" "$(jq '.open_works|length' <<<"$BODY")" "  … Problem.open_works[] 에 열린 미션"
call POST "/rooms/$ROOM/works" '{"goal":"초안은 세지 않는다","draft":true}'
chk H.3 "201/draft" "$CODE/$(jq -r .status <<<"$BODY")" "draft 는 실행 중이 아니라 상한에 걸리지 않는다"

# ───────────────────────────── I ─────────────────────────────────────────────
step "I. Director 승계 (openapi 0.2.3 · PRD §12.1-4)"
call DELETE "/workspaces/$WS/members/$MEM_MID"
chk I.1 204 "$CODE" "W4 Director 인 Mem 을 내보낸다 → 204(옛 409 없음)"
chk I.2 "$DIR_ID" "$(wget_ "$W4" | jq -r .director_user_id)" "W4 Director = 그 방의 방장(Dir)"
chk I.3 1 "$(psqlq "select count(*) from message where session_id='$ROOM' and work_id='$W4' and kind='system' and content like '%이 미션의 Director 를 이어받았습니다.'")" "미션 타임라인 한 줄"
chk I.4 1 "$(psqlq "select count(*) from activity_log where session_id='$ROOM' and action='work.director_succeeded'")" "activity_log work.director_succeeded"

# ───────────────────────────── J ─────────────────────────────────────────────
step "J. 옛 경로 방(createSession)에 둘째 미션 — 옛 /sessions 는 그 세션의 미션에 고정"
SID="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg l "$LEAD" --arg rt "$RID" '{title:"옛 세션",goal:"옛 목표",isolation:{kind:"none"},participants:[{agent_id:$l}],assignee_agent_id:$l,runtime_id:$rt}')" | jq -r .id)"
LW="$(psqlq "select legacy_work_id from room where id='$SID'")"
W5="$(open_work "$SID" '{"goal":"옛 방의 새 미션"}')"
chk J.1 "옛 세션/옛 세션/옛 세션" "$(for i in 1 2 3; do api_ok GET "/sessions/$SID" | jq -r .title; done | paste -sd/ -)" "getSession 은 세 번 모두 그 세션의 미션(임의의 한 미션이 아니다)"
M5="$(post "$SID" '{"content":"옛 화면에서 한 줄"}' | jq -r .message.id)"
chk J.2 "$LW" "$(msg_work "$M5")" "work_id 키 없는 옛 게시 → 그 세션의 미션(옛 경로 방만의 호환 규칙)"
M6="$(post "$SID" '{"content":"미션 없음","work_id":null}' | jq -r .message.id)"
chk J.3 "-" "$(msg_work "$M6")" "work_id:null(칩의 「미션 없음」) → 규칙 4"
api_ok POST "/sessions/$SID/pause" >/dev/null
chk J.4 "paused/active" "$(wget_ "$LW" | jq -r .status)/$(wget_ "$W5" | jq -r .status)" "pauseSession 은 그 세션의 미션만"
api_ok POST "/sessions/$SID/cancel" >/dev/null
call DELETE "/sessions/$SID"
chk J.5 "409/session_active" "$CODE/$(jq -r .code <<<"$BODY")" "deleteSession 은 방을 지운다 — 다른 미션이 열려 있으면 409"

# ───────────────────────────── K ─────────────────────────────────────────────
step "K. 미션 시간 상한 (FR-2A.3 — 새 경로)"
api_ok POST "/works/$W5/cancel" >/dev/null
W6="$(open_work "$SID" '{"goal":"짧은 일","limits":{"time_limit":"PT1H"}}')"
psqlq "update work set started_at = now() - interval '61 minutes' where id='$W6'" >/dev/null
KD=$(( $(date +%s) + 90 ))
while [ "$(date +%s)" -lt "$KD" ] && [ "$(psqlq "select status from work where id='$W6'")" != "paused" ]; do sleep 3; done
chk K.1 "paused/time" "$(wget_ "$W6" | jq -r '.status+"/"+(.paused_reason//"-")')" "시간 상한이 지나면 스케줄러가 paused(time) (1분 틱)"
HK="$(psqlq "select id from hitl_request where work_id='$W6' and purpose='time' and status='open'")"
chk K.2 "director/1" "$(psqlq "select approver_spec from hitl_request where id='$HK'")/$(psqlq "select count(*) from inbox_item where type='work_paused' and work_id='$W6'")" "확인 요청(purpose time, Director) · 받은 요청 work_paused"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HK" '{"approved":true}')"
chk K.3 422 "$CODE" "승인에는 time_extension 이 필요하다"
IFS=$'\t' read -r CODE BODY <<<"$(respond_hitl "$HK" '{"approved":true,"time_extension":"PT2H"}')"
chk K.4 "200/active/PT3H" "$CODE/$(wget_ "$W6" | jq -r '.status+"/"+.limits.time_limit')" "time_extension PT2H 승인 → 재개, 상한 PT3H"

step "요약"
PASS_N="$(awk -F'\t' '$2=="PASS"' "$CHECKS" | wc -l | tr -d ' ')"; FAIL_N="$(awk -F'\t' '$2=="FAIL"' "$CHECKS" | wc -l | tr -d ' ')"
log "91_: PASS $PASS_N · FAIL $FAIL_N (out/91-checks.tsv)"
[ "$FAIL_N" = 0 ]
