#!/usr/bin/env bash
# T-R2-W4b 스크린샷 — 미션 설정 편집·Director 교체·조건 고치기 · 여기까지 정리 직접 고르기 · 방 멈춤 배너 다음 권한자 · 좁은 화면 탭 · 온보딩 「첫 방」.
# `next build && next start`(목) 로 찍는다. 단언이 거짓이면 멈춘다(틀린 상태를 찍지 않게).
#
#   r2-w4b-01-edit-{light,dark}.png        S21 편집 모드 — 지금 값으로 채움 · Director 는 이름 + 「Director 교체」 안내 · 「저장」
#   r2-w4b-02-director-light.png           Director 교체 다이얼로그 — 지금 Director · 새 Director · deputy 「그대로 둡니다」
#   r2-w4b-03-blocked-{light,dark}.png     미션 칸 — 리뷰어 없는 검토 승인 → 막힘 이유 + 「조건 고치기」 · 동작 줄(설정 편집 · Director 교체)
#   r2-w4b-04-fix-light.png                조건 고치기 다이얼로그 — 같은 편집기, 리뷰어 필수(저장 비활성 + 사유)
#   r2-w4b-05-member-light.png             Director 가 아닌 멤버 — 편집·교체 비활성 + 사유 두 줄(교체는 owner·admin 층까지)
#   r2-w4b-06-pick-bar-light.png           여기까지 정리 · 직접 고르기 — 타임라인 집기 모드(안내 줄 + 메시지마다 단추)
#   r2-w4b-07-pick-dialog-light.png        집고 돌아온 다이얼로그 — 시작·끝 · 「메시지 N건이 이 범위에 듭니다」
#   r2-w4b-08-banner-next-{light,dark}.png 방 멈춤 배너 — 「HH:MM부터 부방장 「서연」님이 답할 수 있습니다」
#   r2-w4b-09-narrow-tabs-light.png        좁은 화면(1000px) 탭 — role=tablist · aria-selected
#   r2-w4b-10-onboarding-light.png         온보딩 3단계 — 「Lead 만들고 첫 방 만들기」
#
# 사용:
#   COLAB_MOCK_API=1 npm run build && COLAB_MOCK_API=1 npx next start -p 3171 &
#   BASE_URL=http://localhost:3171 bash e2e/r2-w4b-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3171}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-r2w4b-shots-$$}"

ab() { agent-browser "$@"; }
shot() { apic '(function(){window.scrollTo(0,0);return "ok"})()' >/dev/null; sleep 0.3; ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1');document.documentElement.setAttribute('data-theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; exit 1; }; echo "  ✓ $2"; }
# 뷰포트 밖 요소는 click 이 조용히 안 닿는다(v1.1 W-17 교훈) — 먼저 보이게 한다.
click() { apic "(function(){const e=document.querySelector('$1');if(!e)return 'missing';e.scrollIntoView({block:'center'});return 'ok'})()" >/dev/null; ab click "$1" >/dev/null; }
mock_post() { apic "fetch('/api/v1$1',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify($2)}).then(r=>r.status)" >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 초기화 · 로그인 · 시드 — 방 「결제팀」: 에이전트 2 · 부방장 서연 · 미션 1(담당 Researcher) · 메시지 5"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1440 900 >/dev/null
login demo@colab.dev
set_theme light
SEED=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const room = await post(`/workspaces/${ws}/rooms`, { name: "결제팀", description: "결제 관련 논의와 작업" });
  const seed = (b) => post(`/__mock/rooms/${room.id}/seed`, b);
  await seed({ agents: ["Lead", "Researcher"], people: [{ email: "seoyeon@colab.dev", role: "deputy" }, { email: "junho@colab.dev", role: "member" }] });
  const parts = (await fetch(`/api/v1/rooms/${room.id}/participants`).then(j)).items;
  const researcher = parts.find((p) => p.kind === "agent" && p.agent.name === "Researcher").agent.id;
  const w = await post(`/rooms/${room.id}/works`, {
    goal: "국내 B2B SaaS 결제 시장 보고서 10페이지", title: "보고서 초안", assignee_agent_id: researcher,
    acceptance_criteria: ["상위 5개 사업자 비교표", "출처 20건 이상"], limits: { budget_usd: 20, time_limit: "PT4H" },
  });
  await seed({ messages: [
    { content: "이번 주 목표는 결제 시장 보고서 초안입니다.", work: null },
    { content: "시장 규모부터 정리해 주세요.", work: null },
    { content: "시장 규모 자료 3건을 찾았습니다. 표로 정리하는 중입니다.", work: null, agent: "Researcher" },
    { content: "PG 3사 수수료 표도 붙여 주세요.", work: null },
    { content: "점심은 12시 반에 모여요.", work: null },
  ] });
  return [ws, room.id, w.id].join(",");
})()')
IFS=, read -r WS ROOM W1 <<<"$SEED"
echo "  ws=$WS room=$ROOM work=$W1"

