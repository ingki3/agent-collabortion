#!/usr/bin/env bash
# 컴포넌트 스토리 스크린샷 — /dev/components · /dev/badges 를 전체 페이지로 찍는다.
# 사용: BASE_URL=http://localhost:3000 bash e2e/dev-shots.sh   → web/__screenshots__/dev-*.png
#
# agent-browser screenshot [selector] [path] [--full] — 전체 페이지 플래그는 `--full`(`-f`)이며 경로 **뒤**에 온다.
# `--full-page` 는 없는 옵션이라 경로로 해석돼 `web/--full-page` 파일이 생긴다(PR #21 리뷰 R3). 쓰지 말 것.
set -euo pipefail
cd "$(dirname "$0")/.."

BASE_URL="${BASE_URL:-http://localhost:3000}"
SHOT_DIR="__screenshots__"
export AGENT_BROWSER_SESSION="${AGENT_BROWSER_SESSION:-colab-dev-shots-$$}"

ab() { agent-browser "$@"; }
shot_full() { ab screenshot "$SHOT_DIR/$1.png" --full >/dev/null; echo "  📸 $SHOT_DIR/$1.png (full)"; }
trap 'ab close >/dev/null 2>&1 || true' EXIT
mkdir -p "$SHOT_DIR"
ab set viewport 1280 900 >/dev/null

for page in components badges; do
  ab open "$BASE_URL/dev/$page" >/dev/null
  ab wait '[data-testid="story"]' --timeout 15000 >/dev/null 2>&1 || true
  shot_full "dev-$page"
done
