#!/usr/bin/env bash
# e2e/p5/ci.sh — CI 가 부르는 한 줄: 스택 기동 → 72~78 · 81 · 82 · 84 · 88 · 90 · 91 · 92 · 93 → 표 → 종료. 로컬에서도 같은 명령으로 돈다.
#
# 실기 대조 스위치(T-I6): 아래 표의 스크립트는 `RUNTIME=real` 로 같은 스크립트가 실기(claude_code·hermes 로그인)에서 돈다.
#   | 스크립트 | RUNTIME=fake(CI) | RUNTIME=real(로컬) |
#   | 72_~77_  | acpfake 대본     | 실기 A~D·성능·보안 (p2 10_·p3 52_/53_·p4 61_ 의 판정) |
#   | 78_      | 앱 셸 200        | + agent-browser DOM |
#   | 81_ 84_ 88_ 90_ 91_ | 데몬 없음(curl·바이너리) — 모드 무관 | 같음 |
#   | 82_      | 세 층 게이트·관찰 표·lane.actions 전부 | 절 R 만: claude_code reviewer 1턴(브리프 [2]·delegate 시도 여부) |
#   | 92_      | acpfake exec(`colab message post`, --reply-to 없이) | Lead haiku 2턴(최상위·스레드 질문) |
#   | 93_      | acpfake exec(`colab message post --detail-file`) | Lead haiku 조사 턴 — --detail 로 나누는지 관측(N/A) |
#   | 83_      | (CI 에 없다 — 실기 고정) | T-D13 데몬 몫: 로그·MCP argv·툴 목록·[2] 인용 |
#
#   CI:    PG_EXTERNAL=1 PSQL_URL=postgres://… RUNTIME=fake bash e2e/p5/ci.sh     (.github/workflows/ci.yml e2e job)
#   로컬:  bash e2e/p5/ci.sh                                                    (docker Postgres colab-pg-i5)
#   실기:  RUNTIME=real bash e2e/p5/ci.sh                                       (claude_code·hermes 로그인 필요, CI 는 안 돈다)
#
# 스크립트는 하나씩 돈다(서로 다른 워크스페이스·데몬·포트지만 CI 러너의 CPU 가 좁다). 실패한 스크립트가 있어도
# 나머지를 다 돌리고 마지막에 표를 낸다 — 한 줄 실패로 뒤의 증거를 잃지 않게. 종료 코드는 실패 수.
source "$(dirname "$0")/lib_i5.sh"
cd "$E2E_ROOT"
SCRIPTS="${SCRIPTS:-72_scenario_a 73_scenario_b 74_scenario_c 75_scenario_d 76_perf 77_security 78_web_s7 81_observations_commands 82_role_gate 84_cli_allowed_commands 88_room_gate 90_room_read 91_works 92_thread_reply 93_message_detail}"
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
  # 판정 표는 두 모양이다 — lib_i5(p2 관례: id·what·verdict·value, 헤더 1줄, 판정이 3열) 와 lib.sh(70_·81_·84_: id·PASS/FAIL·want·got·note,
  # 헤더 없음, 판정이 2열). 판정 값이 있는 열을 센다.
  p="$(awk -F'\t' '$2=="PASS" || $3=="PASS"' "$chkf" 2>/dev/null | wc -l | tr -d ' ')"
  q="$(awk -F'\t' '$2=="FAIL" || $3=="FAIL"' "$chkf" 2>/dev/null | wc -l | tr -d ' ')"
  na="$(awk -F'\t' '$2=="N/A" || $3=="N/A"' "$chkf" 2>/dev/null | wc -l | tr -d ' ')"
  st=PASS; [ "$rc" = 0 ] || { st=FAIL; FAILS=$((FAILS+1)); }
  nas=""; [ "${na:-0}" -gt 0 ] && nas=" (N/A $na)"
  ROWS+=("$s\t$st\t$p/$q$nas\t${dt}s\t$OUT/$s.log")
  if [ "$rc" != 0 ]; then
    echo "::group::$s FAILED (rc=$rc) — 마지막 60줄"; tail -60 "$OUT/$s.log"; echo "::endgroup::"
    [ -f "$chkf" ] && { echo "::group::$s FAIL 행"; awk -F'\t' '$2=="FAIL" || $3=="FAIL"' "$chkf"; echo "::endgroup::"; }
  fi
done
printf '\n== e2e/p5 (%s, RUNTIME=%s, %ds) ==\n' "$(git rev-parse --short HEAD)" "$RUNTIME" "$(( $(date +%s) - T_ALL0 ))"
printf 'script\tresult\tpass/fail\ttime\tlog\n'; printf '%b\n' "${ROWS[@]}"
{ printf 'script\tresult\tpass/fail\ttime\tlog\n'; printf '%b\n' "${ROWS[@]}"; } > "$OUT/ci-summary.tsv"
exit "$FAILS"
