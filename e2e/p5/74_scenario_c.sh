#!/usr/bin/env bash
# e2e/p5/74_scenario_c.sh — **시나리오 C — Director 개입** (PRD §4 C · EVAL E16-C, E8-06, E10-01·04).
#
# 비용 한 줄(I-3): 페이크 턴 ≈ 8(긴 턴 1 + 개입·재지시·중단·콜드 스타트) · $0 · ≈ 40s. 실기: haiku ≈ $0.05 · ≈ 6분
#
#   C1 running 중 `@R …` 메시지 → 진행 중 턴은 계속(kill 0·취소 0), 새 지시는 같은 lane 의 queued task → 이어서 실행
#   C2 "중단하고 다시 지시"(restartLane) → 취소 + 새 task(attempt 1, restarted_from), lane running 유지, <resumed> 없음
#   C3 "중단"(cancelLane) → lane failed(cancelled), 피드 "사람이 중단함", 새 task 0
#   C4 결정 기록이 **콜드 스타트를 넘어** 브리프 [7] 에 실리는가
#
# 판정은 `e2e/p3/52_scenario_c.sh`(G6) 그대로. 페이크 런타임의 긴 턴은 대본이 다섯 단계를 한 단계씩
# (단계마다 `PART-N done` 게시 + FAKE_STEP_SLEEP 초) 돌아 개입할 시간을 만든다. C4 의 콜드 스타트는
# 실기에서는 transcript 삭제로, 페이크에서는 `known_sessions: []`(session/load 거절 = resume_rejected)로 만든다.
# 산출물: out/74-checks.tsv · out/74.json · out/74-prompt-*.txt · out/74-brief-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-74.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-74.json"; WORK="$P5_TMP_ROOT/74/work"; DLOG="$OUT/daemon-74.log"
TAP="$OUT/tap-74.jsonl"; TAP_PORT="${TAP_PORT_74:-8121}"
MODEL="${LEAD_MODEL}"
g5_chk_init "$OUT/74-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-74.pid"; return 0; }
trap cleanup EXIT

RES_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 시장을 조사하는 조사자다. 답은 한국어로 짧게. 웹 접근이 없다 — 일반 지식으로 답한다.
첫 턴부터 곧바로 아래를 **순서대로**, 서두르지 말고 한 단계씩 수행한다.
- 단계 1~5: note-0N.md 에 여덟 줄을 쓰고 `PART-N done` 게시 (N=1..5, 주제: 시장 규모 / 경쟁 제품 / 가격·채널 / 구매 결정 요인 / 위험 요인).
다섯 단계가 끝나면 `ALL-DONE` 을 게시하고 colab_status_set 으로 status "done".
후속 지시가 오면 그 지시만 짧게 처리하고 status "done". "DECISION: <s> | RATIONALE: <r>" 형식의 지시는 colab_decision_record 로 summary/rationale 을 정확히 그대로 기록한다.
웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라. 파일 쓰기 말고는 colab_* 도구만 쓴다.'
GOAL='가상의 실내 화분 자동 급수기 제품 Y 의 시장 조사 메모를 다섯 조각으로 나눠 만든다'

# 페이크 대본: 단계 1~5 + fin 을 exec 스텝 여섯 개로. [KNOWN] 는 known_sessions.
c_turns() { # c_turns ROLE KNOWN_JSON
  jq -nc --arg role "$1" --arg fix "$FIX" --argjson known "$2" \
    '{known_sessions:$known, turns:[{steps:([1,2,3,4,5,"fin"]|map({exec:("bash "+$fix+"/agent.sh "+$role+" "+(.|tostring))}))}]}'
}
wait_running() { local dl=$(( $(date +%s) + ${2:-300} )) st
  while [ "$(date +%s)" -lt "$dl" ]; do st="$(task_field "$1" status)"; [ "$st" = running ] && { echo running; return 0; }
    case "$st" in completed|failed|cancelled|paused) echo "$st"; return 1;; esac; sleep 1; done; echo timeout; return 1; }
wait_tool_events() { local dl=$(( $(date +%s) + ${4:-240} ))
  while [ "$(date +%s)" -lt "$dl" ]; do [ "$(psqlq "select count(*) from task_event where task_id='$1' and attempt=$2 and class='tool'")" -ge "$3" ] && return 0; sleep 1; done; return 1; }

