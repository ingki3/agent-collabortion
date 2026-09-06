#!/usr/bin/env bash
# plan/spikes/spike05/run_all.sh — 스파이크 5 실기 매트릭스.
# 각 행 = (런타임 × 레이아웃 × settingSources × 전달 방식) 2회.
# 산출물: out/runs.jsonl (한 줄 = 한 런), out/<run_id>.stderr.log
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
OUT="${OUT:-$HERE/out}"
mkdir -p "$OUT"
run() {  # run <runtime> <layout> <setting-sources> <mode> <reps>
  local rt=$1 lay=$2 ss=$3 mode=$4 reps=$5 i
  for i in $(seq 1 "$reps"); do
    echo "=== $rt/$lay/$ss/$mode rep$i ($(date +%T)) ===" >&2
    python3 "$HERE/spike05.py" --runtime "$rt" --layout "$lay" --setting-sources "$ss" \
      --mode "$mode" --rep "$i" --out "$OUT" >> "$OUT/console.log" 2>&1 \
      || echo "!! FAILED $rt/$lay/$ss/$mode rep$i" >&2
  done
}

# (1)(2)(3) 평면 체크아웃 — PLAN 기본안(마커 append + skip-worktree)
run claude_code plain   empty   marker 2
run claude_code plain   project marker 2
run hermes      plain   empty   marker 2
# (4) worktree 격리
run claude_code worktree empty   marker 2
run claude_code worktree project marker 2
run hermes      worktree empty   marker 2
# 우회 A: 별도 파일 + import 지시 (마커는 한 줄)
run claude_code plain   project import 2
run hermes      plain   empty   import 2
# 우회 B: 추적 파일을 아예 건드리지 않는다 (턴 프롬프트가 브리프 파일을 가리킨다)
run claude_code plain   empty   prompt_file 2
run hermes      plain   empty   prompt_file 2
echo "done $(date +%T)" >&2
