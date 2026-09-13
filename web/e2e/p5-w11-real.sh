#!/usr/bin/env bash
# T-W11 실서버 스모크 — dev 서버(T-S12 #200 머지본)에 붙인 웹을 headless 로 돌려 **목이 아닌 실서버**로 S14 설정 8탭 · 대시보드 ·
# S10 시험 대화를 한 번 지나간다. 데몬은 없다 — 컴퓨터는 curl probe 로 online 이 되고, 턴은 202 뒤 **진행 중**에 머문다
# (그 화면이 자연스러운지 본다). 409 재전송·410 은 화면이 보내기를 잠그므로 같은 브라우저 세션의 fetch 로 잰다.
#
#   p5-w11-01-settings-real.png          소유자 — 작업 폴더 탭, 보존 기한 저장 왕복 뒤
#   p5-w11-02-dashboard-real.png         getWorkspaceMetrics 실값 — 빈 워크스페이스라 10행 전부 「아직 잴 수 없음」
#   p5-w11-03-test-chat-real.png         시험 대화 — 턴 202 뒤 진행 중(답하는 중… · 입력 잠금 사유 · 실행 경로 「첫 답이 오면 표시」)
#   p5-w11-03b-test-chat-closed-real.png 닫은 뒤 — queued 턴이 「답을 받기 전에 시험 대화를 끝냈습니다」 로 마감
#
# 사용(서버는 e2e/p5/up.sh 를 네 포트로 — 예: SERVER_URL=http://localhost:8110 PG_PORT=5454 PG_CONTAINER=colab-pg-w11):
#   COLAB_SERVER_URL=http://localhost:8110 npx next dev -p 3016 &
#   BASE_URL=http://localhost:3016 SERVER_URL=http://localhost:8110 PG_CONTAINER=colab-pg-w11 bash e2e/p5-w11-real.sh
set -uo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3016}"
SERVER_URL="${SERVER_URL:-http://localhost:8110}"
PG_CONTAINER="${PG_CONTAINER:-colab-pg-w11}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w11-real-$$}"
ab() { agent-browser "$@"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
fail=0
chk() { if [ "$2" = "$3" ]; then echo "  ✓ $1 ($2)"; else echo "  ✗ $1 got=[$2] want=[$3]"; fail=1; fi; }
open_wait() {
  ab open "$BASE_URL$1" >/dev/null
  ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; } || {
    echo "  ✗ $1 에서 $2 를 못 찾았다. 화면: $(apic 'JSON.stringify({url:location.href,text:document.body.innerText.slice(0,300)})')"; return 1; }
}
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  sleep 1
  for _try in 1 2; do
    ab fill 'input[name="email"]' "$1" >/dev/null
    ab fill 'input[name="password"]' 'password123' >/dev/null
    ab click 'button[type="submit"]' >/dev/null
    ab wait '[data-testid="app-nav"]' --timeout 15000 >/dev/null && return 0
  done
  echo "  ✗ $1 로그인 뒤 앱 셸이 안 떴다. 화면: $(apic 'JSON.stringify({url:location.href,text:document.body.innerText.slice(0,300)})')"; return 1
}
logout() { apic '(async()=>{await fetch("/api/v1/auth/logout",{method:"POST"}).catch(()=>{});return "ok"})()' >/dev/null; }
click_id() { apic "(function(){var b=document.querySelector('[data-testid=\"$1\"]');if(!b||b.disabled)return 'no';b.click();return 'ok'})()"; }
mkdir -p "$SHOT_DIR"


