#!/usr/bin/env bash
# 적대적 경계 검증 — 아무도 시나리오로 찌르지 않은 자리들.
#
# 목적: "정상 경로가 된다"가 아니라 **되면 안 되는 것이 정말 안 되는가**를 본다.
#   D1 TaskToken 범위(G2 Q8)          D2 워크스페이스 경계(사람 세션)
#   D3 501 표면의 정직성               D4 멱등키 경계
#   D5 미인증 접근                     D6 SSE 인가
#   D7 데몬 토큰 경계                  D8 잘못된 입력이 5xx 가 되지 않는가
#
# 전제: up.sh + 01 이 끝나 out/a-ids.txt · cookies-a.txt 가 있고 서버가 살아 있다.
# 에이전트 턴 0. 산출물 out/g-summary.json
set -euo pipefail
cd "$(dirname "$0")"
. ./lib.sh

OUT="${OUT:-./out}"
API="${API:-http://localhost:8080/api/v1}"
read -r WS SESSION AGENT RUNTIME < "$OUT/a-ids.txt"
CK_A="$OUT/cookies-a.txt"

pass=0; fail=0
chk() { # 설명 기대 실제
  if [ "$2" = "$3" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %-58s %s\n' "$1" "$3"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %-58s got=%s want=%s\n' "$1" "$3" "$2"; fi
}
chk_in() { # 설명 "허용값들" 실제
  case " $2 " in *" $3 "*) pass=$((pass+1)); printf '  \033[32m✓\033[0m %-58s %s\n' "$1" "$3";;
  *) fail=$((fail+1)); printf '  \033[31m✗\033[0m %-58s got=%s want∈{%s}\n' "$1" "$3" "$2";; esac
}
code() { curl -sS -o /dev/null -w '%{http_code}' "$@"; }
ucode() { curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" "$@"; }         # 사람 세션 A
tcode() { curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $1" "${@:2}"; }

echo "▶ D1. TaskToken 범위 (G2 Q8: 그 task 의 세션 안에서 읽기만)"
# 살아 있는 토큰을 하나 만든다: 새 메시지로 task 를 만들고 claim 하지 않은 채 토큰만 뽑을 수는 없으므로
# 01 이 쓰던 워크디렉토리의 마지막 토큰 대신, DB 에서 폐기되지 않은 토큰이 없으면 새 task 를 띄운다.
TOK="$(psqlq "select t.token from task_token t join task k on k.id=t.task_id where t.revoked_at is null order by t.created_at desc limit 1" 2>/dev/null || true)"
if [ -z "${TOK:-}" ]; then
  echo "  (살아 있는 task 토큰이 없다 — 평문을 저장하지 않으므로 D1 은 /cli/context 401 만 확인)"
  chk "폐기·위조 토큰의 /cli/context 는 401" 401 "$(tcode ctk_forged_0000000000000000000000 "$API/cli/context")"
else
  MYSESS="$(psqlq "select k.session_id from task_token t join task k on k.id=t.task_id where t.token='$TOK'")"
  chk "자기 세션 읽기 200"                200 "$(tcode "$TOK" "$API/sessions/$MYSESS")"
  OTHER="$(psqlq "select id from session where id <> '$MYSESS' limit 1")"
  [ -n "$OTHER" ] && chk_in "다른 세션 읽기 차단(403/404)" "403 404" "$(tcode "$TOK" "$API/sessions/$OTHER")"
  chk_in "워크스페이스 목록 차단"          "401 403" "$(tcode "$TOK" "$API/workspaces")"
  chk_in "에이전트 목록 차단"              "401 403 404" "$(tcode "$TOK" "$API/workspaces/$WS/agents")"
  chk_in "인박스 차단"                      "401 403 404" "$(tcode "$TOK" "$API/inbox")"
  chk_in "워크스페이스 설정 차단"          "401 403 404" "$(tcode "$TOK" "$API/workspaces/$WS/settings")"
fi
chk "위조 토큰 401"                        401 "$(tcode ctk_totally_made_up_token_value "$API/cli/context")"

echo
echo "▶ D2. 워크스페이스 경계 (사람 세션 A → 남의 워크스페이스)"
# 두 번째 사용자 B 와 그의 워크스페이스를 만든다(에이전트 턴 0).
CK_B="$OUT/cookies-adv-b.txt"; rm -f "$CK_B"
EMAIL_B="adv+$(date +%s)@example.com"
COOKIE="$CK_B" signup "$EMAIL_B" "pw-adv-12345" "침입자" >/dev/null
WS_B="$(COOKIE="$CK_B" create_workspace "침입자팀")"
chk "B 가 자기 워크스페이스 읽기 200"      200 "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/workspaces/$WS_B")"
chk_in "B 가 A 의 워크스페이스 읽기 차단"  "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/workspaces/$WS")"
chk_in "B 가 A 의 세션 읽기 차단"          "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$SESSION")"
chk_in "B 가 A 의 세션 메시지 읽기 차단"   "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$SESSION/messages")"
chk_in "B 가 A 의 세션에 게시 차단"        "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" -X POST "$API/sessions/$SESSION/messages" --data '{"content":"침입"}')"
chk_in "B 가 A 의 런타임 읽기 차단"        "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/runtimes/$RUNTIME")"
chk_in "B 가 A 의 에이전트 목록 차단"      "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/workspaces/$WS/agents")"
# updateWorkspaceSettings 는 x-phase P2 라 아직 501 이다 — 구현이 없으니 남의 설정도 바뀌지 않는다.
# **P2 에서 구현할 때 authz 를 반드시 넣어야 하며**(P2_BACKLOG S-12), 그때 이 기대를 403/404 로 좁힌다.
chk_in "B 가 A 의 워크스페이스 설정 변경 차단(현재 P2 미구현=501)" "403 404 501" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -H 'Content-Type: application/json' -X PATCH "$API/workspaces/$WS/settings" --data '{}')"

