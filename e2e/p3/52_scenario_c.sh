#!/usr/bin/env bash
# e2e/p3/52_scenario_c.sh — T-I3 (e): **시나리오 C — Director 개입** (EVAL E16-C, E8-06, E10-01·04).
#
#   C1  R 이 `running` 인 동안 Director 가 `@R …` 메시지를 보낸다
#       → 진행 중 턴은 **계속 돈다**(프로세스 kill 0, 취소 명령 0), 새 지시는 같은 lane 의 **`queued` task**
#       → 턴이 끝나면 그 task 가 이어서 dispatch 된다
#   C2  "중단하고 다시 지시"(`restartLane`)
#       → 진행 중 턴 취소 + **새 task**(`attempt` 1, `restarted_from_task_id` = 이전 task), lane `running` 유지,
#         프롬프트에 `<resumed>` **없음** · 새 지시만(E8-06)
#   C3  "중단"(`cancelLane`) → lane `failed(cancelled)`, 활동 피드 "사람이 중단함", 새 task 0
#   C4  **결정 기록이 콜드 스타트를 넘어 살아남는가**(브리프 [7]) — 결정을 남기고 런타임 transcript 를
#       지운 뒤 재개시켜, 콜드 스타트한 attempt 의 브리프 [7] 에 그 결정이 실려 있는지 본다
#
# 과제는 저장소 밖의 무해한 주제다(X-2). 수치는 서버 DB 단일 클럭.
# 산출물: out/52-checks.tsv · out/52.json · out/52-prompt-*.txt · out/52-brief-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-52.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-52.json"; WORK="$OUT/work-52"; DLOG="$OUT/daemon-52.log"
TAP="$OUT/tap-52.jsonl"; TAP_PORT="${TAP_PORT_52:-8104}"
MODEL="${LEAD_MODEL}"
EMAIL="g6e+$STAMP@example.com"; PASSWORD="password123"
g5_chk_init "$OUT/52-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$OUT/daemon-52.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-52.pid")" 2>/dev/null || true; }
  return 0
}
trap cleanup EXIT

# 여러 단계를 도는 긴 턴 — "턴 중 개입" 을 재려면 개입할 시간이 있어야 한다.
RES_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 시장을 조사하는 조사자다. 답은 한국어로 짧게.
웹 접근이 없다 — 일반 지식으로 답한다. 가상의 제품이므로 그럴듯한 근사치면 충분하고 "할 수 없다" 고 말하지 마라.

지시를 받으면 현재 작업 디렉토리에서 아래를 **순서대로**, 서두르지 말고 한 단계씩 수행한다.
- 단계 1: note-01.md 에 "시장 규모" 를 여덟 줄로 쓴다. 끝나면 colab_message_post 로 `PART-1 done` 게시.
- 단계 2: note-02.md 에 "경쟁 제품 다섯" 을 여덟 줄로 쓴다. 끝나면 `PART-2 done` 게시.
- 단계 3: note-03.md 에 "가격대와 구매 채널" 을 여덟 줄로 쓴다. 끝나면 `PART-3 done` 게시.
- 단계 4: note-04.md 에 "구매 결정 요인" 을 여덟 줄로 쓴다. 끝나면 `PART-4 done` 게시.
- 단계 5: note-05.md 에 "위험 요인" 을 여덟 줄로 쓴다. 끝나면 `PART-5 done` 게시.
다섯 단계가 끝나면 `ALL-DONE` 을 게시하고 colab_status_set 으로 status "done" 을 부른 뒤 턴을 끝낸다.

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라. 파일 쓰기 말고는 colab_* 도구만 쓴다.'
GOAL='가상의 실내 화분 자동 급수기 제품 Y 의 시장 조사 메모를 다섯 조각으로 나눠 만든다'

