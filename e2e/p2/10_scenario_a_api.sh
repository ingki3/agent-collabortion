#!/usr/bin/env bash
# e2e/p2/10_scenario_a_api.sh — 시나리오 A 1부(G4)를 **API/CLI 경로**로 끝까지 돌린다.
#
#   Lead 가 3항목을 `colab lane delegate` 로 위임 → Researcher lane 3개 **병렬**
#   → 셋 완료 시 합류 **정확히 1회** → Lead 종합 → Writer 초안 → `artifact submit`.
#
# 판정 수치(§2 TASK):
#   (1) Lead 가 깨어난 횟수 = 3 (위임 1 + 합류 1 + 통보 1)
#   (2) Researcher lane 3개가 실제로 동시에 running 이었던 구간
#   (3) 합류 묶음 페이로드에 3개 결과 + 억제된 자식 메시지 (E1-21)
#   (4) artifact submit 201, 다운로드 바이트 = Content-Length
#   (5) 종료 조건 진행률이 artifact_submitted 를 반영
#
# 과제는 **저장소 밖의 무해한 주제**다 — goal·brief 에 이 저장소의 파일명을 쓰지 않는다(G3_DECISION §2 X-2).
# 산출물: out/scenario-a.json, out/claim-tap.jsonl, out/daemon-a.log
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-a.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-a.json"; WORK="$OUT/work-a"; DLOG="$OUT/daemon-a.log"
TAP="$OUT/claim-tap.jsonl"; TAP_PORT="${TAP_PORT:-8091}"
MODEL="${LEAD_MODEL}"
RES="$OUT/scenario-a.json"
CHK="$OUT/a-checks.tsv"; echo -e "id\twhat\tverdict\tvalue" > "$CHK"
pass=0; fail=0
chk() { # id 설명 기대 실제
  if [ "$3" = "$4" ]; then pass=$((pass+1)); printf '  ✓ %-52s %s\n' "$2" "$4" >&2; echo -e "$1\t$2\tPASS\t$4" >> "$CHK"
  else fail=$((fail+1)); printf '  ✗ %-52s got=%s want=%s\n' "$2" "$4" "$3" >&2; echo -e "$1\t$2\tFAIL\tgot=$4 want=$3" >> "$CHK"; fi
}
chk_ge() { # id 설명 최소 실제
  if [ "${4:-0}" -ge "$3" ] 2>/dev/null; then pass=$((pass+1)); printf '  ✓ %-52s %s (≥%s)\n' "$2" "$4" "$3" >&2; echo -e "$1\t$2\tPASS\t$4" >> "$CHK"
  else fail=$((fail+1)); printf '  ✗ %-52s got=%s want≥%s\n' "$2" "$4" "$3" >&2; echo -e "$1\t$2\tFAIL\tgot=$4 want>=$3" >> "$CHK"; fi
}

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$OUT/daemon-a.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-a.pid")" 2>/dev/null || true; }
  return 0
}
trap cleanup EXIT

step "0. claim 탭(테스트 픽스처) — 서버가 데몬에 보내는 TaskBundle 을 기록한다"
rm -f "$TAP" "$OUT/claim-tap-access.tsv"; : > "$TAP"
: > "$DLOG"   # 데몬 로그는 이번 실행 것만 — 누적 로그를 세면 이전 실행의 실패가 이번 판정에 섞인다
python3 "$P2_DIR/fixtures/claimtap.py" "$TAP_PORT" "$SERVER_URL" "$TAP" & TAP_PID=$!
for i in $(seq 1 20); do curl -fsS -o /dev/null "http://localhost:$TAP_PORT/healthz" 2>/dev/null && break; sleep 0.3; done
ok "tap :$TAP_PORT → $SERVER_URL (pid $TAP_PID)"

step "1. 가입 · 워크스페이스 (이름은 ASCII)"
EMAIL="g4a+$STAMP@example.com"
signup "$EMAIL" "password123" "Director" >/dev/null
WS="$(create_workspace "G4 Scenario A $STAMP")"
ok "workspace $WS"

step "2. 페어링 → bin/daemon pair/run"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "http://localhost:$TAP_PORT" --workdir-root "$WORK" --no-turn 2>&1 | tail -2 >&2
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-a.pid"
wait_pairing "$WS" "$PID_" 90 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "runtime $RUNTIME ready"

