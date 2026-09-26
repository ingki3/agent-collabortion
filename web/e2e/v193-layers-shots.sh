#!/usr/bin/env bash
# v0.19.3 스크린샷 — S7 에이전트 메시지 세 층(PRD FR-3.1.2 · SCREEN §4.6 · COMPONENTS §9.6·§9.7). `next build && next start` 로 찍는다
# (목 모드는 **빌드 시점 플래그**다 — COLAB_MOCK_API=1 로 빌드하지 않으면 /api/v1 이 실서버 프록시로 간다).
#
#   v193-01-s7-conversation.png   기본 「대화만」 — 작업 내용·작업 과정 접힌 줄, 아티팩트 참조 줄, 실패 꼬리, 「자동으로 접음」
#   v193-02-s7-detail-open.png    「작업 내용 펼침」 — 작업 내용만 전부 펼침(작업 과정은 닫힌 채)
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3193 &
#   BASE_URL=http://localhost:3193 SHOT_DIR=/tmp/shots bash e2e/v193-layers-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3193}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-v193-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
# 뷰포트 밖 요소의 클릭은 조용히 실패한다 — 버튼은 JS 로 누른다.
jsclick() { apic "(function(){var e=document.querySelector('$1');if(!e)return 'missing';e.click();return 'ok'})()"; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 로그인"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 1600 >/dev/null
login demo@colab.dev

step "시드 — 방 하나 + 에이전트 둘 + 세 층 메시지 넷(seed-layers)"
RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const r = await post(`/workspaces/${ws}/rooms`, { name: "결제팀" });
  await post(`/__mock/rooms/${r.id}/seed`, { agents: ["Researcher", "Lead"] });
  await post(`/__mock/rooms/${r.id}/seed-layers`, {});
  return r.id;
})()')
echo "  room=$RID"
# 방마다 기억하는 보기를 비운다(기본 「대화만」에서 시작).
apic "(function(){try{localStorage.removeItem('colab.timelineView.$RID')}catch(e){};return 'ok'})()" >/dev/null

top_of_timeline() { apic '(function(){var t=document.querySelector("[data-testid=timeline-view]");window.scrollTo(0,Math.max(0,t.getBoundingClientRect().top+window.scrollY-120));return "ok"})()' >/dev/null; sleep 1; }

# 밝은 테마로 고정(Pencil 기준 PNG 와 같은 면) — 설정 화면과 같은 경로(localStorage colab.theme).
apic "(function(){try{localStorage.setItem('colab.theme','light')}catch(e){};return 'ok'})()" >/dev/null

step "대화만(기본)"
open_wait "/rooms/$RID" '[data-testid="process-fold"] [data-testid="fold-fail"]'
ab wait '[data-testid="artifact-ref"]' --timeout 10000 >/dev/null
[ "$(apic 'document.querySelectorAll("[data-testid=detail-body]").length')" = "0" ] || { echo "❌ 기본인데 작업 내용이 펼쳐져 있다"; exit 1; }
top_of_timeline
shot "v193-01-s7-conversation"

step "작업 내용 펼침"
jsclick '[data-testid="view-detail"]' >/dev/null
ab wait '[data-testid="detail-body"]' --timeout 10000 >/dev/null
[ "$(apic 'document.querySelectorAll("[data-testid=process-body]").length')" = "0" ] || { echo "❌ 전환이 작업 과정까지 열었다"; exit 1; }
top_of_timeline
shot "v193-02-s7-detail-open"

apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
echo
echo "✅ 스크린샷 — $SHOT_DIR/v193-*.png"
