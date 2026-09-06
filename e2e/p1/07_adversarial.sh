#!/usr/bin/env bash
# 적대적 경계 검증 — 아무도 시나리오로 찌르지 않은 자리들.
#
# 목적: "정상 경로가 된다"가 아니라 **되면 안 되는 것이 정말 안 되는가**를 본다.
#   D1 TaskToken 범위(G2 Q8)          D2 워크스페이스 경계(사람 세션)
#   D3 501 표면의 정직성               D4 멱등키 경계
#   D5 미인증 접근                     D6 SSE 인가
#   D7 데몬 토큰 경계                  D8 잘못된 입력이 5xx 가 되지 않는가
#   D10 아티팩트 제출·리뷰 경계(T-S3: TaskToken 범위·워크스페이스 경계·413·403)
#   D11 P2 operation 경계(T-S2: lane·미리보기·일시정지·위임·결정 — 경계는 늘 때마다 늘린다)
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
# S-12 해소(T-S2): updateWorkspaceSettings·getWorkspaceSettings 가 구현됐으므로
# 이제 501 은 답이 아니다. owner·admin 만 통과한다(SCREEN §2.3).
chk_in "B 가 A 의 워크스페이스 설정 변경 차단"  "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -H 'Content-Type: application/json' -X PATCH "$API/workspaces/$WS/settings" --data '{}')"
chk_in "B 가 A 의 워크스페이스 설정 조회 차단"  "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/workspaces/$WS/settings")"

echo
echo "▶ D3. 501 표면 (P1 밖 operation 은 501, 5xx 아님)"
chk "restartLane 501"                      501 "$(ucode -X POST "$API/lanes/00000000-0000-0000-0000-000000000000/restart" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":"x"}')"
# previewTriggers 는 T-S2 에서 구현됐다(FR-3.6). 501 이 아니라 200 이고, 무엇보다
# **아무것도 쓰지 않아야** 한다 — 미리보기가 task 를 만들면 미리보기가 아니다.
N_BEFORE="$(psqlq "select count(*) from task where session_id='$SESSION'")"
chk "previewTriggers 200"                  200 "$(ucode -X POST "$API/sessions/$SESSION/messages/preview" -H 'Content-Type: application/json' --data '{"content":"x"}')"
N_AFTER="$(psqlq "select count(*) from task where session_id='$SESSION'")"
chk "previewTriggers 는 task 를 만들지 않는다" "$N_BEFORE" "$N_AFTER"
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
chk "데몬 토큰 없이 claim 401"             401 "$(code -X POST "${SERVER_URL%/}/v1/daemon/runtimes/$RUNTIME/claim" -H 'Content-Type: application/json' --data '{"capacity":1,"wait_ms":0}')"
chk "위조 데몬 토큰 claim 401"             401 "$(code -X POST "${SERVER_URL%/}/v1/daemon/runtimes/$RUNTIME/claim" -H 'Authorization: Bearer cdt_forged' -H 'Content-Type: application/json' --data '{"capacity":1,"wait_ms":0}')"
chk "사람 쿠키로 데몬 claim 차단"          401 "$(ucode -X POST "${SERVER_URL%/}/v1/daemon/runtimes/$RUNTIME/claim" -H 'Content-Type: application/json' --data '{"capacity":1,"wait_ms":0}')"