echo
echo "▶ D3. 501 표면 (P1 밖 operation 은 501, 5xx 아님)"
chk "restartLane 501"                      501 "$(ucode -X POST "$API/lanes/00000000-0000-0000-0000-000000000000/restart" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":"x"}')"
chk "previewTriggers 501"                  501 "$(ucode -X POST "$API/sessions/$SESSION/messages/preview" -H 'Content-Type: application/json' --data '{"content":"x"}')"
chk "listInbox 501"                        501 "$(ucode "$API/inbox")"
chk "listHitlRequests 501"                 501 "$(ucode "$API/sessions/$SESSION/hitl-requests")"

echo
echo "▶ D4. 멱등키 경계"
K="$(uuidgen)"
MARK="adv-idem-$K"   # 실행마다 고유 — 카운트가 이전 실행과 섞이지 않게
B1="$(jq -nc --arg c "$MARK" '{content:$c}')"
C1="$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -H "Idempotency-Key: $K" -X POST "$API/sessions/$SESSION/messages" --data "$B1")"
chk "첫 게시 201"                          201 "$C1"
C2="$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -H "Idempotency-Key: $K" -X POST "$API/sessions/$SESSION/messages" --data "$B1")"
chk_in "같은 키+같은 본문 재생 200/201"    "200 201" "$C2"
C3="$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -H "Idempotency-Key: $K" -X POST "$API/sessions/$SESSION/messages" --data "$(jq -nc --arg c "$MARK-DIFFERENT" '{content:$c}')")"
chk "같은 키+다른 본문 422"                422 "$C3"
chk "멱등키 없음 → 4xx"                    "$( [ "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -X POST "$API/sessions/$SESSION/messages" --data '{"content":"no-key"}')" -ge 400 ] && echo yes || echo no)" yes
chk "비UUID 멱등키 422"                    422 "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -H 'Idempotency-Key: task:1:1' -X POST "$API/sessions/$SESSION/messages" --data '{"content":"bad-key"}')"
N_ADV="$(psqlq "select count(*) from message where session_id='$SESSION' and content like '$MARK%'")"
chk "이번 키로 5회 시도해도 저장은 1건"     1 "$N_ADV"

echo
echo "▶ D5. 미인증 접근"
chk "쿠키 없이 세션 읽기 401"              401 "$(code "$API/sessions/$SESSION")"
chk "쿠키 없이 워크스페이스 목록 401"      401 "$(code "$API/workspaces")"
chk "쿠키 없이 게시 401"                   401 "$(code -X POST "$API/sessions/$SESSION/messages" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":"anon"}')"
chk "getMe 401"                            401 "$(code "$API/me")"

