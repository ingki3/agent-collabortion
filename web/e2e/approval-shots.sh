#!/usr/bin/env bash
# T-APPROVAL 스크린샷 — 「완료 승인 요청」 카드의 **버튼**과, 작업 중 **보류 표시**(PRD v0.19.5 FR-2A.2.1 · SCREEN v0.19.7 §4.6).
# `next build && next start` 로 찍는다(목 모드는 빌드 시점 플래그) — 다른 shots 스크립트와 같은 구조.
#
#   approval-<TAG>-s7-card.png    S7 타임라인의 완료 승인 카드 — 「승인」·「수정 요청」 버튼과 사유 칸.
#                                 (고치기 전에는 이 자리가 **시스템 문장 한 줄 + 「답글」** 이었다.)
#   approval-<TAG>-s7-held.png    우열 미션 칸 — 진행 중인 작업이 있어 승인 요청이 보류된 상태:
#                                 「조건 충족 — 진행 중인 작업이 끝나면 승인을 요청합니다」.
#   approval-<TAG>-s7-stale.png   **실측 상황 재현** — 화면이 목록을 읽은 **뒤에** 열린 요청(`hitl.created` 없음,
#                                 `no_emit`). 고치기 전: 시스템 문장 한 줄 + 「답글」. 고친 뒤: 요청을 직접 읽어 카드.
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3197 &
#   BASE_URL=http://localhost:3197 SHOT_DIR=/tmp/shots TAG=after bash e2e/approval-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3197}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
TAG="${TAG:-after}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-approval-shots-$$}"

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
ab set viewport 1440 1200 >/dev/null
login demo@colab.dev

# 실측 장면 그대로: 담당(Lead)이 아직 도는 중에 v1 을 제출했다 — 아티팩트는 있고 할 일은 `queued`.
RID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rts = await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j);
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const by = (n) => (ags.items ?? []).find((a) => a.name === n);
  const lead = by("Lead");
  const res = by("Researcher");
  // 종료 조건 기본값(아티팩트 제출 + Director 승인)을 가진 미션이 있는 방.
  const r = await post(`/__mock/workspaces/${ws}/seed-room`, {
    title: "게임 제작 방", goal: "턴제 레이싱 게임을 만든다", isolation: { kind: "none" },
    runtime_id: (Array.isArray(rts) ? rts : rts.items)[0].id, assignee_agent_id: lead.id,
    participants: [{ agent_id: lead.id }, { agent_id: res.id }],
  });
  // 담당이 아직 도는 중(할 일 queued) — 이것이 승인 요청을 보류시키는 조건이다.
  await post(`/rooms/${r.id}/messages`, { content: `[@Lead](mention://agent/${lead.id}) 게임을 만들어 주세요` });
  // 그 턴 안에서 v1 제출 — 종료 조건에서 Director 승인만 남는다.
  await post(`/__mock/rooms/${r.id}/seed-artifacts`, { count: 1, type: "doc" });
  return r.id;
})()')
echo "  room=$RID"
apic "(function(){try{localStorage.setItem('colab.theme','light')}catch(e){};return 'ok'})()" >/dev/null

# ── 1) 보류 표시 — 우열 미션 칸 ──────────────────────────────────────────────
ab open "$BASE_URL/rooms/$RID" >/dev/null
ab wait '[data-testid="condition-row"][data-type="user_approval"]' --timeout 20000 >/dev/null
sleep 1
HELD=$(apic 'document.querySelector("[data-testid=condition-row][data-type=user_approval]").getAttribute("data-held")')
HELD_TEXT=$(apic '(document.querySelector("[data-testid=condition-row][data-type=user_approval] [data-testid=condition-line]")||{}).textContent')
echo "  held=$HELD text=$HELD_TEXT"
[ "$HELD" = "running_tasks" ] || echo "  ⚠ 보류 표시가 없다 — 승인 요청이 이미 열렸거나 진행 중인 할 일이 없다"
ab screenshot "$SHOT_DIR/approval-$TAG-s7-held.png" >/dev/null
echo "  📸 $SHOT_DIR/approval-$TAG-s7-held.png"

