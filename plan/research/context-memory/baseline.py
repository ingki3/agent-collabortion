#!/usr/bin/env python3
"""T-CTX0 기준선: 스냅샷(colab-pg-ctx0 / snap)의 게임 제작 방을 이미 있는 행만으로 잰다.

task_usage · task_attempt · task_event · message · task · lane 만 읽는다(읽기 전용 SELECT).
출력: 표준출력에 JSON 한 벌(04-baseline.md 가 인용하는 수치의 원천).
사용: python3 plan/research/context-memory/baseline.py > /tmp/ctx0-baseline.json
      (PSQL 환경변수로 psql 명령을 바꿀 수 있다; 기본은 docker exec colab-pg-ctx0)
"""
import json, os, statistics, subprocess, sys
from collections import Counter, defaultdict

ROOM = os.environ.get("ROOM", "02aa7e66-b326-4def-830d-ee13f2248770")
PSQL = os.environ.get("PSQL", "docker exec -i colab-pg-ctx0 psql -U colab -d snap -X -q -A -t")


def q(sql):
    """JSON 한 줄씩 돌려주는 SELECT(row_to_json)."""
    wrapped = f"SELECT row_to_json(x) FROM ({sql}) x"
    out = subprocess.run(PSQL.split() + ["-c", wrapped], capture_output=True, text=True, check=True).stdout
    return [json.loads(l) for l in out.splitlines() if l.strip()]


def pct(xs, p):
    xs = sorted(xs)
    if not xs:
        return None
    k = (len(xs) - 1) * p
    f = int(k)
    c = min(f + 1, len(xs) - 1)
    return xs[f] + (xs[c] - xs[f]) * (k - f)


def dist(xs):
    xs = [x for x in xs if x is not None]
    if not xs:
        return {"n": 0}
    return {"n": len(xs), "p10": pct(xs, .1), "p25": pct(xs, .25), "median": pct(xs, .5), "p75": pct(xs, .75),
            "p90": pct(xs, .9), "max": max(xs), "mean": statistics.mean(xs), "sum": sum(xs)}


# ── attempts: 한 행 = task × attempt ────────────────────────────────────────
att = q(f"""
SELECT t.id task, ta.attempt, a.name agent, a.role, t.lane_id lane, t.work_id work,
       ta.dispatched_at, ta.started_at, ta.finished_at, ta.resumed, ta.outcome,
       u.cost_usd::float8 usd, u.cache_read, u.input_tokens inp, u.output_tokens outp, u.model,
       (SELECT e.payload->>'session_id' FROM task_event e WHERE e.task_id=t.id AND e.attempt=ta.attempt
          AND e.class='runtime' AND e.verb='resume' ORDER BY e.seq LIMIT 1) resume_sid,
       (SELECT e.payload->>'runtime_kind' FROM task_event e WHERE e.task_id=t.id AND e.attempt=ta.attempt
          AND e.class='runtime' AND e.verb IN ('start','resume') ORDER BY e.seq LIMIT 1) rk,
       (SELECT e.usage FROM task_event e WHERE e.task_id=t.id AND e.attempt=ta.attempt AND e.class='usage'
          ORDER BY e.seq DESC LIMIT 1) last_usage
FROM task t JOIN agent a ON a.id=t.agent_id
JOIN task_attempt ta ON ta.task_id=t.id
LEFT JOIN task_usage u ON u.task_id=t.id AND u.attempt=ta.attempt
WHERE t.session_id='{ROOM}'
ORDER BY ta.dispatched_at NULLS LAST""")

for r in att:
    lu = r.get("last_usage") or {}
    r["cache_write"] = lu.get("cache_write_tokens")
    r["cr_ev"] = lu.get("cache_read_tokens")

# ── 도구 호출: tool_call_id 하나 = 호출 하나, permission 제외 ────────────────
calls = q(f"""
SELECT DISTINCT ON (e.task_id, e.attempt, e.payload->>'tool_call_id')
       e.task_id task, e.attempt, a.name agent, e.verb,
       CASE WHEN e.verb='use_tool' THEN split_part(e.payload->>'title',' ',1) ELSE e.verb END kind,
       e.payload->>'command' cmd
FROM task_event e JOIN task t ON t.id=e.task_id JOIN agent a ON a.id=t.agent_id
WHERE t.session_id='{ROOM}' AND e.class='tool' AND e.verb<>'permission' AND e.payload ? 'tool_call_id'
ORDER BY e.task_id, e.attempt, e.payload->>'tool_call_id', (e.payload ? 'command') DESC, e.seq DESC""")

