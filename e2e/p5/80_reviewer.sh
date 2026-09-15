#!/usr/bin/env bash
# e2e/p5/80_reviewer.sh — T-S18 실서버 스모크: S-84 (openapi 0.1.4)
#   agent_approval 리뷰어 필수(422) · 진행률 agent_name/blocked_reason/next_actor · active 에서 종료 조건 수정
# — **데몬 없이**, 데몬 역할(claim·phase·finish)은 curl 로 흉내(70_·79_ 의 레시피).
#
# 재는 것 (판정 표 out/80-checks.tsv):
#   A. createSession 검증 — 리뷰어 없는 agent_approval → 422 reviewer_required(errors[].field 가 그 원자를
#      가리킨다) · 참여자 아닌 리뷰어 → 422 reviewer_not_participant · artifact_submitted 의 agent_id 도 같은 코드
#      · 리뷰어 지정 → 201, 진행률 conditions[] 에 agent_id·agent_name·next_actor(에이전트 이름/director), blocked_reason null.
#   B. 새 세션이 실제로 닫힌다 — Lead 턴 claim → 아티팩트(task 토큰) → 진행률 artifact_submitted met → @R 멘션으로
#      R 턴 claim → reviewArtifact approve(R 토큰) → completed · 요약 1 · 사람 승인 확인 요청 0(agent_approval 단독).
#   C. 옛 모양 세션(리뷰어 없는 agent_approval, DB 로 심음) — getSession 200 · blocked_reason reviewer_missing ·
#      리뷰어가 참여자에서 빠지면 reviewer_not_participant · archived 면 agent_archived
#      → updateSession(completion_condition, active) 으로 구함: 422 두 코드가 똑같이 막고 → 리뷰어 지정 200 →
#      blocked_reason null · SSE session.completion_progress + session.updated · activity_log
#      session.completion_condition_changed → R 승인 → completed.
#   D. 이미 충족된 원자 유지 — 아티팩트 제출 뒤 조건을 artifact_submitted 단독으로 바꾸면 즉시 completed;
#      user_approval 만 남는 조건으로 바꾸면 확인 요청 1건(두 번 바꿔도 1건) → Director 승인 → completed.
#   E. 권한·상태 — 멤버 403 director_required · completed 세션 422 immutable · paused 에서는 200 이고 paused 유지.
#
# 스택(T-S18 배정): server :8116 · pg :5460 · 컨테이너 colab-pg-s18. 다른 워커 스택과 겹치지 않는다(§0-13).
# 사용: SERVER_URL=http://localhost:8116 PG_PORT=5460 PG_CONTAINER=colab-pg-s18 bash e2e/p5/up.sh
#       bash e2e/p5/80_reviewer.sh
#       SERVER_URL=http://localhost:8116 PG_PORT=5460 PG_CONTAINER=colab-pg-s18 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8116}"
export PG_PORT="${PG_PORT:-5460}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-s18}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/80-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/80-cookies-dir.txt"; rm -f "$COOKIE"
COOKIE_MEM="$OUT/80-cookies-mem.txt"; rm -f "$COOKIE_MEM"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-S18 ports"
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
as() { local c="$1"; shift; COOKIE="$c" api "$@"; }
# tree ATOM_JSON... → {op:and, conditions:[...]}
tree() { jq -nc '$ARGS.positional | map(fromjson) | {op:"and",conditions:.}' --args "$@"; }
# cond SESSION TYPE → conditions[] 의 그 원자 한 행(json)
cond() { api_ok GET "/sessions/$1" | jq -c --arg t "$2" '.completion_progress.conditions[]|select(.type==$t)'; }
# cond_field SESSION TYPE FIELD → 값(null 이면 "null")
cond_field() { cond "$1" "$2" | jq -r --arg f "$3" '.[$f] // "null"'; }
# err_code JSON FIELD → errors[] 에서 그 field 의 code
err_code() { jq -r --arg f "$2" '[.errors[]|select(.field==$f)][0].code // "-"' <<<"$1"; }
# mk_session TITLE TREE_JSON AGENT... → 세션 id (첫 에이전트가 assignee); 실패하면 응답 본문
mk_session() {
  local t="$1" tr="$2"; shift 2
  api POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "$t" --arg rt "$RID" --argjson tr "$tr" --arg a0 "$1" '$ARGS.positional | map({agent_id:.}) |
    {title:$t,goal:"저장소 밖에서 짧은 인사말 한 줄을 쓴다",isolation:{kind:"none"},participants:.,assignee_agent_id:$a0,runtime_id:$rt,completion_condition:$tr}' --args "$@")"
}
run_turn() { # SESSION AGENT → 그 세션·에이전트의 queued task 를 claim → phase running. 표준출력: task_id<TAB>task_token
  local s="$1" a="$2" cl b tid tok
  cl="$(claim)"
  b="$(jq -c --arg s "$s" --arg a "$a" '.tasks[]|select(.task.session_id==$s and .task.agent_id==$a)' <<<"$cl")"
  [ -n "$b" ] || die "claim 에 세션 $s / 에이전트 $a 의 task 가 없다: $cl"
  tid="$(jq -r .task.id <<<"$b")"; tok="$(jq -r .task_token <<<"$b")"
  daemon_api "tasks/$tid/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null
  printf '%s\t%s' "$tid" "$tok"
}
finish_turn() { # TASK
  daemon_api "tasks/$1/attempts/1/finish" '{"outcome":"completed","stop_reason":"end_turn","transport":"acp","last_seq":0,
    "usage":{"input_tokens":2000,"output_tokens":500,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null
}
submit() { # SESSION TASK_TOKEN NAME → artifact id
  printf '# %s\n안녕\n' "$3" > "$OUT/80-$3.md"
  curl -sS -X POST "$API/sessions/$1/artifacts" -H "Authorization: Bearer $2" -H "Idempotency-Key: $(uuid)" \
    -F "name=$3" -F type=doc -F "file=@$OUT/80-$3.md" | jq -r '.artifact.id // empty'
}
review() { # ARTIFACT TASK_TOKEN VERDICT → 코드
  curl -sS -o "$OUT/80-review.json" -w '%{http_code}' -X POST "$API/artifacts/$1/review" -H "Authorization: Bearer $2" -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg v "$3" '{verdict:$v,comments:"확인"}')"
}
wake() { # SESSION AGENT_ID NAME → Director 가 @멘션으로 그 에이전트를 깨운다
  api_ok POST "/sessions/$1/messages" "$(jq -nc --arg a "$2" --arg n "$3" '{content:("[@"+$n+"](mention://agent/"+$a+") 검토 부탁합니다")}')" -H "Idempotency-Key: $(uuid)" >/dev/null
}
patch_cond() { # COOKIEFILE SESSION TREE_JSON → 코드/본문(표준출력 두 줄: code, body)
  as "$1" PATCH "/sessions/$2" "$(jq -nc --argjson tr "$3" '{completion_condition:$tr}')"
}
approval_open() { psqlq "select count(*) from hitl_request where session_id='$1' and purpose='user_approval' and status='open'"; }
cond_changed() { psqlq "select count(*) from activity_log where session_id='$1' and action='session.completion_condition_changed'"; }

step "0. 계정 2(Director=owner · member) · 워크스페이스 · 에이전트 Lead·R·W · 페어링(curl) · probe"
signup "s18-dir-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "S18 $RUN")"
INV_MEM="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}' | jq -r .token)"
COOKIE="$COOKIE_MEM" signup "s18-mem-$RUN@example.com" password123 "Mem" >/dev/null
as "$COOKIE_MEM" POST "/invites/$INV_MEM/accept" | api_code | grep -q '^20' || die "member invite accept"
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" --arg r "$2" '{name:$n,role:$r,role_description:"d",
  instructions:"짧게, 한국어로 답한다. 저장소나 다른 디렉토리를 뒤지지 마라. 도구가 실패해도 재시도하거나 다른 방법을 찾지 마라.",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
LEAD="$(mk_agent Lead lead)"; R="$(mk_agent R reviewer)"; W="$(mk_agent W writer)"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-s18" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-s18-$RUN")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. createSession 검증 — 리뷰어 없음 422 · 참여자 아님 422 · 리뷰어 지정 201 + 진행률 사람 말"
R_="$(mk_session "리뷰어 없음 $RUN" "$(tree '{"type":"artifact_submitted","who":"assignee"}' '{"type":"agent_approval"}')" "$LEAD" "$R")"
api_body <<<"$R_" | jq . > "$OUT/80-422-required.json"
chk A.1 "422/reviewer_required" "$(api_code <<<"$R_")/$(err_code "$(api_body <<<"$R_")" completion_condition/conditions/1/agent_id)" "agent_approval 에 agent_id 없음 → 422 reviewer_required (field 가 그 원자)"
chk A.2 1 "$(api_body <<<"$R_" | jq -r '.errors[0].message' | grep -c '리뷰어' || true)" "errors[].message 는 사람 말(리뷰어를 고르라고)"
R_="$(mk_session "참여자 아님 $RUN" "$(tree "$(jq -nc --arg a "$W" '{type:"agent_approval",agent_id:$a}')")" "$LEAD" "$R")"
chk A.3 "422/reviewer_not_participant" "$(api_code <<<"$R_")/$(err_code "$(api_body <<<"$R_")" completion_condition/conditions/0/agent_id)" "W 는 참여자가 아니다 → 422 reviewer_not_participant"
R_="$(mk_session "제출자 참여자 아님 $RUN" "$(tree "$(jq -nc --arg a "$W" '{type:"artifact_submitted",agent_id:$a}')" '{"type":"user_approval"}')" "$LEAD" "$R")"
chk A.4 "422/reviewer_not_participant" "$(api_code <<<"$R_")/$(err_code "$(api_body <<<"$R_")" completion_condition/conditions/0/agent_id)" "artifact_submitted 의 agent_id 도 참여자 검사(같은 코드)"
chk A.5 0 "$(psqlq "select count(*) from session where workspace_id='$WS'")" "422 셋 다 세션을 만들지 않았다"
TREE_B="$(tree '{"type":"artifact_submitted","who":"assignee"}' "$(jq -nc --arg a "$R" '{type:"agent_approval",agent_id:$a}')")"
R_="$(mk_session "리뷰어 R $RUN" "$TREE_B" "$LEAD" "$R")"
chk A.6 201 "$(api_code <<<"$R_")" "리뷰어 R(참여자) 지정 → 201"
S_B="$(api_body <<<"$R_" | jq -r .id)"
api_ok GET "/sessions/$S_B" | jq .completion_progress > "$OUT/80-progress-new.json"
chk A.7 "$R/R/null/R" "$(cond "$S_B" agent_approval | jq -r '(.agent_id//"null")+"/"+(.agent_name//"null")+"/"+(.blocked_reason//"null")+"/"+(.next_actor//"null")')" "agent_approval: agent_id=R · agent_name=R · blocked_reason null · next_actor=R"
chk A.8 "$LEAD/Lead/null/Lead" "$(cond "$S_B" artifact_submitted | jq -r '(.agent_id//"null")+"/"+(.agent_name//"null")+"/"+(.blocked_reason//"null")+"/"+(.next_actor//"null")')" "artifact_submitted(who: assignee): assignee Lead 로 풀린다"
chk A.9 "false/0/2" "$(api_ok GET "/sessions/$S_B" | jq -r '.completion_progress|(.satisfied|tostring)+"/"+(.met|tostring)+"/"+(.total|tostring)')" "satisfied false · met 0/2"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 새 세션이 닫힌다 — Lead 제출 → R 승인(task 토큰) → completed"
IFS=$'\t' read -r T_LEAD TT_LEAD <<<"$(run_turn "$S_B" "$LEAD")"
ART_B="$(submit "$S_B" "$TT_LEAD" report-b)"
chk B.1 1 "$(psqlq "select count(*) from artifact where id='${ART_B:-00000000-0000-0000-0000-000000000000}'")" "Lead 가 아티팩트 제출(task 토큰)"
chk B.2 "true/null" "$(cond "$S_B" artifact_submitted | jq -r '(.met|tostring)+"/"+(.next_actor//"null")')" "artifact_submitted met · 더는 누구 차례도 아니다"
chk B.3 "R" "$(cond_field "$S_B" agent_approval next_actor)" "agent_approval 은 R 차례"
chk B.4 403 "$(review "$ART_B" "$TT_LEAD" approve)" "리뷰어 아닌 Lead 의 승인 → 403 (지정된 리뷰어만)"
finish_turn "$T_LEAD"
wake "$S_B" "$R" R
IFS=$'\t' read -r T_R TT_R <<<"$(run_turn "$S_B" "$R")"
chk B.5 200 "$(review "$ART_B" "$TT_R" approve)" "R 승인 → 200"
finish_turn "$T_R"
chk B.6 "true/2/2" "$(jq -r '.completion_progress|(.satisfied|tostring)+"/"+(.met|tostring)+"/"+(.total|tostring)' "$OUT/80-review.json")" "reviewArtifact 응답의 진행률 satisfied 2/2"
chk B.7 completed "$(psqlq "select status from session where id='$S_B'")" "세션 completed"
chk B.8 "1/0" "$(psqlq "select (select count(*) from message where session_id='$S_B' and kind='summary')||'/'||(select count(*) from hitl_request where session_id='$S_B' and purpose='user_approval')")" "요약 1 · 사람 승인 확인 요청 0(agent_approval 에 사람 관문 없음)"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 옛 모양 세션(DB 로 심음) — blocked_reason 3종 → updateSession 으로 구함 → R 승인 → completed"
TREE_OLD='{"op":"and","conditions":[{"type":"artifact_submitted","who":"assignee"},{"type":"agent_approval"}]}'
mk_old() { # TITLE AGENT... → 세션 id (user_approval 로 만들고 completion_condition 을 옛 모양으로 덮는다)
  local t="$1"; shift; local s
  s="$(mk_session "$t" "$(tree '{"type":"user_approval"}')" "$@" | api_body | jq -r .id)"
  psqlq "update session set completion_condition='$TREE_OLD'::jsonb where id='$s'" >/dev/null
  printf '%s' "$s"
}
S_C="$(mk_old "옛 모양 $RUN" "$LEAD" "$R")"
chk C.1 200 "$(api GET "/sessions/$S_C" | api_code)" "옛 모양 세션 getSession 200 (500 아님)"
api_ok GET "/sessions/$S_C" | jq .completion_progress > "$OUT/80-progress-old.json"
chk C.2 "reviewer_missing/null/null/null" "$(cond "$S_C" agent_approval | jq -r '(.blocked_reason//"null")+"/"+(.agent_id//"null")+"/"+(.agent_name//"null")+"/"+(.next_actor//"null")')" "agent_approval: blocked_reason reviewer_missing · 지정 없음 · 누구 차례도 아님"
chk C.3 "Lead/null" "$(cond "$S_C" artifact_submitted | jq -r '(.next_actor//"null")+"/"+(.blocked_reason//"null")')" "artifact_submitted 는 Lead 차례(막히지 않음)"
chk C.4 false "$(api_ok GET "/sessions/$S_C" | jq -r .completion_progress.satisfied)" "satisfied false"
# 리뷰어가 세션을 떠난 경우 · archived 인 경우 — 각각 다른 세션으로
S_C2="$(mk_session "리뷰어 떠남 $RUN" "$(tree "$(jq -nc --arg a "$R" '{type:"agent_approval",agent_id:$a}')")" "$LEAD" "$R" | api_body | jq -r .id)"
psqlq "delete from session_participant where session_id='$S_C2' and agent_id='$R'" >/dev/null
chk C.5 "reviewer_not_participant/R" "$(cond "$S_C2" agent_approval | jq -r '(.blocked_reason//"null")+"/"+(.agent_name//"null")')" "리뷰어가 참여자에서 빠짐 → reviewer_not_participant (이름은 그대로)"
S_C3="$(mk_session "리뷰어 archived $RUN" "$(tree "$(jq -nc --arg a "$W" '{type:"agent_approval",agent_id:$a}')")" "$LEAD" "$W" | api_body | jq -r .id)"
psqlq "update agent set archived_at=now() where id='$W'" >/dev/null
chk C.6 "agent_archived/W" "$(cond "$S_C3" agent_approval | jq -r '(.blocked_reason//"null")+"/"+(.agent_name//"null")')" "리뷰어 archived → agent_archived"
psqlq "update agent set archived_at=null where id='$W'" >/dev/null
# 구하기 — 검증이 먼저 막는다
R_="$(patch_cond "$COOKIE" "$S_C" "$(tree '{"type":"agent_approval"}')")"
chk C.7 "422/reviewer_required" "$(api_code <<<"$R_")/$(err_code "$(api_body <<<"$R_")" completion_condition/conditions/0/agent_id)" "updateSession 도 리뷰어 없음 → 422 reviewer_required"
R_="$(patch_cond "$COOKIE" "$S_C" "$(tree "$(jq -nc --arg a "$W" '{type:"agent_approval",agent_id:$a}')")")"
chk C.8 "422/reviewer_not_participant" "$(api_code <<<"$R_")/$(err_code "$(api_body <<<"$R_")" completion_condition/conditions/0/agent_id)" "updateSession 참여자 아닌 W → 422 reviewer_not_participant"
chk C.9 "reviewer_missing/0" "$(cond_field "$S_C" agent_approval blocked_reason)/$(cond_changed "$S_C")" "422 는 저장하지 않았다(옛 모양 그대로 · 활동 기록 0)"
SSE="$OUT/80-sse.log"; : > "$SSE"
curl -sN -b "$COOKIE" "$API/workspaces/$WS/stream?session_id=$S_C" > "$SSE" 2>/dev/null &
SSE_PID=$!; disown; echo "$SSE_PID" > "$OUT/80-sse.pid"; sleep 1
TREE_FIX="$(tree '{"type":"artifact_submitted","who":"assignee"}' "$(jq -nc --arg a "$R" '{type:"agent_approval",agent_id:$a}')")"
R_="$(patch_cond "$COOKIE" "$S_C" "$TREE_FIX")"
api_body <<<"$R_" | jq . > "$OUT/80-patch-fix.json"
chk C.10 "200/active" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .status)" "리뷰어 R 지정 → 200, 세션은 active 그대로"
chk C.11 "null/R/R" "$(api_body <<<"$R_" | jq -r '.completion_progress.conditions[]|select(.type=="agent_approval")|(.blocked_reason//"null")+"/"+(.agent_name//"null")+"/"+(.next_actor//"null")')" "응답 진행률: blocked_reason null · agent_name R · next_actor R"
chk C.12 1 "$(cond_changed "$S_C")" "activity_log session.completion_condition_changed 1행"
# jsonb 는 키 순서를 보존하지 않는다 — 양쪽을 jq -S 로 정렬해 비교.
chk C.13 "$(jq -cS . <<<"$TREE_OLD")" "$(psqlq "select payload->'from' from activity_log where session_id='$S_C' and action='session.completion_condition_changed'" | jq -cS .)" "활동 기록 payload.from = 옛 트리"
sleep 1; kill "$SSE_PID" 2>/dev/null || true; rm -f "$OUT/80-sse.pid"
chk C.14 "1/1" "$(grep -c '^event: session.completion_progress' "$SSE" || true)/$(grep -c '^event: session.updated' "$SSE" || true)" "SSE session.completion_progress 1 · session.updated 1"
chk C.15 "R/null" "$(grep -A1 '^event: session.completion_progress' "$SSE" | grep '^data:' | sed 's/^data: //' | jq -r '.payload.completion_progress.conditions[]|select(.type=="agent_approval")|(.agent_name//"null")+"/"+(.blocked_reason//"null")')" "프레임의 진행률도 같은 열(agent_name R · 막힘 없음)"
# 이제 닫힌다
IFS=$'\t' read -r T_LEAD2 TT_LEAD2 <<<"$(run_turn "$S_C" "$LEAD")"
ART_C="$(submit "$S_C" "$TT_LEAD2" report-c)"
finish_turn "$T_LEAD2"
wake "$S_C" "$R" R
IFS=$'\t' read -r T_R2 TT_R2 <<<"$(run_turn "$S_C" "$R")"
chk C.16 200 "$(review "$ART_C" "$TT_R2" approve)" "R 승인 → 200"
finish_turn "$T_R2"
chk C.17 completed "$(psqlq "select status from session where id='$S_C'")" "구한 세션 completed"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 이미 충족된 원자 유지 — 제출 뒤 조건 변경: 단독이면 즉시 completed · user_approval 만 남으면 확인 요청 1건"
S_D="$(mk_old "충족 유지 $RUN" "$LEAD" "$R")"
IFS=$'\t' read -r T_D TT_D <<<"$(run_turn "$S_D" "$LEAD")"
ART_D="$(submit "$S_D" "$TT_D" report-d)"
finish_turn "$T_D"
chk D.1 "true/reviewer_missing" "$(cond_field "$S_D" artifact_submitted met)/$(cond_field "$S_D" agent_approval blocked_reason)" "제출 met · 리뷰어 없는 원자는 여전히 막힘"
R_="$(patch_cond "$COOKIE" "$S_D" "$(tree '{"type":"artifact_submitted","who":"assignee"}')")"
chk D.2 "200/completed" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .status)" "artifact_submitted 단독으로 바꾸면 이미 충족 → completed"
chk D.3 1 "$(psqlq "select count(*) from message where session_id='$S_D' and kind='summary'")" "기존 완료 경로(요약 1)"
S_D2="$(mk_old "승인만 남음 $RUN" "$LEAD" "$R")"
IFS=$'\t' read -r T_D2 TT_D2 <<<"$(run_turn "$S_D2" "$LEAD")"
ART_D2="$(submit "$S_D2" "$TT_D2" report-d2)"
finish_turn "$T_D2"
chk D.4 0 "$(approval_open "$S_D2")" "바꾸기 전 열린 사람 승인 요청 0"
TREE_UA="$(tree '{"type":"artifact_submitted","who":"assignee"}' '{"type":"user_approval"}')"
chk D.5 200 "$(patch_cond "$COOKIE" "$S_D2" "$TREE_UA" | api_code)" "user_approval 만 남는 조건으로 변경 → 200"
chk D.6 1 "$(approval_open "$S_D2")" "플랫폼이 사람 승인 요청 1건 발행"
chk D.7 200 "$(patch_cond "$COOKIE" "$S_D2" "$TREE_UA" | api_code)" "같은 조건으로 한 번 더 → 200"
chk D.8 1 "$(approval_open "$S_D2")" "요청은 여전히 1건(두 장이 아니다)"
chk D.9 director "$(cond_field "$S_D2" user_approval next_actor)" "user_approval 은 director 차례"
HITL="$(psqlq "select id from hitl_request where session_id='$S_D2' and purpose='user_approval' and status='open'")"
IFS=$'\t' read -r HC HB <<<"$(respond_hitl "$HITL" '{"approved":true}')"
chk D.10 200 "$HC" "Director 승인 응답 200"
chk D.11 completed "$(psqlq "select status from session where id='$S_D2'")" "→ completed"

