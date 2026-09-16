#!/usr/bin/env bash
# T-W15 스크린샷 — 종료 조건을 알기 쉽게(S-84 · W-19, Director 지적 2026-09-15 "복잡하고 종료 조건의 파악이 어렵다").
# `next build && next start` 로 찍는다(PR #186 NN5). 테마는 localStorage + <html data-theme>(PR #188 NN3).
#
#   p5-w15-01-s6-reviewer-{light,dark}.png     S6 6단계 — 사람 말 조건 이름 · 「에이전트 검토 승인」 을 고르면 리뷰어 선택 필수(다음 비활성 + 사유)
#   p5-w15-02-s6-summary-{light,dark}.png      S6 7단계 요약 — "보고서 제출 (담당 에이전트) 그리고 Lead 의 검토 승인 그리고 Director 승인"
#   p5-w15-03-s7-progress-ok-{light,dark}.png  S7 진행률 정상 — 보고서 제출 ✓ (Researcher, m/d) · Lead 의 검토 승인 — Lead 차례 · Director 승인 — 받은 요청에서
#   p5-w15-04-s7-blocked-{light,dark}.png      S7 진행률 막힘 — 리뷰어 없는 옛 세션: ✗ 대신 이유 + 「조건 고치기」 + "남은 것: … 막힘 1개"
#   p5-w15-05-fix-dialog-{light,dark}.png      「조건 고치기」 다이얼로그(6단계와 같은 편집기) — 리뷰어 비어 저장 비활성 + 사유
#   p5-w15-06-s7-fixed-{light,dark}.png        고친 뒤 — 막힘 해소, 보고서 제출 ✓ 유지
#   p5-w15-07-s7-blocked-member-light.png      멤버 시점(W-20) — 같은 막힘을 Director 아닌 멤버가 본다: "Director 가 조건을 고쳐야 …" · 「조건 고치기」 없음
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/p5-w15-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w15-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
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
# 6단계까지 — 참여자 둘(Lead · Researcher), 담당은 Lead(역할 우선).
wizard_to_conditions() {
  open_wait "/sessions/new" '[data-testid="session-wizard"]'
  ab fill '[data-testid="session-title"]' '국내 B2B SaaS 결제 시장 조사' >/dev/null
  ab fill '[data-testid="session-goal"]' '보고서 10페이지 — 상위 5개 사업자 비교' >/dev/null
  ab click '[data-testid="wizard-next"]' >/dev/null   # 2 Director
  ab click '[data-testid="wizard-next"]' >/dev/null   # 3 격리
  ab click '[data-testid="wizard-next"]' >/dev/null   # 4 컴퓨터
  ab click '[data-testid="wizard-next"]' >/dev/null   # 5 참여자
  ab wait '[data-testid="participant-option"]' --timeout 10000 >/dev/null
  apic '(function(){document.querySelectorAll("[data-testid=participant-option] input[type=checkbox]").forEach(function(c){ if(!c.checked) c.click(); });return "ok"})()' >/dev/null
  ab click '[data-testid="wizard-next"]' >/dev/null   # 6 종료 조건
  ab wait '[data-testid="condition-editor"]' --timeout 10000 >/dev/null
}

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

for THEME in light dark; do
  step "테마 $THEME — S6 6단계: 에이전트 검토 승인을 고르면 리뷰어 필수(다음 비활성 + 사유)"
  set_theme "$THEME"
  wizard_to_conditions
  ab click '[data-testid="condition-row"][data-type="agent_approval"]' >/dev/null
  ab wait '[data-testid="reviewer-required"]' --timeout 5000 >/dev/null
  shot_full "p5-w15-01-s6-reviewer-$THEME"

  step "테마 $THEME — S6 7단계 요약 문장(사람 말)"
  LEAD=$(apic '(function(){var o=[...document.querySelectorAll("[data-testid=reviewer-select] option")].find(function(x){return x.textContent.indexOf("Researcher")>=0});return o?o.value:""})()')
  ab select '[data-testid="reviewer-select"]' "$LEAD" >/dev/null
  ab click '[data-testid="wizard-next"]' >/dev/null   # 7 한도 + 요약
  ab wait '[data-testid="summary-condition"]' --timeout 5000 >/dev/null
  apic "(function(){document.querySelector('[data-testid=wizard-summary]').scrollIntoView({block:'center'});return 'ok'})()" >/dev/null
  shot "p5-w15-02-s6-summary-$THEME"
done

