#!/usr/bin/env bash
# e2e/p2/12_mock_vs_real.sh — `web/e2e/p2-mock.sh` 를 **BASE_URL 만 바꿔** 실서버에 돌린다(T-W2 가 그렇게 돈다고 했다).
#
# 목적: 웹이 보는 목 API 와 실서버가 **다른 모양**을 주는 지점을 찾는다. 그 차이가 곧 결함이다
# (P1 에서 같은 부류로 7건이 나왔다). 웹 코드는 건드리지 않는다 — 계정만 실서버에 만들어 준다.
#
# 사용: bash e2e/p2/up.sh && bash e2e/p2/12_mock_vs_real.sh
# 산출물: out/mock-real.txt (목 실행), out/real-run.txt (실서버 실행), out/mock-vs-real.tsv (행별 대조)
source "$(dirname "$0")/lib.sh"
trap '[ -f "$OUT/daemon-demo.pid" ] && kill -TERM -- "-$(cat "$OUT/daemon-demo.pid")" 2>/dev/null; true' EXIT
STAMP="$(date +%s)"
MOCK_PORT="${MOCK_PORT:-3111}"
# **실행마다 새 계정**을 쓴다. 목은 `/__mock/reset` 으로 초기화되지만 실서버에는 그런 것이 없어
# 한 계정을 재사용하면 자국이 쌓인다 — p2-mock.sh 가 마지막에 켜는 킬 스위치, 매번 3명씩 늘어나는
# 템플릿 에이전트, 죽은 런타임. 그 상태에서 `agents[0:2]` 가 무엇을 고를지는 실행마다 달라진다
# (2026-09-06 실측: 두 번째 실행부터 세션 생성이 403 not_invitable, 매핑이 unmapped).
DEMO_EMAIL="${E2E_EMAIL:-demo+$STAMP@colab.dev}"; DEMO_PW="${E2E_PASSWORD:-password123}"

step "1. 실서버에 데모 계정·워크스페이스 준비 (p2-mock.sh 가 그 계정으로 로그인한다)"
COOKIE="$OUT/cookies-demo.txt"; rm -f "$COOKIE"
if ! login "$DEMO_EMAIL" "$DEMO_PW" 2>/dev/null; then
  signup "$DEMO_EMAIL" "$DEMO_PW" "데모" >/dev/null
fi
if [ "$(api_ok GET /workspaces | jq 'length')" = 0 ]; then create_workspace "Demo $STAMP" >/dev/null; fi
WS="$(api_ok GET /workspaces | jq -r '.[0].id')"
# 목은 워크스페이스에 런타임 1대와 에이전트 3명을 이미 갖고 있다. 실서버에는 없으므로 같은 출발선을 만들어 준다 —
# 그러지 않으면 첫 행(agent-templates)에서 무너져 뒤의 30행이 전부 "빈 문자열" 이 되고, 무엇이 진짜 갈린 지점인지 안 보인다.
# 목은 `/__mock/reset` 으로 매 실행 초기화된다. 실서버에는 그런 것이 없어 **이전 실행의 자국**이 남는다 —
# p2-mock.sh 는 마지막에 킬 스위치(respond_to: nobody)를 켜고 끄지 않으므로, 두 번째 실행은 세션 생성에서
# 403 not_invitable 로 무너진다(2026-09-06 실측). 실행 전에 이 워크스페이스의 자국을 되돌린다.
# 런타임이 **온라인**이어야 한다 — 템플릿 매핑이 probe 의 모델 목록을 읽는다(offline 이면 unmapped).
if [ "$(api_ok GET "/workspaces/$WS/runtimes" | jq '[.[]|select(.status=="online")]|length')" = 0 ]; then
  read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
  rm -f "$OUT/daemon-demo.json"; mkdir -p "$OUT/work-demo"
  COLAB_DAEMON_CONFIG="$OUT/daemon-demo.json" "$BIN/daemon" pair "$PTOK" --server "$SERVER_URL" --workdir-root "$OUT/work-demo" 2>&1 | tail -1 >&2   # PONG 턴을 돈다: models 가 있어야 템플릿 매핑이 mapped
  # **PONG 턴을 도는 daemon_start_p2 여야 한다.** `--no-turn` 으로 띄우면 capabilities[].models 가
  # 빈 배열로 덮이고, 그러면 팀 템플릿 매핑이 unmapped → apply 가 프로파일 없는 에이전트를 만들고
  # → 세션 생성이 422 no_profile 로 무너져 뒤의 20행이 전부 무의미해진다(G4_REPORT S-12·D-2).
  daemon_start_p2 "$OUT/daemon-demo.json" "$OUT/daemon-demo.log" > "$OUT/daemon-demo.pid"
  wait_pairing "$WS" "$PID_" 90 || bad "pairing not ready"
