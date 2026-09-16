#!/usr/bin/env bash
# T-W16 스크린샷 — v1.1 첫 라운드: S14 「관찰」 표(K-18) · S10 역할의 허용 명령(K-19) · 빈 턴 카드(FR-7.2).
# `next build && next start` 로 찍는다(PR #186 NN5). 테마는 localStorage + <html data-theme>(PR #188 NN3).
#
#   p5-w16-01-s14-observations-{light,dark}.png   S14 대시보드 — 지표 10개 표 **아래** 「관찰」 표(목표치 없이 분포만 · 5행 · n 0 은 "아직 잴 수 없음" · breakdown 하위 행)
#   p5-w16-02-s10-role-commands-{light,dark}.png  S10 정체성 — Researcher 의 허용 명령("할 수 있는 일: … " + "…은 못 합니다 — Lead 의 일")
#   p5-w16-03-s10-role-preview-{light,dark}.png   S10 — 역할을 reviewer 로 바꾸면 저장 전 미리보기("저장하면 이 목록으로 바뀝니다")
#   p5-w16-04-s7-empty-turn-{light,dark}.png      S7 — 빈 턴: 작업 줄기 카드 ⓘ 한 줄 + 이력 「활동」 의 정보 카드(오류 아님)
#   p5-w16-05-s7-done-cancel-{light,dark}.png     S7 — K-16(계약 v0.1.6 #255): done 줄기인데 실행이 아직 돌면 「중단」 + 안내 한 줄(T-W17)
#   (V-1: 01 관찰 표에 「다시 세기」 · routing 값 옆 "규칙 6·7 폴백 비율" 한 줄 — 같은 컷에 든다)
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3117 &
#   BASE_URL=http://localhost:3117 bash e2e/p5-w16-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-w16-shots-$$}"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }
# 클릭 → 기대 selector 가 나올 때까지(하이드레이션 직후 첫 클릭이 새는 일이 있다 — PR #199 함정) 최대 3번.
click_until() { for _ in 1 2 3; do ab click "$1" >/dev/null 2>&1 || true; ab wait "$2" --timeout 5000 >/dev/null 2>&1 && return 0; sleep 1; done; ab wait "$2" --timeout 5000 >/dev/null; }
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

step "목 저장소 초기화"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null

step "로그인"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

RES=$(apic '(async () => { const me = await fetch("/api/v1/me").then(r => r.json()); const a = await fetch(`/api/v1/workspaces/${me.workspaces[0].id}/agents`).then(r => r.json()); return a.items.find(x => x.name === "Researcher").id; })()')
echo "  researcher=$RES"

for THEME in light dark; do
  step "테마 $THEME — S14 대시보드: 지표 표 아래 「관찰」 표"
  set_theme "$THEME"
  open_wait "/settings?tab=dashboard" '[data-testid="observations-table"]'
  apic "(function(){document.querySelector('[data-testid=observations-wrap]').scrollIntoView({block:'end'});return 'ok'})()" >/dev/null
  shot_full "p5-w16-01-s14-observations-$THEME"

  step "테마 $THEME — S10 Researcher 의 허용 명령(읽기 전용)"
  open_wait "/agents/$RES" '[data-testid="role-commands"]'
  apic "(function(){document.querySelector('[data-testid=agent-identity]').scrollIntoView({block:'start'});return 'ok'})()" >/dev/null
  shot "p5-w16-02-s10-role-commands-$THEME"

  step "테마 $THEME — S10 역할을 reviewer 로 바꾸면 저장 전 미리보기"
  ab select '[data-testid="agent-role"]' reviewer >/dev/null
  ab wait '[data-testid="role-commands"][data-preview="true"]' --timeout 5000 >/dev/null
  shot "p5-w16-03-s10-role-preview-$THEME"
done

# 브라우저를 새로 연다 — 앞 화면들이 연 SSE 스트림이 헤드리스에서 곧바로 닫히지 않아 호스트당 연결 6개가 차면 다음 fetch 가 10초 넘게
# 멈춘다(「이전 작업」 이 "불러오는 중…" 에 걸림). 사용자는 Link 로 이동해 연결 하나를 유지하므로 화면의 문제가 아니다.
ab close >/dev/null 2>&1 || true
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION}-s7"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