step "시드 — 세션 하나(보고서 제출 AND Lead 의 검토 승인 AND Director 승인) + 아티팩트 1"
SID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const a = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const lead = a.find((x) => x.name === "Lead"), res = a.find((x) => x.name === "Researcher");
  const s = await post(`/workspaces/${ws}/sessions`, {
    title: "국내 B2B SaaS 결제 시장 조사", goal: "보고서 10페이지 — 상위 5개 사업자 비교", isolation: { kind: "none" }, runtime_id: rt.id,
    participants: [{ agent_id: lead.id }, { agent_id: res.id }], assignee_agent_id: res.id,
    completion_condition: { op: "and", conditions: [{ type: "artifact_submitted", who: "assignee" }, { type: "agent_approval", agent_id: lead.id }, { type: "user_approval" }] },
  });
  await new Promise((r) => setTimeout(r, 3000));
  return s.id;
})()')
echo "  session=$SID"

for THEME in light dark; do
  step "테마 $THEME — S7 진행률 정상"
  set_theme "$THEME"
  open_wait "/sessions/$SID" '[data-testid="aside-progress"]'
  shot "p5-w15-03-s7-progress-ok-$THEME"
done

step "시드 — 리뷰어 없는 옛 세션으로(보고서는 제출됨)"
apic "fetch('/api/v1/__mock/sessions/$SID/seed-legacy-condition', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{\"met_artifact\":true,\"with_user_approval\":true}' }).then(r => r.status)" >/dev/null

for THEME in light dark; do
  step "테마 $THEME — S7 진행률 막힘 + 조건 고치기 다이얼로그"
  set_theme "$THEME"
  open_wait "/sessions/$SID" '[data-testid="progress-blocked"]'
  shot "p5-w15-04-s7-blocked-$THEME"
  ab click '[data-testid="fix-condition-open"]' >/dev/null
  ab wait '[data-testid="fix-condition-dialog"]' --timeout 5000 >/dev/null
  shot "p5-w15-05-fix-dialog-$THEME"
  ab click '[data-testid="fix-condition-cancel"]' >/dev/null
done

step "멤버 시점 — 같은 막힘, 「조건 고치기」 없음 · 'Director 가 조건을 고쳐야 …'(밝음, W-20)"
apic "fetch('/api/v1/__mock/sessions/$SID/role', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{\"role\":\"member\"}' }).then(r => r.status)" >/dev/null
set_theme light
open_wait "/sessions/$SID" '[data-testid="progress-blocked"]'
MEMBER_LINE=$(apic '(function(){return document.querySelector("[data-testid=progress-blocked]").textContent})()')
echo "  막힘 줄: $MEMBER_LINE"
case "$MEMBER_LINE" in *"Director 가 조건을 고쳐야"*) ;; *) echo "❌ 멤버 시점 문장이 아니다: $MEMBER_LINE"; exit 1;; esac
HAS_FIX=$(apic '(function(){return document.querySelector("[data-testid=fix-condition-open]") ? "yes" : "no"})()')
[ "$HAS_FIX" = "no" ] || { echo "❌ 멤버에게 「조건 고치기」 가 보인다"; exit 1; }
shot "p5-w15-07-s7-blocked-member-light"
# Director 로 되돌린다 — 아래 왕복은 Director 만 할 수 있다.
apic "fetch('/api/v1/__mock/sessions/$SID/role', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{\"role\":\"director\"}' }).then(r => r.status)" >/dev/null

step "조건 고치기 왕복 — 리뷰어를 Lead 로 → 막힘 해소, 보고서 제출 ✓ 유지"
set_theme light
open_wait "/sessions/$SID" '[data-testid="progress-blocked"]'
ab click '[data-testid="fix-condition-open"]' >/dev/null
ab wait '[data-testid="reviewer-select"]' --timeout 5000 >/dev/null
LEAD=$(apic '(function(){var o=[...document.querySelectorAll("[data-testid=fix-condition-dialog] [data-testid=reviewer-select] option")].find(function(x){return x.textContent.indexOf("Lead")>=0});return o?o.value:""})()')
ab select '[data-testid="fix-condition-dialog"] [data-testid="reviewer-select"]' "$LEAD" >/dev/null
ab click '[data-testid="fix-condition-save"]' >/dev/null
ab wait '[data-testid="condition-row"][data-type="agent_approval"]:not([data-blocked])' --timeout 10000 >/dev/null
for THEME in light dark; do
  set_theme "$THEME"
  open_wait "/sessions/$SID" '[data-testid="aside-progress"]'
  shot "p5-w15-06-s7-fixed-$THEME"
done

step "테마를 시스템 따름으로 되돌린다"
clear_theme

echo
echo "✅ 스크린샷 — $SHOT_DIR/p5-w15-*.png"