step "0. claim 탭"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 페어링 (capacity 4 — 세 세션이 나란히 돈다)"
signup "i5c+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G9 Scenario C $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-74.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME_ID="$(runtime_of_config "$CFG")"
R1="$(create_agent_fake "$WS" Rsearch1 researcher claude_code "$MODEL" "$RES_INS" '시장 조사 메모를 쓴다' "$(c_turns Rsearch1 '[]')")"
R2="$(create_agent_fake "$WS" Rsearch2 researcher claude_code "$MODEL" "$RES_INS" '시장 조사 메모를 쓴다' "$(c_turns Rsearch2 '["sess-1"]')")"
R3="$(create_agent_fake "$WS" Rsearch3 researcher claude_code "$MODEL" "$RES_INS" '시장 조사 메모를 쓴다' "$(c_turns Rsearch3 '["sess-1"]')")"
S1="$(create_session_p3 "$WS" "제품 Y 조사 (개입-메시지)" "$GOAL" "$R1" "$RUNTIME_ID" '{}' "$R1")"
S2="$(create_session_p3 "$WS" "제품 Y 조사 (중단하고 다시 지시)" "$GOAL" "$R2" "$RUNTIME_ID" '{}' "$R2")"
S3="$(create_session_p3 "$WS" "제품 Y 조사 (중단)" "$GOAL" "$R3" "$RUNTIME_ID" '{}' "$R3")"
T1="$(session_initial_task "$S1")"; T2="$(session_initial_task "$S2")"; T3="$(session_initial_task "$S3")"
ok "S1=$S1/$T1 · S2=$S2/$T2 · S3=$S3/$T3"
T0="$(now_ms)"

step "2. 세 자극을 **턴이 살아 있는 동안** 한 자리에서 낸다"
for t in "$T1" "$T2" "$T3"; do wait_running "$t" 300 >/dev/null || bad "task $t 가 running 으로 가지 않았다"; done
for t in "$T1" "$T2" "$T3"; do wait_tool_events "$t" 1 1 || bad "task $t 의 턴이 시작되지 않았다"; done
LANE1="$(task_field "$T1" lane_id)"; LANE2="$(task_field "$T2" lane_id)"; LANE3="$(task_field "$T3" lane_id)"
PG_BEFORE="$(pgid_of_attempt "$WORK" "$T1" 1)"
EV_BEFORE="$(psqlq "select count(*) from task_event where task_id='$T1' and attempt=1")"
chk_ge E0 "개입 전: attempt 1 의 프로세스가 살아 있다" 1 "$(procs_of_attempt "$WORK" "$T1" 1)"
post_message "$S1" "$(mention Rsearch1 "$R1") 한국 시장으로 좁혀줘" >/dev/null
NEW_INSTRUCTION="이전 지시는 취소한다. 대신 note-99.md 에 제품 Y 의 보증 정책 한 가지만 세 줄로 쓰고 끝내라"
RS="$(api POST "/lanes/$LANE2/restart" "$(jq -nc --arg c "$NEW_INSTRUCTION" '{content:$c}')" -H "Idempotency-Key: $(uuid)")"
RS_CODE="$(api_code <<<"$RS")"; RS_BODY="$(api_body <<<"$RS")"; printf '%s\n' "$RS_BODY" > "$OUT/74-restart.json"
T_CANCEL="$(now_ms)"
CC="$(api POST "/lanes/$LANE3/cancel" '' | api_code)"
ok "자극 3건: 메시지 · restart(HTTP $RS_CODE) · cancel(HTTP $CC)"

step "3. C1 — 진행 중 턴은 계속 돌고 새 지시는 queued 가 된다 (E16-C 1행)"
sleep 8
chk E1  "**진행 중 턴이 계속 돈다** (task 여전히 running)" running "$(task_field "$T1" status)"
chk E1b "프로세스 kill 0 — 같은 pgid 가 그대로 살아 있다" yes \
  "$( [ "$(pgid_of_attempt "$WORK" "$T1" 1)" = "$PG_BEFORE" ] && [ "$(procs_of_attempt "$WORK" "$T1" 1)" -ge 1 ] && echo yes || echo no )"
chk E1c "취소 명령 0건 (메시지는 취소가 아니다)" 0 "$(psqlq "select count(*) from daemon_command where task_id='$T1' and type='cancel'")"
chk_ge E1d "턴이 계속 이벤트를 낸다" "$((EV_BEFORE+1))" "$(psqlq "select count(*) from task_event where task_id='$T1' and attempt=1")"
T1B=""
for _ in $(seq 1 30); do T1B="$(psqlq "select id from task where session_id='$S1' and id<>'$T1' order by created_at desc limit 1")"; [ -n "$T1B" ] && break; sleep 1; done
chk E2  "새 지시가 **새 task** 로 큐에 들어간다" yes "$( [ -n "$T1B" ] && echo yes || echo no )"
if [ -n "$T1B" ]; then
chk E2b "그 task 는 같은 lane 이다 L(R,1)" "$LANE1" "$(task_field "$T1B" lane_id)"
chk E2c "그 task 는 아직 실행되지 않는다 (queued/deferred)" yes "$(in_set "$(task_field "$T1B" status)" queued deferred)"
chk E2d "재지시가 아니다 (restarted_from_task_id 없음)" - "$(task_field "$T1B" restarted_from_task_id)"
fi

