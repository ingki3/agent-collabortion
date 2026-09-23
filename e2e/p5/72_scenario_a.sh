#!/usr/bin/env bash
# e2e/p5/72_scenario_a.sh — **시나리오 A 전체** (PRD §4 A 4~8단계 · EVAL E1-15·E1-21·E6-01·E7·FR-2.4).
#
# 비용 한 줄(I-3): 페이크 턴 ≈ 9(Lead 3 · Researcher 3 · Writer 2 + 요약 폴백) · $0 · ≈ 10s. 실기(RUNTIME=real): 같은 턴 haiku ≈ $0.05 · ≈ 5분
#
#   Lead 위임 3 → Researcher lane 3 병렬 → 합류 1회 → Lead 종합 · Writer 위임 → Writer **HITL 질문**
#   (proposed_default, 턴 종료) → Director 답 → Writer 새 attempt(resume 우선) → 아티팩트 제출 →
#   `artifact_submitted` 충족 → 플랫폼이 `user_approval` HITL 발행 → Director 승인 → **completed** → 요약 1개.
#
# 판정은 `e2e/p2/10_scenario_a_api.sh`(G4) 의 A1~A9 를 그대로 옮기고 HITL·승인·완료·요약을 더했다.
# 기본은 **페이크 런타임**(lib.sh) — CI 에서 돈다. `RUNTIME=real` 이면 실기(claude_code 로그인 필요).
# 산출물: out/72-checks.tsv · out/72.json · out/72-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-72.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-72.json"; WORK="$P5_TMP_ROOT/72/work"; DLOG="$OUT/daemon-72.log"
TAP="$OUT/tap-72.jsonl"; TAP_PORT="${TAP_PORT_72:-8119}"
MODEL="${LEAD_MODEL}"
g5_chk_init "$OUT/72-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-72.pid"; return 0; }
trap cleanup EXIT

# 실기 지시문(페이크는 fixtures/agent.sh 가 같은 규칙을 셸로 한다)
source "$E2E_ROOT/e2e/p2/fixtures/scenario_a_agents.sh"
# WRITER_CHARS: 실기 Writer 의 집필 길이(S-66 자극). 3000자 ≈ 5 KB · 35s(haiku 실측). #204 이전 데몬의 stall 은
# "tool 입력 생성 3분 무음" 이라 ≈ 30 KB 부터 난다 — 전후 대조의 "재현" 판은 15000 이상으로 돌린다.
WRITER_CHARS="${WRITER_CHARS:-3000}"
WRITER_INS_P5="$WRITER_INS
Before writing the draft, ask ONE question with colab_hitl_ask: question \"타깃 독자가 투자자인지 내부 경영진인지 정해 주세요\", default \"투자자\", choices \"투자자,경영진\". Then END YOUR TURN without writing anything. When you are resumed with the answer, write the draft as ONE file with a single Write call — at least $WRITER_CHARS Korean characters, no placeholders, no shortcuts — and submit it. $P5_RULES"

step "0. claim 탭 (서버→데몬 TaskBundle 기록) · 페이크 런타임"
rm -f "$TAP" "$OUT/tap-72-access.tsv"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT (pid $TAP_PID) · RUNTIME=$RUNTIME"

step "1. 계정 · 워크스페이스 · 페어링(capacity 4)"
EMAIL="i5a+$STAMP@example.com"
signup "$EMAIL" password123 Director >/dev/null
WS="$(create_workspace "G9 Scenario A $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-72.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"
KINDS="$(runtime_kinds "$RUNTIME_ID")"
chk A0 "런타임이 claude_code 를 광고한다 (probe, kinds=$KINDS)" yes "$(in_set claude_code $KINDS)"

