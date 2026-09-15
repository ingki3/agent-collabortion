#!/usr/bin/env bash
# T-W7 스크린샷 6장 — 같은 화면 셋을 밝게/어둡게 한 장씩(COMPONENTS §8.2·§8.3).
#
#   p5-w7-01-sessions-light.png / -dark.png   S5 세션 목록  — 목록 행 제목(--fs-card) · 배지
#   p5-w7-02-runtimes-light.png / -dark.png   S11 런타임    — 카드 능력 목록 · 저장소 · 경고
#   p5-w7-03-inbox-light.png   / -dark.png    S8 인박스     — 심각도 배지 · soft 배경(밝음 12% / 어두움 20%)
#
# 테마는 설정 화면의 라디오와 같은 경로로 바꾼다 — localStorage + <html data-theme>.
# `ab emulate` 같은 것에 기대지 않는다: 실제 사용자가 고르는 길을 그대로 밟아야
# [data-theme] 쪽 규칙(=수동 지정)이 찍힌다.
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/w7-shots.sh
#
# agent-browser screenshot [selector] path [--full] — 전체 페이지 플래그는 `--full` 이고 **경로 뒤**다.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
SHOT_DIR="__screenshots__"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w7-shots-$$}"

ab() { agent-browser "$@"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }

# 테마를 심고 **다시 연다** — data-theme 는 첫 페인트에 layout.tsx 인라인 스크립트가 붙인다.
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
clear_theme() { apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'system'})()" >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"
ab set viewport 1400 950 >/dev/null

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인"
ab open "$BASE_URL/login" >/dev/null
ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
ab fill 'input[name="email"]' 'demo@colab.dev' >/dev/null
ab fill 'input[name="password"]' 'password123' >/dev/null
ab click 'button[type="submit"]' >/dev/null
ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null

step "시드 — 세션 하나(참여자 2) · 인박스 항목 · 런타임 저장소"
apic '
(async () => {
  const j = (r) => r.json();
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const s = await post(`/workspaces/${ws}/sessions`, {
    title: "결제 모듈 구현", goal: "Backend·Frontend 가 각자 구현하고 QA 가 리뷰한다",
    isolation: { kind: "none" }, runtime_id: rt.id,
    participants: [{ agent_id: ags.items[0].id }, { agent_id: ags.items[1].id }],
    assignee_agent_id: ags.items[0].id,
  });
  // 인박스 항목은 HITL 로 만든다 — 목에 seed-inbox 는 없다(seed-hitl / seed-lanes 뿐).
  await post(`/__mock/sessions/${s.id}/seed-lanes`, { statuses: ["running", "blocked", "done"] });
  await post(`/__mock/sessions/${s.id}/seed-hitl`, {});
  await post(`/__mock/sessions/${s.id}/seed-hitl`, { age_ms: 30 * 3600000 });
  return s.id;
})()' >/dev/null || echo "  (시드 일부 생략 — 목이 해당 씨앗을 모른다)"

for THEME in light dark; do
  step "테마 $THEME"
  set_theme "$THEME"

  ab open "$BASE_URL/sessions" >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
  shot_full "p5-w7-01-sessions-$THEME"

  ab open "$BASE_URL/runtimes" >/dev/null
  ab wait '[data-testid="runtime-card"]' --timeout 20000 >/dev/null
  shot_full "p5-w7-02-runtimes-$THEME"

  ab open "$BASE_URL/inbox" >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
  shot_full "p5-w7-03-inbox-$THEME"
done

step "덤 — 설정의 테마 선택(요구 6장 밖, 리뷰용)"
ab open "$BASE_URL/settings" >/dev/null
ab wait '[data-testid="theme-select"]' --timeout 20000 >/dev/null
shot_full "p5-w7-04-settings-theme"

step "설정 화면을 시스템 따름으로 되돌린다(다음 실행이 앞 실행의 선택을 물려받지 않게)"
clear_theme

echo
echo "✅ 스크린샷 6장 — $SHOT_DIR/p5-w7-*-{light,dark}.png"
