#!/usr/bin/env bash
# e2e/p5/81_observations_commands.sh — T-S19 실서버 스모크: v1.1 첫 라운드 (openapi 0.1.5, 계약 #244)
#   K-18 관찰 표 getWorkspaceObservations · FR-7.2 빈 턴 카드 · K-19 역할별 colab 명령(403 command_not_allowed)
# — **데몬 없이**, 데몬 역할(claim·phase·events·finish)은 curl 로 흉내(70_·79_·80_ 의 레시피).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 15s
#
# 재는 것 (판정 표 out/81-checks.tsv):
#   A. 관찰 표 모양 — 멤버 200 · 5행 §11 순서 · label/note 한국어 · 표본 없는 행은 n 0·null ·
#      routing_concentration 만 breakdown(9종) · window 에코 · 잘못된 window 422 · 비멤버 403.
#   B. 세션 하나를 돌린다 — Director 가 Lead 를 멘션 → Lead 턴이 `lane delegate` 로 W 에게 위임(task 토큰) →
#      W 턴이 메시지 게시 → 두 턴 finish. 관찰 5행이 n ≥ 1 로 실값을 낸다(chain_scale·chain_depth·join_breadth·
#      routing_concentration breakdown·empty_turn_rate).
#   C. 빈 턴 — Lead 를 다시 멘션 → 턴이 아무것도 안 하고 finish(end_turn) → task_event 에
#      status/turn_end/"empty_turn"/info 카드 1행(args.note 문장), 두 번째 finish 에도 1행, SSE task_event.appended 로
#      나갔다(stream_event), 관찰 empty_turn_rate 가 그 행을 센다. 메시지를 올린 W 턴에는 카드가 없다.
#   D. 역할별 명령 — Agent.allowed_commands(reviewer 10 · writer 9 · lead 13 · custom 13 = 계약 §2.5) ·
#      getCliContext.allowed_commands · 번들 task.allowed_commands 가 같은 표 · reviewer 토큰 lane delegate → 403
#      command_not_allowed(§2.5 문장 + command 칸) + task_event status rejected 행 · writer 토큰 review approve → 403 ·
#      reviewer artifact submit → 403 · lead 토큰 lane delegate → 201 · custom 토큰 lane delegate → 201 ·
#      사람(Director)은 표에 안 걸린다.
#
# 스택(T-S19 배정, V11_TASKS §0): server :8117 · pg :5461 · 컨테이너 colab-pg-s19.
# 사용: SERVER_URL=http://localhost:8117 PG_PORT=5461 PG_CONTAINER=colab-pg-s19 bash e2e/p5/up.sh
#       bash e2e/p5/81_observations_commands.sh
#       SERVER_URL=http://localhost:8117 PG_PORT=5461 PG_CONTAINER=colab-pg-s19 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8117}"
export PG_PORT="${PG_PORT:-5461}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-s19}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/81-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/81-cookies-dir.txt"; rm -f "$COOKIE"
COOKIE_OUT="$OUT/81-cookies-outsider.txt"; rm -f "$COOKIE_OUT"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-S19 ports"
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
as() { local c="$1"; shift; COOKIE="$c" api "$@"; }
# tok_api TOKEN METHOD PATH [JSON] → 본문 + 마지막 줄 코드 (task 토큰으로)
tok_api() {
  local tok="$1" method="$2" path="$3" body="${4:-}"
  if [ -n "$body" ]; then
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuid)" -X "$method" "$API$path" --data "$body"
  else
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $tok" -X "$method" "$API$path"
  fi
}
obs() { api_ok GET "/workspaces/$WS/observations${1:-}"; }
obs_row() { obs | jq -c --arg k "$1" '.rows[]|select(.key==$k)'; }
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" --arg r "$2" '{name:$n,role:$r,role_description:"d",
  instructions:"짧게, 한국어로 답한다. 저장소나 다른 디렉토리를 뒤지지 마라.",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
