#!/usr/bin/env bash
# e2e/p5/78_members_notifications.sh — T-S14 실서버 스모크: S14 멤버 탭(역할 변경·제거) · 알림 탭(개인)
# — openapi updateMemberRole · removeMember · getNotificationSettings · updateNotificationSettings (S-72).
# 데몬 없이, 계정 셋(owner · admin · member)과 내보내질 넷째(victim)로 계약의 권한 줄을 그대로 왕복한다.
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 10s
#
# 재는 것 (판정 표 out/78-checks.tsv):
#   A. updateMemberRole — 멤버 403 · admin 이 멤버 승격 200 · admin 이 owner 강등 403(owner_only) ·
#      admin 이 자기를 owner 로 403 · 마지막 owner 강등 409(last_owner) · owner 둘일 때 강등 200 ·
#      역할 enum 422 · 다른 워크스페이스 id 404 · activity_log member.role_changed
#   B. removeMember — 멤버 403 · admin 이 owner 제거 403 · 마지막 owner 409 · Director 인 진행 중 세션이 있어도
#      204 — openapi 0.2.3(T-R1b2): 그 방의 방장이 Director 를 잇고 미션 타임라인·activity_log 에 남는다 ·
#      내보내진 사람은 403 not_member · 두 번째 404 · activity_log member.removed · 그 멤버의 받은 요청·구독 행 0(CASCADE)
#   C. 알림 설정 — 기본값 email true · push false · all · 부분 갱신 · 본인 행만 · enum 422 · 익명 401
#   D. 문장 — 이 스크립트가 받은 Problem.detail 전부 한글이고 §8.4 옛말(runtime·lane·owner·admin…)이 없다
#
# 스택: e2e/p5/up.sh 를 T-S14 포트로 — SERVER_URL=http://localhost:8111 PG_PORT=5455 PG_CONTAINER=colab-pg-s14
# (§0-13; 같은 머신의 T-S12 :8107/:5451 · T-D12 · T-I5 스택을 밟지 않는다).
# 사용: export SERVER_URL=http://localhost:8111 PG_PORT=5455 PG_CONTAINER=colab-pg-s14
#       bash e2e/p5/up.sh && bash e2e/p5/78_members_notifications.sh ; bash e2e/p5/down.sh
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/78-checks.tsv"; : > "$CHECKS"
PROBLEMS="$OUT/78-problems.jsonl"; : > "$PROBLEMS"
API="$SERVER_URL/api/v1"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh"

# 계정마다 쿠키 통을 따로 — lib 의 api 는 $COOKIE 하나를 쓴다.
as() { COOKIE="$OUT/78-cookie-$1.txt"; }
rm -f "$OUT"/78-cookie-*.txt
# call METHOD PATH [JSON] → 전역 CODE·BODY. 4xx 본문은 D 절 문장 검사용으로 모아 둔다.
call() {
  local out; out="$(api "$@")"; CODE="$(api_code <<<"$out")"; BODY="$(api_body <<<"$out")"
  case "$CODE" in 4*) jq -c --arg op "$1 $2" '. + {op:$op}' <<<"$BODY" >> "$PROBLEMS" 2>/dev/null || true;; esac
}
code_of() { jq -r '.code // "-"' <<<"$BODY"; }
role_db() { psqlq "select role from member where id='$1'"; }
activity() { psqlq "select count(*) from activity_log where workspace_id='$WS' and action='$1'"; }

