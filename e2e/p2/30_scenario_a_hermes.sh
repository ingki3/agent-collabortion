#!/usr/bin/env bash
# e2e/p2/30_scenario_a_hermes.sh — G5 (a)+(b): 시나리오 A 를 **Hermes 프로파일**로, 그리고 폴백 전환.
#
#   A. probe — hermes 런타임 능력 광고(harness §9)가 실기와 맞는가
#   B. 시나리오 A 를 Researcher = `runtime_kind: hermes` 로 끝까지 (10_ 과 **같은 지시문**,
#      바뀐 것은 런타임 하나뿐이다 — 차이가 프롬프트 차이로 흐려지면 안 된다)
#   C. E8-08 폴백 — hermes 프로파일을 고의로 실패시키면 서버가 claude_code 로 재큐잉하는가
#      (같은 workdir · attempt+1 · runtime_kind 가 바뀌면 resume 비움)
#   D. E8-09 — 대체 프로파일이 없으면 queued 유지 + Director 알림
#
# 과제는 저장소 밖의 무해한 주제다(G3_DECISION §2 X-2). 워크스페이스 이름은 ASCII.
# 산출물: out/hermes.json · out/h-checks.tsv · out/daemon-h.log · out/claim-tap-h.jsonl
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-h.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-h.json"; WORK="$OUT/work-h"; DLOG="$OUT/daemon-h.log"
TAP="$OUT/claim-tap-h.jsonl"; TAP_PORT="${TAP_PORT:-8092}"
MODEL="${LEAD_MODEL}"                                  # claude_code 쪽 모델(haiku)
HERMES_MODEL="${HERMES_MODEL:-claude-haiku-4-5-20251001}"   # 접두어 없이 — 데몬이 anthropic: 을 붙인다
BAD_MODEL="${BAD_MODEL:-claude-haiku-4-5-TYPO}"        # 오타 → 재시도 가능한 실패
RES="$OUT/hermes.json"
g5_chk_init "$OUT/h-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$OUT/daemon-h.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-h.pid")" 2>/dev/null || true; }
  return 0
}
trap cleanup EXIT

step "0. claim 탭 — 서버가 데몬에 보내는 TaskBundle 을 기록한다"
rm -f "$TAP" "$OUT/claim-tap-access.tsv"; : > "$TAP"; : > "$DLOG"
python3 "$P2_DIR/fixtures/claimtap.py" "$TAP_PORT" "$SERVER_URL" "$TAP" & TAP_PID=$!
for i in $(seq 1 20); do curl -fsS -o /dev/null "http://localhost:$TAP_PORT/healthz" 2>/dev/null && break; sleep 0.3; done
ok "tap :$TAP_PORT → $SERVER_URL (pid $TAP_PID)"

step "1. 가입 · 워크스페이스 (ASCII 이름)"
EMAIL="g5h+$STAMP@example.com"
signup "$EMAIL" "password123" "Director" >/dev/null
WS="$(create_workspace "G5 Hermes $STAMP")"
ok "workspace $WS"

step "2. 페어링 → daemon pair (PONG 턴 O — 능력 광고를 실측으로 채운다) · run"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -f "$CFG"; mkdir -p "$WORK"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "http://localhost:$TAP_PORT" --workdir-root "$WORK" 2>&1 | tail -3 >&2
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-h.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "runtime $RUNTIME ready"