# ── 시드(curl, 서버 직접) — 계정 셋(소유자·멤버·관리자) · 워크스페이스 · 에이전트 · 컴퓨터 하나(pair + probe 로 online) ──
API="$SERVER_URL/api/v1"
RUN="w11-$(date +%H%M%S)"
CK="$(mktemp -t colab-w11-cookies)"; trap 'rm -f "$CK"; ab close >/dev/null 2>&1 || true' EXIT
sapi() { local m="$1" p="$2" b="${3:-}"; if [ -n "$b" ]; then curl -sS -b "$CK" -c "$CK" -H 'Content-Type: application/json' -X "$m" "$API$p" --data "$b"; else curl -sS -b "$CK" -c "$CK" -X "$m" "$API$p"; fi; }
jqr() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }
OWNER="owner-$RUN@example.com"; MEMBER="member-$RUN@example.com"; ADMIN="admin-$RUN@example.com"
sapi POST /auth/signup "{\"email\":\"$OWNER\",\"password\":\"password123\",\"display_name\":\"Owner\"}" >/dev/null
WS="$(sapi POST /workspaces "{\"name\":\"W11 $RUN\"}" | jqr 'd["id"]')"
AG="$(sapi POST "/workspaces/$WS/agents" '{"name":"Guide","role":"researcher","role_description":"제품 사용법을 설명한다","instructions":"짧게, 한국어로 답한다.","budget_per_task":0.5,"profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-sonnet-5","is_default":true}]}' | jqr 'd["id"]')"
INV_M="$(sapi POST "/workspaces/$WS/invites" "{\"email\":\"$MEMBER\",\"role\":\"member\"}" | jqr 'd["token"]')"
INV_A="$(sapi POST "/workspaces/$WS/invites" "{\"email\":\"$ADMIN\",\"role\":\"admin\"}" | jqr 'd["token"]')"
PC="$(sapi POST "/workspaces/$WS/runtimes/pairings" '{"name":"mac-w11"}' | jqr 'd["pairing_token"]')"
PR="$(curl -sS -X POST "$SERVER_URL/v1/daemon/pair" -H 'Content-Type: application/json' -d "{\"pairing_code\":\"$PC\",\"hostname\":\"mac-w11\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")"
DTOK="$(jqr 'd["daemon_token"]' <<<"$PR")"; RID="$(jqr 'd["runtime_id"]' <<<"$PR")"
# probe → online(데몬 없이). heartbeat 가 없으니 유예가 지나면 offline 이 된다 — 이 스크립트는 그 안에 끝난다.
curl -sS -X POST "$SERVER_URL/v1/daemon/runtimes/$RID/probe" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d '{"daemon_version":"0.1.0","hostname":"mac-w11","capabilities":[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}],"repos":[],"workdir_root":"/tmp/colab-w11","disk":{"used_bytes":0},"colab_cli":{"present":true,"version":"0.1.0"}}' -o /dev/null
for pair in "$MEMBER:Mem:$INV_M" "$ADMIN:Adm:$INV_A"; do
  em="${pair%%:*}"; rest="${pair#*:}"; nm="${rest%%:*}"; tok="${rest#*:}"
  rm -f "$CK"; sapi POST /auth/signup "{\"email\":\"$em\",\"password\":\"password123\",\"display_name\":\"$nm\"}" >/dev/null
  sapi POST "/invites/$tok/accept" '{}' >/dev/null
done
echo "  seed: ws=$WS agent=$AG runtime=$RID owner=$OWNER"

step "소유자 로그인 — 설정 8탭 읽기"
ab set viewport 1280 900 >/dev/null
login "$OWNER"
for t in members runtime budget loop context workdir security notifications; do
  open_wait "/settings?tab=$t" "[data-testid=\"settings-tab-$t\"]" && echo "  ✓ 탭 $t 렌더" || fail=1
  chk "  탭 $t 오류 없음" "$(apic 'document.querySelector("[data-testid=settings-error]")?"err":"none"')" "none"
done

CURV="$(apic "(async()=>{const s=await fetch('/api/v1/workspaces/$WS/settings').then(r=>r.json());return String(s.workdir_retention_days)})()")"
WANT=$((CURV + 1))
step "저장 왕복 — 작업 폴더 보존 $CURV → $WANT (updateWorkspaceSettings 200) → 다시 읽어 $WANT"
open_wait "/settings?tab=workdir" '[data-testid="row-retention"]'
sleep 1
for _try in 1 2; do
  ab fill '[aria-label="작업 폴더 보존"]' "$WANT" >/dev/null
  ab wait '[data-testid="settings-dirty"]' --timeout 5000 >/dev/null && break