step "3. 에이전트 3개 (Lead · Researcher · Writer) — 모두 claude_code 단일 런타임"
source "$P2_DIR/fixtures/scenario_a_agents.sh"
LEAD="$(create_agent_p2 "$WS" Lead lead "$MODEL" "$LEAD_INS" '팀을 이끌고 위임·종합한다')"
RSCH="$(create_agent_p2 "$WS" Researcher researcher "$MODEL" "$RES_INS" '주어진 항목을 조사해 요약한다')"
WRTR="$(create_agent_p2 "$WS" Writer writer "$MODEL" "$WRITER_INS" '보고서 초안을 쓰고 아티팩트로 제출한다')"
ok "Lead=$LEAD Researcher=$RSCH Writer=$WRTR"

step "4. 세션 — 격리 none, 종료 조건 artifact_submitted(Writer) AND user_approval"
SESSION="$(create_session_p2 "$WS" "제품 X 시장 조사" "$SCENARIO_GOAL" "$LEAD" "$RUNTIME" "$WRTR" "$LEAD" "$RSCH" "$WRTR")"
echo "$WS $SESSION $LEAD $RSCH $WRTR $RUNTIME" > "$OUT/a-ids.txt"
ok "session $SESSION"
T_START="$(now_ms)"

step "5. 진행 대기 — Writer 아티팩트 제출 또는 Lead 3번째 턴 종료까지"
DEADLINE=$(( $(date +%s) + ${SCENARIO_TIMEOUT_S:-900} ))
ART_ID=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  ART_ID="$(psqlq "select id from artifact where session_id='$SESSION' order by created_at desc limit 1")"
  LEADN="$(count_tasks_of_agent "$SESSION" Lead)"
  ACT="$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','dispatched','running')")"
  if [ -n "$ART_ID" ] && [ "$LEADN" -ge 3 ] && [ "$ACT" = 0 ]; then break; fi
  sleep 5
done
T_END="$(now_ms)"
log "총 소요 $(( (T_END-T_START)/1000 ))s"

step "6. 판정"
echo "── lane 보드 ──" >&2; lanes_of "$SESSION" | column -t -s $'\t' >&2
echo "── Lead task ──" >&2; tasks_of_agent "$SESSION" Lead | column -t -s $'\t' >&2

# (1) Lead 가 깨어난 횟수 = 3
LEAD_TASKS="$(count_tasks_of_agent "$SESSION" Lead)"
chk A1 "Lead 가 깨어난 횟수 = 3 (위임1+합류1+통보1)" 3 "$LEAD_TASKS"
LEAD_DONE="$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$SESSION' and a.name='Lead' and t.status='completed'")"
chk A1b "Lead task 가 전부 completed" "$LEAD_TASKS" "$LEAD_DONE"

# (2) Researcher lane 3개 + 동시 실행
R_LANES="$(psqlq "select count(*) from lane l join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher'")"
chk A2 "Researcher lane 3개 (위임 3 = lane 3, FR-6.1)" 3 "$R_LANES"
# FR-6.1: 격리 none 이면 workdir 는 lane 당 하나. 실제 디렉토리로 센다(데몬이 만든다).
R_LANE_IDS="$(psqlq "select l.id from lane l join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher'")"
R_WD_DISK=0
while read -r lid; do [ -n "$lid" ] && [ -d "$WORK/sessions/$SESSION/$lid" ] && R_WD_DISK=$((R_WD_DISK+1)); done <<<"$R_LANE_IDS"
chk A2b "격리 none → Researcher workdir 디렉토리 3개 (FR-6.1)" 3 "$R_WD_DISK"
# 같은 사실이 서버에도 있어야 한다 — openapi Lane.workdir_id 는 required 필드다.
R_WD_DB="$(psqlq "select count(*) from workdir where session_id='$SESSION'")"
chk A2e "workdir 행이 서버에 기록된다 (openapi Lane.workdir_id)" 3 "$R_WD_DB"
OVERLAP="$(running_overlap "$SESSION" Researcher)"
chk_ge A2c "Researcher lane 동시 running 최대 겹침 (FR-6.3)" 2 "$OVERLAP"
chk A2d "동시 3개 (위임 3이 병렬)" 3 "$OVERLAP"

