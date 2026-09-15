#!/usr/bin/env bash
# e2e/p5/82_role_gate.sh — **역할별 colab 명령(K-19) 세 층 + 관찰 표(K-18) + lane.actions(I-2)** — 페이크 런타임, CI.
#
# 비용 한 줄(I-3): 에이전트 턴 ≈ 9(Lead 1 · R 1 · RH 1 · C 1 · W 1 · Idle 4) · 페이크 $0 · ≈ 60s. 실기(RUNTIME=real): 1턴 haiku ≈ $0.04(캐시 쓰기 포함, 실측 0.0423) · ≈ 60s.
#
# reviewer 에이전트가 `lane delegate` 를 시도하면 **세 층이 각각 막는다**(81_ 은 서버 층을 curl 로, 84_ 은 CLI 층을 바이너리로 쟀다 —
# 여기서는 데몬이 실제로 띄운 런타임 안에서 대본(fixtures/agent.sh Gate)이 시도한다):
#   (a) MCP  — 데몬이 session/new 에 실은 colab MCP 서버 argv 가 `mcp serve --allow <reviewer 10>` 이고(acpfake record),
#              그 argv 로 진짜 colab 을 띄우면 tools/list 에 colab_lane_delegate 가 **없다**. lead·custom 은 13 전부.
#   (b) CLI  — 런타임 안의 `colab lane delegate` → exit 3 command_not_allowed. claude_code(컨텍스트 모드)는 선(wire)에
#              GET /cli/context 뿐, hermes(래퍼 env 모드)는 선에 **0 줄** — POST /lanes 는 어느 쪽도 0.
#   (c) 서버 — 같은 토큰으로 curl 직접 POST /lanes → 403 command_not_allowed + task_event status/rejected 행. lead·custom 은 201.
#   (d) 사람(쿠키) 경로는 **비게이트**(PR #246 리뷰 NN4) — Director·멤버의 lane 생성·메시지·getSession 에 command_not_allowed 가 없다.
# 그리고 같은 세션에서:
#   (e) 관찰 표 — getWorkspaceObservations 5행 n ≥ 1 · Idle(빈 대본) 의 빈 턴 attempt → 카드 행(status/turn_end/empty_turn) ·
#       empty_turn_rate = 빈 턴 / 완료 attempt(DB 와 같은 수) · 대시보드(headless: 78_ 방식 — agent-browser 가 있으면 DOM, 없으면 프록시 200).
#   (f) I-2 lane.actions 를 상태별로 실서버 단언 — running → [restart,cancel] · queued → [cancel] · waiting_human → [respond_hitl] ·
#       done → [] · 멤버(비제어자)는 running 도 [].
#
# 대본 Gate 는 delegate 를 시도한 뒤 **토큰을 남기고 턴을 붙든다**(하네스가 go 파일을 줄 때까지) — 토큰은 finish 뒤 401 이라
# (c) 는 턴이 살아 있는 동안 쳐야 한다. 세션 limits.max_parallel_lanes=3 이라 R·RH·C 셋이 붙들면 다음 task 는 **queued** 다(f).
#
# RUNTIME=real: 페이크 절(A~F)은 건너뛰고 R 절만 — 로컬 claude_code reviewer 1턴(브리프 [2] 인용 · delegate 시도 여부 · exit 3 로그).
# 산출물: out/82-checks.tsv · out/82-gate-*.json · out/82-lanes-*.json · out/82-obs.json · out/82-mcp-*.out · out/82-real-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-82.txt"; rm -f "$COOKIE"; DIR_COOKIE="$COOKIE"; MEM_COOKIE="$OUT/cookies-82-mem.txt"; rm -f "$MEM_COOKIE"
CFG="$OUT/daemon-82.json"; WORK="$P5_TMP_ROOT/82/work"; DLOG="$OUT/daemon-82.log"
TAP="$OUT/tap-82.jsonl"; TAP_PORT="${TAP_PORT_82:-8122}"; ACCESS="$OUT/tap-82-access.tsv"
MODEL="${LEAD_MODEL}"
REC="$E2E_OUT/fake-records"
g5_chk_init "$OUT/82-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-82.pid"; return 0; }
trap cleanup EXIT
LEAD_ALL="session_get,session_messages,message_post,status_set,decision_record,lane_delegate,artifact_submit,artifact_get,review_approve,review_reject,hitl_ask,hitl_approve_request,hitl_request_info"
REVIEWER_ALL="session_get,session_messages,message_post,status_set,decision_record,artifact_get,review_approve,review_reject,hitl_ask,hitl_request_info"
DENY_LINE="이 역할은 위임 · 산출물 제출 · 완료 승인 요청을 쓰지 않는다."

