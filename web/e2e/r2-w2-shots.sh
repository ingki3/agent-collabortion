#!/usr/bin/env bash
# T-R2-W2 스크린샷 — S7 방 화면 3열 · S22 미션 패널(SCREEN v0.19.2 §4.6 · §4.8). `next build && next start`(목) 로 찍는다.
# 테마는 설정 화면과 같은 경로(localStorage + <html data-theme>)로 고정한다. 단언이 거짓이면 멈춘다(틀린 상태를 찍지 않게).
#
#   r2-w2-01-s7-all-{light,dark}.png    미션 2개 방 (전체) — 세 층 요약 · 나에게 필요한 것 · 칩 줄(● · ⏸ · 지난 미션 ▾) · 카드마다 미션 라벨 ·
#                                       보드(사람 할 일 우선, 완료·실패 접힘) · 우열 「최근 활동」 미션(동작 비활성 + 사유 글자) · 방 전체 칸 접힘
#   r2-w2-02-s7-work-light.png          칩 「보고서 초안」 — 타임라인·보드가 그 미션만(라벨 감춤), 우열은 그 미션(동작 켜짐), 방 전체 칸 펼침(미션별 묶음)
#   r2-w2-03-s7-none-light.png          칩 (미션 없음) — 우열은 칸을 남기고 비운다 · 「미션 없이 오간 대화에는 끝이 없습니다」
#   r2-w2-04-s7-blocked-{light,dark}.png  「이 방 멈춤」 → 전폭 배너(role=alert, 멈춘 미션 수는 칸) · 상단 「멈춤 해제」
#   r2-w2-05-s22-ended-light.png        S22 — 끝난 미션(?work=<id>) 읽기 전용 · 「끝난 미션입니다」
#   r2-w2-06-composer-auto-light.png    작성창 자동 귀속 — 실행 중 서브 미션의 미션으로 「자동: 이 메시지는 미션 〈…〉에 들어갑니다」
#   r2-w2-07-narrow-{work,board}-light.png  좁은 화면(1000px) 탭 넷 — 미션 탭(?work= 로 들어오면 열린 채 시작) · 보드 탭
#   r2-w2-08-fresh-light.png            방금 만든 빈 방 — 「이제 무엇을 하나요?」 세 갈래 · 칩 줄 없이 「+ 새 미션」 · 우열 「아직 연 미션이 없습니다」
#
# 사용:
#   COLAB_MOCK_API=1 npm run build && COLAB_MOCK_API=1 npx next start -p 3147 &
#   BASE_URL=http://localhost:3147 bash e2e/r2-w2-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3147}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-r2w2-shots-$$}"