step "4. C2 — \"중단하고 다시 지시\" (restartLane): 새 task · <resumed> 없음 (E8-06)"
chk F1  "restartLane 이 202 다" 202 "$RS_CODE"
T2B="$(jq -r '.task.id // empty' <<<"$RS_BODY")"
chk F1b "응답이 **새 task** 를 준다" yes "$( [ -n "$T2B" ] && echo yes || echo no )"
chk F1c "cancelled_task_id 가 이전 task 다" "$T2" "$(jq -r '.cancelled_task_id // "-"' <<<"$RS_BODY")"
wait_until 180 'case "$(task_field "'"$T2"'" status)" in cancelled|failed) true;; *) false;; esac' || true
chk F2  "이전 task 가 cancelled 다" cancelled "$(task_field "$T2" status)"
chk F2b "재지시 시점에 lane 은 **running 유지** (E2-15)" running "$(jq -r '.lane.status // "-"' <<<"$RS_BODY")"
if [ -n "$T2B" ]; then
chk F3  "새 task 의 attempt 는 1 이다" 1 "$(task_field "$T2B" attempt)"
chk F3b "**restarted_from_task_id = 이전 task**" "$T2" "$(task_field "$T2B" restarted_from_task_id)"
chk F3c "같은 lane 이다" "$LANE2" "$(task_field "$T2B" lane_id)"
chk F4  "새 지시가 실행됐다" completed "$(WAIT_S=$T_TURN wait_task "$T2B" completed failed cancelled)"
tap_prompt "$TAP" "$T2B" 1 > "$OUT/74-prompt-c2-restart.txt"
chk_has F5  "프롬프트에 새 지시가 있다" "$OUT/74-prompt-c2-restart.txt" "보증 정책"
chk F5b "프롬프트에 **<resumed> 가 없다** (E8-06)" 0 "$(cnt "$OUT/74-prompt-c2-restart.txt" '<resumed')"
chk F5c "프롬프트에 \"이미 게시한 메시지\" 목록도 없다" 0 "$(cnt "$OUT/74-prompt-c2-restart.txt" 'Messages you already posted')"
chk F6  "새 지시대로 일했다 (note-99.md)" yes "$( [ -f "$WORK/sessions/$S2/$LANE2/note-99.md" ] && echo yes || echo no )"
fi

step "5. C3 — \"중단\" (cancelLane): lane failed(cancelled) · 피드 \"사람이 중단함\""
chk G1 "cancelLane 이 202 다" 202 "$CC"
wait_until 180 '[ "$(lane_field "'"$LANE3"'" status)" = failed ]' || true
chk G2  "lane 이 **failed** 다" failed "$(lane_field "$LANE3" status)"
chk G2b "task 가 cancelled · failure_kind=cancelled" "cancelled|cancelled" "$(psqlq "select status::text||'|'||coalesce(failure_kind::text,'-') from task where id='$T3'")"
psqlq "select class||'/'||coalesce(verb,'-')||' '||replace(coalesce(payload::text,''),E'\n','⏎') from task_event where task_id='$T3' order by seq" > "$OUT/74-cancel-feed.txt"
chk_has G3 "활동 피드에 \"사람이 중단함\" (E10-04)" "$OUT/74-cancel-feed.txt" "사람이 중단함"
chk G4  "취소는 새 task 를 만들지 않는다 (E10-04)" 1 "$(task_count "$S3")"
sleep 2
chk G5  "프로세스 트리 잔존 0 (E10-03)" 0 "$(procs_of_attempt "$WORK" "$T3" 1)"
CANCEL_S=$(( ($(now_ms)-T_CANCEL)/1000 ))

step "6. C1 이어서 — 첫 턴은 스스로 끝나고, 그 뒤 새 지시가 실행된다"
chk E3 "첫 턴이 **스스로** 끝났다 (취소가 아니다)" completed "$(WAIT_S=$T_TURN wait_task "$T1" completed failed cancelled)"
chk E3d "첫 턴이 다섯 단계를 다 했다 (ALL-DONE 게시)" 1 "$(psqlq "select count(*) from message where session_id='$S1' and content='ALL-DONE'")"
if [ -n "$T1B" ]; then
  chk E3b "그 뒤 새 지시가 이어서 실행된다" completed "$(WAIT_S=$T_TURN wait_task "$T1B" completed failed cancelled)"
  tap_prompt "$TAP" "$T1B" 1 > "$OUT/74-prompt-c1-followup.txt"
  chk_has E3c "그 프롬프트의 trigger 가 새 지시다" "$OUT/74-prompt-c1-followup.txt" "한국 시장으로 좁혀줘"
