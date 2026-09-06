#!/usr/bin/env bash
# e2e/p3/50_budget_pause_override.sh — T-I3 (c): **예산** (E9-01 · E9-02 · E9-03 · E9-05 · E9-08).
#
#   E9-01  턴 중 누적이 task 상한($1)을 넘으면 → §8.2.2 취소 **명령** → task `paused(budget)`(failed 아님),
#          lane `paused`, Director 에게 시스템 HITL(`source: system` · `purpose: budget` · **`task_id` 채움**)
#   E9-02  Director 가 **웹에서** $3 으로 상향 승인 → `task.budget_override`=3, 에이전트 `budget_per_task`
#          **여전히 $1**, 같은 lane·같은 workdir 로 재개(새 트리거 불필요)
#   E9-08  재개 뒤 누적 $1.50 에서 **취소 없음** — override 를 저장만 하고 강제 시점에 읽지 않으면
#          재개 즉시 다시 paused 가 된다
#   E9-03  거절은 `failed` 가 아니라 `paused(budget)` **유지**
#   E9-05  추정치(`estimated: true`)는 **하드 컷 없음** — 세션 `paused` + 드레인, 진행 중 턴은 산다
#
# ── 차단 결함과 대역 (보고서 §결함) ─────────────────────────────────────────
# 실측: 상한 $0.002 · 실제 턴 비용 $0.0599 인데 **아무 일도 일어나지 않았다**(task completed · 세션 active ·
# HITL 0 · cancel 명령 0). 원인 둘 —
#   (i) 데몬 `acp.Runner.recordUsage` 가 `session/prompt` **응답에서만** 호출되어 턴 중 heartbeat 의
#       `usage` 가 언제나 0 이고, 서버 `daemonHeartbeat` 의 가드가 거짓이라 `enforceBudgetFor` 미호출
#   (ii) `enforceBudgetFor` 는 heartbeat 한 곳에서만 호출된다 — `tasks.Finish` 에서 호출되지 않아
#        사후 강제도 없다(budget.go 주석은 호출한다고 적혀 있다)
# 그래서 **데몬 대역 픽스처**(`fixtures/daemon_heartbeat.sh`)로 daemon-protocol §4.2 와이어 그대로
# usage 를 실어 보낸다. 서버가 하는 일은 실제 데몬이 보냈을 때와 같다 — 재는 것은 서버의 강제 경로이고,
# 대역으로 채운 것은 "데몬이 턴 중에 숫자를 올린다" 하나뿐이다. 그 하나는 아래 D1 에서 **FAIL 로 남긴다.**
#
# 산출물: out/50-checks.tsv · out/50.json · web/__screenshots__/p3-50-*.png
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-50.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-50.json"; WORK="$OUT/work-50"; DLOG="$OUT/daemon-50.log"
TAP="$OUT/tap-50.jsonl"; TAP_PORT="${TAP_PORT_50:-8103}"
HB="$P3_DIR/fixtures/daemon_heartbeat.sh"
MODEL="${LEAD_MODEL}"
BUDGET="${BUDGET:-1}"          # 에이전트 budget_per_task — EVAL E9-01 그대로 $1
OVER_1="${OVER_1:-1.01}"       # 턴 중 누적(초과) — E9-01
BUDGET_NEW="${BUDGET_NEW:-3}"  # 상향값 — E9-02
OVER_2="${OVER_2:-1.50}"       # 재개 뒤 누적 — E9-08
EMAIL="g6c+$STAMP@example.com"; PASSWORD="password123"
export AGENT_BROWSER_SESSION="colab-g6-50-$STAMP"
mkdir -p "$E2E_ROOT/web/__screenshots__"
g5_chk_init "$OUT/50-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$OUT/daemon-50.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-50.pid")" 2>/dev/null || true; }
  agent-browser close >/dev/null 2>&1 || true
  return 0
}
trap cleanup EXIT

# 과제는 저장소 밖의 무해한 것(X-2). 턴이 heartbeat 두 번(30초)보다 길어야 "턴 중" 을 잰다.
INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명서를 쓰는 작성자다. 답은 한국어로 짧게.

