#!/usr/bin/env bash
# e2e/p5/70_testchat.sh — T-S12 실서버 스모크: 테스트 채팅(FR-1.8.1, daemon-protocol v0.8 §4.5)
# + 관측 지표(openapi getWorkspaceMetrics) — **데몬 없이**, 데몬 역할은 curl 로 흉내.
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 15s
#
# 재는 것 (판정 표 out/70-checks.tsv):
#   A. createTestChat → postTestChatTurn(202 · 진행 중 409) → claim 이 §4.5 번들을 준다
#      (task.kind=test_chat · id=test_chat.id · attempt=턴 번호 · task_token 없음 · 브리프에 [2] 없음 ·
#       workdir {dir, <root>/.colab/testchat/<id>, reuse} · 첫 턴 프롬프트 머리 한 줄 · stall 180)
#   B. 데몬 curl: phase → heartbeat(preview → SSE test_chat.delta) → events(message.say 합침, task_event 0)
#      → finish(transport acp · usage 추정 · runtime_session_ref) → SSE test_chat.turn
#      → getTestChat 이 agent 턴·토큰·transport·추정 비용을 돌려준다
#   C. 턴 2 번들의 resume = 턴 1 이 저장한 ref; failed(auth) finish → 턴 error 가 §8.4 문장, 채팅은 열림
#   D. 워크스페이스 비용 test_chat_usd 합산 · by_session 에 없음
#   E. close → claim 응답에 gc {test_chat_id, workdirs:[{id,path}]} (session_id 없음) · 이후 턴 410 ·
#      §6 보고(test_chat_id + gc deleted) 뒤 gc 소비 · workdir 행 0
#   F. 진행 중 턴이 있는 채팅을 close → cancel{task_id=chat, attempt, reason=director} + gc; cancelled finish → 턴 error
#   G. getWorkspaceMetrics: 10개 · 표본 없는 워크스페이스는 전부 value null · n 0 · window 기본 P30D · 잘못된 window 422
#
# 스택: e2e/p5/up.sh (server :8107 · pg :5451 · colab-pg-s12). 다른 워커 스택과 겹치지 않는다(§0-13).
# 사용: bash e2e/p5/up.sh && bash e2e/p5/70_testchat.sh ; bash e2e/p5/down.sh
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/70-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/70-cookies.txt"; rm -f "$COOKIE"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh"
# 데몬 명령을 다음 응답에 실어 주는 claim (capacity 5, 대기 없음)
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }

step "0. 계정 · 워크스페이스 · 에이전트 · 페어링(curl) · probe(claude_code, workdir_root)"
signup "s12-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "S12 $RUN")"
AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc '{name:"Guide",role:"researcher",role_description:"제품 사용법을 설명한다",
  instructions:"짧게, 한국어로 답한다.",budget_per_task:0.5,profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id)"
