#!/usr/bin/env bash
# T-W8 문구 스크린샷 — 목 API 위에서 세 화면을 찍는다(§8.4 반영 확인용).
#
#   p5-w8-01-runtimes.png   S11 연결된 컴퓨터 — 메뉴 한국어 · 능력 줄이 「자세히 보기」 안 · 행동 필요한 줄만 밖
#   p5-w8-02-sessions.png   S5 세션 목록 — 메뉴 · 제목 · 배지("진행 중 · N개 실행 중")
#   p5-w8-03-inbox.png      S8 받은 요청 — 메뉴 뱃지 · 항목 종류 라벨 · 부가 줄(사유가 사람의 말)
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3115 &
#   BASE_URL=http://localhost:3115 bash e2e/p5-w8-shots.sh
#
# agent-browser screenshot [selector] path [--full] — 전체 페이지 플래그는 `--full` 이고 **경로 뒤**다.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3115}"
SHOT_DIR="__screenshots__"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-p5-w8-shots-$$}"

ab() { agent-browser "$@"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }

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

# 로그인 안 된 능력 한 벌을 심는다 — 「자세히 보기」 밖에 남아야 할 줄이 실제로 밖에 서는지 보려면 필요하다.
step "런타임 시드 — 로그인 안 된 Hermes(도구 제한 불가)"
apic '
(async () => {
  const j = (r) => r.json();
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  await fetch(`/api/v1/workspaces/${ws}/runtimes`, { method: "POST" }).catch(() => {});
  await fetch("/api/v1/__mock/runtimes", {
    method: "POST", headers: { "content-type": "application/json" },
    body: JSON.stringify({
      name: "office-pc",
      capabilities: [{ kind: "hermes", version: "0.20.6", adapter_version: null, logged_in: false, models: [], protocol_version: 1, resume: false, usage: false, tool_disallow: false, brief_transport: "instruction_file", allow_once_missing: true }],
    }),
  }).then(j);
  return "ok";
})()' >/dev/null

# 인박스 시드는 **세션 하나를 전제**한다(항목이 세션을 참조한다) — 목록 컷도 빈 화면이 아니어야 하므로 먼저 만든다.
step "세션 · lane · 인박스 시드"
apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const ags = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const sess = await post(`/workspaces/${ws}/sessions`, {
    title: "결제 시장 조사", goal: "국내 B2B SaaS 결제 시장 조사 보고서 10페이지",
    isolation: { kind: "none" }, participants: ags.slice(0, 2).map((a) => ({ agent_id: a.id })),
    assignee_agent_id: ags[0].id,
  });
  await post(`/__mock/sessions/${sess.id}/seed-lanes`);
  await post(`/__mock/inbox/seed`);
  return "ok";
})()' >/dev/null

step "1/3 S11 연결된 컴퓨터"
ab open "$BASE_URL/runtimes" >/dev/null
ab wait '[data-testid="runtime-card"]' --timeout 20000 >/dev/null
shot_full "p5-w8-01-runtimes"

step "2/3 S5 세션 목록"
ab open "$BASE_URL/sessions" >/dev/null
ab wait '[data-testid="session-list"]' --timeout 20000 >/dev/null
shot_full "p5-w8-02-sessions"

step "3/3 S8 받은 요청"
ab open "$BASE_URL/inbox" >/dev/null
ab wait '[data-testid="inbox-list"]' --timeout 20000 >/dev/null
shot_full "p5-w8-03-inbox"

echo
echo "✅ T-W8 스크린샷 3장"
