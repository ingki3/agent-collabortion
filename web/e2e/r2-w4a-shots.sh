#!/usr/bin/env bash
# T-R2-W4a 스크린샷 — S8 받은 요청 v0.19 · 안 읽음 · S9 · S14(방 기본값 · 구독 3층) · S15 활동 로그(SCREEN v0.19.2 §4.14~§4.18).
# `next build && next start`(목 모드)로 찍는다. 테마는 localStorage `colab.theme` 로 고정하고 화면을 새로 연다.
#
#   r2-w4a-01-s8-inbox-{light,dark}.png          S8 — 방 층 항목 전부(방 멈춤 · 격리 확인 둘 · 미션 밖 요청 · 제안 · 미션 일시정지/완료 · 초대 · 용량) · 필터 두 줄
#   r2-w4a-02-s8-room-paused-{light,dark}.png    S8 room_paused 카드 — 「미션 N개와 대화 전부」 · 「한꺼번에 다시 돕니다 — 잔여 합계」 · 새 상한 입력
#   r2-w4a-03-s8-isolation-{light,dark}.png      S8 isolation_confirm 두 장 — 「워크트리로 나눔 / 이대로 진행」 · 저장소 고르기
#   r2-w4a-04-s8-deputy-{light,dark}.png         S8 부방장 항목 — 「부방장으로서 · HH:MM부터 답할 수 있습니다」(비활성 먼저)
#   r2-w4a-05-s5-unread-{light,dark}.png         S5 + 내비 「방」 옆 안 읽음 합계
#   r2-w4a-06-s9-agents-{light,dark}.png         S9 — 「참여 중인 방 N」 펼침 · 동시 사용
#   r2-w4a-07-s14-room-defaults-{light,dark}.png S14 방 기본값 탭
#   r2-w4a-08-s14-subscriptions-{light,dark}.png S14 알림 — 구독 단위 3층
#   r2-w4a-09-s15-audit-{light,dark}.png         S15 활동 로그
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3163 &
#   BASE_URL=http://localhost:3163 bash e2e/r2-w4a-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3163}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-r2w4a-shots-$$}"
JAR="$(mktemp)"

ab() { agent-browser "$@"; }
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
set_theme() { apic "(function(){try{localStorage.setItem('colab.theme','$1')}catch(e){};return '$1'})()" >/dev/null; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; }
login() {
  ab open "$BASE_URL/login" >/dev/null
  ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
  ab fill 'input[name="email"]' "$1" >/dev/null
  ab fill 'input[name="password"]' 'password123' >/dev/null
  ab click 'button[type="submit"]' >/dev/null
  ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
}
# 단언 — 덧붙임(getRoom · getHitlRequest)이 늦게 오므로 10초까지 다시 본다.
assert_js() { local r i; for i in $(seq 1 20); do r=$(apic "$1"); { [ "$r" = "True" ] || [ "$r" = "true" ]; } && { echo "  ✓ $2"; return 0; }; sleep 0.5; done; echo "✗ 단언 실패: $2 ($r)"; exit 1; }
# 한 카드만 보이게 스크롤(카드 머리가 화면 맨 위).
scroll_to() { apic "(function(){var e=document.querySelector('$1');if(!e)return 'none';window.scrollTo(0,e.getBoundingClientRect().top+window.scrollY-16);return 'ok'})()" >/dev/null; sleep 0.3; }