step "A. probe — hermes 능력 광고 (harness §9)"
RT_JSON="$(api_ok GET "/runtimes/$RUNTIME")"; echo "$RT_JSON" | jq -c '.capabilities' > "$OUT/h-caps.json"
jq -c '.capabilities[]?' <<<"$RT_JSON" >&2
HC="$(jq -c '.capabilities[]?|select(.kind=="hermes")' <<<"$RT_JSON")"
chk H1  "probe 에 hermes 능력 행이 있다"                     yes "$( [ -n "$HC" ] && echo yes || echo no )"
if [ -n "$HC" ]; then
  chk H1b "hermes logged_in=true"                            true "$(jq -r '.logged_in' <<<"$HC")"
  chk H1c "hermes brief_transport=instruction_file (§1)"     instruction_file "$(jq -r '.brief_transport' <<<"$HC")"
  chk H1d "hermes resume=true (session/load, §6)"            true "$(jq -r '.resume' <<<"$HC")"
  chk H1e "hermes usage=true (G1 F6)"                        true "$(jq -r '.usage' <<<"$HC")"
  chk H1f "hermes tool_disallow=false (claude 와 갈린다)"    false "$(jq -r '.tool_disallow' <<<"$HC")"
  chk H1g "hermes supported_options 는 v1 에서 비어 있다"    0 "$(jq -r '(.supported_options//{})|length' <<<"$HC")"
  chk H1h "hermes 버전 ≥ 0.20.6 (§1 어댑터 고정)"            yes \
    "$(python3 -c "import sys;v=sys.argv[1].split('.');print('yes' if [int(x) for x in v[:3]]>=[0,20,6] else 'no')" "$(jq -r '.version' <<<"$HC" | tr -d 'v')" 2>/dev/null || echo no)"
  chk H1i "hermes protocol_version=1"                        1 "$(jq -r '.protocol_version' <<<"$HC")"
  # §10 v0.8 — 도구 표면. G5 1차에서 이 칸이 비어 있어 probe 11/11 초록인데 에이전트가
  # 플랫폼에 한 마디도 못 했다(D-7). 판정은 실측이다: initialize 에 mcpCapabilities 가
  # 있으면 mcp, 없으면 cli_wrapper.
  chk H1k "hermes tool_surface=cli_wrapper (§10 v0.8, MCP 미존중)" cli_wrapper "$(jq -r '.tool_surface // "none"' <<<"$HC")"
fi
CC="$(jq -c '.capabilities[]?|select(.kind=="claude_code")' <<<"$RT_JSON")"
chk H1l "claude_code tool_surface=mcp (두 런타임이 갈린다)"   mcp "$(jq -r '.tool_surface // "none"' <<<"${CC:-{\}}")"
chk H1j "colab_cli 는 런타임이 아니라 probe 최상위 (§3)"     true "$(jq -r '.colab_cli.present // "null"' <<<"$RT_JSON")"

step "B. 시나리오 A — Researcher = hermes, Lead·Writer = claude_code"
source "$P2_DIR/fixtures/scenario_a_agents.sh"
LEAD="$(create_agent_p2   "$WS" Lead       lead       "$MODEL" "$LEAD_INS"   '팀을 이끌고 위임·종합한다')"
RSCH="$(create_agent_kind "$WS" Researcher researcher hermes "$HERMES_MODEL" "$RES_INS" '주어진 항목을 조사해 요약한다')"
WRTR="$(create_agent_p2   "$WS" Writer     writer     "$MODEL" "$WRITER_INS" '보고서 초안을 쓰고 아티팩트로 제출한다')"
chk H2 "Researcher 프로파일이 hermes 로 저장됐다" hermes "$(psqlq "select runtime_kind from agent_profile where agent_id='$RSCH'")"
chk H2b "hermes 프로파일 model 은 접두어 없이 저장된다 (§1)" "$HERMES_MODEL" "$(psqlq "select model from agent_profile where agent_id='$RSCH'")"

SESSION="$(create_session_p2 "$WS" "제품 X 시장 조사 (Hermes)" "$SCENARIO_GOAL" "$LEAD" "$RUNTIME" "$WRTR" "$LEAD" "$RSCH" "$WRTR")"
echo "$WS $SESSION $LEAD $RSCH $WRTR $RUNTIME" > "$OUT/h-ids.txt"
ok "session $SESSION"
T_START="$(now_ms)"

