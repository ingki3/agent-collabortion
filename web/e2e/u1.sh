#!/usr/bin/env bash
# U1 "민지의 첫 15분" E2E — agent-browser 스크립트 (EVAL_USER U1 1~13단계).
#
# 1부(서버만): 가입(S2) → 온보딩 1단계 워크스페이스 → 2단계 S12 인라인 **대기 중** 단계까지 검증 + 스크린샷.
# 2부(데몬 페어링 후, Integrator 가 실행): 준비 완료 → 3단계 Lead 생성 → S6(제목·goal) → S7(goal 시스템 메시지 · 참여자 칩 · 에이전트 답글).
#
# 사용:
#   BASE_URL=http://localhost:3000 bash e2e/u1.sh            # 1부만(S12 대기에서 종료, exit 0)
#   FULL=1 bash e2e/u1.sh                                     # 2부까지 — 데몬이 붙을 때까지 최대 PAIR_TIMEOUT 초 대기
#   MOCK=1 FULL=1 bash e2e/u1.sh                              # 목 API(COLAB_MOCK_API=1 로 띄운 web)에서 페어링을 흉내 내 2부까지
# 스크린샷: web/__screenshots__/u1-*.png
set -euo pipefail
cd "$(dirname "$0")/.."

BASE_URL="${BASE_URL:-http://localhost:3000}"
FULL="${FULL:-0}"
MOCK="${MOCK:-0}"
PAIR_TIMEOUT="${PAIR_TIMEOUT:-600}"
SHOT_DIR="__screenshots__"
STAMP="$(date +%s)"
EMAIL="${E2E_EMAIL:-minji+${STAMP}@example.com}"
# U1 3단계의 이름은 "마케팅팀" 이지만 실행마다 접미어를 붙인다 — 서버가 **같은 슬러그로 두 번째 워크스페이스를 만들 때 500** 을 내기
# 때문이다(auth.go createWorkspace: 유일 제약 재시도가 같은 tx 안 → `25P02 current transaction is aborted`. G3 S-4 와 같은 결함,
# 위치만 다름). 이 스크립트는 W 범위라 서버를 못 고치므로 이름을 유일하게 해 재실행 가능하게 두고, 결함은 S 스트림에 보고했다.
WORKSPACE_NAME="${E2E_WORKSPACE_NAME:-마케팅팀 ${STAMP}}"
PASSWORD="${E2E_PASSWORD:-password123}"
NAME="${E2E_NAME:-민지}"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-u1-${STAMP}}"