mention() { # SESSION AGENT_ID NAME TEXT → Director 가 @멘션
  api_ok POST "/sessions/$1/messages" "$(jq -nc --arg a "$2" --arg n "$3" --arg t "$4" '{content:("[@"+$n+"](mention://agent/"+$a+") "+$t)}')" -H "Idempotency-Key: $(uuid)" >/dev/null
}
run_turn() { # SESSION AGENT → claim → phase running. 표준출력: task_id<TAB>task_token<TAB>allowed_commands(csv)
  local s="$1" a="$2" cl b tid tok ac
  cl="$(claim)"
  b="$(jq -c --arg s "$s" --arg a "$a" '.tasks[]|select(.task.session_id==$s and .task.agent_id==$a)' <<<"$cl")"
  [ -n "$b" ] || die "claim 에 세션 $s / 에이전트 $a 의 task 가 없다: $cl"
  tid="$(jq -r .task.id <<<"$b")"; tok="$(jq -r .task_token <<<"$b")"; ac="$(jq -r '(.task.allowed_commands // [])|join(",")' <<<"$b")"
  daemon_api "tasks/$tid/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null
  printf '%s\t%s\t%s' "$tid" "$tok" "$ac"
}
finish_turn() { # TASK [STOP_REASON]
  daemon_api "tasks/$1/attempts/1/finish" "$(jq -nc --arg sr "${2:-end_turn}" '{outcome:"completed",stop_reason:$sr,transport:"acp",last_seq:0,
    usage:{input_tokens:2000,output_tokens:500,estimated:false,model:"claude-sonnet-5"}}')" >/dev/null
}
empty_cards() { psqlq "select count(*) from task_event where task_id='$1' and attempt=1 and class='status' and verb='turn_end' and object_ref=to_jsonb('empty_turn'::text) and outcome='info' and payload->'args'->>'note'='아무것도 하지 않고 턴을 끝냈습니다'"; }
refused_rows() { psqlq "select string_agg(verb||':'||(payload->>'command'), ',' order by seq) from task_event where task_id='$1' and class='status' and outcome='rejected' and payload->>'rejected_reason'='command_not_allowed'"; }
allowed_of() { api_ok GET "/agents/$1" | jq -r '.allowed_commands|join(",")'; }
LEAD_ALL="session_get,session_messages,artifact_get,message_post,status_set,decision_record,lane_delegate,artifact_submit,review_approve,review_reject,hitl_ask,hitl_approve_request,hitl_request_info,room_list,room_read,work_propose"
WRITER_ALL="session_get,session_messages,artifact_get,message_post,status_set,decision_record,artifact_submit,hitl_ask,hitl_request_info,room_list,room_read"
REVIEWER_ALL="session_get,session_messages,artifact_get,message_post,status_set,decision_record,review_approve,review_reject,hitl_ask,hitl_request_info,room_list,room_read"

step "0. 계정(Director=owner) · 워크스페이스 · 에이전트 Lead·W(writer)·R(reviewer)·C(custom) · 페어링(curl) · probe"
signup "s19-dir-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "S19 $RUN")"
LEAD="$(mk_agent Lead lead)"; W="$(mk_agent W writer)"; R="$(mk_agent R reviewer)"; C="$(mk_agent C custom)"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-s19" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-s19-$RUN")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. 관찰 표 모양 — 빈 워크스페이스"
obs | jq . > "$OUT/81-obs-empty.json"
chk A.1 "chain_scale,chain_depth,join_breadth,routing_concentration,empty_turn_rate" "$(obs | jq -r '[.rows[].key]|join(",")')" "5행 §11 순서"
chk A.2 "P30D/$WS" "$(obs | jq -r '.window+"/"+.workspace_id')" "window 기본 P30D · workspace_id"
chk A.3 "0/0/0/0/0" "$(obs | jq -r '[.rows[].n]|map(tostring)|join("/")')" "세션이 없으니 5행 전부 n 0"
chk A.4 "null/null/null" "$(obs_row chain_scale | jq -r '(.value|tostring)+"/"+(.median|tostring)+"/"+(.p95|tostring)')" "표본 없는 분포형: value·median·p95 전부 null(0 아님)"
chk A.5 "null" "$(obs_row empty_turn_rate | jq -r '.value|tostring')" "표본 없는 비율형: value null"
chk A.6 5 "$(obs | jq -r '[.rows[]|select((.label|test("[가-힣]")) and (.note|test("[가-힣]")))]|length')" "label·note 다섯 행 모두 한국어"
chk A.7 "9/1,2,3,4,5,6,7,8,platform" "$(obs_row routing_concentration | jq -r '(.breakdown|length|tostring)+"/"+([.breakdown[].kind]|join(","))')" "routing_concentration 만 breakdown 9종(규칙 1~8 + platform)"
chk A.8 0 "$(obs | jq -r '[.rows[]|select(.key!="routing_concentration" and has("breakdown"))]|length')" "다른 행에는 breakdown 없음"
chk A.9 P7D "$(obs '?window=P7D' | jq -r .window)" "window 에코"
chk A.10 422 "$(api GET "/workspaces/$WS/observations?window=7days" | api_code)" "기간 표기가 아니면 422"
COOKIE="$COOKIE_OUT" signup "s19-out-$RUN@example.com" password123 "Out" >/dev/null
chk A.11 403 "$(as "$COOKIE_OUT" GET "/workspaces/$WS/observations" | api_code)" "비멤버 403"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 세션 하나 — Lead 위임(task 토큰) → W 메시지 → 관찰 5행 실값"
S="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg rt "$RID" --arg l "$LEAD" --arg w "$W" --arg r "$R" --arg c "$C" \
  '{title:"관찰",goal:"저장소 밖에서 짧은 인사말 한 줄을 쓴다",isolation:{kind:"none"},participants:[{agent_id:$l},{agent_id:$w},{agent_id:$r},{agent_id:$c}],assignee_agent_id:$l,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"user_approval"}]}}')" | jq -r .id)"