step "B1. 진행 대기 — 아티팩트 제출 + Lead 3턴 종료까지"
DEADLINE=$(( $(date +%s) + ${SCENARIO_TIMEOUT_S:-1500} ))
ART_ID=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  ART_ID="$(psqlq "select id from artifact where session_id='$SESSION' order by created_at desc limit 1")"
  LEADN="$(count_tasks_of_agent "$SESSION" Lead)"
  ACT="$(psqlq "select count(*) from task where session_id='$SESSION' and status in ('queued','dispatched','running')")"
  if [ -n "$ART_ID" ] && [ "$LEADN" -ge 3 ] && [ "$ACT" = 0 ]; then break; fi
  sleep 5
done
T_END="$(now_ms)"; ELAPSED=$(( (T_END-T_START)/1000 )); log "총 소요 ${ELAPSED}s"

step "B2. 판정 — 8단계 중 위임~제출 (승인·completed 는 33_ 이 잇는다)"
echo "── lane 보드 ──" >&2; lanes_of "$SESSION" | column -t -s $'\t' >&2

# Lead 기상 횟수 — **K-5(PRD v0.15 규칙 8, EVAL E1-22) 로 판정한다.**
# 기본은 3(시작 1 + 합류 2)이다. 그런데 자식이 `status set done` **뒤에** 한 줄을 더 올리면
# 그 시점에는 그 자식의 합류 그룹이 이미 발화한 뒤라 규칙 8 억제가 풀려 있고, 멘션은 **일반
# 라우팅**으로 위임자를 깨운다. 그것이 결함이 아니라는 것이 K-5 결정이고, 위임자가 자식 발언
# 한 줄마다 깨어나지 않게 막는 것은 FR-3.4 의 lane 단위 병합이다. 그래서 두 가지를 잰다:
#   (1) 총 기상 = 3 + 합류 뒤 자식 멘션이 만든 task 수
#   (2) 그 task 수가 합류 뒤 자식 멘션 수를 넘지 않는다(병합이 실제로 묶는다)
# 자식이 어떤 순서로 도구를 부르는지는 런타임이 정하므로 (1) 의 우변은 실측이지 상수가 아니다.
POSTJOIN_MENTIONS="$(psqlq "select count(*) from message m
  join agent a on a.id=m.author_id
  join task st on st.id=m.source_task_id
  join lane l on l.id=st.lane_id
  join task dt on dt.id=l.delegated_from_task_id
  where m.session_id='$SESSION' and a.name<>'Lead' and m.mentions::text like '%$LEAD%'
    and dt.join_fired_at is not null and m.created_at > dt.join_fired_at")"
POSTJOIN_TASKS="$(psqlq "select count(*) from task t
  join message m on m.id=t.trigger_message_id
  join agent a on a.id=m.author_id
  join task st on st.id=m.source_task_id
  join lane l on l.id=st.lane_id
  join task dt on dt.id=l.delegated_from_task_id
  where t.session_id='$SESSION' and t.agent_id='$LEAD' and a.name<>'Lead'
    and dt.join_fired_at is not null and m.created_at > dt.join_fired_at")"
LEAD_TASKS="$(count_tasks_of_agent "$SESSION" Lead)"
log "K-5: 합류 뒤 자식 멘션 $POSTJOIN_MENTIONS 건 → Lead task $POSTJOIN_TASKS 개 (총 기상 $LEAD_TASKS)"
chk H3  "Lead 기상 = 3(시작1+합류2) + 합류 뒤 자식 멘션분 (K-5·E1-22)" \
  "$((3 + POSTJOIN_TASKS))" "$LEAD_TASKS"
chk H3b "합류 뒤 멘션이 자식 발언 수만큼 깨우지 않는다 (FR-3.4 병합)" yes \
  "$( [ "${POSTJOIN_TASKS:-0}" -le "${POSTJOIN_MENTIONS:-0}" ] && echo yes || echo no )"
