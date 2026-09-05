#!/usr/bin/env bash
# e2e/p1/02_kill9.sh — (b) 데몬 kill -9: running 중 kill -9 → heartbeat 만료(3분) → 재큐잉 attempt 2 + 토큰 폐기
#   → 폐기 토큰의 colab message post 401 (E11-03·04) → 데몬 재시작 시 claim 전 고아 pgid 정리 (E11-05)
#   → 같은 메시지 중복 게시 0 (E8-04) · workdir 보존 (E11-06)
# 서버 클럭은 실시간(주입 불가) 이므로 실제로 3분 기다린다. Lead 에게 sleep 210 을 시켜 고아가 만료 시점 뒤에 게시를 시도하게 한다.
# 전제: 01_vertical_slice.sh 실행 후(a-ids.txt, cookies-a.txt, daemon-a.json). 데몬은 이 스크립트가 (재)시작한다.
source "$(dirname "$0")/lib.sh"
read -r WS SESSION_A AGENT RUNTIME_ID < "$OUT/a-ids.txt"
COOKIE="$OUT/cookies-a.txt"; CFG="$OUT/daemon-a.json"; WORK="$OUT/work-a"; DLOG="$OUT/daemon-a.log"
SLEEP_S="${SLEEP_S:-210}"
MENTION="$(mention Lead "$AGENT")"

step "0. 잔여 queued task 정리(03_cancel 이 남긴 attempt 2 — D 결함으로 cancelled 대신 재큐잉됨) + 데몬 기동"
LEFT="$(psqlq "update task set status='cancelled', updated_at=now() where status='queued' and session_id in (select id from session where workspace_id='$WS' and title='E2E 취소') returning id" | wc -l | tr -d ' ')"; log "cancelled leftover queued tasks: $LEFT (로컬 테스트 DB 정리)"
if [ -f "$OUT/daemon-a.pid" ] && kill -0 "$(cat "$OUT/daemon-a.pid")" 2>/dev/null; then DPID="$(cat "$OUT/daemon-a.pid")"; else
  daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a.pid"; DPID="$(cat "$OUT/daemon-a.pid")"; sleep 3; fi
ok "daemon pid $DPID"

step "0a. 전용 세션 생성 (이전 시나리오의 잔여 task 와 lane 을 섞지 않기 위해)"
SESSION="$(create_session "$WS" "$AGENT" "E2E kill -9" "kill -9 시나리오(E11-03~06). 지시받은 순서대로 게시·셸 실행." "$RUNTIME_ID")"; ok "session $SESSION"
INIT_TASK="$(session_initial_task "$SESSION")"; WAIT_S=300 wait_task "$INIT_TASK" completed failed >/dev/null || die "initial task did not finish"


