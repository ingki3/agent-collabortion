#!/usr/bin/env bash
# T-W11·T-W12 실서버 스모크 — dev 서버(T-S12 #200 · T-S14 #209 머지본)에 붙인 웹을 headless 로 돌려 **목이 아닌 실서버**로 S14 설정 8탭 ·
# 대시보드 · S10 시험 대화 · **멤버 역할 변경·제거 · 알림 설정(T-W12)** 을 한 번 지나간다. 데몬은 없다 — 컴퓨터는 curl probe 로 online 이
# 되고, 턴은 202 뒤 **진행 중**에 머문다(그 화면이 자연스러운지 본다). 409 재전송·410 은 화면이 보내기를 잠그므로 같은 브라우저 세션의
# fetch 로 잰다. 스크린샷은 **밝음·어두움 둘 다**(PR #207 NN5) — 테마는 설정 화면과 같은 경로(`<html data-theme>` + localStorage)로
# 그 자리에서 바꾸므로 화면 상태(진행 중인 시험 대화 등)가 새로고침으로 날아가지 않는다.
#
#   p5-w11-01-settings-real-{light,dark}.png          소유자 — 작업 폴더 탭, 보존 기한 저장 왕복 뒤
#   p5-w11-02-dashboard-real-{light,dark}.png         getWorkspaceMetrics 실값 — 빈 워크스페이스라 10행 전부 「아직 잴 수 없음」
#   p5-w11-03-test-chat-real-{light,dark}.png         시험 대화 — 턴 202 뒤 진행 중(답하는 중… · 입력 잠금 사유 · 실행 경로 「첫 답이 오면 표시」)
#   p5-w11-03b-test-chat-closed-real-{light,dark}.png 닫은 뒤 — queued 턴이 「답을 받기 전에 시험 대화를 끝냈습니다」 로 마감
#   p5-w12-04-members-owner-{light,dark}.png          멤버 탭(소유자) — 역할 변경 200 뒤 · 제거 409 member_is_director 의 서버 문장(세션 수)
#   p5-w12-05-members-self-demote-{light,dark}.png    멤버 탭(관리자) — 자기 역할 내리기 확인 다이얼로그(PR #209 NN5) · 소유자 행 잠김 사유
#   p5-w12-06-notifications-{light,dark}.png          알림 탭(멤버) — 구독 기본값·푸시 저장 왕복 뒤 「저장됨」
#
# 사용(서버는 e2e/p5/up.sh 를 네 포트로 — 예: SERVER_URL=http://localhost:8112 PG_PORT=5456 PG_CONTAINER=colab-pg-w12):
#   COLAB_SERVER_URL=http://localhost:8112 npx next dev -p 3016 &
#   BASE_URL=http://localhost:3016 SERVER_URL=http://localhost:8112 PG_CONTAINER=colab-pg-w12 bash e2e/p5-w11-real.sh
set -uo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3016}"
SERVER_URL="${SERVER_URL:-http://localhost:8112}"
PG_CONTAINER="${PG_CONTAINER:-colab-pg-w12}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w12-real-$$}"
ab() { agent-browser "$@"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
# 같은 화면을 밝음·어두움으로 — 새로고침 없이. 설정 화면이면 「화면」 라디오를 실제로 눌러(사용자와 같은 경로, 라디오 표시도 맞는다)
# 다른 화면이면 <html data-theme> + localStorage 를 직접 쓴다(lib/theme.ts applyTheme 과 같은 속성·키).
set_theme() {
  local r; r="$(apic "(function(){const i=document.querySelector('[data-testid=theme-select] input[value=$1]');if(!i)return 'no';i.click();return 'ok'})()")"
  [ "$r" = ok ] || apic "(function(){document.documentElement.setAttribute('data-theme','$1');try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null
  sleep 0.5
}
shot_both() { for th in light dark; do set_theme "$th"; shot_full "$1-$th"; done; }
step() { echo; echo "▶ $*"; }
# eval 이 실패하면(JS 예외·탐색 중) 원문 error 를 stderr 에 남긴다 — 빈 값만 보고는 다음에 못 고친다.
apic() { ab eval "$1" --json | python3 -c 'import sys,json
d=json.load(sys.stdin)
if not d.get("data"): print("  [eval 실패] " + str(d.get("error")) [:300], file=sys.stderr); sys.exit(0)
print(d["data"]["result"])'; }
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


# ── 시드(curl, 서버 직접) — 계정 넷(소유자·멤버·관리자·내보낼 멤버) · 워크스페이스 · 에이전트 · 컴퓨터 하나(pair + probe 로 online) ──
API="$SERVER_URL/api/v1"
RUN="w11-$(date +%H%M%S)"
CK="$(mktemp -t colab-w11-cookies)"; trap 'rm -f "$CK"; ab close >/dev/null 2>&1 || true' EXIT
sapi() { local m="$1" p="$2" b="${3:-}"; if [ -n "$b" ]; then curl -sS -b "$CK" -c "$CK" -H 'Content-Type: application/json' -X "$m" "$API$p" --data "$b"; else curl -sS -b "$CK" -c "$CK" -X "$m" "$API$p"; fi; }
jqr() { python3 -c "import sys,json;d=json.load(sys.stdin);print($1)"; }
OWNER="owner-$RUN@example.com"; MEMBER="member-$RUN@example.com"; ADMIN="admin-$RUN@example.com"; VICTIM="victim-$RUN@example.com"
sapi POST /auth/signup "{\"email\":\"$OWNER\",\"password\":\"password123\",\"display_name\":\"Owner\"}" >/dev/null
WS="$(sapi POST /workspaces "{\"name\":\"W11 $RUN\"}" | jqr 'd["id"]')"
AG="$(sapi POST "/workspaces/$WS/agents" '{"name":"Guide","role":"researcher","role_description":"제품 사용법을 설명한다","instructions":"짧게, 한국어로 답한다.","budget_per_task":0.5,"profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-sonnet-5","is_default":true}]}' | jqr 'd["id"]')"
INV_M="$(sapi POST "/workspaces/$WS/invites" "{\"email\":\"$MEMBER\",\"role\":\"member\"}" | jqr 'd["token"]')"
INV_A="$(sapi POST "/workspaces/$WS/invites" "{\"email\":\"$ADMIN\",\"role\":\"admin\"}" | jqr 'd["token"]')"
INV_V="$(sapi POST "/workspaces/$WS/invites" "{\"email\":\"$VICTIM\",\"role\":\"member\"}" | jqr 'd["token"]')"
PC="$(sapi POST "/workspaces/$WS/runtimes/pairings" '{"name":"mac-w11"}' | jqr 'd["pairing_token"]')"
PR="$(curl -sS -X POST "$SERVER_URL/v1/daemon/pair" -H 'Content-Type: application/json' -d "{\"pairing_code\":\"$PC\",\"hostname\":\"mac-w11\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")"
DTOK="$(jqr 'd["daemon_token"]' <<<"$PR")"; RID="$(jqr 'd["runtime_id"]' <<<"$PR")"
# probe → online(데몬 없이). heartbeat 가 없으니 유예가 지나면 offline 이 된다 — 이 스크립트는 그 안에 끝난다.
curl -sS -X POST "$SERVER_URL/v1/daemon/runtimes/$RID/probe" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d '{"daemon_version":"0.1.0","hostname":"mac-w11","capabilities":[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}],"repos":[],"workdir_root":"/tmp/colab-w11","disk":{"used_bytes":0},"colab_cli":{"present":true,"version":"0.1.0"}}' -o /dev/null
for pair in "$MEMBER:Mem:$INV_M" "$ADMIN:Adm:$INV_A" "$VICTIM:Vic:$INV_V"; do
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
open_wait "/settings?tab=notifications" '[data-testid="row-subscription"]' >/dev/null; sleep 1
chk "  알림 탭(T-S14 #209 실구현) 오류 없음" "$(apic 'document.querySelector("[data-testid=notifications-error]")?.textContent||"none"')" "none"
open_wait "/settings?tab=workdir" '[data-testid="row-retention"]' >/dev/null
shot_both "p5-w11-01-settings-real"

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
shot_both "p5-w11-02-dashboard-real"

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
shot_both "p5-w11-03-test-chat-real"
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
shot_both "p5-w11-03b-test-chat-closed-real"


# ═════════════════════════════════════════════════════════════════════════════
# T-W12 — 멤버 역할 변경·제거 · 알림 설정: 화면 → 실서버(#209) 왕복. 서버가 만드는 문장은 화면이 그대로 보인다.
# ⚠ bash 3.2: `"$(apic "(…{a,b}…)")"` 처럼 **따옴표를 겹친 $( ) 안의 중괄호**는 브레이스 확장으로 잘려 나간다(JS 의 `{}` 가 사라져
#   SyntaxError). JS 는 먼저 변수(JS=…)에 담고 `"$(apic "$JS")"` 로만 부른다 — 위 W11 절이 그렇게 하는 이유.
# ═════════════════════════════════════════════════════════════════════════════
MEMS_JS="(async()=>{const p=await fetch('/api/v1/workspaces/$WS/members?limit=100').then(r=>r.json());return JSON.stringify(Object.fromEntries(p.items.map(m=>[m.user.email,{id:m.id,uid:m.user.id,role:m.role}])))})()"
mem_field() { apic "$MEMS_JS" | python3 -c "import sys,json;d=json.load(sys.stdin);print(d['$1']['$2'] if '$1' in d else 'gone')"; }
# 화면의 행 하나를 찾아 그 안에서 동작한다 — 이메일(또는 '(나)')로.
row_js() { echo "[...document.querySelectorAll('[data-testid=member-row]')].find(r=>r.textContent.includes('$1'))"; }
pick_role() { # ROW_KEY ROLE — 제어 select 에 값을 넣고 change 를 쏜다(React 는 native setter 뒤 change 이벤트만 듣는다)
  JS="(function(){const s=$(row_js "$1").querySelector('[data-testid=member-role]');const set=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set;set.call(s,'$2');s.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()"
  apic "$JS" >/dev/null
}
click_in_row() { JS="(function(){const b=$(row_js "$1").querySelector('[data-testid=$2]');if(!b||b.disabled)return 'no';b.click();return 'ok'})()"; apic "$JS"; }
rows_n() { apic 'document.querySelectorAll("[data-testid=member-row]").length'; }

step "멤버 탭(소유자) — 화면에서 역할 변경(member → admin → member) · 서버 재조회로 확인"
open_wait "/settings?tab=members" '[data-testid="member-row"]'
sleep 1
chk "  행 수(소유자·멤버·관리자·내보낼 멤버)" "$(rows_n)" "4"
JS="(function(){const r=$(row_js '(나)');return r.querySelector('[data-testid=member-role]').disabled+'/'+(r.querySelector('[data-testid=member-role-why]')?.textContent||'')})()"
chk "  내 행(소유자)의 역할 선택 잠김 + 사유" "$(apic "$JS")" "true/내 소유자 역할은 다른 소유자가 바꿔야 합니다"
pick_role "$MEMBER" admin; sleep 2
chk "  updateMemberRole 200 → 서버 role" "$(mem_field "$MEMBER" role)" "admin"
chk "  화면 오류 없음" "$(apic 'document.querySelector("[data-testid=members-error]")?.textContent||"none"')" "none"
pick_role "$MEMBER" member; sleep 2
chk "  되돌리기 → 서버 role" "$(mem_field "$MEMBER" role)" "member"

step "멤버 탭(소유자) — 내보내기: Director 인 진행 중 세션이 있으면 409 member_is_director 의 서버 문장(세션 수) → Director 교체 뒤 204"
VUID="$(mem_field "$VICTIM" uid)"; VMID="$(mem_field "$VICTIM" id)"; OUID="$(mem_field "$OWNER" uid)"; MYMID="$(mem_field "$OWNER" id)"
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/sessions',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({title:'제거 판정용',goal:'멤버 제거 판정을 위한 세션',isolation:{kind:'none'},participants:[{agent_id:'$AG'}],assignee_agent_id:'$AG',runtime_id:'$RID'})});const b=await r.json();return r.status===201?b.id:('ERR'+r.status+JSON.stringify(b))})()"
SID="$(apic "$JS")"
JS="(async()=>{const r=await fetch('/api/v1/sessions/$SID/director',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({director_user_id:'$VUID'})});return String(r.status)})()"
chk "  changeDirector → 내보낼 멤버(#209 가 고친 500 자리)" "$(apic "$JS")" "200"
[ "$(click_in_row "$VICTIM" member-remove)" = "ok" ] || { echo "  ✗ 내보내기 버튼"; fail=1; }
sleep 0.5
[ "$(click_in_row "$VICTIM" member-remove-yes)" = "ok" ] || { echo "  ✗ 내보내기 확인 버튼"; fail=1; }
sleep 2
chk "  409 detail 그대로(세션 1개)" "$(apic 'document.querySelector("[data-testid=members-error]")?.textContent||""')" "이 멤버가 Director 인 진행 중 세션이 1개 있습니다 — 먼저 그 세션의 Director 를 교체해 주세요"
chk "  아직 멤버(행 수)" "$(rows_n)" "4"
shot_both "p5-w12-04-members-owner"
JS="(async()=>{const r=await fetch('/api/v1/sessions/$SID/director',{method:'PUT',headers:{'content-type':'application/json'},body:JSON.stringify({director_user_id:'$OUID'})});return String(r.status)})()"
chk "  Director 를 소유자로 되돌림(78_ B.9 와 같은 길)" "$(apic "$JS")" "200"
# 409 뒤에도 확인 줄은 열린 채다(화면은 실패 뒤 확인을 닫지 않는다 — 사유를 읽고 다시 누를 수 있게) → 「내보내기」 확인만 다시 누른다.
[ "$(click_in_row "$VICTIM" member-remove-yes)" = "ok" ] || { echo "  ✗ 내보내기 확인 버튼(2)"; fail=1; }
sleep 2
chk "  removeMember 204 → 행 수" "$(rows_n)" "3"
chk "  서버 재조회 — 내보내진 멤버 없음" "$(mem_field "$VICTIM" role)" "gone"
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/members/$VMID',{method:'DELETE'});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  두 번째 제거 404 — 서버 문장" "$(apic "$JS")" "404/not_found/멤버를 찾을 수 없습니다"
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/members/$MYMID',{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({role:'admin'})});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  마지막 소유자 강등 409(화면은 잠겨 있어 fetch 로)" "$(apic "$JS")" "409/last_owner/마지막 소유자는 강등할 수 없습니다 — 먼저 다른 멤버를 소유자로 지정해 주세요"

step "멤버 탭(관리자) — 자기 역할 내리기 확인 다이얼로그(NN5): 취소 → 그대로 · 내리기 → 서버 member · 소유자 행은 잠김"
logout; login "$ADMIN"
open_wait "/settings?tab=members" '[data-testid="member-row"]'
sleep 1
JS="(function(){const r=$(row_js "$OWNER");return r.querySelector('[data-testid=member-role]').disabled+'/'+(r.querySelector('[data-testid=member-role-why]')?.textContent||'')})()"
chk "  소유자 행 — 역할 선택 잠김 사유" "$(apic "$JS")" "true/소유자의 역할은 소유자만 바꿀 수 있습니다"
JS="(function(){const b=$(row_js "$OWNER").querySelector('[data-testid=member-remove]');return b.disabled+'/'+b.title})()"
chk "  소유자 행 — 내보내기 잠김 + 사유" "$(apic "$JS")" "true/소유자는 소유자만 내보낼 수 있습니다"
JS="(function(){const s=$(row_js '(나)').querySelector('[data-testid=member-role]');return String(s.querySelector('option[value=owner]').disabled)})()"
chk "  내 행 — 「소유자」 항목 꺼짐(승격도 소유자만)" "$(apic "$JS")" "true"
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/members/$MYMID',{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({role:'member'})});const b=await r.json();return r.status+'/'+b.code+'/'+b.detail})()"
chk "  (fetch) 관리자가 소유자 강등 → 403 서버 문장" "$(apic "$JS")" "403/owner_only/소유자 역할을 주거나 거두는 것은 소유자만 할 수 있습니다"
pick_role '(나)' member; sleep 1
chk "  확인 다이얼로그 — 무엇이 사라지는지" "$(apic 'document.querySelector("[data-testid=member-self-demote]")?.textContent.slice(0,33)||""')" "내 역할을 관리자에서 멤버로 내립니다. 멤버 초대·역할 변경"
shot_both "p5-w12-05-members-self-demote"
[ "$(click_id member-self-demote-no)" = "ok" ] || { echo "  ✗ 취소 버튼"; fail=1; }
sleep 1
chk "  취소 → 서버 role 그대로" "$(mem_field "$ADMIN" role)" "admin"
chk "  취소 → 다이얼로그 닫힘" "$(apic 'document.querySelector("[data-testid=member-self-demote]")?"open":"closed"')" "closed"
pick_role '(나)' member; sleep 1
[ "$(click_id member-self-demote-yes)" = "ok" ] || { echo "  ✗ 내리기 버튼"; fail=1; }
sleep 2
chk "  내리기 → 서버 role" "$(mem_field "$ADMIN" role)" "member"
# 내린 뒤 me.workspaces[].my_role 은 다음 /me 까지 admin 이라 선택이 바로 잠기지는 않는다 — 서버가 다음 관리 op 을 403 으로 막는다.
JS="(async()=>{const r=await fetch('/api/v1/workspaces/$WS/invites');const b=await r.json();return r.status+'/'+b.code})()"
chk "  내린 뒤 — 서버가 관리 op 을 막는다(fetch)" "$(apic "$JS")" "403/admin_required"

step "알림 탭(멤버) — 구독 기본값·푸시 저장 왕복(updateNotificationSettings 200) → 다시 읽어 확인 · enum 밖 422"
logout; login "$MEMBER"
open_wait "/settings?tab=notifications" '[data-testid="row-subscription"]'
sleep 1
NJS="(async()=>{const s=await fetch('/api/v1/me/notification-settings').then(r=>r.json());return s.email+'/'+s.push+'/'+s.default_subscription})()"
chk "  기본값(openapi default)" "$(apic "$NJS")" "true/false/all"
ab click '[data-testid="notif-push"]' >/dev/null
JS="(function(){const s=document.querySelector('[data-testid=notif-subscription]');const set=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set;set.call(s,'hitl_only');s.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()"
apic "$JS" >/dev/null
sleep 0.5
[ "$(click_id settings-save)" = "ok" ] || { echo "  ✗ 저장 버튼 비활성"; fail=1; }
ab wait '[data-testid="settings-saved"]' --timeout 10000 >/dev/null && echo "  ✓ 저장됨 표시" || { echo "  ✗ 저장됨 표시 없음: $(apic 'document.querySelector("[data-testid=notifications-error]")?.textContent||document.body.innerText.slice(0,300)')"; fail=1; }
shot_both "p5-w12-06-notifications"
chk "  서버 재조회" "$(apic "$NJS")" "true/true/hitl_only"
JS="(async()=>{const r=await fetch('/api/v1/me/notification-settings',{method:'PATCH',headers:{'content-type':'application/json'},body:JSON.stringify({default_subscription:'nope'})});const b=await r.json();return r.status+'/'+b.errors[0].field+'/'+b.errors[0].message})()"
chk "  enum 밖 422 — 서버 문장" "$(apic "$JS")" "422/default_subscription/구독 기본값은 전부 · 사람 확인만 · 종료만 중 하나여야 합니다"
chk "  거부된 PATCH 는 아무것도 안 바꿨다" "$(apic "$NJS")" "true/true/hitl_only"
open_wait "/settings?tab=notifications" '[data-testid="row-subscription"]'
sleep 1
chk "  새로 열어도 저장값이 보인다" "$(apic 'document.querySelector("[data-testid=notif-subscription]").value+"/"+document.querySelector("[data-testid=notif-push]").checked')" "hitl_only/true"

echo; [ $fail = 0 ] && echo "✅ 실서버 스모크 통과" || echo "❌ 실서버 스모크 실패 있음"; exit $fail
