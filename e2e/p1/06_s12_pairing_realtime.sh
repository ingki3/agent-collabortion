#!/usr/bin/env bash
# e2e/p1/06_s12_pairing_realtime.sh — U1 4단계(S12 상태 자동 갱신) 실패 원인 분리. 에이전트 턴 없음(--no-turn).
#  (1) 서버: 워크스페이스 SSE 를 curl 로 구독한 채 API 로 페어링 발급 → daemon pair → `pairing.updated` 프레임이 오는가 (S)
#  (2) 웹: 로그인 후 /runtimes/new(S12 단독) 를 열어 표시된 2행으로 daemon pair → 패널이 ready 로 바뀌는가, 페어링이 몇 개 생기는가 (W)
# 전제: 01_vertical_slice.sh 실행 후(a-ids.txt, cookies-a.txt). 결과: e2e/p1/out/f-summary.json, 스크린샷 web/__screenshots__/p1-s12-*.png
source "$(dirname "$0")/lib.sh"
# 같은 호스트명을 같은 워크스페이스에 두 번 페어링하면 서버가 500(runtime.name 유일 제약 재시도가 같은 tx 안에서 25P02) 이라
# — 그 자체가 S 결함(runtimes.go:158~170; 첫 실행에서 발견) — 검사마다 새 사용자·워크스페이스를 만든다.
STAMP="$(date +%s)"; EMAIL="e2e-f+$STAMP@example.com"; PASS="password123"
COOKIE="$OUT/cookies-f.txt"; rm -f "$COOKIE"
signup "$EMAIL" "$PASS" "e2e-f" >/dev/null; WS="$(create_workspace "e2e-f $STAMP")"; ok "fresh user $EMAIL workspace $WS"
step "(1) 서버 SSE: pairing.updated 프레임"
SSE="$OUT/f-sse.txt"; : > "$SSE"
# 주의: 이전 실행의 curl 이 살아 있으면 같은 파일에 섞여 쓴다(1차 실행에서 프레임이 깨져 보인 원인) → 먼저 정리하고, 종료 시 반드시 죽인다
pkill -f "workspaces/.*/stream" 2>/dev/null || true
curl -sN -b "$COOKIE" -H 'Accept: text/event-stream' "$API/workspaces/$WS/stream" > "$SSE" 2>&1 &
SSE_PID=$!; trap 'kill $SSE_PID 2>/dev/null; agent-browser close >/dev/null 2>&1; true' EXIT   # trap 의 마지막 명령이 종료코드를 정한다 — 이미 죽은 curl 의 kill 실패로 스크립트가 1 을 내지 않도록
sleep 1
IFS=$'\t' read -r PID1 CODE1 < <(create_pairing "$WS")
CFG1="$OUT/daemon-f1.json"; rm -f "$CFG1"
daemon_pair "$CODE1" "$CFG1" "$OUT/work-f1" --no-turn
sleep 3
kill "$SSE_PID" 2>/dev/null || true
N_PAIR="$(grep -c 'pairing.updated' "$SSE" || true)"; N_RT="$(grep -c 'runtime.updated' "$SSE" || true)"
log "SSE 프레임: pairing.updated=$N_PAIR runtime.updated=$N_RT (총 $(grep -c '^data:' "$SSE" || true) data 줄)"
grep -o '"type":"[a-z_.]*"' "$SSE" | sort | uniq -c | sed 's/^/    /' >&2
API_ST="$(pairing_status "$WS" "$PID1")"; log "API GET pairing → $API_ST"
[ "${N_PAIR:-0}" -ge 1 ] && ok "서버가 pairing.updated 를 SSE 로 보냄" || bad "서버 SSE 에 pairing.updated 0 (REST 는 $API_ST) — S: publishPairing 미호출/미전달"