chk H4  "Researcher lane 3개 (위임 3 = lane 3)"       3 "$(psqlq "select count(*) from lane l join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher'")"
OVERLAP="$(running_overlap "$SESSION" Researcher)"
chk H5  "hermes lane 3개가 동시에 running (FR-6.3)"   3 "$OVERLAP"
J_ROWS="$(join_fired "$SESSION" | wc -l | tr -d ' ')"
chk H6  "합류 그룹 발화 2건 (J1=Researcher 3, J2=Writer 1)" 2 "$J_ROWS"
chk H6b "합류 시스템 메시지 = 그룹 수 (정확히 1회씩)" "$J_ROWS" \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and author_type='system' and content like '위임한 작업이 모두 끝났습니다%'")"
R_DONE="$(psqlq "select count(*) from lane l join agent a on a.id=l.agent_id where l.session_id='$SESSION' and a.name='Researcher' and l.status='done'")"
chk H7  "hermes lane 3개가 전부 done (턴 종료 판정이 실기에서 선다)" 3 "$R_DONE"
chk H8  "Writer 가 아티팩트를 제출했다"               yes "$( [ -n "$ART_ID" ] && echo yes || echo no )"
CP="$(completion_progress "$SESSION")"; log "completion_progress: $CP"
chk H8b "진행률 met=1 / total=2 (artifact_submitted 충족)" "1/2" "$(jq -r '"\(.met)/\(.total)"' <<<"$CP")"
chk H8c "세션은 아직 active (E6-01 — 승인 전)"        active "$(psqlq "select status from session where id='$SESSION'")"

step "B3. hermes 하네스 규칙이 실기에서 어떻게 보이는가 (harness §1·§6·§8)"
# §1 모델 접두어 — 데몬이 anthropic: 을 붙여 session/set_model 을 부른다
# 데몬 로그는 요약형이라 set_model 호출이 남지 않는다. 계약(§1)의 두 쪽을 대신 대조한다:
# 프로파일은 접두어 **없이** 저장되고(H2b), hermes 가 광고하는 모델 id 는 전부 `provider:model` 이다.
HM_PREFIXED="$(jq -r '[.models[]?|select(test("^[a-z0-9-]+:"))]|length' <<<"$HC" 2>/dev/null || echo 0)"
HM_TOTAL="$(jq -r '(.models//[])|length' <<<"$HC" 2>/dev/null || echo 0)"
chk H9  "hermes 가 광고하는 모델 id 는 전부 provider 접두어를 갖는다 (§1 접두어 규칙의 근거)" \
  "$HM_TOTAL" "$HM_PREFIXED"
chk H9b "그 목록에 프로파일 모델의 anthropic: 형태가 있다" yes \
  "$(jq -e --arg m "anthropic:$HERMES_MODEL" '.models|index($m)!=null' <<<"$HC" >/dev/null 2>&1 && echo yes || echo no)"
# §1 브리프 전달 — instruction_file. **디스크로는 못 잰다**: 데몬이 턴이 끝나면 마커 구간을 지운다
# (loop.go `defer brief.Remove(prep)`). 그래서 서버가 보낸 TaskBundle.brief 를 탭에서 읽는다.
R_TASK1="$(psqlq "select t.id from task t join agent a on a.id=t.agent_id where t.session_id='$SESSION' and a.name='Researcher' order by t.created_at limit 1")"
python3 "$P2_DIR/fixtures/brief_of_task.py" "$TAP" "$R_TASK1" > "$OUT/h-brief.txt" 2>/dev/null || : > "$OUT/h-brief.txt"
BRIEF_TRANSPORT="$(head -1 "$OUT/h-brief.txt" 2>/dev/null || true)"
chk H10  "hermes TaskBundle 의 brief.transport = instruction_file (§1)" instruction_file "$BRIEF_TRANSPORT"
chk_has H10b "브리프 본문이 [1] Agent Identity 로 시작한다" "$OUT/h-brief.txt" "[1] Agent Identity"
chk_has H10c "브리프에 colab 도구 사용 규칙이 있다 ([2])"    "$OUT/h-brief.txt" "colab message post"
# 도구 표면 — hermes 턴이 colab 과 말할 수단을 실제로 가졌는가 (e2e 만 증명할 수 있는 것)
chk H10d "hermes 턴이 세션에 무엇이든 남겼다 (메시지 또는 status)" yes \
  "$( [ "$(psqlq "select count(*) from message m join agent a on a.id=m.author_id where m.session_id='$SESSION' and a.name='Researcher'")" -ge 1 ] && echo yes || echo no )"
