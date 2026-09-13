#!/usr/bin/env bash
# e2e/p5/fixtures/agent.sh ROLE [ARG] — 페이크 런타임의 **대본**(모델 답 자리).
#
# acpfake 의 `exec` 스텝이 런타임 프로세스의 환경(데몬이 준 COLAB_* + PATH) 과 CWD(workdir) 그대로
# 이 스크립트를 부른다. 턴 프롬프트는 ACPFAKE_PROMPT 로 온다 — 실물 서버가 만든 프롬프트다. 여기서
# `<trigger>` 를 읽어 역할별로 **진짜 `colab` CLI** 를 부른다(위임·게시·아티팩트·HITL·리뷰·결정).
# 판단 규칙은 실기 지시문(72_~75_ 의 *_INS)과 같은 것을 셸로 옮긴 것이다. 모델이 없을 뿐,
# 서버·데몬·CLI 가 보는 것은 실기와 같다.
#
# 흔적: $FAKE_OUT/agent-trace.tsv (시각·역할·task·무엇을 했나)
set -u
ROLE="$1"; ARG="${2:-}"
P="${ACPFAKE_PROMPT:-}"
TRIG="$(printf '%s\n' "$P" | awk '/^<trigger>/{f=1;next} /^<\/trigger>/{f=0} f')"
AUTHOR="$(printf '%s' "$TRIG" | sed -n 's/.*author="\([^"]*\)".*/\1/p' | head -1)"
RESUMED=0; printf '%s\n' "$P" | grep -q '^<resumed' && RESUMED=1
TRACE="${FAKE_OUT:-.}/agent-trace.tsv"
log()  { printf '%s\t%s\t%s\t%s\t%s\n' "$(date +%H:%M:%S)" "$ROLE" "${COLAB_TASK_ID:-}" "${COLAB_TASK_ATTEMPT:-}" "$*" >> "$TRACE"; }
has()  { printf '%s' "$TRIG" | grep -qF -- "$1"; }
phas() { printf '%s' "$P" | grep -qF -- "$1"; }
post() { # post BODY [MENTION]
  local out; out="$(colab message post --body "$1" ${2:+--mention "$2"} 2>&1)"; log "post → $(printf '%s' "$out" | tr -d '\n' | cut -c1-160)"; }
done_() { colab status set done >/dev/null 2>&1; log "status done"; }
submit() { # submit TYPE FILE NAME → artifact id (stdout)
  local out; out="$(colab artifact submit --type "$1" ${2:+--file "$2"} --name "$3" 2>&1)"
  log "artifact submit $3 → $(printf '%s' "$out" | tr -d '\n' | cut -c1-200)"
  printf '%s' "$out" | jq -r '.artifact_id // .artifact.id // empty' 2>/dev/null
}
# 위임 브리프의 지문: 멘션 링크를 벗긴 트리거 본문 첫 줄
brief_text() { printf '%s' "$TRIG" | sed -n '/^<message/,/^<\/message>/p' | sed '1d;$d' | sed -E 's/^(\[@[^]]*\]\([^)]*\)[[:space:]]*)+//' | head -1; }
log "turn: author=${AUTHOR:-?} resumed=$RESUMED arg=$ARG trigger=$(printf '%s' "$TRIG" | tr '\n' ' ' | cut -c1-120)"

case "$ROLE" in
# ── 시나리오 A ───────────────────────────────────────────────────────────────
Lead)
  # 턴 1 위임 3 → 턴 2 합류(Researcher) 종합 + Writer 위임 → 턴 3 합류(Writer) 마무리. 멘션은 하지 않는다
  # (멘션하면 규칙 3 재진입으로 "Lead 가 깨어난 횟수 = 3" 이 4가 된다 — p2 fixtures/scenario_a_agents.sh).
  if has "- Writer:"; then
    post "초안이 제출됐습니다. 수고했습니다."
  elif has "위임한 작업이 모두 끝났습니다"; then
    post "종합: 1) 시장 규모 — 완만한 성장 2) 경쟁 — 주요 5종 3) 채널 — 온라인 중심."
    out="$(colab lane delegate --agent Writer --brief "위 종합을 바탕으로 보고서 초안을 파일로 쓰고 artifact 로 제출하라" 2>&1)"
    log "delegate(Writer) → $(printf '%s' "$out" | tr -d '\n' | cut -c1-120)"
  else
    for t in "시장 규모와 성장률" "경쟁 제품 다섯 가지" "가격대와 구매 채널"; do
      out="$(colab lane delegate --agent Researcher --brief "가상의 스마트 물병 제품 X 의 $t 를 조사해 요약하라" 2>&1)"
      log "delegate($t) → $(printf '%s' "$out" | tr -d '\n' | cut -c1-120)"
    done
    post "계획: 세 항목을 Researcher 에게 병렬로 위임했습니다."
  fi
  done_ ;;
Researcher)
  topic="$(brief_text)"
  printf '# 조사 메모\n%s\n' "$topic" > note.md
  post "조사 결과 — ${topic:-항목}: 근사치로 정리했습니다. (1) 국내 시장은 완만한 성장, (2) 주요 경쟁 5종, (3) 온라인 채널 중심." Lead
  done_ ;;