echo
echo "▶ D6. SSE 인가 (B 가 A 의 워크스페이스 스트림 구독)"
SSE_B="$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -m 5 -H 'Accept: text/event-stream' "$API/workspaces/$WS/stream" || true)"
chk_in "B 의 A-스트림 구독 차단"           "403 404" "$SSE_B"
chk "쿠키 없이 스트림 401"                 401 "$(curl -sS -o /dev/null -w '%{http_code}' -m 5 -H 'Accept: text/event-stream' "$API/workspaces/$WS/stream" || true)"

echo
echo "▶ D7. 데몬 토큰 경계"
chk "데몬 토큰 없이 claim 401"             401 "$(code -X POST "http://localhost:8080/v1/daemon/runtimes/$RUNTIME/claim" -H 'Content-Type: application/json' --data '{"capacity":1,"wait_ms":0}')"
chk "위조 데몬 토큰 claim 401"             401 "$(code -X POST "http://localhost:8080/v1/daemon/runtimes/$RUNTIME/claim" -H 'Authorization: Bearer cdt_forged' -H 'Content-Type: application/json' --data '{"capacity":1,"wait_ms":0}')"
chk "사람 쿠키로 데몬 claim 차단"          401 "$(ucode -X POST "http://localhost:8080/v1/daemon/runtimes/$RUNTIME/claim" -H 'Content-Type: application/json' --data '{"capacity":1,"wait_ms":0}')"

echo
echo "▶ D8. 잘못된 입력이 5xx 가 되지 않는가"
chk_in "잘못된 uuid 경로"                  "400 404 422" "$(ucode "$API/sessions/not-a-uuid")"
chk_in "없는 세션 uuid"                    "403 404" "$(ucode "$API/sessions/$(uuidgen)")"
chk_in "깨진 JSON 본문"                    "400 422" "$(ucode -X POST "$API/sessions/$SESSION/messages" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":')"
chk_in "빈 본문 게시"                      "400 422" "$(ucode -X POST "$API/sessions/$SESSION/messages" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":""}')"
BIG="$(python3 -c 'print("가"*200000)')"
chk_in "200k 본문(상한 또는 수용)"          "201 400 413 422" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" -X POST "$API/sessions/$SESSION/messages" --data "$(jq -n --arg c "$BIG" '{content:$c}')")"
# 계약은 limit minimum:1 maximum:200 이지만 서버는 범위를 거절하지 않고 **조용히 기본값 50 으로 강제**한다
# (messages·agents·sessions·events 네 저장소 모두 clamp — 자원 고갈 위험은 없다).
# 계약↔구현 불일치이므로 P2_BACKLOG S-11 로 남기고, 여기서는 "5xx 가 아니다 + 결과가 상한을 넘지 않는다"만 본다.
chk_in "음수 limit(현재 clamp)"            "200 400 422" "$(ucode "$API/sessions/$SESSION/messages?limit=-1")"
chk_in "거대 limit(현재 clamp)"            "200 400 422" "$(ucode "$API/sessions/$SESSION/messages?limit=100000")"
N_BIG="$(curl -sS -b "$CK_A" "$API/sessions/$SESSION/messages?limit=100000" | jq '.items|length')"
[ "$N_BIG" -le 200 ] && chk "거대 limit 결과가 상한 200 이하" yes yes || chk "거대 limit 결과가 상한 200 이하" yes no

echo
echo "▶ D9. 서버 5xx 가 하나도 없었는가 (이 스크립트 구간)"
FIVE="$(grep -c '"status":5' "$OUT/server.log" 2>/dev/null || echo 0)"
log "server.log 5xx 라인 누적: $FIVE (참고 — 이전 시나리오 포함)"

echo
printf '결과: PASS %d · FAIL %d\n' "$pass" "$fail"
jq -n --argjson pass "$pass" --argjson fail "$fail" '{adversarial:{pass:$pass,fail:$fail}}' | tee "$OUT/g-summary.json"
[ "$fail" = 0 ]
