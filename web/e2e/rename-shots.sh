#!/usr/bin/env bash
# T-RENAME 스크린샷 — 방 이름·설명 바꾸기(PRD v0.19.4 FR-2.1.2 · SCREEN v0.19.5 §4.6·§4.11·§4.3 · COMPONENTS §9.9).
# `next build && next start`(목) 로 찍는다 — 개발 오버레이 배지가 없게. 다른 shots 스크립트와 같은 구조.
#
#   rename-01-s7-view.png     S7 방 머리 — 보기 상태. 권한자에게 이름 옆 ✎(hover·focus 때).
#   rename-02-s7-edit.png     S7 방 머리 — 편집 칸(이름과 같은 글자 크기) + 「저장」·「취소」 + 도움말 「방 이름은 1~200자입니다」.
#   rename-03-s7-error.png    S7 방 머리 — 비었을 때 「방 이름을 적어 주세요」 + 「저장」 비활성(테두리 빨강).
#   rename-04-s20-name.png    S20 방 설정 맨 위 「이름·설명」 묶음 — 두 칸 + 저장 + 영향 한 줄.
#   rename-05-s5-menu.png     S5 방 카드 「…」 메뉴 — 「이름 바꾸기」(권한자만) · 보관 · 삭제.
#   rename-06-s5-edit.png     S5 방 카드 — 「이름 바꾸기」를 누른 뒤 카드 이름 줄이 같은 편집 칸으로.
#   rename-07-s7-system.png   S7 타임라인 — 저장 뒤 시스템 메시지 「… 방 이름을 〈옛〉에서 〈새〉(으)로 바꿨습니다.」
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3196 &
#   BASE_URL=http://localhost:3196 SHOT_DIR=__screenshots__/rename bash e2e/rename-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3196}"
SHOT_DIR="${SHOT_DIR:-__screenshots__/rename}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-rename-shots-$$}"

ab() { agent-browser "$@"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
# 화면의 단언 — 거짓이면 멈춘다(틀린 상태를 찍지 않게).
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; exit 1; }; echo "  ✓ $2"; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 로그인(방장) · 방 하나"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 900 >/dev/null
login demo@colab.dev
apic "(function(){try{localStorage.setItem('colab.theme','light')}catch(e){};return 'ok'})()" >/dev/null
RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rts = await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j);
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const lead = (ags.items ?? []).find((a) => a.name === "Lead");
  const r = await post(`/__mock/workspaces/${ws}/seed-room`, {
    title: "STO 시장 리서치", goal: "토큰증권 시장을 조사한다", isolation: { kind: "none" },
    runtime_id: (Array.isArray(rts) ? rts : rts.items)[0].id, assignee_agent_id: lead.id, participants: [{ agent_id: lead.id }],
  });
  await fetch(`/api/v1/rooms/${r.id}`, { method: "PATCH", headers: { "content-type": "application/json" }, body: JSON.stringify({ description: "STO 관련 조사와 정리" }) });
  return r.id;
})()')
echo "  room=$RID"

step "01 — S7 방 머리 보기(✎)"
ab open "$BASE_URL/rooms/$RID" >/dev/null
ab wait '[data-testid="room-title"]' --timeout 20000 >/dev/null
sleep 1
assert_js 'document.querySelector("[data-testid=room-title-pencil]") !== null' "권한자에게 ✎ 가 있다"
assert_js 'document.querySelector("[data-testid=room-title-pencil]").getAttribute("aria-label") === "방 이름 바꾸기"' "✎ aria-label"
# hover 상태로 보이게 — 실제로는 마우스를 올려야 나타난다(포커스도 같다).
apic '(function(){document.querySelector("[data-testid=room-title-pencil]").focus();return "ok"})()' >/dev/null
sleep 1
shot rename-01-s7-view

step "02 — S7 편집 칸"
# 들어가는 길 둘 중 이름 글자를 누른다(§4.6 「이름 글자를 누르거나 ✎ 를 누른다」) — ✎ 는 hover 전 opacity 0 이라
# 자동화의 보이기 검사에 걸린다(사람은 마우스를 올린 뒤 누르므로 문제가 없다).
ab click '[data-testid="room-title-text"]' >/dev/null
ab wait '[data-testid="room-title-input"]' --timeout 20000 >/dev/null
ab fill '[data-testid="room-title-input"]' 'STO·토큰증권 리서치' >/dev/null
sleep 1
assert_js 'document.querySelector("[data-testid=room-title-input]").getAttribute("aria-label") === "방 이름"' "입력 칸 aria-label"
assert_js 'document.getElementById(document.querySelector("[data-testid=room-title-input]").getAttribute("aria-describedby")).textContent === "방 이름은 1~200자입니다"' "도움말 한 줄"
shot rename-02-s7-edit