지시를 받으면 현재 작업 디렉토리에서 아래를 **순서대로**, 서두르지 말고 한 단계씩 수행한다.
- 단계 1: manual-01.md 에 "개봉과 구성품" 을 여덟 줄로 쓴다. 끝나면 colab_message_post 로 `STEP-1 done` 게시.
- 단계 2: manual-02.md 에 "설치와 급수 주기 설정" 을 여덟 줄로 쓴다. 끝나면 `STEP-2 done` 게시.
- 단계 3: manual-03.md 에 "물 보충과 청소" 를 여덟 줄로 쓴다. 끝나면 `STEP-3 done` 게시.
- 단계 4: manual-04.md 에 "문제 해결 여덟 가지" 를 여덟 줄로 쓴다. 끝나면 `STEP-4 done` 게시.
- 단계 5: manual-05.md 에 "보관과 폐기" 를 여덟 줄로 쓴다. 끝나면 `STEP-5 done` 게시.
다섯 단계가 모두 끝나면 `ALL-DONE` 을 게시하고 colab_status_set 으로 status "done" 을 부른 뒤 턴을 끝낸다.

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라. 파일 쓰기 말고는 colab_* 도구만 쓴다.'
GOAL='가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명서 초안을 다섯 조각으로 나눠 쓴다'

# wait_running TASK [TIMEOUT] → 그 task 가 running 이 될 때까지 (attempt 프로세스가 살아 있어야 대역이 성립한다)
wait_running() {
  local dl=$(( $(date +%s) + ${2:-300} )) st
  while [ "$(date +%s)" -lt "$dl" ]; do
    st="$(task_field "$1" status)"; [ "$st" = running ] && { echo running; return 0; }
    case "$st" in completed|failed|cancelled|paused) echo "$st"; return 1;; esac
    sleep 2
  done
  echo timeout; return 1
}

step "0. claim 탭"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT (pid $TAP_PID)"

step "1. 계정 · 페어링 (capacity=3 — 승인·거절·추정 세 세션이 나란히 돈다)"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G6 Budget $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_cap "$PTOK" "$CFG" "$WORK" 3
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$OUT/daemon-50.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "ws=$WS runtime=$RUNTIME"

step "2. 에이전트 budget_per_task = \$$BUDGET · 세션 3개 (A 승인 · B 거절 · C 추정)"
AG_A="$(create_agent_p2 "$WS" SpenderA writer "$MODEL" "$INS" '설명서를 쓴다')"
AG_B="$(create_agent_p2 "$WS" SpenderB writer "$MODEL" "$INS" '설명서를 쓴다')"
AG_C="$(create_agent_p2 "$WS" SpenderC writer "$MODEL" "$INS" '설명서를 쓴다')"
for a in "$AG_A" "$AG_B" "$AG_C"; do set_agent_budget "$a" "$BUDGET"; done
chk S0 "에이전트 budget_per_task = \$$BUDGET" "$(printf '%.2f' "$BUDGET")" "$(psqlq "select round(budget_per_task::numeric,2)::text from agent where id='$AG_A'")"
SA="$(create_session_p3 "$WS" "제품 Y 설명서 (예산 승인)" "$GOAL" "$AG_A" "$RUNTIME" '{}' "$AG_A")"
SB="$(create_session_p3 "$WS" "제품 Y 설명서 (예산 거절)" "$GOAL" "$AG_B" "$RUNTIME" '{}' "$AG_B")"
SC_="$(create_session_p3 "$WS" "제품 Y 설명서 (추정 비용)" "$GOAL" "$AG_C" "$RUNTIME" '{}' "$AG_C")"
TA="$(session_initial_task "$SA")"; TB="$(session_initial_task "$SB")"; TC="$(session_initial_task "$SC_")"
ok "A=$TA B=$TB C=$TC"
T0="$(now_ms)"

step "3. 결함 확인 — 데몬은 **턴 중** usage 를 올리지 않는다 (heartbeat 3회 이상 기다린다)"
for t in "$TA" "$TB" "$TC"; do wait_running "$t" 300 >/dev/null || bad "task $t 가 running 으로 가지 않았다"; done
sleep 50    # HeartbeatInterval=15s → 3회 이상
HB_ROWS="$(psqlq "select count(*) from task_usage where task_id='$TA'")"
HB_COST="$(psqlq "select coalesce(round(cost_usd::numeric,4)::text,'0') from task_usage where task_id='$TA'")"
chk D1 "**데몬이 턴 중 usage 를 보고한다** (신규 결함 — heartbeat 3회 뒤에도 task_usage 가 없다)" 1 "$HB_ROWS"
log "턴 시작 50s 뒤 task_usage: 행 $HB_ROWS · cost ${HB_COST:-0}"
chk D1b "그래서 아직 아무 강제도 없다 (task 는 running)" running "$(task_field "$TA" status)"

