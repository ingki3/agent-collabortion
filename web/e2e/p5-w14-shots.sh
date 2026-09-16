#!/usr/bin/env bash
# T-W14 스크린샷 — S7 메시지 본문 마크다운(PRD FR-3.1, Director 지적 2026-09-15 · W-12). `next build && next start` 로 찍는다
# (PR #186 NN5 — 개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다(PR #188 NN3).
#
#   p5-w14-01-s7-markdown-{light,dark}.png   S7 타임라인 — 첫 답변(목록·굵게·인라인 코드) + `seed-markdown` 의 text(제목·중첩 목록·표·인용·
#                                            코드 블록·링크·멘션 칩·HTML 원문) + summary(W-12) 카드. 전체 페이지.
#   p5-w14-02-s7-delta-{light,dark}.png      「작성 중…」 델타(`seed-delta` 스냅숏) — 닫히지 않은 코드 펜스가 열린 채로 그려지고 커서가 붙는다.
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/p5-w14-shots.sh
# ⚠ 개발 서버와 포트·빌드를 분리할 것(PR #191 NN5). SHOT_DIR 로 저장 위치를 바꿀 수 있다(기본 __screenshots__).
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w14-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
clear_theme() { apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'system'})()" >/dev/null; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }
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

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

step "시드 — 세션 하나 + 마크다운 메시지(text · summary)"
SID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const a = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const s = await post(`/workspaces/${ws}/sessions`, {
    title: "국내 B2B SaaS 결제 시장 조사", goal: "보고서 10페이지 — 상위 5개 사업자 비교", isolation: { kind: "none" }, runtime_id: rt.id,
    participants: a.slice(0, 2).map((x) => ({ agent_id: x.id })), assignee_agent_id: a[0].id,
  });
  await new Promise((r) => setTimeout(r, 3500)); // 첫 답변(목의 simulateRun)이 게시될 때까지
  await post(`/__mock/sessions/${s.id}/seed-markdown`, {});
  return s.id;
})()')
echo "  session=$SID"

for THEME in light dark; do
  step "테마 $THEME — S7 마크다운 메시지(세로로 긴 뷰포트 — 타임라인은 안쪽 스크롤이라 --full 로는 다 안 잡힌다)"
  set_theme "$THEME"
  ab set viewport 1280 1750 >/dev/null
  open_wait "/sessions/$SID" '[data-testid="message-card"][data-kind="summary"]'
  ab wait '.msg__body .md-table' --timeout 10000 >/dev/null
  shot "p5-w14-01-s7-markdown-$THEME"
done

step "「작성 중…」 델타 — 열린 코드 펜스(밝음·어두움)"
ab set viewport 1280 900 >/dev/null
for THEME in light dark; do
  set_theme "$THEME"
  open_wait "/sessions/$SID" '[data-testid="message-card"][data-kind="summary"]'
  # 목이 델타 스냅숏 하나를 SSE 로 흘린다(게시 없음) — 열린 펜스가 코드 상자로, 끝에 커서.
  apic "fetch('/api/v1/__mock/sessions/$SID/seed-delta', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' }).then(r => r.status)" >/dev/null
  ab wait '[data-testid="message-delta"] pre[data-open="true"]' --timeout 10000 >/dev/null
  # W-18: 화면의 자동 스크롤이 작성창 높이만큼 scroll-margin 을 두고 내리므로 델타 카드가 작성창 뒤에 숨지 않는다 — 우회(window.scrollTo) 없이
  # 그대로 찍고, 델타 카드의 아래변이 작성창의 윗변보다 위에 있는지 **잰다**(단언). 렌더 뒤 한 프레임 기다린다.
  sleep 1
  GAP=$(apic '(function(){var d=document.querySelector("[data-testid=message-delta]").getBoundingClientRect();var c=document.querySelector(".s7__composer").getBoundingClientRect();return Math.round(c.top-d.bottom)})()')
  echo "  델타 카드 아래변 ↔ 작성창 윗변: ${GAP}px"
  [ "$GAP" -ge 0 ] || { echo "❌ 델타 카드가 작성창 뒤에 ${GAP}px 숨어 있다(W-18)"; exit 1; }
  shot "p5-w14-02-s7-delta-$THEME"
done

step "테마를 시스템 따름으로 되돌린다"
clear_theme

echo
echo "✅ 스크린샷 — $SHOT_DIR/p5-w14-*.png"
