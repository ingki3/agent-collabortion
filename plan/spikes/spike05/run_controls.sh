#!/usr/bin/env bash
# plan/spikes/spike05/run_controls.sh — 대조군 2종.
#   nohide : 마커를 append 하되 숨기지 않는다 (PRD §8.4 가 걱정한 (b) "에이전트가 커밋해버린다" 의 실측)
#   meta   : claude_code 의 v1 계약 경로(_meta.systemPrompt.append) — 디스크에 아무것도 쓰지 않는다
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$HERE/out}"
run() { local rt=$1 lay=$2 ss=$3 mode=$4 reps=$5 i
  for i in $(seq 1 "$reps"); do
    echo "=== $rt/$lay/$ss/$mode rep$i ($(date +%T)) ===" >&2
    python3 "$HERE/spike05.py" --runtime "$rt" --layout "$lay" --setting-sources "$ss" \
      --mode "$mode" --rep "$i" --out "$OUT" >> "$OUT/console.log" 2>&1 \
      || echo "!! FAILED $rt/$lay/$ss/$mode rep$i" >&2
  done
}
run hermes      plain empty   nohide 2
run claude_code plain project nohide 2
run claude_code plain empty   meta   2
echo "controls done $(date +%T)" >&2