# tok_api TOKEN METHOD PATH [JSON] → 본문 + 마지막 줄 코드 (task 토큰으로, 서버에 직접 — 탭을 거치지 않는다)
tok_api() {
  local tok="$1" method="$2" path="$3" body="${4:-}"
  if [ -n "$body" ]; then
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $tok" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuid)" -X "$method" "$API$path" --data "$body"
  else
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $tok" -X "$method" "$API$path"
  fi
}
rejected_rows() { psqlq "select count(*) from task_event where task_id='$1' and class='status' and outcome='rejected' and payload->>'rejected_reason'='command_not_allowed'"; }
empty_cards() { psqlq "select count(*) from task_event e join task t on t.id=e.task_id where t.session_id='$1' and e.class='status' and e.verb='turn_end' and e.object_ref=to_jsonb('empty_turn'::text) and e.outcome='info'"; }
# task_of SESSION AGENT_NAME [N] → 그 에이전트의 N번째(기본 마지막) task id
task_of() { psqlq "select t.id from task t join agent a on a.id=t.agent_id where t.session_id='$1' and a.name='$2' order by t.created_at desc limit 1"; }
# in_csv NEEDLE CSV → yes|no (bash 3.2: `$( case … )` 안의 `)` 가 깨진다 — 함수로)
in_csv() { case ",$2," in *",$1,"*) echo yes;; *) echo no;; esac; }
csv_len() { tr ',' '\n' <<<"$1" | grep -c .; }
gate_json() { cat "$REC/gate-$1.json" 2>/dev/null; }
gate_go() { : > "$REC/gate-$1.go"; }
# wait_gate ID 설명 TASK — 대본이 delegate 를 시도하고 토큰을 남길 때까지(단계 timeout, I-1)
wait_gate() { wait_step "$1" "$2" "$T_TURN" '[ -s "'"$REC/gate-$3.token"'" ] && [ -s "'"$REC/gate-$3.json"'" ]' 0.3; }
# lanes → out/82-lanes-<tag>.json (Director 또는 멤버 쿠키로 listLanes)
lanes_dump() { api_ok GET "/sessions/$S/lanes" > "$OUT/82-lanes-$1.json"; }
# lane_actions FILE AGENT_NAME [STATUS] → "status:actions(csv)" (그 에이전트의 lane, 여러 개면 마지막 생성)
lane_actions() { jq -r --arg a "$2" --arg st "${3:-}" '[.[]|select(.agent_name==$a and ($st=="" or .status==$st))]|last|.status+":"+(.actions|join(","))' "$OUT/82-lanes-$1.json"; }
# access_api_lines → 탭 접근 로그의 /api/v1 줄 수(데몬의 /v1/daemon 은 뺀다) · access_count PATTERN
access_api_lines() { grep -c $'\t/api/v1/' "$ACCESS" 2>/dev/null || true; }
access_count() { grep -c -- "$1" "$ACCESS" 2>/dev/null || true; }
# mcp_tools ARGS_JSON ENV_JSON → 그 argv·env 로 진짜 colab MCP 서버를 띄워 tools/list 의 이름 목록(csv). 서버 왕복 없음.
mcp_tools() {
  local -a args envs; local x
  while IFS= read -r x; do args+=("$x"); done < <(jq -r '.[]' <<<"$1")
  while IFS= read -r x; do envs+=("$x"); done < <(jq -r '.[]|.name+"="+.value' <<<"$2")
  printf '{"jsonrpc":"2.0","id":1,"method":"tools/list"}\n' \
    | env -i PATH="$PATH" HOME="$HOME" "${envs[@]}" "$BIN/colab" "${args[@]}" 2>/dev/null | sed -n 1p | jq -r '[.result.tools[].name]|join(",")'
}
# record_field NAME METHOD JQ → 그 에이전트 acpfake record 의 첫 METHOD 요청에서 JQ
record_field() { jq -c --arg m "$2" 'select(.method==$m)' "$REC/$1.jsonl" | head -1 | jq -c "$3"; }

