#!/usr/bin/env bash
# e2e/p5/fixtures/ledger_flow.sh ROLE — 102_mission_ledger.sh 의 페이크 대본(acpfake exec, 턴마다 한 번).
#   프롬프트(ACPFAKE_PROMPT)의 내용으로 할 일을 고른다. 흔적은 $FAKE_OUT/102-<tag> 에 남긴다.
#
#   Lead : 첫 턴(<mission_ledger> 없음) → plan · fact(given) · assignment · open_question · lesson 두 번(같은 내용 →
#          support 2) 을 원장에 쓰고 memory get 으로 읽는다.
#          <mission_ledger> 가 있는 턴 → 프롬프트에서 원장·② 모양을 관측해 기록한다.
#   R    : 일반 턴 → plan 쓰기 시도(역할 밖 → exit 3) · Lead 의 fact 를 supersede · 같은 fact 를 다시 supersede(→ exit 3
#          memory_not_active) · open_question 철회.
set -u
ROLE="$1"; P="${ACPFAKE_PROMPT:-}"; O="${FAKE_OUT:-/tmp}"
has() { printf '%s' "$P" | grep -qF -- "$1"; }
rec() { printf '%s\n' "$2" >> "$O/102-$1"; }
ecode() { printf '%s' "$1" | jq -r "$2" 2>/dev/null | head -1; }
between() { printf '%s' "$P" | awk -v a="$1" -v b="$2" 'index($0,a){on=1} on{print} index($0,b){if(on) exit}'; }

role_Lead() {
  if has "<mission_ledger "; then
    led="$(between "<mission_ledger " "</mission_ledger>")"
    mm="$(between "<mission_messages " "</mission_messages>")"
    rec lead-ledger-seen "1"
    rec lead-ledger-fact-new "$(printf '%s' "$led" | grep -c '분당 120회')"
    rec lead-ledger-fact-old "$(printf '%s' "$led" | grep -c '분당 60회')"
    rec lead-ledger-lesson "$(printf '%s' "$led" | grep -c 'lesson (dead_end, support 2) 스크래핑은 막힌다')"
    rec lead-ledger-plan "$(printf '%s' "$led" | grep -c '] plan 1) 수집')"
    rec lead-ledger-question "$(printf '%s' "$led" | grep -c 'open_question')"
    rec lead-mm-lines "$(printf '%s' "$mm" | grep -cE '^- \[[0-9a-f-]{36}\] [^:]+: ')"
    rec lead-mm-detail "$(printf '%s' "$mm" | grep -c '<detail')"
    # 블록 자리: ② 다음, ③ 앞.
    order="$(printf '%s' "$P" | grep -nE '^(<mission_messages |<mission_ledger |<room_decisions |<roster_status>)' | cut -d: -f2 | cut -c1-16 | tr '\n' '|')"
    rec lead-order "$order"
    colab message post --body "원장 확인했습니다" >/dev/null 2>&1
  else
    out="$(colab memory note --kind plan --content "1) 수집 2) 표 정리 3) 초안 — 카드로 나눈다" 2>&1)"; rec lead-plan "$?"
    out="$(colab memory note --kind fact --certainty given --content "API 한도는 분당 60회" 2>&1)"; rec lead-fact "$?"
    printf '%s' "$out" | jq -r '.id // empty' > "$O/102-fact-id"
    out="$(colab memory note --kind assignment --content "R: 수집 · Lead: 정리" 2>&1)"; rec lead-assign "$?"
    out="$(colab memory note --kind open_question --content "연결/별도 중 무엇으로 쓰나?" 2>&1)"; rec lead-question "$?"
    printf '%s' "$out" | jq -r '.id // empty' > "$O/102-question-id"
    out="$(colab memory note --kind lesson --outcome dead_end --content "스크래핑은 막힌다 — 공식 API 를 쓴다" 2>&1)"; rec lead-lesson1 "$? $(ecode "$out" '.support_count')"
    out="$(colab memory note --kind lesson --outcome dead_end --content "스크래핑은 막힌다 — 공식 API 를 쓴다" 2>&1)"; rec lead-lesson2 "$? $(ecode "$out" '.support_count')"
    out="$(colab memory get 2>/dev/null)"; rec lead-get "$? $(printf '%s' "$out" | jq -r 'if type=="array" then length else (.items // [] | length) end' 2>/dev/null)"
    colab message post --body "원장에 계획과 사실을 적었습니다" >/dev/null 2>&1
  fi
}

role_R() {
  out="$(colab memory note --kind plan --content "내가 계획을 바꾼다" 2>/dev/null)"; rec r-plan "$? $(ecode "$out" '.error.code // .code // "-"')"
  fid="$(cat "$O/102-fact-id" 2>/dev/null)"
  out="$(colab memory supersede "$fid" --content "API 한도는 분당 120회 (문서 확인)" 2>&1)"; rec r-supersede "$?"
  out="$(colab memory supersede "$fid" --content "또 바꾼다" 2>/dev/null)"; rec r-supersede-again "$? $(ecode "$out" '.error.code // .code // "-"')"
  qid="$(cat "$O/102-question-id" 2>/dev/null)"
  out="$(colab memory retire "$qid" --reason "Director 가 연결 기준으로 정했다" 2>&1)"; rec r-retire "$?"
  colab message post --body "한도 값을 고쳤습니다" >/dev/null 2>&1
}

case "$ROLE" in
  Lead|R) "role_$ROLE" ;;
esac
exit 0
