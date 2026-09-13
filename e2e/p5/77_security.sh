#!/usr/bin/env bash
# e2e/p5/77_security.sh — **보안 점검 (PRD §9 보안 행)**. 페이크 런타임(모델 0회). 실패는 **번호 없이** 보고(§0-11).
#
#   S1 originator 기준 권한 — 에이전트 체인으로 상승 없음:
#      멤버가 건 task 의 originator = 멤버 · 그 task 가 위임한 자식 task 의 originator 도 멤버(체인 보존).
#      task 토큰으로 Director 전용 op(complete·pause·cancelLane·respondHitl) → 401/403. 멤버 쿠키로 cancelLane → 403(E10-05).
#      멤버가 owner 전용(respond_to=owner) 에이전트를 세션에 초대 → 403, Director 는 2xx (FR-1.9 originator 사다리).
#   S2 토큰 범위 — 다른 세션의 op 403 · 다른 task 의 setTaskStatus 403 · finish 뒤 401 · revoke(취소) 뒤 401 (E11-04).
#   S3 task_event 마스킹 — 설정을 켜면 셸 출력·diff 본문이 요약([마스킹됨 · N자])만 저장, 끄면 본문 저장.
#   S4 데몬 토큰으로 사람 op 불가 — getSession·completeSession·postMessage 401/403.
#   S5 SSE 가 남의 워크스페이스 이벤트를 안 흘린다 — B 워크스페이스 스트림에 A 세션 id 0건. 멤버 아닌 사용자의 A 스트림 403.
#
# 산출물: out/77-checks.tsv · out/77.json · out/77-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
DIR_COOKIE="$OUT/cookies-77-dir.txt"; MEM_COOKIE="$OUT/cookies-77-mem.txt"; OUT_COOKIE="$OUT/cookies-77-out.txt"
rm -f "$DIR_COOKIE" "$MEM_COOKIE" "$OUT_COOKIE"
COOKIE="$DIR_COOKIE"
CFG="$OUT/daemon-77.json"; WORK="$P5_TMP_ROOT/77/work"; DLOG="$OUT/daemon-77.log"
MODEL="${LEAD_MODEL}"
g5_chk_init "$OUT/77-checks.tsv"
cleanup() { daemon_stop "$OUT/daemon-77.pid"; [ -n "${SSE_PID:-}" ] && kill "$SSE_PID" 2>/dev/null; return 0; }
trap cleanup EXIT
PROBE_INS='너는 Probe 다. 지시를 받으면 colab_message_post 로 "ok" 를 게시하고 status done.'
# tok_call TOKEN METHOD PATH [JSON] → HTTP 코드
tok_call() { local t="$1" m="$2" p="$3" b="${4:-}"
  if [ -n "$b" ]; then curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $t" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuid)" -X "$m" "$API$p" --data "$b"
  else curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $t" -H "Idempotency-Key: $(uuid)" -X "$m" "$API$p"; fi; }
tok_body() { local t="$1" m="$2" p="$3"; curl -sS -H "Authorization: Bearer $t" -X "$m" "$API$p"; }
code_in() { in_set "$1" "${@:2}" | sed 's/^[^y].*/no/'; }   # yes | no
# probe_token TASK → 대본이 남긴 그 task 의 토큰 (fixtures/agent.sh Probe: FAKE_CMD 가 기록한다)
probe_token() { awk -F'\t' -v t="$1" '$1==t{print $3}' "$E2E_OUT/fake-records/probe-results.tsv" | tail -1; }
# 프로파일 env: 대본이 자기 토큰·환경을 기록한다(테스트 픽스처 — out/ 는 gitignore).
PROBE_CMD='printf "%s" "$COLAB_TASK_TOKEN"'

step "1. 사람 셋 — Director(owner) · 멤버(초대) · 외부인(자기 워크스페이스 B) · 페어링"
signup "i5s-dir+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G9 Security A $STAMP")"
TOK="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}' | jq -r .token)"
COOKIE="$MEM_COOKIE"
MEM_ID="$(api_ok POST /auth/signup "$(jq -nc --arg e "i5s-mem+$STAMP@example.com" --arg t "$TOK" '{display_name:"멤버",email:$e,password:"password123",invite_token:$t}')" | jq -r '.user.id // .id')"
COOKIE="$OUT_COOKIE"
OUT_ID="$(signup "i5s-out+$STAMP@example.com" password123 외부인)"
WS_B="$(create_workspace "G9 Security B $STAMP")"
COOKIE="$DIR_COOKIE"
DIR_ID="$(api_ok GET /me | jq -r '.id // .user.id')"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"; : > "$DLOG"; rm -f "$E2E_OUT/fake-records/probe-results.tsv"
daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-77.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME_ID="$(runtime_of_config "$CFG")"
DAEMON_TOKEN="$(jq -r .daemon_token "$CFG")"
ok "dir=$DIR_ID mem=$MEM_ID out=$OUT_ID wsB=$WS_B"

