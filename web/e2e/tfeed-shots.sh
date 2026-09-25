#!/usr/bin/env bash
# T-FEED 스크린샷 — S7 「작업 과정」 펼침의 「진행 중…」(끝난 턴). `next build && next start` 로 찍는다(목 모드는 빌드 시점 플래그).
#
#   tfeed-<TAG>-s7-process.png   세 층 시드(seed-layers)의 조사 결과 메시지 · 제출 알림 — 「작업 과정」 두 개를 펼친 모습.
#                                조사 결과 턴의 첫 줄은 runtime/start → started(실서버 모양), 제출 알림 턴에는 짝 없는 도구 started 가 하나 있다.
#                                둘 다 **끝난 턴**이다 — 고친 뒤에는 「진행 중…」이 없어야 한다(짝 없는 도구 줄은 「결과 없음」).
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3196 &
#   BASE_URL=http://localhost:3196 SHOT_DIR=/tmp/shots TAG=after bash e2e/tfeed-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3196}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
TAG="${TAG:-after}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-tfeed-shots-$$}"

ab() { agent-browser "$@"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
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

curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 1900 >/dev/null
login demo@colab.dev
RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const r = await post(`/workspaces/${me.workspaces[0].id}/rooms`, { name: "마리오 카트" });
  await post(`/__mock/rooms/${r.id}/seed`, { agents: ["Researcher", "Lead"] });
  await post(`/__mock/rooms/${r.id}/seed-layers`, {});
  return r.id;
})()')
echo "  room=$RID"
apic "(function(){try{localStorage.setItem('colab.theme','light');localStorage.removeItem('colab.timelineView.$RID')}catch(e){};return 'ok'})()" >/dev/null
ab open "$BASE_URL/rooms/$RID" >/dev/null
ab wait '[data-testid="process-fold"] [data-testid="fold-fail"]' --timeout 20000 >/dev/null
sleep 1
# 조사 결과(첫 세 층 메시지)와 제출 알림(실패 꼬리가 있는 것)의 「작업 과정」을 연다.
apic '(function(){var f=[...document.querySelectorAll("[data-testid=process-fold]")];var a=f[0];var b=f.find(function(x){return x.querySelector("[data-testid=fold-fail]")});a.click();if(b&&b!==a)b.click();return f.length})()' >/dev/null
sleep 1
PENDING=$(apic 'document.querySelectorAll("[data-testid=process-body] [data-testid=feed-pending]").length')
UNRES=$(apic 'document.querySelectorAll("[data-testid=process-body] [data-testid=feed-unresolved]").length')
echo "  feed-pending=$PENDING feed-unresolved=$UNRES"
apic '(function(){var t=document.querySelector("[data-testid=process-body]");window.scrollTo(0,Math.max(0,t.getBoundingClientRect().top+window.scrollY-260));return "ok"})()' >/dev/null
sleep 1
ab screenshot "$SHOT_DIR/tfeed-$TAG-s7-process.png" >/dev/null
echo "  📸 $SHOT_DIR/tfeed-$TAG-s7-process.png"
apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
