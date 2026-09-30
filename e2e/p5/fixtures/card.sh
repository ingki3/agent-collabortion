#!/usr/bin/env bash
# e2e/p5/fixtures/card.sh — 작업 카드(PRD FR-3.8 · colab-cli v0.9.10) 대본 조각.
#   source 하면 delegate_card · report_card 함수, 실행하면 `card.sh delegate NAME GOAL [METHOD]` ·
#   `card.sh report [met|partial|unmet]` (ACPFAKE_PROMPT 의 <task_card> 를 읽는다).
#   log 함수가 없으면(인라인 exec) 조용히 넘어간다.
# `type log` 는 macOS 의 /usr/bin/log 를 찾아 참이 된다(그 log 는 실패해 `&& colab status set done` 을 끊었다) — 함수만 본다.
declare -F log >/dev/null 2>&1 || log() { :; }
P="${P:-${ACPFAKE_PROMPT:-}}"
# delegate_card NAME GOAL [METHOD] — 카드 파일을 써서 `colab card delegate --file`.
delegate_card() {
  local f; f="$(mktemp "${TMPDIR:-/tmp}/card.XXXXXX")"
  jq -nc --arg a "$1" --arg g "$2" --arg m "${3:-review}" \
    '{agent:$a, goal:$g, criteria:[{text:($g+" — 결과를 보고한다"), method:$m}], boundaries:"맡은 것 밖의 파일은 건드리지 않는다"}' > "$f"
  colab card delegate --file "$f" 2>&1; rm -f "$f"
}
# 카드 task(<task_card> 가 있는 턴)면 결과 카드를 낸다 — 기준마다 한 번(harness v0.9.16). FAKE_NO_REPORT=1 이면 내지 않는다
# (턴 종료 게이트 → result_card_missing 후속을 재는 대본). VERDICT 기본 met(근거는 커밋 해시 형식).
report_card() {
  printf '%s\n' "$P" | grep -q '^<task_card ' || return 0
  [ "${FAKE_NO_REPORT:-0}" = 1 ] && { log "report skipped (FAKE_NO_REPORT)"; return 0; }
  local n f out
  n="$(printf '%s\n' "$P" | awk '/^<task_card /{f=1} f && /^Criteria:/{c=1;next} c && /^[0-9]+\. /{k++} c && /^Do not:/{print k+0; exit}')"
  f="$(mktemp "${TMPDIR:-/tmp}/result.XXXXXX")"
  jq -nc --argjson n "${n:-1}" --arg v "${1:-met}" \
    '{summary:"맡은 일을 했습니다.", verdicts:[range(1;$n+1) | {criterion:., verdict:$v, evidence:(if $v=="met" then [{kind:"commit",ref:"e2e0000"}] else [] end), note:(if $v=="met" then null else "모자란 부분이 있습니다" end)}], confirmed:["대본이 확인"], assumed:[]}' > "$f"
  out="$(colab card report --file "$f" 2>&1)"; rm -f "$f"
  log "card report → $(printf '%s' "$out" | tr -d '\n' | cut -c1-160)"
  return 0
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  case "${1:-}" in
    delegate) shift; delegate_card "$@" ;;
    report)   shift; report_card "$@" ;;
    *) echo "usage: card.sh delegate NAME GOAL [METHOD] | report [VERDICT]" >&2; exit 2 ;;
  esac
fi
