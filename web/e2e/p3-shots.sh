#!/usr/bin/env bash
# T-W3 스크린샷 4장 — 목 API 위에서 찍는다(에이전트 턴은 목이 흉내 낸다).
#
#   p3-w3-01-inbox.png          S8 인박스 — 7종 · 심각도 · overdue 최상단 · Needs Action 필터
#   p3-w3-02-hitl-card.png      HITL 카드 **두 자리** — 타임라인(S7)과 Inbox Item 이 같은 하위 컴포넌트를 쓴다
#   p3-w3-03-paused-banner.png  paused 배너(사유별) + lane 카드
#   p3-w3-04-s7-deputy.png      S7-D — 상단 액션 비활성 + HITL 버튼 🔒 HH:MM부터
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3113 &
#   BASE_URL=http://localhost:3113 bash e2e/p3-shots.sh
#
# agent-browser screenshot [selector] path [--full] — 전체 페이지 플래그는 `--full` 이고 **경로 뒤**다.
# `--full-page` 는 없는 옵션이라 경로로 해석돼 `web/--full-page` 파일이 생긴다(PR #21 리뷰 R3).
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3113}"
SHOT_DIR="__screenshots__"
PAUSE_REASON="${PAUSE_REASON:-budget}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-p3-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
# 페이지 안에서 목 API 를 부른다 — 브라우저 세션 쿠키를 그대로 쓰기 위해서다.
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"
ab set viewport 1400 950 >/dev/null

# 목 저장소를 비우고 시작한다 — 재실행마다 항목이 쌓이면 스크린샷이 목록이 아니라 로그가 된다.
# **로그인보다 먼저** 해야 한다: reset 은 쿠키 표까지 새로 만든다.
step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인"
ab open "$BASE_URL/login" >/dev/null
ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
ab fill 'input[name="email"]' 'demo@colab.dev' >/dev/null
ab fill 'input[name="password"]' 'password123' >/dev/null
ab click 'button[type="submit"]' >/dev/null
ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null

step "세션 + HITL + 인박스 시드"
SID=$(apic '
(async () => {
  const j = (r) => r.json();
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const s = await post(`/workspaces/${ws}/sessions`, {
    title: "국내 B2B SaaS 결제 시장 조사", goal: "결제 시장 보고서 10페이지", isolation: { kind: "none" },
    participants: [{ agent_id: ags.items[0].id }, { agent_id: ags.items[1].id }], assignee_agent_id: ags.items[0].id,
  });
  await post(`/__mock/sessions/${s.id}/seed-lanes`);
  await post(`/__mock/inbox/seed`);
  // 기한이 지난 질문 하나(인박스 최상단) + 아직 남은 승인 하나
  await post(`/__mock/sessions/${s.id}/seed-hitl`, { age_ms: 30 * 3600000 });
  await post(`/__mock/sessions/${s.id}/seed-hitl`, {
    type: "approval", proposed_default: null, agent_id: ags.items[1].id,
    question: "보고서 초안을 승인해 주세요", context: "승인 대상: 보고서.pdf",
  });
  return s.id;
})()')
echo "  session=$SID"

step "1/4 S8 인박스"
ab open "$BASE_URL/inbox" >/dev/null
ab wait '[data-testid="inbox-list"]' --timeout 20000 >/dev/null
shot_full "p3-w3-01-inbox"

step "2/4 HITL 카드 두 자리(타임라인 · 인박스)"
ab open "$BASE_URL/dev/components" >/dev/null
ab wait '[data-testid="story-hitl-both"]' --timeout 20000 >/dev/null
ab scrollintoview '[data-testid="story-hitl-both"]' >/dev/null
# 요소 스크린샷 — 타임라인 카드 9종과 Inbox Item 7종이 한 컷에 들어가야 "같은 본문" 이 보인다.
ab screenshot '[data-testid="story-hitl-both"]' "$SHOT_DIR/p3-w3-02-hitl-card.png" >/dev/null
echo "  📸 $SHOT_DIR/p3-w3-02-hitl-card.png"

step "3/4 paused 배너 ($PAUSE_REASON) — S7 우열"
apic "fetch('/api/v1/__mock/sessions/$SID/pause', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ reason: '$PAUSE_REASON' }) }).then(r => r.status)"
ab open "$BASE_URL/sessions/$SID" >/dev/null
ab wait '[data-testid="paused-banner"]' --timeout 20000 >/dev/null
shot "p3-w3-03-paused-banner"

step "4/4 S7-D — deputy 변형(상단 액션 비활성 · HITL 🔒)"
apic "fetch('/api/v1/__mock/sessions/$SID/role', { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ role: 'deputy' }) }).then(r => r.status)"
ab open "$BASE_URL/sessions/$SID" >/dev/null
ab wait '[data-testid="session-actions"][data-role="deputy"]' --timeout 20000 >/dev/null
shot "p3-w3-04-s7-deputy"

echo
echo "✅ 스크린샷 4장 — $SHOT_DIR/p3-w3-*.png"