# ── 2) 버튼 카드 — 할 일이 끝난 뒤 열린 완료 승인 요청 ────────────────────────
apic "
(async () => {
  const post = (p, b) => fetch('/api/v1' + p, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b ?? {}) }).then((r) => r.json());
  await post('/__mock/rooms/$RID/seed-hitl', { source: 'system', type: 'approval', purpose: 'user_approval', question: '종료 조건이 모두 충족되었습니다. 승인하시겠습니까?' });
  return 'ok';
})()" >/dev/null
ab open "$BASE_URL/rooms/$RID" >/dev/null
ab wait '[data-testid="hitl-card"]' --timeout 20000 >/dev/null
sleep 1
APPROVE=$(apic '(document.querySelector("[data-testid=hitl-approve]")||{}).textContent')
REJECT=$(apic '(document.querySelector("[data-testid=hitl-reject]")||{}).textContent')
PLAIN=$(apic 'document.querySelectorAll("[data-testid=timeline-hitl] [data-testid=message-card]").length')
echo "  buttons=[$APPROVE][$REJECT] plain-fallback=$PLAIN"
apic '(function(){var c=document.querySelector("[data-testid=hitl-card]");window.scrollTo(0,Math.max(0,c.getBoundingClientRect().top+window.scrollY-220));return "ok"})()' >/dev/null
sleep 1
ab screenshot "$SHOT_DIR/approval-$TAG-s7-card.png" >/dev/null
echo "  📸 $SHOT_DIR/approval-$TAG-s7-card.png"

# ── 3) 실측 재현 — 화면이 목록을 읽은 뒤에 열린 요청(프레임 없음) ────────────
RID2=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rts = await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j);
  const ags = await fetch(`/api/v1/workspaces/${ws}/agents`).then(j);
  const lead = (ags.items ?? []).find((a) => a.name === "Lead");
  const r = await post(`/__mock/workspaces/${ws}/seed-room`, {
    title: "게임 제작 방 (프레임 유실)", goal: "턴제 레이싱 게임을 만든다", isolation: { kind: "none" },
    runtime_id: (Array.isArray(rts) ? rts : rts.items)[0].id, assignee_agent_id: lead.id, participants: [{ agent_id: lead.id }],
  });
  return r.id;
})()')
ab open "$BASE_URL/rooms/$RID2" >/dev/null
ab wait '[data-testid="timeline"]' --timeout 20000 >/dev/null
sleep 1
# 화면이 목록을 이미 읽은 뒤에 요청이 열린다 — 서버가 hitl.created 를 안 보내던 그 상태.
apic "
(async () => {
  const post = (p, b) => fetch('/api/v1' + p, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(b ?? {}) }).then((r) => r.json());
  await post('/__mock/rooms/$RID2/seed-hitl', { source: 'system', type: 'approval', purpose: 'user_approval', no_emit: true, question: '종료 조건이 모두 충족되었습니다. 승인하시겠습니까?' });
  return 'ok';
})()" >/dev/null
sleep 3
CARD2=$(apic 'document.querySelectorAll("[data-testid=hitl-card]").length')
LOADING=$(apic 'document.querySelectorAll("[data-testid=hitl-card-loading]").length')
echo "  stale-list: hitl-card=$CARD2 loading=$LOADING (고치기 전에는 둘 다 0 — 평문 한 줄)"
apic '(function(){var t=document.querySelector("[data-testid=timeline]");window.scrollTo(0,Math.max(0,t.getBoundingClientRect().top+window.scrollY-120));return "ok"})()' >/dev/null
sleep 1
ab screenshot "$SHOT_DIR/approval-$TAG-s7-stale.png" >/dev/null
echo "  📸 $SHOT_DIR/approval-$TAG-s7-stale.png"
apic "(function(){try{localStorage.removeItem('colab.theme')}catch(e){};return 'ok'})()" >/dev/null
