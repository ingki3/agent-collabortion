#!/usr/bin/env python3
"""T-CTX0 기준선 2부: 재개 세션의 크기 추이와 턴 프롬프트 재생(bundle.go) 결합.

입력: baseline.py 출력(/tmp/ctx0-baseline.json 기본)과 TestReplayContextMetrics 출력(/tmp/ctx0-replay.json).
출력: 표준출력 JSON — 토큰 보정계수, 세션별 턴 표, 가설 판정용 합계.
"""
import datetime as dt
import json, os, statistics, subprocess, sys
from collections import defaultdict

ROOM = os.environ.get("ROOM", "02aa7e66-b326-4def-830d-ee13f2248770")
PSQL = os.environ.get("PSQL", "docker exec -i colab-pg-ctx0 psql -U colab -d snap -X -q -A -t")
REPLAY = os.environ.get("REPLAY", "/tmp/ctx0-replay.json")


def q(sql):
    out = subprocess.run(PSQL.split() + ["-c", f"SELECT row_to_json(x) FROM ({sql}) x"],
                         capture_output=True, text=True, check=True).stdout
    return [json.loads(l) for l in out.splitlines() if l.strip()]


replay = {(r["task"], r["attempt"]): r for r in json.load(open(REPLAY))}

# 첫 usage 이벤트 = 그 턴의 첫 API 호출(또는 처음 몇 호출)의 누적. tool 이벤트가 그 앞에 몇 개인지로
# 「첫 호출 하나만」인지 가린다: 도구 호출 시작 전이면 API 호출은 하나다.
first = q(f"""
SELECT DISTINCT ON (e.task_id, e.attempt) e.task_id task, e.attempt, a.name agent, ta.resumed,
       ta.dispatched_at, ta.finished_at, t.lane_id lane,
       (e.usage->>'cache_read_tokens')::bigint cr, (e.usage->>'cache_write_tokens')::bigint cw,
       (e.usage->>'input_tokens')::bigint inp,
       (SELECT count(DISTINCT x.payload->>'tool_call_id') FROM task_event x WHERE x.task_id=e.task_id AND x.attempt=e.attempt
          AND x.class='tool' AND x.seq < e.seq) calls_before,
       (SELECT x.payload->>'session_id' FROM task_event x WHERE x.task_id=e.task_id AND x.attempt=e.attempt
          AND x.class='runtime' AND x.verb='resume' LIMIT 1) sid,
       (SELECT x.payload->>'runtime_kind' FROM task_event x WHERE x.task_id=e.task_id AND x.attempt=e.attempt
          AND x.class='runtime' AND x.verb IN ('start','resume') LIMIT 1) rk
FROM task_event e JOIN task t ON t.id=e.task_id JOIN agent a ON a.id=t.agent_id
JOIN task_attempt ta ON ta.task_id=e.task_id AND ta.attempt=e.attempt
WHERE t.session_id='{ROOM}' AND e.class='usage'
ORDER BY e.task_id, e.attempt, e.seq""")
last = {(r["task"], r["attempt"]): r for r in q(f"""
SELECT DISTINCT ON (e.task_id, e.attempt) e.task_id task, e.attempt,
       (e.usage->>'cache_read_tokens')::bigint cr, (e.usage->>'cache_write_tokens')::bigint cw,
       (e.usage->>'input_tokens')::bigint inp, (e.usage->>'output_tokens')::bigint outp
FROM task_event e JOIN task t ON t.id=e.task_id
WHERE t.session_id='{ROOM}' AND e.class='usage' ORDER BY e.task_id, e.attempt, e.seq DESC""")}

out = {}

# 1) 토큰 보정: 재개 턴인데 브리프가 직전 턴과 같고 첫 호출이 세션 접두를 적중한 경우, 그 첫 호출의
#    cache_write = 새 턴 프롬프트 + 직전 턴 마지막 답(캐시되지 않은 꼬리). 그래서 cw / (rune/3 추정) 은
#    「실제 토큰 / rune/3 추정」의 상한이다. 아래 2) 를 한 번 돈 뒤 채운다.
SYSTEM = 10130
K = None

# 2) 재개 세션: 턴 k 의 시작 크기 S_k = 첫 호출(cr+cw+inp). S_{k+1} - S_k = 턴 k 의 일(도구 결과·답) + 프롬프트 k+1.
by_sid = defaultdict(list)
by_lane = defaultdict(list)
for f in first:
    by_lane[f["lane"]].append(f)
