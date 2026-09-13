#!/usr/bin/env bash
# T-W6 스크린샷 — S14 설정 8탭 + 대시보드 · S10 시험 대화 · W-10. **`next build && next start` 로 찍는다**(PR #186 NN5 —
# 개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다(PR #188 NN3).
#
#   p5-w6-01-settings-members-{light,dark}.png     멤버 탭 — 목록·역할·초대 링크
#   p5-w6-02-settings-loop-{light,dark}.png        루프 상한 탭 — 기본값 + 영향 한 줄(U14-1), 값 하나를 바꾼 상태(저장 활성)
#   p5-w6-03-settings-workdir-{light,dark}.png     작업 폴더 탭 — 보존 3일로 바꾼 뒤 영향 문장(U14-2)
#   p5-w6-04-settings-security-{light,dark}.png    보안 탭(owner) — 마스킹 + 영향(U14-3)
#   p5-w6-05-settings-notifications-{light,dark}.png 알림 탭(개인) — 구독 기본값(U14-4)
#   p5-w6-06-dashboard-{light,dark}.png            대시보드 — 10행 · 아직 잴 수 없음 · 판정 글리프 · breakdown
#   p5-w6-07-test-chat-{light,dark}.png            S10 시험 대화 — 열기 → 턴 확정(실행 경로·토큰·비용)
#   p5-w6-08-settings-member-readonly.png          멤버 계정 — 예산 탭 읽기 전용 + 비활성 사유(DisabledHint)
#   p5-w6-09-settings-security-admin.png           관리자 계정 — 보안 탭은 소유자만(비활성 사유)
#   p5-w6-10-session-runtime-name.png              W-10 — S7 「세션 설정 → 컴퓨터」에 이름
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3016 &
#   BASE_URL=http://localhost:3016 bash e2e/p5-w6-shots.sh
#
# ⚠ `next build` 는 `.next/` 를 통째로 다시 쓴다 — 같은 워크트리의 `next dev` 를 먼저 끝내고(pid 로), start 는 다른 포트로(PR #191 NN5).
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3016}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w6-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
clear_theme() { apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'system'})()" >/dev/null; }
# 두 번째도 실패하면 그때 화면의 글을 찍어 둔다 — 무엇이 떠 있었는지 모르면 다음에 고칠 수 없다.
open_wait() {
  ab open "$BASE_URL$1" >/dev/null
  ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; } || {
    echo "  ✗ $1 에서 $2 를 못 찾았다. 화면: $(apic 'JSON.stringify({url:location.href,text:document.body.innerText.slice(0,300)})')"; return 1; }
}
# 로그인 폼은 정적으로 먼저 그려져(prerender) 하이드레이션 전에 채우면 React 상태가 비어 제출이 헛돈다 — 두 번째 로그인부터
# 관측(첫 로그인은 브라우저 기동이 느려 우연히 맞았다). 한 번 기다린 뒤 채우고, 그래도 안 되면 한 번 더 채워 누른다.
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  sleep 1
  for _try in 1 2; do
    ab fill 'input[name="email"]' "$1" >/dev/null
    ab fill 'input[name="password"]' 'password123' >/dev/null
    ab click 'button[type="submit"]' >/dev/null
    ab wait '[data-testid="app-nav"]' --timeout 15000 >/dev/null && return 0
  done
  echo "  ✗ $1 로그인 뒤 앱 셸이 안 떴다. 화면: $(apic 'JSON.stringify({url:location.href,text:document.body.innerText.slice(0,300)})')"; return 1
}
logout() { apic '(async()=>{await fetch("/api/v1/auth/logout",{method:"POST"}).catch(()=>{});return "ok"})()' >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인(소유자)"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

# 시드 — 초대 링크 하나(멤버 탭 목록용) · 세션 하나(W-10 용, 컴퓨터를 명시).
SEED=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  await post(`/workspaces/${ws}/invites`, { email: "newbie@colab.dev", role: "member", expires_in_hours: 168 });
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const a = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const s = await post(`/workspaces/${ws}/sessions`, { title: "온보딩 문서 정리", goal: "설치 안내를 한 페이지로 줄인다", isolation: { kind: "none" }, runtime_id: rt.id, participants: [{ agent_id: a[0].id }], assignee_agent_id: a[0].id });
  return [a[0].id, s.id].join(",");
})()')
AG=$(echo "$SEED" | cut -d, -f1)
S1=$(echo "$SEED" | cut -d, -f2)
echo "  seed: agent=$AG session=$S1"