step "2. 에이전트 — Probe(누구나) · Guard(respond_to=owner, 위임 대상) · Slow(취소용 긴 턴)"
PROBE="$(PROFILE_ENV="$(fake_env Probe claude | jq -c --arg c "$PROBE_CMD" '. + {FAKE_CMD:$c}')" create_agent_kind "$WS" Probe researcher claude_code "$MODEL" "$PROBE_INS" '점검용')"
# Guard: Director 소유 · respond_to=owner → 멤버는 초대할 수 없다(FR-1.9). 위임 자식 originator 확인용.
GUARD="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg i "$PROBE_INS" --argjson env "$(fake_env Probe claude)" \
  '{name:"Guard",role:"researcher",role_description:"owner 전용",instructions:$i,respond_to:"owner",
    profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-haiku-4-5-20251001",is_default:true,env:$env}]}')" | jq -r .id)"
SLOW="$(PROFILE_ENV="$(fake_env Slow claude "$(jq -nc --arg fix "$FIX" '{turns:[{steps:[{exec:("bash "+$fix+"/agent.sh Probe")},{sleep_ms:45000},{chunk:"late"}]}]}')" | jq -c --arg c "$PROBE_CMD" '. + {FAKE_CMD:$c, FAKE_NO_DONE:"1"}')" \
  create_agent_kind "$WS" Slow researcher claude_code "$MODEL" "$PROBE_INS" '긴 턴')"
# Probe 의 위임 대상 = Guard: 위임 브리프를 받으면 Guard 의 대본(Probe 역할)이 ok 를 게시한다.
# 깨어날 때마다 다시 위임한다 — 합류(위임 완료 통보)로 깨어나도 또 위임하므로 **위임↔합류 사이클**이 된다.
# 8회로 묶는다(첫 실행에서 무한 반복: 70초에 529회, 세션은 active 그대로 — FR-3.5 루프 상한이 이 경로를 안 본다. 보고).
DELEG_CMD='n=$(cat "$FAKE_OUT/deleg-count" 2>/dev/null || echo 0); if [ "$n" -lt 8 ]; then echo $((n+1)) > "$FAKE_OUT/deleg-count"; colab lane delegate --agent Guard --brief "체인 확인 $n" 2>&1; fi'
rm -f "$E2E_OUT/fake-records/deleg-count"
DELEGATOR="$(PROFILE_ENV="$(fake_env Probe claude | jq -c --arg c "$DELEG_CMD" '. + {FAKE_CMD:$c}')" create_agent_kind "$WS" Delegator lead claude_code "$MODEL" "$PROBE_INS" '위임자')"
ok "Probe=$PROBE Guard=$GUARD Slow=$SLOW Delegator=$DELEGATOR"