step "2. 에이전트 3 (Lead · Researcher · Writer, claude_code) · 세션(artifact_submitted(Writer) AND user_approval)"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$MODEL" "$LEAD_INS" '팀을 이끌고 위임·종합한다')"
RSCH="$(create_agent_fake "$WS" Researcher researcher claude_code "$MODEL" "$RES_INS" '주어진 항목을 조사해 요약한다')"
WRTR="$(create_agent_fake "$WS" Writer writer claude_code "$MODEL" "$WRITER_INS_P5" '보고서 초안을 쓰고 아티팩트로 제출한다')"
SESSION="$(create_session_p2 "$WS" "제품 X 시장 조사" "$SCENARIO_GOAL" "$LEAD" "$RUNTIME_ID" "$WRTR" "$LEAD" "$RSCH" "$WRTR")"
echo "$WS $SESSION $LEAD $RSCH $WRTR $RUNTIME_ID $EMAIL" > "$OUT/72-ids.txt"
ok "session $SESSION"
T0="$(now_ms)"

step "3. Researcher 3 lane 동시 running (폴링 · 단계 timeout, I-1) → Writer 의 HITL 질문까지 (합류 → Writer 위임 → hitl ask)"
# A2d 의 자리(CI 흔들림, PR #249 attempt 1: got=2). 페이크 대본의 barrier(fixtures/agent.sh Researcher)가 셋이 모일 때까지
# 붙들고, 여기서 그 순간을 DB 로 잡는다. 못 잡으면 이 행이 FAIL 이고 뒤의 A2d(스윕)도 FAIL — 두 눈으로 같은 것을 본다.
wait_step A2d0 "Researcher task 3개가 **동시에** running (FR-6.3, 폴링)" "$T_TURN" \
  '[ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'"'$SESSION'"'"' and a.name='"'"'Researcher'"'"' and t.status='"'"'running'"'"'")" = 3 ]' 0.3 || true
H1="$(wait_hitl "$SESSION" $((T_TURN*3)))"
chk A_H1 "Writer 가 HITL 질문을 열었다 (FR-5.1)" yes "$( [ -n "$H1" ] && echo yes || echo no )"
if [ -n "$H1" ]; then
  IFS=$'\t' read -r H_SRC H_TYPE H_PURPOSE H_STATUS H_APPR H_TASK <<<"$(psqlq "select source::text, type::text, coalesce(purpose,'-'), status::text, approver_spec, coalesce(task_id::text,'-') from hitl_request where id='$H1'")"
  chk A_H1b "질문 type=choice (choices 2)"                choice "$H_TYPE"
  chk A_H1c "source=agent · 승인자 director"              "agent|director" "$H_SRC|$H_APPR"
  chk A_H1d "proposed_default 가 있다 (E7-05)"             투자자 "$(hitl_field "$H1" proposed_default)"
  T_W="$H_TASK"
  wait_until 60 '[ "$(task_field "'"$T_W"'" status)" = waiting_human ]' || true
  chk A_H1e "Writer task 가 waiting_human (턴 종료, 슬롯 반납)" waiting_human "$(task_field "$T_W" status)"
  chk A_H1f "Director 인박스에 hitl 항목"                   yes "$( [ "$(inbox_count "$SESSION" hitl_request)" -ge 1 ] && echo yes || echo no )"
  step "4. Director 가 답한다 → Writer 새 attempt(resume 우선) → 아티팩트"
  read -r RC _ <<<"$(respond_hitl "$H1" '{"answer":"투자자"}')"
  chk A_H2 "respondHitlRequest 2xx (HTTP $RC)" yes "$( [ "${RC:0:1}" = 2 ] && echo yes || echo no )"
  wait_task_attempt "$T_W" 2 || true
  WAIT_S=$T_TURN wait_task "$T_W" completed failed cancelled >/dev/null || true
  chk A_H2b "Writer attempt 2 로 재개됐다"                  yes "$( [ "$(task_field "$T_W" attempt)" -ge 2 ] && echo yes || echo no )"
  RESUMED="$(psqlq "select coalesce(resumed::text,'-') from task_attempt where task_id='$T_W' and attempt=2")"
  chk A_H2c "attempt 2 가 런타임 resume 로 이어졌다 (E7 resume 우선)" true "$RESUMED"
  tap_prompt "$TAP" "$T_W" 2 > "$OUT/72-writer-prompt-attempt2.txt" 2>/dev/null || true
  chk_has A_H2d "attempt 2 프롬프트에 <resumed> + 답이 있다" "$OUT/72-writer-prompt-attempt2.txt" "투자자"
