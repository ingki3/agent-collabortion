#!/usr/bin/env bash
# e2e/p3/40_cli_hitl_smoke.sh — T-C4 스모크: HITL 3종을 **에이전트가 실제로 닿는 두 경로**로 한 번씩.
#
#   경로 1  Claude Code — `tool_surface: mcp`.   `colab mcp serve` stdio 에 tools/call.
#   경로 2  Hermes      — `tool_surface: cli_wrapper`. 데몬이 attempt 마다 만드는 래퍼
#           `<workdir_root>/.colab/bin/<task>.<attempt>/colab` 을 harness.md §10 그대로 만들고,
#           **위생화된 env**(`env -i`, PATH·COLAB_* 없음)에서 부른다. Hermes 도구가 도는 환경이 그렇다.
#
# 서버 T-S5(PR #124)가 아직 열려 있어 **목 서버**(mock_hitl_server.py, 계약 openapi createHitlRequest)로 돈다.
# 머지되면 MOCK 대신 실서버 URL·실토큰만 바꾸면 같은 스크립트가 그대로 돈다.
#
# 사용: bash e2e/p3/40_cli_hitl_smoke.sh
# 산출물: e2e/p3/out/{capture.jsonl,mcp.jsonl,*.txt}
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/e2e/p3/out"; rm -rf "$OUT"; mkdir -p "$OUT"
CAPTURE="$OUT/capture.jsonl"
FAILED=0
step() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
ok()   { printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED+1)); }
# 요청 본문 n번째(1-base)의 jq 표현식 값
capture_at() { sed -n "${1}p" "$CAPTURE" | jq -r "$2"; }
capture_n()  { [ -f "$CAPTURE" ] && wc -l < "$CAPTURE" | tr -d ' ' || echo 0; }
# 목을 비운다(열린 HITL 해제 + capture 절단). 목을 죽였다 다시 띄우는 대신 이 한 줄인 이유:
# `kill` 한 백그라운드 잡을 `wait` 하면 bash 3.2 가 그 SIGTERM 을 스크립트 자신에게 옮겨 조용히 끝난다.
reset_mock() { curl -sS -X POST "$SERVER_URL/__mock/reset" -o /dev/null; }

TASK_ID="11111111-1111-4111-8111-111111111111"
LANE_ID="22222222-2222-4222-8222-222222222222"
SESSION_ID="33333333-3333-4333-8333-333333333333"
TOKEN="ctk_e2e_p3_smoke_token"

step "0. colab 빌드 (배포와 같은 ldflags — C-3)"
make -C "$ROOT" build >"$OUT/build.txt" 2>&1 || { bad "build"; cat "$OUT/build.txt"; exit 1; }
COLAB="$ROOT/bin/colab"
VERSION_LINE="$("$COLAB" --version)"
# probe 와 같은 정규식으로 첫 x.y.z 를 잡는다 — 이것이 S11 카드에 실린다(C-3).
FIRST_VER="$(printf '%s' "$VERSION_LINE" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
CLI_VER="$(printf '%s' "$VERSION_LINE" | awk '{print $2}')"
[ "$FIRST_VER" = "$CLI_VER" ] \
  && ok "version: probe 가 잡는 첫 매치 = CLI 버전 $FIRST_VER ('$VERSION_LINE')" \
  || bad "version: probe 는 '$FIRST_VER' 를 잡는데 CLI 버전은 '$CLI_VER' ('$VERSION_LINE')"

step "1. 목 서버 기동 (계약 openapi createHitlRequest)"
PORT="$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()')"
python3 "$ROOT/e2e/p3/mock_hitl_server.py" "$PORT" "$CAPTURE" 2>"$OUT/mock.log" &
MOCK_PID=$!
# 프로세스 종료는 pid 로만 (P2_TASKS §0-10).
trap 'kill "$MOCK_PID" 2>/dev/null; true' EXIT
for _ in $(seq 1 50); do
  python3 -c "import socket,sys; s=socket.socket(); sys.exit(0 if s.connect_ex(('127.0.0.1',$PORT))==0 else 1)" && break
  sleep 0.1
done
SERVER_URL="http://127.0.0.1:$PORT"
ok "목 서버 $SERVER_URL (pid $MOCK_PID)"

# 데몬이 attempt 프로세스에 넣는 환경 (harness.md §2.1 / colab-cli.md §1).
colab_env() {
  env COLAB_TASK_TOKEN="$TOKEN" COLAB_SERVER_URL="$SERVER_URL" \
      COLAB_TASK_ID="$TASK_ID" COLAB_TASK_ATTEMPT=1 COLAB_LANE_ID="$LANE_ID" \
      COLAB_SESSION_ID="$SESSION_ID" COLAB_AGENT_NAME=Researcher "$@"
}

