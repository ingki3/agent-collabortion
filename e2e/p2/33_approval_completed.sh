#!/usr/bin/env bash
# e2e/p2/33_approval_completed.sh — G5 (a) 뒷단: 종료 조건 → 승인 → `completed` (E6-01 · E6-03).
#
#   E6-01  지정 에이전트(Writer)가 `colab artifact submit` → `artifact_submitted` 충족,
#          **플랫폼이** Director 에게 `approval` HITL 발행(`source: system`, `task_id` 비움),
#          세션은 `active` 유지
#   E6-03  Director 승인 → `active → completing → completed`, `session_summary` 1개,
#          격리 `none`/`container` 의 workdir 즉시 삭제
#
# **경로 주의.** `respondHitlRequest` 는 x-phase P3 이라 이 스택에서 501 이다. P2 에서 사람이
# 세션을 끝내는 정식 경로는 `completeSession`(FR-2.2 `manual`, E6-08)뿐이므로 E6-03 의 기대값을
# 그 경로로 옮겨 적용한다 — `user_approval` 원자 자체를 충족시키는 HTTP 입구가 P2 에 없다는 사실은
# 결함으로 따로 기록한다(G5_REPORT S-25). 두 가지를 다 잰다:
#   (1) HITL 카드가 실제로 발행되는가 (E6-01 — P2 서버가 하는 일)
#   (2) 승인 응답으로 원자를 충족시킬 수 있는가 (respondHitlRequest — P3 이면 501 을 기록)
#   (3) completeSession 으로 완료 전이·요약·workdir 삭제가 도는가 (E6-03 의 나머지)
#
# 산출물: out/approval.json · out/a3-checks.tsv
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-a3.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-a3.json"; WORK="$OUT/work-a3"; DLOG="$OUT/daemon-a3.log"
MODEL="${LEAD_MODEL}"
g5_chk_init "$OUT/a3-checks.tsv"

