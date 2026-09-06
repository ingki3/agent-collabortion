#!/usr/bin/env bash
# e2e/p3/50_budget_pause_override.sh — T-I3 (c): **예산** (E9-01 · E9-02 · E9-03 · E9-05 · E9-08 · E9-10).
#
# 2판(핫픽스 뒤 재측정). **데몬 대역 우회가 없다** — 자극은 전부 에이전트가 실제로 쓴 돈이다.
# 1판(957ffd3)은 `fixtures/daemon_heartbeat.sh` 로 §4.2 와이어를 흉내 내야 했다: 데몬이 턴 중에 usage 를
# 올리지 않았고(D-17), 서버가 추정 보고의 금액을 0 으로 떨어뜨렸고(K-8/S-48), finish 에도 강제가
# 없었다(S-44). 셋 다 닫혔다 — 데몬 #145 · 서버 #136 · #147.
#
#   E9-01  턴 중 누적이 task 상한을 넘으면 → §8.2.2 취소 **명령** → task `paused(budget)`(failed 아님),
#          lane `paused`, Director 에게 시스템 HITL(`source: system` · `purpose: budget` · **`task_id` 채움**)
#          + **S7 타임라인 카드**(S-45) + 인박스
#   E9-02  Director 가 **웹에서** 3배로 상향 승인 → `task.budget_override`, 에이전트 `budget_per_task`
#          **불변**, 같은 lane·같은 workdir 로 재개(새 트리거 불필요)
#   E9-08  재개 뒤 누적이 **원래 상한을 다시 넘는데도** 취소 없음 — override 를 저장만 하고 강제 시점에
#          읽지 않으면 재개 즉시 다시 paused 가 된다
#   E9-03  거절은 `failed` 가 아니라 `paused(budget)` **유지**
#   E9-05  추정치(`estimated: true`)는 **하드 컷 없음** — 세션 `paused(budget)` + 드레인 + 시스템
#          HITL(`purpose=budget`, **`task_id` 비움**) + 인박스 1(옛 `session_paused` 카드는 없다) + claim 0.
#          S-48 뒤로는 추정 금액도 워크스페이스 가격표로 매겨져 강제에 **도달한다**
#   K-10   그 HITL 승인 한 번이 곧 세션 재개다 — `limits.budget_usd` = 승인 금액 · 세션 `active` ·
#          park 된 task `queued`(S-46) · 다음 dispatch 재개
#   E9-10  턴이 끝난 뒤 발견한 초과(사후 강제, S-44)
#
# ── 금액을 EVAL 의 $1 이 아니라 실기 눈금으로 잡은 이유 ─────────────────────
# EVAL E9-01 은 `budget_per_task` $1 · 턴 중 $1.01 이다. 그 금액은 **실기 한 턴으로 도달할 수 없다** —
# haiku 한 턴의 실측 비용은 $0.075 안팎이다(이 스택 캘리브레이션: measured $0.075451). 1판은 그래서
# 대역으로 $1.01 을 "보냈다". 2판은 반대로 **상한을 실기 눈금으로 내리고 EVAL 의 비율을 지킨다**:
#   상한 L → 턴 중 초과 → 상향 3L → 재개 뒤 다시 L 초과(취소 없음). L = $0.05.
# 그리고 L 은 **한 턴의 추정치와 실측치 사이**에 있어야 한다. 같은 턴을 서버는 두 번 본다 —
# 턴 중에는 어댑터 원시 스트림의 토큰을 가격표로 매긴 **추정**($0.044 = 67·1 + 4913·5 + 190812·0.1 /1e6),
# 턴 끝에는 `result.total_cost_usd` 의 **실측**($0.0755). 추정이 먼저 넘으면 E9-05 분기(세션 드레인)로
# 가고 E9-01(실측 → 취소 명령)에 도달하지 못한다. $0.044 < **$0.05** < $0.0755 가 그 사이다.
#
# 산출물: out/50-checks.tsv · out/50.json · out/50-*.tsv · web/__screenshots__/p3-50-*.png
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-50.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-50.json"; WORK="$OUT/work-50"; DLOG="$OUT/daemon-50.log"
TAP="$OUT/tap-50.jsonl"; TAP_PORT="${TAP_PORT_50:-8103}"
MODEL="${LEAD_MODEL}"
BUDGET="${BUDGET:-0.05}"            # A·B 의 budget_per_task — 실기 한 턴 실측($0.0755)보다 낮고 추정($0.044)보다 높다
BUDGET_NEW="${BUDGET_NEW:-0.15}"    # 상향값 = 3×L (EVAL 의 $1→$3 비율)
EST_BUDGET="${EST_BUDGET:-0.015}"   # C 의 budget_per_task — **턴 중 추정치**로 넘긴다(E9-05)
EST_NEW="${EST_NEW:-0.30}"
SESS_LIMIT="${SESS_LIMIT:-0.05}"    # D 의 session limits.budget_usd — 세션 범위 초과(K-10 · S-46)
SESS_NEW="${SESS_NEW:-0.15}"
HM_BUDGET="${HM_BUDGET:-0.10}"      # H(hermes) — hermes 한 턴 추정 $0.185
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

# 과제는 저장소 밖의 무해한 것(X-2). 지시문 기본 문구는 §0-16 이다 — "저장소를 뒤지지 마라" 와
# "도구가 실패해도 재시도하거나 다른 방법을 찾지 마라". 한 턴에 다섯 조각만 쓰게 해서 **재개한 턴도
# 첫 턴만큼 돈을 쓰게** 만든다(E9-08 은 재개 뒤에도 원래 상한을 넘어야 성립한다).
INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명서를 쓰는 작성자다. 답은 한국어로 짧게.