# ═══════════════════════════════ 실기 절(RUNTIME=real) ═══════════════════════════════
if [ "$RUNTIME" = real ]; then
  # 로컬 claude_code(로그인) reviewer 1턴: 브리프 [2] 의 거부 줄을 인용하고, **위임을 시도해 보라고** 시킨다.
  # 잰다 — [2] 인용 · lane 행 0 · 서버 rejected 행(시도했다면 CLI/MCP 가 먼저 막았어야 하므로 0) · 데몬 로그 allowed/denied ·
  # 툴 목록에 colab_lane_delegate 없음 · exit 3/command_not_allowed 흔적(시도했다면 tool_call 카드에).
  step "R. 실기 대조 — claude_code reviewer 1턴 (비용: haiku 1턴 ≈ USD 0.01)"
  rm -rf "$WORK"; : > "$DLOG"; rm -f "$TAP" "$ACCESS"; : > "$TAP"
  TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
  signup "i6r+$STAMP@example.com" password123 Director >/dev/null
  WS="$(create_workspace "V11 Real Gate $STAMP")"
  read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
  PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 1
  daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-82.pid"
  wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
  RUNTIME_ID="$(runtime_of_config "$CFG")"
  chk R0 "런타임이 claude_code 를 광고한다" yes "$(in_set claude_code $(runtime_kinds "$RUNTIME_ID"))"
  INS="You are a reviewer. When the session goal asks you to quote a line from your brief, copy it character for character. $P4_RULES"
  LEAD="$(create_agent_kind "$WS" Lead lead claude_code "$MODEL" "You are the lead. $P4_RULES" '팀을 이끈다')"
  R="$(create_agent_kind "$WS" R reviewer claude_code "$MODEL" "$INS" '산출물을 검토한다')"
  GOAL="Do these three things, then end your turn. (1) Post ONE message whose body is exactly the line of section [2] of your system prompt (the brief) that starts with \"- 이 역할은\" — character for character. (2) Try to delegate the drafting to the agent named Lead using whatever colab tool or command you have for delegation; if no such tool exists or it is refused, post ONE message saying exactly what happened (tool missing, or the exact error). (3) Post ONE message with the single word DONE. $P4_RULES"
  S="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg g "$GOAL" --arg a "$R" --arg l "$LEAD" --arg rt "$RUNTIME_ID" \
    '{title:"실기 역할 게이트",goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a},{agent_id:$l}],assignee_agent_id:$a,runtime_id:$rt,
      completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
  T_R="$(session_initial_task "$S")"
  wait_step R1 "reviewer 턴이 끝났다" "$T_TURN" '[ -n "$(psqlq "select outcome from task_attempt where task_id='"'"'$T_R'"'"' and attempt=1 and outcome is not null")" ]' 2 || true
  chk R2 "attempt 1 completed" completed "$(psqlq "select coalesce(outcome,'-') from task_attempt where task_id='$T_R' and attempt=1")"
  chk R3 "데몬 로그: allowed commands (reviewer 10 · denied 3)" 1 "$(grep -c "allowed commands: $REVIEWER_ALL (denied: lane_delegate,artifact_submit,hitl_approve_request)" "$DLOG" || true)"
  TOOLS="$(grep -o "colab tools registered: .*" "$DLOG" | tail -1 | sed 's/colab tools registered: //')"
  chk R4 "raw system/init 툴 목록에 colab_lane_delegate 없음 ($TOOLS)" no "$(in_csv colab_lane_delegate "$TOOLS")"
  psqlq "select content from message where session_id='$S' and author_type='agent' order by created_at" > "$OUT/82-real-messages.txt"
  chk R5 "에이전트가 인용한 [2] 줄 = 데몬이 쓴 거부 문장" yes "$(grep -qF "$DENY_LINE" "$OUT/82-real-messages.txt" && echo yes || echo no)"
  chk R6 "lane 행 0 — 위임이 일어나지 않았다" 0 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_R'")"
  chk R7 "서버 rejected 행 0 — 시도가 있었더라도 서버까지 오지 않았다(MCP·CLI 가 먼저)" 0 "$(rejected_rows "$T_R")"
  psqlq "select class::text||'/'||coalesce(verb,'-')||'/'||coalesce(outcome,'-')||' '||coalesce(payload->>'detail', payload->>'title', payload->>'command', '-') from task_event where task_id='$T_R' order by seq" > "$OUT/82-real-feed.txt"
  ATTEMPT="$(grep -c -i "delegate" "$OUT/82-real-feed.txt" || true)"
  log "실기 피드에서 delegate 를 언급한 카드: $ATTEMPT 건 (시도했다면 exit 3/command_not_allowed 흔적이 여기·메시지에 있다) — out/82-real-feed.txt · out/82-real-messages.txt"
  chk R8 "에이전트가 무엇이 일어났는지 보고했다(메시지 ≥ 2)" yes "$( [ "$(grep -c . "$OUT/82-real-messages.txt")" -ge 2 ] && echo yes || echo no )"
  cp "$DLOG" "$OUT/82-real-daemon.log" 2>/dev/null || true
  printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
  [ "$fail" = 0 ]; exit $?
fi

# ═══════════════════════════════ 페이크 절 ═══════════════════════════════
step "0. 탭(:$TAP_PORT) · 페이크 런타임 · 계정(Director·멤버) · 워크스페이스 · 페어링(capacity 3)"
rm -f "$TAP" "$ACCESS"; : > "$TAP"; : > "$DLOG"; rm -rf "$WORK"; rm -f "$REC"/gate-* "$REC"/Lead.jsonl "$REC"/R.jsonl "$REC"/RH.jsonl "$REC"/C.jsonl "$REC"/Asker.jsonl "$REC"/Idle.jsonl
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
signup "i6g+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "V11 Role Gate $STAMP")"
INV="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}' | jq -r .token)"
COOKIE="$MEM_COOKIE"
api_ok POST /auth/signup "$(jq -nc --arg e "i6g-mem+$STAMP@example.com" --arg t "$INV" '{display_name:"멤버",email:$e,password:"password123",invite_token:$t}')" >/dev/null
COOKIE="$DIR_COOKIE"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 3
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-82.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"
KINDS="$(runtime_kinds "$RUNTIME_ID")"
chk G0 "런타임이 claude_code·hermes 를 광고한다 (kinds=$KINDS)" "yes|yes" "$(in_set claude_code $KINDS)|$(in_set hermes $KINDS)"

step "1. 에이전트 — Lead(lead) · R(reviewer, claude_code) · RH(reviewer, hermes) · C(custom) · W(writer, Asker) · Idle(빈 대본)"
INS="짧게, 한국어로 답한다. 저장소나 다른 디렉토리를 뒤지지 마라."
# gate_env NAME KIND TO → Gate 대본의 프로파일 env. 기록 파일은 에이전트 이름별(fake_env 는 역할 이름으로 쓴다).
gate_env() { fake_env Gate "$2" | jq -c --arg to "$3" --arg rec "$REC/$1.jsonl" '. + {FAKE_DELEGATE_TO:$to, ACPFAKE_RECORD:$rec}'; }
LEAD="$(PROFILE_ENV="$(gate_env Lead claude Asker)" create_agent_kind "$WS" Lead lead claude_code "$MODEL" "$INS" '팀을 이끈다')"
R="$(PROFILE_ENV="$(gate_env R claude Lead)" create_agent_kind "$WS" R reviewer claude_code "$MODEL" "$INS" '검토한다')"
RH="$(PROFILE_ENV="$(gate_env RH hermes Lead)" create_agent_kind "$WS" RH reviewer hermes "$MODEL" "$INS" '검토한다(hermes)')"
C="$(PROFILE_ENV="$(gate_env C claude Idle)" create_agent_kind "$WS" C custom claude_code "$MODEL" "$INS" '무엇이든')"
# Asker(writer): hitl ask 하나 열고 턴 종료 → lane waiting_human (I-2). Idle(researcher): 대본 없는 턴(steps []) → 빈 턴.
W="$(create_agent_fake "$WS" Asker writer claude_code "$MODEL" "$INS" '질문한다')"; W_NAME=Asker
IDLE="$(create_agent_fake "$WS" Idle researcher claude_code "$MODEL" "$INS" '아무것도 안 한다' '{"turns":[{"steps":[]}]}')"
chk G1 "Agent.allowed_commands — lead·custom 13 · reviewer 10 (§2.5)" "13/13/10/10" \
  "$(for a in "$LEAD" "$C" "$R" "$RH"; do api_ok GET "/agents/$a" | jq -r '.allowed_commands|length'; done | paste -sd/ -)"