# (3) 합류 정확히 1회 + 페이로드
echo "── 합류 발화 ──" >&2; join_fired "$SESSION" | column -t -s $'\t' >&2
J_ROWS="$(join_fired "$SESSION" | wc -l | tr -d ' ')"
chk A3 "합류 그룹 발화 2건 (J1=Researcher 3, J2=Writer 1)" 2 "$J_ROWS"
J1_TASK="$(join_fired "$SESSION" | head -1 | cut -f1)"
J1_CHILDREN="$(join_fired "$SESSION" | head -1 | cut -f3)"
chk A3b "J1 자식 lane 3개" 3 "$J1_CHILDREN"
J_MSGS="$(psqlq "select count(*) from message where session_id='$SESSION' and author_type='system' and content like '위임한 작업이 모두 끝났습니다%'")"
chk A3c "합류 시스템 메시지 = 그룹 수 (정확히 1회씩)" "$J_ROWS" "$J_MSGS"

# E1-15/21 — Researcher 가 @Lead 를 멘션했지만 트리거되지 않았고(억제), 그 메시지가 합류 묶음에 실린다
R_MENTION="$(psqlq "select count(*) from message m join agent a on a.id=m.author_id where m.session_id='$SESSION' and a.name='Researcher' and m.mentions::text like '%$LEAD%'")"
chk_ge A4 "Researcher 가 @Lead 를 멘션한 메시지" 1 "$R_MENTION"
# 억제 기간은 **합류 발화 전까지**다(E1-17) — 그 전에 온 Researcher 멘션만 센다.
LEAD_BY_MENTION="$(psqlq "select count(*) from task t join message m on m.id=t.trigger_message_id join agent a on a.id=m.author_id
  where t.session_id='$SESSION' and t.agent_id='$LEAD' and a.name='Researcher'
    and m.created_at < (select join_fired_at from task where id='$J1_TASK')")"
chk A4b "규칙 8: 합류 전 Researcher 멘션은 Lead task 를 만들지 않았다 (E1-15)" 0 "$LEAD_BY_MENTION"
# 합류 턴 프롬프트(=claim 응답의 TaskBundle.prompt)에 자식 결과 3개가 실렸는가 (E1-21)
# 합류로 깨어난 Lead task = trigger_message 가 J1 의 합류 시스템 메시지인 task
JOIN_TASK="$(psqlq "select t.id from task t join message m on m.id=t.trigger_message_id
  where t.session_id='$SESSION' and t.agent_id='$LEAD' and m.author_type='system'
    and m.content like '위임한 작업이 모두 끝났습니다%' order by t.created_at limit 1")"
# 멘션 링크는 세 메시지가 똑같으므로 지우고 본문 앞부분을 지문으로 쓴다.
psqlq "select left(replace(regexp_replace(m.content, '^(\[@[^]]*\]\([^)]*\)[[:space:]]*)+', ''), E'\n','⏎'),60) from message m join agent a on a.id=m.author_id
       where m.session_id='$SESSION' and a.name='Researcher'
         and m.created_at < (select join_fired_at from task where id='$J1_TASK') order by m.created_at" > "$OUT/a-child-msgs.txt"
python3 "$P2_DIR/fixtures/prompt_of_task.py" "$TAP" "$JOIN_TASK" > "$OUT/a-join-prompt.txt"
CARRIED=0
while IFS= read -r frag; do
  [ -n "$frag" ] || continue
  # cut -c 는 로케일에 따라 바이트를 자른다(멀티바이트가 깨진다) — 첫 줄 전체를 그대로 지문으로 쓴다.
  key="$(printf '%s' "$frag" | sed 's/⏎.*//')"
  grep -qF -e "$key" "$OUT/a-join-prompt.txt" && CARRIED=$((CARRIED+1))   # -e: 지문이 '-' 로 시작할 수 있다
done < "$OUT/a-child-msgs.txt"
chk A4c "합류 턴 프롬프트가 자식 메시지 3개를 싣는다 (E1-21)" 3 "$CARRIED"
chk A4d "합류 프롬프트의 trigger 가 합류 시스템 메시지다" yes "$(grep -q '위임한 작업이 모두 끝났습니다' "$OUT/a-join-prompt.txt" && echo yes || echo no)"

