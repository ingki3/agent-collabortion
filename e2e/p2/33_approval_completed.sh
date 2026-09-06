#!/usr/bin/env bash
# e2e/p2/33_approval_completed.sh — G5 (a) 뒷단: 종료 조건 → 승인/거절 → `completed`
# (E6-01 · E6-03 · E6-04).
#
#   E6-01  지정 에이전트(Writer)가 `colab artifact submit` → `artifact_submitted` 충족,
#          **플랫폼이** Director 에게 `approval` HITL 발행(`source: system`, `task_id` 비움),
#          세션은 `active` 유지
#   E6-03  Director **승인** → `active → completing → completed`, `session_summary` 1개,
#          격리 `none`/`container` 의 workdir 즉시 삭제
#   E6-04  Director **거절** → 세션 `active` 유지, `artifact_submitted` 플래그 유지,
#          거절 사유가 결정 기록에 저장, 에이전트 트리거 없음
#
# **승인은 정식 op 로 잰다.** 계약 PR #101 이 `respondHitlRequest` 의 승인·거절을 P2 로 확정했고
# (플랫폼 발행 `approval` 한정 — 에이전트 발행 HITL 응답·재큐잉·예산 상향·deputy 는 P3),
# 서버 PR #103 이 구현했다. 마이그레이션 0012 의 `hitl_request.purpose` 가 "이 approval 이 종료
# 조건의 것인가"를 구분한다 — 예산·루프 정지도 `source=system` + `type=approval` 이라 그 둘만으로는
# 갈라지지 않는다. `completeSession` 우회는 더 쓰지 않는다.
#
# 세션 두 개를 **동시에** 띄운다(A=승인 · B=거절). Writer 턴이 병렬로 돌아 대기가 반이다.
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

# hitl_of SESSION → 플랫폼이 발행한 approval HITL id
hitl_of() { psqlq "select id from hitl_request where session_id='$1' and type='approval' and source='system' order by created_at desc limit 1"; }
# wait_submit SESSION [TIMEOUT_S] → 아티팩트 id (없으면 빈 문자열)
wait_submit() {
  local s="$1" dl=$(( $(date +%s) + ${2:-600} )) a=""
  while [ "$(date +%s)" -lt "$dl" ]; do
    a="$(psqlq "select id from artifact where session_id='$s' order by created_at desc limit 1")"
    [ -n "$a" ] && [ "$(psqlq "select count(*) from task where session_id='$s' and status in ('queued','dispatched','running')")" = 0 ] && break
    sleep 4
  done
  printf '%s' "$a"
}

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

step "1. Writer(지정 제출자) · 세션 두 개 (A=승인 · B=거절)"
SUBMIT_INS='You are a Writer. Always answer in Korean.

On your first turn:
  a. Write a short Korean report draft (10 to 20 lines, markdown) about the session goal from your own general knowledge. Save it with the Write tool as "report-draft.md" in your current working directory.
  b. Call colab_artifact_submit with type "doc", file set to the ABSOLUTE path of that file, and name "product-x-market-report.md".
  c. Call colab_status_set with status "done". This must be the LAST tool call of the turn.
  d. End your turn. Post no message.

Do not search the web. Apart from writing that one file, use only the colab_* tools.'
WRTR="$(create_agent_p2 "$WS" Writer writer "$MODEL" "$SUBMIT_INS" '보고서 초안을 쓰고 아티팩트로 제출한다')"
SESSION="$(create_session_p2 "$WS" "제품 X 보고서 (승인)" "$SCENARIO_GOAL" "$WRTR" "$RUNTIME" "$WRTR" "$WRTR")"
SESSION_R="$(create_session_p2 "$WS" "제품 X 보고서 (거절)" "$SCENARIO_GOAL" "$WRTR" "$RUNTIME" "$WRTR" "$WRTR")"
echo "$WS $SESSION $SESSION_R $WRTR $RUNTIME" > "$OUT/a3-ids.txt"
ok "session A=$SESSION · B=$SESSION_R"
T_START="$(now_ms)"
CP0="$(completion_progress "$SESSION")"
chk P0  "제출 전 진행률 0/2"      "0/2" "$(jq -r '"\(.met)/\(.total)"' <<<"$CP0")"
chk P0b "제출 전 satisfied=false" false "$(jq -r .satisfied <<<"$CP0")"

