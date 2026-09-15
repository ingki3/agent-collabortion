#!/usr/bin/env bash
# e2e/p2/34_template_3min.sh — G5 (e): 팀 템플릿에서 팀 생성 → 세션 시작까지의 시간.
#
# PLAN §6.2 의 DoD 는 **Director 실측 3분**이다. 사람이 재는 것이므로 이 스크립트는 판정하지 않는다 —
# 하는 일은 두 가지다:
#   (1) **경로가 실제로 있는가**를 화면에서 확인한다(S9 팀 템플릿 → S6 마법사 → 세션 시작).
#       한 칸이라도 막히면 Director 가 3분 안에 끝낼 방법이 없으므로 그것은 여기서 FAIL 로 잡힌다.
#   (2) 각 구간의 **기계 소요 시간**을 재서 사람 실측의 하한을 준다(사람은 읽고 고르는 시간이 더 든다).
# 판정 칸은 보고서에서 "Director 실측 대기" 로 남는다.
#
# 구간: T0 로그인 완료 → T1 템플릿 적용 완료 → T2 세션 생성(S7 열림) → T3 첫 lane running
# 스크린샷: web/__screenshots__/p2-t-*.png · 산출물: out/template.json · out/t-checks.tsv
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-t.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-t.json"; WORK="$OUT/work-t"; DLOG="$OUT/daemon-t.log"
SHOT="$E2E_ROOT/web/__screenshots__"; mkdir -p "$SHOT"
TEMPLATE="${TEMPLATE_KEY:-research_team}"
g5_chk_init "$OUT/t-checks.tsv"
export AGENT_BROWSER_SESSION="colab-g5-t-${STAMP}"
ab() { agent-browser "$@"; }
abget() { agent-browser "$@" 2>/dev/null || true; }
shot() { ab screenshot "$SHOT/$1.png" >/dev/null 2>&1; log "📸 $1.png ($(date '+%H:%M:%S'))"; }
count() { local n; n="$(abget get count "$1")"; echo "${n:-0}"; }
wait_sel() { ab wait "$1" --timeout "$((${2:-25} * 1000))" >/dev/null 2>&1; }