step "3. S1 — originator: 멤버가 건 체인은 멤버로 남는다 · task 토큰은 사람 op 를 못 한다"
S="$(create_session_p3 "$WS" "security A" "보안 점검" "$PROBE" "$RUNTIME_ID" '{}' "$PROBE" "$DELEGATOR" "$GUARD" "$SLOW")"
T_INIT="$(session_initial_task "$S")"; WAIT_S=120 wait_task "$T_INIT" completed failed >/dev/null
chk S1a "세션 첫 task 의 originator = Director(생성자)" "$DIR_ID" "$(task_field "$T_INIT" originator_user_id)"
# 멤버가 Delegator 를 멘션 → 그 task originator = 멤버 → Delegator 가 Guard 에 위임 → 자식 originator 도 멤버
COOKIE="$MEM_COOKIE"
R="$(post_message "$S" "$(mention Delegator "$DELEGATOR") 체인을 확인해줘")"
T_MEM="$(jq -r '.triggers[0].task_id // empty' <<<"$R")"
COOKIE="$DIR_COOKIE"
chk S1b "멤버의 멘션이 task 를 만든다 (FR-5.3 누구나 게시)" yes "$( [ -n "$T_MEM" ] && echo yes || echo no )"
WAIT_S=120 wait_task "$T_MEM" completed failed cancelled >/dev/null || true
chk S1c "그 task 의 originator = 멤버" "$MEM_ID" "$(task_field "$T_MEM" originator_user_id)"
T_CHILD="$(psqlq "select id from task where delegated_from_task_id='$T_MEM' order by created_at limit 1")"
chk S1d "위임된 자식 task 가 생겼다 (Delegator → Guard)" yes "$( [ -n "$T_CHILD" ] && echo yes || echo no )"
[ -n "$T_CHILD" ] && chk S1e "**자식 task 의 originator 도 멤버** (체인으로 상승 없음, PRD §9)" "$MEM_ID" "$(task_field "$T_CHILD" originator_user_id)"
WAIT_S=120 wait_task "$T_CHILD" completed failed cancelled >/dev/null 2>&1 || true
wait_until 120 '[ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'$S'"' and a.name='"'Guard'"'")" -ge 8 ]' || true
wait_quiet "$S" 60 || true
GUARD_N="$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Guard'")"
# FR-3.5: 같은 짝의 왕복이 max_pair_roundtrips(5)를 넘으면 세션이 loop 로 일시정지돼야 한다. 위임(delegateLane)과
# 합류 wake 는 postMessage 의 루프 검사를 타지 않아 8회 사이클이 그대로 돈다(첫 실행 529회). 신규 결함 — 보고.
chk_na S1x "위임↔합류 사이클 8회가 루프 상한(FR-3.5 max_pair_roundtrips=5)에 걸리는가 (신규 결함)" \
  "guard_tasks=$GUARD_N session=$(sess_status "$S")" "delegateLane·합류 wake 가 루프 검사 밖 — 세션이 paused(loop) 가 되지 않는다"
