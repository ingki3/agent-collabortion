#!/usr/bin/env bash
# e2e/p5/81_cli_allowed_commands.sh — T-C7 실서버 스모크: v1.1 K-19 CLI 층 (colab-cli.md v0.6 §2.5·§3)
#   역할 밖 명령은 CLI 가 **서버에 보내기 전에** exit 3 `command_not_allowed` 로 거부한다 ·
#   `colab mcp serve --allow` 는 허용 툴만 등록한다 · `COLAB_ALLOWED_COMMANDS`(데몬 래퍼) 가 있으면 왕복 0.
# 서버 층(403·task_event rejected 행)은 81_observations_commands.sh(T-S19) 가 잰다. 여기서는 세 표면을 **실제
# colab 바이너리**로 돌린다: (1) 컨텍스트 모드(데몬 env 그대로, 목록은 getCliContext) (2) 래퍼 모드
# (harness §10 — `env -i` 로 위생화된 env 에서 COLAB_ALLOWED_COMMANDS 를 export 하는 래퍼) (3) MCP stdio.
#
# "서버 미호출" 은 **선(wire)** 에서 센다: 서버는 접근 로그가 없으므로 fixtures/tap_proxy.py 를 CLI 와 서버 사이에
# 두고(COLAB_SERVER_URL=탭) 요청 줄을 센다. DB(lane 행·task_event rejected 행)로 한 번 더 확인한다.
#
# 재는 것 (판정 표 out/81c-checks.tsv):
#   A. 컨텍스트 모드 — reviewer 토큰: `colab lane delegate` → exit 3 · code/role/command/allowed · 문장(서버와 같은 글자) ·
#      탭에 GET /cli/context 1줄뿐(POST /lanes 0) · lane 행 0 · 서버 rejected 행 0(서버 게이트까지 안 갔다) ·
#      `colab artifact submit`(없는 파일) → exit 3(파일을 읽기 전에 거부, 2 아님) · `colab review approve` → 서버 도달
#      (탭에 POST /artifacts/{A}/review) · `colab message post` → 201 · lead 토큰 `colab lane delegate` → 0 + lane 1.
#   B. 래퍼 모드 — 래퍼가 COLAB_ALLOWED_COMMANDS(번들 task.allowed_commands)를 export, `env -i`: delegate → exit 3 ·
#      탭 0줄(컨텍스트조차 없음) · role "" 문장 괄호 생략 · 허용 명령(session get)은 탭 1줄(컨텍스트 없이 바로).
#   C. MCP — `colab mcp serve --allow <reviewer 10>`: tools/list 10(delegate·submit·approve-request 없음) ·
#      colab_lane_delegate 호출 → isError command_not_allowed · 탭 0줄 · colab_session_get → 결과 · --allow 없이 13.
#   D. 서버 우회 방어 한 줄 — 같은 토큰으로 curl 직접 POST /lanes → 403 command_not_allowed(81_ D.8 재확인, "세 층").
#
# 스택(T-C7 배정, V11_TASKS §0): server :8119 · pg :5463 · 컨테이너 colab-pg-c7. 탭은 :8129(SERVER_URL 포트+10).
# 사용: SERVER_URL=http://localhost:8119 PG_PORT=5463 PG_CONTAINER=colab-pg-c7 bash e2e/p5/up.sh
#       bash e2e/p5/81_cli_allowed_commands.sh
#       SERVER_URL=http://localhost:8119 PG_PORT=5463 PG_CONTAINER=colab-pg-c7 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8119}"
export PG_PORT="${PG_PORT:-5463}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-c7}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/81c-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/81c-cookies-dir.txt"; rm -f "$COOKIE"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-C7 ports"