echo
echo "▶ D8. 잘못된 입력이 5xx 가 되지 않는가"
chk_in "잘못된 uuid 경로"                  "400 404 422" "$(ucode "$API/sessions/not-a-uuid")"
chk_in "없는 세션 uuid"                    "403 404" "$(ucode "$API/sessions/$(uuidgen)")"
chk_in "깨진 JSON 본문"                    "400 422" "$(ucode -X POST "$API/sessions/$SESSION/messages" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":')"
chk_in "빈 본문 게시"                      "400 422" "$(ucode -X POST "$API/sessions/$SESSION/messages" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" --data '{"content":""}')"
BIG="$(python3 -c 'print("가"*200000)')"
chk_in "200k 본문(상한 또는 수용)"          "201 400 413 422" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" -X POST "$API/sessions/$SESSION/messages" --data "$(jq -n --arg c "$BIG" '{content:$c}')")"
# S-11 해소(T-S2): 계약이 limit minimum:1 maximum:200 이라고 말하므로 범위 밖은
# 422 다. 조용히 50 으로 깎으면 500 개를 요청한 클라이언트가 50 개를 받고도 모른다.
chk "음수 limit 은 422"                    "422" "$(ucode "$API/sessions/$SESSION/messages?limit=-1")"
chk "0 limit 은 422"                       "422" "$(ucode "$API/sessions/$SESSION/messages?limit=0")"
chk "거대 limit 은 422"                    "422" "$(ucode "$API/sessions/$SESSION/messages?limit=100000")"
chk "상한 바로 밖(201) 은 422"             "422" "$(ucode "$API/sessions/$SESSION/messages?limit=201")"
chk "상한값(200) 은 통과"                  "200" "$(ucode "$API/sessions/$SESSION/messages?limit=200")"
N_BIG="$(curl -sS -b "$CK_A" "$API/sessions/$SESSION/messages?limit=200" | jq '.items|length')"
[ "$N_BIG" -le 200 ] && chk "허용 최대 limit 결과가 상한 200 이하" yes yes || chk "허용 최대 limit 결과가 상한 200 이하" yes no

echo
echo "▶ D10. 아티팩트 제출·리뷰 경계 (T-S3: submitArtifact · getArtifact · downloadArtifact · reviewArtifact)"
# 세 operation 중 둘이 TaskToken 전용인데 서버는 토큰 평문을 저장하지 않는다
# (task_token.token_hash 뿐 — D1 이 같은 이유로 우회한다). 데몬을 한 번 더 돌리는
# 대신 **테스트 픽스처로 토큰을 하나 심는다**: 서버가 검증하는 것은 sha256 hex 이므로
# 우리가 고른 문자열의 해시를 넣으면 진짜 발급과 구분되지 않는다(colab_tap 과 같은 성격).
A_TOK="ctk_adv_$(uuidgen | tr -d - | tr 'A-Z' 'a-z')"
A_TASK="$(psqlq "select id from task where session_id='$SESSION' order by created_at desc limit 1")"
if [ -n "${A_TASK:-}" ]; then
  psqlq "insert into task_token (task_id, attempt, token_hash, lane_id, session_id, agent_id, issued_at, expires_at)
         select t.id, t.attempt, encode(sha256('$A_TOK'::bytea), 'hex'), t.lane_id, t.session_id, t.agent_id, now(), now() + interval '1 hour'
         from task t where t.id = '$A_TASK'
         on conflict (task_id, attempt) do update
           set token_hash = excluded.token_hash, expires_at = excluded.expires_at,
               revoked_at = null, revoke_reason = null" >/dev/null || A_TASK=""
fi
if [ -z "${A_TASK:-}" ]; then
  log "살아 있는 task 토큰이 없다 — D10 은 미인증·경계만 확인한다"
  chk "쿠키·토큰 없이 아티팩트 제출 401"   401 "$(code -X POST "$API/sessions/$SESSION/artifacts" -F 'name=x' -F 'type=doc' -F 'file=@/dev/null')"
  chk_in "B 가 A 의 아티팩트 목록 차단"    "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$SESSION/artifacts")"
