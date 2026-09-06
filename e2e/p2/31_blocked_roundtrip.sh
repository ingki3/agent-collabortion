#!/usr/bin/env bash
# e2e/p2/31_blocked_roundtrip.sh — G5 (c): blocked 왕복 E3-05 · E3-06 · E3-07 을 실기로.
#
#   E3-05  자식이 `colab status set blocked --note` → lane blocked(workdir 보존) ·
#          lane 스레드에 **질문 카드**(kind=blocked_q) · `lane.blocked_message_id` ·
#          위임자 **즉시** 기상(형제 상태 무관, 합류가 아니다)
#   E3-06  형제가 전부 done → 합류 발화(blocked = 종료 취급) · 질문 재포함 ·
#          "답을 기다리는 자식 1개" 문구
#   E3-07  위임자가 질문 카드에 답글 → 해소 규칙 1 → 같은 lane, blocked→running,
#          reentry_count+1, 턴 프롬프트가 그 lane 의 runtime_session_ref 로 resume 시도
#   그리고 **웹에서 질문 카드가 보이는가**(S7 피드, COMPONENTS §2.2 K3).
#
# 산출물: out/blocked.json · out/b-checks.tsv · web/__screenshots__/p2-b-*.png
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-b.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-b.json"; WORK="$OUT/work-b"; DLOG="$OUT/daemon-b.log"
TAP="$OUT/claim-tap-b.jsonl"; TAP_PORT="${TAP_PORT:-8093}"
MODEL="${LEAD_MODEL}"
g5_chk_init "$OUT/b-checks.tsv"
export AGENT_BROWSER_SESSION="colab-g5-b-${STAMP}"

cleanup() {
  agent-browser close >/dev/null 2>&1 || true
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$OUT/daemon-b.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-b.pid")" 2>/dev/null || true; }
  return 0
}
trap cleanup EXIT

step "0. claim 탭 (턴 프롬프트·resume 를 보기 위해) · 계정 · 페어링"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
python3 "$P2_DIR/fixtures/claimtap.py" "$TAP_PORT" "$SERVER_URL" "$TAP" & TAP_PID=$!
for i in $(seq 1 20); do curl -fsS -o /dev/null "http://localhost:$TAP_PORT/healthz" 2>/dev/null && break; sleep 0.3; done
EMAIL="g5b+$STAMP@example.com"; PASSWORD="password123"
signup "$EMAIL" "$PASSWORD" "Director" >/dev/null
WS="$(create_workspace "G5 Blocked $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "http://localhost:$TAP_PORT" --workdir-root "$WORK" --no-turn 2>&1 | tail -1 >&2
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-b.pid"
wait_pairing "$WS" "$PID_" 120 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "ws=$WS runtime=$RUNTIME"

step "1. 에이전트 2개 · 세션 (종료 조건 manual — 이 스크립트의 판정은 blocked 왕복이다)"
source "$P2_DIR/fixtures/blocked_agents.sh"
LEAD="$(create_agent_p2 "$WS" Lead       lead       "$MODEL" "$B_LEAD_INS" '팀을 이끌고 위임·종합한다')"
RSCH="$(create_agent_p2 "$WS" Researcher researcher "$MODEL" "$B_RES_INS"  '주어진 항목을 조사해 요약한다')"
SESSION="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "제품 X 시장 조사 (blocked)" --arg g "$SCENARIO_GOAL" \
  --arg a "$LEAD" --arg rt "$RUNTIME" --arg r "$RSCH" \
  '{title:$t,goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a},{agent_id:$r}],assignee_agent_id:$a,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
echo "$WS $SESSION $LEAD $RSCH $RUNTIME" > "$OUT/b-ids.txt"
ok "session $SESSION"
T_START="$(now_ms)"

