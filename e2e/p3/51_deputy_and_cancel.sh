#!/usr/bin/env bash
# e2e/p3/51_deputy_and_cancel.sh — T-I3 (d): **deputy 위임 시점**과 **취소 권한** (E7-09·10·11, E10-04·05·06).
#
#   E7-11  일반 멤버는 응답할 수 없다 — `403`, `can_respond_from` 은 **null**(생기지 않을 권리에
#          시각을 약속하지 않는다). 화면은 버튼을 숨기지 않고 "권한 없음" 을 보여준다
#   E7-09  발행 후 기한 절반 전(11h) deputy 응답 → `403 deputy_not_yet` + `can_respond_from`.
#          화면은 승인 버튼 **비활성** + "HH:MM 부터"
#   E7-10  기한 절반 뒤(12h+) deputy 응답 → **수락**. 화면 버튼 활성
#   E10-05 일반 멤버의 취소 → `403`, 화면 버튼 비활성
#   E10-06 deputy 의 취소 → **즉시** 동작(승인과 달리 시점 제한이 없다), lane `failed(cancelled)`,
#          활동 피드 "사람이 중단함"
#
# ── 시각을 어떻게 옮겼나(우회 표기) ──────────────────────────────────────────
# 서버 바이너리에는 클럭 주입 경로가 없다(`server/cmd/server/main.go` 는 `clock.Real{}` 고정).
# `hitl.Authorize` 가 보는 값은 두 개뿐이다 — `Elapsed = now - created_at`, `DueIn = due_at - created_at`.
# 그래서 **`created_at` 과 `due_at` 을 같은 만큼 과거로 민다**(lib.sh `backdate_hitl`): 기한 길이 24h 는
# 그대로 두고 경과 시간만 옮긴다. 서버 코드도 계약도 건드리지 않는다.
#
# 산출물: out/51-checks.tsv · out/51.json · web/__screenshots__/p3-51-*.png
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
CFG="$OUT/daemon-51.json"; WORK="$OUT/work-51"; DLOG="$OUT/daemon-51.log"
DIR_COOKIE="$OUT/cookies-51-dir.txt"; DEP_COOKIE="$OUT/cookies-51-dep.txt"; MEM_COOKIE="$OUT/cookies-51-mem.txt"
rm -f "$DIR_COOKIE" "$DEP_COOKIE" "$MEM_COOKIE"
MODEL="${LEAD_MODEL}"
DIR_EMAIL="g6d-dir+$STAMP@example.com"; DEP_EMAIL="g6d-dep+$STAMP@example.com"; MEM_EMAIL="g6d-mem+$STAMP@example.com"
PASSWORD="password123"
mkdir -p "$E2E_ROOT/web/__screenshots__"
g5_chk_init "$OUT/51-checks.tsv"

cleanup() {
  [ -f "$OUT/daemon-51.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-51.pid")" 2>/dev/null || true; }
  agent-browser close >/dev/null 2>&1 || true
  return 0
}
trap cleanup EXIT

# 에이전트 발행 HITL 은 K-7·C-4(계약 충돌 · CLI 404)로 정식 도구가 서지 않는다 — 48_ 과 같은 우회로
# attempt 토큰을 써서 openapi `createHitlRequest` 에 직접 등록한다. 서버가 보는 것은 정식 경로와 같다.
ASK_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 설명 초안을 쓰는 작성자다. 답은 한국어로 짧게.

첫 턴에 할 일은 아래 두 가지뿐이다. 각각 한 번씩만 하고, 실패해도 재시도하거나 다른 방법을 찾지 마라.
  a. 셸에서 아래 명령을 그대로 한 번 실행한다.
     curl -sS -X POST "$COLAB_SERVER_URL/api/v1/sessions/$COLAB_SESSION_ID/hitl-requests" -H "Authorization: Bearer $COLAB_TASK_TOKEN" -H "Content-Type: application/json" -d '"'"'{"type":"approval","summary":"이 설명 초안을 이대로 확정해도 될까요?"}'"'"'
  b. 메시지를 하나도 게시하지 말고 즉시 턴을 끝낸다.

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라.'