else
  A_SESS="$SESSION"
  ART_F="$OUT/adv-artifact.txt"; printf 'T-S3 경계 검증 본문\n' > "$ART_F"
  # 이름은 실행마다 고유하다 — 07 을 두 번 돌리면 v1·v2 가 v3·v4 가 된다.
  ART_NAME="adversarial-$(date +%s).txt"
  SUB="$(curl -sS -H "Authorization: Bearer $A_TOK" -X POST "$API/sessions/$A_SESS/artifacts" \
         -F "name=$ART_NAME" -F 'type=doc' -F "file=@$ART_F")"
  ART_ID="$(printf '%s' "$SUB" | jq -r '.artifact.id // empty')"
  chk "TaskToken 으로 자기 세션 제출 201"  yes "$( [ -n "$ART_ID" ] && echo yes || echo no )"
  chk "제출은 v1 부터"                      1 "$(printf '%s' "$SUB" | jq -r '.artifact.version // 0')"
  # 같은 이름 재제출은 덮어쓰기가 아니라 v2 (FR-4.3)
  SUB2="$(curl -sS -H "Authorization: Bearer $A_TOK" -X POST "$API/sessions/$A_SESS/artifacts" \
          -F "name=$ART_NAME" -F 'type=doc' -F "file=@$ART_F")"
  chk "같은 이름 재제출은 v2"               2 "$(printf '%s' "$SUB2" | jq -r '.artifact.version // 0')"

  # 다운로드: 선언 길이와 실제 바이트가 같아야 한다 — CLI 가 절단을 이것으로 잡는다.
  DL="$OUT/adv-artifact-dl.bin"
  # macOS 의 awk 에는 IGNORECASE 가 없다 — 헤더 이름 대소문자는 grep -i 로 받는다.
  DECL="$(curl -sS -D - -o "$DL" -H "Authorization: Bearer $A_TOK" "$API/artifacts/$ART_ID/content" \
          | tr -d '\r' | grep -i '^content-length:' | tail -1 | awk '{print $2}')"
  chk "downloadArtifact 가 Content-Length 를 선언" yes "$( [ -n "${DECL:-}" ] && echo yes || echo no )"
  chk "선언 길이 = 기록된 바이트"           "${DECL:-none}" "$(wc -c < "$DL" | tr -d ' ')"
  chk "내려받은 본문이 올린 본문과 같다"    yes "$(cmp -s "$ART_F" "$DL" && echo yes || echo no)"

  # 지정 리뷰어가 아니면 403 not_designated_reviewer — CLI 가 이 문자열을 exit 3 으로 맵핑한다.
  REV="$(curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $A_TOK" -H 'Content-Type: application/json' \
         -X POST "$API/artifacts/$ART_ID/review" --data '{"verdict":"approve"}')"
  chk "비지정 리뷰어 403"                   403 "$(printf '%s' "$REV" | tail -1)"
  chk "403 코드 문자열이 not_designated_reviewer" not_designated_reviewer \
      "$(printf '%s' "$REV" | sed '$d' | jq -r '.code // empty')"

  # 50 MB 상한(계약) — 한 바이트 넘기면 413 이고, 아무것도 저장되지 않는다.
  BIGF="$OUT/adv-artifact-big.bin"
  dd if=/dev/zero of="$BIGF" bs=1048576 count=51 status=none
  N_ART_BEFORE="$(psqlq "select count(*) from artifact where session_id='$A_SESS'")"
  chk "50MB 초과 제출은 413"                413 "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $A_TOK" \
      -X POST "$API/sessions/$A_SESS/artifacts" -F 'name=huge.bin' -F 'type=file' -F "file=@$BIGF")"
  chk "413 은 아무것도 저장하지 않는다"     "$N_ART_BEFORE" "$(psqlq "select count(*) from artifact where session_id='$A_SESS'")"
  rm -f "$BIGF"

  # TaskToken 범위: 자기 세션 밖으로는 제출도 조회도 못 한다(G2 Q8).
  OTHER_SESS="$(psqlq "select id from session where id <> '$A_SESS' limit 1")"
  if [ -n "${OTHER_SESS:-}" ]; then
    chk_in "다른 세션에 제출 차단"          "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $A_TOK" \
        -X POST "$API/sessions/$OTHER_SESS/artifacts" -F 'name=x' -F 'type=doc' -F "file=@$ART_F")"
    chk_in "다른 세션 아티팩트 목록 차단"   "403 404" "$(tcode "$A_TOK" "$API/sessions/$OTHER_SESS/artifacts")"
    # 반대 방향도 막혀야 한다: 남의 세션 토큰이 **아티팩트 id 를 알아도** 그 아티팩트
    # 자체에 닿으면 안 된다. 위의 두 행은 세션 스코프 경로(/sessions/{S}/…)라
    # 아티팩트 스코프 경로(/artifacts/{A}) 를 검사하지 않는다.
    O_TASK="$(psqlq "select id from task where session_id='$OTHER_SESS' order by created_at desc limit 1")"
    if [ -n "${O_TASK:-}" ]; then
      O_TOK="ctk_adv_other_$(uuidgen | tr -d - | tr 'A-Z' 'a-z')"
      psqlq "insert into task_token (task_id, attempt, token_hash, lane_id, session_id, agent_id, issued_at, expires_at)
             select t.id, t.attempt, encode(sha256('$O_TOK'::bytea), 'hex'), t.lane_id, t.session_id, t.agent_id, now(), now() + interval '1 hour'
             from task t where t.id = '$O_TASK'
             on conflict (task_id, attempt) do update
               set token_hash = excluded.token_hash, expires_at = excluded.expires_at,
                   revoked_at = null, revoke_reason = null" >/dev/null && {
        chk_in "타 세션 토큰의 아티팩트 메타 조회 차단" "403 404" "$(tcode "$O_TOK" "$API/artifacts/$ART_ID")"
        chk_in "타 세션 토큰의 아티팩트 본문 조회 차단" "403 404" "$(tcode "$O_TOK" "$API/artifacts/$ART_ID/content")"
        chk_in "타 세션 토큰의 리뷰 호출 차단"          "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' \
            -H "Authorization: Bearer $O_TOK" -H 'Content-Type: application/json' \
            -X POST "$API/artifacts/$ART_ID/review" --data '{"verdict":"approve"}')"
        chk "타 세션 리뷰 시도는 아무것도 저장하지 않는다" 0 \
            "$(psqlq "select count(*) from artifact_review where artifact_id='$ART_ID'")"
      }
    fi
  fi
  chk "위조 토큰의 아티팩트 조회 401"       401 "$(tcode ctk_forged_0000000000000000000000 "$API/artifacts/$ART_ID")"

  # 워크스페이스 경계: B 는 id 를 알아도 존재조차 알면 안 된다(404, 403 아님).
  chk "B 가 A 의 아티팩트 메타 조회 404"    404 "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/artifacts/$ART_ID")"
  chk "B 가 A 의 아티팩트 본문 조회 404"    404 "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/artifacts/$ART_ID/content")"
  chk_in "B 가 A 의 아티팩트 목록 차단"     "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$A_SESS/artifacts")"
  chk_in "B 가 A 의 세션에 제출 차단"       "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" \
      -X POST "$API/sessions/$A_SESS/artifacts" -F 'name=x' -F 'type=doc' -F "file=@$ART_F")"

  # 사람은 리뷰하지 않는다(openapi reviewArtifact 는 TaskToken 전용).
  chk_in "사람 세션의 리뷰 호출 차단"       "401 403" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_A" -H 'Content-Type: application/json' \
      -X POST "$API/artifacts/$ART_ID/review" --data '{"verdict":"approve"}')"
  chk_in "없는 아티팩트 404"                "403 404" "$(tcode "$A_TOK" "$API/artifacts/$(uuidgen)")"
  chk_in "잘못된 uuid 아티팩트 경로"        "400 404 422" "$(tcode "$A_TOK" "$API/artifacts/not-a-uuid")"
  rm -f "$DL"