ab() { agent-browser "$@"; }
# agent-browser screenshot [selector] [path] [--full]
# 전체 페이지 플래그는 `--full`(`-f`)이고 **경로 뒤**에 둔다. `--full-page` 는 없는 옵션이라 경로 인자로 잡혀
# 저장소에 `--full-page` 라는 파일이 생긴다(PR #21 리뷰 R3).
shot() { ab screenshot "$SHOT_DIR/$1.png" >/dev/null; echo "  📸 $SHOT_DIR/$1.png"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
step() { echo; echo "▶ $*"; }
fail() { echo "✗ $*" >&2; shot "u1-failure" || true; ab close >/dev/null 2>&1 || true; exit 1; }
# 텍스트가 나타날 때까지 (초)
wait_text() { ab wait --text "$1" --timeout "$((${2:-25} * 1000))" >/dev/null || fail "'$1' 이(가) 보이지 않음"; }
# testid 의 data-status 가 원하는 값이 될 때까지
wait_status() { local sel="$1" want="$2" secs="${3:-30}"; ab wait --fn "document.querySelector('[data-testid=\"$sel\"]')?.getAttribute('data-status')==='$want'" --timeout "$((secs * 1000))" >/dev/null || fail "$sel 이 $want 가 아님"; }

trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"
ab set viewport 1280 900 >/dev/null

# ── 1단계: 가입(S2) ──
step "U1-1 회원가입 S2 ($EMAIL)"
ab open "$BASE_URL/signup" >/dev/null
ab wait '[data-testid="signup-form"]' >/dev/null
ab fill 'input[name=display_name]' "$NAME" >/dev/null
ab fill 'input[name=email]' "$EMAIL" >/dev/null
ab fill 'input[name=password]' "$PASSWORD" >/dev/null
shot "u1-01-signup"
ab click 'button[type=submit]' >/dev/null

# ── 2단계: 온보딩 S4-1 ──
step "U1-2 온보딩 1단계 — 워크스페이스 이름"
ab wait --url "**/onboarding" >/dev/null || fail "온보딩으로 이동하지 않음"
ab wait '[data-testid="workspace-name"]' >/dev/null
ab wait '[data-testid="workspace-skip"]' >/dev/null || fail "S4-1 건너뛰기 링크 없음 (U1 2단계)"
shot "u1-02-onboarding-workspace"

# ── 3단계: 워크스페이스 생성 → S4-2 (S12 인라인) ──
step "U1-3 '$WORKSPACE_NAME' → 2단계 컴퓨터 연결(S12 인라인)"
ab fill '[data-testid="workspace-name"]' "$WORKSPACE_NAME" >/dev/null
ab click '[data-testid="workspace-next"]' >/dev/null
ab wait '[data-testid="pairing-panel"]' >/dev/null
ab wait '[data-testid="install-cmd-1"]' >/dev/null
ab wait '[data-testid="install-cmd-2"]' >/dev/null
wait_status pairing-status waiting 10
CMD1="$(ab get text '[data-testid="install-cmd-1"] code')"
CMD2="$(ab get text '[data-testid="install-cmd-2"] code')"
echo "  설치 명령 1: $CMD1"
echo "  설치 명령 2: $CMD2"
[ -n "$CMD1" ] && [ -n "$CMD2" ] || fail "설치 명령 2줄이 비어 있음"
ab wait '[data-testid="stage-waiting"][aria-current="step"]' >/dev/null
shot "u1-03-s12-waiting"
echo "✓ 1부 통과 — S12 '대기 중' 단계까지 (설치 명령 2줄 + 4단계 표시)"

if [ "$FULL" != "1" ]; then
  echo
  echo "2부(데몬 페어링 이후)는 FULL=1 로 실행합니다. 데몬은 위 명령 2줄로 붙입니다."
  exit 0
fi

# ── 4단계: 데몬 연결 대기 → 준비 완료 ──
step "U1-4 데몬 연결 대기 (대기 중 → 연결됨 → CLI 감지 중 → 준비 완료)"
if [ "$MOCK" = "1" ]; then
  # 목: 데몬 대신 목 전용 advance 엔드포인트로 페어링 단계를 진행시킨다
  PAIRING_ID="$(curl -s "$BASE_URL/api/v1/__mock/last-pairing" | sed -E 's/.*"id":"([^"]+)".*/\1/')"
  [ -n "$PAIRING_ID" ] || fail "목 페어링 id 를 얻지 못함"
  curl -s -X POST "$BASE_URL/api/v1/__mock/pairings/$PAIRING_ID/advance?to=ready" >/dev/null
fi
wait_status pairing-status ready "$PAIR_TIMEOUT"
wait_text "Claude Code" 10
shot "u1-04-s12-ready"
ab click '[data-testid="pairing-next"]' >/dev/null

# ── 5·6단계: 에이전트 (P1: Lead 하나) ──
step "U1-5/6 3단계 에이전트 — Lead 생성 (템플릿은 P2)"
ab wait '[data-testid="agent-create"]' >/dev/null
shot "u1-05-onboarding-agent"
ab click '[data-testid="agent-create"]' >/dev/null

# ── 7~13단계: S6 goal → 시작 → S7 ──
step "U1-7~12 S6 새 세션 — 제목·goal 만 입력, 나머지 기본값"
ab wait --url "**/sessions/new" >/dev/null || fail "S6 로 이동하지 않음"
ab wait '[data-testid="session-defaults"]' >/dev/null
ab fill '[data-testid="session-title"]' "결제 시장 조사" >/dev/null
ab fill '[data-testid="session-goal"]' "국내 B2B SaaS 결제 시장 조사 보고서 10페이지" >/dev/null
shot "u1-07-s6-goal"
ab click '[data-testid="session-start"]' >/dev/null

step "U1-13 S7 — goal 시스템 메시지 · 참여자 칩 · 에이전트 응답(실시간)"
ab wait --url "**/sessions/*" >/dev/null || fail "S7 로 이동하지 않음"
ab wait '[data-testid="session-detail"]' >/dev/null
# 첫 시스템 메시지: 실서버는 `Session started. Goal: …`(영문), 목 API 는 `세션 시작 — goal: …` — 둘 다 허용(G3 W-3)
ab wait --fn "['Session started. Goal:','세션 시작 — goal'].some(t => document.body.innerText.includes(t))" --timeout 15000 >/dev/null || fail "goal 시스템 메시지가 보이지 않음(실서버 'Session started. Goal:' / 목 '세션 시작 — goal')"
ab wait '[data-testid="participants"] [data-testid="agent-chip"]' >/dev/null || fail "참여자 칩 없음"
shot "u1-13-s7-started"
# 에이전트 답글이 새로고침 없이 도착하는지(실시간). 실서버는 데몬 실행 시간이 있으므로 넉넉히 기다린다.
ab wait --fn "document.querySelectorAll('[data-testid=\"message-card\"]').length >= 2" --timeout "$((PAIR_TIMEOUT * 1000))" >/dev/null || fail "에이전트 답글이 타임라인에 오지 않음"
shot "u1-13b-s7-agent-reply"
# 작성창 멘션 → 전송 → 답글
ab fill '[data-testid="composer-input"]' "@" >/dev/null
ab wait '[data-testid="mention-menu"]' >/dev/null || fail "멘션 자동완성이 열리지 않음"
ab press Enter >/dev/null
ab type '[data-testid="composer-input"]' "인사해줘" >/dev/null
ab wait '[data-testid="chip-trigger"]' >/dev/null
shot "u1-14-composer-mention"
ab click '[data-testid="composer-send"]' >/dev/null
ab wait --fn "document.querySelectorAll('[data-testid=\"message-card\"]').length >= 4" --timeout "$((PAIR_TIMEOUT * 1000))" >/dev/null || fail "멘션 답글이 오지 않음"
ab find testid activity-toggle click >/dev/null 2>&1 || true
ab wait '[data-testid="activity-rail"]' >/dev/null 2>&1 || true
shot_full "u1-15-s7-reply-and-rail"
echo "✓ 2부 통과 — U1 13단계 + 멘션 왕복"
