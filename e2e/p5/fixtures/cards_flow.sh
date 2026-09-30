#!/usr/bin/env bash
# e2e/p5/fixtures/cards_flow.sh ROLE — 101_task_cards.sh 의 페이크 대본(acpfake exec, 턴마다 한 번).
#   프롬프트(ACPFAKE_PROMPT)의 블록으로 무엇을 할지 고른다 — 턴 번호가 아니라 내용으로.
#   흔적은 $FAKE_OUT/101-<tag> 파일에 남긴다(하네스가 읽는다).
#
#   Lead  : 목표 턴 → 카드 둘 위임(첫 시도는 기준 없는 카드 → card_invalid, 고쳐서 통과) ·
#           <result_cards> 턴 1 → C-1 accept · C-2 revise ·  <result_cards> 턴 2 → C-2 accept.
#   A     : 카드 턴 → 결과 카드(met) + status set done.
#   B     : 카드 v1 첫 턴 → 게시만, 결과 없이 턴 종료 · result_card_missing 후속 → 결과 카드 ·
#           카드 v2(수정 요청) → 결과 카드.
#   Asker : 목표 턴 → Q 에게 멘션으로 질문(카드 없이). 답이 오면 아무것도 안 한다.
#   Q     : 질문 턴 → artifact submit 을 시도(거부 기록) → 답을 게시.
set -u
ROLE="$1"; P="${ACPFAKE_PROMPT:-}"; O="${FAKE_OUT:-/tmp}"
FIX="$(cd "$(dirname "$0")" && pwd)"
source "$FIX/card.sh"
has() { printf '%s' "$P" | grep -qF -- "$1"; }
rec() { printf '%s\n' "$2" >> "$O/101-$1"; }
card_file() { # NAME GOAL CRITERIA_JSON
  local f; f="$(mktemp "${TMPDIR:-/tmp}/card.XXXXXX")"
  jq -nc --arg a "$1" --arg g "$2" --argjson c "$3" '{agent:$a, goal:$g, criteria:$c, boundaries:"맡은 것 밖의 파일은 건드리지 않는다"}' > "$f"
  printf '%s' "$f"
}
# bash 3.2(macOS): case 가지 안의 `$(…)` 속 `)` 가 가지 끝으로 읽힌다 — 역할마다 함수로 두고 case 는 부르기만 한다.
ask_q() { local qid; qid="$(cat "$O/101-q-id")"; colab message post --body "[@Q](mention://agent/$qid) 가격대가 얼마인가요?"; }
ecode() { printf '%s' "$1" | jq -r "$2" 2>/dev/null | head -1; }
role_Lead() {
  if has "<result_cards"; then
    n="$(cat "$O/101-lead-judge" 2>/dev/null || echo 0)"; echo $((n+1)) > "$O/101-lead-judge"
    if [ "$n" = 0 ]; then
      out="$(colab card accept C-1 2>&1)"; rec lead-accept1 "$?"
      out="$(colab card revise C-2 --reason "출처를 한 줄 더 붙여 주세요" 2>&1)"; rec lead-revise "$?"
    else
      out="$(colab card accept C-2 2>&1)"; rec lead-accept2 "$?"
    fi
  elif [ ! -s "$O/101-lead-delegated" ]; then
    f="$(card_file A "시장 규모를 조사한다" '[]')"
    out="$(colab card delegate --file "$f" 2>&1)"; rc=$?
    rec lead-invalid "$rc $(ecode "$out" '.error.code // .code // "-"')"
    f="$(card_file A "시장 규모를 조사한다" '[{"text":"규모를 숫자 하나로","method":"review"}]')"
    out="$(colab card delegate --file "$f" 2>&1)"; rec lead-deleg-a "$?"
    f="$(card_file B "경쟁 제품을 조사한다" '[{"text":"경쟁 제품 셋","method":"review"}]')"
    out="$(colab card delegate --file "$f" 2>&1)"; rec lead-deleg-b "$?"
    echo 1 > "$O/101-lead-delegated"
  fi
}
role_A() {
  if has "<task_card "; then
    colab message post --body "A 결과: 3조 원" >/dev/null 2>&1
    report_card met; colab status set done >/dev/null 2>&1; rec a-done "ok"
  fi
}
role_B() {
  if has 'reason="result_card_missing"'; then
    report_card met; rec b-followup-report "ok"
  elif has "<task_card " && has 'version="2"'; then
    colab message post --body "B 결과 v2: 출처 추가" >/dev/null 2>&1
    report_card met; colab status set done >/dev/null 2>&1; rec b-v2 "ok"
  elif has "<task_card "; then
    colab message post --body "B 결과: 제품 셋" >/dev/null 2>&1; rec b-noreport "ok"
  fi
}
role_Asker() {
  if ! has "ANSWER"; then
    ask_q >/dev/null 2>&1; rec asker-asked "ok"
  fi
}
role_Q() {
  if has "This is a question from"; then
    printf 'x' > "$O/101-q.txt"
    out="$(colab artifact submit --type doc --name q.txt --file "$O/101-q.txt" 2>&1)"; rc=$?
    rec q-submit "$rc $(ecode "$out" '.error.code // "-"')"
    printf '%s' "$out" | jq -r '.error.detail // empty' 2>/dev/null > "$O/101-q-submit-detail"
    colab message post --body "ANSWER 만 원대입니다" >/dev/null 2>&1; rec q-answered "ok"
  fi
}
case "$ROLE" in
  Lead|A|B|Asker|Q) "role_$ROLE" ;;
esac
exit 0
