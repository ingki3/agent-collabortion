#!/usr/bin/env bash
# e2e/p2/11_scenario_a_web.sh — 같은 시나리오 A 를 **웹(agent-browser)** 으로, EVAL_USER U2·U4·U5 여정대로.
#
# 10_ 이 API/CLI 경로라면 이것은 사람이 보는 경로다. 판정은 "화면에 무엇이 보이는가"다:
#   U2-1 lane 보드 카드 3장 running + 브리프 한 줄      U2-3 Researcher 칩 working · Lead 칩 idle
#   U2-4 2장 done · 1장 running 인데 Lead 는 여전히 idle  U2-5 합류 시스템 메시지 **한 번** + Lead working
#   U2-6 Writer 카드 + 우열 종료 조건 진행률 0/2         U5-1 제출 후 1/2 + 아티팩트 행
#   U4-1 작성창 트리거 미리보기 칩이 **서버 값**(previewTriggers) 인가
#   U15-3 `@all` 은 "트리거 없음 — 기록만"
# 스크린샷: web/__screenshots__/p2-a-*.png · 단계 판정: out/w-steps.tsv
source "$(dirname "$0")/lib.sh"
cd "$E2E_ROOT/web"
SHOT_DIR="__screenshots__"; mkdir -p "$SHOT_DIR"
STAMP="$(date +%s)"
EMAIL="g4w+$STAMP@example.com"; PASSWORD="password123"
CFG="$OUT/daemon-w.json"; WORK="$OUT/work-w"; DLOG="$OUT/daemon-w.log"
COOKIE="$OUT/cookies-w.txt"; rm -f "$COOKIE"
STEPS="$OUT/w-steps.tsv"; echo -e "step\tscreen\texpected\tresult\tnote" > "$STEPS"
export AGENT_BROWSER_SESSION="colab-g4-a-${STAMP}"
ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null 2>&1; log "📸 $1.png"; }
rec() { echo -e "$1\t$2\t$3\t$4\t${5:-}" >> "$STEPS"; [ "$4" = PASS ] && ok "$1 $3" || bad "$1 $3 — $4 ${5:-}"; }
try() { "$@" >/dev/null 2>&1; }
wait_sel() { ab wait "$1" --timeout "$((${2:-25} * 1000))" >/dev/null 2>&1; }
wait_fn()  { ab wait --fn "$1" --timeout "$((${2:-60} * 1000))" >/dev/null 2>&1; }
count()    { ab get count "$1" 2>/dev/null || echo 0; }
cleanup() { ab close >/dev/null 2>&1 || true; [ -f "$OUT/daemon-w.pid" ] && kill -TERM -- "-$(cat "$OUT/daemon-w.pid")" 2>/dev/null; return 0; }
trap cleanup EXIT
ab set viewport 1440 1000 >/dev/null

step "0. 계정·런타임·에이전트는 API 로 준비한다(웹 판정 대상은 S6·S7 이다)"
signup "$EMAIL" "$PASSWORD" "민지" >/dev/null
WS="$(create_workspace "G4 Web A $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "$SERVER_URL" --workdir-root "$WORK" --no-turn 2>&1 | tail -1 >&2
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-w.pid"
wait_pairing "$WS" "$PID_" 90 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
source "$P2_DIR/fixtures/scenario_a_agents.sh"
LEAD="$(create_agent_p2 "$WS" Lead lead "$LEAD_MODEL" "$LEAD_INS" '팀을 이끌고 위임·종합한다')"
RSCH="$(create_agent_p2 "$WS" Researcher researcher "$LEAD_MODEL" "$RES_INS" '주어진 항목을 조사해 요약한다')"
WRTR="$(create_agent_p2 "$WS" Writer writer "$LEAD_MODEL" "$WRITER_INS" '보고서 초안을 쓰고 아티팩트로 제출한다')"
ok "ws=$WS runtime=$RUNTIME"