for lane, fs in by_lane.items():
    fs.sort(key=lambda f: f["dispatched_at"])
    for i, f in enumerate(fs):
        if f["sid"]:
            if not by_sid[f["sid"]] and i > 0:
                by_sid[f["sid"]].append(fs[i - 1])
            by_sid[f["sid"]].append(f)
sessions = []
agg = defaultdict(lambda: {"delta": 0, "prompt": 0, "n": 0, "shares": []})
for sid, fs in by_sid.items():
    turns = []
    for f in fs:
        r = replay.get((f["task"], f["attempt"]))
        l = last.get((f["task"], f["attempt"])) or {}
        start = (f["cr"] or 0) + (f["cw"] or 0) + (f["inp"] or 0)
        turns.append({"task": f["task"], "resumed": bool(f["sid"]), "start_tokens": start,
                      "single_call": f["cr"] == SYSTEM,
                      "brief_sha": r and r.get("brief_sha"), "prefix_sha": r and r.get("prefix_sha"),
                      "first_call_cache_read": f["cr"], "first_call_cache_write": f["cw"],
                      "calls_before_first_usage": f["calls_before"],
                      "turn_cache_read": l.get("cr"), "turn_cache_write": l.get("cw"),
                      "prompt_est": r["prompt"]["tokens_est"] if r else None,
                      "prompt_bytes": r["prompt"]["bytes"] if r else None,
                      "brief6": r["sections"].get("brief.6", {}).get("bytes", 0) if r else None,
                      "brief7": r["sections"].get("brief.7", {}).get("bytes", 0) if r else None,
                      "dispatched_at": f["dispatched_at"], "finished_at": f.get("finished_at"),
                      "rk": f["rk"]})
    for a, b in zip(turns, turns[1:]):
        b["brief_changed"] = a["brief_sha"] != b["brief_sha"]
        b["prefix_changed"] = a["prefix_sha"] != b["prefix_sha"]
        b["brief6_changed"] = a["brief6"] != b["brief6"]
        b["brief7_changed"] = a["brief7"] != b["brief7"]
        if a.get("finished_at") and b.get("dispatched_at"):
            b["gap_s"] = (dt.datetime.fromisoformat(b["dispatched_at"]) - dt.datetime.fromisoformat(a["finished_at"])).total_seconds()
        b["prev_start"] = a["start_tokens"]
        b["prev_single"] = a["single_call"]
    sessions.append({"session": sid, "agent": fs[0]["agent"], "turns": turns})
cal = []
for s_ in sessions:
    for t in s_["turns"][1:]:
        if t["resumed"] and t["rk"] == "claude_code" and not t.get("brief_changed") and (t["first_call_cache_read"] or 0) > SYSTEM \
                and t["calls_before_first_usage"] <= 1 and t["prompt_est"]:
            cal.append({"task": t["task"], "cw": t["first_call_cache_write"], "prompt_est": t["prompt_est"],
                        "prompt_bytes": t["prompt_bytes"], "k": t["first_call_cache_write"] / t["prompt_est"],
                        "bytes_per_token": t["prompt_bytes"] / t["first_call_cache_write"]})
K = statistics.median(c["k"] for c in cal) if cal else 1.0
out["calibration"] = {"n": len(cal), "system_prefix_tokens": SYSTEM, "tokens_per_rune3_upper": K,
                      "k_range": [min(c["k"] for c in cal), max(c["k"] for c in cal)] if cal else None,
                      "bytes_per_token_lower": statistics.median(c["bytes_per_token"] for c in cal) if cal else None,
                      "samples": cal}

agg = defaultdict(lambda: {"delta": 0, "prompt": 0, "n": 0, "shares": []})
for s_ in sessions:
    for t in s_["turns"][1:]:
        if not t["resumed"] or t["prompt_est"] is None or not (t["prev_single"] and t["single_call"]):
            continue
        d = t["start_tokens"] - t["prev_start"]
        t["growth_from_prev_start"] = d
        if d <= 0:
            t["compacted"] = True
            continue
        p = t["prompt_est"] * K
        ag = agg[s_["agent"]]
        ag["delta"] += d
        ag["prompt"] += p
        ag["n"] += 1
        ag["shares"].append(min(p / d, 1.0))