chk H10e "hermes 턴에 'colab: command not found' 흔적이 없다 (CLI 표면)" 0 \
  "$(psqlq "select count(*) from task_event te join task t on t.id=te.task_id join agent a on a.id=t.agent_id
            where t.session_id='$SESSION' and a.name='Researcher' and te.payload::text like '%colab: command not found%'")"

# §10 cli_wrapper 실증 — 리뷰어 요구: 래퍼가 실제로 **불렸는가**.
# 데몬 로그는 정상 경로에서 래퍼 경로를 찍지 않으므로(오류·불일치 때만), 증거는 에이전트가
# 남긴 도구 호출이다. 래퍼 절대 경로가 그 안에 그대로 있어야 한다.
WRAP_RE="$WORK/.colab/bin/"
# payload 안에서 래퍼 절대 경로가 나온 자리를 그대로 뽑는다(title 에는 없고 본문에 있다).
psqlq "select distinct substring(te.payload::text from '/[^\\\\\"[:space:]]*[.]colab/bin/[^\\\\\"[:space:]]*')
       from task_event te join task t on t.id=te.task_id join agent a on a.id=t.agent_id
       where t.session_id='$SESSION' and a.name='Researcher' and te.payload::text like '%.colab/bin/%'" \
  > "$OUT/h-wrapper-calls.txt"
grep -iE 'tool_surface|tool wrapper' "$DLOG" > "$OUT/h-toolsurface.txt" 2>/dev/null || : > "$OUT/h-toolsurface.txt"
chk_ge H14 "hermes 턴이 래퍼 절대 경로로 colab 을 불렀다 (§10 cli_wrapper)" 1 \
  "$(wc -l < "$OUT/h-wrapper-calls.txt" | tr -d ' ')"
chk_has H14b "그 경로가 <workdir_root>/.colab/bin/ 아래다" "$OUT/h-wrapper-calls.txt" "$WRAP_RE"
# 래퍼는 attempt 토큰을 담으므로 finish 에서 지워져야 한다(§10).
chk H14c "턴이 끝난 뒤 래퍼 디렉토리가 남지 않았다 (토큰 정리)" 0 \
  "$(find "$WORK/.colab/bin" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')"
# §6 재개 — runtime_session_ref 가 hermes 모양인가
REF="$(lane_ref "$SESSION" Researcher)"; echo "$REF" > "$OUT/h-lane-ref.json"
chk H11  "Researcher lane 의 runtime_session_ref.runtime_kind = hermes" hermes "$(jq -r '.runtime_kind // "none"' <<<"$REF")"
chk H11b "runtime_session_ref 에 session_id 가 있다 (§6)"  yes "$(jq -r '(.session_id|length>0)|if . then "yes" else "no" end' <<<"$REF" 2>/dev/null || echo no)"
chk H11c "runtime_session_ref 에 provenance 가 있다 (§6 유실 감지)" yes \
  "$(jq -r 'if (.provenance|type)=="object" then "yes" else "no" end' <<<"$REF" 2>/dev/null || echo no)"
# §8 본문 오류 접두어 — 실기에서 오탐이 없어야 한다
chk H12  "본문 오류 접두어 오탐 0 (§8: ^API call failed after N retries:)" 0 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and content ~ '^API call failed after [0-9]+ retries: '")"
chk H12b "Researcher task 에 실패한 attempt 0 (실기 hermes 턴이 성공)" 0 \
  "$(psqlq "select count(*) from task_attempt ta join task t on t.id=ta.task_id join agent a on a.id=t.agent_id
            where t.session_id='$SESSION' and a.name='Researcher' and ta.outcome<>'completed'")"
# §7/§2.2 늦은 청크 — hermes 응답이 잘리지 않았는가(자식 메시지 3개가 온전히 실렸다)
chk H13  "hermes 자식 메시지 3개 (250ms 정적 대기가 청크를 잃지 않았다, E12-04)" 3 \
  "$(psqlq "select count(*) from message m join agent a on a.id=m.author_id where m.session_id='$SESSION' and a.name='Researcher' and m.kind='text'")"