step "03 — S7 오류(비었을 때 — 공백만도 빈 이름이다)"
ab fill '[data-testid="room-title-input"]' ' ' >/dev/null
sleep 1
assert_js 'document.querySelector("[data-testid=room-title-help]").textContent === "방 이름을 적어 주세요"' "빈 이름 문장"
assert_js 'document.querySelector("[data-testid=room-title-save]").disabled === true' "「저장」 비활성"
shot rename-03-s7-error

step "07 — 저장 → 타임라인 시스템 메시지"
ab fill '[data-testid="room-title-input"]' 'STO·토큰증권 리서치' >/dev/null
ab click '[data-testid="room-title-save"]' >/dev/null
sleep 2
assert_js 'document.querySelector("[data-testid=room-title-text]").textContent === "STO·토큰증권 리서치"' "방 머리가 새 이름으로"
assert_js '[...document.querySelectorAll("[data-testid=timeline] *")].some((e) => e.childElementCount === 0 && e.textContent.includes("방 이름을 STO 시장 리서치에서 STO·토큰증권 리서치로 바꿨습니다"))' "시스템 메시지 문장"
apic '(function(){var xs=[...document.querySelectorAll("[data-testid=timeline] *")].filter((e)=>e.childElementCount===0&&e.textContent.includes("방 이름을"));if(!xs.length)return "none";var r=xs[xs.length-1].getBoundingClientRect();window.scrollTo(0,Math.max(0,r.top+window.scrollY-320));return "ok"})()' >/dev/null
sleep 1
shot rename-07-s7-system

step "04 — S20 「이름·설명」 묶음"
ab open "$BASE_URL/rooms/$RID/settings" >/dev/null
ab wait '[data-testid="rd-settings-group-name"]' --timeout 20000 >/dev/null
sleep 1
assert_js 'document.querySelector(".rd-group") === document.querySelector("[data-testid=rd-settings-group-name]")' "맨 위 묶음이다"
assert_js 'document.querySelector("[data-testid=rd-settings-save-name]").getAttribute("aria-disabled") === "true"' "바뀐 칸이 없으면 「저장」 비활성"
ab fill '[data-testid="rd-settings-description"]' 'STO·토큰증권 전반의 조사와 정리' >/dev/null
sleep 1
assert_js 'document.querySelector("[data-testid=rd-settings-save-name]").getAttribute("aria-disabled") !== "true"' "설명을 고치면 「저장」 활성"
shot rename-04-s20-name

step "05·06 — S5 카드 「…」 「이름 바꾸기」"
ab open "$BASE_URL/rooms" >/dev/null
ab wait '[data-testid="room-list"]' --timeout 20000 >/dev/null
sleep 1
apic "(function(){var c=document.querySelector('[data-room-id=\"$RID\"] [data-testid=room-menu-button]');c.click();return 'ok'})()" >/dev/null
sleep 1
assert_js "document.querySelector('[data-room-id=\"$RID\"] [data-testid=room-menu-rename]') !== null" "권한자 카드에 「이름 바꾸기」"
assert_js "document.querySelector('[data-room-id=\"$RID\"] [data-testid=room-menu-rename]').textContent === '이름 바꾸기'" "항목 문구"
shot rename-05-s5-menu
apic "(function(){document.querySelector('[data-room-id=\"$RID\"] [data-testid=room-menu-rename]').click();return 'ok'})()" >/dev/null
sleep 1
assert_js "document.querySelector('[data-room-id=\"$RID\"] [data-testid=room-rename-input]') !== null" "카드 이름 줄이 편집 칸으로"
assert_js "document.querySelector('[data-room-id=\"$RID\"] [data-testid=room-name]') === null" "이름 글자는 그동안 없다"
shot rename-06-s5-edit

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "== rename-shots: 7장 ($SHOT_DIR) =="