# ── 턴 중 usage 시계열(호출별 cache_read 증가) ─────────────────────────────
usage_ev = q(f"""
SELECT e.task_id task, e.attempt, a.name agent, e.seq, e.ts,
       (e.usage->>'cache_read_tokens')::bigint cr, (e.usage->>'cache_write_tokens')::bigint cw,
       (e.usage->>'input_tokens')::bigint inp
FROM task_event e JOIN task t ON t.id=e.task_id JOIN agent a ON a.id=t.agent_id
WHERE t.session_id='{ROOM}' AND e.class='usage' ORDER BY e.task_id, e.attempt, e.seq""")

out: dict = {"room": ROOM}

# 1) 에이전트별 task 당 비용·cache_read
by_agent = defaultdict(lambda: defaultdict(float))
per_task = defaultdict(lambda: {"usd": 0.0, "cr": 0, "cw": 0, "inp": 0, "agent": None})
for r in att:
    pt = per_task[r["task"]]
    pt["agent"] = r["agent"]
    pt["usd"] += r["usd"] or 0
    pt["cr"] += r["cache_read"] or 0
    pt["cw"] += r["cache_write"] or 0
    pt["inp"] += r["inp"] or 0
agents = sorted({r["agent"] for r in att})
calls_by_task = Counter((c["task"]) for c in calls)
out["agents"] = {}
for ag in agents:
    ts = [v for k, v in per_task.items() if v["agent"] == ag]
    tids = [k for k, v in per_task.items() if v["agent"] == ag]
    ncalls = sum(calls_by_task[t] for t in tids)
    cr_sum = sum(v["cr"] for v in ts)
    cw_sum = sum(v["cw"] for v in ts)
    inp_sum = sum(v["inp"] for v in ts)
    out["agents"][ag] = {
        "tasks": len(ts), "attempts": sum(1 for r in att if r["agent"] == ag),
        "usd": dist([v["usd"] for v in ts]), "cache_read": dist([v["cr"] for v in ts]),
        "cache_write": dist([v["cw"] for v in ts]),
        "tool_calls": ncalls, "tool_calls_per_task": dist([calls_by_task[t] for t in tids]),
        "cache_read_per_call": (cr_sum / ncalls) if ncalls else None,
        "input_total": inp_sum, "cache_read_total": cr_sum, "cache_write_total": cw_sum,
        "cache_write_share": cw_sum / (cr_sum + cw_sum + inp_sum) if (cr_sum + cw_sum + inp_sum) else None,
        "runtime_kinds": dict(Counter(r["rk"] for r in att if r["agent"] == ag)),
    }
out["room_usd"] = sum(v["usd"] for v in per_task.values())

# 2) 도구 종류 분포(에이전트별) + 셸 명령 분류(Lead)
def shell_class(cmd):
    c = (cmd or "").lower()
    if "chrome" in c or "node " in c or "playwright" in c or "puppeteer" in c or "npx" in c:
        return "browser/node test"
    if "python" in c or "ffmpeg" in c or "sox" in c or "convert " in c or "magick" in c:
        return "python/media"
    if c.startswith(("grep", "sed", "cat", "head", "tail", "ls", "wc", "shasum", "find", "diff", "stat", "file ")) or " | grep" in c:
        return "file inspection"
    if c.startswith("colab") or " colab " in c:
        return "colab CLI"
    return "other"

out["tool_kinds"] = {}
for ag in agents:
    cs = [c for c in calls if c["agent"] == ag]
    kinds = Counter()
    for c in cs:
        k = c["kind"] or c["verb"]
        if k.startswith("mcp__colab__colab_"):
            k = "colab:" + k[len("mcp__colab__colab_"):]
        kinds[k] += 1
    out["tool_kinds"][ag] = dict(kinds.most_common(15))
lead_shell = Counter(shell_class(c["cmd"]) for c in calls if c["agent"] == "Lead" and c["verb"] == "run_shell")
out["lead_shell_classes"] = dict(lead_shell)

# 3) 재개된 세션 길이와 턴마다 cache_read 증가
#    세션 id: 재개 attempt 는 runtime.resume 의 session_id; 세션의 첫(콜드) attempt 는 같은 lane 의
#    다음 attempt 가 그 id 로 재개할 때 그 세션의 0번 턴으로 붙인다.
by_lane = defaultdict(list)
for r in att:
    by_lane[r["lane"]].append(r)
