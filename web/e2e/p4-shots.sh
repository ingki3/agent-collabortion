#!/usr/bin/env bash
# T-W5 스크린샷 — 목 API 위에서 찍는다(에이전트 턴은 목이 흉내 낸다).
#
#   p4-w5-01-s6-isolation.png   S6 4단계 — 격리가 런타임 후보를 제한한다("자동 선택" 비활성 + 사유, 비후보 + 사유)
#   p4-w5-05-s6-repo-check.png  S6 3단계 — 저장소 검증 결과(경로·git·클린·브랜치·remote) + 워크트리 바인딩 안내
#   p4-w5-02-s11-runtimes.png   S11 — 오프라인 유예 잔여/만료 · 저장소 remote URL · 삭제 409(걸린 세션 목록)
#   p4-w5-03-s13-workdirs.png   S13 — workdir 목록 · 용량 사용률 · GC 차단 사유 두 갈래
#   p4-w5-04-s17-rebind.png     S17 — 재바인딩 다이얼로그(후보 사유 · 유실 경고 · 아티팩트 순서)
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/p4-shots.sh
#
# agent-browser screenshot [selector] path [--full] — 전체 페이지 플래그는 `--full` 이고 **경로 뒤**다.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
SHOT_DIR="__screenshots__"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-p4-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
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

step "머신 셋 — 같은 remote(후보) · 다른 remote(비후보)"
apic '
(async () => {
  const j = (r) => r.json();
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  await post(`/__mock/runtimes`, { name: "desktop", repos: [{ path: "/srv/checkouts/colab", remote_url: rt.repos[0].remote_url, branch: "main", clean: true }] });
  await post(`/__mock/runtimes`, { name: "office-pc", repos: [{ path: rt.repos[0].path, remote_url: "git@github.com:someone/else.git", branch: "main", clean: true }] });
  return "ok";
})()' >/dev/null

step "1/5 S6 3단계 — 저장소 검증 결과(데몬 probe)"
ab open "$BASE_URL/sessions/new" >/dev/null
ab wait '[data-testid="session-wizard"]' --timeout 20000 >/dev/null
ab fill '[data-testid="session-title"]' '결제 모듈 구현' >/dev/null
ab fill '[data-testid="session-goal"]' 'Backend·Frontend 가 각자 워크트리에서 구현하고 QA 가 리뷰한다' >/dev/null
ab click '[data-testid="wizard-next"]' >/dev/null   # 2 Director
ab click '[data-testid="wizard-next"]' >/dev/null   # 3 격리
ab wait '[data-testid="wizard-isolation"]' --timeout 10000 >/dev/null
ab click '[data-testid="isolation-worktree"] input' >/dev/null
REPO=$(apic 'fetch("/api/v1/me").then(r=>r.json()).then(m=>fetch(`/api/v1/workspaces/${m.workspaces[0].id}/runtimes`)).then(r=>r.json()).then(d=>d[0].repos[0].path)')
ab select '[data-testid="repo-select"]' "$REPO" >/dev/null
ab wait '[data-testid="repo-check"]' --timeout 10000 >/dev/null
shot_full "p4-w5-05-s6-repo-check"

step "2/5 S6 4단계 — 격리가 런타임 후보를 제한한다(자동 선택 비활성 + 사유)"
ab click '[data-testid="wizard-next"]' >/dev/null   # 4 런타임
ab wait '[data-testid="runtime-auto"][data-allowed="false"]' --timeout 10000 >/dev/null
shot_full "p4-w5-01-s6-isolation"

step "세션·workdir·아티팩트 시드 + 런타임 오프라인 8일"
SID=$(apic '
(async () => {
  const j = (r) => r.json();
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rts = await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j);
  const rt = rts[0];
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const s = await post(`/workspaces/${ws}/sessions`, {
    title: "결제 모듈 구현", goal: "Backend·Frontend 병렬 구현 후 QA 리뷰",
    isolation: { kind: "worktree", repo_path: rt.repos[0].path, remote_url: rt.repos[0].remote_url },
    runtime_id: rt.id,
    participants: [{ agent_id: ags.items[0].id }, { agent_id: ags.items[1].id }], assignee_agent_id: ags.items[0].id,
  });
  await post(`/__mock/sessions/${s.id}/seed-workdirs`);
  await post(`/__mock/sessions/${s.id}/seed-artifacts`, { count: 3, type: "diff" });
  await post(`/__mock/runtimes/${rt.id}/offline`, { days: 8 });
  return s.id + "|" + rt.id;
})()')
RT="${SID##*|}"; SID="${SID%%|*}"
echo "  session=$SID runtime=$RT"

step "3/5 S11 — 유예 만료 · 삭제 409(걸린 세션)"
ab open "$BASE_URL/runtimes" >/dev/null
ab wait '[data-testid="runtime-card"]' --timeout 20000 >/dev/null
ab click '[data-testid="runtime-delete"]' >/dev/null
ab wait '[data-testid="runtime-delete-blocked"]' --timeout 10000 >/dev/null
ab click '[data-testid="runtime-sessions-toggle"]' >/dev/null
ab wait '[data-testid="runtime-sessions"]' --timeout 10000 >/dev/null
shot_full "p4-w5-02-s11-runtimes"

step "4/5 S13 — workdir 목록 · 차단 사유"
ab open "$BASE_URL/runtimes/$RT/workdirs" >/dev/null
ab wait '[data-testid="workdir-list"]' --timeout 20000 >/dev/null
# **차단된 행**을 눌러 409 → 사유 + 확인 버튼까지 한 컷에 담는다(클린한 행을 누르면 그냥 지워진다).
ab click '[data-gc-reason="unmerged_commits"] [data-testid="workdir-delete"]' >/dev/null
ab wait '[data-testid="workdir-refused"]' --timeout 10000 >/dev/null
shot_full "p4-w5-03-s13-workdirs"

step "5/5 S17 — 재바인딩 다이얼로그"
ab open "$BASE_URL/sessions/$SID" >/dev/null
ab wait '[data-testid="paused-banner"]' --timeout 20000 >/dev/null
ab click '[data-testid="paused-rebind"]' >/dev/null
ab wait '[data-testid="rebind-dialog"]' --timeout 10000 >/dev/null
shot "p4-w5-04-s17-rebind"

echo
echo "✅ 스크린샷 — $SHOT_DIR/p4-w5-*.png (요구 4장 + S6 저장소 검증 1장)"