api() { curl -sS -b "$JAR" -c "$JAR" -X "$1" "$BASE_URL/api/v1$2" -H 'content-type: application/json' ${3:+-d "$3"}; }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }
curl_login() { api POST /auth/login "{\"email\":\"$1\",\"password\":\"password123\"}" >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true; rm -f "$JAR"' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 시드"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
curl_login demo@colab.dev
WS=$(api GET /me | jget '["workspaces"][0]["id"]')
DEMO=$(api GET /me | jget '["user"]["id"]')
AGENTS=$(api GET "/workspaces/$WS/agents" | python3 -c "import sys,json;print(' '.join(a['id'] for a in json.load(sys.stdin)['items']))")
LEAD=$(echo "$AGENTS" | cut -d' ' -f1); RES=$(echo "$AGENTS" | cut -d' ' -f2)
ROOM=$(api POST "/workspaces/$WS/rooms" '{"name":"결제팀 — 수수료 정책과 정산 주기 재검토","description":"결제 관련 논의와 작업"}' | jget '["id"]')
INFRA=$(api POST "/workspaces/$WS/rooms" '{"name":"인프라","description":"배포·모니터링"}' | jget '["id"]')
api POST "/rooms/$ROOM/participants" "{\"agent_id\":\"$LEAD\"}" >/dev/null
api POST "/rooms/$ROOM/participants" "{\"agent_id\":\"$RES\"}" >/dev/null
api POST "/rooms/$INFRA/participants" "{\"agent_id\":\"$LEAD\"}" >/dev/null
api POST "/rooms/$ROOM/works" '{"goal":"경쟁사 결제 수수료 비교표 — 국내 5개사, 정산 주기·환불 규칙 포함"}' >/dev/null
# 서연이 방장이고 내가 부방장인 방 — 미션 밖 요청이 기한 절반 전이라 잠겨 있다.
curl_login seoyeon@colab.dev
DEP=$(api POST "/workspaces/$WS/rooms" '{"name":"마케팅 카피"}' | jget '["id"]')
api POST "/rooms/$DEP/participants" "{\"user_id\":\"$DEMO\"}" >/dev/null
api PUT "/rooms/$DEP/deputy" "{\"user_id\":\"$DEMO\"}" >/dev/null
# 서연 쪽 비공개 방에도 Lead 가 있다 — 데모는 볼 수 없어 「+ 볼 수 없는 방」 으로만.
HID=$(api POST "/workspaces/$WS/rooms" '{"name":"인수합병 검토"}' | jget '["id"]')
api PATCH "/rooms/$HID" '{"visibility":"invited"}' >/dev/null
api POST "/rooms/$HID/participants" "{\"agent_id\":\"$LEAD\"}" >/dev/null
curl_login demo@colab.dev
api POST /__mock/activity/seed '{}' >/dev/null
api POST /__mock/inbox/seed-v19 "{\"room_id\":\"$ROOM\",\"types\":[\"room_paused\",\"isolation_confirm\",\"lane_blocked\",\"work_proposed\",\"work_paused\",\"work_completed\",\"room_invited\",\"workdir_quota\"]}" >/dev/null
api POST /__mock/inbox/seed-v19 "{\"room_id\":\"$ROOM\",\"deputy_room_id\":\"$DEP\",\"types\":[\"hitl_request\"]}" >/dev/null
api POST "/__mock/rooms/$INFRA/seed" '{"unread":4}' >/dev/null
echo "  room=$ROOM infra=$INFRA deputy-room=$DEP"

step "로그인(소유자 · 데모)"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

for theme in light dark; do
  set_theme "$theme"
  step "01 — S8 받은 요청 ($theme)"
  open_wait "/inbox" '[data-testid="inbox-list"]'
  ab wait '[data-testid="inbox-room-paused-stopped"]' --timeout 20000 >/dev/null
  assert_js 'document.querySelectorAll("[data-testid=inbox-item]").length >= 10' "새 타입 전부"
  assert_js '!!document.querySelector("[data-testid=inbox-scope]")' "필터 둘째 줄(방·미션)"
  assert_js '[...document.querySelectorAll("[data-testid=inbox-room]")].some(e => e.textContent.includes("결제팀 — 수수료 정책과 정산 주기 재검토"))' "방 이름 — 줄이지 않는다"
  shot_full "r2-w4a-01-s8-inbox-$theme"

  step "02 — room_paused ($theme)"
  scroll_to '[data-type=room_paused]'
  assert_js 'document.querySelector("[data-testid=inbox-room-paused-resume]").textContent.includes("잔여 예산 합계 $6.50")' "잔여 합계"
  shot "r2-w4a-02-s8-room-paused-$theme"

  step "03 — isolation_confirm ($theme)"
  scroll_to '[data-type=isolation_confirm]'
  ab wait '[data-testid="inbox-isolation-repo"]' --timeout 20000 >/dev/null
  shot "r2-w4a-03-s8-isolation-$theme"

  step "04 — 부방장 항목 ($theme)"
  scroll_to '[data-basis=room_deputy]'
  assert_js '/^\d\d:\d\d부터 답할 수 있습니다$/.test(document.querySelector("[data-basis=room_deputy] [data-testid=inbox-delegation]").textContent)' "위임 전 — 비활성 먼저"
  shot "r2-w4a-04-s8-deputy-$theme"

  step "05 — S5 · 내비 안 읽음 ($theme)"
  open_wait "/rooms" '[data-testid="rooms-unread-badge"]'
  ab wait '[data-testid="room-unread"]' --timeout 20000 >/dev/null
  assert_js 'document.querySelector("[data-testid=rooms-unread-badge]").textContent === String([...document.querySelectorAll("[data-testid=room-unread]")].reduce((a, e) => a + Number(e.textContent), 0))' "내비 합계 = 카드 배지 합"
  shot "r2-w4a-05-s5-unread-$theme"

  step "07 — S14 방 기본값 ($theme)"
  open_wait "/settings?tab=rooms" '[data-testid="room-defaults-head"]'
  shot_full "r2-w4a-07-s14-room-defaults-$theme"

  step "08 — S14 구독 3층 ($theme)"
  open_wait "/settings?tab=notifications" '[data-testid="subs-room"]'
  ab select '[data-testid="subs-room"]' "$ROOM" >/dev/null
  ab wait '[data-testid="subs-room-level"]' --timeout 20000 >/dev/null
  scroll_to '[data-testid=subscriptions]'
  shot "r2-w4a-08-s14-subscriptions-$theme"

  step "09 — S15 활동 로그 ($theme)"
  open_wait "/settings/audit" '[data-testid="audit-table"]'
  assert_js '["room.read","room.read.denied","room_link.created","room.visibility_changed","room.audit_viewed","room.deleted","room.owner_succeeded"].every(a => document.querySelector(`[data-testid=audit-row][data-action="${a}"]`))' "§4.18 필수 행 전부"
  shot_full "r2-w4a-09-s15-audit-$theme"
done
set_theme light

# S9 는 멤버(준호)의 눈으로 — 소유자는 감사 열람으로 invited 방까지 보므로 「+ 볼 수 없는 방」이 생기지 않는다.
# 새 브라우저 세션으로 — 소유자 세션이 남아 있으면 「볼 수 없는 방」이 0 이 된다.
ab close >/dev/null 2>&1 || true
export AGENT_BROWSER_SESSION="$AGENT_BROWSER_SESSION-member"
ab set viewport 1280 900 >/dev/null 2>&1 || true
login junho@colab.dev
assert_js 'document.querySelector(".app-nav__user b")?.textContent === "준호"' "멤버(준호)로 들어왔다"
for theme in light dark; do
  set_theme "$theme"
  step "06 — S9 참여 중인 방 ($theme, 멤버)"
  open_wait "/agents" "[data-agent-id=\"$LEAD\"] [data-testid=agent-rooms-toggle]"
  ab click "[data-agent-id=\"$LEAD\"] [data-testid=agent-rooms-toggle]" >/dev/null
  ab wait "[data-agent-id=\"$LEAD\"] [data-testid=agent-rooms-hidden]" --timeout 20000 >/dev/null
  assert_js "!document.querySelector('[data-agent-id=\"$LEAD\"] [data-testid=agent-rooms-list]').textContent.includes('인수합병')" "볼 수 없는 방은 이름 없이 수만"
  shot "r2-w4a-06-s9-agents-$theme"
done
set_theme light

echo; echo "✓ 끝 — $SHOT_DIR/r2-w4a-*.png"
