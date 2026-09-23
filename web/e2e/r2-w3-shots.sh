#!/usr/bin/env bash
# T-R2-W3 스크린샷 — 방의 다이얼로그·설정 화면(SCREEN v0.19.2 §4.7·§4.9~§4.13). **`next build && next start` 로 찍는다**
# (개발 오버레이 배지가 없게). 테마는 설정 화면과 같은 경로(localStorage `colab.theme`)로 고정하고 화면을 새로 연다.
#
#   r2-w3-01-s19-people-{light,dark}.png    S19 참여자 — 사람·에이전트 한 목록 · 방장 본인 「이 방에서 나가기」 비활성 + 사유 · Director 인 서연 내보내기 비활성
#   r2-w3-02-s19-agents-{light,dark}.png    S19 에이전트 탭 — 머리 한 줄(지난 대화 전부를 읽는다) · 컴퓨터 미정 안내
#   r2-w3-03-s20-settings-{light,dark}.png  S20 방 설정(전체) — 묶음 여덟 · 영향 한 줄 · 연결된 참고 방
#   r2-w3-04-s24-links-{light,dark}.png     S24 참고 방 링크(S20 위 다이얼로그) — 연결된 방 · 후보 · 「에이전트 쪽 조건만」
#   r2-w3-05-s23-reads-{light,dark}.png     S23 맥락 읽기 기록 — 세 묶음 · 잘림 칩 · 거부 행은 방 이름 없음(originator_left 만 문장째)
#   r2-w3-06-s21-new-{light,dark}.png       S21 새 미션 — 정의 한 줄 · 담당 → 종료 조건 문장 · 기본값 펼침(예산 줄·자율성)
#   r2-w3-07-s26-proposal-{light,dark}.png  S26 미션 제안 확인 — 근거 · 세 버튼
#   r2-w3-08-s21-cap-light.png              S21 동시 미션 상한 — 열기 전에 알림 + 열린 미션 목록
#   r2-w3-09-s20-readonly-light.png         S20 방 참여자(권한 밖) — 읽기 전용 사유 한 줄
#   r2-w3-10-s26-resolved-light.png         S26 이미 처리된 제안 — 「이 제안은 … 에 거절했습니다」 + 사유
#
# S21·S26 은 S7(T-R2-W2)에 마운트된 `RoomQueryDialogs` 로 방 화면 위에 뜬다(`/rooms/<id>?work=new` · `?work_proposal=`). 개발 전용 페이지는 지웠다.
#
# 사용:
#   COLAB_MOCK_API=1 COLAB_DEV_PAGES=1 npx next build && COLAB_MOCK_API=1 COLAB_DEV_PAGES=1 npx next start -p 3161 &
#   BASE_URL=http://localhost:3161 bash e2e/r2-w3-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3161}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-r2w3-shots-$$}"
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
logout() { apic '(async()=>{await fetch("/api/v1/auth/logout",{method:"POST"}).catch(()=>{});return "ok"})()' >/dev/null; }
assert_js() { local r; r=$(apic "$1"); [ "$r" = "True" ] || [ "$r" = "true" ] || { echo "✗ 단언 실패: $2 ($r)"; exit 1; }; echo "  ✓ $2"; }

# ── 시드는 curl 로(쿠키 통) — 브라우저 세션과 같은 목 저장소를 쓴다 ──
api() { curl -sS -b "$JAR" -c "$JAR" -X "$1" "$BASE_URL/api/v1$2" -H 'content-type: application/json' ${3:+-d "$3"}; }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }
curl_login() { api POST /auth/login "{\"email\":\"$1\",\"password\":\"password123\"}" >/dev/null; }