LONG_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명서를 쓰는 작성자다. 답은 한국어로 짧게.

**첫 턴부터 곧바로** 현재 작업 디렉토리에서 아래를 순서대로, 서두르지 말고 한 단계씩 수행한다.
기다리지 말고 아무것도 묻지 말고 바로 시작한다.
- 단계 1..8: chapter-01.md 부터 chapter-08.md 까지 각각 여덟 줄짜리 장을 하나씩 쓴다.
  한 장을 끝낼 때마다 colab_message_post 로 `CHAPTER-<번호> done` 을 게시한다.
여덟 장이 끝나면 `ALL-DONE` 을 게시하고 colab_status_set 으로 status "done" 을 부른 뒤 턴을 끝낸다.

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라. 도구가 실패해도 재시도하거나
다른 방법을 찾지 마라. 파일 쓰기 말고는 colab_* 도구만 쓴다.'

step "1. 사람 셋 — Director · deputy · 일반 멤버 (초대 링크로)"
COOKIE="$DIR_COOKIE"
signup "$DIR_EMAIL" "$PASSWORD" "민지(Director)" >/dev/null
WS="$(create_workspace "G6 Deputy $STAMP")"
join_as() { # EMAIL NAME COOKIEFILE → user id
  local tok; COOKIE="$DIR_COOKIE"; tok="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}' | jq -r .token)"
  COOKIE="$3"; rm -f "$3"
  api_ok POST /auth/signup "$(jq -nc --arg e "$1" --arg n "$2" --arg t "$tok" \
    '{display_name:$n,email:$e,password:"password123",invite_token:$t}')" | jq -r '.user.id // .id'
}
DEP_ID="$(join_as "$DEP_EMAIL" "지훈(deputy)" "$DEP_COOKIE")"
MEM_ID="$(join_as "$MEM_EMAIL" "수아(멤버)" "$MEM_COOKIE")"
COOKIE="$DIR_COOKIE"
chk M0 "워크스페이스 멤버 3명" 3 "$(api_ok GET "/workspaces/$WS/members" | jq -r '(.items // .)|length')"
ok "deputy=$DEP_ID member=$MEM_ID"

step "2. 페어링 · 에이전트"
: > "$DLOG"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
daemon_pair_cap "$PTOK" "$CFG" "$WORK" 2
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$OUT/daemon-51.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ASKER="$(create_agent_p2 "$WS" Asker writer "$MODEL" "$ASK_INS" '설명 초안을 쓴다')"
LONGA="$(create_agent_p2 "$WS" Chapters writer "$MODEL" "$LONG_INS" '설명서를 장별로 쓴다')"
ok "ws=$WS runtime=$RUNTIME"

step "3. 세션 2개 — H(승인 게이트) · C(취소). deputy 를 지정한다"
SH="$(create_session_p3 "$WS" "제품 Y 초안 확정 (deputy 게이트)" "$P3_GOAL" "$ASKER" "$RUNTIME" \
      "$(jq -nc --arg d "$DEP_ID" '{deputy_director_user_id:$d}')" "$ASKER")"
SC_="$(create_session_p3 "$WS" "제품 Y 설명서 (취소)" "$P3_GOAL" "$LONGA" "$RUNTIME" \
      "$(jq -nc --arg d "$DEP_ID" '{deputy_director_user_id:$d}')" "$LONGA")"
chk M1 "세션 H 에 deputy 가 지정됐다" "$DEP_ID" \
  "$(psqlq "select coalesce(deputy_director_user_id::text,'-') from session where id='$SH'")"
TH="$(session_initial_task "$SH")"; TC="$(session_initial_task "$SC_")"
T0="$(now_ms)"