ROOT="/tmp/colab-s12-$RUN"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-s12" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "$ROOT")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"
# 공유 스택(다른 스크립트가 먼저 돈 DB)에서도 재도록 전역 수는 이 스크립트 시작 시점과의 차이로 잰다(R4 e2e 이관).
EV0="$(psqlq "select count(*) from task_event")"
chk 0.2 "$ROOT" "$(psqlq "select workdir_root from runtime where id='$RID'")" "workdir_root 저장(§4.1 절대 경로 재료)"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. createTestChat → 턴 게시 → claim 번들(§4.5)"
CHAT_JSON="$(api_ok POST "/agents/$AG/test-chats" '{}')"
CHAT="$(jq -r .id <<<"$CHAT_JSON")"
chk A.1 "open/$RID" "$(jq -r '.status+"/"+.runtime_id' <<<"$CHAT_JSON")" "채팅 열림 · 그 runtime_kind 의 온라인 컴퓨터로 고정"
chk A.2 0 "$(psqlq "select count(*) from room where workspace_id='$WS'")" "세션 0개 (FR-1.8.1 세션이 아니다)"
T1="$(api POST "/test-chats/$CHAT/turns" '{"content":"안녕, 너는 누구니?"}' -H "Idempotency-Key: $(uuid)")"
chk A.3 202 "$(api_code <<<"$T1")" "postTestChatTurn 202"
chk A.4 user "$(api_body <<<"$T1" | jq -r .role)" "202 본문 = 사용자 턴"
T1b="$(api POST "/test-chats/$CHAT/turns" '{"content":"또 하나"}' -H "Idempotency-Key: $(uuid)")"
chk A.5 "409/turn_in_progress" "$(api_code <<<"$T1b")/$(api_body <<<"$T1b" | jq -r .code)" "이전 턴 진행 중이면 409"
CL="$(claim)"; echo "$CL" > "$OUT/70-claim-1.json"
B="$(jq -c --arg c "$CHAT" '.tasks[]|select(.task.id==$c)' <<<"$CL")"; echo "$B" | jq . > "$OUT/70-bundle-1.json"
chk A.6 "test_chat/$CHAT/1/$CHAT" "$(jq -r '.task.kind+"/"+.task.id+"/"+(.task.attempt|tostring)+"/"+.task.test_chat_id' <<<"$B")" "task.kind=test_chat · id=test_chat.id · attempt=1 · test_chat_id"
chk A.7 "" "$(jq -r '.task_token' <<<"$B")" "task_token 빈 문자열 (E15-03)"
chk A.8 "//" "$(jq -r '.task.lane_id+"/"+.task.session_id+"/"+.task.trigger_message_id' <<<"$B")" "lane·session·trigger 빈 값"
chk A.9 0 "$(jq -r '.brief.text' <<<"$B" | grep -c '^\[2\]' || true)" "브리프에 [2] colab 명령 절 없음"
chk A.10 1 "$(jq -r '.brief.text' <<<"$B" | grep -c '^\[1\] Agent Identity' || true)" "브리프 [1] 정체성·지시문"
chk A.11 "dir/$ROOT/.colab/testchat/$CHAT/true" "$(jq -r '.workdir.kind+"/"+.workdir.path+"/"+(.workdir.reuse|tostring)' <<<"$B")" "workdir {dir, <root>/.colab/testchat/<id>, reuse:true}"
chk A.12 "이것은 시험 대화다 — 플랫폼 명령은 쓸 수 없다." "$(jq -r '.prompt' <<<"$B" | head -1)" "첫 턴 프롬프트 머리 한 줄"
chk A.13 "안녕, 너는 누구니?" "$(jq -r '.prompt' <<<"$B" | tail -1)" "프롬프트 = 사용자 턴 본문"
chk A.14 "null/180/0.5" "$(jq -r '(.resume|tostring)+"/"+(.limits.stall_seconds|tostring)+"/"+(.limits.budget_usd|tostring)' <<<"$B")" "resume null · stall 180 · budget=agent.budget_per_task"
chk A.15 dispatched "$(psqlq "select turn_status from test_chat where id='$CHAT'")" "claim 뒤 turn_status=dispatched"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 데몬 curl: phase → heartbeat(preview) → events → finish → SSE · getTestChat"
SSE="$OUT/70-sse.log"; : > "$SSE"
curl -sN -b "$COOKIE" "$API/workspaces/$WS/stream?test_chat_id=$CHAT" > "$SSE" 2>/dev/null &
SSE_PID=$!; echo "$SSE_PID" > "$OUT/70-sse.pid"
sleep 1
ATT="tasks/$CHAT/attempts/1"
# bash 3.2: 이스케이프한 따옴표를 $( ) 안에 두면 인자가 갈라진다(실측: chk 의 got·note 가 둘 다 코드가 됐다) — 본문은 jq 로 먼저 변수에.
PH1="$(jq -nc --arg p "$ROOT/.colab/testchat/$CHAT" '{phase:"preparing",pgid:4242,workdir_path:$p}')"
PH2="$(jq -nc --arg p "$ROOT/.colab/testchat/$CHAT" '{phase:"running",pgid:4242,workdir_path:$p}')"
chk B.1 200 "$(daemon_api_code "$ATT/phase" "$PH1")" "phase preparing 200"
chk B.2 200 "$(daemon_api_code "$ATT/phase" "$PH2")" "phase running 200"
HB="$(daemon_api "$ATT/heartbeat" '{"usage":{"input_tokens":100,"output_tokens":10,"estimated":true,"model":"claude-sonnet-5"},"last_seq":0,"preview":{"text":"나는 Guide"}}')"
chk B.3 "[]" "$(jq -c .commands <<<"$HB")" "heartbeat 200 · 명령 없음"
NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
EV="$(daemon_api "$ATT/events" "$(jq -nc --arg c "$CHAT" --arg ts "$NOW" '{events:[
  {task_id:$c,attempt:1,seq:1,ts:$ts,class:"runtime",verb:"start",outcome:"ok",payload:{runtime_kind:"claude_code"}},
  {task_id:$c,attempt:1,seq:2,ts:$ts,class:"message",verb:"say",outcome:"ok",partial:true,payload:{kind:"text",text:"나는 "}},
  {task_id:$c,attempt:1,seq:3,ts:$ts,class:"message",verb:"say",outcome:"ok",payload:{kind:"thought",text:"(생각)"}},
  {task_id:$c,attempt:1,seq:4,ts:$ts,class:"message",verb:"say",outcome:"ok",payload:{kind:"text",text:"나는 Guide 에이전트입니다."}}]}')")"
