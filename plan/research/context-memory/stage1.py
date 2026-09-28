#!/usr/bin/env python3
"""T-CTX1 맥락 1단계 성과 측정 — 04-baseline 과 같은 사본·같은 방에서 새 규칙을 재생한 결과를 모은다.

입력
  BASE      baseline_sessions.py 출력(옛 규칙 재생 + 실측 usage)            기본 /tmp/ctx1-baseline-sessions.json
  OLD       TestReplayContextMetrics 출력(옛 bundle.go, 전 에이전트)          기본 /tmp/ctx1-old-replay.json
  NEW_DIR   TestReplayContextStage1 출력 디렉터리 접두(에이전트별 -<Agent>, Lead 는 접두 그대로)
                                                                             기본 /tmp/ctx1-replay
  RECALL    recall-set-v1.json
출력: 표준출력 JSON.

모델(세션 크기 — 04-baseline §2 의 측정에서 출발한다; 새 규칙의 세션은 아직 없다)
  * 턴 k 의 시작 크기 S_k = 첫 API 호출의 cache_read + cache_write + input (실측, 단일 호출 턴만).
  * 옛 규칙에서 재개 턴 k+1 의 증가 S_{k+1} - S_k = W_k(턴 k 의 일: 도구 결과·답) + P_old_{k+1}(새 턴 프롬프트 한 벌).
    그래서 W_k = (S_{k+1} - S_k) - K·P_old_{k+1}. 잴 수 없는 턴(첫 usage 가 여러 호출·압축 꺾임·마지막 턴)은 Lead 중앙값.
  * 새 규칙: 재개 턴의 시작 = S'_k + W_k + K·P_delta_{k+1}; 콜드 턴의 시작 = SYSTEM + K·(브리프 + P_cold).
    재개 여부 = 옛 실행에서 실제로 재개였고(ref·런타임이 이어졌고) AND 직전 턴 시작 S'_k ≤ 300,000(§6 상한).
  * 턴의 호출당 맥락 ≈ 시작 + W/2(턴 안에서 일이 고르게 쌓인다고 본다). cache_read 는 호출 수 × 호출당 맥락이므로
    호출 수가 같다고 두면 턴 cache_read 비 = 호출당 맥락 비.
  * 비용(opus-5, 04-baseline §3 적합): 입력 $5 · 읽기 $0.5 · 쓰기 $10(1시간 TTL) /M.
    - 옛 규칙 재개 턴 중 브리프가 바뀐 턴은 첫 호출이 세션 전체를 쓰기로 냈다(실측 first_call_cache_write).
      새 규칙은 브리프가 고정이라 재개 턴 첫 호출 쓰기 = K·P_delta + 직전 턴 꼬리(옛 적중 턴 6개의 「쓰기 - 프롬프트」 중앙).
    - 계획적 콜드 턴의 첫 호출 쓰기 = K·(브리프 + P_cold).
    - 첫 호출 뒤의 쓰기(턴 안의 도구 결과)는 옛 실측 그대로.
"""
import datetime as dt
import json, os, statistics, sys
from collections import Counter, defaultdict

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from grade_recall import grade, summarize  # noqa: E402

BASE = os.environ.get("BASE", "/tmp/ctx1-baseline-sessions.json")
OLD = os.environ.get("OLD", "/tmp/ctx1-old-replay.json")
NEW_DIR = os.environ.get("NEW_DIR", "/tmp/ctx1-replay")
RECALL = os.environ.get("RECALL", os.path.join(HERE, "recall-set-v1.json"))
CAP = 300_000
SYSTEM = 10130
PRICE = {"in": 5, "read": 0.5, "write": 10}
AGENTS = ["Lead", "Writer", "Researcher", "Designer", "Developer"]