Writer)
  if [ "$RESUMED" = 1 ] || phas "<hitl_answer" || phas "Director"; then
    # HITL 답을 받은 뒤 — 3000자 이상의 초안을 쓰고 아티팩트로 제출 (S-66 의 자극과 같은 길이)
    python3 - > report.md <<'PY'
para = "스마트 물병 제품 X 시장 조사 보고서 초안. 타깃 독자는 투자자다. 국내 시장은 완만하게 성장하며, 주요 경쟁 제품 다섯 종이 온라인 채널을 중심으로 경쟁한다. 가격대는 중가 이상이 다수이며 구매 결정 요인은 배터리·앱 연동·디자인 순이다. "
print("# 보고서 초안\n")
for i in range(1, 16):
    print(f"## {i}. 절\n{para}\n")
PY
    aid="$(submit doc "$PWD/report.md" report.md)"
    post "보고서 초안을 제출했습니다 (artifact ${aid:-?}, $(wc -c < report.md | tr -d ' ') bytes)."
    done_
  else
    out="$(colab hitl ask --question "타깃 독자가 투자자인지 내부 경영진인지 정해 주세요" --default 투자자 --choices 투자자,경영진 2>&1)"
    log "hitl ask → $(printf '%s' "$out" | tr -d '\n' | cut -c1-160)"
  fi ;;
# ── 시나리오 B (worktree) ────────────────────────────────────────────────────
PM)
  if has "위임한 작업이 모두 끝났습니다"; then
    fe="$(colab session messages --limit 50 2>/dev/null | jq -r '.items[]?.content // empty' | grep -o 'FRONTEND-DIFF [0-9a-f-]*' | tail -1 | awk '{print $2}')"
    post "리뷰 부탁합니다. FRONTEND-DIFF ${fe:-?}" QA
  elif [ "$AUTHOR" = QA ]; then
    post "확인했습니다."
  else
    printf '# SPEC\n급수 시간 계산과 패널 표시를 각각 구현한다.\n' > SPEC.md
    for pair in "Backend:src/pump.py 의 water_seconds 를 구현하라" "Frontend:src/ui.py 의 render 를 고쳐라"; do
      out="$(colab lane delegate --agent "${pair%%:*}" --brief "${pair#*:}" 2>&1)"
      log "delegate(${pair%%:*}) → $(printf '%s' "$out" | tr -d '\n' | cut -c1-120)"
    done
    post "스펙을 썼고 Backend·Frontend 에 위임했습니다."
  fi
  done_ ;;
Backend)
  python3 - <<'PY'
import re
p="src/pump.py"; s=open(p).read()
s=s.replace("    return 0\n","    return 5 if moisture < 30 else 0\n")
open(p,"w").write(s)
PY
  git diff --stat >/dev/null 2>&1
  aid="$(submit diff "" backend)"
  post "BACKEND-DIFF ${aid:-?}"
  done_ ;;
Frontend)
  if [ "$AUTHOR" = QA ] || has "QA-FIX-9421"; then
    { printf '# QA-FIX-9421\n'; cat src/ui.py; } > src/ui.py.new && mv src/ui.py.new src/ui.py
  else
    python3 - <<'PY'
p="src/ui.py"; s=open(p).read()
s=s.replace('return "status: " + status','return "planter: " + status')
open(p,"w").write(s)
PY
  fi
  git diff --stat >/dev/null 2>&1
  aid="$(submit diff "" frontend)"
  post "FRONTEND-DIFF ${aid:-?}"
  done_ ;;
