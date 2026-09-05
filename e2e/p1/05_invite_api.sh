#!/usr/bin/env bash
# e2e/p1/05_invite_api.sh — P1 DoD 4 "초대 링크로 두 번째 멤버" 를 API 로 검증 (U13 백엔드 경로, 에이전트 턴 없음).
# owner 가 초대 링크 생성 → 미로그인 사용자가 signup(invite_token) → 멤버 2명, 초대 accepted, 두 번째 사용자의 워크스페이스 목록에 포함.
# 전제: 01_vertical_slice.sh 실행 후(a-ids.txt, cookies-a.txt). 브라우저 경로는 04_u1_browser.sh U13 단계.
source "$(dirname "$0")/lib.sh"
read -r WS SESSION AGENT RUNTIME_ID < "$OUT/a-ids.txt"
COOKIE="$OUT/cookies-a.txt"
STAMP="$(date +%s)"
N0="$(api_ok GET "/workspaces/$WS/members" | jq -r '(.items // .)|length')"; log "members before: $N0"
step "1. owner 초대 링크 생성 (createInvite)"
INV="$(api_ok POST "/workspaces/$WS/invites" '{"role":"member"}')"
INV_ID="$(jq -r .id <<<"$INV")"; TOKEN="$(jq -r .token <<<"$INV")"; URL="$(jq -r .url <<<"$INV")"
ok "invite $INV_ID url=$URL"
case "$URL" in "$WEB_URL"/invite/*) ok "invite.url 이 웹 오리진($WEB_URL)";; *) bad "invite.url 오리진이 웹(:3000)이 아님 — 사용자가 받는 링크가 서버(:8080) 를 가리킴 (S: COLAB_SERVER_URL 로 만든다)";; esac
step "2. 미로그인 미리보기 (previewInvite, 공개)"
COOKIE="$OUT/cookies-invitee.txt"; rm -f "$COOKIE"
PV="$(api_ok GET "/invites/$TOKEN")"; ok "preview: workspace=$(jq -r .workspace.name <<<"$PV") role=$(jq -r .role <<<"$PV") invited_by=$(jq -r .invited_by.display_name <<<"$PV")"
step "3. 두 번째 사용자 가입 + invite_token (S3 미로그인 흐름)"
RES="$(api_ok POST /auth/signup "$(jq -nc --arg e "seoyeon+$STAMP@example.com" --arg t "$TOKEN" '{display_name:"서연",email:$e,password:"password123",invite_token:$t}')")"
ACC="$(jq -r '(.accepted_invite // empty) | if type=="object" then .role else . end' <<<"$RES")"; log "signup → accepted_invite=$ACC"
WS2="$(api_ok GET /workspaces | jq -r '.[].id')"; [ "$WS2" = "$WS" ] && ok "두 번째 사용자의 워크스페이스 = $WS (S4 건너뛰고 S5)" || bad "두 번째 사용자 워크스페이스 목록: '$WS2'"
step "4. 멤버 2명 · 초대 accepted"
COOKIE="$OUT/cookies-a.txt"
MEMBERS="$(api_ok GET "/workspaces/$WS/members" | jq -c '.items // .')"; N="$(jq -r 'length' <<<"$MEMBERS")"; log "members: $(jq -r '.[]|.role+":"+.user.display_name' <<<"$MEMBERS" | tr '\n' ' ')"
[ "$N" = "$((N0+1))" ] && ok "멤버 $N0 → $N 명 (+1)" || bad "멤버 $N0 → $N 명"
ST="$(api_ok GET "/workspaces/$WS/invites" | jq -r --arg id "$INV_ID" '.[]|select(.id==$id)|.status')"; [ "$ST" = accepted ] && ok "초대 상태 accepted" || bad "초대 상태 '$ST'"
jq -n --arg url "$URL" --argjson members "$N" --arg status "$ST" --arg accepted "$ACC" '{invite_url:$url,members:$members,invite_status:$status,signup_accepted_invite:$accepted}' | tee "$OUT/e-summary.json"
