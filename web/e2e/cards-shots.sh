#!/usr/bin/env bash
# T-CARD-W 스크린샷 — 작업 카드(PRD FR-3.8 · SCREEN v0.19.15 §4.6 「작업 카드」·「분담표」 · COMPONENTS §9.13 · Pencil `S7-K 작업 카드`).
# `next build && next start`(목) 로 찍는다. 시드 `seed-cards` = C-1 수락 · C-2 자동 · C-3 판정 대기(근거 없음) · └C-4 · C-5 2판 · ‹질문›.
#
#   cards-01-delegation-light.png   C-3 위임 카드 — 머리 · 목표 · 완료 기준 + 확인 방법 칩 · 하지 않을 것(경계 막대) · 참고 칩 · 결과물·예산
#   cards-02-result-light.png       C-3 결과 카드 — 기준별 글리프 · 근거 링크 · 「부분 · 근거 없음」 · 확인함 · 가정함(경고색) · 벗어난 점 · 남은 문제 · 비용
#   cards-03-menu-light.png         C-3 결과 카드 「⋯」 — Director 에게 「수락」·「수정 요청…」
#   cards-04-accepted-light.png     C-1 결과 카드 — 수락 칩 · 판정 줄
#   cards-05-auto-light.png         C-2 자동 결과 카드 — 「자동」 칩 · 모든 기준 ✗
#   cards-06-revise-v2-light.png    C-5 2판 위임 카드 — 「수정 요청 · 사유」 · 지워진 참고 칩(흐림)
#   cards-07-question-light.png     카드 없는 에이전트 멘션 ‹질문›(Writer → @Researcher)과 ‹답›
#   cards-08-board-light.png        미션 칸 「개요 · 분담표」 — 트리(└C-4) · 판정 대기 칩 · k/N · 비용
#   cards-09-result-dark.png        02 다크 · cards-10-board-dark.png  08 다크
#   cards-11-narrow-700-dark.png    좁은 화면 700px — 결과 카드 전폭 · cards-12-narrow-board-light.png  좁은 화면 「미션」 탭의 분담표
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3231 &
#   BASE_URL=http://localhost:3231 SHOT_DIR=__screenshots__/cards bash e2e/cards-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3231}"
SHOT_DIR="${SHOT_DIR:-__screenshots__/cards}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-cards-shots-$$}"

ab() { agent-browser "$@"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
# macOS 에 coreutils timeout 이 없다 — N 초 뒤 죽이는 감싸개.
with_timeout() { local s=$1; shift; "$@" & local p=$!; ( sleep "$s"; kill "$p" 2>/dev/null ) 2>/dev/null & local k=$!; disown "$k" 2>/dev/null || true; wait "$p" 2>/dev/null; local rc=$?; kill "$k" 2>/dev/null || true; return $rc; }
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
# 새 방 + 미션 + 카드 시드. RID·WID 를 남긴다.
make_room() {
  local out
  out=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const r = await post(`/workspaces/${me.workspaces[0].id}/rooms`, { name: "마리오 카트" });
  const w = await post(`/rooms/${r.id}/works`, { goal: "마리오 카트 만들기", title: "마리오 카트" });
  const s = await post(`/__mock/rooms/${r.id}/seed-cards`, { work_id: w.id });
  return [r.id, w.id].join(" ");
})()')
  RID=${out% *}; WID=${out#* }
  echo "  room=$RID work=$WID"
}
open_room() {
  ab open "$BASE_URL/rooms/$RID?work=$WID" >/dev/null
  # 좁은 화면(≤1100px)은 `?work=` 로 들어오면 「미션」 탭이 먼저 열려 타임라인이 숨는다 — 탭 줄을 기다려 타임라인 탭으로.
  if [ "$(apic 'window.innerWidth <= 1100')" = "True" ]; then
    ab wait '[data-testid="tab-timeline"]' --timeout 20000 >/dev/null
    with_timeout 10 agent-browser click '[data-testid="tab-timeline"]' >/dev/null 2>&1 || true
    sleep 1
  fi
  ab wait '[data-testid="timeline"]' --timeout 20000 >/dev/null
  ab wait '[data-testid="result-card"] [data-testid="card-verdict"]' --timeout 20000 >/dev/null
  sleep 2
}
# 카드 번호(C-n)·역할의 말풍선을 화면 가운데로.
show() {
  apic "(function(){var a=[...document.querySelectorAll('[data-testid=$2]')].filter(function(e){return e.getAttribute('data-card-label')==='$1'});var e=a[a.length-1];e.scrollIntoView({block:'${3:-center}'});return 'ok'})()" >/dev/null
  sleep 1
}
# 카드 말풍선의 article(메뉴·말풍선 폭은 article 에 있다).
ART='function art(l,t){var a=[...document.querySelectorAll("[data-testid="+t+"][data-card-label="+l+"]")];var c=a[a.length-1];return c&&c.closest("article")}'
board_tab() { ab click '[data-testid="work-panel-tab-board"]' >/dev/null; sleep 1; }
trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 초기화 · 로그인(demo = 미션 Director) · 방 · 미션 · 카드 시드"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1440 960 >/dev/null
login demo@colab.dev
set_theme light
make_room
open_room

