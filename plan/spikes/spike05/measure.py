#!/usr/bin/env python3
"""plan/spikes/spike05/measure.py — out/runs.jsonl → 보고서 표(markdown).

한 행 = (런타임 × 레이아웃 × settingSources × 전달 방식). 셀은 "성공/시도".
"""
import json, os, sys
from collections import OrderedDict

HERE = os.path.dirname(os.path.abspath(__file__))
path = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "out", "runs.jsonl")
rows = [json.loads(l) for l in open(path, encoding="utf-8") if l.strip()]

MODE_LABEL = {"marker": "마커 append + skip-worktree", "import": "우회 A: 별도 파일 + @import",
              "prompt_file": "우회 B: 미추적 파일 + 턴 프롬프트", "meta": "대조: _meta.systemPrompt(계약 v1)",
              "nohide": "대조: 마커 append, 숨기지 않음"}

groups = OrderedDict()
for r in rows:
    key = (r["runtime"], r["layout"], r.get("setting_sources_label", "empty"), r["mode"])
    groups.setdefault(key, []).append(r)


def frac(rs, f):
    n = sum(1 for r in rs if f(r))
    return f"{n}/{len(rs)}"


def yn(rs, key):
    return frac(rs, lambda r: bool(r.get(key)))


print("| 런타임 | 레이아웃 | settingSources | 전달 방식 | n | (1) 마커 읽음 | (1b) 원본 규칙 읽음 | 도구 0 | (2) 편집됨 | (2) 커밋됨 | 커밋에 마커 섞임 | (3) 복원 후 마커 0 | (3) 원본 무손상 | (3) 편집 보존 | (3) status 클린 |")
print("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
for (rt, lay, ss, mode), rs in groups.items():
    print("| `%s` | %s | `%s` | %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |" % (
        rt, lay, ss if rt == "claude_code" else "—", MODE_LABEL[mode], len(rs),
        yn(rs, "read_code"), yn(rs, "read_orig_rule"), yn(rs, "turn1_toolfree"),
        yn(rs, "file_has_note_after_edit"), yn(rs, "committed_new"), yn(rs, "head_has_marker"),
        yn(rs, "restore_no_marker"), yn(rs, "restore_original_intact"),
        yn(rs, "restore_note_kept"), yn(rs, "restore_clean")))

print()
print("### 에이전트가 본 커밋 결과 (turn 2 STEP2)")
print()
print("| 런타임 | settingSources | 전달 방식 | 에이전트가 보고한 커밋 결과 |")
print("|---|---|---|---|")
seen = set()
for (rt, lay, ss, mode), rs in groups.items():
    for r in rs:
        line = ""
        for l in (r.get("turn2_text") or "").splitlines():
            if l.startswith("STEP2="):
                line = l[len("STEP2="):].strip()
        k = (rt, ss, mode, line)
        if line and k not in seen:
            seen.add(k)
            print("| `%s` | `%s` | %s | `%s` |" % (rt, ss if rt == "claude_code" else "—", MODE_LABEL[mode], line[:110]))