step "2. 경로 1 — Claude Code (tool_surface: mcp): colab_hitl_ask 를 stdio 로"
{
  echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"0"}}}'
  echo '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  echo '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  echo '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"colab_hitl_ask","arguments":{"question":"독자는 누구인가?","default":"투자자","context":"브리프에 없다"}}}'
} | colab_env "$COLAB" mcp serve > "$OUT/mcp.jsonl" 2>"$OUT/mcp.err"

TOOLS="$(jq -r 'select(.id==2)|.result.tools[].name' "$OUT/mcp.jsonl" | tr '\n' ',')"
case "$TOOLS" in
  *colab_hitl_ask,*colab_hitl_approve_request,*colab_hitl_request_info,*)
    ok "tools/list 에 HITL 툴 3종 (contracts/colab-cli.md v0.5 §3)" ;;
  *) bad "tools/list = $TOOLS" ;;
esac
ASK="$(jq -c 'select(.id==3)|.result.structuredContent' "$OUT/mcp.jsonl")"
[ "$(printf '%s' "$ASK" | jq -r '.turn_end_required')" = "true" ] \
  && ok "MCP colab_hitl_ask → turn_end_required:true (E7-01)" || bad "MCP ask = $ASK"
[ "$(printf '%s' "$ASK" | jq -r '.instruction')" = "등록됨 — 이 턴을 끝내라" ] \
  && ok "MCP 반환 문구 = '등록됨 — 이 턴을 끝내라'" || bad "MCP instruction = $(printf '%s' "$ASK" | jq -r '.instruction')"
[ "$(printf '%s' "$ASK" | jq -r '.hitl_request.purpose')" = "agent" ] \
  && ok "서버가 준 필드가 --json 에 그대로 (purpose=agent, PR #110)" || bad "purpose 유실: $ASK"
[ "$(capture_n)" = 1 ] && [ "$(capture_at 1 '.body.type')" = "question" ] \
  && ok "서버 본문 {type:question, proposed_default:$(capture_at 1 '.body.proposed_default')}" \
  || bad "요청 $(capture_n)건: $(cat "$CAPTURE")"

step "3. 경로 1 계속 — 같은 task 두 번째 요청은 409 → 3 (E7-04)"
set +e
colab_env "$COLAB" hitl approve-request --summary "배포해도 되나" > "$OUT/second.json" 2>"$OUT/second.err"
CODE=$?
set -e
[ "$CODE" = 3 ] && ok "종료 코드 3" || bad "종료 코드 $CODE (기대 3)"
[ "$(jq -r '.error.code' "$OUT/second.json")" = "hitl_already_open" ] \
  && ok "서버 코드 hitl_already_open 그대로" || bad "$(cat "$OUT/second.json")"
grep -q "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee" "$OUT/second.json" \
  && ok "서버 메시지(열린 요청 id 포함)가 사용자에게 그대로 전달" || bad "서버 메시지 유실: $(cat "$OUT/second.json")"

step "4. 클라이언트 검사 — default 없는 question·choice 는 요청조차 가지 않는다 (E7-05·E7-20)"
BEFORE="$(capture_n)"
for args in "ask --question 독자? " "ask --question 어느쪽? --choices A,B"; do
  set +e
  # shellcheck disable=SC2086
  colab_env "$COLAB" hitl $args >"$OUT/nodefault.json" 2>&1
  CODE=$?
  set -e
  [ "$CODE" = 2 ] && ok "hitl $args → 종료 코드 2" || bad "hitl $args → $CODE ($(cat "$OUT/nodefault.json"))"
done
[ "$(capture_n)" = "$BEFORE" ] \
  && ok "서버에 간 요청 0건 — 422 를 기다리지 않는다" || bad "요청이 갔다: $(capture_n) > $BEFORE"