# wait_running TASK [TIMEOUT]
wait_running() {
  local dl=$(( $(date +%s) + ${2:-300} )) st
  while [ "$(date +%s)" -lt "$dl" ]; do
    st="$(task_field "$1" status)"; [ "$st" = running ] && { echo running; return 0; }
    case "$st" in completed|failed|cancelled|paused) echo "$st"; return 1;; esac
    sleep 2
  done
  echo timeout; return 1
}
# wait_tool_events TASK ATTEMPT N [TIMEOUT] — 그 attempt 가 툴을 N개 이상 낼 때까지 (턴이 "한창" 인 시점)
wait_tool_events() {
  local dl=$(( $(date +%s) + ${4:-240} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select count(*) from task_event where task_id='$1' and attempt=$2 and class='tool'")" -ge "$3" ] && return 0
    sleep 3
  done
  return 1
}

step "0. claim 탭"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT (pid $TAP_PID)"

step "1. 계정 · 페어링 (capacity=4 — 세 세션이 나란히 돈다)"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G6 Scenario C $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_cap "$PTOK" "$CFG" "$WORK" 4
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$OUT/daemon-52.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
R1="$(create_agent_p2 "$WS" Rsearch1 researcher "$MODEL" "$RES_INS" '시장 조사 메모를 쓴다')"
R2="$(create_agent_p2 "$WS" Rsearch2 researcher "$MODEL" "$RES_INS" '시장 조사 메모를 쓴다')"
R3="$(create_agent_p2 "$WS" Rsearch3 researcher "$MODEL" "$RES_INS" '시장 조사 메모를 쓴다')"
S1="$(create_session_p3 "$WS" "제품 Y 조사 (개입-메시지)" "$GOAL" "$R1" "$RUNTIME" '{}' "$R1")"
S2="$(create_session_p3 "$WS" "제품 Y 조사 (중단하고 다시 지시)" "$GOAL" "$R2" "$RUNTIME" '{}' "$R2")"
S3="$(create_session_p3 "$WS" "제품 Y 조사 (중단)" "$GOAL" "$R3" "$RUNTIME" '{}' "$R3")"
T1="$(session_initial_task "$S1")"; T2="$(session_initial_task "$S2")"; T3="$(session_initial_task "$S3")"
ok "S1=$S1/$T1 · S2=$S2/$T2 · S3=$S3/$T3"
T0="$(now_ms)"

step "2. 세 자극을 **턴이 살아 있는 동안** 한 자리에서 낸다"
# 개입은 "진행 중 턴" 에만 의미가 있다. C1 의 판정(첫 턴이 스스로 끝난다)을 먼저 기다리면 그 사이
# S2·S3 의 턴이 끝나 restart/cancel 이 409 가 된다(1차 실행 실측). 그래서 세 자극을 여기서 다 낸다.
for t in "$T1" "$T2" "$T3"; do wait_running "$t" 300 >/dev/null || bad "task $t 가 running 으로 가지 않았다"; done
for t in "$T1" "$T2" "$T3"; do wait_tool_events "$t" 1 3 || bad "task $t 의 턴이 시작되지 않았다"; done
LANE1="$(task_field "$T1" lane_id)"; LANE2="$(task_field "$T2" lane_id)"; LANE3="$(task_field "$T3" lane_id)"
PG_BEFORE="$(pgid_of_attempt "$WORK" "$T1" 1)"
PROC_BEFORE="$(procs_of_attempt "$WORK" "$T1" 1)"
EV_BEFORE="$(psqlq "select count(*) from task_event where task_id='$T1' and attempt=1")"
chk_ge E0 "개입 전: attempt 1 의 프로세스가 살아 있다" 1 "$PROC_BEFORE"

# C1 — 그냥 메시지
post_message "$S1" "$(mention Rsearch1 "$R1") 한국 시장으로 좁혀줘" >/dev/null
# C2 — 중단하고 다시 지시
NEW_INSTRUCTION="이전 지시는 취소한다. 대신 note-99.md 에 제품 Y 의 보증 정책 한 가지만 세 줄로 쓰고 끝내라"
RS="$(api POST "/lanes/$LANE2/restart" "$(jq -nc --arg c "$NEW_INSTRUCTION" '{content:$c}')" -H "Idempotency-Key: $(uuid)")"
RS_CODE="$(api_code <<<"$RS")"; RS_BODY="$(api_body <<<"$RS")"
printf '%s\n' "$RS_BODY" > "$OUT/52-restart.json"
# C3 — 중단
T_CANCEL="$(now_ms)"
CC="$(api POST "/lanes/$LANE3/cancel" '' | api_code)"
ok "자극 3건: 메시지 · restart(HTTP $RS_CODE) · cancel(HTTP $CC)"

step "3. C1 — 진행 중 턴은 계속 돌고 새 지시는 queued 가 된다 (E16-C 1행)"
sleep 12
chk E1  "**진행 중 턴이 계속 돈다** (task 여전히 running)" running "$(task_field "$T1" status)"
chk E1b "프로세스 kill 0 — 같은 pgid 가 그대로 살아 있다"  yes \
  "$( [ "$(pgid_of_attempt "$WORK" "$T1" 1)" = "$PG_BEFORE" ] && [ "$(procs_of_attempt "$WORK" "$T1" 1)" -ge 1 ] && echo yes || echo no )"
chk E1c "취소 명령 0건 (메시지는 취소가 아니다)"           0 \
  "$(psqlq "select count(*) from daemon_command where task_id='$T1' and type='cancel'")"
chk_ge E1d "턴이 계속 이벤트를 낸다"                        "$((EV_BEFORE+1))" \
  "$(psqlq "select count(*) from task_event where task_id='$T1' and attempt=1")"
T1B=""
for _ in $(seq 1 30); do
  T1B="$(psqlq "select id from task where session_id='$S1' and id<>'$T1' order by created_at desc limit 1")"
  [ -n "$T1B" ] && break; sleep 2
done
chk E2  "새 지시가 **새 task** 로 큐에 들어간다"            yes "$( [ -n "$T1B" ] && echo yes || echo no )"
if [ -n "$T1B" ]; then
chk E2b "그 task 는 같은 lane 이다 L(R,1)"                  "$LANE1" "$(task_field "$T1B" lane_id)"
chk E2c "그 task 는 아직 실행되지 않는다 (queued/deferred)"  yes \
  "$(in_set "$(task_field "$T1B" status)" queued deferred)"
chk E2d "재지시가 아니다 (restarted_from_task_id 없음)"     - "$(task_field "$T1B" restarted_from_task_id)"
fi

step "4. C2 — \"중단하고 다시 지시\" (restartLane): 새 task · <resumed> 없음 (E8-06)"
chk F1  "restartLane 이 202 다"                             202 "$RS_CODE"
T2B="$(jq -r '.task.id // empty' <<<"$RS_BODY")"
chk F1b "응답이 **새 task** 를 준다"                        yes "$( [ -n "$T2B" ] && echo yes || echo no )"
chk F1c "cancelled_task_id 가 이전 task 다"                 "$T2" "$(jq -r '.cancelled_task_id // "-"' <<<"$RS_BODY")"
DEADLINE=$(( $(date +%s) + 180 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  case "$(task_field "$T2" status)" in cancelled|failed) break;; esac; sleep 3
done
chk F2  "이전 task 가 cancelled 다"                         cancelled "$(task_field "$T2" status)"
# 계약이 말하는 "lane 은 running 유지" 는 **재지시 시점**의 상태다 — 새 task 가 끝나면 lane 은 done 이
# 된다. 그래서 나중에 조회하지 않고 restart **응답 본문의 lane** 을 본다(1차 실행에서 done 을 잡았다).
chk F2b "재지시 시점에 lane 은 **running 유지** (자리를 잃지 않는다, E2-15)" running \
  "$(jq -r '.lane.status // "-"' <<<"$RS_BODY")"
if [ -n "$T2B" ]; then
chk F3  "새 task 의 attempt 는 1 이다"                      1 "$(task_field "$T2B" attempt)"
chk F3b "**restarted_from_task_id = 이전 task**"            "$T2" "$(task_field "$T2B" restarted_from_task_id)"
chk F3c "같은 lane 이다"                                    "$LANE2" "$(task_field "$T2B" lane_id)"
F2ST="$(WAIT_S=${TURN_WAIT_S:-900} wait_task "$T2B" completed failed cancelled)"
chk F4  "새 지시가 실행됐다"                                completed "$F2ST"
tap_prompt "$TAP" "$T2B" 1 > "$OUT/52-prompt-c2-restart.txt"
chk_has F5  "프롬프트에 새 지시가 있다"                     "$OUT/52-prompt-c2-restart.txt" "보증 정책"
chk F5b "프롬프트에 **<resumed> 가 없다** (E8-06)"          0 \
  "$(grep -c "<resumed" "$OUT/52-prompt-c2-restart.txt" || true)"
chk F5c "프롬프트에 \"이미 게시한 메시지\" 목록도 없다"     0 \
  "$(grep -c "Messages you already posted" "$OUT/52-prompt-c2-restart.txt" || true)"
fi

step "5. C3 — \"중단\" (cancelLane): lane failed(cancelled) · 피드 \"사람이 중단함\""
chk G1 "cancelLane 이 202 다" 202 "$CC"
DEADLINE=$(( $(date +%s) + 180 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do [ "$(lane_field "$LANE3" status)" = failed ] && break; sleep 3; done
chk G2  "lane 이 **failed** 다"                             failed "$(lane_field "$LANE3" status)"
chk G2b "task 가 cancelled · failure_kind=cancelled"        "cancelled|cancelled" \
  "$(psqlq "select status::text||'|'||coalesce(failure_kind::text,'-') from task where id='$T3'")"
# 활동 피드 = `task_event`(class=status · verb=cancel · payload.note), 세션 `message` 가 아니다.
psqlq "select class||'/'||coalesce(verb,'-')||' '||replace(coalesce(payload::text,''),E'\n','⏎')
       from task_event where task_id='$T3' order by seq" > "$OUT/52-cancel-feed.txt"
chk_has G3 "활동 피드에 \"사람이 중단함\" (E10-04)"         "$OUT/52-cancel-feed.txt" "사람이 중단함"
chk G4  "취소는 새 task 를 만들지 않는다 (E10-04)"          1 "$(task_count "$S3")"
chk G5  "프로세스 트리 잔존 0 (E10-03)"                     0 "$(procs_of_attempt "$WORK" "$T3" 1)"
CANCEL_S=$(( ($(now_ms)-T_CANCEL)/1000 ))
psqlq "select coalesce(ts,created_at), class::text, coalesce(verb,'-') from task_event where task_id='$T3' and attempt=1 order by seq desc limit 8" > "$OUT/52-cancel-events.tsv"
log "중단 → lane failed 까지 ${CANCEL_S}s (마지막 이벤트: out/52-cancel-events.tsv)"

step "6. C1 이어서 — 첫 턴은 스스로 끝나고, 그 뒤 새 지시가 실행된다"
FIRST_END="$(WAIT_S=${TURN_WAIT_S:-900} wait_task "$T1" completed failed cancelled)"
chk E3  "첫 턴이 **스스로** 끝났다 (취소가 아니다)"        completed "$FIRST_END"
if [ -n "$T1B" ]; then
  SECOND="$(WAIT_S=${TURN_WAIT_S:-900} wait_task "$T1B" completed failed cancelled)"
  chk E3b "그 뒤 새 지시가 이어서 실행된다"                 completed "$SECOND"
  tap_prompt "$TAP" "$T1B" 1 > "$OUT/52-prompt-c1-followup.txt"
  chk_has E3c "그 프롬프트의 trigger 가 새 지시다"          "$OUT/52-prompt-c1-followup.txt" "한국 시장으로 좁혀줘"
fi

step "7. C4 — 결정 기록이 **콜드 스타트를 넘어** 살아남는가 (브리프 [7])"
# `recordDecision` 은 openapi 에서 **TaskToken 전용**이다(사람의 결정은 HITL 응답이 남긴다) — 그래서
# 결정은 에이전트에게 시켜 남긴다. 그 뒤 런타임 transcript 를 지워 다음 턴을 콜드 스타트로 만들고,
# 그 attempt 의 **브리프 [7]** 에 결정이 실려 있는지 본다(브리프는 claim 탭의 `brief.text` 로만 보인다).
DEC_SUMMARY="조사 범위를 한국 시장으로 좁힌다"
post_message "$S1" "$(mention Rsearch1 "$R1") colab_decision_record 를 한 번 불러 summary 를 정확히 \"$DEC_SUMMARY\" 로, rationale 을 \"Director 가 턴 중에 그렇게 지시했다\" 로 기록하라. 다른 일은 하지 마라." >/dev/null
T1D=""
for _ in $(seq 1 40); do
  T1D="$(psqlq "select id from task where session_id='$S1' order by created_at desc limit 1")"
  [ -n "$T1D" ] && [ "$T1D" != "$T1B" ] && [ "$T1D" != "$T1" ] && break; sleep 2
done
chk H0  "결정 기록 지시가 새 task 를 만들었다" yes "$( [ -n "$T1D" ] && echo yes || echo no )"
WAIT_S=${TURN_WAIT_S:-900} wait_task "$T1D" completed failed cancelled >/dev/null
chk H1  "에이전트가 결정을 남겼다 (FR-4.2)" 1 \
  "$(psqlq "select count(*) from decision where session_id='$S1' and summary='$DEC_SUMMARY'")"
chk H1b "그 결정의 source 가 agent 다"       agent \
  "$(psqlq "select coalesce(source::text,'-') from decision where session_id='$S1' order by created_at desc limit 1")"

SID1="$(psqlq "select runtime_session_ref->>'session_id' from lane where id='$LANE1'")"
WD1="$WORK/sessions/$S1/$LANE1"
ENC1="$(printf '%s' "$WD1" | tr '/._' '---')"
TR1="$HOME/.claude/projects/$ENC1/$SID1.jsonl"
if [ -f "$TR1" ]; then rm -f "$TR1"; ok "transcript 제거 → 다음 턴은 콜드 스타트 ($SID1)"; else bad "transcript 없음: $TR1"; fi
chk H2 "런타임 세션 기록을 지웠다 (강제 콜드 스타트)" no "$( [ -f "$TR1" ] && echo yes || echo no )"

post_message "$S1" "$(mention Rsearch1 "$R1") note-06.md 에 요약 세 줄을 덧붙여라" >/dev/null
T1C=""
for _ in $(seq 1 40); do
  T1C="$(psqlq "select id from task where session_id='$S1' order by created_at desc limit 1")"
  [ -n "$T1C" ] && [ "$T1C" != "$T1D" ] && [ "$T1C" != "$T1B" ] && [ "$T1C" != "$T1" ] && break; sleep 2
done
chk H3 "새 task 가 생겼다" yes "$( [ -n "$T1C" ] && echo yes || echo no )"
if [ -n "$T1C" ]; then
  H3ST="$(WAIT_S=${TURN_WAIT_S:-900} wait_task "$T1C" completed failed cancelled)"
  chk H3b "그 턴이 끝났다" completed "$H3ST"
  tap_brief "$TAP" "$T1C" 1 --last > "$OUT/52-brief-c4.txt"
  chk_has H4  "브리프에 **[7] Decision Log** 구간이 있다"   "$OUT/52-brief-c4.txt" "[7] Decision Log"
  chk_has H4b "그 구간에 앞서 남긴 결정이 실려 있다"        "$OUT/52-brief-c4.txt" "$DEC_SUMMARY"
  RES1="$(psqlq "select coalesce(payload->>'outcome','-') from task_event where task_id='$T1C' and class='runtime' and verb='resume' order by seq limit 1")"
  log "C4 재개 판정: ${RES1:--} (transcript 를 지웠으므로 콜드 스타트여야 한다)"
  chk H5 "그 턴은 콜드 스타트다 (resumed 가 아니다)" no "$( [ "$RES1" = resumed ] && echo yes || echo no )"
  chk H6 "콜드 스타트인데도 턴이 일을 했다 (툴 이벤트 ≥ 1)" yes \
    "$( [ "$(psqlq "select count(*) from task_event where task_id='$T1C' and class='tool'")" -ge 1 ] && echo yes || echo no )"
fi

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg s1 "$S1" --arg s2 "$S2" --arg s3 "$S3" \
  --arg t1 "$T1" --arg t1b "${T1B:-}" --arg t1c "${T1C:-}" --arg t1d "${T1D:-}" --arg t2 "$T2" --arg t2b "${T2B:-}" --arg t3 "$T3" \
  --arg lane1 "$LANE1" --arg lane2 "$LANE2" --arg lane3 "$LANE3" \
  --argjson cancel_s "$CANCEL_S" --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" \
  --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,
    c1:{session:$s1,running_task:$t1,queued_task:$t1b,decision_task:$t1d,cold_start_task:$t1c,lane:$lane1},
    c2:{session:$s2,cancelled_task:$t2,new_task:$t2b,lane:$lane2},
    c3:{session:$s3,task:$t3,lane:$lane3,cancel_to_failed_s:$cancel_s},
    elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/52.json"
[ "$fail" = 0 ]