fi

step "5. 진행 대기 — Lead 3번째 턴 종료 · 활동 0"
DEADLINE=$(( $(date +%s) + T_TURN*2 )); ART_ID=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  ART_ID="$(psqlq "select id from artifact where session_id='$SESSION' order by created_at desc limit 1")"
  LEADN="$(count_tasks_of_agent "$SESSION" Lead)"
  ACT="$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','dispatched','preparing','running')")"
  if [ -n "$ART_ID" ] && [ "$LEADN" -ge 3 ] && [ "$ACT" = 0 ]; then break; fi
  sleep 3
done
T_END="$(now_ms)"; log "여기까지 $(( (T_END-T0)/1000 ))s"
echo "── lane 보드 ──" >&2; lanes_of "$SESSION" | column -t -s $'\t' >&2
echo "── Lead task ──" >&2; tasks_of_agent "$SESSION" Lead | column -t -s $'\t' >&2

step "6. 판정 (G4 A1~A9 그대로)"
LEAD_TASKS="$(count_tasks_of_agent "$SESSION" Lead)"
chk A1 "Lead 가 깨어난 횟수 = 3 (위임1+합류1+통보1)" 3 "$LEAD_TASKS"
chk A1b "Lead task 가 전부 completed" "$LEAD_TASKS" "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$SESSION' and a.name='Lead' and t.status='completed'")"
R_LANES="$(lanes_count "$SESSION" Researcher)"
chk A2 "Researcher lane 3개 (위임 3 = lane 3, FR-6.1)" 3 "$R_LANES"
R_WD_DISK=0
while read -r lid; do [ -n "$lid" ] && [ -d "$WORK/sessions/$SESSION/$lid" ] && R_WD_DISK=$((R_WD_DISK+1)); done \
  <<<"$(psqlq "select l.id from lane l join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher'")"
chk A2b "격리 none → Researcher workdir 디렉토리 3개 (FR-6.1)" 3 "$R_WD_DISK"
chk A2e "Researcher lane 3개가 각각 workdir 행을 가리킨다" 3 "$(psqlq "select count(*) from workdir w join lane l on l.workdir_id=w.id join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher'")"
chk A2f "격리 none → workdir 행 수 = lane 수" "$(psqlq "select count(*) from lane where session_id='$SESSION'")" "$(psqlq "select count(*) from workdir where session_id='$SESSION'")"
OVERLAP="$(running_overlap "$SESSION" Researcher)"
chk_ge A2c "Researcher lane 동시 running 최대 겹침 (FR-6.3)" 2 "$OVERLAP"
chk A2d "동시 3개 (위임 3이 병렬)" 3 "$OVERLAP"
echo "── 합류 발화 ──" >&2; join_fired "$SESSION" | column -t -s $'\t' >&2
J_ROWS="$(join_fired "$SESSION" | wc -l | tr -d ' ')"
chk A3 "합류 그룹 발화 2건 (J1=Researcher 3, J2=Writer 1)" 2 "$J_ROWS"
J1_TASK="$(join_fired "$SESSION" | head -1 | cut -f1)"
chk A3b "J1 자식 lane 3개" 3 "$(join_fired "$SESSION" | head -1 | cut -f3)"
chk A3c "합류 시스템 메시지 = 그룹 수 (정확히 1회씩)" "$J_ROWS" "$(psqlq "select count(*) from message where session_id='$SESSION' and author_type='system' and content like '위임한 작업이 모두 끝났습니다%'")"
R_MENTION="$(psqlq "select count(*) from message m join agent a on a.id=m.author_id where m.session_id='$SESSION' and a.name='Researcher' and m.mentions::text like '%$LEAD%'")"
chk_ge A4 "Researcher 가 @Lead 를 멘션한 메시지" 1 "$R_MENTION"
chk A4b "규칙 8: 합류 전 Researcher 멘션은 Lead task 를 만들지 않았다 (E1-15)" 0 \
  "$(psqlq "select count(*) from task t join message m on m.id=t.trigger_message_id join agent a on a.id=m.author_id
            where t.session_id='$SESSION' and t.agent_id='$LEAD' and a.name='Researcher' and m.created_at < (select join_fired_at from task where id='$J1_TASK')")"