step "4. 데몬 대역으로 **세 턴 모두** turn-중 usage 를 보고한다 (daemon-protocol §4.2 와이어)"
# 세 자극을 한 자리에서 낸다 — A 의 웹·승인·재개를 먼저 하면 B·C 의 턴이 그 사이에 끝나 버려
# "턴 중" 이 성립하지 않는다(1차 실행 실측).
read -r HB_CODE HB_BODY <<<"$(bash "$HB" "$CFG" "$TA" "$(task_field "$TA" attempt)" 12000 8000 "$OVER_1" false)"
chk C0  "A: heartbeat 가 받아들여진다 (HTTP $HB_CODE)" 200 "$HB_CODE"
printf '%s\n' "$HB_BODY" > "$OUT/50-heartbeat-response.json"
read -r HB_CODE_B _ <<<"$(bash "$HB" "$CFG" "$TB" "$(task_field "$TB" attempt)" 12000 8000 "$OVER_1" false)"
chk C0b "B: heartbeat 가 받아들여진다 (HTTP $HB_CODE_B)" 200 "$HB_CODE_B"
read -r HB_CODE_C _ <<<"$(bash "$HB" "$CFG" "$TC" "$(task_field "$TC" attempt)" 12000 8000 "$OVER_1" true)"
chk C0c "C: **추정치** heartbeat 가 받아들여진다 (HTTP $HB_CODE_C)" 200 "$HB_CODE_C"