cleanup() { ab close >/dev/null 2>&1 || true; [ -f "$OUT/daemon-t.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-t.pid")" 2>/dev/null || true; }; return 0; }
trap cleanup EXIT

command -v agent-browser >/dev/null 2>&1 || die "agent-browser 가 필요하다 (이 스크립트는 사람 경로를 잰다)"

step "0. 사전 준비 — 계정과 연결된 컴퓨터는 사람이 이미 갖고 있다고 본다(측정 구간 밖)"
: > "$DLOG"
EMAIL="g5t+$STAMP@example.com"; PASSWORD="password123"
signup "$EMAIL" "$PASSWORD" "민지" >/dev/null
WS="$(create_workspace "G5 Template $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "$SERVER_URL" --workdir-root "$WORK" --no-turn 2>&1 | tail -2 >&2
# `daemon run --no-turn` 은 재시작 때 capabilities.models 를 **빈 배열로 덮는다**(G3_REPORT §2).
# 템플릿의 프로파일 자동 매핑이 바로 그 models 를 읽으므로, 여기서는 반드시 턴을 도는 run 을 쓴다.
daemon_start_p2 "$CFG" "$DLOG" > "$OUT/daemon-t.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
# 능력 광고에 모델이 실릴 때까지 기다린다 — 측정 구간 밖이다(사람은 이미 연결해 둔 상태에서 시작한다)
for i in $(seq 1 60); do
  [ "$(psqlq "select coalesce(jsonb_array_length((capabilities->0)->'models'),0) from runtime where id='$RUNTIME'")" -ge 1 ] && break
  sleep 3
done
MODELS_N="$(psqlq "select coalesce(jsonb_array_length((capabilities->0)->'models'),0) from runtime where id='$RUNTIME'")"
ok "ws=$WS runtime=$RUNTIME (능력 광고 모델 $MODELS_N 개까지 끝난 상태에서 시작한다)"

step "1. 로그인 → T0"
ab set viewport 1440 1000 >/dev/null
ab open "$WEB_URL/login" >/dev/null
wait_sel '[data-testid="login-form"]' || die "login form"
ab fill 'input[name=email]' "$EMAIL" >/dev/null
ab fill 'input[name=password]' "$PASSWORD" >/dev/null
ab click 'button[type=submit]' >/dev/null
ab wait --url "**/sessions" --timeout 20000 >/dev/null 2>&1 || true
T0="$(now_ms)"; log "T0 로그인 완료 $(date '+%H:%M:%S')"

step "2. S9 — 팀 템플릿 목록 → 리서치 팀 적용"
ab open "$WEB_URL/agents" >/dev/null
wait_sel '[data-testid="open-templates"]' 25 || die "S9 에이전트 화면이 열리지 않음"
ab click '[data-testid="open-templates"]' >/dev/null
wait_sel '[data-testid="team-templates"]' 20 || true
ab wait --fn 'document.querySelectorAll("[data-testid=template-card]").length>0' --timeout 20000 >/dev/null 2>&1 || true
TCARDS="$(count '[data-testid="template-card"]')"
shot p2-t-01-templates
chk T1  "팀 템플릿 3종이 보인다 (FR-1.4)"                  3 "$TCARDS"
chk T1b "리서치 팀 카드가 있다"                            1 "$(count "[data-testid=\"template-card\"][data-key=\"$TEMPLATE\"]")"
MAPPED="$(count '[data-testid="template-agent"][data-mapping="mapped"]')"
UNMAPPED="$(count '[data-testid="template-agent"][data-mapping="unmapped"]')"
chk_ge T1c "템플릿 에이전트에 프로파일이 자동 매핑됐다"     1 "$MAPPED"
chk T1d "매핑 실패가 0 이다 (unmapped 는 프로파일 없는 에이전트를 만든다 — S-30)" 0 "$UNMAPPED"
log "매핑 mapped=$MAPPED · unmapped=$UNMAPPED (unmapped 는 등록은 되지만 사람이 프로파일을 골라야 한다)"
ab click "[data-testid=\"apply-$TEMPLATE\"]" >/dev/null
wait_sel '[data-testid="template-applied"]' 30 || true
ab wait --fn 'document.querySelectorAll("[data-testid=agent-card]").length>=3' --timeout 30000 >/dev/null 2>&1 || true
T1="$(now_ms)"
AGENT_CARDS="$(count '[data-testid="agent-card"]')"
shot p2-t-02-agents-created
chk T2  "템플릿 한 번으로 에이전트가 일괄 생성됐다"        yes "$( [ "${AGENT_CARDS:-0}" -ge 3 ] && echo yes || echo no )"
chk T2b "DB 의 에이전트 수가 화면과 같다"                  "$AGENT_CARDS" "$(psqlq "select count(*) from agent where workspace_id='$WS' and archived_at is null")"
chk T2d "만들어진 에이전트가 전부 기본 프로파일을 갖는다 (없으면 세션에서 못 쓴다)" 0 \
  "$(psqlq "select count(*) from agent a where a.workspace_id='$WS' and not exists (select 1 from agent_profile p where p.agent_id=a.id)")"
chk T2c "definition_source 에 템플릿 키가 남는다 (FR-1.7)"  yes \
  "$( [ "$(psqlq "select count(*) from agent where workspace_id='$WS' and definition_source::text like '%$TEMPLATE%'")" -ge 1 ] 2>/dev/null && echo yes || echo no )"
log "T1 템플릿 적용 완료 $(date '+%H:%M:%S') — 구간 $(( (T1-T0)/1000 ))s"

step "3. S6 마법사 — 목표 → 참여자 → 종료 조건 → 시작"
AGENT_IDS="$(psqlq "select id from agent where workspace_id='$WS' and archived_at is null order by created_at")"
FIRST_AGENT="$(head -1 <<<"$AGENT_IDS")"
ab open "$WEB_URL/sessions/new" >/dev/null
wait_sel '[data-testid="session-wizard"]' 25 || die "S6 마법사가 열리지 않음"
ab fill '[data-testid="session-title"]' "제품 X 시장 조사 (템플릿)" >/dev/null
ab fill '[data-testid="session-goal"]' "$SCENARIO_GOAL" >/dev/null
next_step() { sleep 1; ab click '[data-testid="wizard-next"]' >/dev/null; wait_sel "[data-testid=\"$1\"]" "${2:-15}"; }
next_step wizard-director   || true
next_step wizard-isolation  || true
next_step wizard-runtime    || true
sleep 2
RC="$(count '[data-testid="runtime-candidate"]')"
chk T3  "런타임 후보에 연결한 컴퓨터가 보인다"             yes "$( [ "${RC:-0}" -ge 1 ] && echo yes || echo no )"
[ "${RC:-0}" -ge 1 ] && ab click '[data-testid="runtime-candidate"]' >/dev/null
next_step wizard-participants || true
PN="$(count '[data-testid="participant-option"]')"
chk T4  "마법사가 템플릿으로 만든 에이전트를 참여자 후보로 준다" yes \
  "$( [ "${PN:-0}" -ge 3 ] && echo yes || echo no )"
while read -r id; do [ -n "$id" ] && ab click "[data-testid=\"participant-option\"][data-agent-id=\"$id\"] input[type=checkbox]" >/dev/null 2>&1 || true; done <<<"$AGENT_IDS"
ab click "[data-testid=\"participant-option\"][data-agent-id=\"$FIRST_AGENT\"] [data-testid=\"assignee-radio\"]" >/dev/null 2>&1 || true
shot p2-t-03-wizard-participants
next_step wizard-conditions || true
# 마법사는 7단계다(STEPS: goal·Director·격리·런타임·참여자·종료 조건·한도). 마지막 단계에
# 한도와 요약이 함께 있고 '다음' 대신 '시작' 이 뜬다 — 여기서 한 번 더 누르면 셀렉터가 없다.
next_step wizard-limits     || true
shot p2-t-04-wizard-summary
sleep 1
ab click '[data-testid="session-start"]' >/dev/null
if ab wait --fn "/^\\/sessions\\/[0-9a-f-]{36}\$/.test(location.pathname)" --timeout 30000 >/dev/null 2>&1; then
  START_OK=yes
else
  START_OK=no
fi
wait_sel '[data-testid="session-detail"]' 25 || true
T2="$(now_ms)"
SESSION="$(abget get attr '[data-testid="session-detail"]' data-session-id)"
shot p2-t-05-session-open
chk T5  "마법사 '시작' 이 세션을 만든다"                   yes "$START_OK"
chk T5b "S7 이 열렸다 (세션 id 를 화면에서 읽는다)"        yes "$( [ -n "$SESSION" ] && echo yes || echo no )"
log "T2 세션 생성 $(date '+%H:%M:%S') — 마법사 구간 $(( (T2-T1)/1000 ))s"

step "4. 세션이 실제로 돌기 시작하는가 (T3)"
T3="$T2"; RUN_OK=no
if [ -n "$SESSION" ]; then
  END=$(( $(date +%s) + 180 ))
  while [ "$(date +%s)" -lt "$END" ]; do
    if [ "$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('preparing','dispatched','running','completed')")" -ge 1 ]; then
      RUN_OK=yes; T3="$(now_ms)"; break
    fi
    sleep 3
  done
  shot p2-t-06-lane-board
fi
chk T6  "세션 시작 직후 첫 task 가 실제로 돈다"            yes "$RUN_OK"
echo "$WS $SESSION $RUNTIME" > "$OUT/t-ids.txt"

step "5. 구간 시간 (사람 실측의 하한)"
D_TEMPLATE=$(( (T1-T0)/1000 )); D_WIZARD=$(( (T2-T1)/1000 )); D_TOTAL=$(( (T2-T0)/1000 )); D_RUN=$(( (T3-T0)/1000 ))
printf '  로그인→템플릿 적용   %4ds\n  템플릿→세션 생성     %4ds\n  합계(팀 생성→세션 시작) %4ds\n  첫 task 가 돌기까지  %4ds\n' \
  "$D_TEMPLATE" "$D_WIZARD" "$D_TOTAL" "$D_RUN" >&2
chk_ge T7 "합계가 측정됐다 (초)" 1 "$D_TOTAL"
log "판정은 하지 않는다 — DoD 의 3분은 **Director 실측**이다."
log "위 수치는 agent-browser 의 DOM 조작 기준이다: 셀렉터로 바로 채우고 누르므로 사람이 읽고 고르고"
log "타이핑하는 시간이 빠져 있다. 사람 실측의 **하한**이지 예측이 아니다."

step "결과"
printf '판정: PASS %d · FAIL %d  (경로 확인만; 3분 판정은 Director 실측 대기)\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "${SESSION:-}" --arg template "$TEMPLATE" \
  --argjson templates "${TCARDS:-0}" --argjson agents "${AGENT_CARDS:-0}" \
  --argjson mapped "${MAPPED:-0}" --argjson unmapped "${UNMAPPED:-0}" \
  --argjson t_template_s "$D_TEMPLATE" --argjson t_wizard_s "$D_WIZARD" \
  --argjson t_total_s "$D_TOTAL" --argjson t_first_task_s "$D_RUN" \
  --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,session:$session,template:$template,template_cards:$templates,agents_created:$agents,
    profile_mapping:{mapped:$mapped,unmapped:$unmapped},
    machine_seconds:{login_to_template:$t_template_s,template_to_session:$t_wizard_s,
                     total_team_to_session:$t_total_s,to_first_task:$t_first_task_s},
    verdict:"Director 실측 대기",pass:$pass,fail:$fail}' | tee "$OUT/template.json"
[ "$fail" = 0 ]