# 살아 있는 task 토큰 확보: Slow 를 깨운다(턴 20s) → 대본이 토큰을 기록
R="$(post_message "$S" "$(mention Slow "$SLOW") 천천히")"
T_SLOW="$(jq -r '.triggers[0].task_id // empty' <<<"$R")"
wait_until 60 '[ -n "$(probe_token "'"$T_SLOW"'")" ]' || bad "Slow 토큰이 기록되지 않았다"
TK="$(probe_token "$T_SLOW")"
chk S1f "task 토큰이 기록됐다 (ctk_)" yes "$( [ "${TK:0:4}" = ctk_ ] && echo yes || echo no )"
chk S1g "task 토큰으로 completeSession → 401/403"   yes "$(code_in "$(tok_call "$TK" POST "/sessions/$S/complete" '{"confirm":true}')" 401 403)"
chk S1h "task 토큰으로 pauseSession → 401/403"      yes "$(code_in "$(tok_call "$TK" POST "/sessions/$S/pause" '{}')" 401 403)"
chk S1i "task 토큰으로 cancelLane → 401/403"        yes "$(code_in "$(tok_call "$TK" POST "/lanes/$(task_field "$T_SLOW" lane_id)/cancel")" 401 403)"
chk S1j "task 토큰으로 updateWorkspaceSettings → 401/403" yes "$(code_in "$(tok_call "$TK" PATCH "/workspaces/$WS/settings" '{"task_event_masking":true}')" 401 403)"
chk S1k "task 토큰으로 createSession → 401/403"     yes "$(code_in "$(tok_call "$TK" POST "/workspaces/$WS/sessions" '{"title":"x","goal":"y","participants":[]}')" 401 403)"
COOKIE="$MEM_COOKIE"
chk S1l "멤버 쿠키로 cancelLane → 403 (E10-05)"      403 "$(api POST "/lanes/$(task_field "$T_SLOW" lane_id)/cancel" '' | api_code)"
chk S1m "멤버가 owner 전용 에이전트를 초대 → 403 (FR-1.9 originator)" 403 \
  "$(api POST "/workspaces/$WS/sessions" "$(jq -nc --arg g "$GUARD" --arg rt "$RUNTIME_ID" '{title:"mem",goal:"초대 시도",isolation:{kind:"none"},participants:[{agent_id:$g}],assignee_agent_id:$g,runtime_id:$rt,completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | api_code)"
COOKIE="$DIR_COOKIE"
chk S1n "Director 는 같은 초대가 2xx" yes "$(code_in "$(api POST "/workspaces/$WS/sessions" "$(jq -nc --arg g "$GUARD" --arg rt "$RUNTIME_ID" '{title:"dir",goal:"초대",isolation:{kind:"none"},participants:[{agent_id:$g}],assignee_agent_id:$g,runtime_id:$rt,completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | api_code)" 200 201)"

step "4. S2 — 토큰 범위"
S2="$(create_session_p3 "$WS" "security other" "다른 세션" "$PROBE" "$RUNTIME_ID" '{}' "$PROBE")"
chk S2a "살아 있는 토큰으로 자기 세션 getCliContext 200" 200 "$(tok_call "$TK" GET "/cli/context")"
chk S2b "다른 세션에 postMessage → 403/404"        yes "$(code_in "$(tok_call "$TK" POST "/sessions/$S2/messages" '{"content":"x"}')" 403 404)"
chk S2c "다른 세션 listMessages → 403/404"         yes "$(code_in "$(tok_call "$TK" GET "/sessions/$S2/messages")" 403 404)"
chk S2d "다른 task 의 setTaskStatus → 403"          yes "$(code_in "$(tok_call "$TK" POST "/tasks/$T_INIT/status" '{"status":"done"}')" 403 404)"
chk S2e "다른 세션에 submitArtifact → 403/404"     yes "$(code_in "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $TK" -H "Idempotency-Key: $(uuid)" -F type=doc -F name=x.md -F 'file=@/dev/null;filename=x.md' "$API/sessions/$S2/artifacts")" 403 404 422)"
# 취소 → revoke → 401 token_revoked (E11-04)
CCR="$(api POST "/lanes/$(task_field "$T_SLOW" lane_id)/cancel" '')"; CC="$(api_code <<<"$CCR")"
printf '%s\n' "$CCR" > "$OUT/77-cancel.json"
chk S2f "Director 가 Slow lane 을 취소한다 (202) [task=$(task_field "$T_SLOW" status)]" 202 "$CC"
wait_until 60 '[ "$(task_field "'"$T_SLOW"'" status)" = cancelled ]' || true
chk S2g "**revoke 뒤 401** (E11-04)" 401 "$(tok_call "$TK" GET "/cli/context")"
chk S2h "revoke 뒤 postMessage 도 401" 401 "$(tok_call "$TK" POST "/sessions/$S/messages" '{"content":"late"}')"
# finish 뒤 401: 정상 종료한 Probe task 의 토큰
R="$(post_message "$S" "$(mention Probe "$PROBE") 한 번 더")"
T_P2="$(jq -r '.triggers[0].task_id // empty' <<<"$R")"
WAIT_S=120 wait_task "$T_P2" completed failed cancelled >/dev/null || true
wait_until 30 '[ -n "$(probe_token "'"$T_P2"'")" ]' || true
TK2="$(probe_token "$T_P2")"
chk S2i "**finish 뒤 401** (attempt 전용 토큰)" 401 "$(tok_call "$TK2" GET "/cli/context")"