chk B.4 4 "$(jq -r .accepted_seq_max <<<"$EV")" "events accepted_seq_max=4"
chk B.5 "$EV0" "$(psqlq "select count(*) from task_event")" "task_event 저장 0 (§4.5 — 시작 시점과 같은 수)"
FIN="$(daemon_api "$ATT/finish" "$(jq -nc --arg cwd "$ROOT/.colab/testchat/$CHAT" --arg ts "$NOW" '{outcome:"completed",stop_reason:"end_turn",transport:"acp",last_seq:4,
  usage:{input_tokens:1200,output_tokens:300,estimated:true,model:"claude-sonnet-5"},
  runtime_session_ref:{runtime_kind:"claude_code",session_id:"acp-s12-1",cwd:$cwd,created_at:$ts}}')")"
chk B.6 true "$(jq -r .ok <<<"$FIN")" "finish 200 ok"
sleep 1; kill "$SSE_PID" 2>/dev/null || true; rm -f "$OUT/70-sse.pid"
chk B.7 1 "$(grep -c '^event: test_chat.delta' "$SSE" || true)" "SSE test_chat.delta 1 프레임(heartbeat preview)"
chk B.8 1 "$(grep -c '^event: test_chat.turn' "$SSE" || true)" "SSE test_chat.turn 1 프레임(finish)"
chk B.9 "나는 Guide 에이전트입니다." "$(grep -A1 '^event: test_chat.turn' "$SSE" | grep '^data:' | sed 's/^data: //' | jq -r .payload.turn.content)" "turn 프레임의 agent 본문 = 합쳐진 message.say(text, 비partial)"
G="$(api_ok GET "/test-chats/$CHAT")"; echo "$G" | jq . > "$OUT/70-testchat.json"
chk B.10 "user/agent" "$(jq -r '[.turns[].role]|join("/")' <<<"$G")" "턴 [user, agent]"
chk B.11 "나는 Guide 에이전트입니다." "$(jq -r '.turns[1].content' <<<"$G")" "agent 턴 본문"
chk B.12 "1200/300" "$(jq -r '(.turns[1].usage.input_tokens|tostring)+"/"+(.turns[1].usage.output_tokens|tostring)' <<<"$G")" "agent 턴 usage"
chk B.13 "acp/1200/300/true/open" "$(jq -r '.transport+"/"+(.input_tokens|tostring)+"/"+(.output_tokens|tostring)+"/"+(.estimated|tostring)+"/"+.status' <<<"$G")" "transport acp · 채팅 토큰 합 · estimated · open"
chk B.14 0.0054 "$(jq -r '.cost_usd*10000|round/10000' <<<"$G")" "추정 비용 = 워크스페이스 가격표(sonnet-5 \$2/\$10 per MTok)"
chk B.15 idle "$(psqlq "select turn_status from test_chat where id='$CHAT'")" "finish 뒤 turn_status=idle"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 턴 2 = resume 로 잇기 · failed(auth) → §8.4 문장"
api_ok POST "/test-chats/$CHAT/turns" '{"content":"한 문장으로 다시"}' -H "Idempotency-Key: $(uuid)" >/dev/null
B2="$(claim | jq -c --arg c "$CHAT" '.tasks[]|select(.task.id==$c)')"; echo "$B2" | jq . > "$OUT/70-bundle-2.json"
chk C.1 "2/acp-s12-1" "$(jq -r '(.task.attempt|tostring)+"/"+.resume.session_id' <<<"$B2")" "attempt 2 · resume = 턴 1 이 저장한 ref"
chk C.2 "한 문장으로 다시" "$(jq -r .prompt <<<"$B2")" "둘째 턴 프롬프트에는 머리 한 줄 없음"
ATT2="tasks/$CHAT/attempts/2"
daemon_api "$ATT2/phase" '{"phase":"running"}' >/dev/null
daemon_api "$ATT2/finish" '{"outcome":"failed","failure_kind":"auth","transport":"acp"}' >/dev/null
G="$(api_ok GET "/test-chats/$CHAT")"
chk C.3 "컴퓨터의 로그인이 만료되었습니다 — 그 컴퓨터에서 다시 로그인한 뒤 시도해 주세요" "$(jq -r '.turns[3].error' <<<"$G")" "failed(auth) 턴 error = §8.4 문장"
chk C.4 open "$(jq -r .status <<<"$G")" "턴은 남고 채팅은 열려 있다"
chk C.5 "409/stale_attempt" "$(daemon_api_code "$ATT/heartbeat" '{}')/$(daemon_api "$ATT/heartbeat" '{}' | jq -r .code)" "지난 attempt 보고는 409 stale_attempt"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 비용: 워크스페이스 test_chat_usd 에만"
COST="$(api_ok GET "/workspaces/$WS/cost")"
chk D.1 0.0054 "$(jq -r '.test_chat_usd*10000|round/10000' <<<"$COST")" "getWorkspaceCost.test_chat_usd = 채팅 비용"
chk D.2 0 "$(jq -r '.by_session|length' <<<"$COST")" "by_session 에 없음 (세션 아님)"

