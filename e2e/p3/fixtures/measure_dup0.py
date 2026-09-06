#!/usr/bin/env python3
"""e2e/p3/fixtures/measure_dup0.py — 중복 0 판정기 (T-I3 (b), E8-04).

`plan/spikes/spike04c/measure.py` 를 **그대로** 옮긴 것이다 — 스파이크 4c 표와 이 실기 1회가 같은 자를
쓰게 하려고 판정 로직을 복사했고, 고친 것은 아래 한 곳뿐이다.

  * `q()` 가 psql 출력에 전역 `.strip()` 을 걸어 **마지막 줄의 빈 후행 칸이 잘렸다**.
    `resumed` 가 NULL 인 attempt 가 마지막 행이면 `r[3]` 에서 IndexError 로 죽는다(G6 1회차 실측).
    행을 기대 칸 수만큼 패딩해서 읽는다. 스파이크 표는 이 경로를 타지 않았으므로 과거 수치는 바뀌지 않는다.

판정 항목(T-D4 (3) 그대로):
  continued      attempt 2 가 남은 작업을 끝냈는가 (두 파일 6항목 + ALL-DONE 게시)
  same_files     attempt 1 이 만든 같은 파일을 이어서 편집했는가 (새로 만들지 않았는가)
  dup_messages   이미 게시한 STAGE 메시지를 다시 게시한 수 (0 이어야 한다)
  dup_edits      파일 안에 같은 항목 번호가 두 번 이상 나온 수 (0 이어야 한다)
  workdir_first  attempt 2 의 첫 tool 이벤트가 읽기/셸/검색인가 ("먼저 확인" 지시 준수)
"""
import argparse, json, re, subprocess, os

ap = argparse.ArgumentParser()
for f in "batch arm kind name session task snap workdir pg tap".split():
    ap.add_argument("--" + f, default="")
a = ap.parse_args()


def q(sql):
    out = subprocess.run(
        ["docker", "exec", "-i", a.pg, "psql", "-U", "colab", "-d", "colab", "-tA", "-F", "\t", "-c", sql],
        capture_output=True, text=True)
    rows = []
    for l in out.stdout.split("\n"):
        if not l.strip():
            continue
        rows.append(l.split("\t"))
    width = max((len(r) for r in rows), default=0)
    return [r + [""] * (width - len(r)) for r in rows]


def one(sql, default=""):
    r = q(sql)
    return r[0][0] if r else default


STAGES = ["STAGE-A1", "STAGE-B1", "STAGE-A2", "STAGE-B2", "ALL-DONE"]
ITEM = re.compile(r"^\s*[-*]?\s*\[(\d)\]", re.M)


def stage_of(text):
    t = text.upper().replace("-", "").replace(" ", "")
    for s in STAGES:
        if s.replace("-", "") in t:
            return s
    return None


def items(path):
    """파일 안의 항목 번호 목록 (중복 판정용). 파일이 없으면 None."""
    if not path or not os.path.exists(path):
        return None
    txt = open(path, encoding="utf-8", errors="replace").read()
    return [m.group(1) for m in ITEM.finditer(txt)]


# ── 메시지: kill 직후 스냅샷 ↔ 최종 ─────────────────────────────────────────
snap_posted = []
p = os.path.join(a.snap, "posted.tsv")
if os.path.exists(p):
    snap_posted = [l.split("\t", 1) for l in open(p, encoding="utf-8", errors="replace").read().strip().split("\n") if l.strip()]
pre_ids = {r[0] for r in snap_posted}
pre_stages = [s for s in (stage_of(r[1]) for r in snap_posted if len(r) > 1) if s]

allm = q("select id, replace(content,E'\\n',' ') from message where source_task_id='%s' order by created_at" % a.task)
post_rows = [r for r in allm if r[0] not in pre_ids and len(r) > 1]
post_stages = [s for s in (stage_of(r[1]) for r in post_rows) if s]
dup_messages = sum(1 for s in post_stages if s in pre_stages)

