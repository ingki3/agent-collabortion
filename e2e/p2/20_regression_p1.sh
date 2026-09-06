#!/usr/bin/env bash
# e2e/p2/20_regression_p1.sh — P1 시나리오 01~07 을 **P2 스택**(포트가 다른 전용 서버·웹·Postgres)에 그대로 돌린다.
#
# 왜: P2 가 라우터·lane·합류·아티팩트를 넣으면서 P1 의 수직 슬라이스를 깨지 않았는가. 회귀는 게이트마다 다시 잰다.
# 순서는 e2e/p1/README.md 그대로: 01 → 03 → 02 → 05 → 06 → 04 → 07. 01 이 만든 out/a-ids.txt 를 뒤가 쓴다.
#
# 사용: bash e2e/p2/up.sh && bash e2e/p2/20_regression_p1.sh          # 01 은 N=20 (G3 와 같은 횟수)
#       N=5 bash e2e/p2/20_regression_p1.sh                            # 비용을 줄일 때
# 산출물: out/p1/*.json (각 시나리오 요약) · out/regression.tsv
source "$(dirname "$0")/lib.sh"
export E2E_OUT="$OUT/p1"; mkdir -p "$E2E_OUT"
export SERVER_URL WEB_URL PG_PORT PG_CONTAINER
N="${N:-20}"
# 04·06 은 agent-browser 를 쓴다. 다른 세션이 열려 있으면 새 창이 net::ERR_ABORTED 로 죽는 것을
# 2026-09-06 실행에서 봤다(디버깅용 세션과 충돌). 먼저 전부 닫는다.
agent-browser close --all >/dev/null 2>&1 || true
TSV="$OUT/regression.tsv"; echo -e "script\texit\tnote" > "$TSV"
run() { # script [env...]
  local sh="$1"; shift
  step "e2e/p1/$sh"
  local t0; t0="$(date +%s)"
  if env "$@" bash "$E2E_ROOT/e2e/p1/$sh" > "$OUT/p1/${sh%.sh}.log" 2>&1; then rc=0; else rc=$?; fi
  local note; note="$(tail -3 "$OUT/p1/${sh%.sh}.log" | tr '\n' ' ' | tr -s ' ' | head -c 200)"
  printf '%s\t%s\t%s\n' "$sh" "$rc" "$note" >> "$TSV"
  [ "$rc" = 0 ] && ok "$sh ($(( $(date +%s) - t0 ))s)" || bad "$sh exit=$rc ($(( $(date +%s) - t0 ))s) — $OUT/p1/${sh%.sh}.log"
}
run 01_vertical_slice.sh "N=$N"
run 03_cancel.sh
run 02_kill9.sh
run 05_invite_api.sh
run 06_s12_pairing_realtime.sh
run 04_u1_browser.sh
run 07_adversarial.sh
step "결과"
column -t -s $'\t' "$TSV" >&2
