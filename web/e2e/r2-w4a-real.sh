#!/usr/bin/env bash
# T-R2-W4a 실서버 한 번 — S8 받은 요청에 room_paused · isolation_confirm 카드(SCREEN §4.14). 데몬 없이 curl 로 데몬 역할(88_ 레시피).
#
#   1. 방장 Dir 가입 · 워크스페이스 · curl 페어링 · 에이전트 둘
#   2. room_paused — 시간당 주고받기 상한 1 에서 에이전트 멘션 둘 → room.blocked_reason=loop + 방장 확인 요청(room_owner)
#   3. isolation_confirm — 저장소가 있는 컴퓨터로 none 방의 첫 실행 → 보류 + 방장 확인 요청(purpose isolation)
#   4. 브라우저(Dir) /inbox — 두 카드가 방 이름·수신자 근거·결과 이름 버튼으로 그려진다 · 밝음/어두움 스크린샷
#   5. 「워크트리로 나눔」을 눌러 답하면 방이 worktree 로 고정된다(서버 판정)
#
# 스택(이 작업): server :8147 · pg :5497(colab-pg-w4a-5497) · web :3167
#   SERVER_URL=http://localhost:8147 PG_PORT=5497 PG_CONTAINER=colab-pg-w4a-5497 WEB_URL=http://localhost:3167 E2E_OUT=$PWD/e2e/p5/out-w4a WITH_WEB=1 bash e2e/p5/up.sh
#   bash web/e2e/r2-w4a-real.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8147}"
export PG_PORT="${PG_PORT:-5497}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-w4a-5497}"
export WEB_URL="${WEB_URL:-http://localhost:3167}"
export E2E_OUT="${E2E_OUT:-$(cd "$(dirname "$0")/../.." && pwd)/e2e/p5/out-w4a}"
source "$(dirname "$0")/../../e2e/p5/lib.sh"
WEB_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SHOT_DIR="$WEB_DIR/__screenshots__"
RUN="$(date +%H%M%S)-$RANDOM"
API="$SERVER_URL/api/v1"
CHECKS="$OUT/w4a-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/w4a-dir.txt"; rm -f "$COOKIE"
export AGENT_BROWSER_SESSION="colab-r2w4a-real-$$"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL"
CAPS='[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]'
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
mk_agent() { api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" '{name:$n,role:"lead",role_description:"d",instructions:"짧게",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id; }
# 방 + 에이전트 둘 + 담당 Lead 의 미션 하나 — v0.3.0(R4, D22)에서 createSession 이 지워져 방·미션 op 셋으로 만든다.
# 첫 턴은 Director 가 Lead 를 멘션해 연다(옛 createSession 의 「담당 첫 할 일」 자리). PIN=yes 면 방 컴퓨터를 미리 고정한다.
mk_session() { # TITLE PIN(yes|no) → 방 id
  local room
  room="$(api_ok POST "/workspaces/$WS/rooms" "$(jq -nc --arg t "$1" '{name:$t}')" | jq -r .id)"
  [ "$2" = "yes" ] && api_ok PATCH "/rooms/$room" "$(jq -nc --arg rt "$RID" '{runtime_id:$rt,isolation:{kind:"none"}}')" >/dev/null
  for a in "$LEAD" "$R"; do api_ok POST "/rooms/$room/participants" "$(jq -nc --arg a "$a" '{agent_id:$a}')" >/dev/null; done
  local work; work="$(api_ok POST "/rooms/$room/works" "$(jq -nc --arg l "$LEAD" '{goal:"짧은 인사말 한 줄",assignee_agent_id:$l}')" | jq -r .id)"
  api_ok POST "/rooms/$room/messages" "$(jq -nc --arg c "$(mention Lead "$LEAD") 짧은 인사말 한 줄" --arg w "$work" '{content:$c,work_id:$w}')" -H "Idempotency-Key: $(uuid)" >/dev/null
  echo "$room"
}
ab() { agent-browser "$@"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
wait_js() { local r i; for i in $(seq 1 30); do r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] && { echo true; return; }; sleep 0.5; done; echo "$r"; }
trap 'ab close >/dev/null 2>&1 || true' EXIT

step "1. 방장 Dir · 워크스페이스 · 페어링"
EMAIL="w4a-dir-$RUN@example.com"
signup "$EMAIL" password123 "민호" >/dev/null
WS="$(create_workspace "W4a $RUN")"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "macbook-w4a" "$CAPS" "/tmp/colab-w4a-$RUN")"
export DTOK RID
LEAD="$(mk_agent Lead)"; R="$(mk_agent R)"

step "2. room_paused — 루프 상한으로 방 멈춤(서버는 루프 멈춤만 room_paused 로 낸다 — 예산 멈춤은 hitl_request, W4a 보고)"
psqlq "update workspace_settings set loop_limits = '{\"max_hops_per_hour\": 1}' where workspace_id='$WS'" >/dev/null
SA="$(mk_session "결제팀 — 수수료 정책과 정산 주기 재검토 $RUN" yes)"
B="$(claim | jq -c --arg s "$SA" --arg a "$LEAD" '[.tasks[]|select(.task.session_id==$s and .task.agent_id==$a)][0]')"
TA="$(jq -r .task.id <<<"$B")"; TOKA="$(jq -r .task_token <<<"$B")"
daemon_api "tasks/$TA/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null
for n in 하나 둘; do
  curl -sS -X POST "$API/rooms/$SA/messages" -H "Authorization: Bearer $TOKA" -H "Idempotency-Key: $(uuid)" -H 'Content-Type: application/json' \
    -d "$(jq -nc --arg c "$(mention R "$R") $n" '{content:$c}')" >/dev/null
