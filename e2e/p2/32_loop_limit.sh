#!/usr/bin/env bash
# e2e/p2/32_loop_limit.sh — G5 (d): 루프 상한 E4-03 을 실기로.
#
#   워크스페이스 설정(`updateWorkspaceSettings`, FR-3.5 "워크스페이스 설정에서 조정")으로
#   `max_pair_roundtrips` 를 낮추고 Lead↔Researcher 핑퐁을 만든다. 상한을 넘는 트리거에서
#   세션이 `paused(loop)` 가 되고 `paused_detail.loop.limit = pair_roundtrips` 이며
#   **그 트리거로 task 가 생기지 않는다**.
#
#   E4-03 문언은 기본값 5(5왕복 = 10트리거)이고, 여기서는 같은 규칙을 상한 N 으로 재현한다:
#   pairRoundtrips = (연속 같은 쌍 트리거 수 + 1) / 2 이므로 상한 N 은 2N+1 번째 트리거에서 걸린다.
#
# 산출물: out/loop.json · out/l-checks.tsv
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-l.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-l.json"; WORK="$OUT/work-l"; DLOG="$OUT/daemon-l.log"
MODEL="${LEAD_MODEL}"
LIMIT="${PAIR_LIMIT:-2}"
g5_chk_init "$OUT/l-checks.tsv"