step "2. E6-01 — 두 세션의 Writer 제출까지"
ART="$(wait_submit "$SESSION" "${SUBMIT_TIMEOUT_S:-600}")"
ART_R="$(wait_submit "$SESSION_R" "${SUBMIT_TIMEOUT_S:-600}")"
[ -n "$ART" ]   || { lanes_of "$SESSION"   | column -t -s $'\t' >&2; die "A: 아티팩트가 제출되지 않았다 (E6-01 전제 실패)"; }
[ -n "$ART_R" ] || { lanes_of "$SESSION_R" | column -t -s $'\t' >&2; die "B: 아티팩트가 제출되지 않았다 (E6-04 전제 실패)"; }
T_SUBMIT="$(now_ms)"
chk P1  "지정 에이전트(Writer)가 제출했다"           Writer \
  "$(psqlq "select a.name from artifact ar join agent a on a.id=ar.submitted_by_agent_id where ar.id='$ART'")"
CP1="$(completion_progress "$SESSION")"; log "completion_progress: $CP1"
chk P1b "artifact_submitted 충족"                     true "$(jq -r '.conditions[]|select(.type=="artifact_submitted")|.met' <<<"$CP1")"
chk P1c "user_approval 미충족"                        false "$(jq -r '.conditions[]|select(.type=="user_approval")|.met' <<<"$CP1")"
chk P1d "진행률 1/2"                                  "1/2" "$(jq -r '"\(.met)/\(.total)"' <<<"$CP1")"
chk P1e "세션은 active 유지 (E6-01)"                  active "$(psqlq "select status from session where id='$SESSION'")"
chk P1f "human_gate=true (사람 승인이 남았다)"        true "$(jq -r .human_gate <<<"$CP1")"
HITL="$(hitl_of "$SESSION")"
chk P2  "플랫폼이 approval HITL 을 발행했다"          yes "$( [ -n "$HITL" ] && echo yes || echo no )"
H_PURPOSE=-
if [ -n "$HITL" ]; then
  IFS=$'\t' read -r H_SRC H_TASK H_APPROVER H_PURPOSE <<<"$(psqlq "select source::text, coalesce(task_id::text,'NULL'), approver_spec, coalesce(purpose,'-') from hitl_request where id='$HITL'")"
  chk P2b "그 HITL 의 source 가 system 이다 (에이전트 턴이 아니다)" system "$H_SRC"
  chk P2c "task_id 가 비어 있다 (§7)"                 NULL "$H_TASK"
  chk P2d "승인자는 Director 다"                      director "$H_APPROVER"
  chk P2e "HITL 이 열려 있다"                          open "$(psqlq "select status from hitl_request where id='$HITL'")"
  chk P2f "purpose 가 종료 조건 승인임을 구분한다 (0012)" yes \
    "$( [ "$H_PURPOSE" != '-' ] && [ -n "$H_PURPOSE" ] && echo yes || echo no )"
  log "hitl_request.purpose = $H_PURPOSE"
fi

step "3. E6-04 — 세션 B 를 **거절**한다 (정식 경로)"
HITL_R="$(hitl_of "$SESSION_R")"
chk R0 "B 에도 approval HITL 이 발행됐다"            yes "$( [ -n "$HITL_R" ] && echo yes || echo no )"
REJ_REASON="출처 링크가 없어 이대로는 못 쓴다"
REJ_CODE="$(api POST "/hitl-requests/$HITL_R/response" \
  "$(jq -nc --arg r "$REJ_REASON" '{approved:false,reason:$r}')" -H "Idempotency-Key: $(uuid)" | api_code)"
REJ_OK=no; [ "${REJ_CODE:0:1}" = 2 ] && REJ_OK=yes
chk R1 "거절이 정식 경로로 받아들여진다 (HTTP $REJ_CODE)" yes "$REJ_OK"
sleep 2
chk R2 "거절해도 세션은 active 유지 (E6-04)"         active "$(psqlq "select status from session where id='$SESSION_R'")"
CPR="$(completion_progress "$SESSION_R")"
chk R3 "artifact_submitted 플래그가 유지된다"        true "$(jq -r '.conditions[]|select(.type=="artifact_submitted")|.met' <<<"$CPR")"
chk R4 "user_approval 은 충족되지 않았다"            false "$(jq -r '.conditions[]|select(.type=="user_approval")|.met' <<<"$CPR")"
chk R5 "거절 사유가 결정 기록에 저장된다 (FR-4.2)"   1 \
  "$(psqlq "select count(*) from decision where session_id='$SESSION_R' and rationale='$REJ_REASON'")"
chk R6 "그 결정의 source 가 hitl 이다 (사람이 정했다)" hitl \
  "$(psqlq "select coalesce(source::text,'-') from decision where session_id='$SESSION_R' order by created_at desc limit 1")"
