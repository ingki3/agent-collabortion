#!/usr/bin/env python3
"""claim 탭 JSONL 에서 한 task(·attempt)의 **브리프 전문**을 꺼낸다.

턴 프롬프트(`prompt_of.py`)와 달리 브리프는 `TaskBundle.brief.text` 로 따로 온다.
harness v0.9.14 부터 결정 기록은 브리프가 아니라 턴 프롬프트 `<room_decisions>` 에 있다 —
여기서는 브리프에 [6]·[7] 이 **없는지**를 잰다.

사용: brief_of.py <tap.jsonl> <task_id> [attempt] [--last]
"""
import json, sys

tap, want = sys.argv[1], sys.argv[2]
rest = sys.argv[3:]
last = "--last" in rest
attempt = next((int(a) for a in rest if a.isdigit()), None)

hits = []
for line in open(tap):
    try:
        d = json.loads(line)
    except Exception:
        continue
    for t in (d.get("body") or {}).get("tasks") or []:
        task = t.get("task") or {}
        if task.get("id") != want:
            continue
        if attempt is not None and int(task.get("attempt") or 0) != attempt:
            continue
        hits.append((t.get("brief") or {}).get("text") or "")
if last:
    hits = hits[-1:]
sys.stdout.write("\n".join(hits))