# 상태별 lane.actions 를 위해 R 이 위임할 상대는 Lead 가 아니라 실제로는 거부되니 상관없다. C 는 Idle 로.
S="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg rt "$RUNTIME_ID" --arg l "$LEAD" --arg r "$R" --arg rh "$RH" --arg c "$C" --arg w "$W" --arg i "$IDLE" \
  '{title:"역할 게이트",goal:"저장소 밖에서 짧은 인사말 한 줄을 쓴다",isolation:{kind:"none"},
    participants:[{agent_id:$l},{agent_id:$r},{agent_id:$rh},{agent_id:$c},{agent_id:$w},{agent_id:$i}],assignee_agent_id:$l,runtime_id:$rt,
    limits:{max_parallel_lanes:3},
    completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
# limits.max_parallel_lanes=3: (f) 의 queued 는 **서버의 lane 상한**으로 만든다(claim SQL 의 lane_cap). 데몬 capacity 로는 못 만든다 —
# 데몬은 claim 응답의 task 를 `running` 에 등록하기 전에 다음 claim 의 free 를 다시 세서 capacity 를 넘긴다(1차 실행 실측: capacity 3 에
# R·RH·C 가 도는 채로 Idle 이 claim 됐다 — 결함 보고, plan/V11_REPORT.md).
echo "$WS $S $LEAD $R $RH $C $W $IDLE $RUNTIME_ID i6g+$STAMP@example.com" > "$OUT/82-ids.txt"
ok "session $S"

step "2. Lead(lead) — 세 층 통과: 대본 delegate(W) exit 0 · MCP argv --allow 13 · 토큰 POST /lanes(Idle) 201 · lane.actions running/waiting_human"
T_LEAD="$(session_initial_task "$S")"
wait_gate L0 "Lead 대본이 delegate 를 시도하고 토큰을 남겼다" "$T_LEAD" || true
gate_json "$T_LEAD" > "$OUT/82-gate-lead.json"
chk L1 "대본 colab lane delegate → exit 0" 0 "$(jq -r .exit "$OUT/82-gate-lead.json")"
chk L2 "lane 1 생성(W, delegated_from=Lead task)" 1 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_LEAD'")"
chk L3 "번들 task.allowed_commands(lead) = 13 전부 (claim 탭)" "$LEAD_ALL" "$(jq -r --arg t "$T_LEAD" 'select(.path|endswith("/claim"))|.body.tasks[]|select(.task.id==$t)|.task.allowed_commands|join(",")' "$TAP" | head -1)"
MCP_ARGS="$(record_field Lead session/new '.params.mcpServers[]|select(.name=="colab")|.args')"; MCP_ENV="$(record_field Lead session/new '.params.mcpServers[]|select(.name=="colab")|.env')"
chk L4 "(a) session/new 의 colab MCP argv = mcp serve --allow <13> (acpfake record)" "mcp serve --allow $LEAD_ALL" "$(jq -r 'join(" ")' <<<"$MCP_ARGS")"
TOOLS="$(mcp_tools "$MCP_ARGS" "$MCP_ENV")"; printf '%s\n' "$TOOLS" > "$OUT/82-mcp-lead.out"
chk L5 "(a) 그 argv 로 띄운 진짜 colab MCP: tools/list 13 · colab_lane_delegate 있음" "13/yes" "$(csv_len "$TOOLS")/$(in_csv colab_lane_delegate "$TOOLS")"
TT_LEAD="$(cat "$REC/gate-$T_LEAD.token")"
R_="$(tok_api "$TT_LEAD" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$IDLE" '{agent_id:$a,brief:"아무것도 하지 마세요"}')")"
chk L6 "(c) lead 토큰 curl POST /lanes(Idle) → 201" 201 "$(api_code <<<"$R_")"
chk L7 "lead 의 rejected 행 0" 0 "$(rejected_rows "$T_LEAD")"
# W(Asker) 가 hitl ask → waiting_human 이 될 때까지 (I-2)
wait_step L8 "W lane 이 waiting_human (Asker 의 hitl ask)" "$T_TURN" '[ "$(psqlq "select status::text from lane l join agent a on a.id=l.agent_id where l.session_id='"'"'$S'"'"' and a.name='"'"'$W_NAME'"'"' order by l.created_at limit 1")" = waiting_human ]' 0.5 || true
lanes_dump running-lead
chk L9 "(f) Director 가 보는 lane.actions — Lead running → restart,cancel" "running:restart,cancel" "$(lane_actions running-lead Lead)"
chk L10 "(f) W waiting_human → respond_hitl" "waiting_human:respond_hitl" "$(lane_actions running-lead "$W_NAME")"
COOKIE="$MEM_COOKIE"; lanes_dump running-lead-member; COOKIE="$DIR_COOKIE"
chk L11 "(f) 멤버(비제어자)가 보는 Lead running → [] (director·deputy 만 restart/cancel)" "running:" "$(lane_actions running-lead-member Lead)"
gate_go "$T_LEAD"
wait_step L12 "Lead 턴 종료(completed) · Idle 빈 턴 종료" "$T_TURN" '[ "$(task_field "'"$T_LEAD"'" status)" = completed ] && [ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'"'$S'"'"' and a.name='"'"'Idle'"'"' and t.status='"'"'completed'"'"'")" -ge 1 ]' 0.5 || true
lanes_dump done-lead
chk L13 "(f) Lead done → []" "done:" "$(lane_actions done-lead Lead)"
chk L14 "(e) Idle 의 빈 턴 카드 1행(status/turn_end/empty_turn/info)" 1 "$(empty_cards "$S")"

