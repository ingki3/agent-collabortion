#!/usr/bin/env bash
# T-R1.5 스크린샷 — 문구 전환(「세션」→ 방·미션 · 「작업 줄기」→ 서브 미션, PRD §3.2 · SCREEN §3.4) before/after.
# 같은 스크립트를 origin/dev 빌드(before)와 이 브랜치 빌드(after)에 돌린다 — 둘 다 `next build && next start`(목 모드).
# 문구만 바뀐 PR 이라 단언은 화면이 떴는지만 본다(before 빌드에는 새 문구가 없다).
#
#   r15-01-s5-rooms-<tag>.png        S5 방 목록
#   r15-02-s7-room-<tag>.png         S7 방 화면 — 서브 미션 보드 · 작성창 「새 서브 미션으로 보내기」 · 우열 종료 조건(「아티팩트 제출」)
#   r15-03-s8-inbox-<tag>.png        S8 받은 요청 — 미션 일시정지 · 컴퓨터 유예 만료 카드(after: room_paused, before: runtime_offline)
#   r15-04-s11-computers-<tag>.png   S11 연결된 컴퓨터 — 「쓰는 중인 방」 펼침
#
# 사용:
#   COLAB_MOCK_API=1 npx next build && COLAB_MOCK_API=1 npx next start -p 3161 &
#   BASE_URL=http://localhost:3161 TAG=after bash e2e/r15-shots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3161}"
TAG="${TAG:-after}"
SHOT_DIR="${SHOT_DIR:-__screenshots__}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-r15-shots-$$}"
JAR="$(mktemp)"

ab() { agent-browser "$@"; }
shot() { apic '(function(){window.scrollTo(0,0);return "ok"})()' >/dev/null; sleep 0.5; ab screenshot "$SHOT_DIR/$1-$TAG.png" >/dev/null; echo "  📸 $SHOT_DIR/$1-$TAG.png"; }
step() { echo; echo "▶ $*"; }
apic() { ab eval "$1" --json | python3 -c 'import sys,json;print(json.load(sys.stdin)["data"]["result"])'; }
open_wait() { ab open "$BASE_URL$1" >/dev/null; ab wait "$2" --timeout 20000 >/dev/null || { sleep 3; ab wait "$2" --timeout 20000 >/dev/null; }; sleep 1; }
api() { curl -sS -b "$JAR" -c "$JAR" -X "$1" "$BASE_URL/api/v1$2" -H 'content-type: application/json' ${3:+-d "$3"}; }
jget() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval('d'+sys.argv[1]))" "$1"; }

trap 'ab close >/dev/null 2>&1 || true; rm -f "$JAR"' EXIT
mkdir -p "$SHOT_DIR"

step "목 저장소 초기화 · 시드"
curl -sS -X POST "$BASE_URL/api/v1/__mock/reset" -o /dev/null
api POST /auth/login '{"email":"demo@colab.dev","password":"password123"}' >/dev/null
WS=$(api GET /me | jget '["workspaces"][0]["id"]')
RT=$(api POST /__mock/runtimes '{"name":"office-pc","repos":[{"path":"/work/colab","remote_url":"git@github.com:acme/colab.git","clean":true}]}' | jget '["id"]')
TPL=$(api GET "/workspaces/$WS/agent-templates" | jget '[0]["key"]')
api POST "/workspaces/$WS/agent-templates/$TPL/apply" '{}' >/dev/null
A1=$(api GET "/workspaces/$WS/agents" | jget '["items"][0]["id"]')
A2=$(api GET "/workspaces/$WS/agents" | jget '["items"][1]["id"]')
S=$(api POST "/__mock/workspaces/$WS/seed-room" "{\"title\":\"결제 모듈 구현\",\"goal\":\"Backend·Frontend 가 각자 구현하고 QA 가 리뷰한다\",\"isolation\":{\"kind\":\"none\"},\"runtime_id\":\"$RT\",\"participants\":[{\"agent_id\":\"$A1\"},{\"agent_id\":\"$A2\"}],\"assignee_agent_id\":\"$A1\"}" | jget '["id"]')
api POST "/__mock/rooms/$S/seed-lanes" '{"statuses":["running","blocked","done"]}' >/dev/null
api POST "/__mock/rooms/$S/seed-hitl" '{}' >/dev/null
api POST /__mock/inbox/seed '{}' >/dev/null
echo "  ws=$WS session/room=$S runtime=$RT"

ab open "$BASE_URL/login" >/dev/null
ab wait '[data-testid="login-form"]' --timeout 20000 >/dev/null
ab fill 'input[name="email"]' demo@colab.dev >/dev/null
ab fill 'input[name="password"]' password123 >/dev/null
ab click 'button[type="submit"]' >/dev/null
ab wait '[data-testid="app-nav"]' --timeout 20000 >/dev/null
ab set viewport 1440 900 >/dev/null

step "S5 방 목록"
open_wait /rooms '[data-testid="page-head"]'
shot r15-01-s5-rooms

step "S7 방 화면"
open_wait "/rooms/$S" '[data-testid="lane-board"]'
shot r15-02-s7-room

step "S11 연결된 컴퓨터 — 쓰는 중인 방"
open_wait /runtimes '[data-testid="runtime-sessions-toggle"]'
ab click '[data-testid="runtime-sessions-toggle"]' >/dev/null
sleep 1
shot r15-04-s11-computers

step "S8 받은 요청 — 컴퓨터 유예 만료(8일) 뒤"
api POST "/__mock/runtimes/$RT/offline" '{"days":8}' >/dev/null
open_wait /inbox '[data-testid="inbox-page"]'
shot r15-03-s8-inbox