for theme in light dark; do
  step "01 — 설정 편집(S21 편집 모드) ($theme)"
  set_theme "$theme"
  open_wait "/rooms/$ROOM?work=$W1" "[data-testid=\"work-panel\"][data-work-id=\"$W1\"]"
  click '[data-testid="work-action-edit"]'
  ab wait '[data-testid="rd-edit-work"]' --timeout 10000 >/dev/null
  assert_js 'document.querySelector("[data-testid=rd-create-work-goal]").value.startsWith("국내 B2B SaaS")' "지금 값으로 채움(goal)"
  assert_js 'document.querySelector("[data-testid=rd-edit-work-director]").textContent.includes("「Director 교체」에서 바꿉니다")' "Director 는 여기서 안 바꾼다"
  assert_js 'document.querySelector("[data-testid=rd-create-work-open]").textContent === "저장"' "단추 「저장」"
  shot "r2-w4b-01-edit-$theme"
  click '[data-testid="rd-create-work-cancel"]'
done
set_theme light

step "02 — Director 교체"
click '[data-testid="work-action-director"]'
ab wait '[data-testid="change-director"]' --timeout 10000 >/dev/null
assert_js 'document.querySelector("[data-testid=change-director-save]").disabled' "새 Director 를 고르기 전 비활성"
SEO=$(apic '(function(){const o=[...document.querySelectorAll("[data-testid=change-director-to] option")].find(x=>x.textContent.includes("서연"));return o?o.value:""})()')
ab select '[data-testid="change-director-to"]' "$SEO" >/dev/null
assert_js '!document.querySelector("[data-testid=change-director-save]").disabled' "고르면 켜짐"
shot r2-w4b-02-director-light
click '[data-testid="change-director-cancel"]'

step "03 — 리뷰어 없는 검토 승인에 걸린 미션(목 시드) → 막힘 이유 + 「조건 고치기」"
mock_post "/__mock/works/$W1/seed-reviewerless" '{}'
for theme in light dark; do
  set_theme "$theme"
  open_wait "/rooms/$ROOM?work=$W1" '[data-testid="work-fix-condition"]'
  assert_js 'document.querySelector("[data-testid=progress-blocked]").textContent.includes("조건을 고쳐야 미션이 끝날 수 있습니다")' "막힘 — 미션의 말"
  apic '(function(){document.querySelector("[data-testid=progress-blocked]").scrollIntoView({block:"center"});return "ok"})()' >/dev/null
  sleep 0.3
  ab screenshot "$SHOT_DIR/r2-w4b-03-blocked-$theme.png" >/dev/null; echo "  📸 $SHOT_DIR/r2-w4b-03-blocked-$theme.png"
done
set_theme light

step "04 — 조건 고치기 다이얼로그(같은 편집기, 리뷰어 필수)"
click '[data-testid="work-fix-condition"]'
ab wait '[data-testid="fix-work-condition"]' --timeout 10000 >/dev/null
assert_js 'document.querySelector("[data-testid=fix-work-condition-save]").disabled && !!document.querySelector("[data-testid=reviewer-required]")' "리뷰어 없으면 저장 비활성 + 사유"
shot r2-w4b-04-fix-light
LEAD=$(apic '(function(){const o=[...document.querySelectorAll("[data-testid=reviewer-select] option")].find(x=>x.textContent.includes("Lead"));return o?o.value:""})()')
ab select '[data-testid="reviewer-select"]' "$LEAD" >/dev/null
click '[data-testid="fix-work-condition-save"]'
ab wait --fn '!document.querySelector("[data-testid=fix-work-condition]") && !document.querySelector("[data-testid=work-fix-condition]")' --timeout 10000 >/dev/null
echo "  ✓ 리뷰어를 고르고 저장 → 막힘 풀림"

step "05 — Director 가 아닌 멤버(준호)"
login junho@colab.dev
set_theme light
open_wait "/rooms/$ROOM?work=$W1" "[data-testid=\"work-panel\"][data-work-id=\"$W1\"]"
assert_js 'document.querySelector("[data-testid=work-action-edit]").disabled && document.querySelector("[data-testid=work-action-director]").disabled' "편집·교체 비활성"
assert_js 'document.querySelector("[data-testid=work-director-why]").textContent.includes("소유자·관리자")' "교체 사유는 owner·admin 층까지"
apic '(function(){document.querySelector("[data-testid=work-actions]").scrollIntoView({block:"center"});return "ok"})()' >/dev/null
sleep 0.3
ab screenshot "$SHOT_DIR/r2-w4b-05-member-light.png" >/dev/null; echo "  📸 $SHOT_DIR/r2-w4b-05-member-light.png"
login demo@colab.dev
set_theme light

