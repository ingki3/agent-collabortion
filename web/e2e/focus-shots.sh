#!/usr/bin/env bash
# T-FOCUS 스크린샷 — 「지금」 줄(PRD FR-3.1.5 · SCREEN v0.19.13 · COMPONENTS §9.10 · Pencil S7-C `CGMWw`·`LgIO6` · S7-CD `j8dkp`·`KBkri` · 레인 카드 `W8dzkH`).
# `next build && next start`(목) 로 찍는다 — 개발 오버레이 배지가 없게. bubble-shots.sh 와 같은 구조.
#
#   focus-01-light.png          S7 타임라인 — 두 에이전트가 동시에 작업 중. Lead 말풍선 첫 줄 「지금 코너에서 … · 3분 전」(에이전트 선언, $ink 500),
#                               Researcher 말풍선 첫 줄 「지금 @Lead의 「BGM v2 …」 요청을 처리하고 있습니다 · 방금」(대신 문장, 흐리게).
#   focus-02-dark.png           01 과 같은 상태, 다크(Pencil S7-C 와 같은 테마).
#   focus-03-derived-light.png  대신 문장 쪽으로 스크롤한 타임라인 — Researcher 말풍선(focus.source = derived, 문장 전체 $ink-2 · 보통 굵기 · 툴팁).
#   focus-04-lanecard-light.png 좌열 서브 미션 카드 — 상태 문구 자리에 같은 「지금」 줄(두 줄 말줄임), 옛 「실행 중 — 취소는 즉시 가능」 없음,
#                               「중단」 버튼 title 「취소는 즉시 가능」.
#   focus-05-narrow-700-dark.png 좁은 화면 700px(Pencil S7-CD 대조) — 「지금」 줄이 두 줄까지 접힌다.
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3199 &
#   BASE_URL=http://localhost:3199 SHOT_DIR=__screenshots__/focus bash e2e/focus-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3199}"
SHOT_DIR="${SHOT_DIR:-__screenshots__/focus}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-focus-shots-$$}"

ab() { agent-browser "$@"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; exit 1; }; echo "  ✓ $2"; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return 'ok'})()" >/dev/null; }
open_working() {
  ab open "$BASE_URL/rooms/$RID" >/dev/null
  ab wait '[data-testid="timeline"]' --timeout 20000 >/dev/null
  sleep 2
  apic "fetch('/api/v1/__mock/rooms/$RID/seed-working', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' }).then(r => r.status)" >/dev/null
  ab wait '[data-testid="working-bubble"] [data-testid="working-focus"]' --timeout 20000 >/dev/null
  sleep 2
}
to_bottom() { apic '(function(){var e=document.querySelector("[data-testid=timeline-end]");e.scrollIntoView({block:"end"});return "ok"})()' >/dev/null; sleep 1; }
common_asserts() {
  assert_js 'document.querySelectorAll("[data-testid=working-bubble] [data-testid=working-focus]").length === 2' "말풍선마다 「지금」 줄"
  assert_js '[...document.querySelectorAll("[data-testid=working-bubble-body]")].every((b) => b.firstElementChild && b.firstElementChild.dataset.testid === "working-focus")' "「지금」 줄이 말풍선 첫 줄(진행 메모 위)"
  assert_js '[...document.querySelectorAll("[data-testid=working-focus]")].every((l) => l.getAttribute("aria-live") === "polite")' "aria-live=polite"
  assert_js 'document.querySelector("[data-testid=working-focus][data-source=agent]").innerText.startsWith("지금 코너에서")' "에이전트 문장"
  assert_js '(function(){var a=getComputedStyle(document.querySelector("[data-testid=working-focus][data-source=agent] [data-testid=working-focus-text]"));var d=getComputedStyle(document.querySelector("[data-testid=working-focus][data-source=derived] [data-testid=working-focus-text]"));return a.color !== d.color && a.fontWeight === "500" && d.fontWeight === "400"})()' "대신 문장은 흐리게(색·굵기가 다르다)"
  assert_js 'getComputedStyle(document.querySelector("[data-testid=working-focus]")).webkitLineClamp === "2"' "두 줄 말줄임"
}
trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 로그인 · 방(Lead · Researcher) + 대화 몇 줄"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 900 >/dev/null
login demo@colab.dev
set_theme light
RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const r = await post(`/workspaces/${me.workspaces[0].id}/rooms`, { name: "마리오 카트" });
  await post(`/__mock/rooms/${r.id}/seed`, { agents: ["Lead", "Researcher"], messages: [
    { content: "@Lead 코너에서 차가 미끄러집니다. 원인을 찾아 주세요." },
    { content: "접지 공식부터 보겠습니다. BGM 은 Researcher 가 맡습니다.", agent: "Lead" },
  ] });
  return r.id;
})()')
echo "  room=$RID"

step "01 — 두 에이전트의 「지금」 줄(라이트)"
open_working
to_bottom
common_asserts
shot focus-01-light

step "02 — 다크"
set_theme dark
open_working
to_bottom
common_asserts
shot focus-02-dark

step "03 — 대신 문장(derived, 라이트)"
set_theme light
open_working
to_bottom
assert_js 'document.querySelector("[data-testid=working-focus][data-source=derived]").innerText.includes("요청을 처리하고 있습니다")' "대신 문장 = 「… 요청을 처리하고 있습니다」"
assert_js 'document.querySelector("[data-testid=working-focus][data-source=derived]").title.includes("받은 요청으로 만든 문장")' "대신 문장 툴팁"
apic '(function(){var b=document.querySelector("[data-testid=working-focus][data-source=derived]").closest("[data-testid=working-bubble]");b.scrollIntoView({block:"center"});return "ok"})()' >/dev/null
sleep 1
shot focus-03-derived-light

step "04 — 서브 미션 카드(라이트)"
assert_js 'document.querySelectorAll("[data-testid=lane-card] [data-testid=lane-focus]").length === 2' "카드 둘에 「지금」 줄"
assert_js '![...document.querySelectorAll("[data-testid=lane-card]")].some((c) => c.innerText.includes("실행 중 — 취소는 즉시 가능") && c.querySelector("[data-testid=lane-focus]"))' "「지금」 줄이 있는 카드에 옛 상태 문구 없음"
assert_js '[...document.querySelectorAll("[data-testid=lane-card] [data-testid=lane-action-cancel]")].some((b) => b.title === "취소는 즉시 가능")' "중단 버튼 title 「취소는 즉시 가능」"
apic '(function(){var c=document.querySelector("[data-testid=lane-card] [data-testid=lane-focus]").closest("[data-testid=lane-card]");c.scrollIntoView({block:"center"});return "ok"})()' >/dev/null
sleep 1
shot focus-04-lanecard-light

step "05 — 좁은 화면 700px(다크 — Pencil S7-CD)"
set_theme dark
ab set viewport 700 1000 >/dev/null
open_working
to_bottom
assert_js 'document.querySelectorAll("[data-testid=working-focus]").length === 2' "700px 에도 「지금」 줄 둘"
assert_js '(function(){var l=document.querySelector("[data-testid=working-focus]");var lh=parseFloat(getComputedStyle(l).lineHeight);return l.getBoundingClientRect().height <= lh*2+2})()' "두 줄을 넘지 않는다"
shot focus-05-narrow-700-dark

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "== focus-shots: 5장 ($SHOT_DIR) =="
