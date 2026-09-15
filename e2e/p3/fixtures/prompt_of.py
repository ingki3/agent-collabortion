#!/usr/bin/env python3
"""claim 탭 JSONL 에서 한 task 의 **특정 attempt** 턴 프롬프트를 꺼낸다.

`e2e/p2/fixtures/prompt_of_task.py` 는 같은 task 의 모든 claim 을 이어 붙인다 — 재개(attempt 2)를
재는 T-I3 에서는 attempt 1 과 2 의 프롬프트가 섞여 "답변이 프롬프트에 들어갔다" 가 항상 참이 된다.

사용: prompt_of.py <tap.jsonl> <task_id> [attempt] [--last]
      attempt 를 주면 그 attempt 만, --last 면 마지막 claim 하나만.
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
        hits.append(t.get("prompt") or "")
if last:
    hits = hits[-1:]
sys.stdout.write("\n".join(hits))