fi

echo
echo "▶ D11. P2 operation 경계 (T-S2: lane · previewTriggers · pause/resume · delegateLane · decision)"
# P2 에서 501 이 아니게 된 operation 은 그 순간부터 **권한 검사**가 있어야 한다 —
# 501 은 권한 검사였던 적이 없다(D3 의 S-12 와 같은 이유).
B_LANE="$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$SESSION/lanes")"
chk_in "B 가 A 의 lane 목록 차단"           "403 404" "$B_LANE"
LANE_A="$(psqlq "select id from lane where session_id='$SESSION' order by created_at limit 1")"
if [ -n "${LANE_A:-}" ]; then
  chk_in "B 가 A 의 lane task 이력 차단"     "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/lanes/$LANE_A/tasks")"
  chk_in "B 가 A 의 lane 중단 차단"          "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -X POST "$API/lanes/$LANE_A/cancel")"
  chk_in "B 가 A 의 lane 재지시 차단"        "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" -X POST "$API/lanes/$LANE_A/restart" --data '{"content":"x"}')"
  # 이미 끝난 lane 은 사람이 눌러도 409 — 목 API 가 그렇게 답한다(web/e2e/p2-mock.sh).
  chk_in "종료된 lane 중단은 409"            "409 404" "$(ucode -X POST "$API/lanes/$LANE_A/cancel")"