step "0. colab 바이너리(HEAD $(git rev-parse --short HEAD)) · 탭 프록시"
# COLAB_BIN=<path> 로 다른 바이너리(예: origin/dev 의 colab)를 끼워 before/after 를 대조할 수 있다 — 게이트 없는
# 바이너리는 A.5·A.7·B.2·C.1~C.3 이 FAIL 이어야 한다(서버 403 이 exit 3 을 대신 내므로 A.1 만으로는 못 가른다).
COLAB="${COLAB_BIN:-$BIN/colab-c7}"
[ -n "${COLAB_BIN:-}" ] || (cd cli && go build -o "$COLAB" ./cmd/colab) || die "colab build"
TAP_PORT=$(( ${SERVER_URL##*:} + 10 )); TAP_URL="http://127.0.0.1:$TAP_PORT"; TAPLOG="$OUT/81c-tap.log"; : > "$TAPLOG"
setsid_run "$OUT/81c-tap.out" python3 "$(dirname "$0")/fixtures/tap_proxy.py" "$TAP_PORT" "$SERVER_URL" "$TAPLOG" > "$OUT/81c-tap.pid"
trap 'kill "$(cat "$OUT/81c-tap.pid")" 2>/dev/null' EXIT
for i in $(seq 1 40); do curl -fsS "$TAP_URL/healthz" >/dev/null 2>&1 && break; sleep 0.25; done
curl -fsS "$TAP_URL/healthz" >/dev/null || die "tap proxy did not start (see $OUT/81c-tap.out)"
ok "colab $("$COLAB" --version) · tap $TAP_URL → $SERVER_URL"
tap_reset() { : > "$TAPLOG"; }
tap_lines() { grep -c . "$TAPLOG" || true; }
tap_count() { grep -c -- "$1" "$TAPLOG" || true; }   # tap_count 'POST /api/v1/sessions/.../lanes'

claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" --arg r "$2" '{name:$n,role:$r,role_description:"d",
  instructions:"짧게, 한국어로 답한다. 저장소나 다른 디렉토리를 뒤지지 마라.",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
mention() { api_ok POST "/sessions/$1/messages" "$(jq -nc --arg a "$2" --arg n "$3" --arg t "$4" '{content:("[@"+$n+"](mention://agent/"+$a+") "+$t)}')" -H "Idempotency-Key: $(uuid)" >/dev/null; }
run_turn() { # SESSION AGENT → task_id<TAB>task_token<TAB>lane_id<TAB>allowed_commands(csv)
  local s="$1" a="$2" cl b tid tok lid ac
  cl="$(claim)"
  b="$(jq -c --arg s "$s" --arg a "$a" '.tasks[]|select(.task.session_id==$s and .task.agent_id==$a)' <<<"$cl")"
  [ -n "$b" ] || die "claim 에 세션 $s / 에이전트 $a 의 task 가 없다: $cl"
  tid="$(jq -r .task.id <<<"$b")"; tok="$(jq -r .task_token <<<"$b")"; lid="$(jq -r .task.lane_id <<<"$b")"
  ac="$(jq -r '(.task.allowed_commands // [])|join(",")' <<<"$b")"
  daemon_api "tasks/$tid/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null
  printf '%s\t%s\t%s\t%s' "$tid" "$tok" "$lid" "$ac"
}
finish_turn() { daemon_api "tasks/$1/attempts/1/finish" '{"outcome":"completed","stop_reason":"end_turn","transport":"acp","last_seq":0,
  "usage":{"input_tokens":2000,"output_tokens":500,"estimated":false,"model":"claude-sonnet-5"}}' >/dev/null; }
refused_rows() { psqlq "select count(*) from task_event where task_id='$1' and class='status' and outcome='rejected' and payload->>'rejected_reason'='command_not_allowed'"; }
# cli TOKEN TASK LANE NAME ARGS… → stdout JSON, 마지막 줄 exit 코드. 데몬이 주는 env 그대로(COLAB_ALLOWED_COMMANDS 없음).
cli() {
  local tok="$1" tid="$2" lid="$3" name="$4"; shift 4
  env -i PATH="$PATH" HOME="$HOME" COLAB_TASK_TOKEN="$tok" COLAB_SERVER_URL="$TAP_URL" COLAB_TASK_ID="$tid" COLAB_TASK_ATTEMPT=1 \
    COLAB_LANE_ID="$lid" COLAB_SESSION_ID="$S" COLAB_AGENT_NAME="$name" COLAB_STATE_DIR="$OUT/81c-state" "$COLAB" "$@" 2>>"$OUT/81c-cli.err"
  printf '\n%s' "$?"
}
REVIEWER_ALL="session_get,session_messages,message_post,status_set,decision_record,artifact_get,review_approve,review_reject,hitl_ask,hitl_request_info"

step "1. 계정 · 워크스페이스 · Lead(lead)·R(reviewer) · 페어링 · 세션"
signup "c7-dir-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "C7 $RUN")"
LEAD="$(mk_agent Lead lead)"; R="$(mk_agent R reviewer)"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-c7" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-c7-$RUN")"
export DTOK RID
S="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg rt "$RID" --arg l "$LEAD" --arg r "$R" \
  '{title:"명령 게이트",goal:"저장소 밖에서 짧은 인사말 한 줄을 쓴다",isolation:{kind:"none"},participants:[{agent_id:$l},{agent_id:$r}],assignee_agent_id:$l,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"agent_approval",agent_id:$r}]}}')" | jq -r .id)"
# Lead 턴: 아티팩트 하나 제출(리뷰 대상) — CLI 로. 그리고 Lead 의 delegate 는 0 이어야 한다(A.10).
mention "$S" "$LEAD" Lead "초안을 올려 주세요"
IFS=$'\t' read -r T_L TT_L L_L AC_L <<<"$(run_turn "$S" "$LEAD")"
printf '# 초안\n안녕하세요\n' > "$OUT/81c-draft.md"
tap_reset
OUT_L="$(cli "$TT_L" "$T_L" "$L_L" Lead artifact submit --type doc --file "$OUT/81c-draft.md" --name draft.md)"
chk 1.1 0 "$(api_code <<<"$OUT_L")" "Lead(lead) 의 colab artifact submit → exit 0"
ART="$(api_body <<<"$OUT_L" | jq -r '.artifact_id // .artifact.id // empty')"
chk 1.2 1 "$(psqlq "select count(*) from artifact where id='${ART:-00000000-0000-0000-0000-000000000000}'")" "아티팩트 저장(id $ART)"
OUT_L="$(cli "$TT_L" "$T_L" "$L_L" Lead lane delegate --agent R --brief "초안을 검토해 주세요")"
chk 1.3 0 "$(api_code <<<"$OUT_L")" "lead 토큰 colab lane delegate → exit 0(표 안)"
chk 1.4 1 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_L'")" "lane 1 생성"
chk 1.5 2 "$(tap_count 'GET /api/v1/cli/context')" "선: CLI 프로세스 둘(제출·위임)이 게이트용 컨텍스트를 각 1회 = 2"
finish_turn "$T_L"

step "A. 컨텍스트 모드 — reviewer 토큰(데몬 env 그대로, 목록은 getCliContext.allowed_commands)"
IFS=$'\t' read -r T_R TT_R L_R AC_R <<<"$(run_turn "$S" "$R")"
chk A.0 "$REVIEWER_ALL" "$AC_R" "번들 task.allowed_commands(reviewer 10) — 래퍼 모드(B)가 export 할 값"
tap_reset
OUT_R="$(cli "$TT_R" "$T_R" "$L_R" R lane delegate --agent Lead --brief "다시 써 주세요")"
chk A.1 3 "$(api_code <<<"$OUT_R")" "reviewer 토큰 colab lane delegate → exit 3"
chk A.2 "command_not_allowed/reviewer/lane_delegate" "$(api_body <<<"$OUT_R" | jq -r '.error|.code+"/"+.role+"/"+.command')" "--json error.code·role·command"
chk A.3 "$REVIEWER_ALL" "$(api_body <<<"$OUT_R" | jq -r '.error.allowed|join(",")')" "error.allowed = 서버가 준 목록"
chk A.4 "이 역할(reviewer)은 lane delegate 를 쓸 수 없습니다" "$(api_body <<<"$OUT_R" | jq -r '.error.detail')" "문장 = 서버 403 문장(81_ D.9)과 같은 글자"
chk A.5 "1/0" "$(tap_lines)/$(tap_count 'POST /api/v1/sessions/'"$S"'/lanes')" "선: 요청 1줄(GET /cli/context) · POST /lanes 0"
chk A.6 0 "$(psqlq "select count(*) from lane where session_id='$S' and delegated_from_task_id='$T_R'")" "lane 행 0"
chk A.7 0 "$(refused_rows "$T_R")" "서버 rejected 행 0 — 서버 게이트까지 가지 않았다(CLI 가 먼저)"
tap_reset
OUT_R="$(cli "$TT_R" "$T_R" "$L_R" R artifact submit --type doc --file "$OUT/no-such-file.md")"
chk A.8 "3/command_not_allowed" "$(api_code <<<"$OUT_R")/$(api_body <<<"$OUT_R" | jq -r '.error.code')" "reviewer artifact submit(없는 파일) → exit 3 — 파일을 읽기 전에 거부(2 가 아니다)"
chk A.9 "1" "$(tap_lines)" "선: GET /cli/context 1줄뿐"
tap_reset
OUT_R="$(cli "$TT_R" "$T_R" "$L_R" R review approve --artifact "$ART" --note "좋습니다")"
chk A.10 0 "$(api_code <<<"$OUT_R")" "reviewer colab review approve → exit 0(지정 리뷰어)"
chk A.11 1 "$(tap_count 'POST /api/v1/artifacts/'"$ART"'/review -> 2')" "선: POST /artifacts/{A}/review 가 서버에 도달(2xx)"
chk A.12 "approve" "$(psqlq "select verdict::text from artifact_review where artifact_id='$ART'")" "artifact_review 행 approve"
tap_reset
OUT_R="$(cli "$TT_R" "$T_R" "$L_R" R message post --body "검토 끝")"
chk A.13 0 "$(api_code <<<"$OUT_R")" "reviewer colab message post → exit 0(표 안)"
chk A.14 1 "$(tap_count 'POST /api/v1/sessions/'"$S"'/messages -> 201')" "선: POST /messages 201"

step "B. 래퍼 모드 — harness §10 래퍼(COLAB_ALLOWED_COMMANDS export) 를 env -i 로"
WRAPDIR="$OUT/81c-work/.colab/bin/$T_R.1"; mkdir -p "$WRAPDIR"
cat > "$WRAPDIR/colab" <<WRAP
#!/bin/sh
export COLAB_TASK_TOKEN='$TT_R'
export COLAB_SERVER_URL='$TAP_URL'
export COLAB_TASK_ID='$T_R'
export COLAB_TASK_ATTEMPT='1'
export COLAB_LANE_ID='$L_R'
export COLAB_SESSION_ID='$S'
export COLAB_AGENT_NAME='R'
export COLAB_ALLOWED_COMMANDS='$AC_R'
export COLAB_STATE_DIR='$OUT/81c-state'
exec '$COLAB' "\$@"
WRAP
chmod 0700 "$WRAPDIR/colab"
tap_reset
OUT_W="$(env -i "$WRAPDIR/colab" lane delegate --agent Lead --brief "다시" 2>"$OUT/81c-wrap.err"; printf '\n%s' "$?")"
chk B.1 "3/command_not_allowed" "$(api_code <<<"$OUT_W")/$(api_body <<<"$OUT_W" | jq -r '.error.code')" "래퍼(env -i) lane delegate → exit 3"
chk B.2 0 "$(tap_lines)" "선: 요청 0줄 — 컨텍스트조차 부르지 않았다(목록은 env)"
chk B.3 "/이 역할은 lane delegate 를 쓸 수 없습니다" "$(api_body <<<"$OUT_W" | jq -r '.error|.role+"/"+.detail')" "env 모드는 role 을 모른다 → role \"\" · 문장은 괄호 생략(Lead 판정)"
chk B.4 "$REVIEWER_ALL" "$(api_body <<<"$OUT_W" | jq -r '.error.allowed|join(",")')" "error.allowed = env 목록"
chk B.5 1 "$(grep -c '이 역할은 lane delegate 를 쓸 수 없습니다' "$OUT/81c-wrap.err")" "stderr 에 사람 말 한 줄"
tap_reset
OUT_W="$(env -i "$WRAPDIR/colab" session get 2>>"$OUT/81c-wrap.err"; printf '\n%s' "$?")"
chk B.6 "0/1/0" "$(api_code <<<"$OUT_W")/$(tap_count 'GET /api/v1/sessions/'"$S"' -> 200')/$(tap_count 'GET /api/v1/cli/context')" "허용 명령(session get)은 바로 서버로: GET /sessions/{S} 1 · /cli/context 0"

step "C. MCP — colab mcp serve --allow <reviewer 10>"
mcp_in() { printf '%s\n' "$@"; }
tap_reset
mcp_in '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
       '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"colab_lane_delegate","arguments":{"agent":"Lead","brief":"b"}}}' \
       '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"colab_session_get","arguments":{}}}' \
  | env -i PATH="$PATH" HOME="$HOME" COLAB_TASK_TOKEN="$TT_R" COLAB_SERVER_URL="$TAP_URL" COLAB_TASK_ID="$T_R" COLAB_TASK_ATTEMPT=1 \
      COLAB_LANE_ID="$L_R" COLAB_SESSION_ID="$S" COLAB_AGENT_NAME=R COLAB_STATE_DIR="$OUT/81c-state" \
      "$COLAB" mcp serve --allow "$AC_R" > "$OUT/81c-mcp.out" 2>"$OUT/81c-mcp.err"
chk C.1 "10" "$(sed -n 1p "$OUT/81c-mcp.out" | jq -r '.result.tools|length')" "tools/list 10(reviewer 표)"
chk C.2 "0" "$(sed -n 1p "$OUT/81c-mcp.out" | jq -r '[.result.tools[].name|select(.=="colab_lane_delegate" or .=="colab_artifact_submit" or .=="colab_hitl_approve_request")]|length')" "delegate·submit·approve-request 툴 없음"
chk C.3 "true/command_not_allowed/lane_delegate" "$(sed -n 2p "$OUT/81c-mcp.out" | jq -r '(.result.isError|tostring)+"/"+.result.structuredContent.error.code+"/"+.result.structuredContent.error.command')" "잘린 툴 호출 → isError command_not_allowed(프로토콜 오류 아님)"
chk C.4 "명령 게이트" "$(sed -n 3p "$OUT/81c-mcp.out" | jq -r '.result.structuredContent.title')" "허용 툴(colab_session_get) → 결과"
chk C.5 "1/0" "$(tap_lines)/$(tap_count 'POST /api/v1/sessions/'"$S"'/lanes')" "선: GET /sessions/{S} 1줄뿐 · /lanes 0 (--allow 가 게이트 목록이라 /cli/context 도 0)"
mcp_in '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  | env -i PATH="$PATH" HOME="$HOME" COLAB_TASK_TOKEN="$TT_R" COLAB_SERVER_URL="$TAP_URL" COLAB_SESSION_ID="$S" "$COLAB" mcp serve > "$OUT/81c-mcp-all.out" 2>/dev/null
chk C.6 13 "$(sed -n 1p "$OUT/81c-mcp-all.out" | jq -r '.result.tools|length')" "--allow 없으면 13 전부"

step "D. 서버 우회 방어(세 층) — 같은 reviewer 토큰으로 curl 직접"
R_="$(curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $TT_R" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuid)" -X POST "$API/sessions/$S/lanes" --data "$(jq -nc --arg a "$LEAD" '{agent_id:$a,brief:"우회"}')")"
chk D.1 "403/command_not_allowed" "$(api_code <<<"$R_")/$(api_body <<<"$R_" | jq -r .code)" "curl 직접 POST /lanes → 서버 403 command_not_allowed"
CLI_DETAIL="$(cli "$TT_R" "$T_R" "$L_R" R lane delegate --agent Lead --brief x | api_body | jq -r '.error.detail')"
chk D.2 "$(api_body <<<"$R_" | jq -r .detail)" "$CLI_DETAIL" "서버 403 문장 == CLI exit 3 문장 (글자 단위)"
chk D.3 1 "$(refused_rows "$T_R")" "서버 rejected 행 1 = curl 우회분만(CLI 거부는 서버에 닿지 않았다)"
finish_turn "$T_R"

step "결과 — $CHECKS"
printf '  PASS %s · FAIL %s\n' "$(grep -c $'\tPASS\t' "$CHECKS")" "$FAILS" >&2
[ "$FAILS" = 0 ]