step "1. task 게시: 'kill9-start' 게시 → sleep $SLEEP_S → 'kill9-done' 게시"
RES="$(post_message "$SESSION" "$MENTION 순서대로: (1) 'kill9-start' 라고만 게시, (2) 셸(Bash)에서 \`sleep $SLEEP_S\` 실행, (3) 'kill9-done' 이라고만 게시. 그 외 말은 하지 마.")"
TASK="$(jq -r '.triggers[0].task_id' <<<"$RES")"; LANE="$(jq -r '.triggers[0].lane_id' <<<"$RES")"; ok "task $TASK"
WAIT_S=180 wait_task "$TASK" running >/dev/null || die "task not running: $(task_status "$TASK")"
for ((i=0;i<180;i++)); do
  n="$(psqlq "select count(*) from task_event where task_id='$TASK' and attempt=1 and class='tool' and verb='run_shell' and outcome='started'")"
  m="$(psqlq "select count(*) from message where source_task_id='$TASK' and content like '%kill9-start%'")"
  [ "${n:-0}" -ge 1 ] && [ "${m:-0}" -ge 1 ] && break; sleep 1
done
[ "${n:-0}" -ge 1 ] || die "run_shell started not seen"
[ "${m:-0}" -ge 1 ] || bad "'kill9-start' 가 sleep 전에 게시되지 않음 (LLM 순서 미준수 — 계속 진행)"
T_SLEEP_START="$(date +%s)"
PGID="$(pgid_of_attempt "$WORK" "$TASK" 1)"; [ -n "$PGID" ] || die "no pgid record"
TOKEN1="$(awk -F'\t' -v t="$TASK" '$2==t && $3=="1" {tok=$4} END{print tok}' "$OUT/colab-tap.log")"
[ -n "$TOKEN1" ] || die "attempt-1 token not captured by colab tap ($OUT/colab-tap.log)"
ok "attempt 1 running: pgid=$PGID token=${TOKEN1:0:8}… (tap) start-msg=$m"
HB_BEFORE="$(psqlq "select to_char(heartbeat_at,'HH24:MI:SS') from task where id='$TASK'")"

step "2. 데몬 kill -9 (pid $DPID) — 런타임 프로세스 그룹은 남는다"
kill -9 "$DPID"; T_KILL="$(date +%s)"; rm -f "$OUT/daemon-a.pid"
sleep 2
if pg_alive "$PGID"; then ok "고아 생존 확인 pgid=$PGID ($(pg_procs "$PGID" | wc -l | tr -d ' ') procs)"; pg_procs "$PGID" | sed 's/^/    /' >&2; ORPHAN_ALIVE=true; else bad "런타임 프로세스가 데몬과 함께 죽음(고아 없음) — 고아 시나리오 약화"; ORPHAN_ALIVE=false; fi
log "last heartbeat_at=$HB_BEFORE, 서버 만료 = 마지막 heartbeat + 3분 (sweep 10초 주기)"

step "3. heartbeat 만료 → task queued attempt 2 + 토큰 폐기 대기 (E11-03)"
WAIT_S=330 wait_task_attempt "$TASK" 2 || die "task not requeued within 330s: status=$(task_status "$TASK") attempt=$(task_attempt "$TASK")"
T_REQ="$(date +%s)"; ok "requeued: attempt=$(task_attempt "$TASK") status=$(task_status "$TASK") after $((T_REQ-T_KILL))s"
REV="$(psqlq "select (revoked_at is not null)::text||' '||coalesce(revoke_reason,'') from task_token where task_id='$TASK' and attempt=1")"
[[ "$REV" == true* ]] && ok "attempt-1 token revoked ($REV)" || bad "attempt-1 token NOT revoked ($REV)"
A1="$(psqlq "select coalesce(outcome,'-')||'/'||coalesce(failure_kind::text,'-') from task_attempt where task_id='$TASK' and attempt=1")"; log "attempt 1 outcome=$A1"
CMDS="$(psqlq "select type||':'||coalesce(consumed_by,'pending') from daemon_command where task_id='$TASK' order by created_at" 2>/dev/null | tr '\n' ' ')"; log "daemon_command: $CMDS"

step "4. 폐기 토큰으로 colab message post → 401 token_revoked, 저장 0 (E11-04)"
set +e
POST_OUT="$(COLAB_TASK_TOKEN="$TOKEN1" COLAB_SERVER_URL="$SERVER_URL" COLAB_TASK_ID="$TASK" COLAB_TASK_ATTEMPT=1 COLAB_LANE_ID="$LANE" COLAB_SESSION_ID="$SESSION" COLAB_AGENT_NAME=Lead COLAB_STATE_DIR="$OUT/colab-state-revoked" "$BIN/colab" message post --body "dup-after-revoke" 2>&1)"; RC=$?
set -e
log "colab exit=$RC $(head -c 300 <<<"$POST_OUT")"
[ "$RC" = 4 ] && grep -q token_revoked <<<"$POST_OUT" && ok "exit 4 token_revoked" || bad "expected exit 4 token_revoked, got exit $RC"
DUP="$(psqlq "select count(*) from message where session_id='$SESSION' and content like '%dup-after-revoke%'")"; [ "$DUP" = 0 ] && ok "저장 0" || bad "폐기 토큰 메시지 저장됨 $DUP"

step "5. 고아의 자체 게시 시도 시점(sleep 종료 ≈ +${SLEEP_S}s) 까지 대기 후 'kill9-done'(attempt 1) 게시 여부"
until [ "$(date +%s)" -ge $((T_SLEEP_START + SLEEP_S + 25)) ]; do sleep 5; done
DONE1="$(psqlq "select count(*) from message where source_task_id='$TASK' and content like '%kill9-done%'")"
[ "$DONE1" = 0 ] && ok "폐기 뒤 고아의 'kill9-done' 게시 0 (401 로 막힘)" || bad "고아가 폐기 뒤에도 게시함: $DONE1 (토큰 폐기 실패)"
pg_alive "$PGID" && log "고아 pgid=$PGID 아직 생존 ($(pg_procs "$PGID" | wc -l | tr -d ' ') procs) — 재시작 sweep 대상" || log "고아 pgid=$PGID 이미 종료"
ORPHAN_ALIVE_AT_RESTART="$(pg_alive "$PGID" && echo true || echo false)"

step "6. 데몬 재시작 → claim 전 고아 정리 (E11-05) → attempt 2 실행 → 완료"
LOGMARK="$(wc -l < "$DLOG")"
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a.pid"; DPID="$(cat "$OUT/daemon-a.pid")"
sleep 4
(tail -n +"$((LOGMARK+1))" "$DLOG" | grep -E "orphan|claim" || true) | head -5 | sed 's/^/    daemon: /' >&2
SWEEP="$( (tail -n +"$((LOGMARK+1))" "$DLOG" | grep -E "orphan $TASK\.1 " || true) | head -1)"
[ -n "$SWEEP" ] && ok "sweep 로그: $SWEEP" || bad "sweep 로그에 $TASK.1 없음"
pg_alive "$PGID" && bad "고아 pgid=$PGID 여전히 생존" || ok "고아 pgid=$PGID 종료됨 (프로세스 0)"
WAIT_S=$((SLEEP_S+240)) wait_task "$TASK" completed failed cancelled >/dev/null || die "attempt 2 did not finish: $(task_status "$TASK")"
sleep 1
ok "task $(task_status "$TASK") attempt=$(task_attempt "$TASK")"

step "7. 판정: 중복 게시 0 (E8-04) · workdir 보존 (E11-06)"
psqlq "select attempt, coalesce(outcome,'-'), coalesce(failure_kind::text,'-'), coalesce(stop_reason,'-') from task_attempt where task_id='$TASK' order by attempt" | sed 's/^/    attempt: /' >&2
psqlq "select to_char(created_at,'HH24:MI:SS'), coalesce(source_task_id::text,'-'), left(content,60) from message where session_id='$SESSION' and created_at > (select created_at from message where id=(select trigger_message_id from task where id='$TASK')) order by created_at" | sed 's/^/    msg: /' >&2
START_N="$(psqlq "select count(*) from message where source_task_id='$TASK' and content like '%kill9-start%'")"
DONE_N="$(psqlq "select count(*) from message where source_task_id='$TASK' and content like '%kill9-done%'")"
TOTAL_N="$(psqlq "select count(*) from message where source_task_id='$TASK'")"
[ "$START_N" = 1 ] && ok "'kill9-start' 정확히 1건 (attempt 2 가 재게시하지 않음)" || bad "'kill9-start' $START_N 건"
[ "$DONE_N" = 1 ] && ok "'kill9-done' 정확히 1건" || bad "'kill9-done' $DONE_N 건"
WD="$WORK/sessions/$SESSION/$LANE"; [ -d "$WD" ] && ok "workdir 보존 $WD" || bad "workdir 없음 $WD"
REV2="$(psqlq "select (revoked_at is not null)::text from task_token where task_id='$TASK' and attempt=2")"; log "attempt-2 token revoked after finish: $REV2"
jq -n --arg task "$TASK" --argjson pgid "$PGID" --argjson orphan_alive_after_kill "$ORPHAN_ALIVE" --argjson orphan_alive_at_restart "$ORPHAN_ALIVE_AT_RESTART" --argjson requeue_after_kill_s "$((T_REQ-T_KILL))" \
  --arg token1_revoked "$REV" --argjson revoked_post_exit "$RC" --argjson dup_saved "$DUP" --argjson done_by_orphan "$DONE1" --argjson start_msgs "$START_N" --argjson done_msgs "$DONE_N" --argjson total_msgs "$TOTAL_N" --arg final "$(task_status "$TASK")" --arg attempt1 "$A1" \
  '{task:$task,pgid:$pgid,orphan_alive_after_kill:$orphan_alive_after_kill,orphan_alive_at_restart:$orphan_alive_at_restart,requeue_after_kill_s:$requeue_after_kill_s,token1_revoked:$token1_revoked,attempt1_outcome:$attempt1,revoked_post_exit:$revoked_post_exit,revoked_post_saved:$dup_saved,kill9_done_by_orphan:$done_by_orphan,kill9_start_msgs:$start_msgs,kill9_done_msgs:$done_msgs,task_msgs_total:$total_msgs,final_status:$final}' | tee "$OUT/b-summary.json"
