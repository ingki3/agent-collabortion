#!/usr/bin/env bash
# e2e/p3/fixtures/g_user_approval_card.sh — T-I3 (g): 시나리오 A 의 마지막 `user_approval` 이
# **정식 HITL 카드로 도는가**.
#
# 두 단계다.
#   1. `e2e/p2/33_approval_completed.sh` 를 **G6 스택 환경으로** 한 번 돌린다(E6-01·03·04 재확인).
#      33_ 은 DB 만 본다 — 승인 HITL 이 열리고, 승인하면 completed 가 되는지.
#   2. 그 세션의 **웹 화면**을 본다(§0-9 DOM): S7 타임라인의 HITL 카드와 S8 인박스 항목.
#      "정식 카드" 의 정의는 SCREEN §4.5 다 — 중앙 타임라인에 `hitl` 카드가 서고, 거기서 답할 수 있다.
#
# 사용: bash e2e/p3/fixtures/g_user_approval_card.sh
# 산출물: out/g-checks.tsv · out/g.json · out/a3-*.{json,tsv}(재실행한 33_ 이 쓰는 이름) ·
#         web/__screenshots__/p3-4g-*.png. (§0-17 의 번호 규칙은 번호 스크립트에 붙는다 — 이것은 픽스처라 `g-` 를 쓴다.)
source "$(cd "$(dirname "$0")/.." && pwd)/lib.sh"
STAMP="$(date +%s)"
g5_chk_init "$OUT/g-checks.tsv"

step "1. e2e/p2/33_approval_completed.sh 를 G6 스택으로 재실행"
RC=0
SERVER_URL="$SERVER_URL" WEB_URL="$WEB_URL" PG_PORT="$PG_PORT" PG_CONTAINER="$PG_CONTAINER" E2E_OUT="$OUT" \
  bash "$E2E_ROOT/e2e/p2/33_approval_completed.sh" > "$OUT/g-33.log" 2>&1 || RC=$?
tail -5 "$OUT/g-33.log" >&2
A3_PASS="$(grep -oE 'PASS [0-9]+' "$OUT/g-33.log" | tail -1 | awk '{print $2}')"
A3_FAIL="$(grep -oE 'FAIL [0-9]+' "$OUT/g-33.log" | tail -1 | awk '{print $2}')"
chk G0 "33_ 이 통과한다 (E6-01·03·04 재확인) — PASS ${A3_PASS:-?} · FAIL ${A3_FAIL:-?}" 0 "${A3_FAIL:-1}"
read -r WS SESSION SESSION_R WRTR RUNTIME < "$OUT/a3-ids.txt"
# Director 이메일은 33_ 이 로그에 찍지 않는다 — DB 에서 그 워크스페이스의 첫 멤버로 찾는다.
EMAIL="$(psqlq "select u.email from app_user u join member m on m.user_id=u.id
                where m.workspace_id='$WS' order by m.created_at limit 1")"
HITL="$(psqlq "select id from hitl_request where session_id='$SESSION' and source='system' order by created_at desc limit 1")"
ok "session=$SESSION hitl=$HITL director=$EMAIL"
# API 관측은 그 Director 로 로그인해서 한다 — 기본 쿠키(out/cookies.txt)는 33_ 의 계정이 아니다.
COOKIE="$OUT/cookies-g.txt"; rm -f "$COOKIE"
login "$EMAIL" password123
chk G1  "완료 승인 HITL 이 있다 (source=system)"        system "$(hitl_field "$HITL" source)"
chk G1b "purpose=user_approval (0012 — 예산·루프와 갈린다)" user_approval "$(hitl_field "$HITL" purpose)"

step "2. 웹 — 그 승인이 **정식 HITL 카드**로 도는가 (S7 타임라인 · S8 인박스)"
export AGENT_BROWSER_SESSION="colab-g6-4g-$STAMP"
ab set viewport 1440 1000 >/dev/null 2>&1 || true
web_login "$EMAIL" password123 >/dev/null 2>&1 || bad "웹 로그인 실패"
ab open "$WEB_URL/sessions/$SESSION" >/dev/null 2>&1 || true
abwait '[data-testid="timeline"]' 40 || true
sleep 3
shot "p3-4g-01-session-timeline"
CARDS="$(abcount '[data-testid="hitl-card"]')"
chk G2  "**S7 타임라인에 HITL 카드가 렌더된다** (SCREEN §4.5 중앙 표)" yes \
  "$( [ "${CARDS:-0}" -ge 1 ] && echo yes || echo no )"
chk G2b "그 근거: hitl_request.message_id 가 채워져 있다 (타임라인 카드 메시지)" yes \
  "$( [ "$(psqlq "select case when message_id is null then 'no' else 'yes' end from hitl_request where id='$HITL'")" = yes ] && echo yes || echo no )"
chk G2c "타임라인에 kind='hitl' 메시지가 있다"          1 \
  "$(psqlq "select count(*) from message where session_id='$SESSION' and kind='hitl'")"
ab open "$WEB_URL/inbox" >/dev/null 2>&1 || true
abwait '[data-testid="inbox-page"]' 40 || true
shot "p3-4g-02-inbox"
IB="$(abcount '[data-testid="inbox-item"]')"
chk G3  "S8 인박스에는 항목이 뜬다"                      yes "$( [ "${IB:-0}" -ge 1 ] && echo yes || echo no )"
chk G3b "인박스 카드가 purpose 를 싣는다 (K-9 — 웹이 상세를 한 번 더 읽지 않아도 된다)" user_approval \
  "$(api_ok GET "/inbox?limit=50" | jq -r --arg r "$HITL" '[.items[]|select(.ref_id==$r)][0].card.purpose // "-"')"
INBOX_TXT="$(abget get text '[data-testid="inbox-list"]' | tr '\n' ' ')"
printf '%s\n' "$INBOX_TXT" > "$OUT/g-inbox.txt"
log "인박스 본문: $(head -c 200 <<<"$INBOX_TXT")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg session "$SESSION" --arg hitl "$HITL" \
  --argjson a3_pass "${A3_PASS:-0}" --argjson a3_fail "${A3_FAIL:-0}" --argjson cards "${CARDS:-0}" \
  --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,session:$session,hitl:$hitl,rerun_33:{pass:$a3_pass,fail:$a3_fail},
    timeline_hitl_cards:$cards,pass:$pass,fail:$fail}' | tee "$OUT/g.json"
[ "$fail" = 0 ]