step "5. S3 — task_event 마스킹 (owner 만 켠다 · 켜면 요약만 저장)"
COOKIE="$MEM_COOKIE"
chk S3a "멤버가 마스킹 설정 → 403" 403 "$(api PATCH "/workspaces/$WS/settings" '{"task_event_masking":true}' | api_code)"
COOKIE="$DIR_COOKIE"
chk S3b "owner 가 마스킹 설정 → 2xx" yes "$(code_in "$(api PATCH "/workspaces/$WS/settings" '{"task_event_masking":true}' | api_code)" 200 204)"
chk S3c "설정이 저장됐다" true "$(api_ok GET "/workspaces/$WS/settings" | jq -r .task_event_masking)"
EDIT_TURN="$(jq -nc --arg fix "$FIX" '{turns:[{steps:[{tool_call:{id:"e1",title:"edit secret.txt",kind:"edit",path:"secret.txt"}},
   {tool_update:{id:"e1",status:"completed",path:"secret.txt",old_text:"",new_text:"SECRET-DIFF-BODY-7731"}},
   {exec:"echo SECRET-SHELL-OUTPUT-8842"},{exec:("bash "+$fix+"/agent.sh Probe")}]}]}')"
MASKED="$(create_agent_fake "$WS" Masked researcher claude_code "$MODEL" "$PROBE_INS" '마스킹 확인' "$EDIT_TURN")"
SM="$(create_session_p3 "$WS" "masking on" "마스킹" "$MASKED" "$RUNTIME_ID" '{}' "$MASKED")"
TM="$(session_initial_task "$SM")"; WAIT_S=120 wait_task "$TM" completed failed >/dev/null
psqlq "select class||'/'||coalesce(verb,'-')||' '||coalesce(payload::text,'') from task_event where task_id='$TM' order by seq" > "$OUT/77-events-masked.txt"
# summary(출력)·command(인자) 는 가려진다. **title 은 가려지지 않는다** — 어댑터가 tool_call.title 에 셸 명령
# 전체를 싣는 모양(페이크도 같은 모양)이라 본문이 title 로 새어 저장된다. 신규 결함으로 보고(번호는 Lead).
chk S3d "마스킹 ON: 셸 출력(summary)·명령 인자(command) 본문 없음" 0 \
  "$(psqlq "select count(*) from task_event where task_id='$TM' and (payload->>'summary' like '%SECRET-SHELL%' or payload->>'command' like '%SECRET-SHELL%')")"
chk_na S3d2 "마스킹 ON: title 에도 본문 없음 (신규 결함 — title 미마스킹)" \
  "$(psqlq "select count(*) from task_event where task_id='$TM' and payload->>'title' like '%SECRET-SHELL%'")" "payload.title 에 셸 명령 전체가 남는다 — 보고"
chk S3e "마스킹 ON: diff 본문이 저장되지 않았다"   0 "$(cnt "$OUT/77-events-masked.txt" 'SECRET-DIFF-BODY-7731')"
chk S3f "마스킹 ON: 요약([마스킹됨 · N자]) 이 남았다" yes "$( [ "$(cnt "$OUT/77-events-masked.txt" '마스킹됨')" -ge 1 ] && echo yes || echo no )"
chk S3g "마스킹 ON: 카드 모양(파일 경로·명령 첫 토큰)은 남는다" yes "$( grep -q 'secret.txt' "$OUT/77-events-masked.txt" && grep -q 'echo' "$OUT/77-events-masked.txt" && echo yes || echo no )"
chk S3h "listTaskEvents 응답의 summary·command·diff 에 본문 없음" 0 \
  "$(api_ok GET "/tasks/$TM/events" | jq -r '.items[]? // .[]? | .payload | (.summary // ""), (.command // ""), (.diff // ""), (.new_text // "")' 2>/dev/null | cnt /dev/stdin 'SECRET-SHELL-OUTPUT-8842' 'SECRET-DIFF-BODY-7731')"
api_ok PATCH "/workspaces/$WS/settings" '{"task_event_masking":false}' >/dev/null
SM2="$(create_session_p3 "$WS" "masking off" "마스킹 해제" "$MASKED" "$RUNTIME_ID" '{}' "$MASKED")"
TM2="$(session_initial_task "$SM2")"; WAIT_S=120 wait_task "$TM2" completed failed >/dev/null
psqlq "select coalesce(payload::text,'') from task_event where task_id='$TM2'" > "$OUT/77-events-plain.txt"
chk S3i "마스킹 OFF: 셸 출력 본문이 저장된다 (대조군)" yes "$( [ "$(cnt "$OUT/77-events-plain.txt" 'SECRET-SHELL-OUTPUT-8842')" -ge 1 ] && echo yes || echo no )"