# (4) 아티팩트
chk A5 "Writer 가 아티팩트를 제출했다" yes "$( [ -n "$ART_ID" ] && echo yes || echo no )"
if [ -n "$ART_ID" ]; then
  A_BY="$(psqlq "select a.name from artifact ar join agent a on a.id=ar.submitted_by_agent_id where ar.id='$ART_ID'" 2>/dev/null || echo '?')"
  chk A5b "제출자는 Writer" Writer "$A_BY"
  SUB_201="$(awk -F'\t' '$2=="POST" && $3 ~ /\/artifacts$/ && $4==201' "$OUT/claim-tap-access.tsv" 2>/dev/null | wc -l | tr -d ' ')"
  chk_ge A5c "submitArtifact 201 (프록시 액세스 로그)" 1 "${SUB_201:-0}"
  DL="$OUT/a-artifact-dl.bin"
  DECL="$(curl -sS -D - -o "$DL" -b "$COOKIE" "$API/artifacts/$ART_ID/content" | tr -d '\r' | grep -i '^content-length:' | tail -1 | awk '{print $2}')"
  chk A6 "다운로드 바이트 = Content-Length" "${DECL:-none}" "$(wc -c < "$DL" | tr -d ' ')"
  chk_ge A6b "본문이 비어 있지 않다" 100 "$(wc -c < "$DL" | tr -d ' ')"
fi

# (5) 종료 조건 진행률
CP="$(completion_progress "$SESSION")"
log "completion_progress: $CP"
chk A7 "진행률 met = 1 (artifact_submitted 충족, user_approval 미충족)" 1 "$(jq -r .met <<<"$CP")"
chk A7b "진행률 total = 2" 2 "$(jq -r .total <<<"$CP")"
chk A7c "artifact_submitted 조건 met=true" true "$(jq -r '.conditions[]|select(.type=="artifact_submitted")|.met' <<<"$CP")"
chk A7d "user_approval 조건 met=false" false "$(jq -r '.conditions[]|select(.type=="user_approval")|.met' <<<"$CP")"
chk A7e "satisfied=false (사람 승인 전)" false "$(jq -r .satisfied <<<"$CP")"
chk A7f "human_gate=true" true "$(jq -r .human_gate <<<"$CP")"
SESS_STATUS="$(psqlq "select status from session where id='$SESSION'")"
chk A7g "세션은 아직 active (E6-01)" active "$SESS_STATUS"

# 보너스: previewTriggers 가 서버 값인지 (웹 판정은 11 에서)
PV="$(api_ok POST "/sessions/$SESSION/messages/preview" "$(jq -nc --arg c "$(mention Researcher "$RSCH") 보완해줘" '{content:$c}')")"
chk A8 "previewTriggers 가 Researcher 를 지목" Researcher "$(jq -r '.triggers[0].agent_name // empty' <<<"$PV")"
chk A8b "미리보기가 lane 해소 결과를 준다(로컬 계산 불가)" true "$(jq -r '(.triggers[0].lane.resolution|type=="number")' <<<"$PV")"

# (6) hotfix PR #71 회귀 — 우회 없이 인증되는가, colab CLI 가 probe 에 실리는가
AUTH_FAIL="$(psqlq "select count(*) from task where session_id='$SESSION' and failure_kind='auth'")"
chk A9 "프로파일 env 우회 없이 claude_code 가 인증된다 (PR #71, D-1)" 0 "$AUTH_FAIL"
chk A9b "데몬 로그에 'colab --version failed' 없음 (PR #71, C-1)" 0 "$(grep -c 'colab --version failed' "$DLOG" 2>/dev/null | tr -d ' ')"
RT_JSON="$(api_ok GET "/runtimes/$RUNTIME")"
chk A9c "probe 의 colab_cli.present 가 API 에 실린다 (daemon-protocol §3)" true "$(jq -r '.colab_cli.present // "null"' <<<"$RT_JSON")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg runtime "$RUNTIME" --arg artifact "${ART_ID:-}" \
  --argjson lead_tasks "${LEAD_TASKS:-0}" --argjson r_lanes "${R_LANES:-0}" --argjson overlap "${OVERLAP:-0}" \
  --argjson joins "${J_ROWS:-0}" --argjson carried "${CARRIED:-0}" --argjson elapsed_s "$(( (T_END-T_START)/1000 ))" \
  --argjson pass "$pass" --argjson fail "$fail" --argjson cp "$CP" \
  '{workspace:$ws,session:$session,runtime:$runtime,artifact:$artifact,lead_wakeups:$lead_tasks,researcher_lanes:$r_lanes,
    max_concurrent_researcher_lanes:$overlap,join_groups_fired:$joins,child_messages_in_join_prompt:$carried,
    elapsed_s:$elapsed_s,completion_progress:$cp,pass:$pass,fail:$fail}' | tee "$RES"
[ "$fail" = 0 ]
