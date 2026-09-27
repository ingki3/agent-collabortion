#!/usr/bin/env bash
# T-PARTS 스크린샷 — 부분 메시지(PRD FR-3.1.4 · SCREEN v0.19.11 §4.6 · COMPONENTS §9.11 · Pencil 「S7-C 방 화면 · 대화 배치」의 부분 말풍선).
# `next build && next start`(목) 로 찍는다 — 개발 오버레이 배지가 없게. 다른 shots 스크립트와 같은 구조.
#
#   parts-01-light.png              S7 타임라인 — Lead 의 한 말풍선에 부분 셋(보고 → 방장 · 요청 → @Designer · 요청 → @Developer).
#                                   작성자 머리 한 번, 부분마다 머리(‹종류› → 받는 쪽 · 보고는 ↩) · 본문 · 답글, **작업 과정은 맨 아래 하나**.
#   parts-02-dark.png               01 과 같은 상태, 다크(Pencil S7-C 와 같은 테마).
#   parts-03-narrow-700-dark.png    좁은 화면 700px — `message-layers.css` 의 720px 분기 **아래**. Pencil `S7-CD`(다크·좁은) 대조: 말풍선 전폭,
#                                   받는 쪽 칩이 둘째 줄로 접힘.
#   parts-04-narrow-1000-light.png  좁은 화면 1000px — 720 경계 **위**(형제 스크립트와 같은 폭): 말풍선 88% 폭, 부분 머리는 한 줄.
#   parts-05-filling-light.png      실시간 채우는 중 — 첫 부분만 도착(1/3). 「작업 중」 말풍선이 이 말풍선으로 바뀌었고, 남은 부분은 같은 말풍선에 붙는다.
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3198 &
#   BASE_URL=http://localhost:3198 SHOT_DIR=__screenshots__/parts bash e2e/parts-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3198}"
SHOT_DIR="${SHOT_DIR:-__screenshots__/parts}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-parts-shots-$$}"

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
to_bottom() { apic '(function(){var e=document.querySelector("[data-testid=timeline-end]");e.scrollIntoView({block:"end"});return "ok"})()' >/dev/null; sleep 1; }
# 스크린샷마다 **새 방** — 같은 방에 두 번 시드하면 묶음이 둘이 된다(리셋은 로그인 세션까지 지운다).
make_room() {
  RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const r = await post(`/workspaces/${me.workspaces[0].id}/rooms`, { name: "마리오 카트" });
  return r.id;
})()')
  echo "  room=$RID"
}
# 방을 열고(SSE 구독) 부분 메시지를 시드한다. stream=1 이면 첫 부분만 온다.
open_parts() {
  ab open "$BASE_URL/rooms/$RID" >/dev/null
  ab wait '[data-testid="timeline"]' --timeout 20000 >/dev/null
  sleep 2
  apic "fetch('/api/v1/__mock/rooms/$RID/seed-parts', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{\"stream\":${1:-false}}' }).then(r => r.json()).then(j => j.group_id)" > /tmp/parts-group.$$
  ab wait '[data-testid="part-bubble"]' --timeout 20000 >/dev/null
  sleep 2
}
GID() { cat "/tmp/parts-group.$$"; }
full_asserts() {
  assert_js 'document.querySelectorAll("[data-testid=part-bubble]").length === 1' "부분 셋이 말풍선 **하나**"
  assert_js 'document.querySelectorAll("[data-testid=part-bubble] [data-testid=part-head]").length === 3' "부분 머리 셋"
  assert_js 'document.querySelectorAll("[data-testid=part-bubble] [data-testid=reply-button]").length === 3' "답글은 부분마다"
  assert_js 'document.querySelectorAll("[data-testid=part-bubble] [data-testid=part-process] [data-testid=process-fold]").length === 1' "작업 과정은 묶음에 하나(#339 조각 규칙)"
  assert_js '(function(){var p=document.querySelector("[data-testid=part-bubble] [data-testid=part][data-speech=report]");return !!p && p.innerText.includes("↩")})()' "보고 부분에 ↩ 원래 지시"
  assert_js 'document.querySelector("[data-testid=part-bubble]").querySelectorAll(".msg__author").length === 1' "작성자 머리는 한 번"
  assert_js 'document.querySelector("[data-testid=part-bubble] [data-me=true]") !== null' "보는 사람이 받는 쪽인 부분은 「나」"
}
trap 'ab close >/dev/null 2>&1 || true; rm -f /tmp/parts-group.$$' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 로그인 · 방(스크린샷마다 새로 만든다 — Lead · Designer · Developer 는 시드가 들인다)"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 900 >/dev/null
login demo@colab.dev
set_theme light
make_room

