#!/usr/bin/env bash
# e2e/p4/65_summary_refusal.sh — T-I4 (e): **세션 요약의 실패 경로** (EVAL E6-11·E6-12, FR-2.4, PRD §8.5).
#
#   S1  refusal  → 활동 피드에 오류(카테고리 그대로) + 세션은 **completed** + 요약 메시지 0 (E6-11)
#   S2  전송 오류 → 같은 결론, 카테고리 `transport_error` (E6-12 — `stop_reason` 이 아예 없다)
#   S3  정상     → 요약 메시지 1개 + `generated_by: platform_llm` (목 엔드포인트가 실제 경로에 있다는 증거)
#   S4  키 없음  → 폴백(행 조립) 요약 1개 + `generated_by: fallback`
#   S5  거절 본문("I can't help with that.")이 **요약으로 게시되지 않는다** (§8.5 폴백 행의 순서)
#
# 플랫폼 LLM 은 목 엔드포인트다. 서버 `llm.FromEnv` 가 `ANTHROPIC_BASE_URL` 로 주소를 바꾸도록
# 이미 열려 있다("so the isolated stack can point at a stub without an API key leaving the machine").
# **대역이 아니다** — 요약 결정·피드·완료는 전부 실서버 코드가 낸다. 흉내 내는 것은 Anthropic API
# 자신뿐이고, 그것을 실호출하면 refusal 을 재현할 수 없다.
#
# 전용 서버 프로세스를 :8106 에 따로 띄운다(같은 Postgres, 스택의 :8105 는 건드리지 않는다).
# 종료는 pid·포트로만(§0-10).
#
# 산출물: out/65-checks.tsv · out/65-mock-*.jsonl
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
SERVER_URL="http://localhost:${P65_PORT:-8106}"; API="$SERVER_URL/api/v1"
MOCK_PORT="${P65_MOCK_PORT:-8117}"
COOKIE="$OUT/cookies-65.txt"; rm -f "$COOKIE"
EMAIL="i4s+$STAMP@example.com"; PASSWORD="password123"
g5_chk_init "$OUT/65-checks.tsv"