ab() { agent-browser "$@"; }
shot() { apic '(function(){window.scrollTo(0,0);return "ok"})()' >/dev/null; sleep 0.3; ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1');document.documentElement.setAttribute('data-theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; exit 1; }; echo "  ✓ $2"; }
# 뷰포트 밖 요소는 click 이 조용히 안 닿는다(v1.1 W-17 교훈) — 먼저 보이게 한다.
click() { apic "(function(){const e=document.querySelector('$1');if(!e)return 'missing';e.scrollIntoView({block:'center'});return 'ok'})()" >/dev/null; ab click "$1" >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 초기화 · 로그인 · 시드 — 새 방 「결제팀」: 에이전트 2 · 미션 3(진행·일시정지(예산)·완료) · 메시지 · 서브 미션 7 · 확인 요청 1"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
ab set viewport 1440 900 >/dev/null
login demo@colab.dev
set_theme light
SEED=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const room = await post(`/workspaces/${ws}/rooms`, { name: "결제팀", description: "결제 관련 논의와 작업" });
  const seed = (b) => post(`/__mock/rooms/${room.id}/seed`, b);
  await seed({ agents: ["Lead", "Researcher"], people: [{ email: "seoyeon@colab.dev", role: "deputy" }, { email: "junho@colab.dev", role: "member" }] });
  const s = await seed({
    // work 순번(messages·lanes 의 work)은 **같은 호출 안의** works 만 가리킨다 — 한 번에 보낸다.
    works: [
      { title: "보고서 초안", goal: "국내 B2B SaaS 결제 시장 보고서 10페이지", budget_usd: 20, cost_usd: 3.2 },
      { title: "수수료 비교", goal: "PG 3사 수수료 비교표", status: "paused", paused_reason: "budget", budget_usd: 20, cost_usd: 21.4 },
      { title: "지난 분기 보고서", goal: "2분기 결제 동향 정리", status: "completed", cost_usd: 1.1 },
    ],
    messages: [
      { content: "이번 주 목표는 결제 시장 보고서 초안입니다.", work: null },
      { content: "시장 규모부터 정리해 주세요.", work: 0 },
      { content: "시장 규모 자료 3건을 찾았습니다. 표로 정리하는 중입니다.", work: 0, agent: "Researcher" },
      { content: "PG 3사 수수료 표를 비교해 주세요.", work: 1 },
      { content: "예산 $20 을 넘겨 멈췄습니다 — 계속하려면 승인이 필요합니다.", work: 1, agent: "Lead" },
      { content: "점심은 12시 반에 모여요.", work: null },
    ],
    lanes: [
      { agent: "Researcher", status: "running", work: 0, brief: "시장 규모 조사" },
      { agent: "Lead", status: "queued", work: 0, brief: "목차 초안", queued_reason: "room_lanes" },
      { agent: "Lead", status: "paused", work: 1, brief: "수수료 표", paused_over_usd: 1.4 },
      { agent: "Researcher", status: "blocked", work: 1, brief: "PG 약관 확인" },
      { agent: "Lead", status: "failed", work: null, brief: "회의록 정리", failure_kind: "timeout" },
    ],
  });
  await post(`/__mock/rooms/${room.id}/seed-hitl`, { question: "보고서 독자가 투자자인가요, 내부 경영진인가요?" });
  return [ws, room.id, s.works[0], s.works[1], s.works[2]].join(",");
})()')
IFS=, read -r WS ROOM W1 W2 W3 <<<"$SEED"
echo "  ws=$WS room=$ROOM works=$W1,$W2,$W3"

for theme in light dark; do
  step "01 — (전체) ($theme)"
  set_theme "$theme"
  open_wait "/rooms/$ROOM" '[data-testid="work-chips"]'
  ab wait '[data-testid="work-panel-recent"]' --timeout 20000 >/dev/null
  assert_js 'document.querySelectorAll("[data-testid=work-chip]").length === 2' "칩 2(열린 미션) + 지난 미션 ▾"
  assert_js 'document.querySelector("[data-testid=chip-past]").textContent.includes("지난 미션 1개")' "지난 미션 1개"
  assert_js 'document.querySelectorAll("[data-testid=message-work-label]").length >= 6' "(전체) — 카드마다 미션 라벨"
  assert_js 'document.querySelector("[data-testid=work-action-complete]").disabled && document.querySelector("[data-testid=work-actions-why]").textContent.startsWith("어느 미션인지 먼저 고르세요")' "(전체) — 미션 동작 비활성 + 사유 글자"
  assert_js 'document.querySelector("[data-testid=lane-group-failed]").getAttribute("data-folded") === "true"' "보드 — 실패 묶음 접힘"
  assert_js 'document.querySelector("[data-testid=room-panel-toggle]").getAttribute("aria-expanded") === "false"' "방 전체 칸 기본 접힘"
  assert_js 'document.querySelector("[data-testid=lane-queued-reason]").textContent === "이 방의 동시 서브 미션 상한(5)에 닿았습니다"' "대기 사유 사람 말(상한은 칸)"
  shot "r2-w2-01-s7-all-$theme"
done
set_theme light

step "02 — 칩 「보고서 초안」 (타임라인·보드·우열 연동) + 방 전체 칸 펼침"
open_wait "/rooms/$ROOM" '[data-testid="work-chips"]'
click "[data-testid=\"work-chip\"][data-work-id=\"$W1\"]"
ab wait "[data-testid=\"work-panel\"][data-work-id=\"$W1\"][data-mode=\"picked\"]" --timeout 20000 >/dev/null
assert_js 'location.search.includes("work=")' "선택이 URL(?work=)에 선다"
assert_js 'document.querySelectorAll("[data-testid=message-work-label]").length === 0 && document.querySelectorAll("[data-testid=lane-work-label]").length === 0' "칩을 고르면 라벨을 감춘다(양방향)"
assert_js '!document.querySelector("[data-testid=work-action-complete]").disabled' "고른 미션 — 동작 켜짐"
click '[data-testid="room-panel-toggle"]'
ab wait '[data-testid="room-cost-not-sum"]' --timeout 10000 >/dev/null
shot r2-w2-02-s7-work-light