for THEME in light dark; do
  step "테마 $THEME — 설정 탭 5개 + 대시보드"
  set_theme "$THEME"
  open_wait "/settings?tab=members" '[data-testid="invite-row"]'
  shot_full "p5-w6-01-settings-members-$THEME"

  open_wait "/settings?tab=loop" '[data-testid="row-pair-roundtrips"]'
  ab fill '[aria-label="둘이 연속으로 주고받는 횟수"]' '2' >/dev/null
  ab wait '[data-testid="settings-dirty"]' --timeout 5000 >/dev/null
  shot_full "p5-w6-02-settings-loop-$THEME"

  open_wait "/settings?tab=workdir" '[data-testid="row-retention"]'
  ab fill '[aria-label="작업 폴더 보존"]' '3' >/dev/null
  ab wait '[data-testid="settings-dirty"]' --timeout 5000 >/dev/null
  shot_full "p5-w6-03-settings-workdir-$THEME"

  open_wait "/settings?tab=security" '[data-testid="row-masking"]'
  shot_full "p5-w6-04-settings-security-$THEME"

  open_wait "/settings?tab=notifications" '[data-testid="row-subscription"]'
  shot_full "p5-w6-05-settings-notifications-$THEME"

  open_wait "/settings?tab=dashboard" '[data-testid="metrics-table"]'
  shot_full "p5-w6-06-dashboard-$THEME"

  step "테마 $THEME — S10 시험 대화(열기 → 턴 확정)"
  open_wait "/agents/$AG" '[data-testid="test-chat-open"]'
  # 화면 아래쪽 버튼은 좌표 클릭이 가끔 빗나간다(설정 화면을 여러 번 오간 뒤 관측) — DOM click 으로 누른다. React 핸들러는 같다.
  click_id() { apic "(function(){var b=document.querySelector('[data-testid=\"$1\"]');if(!b||b.disabled)return 'no';b.click();return 'ok'})()"; }
  [ "$(click_id test-chat-open)" = "ok" ] || { echo "  ✗ 시험 대화 열기 버튼이 비활성이다"; exit 1; }
  ab wait '[data-testid="test-chat-input"]' --timeout 20000 >/dev/null
  ab fill '[data-testid="test-chat-input"]' '지시문대로 답해 봐 — 설정이 맞는지 본다' >/dev/null
  [ "$(click_id test-chat-send)" = "ok" ] || { echo "  ✗ 보내기 버튼이 비활성이다"; exit 1; }
  ab wait '[data-testid="test-chat-turn-agent"]' --timeout 20000 >/dev/null
  shot_full "p5-w6-07-test-chat-$THEME"
  click_id test-chat-close >/dev/null
done

step "W-10 — S7 세션 설정의 컴퓨터 이름(밝음)"
set_theme light
open_wait "/sessions/$S1" '[data-testid="aside-runtime"]'
shot "p5-w6-10-session-runtime-name"
echo "  aside-runtime: $(apic 'document.querySelector("[data-testid=aside-runtime]").textContent')"

step "관리자 계정 — 보안 탭은 소유자만"
apic '(async()=>{const j=r=>r.json();const me=await fetch("/api/v1/me").then(j);const ws=me.workspaces[0].id;const ms=(await fetch(`/api/v1/workspaces/${ws}/members`).then(j)).items;const seo=ms.find(m=>m.user.email==="seoyeon@colab.dev");await fetch(`/api/v1/workspaces/${ws}/members/${seo.id}`,{method:"PATCH",headers:{"content-type":"application/json"},body:JSON.stringify({role:"admin"})});return "ok"})()' >/dev/null
logout
login seoyeon@colab.dev
set_theme light
open_wait "/settings?tab=security" '[data-testid="settings-security-hint"]'
shot_full "p5-w6-09-settings-security-admin"

step "멤버 계정 — 예산 탭 읽기 전용 + 비활성 사유"
logout
login junho@colab.dev
set_theme light
open_wait "/settings?tab=budget" '[data-testid="settings-budget-hint"]'
shot_full "p5-w6-08-settings-member-readonly"

step "테마를 시스템 따름으로 되돌린다"
clear_theme

echo
echo "✅ 스크린샷 — $SHOT_DIR/p5-w6-*.png"