현재 작업 디렉토리에 manual-01.md 부터 manual-10.md 까지 열 조각을 쓴다. 각 조각은 여덟 줄이다.
- manual-01 개봉과 구성품 · manual-02 설치 · manual-03 급수 주기 설정 · manual-04 물 보충 · manual-05 청소
- manual-06 문제 해결 · manual-07 부품 교체 · manual-08 절전 · manual-09 보관 · manual-10 폐기
**한 턴에는 아직 없는 파일 다섯 개까지만 쓰고 턴을 끝낸다.** 다음 턴에서 이어 쓴다.
매 턴을 시작할 때 먼저 현재 디렉토리의 파일 목록을 보고 무엇이 남았는지 정한다.
열 조각이 모두 있으면 colab_status_set 으로 status "done" 을 부르고 턴을 끝낸다.

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라. 도구가 실패해도 재시도하거나
다른 방법을 찾지 마라. 파일 쓰기와 목록 보기 말고는 colab_* 도구만 쓴다.'
# Hermes 는 MCP 를 존중하지 않는다(harness §10) — colab_* 대신 셸만 쓴다.
HM_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명서를 쓰는 작성자다. 답은 한국어로 짧게.
현재 작업 디렉토리에 manual-01.md 부터 manual-05.md 까지 다섯 조각을 각각 여덟 줄로 쓰고 턴을 끝낸다.
웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라. 도구가 실패해도 재시도하거나
다른 방법을 찾지 마라.'
GOAL='가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명서 초안을 조각으로 나눠 쓴다'