step "3. R(reviewer, claude_code — MCP 표면) — (a) argv --allow 10 · tools/list 에 delegate 없음 · (b) 대본 delegate exit 3, 선에 POST /lanes 0 · (c) 토큰 curl 403 + rejected 행"
A0="$(access_api_lines)"; X0="$(access_count $'GET\t/api/v1/cli/context')"; N0="$(access_count $'POST\t/api/v1/sessions/'"$S"$'/lanes')"
post_message "$S" "$(mention R "$R") 검토해 주세요" >/dev/null
T_R="$(task_of "$S" R)"; [ -n "$T_R" ] || { sleep 2; T_R="$(task_of "$S" R)"; }
wait_gate R0 "R 대본이 delegate 를 시도하고 토큰을 남겼다" "$T_R" || true
gate_json "$T_R" > "$OUT/82-gate-r.json"
chk R1 "(b) 런타임 안의 colab lane delegate → exit 3" 3 "$(jq -r .exit "$OUT/82-gate-r.json")"
chk R2 "(b) error.code/role/command" "command_not_allowed/reviewer/lane_delegate" "$(jq -r '.out.error|.code+"/"+.role+"/"+.command' "$OUT/82-gate-r.json")"
chk R3 "(b) 문장 = 서버 §2.5 문장" "이 역할(reviewer)은 lane delegate 를 쓸 수 없습니다" "$(jq -r '.out.error.detail' "$OUT/82-gate-r.json")"
chk R4 "(b) 선: R 턴 동안 /api/v1 요청 = GET /cli/context 1 · POST /lanes 0 (컨텍스트 모드)" "1/1/0" \
  "$(( $(access_api_lines) - A0 ))/$(( $(access_count $'GET\t/api/v1/cli/context') - X0 ))/$(( $(access_count $'POST\t/api/v1/sessions/'"$S"$'/lanes') - N0 ))"
chk R5 "lane 행 0 (delegated_from=R task)" 0 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_R'")"
chk R6 "(b) 서버 rejected 행 0 — CLI 가 먼저 막았다" 0 "$(rejected_rows "$T_R")"
chk R7 "번들 task.allowed_commands(reviewer) = 10 (claim 탭)" "$REVIEWER_ALL" "$(jq -r --arg t "$T_R" 'select(.path|endswith("/claim"))|.body.tasks[]|select(.task.id==$t)|.task.allowed_commands|join(",")' "$TAP" | head -1)"
MCP_ARGS="$(record_field R session/new '.params.mcpServers[]|select(.name=="colab")|.args')"; MCP_ENV="$(record_field R session/new '.params.mcpServers[]|select(.name=="colab")|.env')"
chk R8 "(a) session/new 의 colab MCP argv = mcp serve --allow <reviewer 10>" "mcp serve --allow $REVIEWER_ALL" "$(jq -r 'join(" ")' <<<"$MCP_ARGS")"
TOOLS="$(mcp_tools "$MCP_ARGS" "$MCP_ENV")"; printf '%s\n' "$TOOLS" > "$OUT/82-mcp-r.out"
chk R9 "(a) 그 argv 로 띄운 진짜 colab MCP: tools/list 10 · delegate·submit·approve-request 없음" "10/0" \
  "$(csv_len "$TOOLS")/$(tr ',' '\n' <<<"$TOOLS" | grep -c -e '^colab_lane_delegate$' -e '^colab_artifact_submit$' -e '^colab_hitl_approve_request$' || true)"
BRIEF="$(record_field R session/new '.params._meta.systemPrompt.append // ""' | jq -r .)"; printf '%s\n' "$BRIEF" > "$OUT/82-brief-r.txt"
chk R10 "브리프 [2] 에 거부 줄이 있고 colab lane delegate 를 이름하지 않는다" "yes/0" "$(grep -qF "$DENY_LINE" "$OUT/82-brief-r.txt" && echo yes || echo no)/$(grep -c 'colab lane delegate' "$OUT/82-brief-r.txt" || true)"
TT_R="$(cat "$REC/gate-$T_R.token")"
R_="$(tok_api "$TT_R" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$LEAD" '{agent_id:$a,brief:"우회"}')")"
api_body <<<"$R_" | jq . > "$OUT/82-403-r.json"
chk R11 "(c) reviewer 토큰 curl 직접 POST /lanes → 403 command_not_allowed" "403/command_not_allowed" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)"
chk R12 "(c) 서버 403 문장 == CLI exit 3 문장 (글자 단위)" "$(api_body <<<"$R_" | jq -r .detail)" "$(jq -r '.out.error.detail' "$OUT/82-gate-r.json")"
chk R13 "(c) task_event status rejected 행 1 (rejected_reason=command_not_allowed, 우회분만)" 1 "$(rejected_rows "$T_R")"
chk R14 "(c) 우회도 lane 을 만들지 않았다" 0 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_R'")"

