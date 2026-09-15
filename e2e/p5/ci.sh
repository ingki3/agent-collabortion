#!/usr/bin/env bash
# e2e/p5/ci.sh — CI 가 부르는 한 줄: 스택 기동 → 72~78 → 표 → 종료. 로컬에서도 같은 명령으로 돈다.
#
#   CI:    PG_EXTERNAL=1 PSQL_URL=postgres://… RUNTIME=fake bash e2e/p5/ci.sh     (.github/workflows/ci.yml e2e job)
#   로컬:  bash e2e/p5/ci.sh                                                    (docker Postgres colab-pg-i5)
#   실기:  RUNTIME=real bash e2e/p5/ci.sh                                       (claude_code·hermes 로그인 필요, CI 는 안 돈다)
#
# 스크립트는 하나씩 돈다(서로 다른 워크스페이스·데몬·포트지만 CI 러너의 CPU 가 좁다). 실패한 스크립트가 있어도
# 나머지를 다 돌리고 마지막에 표를 낸다 — 한 줄 실패로 뒤의 증거를 잃지 않게. 종료 코드는 실패 수.
source "$(dirname "$0")/lib_i5.sh"
cd "$E2E_ROOT"
SCRIPTS="${SCRIPTS:-72_scenario_a 73_scenario_b 74_scenario_c 75_scenario_d 76_perf 77_security 78_web_s7}"
T_ALL0="$(date +%s)"
bash e2e/p5/up_i5.sh || { echo "::error::e2e/p5/up.sh failed"; exit 1; }
trap 'bash e2e/p5/down_i5.sh >/dev/null 2>&1 || true' EXIT
declare -a ROWS; FAILS=0
for s in $SCRIPTS; do
  f="e2e/p5/$s.sh"; [ -f "$f" ] || { ROWS+=("$s\tSKIP\t-\t-\t(없음)"); continue; }
  t0="$(date +%s)"
  if bash "$f" > "$OUT/$s.log" 2>&1; then rc=0; else rc=$?; fi
  dt=$(( $(date +%s) - t0 ))
  n="${s%%_*}"; chkf="$OUT/$n-checks.tsv"
  p="$(awk -F'\t' 'NR>1 && $3=="PASS"' "$chkf" 2>/dev/null | wc -l | tr -d ' ')"
  q="$(awk -F'\t' 'NR>1 && $3=="FAIL"' "$chkf" 2>/dev/null | wc -l | tr -d ' ')"
  na="$(awk -F'\t' 'NR>1 && $3=="N/A"' "$chkf" 2>/dev/null | wc -l | tr -d ' ')"
  st=PASS; [ "$rc" = 0 ] || { st=FAIL; FAILS=$((FAILS+1)); }
  nas=""; [ "${na:-0}" -gt 0 ] && nas=" (N/A $na)"
  ROWS+=("$s\t$st\t$p/$q$nas\t${dt}s\t$OUT/$s.log")
  if [ "$rc" != 0 ]; then
    echo "::group::$s FAILED (rc=$rc) — 마지막 60줄"; tail -60 "$OUT/$s.log"; echo "::endgroup::"
    [ -f "$chkf" ] && { echo "::group::$s FAIL 행"; awk -F'\t' 'NR>1 && $3=="FAIL"' "$chkf"; echo "::endgroup::"; }
  fi
done
printf '\n== e2e/p5 (%s, RUNTIME=%s, %ds) ==\n' "$(git rev-parse --short HEAD)" "$RUNTIME" "$(( $(date +%s) - T_ALL0 ))"
printf 'script\tresult\tpass/fail\ttime\tlog\n'; printf '%b\n' "${ROWS[@]}"
{ printf 'script\tresult\tpass/fail\ttime\tlog\n'; printf '%b\n' "${ROWS[@]}"; } > "$OUT/ci-summary.tsv"
exit "$FAILS"