step "0. 계정 넷 · 워크스페이스 · 컴퓨터(curl 페어링) · 초대 수락"
as owner;  OWNER_UID="$(signup "s14-owner-$RUN@example.com"  password123 "Owner")"
WS="$(create_workspace "S14 $RUN")"
OWNER_MID="$(api_ok GET "/workspaces/$WS/members" | jq -r '.items[0].id')"
chk 0.1 owner "$(api_ok GET "/workspaces/$WS/members" | jq -r '.items[0].role')" "만든 사람이 owner"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-s14" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-s14-$RUN")"
AG="$(create_agent "$WS" "Lead" "claude-haiku-4-5-20251001")"
invite_as() { # role → token (owner 가 만든다)
  as owner; api_ok POST "/workspaces/$WS/invites" "$(jq -nc --arg r "$1" '{role:$r}')" | jq -r .token
}
join() { # who email role → 전역 <WHO>_UID · <WHO>_MID
  local who="$1" email="$2" role="$3" tok m up
  up="$(tr a-z A-Z <<<"$who")"   # bash 3.2 에는 ${who^^} 가 없다
  tok="$(invite_as "$role")"
  as "$who"; signup "$email" password123 "$who" >/dev/null
  m="$(api_ok POST "/invites/$tok/accept" '')"
  printf -v "${up}_UID" '%s' "$(jq -r .user.id <<<"$m")"
  printf -v "${up}_MID" '%s' "$(jq -r .id <<<"$m")"
}
join admin  "s14-admin-$RUN@example.com"  admin
join member "s14-member-$RUN@example.com" member
join victim "s14-victim-$RUN@example.com" member
chk 0.2 "owner/admin/member/member" "$(as owner; api_ok GET "/workspaces/$WS/members" | jq -r '[.items[].role]|join("/")')" "멤버 넷의 역할"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. updateMemberRole — 권한: owner · admin. owner 강등은 owner 만. 마지막 owner 409"
as member; call PATCH "/workspaces/$WS/members/$VICTIM_MID" '{"role":"admin"}'
chk A.1 "403/admin_required" "$CODE/$(code_of)" "멤버는 역할을 못 바꾼다"
chk A.2 member "$(role_db "$VICTIM_MID")" "  … 그리고 아무것도 안 바뀌었다"
as admin;  call PATCH "/workspaces/$WS/members/$VICTIM_MID" '{"role":"admin"}'
chk A.3 "200/admin/$VICTIM_MID" "$CODE/$(jq -r .role <<<"$BODY")/$(jq -r .id <<<"$BODY")" "admin 이 멤버를 admin 으로 → 200 Member"
chk A.4 admin "$(role_db "$VICTIM_MID")" "  … DB 에 반영"
call PATCH "/workspaces/$WS/members/$OWNER_MID" '{"role":"admin"}'
chk A.5 "403/owner_only" "$CODE/$(code_of)" "admin 이 owner 강등 → 403 (SCREEN §2.3 owner 강등은 owner 만)"
call PATCH "/workspaces/$WS/members/$ADMIN_MID" '{"role":"owner"}'
chk A.6 "403/owner_only" "$CODE/$(code_of)" "admin 이 자기를 owner 로 → 403 (승격도 owner 만 — PR 본문 판단)"
as owner;  call PATCH "/workspaces/$WS/members/$OWNER_MID" '{"role":"admin"}'
chk A.7 "409/last_owner" "$CODE/$(code_of)" "마지막 owner 강등 → 409"
chk A.8 owner "$(role_db "$OWNER_MID")" "  … owner 그대로"
call PATCH "/workspaces/$WS/members/$VICTIM_MID" '{"role":"owner"}'
chk A.9 "200/owner" "$CODE/$(jq -r .role <<<"$BODY")" "owner 가 다른 멤버를 owner 로 → 200"
call PATCH "/workspaces/$WS/members/$OWNER_MID" '{"role":"admin"}'
chk A.10 "200/admin" "$CODE/$(jq -r .role <<<"$BODY")" "owner 둘이면 자기 강등 200"
as victim; call PATCH "/workspaces/$WS/members/$OWNER_MID" '{"role":"owner"}'
chk A.11 "200/owner" "$CODE/$(jq -r .role <<<"$BODY")" "새 owner 가 원래 owner 를 되돌린다"
call PATCH "/workspaces/$WS/members/$VICTIM_MID" '{"role":"member"}'
chk A.12 "200/member" "$CODE/$(jq -r .role <<<"$BODY")" "자기를 member 로 (owner 둘이라 200)"
as owner;  call PATCH "/workspaces/$WS/members/$VICTIM_MID" '{"role":"god"}'
chk A.13 "422/role" "$CODE/$(jq -r '.errors[0].field // "-"' <<<"$BODY")" "역할 enum 밖 → 422 errors[].field=role"
call PATCH "/workspaces/$WS/members/$(uuid)" '{"role":"admin"}'
chk A.14 "404/not_found" "$CODE/$(code_of)" "없는(다른 워크스페이스의) 멤버 id → 404"
chk A.15 5 "$(activity member.role_changed)" "activity_log member.role_changed = 실제 바뀐 횟수 5 (PRD §7)"
as owner; api_ok GET "/workspaces/$WS/members" | jq . > "$OUT/78-members-after-A.json"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. removeMember — 마지막 owner 409 · Director 인 진행 중 세션은 방장이 승계(0.2.3) → 204"
as member; call DELETE "/workspaces/$WS/members/$VICTIM_MID"
chk B.1 "403/admin_required" "$CODE/$(code_of)" "멤버는 내보내지 못한다"
as admin;  call DELETE "/workspaces/$WS/members/$OWNER_MID"
chk B.2 "403/owner_only" "$CODE/$(code_of)" "admin 이 owner 제거 → 403"
as owner;  call DELETE "/workspaces/$WS/members/$OWNER_MID"
chk B.3 "409/last_owner" "$CODE/$(code_of)" "마지막 owner 제거 → 409"
SID="$(create_session "$WS" "$AG" "S14 $RUN" "멤버 제거 판정용" "$RID")"
call PUT "/works/$(work_of "$SID")/director" "$(jq -nc --arg u "$VICTIM_UID" '{director_user_id:$u}')"
chk B.4 200 "$CODE" "changeWorkDirector → victim (옛 changeDirector 가 500 이던 결함 — activity_log 열 이름)"
chk B.5 "$VICTIM_UID" "$(psqlq "select director_user_id from work where room_id='$SID'")" "  … DB 의 Director"
chk B.6 1 "$(psqlq "select count(*) from activity_log where session_id='$SID' and action='work.director_changed'")" "  … activity_log work.director_changed(R4: 옛 session.director_changed)"
as admin;  call DELETE "/workspaces/$WS/members/$VICTIM_MID"
chk B.7 204 "$CODE" "Director 인 진행 중 세션이 있어도 내보낸다 → 204 (openapi 0.2.3, 옛 409 없음)"
# R4: createRoom 방에는 legacy_work_id 가 없다 — 이 방의 미션을 work_of 로 집는다.
chk B.8 "$OWNER_UID" "$(psqlq "select director_user_id from work where id='$(work_of "$SID")'")" "  … 그 방의 방장(세션을 만든 owner)이 Director 를 이었다"
chk B.9 1 "$(psqlq "select count(*) from activity_log where session_id='$SID' and action='work.director_succeeded'")" "  … activity_log work.director_succeeded"
chk B.10 1 "$(psqlq "select count(*) from message where session_id='$SID' and kind='system' and work_id is not null and content like '%이 미션의 Director 를 이어받았습니다.'")" "  … 미션 타임라인 시스템 메시지"
chk B.11 0 "$(psqlq "select count(*) from member where id='$VICTIM_MID'")" "  … member 행 0"
chk B.12 "0/0" "$(psqlq "select count(*) from inbox_item where member_id='$VICTIM_MID'")/$(psqlq "select count(*) from session_subscription where user_id='$VICTIM_UID'")" "  … 받은 요청·구독 행 0"
chk B.13 1 "$(activity member.removed)" "activity_log member.removed = 1"
as victim; call GET "/workspaces/$WS/members"
chk B.14 "403/not_member" "$CODE/$(code_of)" "내보내진 사람은 멤버 목록도 못 본다"
as admin;  call DELETE "/workspaces/$WS/members/$VICTIM_MID"
chk B.15 "404/not_found" "$CODE/$(code_of)" "두 번째 제거 → 404"
chk B.16 "owner/admin/member" "$(as owner; api_ok GET "/workspaces/$WS/members" | jq -r '[.items[].role]|join("/")')" "남은 멤버 셋"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 알림 설정(개인) — 기본값 · 부분 갱신 · 본인 행만 · enum 422 · 익명 401"
as member; call GET "/me/notification-settings"
chk C.1 "200/true/false/all" "$CODE/$(jq -r '(.email|tostring)+"/"+(.push|tostring)+"/"+.default_subscription' <<<"$BODY")" "기본값 = openapi default"
call PATCH "/me/notification-settings" '{"push":true}'
chk C.2 "200/true/true/all" "$CODE/$(jq -r '(.email|tostring)+"/"+(.push|tostring)+"/"+.default_subscription' <<<"$BODY")" "push 만 보내면 나머지는 그대로"
call PATCH "/me/notification-settings" '{"email":false,"push":true,"default_subscription":"hitl_only"}'
chk C.3 "200/false/true/hitl_only" "$CODE/$(jq -r '(.email|tostring)+"/"+(.push|tostring)+"/"+.default_subscription' <<<"$BODY")" "화면이 보내는 모양(셋 다) → 200"
call GET "/me/notification-settings"
chk C.4 "false/true/hitl_only" "$(jq -r '(.email|tostring)+"/"+(.push|tostring)+"/"+.default_subscription' <<<"$BODY")" "GET 이 저장된 값을 돌려준다"
chk C.5 "false/true/hitl_only" "$(psqlq "select (notification_settings->>'email')||'/'||(notification_settings->>'push')||'/'||(notification_settings->>'default_subscription') from app_user where id='$MEMBER_UID'")" "  … app_user.notification_settings (0021; jsonb 는 키 순서를 안 지킨다)"
as owner;  call GET "/me/notification-settings"
chk C.6 "true/false/all" "$(jq -r '(.email|tostring)+"/"+(.push|tostring)+"/"+.default_subscription' <<<"$BODY")" "다른 사람의 설정은 그대로 (본인 행만)"
as member; call PATCH "/me/notification-settings" '{"default_subscription":"everything"}'
chk C.7 "422/default_subscription" "$CODE/$(jq -r '.errors[0].field // "-"' <<<"$BODY")" "구독 기본값 enum 밖 → 422"
call GET "/me/notification-settings"
chk C.8 hitl_only "$(jq -r .default_subscription <<<"$BODY")" "  … 거부된 PATCH 는 아무것도 안 바꿨다"
as anon; rm -f "$COOKIE"; call GET "/me/notification-settings"
chk C.9 401 "$CODE" "익명 → 401"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 이 스크립트가 받은 Problem 문장 — 한글 · §8.4 옛말 없음"
N="$(wc -l < "$PROBLEMS" | tr -d ' ')"
chk_ge D.1 12 "$N" "4xx Problem 을 모았다(out/78-problems.jsonl)"
chk D.2 0 "$(jq -r '.detail, (.errors[]?.message // empty)' "$PROBLEMS" | grep -cv '[가-힣]' || true)" "detail·errors[].message 전부 한글"
chk D.3 0 "$(jq -r '.detail, (.errors[]?.message // empty)' "$PROBLEMS" | grep -ciE '(^|[^a-z_])(runtime|lane|task|attempt|workdir|owner|admin|hitl)([^a-z_]|$)|런타임|머신' || true)" "§8.4 옛말·내부 명사 없음"
chk D.4 0 "$(jq -r '.title' "$PROBLEMS" | grep -cv '[가-힣]' || true)" "title 전부 한글"

step "결과"
printf '\n%s\n' "$(cat "$CHECKS")" >&2
echo "FAILS=$FAILS" >&2
[ "$FAILS" = 0 ] && ok "78_ 전부 통과 ($(wc -l < "$CHECKS" | tr -d ' ') checks)" || die "78_ 실패 $FAILS 건"
