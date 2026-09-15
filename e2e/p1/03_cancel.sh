#!/usr/bin/env bash
# e2e/p1/03_cancel.sh — (c) 취소: 실행 중(툴 sleep) 취소 → 프로세스 트리 잔존 0 (E11-07, E10-03, E10-04, E10-13)
#  (A) 사람 경로 — 서버 `cancelLane`(PR #33, 202) 로 발동: 명령 발행 → 데몬 취소 절차 → finish outcome=cancelled
#      → task cancelled(failure_kind=cancelled) · lane failed · **재큐잉 없음** · 프로세스 트리 0.
#  (B) 데몬 정상 종료 경로 — SIGTERM 1회(E10-13, PR #38): harness §5 절차 → finish cancelled(= failed 아님).
# 전제: 01_vertical_slice.sh 실행 후(a-ids.txt, cookies-a.txt, daemon-a 실행 중).
source "$(dirname "$0")/lib.sh"
read -r WS SESSION_A AGENT RUNTIME_ID < "$OUT/a-ids.txt"
COOKIE="$OUT/cookies-a.txt"; CFG="$OUT/daemon-a.json"; WORK="$OUT/work-a"; DLOG="$OUT/daemon-a.log"
if [ -f "$OUT/daemon-a.pid" ] && kill -0 "$(cat "$OUT/daemon-a.pid")" 2>/dev/null; then DPID="$(cat "$OUT/daemon-a.pid")"; else
  daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a.pid"; DPID="$(cat "$OUT/daemon-a.pid")"; sleep 3; fi
MENTION="$(mention Lead "$AGENT")"