# ───────────────────────────── E ─────────────────────────────────────────────
step "E. 권한·상태 — 멤버 403 · completed 422 immutable · paused 200(paused 유지)"
S_E="$(mk_old "권한 $RUN" "$LEAD" "$R")"
R_="$(patch_cond "$COOKIE_MEM" "$S_E" "$TREE_FIX")"
chk E.1 "403/director_required" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)" "멤버(Director 아님) → 403 director_required"
R_="$(patch_cond "$COOKIE" "$S_D" "$TREE_FIX")"
chk E.2 "422/immutable" "$(api_code <<<"$R_")/$(err_code "$(api_body <<<"$R_")" completion_condition)" "completed 세션 → 422 immutable"
api_ok POST "/sessions/$S_E/pause" '{"mode":"drain"}' >/dev/null
R_="$(patch_cond "$COOKIE" "$S_E" "$TREE_FIX")"
chk E.3 "200/paused/null" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r '.status+"/"+(.completion_progress.conditions[]|select(.type=="agent_approval")|(.blocked_reason//"null"))')" "paused 에서 변경 200 · paused 유지 · 막힘 해소"
chk E.4 1 "$(cond_changed "$S_E")" "paused 변경도 활동 기록 1행"

step "결과: $CHECKS"
printf '%s\n' "PASS $(grep -c $'\tPASS\t' "$CHECKS") · FAIL $FAILS" | tee "$OUT/80-summary.txt"
[ "$FAILS" = 0 ]