step "01 — 한 말풍선, 부분 셋(라이트)"
open_parts
to_bottom
full_asserts
shot parts-01-light

step "02 — 같은 상태, 다크(Pencil S7-C 대조)"
set_theme dark
make_room
open_parts
to_bottom
full_asserts
shot parts-02-dark

step "03 — 좁은 화면 700px(다크 — Pencil S7-CD 와 같은 테마)"
ab set viewport 700 1000 >/dev/null
make_room
open_parts
to_bottom
assert_js 'document.querySelectorAll("[data-testid=part-bubble]").length === 1' "700px 에도 말풍선 하나"
assert_js '(function(){var b=document.querySelector("[data-testid=part-bubble] .convo__bubble");var col=b.closest(".convo__col");return b.getBoundingClientRect().width >= col.getBoundingClientRect().width - 1})()' "≤720px 분기 — 말풍선 전폭"
# 받는 쪽 칩이 둘째 줄로 접혔는가 — 종류 배지와 받는 쪽의 top 이 다르다.
assert_js '(function(){var h=document.querySelector("[data-testid=part-head]");var k=h.querySelector(".part__kindrow"),t=h.querySelector(".convo__to");return Math.abs(k.getBoundingClientRect().top - t.getBoundingClientRect().top) > 4})()' "받는 쪽 칩이 둘째 줄"
shot parts-03-narrow-700-dark

step "04 — 좁은 화면 1000px(라이트 — 720 위)"
set_theme light
ab set viewport 1000 900 >/dev/null
make_room
open_parts
to_bottom
assert_js 'document.querySelectorAll("[data-testid=part-bubble] [data-testid=part-head]").length === 3' "1000px 에도 부분 머리 셋"
assert_js '(function(){var b=document.querySelector("[data-testid=part-bubble] .convo__bubble");var col=b.closest(".convo__col");return b.getBoundingClientRect().width < col.getBoundingClientRect().width})()' "720px 위 — 말풍선은 88% 폭"
assert_js '(function(){var h=document.querySelector("[data-testid=part-head]");var k=h.querySelector(".part__kindrow"),t=h.querySelector(".convo__to");return Math.abs(k.getBoundingClientRect().top - t.getBoundingClientRect().top) <= 4})()' "부분 머리는 한 줄"
shot parts-04-narrow-1000-light

step "05 — 실시간 채우는 중(첫 부분만 · 「작업 중」 말풍선이 교체됐다)"
ab set viewport 1280 900 >/dev/null
make_room
open_parts true
to_bottom
assert_js 'document.querySelectorAll("[data-testid=part-bubble] [data-testid=part-head]").length === 1' "부분 하나가 왔다(1/3)"
assert_js 'document.querySelector("[data-testid=working-bubble]") === null' "「작업 중」 말풍선이 이 말풍선으로 바뀜"
shot parts-05-filling-light
# 다음 부분이 **같은** 말풍선에 붙는가 — 새 말풍선이 생기지 않는다.
apic "fetch('/api/v1/__mock/rooms/$RID/parts-step', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ group_id: '$(GID)' }) }).then(r => r.status)" >/dev/null
sleep 2
assert_js 'document.querySelectorAll("[data-testid=part-bubble]").length === 1 && document.querySelectorAll("[data-testid=part-bubble] [data-testid=part-head]").length === 2' "둘째 부분이 같은 말풍선에 붙었다(2/3)"

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "== parts-shots: 5장 ($SHOT_DIR) =="