mention "$S" "$LEAD" Lead "시작해 주세요"
IFS=$'\t' read -r T_LEAD TT_LEAD AC_LEAD <<<"$(run_turn "$S" "$LEAD")"
R_="$(tok_api "$TT_LEAD" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$W" '{agent_id:$a,brief:"인사말 초안을 써 주세요"}')")"
chk B.1 201 "$(api_code <<<"$R_")" "Lead(lead) 의 lane delegate → 201"
finish_turn "$T_LEAD"
IFS=$'\t' read -r T_W TT_W AC_W <<<"$(run_turn "$S" "$W")"
chk B.2 201 "$(tok_api "$TT_W" POST "/sessions/$S/messages" '{"content":"안녕하세요"}' | api_code)" "W(writer) 의 message post → 201"
finish_turn "$T_W"
obs | jq . > "$OUT/81-obs-session.json"
chk B.3 "2/1/1/3/2" "$(obs | jq -r '[.rows[].n]|map(tostring)|join("/")')" "n: chain_scale 2(사람 hop: 세션 시작·멘션) · chain_depth 1(세션) · join_breadth 1(그룹) · routing 3(hop: 세션 시작·멘션·위임) · empty_turn 2(완료 attempt)"
chk B.4 "0.5/0.95" "$(obs_row chain_scale | jq -r '(.median|tostring)+"/"+(.p95|tostring)')" "chain_scale: 세션 시작 hop 뒤 0 · 멘션 뒤 위임 1 → 중앙값 0.5 · p95 0.95"
chk B.5 "2/2" "$(obs_row chain_depth | jq -r '(.median|tostring)+"/"+(.p95|tostring)')" "chain_depth: 사람 → Lead → W = 2"
chk B.6 "1/1" "$(obs_row join_breadth | jq -r '(.median|tostring)+"/"+(.p95|tostring)')" "join_breadth: Lead 의 위임 그룹 자식 lane 1"
chk B.7 "2=2,platform=1" "$(obs_row routing_concentration | jq -r '[.breakdown[]|select(.n>0)|.kind+"="+(.n|tostring)]|join(",")')" "routing breakdown: 규칙 2(멘션·위임) 2 · platform(세션 시작 사람 hop) 1"
chk B.8 0 "$(obs_row routing_concentration | jq -r '.value')" "규칙 6·7 폴백 비율 0"
chk B.9 0 "$(obs_row empty_turn_rate | jq -r '.value')" "빈 턴 비율 0(두 턴 다 무언가 했다)"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 빈 턴 — Lead 가 아무것도 안 하고 end_turn → 카드 1행 · SSE · 관찰"
mention "$S" "$LEAD" Lead "다시 봐 주세요"
IFS=$'\t' read -r T_E TT_E AC_E <<<"$(run_turn "$S" "$LEAD")"
# 데몬 배치: 생각 한 줄·읽기 한 번 — 셋(메시지·플랫폼 조작·편집) 중 아무것도 아니다
SE0="$(psqlq "select count(*) from stream_event where workspace_id='$WS' and type='task_event.appended' and payload->>'task_id'='$T_E'")"
EV="$(daemon_api "tasks/$T_E/attempts/1/events" "$(jq -nc --arg t "$T_E" '{events:[
  {task_id:$t,attempt:1,seq:1,ts:(now|todate),class:"message",verb:"think",object_ref:"t",outcome:"ok",payload:{kind:"thought",text:"음",chars:1}},
  {task_id:$t,attempt:1,seq:2,ts:(now|todate),class:"tool",verb:"read",object_ref:"a.md",outcome:"ok",payload:{tool_call_id:"c1",kind:"read",path:"a.md"}}]}')")"
