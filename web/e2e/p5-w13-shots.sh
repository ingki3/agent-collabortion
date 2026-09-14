#!/usr/bin/env bash
# T-W13 스크린샷 — S5 세션 카드 「…」 옵션 + 삭제(FR-2.7 · SCREEN §4.3·§5). `next build && next start` 로 찍는다(PR #186 NN5 —
# 개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다(PR #188 NN3).
#
#   p5-w13-01-menu-{light,dark}.png            카드 「…」 메뉴 열림 — 끝난 세션(활성 「삭제」)
#   p5-w13-02-menu-blocked-{light,dark}.png    진행 중 세션 — 「삭제」 비활성 + 사유 "진행 중인 세션은 먼저 종료하세요"
#   p5-w13-03-dialog-{light,dark}.png          확인 다이얼로그 — 제목에 세션 이름 · 사라지는 것 · 되돌릴 수 없음 · 위험 색 「삭제」
#   p5-w13-04-workdirs-{light,dark}.png        409 workdir_unmerged — 다이얼로그 안에 작업 폴더 목록(경로·브랜치·사유) + 「작업 폴더 관리」 링크
#   p5-w13-05-deleted-light.png                204 뒤 — 카드가 빠지고 안내 한 줄
#   p5-w13-06-menu-member-light.png            멤버 계정 — 남의 끝난 세션의 「삭제」 비활성 + 사유 "Director 나 소유자·관리자만 …"
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/p5-w13-shots.sh
# ⚠ 개발 서버와 포트·빌드를 분리할 것(PR #191 NN5). SHOT_DIR 로 저장 위치를 바꿀 수 있다(기본 __screenshots__).
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w13-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
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
logout() { apic '(async()=>{await fetch("/api/v1/auth/logout",{method:"POST"}).catch(()=>{});return "ok"})()' >/dev/null; }
menu_of() { echo "[data-testid=\"session-menu-$1\"] [data-testid=\"session-menu-button\"]"; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인(소유자)"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

# 세션 4 — s1 끝남(cancelled, 삭제 가능) · s2 진행 중(비활성) · s3 끝남 + 미병합 worktree(409) · s4 끝남, Director 는 서연(멤버 계정용).
step "시드 — 세션 4 (끝남·진행 중·작업 폴더 남음·남의 세션)"
SEED=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const a = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const mk = (title, goal, isolation) => post(`/workspaces/${ws}/sessions`, {
    title, goal, isolation, runtime_id: rt.id, participants: a.slice(0, 2).map((x) => ({ agent_id: x.id })), assignee_agent_id: a[0].id,
  });
  const s1 = await mk("국내 B2B SaaS 결제 시장 조사", "보고서 10페이지 — 상위 5개 사업자 비교", { kind: "none" });
  const s2 = await mk("결제 모듈 구현", "Backend·Frontend 가 각자 구현하고 QA 가 리뷰한다", { kind: "none" });
  const s3 = await mk("온보딩 문서 정리", "설치 안내를 한 페이지로 줄인다", { kind: "worktree", repo_path: "/work/colab" });
  const s4 = await mk("주간 리뷰 요약", "지난주 PR 리뷰 코멘트를 항목별로 모은다", { kind: "none" });
  await post(`/sessions/${s1.id}/cancel`, {});
  await post(`/sessions/${s3.id}/cancel`, {});
  await post(`/__mock/sessions/${s3.id}/seed-workdirs`, {});
  const mems = (await fetch(`/api/v1/workspaces/${ws}/members`).then(j)).items;
  const seo = mems.find((m) => m.user.email === "seoyeon@colab.dev");
  await fetch(`/api/v1/sessions/${s4.id}/director`, { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ director_user_id: seo.user.id }) });
  return [s1.id, s2.id, s3.id, s4.id].join(",");
})()')
echo "  seed: $SEED"
S1=$(echo "$SEED" | cut -d, -f1); S2=$(echo "$SEED" | cut -d, -f2); S3=$(echo "$SEED" | cut -d, -f3); S4=$(echo "$SEED" | cut -d, -f4)

for THEME in light dark; do
  step "테마 $THEME — 메뉴 열림(활성) · 메뉴(비활성 + 사유) · 다이얼로그 · 409 작업 폴더 목록"
  set_theme "$THEME"
  open_wait /sessions '[data-testid="session-row"]'
  ab click "$(menu_of "$S1")" >/dev/null
  ab wait '[data-testid="session-menu-list"]' --timeout 10000 >/dev/null
  shot "p5-w13-01-menu-$THEME"
  ab click "$(menu_of "$S2")" >/dev/null
  ab wait '[data-testid="session-menu-delete"][aria-disabled="true"]' --timeout 10000 >/dev/null
  shot "p5-w13-02-menu-blocked-$THEME"
  ab click "$(menu_of "$S1")" >/dev/null
  ab wait '[data-testid="session-menu-list"]' --timeout 10000 >/dev/null
  ab click '[data-testid="session-menu-delete"]' >/dev/null
  ab wait '[data-testid="delete-session-dialog"]' --timeout 10000 >/dev/null
  shot "p5-w13-03-dialog-$THEME"
  ab click '[data-testid="delete-session-cancel"]' >/dev/null
  ab click "$(menu_of "$S3")" >/dev/null
  ab wait '[data-testid="session-menu-list"]' --timeout 10000 >/dev/null
  ab click '[data-testid="session-menu-delete"]' >/dev/null
  ab wait '[data-testid="delete-session-dialog"]' --timeout 10000 >/dev/null
  ab click '[data-testid="delete-session-confirm"]' >/dev/null
  ab wait '[data-testid="delete-session-workdirs"]' --timeout 10000 >/dev/null
  shot "p5-w13-04-workdirs-$THEME"
  ab click '[data-testid="delete-session-cancel"]' >/dev/null
done

step "204 — 카드가 빠지고 안내 한 줄(밝음)"
set_theme light
open_wait /sessions '[data-testid="session-row"]'
ab click "$(menu_of "$S1")" >/dev/null
ab wait '[data-testid="session-menu-list"]' --timeout 10000 >/dev/null
ab click '[data-testid="session-menu-delete"]' >/dev/null
ab wait '[data-testid="delete-session-dialog"]' --timeout 10000 >/dev/null
ab click '[data-testid="delete-session-confirm"]' >/dev/null
ab wait '[data-testid="session-deleted-notice"]' --timeout 10000 >/dev/null
shot "p5-w13-05-deleted-light"

step "멤버 계정 — 남의 끝난 세션(s3)의 「삭제」 비활성 + 역할 사유"
logout
login seoyeon@colab.dev
set_theme light
open_wait /sessions '[data-testid="session-row"]'
ab click "$(menu_of "$S3")" >/dev/null
ab wait '[data-testid="session-menu-delete"][aria-disabled="true"]' --timeout 10000 >/dev/null
shot "p5-w13-06-menu-member-light"

step "테마를 시스템 따름으로 되돌린다"
clear_theme

echo
echo "✅ 스크린샷 — $SHOT_DIR/p5-w13-*.png"