chk R7 "HITL 이 answered 이고 approved=false 다"     "answered|false" \
  "$(psqlq "select status::text||'|'||coalesce(approved::text,'-') from hitl_request where id='$HITL_R'")"
chk R8 "거절이 에이전트를 트리거하지 않았다 (사람이 다음 지시)" 1 \
  "$(psqlq "select count(*) from task where session_id='$SESSION_R'")"
chk R9 "요약 메시지는 아직 없다 (세션이 안 끝났다)"  0 \
  "$(psqlq "select count(*) from message where session_id='$SESSION_R' and kind='summary'")"

step "4. E6-03 — 세션 A 를 **승인**한다 (정식 경로 respondHitlRequest)"
WD_PATHS="$(psqlq "select w.path_or_ref from workdir w where w.session_id='$SESSION'")"
WD_BEFORE=0; while read -r p; do [ -n "$p" ] && [ -d "$p" ] && WD_BEFORE=$((WD_BEFORE+1)); done <<<"$WD_PATHS"
chk P4 "완료 전 workdir 디렉토리가 존재한다 (삭제를 잴 기준)" yes "$( [ "$WD_BEFORE" -ge 1 ] && echo yes || echo no )"
IDEM="$(uuid)"
RESP="$(api POST "/hitl-requests/$HITL/response" '{"approved":true}' -H "Idempotency-Key: $IDEM")"
RESP_CODE="$(api_code <<<"$RESP")"; RESP_BODY="$(api_body <<<"$RESP")"
RESP_OK=no; [ "${RESP_CODE:0:1}" = 2 ] && RESP_OK=yes
chk P5  "respondHitlRequest 로 승인할 수 있다 (계약 #101 · 서버 #103, HTTP $RESP_CODE)" yes "$RESP_OK"
# jq 의 `//` 는 **false 를 빈 값으로 친다** — `.ignored // "none"` 은 false 에도 "none" 을 준다.
chk P5a "응답의 ignored=false (첫 응답이다)"          false "$(jq -r 'if has("ignored") then .ignored else "none" end' <<<"$RESP_BODY")"
REPLAY="$(api POST "/hitl-requests/$HITL/response" '{"approved":true}' -H "Idempotency-Key: $IDEM" | api_code)"
chk P5b "같은 멱등키 재요청도 2xx 로 첫 결과를 돌려준다 (E7-08)" yes \
  "$( [ "${REPLAY:0:1}" = 2 ] && echo yes || echo no )"
sleep 2

S_STATUS="$(psqlq "select status from session where id='$SESSION'")"
chk P6  "세션이 completed 다"                          completed "$S_STATUS"
chk P6b "finished_at 이 찍혔다"                        yes "$(psqlq "select case when finished_at is null then 'no' else 'yes' end from session where id='$SESSION'")"
chk P6c "paused_reason·paused_detail 이 비워졌다"      1 \
  "$(psqlq "select count(*) from session where id='$SESSION' and paused_reason is null and paused_detail is null")"
MET="$(psqlq "select completion_met::text from session where id='$SESSION'")"
printf '%s\n' "$MET" > "$OUT/a3-met.json"
log "completion_met = $MET"
chk P7  "**user_approval 원자가 충족됐다** (S-25 해소)" true "$(jq -r '.user_approval // false' <<<"$MET")"
chk P7b "artifact_submitted 도 그대로 충족"            true "$(jq -r '.artifact_submitted // false' <<<"$MET")"
chk P7c "manual 은 쓰이지 않았다 (우회가 아니다)"      false "$(jq -r '.manual // false' <<<"$MET")"
chk P8  "session_summary 메시지가 정확히 1개 (FR-2.4)"  1 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and kind='summary'")"
psqlq "select replace(content,E'\n','⏎') from message where session_id='$SESSION' and kind='summary' limit 1" > "$OUT/a3-summary.txt"
chk P8b "요약이 system 이 쓴 것이다"                   system \
  "$(psqlq "select author_type::text from message where session_id='$SESSION' and kind='summary' limit 1")"
chk_ge P8c "요약 본문이 비어 있지 않다 (본문 품질은 P4)" 20 "$(wc -c < "$OUT/a3-summary.txt" | tr -d ' ')"
chk P9  "남아 있던 queued/deferred task 가 취소됐다"    0 \
  "$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','deferred')")"
chk P9b "Director 인박스에 session_completed 알림"      1 \
  "$(psqlq "select count(*) from inbox_item where session_id='$SESSION' and type='session_completed'")"