JOIN_TASK="$(psqlq "select t.id from task t join message m on m.id=t.trigger_message_id where t.session_id='$SESSION' and t.agent_id='$LEAD' and m.author_type='system' and m.content like '위임한 작업이 모두 끝났습니다%' order by t.created_at limit 1")"
psqlq "select left(replace(regexp_replace(m.content, '^(\[@[^]]*\]\([^)]*\)[[:space:]]*)+', ''), E'\n','⏎'),60) from message m join agent a on a.id=m.author_id
       where m.session_id='$SESSION' and a.name='Researcher' and m.created_at < (select join_fired_at from task where id='$J1_TASK') order by m.created_at" > "$OUT/72-child-msgs.txt"
tap_prompt "$TAP" "$JOIN_TASK" 1 > "$OUT/72-join-prompt.txt" 2>/dev/null || true
CARRIED=0
while IFS= read -r frag; do [ -n "$frag" ] || continue; key="$(printf '%s' "$frag" | sed 's/⏎.*//')"; grep -qF -e "$key" "$OUT/72-join-prompt.txt" && CARRIED=$((CARRIED+1)); done < "$OUT/72-child-msgs.txt"
chk A4c "합류 턴 프롬프트가 자식 메시지 3개를 싣는다 (E1-21)" 3 "$CARRIED"
chk_has A4d "합류 프롬프트의 trigger 가 합류 시스템 메시지다" "$OUT/72-join-prompt.txt" "위임한 작업이 모두 끝났습니다"
chk A5 "Writer 가 아티팩트를 제출했다" yes "$( [ -n "$ART_ID" ] && echo yes || echo no )"
if [ -n "$ART_ID" ]; then
  chk A5b "제출자는 Writer" Writer "$(psqlq "select a.name from artifact ar join agent a on a.id=ar.submitted_by_agent_id where ar.id='$ART_ID'")"
  chk_ge A5c "submitArtifact 201 (프록시 액세스 로그)" 1 "$(awk -F'\t' '$2=="POST" && $3 ~ /\/artifacts$/ && $4==201' "$OUT/tap-72-access.tsv" 2>/dev/null | wc -l | tr -d ' ')"
  DL="$OUT/72-artifact-dl.bin"
  DECL="$(curl -sS -D - -o "$DL" -b "$COOKIE" "$API/artifacts/$ART_ID/content" | tr -d '\r' | grep -i '^content-length:' | tail -1 | awk '{print $2}')"
  chk A6 "다운로드 바이트 = Content-Length" "${DECL:-none}" "$(wc -c < "$DL" | tr -d ' ')"
  chk_ge A6b "본문 ≥ 3000 bytes (집필 길이)" 3000 "$(wc -c < "$DL" | tr -d ' ')"
fi
CP="$(completion_progress "$SESSION")"; log "completion_progress: $CP"
chk A7 "진행률 met=1 (artifact_submitted 충족, user_approval 미충족)" 1 "$(jq -r .met <<<"$CP")"
chk A7c "artifact_submitted met=true" true "$(jq -r '.conditions[]|select(.type=="artifact_submitted")|.met' <<<"$CP")"
chk A7d "user_approval met=false" false "$(jq -r '.conditions[]|select(.type=="user_approval")|.met' <<<"$CP")"
chk A7g "세션은 아직 active (E6-01)" active "$(sess_status "$SESSION")"
PV="$(api_ok POST "/sessions/$SESSION/messages/preview" "$(jq -nc --arg c "$(mention Researcher "$RSCH") 보완해줘" '{content:$c}')")"
chk A8 "previewTriggers 가 Researcher 를 지목" Researcher "$(jq -r '.triggers[0].agent_name // empty' <<<"$PV")"
chk A9 "auth 실패 0" 0 "$(psqlq "select count(*) from task where session_id='$SESSION' and failure_kind='auth'")"
chk A9c "probe 의 colab_cli.present 가 API 에 실린다" true "$(api_ok GET "/runtimes/$RUNTIME_ID" | jq -r '.colab_cli.present // "null"')"

