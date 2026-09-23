#!/usr/bin/env bash
# T-R2-W1 스크린샷 — S5 방 목록 · S25 방 찾기 · S18 방 만들기(SCREEN v0.19.2 §4.3~§4.5). `next build && next start`(목) 로 찍는다
# (개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다.
#
#   r2-w1-01-s5-empty-light.png        방 0개 · 컴퓨터 0대 — 「첫 방을 만들어 보세요」 + 예시 두 줄 + 새 방 + 컴퓨터 연결 보조 줄(막지 않는다)
#   r2-w1-02-s5-{light,dark}.png       방 목록 한 열 — 안 읽음 · 진행 중인 미션 · 주의 배지 · 멈춤 배지(예산·직접) · 참여자 +N · 공개 방 N개 줄
#   r2-w1-03-s5-blocked-light.png      멈춤 배지 4종(예산·컴퓨터 연결 끊김·루프 상한·직접) + 보관 포함(보관됨 흐림)
#   r2-w1-04-s5-menu-light.png         「…」 메뉴 — 진행 중인 미션이 있어 삭제 비활성 + 사유
#   r2-w1-05-s25-nomatch-light.png     검색 결과 0 — 「〈말〉」에 걸리는 방이 없습니다 + 보관 포함해서 다시 찾기
#   r2-w1-06-s18-{light,dark}.png      방 만들기 모달 — 이름·설명 · ⓘ 한 줄 · 방 설정(만든 뒤) · 같은 이름 경고
#   r2-w1-07-created-light.png         만들기 → /rooms/<id>(W1 당시는 임시 화면 — W2 뒤로는 S7 방 화면)
#
# 사용:
#   COLAB_MOCK_API=1 npm run build && COLAB_MOCK_API=1 npx next start -p 3141 &
#   BASE_URL=http://localhost:3141 bash e2e/r2-w1-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3141}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-r2w1-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
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
# 화면의 단언 — 조건이 거짓이면 스크립트가 멈춘다(스크린샷이 틀린 상태를 찍지 않게).
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; exit 1; }; echo "  ✓ $2"; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 로그인(소유자) · 컴퓨터를 끊어 둔다(빈 상태의 보조 줄)"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1280 900 >/dev/null
login demo@colab.dev
set_theme light
apic '(async () => {
  const me = await fetch("/api/v1/me").then((r) => r.json());
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then((r) => r.json()))[0];
  await fetch(`/api/v1/__mock/runtimes/${rt.id}/offline`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ days: 1 }) });
  return "ok";
})()' >/dev/null

step "01 — 빈 상태"
open_wait /rooms '[data-testid="empty-no-room"]'
assert_js 'document.querySelector("[data-testid=empty-no-computer]") !== null' "컴퓨터 0 대 — 보조 줄(막지 않는다)"
shot r2-w1-01-s5-empty-light

step "시드 — 컴퓨터 복구 · 옛 세션 2(→ 방) · 새 방 5 · 서연의 공개 방 2"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
login demo@colab.dev
set_theme light
SEED=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const a = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const mk = (title) => post(`/workspaces/${ws}/sessions`, { title, goal: "보고서 10페이지", isolation: { kind: "none" }, runtime_id: rt.id, participants: a.map((x) => ({ agent_id: x.id })), assignee_agent_id: a[0].id });
  const room = (name, description) => post(`/workspaces/${ws}/rooms`, { name, description });
  const onb = await room("온보딩 문서", "v1 온보딩 가이드");
  const sto = await room("STO 시장 조사", "");
  const mkt = await room("마케팅", "캠페인 카피와 리서치");
  const infra = await mk("인프라");
  const pay = await mk("결제팀");
  const seed = (id, b) => post(`/__mock/rooms/${id}/seed`, b);
  await seed(pay.id, { description: "결제 관련 논의와 작업", unread: 3, people: [{ email: "seoyeon@colab.dev", role: "deputy" }, { email: "junho@colab.dev", role: "member" }] });
  await seed(infra.id, { description: "배포·모니터링", blocked_reason: "budget" });
  await seed(mkt.id, { unread: 12 });
  await seed(sto.id, { blocked_reason: "manual" });
  await post(`/rooms/${onb.id}/archive`);
  // 결제팀 — 실패한 서브 미션 1(내가 Director) · 확인 요청 1
  await post(`/__mock/sessions/${pay.id}/seed-lanes`, {});
  await post(`/__mock/sessions/${pay.id}/seed-hitl`, {});
  return [ws, pay.id, infra.id, mkt.id, sto.id, onb.id].join(",");
})()')
IFS=, read -r WS PAY INFRA MKT STO ONB <<<"$SEED"
echo "  ws=$WS pay=$PAY"
logout
login seoyeon@colab.dev
apic "(async () => { for (const name of [\"디자인 리뷰\", \"채용\"]) await fetch(\"/api/v1/workspaces/$WS/rooms\", { method: \"POST\", headers: { \"content-type\": \"application/json\" }, body: JSON.stringify({ name }) }); return \"ok\"; })()" >/dev/null
logout
login demo@colab.dev