step "1. 로그인 → S6 마법사로 세션 만들기 (사람이 하는 경로)"
ab open "$WEB_URL/login" >/dev/null
wait_sel '[data-testid="login-form"]' || die "login form"
ab fill 'input[name=email]' "$EMAIL" >/dev/null; ab fill 'input[name=password]' "$PASSWORD" >/dev/null
ab click 'button[type=submit]' >/dev/null
try ab wait --url "**/sessions" --timeout 20000 || true
ab open "$WEB_URL/sessions/new" >/dev/null
wait_sel '[data-testid="session-wizard"]' 25 || die "S6 마법사가 열리지 않음"
ab fill '[data-testid="session-title"]' "제품 X 시장 조사 (웹)" >/dev/null
ab fill '[data-testid="session-goal"]' "$SCENARIO_GOAL" >/dev/null
shot p2-a-01-wizard-goal
STEPS_N="$(count '[data-testid="wizard-steps"] span')"
rec W1 S6-1 "마법사 7단계 · 제목·goal 입력" "$( [ "$STEPS_N" -ge 7 ] && echo PASS || echo FAIL )" "steps=$STEPS_N"
# 2 Director → 3 격리 → 4 런타임 → 5 참여자 → 6 종료 조건 → 7 한도
ab click '[data-testid="wizard-next"]' >/dev/null   # → director
ab click '[data-testid="wizard-next"]' >/dev/null   # → isolation
wait_sel '[data-testid="wizard-isolation"]' 10
ab click '[data-testid="wizard-next"]' >/dev/null   # → runtime
wait_sel '[data-testid="wizard-runtime"]' 10
sleep 2
RC="$(count '[data-testid="runtime-candidate"]')"
WERR="$(ab get text '[data-testid="new-session-error"]' 2>/dev/null | tr '\n' ' ' | head -c 120)"
rec W2 S6-4 "런타임 후보에 방금 연결한 컴퓨터가 보인다" "$( [ "${RC:-0}" -ge 1 ] && echo PASS || echo FAIL )" "candidates=${RC:-0} error='$WERR'"
[ "${RC:-0}" -ge 1 ] && ab click '[data-testid="runtime-candidate"]' >/dev/null
ab click '[data-testid="wizard-next"]' >/dev/null   # → participants
wait_sel '[data-testid="wizard-participants"]' 10
PN="$(count '[data-testid="participant-option"]')"
for id in "$LEAD" "$RSCH" "$WRTR"; do ab click "[data-testid=\"participant-option\"][data-agent-id=\"$id\"] input[type=checkbox]" >/dev/null 2>&1 || true; done
ab click "[data-testid=\"participant-option\"][data-agent-id=\"$LEAD\"] [data-testid=\"assignee-radio\"]" >/dev/null 2>&1 || true
shot p2-a-02-wizard-participants
rec W3 S6-5 "참여자 3명 + assignee=Lead" "$( [ "$PN" -ge 3 ] && echo PASS || echo FAIL )" "options=$PN"
ab click '[data-testid="wizard-next"]' >/dev/null   # → conditions
wait_sel '[data-testid="wizard-conditions"]' 10
COND="$(ab get text '[data-testid="wizard-conditions"]' 2>/dev/null | tr '\n' ' ')"
rec W4 S6-6 "종료 조건 기본값 = 아티팩트 제출 AND Director 승인" \
  "$( grep -q "아티팩트 제출" <<<"$COND" && grep -q "승인" <<<"$COND" && echo PASS || echo FAIL )" "$(head -c 110 <<<"$COND")"
ab click '[data-testid="wizard-next"]' >/dev/null   # → limits/summary
wait_sel '[data-testid="wizard-summary"]' 10
shot p2-a-03-wizard-summary
T0="$(now_ms)"
ab click '[data-testid="session-start"]' >/dev/null
if wait_fn "/^\\/sessions\\/[0-9a-f-]{36}$/.test(location.pathname)" 25; then
  WIZARD_OK=yes
else
  # 마법사가 세션을 만들지 못하면 U2 여정 자체를 볼 수 없다 — 원인을 적고 API 로 같은 세션을 만들어 S7 판정을 계속한다.
  WIZARD_OK=no
  WERR2="$(ab get text '[data-testid="new-session-error"]' 2>/dev/null | tr '\n' ' ' | head -c 160)"
  rec W4b S6 "마법사 '시작' 이 세션을 만든다" FAIL "error='$WERR2' url=$(ab get url)"
  SESSION="$(create_session_p2 "$WS" "제품 X 시장 조사 (웹)" "$SCENARIO_GOAL" "$LEAD" "$RUNTIME" "$WRTR" "$LEAD" "$RSCH" "$WRTR")"
  ab open "$WEB_URL/sessions/$SESSION" >/dev/null