step "7. 8단계 — 플랫폼이 user_approval HITL 발행 → Director 승인 → completed → 요약 1개"
H2="$(wait_hitl "$SESSION" 60 user_approval)"
chk A10 "플랫폼이 user_approval HITL 을 발행했다 (source=system)" system "$( [ -n "$H2" ] && hitl_field "$H2" source || echo none)"
if [ -n "$H2" ]; then
  read -r RC2 _ <<<"$(respond_hitl "$H2" '{"approved":true}')"
  chk A10b "승인 2xx (HTTP $RC2)" yes "$( [ "${RC2:0:1}" = 2 ] && echo yes || echo no )"
  wait_until 60 '[ "$(sess_status "'"$SESSION"'")" = completed ]' || true
fi
chk A11  "세션 completed (자동 완료, E6-03)" completed "$(sess_status "$SESSION")"
chk A11b "완료 원자: artifact_submitted+user_approval, manual 아님" "true|true|false" \
  "$(psqlq "select completion_met::text from work where room_id='$SESSION'" | jq -r '"\(.artifact_submitted // false)|\(.user_approval // false)|\(.manual // false)"')"
chk A12  "session_summary 메시지 정확히 1개 (FR-2.4)" 1 "$(summary_count "$SESSION")"
summary_body "$SESSION" > "$OUT/72-summary.txt" 2>/dev/null || true
chk A12b "요약에 FR-2.4 네 절" 4 "$(psqlq "select (content like '%결정 기록%')::int + (content like '%아티팩트%')::int + (content like '%비용%')::int + (content like '%타임라인%')::int from message where session_id='$SESSION' and kind='summary'")"
GB="$(psqlq "select coalesce(e.object_ref::text,'-') from task_event e join task t on t.id=e.task_id where t.session_id='$SESSION' and e.object_ref::text like '%generated_by%' limit 1")"
chk A12c "generated_by 피드 항목 ($GB)" yes "$(printf '%s' "$GB" | grep -q generated_by && echo yes || echo no)"
chk A13  "실패한 attempt 0 (전 attempt completed)" 0 "$(psqlq "select count(*) from task_attempt ta join task t on t.id=ta.task_id where t.session_id='$SESSION' and ta.outcome not in ('completed')")"
ELAPSED=$(( ($(now_ms)-T0)/1000 ))

step "결과"
printf '판정: PASS %d · FAIL %d · %ds\n' "$pass" "$fail" "$ELAPSED" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg runtime "$RUNTIME_ID" --arg artifact "${ART_ID:-}" --arg mode "$RUNTIME" \
  --argjson lead_tasks "${LEAD_TASKS:-0}" --argjson r_lanes "${R_LANES:-0}" --argjson overlap "${OVERLAP:-0}" \
  --argjson joins "${J_ROWS:-0}" --argjson carried "${CARRIED:-0}" --argjson elapsed_s "$ELAPSED" \
  --argjson pass "$pass" --argjson fail "$fail" \
  '{runtime_mode:$mode,workspace:$ws,session:$session,runtime:$runtime,artifact:$artifact,lead_wakeups:$lead_tasks,researcher_lanes:$r_lanes,
    max_concurrent_researcher_lanes:$overlap,join_groups_fired:$joins,child_messages_in_join_prompt:$carried,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/72.json"
[ "$fail" = 0 ]