step "C. E8-08 폴백 — hermes 프로파일이 실패하면 서버가 claude_code 로 재큐잉하는가"
FB_INS='You are a helper. Your only job: call colab_status_set with status "done" immediately, then end your turn. Do not post any message.'
FBA="$(create_agent_2profiles "$WS" Faller custom "$FB_INS" hermes "$BAD_MODEL" '[]' claude_code "$MODEL")"
link_fallback "$FBA" primary spare      # ← 우회(S-24): 생성 API 가 fallback_profile 을 버린다
P_PRIMARY="$(profile_of "$FBA" primary)"; P_SPARE="$(profile_of "$FBA" spare)"
chk C0 "폴백 연결이 DB 에 섰다 (정식 경로 부재 — S-24)" "$P_SPARE" \
  "$(psqlq "select coalesce(fallback_profile_id::text,'-') from agent_profile where id='$P_PRIMARY'")"
FB_SESSION="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "폴백 전환" --arg g "폴백 확인용 세션" --arg a "$FBA" --arg rt "$RUNTIME" \
  '{title:$t,goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
FB_TASK="$(session_initial_task "$FB_SESSION")"
ok "fallback session $FB_SESSION task $FB_TASK"
FB_WD_BEFORE=""; FB_DEADLINE=$(( $(date +%s) + 420 ))
while [ "$(date +%s)" -lt "$FB_DEADLINE" ]; do
  ST="$(task_status "$FB_TASK")"; AT="$(task_attempt "$FB_TASK")"
  [ -z "$FB_WD_BEFORE" ] && FB_WD_BEFORE="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id=(select lane_id from task where id='$FB_TASK')")"
  [ "$ST" = completed ] && break
  [ "$ST" = failed ] && break
  sleep 4
done
echo "── task_attempt ──" >&2; task_attempts "$FB_TASK" | column -t -s $'\t' >&2
A1_KIND="$(psqlq "select coalesce(failure_kind::text,'-') from task_attempt where task_id='$FB_TASK' and attempt=1")"
chk C1  "attempt 1 이 실패했다 (hermes 모델 오타)"        yes "$( [ "$A1_KIND" != '-' ] && [ -n "$A1_KIND" ] && echo yes || echo no )"
A1_RETRYABLE=no
case "$A1_KIND" in other|network|stall|timeout) A1_RETRYABLE=yes;; esac
chk C1b "그 failure_kind 가 재시도 가능하다 (§8) — 관측 kind=$A1_KIND" yes "$A1_RETRYABLE"
chk C2  "attempt 가 2 이상으로 올랐다 (E8-08 재큐잉)"      yes "$( [ "$(task_attempt "$FB_TASK")" -ge 2 ] 2>/dev/null && echo yes || echo no )"
CUR_PROF="$(psqlq "select profile_id from task where id='$FB_TASK'")"
chk C3  "task 프로파일이 spare(claude_code) 로 바뀌었다"   "$P_SPARE" "$CUR_PROF"
chk C3b "lane 프로파일도 같이 바뀌었다"                    "$P_SPARE" "$(psqlq "select profile_id from lane where id=(select lane_id from task where id='$FB_TASK')")"
FB_WD_AFTER="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id=(select lane_id from task where id='$FB_TASK')")"
chk C4  "workdir 를 그대로 재사용한다 (§4.4 workdir.reuse)" "$FB_WD_BEFORE" "$FB_WD_AFTER"
chk C4b "workdir 행이 하나뿐이다 (새로 만들지 않았다)"      1 "$(psqlq "select count(*) from workdir where session_id='$FB_SESSION'")"
chk C5  "runtime_kind 가 바뀌었으니 최종 lane 은 claude_code 로 돈다" claude_code \
  "$(psqlq "select p.runtime_kind from agent_profile p where p.id='$CUR_PROF'")"