fi
wait_sel '[data-testid="session-detail"]' 25 || die "S7 이 열리지 않음"
SESSION="$(ab get attr '[data-testid="session-detail"]' data-session-id)"
[ "$WIZARD_OK" = yes ] && rec W4b S6 "마법사 '시작' 이 세션을 만든다" PASS "session=$SESSION"
echo "$WS $SESSION $LEAD $RSCH $WRTR $RUNTIME" > "$OUT/w-ids.txt"
ok "session $SESSION"

step "2. U2-1·3 — lane 보드 카드 3장 running · 참여자 칩"
# 카드 3장이 동시에 running 인 순간을 화면에서 잡는다(폴링으로 최댓값을 기록).
MAXRUN=0
END=$(( $(date +%s) + 180 ))
while [ "$(date +%s)" -lt "$END" ]; do
  N="$(count '[data-testid="lane-card"][data-status="running"]')"
  [ "${N:-0}" -gt "$MAXRUN" ] && { MAXRUN="$N"; [ "$N" -ge 3 ] && shot p2-a-04-lane-board-3running; }
  [ "$MAXRUN" -ge 3 ] && break
  DONE_N="$(count '[data-testid="lane-card"][data-status="done"]')"
  [ "${DONE_N:-0}" -ge 3 ] && break
  sleep 2
done
[ "$MAXRUN" -lt 3 ] && shot p2-a-04-lane-board-3running
rec W5 S7 "lane 보드에 Researcher 카드 3장이 **동시에** running (U2-1)" "$( [ "$MAXRUN" -ge 3 ] && echo PASS || echo FAIL )" "화면에서 본 최대 동시 running=$MAXRUN"
BRIEF_N="$(count '[data-testid="lane-brief"]')"
rec W6 S7 "각 카드에 브리프 한 줄 (U2-1)" "$( [ "${BRIEF_N:-0}" -ge 3 ] && echo PASS || echo FAIL )" "lane-brief=$BRIEF_N"
CHIP_R="$(ab get attr '[data-testid="participants"] [data-testid="agent-chip"][data-agent-id="'"$RSCH"'"]' data-status 2>/dev/null || ab get attr '[data-testid="agent-chip"]' data-status 2>/dev/null || echo '?')"
CHIP_L="$(ab get attr '[data-testid="participants"] [data-testid="agent-chip"][data-agent-id="'"$LEAD"'"]' data-status 2>/dev/null || echo '?')"
rec W7 S7 "Researcher 칩 working · Lead 칩 idle (U2-3 · E5-11)" \
  "$( [ "$CHIP_R" = working ] && [ "$CHIP_L" = idle ] && echo PASS || echo FAIL )" "Researcher=$CHIP_R Lead=$CHIP_L"

step "3. U4-1 · U15-3 — 작성창 트리거 미리보기가 **서버 값**인가"
# 로컬 계산이면 서버를 끊어도 칩이 뜬다. 여기서는 previewTriggers 응답과 화면 칩을 대조한다.
ab fill '[data-testid="composer-input"]' "$(mention Researcher "$RSCH") 범위를 국내로 좁혀줘" >/dev/null
wait_sel '[data-testid="chip-trigger"]' 15
CHIPTXT="$(ab get text '[data-testid="composer-chips"]' 2>/dev/null | tr '\n' ' ')"
PV="$(api_ok POST "/sessions/$SESSION/messages/preview" "$(jq -nc --arg c "$(mention Researcher "$RSCH") 범위를 국내로 좁혀줘" '{content:$c}')")"
PV_NAME="$(jq -r '.triggers[0].agent_name // empty' <<<"$PV")"
PV_PROF="$(jq -r '.triggers[0].profile.name // empty' <<<"$PV")"
shot p2-a-05-composer-preview
rec W8 S7 "미리보기 칩이 서버 previewTriggers 와 같은 에이전트를 말한다 (U4-1 · FR-3.6)" \
  "$( [ -n "$PV_NAME" ] && grep -q "$PV_NAME" <<<"$CHIPTXT" && echo PASS || echo FAIL )" "chip='$(head -c 90 <<<"$CHIPTXT")' server=$PV_NAME/$PV_PROF"
ab fill '[data-testid="composer-input"]' "[@all](mention://all) 다들 상황 공유" >/dev/null
wait_sel '[data-testid="chip-no-trigger"], [data-testid="chip-note-only"]' 15
NOTRIG="$(ab get text '[data-testid="composer-chips"]' 2>/dev/null | tr '\n' ' ')"
rec W9 S7 "@all 은 '트리거 없음 — 기록만' (U15-3 · E1-05)" \
  "$( grep -qE "트리거 없음|기록만" <<<"$NOTRIG" && echo PASS || echo FAIL )" "$(head -c 90 <<<"$NOTRIG")"