sessions = defaultdict(list)
for lane, rs in by_lane.items():
    rs.sort(key=lambda r: r["dispatched_at"] or "")
    for i, r in enumerate(rs):
        sid = r["resume_sid"]
        if sid:
            if not sessions[sid] and i > 0 and not rs[i - 1]["resume_sid"]:
                sessions[sid].append(rs[i - 1])
            elif not sessions[sid] and i > 0 and rs[i - 1]["resume_sid"] != sid:
                sessions[sid].append(rs[i - 1])
            sessions[sid].append(r)
sess_out = []
for sid, rs in sessions.items():
    ag = rs[0]["agent"]
    # 턴 첫 API 호출의 cache_read(= 재개 시점 세션 크기의 하한): 그 attempt 첫 usage 이벤트
    firsts = []
    for r in rs:
        evs = [e for e in usage_ev if e["task"] == r["task"] and e["attempt"] == r["attempt"]]
        firsts.append(evs[0]["cr"] if evs else None)
    sess_out.append({"session": sid, "agent": ag, "turns": len(rs),
                     "turn_cache_read": [r["cache_read"] for r in rs],
                     "turn_cache_write": [r["cache_write"] for r in rs],
                     "turn_first_usage_cache_read": firsts,
                     "turn_calls": [calls_by_task[r["task"]] for r in rs],
                     "turn_usd": [round(r["usd"] or 0, 2) for r in rs],
                     "turn_dispatched": [r["dispatched_at"] for r in rs]})
out["sessions"] = sorted(sess_out, key=lambda s: -s["turns"])

# 4) 턴 중 호출별 cache_read: 이웃한 usage 이벤트 사이의 증가 / 그 사이 호출 수 는 알 수 없어
#    대신 attempt 별 (cache_read 합 / 호출 수) 와, 한 heartbeat 창 안 cache_read 증가량의 분포를 낸다.
per_attempt_cr_per_call = defaultdict(list)
for r in att:
    n = sum(1 for c in calls if c["task"] == r["task"] and c["attempt"] == r["attempt"])
    if n and r["cache_read"]:
        per_attempt_cr_per_call[r["agent"]].append(r["cache_read"] / (n + 1))
out["cache_read_per_call_attempt"] = {ag: dist(v) for ag, v in per_attempt_cr_per_call.items()}

# 5) 깨어나는 간격(같은 lane 의 직전 attempt 끝 → 이 attempt dispatch) 과 cache_write
import datetime as dt
def parse_ts(s):
    return dt.datetime.fromisoformat(s) if s else None
gaps = []
for lane, rs in by_lane.items():
    rs.sort(key=lambda r: r["dispatched_at"] or "")
    for i in range(1, len(rs)):
        a, b = rs[i - 1], rs[i]
        if not a["finished_at"] or not b["dispatched_at"]:
            continue
        g = (parse_ts(b["dispatched_at"]) - parse_ts(a["finished_at"])).total_seconds()
        tot = (b["cache_read"] or 0) + (b["cache_write"] or 0) + (b["inp"] or 0)
        evs = [e for e in usage_ev if e["task"] == b["task"] and e["attempt"] == b["attempt"]]
        first = evs[0] if evs else None
        gaps.append({"agent": b["agent"], "gap_s": g, "resumed": bool(b["resume_sid"]),
                     "cache_write": b["cache_write"], "cache_read": b["cache_read"],
                     "cw_share": (b["cache_write"] or 0) / tot if tot else None,
                     "first_cw": first["cw"] if first else None, "first_cr": first["cr"] if first else None})
out["gaps"] = gaps
res = [g for g in gaps if g["resumed"]]
def grp(cond):
    xs = [g for g in res if cond(g)]
    return {"n": len(xs), "cw_share": dist([g["cw_share"] for g in xs if g["cw_share"] is not None]),
            "first_cw": dist([g["first_cw"] for g in xs if g["first_cw"] is not None]),
            "first_cw_over_first_total": dist([g["first_cw"] / (g["first_cw"] + g["first_cr"]) for g in xs
                                              if g["first_cw"] is not None and (g["first_cw"] + (g["first_cr"] or 0)) > 0])}
out["gap_split_resumed"] = {"le_5min": grp(lambda g: g["gap_s"] <= 300), "gt_5min": grp(lambda g: g["gap_s"] > 300),
                            "gt_1h": grp(lambda g: g["gap_s"] > 3600)}
out["gap_dist"] = {ag: dist([g["gap_s"] for g in gaps if g["agent"] == ag]) for ag in agents}
out["gap_buckets"] = dict(Counter(("≤1m" if g["gap_s"] <= 60 else "1–5m" if g["gap_s"] <= 300 else
                                   "5–60m" if g["gap_s"] <= 3600 else ">1h") for g in gaps))

json.dump(out, sys.stdout, ensure_ascii=False, indent=1, default=str)
