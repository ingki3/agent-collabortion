#!/usr/bin/env bash
# T-BUBBLE 스크린샷 — 「작업 중」 말풍선(SCREEN v0.19.10 §4.6 · COMPONENTS §9.10 · Pencil 「S7-C 방 화면 · 대화 배치」 맨 아래).
# `next build && next start`(목) 로 찍는다 — 개발 오버레이 배지가 없게. 다른 shots 스크립트와 같은 구조.
#
#   bubble-01-two-light.png       S7 타임라인 — 두 에이전트(Lead · Researcher)가 동시에 작업 중. 말풍선 둘(점선 · 배경 없음), 머리 요약 + 실패 꼬리 + 「···」,
#                                 진행 메모 마지막 문장 한 줄. 옛 「작성 중…」 블록 · 옛 「작업 중」 줄 0.
#   bubble-02-expanded-light.png  Researcher 말풍선을 펼친 모습 — 진행 메모 조각마다 한 문단(옛 데몬 폴백: 델타 사이 도구 이벤트로 나뉨) → 활동 피드.
#   bubble-03-two-dark.png        01 과 같은 상태, 다크.
#   bubble-04-expanded-dark.png   Lead 말풍선 펼침(새 데몬: 빈 줄 문단), 다크.
#   bubble-05-narrow-light.png    좁은 화면(420px) — 말풍선 전폭, 머리가 두 줄로 접힌다.
#   bubble-06-posted-light.png    Lead 가 게시 → 말풍선 자리에 게시된 메시지(채운 말풍선), Researcher 말풍선은 그대로.
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3197 &
#   BASE_URL=http://localhost:3197 SHOT_DIR=__screenshots__/bubble bash e2e/bubble-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3197}"
SHOT_DIR="${SHOT_DIR:-__screenshots__/bubble}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-bubble-shots-$$}"

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
# 방을 열고(SSE 구독) 두 에이전트의 턴을 시작한다 — 진행 메모는 SSE 로만 흐르고 저장되지 않으므로 방을 연 뒤에 시드한다.
open_working() {
  ab open "$BASE_URL/rooms/$RID" >/dev/null
  ab wait '[data-testid="timeline"]' --timeout 20000 >/dev/null
  sleep 2
  apic "fetch('/api/v1/__mock/rooms/$RID/seed-working', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' }).then(r => r.json()).then(j => JSON.stringify(j.tasks.map(t => t.agent_id)))" > /tmp/bubble-agents.$$
  ab wait '[data-testid="working-bubble"] [data-testid="working-memo-line"]' --timeout 20000 >/dev/null
  sleep 2
}
to_bottom() { apic '(function(){var e=document.querySelector("[data-testid=timeline-end]");e.scrollIntoView({block:"end"});return "ok"})()' >/dev/null; sleep 1; }
expand() { apic "(function(){var b=document.querySelectorAll('[data-testid=working-bubble]')[$1];b.querySelector('[data-testid=working-fold]').click();return 'ok'})()" >/dev/null; sleep 1; }
common_asserts() {
  assert_js 'document.querySelectorAll("[data-testid=working-bubble]").length === 2' "말풍선 둘(에이전트마다 하나)"
  assert_js 'document.querySelector("[data-testid=message-delta]") === null && document.querySelector("[data-testid=working-row]") === null' "옛 작성 중 블록 · 옛 작업 중 줄 0"
  assert_js '!document.body.innerText.includes("작성 중…")' "「작성 중…」 문구 0"
  assert_js '[...document.querySelectorAll("[data-testid=working-bubble]")].every((b) => b.querySelector(".convo__arrow, .convo__to") === null)' "받는 쪽(→) 없음"
  assert_js 'getComputedStyle(document.querySelector(".wbub__bubble")).borderTopStyle === "dashed"' "점선 테두리"
  assert_js 'document.querySelector("[data-testid=working-head]").getAttribute("aria-live") === "polite" && document.querySelector("[data-testid=working-memo-line]").closest("[aria-live]") === null' "aria-live 는 머리에만"
}
trap 'ab close >/dev/null 2>&1 || true; rm -f /tmp/bubble-agents.$$' EXIT
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
    { content: "@Lead 4인 밸런스가 1등으로 쏠립니다. 원인을 찾아 주세요." },
    { content: "밸런스 하네스부터 보겠습니다. BGM 은 Researcher 가 맡습니다.", agent: "Lead" },
  ] });
  return r.id;
})()')
echo "  room=$RID"

step "01 — 두 에이전트 동시 작업(라이트)"
open_working
to_bottom
common_asserts
assert_js 'document.querySelectorAll("[data-testid=fold-fail]").length >= 1' "Lead 실패 꼬리"
shot bubble-01-two-light

step "02 — Researcher 펼침(옛 데몬 폴백 문단)"
expand 1
assert_js 'document.querySelectorAll("[data-testid=working-bubble]")[1].querySelectorAll("[data-testid=working-memo-para]").length === 2' "조각 둘 = 문단 둘"
to_bottom
shot bubble-02-expanded-light

step "03·04 — 다크"
set_theme dark
open_working
to_bottom
common_asserts
shot bubble-03-two-dark
expand 0
assert_js 'document.querySelectorAll("[data-testid=working-bubble]")[0].querySelectorAll("[data-testid=working-memo-para]").length === 4' "Lead 조각 넷 = 문단 넷(빈 줄)"
apic '(function(){var b=document.querySelectorAll("[data-testid=working-bubble]")[0];window.scrollTo(0,Math.max(0,b.getBoundingClientRect().top+window.scrollY-120));var t=document.querySelector(".s7__timeline");if(t)t.scrollTop=Math.max(0,b.offsetTop-120);return "ok"})()' >/dev/null
sleep 1
shot bubble-04-expanded-dark

step "05 — 좁은 화면(라이트)"
set_theme light
ab set viewport 420 900 >/dev/null
open_working
to_bottom
assert_js 'document.querySelectorAll("[data-testid=working-bubble]").length === 2' "좁은 화면에도 말풍선 둘"
shot bubble-05-narrow-light

step "06 — Lead 게시 → 그 자리에 메시지"
ab set viewport 1280 900 >/dev/null
open_working
LEAD=$(python3 -c "import json;print(json.load(open('/tmp/bubble-agents.$$'))[0])")
apic "fetch('/api/v1/__mock/rooms/$RID/working-step', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ agent_id: '$LEAD', action: 'post' }) }).then(r => r.status)" >/dev/null
sleep 2
assert_js "document.querySelector('[data-testid=working-bubble][data-agent-id=\"$LEAD\"]') === null" "Lead 말풍선이 메시지로 바뀜"
assert_js 'document.querySelectorAll("[data-testid=working-bubble]").length === 1' "Researcher 말풍선은 그대로"
to_bottom
shot bubble-06-posted-light

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "== bubble-shots: 6장 ($SHOT_DIR) =="
