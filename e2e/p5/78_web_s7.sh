#!/usr/bin/env bash
# e2e/p5/78_web_s7.sh — **웹 S7 한 화면**(세션 상세) headless. 72_ 가 만든 세션을 연다.
#
# 비용 한 줄(I-3): 에이전트 턴 0(72_ 의 세션을 연다) · $0 · ≈ 3s(agent-browser 면 +10s)
#
#   agent-browser 가 있으면(로컬): 로그인 → /rooms/<id> → 3열(서브 미션 보드·타임라인·미션 칸) DOM 판정 + 스크린샷.
#   없으면(CI): next build 산출물이 뜨는지만 — /login 200 · /sessions/<id> → /rooms/<id> 307 · /rooms/<id> 200 · 앱 셸(HTML) 에 마운트 지점.
#   72_ 의 out/72-ids.txt 가 없으면(단독 실행) 새 세션 하나를 API 로 만든다(런타임 없이 — 화면만).
source "$(dirname "$0")/lib_i5.sh"
g5_chk_init "$OUT/78-checks.tsv"
COOKIE="$OUT/cookies-72.txt"
if [ ! -f "$OUT/72-ids.txt" ] || [ ! -f "$COOKIE" ]; then
  STAMP="$(date +%s)"; COOKIE="$OUT/cookies-78.txt"; rm -f "$COOKIE"
  signup "i5w+$STAMP@example.com" password123 Director >/dev/null
  WS="$(create_workspace "G9 Web $STAMP")"
  A="$(api_ok POST "/workspaces/$WS/agents" '{"name":"Lead","role":"lead","role_description":"x","instructions":"y","profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-haiku-4-5-20251001","is_default":true}]}' | jq -r .id)"
  SESSION="$(api "POST" "/workspaces/$WS/sessions" "$(jq -nc --arg a "$A" '{title:"web s7",goal:"화면",isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a,completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | api_body | jq -r '.id // empty')"
  EMAIL="i5w+$STAMP@example.com"
else
  read -r WS SESSION _ _ _ _ EMAIL <<<"$(cat "$OUT/72-ids.txt")"
fi
chk W0 "웹이 떠 있다 (/login 200)" 200 "$(curl -sS -o /dev/null -w '%{http_code}' "$WEB_URL/login")"
# v0.19 R2-W1: 옛 /sessions/<id> 는 /rooms/<id> 로 307 — 옛 주소가 새 방 화면으로 가는지와 새 화면이 뜨는지를 따로 본다.
chk W1r "/sessions/<id> 는 /rooms/<id> 로 307" "307 /rooms/$SESSION" "$(curl -sS -o /dev/null -w '%{http_code} %{redirect_url}' -b "$COOKIE" "$WEB_URL/sessions/$SESSION" | sed -E 's#https?://[^/]+##')"
chk W1 "/rooms/<id> 가 200 (앱 셸)" 200 "$(curl -sS -o "$OUT/78-s7.html" -w '%{http_code}' -b "$COOKIE" "$WEB_URL/rooms/$SESSION")"
chk W1b "앱 셸 HTML 에 Next 마운트가 있다" yes "$(grep -q '__next\|/_next/' "$OUT/78-s7.html" && echo yes || echo no)"
chk W1c "/api/v1 프록시가 서버에 닿는다 (GET /api/v1/sessions/<id> 200)" 200 "$(curl -sS -o /dev/null -w '%{http_code}' -b "$COOKIE" "$WEB_URL/api/v1/sessions/$SESSION")"
if command -v agent-browser >/dev/null 2>&1 && [ -n "${EMAIL:-}" ] && [ "${WITH_BROWSER:-1}" = 1 ]; then
  export AGENT_BROWSER_SESSION="colab-p5-78-$$"
  web_login "$EMAIL" password123
  ab open "$WEB_URL/rooms/$SESSION" >/dev/null
  abwait '[data-testid="lane-board"]' 30 || abwait '[data-testid="room-detail"]' 30 || true
  sleep 2
  chk W2 "S7 lane 보드가 그려진다"        yes "$( [ "$(abcount '[data-testid="lane-board"]')" -ge 1 ] && echo yes || echo no )"
  # v0.19 R2-W2: /rooms/<id> 는 새 방 화면(3열 + 미션 칩 줄)이다 — 옛 세션의 방도 같은 화면을 연다.
  chk W2b "S7 방 화면이 그려진다 (room-detail · 미션 칩 줄)"       yes "$( [ "$(abcount '[data-testid="room-detail"]')" -ge 1 ] && [ "$(abcount '[data-testid="work-chips"]')" -ge 1 ] && echo yes || echo no )"
  chk W2c "메시지 카드 ≥ 1"              yes "$( [ "$(abcount '[data-testid="message-card"]')" -ge 1 ] && echo yes || echo no )"
  mkdir -p "$E2E_ROOT/web/__screenshots__"
  ab screenshot "$E2E_ROOT/web/__screenshots__/p5-78-s7.png" >/dev/null 2>&1 && ok "📸 web/__screenshots__/p5-78-s7.png" || true
  ab close >/dev/null 2>&1 || true
else
  chk_na W2 "S7 DOM 판정" skipped "agent-browser 없음(CI) — next build + 앱 셸 200 까지만"
fi
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
[ "$fail" = 0 ]