cleanup() { [ -f "$OUT/daemon-a3.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-a3.pid")" 2>/dev/null || true; }; return 0; }
trap cleanup EXIT

step "0. 계정 · 페어링"
: > "$DLOG"
EMAIL="g5a3+$STAMP@example.com"
signup "$EMAIL" "password123" "Director" >/dev/null
WS="$(create_workspace "G5 Approval $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "$SERVER_URL" --workdir-root "$WORK" --no-turn 2>&1 | tail -1 >&2
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a3.pid"
wait_pairing "$WS" "$PID_" 120 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "ws=$WS runtime=$RUNTIME"

step "1. Writer(지정 제출자) · 세션 (종료 조건 artifact_submitted(Writer) AND user_approval)"
source "$P2_DIR/fixtures/scenario_a_agents.sh"
SUBMIT_INS='You are a Writer. Always answer in Korean.

On your first turn:
  a. Write a short Korean report draft (10 to 20 lines, markdown) about the session goal from your own general knowledge. Save it with the Write tool as "report-draft.md" in your current working directory.
  b. Call colab_artifact_submit with type "doc", file set to the ABSOLUTE path of that file, and name "product-x-market-report.md".
  c. Call colab_status_set with status "done". This must be the LAST tool call of the turn.
  d. End your turn. Post no message.

Do not search the web. Apart from writing that one file, use only the colab_* tools.'
WRTR="$(create_agent_p2 "$WS" Writer writer "$MODEL" "$SUBMIT_INS" '보고서 초안을 쓰고 아티팩트로 제출한다')"
SESSION="$(create_session_p2 "$WS" "제품 X 보고서 (승인)" "$SCENARIO_GOAL" "$WRTR" "$RUNTIME" "$WRTR" "$WRTR")"
echo "$WS $SESSION $WRTR $RUNTIME" > "$OUT/a3-ids.txt"
ok "session $SESSION"
T_START="$(now_ms)"
CP0="$(completion_progress "$SESSION")"
chk P0  "제출 전 진행률 0/2"  "0/2" "$(jq -r '"\(.met)/\(.total)"' <<<"$CP0")"
chk P0b "제출 전 satisfied=false" false "$(jq -r .satisfied <<<"$CP0")"

step "2. E6-01 — Writer 제출까지"
ART=""; DEADLINE=$(( $(date +%s) + ${SUBMIT_TIMEOUT_S:-600} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  ART="$(psqlq "select id from artifact where session_id='$SESSION' order by created_at desc limit 1")"
  [ -n "$ART" ] && [ "$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','dispatched','running')")" = 0 ] && break
  sleep 4
done
[ -n "$ART" ] || { lanes_of "$SESSION" | column -t -s $'\t' >&2; die "아티팩트가 제출되지 않았다 (E6-01 전제 실패)"; }
T_SUBMIT="$(now_ms)"
chk P1  "지정 에이전트(Writer)가 제출했다"           Writer \
  "$(psqlq "select a.name from artifact ar join agent a on a.id=ar.submitted_by_agent_id where ar.id='$ART'")"
CP1="$(completion_progress "$SESSION")"; log "completion_progress: $CP1"
chk P1b "artifact_submitted 충족"                     true "$(jq -r '.conditions[]|select(.type=="artifact_submitted")|.met' <<<"$CP1")"
chk P1c "user_approval 미충족"                        false "$(jq -r '.conditions[]|select(.type=="user_approval")|.met' <<<"$CP1")"
chk P1d "진행률 1/2"                                  "1/2" "$(jq -r '"\(.met)/\(.total)"' <<<"$CP1")"
chk P1e "세션은 active 유지 (E6-01)"                  active "$(psqlq "select status from session where id='$SESSION'")"
chk P1f "human_gate=true (사람 승인이 남았다)"        true "$(jq -r .human_gate <<<"$CP1")"
# 플랫폼이 발행한 approval HITL
HITL="$(psqlq "select id from hitl_request where session_id='$SESSION' and type='approval' order by created_at desc limit 1")"
chk P2  "플랫폼이 approval HITL 을 발행했다"          yes "$( [ -n "$HITL" ] && echo yes || echo no )"
if [ -n "$HITL" ]; then
  IFS=$'\t' read -r H_SRC H_TASK H_APPROVER <<<"$(psqlq "select source::text, coalesce(task_id::text,'NULL'), approver_spec from hitl_request where id='$HITL'")"
  chk P2b "그 HITL 의 source 가 system 이다 (에이전트 턴이 아니다)" system "$H_SRC"
  chk P2c "task_id 가 비어 있다 (§7)"                 NULL "$H_TASK"
  chk P2d "승인자는 Director 다"                      director "$H_APPROVER"
  chk P2e "HITL 이 열려 있다"                          open "$(psqlq "select status from hitl_request where id='$HITL'")"
fi

step "3. 승인 응답 경로 — respondHitlRequest 가 P2 에 있는가"
RESP_CODE=NA
if [ -n "$HITL" ]; then
  RESP_CODE="$(api POST "/hitl-requests/$HITL/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)" | api_code)"
fi
RESP_OK=no; [ "${RESP_CODE:0:1}" = 2 ] && RESP_OK=yes
chk P3  "respondHitlRequest 로 user_approval 을 충족시킬 수 있다 (HTTP $RESP_CODE)" yes "$RESP_OK"
if [ "$RESP_OK" = yes ]; then
  APPROVE_PATH=respondHitlRequest
else
  log "respondHitlRequest 가 $RESP_CODE — x-phase P3 이므로 P2 의 승인 경로(completeSession)로 판정한다"
  APPROVE_PATH=completeSession
fi

step "4. E6-03 — 승인 → completing → completed + 요약 자동 게시"
WD_PATHS="$(psqlq "select w.path_or_ref from workdir w where w.session_id='$SESSION'")"
WD_BEFORE=0; while read -r p; do [ -n "$p" ] && [ -d "$p" ] && WD_BEFORE=$((WD_BEFORE+1)); done <<<"$WD_PATHS"
chk P4  "완료 전 workdir 디렉토리가 존재한다 (삭제를 잴 기준)" yes "$( [ "$WD_BEFORE" -ge 1 ] && echo yes || echo no )"
if [ "$APPROVE_PATH" = completeSession ]; then
  api_ok POST "/sessions/$SESSION/complete" '{"confirm":true}' >/dev/null
fi
sleep 2
S_STATUS="$(psqlq "select status from session where id='$SESSION'")"
chk P5  "세션이 completed 다"                          completed "$S_STATUS"
chk P5b "finished_at 이 찍혔다"                        yes "$(psqlq "select case when finished_at is null then 'no' else 'yes' end from session where id='$SESSION'")"
chk P5c "paused_reason·paused_detail 이 비워졌다"      "1" \
  "$(psqlq "select count(*) from session where id='$SESSION' and paused_reason is null and paused_detail is null")"
chk P6  "session_summary 메시지가 정확히 1개 (FR-2.4)"  1 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and kind='summary'")"
psqlq "select replace(content,E'\n','⏎') from message where session_id='$SESSION' and kind='summary' limit 1" > "$OUT/a3-summary.txt"
chk P6b "요약이 system 이 쓴 것이다"                   system \
  "$(psqlq "select author_type::text from message where session_id='$SESSION' and kind='summary' limit 1")"
chk_ge P6c "요약 본문이 비어 있지 않다 (본문 품질은 P4)" 20 "$(wc -c < "$OUT/a3-summary.txt" | tr -d ' ')"
chk P7  "남아 있던 queued/deferred task 가 취소됐다"    0 \
  "$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','deferred')")"
chk P8  "Director 인박스에 session_completed 알림"      1 \
  "$(psqlq "select count(*) from inbox_item where session_id='$SESSION' and type='session_completed'")"
# 완료 원자: respondHitl 경로면 user_approval, completeSession 경로면 manual
MET="$(psqlq "select completion_met::text from session where id='$SESSION'")"
printf '%s\n' "$MET" > "$OUT/a3-met.json"
log "completion_met = $MET (경로 $APPROVE_PATH)"
if [ "$APPROVE_PATH" = respondHitlRequest ]; then
  chk P9 "user_approval 원자가 충족됐다"               true "$(jq -r '.user_approval // false' <<<"$MET")"
else
  chk P9 "manual 원자로 완료됐다 (P2 의 승인 경로)"    true "$(jq -r '.manual // false' <<<"$MET")"
  chk P9b "user_approval 원자는 여전히 미충족 — P2 에 입구가 없다 (S-25)" false "$(jq -r '.user_approval // false' <<<"$MET")"
fi
# workdir 즉시 삭제 (격리 none)
sleep 3
WD_AFTER=0; while read -r p; do [ -n "$p" ] && [ -d "$p" ] && WD_AFTER=$((WD_AFTER+1)); done <<<"$WD_PATHS"
chk P10 "격리 none 의 workdir 가 즉시 삭제됐다 (E6-03)" 0 "$WD_AFTER"
log "workdir: 완료 전 $WD_BEFORE 개 → 완료 후 $WD_AFTER 개"
GC_CMD="$(psqlq "select count(*) from daemon_command where session_id='$SESSION' and type='gc'" 2>/dev/null || echo NA)"
log "서버가 낸 gc 명령 수 = $GC_CMD (daemon-protocol §6: 삭제는 데몬이, 판정은 서버가)"

T_END="$(now_ms)"
step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg artifact "$ART" --arg hitl "${HITL:-}" \
  --arg approve_path "$APPROVE_PATH" --arg respond_code "$RESP_CODE" --arg status "$S_STATUS" \
  --argjson wd_before "$WD_BEFORE" --argjson wd_after "$WD_AFTER" \
  --argjson submit_s "$(( (T_SUBMIT-T_START)/1000 ))" --argjson elapsed_s "$(( (T_END-T_START)/1000 ))" \
  --argjson pass "$pass" --argjson fail "$fail" --argjson met "$(cat "$OUT/a3-met.json")" \
  '{workspace:$ws,session:$session,artifact:$artifact,hitl_request:$hitl,approve_path:$approve_path,
    respond_hitl_http:$respond_code,final_status:$status,completion_met:$met,
    workdirs:{before:$wd_before,after:$wd_after},submit_s:$submit_s,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' \
  | tee "$OUT/approval.json"
[ "$fail" = 0 ]