# ── 파일 상태 ──────────────────────────────────────────────────────────────
files = {}
for f in ("part-one.md", "part-two.md"):
    files[f] = {"before": items(os.path.join(a.snap, f)),
                "after": items(os.path.join(a.workdir, f)) if a.workdir else None}
dup_edits = 0
complete = True
for f, v in files.items():
    af = v["after"]
    if af is None:
        complete = False
        continue
    dup_edits += len(af) - len(set(af))
    if sorted(set(af)) != list("123456"):
        complete = False
same_files = all(v["before"] and v["after"] for v in files.values())
extra = []
if a.workdir and os.path.isdir(a.workdir):
    extra = sorted(x for x in os.listdir(a.workdir)
                   if x not in ("part-one.md", "part-two.md", "AGENTS.md", "CLAUDE.md") and not x.startswith("."))

# ── 재개 판정 이벤트 ───────────────────────────────────────────────────────
ev = q("select attempt, outcome, coalesce(payload->>'resume_reason',''), coalesce(payload->>'runtime_kind','') "
       "from task_event where task_id='%s' and class='runtime' and verb='resume' order by attempt, seq" % a.task)
resume_events = [{"attempt": int(r[0]), "outcome": r[1], "reason": r[2], "kind": r[3]} for r in ev]
a2 = [e for e in resume_events if e["attempt"] == 2]

# 첫 "확인/편집" 툴이 무엇인가. permission·use_tool(콜랩 MCP)은 확인도 편집도 아니므로 건너뛴다.
first_tools = q("select verb, coalesce(object_ref::text,'') from task_event where task_id='%s' and attempt=2 "
                "and class='tool' order by seq limit 12" % a.task)
decisive = [r for r in first_tools if r[0] in ("read", "run_shell", "search", "edit_file")]
workdir_first = bool(decisive) and decisive[0][0] in ("read", "run_shell", "search")

attempts = q("select attempt, coalesce(outcome,''), coalesce(failure_kind::text,''), coalesce(resumed::text,'') "
             "from task_attempt where task_id='%s' order by attempt" % a.task)
status = one("select status::text from task where id='%s'" % a.task)
final_attempt = one("select attempt::text from task where id='%s'" % a.task, "0")
lane_ref = one("select coalesce(runtime_session_ref::text,'null') from lane where session_id='%s' limit 1" % a.session)

# ── attempt 2 턴 프롬프트 (claim 탭) ───────────────────────────────────────
prompt2 = brief2 = resume_field = ""
posted_ids = []
if a.tap and os.path.exists(a.tap):
    for line in open(a.tap, encoding="utf-8", errors="replace"):
        try:
            b = json.loads(line)["body"]
        except Exception:
            continue
        for t in (b.get("tasks") or []):
            tk = t.get("task") or {}
            if tk.get("id") == a.task and tk.get("attempt") == 2:
                prompt2 = t.get("prompt", "")
                brief2 = (t.get("brief") or {}).get("text", "")
                resume_field = json.dumps(t.get("resume"), ensure_ascii=False)
                posted_ids = t.get("posted_message_ids") or []

print(json.dumps({
    "batch": a.batch, "arm": a.arm, "kind": a.kind, "name": a.name,
    "session": a.session, "task": a.task, "status": status, "attempt": int(final_attempt or 0),
    "attempts": [{"n": int(r[0]), "outcome": r[1], "failure": r[2], "resumed": r[3]} for r in attempts],
    "resume_events": resume_events, "attempt2_resume": (a2[0] if a2 else None),
    "pre_kill": {"stages": pre_stages, "files": {f: v["before"] for f, v in files.items()}},
    "post": {"stages": post_stages, "files": {f: v["after"] for f, v in files.items()}, "extra_files": extra},
    "continued": complete, "same_files": same_files,
    "dup_messages": dup_messages, "dup_edits": dup_edits,
    "workdir_first": workdir_first, "first_tools": [r[0] + ":" + r[1][:40] for r in first_tools],
    "lane_ref": lane_ref[:200],
    "prompt2": prompt2, "brief2_len": len(brief2), "resume_field": resume_field,
    "posted_message_ids": posted_ids,
}, ensure_ascii=False))