cleanup() { [ -f "$OUT/daemon-l.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-l.pid")" 2>/dev/null || true; }; return 0; }
trap cleanup EXIT

step "0. 계정 · 페어링"
: > "$DLOG"
EMAIL="g5l+$STAMP@example.com"
signup "$EMAIL" "password123" "Director" >/dev/null
WS="$(create_workspace "G5 Loop $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "$SERVER_URL" --workdir-root "$WORK" --no-turn 2>&1 | tail -1 >&2
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-l.pid"
wait_pairing "$WS" "$PID_" 120 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "ws=$WS runtime=$RUNTIME"

step "1. 워크스페이스 설정으로 max_pair_roundtrips 를 $LIMIT 로 (정식 op 가 있는가)"
SET_CODE="$(api PATCH "/workspaces/$WS/settings" "$(jq -nc --argjson v "$LIMIT" '{loop_limits:{max_pair_roundtrips:$v}}')" | api_code)"
SET_OK=no; [ "${SET_CODE:0:1}" = 2 ] && SET_OK=yes
chk L0  "updateWorkspaceSettings 가 501 이 아니다 (x-phase P2, HTTP $SET_CODE)" yes "$SET_OK"
if [ "${SET_CODE:0:1}" != 2 ]; then
  log "설정 op 를 못 쓴다 → 기본값 5 로 실측한다(결함으로 기록)"
  LIMIT=5
fi
GOT="$(api_ok GET "/workspaces/$WS/settings" | jq -r '.loop_limits.max_pair_roundtrips')"
chk L0b "설정이 되읽힌다 (max_pair_roundtrips=$LIMIT)" "$LIMIT" "$GOT"
chk L0c "다른 상한은 건드리지 않았다 (부분 갱신)"  8 "$(api_ok GET "/workspaces/$WS/settings" | jq -r '.loop_limits.max_chain_depth')"

step "2. 서로만 멘션하는 두 에이전트 · 세션"
PING_LEAD='You are Lead. Always answer in Korean and keep every message under 25 words.
Every time you are triggered: call colab_message_post exactly once with a one-line follow-up question about the topic and set mention to ["@Researcher"]. Then end your turn.
Never call colab_status_set. Never call colab_lane_delegate. Never run shell commands, never read or write files, never search the web.'
PING_RES='You are Researcher. Always answer in Korean and keep every message under 25 words.
Every time you are triggered: call colab_message_post exactly once with a one-line answer AND a one-line question back, and set mention to ["@Lead"]. Then end your turn.
Never call colab_status_set. Never call colab_lane_delegate. Never run shell commands, never read or write files, never search the web.'
LEAD="$(create_agent_p2 "$WS" Lead       lead       "$MODEL" "$PING_LEAD" '팀을 이끈다')"
RSCH="$(create_agent_p2 "$WS" Researcher researcher "$MODEL" "$PING_RES"  '조사한다')"
SESSION="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "핑퐁 (E4-03)" --arg g "$SCENARIO_GOAL" \
  --arg a "$LEAD" --arg rt "$RUNTIME" --arg r "$RSCH" \
  '{title:$t,goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a},{agent_id:$r}],assignee_agent_id:$a,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
echo "$WS $SESSION $LEAD $RSCH $RUNTIME" > "$OUT/l-ids.txt"
ok "session $SESSION"
T_START="$(now_ms)"

step "3. 핑퐁이 상한에 걸릴 때까지 (상한 $LIMIT → $((2*LIMIT+1)) 번째 에이전트 트리거에서 정지)"
IDLE=0
DEADLINE=$(( $(date +%s) + ${LOOP_TIMEOUT_S:-900} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  ST="$(psqlq "select status from session where id='$SESSION'")"
  [ "$ST" = paused ] && break
  # 핑퐁이 멈췄는데 상한도 안 걸렸으면(=지시문이 안 먹었으면) 더 기다릴 이유가 없다.
  # 30초 연속 유휴를 두 번 확인해야 포기한다 — 턴 사이 간격을 유휴로 오인하면 측정이 죽는다.
  if [ "$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','dispatched','running')")" = 0 ]; then
    IDLE=$((IDLE+1))
  else
    IDLE=0
  fi
  [ "${IDLE:-0}" -ge 6 ] && { log "30초 넘게 유휴 — 핑퐁이 멈췄다"; break; }
  sleep 5
done
# paused 전이 뒤 취소가 반영될 시간을 준다(§8.2.2 취소는 비동기다)
sleep 8
T_END="$(now_ms)"
echo "── 메시지 흐름 ──" >&2
psqlq "select m.created_at, coalesce(a.name,'human/system'), left(replace(m.content,E'\n','⏎'),70)
       from message m left join agent a on a.id=m.author_id where m.session_id='$SESSION' order by m.created_at" | column -t -s $'\t' >&2
echo "── task ──" >&2
psqlq "select t.created_at, a.name, t.status from task t join agent a on a.id=t.agent_id where t.session_id='$SESSION' order by t.created_at" | column -t -s $'\t' >&2

step "4. 판정 (E4-03)"
IFS=$'\t' read -r S_STATUS S_REASON S_DETAIL <<<"$(session_paused "$SESSION")"
printf '%s\n' "$S_DETAIL" > "$OUT/l-paused-detail.json"
chk L1  "세션이 paused 다"                                 paused "$S_STATUS"
chk L1b "paused_reason = loop"                             loop   "$S_REASON"
chk L2  "paused_detail.loop.limit = pair_roundtrips"       pair_roundtrips "$(jq -r '.loop.limit // "none"' <<<"$S_DETAIL" 2>/dev/null || echo none)"
chk L2b "paused_detail.loop.count 가 상한을 넘는다"         yes \
  "$( [ "$(jq -r '.loop.count // 0' <<<"$S_DETAIL" 2>/dev/null || echo 0)" -gt "$LIMIT" ] 2>/dev/null && echo yes || echo no )"
chk L2c "paused_detail.loop.agents 가 두 명이다 (누가 도는지)" 2 "$(jq -r '(.loop.agents//[])|length' <<<"$S_DETAIL" 2>/dev/null || echo 0)"
chk L2d "paused_detail.reason = loop (분기와 라벨이 같다)"  loop "$(jq -r '.reason // "none"' <<<"$S_DETAIL" 2>/dev/null || echo none)"
# 마지막 트리거로 task 가 생기지 않았다: 에이전트 메시지 수 > 에이전트 task 수
AG_MSGS="$(psqlq "select count(*) from message where session_id='$SESSION' and author_type='agent'")"
AG_TASKS="$(psqlq "select count(*) from task where session_id='$SESSION'")"
chk L3  "상한을 넘긴 트리거로 task 가 생기지 않았다 (메시지 > task)" yes \
  "$( [ "${AG_MSGS:-0}" -ge "${AG_TASKS:-0}" ] && echo yes || echo no )"
chk L3b "마지막 에이전트 메시지 뒤에 새 task 가 없다"       0 \
  "$(psqlq "select count(*) from task t where t.session_id='$SESSION'
            and t.created_at > (select max(created_at) from message where session_id='$SESSION' and author_type='agent')")"
chk L4  "진행 중이던 task 가 남아 있지 않다 (정지가 실제로 멈춘다)" 0 \
  "$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','dispatched','running')")"
chk L5  "Director 에게 알림이 갔다 (FR-3.5 source: system)"  yes \
  "$( [ "$(psqlq "select count(*) from hitl_request where session_id='$SESSION'")" -ge 1 ] 2>/dev/null && echo yes || echo no )"
HITL_SRC="$(psqlq "select coalesce(source::text,'-') from hitl_request where session_id='$SESSION' order by created_at desc limit 1" 2>/dev/null || echo '-')"
chk L5b "그 HITL 의 source 가 system 이다"                  system "$HITL_SRC"

step "5. 상한을 넘긴 그 지점의 왕복 수 (관측값)"
COUNT="$(jq -r '.loop.count // 0' <<<"$S_DETAIL" 2>/dev/null || echo 0)"
log "관측 pair_roundtrips = $COUNT (상한 $LIMIT), 에이전트 메시지 $AG_MSGS · task $AG_TASKS, 소요 $(( (T_END-T_START)/1000 ))s"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg status "$S_STATUS" --arg reason "$S_REASON" \
  --argjson limit "$LIMIT" --argjson count "${COUNT:-0}" --argjson agent_msgs "${AG_MSGS:-0}" --argjson tasks "${AG_TASKS:-0}" \
  --argjson elapsed_s "$(( (T_END-T_START)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  --argjson detail "$(cat "$OUT/l-paused-detail.json")" \
  '{workspace:$ws,session:$session,status:$status,paused_reason:$reason,max_pair_roundtrips:$limit,
    observed_roundtrips:$count,agent_messages:$agent_msgs,tasks:$tasks,paused_detail:$detail,
    elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/loop.json"
[ "$fail" = 0 ]