step "6. S4 — 데몬 토큰으로 사람 op 불가"
chk S4a "데몬 토큰 getSession → 401/403"     yes "$(code_in "$(tok_call "$DAEMON_TOKEN" GET "/sessions/$S")" 401 403)"
chk S4b "데몬 토큰 completeSession → 401/403" yes "$(code_in "$(tok_call "$DAEMON_TOKEN" POST "/sessions/$S/complete" '{"confirm":true}')" 401 403)"
chk S4c "데몬 토큰 postMessage → 401/403"    yes "$(code_in "$(tok_call "$DAEMON_TOKEN" POST "/sessions/$S/messages" '{"content":"x"}')" 401 403)"
chk S4d "데몬 토큰 listInbox → 401/403"      yes "$(code_in "$(tok_call "$DAEMON_TOKEN" GET "/inbox")" 401 403)"
chk S4e "데몬 토큰 createAgent → 401/403"    yes "$(code_in "$(tok_call "$DAEMON_TOKEN" POST "/workspaces/$WS/agents" '{"name":"x","role":"lead","instructions":"y","profiles":[]}')" 401 403)"
chk S4f "데몬 토큰으로 자기 런타임 §4.1 claim 은 된다 (대조군)" yes "$(code_in "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $DAEMON_TOKEN" -H 'Content-Type: application/json' -X POST "$SERVER_URL/v1/daemon/runtimes/$RUNTIME_ID/claim" --data '{"capacity":0,"wait_ms":0}')" 200)"

step "7. S5 — SSE 격리"
COOKIE="$OUT_COOKIE"
chk S5a "멤버 아닌 사용자의 A 스트림 → 403/404" yes "$(code_in "$(curl -sS -o /dev/null -w '%{http_code}' -b "$OUT_COOKIE" -m 3 "$API/workspaces/$WS/stream" || true)" 403 404)"
: > "$OUT/77-sse-b.txt"
curl -sS -N -b "$OUT_COOKIE" -m 20 "$API/workspaces/$WS_B/stream" > "$OUT/77-sse-b.txt" 2>/dev/null &
SSE_PID=$!
: > "$OUT/77-sse-a.txt"
curl -sS -N -b "$DIR_COOKIE" -m 20 "$API/workspaces/$WS/stream" > "$OUT/77-sse-a.txt" 2>/dev/null &
SSE_A_PID=$!
sleep 1
COOKIE="$DIR_COOKIE"
R="$(post_message "$S" "$(mention Probe "$PROBE") SSE 자극 $STAMP")"
T_SSE="$(jq -r '.triggers[0].task_id // empty' <<<"$R")"
WAIT_S=60 wait_task "$T_SSE" completed failed cancelled >/dev/null || true
sleep 2; kill "$SSE_PID" "$SSE_A_PID" 2>/dev/null; wait "$SSE_PID" "$SSE_A_PID" 2>/dev/null || true; SSE_PID=""
chk S5b "A 스트림(Director)에는 그 세션 이벤트가 흐른다 (대조군)" yes "$( [ "$(cnt "$OUT/77-sse-a.txt" "$S")" -ge 1 ] && echo yes || echo no )"
chk S5c "**B 스트림에 A 세션 id 0건**" 0 "$(cnt "$OUT/77-sse-b.txt" "$S" "$T_SSE")"
chk S5d "B 스트림에 A 워크스페이스 id 0건" 0 "$(cnt "$OUT/77-sse-b.txt" "$WS")"
chk S5e "B 스트림에 A 의 메시지 본문 0건" 0 "$(cnt "$OUT/77-sse-b.txt" "SSE 자극 $STAMP")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg s "$S" --arg mode "$RUNTIME" --argjson pass "$pass" --argjson fail "$fail" \
  '{runtime_mode:$mode,workspace:$ws,session:$s,pass:$pass,fail:$fail}' | tee "$OUT/77.json"
[ "$fail" = 0 ]