step "4. E10-05·06 — 취소 권한: 멤버 403 · deputy 즉시"
# **취소 arm 을 먼저 한다**: 취소는 "진행 중 turn" 에만 의미가 있고, HITL 게이트 arm(시각 이동·브라우저)
# 을 먼저 하면 그 사이 이 lane 의 턴이 끝나 409 가 된다(1차 실행 실측).
CST="$(WAIT_S=${RUN_WAIT_S:-300} wait_task "$TC" running dispatched completed failed cancelled)"
for _ in $(seq 1 60); do
  [ "$(psqlq "select count(*) from task_event where task_id='$TC' and attempt=1 and class='tool'")" -ge 3 ] && break
  sleep 3
done
LANE_C="$(task_field "$TC" lane_id)"
chk X0 "C: lane 이 돌고 있다" yes "$(in_set "$CST" running dispatched)"
COOKIE="$MEM_COOKIE"
MC="$(api POST "/lanes/$LANE_C/cancel" '' | api_code)"
chk X1 "멤버의 취소는 403 (E10-05)" 403 "$MC"
chk X1b "그래서 lane 은 계속 돈다"  yes \
  "$(in_set "$(lane_field "$LANE_C" status)" running queued dispatched)"
export AGENT_BROWSER_SESSION="colab-g6-51-mem-$STAMP"
ab set viewport 1440 1000 >/dev/null 2>&1 || true
web_login "$MEM_EMAIL" "$PASSWORD" >/dev/null 2>&1 || bad "멤버 웹 로그인 실패"
ab open "$WEB_URL/sessions/$SC_" >/dev/null 2>&1 || true
abwait '[data-testid="lane-card"]' 40 || true
shot "p3-51-04-member-cancel-disabled"
chk W4  "멤버 화면에 \"중단\" 버튼이 **보인다**(숨기지 않는다, SCREEN §7)" yes \
  "$( [ "$(abcount '[data-testid="lane-action-cancel"]')" -ge 1 ] && echo yes || echo no )"
chk W4b "그 버튼이 **비활성**이다 (E10-05)"              yes \
  "$( [ "$(abcount '[data-testid="lane-action-cancel"]:disabled')" -ge 1 ] && echo yes || echo no )"
COOKIE="$DEP_COOKIE"
# §8 되먹임 2: **개입은 턴이 살아 있는 동안에만 잰다.** 턴이 이미 끝났으면 E10-04 의 자극 자체가
# 성립하지 않는다 — 서버는 `completed` finish 를 받고, 취소는 흡수되지 않는다(2판 §9.6 관찰).
# 그래서 취소 직전에 그 attempt 가 아직 turn_end 를 내지 않았음을 못 박는다.
chk X1c "취소 시점에 그 턴이 **아직 살아 있다** (§8 되먹임 2 — 이 자극의 전제)" 0 \
  "$(psqlq "select count(*) from task_event where task_id='$TC' and attempt=1 and class='runtime' and verb='turn_end'")"