lane_status() { psqlq "select status from lane where id='$1'"; }
task_failure_kind() { psqlq "select coalesce(failure_kind::text,'-') from task where id='$1'"; }
# wait_lane LANE STATUS... — WAIT_S 초 안에 원하는 상태가 되면 그 상태를 출력
wait_lane() {
  local lane="$1"; shift; local want=" $* " s i
  for ((i=0;i<${WAIT_S:-120};i++)); do s="$(lane_status "$lane")"; case "$want" in *" $s "*) echo "$s"; return 0;; esac; sleep 1; done
  echo "timeout($s)"; return 1
}
# start_sleep_task SESSION → "task<TAB>lane<TAB>pgid<TAB>procs" — sleep 120 을 시키고 run_shell 이 실제로 뜰 때까지 기다린다
start_sleep_task() {
  local session="$1" res task lane n i pgid
  res="$(post_message "$session" "$MENTION 셸(Bash)에서 \`sleep 120\` 을 실행한 뒤 'cancel-done' 이라고만 게시해줘. sleep 전에는 아무것도 게시하지 마.")"
  task="$(jq -r '.triggers[0].task_id' <<<"$res")"; lane="$(jq -r '.triggers[0].lane_id' <<<"$res")"
  ok "task $task lane $lane" >&2
  WAIT_S=180 wait_task "$task" running >/dev/null || die "task not running: $(task_status "$task")"
  for ((i=0;i<150;i++)); do
    n="$(psqlq "select count(*) from task_event where task_id='$task' and class='tool' and verb='run_shell' and outcome='started'")"
    [ "${n:-0}" -ge 1 ] && break; sleep 1
  done
  [ "${n:-0}" -ge 1 ] || die "run_shell started event not seen (events: $(psqlq "select class||'.'||coalesce(verb,'')||'→'||coalesce(outcome,'') from task_event where task_id='$task' order by seq" | tr '\n' ' '))"
  pgid="$(pgid_of_attempt "$WORK" "$task" 1)"; [ -n "$pgid" ] || die "no pgid record (E11-01)"
  ok "running; pgid=$pgid (E11-01 기록 확인) 프로세스 트리:" >&2; pg_procs "$pgid" | sed 's/^/    /' >&2
  printf '%s\t%s\t%s\t%s\n' "$task" "$lane" "$pgid" "$(pg_procs "$pgid" | wc -l | tr -d ' ')"
}
# assert_tree_gone TASK PGID BEFORE LABEL → 프로세스 그룹·어댑터·pgid 파일·잔여 게시 판정, 잔존 개수 출력
assert_tree_gone() {
  local task="$1" pgid="$2" before="$3" label="$4" left adapters
  left="$(pg_procs "$pgid" | wc -l | tr -d ' ')"
  if pg_alive "$pgid" || [ "$left" -gt 0 ]; then bad "[$label] 프로세스 그룹 $pgid 잔존 $left 개 (E11-07 실패):"; pg_procs "$pgid" | sed 's/^/    /' >&2
  else ok "[$label] 프로세스 그룹 $pgid 잔존 0 (이전 $before 개) — E11-07"; fi
  adapters="$( (pgrep -f 'claude-agent-acp' || true) | wc -l | tr -d ' ')"
  [ "$adapters" = 0 ] && ok "[$label] claude-agent-acp 프로세스 0" || bad "[$label] claude-agent-acp 프로세스 $adapters 잔존"
  [ -f "$WORK/.colab/attempts/$task.1.json" ] && bad "[$label] pgid 기록 파일 남음 (E11-02)" || ok "[$label] pgid 기록 삭제됨 (E11-02)"
  echo "$left"
}
# dump_task TASK — 이벤트·attempt·토큰을 로그로
dump_task() {
  local task="$1"
  log "task status=$(task_status "$task") failure_kind=$(task_failure_kind "$task") attempt=$(task_attempt "$task") attempts=[$(psqlq "select attempt||'/'||coalesce(outcome,'-')||'/'||coalesce(stop_reason,'-') from task_attempt where task_id='$task' order by attempt" | tr '\n' ' ')] token_revoked=$(psqlq "select (revoked_at is not null)||':'||coalesce(revoke_reason,'') from task_token where task_id='$task' and attempt=1")"
  psqlq "select seq, class, coalesce(verb,''), coalesce(outcome,''), left(coalesce(payload::text,''),80) from task_event where task_id='$task' order by seq" | sed 's/^/    ev: /' >&2
}

step "0. 전용 세션 생성 (이전 시나리오의 잔여 task 와 lane 을 섞지 않기 위해)"
# 주의(2026-09-06 실측): goal 에 "취소 시나리오(E11-07)" 처럼 이 저장소의 시나리오 이름을 쓰면
# haiku Lead 가 저장소에서 `e2e/p1/03_cancel.sh` 를 찾아 **스스로 실행**해 세션이 재귀 생성되고 데몬이 SIGTERM 을 맞는다.
# goal 은 저장소를 가리키지 않는 중립 문장이어야 한다.
SESSION="$(create_session "$WS" "$AGENT" "E2E 취소" "지시받은 셸 명령만 그대로 실행하고 결과를 한 줄로 게시한다. 저장소의 파일을 찾아 읽거나 실행하지 않는다." "$RUNTIME_ID")"; ok "session $SESSION"
INIT_TASK="$(session_initial_task "$SESSION")"; WAIT_S=300 wait_task "$INIT_TASK" completed failed >/dev/null || die "initial task did not finish"

# ─────────────────────────── (A) 서버 cancelLane 경로 (E10-04, E11-07) ───────────────────────────
step "A1. 긴 툴 실행 task 게시 (sleep 120)"
IFS=$'\t' read -r TASK LANE PGID NPROC_BEFORE < <(start_sleep_task "$SESSION")

step "A2. 사람 경로 — POST /lanes/$LANE/cancel"
TA0="$(now_ms)"
OUT_C="$(api POST "/lanes/$LANE/cancel" '{}')"; CODE_C="$(api_code <<<"$OUT_C")"; BODY_C="$(api_body <<<"$OUT_C")"
log "POST /lanes/$LANE/cancel → HTTP $CODE_C $(head -c 200 <<<"$BODY_C")"
[ "$CODE_C" = 202 ] && ok "cancelLane 202 (lane.status=$(jq -r '.status // "-"' <<<"$BODY_C"), actions=$(jq -rc '.actions // []' <<<"$BODY_C"))" \
  || bad "cancelLane HTTP $CODE_C — 202 여야 한다(PR #33)"

step "A3. finish outcome=cancelled → task cancelled · lane failed · 재큐잉 없음"
STATUS_A="$(WAIT_S=120 wait_task "$TASK" cancelled completed failed || true)"
TA1="$(now_ms)"; CANCEL_S="$(( (TA1-TA0)/1000 ))"
FK_A="$(task_failure_kind "$TASK")"
case "$STATUS_A" in
  cancelled) ok "task cancelled (${CANCEL_S}s, failure_kind=$FK_A) — daemon-protocol §4.3 cancel → finish cancelled";;
  *) bad "task 상태 '$STATUS_A' failure_kind=$FK_A — cancelLane 이 cancelled 로 끝내지 못했다";;
esac
LANE_A="$(WAIT_S=60 wait_lane "$LANE" failed completed || true)"
[ "$LANE_A" = failed ] && ok "lane failed (failure_kind 는 task 에서 파생 = $FK_A) — SCREEN §4.5" || bad "lane 상태 '$LANE_A' — failed 여야 한다"
ATT_A="$(task_attempt "$TASK")"
# lane 은 (세션, 에이전트) 단위로 재사용된다 — 세션 초기 task 도 같은 lane 에 있다(실측).
# 재큐잉 판정은 "취소된 task 이후에 이 lane 에 새 task 가 생겼는가" 로 본다.
NEW_TASKS_A="$(psqlq "select count(*) from task where lane_id='$LANE' and created_at > (select created_at from task where id='$TASK')")"
QUEUED_A="$(psqlq "select count(*) from task where lane_id='$LANE' and status in ('queued','running')")"
if [ "$ATT_A" = 1 ] && [ "$QUEUED_A" = 0 ] && [ "$NEW_TASKS_A" = 0 ]; then ok "재큐잉 없음 (attempt=1, 취소 뒤 새 task 0, queued/running 0) — E10-04"
else bad "재큐잉 발생: attempt=$ATT_A 취소 뒤 새 task=$NEW_TASKS_A queued/running=$QUEUED_A"; fi
sleep 2
LEFT_A="$(assert_tree_gone "$TASK" "$PGID" "$NPROC_BEFORE" A)"
# S-52(PR #167): `status` payload 는 {command,args,result_ref,rejected_reason} 로 닫혀 있다 —
# 사람이 읽는 문장은 `payload.args.note`(PRD §7 v0.16).
FEED_A="$(psqlq "select count(*) from task_event where task_id='$TASK' and class='status' and verb='cancel' and payload->'args'->>'note'='사람이 중단함'")"
[ "${FEED_A:-0}" -ge 1 ] && ok "활동 피드에 '사람이 중단함' 기록 (E10-04)" || bad "활동 피드에 status/cancel '사람이 중단함' 없음"
DONE_A="$(psqlq "select count(*) from message where session_id='$SESSION' and source_task_id='$TASK' and content like '%cancel-done%'")"
[ "$DONE_A" = 0 ] && ok "취소 뒤 'cancel-done' 게시 0" || bad "취소됐는데 'cancel-done' 게시 $DONE_A"
dump_task "$TASK"
kill -0 "$DPID" 2>/dev/null && ok "데몬은 계속 살아 있다(취소는 데몬을 죽이지 않는다)" || bad "cancelLane 뒤 데몬이 죽었다"

# ─────────────────────── (B) 데몬 정상 종료(SIGTERM) 경로 (E10-13, PR #38) ───────────────────────
step "B1. 두 번째 sleep task 게시 (데몬 SIGTERM 경로용)"
IFS=$'\t' read -r TASK_B LANE_B PGID_B NPROC_B < <(start_sleep_task "$SESSION")

step "B2. 데몬 SIGTERM → harness §5 취소 절차 → finish cancelled (E10-13)"
T0="$(now_ms)"
kill -TERM "$DPID"
for ((i=0;i<90;i++)); do kill -0 "$DPID" 2>/dev/null || break; sleep 1; done
kill -0 "$DPID" 2>/dev/null && die "daemon did not exit within 90s after SIGTERM"
T1="$(now_ms)"; EXIT_S="$(( (T1-T0)/1000 ))"; ok "daemon exited in ${EXIT_S}s"
rm -f "$OUT/daemon-a.pid"
sleep 2
STATUS_B="$(WAIT_S=60 wait_task "$TASK_B" cancelled completed failed || true)"
FK_B="$(task_failure_kind "$TASK_B")"
case "$STATUS_B" in
  cancelled) ok "E10-13: SIGTERM 종료가 finish cancelled 로 보고 (failure_kind=$FK_B)";;
  *) bad "E10-13 실패: task 상태 '$STATUS_B' failure_kind=$FK_B — D-1(ctx 취소가 취소 절차를 앞질러 failed(other))";;
esac
ATT_B="$(task_attempt "$TASK_B")"; QUEUED_B="$(psqlq "select count(*) from task where lane_id='$LANE_B' and status in ('queued','running')")"
[ "$ATT_B" = 1 ] && [ "$QUEUED_B" = 0 ] && ok "재큐잉 없음 (attempt=1, queued/running 0)" || bad "재큐잉 발생: attempt=$ATT_B queued/running=$QUEUED_B"
LANE_B_ST="$(WAIT_S=30 wait_lane "$LANE_B" failed completed || true)"
LEFT_B="$(assert_tree_gone "$TASK_B" "$PGID_B" "$NPROC_B" B)"
DONE_B="$(psqlq "select count(*) from message where session_id='$SESSION' and source_task_id='$TASK_B' and content like '%cancel-done%'")"
[ "$DONE_B" = 0 ] && ok "종료 뒤 'cancel-done' 게시 0" || bad "'cancel-done' 게시 $DONE_B"
# harness §5 순서: 취소 절차가 밟혔으면 runtime.error(failed/other) 없이 runtime.cancel 이 있어야 한다(E10-03)
ERR_B="$(psqlq "select count(*) from task_event where task_id='$TASK_B' and class='runtime' and verb='error' and outcome='failed'")"
CANCEL_EV_B="$(psqlq "select count(*) from task_event where task_id='$TASK_B' and class='runtime' and verb='cancel'")"
[ "${ERR_B:-0}" = 0 ] && ok "runtime.error failed 0건 (E10-03: 즉시 kill 아님)" || bad "runtime.error failed ${ERR_B}건 — 취소 절차 전에 ctx 가 끊겼다"
log "runtime.cancel 이벤트 ${CANCEL_EV_B}건"
dump_task "$TASK_B"
(grep -E "$TASK_B" "$DLOG" || true) | tail -5 | sed 's/^/    daemon: /' >&2

step "결과"
jq -n --arg task "$TASK" --arg lane "$LANE" --arg cancel_http "$CODE_C" --argjson pgid "$PGID" --argjson before "$NPROC_BEFORE" --argjson left "$LEFT_A" \
  --arg status "$STATUS_A" --arg fk "$FK_A" --arg lane_status "$LANE_A" --argjson attempt "$ATT_A" --argjson new_tasks_after_cancel "$NEW_TASKS_A" --argjson queued "$QUEUED_A" \
  --argjson feed "${FEED_A:-0}" --argjson cancel_s "$CANCEL_S" \
  --arg task_b "$TASK_B" --arg status_b "$STATUS_B" --arg fk_b "$FK_B" --arg lane_b_status "$LANE_B_ST" --argjson attempt_b "$ATT_B" --argjson left_b "$LEFT_B" \
  --argjson runtime_error_b "${ERR_B:-0}" --argjson exit_s "$EXIT_S" \
  '{cancelLane:{task:$task,lane:$lane,http:$cancel_http,cancel_to_cancelled_s:$cancel_s,task_status:$status,task_failure_kind:$fk,lane_status:$lane_status,
                attempt:$attempt,new_tasks_after_cancel:$new_tasks_after_cancel,queued_or_running:$queued,feed_cancel_note:$feed,pgid:$pgid,procs_before:$before,procs_left:$left},
    sigterm:{task:$task_b,task_status:$status_b,task_failure_kind:$fk_b,lane_status:$lane_b_status,attempt:$attempt_b,procs_left:$left_b,
             runtime_error_failed:$runtime_error_b,daemon_exit_s:$exit_s}}' | tee "$OUT/c-summary.json"
