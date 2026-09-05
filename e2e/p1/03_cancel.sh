#!/usr/bin/env bash
# e2e/p1/03_cancel.sh — (c) 취소: 실행 중(툴 sleep) 취소 → 프로세스 트리 잔존 0 (E11-07, E10-03 실기 대응)
# P1 서버는 cancelLane 이 501 이므로(PR #22 501 목록) API 경로를 먼저 시도해 코드를 기록하고,
# 데몬의 취소 절차(harness §5: session/cancel → 드레인 → 프로세스 그룹 종료)는 데몬 SIGTERM(kill_switch) 로 발동시킨다.
# 전제: 01_vertical_slice.sh 실행 후(a-ids.txt, cookies-a.txt, daemon-a 실행 중).
source "$(dirname "$0")/lib.sh"
read -r WS SESSION_A AGENT RUNTIME_ID < "$OUT/a-ids.txt"
COOKIE="$OUT/cookies-a.txt"; CFG="$OUT/daemon-a.json"; WORK="$OUT/work-a"; DLOG="$OUT/daemon-a.log"
if [ -f "$OUT/daemon-a.pid" ] && kill -0 "$(cat "$OUT/daemon-a.pid")" 2>/dev/null; then DPID="$(cat "$OUT/daemon-a.pid")"; else
  daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a.pid"; DPID="$(cat "$OUT/daemon-a.pid")"; sleep 3; fi
MENTION="$(mention Lead "$AGENT")"

step "0a. 전용 세션 생성 (이전 시나리오의 잔여 task 와 lane 을 섞지 않기 위해)"
SESSION="$(create_session "$WS" "$AGENT" "E2E 취소" "취소 시나리오(E11-07). 지시받은 셸 명령을 실행하고 짧게 게시." "$RUNTIME_ID")"; ok "session $SESSION"
INIT_TASK="$(session_initial_task "$SESSION")"; WAIT_S=300 wait_task "$INIT_TASK" completed failed >/dev/null || die "initial task did not finish"

step "1. 긴 툴 실행 task 게시 (sleep 120)"
RES="$(post_message "$SESSION" "$MENTION 셸(Bash)에서 \`sleep 120\` 을 실행한 뒤 'cancel-done' 이라고만 게시해줘. sleep 전에는 아무것도 게시하지 마.")"
TASK="$(jq -r '.triggers[0].task_id' <<<"$RES")"; LANE="$(jq -r '.triggers[0].lane_id' <<<"$RES")"
ok "task $TASK lane $LANE"
WAIT_S=180 wait_task "$TASK" running >/dev/null || die "task not running: $(task_status "$TASK")"
for ((i=0;i<150;i++)); do
  n="$(psqlq "select count(*) from task_event where task_id='$TASK' and class='tool' and verb='run_shell' and outcome='started'")"
  [ "${n:-0}" -ge 1 ] && break; sleep 1
done
[ "${n:-0}" -ge 1 ] || die "run_shell started event not seen (events: $(psqlq "select class||'.'||coalesce(verb,'')||'→'||coalesce(outcome,'') from task_event where task_id='$TASK' order by seq" | tr '\n' ' '))"
PGID="$(pgid_of_attempt "$WORK" "$TASK" 1)"; [ -n "$PGID" ] || die "no pgid record (E11-01)"
ok "running; pgid=$PGID (E11-01 기록 확인) 프로세스 트리:"; pg_procs "$PGID" | sed 's/^/    /' >&2
NPROC_BEFORE="$(pg_procs "$PGID" | wc -l | tr -d ' ')"

step "2. API 취소 경로 (cancelLane) 시도"
OUT_C="$(api POST "/lanes/$LANE/cancel" '{}')"; CODE_C="$(api_code <<<"$OUT_C")"
log "POST /lanes/$LANE/cancel → HTTP $CODE_C $(api_body <<<"$OUT_C" | head -c 200)"
[ "$CODE_C" = 501 ] && bad "cancelLane 501 not_implemented — 서버(S) P1 범위 밖. 데몬 취소 절차는 SIGTERM 으로 발동" || ok "cancelLane HTTP $CODE_C"

step "3. 데몬 SIGTERM → 취소 절차(kill_switch) → 프로세스 트리 0"
T0="$(now_ms)"
kill -TERM "$DPID"
for ((i=0;i<90;i++)); do kill -0 "$DPID" 2>/dev/null || break; sleep 1; done
kill -0 "$DPID" 2>/dev/null && die "daemon did not exit within 90s after SIGTERM"
T1="$(now_ms)"; ok "daemon exited in $(( (T1-T0)/1000 ))s"
rm -f "$OUT/daemon-a.pid"
sleep 2
LEFT="$(pg_procs "$PGID" | wc -l | tr -d ' ')"
if pg_alive "$PGID" || [ "$LEFT" -gt 0 ]; then bad "프로세스 그룹 $PGID 잔존 $LEFT 개 (E11-07 실패):"; pg_procs "$PGID" | sed 's/^/    /' >&2; else ok "프로세스 그룹 $PGID 잔존 0 (이전 $NPROC_BEFORE 개) — E11-07"; fi
ADAPTERS="$( (pgrep -f 'claude-agent-acp' || true) | wc -l | tr -d ' ')"; [ "$ADAPTERS" = 0 ] && ok "claude-agent-acp 프로세스 0" || bad "claude-agent-acp 프로세스 $ADAPTERS 잔존"
[ -f "$WORK/.colab/attempts/$TASK.1.json" ] && bad "pgid 기록 파일 남음 (E11-02)" || ok "pgid 기록 삭제됨 (E11-02)"
STATUS="$(task_status "$TASK")"
case "$STATUS" in cancelled) ok "task 상태 cancelled (daemon-protocol §4.3 cancel → finish cancelled)";; *) bad "task 상태 '$STATUS' attempt=$(task_attempt "$TASK") — 취소가 cancelled 로 끝나지 않고 재큐잉/실패 (D: 종료 경로가 ctx 취소로 failed(other) 보고)";; esac
ATT="$(psqlq "select attempt||'/'||coalesce(outcome,'-')||'/'||coalesce(stop_reason,'-') from task_attempt where task_id='$TASK' order by attempt")"
log "task status=$STATUS attempt=$(task_attempt "$TASK") attempts=[$(tr '\n' ' ' <<<"$ATT")] token_revoked=$(psqlq "select revoked_at is not null||':'||coalesce(revoke_reason,'') from task_token where task_id='$TASK' and attempt=1")"
psqlq "select seq, class, coalesce(verb,''), coalesce(outcome,''), left(coalesce(payload::text,''),80) from task_event where task_id='$TASK' order by seq" | sed 's/^/    ev: /' >&2
DONE="$(psqlq "select count(*) from message where session_id='$SESSION' and source_task_id='$TASK' and content like '%cancel-done%'")"
[ "$DONE" = 0 ] && ok "취소 뒤 'cancel-done' 게시 0" || bad "취소됐는데 'cancel-done' 게시 $DONE"
(grep -E "$TASK" "$DLOG" || true) | tail -5 | sed 's/^/    daemon: /' >&2
jq -n --arg task "$TASK" --arg lane "$LANE" --arg cancel_http "$CODE_C" --argjson pgid "$PGID" --argjson before "$NPROC_BEFORE" --argjson left "$LEFT" --arg status "$STATUS" --arg exit_s "$(( (T1-T0)/1000 ))" \
  '{task:$task,lane:$lane,cancelLane_http:$cancel_http,pgid:$pgid,procs_before:$before,procs_left:$left,task_status:$status,daemon_exit_s:$exit_s}' | tee "$OUT/c-summary.json"