step "시드 — 세션 하나(Researcher 참여) + 빈 턴 한 번"
SID=$(apic '
(async () => {
  const j = (r) => r.json();
  const post = (p, b) => fetch(`/api/v1${p}`, { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(b ?? {}) }).then(j);
  const me = await fetch("/api/v1/me").then(j);
  const ws = me.workspaces[0].id;
  const rt = (await fetch(`/api/v1/workspaces/${ws}/runtimes`).then(j))[0];
  const a = (await fetch(`/api/v1/workspaces/${ws}/agents`).then(j)).items;
  const res = a.find((x) => x.name === "Researcher");
  const s = await post(`/workspaces/${ws}/sessions`, {
    title: "국내 B2B SaaS 결제 시장 조사", goal: "보고서 10페이지 — 상위 5개 사업자 비교", isolation: { kind: "none" }, runtime_id: rt.id,
    participants: [{ agent_id: res.id }], assignee_agent_id: res.id, completion_condition: { op: "and", conditions: [{ type: "manual" }] },
  });
  await new Promise((r) => setTimeout(r, 3500)); // 세션 시작 턴(목의 simulateRun)이 끝난 뒤에 빈 턴을 얹는다
  const seeded = await post(`/__mock/sessions/${s.id}/seed-empty-turn`, { agent_id: res.id });
  return `${s.id} ${seeded.lane_id}`;
})()')
LANE=${SID#* }; SID=${SID%% *}
echo "  session=$SID lane=$LANE"

for THEME in light dark; do
  step "테마 $THEME — S7 빈 턴: 이전 작업 → 활동 → 정보 카드 + 카드 ⓘ 한 줄"
  set_theme "$THEME"
  # 세션 시작 턴(담당 에이전트의 첫 답)의 줄기도 있다 — 빈 턴 줄기는 시드가 돌려준 lane id 로 고른다.
  open_wait "/sessions/$SID" "[data-lane-id=\"$LANE\"]"
  sleep 2
  click_until "[data-lane-id=\"$LANE\"] [data-testid=\"lane-tasks-toggle\"]" "[data-lane-id=\"$LANE\"] [data-testid=\"task-activity-toggle\"]"
  click_until "[data-lane-id=\"$LANE\"] [data-testid=\"task-activity-toggle\"]" '[data-testid="feed-row-empty-turn"]'
  ab wait '[data-testid="lane-empty-turn"]' --timeout 5000 >/dev/null
  apic "(function(){document.querySelector('[data-lane-id=\"$LANE\"]').scrollIntoView({block:'center'});return 'ok'})()" >/dev/null
  shot "p5-w16-04-s7-empty-turn-$THEME"
done

# K-16(T-W17) — `colab status set done` 뒤에도 그 턴의 프로세스가 도는 줄기: 서버가 현재 할 일(running)로 판정해 actions 에 cancel 을 싣고,
# 카드는 그 목록 그대로 「중단」을 낸다(+ 왜 done 카드에 「중단」이 있는지 한 줄). 확인 다이얼로그 문장도 done 전용.
step "시드 — done 인데 실행이 아직 도는 줄기(K-16)"
DR=$(apic "(async () => { const r = await fetch('/api/v1/__mock/sessions/$SID/seed-done-running', { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' }).then(x => x.json()); return r.lane_id + ' ' + (r.lane.actions || []).join(','); })()")
DLANE=${DR%% *}; DACT=${DR#* }
echo "  lane=$DLANE actions=$DACT"
[ "$DACT" = "cancel" ] || { echo "❌ done+running 줄기의 actions 가 [cancel] 이 아니다: $DACT"; exit 1; }
for THEME in light dark; do
  step "테마 $THEME — S7 done 줄기의 「중단」(K-16)"
  set_theme "$THEME"
  open_wait "/sessions/$SID" "[data-lane-id=\"$DLANE\"] [data-testid=\"lane-action-cancel\"]"
  ab wait "[data-lane-id=\"$DLANE\"] [data-testid=\"lane-done-running\"]" --timeout 5000 >/dev/null
  ab click "[data-lane-id=\"$DLANE\"] [data-testid=\"lane-action-cancel\"]" >/dev/null
  ab wait '[data-testid="cancel-confirm"]' --timeout 5000 >/dev/null
  # 확인 상자는 좌열(sticky · 안쪽 스크롤) 맨 아래에 붙는다 — 줄기가 셋이면 뷰포트 밖(top≈898/900)이라 agent-browser 클릭이 닿지 않는다.
  # 상자를 열 안으로 끌어올린 뒤 찍고 누른다.
  apic "(function(){document.querySelector('[data-testid=cancel-confirm]').scrollIntoView({block:'nearest'});return 'ok'})()" >/dev/null
  shot "p5-w16-05-s7-done-cancel-$THEME"
  ab click '[data-testid="cancel-confirm-no"]' >/dev/null
done
# 한 번 실제로 중단 — lane 은 done 그대로, 할 일만 cancelled, 버튼은 사라진다(202 뒤 lane.updated).
ab click "[data-lane-id=\"$DLANE\"] [data-testid=\"lane-action-cancel\"]" >/dev/null
ab wait '[data-testid="cancel-confirm-yes"]' --timeout 5000 >/dev/null
apic "(function(){document.querySelector('[data-testid=cancel-confirm]').scrollIntoView({block:'nearest'});return 'ok'})()" >/dev/null
ab click '[data-testid="cancel-confirm-yes"]' >/dev/null
# 202 응답으로 카드가 바뀔 때까지(최대 10초) 기다린다 — 빌드 서버라도 보통 1초 안.
AFTER=""
for _ in $(seq 1 20); do
  AFTER=$(apic "(function(){var c=document.querySelector('[data-lane-id=\"$DLANE\"]');return c.getAttribute('data-status')+' '+(c.querySelector('[data-testid=lane-action-cancel]')?'btn':'nobtn')})()")
  [ "$AFTER" = "done nobtn" ] && break
  sleep 0.5
done
echo "  중단 뒤: $AFTER"
[ "$AFTER" = "done nobtn" ] || {
  echo "❌ 중단 뒤 상태가 'done nobtn' 이 아니다: $AFTER"
  apic "(function(){var y=document.querySelector('[data-testid=cancel-confirm-yes]');var r=y&&y.getBoundingClientRect();return JSON.stringify({confirm:!!document.querySelector('[data-testid=cancel-confirm]'),yes:r&&[r.top,r.bottom,r.left,r.right],vh:window.innerHeight,disabled:y&&y.disabled,err:(document.querySelector('.problem')||{}).textContent||null})})()"
  apic "fetch('/api/v1/sessions/$SID/lanes').then(r=>r.json()).then(ls=>JSON.stringify(ls.map(l=>[l.id.slice(0,8),l.status,l.actions,l.current_task&&l.current_task.status])))"
  exit 1
}

echo
echo "✅ T-W16 스크린샷 완료 → $SHOT_DIR/p5-w16-*.png"
