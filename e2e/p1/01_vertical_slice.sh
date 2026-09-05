#!/usr/bin/env bash
# e2e/p1/01_vertical_slice.sh — (a) 수직 슬라이스 × N회 (E17-01 claim ≤2s, E17-02 첫 출력 중앙값 ≤10s, E11-08 probe)
# 가입 → 워크스페이스 → 페어링 코드 → daemon pair → probe 도착 → Lead(claude_code, haiku) → 세션(none) → "@Lead 인사해줘" × N → 답글 대기.
# 사용: bash e2e/p1/up.sh && N=20 bash e2e/p1/01_vertical_slice.sh
# 출력: e2e/p1/out/a-latency.tsv (회차별 claim/first_event/first_say/reply 초), a-summary.json, 데몬 로그 daemon-a.log
source "$(dirname "$0")/lib.sh"
N="${N:-20}"
STAMP="$(date +%s)"
EMAIL="e2e-a+${STAMP}@example.com"; PASS="password123"
CFG="$OUT/daemon-a.json"; WORK="$OUT/work-a"; DLOG="$OUT/daemon-a.log"
COOKIE="$OUT/cookies-a.txt"; rm -f "$COOKIE"

step "1. 가입·워크스페이스 ($EMAIL)"
USER_ID="$(signup "$EMAIL" "$PASS" "e2e-a")"; ok "user $USER_ID"
WS="$(create_workspace "e2e-a $STAMP")"; ok "workspace $WS"

step "2. 페어링 코드 → daemon pair → probe 도착 (S12 4단계, E11-08)"
IFS=$'\t' read -r PAIRING_ID CODE < <(create_pairing "$WS")
ok "pairing $PAIRING_ID status=$(pairing_status "$WS" "$PAIRING_ID")"
rm -f "$CFG"; mkdir -p "$WORK"
T0="$(now_ms)"
daemon_pair "$CODE" "$CFG" "$WORK"          # 페어링 직후 probe(PONG 1턴 포함) 를 한 번 보낸다
wait_pairing "$WS" "$PAIRING_ID" 60 || die "pairing did not reach ready"
T1="$(now_ms)"
ok "pairing ready in $(( (T1-T0) ))ms"
RUNTIME_ID="$(jq -r .runtime_id "$CFG")"
api_ok GET "/runtimes/$RUNTIME_ID" | jq -c '{name,status,daemon_version,caps:[.capabilities[]|{kind,version,logged_in,adapter_version,models:(.models|length)}]}' | tee "$OUT/a-runtime.json" >&2
psqlq "select jsonb_path_query(capabilities, '\$[*]') ->> 'kind' from runtime where id='$RUNTIME_ID'" | sed 's/^/  db capability kind: /' >&2 || true

step "3. 데몬 run (colab 토큰 tap 픽스처 포함)"
colab_tap "$CFG"
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a.pid"
ok "daemon pid $(cat "$OUT/daemon-a.pid") log $DLOG"
sleep 2

step "4. 에이전트 Lead (claude_code, $LEAD_MODEL) → 세션(none)"
AGENT="$(create_agent "$WS" Lead "$LEAD_MODEL")"; ok "agent $AGENT"
SESSION="$(create_session "$WS" "$AGENT" "E2E 수직 슬라이스" "@Lead 인사 왕복 E2E. 답글은 한 문장으로.")"; ok "session $SESSION"
INIT_TASK="$(session_initial_task "$SESSION")"
log "초기 task $INIT_TASK 완료 대기 (세션 시작 트리거)"
WAIT_S=420 wait_task "$INIT_TASK" completed failed >/dev/null || die "initial task did not finish: $(task_status "$INIT_TASK")"
ok "initial task $(task_status "$INIT_TASK") replies=$(reply_count "$SESSION" "$INIT_TASK")"
echo -e "iter\ttask\tstatus\tclaim_s\tfirst_event_s\tfirst_runtime_out_s\tfirst_say_s\treply_s\treplies" > "$OUT/a-latency.tsv"