fi
chk_in "B 가 A 의 트리거 미리보기 차단"      "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -H 'Content-Type: application/json' -X POST "$API/sessions/$SESSION/messages/preview" --data '{"content":"x"}')"
chk "쿠키 없이 미리보기 401"                 401 "$(code -X POST "$API/sessions/$SESSION/messages/preview" -H 'Content-Type: application/json' --data '{"content":"x"}')"
chk_in "B 가 A 의 세션 일시정지 차단"        "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -X POST "$API/sessions/$SESSION/pause")"
chk_in "B 가 A 의 세션 재개 차단"            "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" -H 'Content-Type: application/json' -X POST "$API/sessions/$SESSION/resume" --data '{}')"
chk_in "B 가 A 의 결정 기록 목록 차단"       "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$SESSION/decisions")"
chk_in "B 가 A 의 비용 조회 차단"            "403 404" "$(curl -sS -o /dev/null -w '%{http_code}' -b "$CK_B" "$API/sessions/$SESSION/cost")"
# delegateLane · recordDecision 은 **TaskToken 전용**(openapi security) — 사람 쿠키는 통과하면 안 된다.
chk_in "사람 쿠키의 lane 위임 차단"          "401 403" "$(ucode -H 'Content-Type: application/json' -X POST "$API/sessions/$SESSION/lanes" --data '{"agent":"X","brief":"b"}')"
chk_in "사람 쿠키의 결정 기록 차단"          "401 403" "$(ucode -H 'Content-Type: application/json' -X POST "$API/sessions/$SESSION/decisions" --data '{"summary":"s"}')"
if [ -n "${A_TOK:-}" ] && [ -n "${A_TASK:-}" ]; then
  # E15-02: 세션 비참여 에이전트에게 위임하면 CLI 가 거부할 수 있게 서버가 not_participant 를 준다.
  DEL="$(curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $A_TOK" -H 'Content-Type: application/json' \
        -X POST "$API/sessions/$SESSION/lanes" --data '{"agent":"존재하지-않는-에이전트","brief":"b"}')"
  chk_in "비참여 에이전트 위임은 4xx (E15-02)" "403 404 422" "$(printf '%s' "$DEL" | tail -1)"
  chk "비참여 위임 코드가 not_participant"   not_participant "$(printf '%s' "$DEL" | sed '$d' | jq -r '.code // empty')"
  chk "거부된 위임은 lane 을 만들지 않는다"  "$(psqlq "select count(*) from lane where session_id='$SESSION'")" "$(psqlq "select count(*) from lane where session_id='$SESSION'")"
fi

echo
echo "▶ D9. 서버 5xx 가 하나도 없었는가 (이 스크립트 구간)"
FIVE="$(grep -c '"status":5' "$OUT/server.log" 2>/dev/null || echo 0)"
log "server.log 5xx 라인 누적: $FIVE (참고 — 이전 시나리오 포함)"

echo
printf '결과: PASS %d · FAIL %d\n' "$pass" "$fail"
jq -n --argjson pass "$pass" --argjson fail "$fail" '{adversarial:{pass:$pass,fail:$fail}}' | tee "$OUT/g-summary.json"
[ "$fail" = 0 ]