# ───────────────────────────── E ─────────────────────────────────────────────
step "E. close → gc 명령 → 410 → §6 영수증으로 소비"
CLOSED="$(api_ok POST "/test-chats/$CHAT/close" '')"
chk E.1 closed "$(jq -r .status <<<"$CLOSED")" "closeTestChat 200 closed"
CL="$(claim)"; echo "$CL" > "$OUT/70-claim-after-close.json"
GC="$(jq -c --arg c "$CHAT" '.commands[]|select(.type=="gc" and .test_chat_id==$c)' <<<"$CL")"
chk E.2 "$CHAT/$ROOT/.colab/testchat/$CHAT/null" "$(jq -r '.workdirs[0].id+"/"+.workdirs[0].path+"/"+(.session_id|tostring)' <<<"$GC")" "gc {test_chat_id, workdirs:[{id,path}]} · session_id 없음"
chk E.3 0 "$(jq -r '[.commands[]|select(.type=="cancel")]|length' <<<"$CL")" "진행 중 턴 없음 → cancel 없음"
T3="$(api POST "/test-chats/$CHAT/turns" '{"content":"닫힌 뒤"}' -H "Idempotency-Key: $(uuid)")"
chk E.4 "410/test_chat_closed" "$(api_code <<<"$T3")/$(api_body <<<"$T3" | jq -r .code)" "닫힌 채팅에 턴 → 410"
chk E.5 200 "$(api POST "/test-chats/$CHAT/close" '' | api_code)" "close 멱등 200"
daemon_api "runtimes/$RID/workdirs" '{"workdirs":[]}' >/dev/null
chk E.6 1 "$(claim | jq -r --arg c "$CHAT" '[.commands[]|select(.type=="gc" and .test_chat_id==$c)]|length')" "영수증 없는 §6 보고는 gc 를 소비하지 않는다(§4.3)"
daemon_api "runtimes/$RID/workdirs" "$(jq -nc --arg c "$CHAT" --arg p "$ROOT/.colab/testchat/$CHAT" '{workdirs:[{id:$c,kind:"dir",path:$p,test_chat_id:$c,bytes:0,gc:{status:"deleted"}}]}')" >/dev/null
chk E.7 0 "$(claim | jq -r --arg c "$CHAT" '[.commands[]|select(.type=="gc" and .test_chat_id==$c)]|length')" "gc deleted 영수증 뒤 명령 소비"
chk E.8 0 "$(psqlq "select count(*) from workdir where id='$CHAT' or path_or_ref like '$ROOT/%'")" "test_chat_id 행은 workdir 테이블에 안 들어간다(이 컴퓨터의 행 0)"