T_CANCEL="$(now_ms)"
DC="$(api POST "/lanes/$LANE_C/cancel" '' | api_code)"
chk X2 "**deputy 의 취소는 즉시 받아들여진다** (시점 제한 없음, E10-06)" 202 "$DC"
COOKIE="$DIR_COOKIE"
DEADLINE=$(( $(date +%s) + 120 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(lane_field "$LANE_C" status)" = failed ] && break; sleep 3
done
chk X3  "lane 이 failed 다"                              failed "$(lane_field "$LANE_C" status)"
chk X3b "task 의 failure_kind=cancelled (E10-04)"         cancelled "$(task_field "$TC" failure_kind)"
chk X3c "task 도 cancelled"                              cancelled "$(task_field "$TC" status)"
# 활동 피드는 `task_event` 다(서버 tasks/service.go: class=status · verb=cancel · payload.args.note — S-52).
# 세션 `message` 가 아니다 — 1차 실행에서 message 를 봐 놓치고 FAIL 이 났다.
psqlq "select class||'/'||coalesce(verb,'-')||' '||replace(coalesce(payload::text,''),E'\n','⏎')
       from task_event where task_id='$TC' order by seq" > "$OUT/51-cancel-feed.txt"
chk_has X4 "활동 피드에 \"사람이 중단함\" (E10-04)"      "$OUT/51-cancel-feed.txt" "사람이 중단함"
chk X5  "취소가 새 task 를 만들지 않았다 (E10-04)"       1 "$(task_count "$SC_")"
chk X6  "취소 뒤 그 attempt 의 프로세스가 남지 않았다"   0 "$(procs_of_attempt "$WORK" "$TC" 1)"
CANCEL_S=$(( ($(now_ms)-T_CANCEL)/1000 ))
log "deputy 취소 → lane failed 까지 ${CANCEL_S}s"


step "5. H 세션 — 에이전트가 approval HITL 을 열고 턴을 끝낸다"
STH="$(WAIT_S=${HITL_WAIT_S:-420} wait_task "$TH" waiting_human failed cancelled completed)"
chk M2 "H: 턴이 waiting_human 으로 끝났다" waiting_human "$STH"
H="$(hitl_of_task "$TH")"
chk M2b "approval HITL 이 열렸다"          approval "$(hitl_field "$H" type)"
chk M2c "approver_spec=director"           director "$(hitl_field "$H" approver_spec)"
DUE_LEN="$(psqlq "select round(extract(epoch from (due_at-created_at))/3600)::text from hitl_request where id='$H'")"
chk M2d "기한 기본값 24h (FR-5.4)"          24 "$DUE_LEN"

step "6. E7-11 — 일반 멤버는 영영 응답할 수 없다 (403, can_respond_from = null)"
COOKIE="$MEM_COOKIE"
MEM_RES="$(api POST "/hitl-requests/$H/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)")"
MEM_CODE="$(api_code <<<"$MEM_RES")"; MEM_BODY="$(api_body <<<"$MEM_RES")"
printf '%s\n' "$MEM_BODY" > "$OUT/51-member-403.json"
chk P1  "멤버 응답이 403 이다"                         403 "$MEM_CODE"
chk P1b "**can_respond_from 이 null** (E7-11 · PR #108)" null \
  "$(jq -r 'if has("can_respond_from") then (.can_respond_from|tostring) else "missing" end' <<<"$MEM_BODY")"
COOKIE="$DIR_COOKIE"
chk P1c "그래도 HITL 은 open 유지"                      open "$(hitl_field "$H" status)"

step "7. E7-09 — 기한 절반 전(11h) deputy 응답은 거부된다"
backdate_hitl "$H" $((11*3600))
COOKIE="$DEP_COOKIE"
DEP_RES="$(api POST "/hitl-requests/$H/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)")"
DEP_CODE="$(api_code <<<"$DEP_RES")"; DEP_BODY="$(api_body <<<"$DEP_RES")"
printf '%s\n' "$DEP_BODY" > "$OUT/51-deputy-early.json"
chk P2  "11h 시점 deputy 응답이 403 이다 (E7-09)"      403 "$DEP_CODE"
chk P2b "code=deputy_not_yet"                          deputy_not_yet "$(jq -r '.code // "-"' <<<"$DEP_BODY")"
CRF="$(jq -r '.can_respond_from // "-"' <<<"$DEP_BODY")"
chk P2c "**can_respond_from 이 시각을 준다** (기한 절반)" yes "$( [ "$CRF" != - ] && [ "$CRF" != null ] && echo yes || echo no )"
CRF_H="$(psqlq "select round(extract(epoch from (timestamptz '$CRF' - created_at))/3600)::text from hitl_request where id='$H'" 2>/dev/null || echo '-')"
chk P2d "그 시각이 발행 + 12h 다"                       12 "$CRF_H"
chk P2e "HITL 은 여전히 open"                           open "$(hitl_field "$H" status)"

