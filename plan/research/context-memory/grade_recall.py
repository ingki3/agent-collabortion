#!/usr/bin/env python3
"""T-CTX0 회상 문항 채점기 (recall-set-v1.json).

결정적 채점: 문항의 근거 조각 중 하나라도 「맥락 텍스트」에 (공백 정규화 후) 그대로 있으면 포함(1).
에이전트 채점: judge=agent 문항은 포함이 필요조건이고, 맥락만으로 정답을 짜 맞출 수 있는지를
에이전트가 판정해 recall-set 의 agent_grade 에 적어 둔다. --agent 를 주면 그 값을 쓴다
(포함 0 인데 agent_grade 1 인 경우는 모순이라 0 으로 센다).

사용:
  python3 grade_recall.py SET.json CONTEXT.txt [CONTEXT2.txt …] [--agent] [--json]
  python3 grade_recall.py SET.json --selftest   # 채점기가 무는지(주입) 확인
"""
import json, re, sys
from collections import defaultdict


def norm(s):
    return re.sub(r"\s+", " ", s).strip()


def grade(qs, ctx, use_agent=False):
    c = norm(ctx)
    rows = []
    for q in qs:
        hit = any(norm(e["snippet"]) in c for e in q["evidence"])
        score = int(hit)
        if use_agent and q.get("judge") == "agent":
            ag = q.get("agent_grade", {}).get("score")
            score = int(hit and ag == 1)
        rows.append({"id": q["id"], "type": q["type"], "judge": q["judge"], "included": int(hit), "score": score})
    return rows


def summarize(rows):
    by = defaultdict(lambda: [0, 0])
    for r in rows:
        by[r["type"]][0] += r["score"]
        by[r["type"]][1] += 1
    total = sum(r["score"] for r in rows)
    return {"total": total, "n": len(rows), "rate": total / len(rows) if rows else 0,
            "by_type": {k: {"score": v[0], "n": v[1], "rate": v[0] / v[1]} for k, v in sorted(by.items())}}


def selftest(qs):
    """주입: 모든 근거를 담은 맥락은 30/30, 근거 하나를 지우면 그 문항만 0, 공백만 바뀐 맥락은 그대로."""
    full = "\n".join(e["snippet"] for q in qs for e in q["evidence"])
    ok = summarize(grade(qs, full))["total"] == len(qs)
    target = qs[0]
    removed = "\n".join(e["snippet"] for q in qs for e in q["evidence"] if q is not target)
    rows = grade(qs, removed)
    ok &= [r["id"] for r in rows if r["score"] == 0] == [target["id"]]
    spaced = full.replace(" ", "  \n ")
    ok &= summarize(grade(qs, spaced))["total"] == len(qs)
    empty = summarize(grade(qs, ""))["total"] == 0
    print("selftest", "PASS" if ok and empty else "FAIL")
    return ok and empty


def main(argv):
    set_path = argv[1]
    doc = json.load(open(set_path))
    qs = doc["questions"]
    if "--selftest" in argv:
        sys.exit(0 if selftest(qs) else 1)
    files = [a for a in argv[2:] if not a.startswith("--")]
    ctx = "\n".join(open(f).read() for f in files)
    rows = grade(qs, ctx, use_agent="--agent" in argv)
    s = summarize(rows)
    if "--json" in argv:
        json.dump({"summary": s, "rows": rows}, sys.stdout, ensure_ascii=False, indent=1)
        return
    for r in rows:
        print(f'{r["id"]} {r["type"]:<18} {r["judge"]:<5} included={r["included"]} score={r["score"]}')
    print(f'TOTAL {s["total"]}/{s["n"]} ({s["rate"]:.0%})')
    for k, v in s["by_type"].items():
        print(f'  {k:<18} {v["score"]}/{v["n"]}')


if __name__ == "__main__":
    main(sys.argv)