step "5. S-29 — 완료 시 workdir GC (서버가 명령을 내는가 · 행이 정리되는가)"
GC_N="$(psqlq "select count(*) from daemon_command where session_id='$SESSION' and type='gc'")"
chk P10 "서버가 gc 명령을 1건 냈다 (daemon-protocol §6)" 1 "$GC_N"
GC_IDS="$(psqlq "select coalesce(payload->>'workdir_ids','[]') from daemon_command where session_id='$SESSION' and type='gc' limit 1")"
printf '%s\n' "$GC_IDS" > "$OUT/a3-gc.json"
WD_ROWS="$(psqlq "select count(*) from workdir where session_id='$SESSION'")"
chk P10b "gc 명령이 그 세션의 workdir 를 전부 담는다"  "$WD_ROWS" \
  "$(jq -r 'if type=="array" then length else 0 end' <<<"$GC_IDS" 2>/dev/null || echo 0)"
# **여기까지가 P2 다.** 실제 삭제는 데몬 몫이고(daemon-protocol §6: 판정은 서버, 삭제는 데몬),
# 데몬의 `gc` 핸들러는 백로그 **D-4**("worktree·GC·rebind_prepare·예산 강제는 P3·P4")로 아직 없다 —
# `handleCommands` 의 default 가 "command gc ignored (P4)" 를 찍는다. 그래서 행은 `active` 로 남고
# 디렉토리도 그대로다. 이것은 미달이 아니라 **단계 밖**이므로 판정하지 않고 관측만 남긴다.
DL=$(( $(date +%s) + ${GC_TIMEOUT_S:-20} ))
while [ "$(date +%s)" -lt "$DL" ]; do
  [ "$(psqlq "select count(*) from workdir where session_id='$SESSION' and status<>'deleted'")" = 0 ] && break
  sleep 5
done
WD_DELETED="$(psqlq "select count(*) from workdir where session_id='$SESSION' and status='deleted'")"
WD_AFTER=0; while read -r p; do [ -n "$p" ] && [ -d "$p" ] && WD_AFTER=$((WD_AFTER+1)); done <<<"$WD_PATHS"
chk_na P10c "workdir 행이 deleted 로 닫힌다"   "$WD_DELETED/$WD_ROWS" "데몬 gc 핸들러는 P3·P4 (백로그 D-4)"
chk_na P10d "디스크의 workdir 디렉토리가 사라진다" "$WD_AFTER 개 남음" "데몬 gc 핸들러는 P3·P4 (백로그 D-4)"
DELIV="$(psqlq "select case when delivered_at is null then 'no' else 'yes' end from daemon_command where session_id='$SESSION' and type='gc' limit 1")"
log "gc 명령 전달 여부 = $DELIV (유휴 데몬에는 붙을 진행 보고가 없어 전달 자체가 안 된다 — 같은 D-4 범위)"
log "workdir: 디렉토리 $WD_BEFORE → $WD_AFTER, 행 $WD_ROWS 중 deleted $WD_DELETED, gc 명령 $GC_N 건"
{ grep -iE 'gc' "$DLOG" 2>/dev/null || true; } > "$OUT/a3-gc-daemon.txt"
[ -s "$OUT/a3-gc-daemon.txt" ] || printf '(데몬 로그에 gc 관련 줄 없음)\n' > "$OUT/a3-gc-daemon.txt"

T_END="$(now_ms)"
step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg session_r "$SESSION_R" --arg artifact "$ART" \
  --arg hitl "${HITL:-}" --arg hitl_r "${HITL_R:-}" --arg respond_code "$RESP_CODE" --arg reject_code "$REJ_CODE" \
  --arg status "$S_STATUS" --arg purpose "${H_PURPOSE:-}" \
  --argjson wd_before "$WD_BEFORE" --argjson wd_after "$WD_AFTER" --argjson wd_rows "${WD_ROWS:-0}" \
  --argjson wd_deleted "${WD_DELETED:-0}" --argjson gc "${GC_N:-0}" \
  --argjson submit_s "$(( (T_SUBMIT-T_START)/1000 ))" --argjson elapsed_s "$(( (T_END-T_START)/1000 ))" \
  --argjson pass "$pass" --argjson fail "$fail" --argjson met "$(cat "$OUT/a3-met.json")" \
  '{workspace:$ws,session:$session,reject_session:$session_r,artifact:$artifact,
    hitl_request:$hitl,hitl_request_reject:$hitl_r,hitl_purpose:$purpose,
    respond_http:$respond_code,reject_http:$reject_code,final_status:$status,completion_met:$met,
    workdirs:{dirs_before:$wd_before,dirs_after:$wd_after,rows:$wd_rows,deleted:$wd_deleted,gc_commands:$gc},
    submit_s:$submit_s,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' \
  | tee "$OUT/approval.json"
[ "$fail" = 0 ]