trap 'ab close >/dev/null 2>&1 || true; rm -f "$JAR"' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 시드"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
curl_login demo@colab.dev
WS=$(api GET /me | jget '["workspaces"][0]["id"]')
uid() { api GET "/workspaces/$WS/members?limit=100" | python3 -c "import sys,json;print([m['user']['id'] for m in json.load(sys.stdin)['items'] if m['user']['email']=='$1'][0])"; }
SEO=$(uid seoyeon@colab.dev); JUN=$(uid junho@colab.dev)
AGENTS=$(api GET "/workspaces/$WS/agents" | python3 -c "import sys,json;print(' '.join(a['id'] for a in json.load(sys.stdin)['items']))")
LEAD=$(echo "$AGENTS" | cut -d' ' -f1)
ROOM=$(api POST "/workspaces/$WS/rooms" '{"name":"결제팀","description":"결제 관련 논의와 작업"}' | jget '["id"]')
INFRA=$(api POST "/workspaces/$WS/rooms" '{"name":"인프라","description":"배포·모니터링"}' | jget '["id"]')
SECRET=$(api POST "/workspaces/$WS/rooms" '{"name":"인수합병 검토"}' | jget '["id"]')
api POST "/workspaces/$WS/rooms" '{"name":"디자인 리뷰"}' >/dev/null
api POST "/rooms/$ROOM/participants" "{\"user_id\":\"$SEO\"}" >/dev/null
api POST "/rooms/$ROOM/participants" "{\"user_id\":\"$JUN\"}" >/dev/null
api POST "/rooms/$ROOM/participants" "{\"agent_id\":\"$LEAD\"}" >/dev/null
api PUT "/rooms/$ROOM/deputy" "{\"user_id\":\"$SEO\"}" >/dev/null
api PATCH "/rooms/$ROOM" '{"limits":{"budget_usd":50,"max_concurrent_works":3},"autonomy":"guided"}' >/dev/null
api POST "/rooms/$ROOM/works" "{\"goal\":\"결제 실패율 주간 보고서 초안\",\"director_user_id\":\"$SEO\"}" >/dev/null
api POST "/rooms/$ROOM/links" "{\"target_room_id\":\"$INFRA\"}" >/dev/null
api POST "/__mock/rooms/$ROOM/reads" "{\"target_room_id\":\"$INFRA\",\"truncated\":true,\"recent_n\":40,\"age_ms\":3600000}" >/dev/null
api POST "/__mock/rooms/$ROOM/reads" "{\"target_room_id\":\"$INFRA\",\"recent_n\":20,\"age_ms\":600000}" >/dev/null
api POST "/__mock/rooms/$INFRA/reads" "{\"target_room_id\":\"$ROOM\",\"recent_n\":10,\"age_ms\":1200000}" >/dev/null
api POST "/__mock/rooms/$ROOM/reads" "{\"target_room_id\":\"$SECRET\",\"denied_reason\":\"originator_not_participant\",\"age_ms\":300000}" >/dev/null
api POST "/__mock/rooms/$ROOM/reads" "{\"target_room_id\":\"$SECRET\",\"denied_reason\":\"no_originator\",\"originator_email\":null,\"age_ms\":200000}" >/dev/null
api POST "/__mock/rooms/$ROOM/reads" "{\"target_room_id\":\"$INFRA\",\"denied_reason\":\"originator_left\",\"originator_email\":\"junho@colab.dev\",\"age_ms\":100000}" >/dev/null
PROP=$(api POST "/__mock/rooms/$ROOM/work-proposals" "{\"agent_id\":\"$LEAD\",\"goal\":\"월말 결제 대사(reconciliation) 자동화\",\"rationale\":\"지난 3주 동안 같은 대사 요청이 매주 들어왔습니다 — 끝을 정해 추적하면 반복을 줄일 수 있습니다\"}" | jget '["id"]')
PROP2=$(api POST "/__mock/rooms/$ROOM/work-proposals" "{\"agent_id\":\"$LEAD\",\"goal\":\"로그 대시보드 정리\",\"rationale\":\"대시보드가 둘로 갈려 있습니다\"}" | jget '["id"]')
api POST "/work-proposals/$PROP2/resolution" '{"action":"reject","reason":"인프라 방에서 이미 하는 중"}' >/dev/null
# 동시 미션 상한 — 따로 한 방(상한 1, 열린 미션 1)
CAP=$(api POST "/workspaces/$WS/rooms" '{"name":"상한 1 방"}' | jget '["id"]')
api PATCH "/rooms/$CAP" '{"limits":{"max_concurrent_works":1}}' >/dev/null
api POST "/rooms/$CAP/works" '{"goal":"릴리스 노트 정리"}' >/dev/null
echo "  room=$ROOM infra=$INFRA proposal=$PROP cap=$CAP"

step "로그인(소유자 · 데모)"
ab set viewport 1280 900 >/dev/null
login demo@colab.dev