chk C.0 2 "$(jq -r '.accepted_seq_max // .code' <<<"$EV")" "데몬 배치 2건(생각·읽기) 수락"
finish_turn "$T_E"
chk C.1 1 "$(empty_cards "$T_E")" "빈 턴 카드 1행(status/turn_end/empty_turn/info · args.note 문장)"
finish_turn "$T_E"
chk C.2 1 "$(empty_cards "$T_E")" "finish 를 다시 보내도 카드는 1행"
chk C.3 1 "$(psqlq "select count(*) from stream_event where workspace_id='$WS' and type='task_event.appended' and payload->>'task_id'='$T_E' and payload->>'verb'='turn_end' and payload->>'outcome'='info'")" "SSE task_event.appended 로 카드가 나갔다(stream_event)"
chk C.4 3 "$(( $(psqlq "select count(*) from stream_event where workspace_id='$WS' and type='task_event.appended' and payload->>'task_id'='$T_E'") - SE0 ))" "데몬 배치 2 + 카드 1 = 그 턴의 appended 프레임 3"
chk C.5 "0/0" "$(empty_cards "$T_LEAD")/$(empty_cards "$T_W")" "위임한 Lead 턴·메시지 올린 W 턴에는 카드 없음"
chk C.6 1 "$(api GET "/tasks/$T_E/events" | api_body | jq '[.items[]|select(.verb=="turn_end" and .outcome=="info" and .payload.args.note=="아무것도 하지 않고 턴을 끝냈습니다")]|length')" "listTaskEvents(Director)에 카드가 보인다"
chk C.7 "3/0.3333" "$(obs_row empty_turn_rate | jq -r '(.n|tostring)+"/"+((.value*10000|round)/10000|tostring)')" "empty_turn_rate: 완료 3 중 빈 턴 1"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 역할별 명령 — 세 표면 + 403 command_not_allowed"
chk D.1 "$LEAD_ALL" "$(allowed_of "$LEAD")" "Agent.allowed_commands lead = 16 전부(§2.5 v0.8)"
chk D.2 "$WRITER_ALL" "$(allowed_of "$W")" "writer = delegate·review·approve-request·work propose 제외 11"
chk D.3 "$REVIEWER_ALL" "$(allowed_of "$R")" "reviewer = delegate·submit·approve-request·work propose 제외 12"
chk D.4 "$LEAD_ALL" "$(allowed_of "$C")" "custom = 전부"
chk D.5 "$LEAD_ALL/$WRITER_ALL" "$AC_LEAD/$AC_W" "번들 task.allowed_commands 가 같은 표(lead·writer)"
# 한 번에 하나씩 멘션·claim — claim 은 queued 를 전부 넘기므로 둘을 같이 멘션하면 첫 claim 이 둘 다 가져간다
mention "$S" "$R" R "검토해 주세요"
IFS=$'\t' read -r T_R TT_R AC_R <<<"$(run_turn "$S" "$R")"
mention "$S" "$C" C "도와주세요"
IFS=$'\t' read -r T_C TT_C AC_C <<<"$(run_turn "$S" "$C")"
chk D.6 "$REVIEWER_ALL/$LEAD_ALL" "$AC_R/$AC_C" "번들 reviewer·custom"
chk D.7 "$REVIEWER_ALL" "$(tok_api "$TT_R" GET /cli/context | api_body | jq -r '.allowed_commands|join(",")')" "getCliContext.allowed_commands(reviewer)"
R_="$(tok_api "$TT_R" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$W" '{agent_id:$a,brief:"대신 써 주세요"}')")"
api_body <<<"$R_" | jq . > "$OUT/81-403-delegate.json"
chk D.8 "403/command_not_allowed" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)" "reviewer 토큰 lane delegate → 403 command_not_allowed"
chk D.9 "이 역할(reviewer)은 lane delegate 를 쓸 수 없습니다" "$(api_body <<<"$R_" | jq -r .detail)" "§2.5 문장(역할 enum · 명령은 CLI 표기)"
chk D.10 "lane_delegate/reviewer" "$(api_body <<<"$R_" | jq -r '.command+"/"+.role')" "Problem 확장 칸 command(enum)·role"
chk D.11 0 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_R'")" "거부된 위임은 lane 을 만들지 않았다"
printf '# r\n리뷰\n' > "$OUT/81-r.md"
chk D.12 403 "$(curl -sS -o "$OUT/81-403-submit.json" -w '%{http_code}' -X POST "$API/sessions/$S/artifacts" -H "Authorization: Bearer $TT_R" -H "Idempotency-Key: $(uuid)" -F name=r.md -F type=doc -F "file=@$OUT/81-r.md")" "reviewer artifact submit → 403"
chk D.13 "delegate:lane delegate,submit_artifact:artifact submit" "$(refused_rows "$T_R")" "task_event status rejected 행 2(rejected_reason=command_not_allowed, command 는 CLI 표기)"
chk D.14 201 "$(tok_api "$TT_R" POST "/sessions/$S/messages" '{"content":"검토 의견입니다"}' | api_code)" "reviewer 의 message post 는 201(허용 명령은 그대로)"
# writer 의 review approve → 403 (아티팩트는 Lead 가 낸 것)
mention "$S" "$LEAD" Lead "초안 내 주세요"
IFS=$'\t' read -r T_L2 TT_L2 _ <<<"$(run_turn "$S" "$LEAD")"
printf '# d\n초안\n' > "$OUT/81-d.md"
ART="$(curl -sS -X POST "$API/sessions/$S/artifacts" -H "Authorization: Bearer $TT_L2" -H "Idempotency-Key: $(uuid)" -F name=d.md -F type=doc -F "file=@$OUT/81-d.md" | jq -r '.artifact.id // empty')"
chk D.15 1 "$(psqlq "select count(*) from artifact where id='${ART:-00000000-0000-0000-0000-000000000000}'")" "Lead(lead) artifact submit → 저장"
chk D.16 1 "$(psqlq "select count(*) from task_event where task_id='$T_L2' and class='status' and verb='submit_artifact' and outcome='ok'")" "§4: artifact submit 도 status 행(빈 턴 판정이 제출만 한 턴을 놓치지 않게)"
finish_turn "$T_L2"
chk D.17 0 "$(empty_cards "$T_L2")" "제출만 한 턴은 빈 턴이 아니다"
mention "$S" "$W" W "검토해 보세요"
IFS=$'\t' read -r T_W2 TT_W2 _ <<<"$(run_turn "$S" "$W")"
R_="$(tok_api "$TT_W2" POST "/artifacts/$ART/review" '{"verdict":"approve","comments":"확인"}')"
chk D.18 "403/command_not_allowed/이 역할(writer)은 review approve 를 쓸 수 없습니다" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r '.code+"/"+.detail')" "writer 토큰 review approve → 403"
chk D.19 "review:review approve" "$(refused_rows "$T_W2")" "writer 의 거부 행"
chk D.20 "403/command_not_allowed" "$(tok_api "$TT_W2" POST "/sessions/$S/hitl-requests" '{"type":"approval","summary":"끝"}' | { b="$(cat)"; printf '%s/%s' "$(api_code <<<"$b")" "$(api_body <<<"$b" | jq -r .code)"; })" "writer 토큰 hitl approve-request → 403"
chk D.21 201 "$(tok_api "$TT_W2" POST "/sessions/$S/hitl-requests" '{"type":"info","what":"무엇","why":"이유"}' | api_code)" "writer 토큰 hitl request-info → 201(허용)"
finish_turn "$T_W2"
# custom · lead: delegate 201
chk D.22 201 "$(tok_api "$TT_C" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$W" '{agent_id:$a,brief:"custom 이 위임"}')" | api_code)" "custom 토큰 lane delegate → 201"
chk D.23 201 "$(tok_api "$TT_C" POST "/sessions/$S/decisions" '{"summary":"custom 결정"}' | api_code)" "custom 토큰 decision record → 201"
chk D.24 1 "$(psqlq "select count(*) from task_event where task_id='$T_C' and class='status' and verb='record_decision' and outcome='ok'")" "§4: decision record 도 status 행"
chk D.25 "0/0" "$(refused_rows "$T_C" | sed 's/^$/0/')/$(refused_rows "$T_LEAD" | sed 's/^$/0/')" "custom·lead 는 거부 행 0"
chk D.26 201 "$(api POST "/sessions/$S/messages" '{"content":"사람이 씁니다"}' -H "Idempotency-Key: $(uuid)" | api_code)" "사람(Director)은 표에 걸리지 않는다"
chk D.27 200 "$(api GET "/sessions/$S" | api_code)" "사람의 getSession 200"

step "결과: $CHECKS"
printf '%s\n' "PASS $(grep -c $'\tPASS\t' "$CHECKS") · FAIL $FAILS" | tee "$OUT/81-summary.txt"
[ "$FAILS" = 0 ]