# ───────────────────────────── F ─────────────────────────────────────────────
step "F. 진행 중 턴이 있는 채팅을 close → cancel + gc"
CHAT2="$(api_ok POST "/agents/$AG/test-chats" '{}' | jq -r .id)"
api_ok POST "/test-chats/$CHAT2/turns" '{"content":"긴 답을 써 줘"}' -H "Idempotency-Key: $(uuid)" >/dev/null
claim >/dev/null
daemon_api "tasks/$CHAT2/attempts/1/phase" '{"phase":"running"}' >/dev/null
api_ok POST "/test-chats/$CHAT2/close" '' >/dev/null
CL="$(claim)"
chk F.1 "$CHAT2/1/director" "$(jq -r --arg c "$CHAT2" '.commands[]|select(.type=="cancel" and .task_id==$c)|.task_id+"/"+(.attempt|tostring)+"/"+.reason' <<<"$CL")" "cancel {task_id=chat, attempt 1, reason director}"
chk F.2 1 "$(jq -r --arg c "$CHAT2" '[.commands[]|select(.type=="gc" and .test_chat_id==$c)]|length' <<<"$CL")" "gc 도 함께"
daemon_api "tasks/$CHAT2/attempts/1/finish" '{"outcome":"cancelled","stop_reason":"cancelled"}' >/dev/null
chk F.3 "사람이 중단했습니다" "$(api_ok GET "/test-chats/$CHAT2" | jq -r '.turns[1].error')" "cancelled finish → 턴 error"
chk F.4 0 "$(claim | jq -r --arg c "$CHAT2" '[.commands[]|select(.type=="cancel" and .task_id==$c)]|length')" "finish 도착으로 cancel 소비"

# ───────────────────────────── G ─────────────────────────────────────────────
step "G. getWorkspaceMetrics"
M="$(api_ok GET "/workspaces/$WS/metrics")"; echo "$M" | jq . > "$OUT/70-metrics.json"
chk G.1 "10/P30D" "$(jq -r '(.metrics|length|tostring)+"/"+.window' <<<"$M")" "10개 · 기본 창 P30D"
chk G.2 "f1_minutes,auto_complete_rate,hitl_response_minutes,delegation_autonomous_rate,parallel_wallclock_reduction,task_success_rate_by_runtime,duplicate_after_resume_rate,resume_success_rate,blocked_response_minutes,weekly_active_sessions" \
  "$(jq -r '[.metrics[].key]|join(",")' <<<"$M")" "§11 열 순서"
chk G.3 10 "$(jq -r '[.metrics[]|select(.value==null and .n==0)]|length' <<<"$M")" "표본 없는 워크스페이스 = 전부 value null · n 0"
chk G.4 0 "$(jq -r '[.metrics[]|select(.label|test("runtime|lane|task|HITL|런타임";"i"))]|length' <<<"$M")" "label 에 내부 용어 없음(§8.4)"
chk G.5 2 "$(jq -r '.metrics[]|select(.key=="task_success_rate_by_runtime")|.breakdown|length' <<<"$M")" "breakdown 런타임 종류 2"
chk G.6 422 "$(api GET "/workspaces/$WS/metrics?window=7days" | api_code)" "잘못된 window → 422"

step "결과: $CHECKS"
column -t -s $'\t' "$CHECKS" | sed 's/^/  /' >&2
if [ "$FAILS" -eq 0 ]; then ok "70_testchat: FAIL 0"; else die "70_testchat: FAIL $FAILS"; fi
