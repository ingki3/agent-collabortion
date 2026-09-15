#!/usr/bin/env python3
"""claim 탭(claimtap.py)이 남긴 JSONL 에서 특정 task 의 턴 프롬프트를 꺼낸다. 사용: prompt_of_task.py <tap.jsonl> <task_id>"""
import json, sys
tap, want = sys.argv[1], sys.argv[2]
for line in open(tap):
    try:
        d = json.loads(line)
    except Exception:
        continue
    for t in (d.get("body") or {}).get("tasks") or []:
        if (t.get("task") or {}).get("id") == want:
            sys.stdout.write(t.get("prompt") or "")