step "5. \"@Lead 인사해줘\" × $N — 매회 답글까지 대기"
MENTION="$(mention Lead "$AGENT")"
for ((i=1;i<=N;i++)); do
  RES="$(post_message "$SESSION" "$MENTION 인사해줘 ($i/$N)")"
  TASK="$(jq -r '.triggers[0].task_id // empty' <<<"$RES")"
  [ -n "$TASK" ] || { bad "iter $i: no trigger: $RES"; echo -e "$i\t-\tno_trigger\t\t\t\t\t0" >> "$OUT/a-latency.tsv"; continue; }
  CO="$(jq -r '.triggers[0].coalesced' <<<"$RES")"
  ST="$(WAIT_S=300 wait_task "$TASK" completed failed cancelled || true)"
  # 답글이 finish 보다 늦게 저장될 수는 없지만(동일 트랜잭션 아님) 1초 여유
  sleep 1
  ROW="$(latency_row "$TASK" "$(task_attempt "$TASK")")"
  REPLIES="$(reply_count "$SESSION" "$TASK")"
  echo -e "$i\t$TASK\t$ST\t$ROW\t$REPLIES" >> "$OUT/a-latency.tsv"
  log "iter $i task=${TASK:0:8} coalesced=$CO status=$ST [claim first_event first_out first_say reply]=$ROW replies=$REPLIES"
done

step "6. 집계"
column -t -s $'\t' "$OUT/a-latency.tsv" >&2
CLAIM_MED="$(awk -F'\t' 'NR>1 && $4!="" {print $4}' "$OUT/a-latency.tsv" | median)"
EVENT_MED="$(awk -F'\t' 'NR>1 && $5!="" {print $5}' "$OUT/a-latency.tsv" | median)"
OUT_MED="$(awk -F'\t' 'NR>1 && $6!="" {print $6}' "$OUT/a-latency.tsv" | median)"
SAY_MED="$(awk -F'\t' 'NR>1 && $7!="" {print $7}' "$OUT/a-latency.tsv" | median)"
REPLY_MED="$(awk -F'\t' 'NR>1 && $8!="" {print $8}' "$OUT/a-latency.tsv" | median)"
CLAIM_MAX="$(awk -F'\t' 'NR>1 && $4!="" {print $4}' "$OUT/a-latency.tsv" | sort -n | tail -1)"
DONE_N="$(awk -F'\t' 'NR>1 && $3=="completed" && $9>=1' "$OUT/a-latency.tsv" | wc -l | tr -d ' ')"
jq -n --arg ws "$WS" --arg session "$SESSION" --arg agent "$AGENT" --arg runtime "$RUNTIME_ID" --arg model "$LEAD_MODEL" \
  --argjson n "$N" --argjson done "$DONE_N" --arg claim "$CLAIM_MED" --arg claim_max "$CLAIM_MAX" --arg ev "$EVENT_MED" --arg fo "$OUT_MED" --arg say "$SAY_MED" --arg reply "$REPLY_MED" --arg pair_ms "$((T1-T0))" \
  '{workspace:$ws,session:$session,agent:$agent,runtime:$runtime,model:$model,iterations:$n,completed_with_reply:$done,median_claim_s:$claim,max_claim_s:$claim_max,median_first_event_s:$ev,median_first_runtime_out_s:$fo,median_first_say_s:$say,median_reply_s:$reply,pairing_to_ready_ms:$pair_ms}' | tee "$OUT/a-summary.json"
echo
[ "$DONE_N" -eq "$N" ] && ok "$N/$N 완료+답글" || bad "$DONE_N/$N 완료+답글"
awk -v m="$CLAIM_MED" 'BEGIN{exit !(m<=2)}' && ok "E17-01 claim 중앙값 ${CLAIM_MED}s ≤ 2s" || bad "E17-01 claim 중앙값 ${CLAIM_MED}s > 2s"
awk -v m="$OUT_MED" 'BEGIN{exit !(m<=10)}' && ok "E17-02 첫 출력(런타임 첫 tool/message 이벤트) 중앙값 ${OUT_MED}s ≤ 10s" || bad "E17-02 첫 출력 중앙값 ${OUT_MED}s > 10s"
awk -v m="$REPLY_MED" 'BEGIN{exit !(m<=10)}' && ok "답글 도착 중앙값 ${REPLY_MED}s ≤ 10s" || bad "답글 도착 중앙값 ${REPLY_MED}s > 10s"
echo "$WS $SESSION $AGENT $RUNTIME_ID" > "$OUT/a-ids.txt"