FB_REF="$(psqlq "select coalesce(l.runtime_session_ref::text,'null') from lane l where l.id=(select lane_id from task where id='$FB_TASK')")"
chk C5b "폴백 뒤 runtime_session_ref 는 새 런타임 것이다 (resume 비움 → 콜드 스타트)" claude_code \
  "$(jq -r '.runtime_kind // "none"' <<<"$FB_REF")"
chk C6  "폴백 뒤 task 가 완료됐다 (전환이 실제로 일을 끝낸다)" completed "$(task_status "$FB_TASK")"
chk C6b "세션은 같은 머신에 남았다 (E8-09 — 다른 머신으로 넘기지 않는다)" "$RUNTIME" \
  "$(psqlq "select runtime_id from session where id='$FB_SESSION'")"

step "D. E8-09 — 대체 프로파일이 없으면 queued 유지 + Director 알림"
NFA="$(create_agent_kind "$WS" Lonely custom hermes "$BAD_MODEL" "$FB_INS" '대안 없는 프로파일')"
NF_SESSION="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "대안 없음" --arg g "E8-09 확인용 세션" --arg a "$NFA" --arg rt "$RUNTIME" \
  '{title:$t,goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
NF_TASK="$(session_initial_task "$NF_SESSION")"
NF_DEADLINE=$(( $(date +%s) + 300 ))
while [ "$(date +%s)" -lt "$NF_DEADLINE" ]; do
  INB="$(psqlq "select count(*) from inbox_item where ref_id='$NF_TASK' and type='run_failed'")"
  [ "${INB:-0}" -ge 1 ] && break
  sleep 3
done
echo "── task_attempt (대안 없음) ──" >&2; task_attempts "$NF_TASK" | column -t -s $'\t' >&2
chk D1  "Director 인박스에 run_failed 알림 1건 (E8-09)"  1 "$(psqlq "select count(*) from inbox_item where ref_id='$NF_TASK' and type='run_failed'")"
chk D1b "알림은 task 당 1건 (재시도 3회여도 한 번만)"     1 "$(psqlq "select count(*) from inbox_item where ref_id='$NF_TASK' and type='run_failed'")"
# E8-09 의 "queued 대기" 는 **재시도 사이의 상태**라 폴링으로 잡으면 놓친다(1차 실행 실측).
# 재큐잉이 실제로 있었다는 것은 attempt 가 둘 이상 기록된 것으로 센다 — 서버는 재시도 가능한
# 실패를 `queued` 로 되돌려야만 다음 attempt 를 만들 수 있다.
chk D2  "대안이 없어도 재큐잉했다 (attempt 2건 이상 기록)"  yes \
  "$( [ "$(psqlq "select count(*) from task_attempt where task_id='$NF_TASK'")" -ge 2 ] && echo yes || echo no )"
chk D2b "프로파일은 바뀌지 않았다 (전환할 대안이 없다)"     1 \
  "$(psqlq "select count(distinct profile_id) from task where id='$NF_TASK'")"
chk D3  "다른 머신으로 넘기지 않았다 (runtime 고정)"      1 \
  "$(psqlq "select count(distinct runtime_id) from task_attempt where task_id='$NF_TASK' and runtime_id is not null")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg runtime "$RUNTIME" --arg artifact "${ART_ID:-}" \
  --arg fb_session "$FB_SESSION" --arg fb_task "$FB_TASK" --arg a1_kind "${A1_KIND:-}" \
  --argjson overlap "${OVERLAP:-0}" --argjson joins "${J_ROWS:-0}" --argjson elapsed_s "${ELAPSED:-0}" \
  --argjson pass "$pass" --argjson fail "$fail" --argjson caps "$(cat "$OUT/h-caps.json")" \
  '{workspace:$ws,session:$session,runtime:$runtime,artifact:$artifact,
    max_concurrent_hermes_lanes:$overlap,join_groups_fired:$joins,elapsed_s:$elapsed_s,
    fallback:{session:$fb_session,task:$fb_task,attempt1_failure_kind:$a1_kind},
    capabilities:$caps,pass:$pass,fail:$fail}' | tee "$RES"
[ "$fail" = 0 ]
