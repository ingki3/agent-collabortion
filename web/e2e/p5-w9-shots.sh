#!/usr/bin/env bash
# T-W9 스크린샷 — 레이아웃·대비 마무리(COMPONENTS §8.5). **`next build && next start` 로 찍는다**(PR #186 NN5 —
# 개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다(PR #188 NN3).
#
#   p5-w9-{01..05}-<화면>-{light,dark}.png   다섯 화면 × 밝음·어두움, 1280×900 (Director 의 화면 폭)
#     01 sessions · 02 inbox · 03 agents · 04 computers · 05 settings
#   p5-w9-1920-<화면>.png                    1920×1000 — 카드 목록이 열을 늘려 채우는지(세션·받은 요청·에이전트·컴퓨터)
#   p5-w9-06-computers-member.png            멤버 계정 — 「컴퓨터 연결」 비활성 사유가 버튼 아래에
#   p5-w9-07-sessions-no-runtime.png         온라인 컴퓨터 0 — 「새 세션」 비활성 사유가 버튼 아래에(빈 상태 카드와 함께)
#   p5-w9-08-session-{light,dark}.png        S7 세션 상세(3열 — 작업 줄기 보드·타임라인·정보) — 상단 바 제거 뒤 가장 복잡한 화면(PR #191 NN3)
#   p5-w9-09-session-paused-member.png       멤버 계정 · 예산 일시정지 — 「계속 진행 승인」 비활성 사유가 버튼 옆 화면 텍스트로(PR #191 NN1)
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3119 &
#   BASE_URL=http://localhost:3119 bash e2e/p5-w9-shots.sh
#
# ⚠ 개발 서버와 포트·빌드를 분리할 것(PR #191 NN5): `next build` 는 `.next/` 를 통째로 다시 쓰므로 **같은 워크트리에서
#   `next dev` 가 돌고 있으면 그 서버가 500 이 된다**(개발 캐시와 프로덕션 산출물이 섞인다). 순서는 — (1) 돌고 있는
#   `next dev` 를 먼저 끝내거나(pid 로, pkill 금지) 다른 워크트리에서 돌리고, (2) `next build`, (3) `next start` 는 개발
#   서버와 **다른 포트**(:3119 처럼)로. 이 스크립트가 붙는 BASE_URL 은 (3) 의 포트다. 촬영 뒤 다시 `next dev` 를 띄우면 된다.
#   SHOT_DIR 로 저장 위치를 바꿀 수 있다(기본 __screenshots__) — 일부만 갱신할 때 임시 폴더에 찍고 골라 옮긴다.
# agent-browser screenshot [selector] path [--full] — 전체 페이지 플래그는 `--full` 이고 **경로 뒤**다.
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3119}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w9-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
clear_theme() { apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'system'})()" >/dev/null; }
# 한 번 더 기다린다 — 권한에 따라 늦게 붙는 요소(비활성 사유 등)는 하이드레이션 직후의 wait 가 가끔 놓친다(T-W10 에서 관측).
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }

login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
logout() { apic '(async()=>{await fetch("/api/v1/auth/logout",{method:"POST"}).catch(()=>{});return "ok"})()' >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인(소유자)"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

# 열이 늘어나는 것을 보려면 카드가 여럿이어야 한다 — 컴퓨터 4대(하나는 오프라인) · 에이전트 5(팀 템플릿) · 세션 4 · 인박스 5.
step "시드 — 컴퓨터 4 · 에이전트 5 · 세션 4 · 받은 요청"
# 결과 "<컴퓨터 수>,<에이전트 수>,<s1 id>,<s3 id>" — S7 촬영에 s1(작업 줄기 3 + 확인 요청)·s3(예산 일시정지)을 쓴다.
SEED=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rts = [];
  for (const [name, repos] of [["office-pc", [{ path: "/work/colab", remote_url: "git@github.com:acme/colab.git", clean: true }]], ["build-box", []], ["laptop-old", []]]) {
    rts.push(await post("/__mock/runtimes", { name, repos }));
  }
  await post(`/__mock/runtimes/${rts[2].id}/offline`, { days: 2 });
  const tpls = await fetch(`/api/v1/workspaces/${ws}/agent-templates`).then(j).catch(() => null);
  if (Array.isArray(tpls) && tpls[0]) await post(`/workspaces/${ws}/agent-templates/${tpls[0].key}/apply`).catch(() => null);
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const a = ags.items;
  const mk = (title, goal, participants) => post(`/workspaces/${ws}/sessions`, {
    title, goal, isolation: { kind: "none" }, runtime_id: rt.id,
    participants: participants.map((x) => ({ agent_id: x.id })), assignee_agent_id: participants[0].id,
  });
  const s1 = await mk("결제 모듈 구현", "Backend·Frontend 가 각자 구현하고 QA 가 리뷰한다", [a[0], a[1]]);
  const s2 = await mk("국내 B2B SaaS 결제 시장 조사", "보고서 10페이지 — 상위 5개 사업자 비교", [a[1]]);
  const s3 = await mk("온보딩 문서 정리", "설치 안내를 한 페이지로 줄인다", [a[0]]);
  const s4 = await mk("주간 리뷰 요약", "지난주 PR 리뷰 코멘트를 항목별로 모은다", [a[0], a[1]]);
  await post(`/__mock/sessions/${s1.id}/seed-lanes`, { statuses: ["running", "blocked", "done"] });
  await post(`/__mock/sessions/${s1.id}/seed-hitl`, {});
  await post(`/__mock/sessions/${s2.id}/seed-lanes`, { statuses: ["running", "running"] });
  await post(`/__mock/sessions/${s3.id}/pause`, { reason: "budget" }).catch(() => null);
  await post("/__mock/inbox/seed", {});
  return [rts.length, a.length, s1.id, s3.id].join(",");
})()')
echo "  seed: $SEED"
S1=$(echo "$SEED" | cut -d, -f3)
S3=$(echo "$SEED" | cut -d, -f4)

SCREENS=("01-sessions:/sessions:[data-testid=\"session-row\"]"
         "02-inbox:/inbox:[data-testid=\"inbox-list\"]"
         "03-agents:/agents:[data-testid=\"agent-card\"]"
         "04-computers:/runtimes:[data-testid=\"runtime-card\"]"
         "05-settings:/settings:[data-testid=\"theme-select\"]")

for THEME in light dark; do
  step "1280 × 테마 $THEME — 다섯 화면 + S7 세션 상세"
  set_theme "$THEME"
  for spec in "${SCREENS[@]}"; do
    IFS=: read -r name path sel <<<"$spec"
    open_wait "$path" "$sel"
    shot "p5-w9-$name-$THEME"
  done
  # S7 — 3열(작업 줄기 보드 · 타임라인 · 정보). 상단 바를 없앤 뒤 가장 복잡한 화면이라 증거에 넣는다(PR #191 NN3).
  open_wait "/sessions/$S1" '[data-testid="session-detail"]'
  ab wait '[data-testid="lane-card"]' --timeout 20000 >/dev/null || true
  shot "p5-w9-08-session-$THEME"
done

step "1920 — 카드 목록이 열을 채우는지(밝음)"
set_theme light
ab set viewport 1920 1000 >/dev/null
for spec in "${SCREENS[@]:0:4}"; do
  IFS=: read -r name path sel <<<"$spec"
  open_wait "$path" "$sel"
  shot "p5-w9-1920-${name#*-}"
done
ab set viewport 1280 900 >/dev/null

step "멤버 계정 — 「컴퓨터 연결」 비활성 사유"
logout
login seoyeon@colab.dev
set_theme light
open_wait /runtimes '[data-testid="add-computer-hint"]'
shot "p5-w9-06-computers-member"

step "멤버 계정 · 예산 일시정지 — 「계속 진행 승인」 비활성 사유가 화면 텍스트로(PR #191 NN1)"
open_wait "/sessions/$S3" '[data-testid="paused-why"]'
shot "p5-w9-09-session-paused-member"

step "온라인 컴퓨터 0 — 「새 세션」 비활성 사유"
logout
login demo@colab.dev
apic '
(async () => {
  const j = (r) => r.json();
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rts = await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j);
  for (const r of rts) if (r.status === "online") await fetch(`/api/v1/__mock/runtimes/${r.id}/offline`, { method: "POST", headers: { "content-type": "application/json" }, body: "{}" });
  return rts.length;
})()' >/dev/null
open_wait /sessions '[data-testid="new-session-hint"]'
shot "p5-w9-07-sessions-no-runtime"

step "테마를 시스템 따름으로 되돌린다"
clear_theme

echo
echo "✅ 스크린샷 — $SHOT_DIR/p5-w9-*.png"
