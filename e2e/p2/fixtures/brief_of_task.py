#!/usr/bin/env python3
"""claim 탭 JSONL 에서 특정 task 의 TaskBundle.brief 를 꺼낸다.
첫 줄에 transport, 그 뒤에 본문. 사용: brief_of_task.py <tap.jsonl> <task_id>"""
import json, sys
tap, want = sys.argv[1], sys.argv[2]
for line in open(tap, encoding="utf-8"):
    try:
        d = json.loads(line)
    except Exception:
        continue
    for t in (d.get("body") or {}).get("tasks") or []:
        if (t.get("task") or {}).get("id") == want:
            b = t.get("brief") or {}
            print(b.get("transport", ""))
            sys.stdout.write(b.get("text", ""))
            raise SystemExit(0)