ab fill '[data-testid="composer-input"]' "" >/dev/null 2>&1 || true

step "4. U2-5 — 합류가 화면에 **한 번**, U2-6 · U5-1 진행률"
wait_fn "document.querySelectorAll('[data-testid=\"message-card\"]').length >= 6" 240 || true
JOIN_SEEN="$(ab get count '[data-testid="message-card"]' 2>/dev/null || echo 0)"
JOIN_TXT_N="$(ab eval "[...document.querySelectorAll('[data-testid=\"message-card\"]')].filter(e=>e.textContent.includes('위임한 작업이 모두 끝났습니다')).length" 2>/dev/null | tr -dc '0-9')"
shot p2-a-06-join
rec W10 S7 "합류 시스템 메시지가 타임라인에 **한 번** (U2-5 · FR-6.5)" \
  "$( [ "${JOIN_TXT_N:-0}" = 1 ] && echo PASS || echo FAIL )" "합류 카드 수=${JOIN_TXT_N:-0} / 전체 카드=$JOIN_SEEN"
PROG0="$(ab get text '[data-testid="progress-count"]' 2>/dev/null | tr -d ' \n')"
rec W11 S7 "우열 종료 조건 진행률이 제출 전 0/2 (U2-6)" "$( [ "$PROG0" = "0/2" ] && echo PASS || echo FAIL )" "progress=$PROG0"

step "5. Writer 제출까지 기다린 뒤 U5-1 — 진행률 1/2 · 아티팩트 행"
END=$(( $(date +%s) + 420 ))
while [ "$(date +%s)" -lt "$END" ]; do
  [ -n "$(psqlq "select id from artifact where session_id='$SESSION' limit 1")" ] && break
  sleep 5
done
sleep 3
PROG1="$(ab get text '[data-testid="progress-count"]' 2>/dev/null | tr -d ' \n')"
ART_ROWS="$(count '[data-testid="artifact-row"]')"
shot p2-a-07-progress-artifact
rec W12 S7 "제출 후 진행률 1/2 (U5-1 · E6-01)" "$( [ "$PROG1" = "1/2" ] && echo PASS || echo FAIL )" "progress=$PROG1"
rec W13 S7 "우열 아티팩트 목록에 제출물이 보인다" "$( [ "${ART_ROWS:-0}" -ge 1 ] && echo PASS || echo FAIL )" "artifact-row=$ART_ROWS"
FEED="$(ab get text '[data-testid="activity-feed"]' 2>/dev/null | tr '\n' ' ' | head -c 200)"
rec W14 S7 "활동 피드에 실행 흐름이 렌더된다 (컷 1 판정 근거)" "$( [ -n "$FEED" ] && echo PASS || echo FAIL )" "$(head -c 120 <<<"$FEED")"

step "6. U5 — Director 승인 경로 (P2 는 인박스 항목 + 승인 API 까지)"
INBOX_CODE="$(curl -sS -o /dev/null -w '%{http_code}' -b "$COOKIE" "$API/inbox")"
HITL="$(psqlq "select id from hitl_request where session_id='$SESSION' order by created_at desc limit 1")"
rec W15 S8 "artifact_submitted 충족 시 user_approval HITL 발행 (E6-01)" \
  "$( [ -n "$HITL" ] && echo PASS || echo FAIL )" "hitl_request=${HITL:-없음} · GET /inbox=$INBOX_CODE (인박스 화면은 P3)"

step "결과"
column -t -s $'\t' "$STEPS" >&2
jq -n --arg session "$SESSION" --arg ws "$WS" --argjson maxrun "${MAXRUN:-0}" \
  --arg progress_before "${PROG0:-}" --arg progress_after "${PROG1:-}" \
  --argjson pass "$(awk -F'\t' 'NR>1&&$4=="PASS"' "$STEPS" | wc -l)" --argjson fail "$(awk -F'\t' 'NR>1&&$4=="FAIL"' "$STEPS" | wc -l)" \
  '{session:$session,workspace:$ws,max_concurrent_running_cards_seen:$maxrun,progress_before:$progress_before,progress_after:$progress_after,pass:$pass,fail:$fail}' | tee "$OUT/w-summary.json"