step "03 — (미션 없음)"
click '[data-testid="chip-none"]'
ab wait '[data-testid="work-panel-none"]' --timeout 20000 >/dev/null
assert_js 'document.querySelector("[data-testid=work-actions-why]").textContent === "미션 없이 오간 대화에는 끝이 없습니다"' "(미션 없음) 사유"
shot r2-w2-03-s7-none-light

step "06 — 작성창 자동 귀속(실행 중 서브 미션의 미션)"
open_wait "/rooms/$ROOM" '[data-testid="composer-input"]'
ab fill '[data-testid="composer-input"]' '@Researcher 표에 출처도 달아 주세요' >/dev/null
ab wait '[data-testid="work-selector"][data-mode="auto"]' --timeout 20000 >/dev/null
assert_js 'document.querySelector("[data-testid=chip-work]").textContent.startsWith("자동: 이 메시지는 미션 「보고서 초안」에 들어갑니다")' "자동: 접두"
shot r2-w2-06-composer-auto-light

step "04 — 「이 방 멈춤」 → 방 멈춤 배너"
open_wait "/rooms/$ROOM" '[data-testid="work-chips"]'
click '[data-testid="room-block"]'
ab wait '[data-testid="block-dialog"]' --timeout 10000 >/dev/null
assert_js 'document.querySelector("[data-testid=block-dialog-works]").textContent === "미션 1개와 미션 밖 대화 전부가 멈춥니다."' "확인 문장 — 진행 중인 미션 수 칸"
click '[data-testid="block-dialog-confirm"]'
ab wait '[data-testid="room-banner"]' --timeout 20000 >/dev/null
assert_js 'document.querySelector("[data-testid=room-banner]").getAttribute("role") === "alert" && document.querySelector("[data-testid=room-banner-stopped] [data-slot]").textContent === "1"' "배너 role=alert · 멈춘 미션 수는 칸"
for theme in light dark; do
  set_theme "$theme"
  sleep 1
  shot "r2-w2-04-s7-blocked-$theme"
done
set_theme light
click '[data-testid="room-banner-unblock"]'
ab wait '[data-testid="room-block"]' --timeout 20000 >/dev/null

step "05 — S22 끝난 미션(?work=<id>)"
open_wait "/rooms/$ROOM?work=$W3" '[data-testid="work-ended"]'
assert_js '!document.querySelector("[data-testid=work-actions]")' "읽기 전용 — 동작 없음"
shot r2-w2-05-s22-ended-light

step "07 — 좁은 화면(1000px) 탭 넷"
ab set viewport 1000 900 >/dev/null
open_wait "/rooms/$ROOM?work=$W2" '[data-testid="tab-work"]'
assert_js 'document.querySelector("[data-testid=room-detail]").getAttribute("data-col") === "work"' "?work= 로 들어오면 미션 탭"
assert_js '[...document.querySelectorAll(".s7__tab")].map(e=>e.textContent).join("|") === "타임라인|보드|미션|방"' "탭 넷"
ab wait '[data-testid="work-paused-banner"]' --timeout 20000 >/dev/null
shot r2-w2-07-narrow-work-light
click '[data-testid="tab-board"]'
sleep 1
shot r2-w2-07-narrow-board-light
ab set viewport 1440 900 >/dev/null

step "08 — 방금 만든 빈 방"
FRESH=$(apic "(async () => { const r = await fetch(\"/api/v1/workspaces/$WS/rooms\", { method: \"POST\", headers: { \"content-type\": \"application/json\" }, body: JSON.stringify({ name: \"인프라\" }) }).then((r) => r.json()); return r.id; })()")
open_wait "/rooms/$FRESH" '[data-testid="room-fresh"]'
assert_js '!document.querySelector("[data-testid=work-chips]") && !!document.querySelector("[data-testid=new-work]")' "칩 줄 없이 「+ 새 미션」만"
assert_js 'document.querySelector("[data-testid=work-panel]").getAttribute("data-mode") === "no_works"' "우열 — 아직 연 미션이 없습니다"
shot r2-w2-08-fresh-light
echo; echo "끝."