step "5. 경로 2 — Hermes (tool_surface: cli_wrapper): 위생화된 env 에서 래퍼로"
# harness.md §10 의 래퍼를 그대로 만든다: COLAB_* 를 export 하고 절대 경로의 colab 을 exec.
# 저장소 트리 밖(<workdir_root>/.colab/)이라 커밋에 섞이지 않는다.
WRAPDIR="$OUT/work/.colab/bin/$TASK_ID.1"; mkdir -p "$WRAPDIR"
cat > "$WRAPDIR/colab" <<WRAP
#!/bin/sh
export COLAB_TASK_TOKEN='$TOKEN'
export COLAB_SERVER_URL='$SERVER_URL'
export COLAB_TASK_ID='$TASK_ID'
export COLAB_TASK_ATTEMPT='1'
export COLAB_LANE_ID='$LANE_ID'
export COLAB_SESSION_ID='$SESSION_ID'
export COLAB_AGENT_NAME='Researcher'
exec '$COLAB' "\$@"
WRAP
chmod 0700 "$WRAPDIR/colab"
reset_mock   # task 당 열린 HITL 은 하나 — 케이스마다 비운다(E7-04)

# `env -i`: PATH 도 COLAB_* 도 없다. 래퍼 파일만이 토큰을 나른다(G5 차단 결함 (b)).
set +e
env -i "$WRAPDIR/colab" hitl request-info --what "결제사 API 키" --why "샌드박스로는 재현되지 않는다" \
  > "$OUT/wrapper_info.json" 2>"$OUT/wrapper_info.err"
CODE=$?
set -e
[ "$CODE" = 0 ] && ok "위생화된 env(env -i)에서 래퍼 실행 성공 (종료 0)" || bad "종료 $CODE: $(cat "$OUT/wrapper_info.err")"
[ "$(jq -r '.type' "$OUT/wrapper_info.json")" = "info" ] \
  && ok "request-info → type=info (v0.5 에서 v1 승격, E7-21)" || bad "$(cat "$OUT/wrapper_info.json")"
[ "$(jq -r '.turn_end_required' "$OUT/wrapper_info.json")" = "true" ] \
  && ok "cli_wrapper 경로도 turn_end_required:true" || bad "$(cat "$OUT/wrapper_info.json")"
[ "$(jq -r '.instruction' "$OUT/wrapper_info.json")" = "등록됨 — 이 턴을 끝내라" ] \
  && ok "두 도구 표면의 반환 문구가 같다" || bad "instruction = $(jq -r '.instruction' "$OUT/wrapper_info.json")"
[ "$(capture_at 1 '.body.what')" = "결제사 API 키" ] && [ "$(capture_at 1 '.body.type')" = "info" ] \
  && ok "서버 본문 {type:info, what:…, why:…}" || bad "본문: $(sed -n 1p "$CAPTURE")"

step "6. approve-request · choice 를 래퍼로 한 번씩 (E7-06 · 골든 E7-20 의 정상 경로)"
for CASE in "approve-request --summary 프로덕션에_배포해도_되나:approval" "ask --question 어느쪽? --default B --choices A,B,C:choice"; do
  ARGS="${CASE%:*}"; WANT="${CASE##*:}"
  reset_mock
  set +e
  # shellcheck disable=SC2086
  env -i "$WRAPDIR/colab" hitl $ARGS > "$OUT/wrapper-$WANT.json" 2>&1
  CODE=$?
  set -e
  [ "$CODE" = 0 ] && [ "$(jq -r '.type' "$OUT/wrapper-$WANT.json")" = "$WANT" ] \
    && ok "hitl $ARGS → type=$WANT, turn_end_required=$(jq -r '.turn_end_required' "$OUT/wrapper-$WANT.json")" \
    || bad "hitl $ARGS → 종료 $CODE: $(cat "$OUT/wrapper-$WANT.json")"
  if [ "$WANT" = approval ]; then
    # approval 에는 proposed_default 가 없어야 한다 — 절대 자동 진행되지 않는다(FR-5.4, E7-06).
    [ "$(capture_at 1 '.body|has("proposed_default")')" = "false" ] \
      && ok "approval 본문에 proposed_default 없음 (E7-06)" || bad "본문: $(sed -n 1p "$CAPTURE")"
  else
    [ "$(capture_at 1 '.body.options|length')" = 3 ] && [ "$(capture_at 1 '.body.proposed_default')" = B ] \
      && ok "choice 본문 options 3개 + proposed_default=B (openapi HitlCreateChoice)" || bad "본문: $(sed -n 1p "$CAPTURE")"
  fi
done

printf '\n'
if [ "$FAILED" = 0 ]; then printf '\033[32mSMOKE PASS\033[0m — 두 도구 표면 모두 초록 (목 서버; 서버 T-S5 PR #124 머지 전)\n'; else
  printf '\033[31mSMOKE FAIL\033[0m — %d건\n' "$FAILED"; fi
exit "$FAILED"
