#!/usr/bin/env python3
"""claim 탭 JSONL 에서 특정 task 의 TaskBundle.resume(runtime_session_ref) 를 꺼낸다.
사용: resume_of_task.py <tap.jsonl> <task_id>  → JSON 한 줄 (없으면 null)"""
import json, sys
tap, want = sys.argv[1], sys.argv[2]
out = None
for line in open(tap, encoding="utf-8"):
    try:
        d = json.loads(line)
    except Exception:
        continue
    for t in (d.get("body") or {}).get("tasks") or []:
        if (t.get("task") or {}).get("id") == want and t.get("resume") is not None:
            out = t.get("resume")
print(json.dumps(out, ensure_ascii=False))