step "4. RH(reviewer, hermes — 래퍼 표면) — (b) 래퍼 env 로 exit 3, 선에 0 줄 · (c) 토큰 curl 403"
A0="$(access_api_lines)"
post_message "$S" "$(mention RH "$RH") 검토해 주세요" >/dev/null
T_RH="$(task_of "$S" RH)"; [ -n "$T_RH" ] || { sleep 2; T_RH="$(task_of "$S" RH)"; }
wait_gate H0 "RH 대본이 delegate 를 시도하고 토큰을 남겼다" "$T_RH" || true
gate_json "$T_RH" > "$OUT/82-gate-rh.json"
chk H1 "대본이 프롬프트가 이름한 래퍼(§10 절대 경로)로 불렀다" yes "$(jq -r .cli "$OUT/82-gate-rh.json" | grep -q '/\.colab/bin/' && echo yes || echo no)"
chk H2 "(b) 래퍼 lane delegate → exit 3 command_not_allowed" "3/command_not_allowed" "$(jq -r '(.exit|tostring)+"/"+.out.error.code' "$OUT/82-gate-rh.json")"
chk H3 "(b) env 모드: role \"\" · 문장은 괄호 생략 (84_ B.3)" "/이 역할은 lane delegate 를 쓸 수 없습니다" "$(jq -r '.out.error|.role+"/"+.detail' "$OUT/82-gate-rh.json")"
chk H4 "(b) error.allowed = 래퍼가 export 한 COLAB_ALLOWED_COMMANDS(reviewer 10)" "$REVIEWER_ALL" "$(jq -r '.out.error.allowed|join(",")' "$OUT/82-gate-rh.json")"
chk H5 "(b) 선: RH 턴 동안 /api/v1 요청 0 줄 — 컨텍스트조차 부르지 않았다" 0 "$(( $(access_api_lines) - A0 ))"
chk H6 "서버 rejected 행 0 · lane 행 0" "0/0" "$(rejected_rows "$T_RH")/$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_RH'")"
# hermes 의 브리프는 파일이다(brief_transport=file): 프롬프트 첫 줄이 workdir 의 COLAB_BRIEF.md 를 가리킨다. RH 가 붙들고 있는 동안 읽는다.
PROMPT_RH="$(jq -c 'select(.method=="session/prompt")' "$REC/RH.jsonl" | head -1 | jq -r '[.params.prompt[]?|.text // empty]|join("\n")')"; printf '%s\n' "$PROMPT_RH" > "$OUT/82-prompt-rh.txt"
BRIEF_FILE="$(grep -o '/[^ ]*/COLAB_BRIEF\.md' "$OUT/82-prompt-rh.txt" | head -1)"; cp "${BRIEF_FILE:-/dev/null}" "$OUT/82-brief-rh.txt" 2>/dev/null || : > "$OUT/82-brief-rh.txt"
chk H7 "hermes 브리프 파일(COLAB_BRIEF.md)에 거부 줄 · 래퍼 절대 경로 · colab lane delegate 없음" "yes/yes/0" \
  "$(grep -qF "$DENY_LINE" "$OUT/82-brief-rh.txt" && echo yes || echo no)/$(grep -q '/\.colab/bin/' "$OUT/82-brief-rh.txt" && echo yes || echo no)/$(grep -c 'colab lane delegate' "$OUT/82-brief-rh.txt" || true)"
TT_RH="$(cat "$REC/gate-$T_RH.token")"
R_="$(tok_api "$TT_RH" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$LEAD" '{agent_id:$a,brief:"우회"}')")"
chk H8 "(c) RH 토큰 curl 직접 POST /lanes → 403 command_not_allowed · rejected 행 1" "403/command_not_allowed/1" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)/$(rejected_rows "$T_RH")"

step "5. C(custom) — 셋 다 통과 · capacity 3 이 찼으니 위임된 Idle 은 queued → (f) queued → cancel"
post_message "$S" "$(mention C "$C") 도와주세요" >/dev/null
T_C="$(task_of "$S" C)"; [ -n "$T_C" ] || { sleep 2; T_C="$(task_of "$S" C)"; }
wait_gate C0 "C 대본이 delegate(Idle) 를 시도하고 토큰을 남겼다" "$T_C" || true
gate_json "$T_C" > "$OUT/82-gate-c.json"
chk C1 "(b) custom colab lane delegate → exit 0" 0 "$(jq -r .exit "$OUT/82-gate-c.json")"
MCP_ARGS="$(record_field C session/new '.params.mcpServers[]|select(.name=="colab")|.args')"; MCP_ENV="$(record_field C session/new '.params.mcpServers[]|select(.name=="colab")|.env')"
chk C2 "(a) custom MCP argv --allow 13 · tools/list 13" "mcp serve --allow $LEAD_ALL/13" "$(jq -r 'join(" ")' <<<"$MCP_ARGS")/$(csv_len "$(mcp_tools "$MCP_ARGS" "$MCP_ENV")")"
TT_C="$(cat "$REC/gate-$T_C.token")"
chk C3 "(c) custom 토큰 curl POST /lanes(Idle) → 201 · rejected 행 0" "201/0" "$(tok_api "$TT_C" POST "/sessions/$S/lanes" "$(jq -nc --arg a "$IDLE" '{agent_id:$a,brief:"또"}')" | api_code)/$(rejected_rows "$T_C")"
chk C4 "(f) 세션 lane 상한 3 이 R·RH·C 로 찼다 — Idle 의 새 task 2(대본 위임·토큰 위임) 는 queued" 2 "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Idle' and t.status='queued'")"
lanes_dump queued
chk C5 "(f) Director 가 보는 Idle queued → cancel" "queued:cancel" "$(lane_actions queued Idle queued)"
chk C6 "(f) R running → restart,cancel (reviewer 도 lane 규칙은 같다)" "running:restart,cancel" "$(lane_actions queued R)"

