#!/usr/bin/env bash
# e2e/p5/lib.sh — P5 스모크 공통 헬퍼 (T-S12 부터).
#
# `e2e/p4/lib.sh`(→ p3 → p2 → p1) 를 그대로 재사용하고 **포트·컨테이너만 분리**한다
# (P3_TASKS §0-13). 같은 머신에 다른 워커의 스택이 떠 있다 — T-I4(:8105/:5450) ·
# T-C6(:8104/:5449) · T-S9(:8103/:5448) 등. 덮어쓰려면 미리 export 한다.
#
# T-S12(테스트 채팅·지표) 배정: server :8107 · pg :5451 · 컨테이너 colab-pg-s12.
export SERVER_URL="${SERVER_URL:-http://localhost:8107}"
export WEB_URL="${WEB_URL:-http://localhost:3019}"
export PG_PORT="${PG_PORT:-5451}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-s12}"
P5_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P5_DIR/out}"
mkdir -p "$E2E_OUT"
source "$P5_DIR/../p4/lib.sh"

# ── 데몬 역할을 curl 로 (데몬 없이) ─────────────────────────────────────────
# T-S12 의 70_ 은 데몬 바이너리를 띄우지 않는다: claim → phase → events → heartbeat → finish 를
# daemon-protocol §4 모양 그대로 curl 로 흉내내 **서버 쪽 계약**만 잰다. 데몬 몫(§4.5 의
# 토큰 없는 환경·mkdir·gc rm -rf)은 T-D12 가 따로 잰다.
#
# daemon_api PATH JSON → 응답 본문 (DTOK 는 pair 가 준 데몬 토큰)
daemon_api() {
  curl -sS -X POST "$SERVER_URL/v1/daemon/$1" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d "$2"
}
# daemon_api_code PATH JSON → HTTP 코드만
daemon_api_code() {
  curl -sS -o /dev/null -w '%{http_code}' -X POST "$SERVER_URL/v1/daemon/$1" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -d "$2"
}
# pair_curl WS NAME CAPS_JSON WORKDIR_ROOT → "runtime_id<TAB>daemon_token" — 페어링 + probe(§3)
pair_curl() {
  local ws="$1" name="$2" caps="$3" root="$4" code rt
  code="$(api_ok POST "/workspaces/$ws/runtimes/pairings" "$(jq -nc --arg n "$name" '{name:$n}')" | jq -r .pairing_token)"
  rt="$(curl -sS -X POST "$SERVER_URL/v1/daemon/pair" -H 'Content-Type: application/json' \
        -d "$(jq -nc --arg c "$code" --arg h "$name" '{pairing_code:$c,hostname:$h,os:"darwin",daemon_version:"0.1.0"}')")"
  DTOK="$(jq -r .daemon_token <<<"$rt")"
  RID="$(jq -r .runtime_id <<<"$rt")"
  daemon_api "runtimes/$RID/probe" "$(jq -nc --arg h "$name" --argjson caps "$caps" --arg root "$root" \
    '{daemon_version:"0.1.0",hostname:$h,capabilities:$caps,repos:[],workdir_root:$root,disk:{used_bytes:0},colab_cli:{present:true,version:"0.1.0"}}')" >/dev/null
  printf '%s\t%s' "$RID" "$DTOK"
}

# ── 판정 표 ─────────────────────────────────────────────────────────────────
# chk ID EXPECT ACTUAL NOTE — out/<번호>-checks.tsv 에 한 줄, 화면에 ok/FAIL
CHECKS="${CHECKS:-$E2E_OUT/checks.tsv}"
FAILS=0
chk() {
  local id="$1" want="$2" got="$3" note="${4:-}" res=PASS
  [ "$want" = "$got" ] || { res=FAIL; FAILS=$((FAILS+1)); }
  printf '%s\t%s\t%s\t%s\t%s\n' "$id" "$res" "$want" "$got" "$note" >> "$CHECKS"
  if [ "$res" = PASS ]; then ok "$id  $note"; else bad "$id  want=[$want] got=[$got]  $note"; fi
}
# chk_ge ID MIN ACTUAL NOTE — 숫자 하한
chk_ge() {
  local id="$1" min="$2" got="$3" note="${4:-}" res=FAIL
  awk -v a="$got" -v b="$min" 'BEGIN{exit !(a+0>=b+0)}' && res=PASS || FAILS=$((FAILS+1))
  printf '%s\t%s\t>=%s\t%s\t%s\n' "$id" "$res" "$min" "$got" "$note" >> "$CHECKS"
  if [ "$res" = PASS ]; then ok "$id  $note ($got)"; else bad "$id  want>=$min got=$got  $note"; fi
}