fi

step "7. C4 — 결정 기록이 **콜드 스타트를 넘어** 살아남는가 (브리프 [7])"
DEC_SUMMARY="조사 범위를 한국 시장으로 좁힌다"
post_message "$S1" "$(mention Rsearch1 "$R1") DECISION: $DEC_SUMMARY | RATIONALE: Director 가 턴 중에 그렇게 지시했다" >/dev/null
T1D=""
for _ in $(seq 1 40); do T1D="$(latest_task "$S1")"; [ -n "$T1D" ] && [ "$T1D" != "$T1B" ] && [ "$T1D" != "$T1" ] && break; sleep 1; done
chk H0 "결정 기록 지시가 새 task 를 만들었다" yes "$( [ -n "$T1D" ] && echo yes || echo no )"
WAIT_S=$T_TURN wait_task "$T1D" completed failed cancelled >/dev/null
chk H1  "에이전트가 결정을 남겼다 (FR-4.2)" 1 "$(psqlq "select count(*) from decision where session_id='$S1' and summary='$DEC_SUMMARY'")"
chk H1b "그 결정의 source 가 agent 다" agent "$(psqlq "select coalesce(source::text,'-') from decision where session_id='$S1' order by created_at desc limit 1")"
if [ "$RUNTIME" = real ]; then
  SID1="$(psqlq "select runtime_session_ref->>'session_id' from lane where id='$LANE1'")"
  ENC1="$(printf '%s' "$WORK/sessions/$S1/$LANE1" | tr '/._' '---')"; TR1="$HOME/.claude/projects/$ENC1/$SID1.jsonl"
  [ -f "$TR1" ] && rm -f "$TR1" && ok "transcript 제거 → 다음 턴은 콜드 스타트"
  chk H2 "런타임 세션 기록을 지웠다 (강제 콜드 스타트)" no "$( [ -f "$TR1" ] && echo yes || echo no )"
else
  chk H2 "페이크는 session/load 를 거절한다 (known_sessions=[] → 콜드 스타트, resume_reason)" session_not_found \
    "$(psqlq "select coalesce(payload->>'resume_reason','-') from task_event where task_id='$T1D' and class='runtime' and verb='resume' order by seq limit 1")"
fi
post_message "$S1" "$(mention Rsearch1 "$R1") note-06.md 에 요약 세 줄을 덧붙여라" >/dev/null
T1C=""
for _ in $(seq 1 40); do T1C="$(latest_task "$S1")"; [ -n "$T1C" ] && [ "$T1C" != "$T1D" ] && [ "$T1C" != "$T1B" ] && [ "$T1C" != "$T1" ] && break; sleep 1; done
chk H3 "새 task 가 생겼다" yes "$( [ -n "$T1C" ] && echo yes || echo no )"
if [ -n "$T1C" ]; then
  chk H3b "그 턴이 끝났다" completed "$(WAIT_S=$T_TURN wait_task "$T1C" completed failed cancelled)"
  tap_brief "$TAP" "$T1C" 1 --last > "$OUT/74-brief-c4.txt"
  chk_has H4  "브리프에 **[7] Decision Log** 구간이 있다" "$OUT/74-brief-c4.txt" "[7] Decision Log"
  chk_has H4b "그 구간에 앞서 남긴 결정이 실려 있다" "$OUT/74-brief-c4.txt" "$DEC_SUMMARY"
  RES1="$(psqlq "select coalesce(resumed::text,'-') from task_attempt where task_id='$T1C' and attempt=1")"
  chk H5 "그 턴은 콜드 스타트다 (task_attempt.resumed ≠ true, 관측=$RES1)" no "$( [ "$RES1" = true ] && echo yes || echo no )"
  chk H6 "콜드 스타트인데도 턴이 일을 했다 (툴 이벤트 ≥ 1 · note-06.md)" yes \
    "$( [ "$(psqlq "select count(*) from task_event where task_id='$T1C' and class='tool'")" -ge 1 ] && [ -f "$WORK/sessions/$S1/$LANE1/note-06.md" ] && echo yes || echo no )"
fi

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg s1 "$S1" --arg s2 "$S2" --arg s3 "$S3" --arg mode "$RUNTIME" \
  --argjson cancel_s "$CANCEL_S" --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  '{runtime_mode:$mode,workspace:$ws,c1:$s1,c2:$s2,c3:$s3,cancel_to_failed_s:$cancel_s,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/74.json"
[ "$fail" = 0 ]