out["sessions"] = sorted(sessions, key=lambda s: -len(s["turns"]))
out["growth_vs_prompt"] = {ag: {"pairs": v["n"], "growth_sum": v["delta"], "prompt_sum_cal": round(v["prompt"]),
                                "prompt_share_of_growth": v["prompt"] / v["delta"] if v["delta"] else None,
                                "median_share": statistics.median(v["shares"]) if v["shares"] else None}
                           for ag, v in agg.items()}
cc = [t for s_ in sessions for t in s_["turns"][1:] if t["resumed"] and t["rk"] == "claude_code"]
def cls(t):
    hit = (t["first_call_cache_read"] or 0) > SYSTEM
    return ("brief_changed" if t.get("brief_changed") else "brief_same") + ("/hit" if hit else "/miss")
from collections import Counter
out["claude_resumed_cache"] = {
    "n": len(cc), "by_class": dict(Counter(cls(t) for t in cc)),
    "miss_by_gap": dict(Counter(("≤5m" if (t.get("gap_s") or 0) <= 300 else "5–60m" if (t.get("gap_s") or 0) <= 3600 else ">1h") + "/" + cls(t) for t in cc)),
    "changed_section": dict(Counter(("6" if t.get("brief6_changed") else "") + ("7" if t.get("brief7_changed") else "") or "other" for t in cc if t.get("brief_changed"))),
    # 적중했다면 cache_read(×0.5$/M)였을 몫이 cache_write(×10$/M)로 청구된 양: 첫 호출 cw 에서 새 프롬프트 몫을 뺀 것
    "rewrite_tokens": sum(max(0, (t["first_call_cache_write"] or 0) - round((t["prompt_est"] or 0) * K)) for t in cc
                          if (t["first_call_cache_read"] or 0) <= SYSTEM),
}
out["claude_resumed_cache"]["rewrite_extra_usd_opus5"] = out["claude_resumed_cache"]["rewrite_tokens"] * (10 - 0.5) / 1e6

# 3) 재개 첫 호출: 세션 접두 적중이 시스템 몫(10,130)뿐인가
res_first = [f for f in first if f["sid"] and f["rk"] == "claude_code"]
out["resume_first_call"] = {
    "n": len(res_first),
    "cache_read_eq_system": sum(1 for f in res_first if f["cr"] == SYSTEM),
    "cache_read_values_other": sorted({f["cr"] for f in res_first if f["cr"] != SYSTEM})[:20],
    "first_write_share_median": statistics.median([(f["cw"] or 0) / ((f["cw"] or 0) + (f["cr"] or 0) + (f["inp"] or 0))
                                                  for f in res_first if (f["cw"] or 0) + (f["cr"] or 0) > 0]),
}
# 4) 캐시 TTL 판정: claude_code attempt 의 실측 비용을 opus-5 단가(입력 5 · 출력 25 · 읽기 0.5 $/M)와
#    쓰기 단가 후보(5분 TTL = 입력×1.25, 1시간 TTL = 입력×2)로 재구성해 비율을 본다.
fit_rows = q(f"""
SELECT u.cost_usd::float8 c, u.cache_read cr, u.output_tokens o, u.input_tokens i,
       (SELECT (e.usage->>'cache_write_tokens')::bigint FROM task_event e WHERE e.task_id=u.task_id AND e.attempt=u.attempt
          AND e.class='usage' ORDER BY e.seq DESC LIMIT 1) cw
FROM task_usage u JOIN task t ON t.id=u.task_id
JOIN task_attempt ta ON ta.task_id=u.task_id AND ta.attempt=u.attempt
WHERE t.session_id='{ROOM}' AND u.estimated = false AND u.model LIKE '%opus-5%'""")
fit = {}
for name, w in (("ttl_5m_x1.25", 6.25), ("ttl_1h_x2", 10.0)):
    rs = sorted((r["cr"] * 0.5 + r["o"] * 25 + r["i"] * 5 + r["cw"] * w) / 1e6 / r["c"]
                for r in fit_rows if r["cw"] is not None and r["c"])
    fit[name] = {"n": len(rs), "pred_over_actual_median": statistics.median(rs) if rs else None,
                 "p10": rs[len(rs) // 10] if rs else None}
out["cache_ttl_cost_fit"] = fit

json.dump(out, sys.stdout, ensure_ascii=False, indent=1, default=str)