for theme in light dark; do
  step "02 — 방 목록 ($theme)"
  set_theme "$theme"
  open_wait /rooms '[data-testid="room-list"]'
  ab wait '[data-testid="room-more-public"]' --timeout 20000 >/dev/null
  assert_js 'document.querySelectorAll("[data-testid=room-row]").length === 4' "참여한 방 4(보관된 방은 기본에서 숨김)"
  assert_js 'document.querySelector("[data-testid=room-more-public]").textContent.includes("공개된 방이 2개 더")' "공개 방 2개 줄"
  assert_js '[...document.querySelectorAll("[role=img][data-kind=room]")].map(e=>e.getAttribute("aria-label")).sort().join("|") === "예산으로 멈춤|직접 멈춤"' "멈춤 배지 — 예산 · 직접"
  shot "r2-w1-02-s5-$theme"
done
set_theme light

step "03 — 멈춤 배지 4종 + 보관 포함"
apic "(async () => { const s = (id, b) => fetch(\`/api/v1/__mock/rooms/\${id}/seed\`, { method: \"POST\", headers: { \"content-type\": \"application/json\" }, body: JSON.stringify(b) }); await s(\"$MKT\", { blocked_reason: \"runtime_offline\" }); await s(\"$PAY\", { blocked_reason: \"loop\" }); return \"ok\"; })()" >/dev/null
open_wait '/rooms?archived=1' '[data-testid="room-list"]'
assert_js '[...document.querySelectorAll("[role=img][data-kind=room]")].map(e=>e.getAttribute("aria-label")).sort().join("|") === "루프 상한으로 멈춤|예산으로 멈춤|직접 멈춤|컴퓨터 연결 끊김으로 멈춤"' "멈춤 배지 4종"
assert_js 'document.querySelector("[data-testid=room-archived]") !== null' "보관됨 칩"
shot r2-w1-03-s5-blocked-light
apic "(async () => { await fetch(\`/api/v1/__mock/rooms/$PAY/seed\`, { method: \"POST\", headers: { \"content-type\": \"application/json\" }, body: JSON.stringify({ blocked_reason: null }) }); await fetch(\`/api/v1/__mock/rooms/$MKT/seed\`, { method: \"POST\", headers: { \"content-type\": \"application/json\" }, body: JSON.stringify({ blocked_reason: null }) }); return \"ok\"; })()" >/dev/null

step "04 — 「…」 메뉴(진행 중인 미션 → 삭제 비활성 + 사유)"
open_wait /rooms '[data-testid="room-list"]'
ab click "[data-testid=\"room-menu-$PAY\"] [data-testid=\"room-menu-button\"]" >/dev/null
ab wait '[data-testid="room-menu-list"]' --timeout 10000 >/dev/null
assert_js 'document.querySelector("[data-testid=room-menu-list]").textContent.includes("미션 1개가 진행 중입니다")' "삭제 비활성 사유"
shot r2-w1-04-s5-menu-light

step "05 — 검색 결과 0"
open_wait '/rooms?q=%EC%97%86%EB%8A%94%EB%A7%90' '[data-testid="empty-no-match"]'
shot r2-w1-05-s25-nomatch-light

for theme in light dark; do
  step "06 — S18 방 만들기 ($theme)"
  set_theme "$theme"
  open_wait /rooms/new '[data-testid="create-room-dialog"]'
  ab fill '[data-testid="create-room-name"]' '결제팀' >/dev/null
  ab fill '[data-testid="create-room-description"]' '결제 관련 논의와 작업' >/dev/null
  ab wait '[data-testid="create-room-duplicate"]' --timeout 10000 >/dev/null
  assert_js 'document.querySelector("[data-testid=create-room-defaults]").textContent === "격리 없음 · 컴퓨터는 첫 실행 때 정해집니다"' "ⓘ 한 줄 — 워크스페이스 기본값"
  shot "r2-w1-06-s18-$theme"
done
set_theme light

step "07 — 만들기 → /rooms/<id>"
open_wait /rooms/new '[data-testid="create-room-dialog"]'
ab fill '[data-testid="create-room-name"]' '인프라 2' >/dev/null
ab click '[data-testid="create-room-submit"]' >/dev/null
ab wait '[data-testid="room-detail"]' --timeout 20000 >/dev/null # T-R2-W2 뒤로는 새 방 화면(S7)이 바로 열린다
assert_js 'location.pathname.startsWith("/rooms/") && location.pathname !== "/rooms/new"' "방 화면으로 이동"
shot r2-w1-07-created-light

step "/sessions → /rooms 307(브라우저)"
open_wait /sessions '[data-testid="room-list"]'
assert_js 'location.pathname === "/rooms"' "옛 주소가 방 목록으로"
echo; echo "끝."