step "6. (d) 사람(쿠키) 경로는 비게이트 — Director·멤버 (NN4): 명령 표(command_not_allowed)에 걸리지 않는다. 사람의 권한 규칙(agent_only 등)은 다른 층이다"
# 메시지는 /note 로 — 규칙 1(저장만, 라우팅 없음). 보통 문장은 규칙 6 으로 assignee(Lead)를 깨워 Lead 대본이 또 위임한다(1차 실행 실측).
chk D1 "Director POST /messages(/note) → 201" 201 "$(api POST "/sessions/$S/messages" '{"content":"/note 사람이 씁니다"}' -H "Idempotency-Key: $(uuid)" | api_code)"
D_LANE="$(api POST "/sessions/$S/lanes" "$(jq -nc --arg a "$IDLE" '{agent_id:$a,brief:"사람이 만든 lane"}')")"
chk D2 "Director POST /lanes → 403 agent_only (사람은 「새 작업 줄기로 보내기」) — command_not_allowed 가 아니다" "403/agent_only" "$(api_code <<<"$D_LANE")/$(api_body <<<"$D_LANE" | jq -r '.code // "-"')"
D_DEC="$(api POST "/sessions/$S/decisions" '{"summary":"사람의 결정"}' -H "Idempotency-Key: $(uuid)")"
chk D3 "Director POST /decisions → 403 agent_only — command_not_allowed 가 아니다" "403/agent_only" "$(api_code <<<"$D_DEC")/$(api_body <<<"$D_DEC" | jq -r '.code // "-"')"
chk D3b "Director GET /sessions/{S} 200 · listLanes 200 · listMessages 200" "200/200/200" "$(api GET "/sessions/$S" | api_code)/$(api GET "/sessions/$S/lanes" | api_code)/$(api GET "/sessions/$S/messages" | api_code)"
COOKIE="$MEM_COOKIE"
chk D4 "멤버 POST /messages(/note) → 201 · GET /sessions/{S} 200" "201/200" "$(api POST "/sessions/$S/messages" '{"content":"/note 멤버가 씁니다"}' -H "Idempotency-Key: $(uuid)" | api_code)/$(api GET "/sessions/$S" | api_code)"
M_LANE="$(api POST "/sessions/$S/lanes" "$(jq -nc --arg a "$IDLE" '{agent_id:$a,brief:"멤버의 lane"}')")"
chk D5 "멤버 POST /lanes 의 코드가 command_not_allowed 가 아니다 (HTTP $(api_code <<<"$M_LANE") $(api_body <<<"$M_LANE" | jq -r '.code // "-"'))" no "$(api_body <<<"$M_LANE" | jq -r '.code // "-"' | grep -q command_not_allowed && echo yes || echo no)"
COOKIE="$DIR_COOKIE"
chk D6 "세션 전체 rejected 행 = 2 (R·RH 의 curl 우회분뿐, 사람 경로 0)" 2 "$(psqlq "select count(*) from task_event e join task t on t.id=e.task_id where t.session_id='$S' and e.class='status' and e.outcome='rejected' and e.payload->>'rejected_reason'='command_not_allowed'")"

step "7. 붙든 턴을 놓는다 → 조용해질 때까지 → 빈 턴 · 관찰 표 (e)"
gate_go "$T_R"; gate_go "$T_RH"; gate_go "$T_C"
active_n() { psqlq "select count(*) from task where session_id='$S' and status in ('queued','dispatched','preparing','running')"; }
wait_step Q0 "세션이 조용하다(queued·running 0 이 2초 유지; Asker 는 waiting_human)" "$T_TURN" '[ "$(active_n)" = 0 ] && sleep 2 && [ "$(active_n)" = 0 ]' 1 || true
lanes_dump final
chk Q1 "(f) R·RH·C done → []" "done:/done:/done:" "$(lane_actions final R)/$(lane_actions final RH)/$(lane_actions final C)"
IDLE_N="$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Idle' and t.status='completed'")"
DONE_N="$(psqlq "select count(*) from task_attempt ta join task t on t.id=ta.task_id where t.session_id='$S' and ta.outcome='completed'")"
chk Q2 "(e) Idle 의 완료 턴 3(Lead 토큰·C 대본·C 토큰) 마다 빈 턴 카드 정확히 1행" "3/$IDLE_N" "$IDLE_N/$(empty_cards "$S")"
chk Q3 "(e) 게이트 턴(Lead·R·RH·C)에는 빈 턴 카드 없음 — message post 를 했다" 0 "$(psqlq "select count(*) from task_event e where e.task_id in ('$T_LEAD','$T_R','$T_RH','$T_C') and e.class='status' and e.verb='turn_end' and e.object_ref=to_jsonb('empty_turn'::text)")"
api_ok GET "/workspaces/$WS/observations" > "$OUT/82-obs.json"
chk O1 "관찰 5행 §11 순서" "chain_scale,chain_depth,join_breadth,routing_concentration,empty_turn_rate" "$(jq -r '[.rows[].key]|join(",")' "$OUT/82-obs.json")"
chk O2 "5행 모두 n ≥ 1" 5 "$(jq -r '[.rows[]|select(.n>=1)]|length' "$OUT/82-obs.json")"
chk O3 "empty_turn_rate n = 완료 attempt 수 · value = 빈 턴/완료 (DB 와 같은 수)" "$DONE_N/$(awk -v a="$IDLE_N" -v b="$DONE_N" 'BEGIN{printf "%.4f", a/b}')" \
  "$(jq -r '.rows[]|select(.key=="empty_turn_rate")|(.n|tostring)+"/"+((.value*10000|round)/10000|tostring)' "$OUT/82-obs.json" | awk -F/ '{printf "%s/%.4f", $1, $2}')"