for theme in light dark; do
  set_theme "$theme"
  step "01·02 — S19 참여자 ($theme)"
  open_wait "/rooms/$ROOM/participants" '[data-testid="rd-part-list"]'
  ab wait '[data-testid="rd-invite-person"]' --timeout 20000 >/dev/null || true
  assert_js 'document.querySelectorAll("[data-testid=rd-part-row]").length === 4' "사람 3 + 에이전트 1 — 한 목록"
  assert_js 'document.querySelector("[data-testid=rd-part-leave]").getAttribute("aria-disabled") === "true"' "방장 본인 — 나가기 비활성"
  assert_js '[...document.querySelectorAll("[data-testid=rd-part-row]")].some(r => r.textContent.includes("Director 입니다 — 먼저 Director 를 교체하세요"))' "서연(Director) — 내보내기 거부 사유"
  shot "r2-w3-01-s19-people-$theme"
  ab click '[data-testid="rd-invite-tab-agents"]' >/dev/null
  ab wait '[data-testid="rd-invite-agents-head"]' --timeout 20000 >/dev/null
  ab scrollintoview '[data-testid="rd-invite-agents-head"]' >/dev/null 2>&1 || true
  shot "r2-w3-02-s19-agents-$theme"

  step "03 — S20 방 설정 ($theme)"
  open_wait "/rooms/$ROOM/settings" '[data-testid="rd-settings-group-lifecycle"]'
  assert_js 'document.querySelectorAll("[data-testid^=rd-settings-group-][data-testid$=-impact]").length === 8' "묶음 여덟 · 영향 한 줄씩"
  shot_full "r2-w3-03-s20-settings-$theme"

  step "04 — S24 참고 방 링크 ($theme)"
  open_wait "/rooms/$ROOM/settings/links" '[data-testid="rd-link-row"]'
  ab wait '[data-testid="rd-link-candidate"]' --timeout 20000 >/dev/null
  shot "r2-w3-04-s24-links-$theme"

  step "05 — S23 맥락 읽기 기록 ($theme)"
  open_wait "/rooms/$ROOM/reads" '[data-testid="rd-reads-denied"]'
  assert_js '!document.querySelector("[data-testid=rd-reads-denied]").textContent.includes("인수합병")' "거부 행 — 방 이름 없음(존재 숨김)"
  assert_js 'document.querySelector("[data-testid=rd-reads-denied]").textContent.includes("인프라 방을 떠나")' "originator_left — PRD 문장째(방 이름 포함)"
  shot_full "r2-w3-05-s23-reads-$theme"

  step "06 — S21 새 미션 ($theme)"
  open_wait "/rooms/$ROOM?work=new" '[data-testid="rd-create-work-assignee"]'
  ab fill '[data-testid="rd-create-work-goal"]' '결제 실패율을 원인별로 정리한 10쪽 보고서' >/dev/null
  ab select '[data-testid="rd-create-work-assignee"]' "$LEAD" >/dev/null
  ab click '[data-testid="rd-create-work-more"] summary' >/dev/null
  ab fill '[data-testid="rd-create-work-budget"]' '20' >/dev/null
  assert_js 'document.querySelector("[data-testid=rd-create-work-condition-sentence]").textContent.includes("보고서 제출 (@Lead) 그리고 Director 승인")' "담당 → 종료 조건 문장"
  assert_js 'document.querySelector("[data-testid=rd-create-work-budget-line]").textContent.includes("방 한도 $50 중 이 미션에 $20")' "예산 줄"
  shot_full "r2-w3-06-s21-new-$theme"

  step "07 — S26 미션 제안 ($theme)"
  open_wait "/rooms/$ROOM?work_proposal=$PROP" '[data-testid="rd-proposal-accept"]'
  shot "r2-w3-07-s26-proposal-$theme"
done
set_theme light

step "08 — S21 동시 미션 상한(밝음)"
open_wait "/rooms/$CAP?work=new" '[data-testid="rd-create-work-cap"]'
ab fill '[data-testid="rd-create-work-goal"]' '두 번째 미션' >/dev/null
assert_js 'document.querySelector("[data-testid=rd-create-work-open]").getAttribute("aria-disabled") === "true"' "상한 — 열기 비활성"
shot_full r2-w3-08-s21-cap-light

step "10 — S26 이미 거절된 제안(밝음)"
open_wait "/rooms/$ROOM?work_proposal=$PROP2" '[data-testid="rd-proposal-resolved"]'
shot r2-w3-10-s26-resolved-light

step "09 — S20 방 참여자(권한 밖, 준호) — 읽기 전용"
logout
login junho@colab.dev
set_theme light
open_wait "/rooms/$ROOM/settings" '[data-testid="rd-settings-group-visibility"]'
assert_js 'document.querySelector("[data-testid=rd-settings-save-limits]").getAttribute("aria-disabled") === "true"' "저장 비활성"
shot r2-w3-09-s20-readonly-light

echo; echo "✓ 끝 — $SHOT_DIR/r2-w3-*.png"