done
[ "$(click_id settings-save)" = "ok" ] || { echo "  ✗ 저장 버튼 비활성"; fail=1; }
ab wait '[data-testid="settings-saved"]' --timeout 10000 >/dev/null && echo "  ✓ 저장됨 표시" || { echo "  ✗ 저장됨 표시 없음: $(apic 'document.body.innerText.slice(0,400)')"; fail=1; }
chk "  서버 재조회 workdir_retention_days" "$(apic "(async()=>{const s=await fetch('/api/v1/workspaces/$WS/settings').then(r=>r.json());return String(s.workdir_retention_days)})()")" "$WANT"
echo "  알림 탭(서버 미구현 op): $(open_wait "/settings?tab=notifications" '[data-testid="settings-tab-notifications"]' >/dev/null; sleep 1; apic 'document.querySelector("[data-testid=notifications-error]")?.textContent||"(오류 표시 없음)"')"
shot_full "p5-w11-01-settings-real"

step "S-69 — 멤버 계정으로 GET 200 · PATCH 403 · 화면 읽기 전용"
logout; login "$MEMBER"
chk "  멤버 GET /settings" "$(apic "(async()=>{const r=await fetch('/api/v1/workspaces/$WS/settings');return String(r.status)})()")" "200"
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/settings',{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({workdir_retention_days:1})});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  멤버 PATCH /settings" "$(apic "$JS")" "403/admin_required/소유자·관리자만 할 수 있습니다"
open_wait "/settings?tab=budget" '[data-testid="settings-budget-hint"]' && echo "  ✓ 멤버 예산 탭 비활성 사유 표시" || fail=1

step "S-70 — 관리자 계정으로 마스킹 PATCH 403 owner_required · 화면은 보안 탭 소유자만"
logout; login "$ADMIN"
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/settings',{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({task_event_masking:true,workdir_retention_days:1})});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  admin PATCH masking(+다른 칸)" "$(apic "$JS")" "403/owner_required/활동 기록 마스킹은 워크스페이스 소유자만 바꿀 수 있습니다"
chk "  거절은 통째 — retention 그대로" "$(apic "(async()=>{const s=await fetch('/api/v1/workspaces/$WS/settings').then(r=>r.json());return String(s.workdir_retention_days)})()")" "$WANT"
open_wait "/settings?tab=security" '[data-testid="settings-security-hint"]' && echo "  ✓ 관리자 보안 탭 비활성 사유 표시" || fail=1

step "대시보드 — getWorkspaceMetrics 실값(빈 워크스페이스 → 10행 전부 아직 잴 수 없음)"
logout; login "$OWNER"
open_wait "/settings?tab=dashboard" '[data-testid="metrics-table"]'
ab wait '[data-testid="metric-row"]' --timeout 15000 >/dev/null
chk "  행 수" "$(apic 'document.querySelectorAll("[data-testid=metric-row]").length')" "10"
chk "  '아직 잴 수 없음' 값 칸 수" "$(apic '[...document.querySelectorAll("[data-testid=metric-value]")].filter(e=>e.textContent.includes("아직 잴 수 없음")).length')" "10"
chk "  판정 unknown 수(10행 + breakdown 2행)" "$(apic '[...document.querySelectorAll("[data-testid=metric-verdict]")].filter(e=>e.dataset.verdict==="unknown").length')" "12"
chk "  첫 행 라벨 = 서버 label" "$(apic 'document.querySelector("[data-testid=metric-row]").textContent.includes("컴퓨터 연결부터 첫 세션 완료까지 걸린 시간")')" "True"
shot_full "p5-w11-02-dashboard-real"