QA)
  fe="$(colab session messages --limit 50 2>/dev/null | jq -r '.items[]?.content // empty' | grep -o 'FRONTEND-DIFF [0-9a-f-]*' | tail -1 | awk '{print $2}')"
  if [ -z "$fe" ]; then fe="$(printf '%s\n' "$P" | grep -o 'FRONTEND-DIFF [0-9a-f-]*' | tail -1 | awk '{print $2}')"; fi
  log "review target frontend=$fe"
  colab artifact get "$fe" --out ./fe.diff >/dev/null 2>&1
  if grep -q 'QA-FIX-9421' ./fe.diff 2>/dev/null; then
    out="$(colab review approve --artifact "$fe" --note 승인 2>&1)"; log "approve → $(printf '%s' "$out" | tr -d '\n' | cut -c1-160)"
  else
    out="$(colab review reject --artifact "$fe" --reason "src/ui.py 맨 첫 줄에 주석 # QA-FIX-9421 한 줄을 추가해 주세요" 2>&1)"; log "reject → $(printf '%s' "$out" | tr -d '\n' | cut -c1-160)"
  fi
  done_ ;;
# ── 시나리오 C (긴 턴 + 개입) ───────────────────────────────────────────────
# ARG = 단계 번호 1~5 또는 fin. 첫 턴(Session started)은 다섯 단계를 **한 단계씩** 돈다 — 단계마다
# 파일 하나 + `PART-N done` 게시 + 잠깐 쉼. 그 사이에 Director 의 개입이 들어온다.
# 후속 턴(메시지·재지시)은 단계 1 에서만 답하고 나머지 단계는 아무것도 하지 않는다.
Rsearch*)
  if has "Session started"; then
    case "$ARG" in
      [1-5]) printf '# note %s\n' "$ARG" > "note-0$ARG.md"; post "PART-$ARG done"; sleep "${FAKE_STEP_SLEEP:-6}" ;;
      fin)   post "ALL-DONE"; done_ ;;
    esac
  elif [ "$ARG" = 1 ]; then
    if has "DECISION:"; then
      s="$(printf '%s' "$TRIG" | sed -n 's/.*DECISION: *\(.*\) | RATIONALE: *\(.*\)$/\1/p' | head -1)"
      r="$(printf '%s' "$TRIG" | sed -n 's/.*DECISION: *\(.*\) | RATIONALE: *\(.*\)$/\2/p' | head -1)"
      out="$(colab decision record --summary "$s" --rationale "$r" 2>&1)"; log "decision → $(printf '%s' "$out" | tr -d '\n' | cut -c1-120)"
      post "결정을 기록했습니다: $s"
    elif has "note-99.md"; then
      printf '# 보증 정책\n1년 무상 보증\n' > note-99.md; post "보증 정책 메모(note-99.md)를 썼습니다."
    elif has "note-06.md"; then
      printf '# 요약\n- 시장\n- 경쟁\n- 가격\n' > note-06.md; post "요약 세 줄을 note-06.md 에 덧붙였습니다."
    else
      post "네, 한국 시장으로 좁혀 반영하겠습니다."
    fi
    done_
  fi ;;
# ── 시나리오 D (폴백 뒤 대체 프로파일이 실제로 일을 끝낸다) ───────────────────
Faller|Lonely)
  printf '# 제품 Y 안내문\n%s\n' "여덟 줄 안내문(가상)" > guide-y.md
  aid="$(submit doc "$PWD/guide-y.md" product-y-guide.md)"
  log "guide artifact=$aid"
  done_ ;;
# ── 성능·보안 (76_·77_) ─────────────────────────────────────────────────────
Echo)   # 게시 → 답 한 줄 (지연 측정)
  post "echo: $(brief_text | cut -c1-60)"
  done_ ;;
Probe)  # 77_: 프로파일 env 로 받은 명령을 실행하고 결과를 남긴다 (권한 상승 시도 등)
  if [ -n "${FAKE_CMD:-}" ]; then
    out="$(sh -c "$FAKE_CMD" 2>&1)"; code=$?
    printf '%s\t%s\t%s\n' "${COLAB_TASK_ID:-}" "$code" "$(printf '%s' "$out" | tr '\n\t' '  ' | cut -c1-400)" >> "${FAKE_OUT:-.}/probe-results.tsv"
    log "probe cmd exit=$code"
  fi
  done_ ;;
*) log "unknown role"; done_ ;;
esac
exit 0