step "06·07 — 여기까지 정리 · 직접 고르기"
open_wait "/rooms/$ROOM" '[data-testid="timeline"] [data-message-id]'
click '[data-testid="room-more"]'
click '[data-testid="room-menu-summarize"]'
click '[data-testid="summarize-pick"]'
assert_js 'document.querySelector("[data-testid=summarize-dialog-confirm]").disabled' "범위 전 「정리」 비활성"
click '[data-testid="summarize-pick-start"]'
ab wait '[data-testid="pick-bar"]' --timeout 10000 >/dev/null
MIDS=$(apic '[...document.querySelectorAll("[data-testid=timeline] [data-message-id]")].map(e=>e.getAttribute("data-message-id")).join(",")')
FIRST_MSG=$(apic '(function(){const t=[...document.querySelectorAll("[data-testid=timeline] .msg")].find(e=>e.textContent.includes("시장 규모부터"));return t?"ok":"missing"})()')
[ "$FIRST_MSG" = ok ] || { echo "✗ 시드 메시지 없음"; exit 1; }
# 시작 = 「시장 규모부터…」, 끝 = 「PG 3사…」 — 메시지 카드 바로 앞의 집기 단추를 누른다.
PICK='(function(txt){const card=[...document.querySelectorAll("[data-testid=timeline] > div")].find(d=>d.textContent.includes(txt)&&d.querySelector("[data-testid=pick-message]"));if(!card)return "missing";const b=card.querySelector("[data-testid=pick-message]");b.scrollIntoView({block:"center"});b.click();return "ok"})'
[ "$(apic "$PICK(\"시장 규모부터\")")" = ok ] || { echo "✗ 시작 집기 실패"; exit 1; }
sleep 0.4
assert_js 'document.querySelector("[data-testid=pick-bar]").getAttribute("data-step") === "to"' "시작을 집으면 끝을 묻는다"
shot r2-w4b-06-pick-bar-light
[ "$(apic "$PICK(\"PG 3사\")")" = ok ] || { echo "✗ 끝 집기 실패"; exit 1; }
ab wait '[data-testid="summarize-picked"] dl' --timeout 10000 >/dev/null
assert_js 'document.querySelector("[data-testid=summarize-preview] [data-slot]").textContent === "3"' "범위 안 메시지 3건"
shot r2-w4b-07-pick-dialog-light
click '[data-testid="summarize-dialog-confirm"]'
ab wait --fn '!document.querySelector("[data-testid=summarize-dialog]")' --timeout 10000 >/dev/null
echo "  ✓ from/to 로 정리 → 다이얼로그 닫힘"

step "08 — 방 멈춤 배너 · 다음 권한자(0.2.8)"
mock_post "/__mock/rooms/$ROOM/seed" '{"blocked_reason":"budget","blocked_detail":{"approver_email":"demo@colab.dev","delegate_in_min":45,"budget_usd":50,"cost_usd":52.1}}'
mock_post "/__mock/rooms/$ROOM/seed-next-approver" '{"email":"seoyeon@colab.dev","role":"room_deputy"}'
login junho@colab.dev
for theme in light dark; do
  set_theme "$theme"
  open_wait "/rooms/$ROOM" '[data-testid="room-banner-next"]'
  assert_js '/^\d\d:\d\d부터 부방장 「서연」님이 답할 수 있습니다$/.test(document.querySelector("[data-testid=room-banner-next]").textContent)' "다음 권한자 — 역할 + 이름"
  shot "r2-w4b-08-banner-next-$theme"
done
set_theme light

step "09 — 좁은 화면 탭(role=tablist)"
ab set viewport 1000 900 >/dev/null
open_wait "/rooms/$ROOM?work=$W1" '[role="tablist"]'
assert_js 'document.querySelectorAll("[role=tablist] [role=tab]").length === 4 && document.querySelector("[data-testid=tab-work]").getAttribute("aria-selected") === "true"' "탭 넷 · ?work= 면 미션 탭 선택"
shot r2-w4b-09-narrow-tabs-light
ab set viewport 1440 900 >/dev/null

step "10 — 온보딩 3단계 CTA 「첫 방 만들기」(새 가입자)"
ab open "$BASE_URL/login" >/dev/null
apic 'fetch("/api/v1/auth/logout",{method:"POST"}).then(r=>r.status)' >/dev/null || true
ab open "$BASE_URL/signup" >/dev/null
ab wait '[data-testid="signup-form"]' --timeout 20000 >/dev/null
STAMP=$(date +%s)
ab fill 'input[name=display_name]' "민지" >/dev/null
ab fill 'input[name=email]' "minji+$STAMP@example.com" >/dev/null
ab fill 'input[name=password]' "password123" >/dev/null
ab click 'button[type=submit]' >/dev/null
ab wait '[data-testid="workspace-name"]' --timeout 20000 >/dev/null
ab fill '[data-testid="workspace-name"]' "마케팅팀 $STAMP" >/dev/null
ab click '[data-testid="workspace-next"]' >/dev/null
ab wait '[data-testid="pairing-skip"]' --timeout 20000 >/dev/null
ab click '[data-testid="pairing-skip"]' >/dev/null
ab wait '[data-testid="agent-create"]' --timeout 20000 >/dev/null
assert_js 'document.querySelector("[data-testid=agent-create]").textContent === "Lead 만들고 첫 방 만들기"' "CTA 「첫 방 만들기」"
shot r2-w4b-10-onboarding-light
click '[data-testid="agent-create"]'
ab wait '[data-testid="create-room-dialog"]' --timeout 20000 >/dev/null
assert_js 'location.pathname === "/rooms/new"' "누르면 S18(/rooms/new)"
echo; echo "끝."