step "4b. E9-01 — A: paused(budget) + 시스템 HITL"
DEADLINE=$(( $(date +%s) + 120 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do [ "$(task_field "$TA" status)" = paused ] && break; sleep 3; done
LANE_A="$(task_field "$TA" lane_id)"
chk C1  "A: task 가 **paused** 다 (failed 가 아니다)     " paused "$(task_field "$TA" status)"
chk C1b "A: paused_reason=budget"                          budget "$(task_field "$TA" paused_reason)"
chk C1c "A: lane 도 paused"                                paused "$(lane_field "$LANE_A" status)"
chk C1d "A: 세션은 active 유지 (task 범위 초과다)"        active "$(psqlq "select status from session where id='$SA'")"
chk C1e "A: 기록된 누적이 상한을 넘었다"                   yes \
  "$(python3 -c "import sys;print('yes' if float(sys.argv[1])>float(sys.argv[2]) else 'no')" \
     "$(psqlq "select coalesce(cost_usd::text,'0') from task_usage where task_id='$TA'")" "$BUDGET")"
HA="$(hitl_of_task "$TA")"
chk C2  "A: 시스템 HITL 이 발행됐다"                       1 "$(psqlq "select count(*) from hitl_request where task_id='$TA'")"
if [ -n "$HA" ]; then
chk C2b "A: source=system"                                 system "$(hitl_field "$HA" source)"
chk C2c "A: type=approval"                                 approval "$(hitl_field "$HA" type)"
chk C2d "A: **purpose=budget** (0012 — 완료 승인·루프 정지와 갈린다)" budget "$(hitl_field "$HA" purpose)"
chk C2e "A: **task_id 가 채워져 있다** (FR-7.3 s-13 — 없으면 재개할 대상이 없다)" "$TA" "$(hitl_field "$HA" task_id)"
chk C2f "A: Director 인박스 항목"                          1 "$(psqlq "select count(*) from inbox_item where ref_id='$HA'")"
fi
chk C3  "A: 서버가 §8.2.2 취소 **명령**을 냈다 (프로세스 kill 이 아니다)" yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from daemon_command where task_id='$TA' and type='cancel'")"
chk C3b "A: 그 명령이 데몬에 전달됐다 (delivered_at, S-35)" yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from daemon_command where task_id='$TA' and type='cancel' and delivered_at is not null")"
DEADLINE=$(( $(date +%s) + 120 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do [ "$(procs_of_attempt "$WORK" "$TA" 1)" = 0 ] && break; sleep 3; done
chk C3c "A: attempt 1 의 프로세스가 남아 있지 않다"        0 "$(procs_of_attempt "$WORK" "$TA" 1)"

step "4c. E9-05 — 추정치(estimated:true) 경로. **실기가 늘 타는 분기인데 잴 수가 없다**"
# 관측: `RecordTurnUsage` 는 `estimated: true` 보고의 cost 를 **0 으로 떨어뜨린다**(harness v0.7.1 —
# "추정 보고가 나르는 0 은 런타임이 잰 숫자가 아니다", 가격은 roll-up 이 매긴다). 그래서 추정 보고는
# heartbeat 강제 경로에서 언제나 `spent = 0` 이고, PlanBudget 의 Estimated 분기(세션 paused + 드레인)에
# **도달하지 못한다.** ACP 경로는 cost_usd 를 주지 않아 실기 usage 가 100% 추정이므로(K-8),
# D-17(데몬이 턴 중 usage 를 올린다)만 고쳐도 예산 강제는 여전히 발동하지 않는다 — 보고서 §2 에 적었다.
sleep 8
C_SPENT="$(psqlq "select coalesce(cost_usd::text,'-') from task_usage where task_id='$TC'")"
C_EST="$(psqlq "select coalesce(estimated::text,'-') from task_usage where task_id='$TC'")"
log "C: 추정 보고 뒤 task_usage = cost ${C_SPENT:--} · estimated ${C_EST:--} (보고한 값은 \$$OVER_1)"
chk N0  "C: 추정치 보고에는 **취소 명령이 없다** (FR-7.3 — 하드 컷 금지)" 0 \
  "$(psqlq "select count(*) from daemon_command where task_id='$TC' and type='cancel'")"
chk N0b "C: 진행 중 턴이 취소되지 않았다 (드레인)"          no \
  "$( [ "$(task_field "$TC" status)" = cancelled ] && echo yes || echo no )"
chk N1  "C: **서버가 추정 보고의 금액을 버리지 않는다** (신규 결함 K-8 — 지금은 0 으로 떨어뜨린다)" \
  yes "$(python3 -c "import sys;print('yes' if float(sys.argv[1] or 0)>0 else 'no')" "${C_SPENT:-0}")"
chk N2  "C: **세션이 paused(budget) 로 멈춘다** (E9-05 — 위 결함으로 도달하지 못한다)" paused \
  "$(psqlq "select status from session where id='$SC_'")"
chk N3  "C: 활동 피드에 \"추정 … 진행 중인 턴은 끝까지\" 기록 (E9-05)" 1 \
  "$(psqlq "select count(*) from task_event where task_id='$TC' and class='status' and verb='pause'")"

step "4d. E9-03 — B: 거절은 failed 가 아니라 paused 유지"
DEADLINE=$(( $(date +%s) + 120 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do [ "$(task_field "$TB" status)" = paused ] && break; sleep 3; done
HB_H="$(hitl_of_task "$TB")"
chk J1 "B: 예산 HITL 이 열렸다" yes "$( [ -n "$HB_H" ] && echo yes || echo no )"
if [ -n "$HB_H" ]; then
  read -r RCB _ <<<"$(respond_hitl "$HB_H" '{"approved":false,"reason":"이 초안은 여기까지면 충분하다"}')"
  chk J2  "거절이 받아들여진다 (HTTP $RCB)" yes "$( [ "${RCB:0:1}" = 2 ] && echo yes || echo no )"
  sleep 5
  chk J3  "B: task 가 **paused(budget) 유지**"          paused "$(task_field "$TB" status)"
  chk J3b "B: paused_reason 이 여전히 budget"           budget "$(task_field "$TB" paused_reason)"
  chk J3c "B: failed 도 cancelled 도 아니다 (E9-03)"     no \
    "$( [ "$(in_set "$(task_field "$TB" status)" failed cancelled)" = yes ] && echo yes || echo no )"
  chk J3d "B: budget_override 는 저장되지 않았다"        - "$(task_field "$TB" budget_override)"
  chk J3e "B: 재큐잉되지 않았다 (attempt 그대로 1)"      1 "$(task_field "$TB" attempt)"
fi

step "5. E9-02 — 상향 승인. **웹에 낼 화면이 없다**(신규 결함) → 정식 op 로 낸다"
# 시스템 발행 HITL(`budget`·`user_approval`·`loop`)은 **타임라인 카드 메시지를 만들지 않는다**:
# `budget.go:188`·`sessions/complete.go:216`·`router/service.go:500` 의 INSERT 는 `message_id` 를 채우지
# 않고 `message(kind='hitl')` 도 게시하지 않는다 — 에이전트 발행 경로(`handlers_hitl_p3.go:203`)만 게시한다.
# S7 은 `kind='hitl'` 메시지를 HitlCard 로 그리므로 이 카드는 화면에 **아예 없다**. 인박스에는 항목이
# 뜨지만 `InboxItemCard` 의 상향 입력칸은 `type === "session_paused"` 조건이라 `hitl_request` 항목에는
# 붙지 않는다 — 즉 **웹에서 예산을 올릴 수 있는 자리가 한 군데도 없다.**
# 아래 W1~W3 이 그 관측이고(FAIL 로 남긴다), 뒷단계는 정식 op(`respondHitlRequest`)로 이어 잰다.
ab set viewport 1440 1000 >/dev/null 2>&1 || true
WEB_OK=no; web_login "$EMAIL" "$PASSWORD" && WEB_OK=yes
chk W0 "웹 로그인" yes "$WEB_OK"
ab open "$WEB_URL/sessions/$SA" >/dev/null 2>&1 || true
abwait '[data-testid="timeline"]' 40 || true
sleep 3
shot "p3-50-01-session-no-budget-card"
BC="$(abcount '[data-testid="hitl-card"][data-purpose="budget"]')"
chk W1  "**S7 타임라인에 예산 HITL 카드가 렌더된다** (신규 결함 — 시스템 발행은 카드 메시지가 없다)" yes \
  "$( [ "${BC:-0}" -ge 1 ] && echo yes || echo no )"
chk W1b "그 근거: hitl_request.message_id 가 채워져 있다" yes \
  "$( [ "$(psqlq "select case when message_id is null then 'no' else 'yes' end from hitl_request where id='$HA'")" = yes ] && echo yes || echo no )"
ab open "$WEB_URL/inbox" >/dev/null 2>&1 || true
abwait '[data-testid="inbox-page"]' 40 || true
shot "p3-50-02-inbox-budget-item"
IB="$(abcount '[data-testid="inbox-item"][data-type="hitl_request"]')"
chk W2  "인박스에는 항목이 뜬다"                          yes "$( [ "${IB:-0}" -ge 1 ] && echo yes || echo no )"
chk W3  "**인박스 카드에 상향 입력칸이 있다** (신규 결함 — hitl_request 항목에는 붙지 않는다)" yes \
  "$( [ "$(abcount '[data-testid="inbox-item"][data-type="hitl_request"] [data-testid="hitl-budget-input"]')" -ge 1 ] \
      || [ "$(abcount '[data-testid="inbox-item"][data-type="hitl_request"] [data-testid="inbox-budget-input"]')" -ge 1 ] \
      && echo yes || echo no )"
# 우회: 정식 op 로 상향 승인한다(계약 respondHitlRequest 의 budget_override_usd).
read -r RCA _ <<<"$(respond_hitl "$HA" "$(jq -nc --argjson b "$BUDGET_NEW" '{approved:true,budget_override_usd:$b}')")"
chk C4  "상향 승인이 정식 op 로 받아들여진다 (HTTP $RCA)"  yes "$( [ "${RCA:0:1}" = 2 ] && echo yes || echo no )"
sleep 3
chk C4a "HITL 이 answered · approved=true"                 "answered|true" \
  "$(psqlq "select status::text||'|'||coalesce(approved::text,'-') from hitl_request where id='$HA'")"
chk C4b "**task.budget_override = \$$BUDGET_NEW**"          "$BUDGET_NEW" \
  "$(psqlq "select round(budget_override::numeric,0)::text from task where id='$TA'")"
chk C4c "**에이전트 budget_per_task 는 불변** (\$$BUDGET)"  "$(printf '%.2f' "$BUDGET")" \
  "$(psqlq "select round(budget_per_task::numeric,2)::text from agent where id='$AG_A'")"
chk C4d "승인이 결정 기록으로 남는다"                      1 "$(psqlq "select count(*) from decision where session_id='$SA'")"

step "6. 같은 lane·workdir 로 재개 + E9-08 (\$$OVER_2 에서 취소 없음)"
wait_task_attempt "$TA" 2 || bad "A: attempt 2 로 넘어가지 않았다"
chk C5  "같은 task 의 attempt 2 다 (새 task 가 아니다)"     2 "$(task_field "$TA" attempt)"
chk C5b "task 수 그대로 (새 트리거 불필요)"                 1 "$(task_count "$SA")"
chk C5c "**같은 lane**"                                     "$LANE_A" "$(task_field "$TA" lane_id)"
chk C5d "같은 workdir 를 다시 쓴다"                         yes \
  "$( [ -d "$WORK/sessions/$SA/$LANE_A" ] && echo yes || echo no )"
wait_running "$TA" 300 >/dev/null || bad "A: attempt 2 가 running 으로 가지 않았다"
read -r HB2_CODE _ <<<"$(bash "$HB" "$CFG" "$TA" 2 15000 12000 "$OVER_2" false)"
chk C6  "재개 뒤 \$$OVER_2 heartbeat 가 받아들여진다 (HTTP $HB2_CODE)" 200 "$HB2_CODE"
sleep 8
chk C6b "**E9-08: 취소가 없다** — task 가 여전히 running"    running "$(task_field "$TA" status)"
chk C6c "새 예산 HITL 이 생기지 않았다"                     1 "$(psqlq "select count(*) from hitl_request where task_id='$TA'")"
chk C6d "취소 명령도 더 나오지 않았다"                      1 \
  "$(psqlq "select count(*) from daemon_command where task_id='$TA' and type='cancel'")"
tap_prompt "$TAP" "$TA" 2 > "$OUT/50-prompt-attempt2.txt"
chk_has C6e "재개 프롬프트에 <resumed> 구간"                "$OUT/50-prompt-attempt2.txt" "<resumed attempt=2>"
AST="$(WAIT_S=${RESUME_WAIT_S:-900} wait_task "$TA" completed failed cancelled paused)"
chk C6f "재개한 턴이 끝까지 갔다"                           completed "$AST"

step "7. 4단위 비용 집계 (E9-07 · E9-09 — getSessionCost)"
COST="$(api_ok GET "/sessions/$SA/cost" || echo '{}')"
printf '%s\n' "$COST" > "$OUT/50-cost.json"
chk K1  "getSessionCost 가 응답한다"           yes "$( [ -n "$COST" ] && echo yes || echo no )"
chk K1b "세션 합계가 0 보다 크다"              yes \
  "$(python3 -c "import json;d=json.load(open('$OUT/50-cost.json'));import sys;
tot=d.get('total_usd') or d.get('cost_usd') or (d.get('session') or {}).get('cost_usd') or 0
print('yes' if float(tot)>0 else 'no')" 2>/dev/null || echo no)"
psqlq "select 'task' unit, count(*) from task_usage u join task t on t.id=u.task_id where t.session_id='$SA'
       union all select 'agent', count(distinct t.agent_id) from task t where t.session_id='$SA'" > "$OUT/50-cost-units.tsv"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg sa "$SA" --arg sb "$SB" --arg sc "$SC_" \
  --arg ta "$TA" --arg tb "$TB" --arg tc "$TC" --arg ha "${HA:-}" --arg hb "${HB_H:-}" \
  --arg budget "$BUDGET" --arg over1 "$OVER_1" --arg new "$BUDGET_NEW" --arg over2 "$OVER_2" \
  --arg final "$AST" --argjson hb_rows "${HB_ROWS:-0}" \
  --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,approve:{session:$sa,task:$ta,hitl:$ha},reject:{session:$sb,task:$tb,hitl:$hb},
    estimated:{session:$sc,task:$tc},
    budget:{per_task_usd:$budget,in_turn_usd:$over1,override_usd:$new,after_resume_usd:$over2},
    daemon_in_turn_usage_rows:$hb_rows,final_status:$final,
    elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/50.json"
[ "$fail" = 0 ]