step "2. E3-05 — 자식이 blocked 로 질문할 때까지"
BL_LANE=""; DEADLINE=$(( $(date +%s) + ${BLOCKED_TIMEOUT_S:-600} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  BL_LANE="$(psqlq "select id from lane where session_id='$SESSION' and status='blocked' limit 1")"
  [ -n "$BL_LANE" ] && break
  sleep 3
done
[ -n "$BL_LANE" ] || { lanes_of "$SESSION" | column -t -s $'\t' >&2; die "blocked lane 이 생기지 않았다 (E3-05 전제 실패)"; }
T_BLOCKED="$(now_ms)"
echo "── lane 보드 (blocked 시점) ──" >&2; lanes_of "$SESSION" | column -t -s $'\t' >&2

chk E305a "자식 lane 이 blocked 다"                       blocked "$(psqlq "select status from lane where id='$BL_LANE'")"
CARD="$(psqlq "select coalesce(blocked_message_id::text,'-') from lane where id='$BL_LANE'")"
chk E305b "lane.blocked_message_id 가 설정됐다"           yes "$( [ "$CARD" != '-' ] && echo yes || echo no )"
chk E305c "그 메시지의 kind 가 blocked_q(질문 카드)다"    blocked_q "$(psqlq "select kind from message where id='$CARD'")"
chk E305d "질문 카드는 자식 에이전트가 쓴 것이다"          Researcher "$(psqlq "select a.name from message m join agent a on a.id=m.author_id where m.id='$CARD'")"
chk E305e "질문 카드의 source_task_id 가 그 lane 의 task 다" 1 \
  "$(psqlq "select count(*) from message m join task t on t.id=m.source_task_id where m.id='$CARD' and t.lane_id='$BL_LANE'")"
chk E305f "lane.blocked_note 가 질문 본문을 캐시한다"      1 \
  "$(psqlq "select count(*) from lane l join message m on m.id=l.blocked_message_id where l.id='$BL_LANE' and l.blocked_note = m.content")"
BL_WD="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id='$BL_LANE'")"
chk E305g "blocked 후에도 workdir 가 남아 있다 (프로세스만 끝난다)" yes \
  "$( [ -n "$BL_WD" ] && [ -d "$BL_WD" ] && echo yes || echo no )"
SIB_TERMINAL="$(psqlq "select count(*) from lane l join agent a on a.id=l.agent_id
                       where l.session_id='$SESSION' and a.name='Researcher' and l.id<>'$BL_LANE' and l.status in ('done','failed')")"
LEAD_WAKE="$(psqlq "select count(*) from task t join message m on m.id=t.trigger_message_id
                    where t.session_id='$SESSION' and t.agent_id='$LEAD' and m.content like '위임한 작업에서 질문이 왔습니다%'")"
chk E305h "위임자를 즉시 깨우는 시스템 메시지 task 1개"   1 "$LEAD_WAKE"
log "참고: 기상 시점 종료된 형제 lane 수 = $SIB_TERMINAL (E3-05 는 이 값과 무관하게 성립해야 한다)"
chk E305j "그 시스템 메시지에 '합류가 아니다' 문구가 있다" 1 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and author_type='system' and content like '%합류가 아닙니다%'")"
psqlq "select content from message where session_id='$SESSION' and author_type='system' and content like '위임한 작업에서 질문이 왔습니다%' limit 1" > "$OUT/b-wake-msg.txt"
BL_NOTE="$(psqlq "select blocked_note from lane where id='$BL_LANE'")"
chk E305k "기상 시스템 메시지가 질문 카드를 인용한다 (카드 id 또는 질문 본문)" yes \
  "$(python3 "$P2_DIR/fixtures/quotes_card.py" "$OUT/b-wake-msg.txt" "$CARD" "$BL_NOTE")"

step "2b. 웹 — S7 피드에 질문 카드가 보이는가 (COMPONENTS §2.2 K3)"
WEB_OK=skip
if command -v agent-browser >/dev/null 2>&1; then
  mkdir -p "$E2E_ROOT/web/__screenshots__"
  agent-browser set viewport 1440 1000 >/dev/null 2>&1 || true
  agent-browser open "$WEB_URL/login" >/dev/null 2>&1 || true
  agent-browser wait '[data-testid="login-form"]' --timeout 25000 >/dev/null 2>&1 || true
  agent-browser fill 'input[name=email]' "$EMAIL" >/dev/null 2>&1 || true
  agent-browser fill 'input[name=password]' "$PASSWORD" >/dev/null 2>&1 || true
  agent-browser click 'button[type=submit]' >/dev/null 2>&1 || true
  agent-browser wait --url "**/sessions" --timeout 20000 >/dev/null 2>&1 || true
  agent-browser open "$WEB_URL/sessions/$SESSION" >/dev/null 2>&1 || true
  agent-browser wait '[data-testid="message-card"]' --timeout 30000 >/dev/null 2>&1 || true
  agent-browser wait --fn 'document.querySelectorAll("[data-testid=message-card][data-kind=blocked_q]").length>0' --timeout 30000 >/dev/null 2>&1 || true
  QCARDS="$(agent-browser get count '[data-testid="message-card"][data-kind="blocked_q"]' 2>/dev/null || echo 0)"
  agent-browser screenshot "$E2E_ROOT/web/__screenshots__/p2-b-01-question-card.png" >/dev/null 2>&1 || true
  agent-browser get text '[data-testid="message-card"][data-kind="blocked_q"]' > "$OUT/b-web-card.txt" 2>/dev/null || : > "$OUT/b-web-card.txt"
  chk W1  "S7 피드에 blocked_q 질문 카드가 렌더된다"       1 "${QCARDS:-0}"
  chk_has W1b "질문 카드에 질문 본문이 보인다"            "$OUT/b-web-card.txt" "범위가 불명확"
  chk_has W1c "질문 카드에 '질문 →' 배지가 붙는다"        "$OUT/b-web-card.txt" "질문 →"
  BLANE_CARD="$(agent-browser get count '[data-testid="lane-card"][data-status="blocked"]' 2>/dev/null || echo 0)"
  chk W1d "lane 보드에 blocked 카드가 있다"               1 "${BLANE_CARD:-0}"
  WEB_OK=ran
else
  log "agent-browser 없음 — 웹 확인 건너뜀"
fi

step "3. E3-06 — 형제가 끝나면 합류가 발화하고 질문이 다시 실린다"
DEADLINE=$(( $(date +%s) + 600 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(join_fired "$SESSION" | wc -l | tr -d ' ')" -ge 1 ] && break
  sleep 3
done
echo "── 합류 발화 ──" >&2; join_fired "$SESSION" | column -t -s $'\t' >&2
psqlq "select replace(content,E'\n','⏎') from message where session_id='$SESSION' and author_type='system' and content like '위임한 작업이 모두 끝났습니다%' limit 1" > "$OUT/b-join-msg.txt"
chk E306a "합류가 발화했다 (blocked 는 종료 취급, FR-6.2)"  1 "$(join_fired "$SESSION" | wc -l | tr -d ' ')"
chk E306b "합류 메시지가 정확히 1개 (FR-6.5)"              1 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and author_type='system' and content like '위임한 작업이 모두 끝났습니다%'")"
chk_has E306c "합류 페이로드에 자식 질문이 재포함된다"      "$OUT/b-join-msg.txt" "질문:"
chk_has E306d "합류 프롬프트에 '답을 기다리는 자식 1개'"    "$OUT/b-join-msg.txt" "답을 기다리는 자식 1개"
chk_has E306e "합류 목록에 blocked 자식이 blocked 로 적힌다" "$OUT/b-join-msg.txt" "Researcher: blocked"

step "4. E3-07 — 위임자가 질문 카드에 답글 → 같은 lane 재진입"
RE_BEFORE="$(psqlq "select reentry_count from lane where id='$BL_LANE'")"
ANSWER_PATH="agent"
DEADLINE=$(( $(date +%s) + ${ANSWER_TIMEOUT_S:-420} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(psqlq "select count(*) from message where parent_id='$CARD'")" -ge 1 ] && break
  sleep 4
done
if [ "$(psqlq "select count(*) from message where parent_id='$CARD'")" = 0 ]; then
  # 우회: 에이전트가 카드 id 를 못 찾은 경우 사람(Director)이 같은 스레드에 답글을 단다.
  # 해소 규칙 1 은 작성자가 사람이든 에이전트든 같다 — E3-07 의 **경로**를 재는 것이 목적이다.
  log "에이전트 답글 없음 → Director 답글로 우회 (보고서에 우회로 표시)"
  ANSWER_PATH="director"
  api_ok POST "/sessions/$SESSION/messages" \
    "$(jq -nc --arg c "경쟁 제품은 국내에서 판매 중인 3개로 한정한다." --arg p "$CARD" '{content:$c,parent_id:$p}')" \
    -H "Idempotency-Key: $(uuid)" >/dev/null
fi
# 관찰(2026-09-06 1차 실행): 위임자가 **멘션 없이** 스레드 답글만 달면 규칙 4로 아무도 트리거되지 않아
# 자식 lane 이 blocked 그대로 남았다. 사람 답글은 규칙 5로 살아나고, 에이전트 답글은 자식 멘션이 있어야
# 살아난다 — 기상 프롬프트가 그 말을 하지 않는다(G5_REPORT S-28). 지시문을 그에 맞춰 고쳐 정식 경로로 잰다.
chk E307a "질문 카드 스레드에 답글이 달렸다"               yes \
  "$( [ "$(psqlq "select count(*) from message where parent_id='$CARD'")" -ge 1 ] && echo yes || echo no )"
log "답글 경로: $ANSWER_PATH (agent = 정식 · director = 우회)"

DEADLINE=$(( $(date +%s) + 420 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(psqlq "select status from lane where id='$BL_LANE'")" != blocked ] && break
  sleep 3
done
RE_AFTER="$(psqlq "select reentry_count from lane where id='$BL_LANE'")"
chk E307b "답글이 같은 lane 으로 해소됐다 (해소 규칙 1 — 새 lane 없음)" 3 \
  "$(psqlq "select count(*) from lane l join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher'")"
chk E307c "reentry_count 가 1 올랐다"                     "$((RE_BEFORE+1))" "$RE_AFTER"
chk E307d "lane 이 blocked 를 벗어났다 (blocked → running)" yes \
  "$( [ "$(psqlq "select status from lane where id='$BL_LANE'")" != blocked ] && echo yes || echo no )"
chk E307e "그 lane 에 task 가 하나 더 생겼다 (재진입 턴)"   2 "$(psqlq "select count(*) from task where lane_id='$BL_LANE'")"
RE_TASK="$(psqlq "select id from task where lane_id='$BL_LANE' order by created_at desc limit 1")"
python3 "$P2_DIR/fixtures/prompt_of_task.py" "$TAP" "$RE_TASK" > "$OUT/b-reentry-prompt.txt" 2>/dev/null || true
python3 "$P2_DIR/fixtures/resume_of_task.py" "$TAP" "$RE_TASK" > "$OUT/b-resume.json" 2>/dev/null || echo null > "$OUT/b-resume.json"
RESUME_JSON="$(cat "$OUT/b-resume.json")"
chk E307f "재진입 TaskBundle 이 resume(runtime_session_ref)을 싣는다 (§6)" yes \
  "$( [ -n "$RESUME_JSON" ] && [ "$RESUME_JSON" != null ] && echo yes || echo no )"
chk E307g "resume 의 runtime_kind 가 lane 것과 같다"       claude_code \
  "$(jq -r '.runtime_kind // "none"' <<<"$RESUME_JSON" 2>/dev/null || echo none)"
chk E307h "재진입 프롬프트가 답글을 트리거로 싣는다"       yes \
  "$(grep -q '국내에서 판매 중인 3개' "$OUT/b-reentry-prompt.txt" 2>/dev/null && echo yes || echo no)"
# 재개가 실제로 일어났는가는 task_event `runtime.resume` 이 말한다(§6: resumed | cold_start).
# task_attempt.resumed 는 finish 가 와야 채워지므로, 턴이 도는 중에는 활동 피드가 유일한 증거다.
RES_OUT="$(psqlq "select coalesce(te.outcome,'-') from task_event te where te.task_id='$RE_TASK' and te.class='runtime' and te.verb='resume' order by te.seq desc limit 1")"
[ -n "$RES_OUT" ] || RES_OUT='-'
chk E307i "재진입 턴이 같은 런타임 세션을 이어받았다 (§6 resume)" resumed "$RES_OUT"
log "runtime.resume outcome = $RES_OUT (resumed = 세션 유지 · cold_start = 유실 감지 후 재시작; 둘 다 §6 상 정상)"

T_END="$(now_ms)"
step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg lane "$BL_LANE" --arg card "$CARD" \
  --arg answer_path "$ANSWER_PATH" --arg web "$WEB_OK" --arg resume_outcome "${RES_OUT:--}" \
  --argjson reentry_before "${RE_BEFORE:-0}" --argjson reentry_after "${RE_AFTER:-0}" \
  --argjson blocked_after_s "$(( (T_BLOCKED-T_START)/1000 ))" --argjson elapsed_s "$(( (T_END-T_START)/1000 ))" \
  --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,session:$session,blocked_lane:$lane,question_card:$card,answer_path:$answer_path,
    web:$web,reentry_count:{before:$reentry_before,after:$reentry_after},resume_outcome:$resume_outcome,
    blocked_after_s:$blocked_after_s,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/blocked.json"
[ "$fail" = 0 ]