def ts(s):
    """dispatched_at 을 UTC 초 단위 문자열로 — 사본(psql)은 +00:00, Go 재생은 +09:00 으로 쓴다."""
    return dt.datetime.fromisoformat(s).astimezone(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")


def med(xs):
    xs = [x for x in xs if x is not None]
    return statistics.median(xs) if xs else None


def pct(xs, p):
    xs = sorted(x for x in xs if x is not None)
    return xs[min(len(xs) - 1, int(len(xs) * p))] if xs else None


base = json.load(open(BASE))
K = base["calibration"]["tokens_per_rune3_upper"]
old = json.load(open(OLD))
old_by = {(r["task"], ts(r["dispatched_at"])): r for r in old}


def new_dir(agent):
    return NEW_DIR if agent == "Lead" else f"{NEW_DIR}-{agent}"


new = {}
for ag in AGENTS:
    for r in json.load(open(os.path.join(new_dir(ag), "replay.json"))):
        r["agent"] = ag
        new[(r["task"], ts(r["dispatched_at"]))] = r

out = {"calibration_K": K, "cap": CAP}

# ── 1. 턴 프롬프트 크기 (보정 토큰 = 추정 × K) ─────────────────────────────────
size = {}
for ag in AGENTS:
    rs = [r for r in new.values() if r["agent"] == ag]
    olds = [old_by[(r["task"], ts(r["dispatched_at"]))] for r in rs if (r["task"], ts(r["dispatched_at"])) in old_by]
    size[ag] = {
        "attempts": len(rs),
        "old_prompt_median": round(med([o["prompt"]["tokens_est"] * K for o in olds])),
        "old_prompt_max": round(max(o["prompt"]["tokens_est"] * K for o in olds)),
        "old_brief_median": round(med([o["brief"]["tokens_est"] * K for o in olds])),
        "new_brief_median": round(med([r["brief_bytes"] / 3 * 0 + 0 for r in rs]) or 0),
        "new_cold_median": round(med([r["cold_tokens_est"] * K for r in rs])),
        "new_cold_max": round(max(r["cold_tokens_est"] * K for r in rs)),
        "delta_n": sum(1 for r in rs if r["delta_bytes"]),
        "delta_median": round(med([r["delta_tokens_est"] * K for r in rs if r["delta_bytes"]]) or 0),
        "delta_p90": round(pct([r["delta_tokens_est"] * K for r in rs if r["delta_bytes"]], 0.9) or 0),
        "delta_max": round(max([r["delta_tokens_est"] * K for r in rs if r["delta_bytes"]] or [0])),
        "old_brief_bytes_median": med([o["brief"]["bytes"] for o in olds]),
        "new_brief_bytes_median": med([r["brief_bytes"] for r in rs]),
    }
    del size[ag]["new_brief_median"]
out["prompt_size"] = size

# ── 2. 브리프 바이트 동일 비율 ────────────────────────────────────────────────
# (a) 같은 lane 의 연속 attempt 쌍 (b) claude_code 재개 턴 78 (04-baseline §3 과 같은 집합)
pairs = {"old": Counter(), "new": Counter()}
by_lane = defaultdict(list)
for r in new.values():
    by_lane[r["lane"]].append(r)
for lane, rs in by_lane.items():
    rs.sort(key=lambda r: r["dispatched_at"])
    for a, b in zip(rs, rs[1:]):
        oa, ob = old_by.get((a["task"], ts(a["dispatched_at"]))), old_by.get((b["task"], ts(b["dispatched_at"])))
        pairs["new"]["same" if a["brief_sha"] == b["brief_sha"] else "changed"] += 1
        if oa and ob:
            pairs["old"]["same" if oa["brief_sha"] == ob["brief_sha"] else "changed"] += 1
out["brief_identity_lane_pairs"] = {k: dict(v) for k, v in pairs.items()}

cc = []
for s in base["sessions"]:
    turns = s["turns"]
    for a, b in zip(turns, turns[1:]):
        if b["resumed"] and b["rk"] == "claude_code":
            na, nb = new.get((a["task"], ts(a["dispatched_at"]))), new.get((b["task"], ts(b["dispatched_at"])))
            cc.append({"old_same": not b.get("brief_changed"), "new_same": bool(na and nb and na["brief_sha"] == nb["brief_sha"]),
                       "why_new_changed": None if (na and nb and na["brief_sha"] == nb["brief_sha"]) else "mission/roster edit or missing"})
out["brief_identity_claude_resumed"] = {"n": len(cc), "old_same": sum(c["old_same"] for c in cc), "new_same": sum(c["new_same"] for c in cc)}

# ── 3. 재개/콜드 결정 분포와 세션 크기 (Lead, 모델) ──────────────────────────────
def model(agent):
    sessions = [s for s in base["sessions"] if s["agent"] == agent]
    # W_k: 측정 가능한 쌍에서
    ws = []
    for s in sessions:
        for a, b in zip(s["turns"], s["turns"][1:]):
            if b["resumed"] and a["single_call"] and b["single_call"] and b["prompt_est"] and b["start_tokens"] > a["start_tokens"]:
                ws.append(b["start_tokens"] - a["start_tokens"] - K * b["prompt_est"])
    w_med = med([max(w, 0) for w in ws])
    hit_tail = [t["first_call_cache_write"] - K * (t["prompt_est"] or 0) for s in sessions for t in s["turns"]
                if t["resumed"] and not t.get("brief_changed") and (t["first_call_cache_read"] or 0) > SYSTEM]
    tail = max(0, med(hit_tail) or 0)
    rows = []
    for s in sessions:
        prev = None
        for i, t in enumerate(s["turns"]):
            key = (t["task"], ts(t["dispatched_at"]))
            n = new.get(key)
            if not n:
                continue
            w_this = None
            if i + 1 < len(s["turns"]):
                b = s["turns"][i + 1]
                if b["resumed"] and t["single_call"] and b["single_call"] and b["prompt_est"] and b["start_tokens"] > t["start_tokens"]:
                    w_this = max(0, b["start_tokens"] - t["start_tokens"] - K * b["prompt_est"])
            w = w_this if w_this is not None else w_med
            brief_tok = n["brief_bytes"] / 1.95  # 04-baseline §4: 1.95 바이트/토큰
            cold_start = SYSTEM + brief_tok + K * n["cold_tokens_est"]
            old_start = t["start_tokens"] if t["single_call"] else None
            if prev is None or not t["resumed"]:
                decision, start = ("cold_first" if prev is None else "cold_runtime"), cold_start
            elif prev["start"] > CAP:
                decision, start = "cold_cap", cold_start
            else:
                decision = "resume"
                start = prev["start"] + prev["w"] + K * (n["delta_tokens_est"] if n["delta_bytes"] else n["cold_tokens_est"])
            # 옛 규칙 첫 호출 쓰기(실측) vs 새 규칙 첫 호출 쓰기(모델)
            old_fw = t["first_call_cache_write"] or 0
            if decision == "resume":
                new_fw = K * (n["delta_tokens_est"] if n["delta_bytes"] else n["cold_tokens_est"]) + tail
            else:
                new_fw = brief_tok + K * n["cold_tokens_est"]
            rows.append({"task": t["task"], "decision": decision, "old_resumed": t["resumed"], "old_start": old_start,
                         "new_start": round(start), "w": round(w), "old_ctx": (old_start + w / 2) if old_start else None,
                         "new_ctx": start + w / 2, "turn_cache_read": t["turn_cache_read"], "turn_cache_write": t["turn_cache_write"],
                         "old_first_write": old_fw, "new_first_write": round(new_fw),
                         "old_brief_changed": bool(t.get("brief_changed"))})
            prev = {"start": start, "w": w}
    return rows, w_med, tail


mod = {}
for ag in ["Lead", "Writer", "Researcher"]:
    rows, w_med, tail = model(ag)
    dist = Counter(r["decision"] for r in rows)
    starts_new = [r["new_start"] for r in rows]
    starts_old = [r["old_start"] for r in rows if r["old_start"]]
    ratio = [r["new_ctx"] / r["old_ctx"] for r in rows if r["old_ctx"]]
    # 비용: 읽기는 호출당 맥락 비로 비례, 쓰기는 첫 호출만 바꾼다.
    read_old = sum(r["turn_cache_read"] or 0 for r in rows)
    read_new = sum((r["turn_cache_read"] or 0) * (r["new_ctx"] / r["old_ctx"]) for r in rows if r["old_ctx"]) + \
        sum(r["turn_cache_read"] or 0 for r in rows if not r["old_ctx"])
    write_old = sum(r["turn_cache_write"] or 0 for r in rows)
    write_new = sum(max(0, (r["turn_cache_write"] or 0) - r["old_first_write"]) + r["new_first_write"] for r in rows)
    usd = lambda rd, wr: (rd * PRICE["read"] + wr * PRICE["write"]) / 1e6
    mod[ag] = {
        "turns": len(rows), "decisions": dict(dist), "W_median": round(w_med), "resume_tail_median": round(tail),
        "start_old_median": round(med(starts_old)), "start_old_p90": round(pct(starts_old, 0.9)), "start_old_max": max(starts_old),
        "start_new_median": round(med(starts_new)), "start_new_p90": round(pct(starts_new, 0.9)), "start_new_max": max(starts_new),
        "per_call_ctx_ratio_median": round(med(ratio), 3),
        "cache_read_old": read_old, "cache_read_new": round(read_new),
        "cache_write_old": write_old, "cache_write_new": round(write_new),
        "usd_read_write_old": round(usd(read_old, write_old), 1), "usd_read_write_new": round(usd(read_new, write_new), 1),
    }
out["session_model"] = mod

# ── 4. 회상 문항 ──────────────────────────────────────────────────────────────
rec = json.load(open(RECALL))
qs = rec["questions"]
last = rec["baseline_turn"]["task"]
lead = sorted([r for r in new.values() if r["agent"] == "Lead"], key=lambda r: r["dispatched_at"])
last_row = [r for r in lead if r["task"] == last][-1]
rd = lambda r, kind: open(os.path.join(NEW_DIR, f'{r["task"]}-{r["attempt"]}.{kind}.txt')).read()
cold_ctx = rd(last_row, "brief") + rd(last_row, "cold")
out["recall_cold"] = summarize(grade(qs, cold_ctx, use_agent=True))
out["recall_brief_only"] = summarize(grade(qs, rd(last_row, "brief"), use_agent=True))["total"]

# 재개 턴: 세션에 이미 있는 것(그 세션의 콜드 턴 전체 프롬프트 + 이후 델타들) + 이번 델타.
lead_rows, _, _ = model("Lead")
dec = {r["task"]: r["decision"] for r in lead_rows}


def session_ctx(upto):
    """upto 까지 같은 lane 에서 새 규칙이 한 세션에 보낸 텍스트 전부(모델의 재개/콜드 결정대로)."""
    lane = upto["lane"]
    rs = [r for r in lead if r["lane"] == lane and r["dispatched_at"] <= upto["dispatched_at"]]
    parts = []
    for r in rs:
        d = dec.get(r["task"], "cold_first")
        if d != "resume" or not r["delta_bytes"]:
            parts = [rd(r, "brief"), rd(r, "cold")]
        else:
            parts.append(rd(r, "prompt"))
    return "\n".join(parts)


resumed_scores = []
for r in lead:
    if dec.get(r["task"]) != "resume" or not r["delta_bytes"]:
        continue
    s_cold = summarize(grade(qs, rd(r, "brief") + rd(r, "cold"), use_agent=True))["total"]
    s_sess = summarize(grade(qs, session_ctx(r), use_agent=True))["total"]
    s_delta = summarize(grade(qs, rd(r, "brief") + rd(r, "prompt"), use_agent=True))["total"]
    resumed_scores.append({"task": r["task"], "cold_prompt": s_cold, "session_plus_delta": s_sess, "delta_only": s_delta})
out["recall_resumed_turns"] = {
    "n": len(resumed_scores),
    "session_ge_cold": sum(1 for x in resumed_scores if x["session_plus_delta"] >= x["cold_prompt"]),
    "median_cold": med([x["cold_prompt"] for x in resumed_scores]),
    "median_session_plus_delta": med([x["session_plus_delta"] for x in resumed_scores]),
    "median_delta_only": med([x["delta_only"] for x in resumed_scores]),
    "rows": resumed_scores,
}
out["recall_last_turn"] = {"decision": dec.get(last), "session_plus_delta": summarize(grade(qs, session_ctx(last_row), use_agent=True))["total"]}

json.dump(out, sys.stdout, ensure_ascii=False, indent=1, default=str)