step "8. 화면 — deputy 에게는 버튼 비활성 + \"HH:MM 부터\", 멤버에게는 권한 없음 (§0-9 DOM)"
export AGENT_BROWSER_SESSION="colab-g6-51-dep-$STAMP"
ab set viewport 1440 1000 >/dev/null 2>&1 || true
web_login "$DEP_EMAIL" "$PASSWORD" >/dev/null 2>&1
ab open "$WEB_URL/sessions/$SH" >/dev/null 2>&1 || true
abwait '[data-testid="hitl-card"]' 40 || true
shot "p3-51-01-deputy-locked"
PERM="$(abget get attr '[data-testid="hitl-body"]' data-permission)"
chk W1  "deputy 화면의 카드 권한 상태 = later"          later "${PERM:-none}"
GATE="$(abget get text '[data-testid="hitl-gate"]' | tr '\n' ' ')"
chk W1b "\"HH:MM 부터 응답 가능\" 안내가 있다"          yes \
  "$( printf '%s' "$GATE" | grep -qE '[0-9]{1,2}:[0-9]{2}' && echo yes || echo no )"
# 비활성 판정은 **CSS `:disabled`** 로 한다 — `get attr … disabled` 는 boolean 속성이 있을 때
# 빈 문자열을 돌려줘 "없음" 과 구별되지 않는다(1차 실행 실측).
chk W1c "승인 버튼이 **비활성**"                        yes \
  "$( [ "$(abcount '[data-testid="hitl-approve"]:disabled')" -ge 1 ] && echo yes || echo no )"
log "deputy gate 문구: $GATE"
export AGENT_BROWSER_SESSION="colab-g6-51-mem-$STAMP"   # 위 4단계에서 이미 로그인돼 있다
ab open "$WEB_URL/sessions/$SH" >/dev/null 2>&1 || true
abwait '[data-testid="hitl-card"]' 40 || true
shot "p3-51-02-member-noright"
MPERM="$(abget get attr '[data-testid="hitl-body"]' data-permission)"
chk W2  "멤버 화면의 카드 권한 상태 = never"            never "${MPERM:-none}"
chk W2b "\"응답 권한이 없습니다\" 안내가 있다 (카드는 보인다)" yes \
  "$( [ "$(abcount '[data-testid="hitl-no-right"]')" -ge 1 ] && echo yes || echo no )"

step "9. E7-10 — 12h 1분 뒤 deputy 응답은 수락된다 (웹에서)"
backdate_hitl "$H" $((3600+120))     # 누적 12h 2분
export AGENT_BROWSER_SESSION="colab-g6-51-dep-$STAMP"
ab open "$WEB_URL/sessions/$SH" >/dev/null 2>&1 || true
abwait '[data-testid="hitl-card"]' 40 || true
sleep 2
PERM2="$(abget get attr '[data-testid="hitl-body"]' data-permission)"
chk W3  "12h 뒤 deputy 화면 권한 상태 = allowed"        allowed "${PERM2:-none}"
chk W3b "승인 버튼이 **활성**"                          yes \
  "$( [ "$(abcount '[data-testid="hitl-approve"]:enabled')" -ge 1 ] && echo yes || echo no )"
shot "p3-51-03-deputy-unlocked"
ab click '[data-testid="hitl-approve"]' >/dev/null 2>&1 || true
sleep 4
chk P3  "**deputy 의 승인이 받아들여졌다** (E7-10)"     answered "$(hitl_field "$H" status)"
chk P3b "approved=true"                                 true "$(hitl_field "$H" approved)"
chk P3c "응답자가 deputy 로 기록됐다"                   "$DEP_ID" \
  "$(psqlq "select coalesce(answered_by::text,'-') from hitl_request where id='$H'")"
chk P3d "결정 기록 1건"                                 1 "$(psqlq "select count(*) from decision where session_id='$SH'")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg sh "$SH" --arg sc "$SC_" --arg h "$H" --arg lane "$LANE_C" \
  --arg dep "$DEP_ID" --arg mem "$MEM_ID" --arg crf "$CRF" --arg gate "$GATE" \
  --argjson cancel_s "$CANCEL_S" --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" \
  --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,hitl_session:$sh,cancel_session:$sc,hitl:$h,lane:$lane,
    deputy:$dep,member:$mem,can_respond_from:$crf,gate_text:$gate,
    deputy_cancel_to_failed_s:$cancel_s,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/51.json"
[ "$fail" = 0 ]