SRV_PID=""; MOCK_PID=""
CFG="$OUT/daemon-65.json"; DLOG="$OUT/daemon-65.log"; WORK="$P4_TMP_ROOT/65/work"
cleanup() {
  daemon_stop "$OUT/daemon-65.pid"
  [ -n "$SRV_PID" ] && { kill -TERM -- "-$SRV_PID" 2>/dev/null || kill -TERM "$SRV_PID" 2>/dev/null; }
  [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null
  return 0
}
trap cleanup EXIT
wait_until() { local dl=$(( $(date +%s) + $1 )); shift; while [ "$(date +%s)" -lt "$dl" ]; do eval "$1" && return 0; sleep 1; done; return 1; }

start_mock() { # MODE → pid
  { lsof -ti ":$MOCK_PORT" 2>/dev/null || true; } | xargs -r kill 2>/dev/null || true
  python3 "$P4_DIR/mock_anthropic.py" --port "$MOCK_PORT" --mode "$1" --log "$OUT/65-mock-$1.jsonl" >/dev/null 2>&1 &
  MOCK_PID=$!
  wait_until 20 'curl -fsS -o /dev/null "http://127.0.0.1:'"$MOCK_PORT"'/" 2>/dev/null' || true
  printf '%s' "$MOCK_PID"
}
stop_mock() { [ -n "$MOCK_PID" ] && kill "$MOCK_PID" 2>/dev/null || true; MOCK_PID=""; sleep 1; return 0; }

start_server() { # KEY BASEURL — 없으면 키 없이(폴백)
  [ -n "$SRV_PID" ] && { kill -TERM -- "-$SRV_PID" 2>/dev/null || true; SRV_PID=""; sleep 2; }
  { lsof -ti ":${SERVER_URL##*:}" 2>/dev/null || true; } | xargs -r kill 2>/dev/null || true
  sleep 1
  # `setsid_run` 은 셸 함수라 `env` 로 실행할 수 없다 — 서브셸에서 export 한다.
  SRV_PID="$(
    export ANTHROPIC_API_KEY="$1" ANTHROPIC_BASE_URL="$2" \
      COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" \
      COLAB_SERVER_URL="$SERVER_URL" COLAB_WEB_URL="$WEB_URL" COLAB_SERVER_ADDR=":${SERVER_URL##*:}"
    setsid_run "$OUT/65-server.log" "$BIN/server"
  )"
  wait_until 60 'curl -fsS "'"$SERVER_URL"'/healthz" >/dev/null 2>&1' || die "server did not start (see $OUT/65-server.log)"
}

# run_arm NAME MODE → session id (한 세션을 만들고 complete 한다)
run_arm() {
  local name="$1" s
  s="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "$name" --arg a "$AG" --arg rt "$RUNTIME" \
      '{title:$t,goal:"요약 실패 경로",isolation:{kind:"none"},participants:[{agent_id:$a}],
        assignee_agent_id:$a,runtime_id:$rt,completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
  api POST "/sessions/$s/complete" '{"confirm":true}' -H "Idempotency-Key: $(uuid)" >/dev/null
  printf '%s' "$s"
}
sum_feed() { psqlq "select coalesce(e.object_ref::text,'-')||' '||coalesce(e.payload->>'detail','-')
                    from task_event e join task t on t.id=e.task_id
                    where t.session_id='$1' and e.object_ref::text like '%summary.%' order by e.created_at"; }
feed_ref() { psqlq "select coalesce(e.object_ref::text,'-') from task_event e join task t on t.id=e.task_id
                    where t.session_id='$1' and e.object_ref::text like '%summary.$2%' limit 1"; }

step "0. refusal 모드 목 + 서버 :${SERVER_URL##*:}"
start_mock refusal >/dev/null
start_server "sk-ant-mock-$STAMP" "http://127.0.0.1:$MOCK_PORT"
ok "server pid $SRV_PID · mock pid $MOCK_PID (refusal)"

step "1. 계정 · 런타임 · 에이전트 (턴은 돌지 않는다 — 요약만 잰다)"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G7 Summary $STAMP")"
# 세션 생성이 온라인 런타임 하나를 요구한다(409 no_runtime). **대역을 쓰지 않으려고** 진짜 데몬을
# 하나 붙인다 — 메시지를 한 번도 게시하지 않으므로 task 는 만들어지지 않고 턴도 돌지 않는다.
: > "$DLOG"; rm -rf "$WORK"
read -r PID65 PTOK65 <<<"$(create_pairing "$WS" | tr '\t' ' ')"
PAIR_SERVER="$SERVER_URL" daemon_pair_p4 "$PTOK65" "$CFG" "$WORK" 1
RUNTIME="$(runtime_of_config "$CFG")"
daemon_run "$CFG" "$DLOG" > "$OUT/daemon-65.pid"
wait_pairing "$WS" "$PID65" 300 || die "pairing not ready"
# 세션 생성은 assignee 초기 task 를 만든다(E16-A 1단계). 바로 complete 하므로 그 task 는 claim 되기 전에
# cancelled 되고 턴은 돌지 않는다 — 요약 피드 행이 붙을 task 는 있고 모델 호출은 요약 하나뿐이다.
AG="$(create_agent_p2 "$WS" Writer writer "$LEAD_MODEL" '첫 턴부터 곧바로 colab_status_set 으로 status "done" 만 부르고 턴을 끝낸다.' '요약 실패 경로 실험용')"
ok "ws=$WS agent=$AG runtime=$RUNTIME"

step "2. S1 — refusal → 피드 오류 + completed + 요약 0 (E6-11)"
S1="$(run_arm "refusal-$STAMP")"
sum_feed "$S1" > "$OUT/65-feed-refusal.txt"
chk S1  "세션은 completed (요약 실패가 세션을 붙잡지 않는다)" completed "$(psqlq "select status::text from session where id='$S1'")"
chk S1b "요약 메시지 0개"                                     0 "$(summary_count "$S1")"
chk S1c "피드에 summary.failed 가 있다"                       yes \
  "$( [ "$(feed_ref "$S1" failed)" != "-" ] && echo yes || echo no )"
chk S1d "카테고리가 stop_details.category 그대로 (policy_violation)" yes \
  "$( grep -q 'policy_violation' "$OUT/65-feed-refusal.txt" 2>/dev/null && echo yes || echo no )"
chk S1e "목이 실제로 호출됐다 (요약이 §8.5 경로로 나간다)"   yes \
  "$( [ -s "$OUT/65-mock-refusal.jsonl" ] && echo yes || echo no )"
chk S5  "거절 본문이 요약으로 게시되지 않았다 (§8.5 순서)"    0 \
  "$(psqlq "select count(*) from message where session_id='$S1' and content like '%can''t help%'")"
MOCKREQ="$(head -1 "$OUT/65-mock-refusal.jsonl" 2>/dev/null || echo '{}')"
chk S1f "요약 job 이 light 모델로 나갔다 (§8.5 모델 행)"       claude-sonnet-5 \
  "$(printf '%s' "$MOCKREQ" | jq -r '.model // "-"')"
chk S1g "안정 접두에 cache_control 이 붙었다 (§8.5 캐싱 행)"   true \
  "$(printf '%s' "$MOCKREQ" | jq -r '.has_cache_control // false')"
chk S1h "서버 측 폴백 opt-in 헤더가 있다 (§8.5 폴백 행)"        server-side-fallback-2026-07-01 \
  "$(printf '%s' "$MOCKREQ" | jq -r '.beta // "-"')"

step "3. S2 — 전송 오류 → 같은 결론, 카테고리 transport_error (E6-12)"
stop_mock
start_mock transport >/dev/null
S2="$(run_arm "transport-$STAMP")"
sum_feed "$S2" > "$OUT/65-feed-transport.txt"
chk S2  "세션은 completed"                    completed "$(psqlq "select status::text from session where id='$S2'")"
chk S2b "요약 메시지 0개"                     0 "$(summary_count "$S2")"
chk S2c "피드 카테고리 = transport_error"     yes \
  "$( grep -q 'transport_error' "$OUT/65-feed-transport.txt" 2>/dev/null && echo yes || echo no )"

step "4. S3 — 정상 응답 → 요약 1개 + generated_by platform_llm"
stop_mock
start_mock ok >/dev/null
S3="$(run_arm "ok-$STAMP")"
summary_body "$S3" > "$OUT/65-summary-ok.txt" 2>/dev/null || true
chk S3  "요약 메시지 1개"                       1 "$(summary_count "$S3")"
chk S3b "본문이 목이 준 것이다 (MOCK-SUMMARY-8421)" yes \
  "$( grep -q 'MOCK-SUMMARY-8421' "$OUT/65-summary-ok.txt" 2>/dev/null && echo yes || echo no )"
chk S3c "generated_by = platform_llm"           yes \
  "$( [ "$(feed_ref "$S3" 'generated_by:platform_llm')" != "-" ] && echo yes || echo no )"
chk S3d "세션 completed"                        completed "$(psqlq "select status::text from session where id='$S3'")"

step "5. S4 — 키 없음 → 폴백 요약 1개 + generated_by fallback"
stop_mock
start_server "" ""
S4="$(run_arm "fallback-$STAMP")"
summary_body "$S4" > "$OUT/65-summary-fallback.txt" 2>/dev/null || true
chk S4  "요약 메시지 1개 (키가 없어도 세션은 요약을 받는다)" 1 "$(summary_count "$S4")"
chk S4b "generated_by = fallback"                            yes \
  "$( [ "$(feed_ref "$S4" 'generated_by:fallback')" != "-" ] && echo yes || echo no )"
chk S4c "폴백 요약에도 FR-2.4 네 절이 있다"                  4 \
  "$(psqlq "select (content like '%결정 기록%')::int + (content like '%아티팩트%')::int + (content like '%비용%')::int + (content like '%타임라인%')::int from message where session_id='$S4' and kind='summary'")"

step "결과"
printf '  PASS %d · FAIL %d  (%s)\n' "$pass" "$fail" "$OUT/65-checks.tsv"
exit "$fail"