# wait_running TASK [TIMEOUT] → 그 task 가 running 이 될 때까지
wait_running() {
  local dl=$(( $(date +%s) + ${2:-300} )) st
  while [ "$(date +%s)" -lt "$dl" ]; do
    st="$(task_field "$1" status)"; [ "$st" = running ] && { echo running; return 0; }
    case "$st" in completed|failed|cancelled|paused) echo "$st"; return 1;; esac
    sleep 2
  done
  echo timeout; return 1
}
# wait_task_paused TASK [TIMEOUT]
wait_task_paused() {
  local dl=$(( $(date +%s) + ${2:-420} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(task_field "$1" status)" = paused ] && return 0
    sleep 3
  done
  return 1
}
# wait_session_paused SESSION [TIMEOUT]
wait_session_paused() {
  local dl=$(( $(date +%s) + ${2:-420} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select status from session where id='$1'")" = paused ] && return 0
    sleep 3
  done
  return 1
}
sess_field() { psqlq "select coalesce(($2)::text,'-') from session where id='$1'"; }
usage_cost() { psqlq "select coalesce(round(cost_usd::numeric,6)::text,'-') from task_usage where task_id='$1'"; }
usage_est()  { psqlq "select coalesce(estimated::text,'-') from task_usage where task_id='$1'"; }
gt() { python3 -c "import sys;print('yes' if float(sys.argv[1] or 0)>float(sys.argv[2]) else 'no')" "$1" "$2"; }
# eqnum GOT WANT → 같으면 WANT, 다르면 GOT (표에 실제 값이 남는다). '0.3' 과 '0.30' 을 가르지 않는다.
eqnum() { python3 -c "import sys
g,w=sys.argv[1],sys.argv[2]
try: print(w if abs(float(g)-float(w))<1e-9 else g)
except Exception: print(g)" "$1" "$2"; }

step "0. claim 탭 — 서버가 데몬에 주는 TaskBundle 을 기록한다"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT (pid $TAP_PID)"

step "1. 계정 · 페어링 (capacity=5 — 다섯 arm 이 나란히 돈다)"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G6 Budget 2 $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_cap "$PTOK" "$CFG" "$WORK" 5
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$OUT/daemon-50.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
chk R0 "런타임이 usage_midturn 을 광고한다 (harness §9 v0.8.5, 실측 광고)" true \
  "$(psqlq "select coalesce((c->>'usage_midturn'),'-') from runtime r, jsonb_array_elements(r.capabilities) c
            where r.id='$RUNTIME' and c->>'kind'='claude_code'")"
ok "ws=$WS runtime=$RUNTIME daemon pid $(cat "$OUT/daemon-50.pid")"

step "2. 다섯 arm — A 승인 · B 거절 · C 추정 · D 세션범위 · H hermes"
AG_A="$(create_agent_p2 "$WS" SpenderA writer "$MODEL" "$INS" '설명서를 쓴다')"
AG_B="$(create_agent_p2 "$WS" SpenderB writer "$MODEL" "$INS" '설명서를 쓴다')"
AG_C="$(create_agent_p2 "$WS" SpenderC writer "$MODEL" "$INS" '설명서를 쓴다')"
AG_D="$(create_agent_p2 "$WS" SpenderD writer "$MODEL" "$INS" '설명서를 쓴다')"
AG_E="$(create_agent_p2 "$WS" PeerD    reviewer "$MODEL" "$INS" '초안을 이어 쓴다')"
AG_H="$(create_agent_kind "$WS" SpenderH writer hermes "$MODEL" "$HM_INS" '설명서를 쓴다')"
set_agent_budget "$AG_A" "$BUDGET"; set_agent_budget "$AG_B" "$BUDGET"
set_agent_budget "$AG_C" "$EST_BUDGET"; set_agent_budget "$AG_H" "$HM_BUDGET"
# D 는 **에이전트 상한이 없다** — 세션 상한만으로 넘긴다(D-16 이 min() 을 잡는 자리).
chk S0  "A 의 budget_per_task = \$$BUDGET" "$(printf '%.3f' "$BUDGET")" \
  "$(psqlq "select round(budget_per_task::numeric,3)::text from agent where id='$AG_A'")"
chk S0b "D 는 에이전트 상한이 없다 (세션 상한만)" - "$(agent_budget "$AG_D")"
SA="$(create_session_p3 "$WS" "제품 Y 설명서 (예산 승인)"   "$GOAL" "$AG_A" "$RUNTIME" '{}' "$AG_A")"
SB="$(create_session_p3 "$WS" "제품 Y 설명서 (예산 거절)"   "$GOAL" "$AG_B" "$RUNTIME" '{}' "$AG_B")"
SC_="$(create_session_p3 "$WS" "제품 Y 설명서 (추정 비용)"  "$GOAL" "$AG_C" "$RUNTIME" '{}' "$AG_C")"
SD="$(create_session_p3 "$WS" "제품 Y 설명서 (세션 예산)"   "$GOAL" "$AG_D" "$RUNTIME" \
      "$(jq -nc --argjson b "$SESS_LIMIT" '{limits:{budget_usd:$b}}')" "$AG_D" "$AG_E")"
SH="$(create_session_p3 "$WS" "제품 Y 설명서 (hermes)"      "$GOAL" "$AG_H" "$RUNTIME" '{}' "$AG_H")"
TA="$(session_initial_task "$SA")"; TB="$(session_initial_task "$SB")"
TC="$(session_initial_task "$SC_")"; TD="$(session_initial_task "$SD")"; TH="$(session_initial_task "$SH")"
chk S0c "D: 세션 limits.budget_usd = \$$SESS_LIMIT" "$SESS_LIMIT" \
  "$(eqnum "$(psqlq "select (limits->>'budget_usd') from session where id='$SD'")" "$SESS_LIMIT")"
ok "A=$TA B=$TB C=$TC D=$TD H=$TH"
T0="$(now_ms)"

step "3. D-17 해결 확인 — 데몬이 **턴 중에** usage 를 올린다 (대역 없음)"
wait_running "$TA" 420 >/dev/null || bad "A 가 running 으로 가지 않았다"
# HeartbeatInterval 15s. 두 번을 보고 나서 잰다 — 첫 heartbeat 는 턴 시작 직후라 토큰이 아직 없을 수 있다.
: > "$OUT/50-midturn.tsv"
MID_ROWS=0; MID_TOK=0; MID_COST=0; MID_EST='-'
DEADLINE=$(( $(date +%s) + 90 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  R="$(psqlq "select coalesce(count(*)::text,'0'), coalesce(max(input_tokens+output_tokens)::text,'0'),
                     coalesce(max(round(cost_usd::numeric,6))::text,'0'), coalesce(bool_or(estimated)::text,'-')
              from task_usage where task_id='$TA'")"
  printf '%s\t%s\n' "$(date +%s)" "$R" >> "$OUT/50-midturn.tsv"
  read -r MID_ROWS MID_TOK MID_COST MID_EST <<<"$R"
  [ "${MID_ROWS:-0}" -ge 1 ] && [ "$(gt "${MID_TOK:-0}" 0)" = yes ] && break
  sleep 3
done
chk D1  "**데몬이 턴 중 usage 를 올린다** (D-17 — 1판은 heartbeat 3회 뒤에도 행 0개)" 1 "$MID_ROWS"
chk D1b "그 보고에 토큰이 실려 있다 (원시 SDK 스트림 누적, harness §7 v0.8.5)" yes "$(gt "${MID_TOK:-0}" 0)"
chk D1c "턴 중 보고는 **추정**이다 (가격이 아직 없다 — §7 v0.7.1)" true "$MID_EST"
chk D1d "**서버가 그 추정에 가격표를 매긴다** (S-48 — 1판은 0 으로 떨어뜨렸다)" yes "$(gt "${MID_COST:-0}" 0)"
chk D1e "아직 강제는 없다 — 추정치가 상한 \$$BUDGET 아래다 (task 는 running)" running "$(task_field "$TA" status)"
log "턴 중 실측: 행 $MID_ROWS · 토큰 $MID_TOK · 추정 금액 \$$MID_COST · estimated=$MID_EST"

step "4. E9-01 — A: 턴 끝 **실측**(estimated:false)이 상한을 넘는다 → 취소 명령 + paused(budget)"
wait_task_paused "$TA" 600 || bad "A: paused 로 가지 않았다 (status=$(task_field "$TA" status))"
LANE_A="$(task_field "$TA" lane_id)"
A_COST="$(usage_cost "$TA")"; A_EST="$(usage_est "$TA")"
log "A: 기록된 누적 \$$A_COST · estimated=$A_EST (상한 \$$BUDGET)"
chk C1  "A: task 가 **paused** 다 (failed 가 아니다)"      paused "$(task_field "$TA" status)"
chk C1b "A: paused_reason=budget"                          budget "$(task_field "$TA" paused_reason)"
chk C1c "A: lane 도 paused"                                paused "$(lane_field "$LANE_A" status)"
chk C1d "A: 세션은 active 유지 (task 범위 초과다)"         active "$(sess_field "$SA" status)"
chk C1e "A: 기록된 누적이 상한을 넘었다"                   yes "$(gt "$A_COST" "$BUDGET")"
chk C1f "A: 그 금액은 **실측**이다 (estimated:false — #145 result.total_cost_usd)" false "$A_EST"
HA="$(hitl_of_task "$TA")"
chk C2  "A: 시스템 HITL 이 발행됐다"                       1 "$(psqlq "select count(*) from hitl_request where task_id='$TA'")"
if [ -n "$HA" ]; then
chk C2b "A: source=system"                                 system "$(hitl_field "$HA" source)"
chk C2c "A: type=approval"                                 approval "$(hitl_field "$HA" type)"
chk C2d "A: **purpose=budget** (0012 — 완료 승인·루프 정지와 갈린다)" budget "$(hitl_field "$HA" purpose)"
chk C2e "A: **task_id 가 채워져 있다** (FR-7.3 s-13 — 없으면 재개할 대상이 없다)" "$TA" "$(hitl_field "$HA" task_id)"
chk C2f "A: Director 인박스 항목"                          1 "$(psqlq "select count(*) from inbox_item where ref_id='$HA'")"
chk C2g "A: **타임라인 카드가 붙었다** (S-45 — message_id NOT NULL)" yes \
  "$(psqlq "select case when message_id is null then 'no' else 'yes' end from hitl_request where id='$HA'")"
chk C2h "A: 그 메시지가 kind='hitl' 로 정확히 1행"          1 \
  "$(psqlq "select count(*) from message where session_id='$SA' and kind='hitl'")"
chk C2i "A: 인박스 카드가 purpose 를 싣는다 (K-9)"         budget \
  "$(api_ok GET "/inbox?limit=50" | jq -r --arg r "$HA" '[.items[]|select(.ref_id==$r)][0].card.purpose // "-"')"
fi
chk C3  "A: 서버가 §8.2.2 취소 **명령**을 냈다 (프로세스 kill 이 아니다)" yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from daemon_command where task_id='$TA' and type='cancel'")"
chk C3b "A: 그 명령이 데몬에 전달됐다 (delivered_at, S-35)" yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from daemon_command where task_id='$TA' and type='cancel' and delivered_at is not null")"
DEADLINE=$(( $(date +%s) + 150 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do [ "$(procs_of_attempt "$WORK" "$TA" 1)" = 0 ] && break; sleep 3; done
chk C3c "A: attempt 1 의 프로세스가 남아 있지 않다"        0 "$(procs_of_attempt "$WORK" "$TA" 1)"
# 데몬의 자기 예산 반쪽(FR-7.3 M9 · daemon §5)도 처음으로 동작한다 — #145 로 ACP 경로가 실측을 주면서
# `budget.go` 의 Estimated 가드가 처음 열렸다. 서버 명령과 이중이다.
chk C3d "A: 데몬이 **자기 §5 예산 취소**도 실행했다 (FR-7.3 M9)" yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from task_event
            where task_id='$TA' and class='runtime' and verb='cancel' and payload->>'detail' like '%유효 예산%'")"
# **신규 결함(서버)**: 서버가 예산으로 task 를 paused 시키면서 `cancel` 명령을 걸어 두는데,
# 그 뒤 데몬이 보내는 finish(outcome=paused_budget|cancelled)를 `tasks.Finish` 가 `cancelRequested` 때문에
# 통째로 `cancelled` 로 바꾼다(service.go 의 `decided = "cancelled"`). 그러면 `cancelLocked` 가
# `paused_reason` 만 지우고 `paused_detail` 은 남겨 `task_paused_detail_check`(0006)를 깨고 **500** 이 난다.
# 결과: 그 attempt 의 finish 가 **영영 기록되지 않는다** — outcome·finished_at NULL, 그리고 finish 가
# 유일한 기록자인 `lane.runtime_session_ref` 도 저장되지 않아 승인 뒤 재개가 **콜드 스타트**가 된다.
FIN500="$(grep -c 'task_paused_detail_check' "$DLOG" 2>/dev/null || echo 0)"
grep -n 'task_paused_detail_check' "$DLOG" > "$OUT/50-finish-500.txt" 2>/dev/null || true
log "데몬이 받은 finish 500(task_paused_detail_check): ${FIN500}건 — out/50-finish-500.txt"
chk C3e "A: attempt 1 의 finish 가 기록됐다 (신규 결함 — 예산 pause 뒤 finish 가 500)" yes \
  "$(psqlq "select case when outcome is null then 'no' else 'yes' end from task_attempt where task_id='$TA' and attempt=1")"
chk C3f "예산 pause 뒤 finish 가 500 을 받지 않는다 (신규 결함, 같은 원인)" 0 "${FIN500:-0}"

step "5. 단가 — 서버가 데몬이 보낸 실측 금액을 그대로 쓰는가 (어댑터 list 단가 재계산 없음)"
DAEMON_COST="$(psqlq "select coalesce((payload->>'cost_usd'),'-') from task_event
                      where task_id='$TA' and class='usage' and verb='report' and payload ? 'cost_usd'
                      order by seq desc limit 1")"
log "데몬 보고 cost_usd=$DAEMON_COST · DB task_usage.cost_usd=$A_COST"
chk P1 "task_usage.cost_usd 가 데몬 보고값과 같다 (서버가 다시 매기지 않는다)" yes \
  "$(python3 -c "import sys
a,b=sys.argv[1],sys.argv[2]
try: print('yes' if abs(float(a)-float(b))<1e-6 else 'no ('+a+' vs '+b+')')
except Exception: print('no ('+a+' vs '+b+')')" "${DAEMON_COST:--}" "$A_COST")"

step "6. E9-02 — **웹에서** 상향 승인 (S-45 카드 · W-6 입력칸)"
ab set viewport 1440 1000 >/dev/null 2>&1 || true
WEB_OK=no; web_login "$EMAIL" "$PASSWORD" && WEB_OK=yes
chk W0 "웹 로그인" yes "$WEB_OK"
ab open "$WEB_URL/sessions/$SA" >/dev/null 2>&1 || true
abwait '[data-testid="timeline"]' 40 || true
sleep 3
shot "p3-50-01-session-budget-card"
chk W1  "**S7 타임라인에 예산 HITL 카드가 렌더된다** (S-45)" yes \
  "$( [ "$(abcount '[data-testid="hitl-card"][data-purpose="budget"]')" -ge 1 ] && echo yes || echo no )"
chk W1b "그 카드에 상향 입력칸이 있다 (W-6 타임라인 쪽, scope=task)" yes \
  "$( [ "$(abcount '[data-testid="hitl-card"][data-purpose="budget"] [data-testid="hitl-budget-input"]')" -ge 1 ] && echo yes || echo no )"
ab open "$WEB_URL/inbox" >/dev/null 2>&1 || true
abwait '[data-testid="inbox-page"]' 40 || true
sleep 3
shot "p3-50-02-inbox-budget-input"
chk W2  "인박스에 항목이 뜬다"                            yes \
  "$( [ "$(abcount '[data-testid="inbox-item"][data-type="hitl_request"]')" -ge 1 ] && echo yes || echo no )"
# 인박스에는 다른 arm 의 항목도 함께 뜬다 — **이 HITL 의 항목만** 집어야 한다(data-item-id).
IB_A="$(psqlq "select id from inbox_item where ref_id='$HA' limit 1")"
IT_A="[data-testid=\"inbox-item\"][data-item-id=\"$IB_A\"]"
IN_SEL="$IT_A [data-testid=\"hitl-budget-input\"]"
chk W3  "**인박스 카드에 상향 입력칸이 있다** (W-6)"       yes \
  "$( [ "$(abcount "$IN_SEL")" -ge 1 ] && echo yes || echo no )"
# 웹에서 낸다 — 금액을 입력하고 "계속 진행 승인".
ab fill "$IN_SEL" "$BUDGET_NEW" >/dev/null 2>&1 || true
sleep 1
shot "p3-50-03-inbox-budget-filled"
ab click "$IT_A [data-testid=\"hitl-approve\"]" >/dev/null 2>&1 || true
DEADLINE=$(( $(date +%s) + 60 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(hitl_field "$HA" status)" = answered ] && break; sleep 2
done
chk C4  "**웹 승인이 서버에 도달했다** (우회 없음)"        answered "$(hitl_field "$HA" status)"
chk C4a "approved=true"                                    true "$(hitl_field "$HA" approved)"
chk C4b "**task.budget_override = \$$BUDGET_NEW**"         "$BUDGET_NEW" \
  "$(eqnum "$(task_field "$TA" budget_override)" "$BUDGET_NEW")"
chk C4c "**에이전트 budget_per_task 는 불변** (\$$BUDGET)" "$(printf '%.3f' "$BUDGET")" \
  "$(psqlq "select round(budget_per_task::numeric,3)::text from agent where id='$AG_A'")"
chk C4d "승인이 결정 기록으로 남는다"                      1 "$(psqlq "select count(*) from decision where session_id='$SA'")"

step "7. 같은 lane·workdir 로 재개 + E9-08 (재개 뒤 원래 상한을 다시 넘어도 취소 없음)"
wait_task_attempt "$TA" 2 || bad "A: attempt 2 로 넘어가지 않았다"
chk C5  "같은 task 의 attempt 2 다 (새 task 가 아니다)"    2 "$(task_field "$TA" attempt)"
chk C5b "task 수 그대로 (새 트리거 불필요)"                1 "$(task_count "$SA")"
chk C5c "**같은 lane**"                                    "$LANE_A" "$(task_field "$TA" lane_id)"
chk C5d "같은 workdir 를 다시 쓴다"                        yes \
  "$( [ -d "$WORK/sessions/$SA/$LANE_A" ] && echo yes || echo no )"
AST="$(WAIT_S=${RESUME_WAIT_S:-900} wait_task "$TA" completed failed cancelled paused)"
A2_COST="$(usage_cost "$TA")"; A2_EST="$(usage_est "$TA")"
log "A attempt 2: 최종 $AST · 누적 \$$A2_COST · estimated=$A2_EST (원래 상한 \$$BUDGET · override \$$BUDGET_NEW)"
# E9-02 는 "resume 우선"이다. attempt 1 의 finish 가 500 으로 죽으면(C3e) 그 finish 가 저장했어야 할
# `lane.runtime_session_ref` 가 없어 데몬은 이어 붙일 세션이 없다 — 같은 결함의 두 번째 증상이다.
chk C5e "**재개가 resume 우선이다** (E9-02 — 신규 결함으로 콜드 스타트가 된다)" true \
  "$(psqlq "select coalesce(resumed::text,'ref 자체가 저장되지 않았다') from task_attempt where task_id='$TA' and attempt=2")"
chk C6  "**E9-08: 재개한 턴의 누적이 원래 상한을 다시 넘었다**" yes "$(gt "$A2_COST" "$BUDGET")"
chk C6b "그런데 취소가 없다 — task 가 paused 가 아니다"    no \
  "$( [ "$(task_field "$TA" status)" = paused ] && echo yes || echo no )"
chk C6c "새 예산 HITL 이 생기지 않았다"                    1 "$(psqlq "select count(*) from hitl_request where task_id='$TA'")"
chk C6d "취소 명령도 더 나오지 않았다"                     1 \
  "$(psqlq "select count(*) from daemon_command where task_id='$TA' and type='cancel'")"
tap_prompt "$TAP" "$TA" 2 > "$OUT/50-prompt-attempt2.txt"
chk_has C6e "재개 프롬프트에 <resumed> 구간"               "$OUT/50-prompt-attempt2.txt" "<resumed attempt=2>"
chk C6f "재개한 턴이 끝까지 갔다"                          completed "$AST"

step "8. E9-03 — B: 거절은 failed 가 아니라 paused 유지"
wait_task_paused "$TB" 600 || bad "B: paused 로 가지 않았다 (status=$(task_field "$TB" status))"
HB_H="$(hitl_of_task "$TB")"
chk J1 "B: 예산 HITL 이 열렸다" yes "$( [ -n "$HB_H" ] && echo yes || echo no )"
if [ -n "$HB_H" ]; then
  read -r RCB _ <<<"$(respond_hitl "$HB_H" '{"approved":false,"reason":"이 초안은 여기까지면 충분하다"}')"
  chk J2  "거절이 받아들여진다 (HTTP $RCB)" yes "$( [ "${RCB:0:1}" = 2 ] && echo yes || echo no )"
  sleep 6
  chk J3  "B: task 가 **paused(budget) 유지**"          paused "$(task_field "$TB" status)"
  chk J3b "B: paused_reason 이 여전히 budget"           budget "$(task_field "$TB" paused_reason)"
  chk J3c "B: failed 도 cancelled 도 아니다 (E9-03)"     no \
    "$( [ "$(in_set "$(task_field "$TB" status)" failed cancelled)" = yes ] && echo yes || echo no )"
  chk J3d "B: budget_override 는 저장되지 않았다"        - "$(task_field "$TB" budget_override)"
  chk J3e "B: 재큐잉되지 않았다 (attempt 그대로 1)"      1 "$(task_field "$TB" attempt)"
fi

step "9. E9-05 · S-48 — C: **추정치**가 상한을 넘는다. 하드 컷 없이 세션 정지 + 드레인"
wait_session_paused "$SC_" 600 || bad "C: 세션이 paused 로 가지 않았다 (status=$(sess_field "$SC_" status))"
C_COST="$(usage_cost "$TC")"; C_EST="$(usage_est "$TC")"
HC="$(hitl_open "$SC_" budget)"; [ -n "$HC" ] || HC="$(psqlq "select id from hitl_request where session_id='$SC_' order by created_at desc limit 1")"
log "C: 세션 정지 시점 누적 \$$C_COST · estimated=$C_EST · 상한 \$$EST_BUDGET"
chk N0  "C: 추정 초과에 **취소 명령이 없다** (FR-7.3 — 하드 컷 금지)" 0 \
  "$(psqlq "select count(*) from daemon_command where task_id='$TC' and type='cancel'")"
chk N0b "C: 진행 중 턴이 취소되지 않았다 (드레인)"         no \
  "$( [ "$(in_set "$(task_field "$TC" status)" cancelled failed)" = yes ] && echo yes || echo no )"
chk N1  "C: **서버가 추정 보고의 금액을 버리지 않는다** (S-48 — 1판은 0 이었다)" yes "$(gt "$C_COST" 0)"
chk N1b "C: 그 금액이 상한을 넘었다"                       yes "$(gt "$C_COST" "$EST_BUDGET")"
# 정지를 **결정한** 값이 추정이었는지를 잰다 — 그 뒤 턴이 끝나면 같은 행이 실측으로 덮이므로
# (drain 이 계속 도니까) 지금 읽는 `estimated` 는 정지 시점의 것이 아니다.
chk N1c "C: **그 정지를 결정한 값이 추정이었다**" true \
  "$(psqlq "select coalesce(bool_or((payload->>'estimated')::boolean)::text,'-') from task_event
            where task_id='$TC' and class='status' and verb='pause'")"
chk N1d "C: 정지 시점 금액쌍이 paused_detail 에 남았다 (한도 \$$EST_BUDGET)" "$EST_BUDGET" \
  "$(eqnum "$(psqlq "select coalesce((paused_detail->'budget'->>'limit_usd'),'-') from session where id='$SC_'")" "$EST_BUDGET")"
chk N2  "C: **세션이 paused(budget)**"                     paused "$(sess_field "$SC_" status)"
chk N2b "C: paused_reason=budget"                          budget "$(sess_field "$SC_" paused_reason)"
chk N3  "C: 활동 피드에 \"진행 중인 턴은 끝까지\" 기록 (E9-05)" 1 \
  "$(psqlq "select count(*) from task_event where task_id='$TC' and class='status' and verb='pause'
            and payload->>'note' like '%진행 중인 턴은 끝까지%'")"
chk N4  "C: 시스템 HITL 1건 (S-48 — 1판은 요청 없이 알림만)" 1 \
  "$(psqlq "select count(*) from hitl_request where session_id='$SC_'")"
if [ -n "$HC" ]; then
chk N4b "C: source=system · type=approval · purpose=budget" "system|approval|budget" \
  "$(psqlq "select source::text||'|'||type::text||'|'||coalesce(purpose,'-') from hitl_request where id='$HC'")"
chk N4c "C: **task_id 가 비어 있다** (세션 범위 — 답은 세션 재개다)" - "$(hitl_field "$HC" task_id)"
chk N4d "C: 타임라인 카드 1장 (S-45)"                      1 \
  "$(psqlq "select count(*) from message where session_id='$SC_' and kind='hitl'")"
chk N5  "C: 인박스는 **HITL 항목 1건**"                    1 \
  "$(psqlq "select count(*) from inbox_item where ref_id='$HC' and type='hitl_request'")"
chk N5b "C: 옛 session_paused 카드는 없다 (한 정지에 카드 한 장)" 0 \
  "$(psqlq "select count(*) from inbox_item i join session s on s.id=i.session_id
            where s.id='$SC_' and i.type='session_paused'")"
fi
# 드레인 — 서버는 진행 중 턴을 끊지 않는다. 턴은 제 할 일을 마치고 정상 종료(`turn_end`/`end_turn`)한다.
# 그 뒤 **데몬 자신의 §5**(FR-7.3 M9)가 실측 총액을 보고 attempt 를 `paused_budget` 으로 닫는다 —
# 서버가 자른 것이 아니다(취소 명령 0).
CST="$(WAIT_S=${DRAIN_WAIT_S:-900} wait_task "$TC" completed failed cancelled paused)"
chk N6  "C: 서버가 진행 중 턴을 끊지 않았다 (턴이 end_turn 으로 제 끝까지 갔다)" yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from task_event
            where task_id='$TC' and class='runtime' and verb='turn_end' and payload->>'stop_reason'='end_turn'")"
chk N6a "C: 결과는 failed·cancelled 가 아니다"             no \
  "$( [ "$(in_set "$CST" failed cancelled)" = yes ] && echo yes || echo no )"
chk N6b "C: 그 뒤로도 서버 취소 명령 0"                    0 \
  "$(psqlq "select count(*) from daemon_command where task_id='$TC' and type='cancel'")"
chk N6c "C: attempt 를 닫은 것은 **데몬의 §5** 다 (outcome=paused_budget)" paused_budget \
  "$(psqlq "select coalesce(outcome,'-') from task_attempt where task_id='$TC' and attempt=1")"
chk N7  "C: 멈춘 세션은 새 task 를 내보내지 않는다 (E5-04 — 이 세션의 dispatch 대기 0건)" 0 \
  "$(psqlq "select count(*) from task where session_id='$SC_' and status in ('dispatched','running')")"
chk N8  "C: heartbeat 이 반복돼도 카드는 한 장이다"        1 \
  "$(psqlq "select count(*) from message where session_id='$SC_' and kind='hitl'")"

step "10. K-10 — C: 그 HITL 승인 한 번이 곧 세션 재개다"
C_SPENT="$(usage_cost "$TC")"
# 먼저: 이미 쓴 금액 이하의 상향은 거절된다(K-10 사전 검증 — 통과시키면 다음 heartbeat 에 같은 정지가
# 다시 걸린다). 답을 내기 전에 두드려야 한다.
read -r RCL RCL_BODY <<<"$(respond_hitl "$HC" '{"approved":true,"budget_override_usd":0.001}')"
chk N9  "이미 쓴 금액(\$$C_SPENT) 이하의 상향은 4xx 다 (K-10 too_low)" yes \
  "$( [ "${RCL:0:1}" = 4 ] && echo yes || echo no )"
chk N9a "그래도 세션은 아직 paused 다"                     paused "$(sess_field "$SC_" status)"
read -r RCC RCC_BODY <<<"$(respond_hitl "$HC" "$(jq -nc --argjson b "$EST_NEW" '{approved:true,budget_override_usd:$b}')")"
chk N10 "승인이 받아들여진다 (HTTP $RCC)" yes "$( [ "${RCC:0:1}" = 2 ] && echo yes || echo no )"
sleep 6
chk N10b "C: 세션이 **active** 로 돌아왔다 (K-10)"         active "$(sess_field "$SC_" status)"
chk N10c "C: limits.budget_usd = 승인 금액 \$$EST_NEW"    "$EST_NEW" \
  "$(eqnum "$(psqlq "select (limits->>'budget_usd') from session where id='$SC_'")" "$EST_NEW")"
chk N10d "C: paused_reason 이 지워졌다"                    - "$(sess_field "$SC_" paused_reason)"

step "11. K-10 · S-46 — D: **세션 범위** 실측 초과 → 웹 승인 → park 된 task 재큐잉"
wait_session_paused "$SD" 900 || bad "D: 세션이 paused 로 가지 않았다 (status=$(sess_field "$SD" status))"
D_COST="$(usage_cost "$TD")"; D_EST="$(usage_est "$TD")"
HD="$(hitl_open "$SD" budget)"; [ -n "$HD" ] || HD="$(psqlq "select id from hitl_request where session_id='$SD' order by created_at desc limit 1")"
log "D: 세션 상한 \$$SESS_LIMIT · 누적 \$$D_COST · estimated=$D_EST"
chk M1  "D: 세션이 paused(budget)"                         paused "$(sess_field "$SD" status)"
chk M1b "D: paused_reason=budget"                          budget "$(sess_field "$SD" paused_reason)"
chk M1c "D: 세션 합계가 상한을 넘었다"                     yes "$(gt "$D_COST" "$SESS_LIMIT")"
chk M1d "D: paused_detail 이 **세션** 금액쌍을 인용한다 (S-48)" "$SESS_LIMIT" \
  "$(eqnum "$(psqlq "select coalesce((paused_detail->'budget'->>'limit_usd'),'-') from session where id='$SD'")" "$SESS_LIMIT")"
chk M2  "D: HITL 의 task_id 가 비어 있다 (세션 범위)"      - "$(hitl_field "$HD" task_id)"
chk M2b "D: purpose=budget · source=system"                "budget|system" \
  "$(psqlq "select coalesce(purpose,'-')||'|'||source::text from hitl_request where id='$HD'")"
chk M2c "D: 타임라인 카드 1장 (S-45)"                      1 \
  "$(psqlq "select count(*) from message where session_id='$SD' and kind='hitl'")"
chk M2d "D: 인박스 항목 1건"                               1 "$(psqlq "select count(*) from inbox_item where ref_id='$HD'")"
chk M3  "D: 실측 초과이므로 취소 **명령**이 나갔다"        yes \
  "$(psqlq "select case when count(*)>0 then 'yes' else 'no' end from daemon_command where task_id='$TD' and type='cancel'")"
chk M3b "D: 그 task 가 park 됐다 (paused(budget))"         "paused|budget" \
  "$(psqlq "select status::text||'|'||coalesce(paused_reason::text,'-') from task where id='$TD'")"
# 멈춘 세션은 새 task 를 내보내지 않는다(E5-04) — 정지 뒤에 트리거를 하나 넣어 본다.
post_message "$SD" "$(mention PeerD "$AG_E") 남은 조각을 이어 써라" >/dev/null
TPD=""
for i in $(seq 1 20); do
  TPD="$(psqlq "select t.id from task t join agent a on a.id=t.agent_id
                where t.session_id='$SD' and a.name='PeerD' order by t.created_at desc limit 1")"
  [ -n "$TPD" ] && break; sleep 2
done
chk M4  "D: 정지 중에도 task 는 만들어진다"                yes "$( [ -n "$TPD" ] && echo yes || echo no )"
sleep 35
chk M4b "D: 그러나 **dispatch 되지 않는다** (E5-04 — 멈춘 세션은 claim 0)" yes \
  "$( [ "$(in_set "$(task_field "$TPD" status)" queued blocked)" = yes ] && echo yes || echo no )"
chk M4c "D: 그 task 의 attempt 는 아직 없다"               0 \
  "$(psqlq "select count(*) from task_attempt where task_id='$TPD'")"
# 웹에서 승인한다 — 세션 범위 상향 입력칸(W-6, scope=session)
ab open "$WEB_URL/inbox" >/dev/null 2>&1 || true
abwait '[data-testid="inbox-page"]' 40 || true
sleep 3
IB_D="$(psqlq "select id from inbox_item where ref_id='$HD' limit 1")"
IT_D="[data-testid=\"inbox-item\"][data-item-id=\"$IB_D\"]"
D_SEL="$IT_D [data-testid=\"hitl-budget-input\"]"
shot "p3-50-04-inbox-session-budget"
chk M5  "D: 인박스 카드에 **세션** 상향 입력칸이 있다 (W-6)" yes \
  "$( [ "$(abcount "$D_SEL")" -ge 1 ] && echo yes || echo no )"
ab fill "$D_SEL" "$SESS_NEW" >/dev/null 2>&1 || true
sleep 1
ab click "$IT_D [data-testid=\"hitl-approve\"]" >/dev/null 2>&1 || true
DEADLINE=$(( $(date +%s) + 60 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do [ "$(hitl_field "$HD" status)" = answered ] && break; sleep 2; done
chk M6  "D: 웹 승인이 도달했다"                            answered "$(hitl_field "$HD" status)"
sleep 6
chk M6b "D: 세션이 active (K-10)"                          active "$(sess_field "$SD" status)"
chk M6c "D: limits.budget_usd = \$$SESS_NEW"               "$SESS_NEW" \
  "$(eqnum "$(psqlq "select (limits->>'budget_usd') from session where id='$SD'")" "$SESS_NEW")"
chk M7  "D: **park 된 task 가 재큐잉됐다** (S-46)"         yes \
  "$( [ "$(in_set "$(task_field "$TD" status)" queued dispatched running completed)" = yes ] && echo yes || echo no )"
DEADLINE=$(( $(date +%s) + 240 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(psqlq "select count(*) from task_attempt where task_id='$TPD'")" -ge 1 ] && break; sleep 4
done
chk M8  "D: 멈춰 있던 다음 task 도 dispatch 된다 (K-10 재dispatch)" yes \
  "$(python3 -c "import sys;print('yes' if int(sys.argv[1])>=1 else 'no')" \
     "$(psqlq "select count(*) from task_attempt where task_id='$TPD'")")"

step "12. E9-10 — H(hermes): 실측 비용이 없는 런타임의 사후 강제"
HST="$(WAIT_S=${HM_WAIT_S:-900} wait_task "$TH" completed failed cancelled paused)"
sleep 8
H_COST="$(usage_cost "$TH")"; H_EST="$(usage_est "$TH")"
HH="$(psqlq "select id from hitl_request where session_id='$SH' order by created_at desc limit 1")"
# 강제가 어느 호출부에서 났는지 — 턴이 살아 있을 때(heartbeat)면 task 가 paused, 끝난 뒤(finish)면 completed.
H_SITE="$( [ "$HST" = completed ] && echo finish_or_prefinish || echo heartbeat )"
log "H: hermes 턴 $HST · 누적 \$$H_COST · estimated=$H_EST · 상한 \$$HM_BUDGET · HITL=${HH:--}"
chk H1  "H: hermes 의 usage 가 서버에 도달한다"            yes "$(gt "$H_COST" 0)"
chk H1b "H: hermes 는 **실측 비용을 주지 않는다** (ACP 에 cost_usd 없음 → 추정)" true "$H_EST"
chk H1c "H: 그 추정도 가격표로 매겨져 상한을 넘었다 (S-48)" yes "$(gt "$H_COST" "$HM_BUDGET")"
chk H2  "H: 세션이 paused(budget)"                         paused "$(sess_field "$SH" status)"
chk H2b "H: 완료한 task 는 그대로다 (completed → paused 전이 없다, E5)" completed "$(task_field "$TH" status)"
chk H3  "H: 시스템 HITL 1건 · purpose=budget"              "system|approval|budget" \
  "$(psqlq "select source::text||'|'||type::text||'|'||coalesce(purpose,'-') from hitl_request where id='$HH'")"
chk H3b "H: 타임라인 카드 1장 (S-45)"                      1 \
  "$(psqlq "select count(*) from message where session_id='$SH' and kind='hitl'")"
chk H3c "H: 인박스 항목 1건"                               1 "$(psqlq "select count(*) from inbox_item where ref_id='$HH'")"
chk H4  "H: 추정 초과이므로 하드 컷이 없다 (취소 명령 0)"  0 \
  "$(psqlq "select count(*) from daemon_command where task_id='$TH' and type='cancel'")"
chk H5  "H: 다음 dispatch 0 (새 attempt 없음)"             1 "$(task_field "$TH" attempt)"
# E9-10 의 나머지 절반(실측 초과가 **finish 뒤** 발견되어 lane 이 paused 되고 HITL 이 task 를 지목하는
# 분기)은 오늘의 런타임으로 실기에 도달하지 않는다 — 실측 금액을 주는 유일한 런타임(claude_code)은
# 그 값을 `finish` **이전**의 heartbeat 으로 보내므로(#145 §5 OnUsage) 언제나 턴-중 분기가 먼저 잡는다.
chk_na H6 "E9-10 실측·사후(lane paused · HITL task_id 채움) 분기" "unit" \
  "실기 도달 불가 — 실측을 주는 런타임이 그 값을 finish 이전 heartbeat 으로 보낸다(#145 OnUsage). 서버 유닛 TestP3BudgetAtFinish* 가 지킨다"

step "13. 4단위 비용 집계 (E9-07 · E9-09 — getSessionCost)"
COST="$(api_ok GET "/sessions/$SA/cost" || echo '{}')"
printf '%s\n' "$COST" > "$OUT/50-cost.json"
chk K1  "getSessionCost 가 응답한다"           yes "$( [ -n "$COST" ] && echo yes || echo no )"
chk K1b "세션 합계가 0 보다 크다"              yes \
  "$(python3 -c "import json;d=json.load(open('$OUT/50-cost.json'))
tot=d.get('total_usd') or d.get('cost_usd') or (d.get('session') or {}).get('cost_usd') or 0
print('yes' if float(tot)>0 else 'no')" 2>/dev/null || echo no)"
psqlq "select t.id, t.status::text, t.attempt, coalesce(u.cost_usd::text,'-'), coalesce(u.estimated::text,'-'),
              coalesce(u.model,'-')
       from task t left join task_usage u on u.task_id=t.id
       where t.session_id in ('$SA','$SB','$SC_','$SD','$SH') order by t.created_at" > "$OUT/50-usage.tsv"
psqlq "select id, status::text, coalesce(paused_reason::text,'-'), coalesce(limits::text,'-'), round(cost_usd::numeric,6)::text
       from session where id in ('$SA','$SB','$SC_','$SD','$SH')" > "$OUT/50-sessions.tsv"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg sa "$SA" --arg sb "$SB" --arg sc "$SC_" --arg sd "$SD" --arg sh "$SH" \
  --arg ta "$TA" --arg tb "$TB" --arg tc "$TC" --arg td "$TD" --arg th "$TH" \
  --arg ha "${HA:-}" --arg hb "${HB_H:-}" --arg hc "${HC:-}" --arg hd "${HD:-}" --arg hh "${HH:-}" \
  --arg budget "$BUDGET" --arg new "$BUDGET_NEW" --arg a1 "${A_COST:-}" --arg a2 "${A2_COST:-}" \
  --arg midcost "${MID_COST:-}" --arg ccost "${C_COST:-}" --arg dcost "${D_COST:-}" --arg hcost "${H_COST:-}" \
  --arg final "${AST:-}" \
  --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,
    approve:{session:$sa,task:$ta,hitl:$ha,limit_usd:$budget,measured_usd:$a1,override_usd:$new,after_resume_usd:$a2,final:$final},
    reject:{session:$sb,task:$tb,hitl:$hb},
    estimated:{session:$sc,task:$tc,hitl:$hc,spent_usd:$ccost},
    session_scope:{session:$sd,task:$td,hitl:$hd,spent_usd:$dcost},
    hermes:{session:$sh,task:$th,hitl:$hh,spent_usd:$hcost},
    midturn_estimate_usd:$midcost,
    elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/50.json"
[ "$fail" = 0 ]