done
chk P.1 loop "$(psqlq "select coalesce(blocked_reason::text,'-') from room where id='$SA'")" "방 루프 멈춤"
INBOX="$(api_ok GET "/inbox?workspace_id=$WS")"
# recipient_basis 는 서버가 비워 보낸다(router.pauseForLoop insert 에 칸이 없다 — W4a 보고). 화면은 방장 대조로 채운다(basisFallback).
chk P.2 "room_paused/action_required/-" "$(jq -r '[.items[]|select(.type=="room_paused")][0]|.type+"/"+.severity+"/"+(.recipient_basis//"-")' <<<"$INBOX")" "받은 요청 room_paused(서버 근거 칸 비어 있음 — 보고)"
chk P.3 "결제팀 — 수수료 정책과 정산 주기 재검토 $RUN" "$(jq -r '[.items[]|select(.type=="room_paused")][0].room.name // "-"' <<<"$INBOX")" "0.2.9 InboxItem.room.name(서버 T-S-r2)"

step "3. isolation_confirm — 저장소 있는 컴퓨터로 첫 실행"
REPO="/tmp/colab-w4a-$RUN/payments"
daemon_api "runtimes/$RID/probe" "$(jq -nc --arg root "/tmp/colab-w4a-$RUN" --arg repo "$REPO" --argjson caps "$CAPS" \
  '{daemon_version:"0.1.0",hostname:"macbook-w4a",capabilities:$caps,repos:[{path:$repo,remote_url:"",branch:"main",clean:true}],workdir_root:$root,disk:{used_bytes:0},colab_cli:{present:true,version:"0.1.0"}}')" >/dev/null
SF="$(mk_session "인프라 $RUN" no)"
claim >/dev/null
INBOX="$(api_ok GET "/inbox?workspace_id=$WS")"
chk I.1 "isolation_confirm/approval/-" "$(jq -r '[.items[]|select(.type=="isolation_confirm")][0]|.type+"/"+(.card.hitl_type//"-")+"/"+(.recipient_basis//"-")' <<<"$INBOX")" "받은 요청 isolation_confirm(approval · 서버 근거 칸 비어 있음 — 보고)"

step "4. 브라우저 /inbox — 두 카드"
ab set viewport 1280 900 >/dev/null
ab open "$WEB_URL/login" >/dev/null
ab wait '[data-testid="login-form"]' --timeout 30000 >/dev/null
ab fill 'input[name="email"]' "$EMAIL" >/dev/null
ab fill 'input[name="password"]' password123 >/dev/null
ab click 'button[type="submit"]' >/dev/null
ab wait '[data-testid="app-nav"]' --timeout 30000 >/dev/null
for theme in light dark; do
  apic "(function(){localStorage.setItem('colab.theme','$theme');return 1})()" >/dev/null
  ab open "$WEB_URL/inbox" >/dev/null
  ab wait '[data-type="room_paused"]' --timeout 30000 >/dev/null
  chk "B.$theme.1" true "$(wait_js 'document.querySelector("[data-type=room_paused] [data-testid=inbox-room]")?.textContent.includes("결제팀 — 수수료 정책과 정산 주기 재검토")')" "room_paused 맥락 한 줄 — 방 이름(줄이지 않음)"
  chk "B.$theme.2" true "$(wait_js 'document.querySelector("[data-type=room_paused] [data-testid=inbox-room-paused-stopped]")?.textContent === "이 방의 미션 1개와 대화 전부가 멈췄습니다"')" "room_paused — 미션 N개와 대화 전부(getRoom blocked_detail)"
  chk "B.$theme.3" true "$(wait_js '!!document.querySelector("[data-type=room_paused] [data-testid=inbox-room-approve]") && document.querySelector("[data-type=room_paused] [data-testid=inbox-basis]").textContent.includes("방장으로서")')" "room_paused — 계속 승인 · 방장으로서"
  chk "B.$theme.4" true "$(wait_js 'document.querySelector("[data-type=isolation_confirm] [data-testid=inbox-isolation-split]")?.textContent === "워크트리로 나눔" && !!document.querySelector("[data-type=isolation_confirm] [data-testid=inbox-isolation-keep]")')" "isolation_confirm — 결과 이름 버튼 둘"
  ab screenshot "$SHOT_DIR/r2-w4a-10-s8-real-$theme.png" --full >/dev/null
  echo "  📸 r2-w4a-10-s8-real-$theme.png"
done
apic "(function(){localStorage.setItem('colab.theme','light');return 1})()" >/dev/null

step "5. 「워크트리로 나눔」 → 방 격리 worktree"
ab click '[data-type="isolation_confirm"] [data-testid="inbox-isolation-split"]' >/dev/null
chk D.1 true "$(wait_js '!document.querySelector("[data-type=isolation_confirm]")')" "답한 카드는 목록에서 내려간다"
sleep 1
chk D.2 worktree "$(psqlq "select coalesce(isolation->>'kind','-') from room where id='$SF'")" "서버 판정 — 승인 = worktree"

summary