step "시험 대화 — 열기 → 턴 202(진행 중) → 재전송 409 → 닫기 → 410"
open_wait "/agents/$AG" '[data-testid="test-chat-open"]'
[ "$(click_id test-chat-open)" = "ok" ] || { echo "  ✗ 시험 대화 열기 버튼 비활성: $(apic 'document.querySelector("[data-testid=test-chat-no-runtime]")?.textContent||document.querySelector("[data-testid=test-chat-error]")?.textContent||"?"')"; fail=1; }
ab wait '[data-testid="test-chat-input"]' --timeout 20000 >/dev/null
ab fill '[data-testid="test-chat-input"]' '안녕, 너는 누구니?' >/dev/null
[ "$(click_id test-chat-send)" = "ok" ] || { echo "  ✗ 보내기 버튼 비활성"; fail=1; }
ab wait '[data-testid="test-chat-turn-user"]' --timeout 20000 >/dev/null && echo "  ✓ 사용자 턴 202 → 화면" || fail=1
sleep 2
chk "  입력 잠금 사유(진행 중)" "$(apic 'document.querySelector("[data-testid=test-chat-lock]")?.textContent||""')" "답을 기다리는 중입니다 — 끝나면 다시 보낼 수 있습니다"
chk "  실행 경로 자리(첫 답 전)" "$(apic 'document.querySelector("[data-testid=test-chat-transport]")?.textContent||""')" "첫 답이 오면 표시"
chk "  상태 배지" "$(apic 'document.querySelector("[data-testid=test-chat-status]")?.textContent||""')" "열림"
echo "  stats: $(apic 'document.querySelector("[data-testid=test-chat-stats]")?.innerText.replace(/\n/g," · ")||""')"
shot_full "p5-w11-03-test-chat-real"
# 화면은 진행 중엔 보내기를 잠그므로(canSend=false) 409 는 같은 세션의 fetch 로 잰다.
# 현재 채팅 id 는 화면 상태에서 — DOM 에 없으면 서버에서 가장 최근 open 채팅을 DB 로 찾는다.
# 현재 채팅 id — 화면 상태 밖에서는 DB 로 찾는다(가장 최근의 진행 중 채팅).
CUR="$(docker exec -i "$PG_CONTAINER" psql -U colab -d colab -tA -c "select id from test_chat where workspace_id='$WS' and status='open' and turn_status<>'idle' order by created_at desc limit 1")"
JS="(async()=>{const r=await fetch('/api/v1/test-chats/$CUR/turns',{method:'POST',headers:{'content-type':'application/json','idempotency-key':crypto.randomUUID()},body:JSON.stringify({content:'또 하나'})});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  재전송 409 turn_in_progress" "$(apic "$JS")" "409/turn_in_progress/에이전트가 아직 답하는 중입니다 — 답이 오면 다음 메시지를 보낼 수 있습니다"
[ "$(click_id test-chat-close)" = "ok" ] || { echo "  ✗ 닫기 버튼 비활성"; fail=1; }
sleep 2
chk "  닫힌 뒤 상태 배지" "$(apic 'document.querySelector("[data-testid=test-chat-status]")?.textContent||""')" "닫힘"
chk "  닫힌 뒤 agent 턴 error(queued → closed_before_answer)" "$(apic 'document.querySelector("[data-testid=test-chat-turn-error]")?.textContent||""')" "답을 받기 전에 시험 대화를 끝냈습니다"
JS="(async()=>{const r=await fetch('/api/v1/test-chats/$CUR/turns',{method:'POST',headers:{'content-type':'application/json','idempotency-key':crypto.randomUUID()},body:JSON.stringify({content:'x'})});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  닫힌 뒤 턴 410 test_chat_closed" "$(apic "$JS")" "410/test_chat_closed/이미 끝난 시험 대화입니다 — 새 시험 대화를 시작해 주세요"
echo "  닫힌 뒤 화면: $(apic 'document.querySelector("[data-testid=test-chat]")?.innerText.slice(0,500).replace(/\n/g," | ")||""')"
shot_full "p5-w11-03b-test-chat-closed-real"

echo; [ $fail = 0 ] && echo "✅ 실서버 스모크 통과" || echo "❌ 실서버 스모크 실패 있음"; exit $fail