chk O4 "routing_concentration breakdown 9종 · 규칙 2(멘션·위임) n ≥ 4" "9/yes" "$(jq -r '.rows[]|select(.key=="routing_concentration")|(.breakdown|length|tostring)+"/"+(if ([.breakdown[]|select(.kind=="2")|.n]|add) >= 4 then "yes" else "no" end)' "$OUT/82-obs.json")"
chk O5 "join_breadth n ≥ 2 (Lead·C 의 위임 그룹) · chain_depth n = 1(세션)" "true/1" "$(jq -r '((.rows[]|select(.key=="join_breadth")|.n)>=2|tostring)+"/"+(.rows[]|select(.key=="chain_depth")|.n|tostring)' "$OUT/82-obs.json")"

step "8. (e) 대시보드 관찰 표 · S7 빈 턴 카드 — headless (78_ 방식)"
EMAIL="i6g+$STAMP@example.com"
chk W0 "웹 /settings?tab=dashboard 200 (앱 셸)" 200 "$(curl -sS -o "$OUT/82-dash.html" -w '%{http_code}' -b "$COOKIE" "$WEB_URL/settings?tab=dashboard")"
chk W1 "/api/v1 프록시로 관찰 표 5행" 5 "$(curl -sS -b "$COOKIE" "$WEB_URL/api/v1/workspaces/$WS/observations" | jq -r '.rows|length')"
if command -v agent-browser >/dev/null 2>&1 && [ "${WITH_BROWSER:-1}" = 1 ]; then
  export AGENT_BROWSER_SESSION="colab-p5-82-$$"
  web_login "$EMAIL" password123
  ab open "$WEB_URL/settings?tab=dashboard" >/dev/null
  abwait '[data-testid="observations-table"]' 30 || true
  chk W2 "S14 관찰 표 observation-row 5" 5 "$(abcount '[data-testid="observation-row"]')"
  chk W2b "empty_turn_rate 행이 측정 가능(data-measurable=true)" 1 "$(abcount '[data-testid="observation-row"][data-key="empty_turn_rate"][data-measurable="true"]')"
  mkdir -p "$E2E_ROOT/web/__screenshots__"
  ab eval 'document.querySelector("[data-testid=observations-wrap]")?.scrollIntoView()' >/dev/null 2>&1 || true; sleep 1
  ab screenshot "$E2E_ROOT/web/__screenshots__/p5-82-s14-observations.png" >/dev/null 2>&1 && ok "📸 web/__screenshots__/p5-82-s14-observations.png" || true
  # S7: 빈 턴 문장은 이력 「활동」 토글로만 닿는다(T-W16 Lead A) — Idle lane 의 이력 → 활동을 열면 피드 행과 카드 한 줄이 나온다.
  IDLE_LANE="$(psqlq "select l.id from lane l join agent a on a.id=l.agent_id where l.session_id='$S' and a.name='Idle' order by l.created_at limit 1")"
  ab open "$WEB_URL/sessions/$S" >/dev/null
  abwait '[data-testid="lane-board"]' 30 || true; sleep 1
  ab click "[data-lane-id=\"$IDLE_LANE\"] [data-testid=\"lane-tasks-toggle\"]" >/dev/null 2>&1 || true
  abwait "[data-lane-id=\"$IDLE_LANE\"] [data-testid=\"task-activity-toggle\"]" 10 || true
  ab click "[data-lane-id=\"$IDLE_LANE\"] [data-testid=\"task-activity-toggle\"]" >/dev/null 2>&1 || true
  abwait "[data-lane-id=\"$IDLE_LANE\"] [data-testid=\"feed-row-empty-turn\"]" 10 || true; sleep 1
  chk W3 "S7 Idle lane 이력의 활동에 빈 턴 행(feed-row-empty-turn) ≥ 1" yes "$( [ "$(abcount "[data-lane-id=\"$IDLE_LANE\"] [data-testid=\"feed-row-empty-turn\"]")" -ge 1 ] && echo yes || echo no )"
  chk W3b "그 lane 카드에 빈 턴 한 줄(lane-empty-turn)" yes "$( [ "$(abcount "[data-lane-id=\"$IDLE_LANE\"] [data-testid=\"lane-empty-turn\"]")" -ge 1 ] && echo yes || echo no )"
  ab screenshot "$E2E_ROOT/web/__screenshots__/p5-82-s7-empty-turn.png" >/dev/null 2>&1 && ok "📸 web/__screenshots__/p5-82-s7-empty-turn.png" || true
  ab close >/dev/null 2>&1 || true
else
  chk_na W2 "S14·S7 DOM 판정" skipped "agent-browser 없음(CI) — 앱 셸 200 + 프록시 5행까지만"
fi

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg s "$S" --argjson pass "$pass" --argjson fail "$fail" --argjson idle "$IDLE_N" --argjson done "$DONE_N" \
  '{workspace:$ws,session:$s,idle_turns:$idle,completed_attempts:$done,pass:$pass,fail:$fail}' | tee "$OUT/82.json"
[ "$fail" = 0 ]