step "(2) 웹 S12 단독 화면(/runtimes/new): 표시된 명령으로 페어링 → 패널 갱신"
# 같은 호스트 재페어링 500 을 피하려 (2) 도 새 사용자·워크스페이스
COOKIE="$OUT/cookies-f2.txt"; rm -f "$COOKIE"; EMAIL="e2e-f2+$STAMP@example.com"
signup "$EMAIL" "$PASS" "e2e-f2" >/dev/null; WS="$(create_workspace "e2e-f2 $STAMP")"; ok "fresh user $EMAIL workspace $WS"
cd "$E2E_ROOT/web"
export AGENT_BROWSER_SESSION="colab-p1-s12-$(date +%s)"
ab() { agent-browser "$@"; }
ab set viewport 1280 900 >/dev/null
ab open "$WEB_URL/login" >/dev/null
ab wait 'input[name=email]' >/dev/null
ab fill 'input[name=email]' "$EMAIL" >/dev/null; ab fill 'input[name=password]' "$PASS" >/dev/null; ab click 'button[type=submit]' >/dev/null
ab wait --url "**/sessions*" --timeout 15000 >/dev/null || log "로그인 후 url=$(ab get url)"
BEFORE="$(psqlq "select count(*) from runtime_pairing where workspace_id='$WS'")"
ab open "$WEB_URL/runtimes/new" >/dev/null
ab wait '[data-testid="install-cmd-2"]' --timeout 15000 >/dev/null
sleep 2
AFTER="$(psqlq "select count(*) from runtime_pairing where workspace_id='$WS'")"
CMD2="$(ab get text '[data-testid="install-cmd-2"] code')"; log "화면 2행: $CMD2"
log "페어링 행 수: $BEFORE → $AFTER (화면 1번 열 때 $((AFTER-BEFORE)) 개 생성)"
[ "$((AFTER-BEFORE))" = 1 ] && ok "페어링 1개 생성" || bad "페어링 $((AFTER-BEFORE)) 개 생성 — W: PairingPanel useEffect create() 중복(StrictMode 이중 실행)"
ab screenshot "__screenshots__/p1-s12-01-waiting.png" >/dev/null
CODE2="$(sed -E 's/.* pair ([^ ]+) --server.*/\1/' <<<"$CMD2")"
CFG2="$OUT/daemon-f2.json"; rm -f "$CFG2"
# E17-09: `daemon pair` 시작부터 패널이 `준비 완료` 를 보일 때까지 10초 안이어야 한다. 시작 시각을 pair 직전에 잡는다.
T0="$(now_ms)"
daemon_pair "$CODE2" "$CFG2" "$OUT/work-f2" --no-turn
SHOWN_ST=""; READY_MS=""
for i in $(seq 1 30); do
  SHOWN_ST="$(ab get attr '[data-testid="pairing-status"]' data-status)"
  [ "$SHOWN_ST" = ready ] && { READY_MS="$(( $(now_ms) - T0 ))"; break; }
  sleep 1
done
READY_S="$(python3 -c "print(f'{${READY_MS:-0}/1000:.1f}')")"
[ -n "$READY_MS" ] && log "pair → 패널 준비 완료: ${READY_S}s (E17-09 기준 10s)"
CMD2_LATER="$(ab get text '[data-testid="install-cmd-2"] code')"
[ "$CMD2_LATER" = "$CMD2" ] && log "화면 2행 불변 (패널 state = 페어링한 것)" || bad "화면 2행이 바뀜 — 처음 표시된 코드로 페어링했지만 패널은 나중 응답(다른 페어링)을 추적: '$CMD2_LATER'"
PAIRED_DB_ST="$(psqlq "select status from runtime_pairing where code_hash=encode(sha256('$CODE2'::bytea),'hex')")"; log "페어링한 코드의 DB 상태=$PAIRED_DB_ST"
ab screenshot "__screenshots__/p1-s12-02-after-pair.png" >/dev/null
[ "$SHOWN_ST" = ready ] && ab screenshot "__screenshots__/p1-s12-03-ready.png" >/dev/null
DB_ST="$(psqlq "select string_agg(status||'@'||to_char(created_at,'HH24:MI:SS'),' ') from runtime_pairing where workspace_id='$WS' and created_at > now() - interval '2 minutes'")"
log "패널 status=$SHOWN_ST (30초 내) · DB 최근 페어링: $DB_ST"
[ "$SHOWN_ST" = ready ] && ok "S12 패널이 ready 로 갱신됨 (${READY_S}s)" || bad "S12 패널이 '$SHOWN_ST' 에 머묾(DB 에는 ready 있음) — 화면이 다른 페어링을 추적하거나 갱신 경로(SSE/폴링) 미동작"
[ -n "$READY_MS" ] && { [ "$READY_MS" -le 10000 ] && ok "E17-09: 10초 안에 준비 완료 (${READY_S}s)" || bad "E17-09: 준비 완료까지 ${READY_S}s — 10초 초과"; }
jq -n --argjson sse_pairing "${N_PAIR:-0}" --argjson sse_runtime "${N_RT:-0}" --arg api_status "$API_ST" --argjson created "$((AFTER-BEFORE))" --arg panel_status "$SHOWN_ST" --arg db "$DB_ST" --argjson shown_changed "$([ "$CMD2_LATER" = "$CMD2" ] && echo false || echo true)" --arg paired_db "$PAIRED_DB_ST" --arg ready_s "${READY_S:-}" \
  '{sse_pairing_updated:$sse_pairing,sse_runtime_updated:$sse_runtime,api_pairing_status:$api_status,pairings_created_by_one_panel:$created,panel_status_after_pair:$panel_status,panel_ready_seconds:($ready_s|if .=="" then null else tonumber end),shown_command_changed:$shown_changed,paired_code_db_status:$paired_db,db_recent_pairings:$db}' | tee "$OUT/f-summary.json"