fi
if [ "$(api_ok GET "/workspaces/$WS/agents" | jq '(.items // .)|length')" -lt 2 ]; then
  source "$P2_DIR/fixtures/scenario_a_agents.sh"
  create_agent_p2 "$WS" Lead lead "$LEAD_MODEL" "$LEAD_INS" >/dev/null
  create_agent_p2 "$WS" Researcher researcher "$LEAD_MODEL" "$RES_INS" >/dev/null
  create_agent_p2 "$WS" Writer writer "$LEAD_MODEL" "$WRITER_INS" >/dev/null
fi
ok "demo 준비됨 (ws=$WS runtimes=$(api_ok GET "/workspaces/$WS/runtimes" | jq 'length') agents=$(api_ok GET "/workspaces/$WS/agents" | jq '(.items // .)|length'))"

step "2. 실서버 실행 — BASE_URL=$SERVER_URL MOCK=0"
( cd "$E2E_ROOT/web" && BASE_URL="$SERVER_URL" MOCK=0 E2E_EMAIL="$DEMO_EMAIL" E2E_PASSWORD="$DEMO_PW" bash e2e/p2-mock.sh ) > "$OUT/real-run.txt" 2>&1 || true
sed -n '1,200p' "$OUT/real-run.txt" >&2

step "3. 목 실행 — 같은 스크립트, COLAB_MOCK_API=1 next dev :$MOCK_PORT"
if ! curl -fsS -o /dev/null "http://localhost:$MOCK_PORT/login" 2>/dev/null; then
  ( cd "$E2E_ROOT/web" && COLAB_MOCK_API=1 setsid_run "$OUT/web-mock.log" npx next dev -p "$MOCK_PORT" ) > "$OUT/web-mock.pid"
  for i in $(seq 1 180); do curl -fsS -o /dev/null "http://localhost:$MOCK_PORT/login" 2>/dev/null && break; sleep 1; done
fi
( cd "$E2E_ROOT/web" && BASE_URL="http://localhost:$MOCK_PORT" MOCK=1 bash e2e/p2-mock.sh ) > "$OUT/mock-run.txt" 2>&1 || true

step "4. 행별 대조 (목에만 있는 `seed-lanes` 행은 제외)"
norm() { grep -E '^[^ ].{0,60} +(OK|FAIL)' "$1" | sed -E 's/  +/\t/' ; }
join -t $'\t' -a1 -a2 -e '(없음)' -o 0,1.2,2.2 \
  <(norm "$OUT/mock-run.txt" | sort -t $'\t' -k1,1) \
  <(norm "$OUT/real-run.txt" | sort -t $'\t' -k1,1) > "$OUT/mock-vs-real.tsv" || true
{ printf 'check\tmock\treal\n'; cat "$OUT/mock-vs-real.tsv"; } | column -t -s $'\t' >&2
DIFF="$(awk -F'\t' '$2!=$3' "$OUT/mock-vs-real.tsv" | wc -l | tr -d ' ')"
log "목과 실서버가 갈리는 행: $DIFF"
awk -F'\t' '$2!=$3' "$OUT/mock-vs-real.tsv" | column -t -s $'\t' >&2
jq -n --argjson diverged "$DIFF" \
  --arg mock_verdict "$(tail -1 "$OUT/mock-run.txt")" --arg real_verdict "$(tail -1 "$OUT/real-run.txt")" \
  '{diverged_rows:$diverged,mock:$mock_verdict,real:$real_verdict}' | tee "$OUT/mock-vs-real.json"