step "01 — 위임 카드(C-3)"
show C-3 task-card start
assert_js '(function(){var c=[...document.querySelectorAll("[data-testid=task-card][data-card-label=C-3]")][0];return !!c.querySelector("[data-testid=card-goal]") && c.querySelectorAll("[data-testid=card-method]").length===3 && !!c.querySelector(".tcard__bound") && c.querySelectorAll("[data-testid=card-ref]").length===3 && !!c.querySelector("[data-testid=card-output]")})()' "위임 카드 칸 전부"
shot cards-01-delegation-light

step "02 — 결과 카드(C-3 판정 대기 · 근거 없음)"
show C-3 result-card
assert_js 'document.querySelectorAll("[data-testid=result-card][data-card-label=C-3] [data-testid=card-downgraded]").length===1' "「부분 · 근거 없음」 한 줄"
assert_js '!!document.querySelector("[data-testid=result-card][data-card-label=C-3] [data-testid=card-assumed].tcard__row--warn")' "가정함은 경고색"
shot cards-02-result-light

step "03 — 「⋯」 메뉴(Director · 결과 제출)"
apic "(function(){$ART;art('C-3','result-card').querySelector('[data-testid=message-menu]').click();return 'ok'})()" >/dev/null
sleep 1
assert_js '!!document.querySelector("[data-testid=card-menu-accept]") && !!document.querySelector("[data-testid=card-menu-revise]")' "수락 · 수정 요청…"
shot cards-03-menu-light
ab press Escape >/dev/null || true

step "04 — 수락(C-1)"
show C-1 result-card
assert_js '/수락 · @Lead/.test(document.querySelector("[data-testid=result-card][data-card-label=C-1] [data-testid=card-judgement]").textContent)' "판정 줄"
shot cards-04-accepted-light

step "05 — 자동(C-2)"
show C-2 result-card
assert_js "(function(){$ART;return !!art('C-2','result-card').querySelector('[data-testid=card-auto]')})()" "「자동」 칩"
shot cards-05-auto-light

step "06 — 2판 수정 요청(C-5)"
show C-5 task-card
assert_js '(function(){var a=[...document.querySelectorAll("[data-testid=task-card][data-card-label=C-5]")];var v2=a[a.length-1];return a.length===2 && !!v2.querySelector("[data-testid=card-revise-reason]") && !!v2.querySelector("[data-testid=card-ref][data-missing=true]")})()' "1판·2판 두 말풍선 · 사유 · 지워진 칩"
shot cards-06-revise-v2-light

step "07 — ‹질문›"
apic '(function(){var e=[...document.querySelectorAll("[data-testid=speech-kind]")].filter(function(x){return x.textContent.includes("질문")});e[e.length-1].scrollIntoView({block:"center"});return "ok"})()' >/dev/null
sleep 1
assert_js '[...document.querySelectorAll("[data-testid=speech-kind]")].some(function(x){return x.textContent.includes("질문") && x.getAttribute("data-tone")==="block"})' "‹질문› block 톤"
shot cards-07-question-light

step "08 — 분담표"
board_tab
assert_js 'document.querySelectorAll("[data-testid=card-board-row]").length===5 && document.querySelector("[data-testid=card-board-row][data-depth=\"1\"]")!==null' "행 다섯 · 하위 카드 └"
shot cards-08-board-light
ab click '[data-testid="card-board-row"][data-depth="1"]' >/dev/null
sleep 1
assert_js '!!document.querySelector(".msg--flash")' "행을 누르면 말풍선 강조"

step "09·10 — 다크"
set_theme dark
open_room
show C-3 result-card
shot cards-09-result-dark
board_tab
shot cards-10-board-dark

step "11 — 좁은 화면 700px(다크)"
ab set viewport 700 1000 >/dev/null
make_room
open_room
show C-3 result-card
assert_js "(function(){$ART;var b=art('C-3','result-card').querySelector('.convo__bubble');"'var col=b.closest(".convo__col");return b.getBoundingClientRect().width >= col.getBoundingClientRect().width - 1})()' "≤720px 결과 카드 전폭"
shot cards-11-narrow-700-dark

step "12 — 좁은 화면 「미션」 탭의 분담표(라이트)"
set_theme light
ab set viewport 1000 900 >/dev/null
make_room
open_room
with_timeout 10 agent-browser click '[data-testid="tab-work"]' >/dev/null 2>&1 || true
sleep 1
board_tab
shot cards-12-narrow-board-light

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "== cards-shots: 12장 ($SHOT_DIR) =="
