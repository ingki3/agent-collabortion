#!/usr/bin/env python3
"""plan/spikes/spike05/spike05.py — 스파이크 5 한 케이스 실행기.

추적 중인 지시 파일(CLAUDE.md / AGENTS.md)에 브리프 마커 구간을 append 하고
`git update-index --skip-worktree` 로 숨긴 뒤, **실제 런타임**으로 4질문을 잰다.

  (1) 런타임이 마커 구간을 읽는가            → turn 1(도구 없이 확인 코드 응답)
  (2) 에이전트가 그 파일을 정당하게 편집·커밋 → turn 2(줄 추가 + git add/commit)
  (3) 마커 구간만 복원 후 git status·원본·에이전트 편집
  (4) worktree 격리에서도 같은가 (index 는 worktree 마다 별개인가)

실험 대상 저장소는 **이 저장소가 아니라** $SPIKE05_WORK 아래 새로 만드는 임시 저장소다.
"""
import argparse, json, os, re, shutil, subprocess, sys, time, uuid

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import acp  # noqa: E402

WORK_ROOT = os.environ.get("SPIKE05_WORK", "/private/tmp/colab-spike05")
MODEL = os.environ.get("SPIKE05_MODEL", "claude-haiku-4-5-20251001")

MARK_START = "<!-- colab:brief:start -->"
MARK_END = "<!-- colab:brief:end -->"
INSTR_FILE = {"claude_code": "CLAUDE.md", "hermes": "AGENTS.md"}
ORIG_CODE = "ORIG-RULE-4471"


# ---------------------------------------------------------------- git helpers
def git(repo, *args, check=True):
    r = subprocess.run(["git", "-C", repo, *args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"git {' '.join(args)} -> {r.returncode}\n{r.stdout}{r.stderr}")
    return r


def gitout(repo, *args):
    r = git(repo, *args, check=False)
    return (r.stdout + r.stderr).strip()


# ------------------------------------------------- 마커 블록 (daemon/internal/brief 와 같은 규칙)
def block(text):
    return MARK_START + "\n" + text.rstrip("\n") + "\n" + MARK_END + "\n"


def strip_block(b: str) -> str:
    s = b.find(MARK_START)
    if s < 0:
        return b
    e = b.find(MARK_END, s)
    if e < 0:
        return b
    end = e + len(MARK_END)
    if end < len(b) and b[end] == "\n":
        end += 1
    if s >= 2 and b[s - 1] == "\n" and b[s - 2] == "\n":
        s -= 1
    return b[:s] + b[end:]


def append_block(path, text):
    orig = open(path, encoding="utf-8").read() if os.path.exists(path) else ""
    stripped = strip_block(orig)
    out = stripped
    if stripped and not stripped.endswith("\n"):
        out += "\n"
    if stripped:
        out += "\n"
    out += block(text)
    open(path, "w", encoding="utf-8").write(out)


# ---------------------------------------------------------------- 임시 저장소
ORIG_CLAUDE = f"""# Widget Catalog - project rules

Owned by the catalog team. Keep entries alphabetical.

- PROJECT_RULE_CODE = {ORIG_CODE}
- Every catalog entry needs a `price` field.
- Do not touch `legacy/` without a ticket.
"""

ORIG_AGENTS = ORIG_CLAUDE.replace("project rules", "agent rules")


def make_repo(root):
    """CLAUDE.md·AGENTS.md 를 추적하는 커밋 3개짜리 임시 저장소."""
    os.makedirs(root, exist_ok=True)
    git(root, "init", "-q", "-b", "main")
    git(root, "config", "user.email", "spike05@example.invalid")
    git(root, "config", "user.name", "Spike Five")
    git(root, "config", "commit.gpgsign", "false")
    open(f"{root}/README.md", "w").write("# Widget Catalog\n\nA tiny fictional catalog.\n")
    git(root, "add", "-A"); git(root, "commit", "-qm", "chore: init")
    open(f"{root}/CLAUDE.md", "w").write(ORIG_CLAUDE)
    open(f"{root}/AGENTS.md", "w").write(ORIG_AGENTS)
    git(root, "add", "-A"); git(root, "commit", "-qm", "docs: project rules")
    os.makedirs(f"{root}/catalog", exist_ok=True)
    open(f"{root}/catalog/widgets.md", "w").write("# Widgets\n\n- alpha - price 3\n- beta - price 5\n")
    git(root, "add", "-A"); git(root, "commit", "-qm", "feat: first widgets")


# ---------------------------------------------------------------- 브리프 본문
def brief_text(code):
    return f"""## [1] Agent Identity

You are `cataloger`, a catalog maintainer on this project.

## [2] Workspace Rules

- SESSION_CONFIRM_CODE = {code}
- Always reply in English.

## [4] Session

Goal: keep the widget catalog tidy.
"""


READ_PROMPT = """Answer from your instructions only. Do not use any tools. Do not search this
directory or any other directory. If a tool fails, do not retry and do not look for another way.

Reply with exactly two lines and nothing else:
CODE=<the value of SESSION_CONFIRM_CODE in your instructions, or NONE>
RULE=<the value of PROJECT_RULE_CODE in your instructions, or NONE>"""


READ_PROMPT_FILE = """Read the file `<BRIEF>` in the current directory - that file holds your
instructions. Do not read or search anything else, here or in any other directory. If a
tool fails, do not retry and do not look for another way.

Reply with exactly two lines and nothing else:
CODE=<the value of SESSION_CONFIRM_CODE in your instructions, or NONE>
RULE=<the value of PROJECT_RULE_CODE in your instructions, or NONE>"""


def edit_prompt(fname, note):
    return f"""Do exactly these three steps in this order, in the current directory only. Do not
search this directory or any other directory beyond what is written here. If a tool
fails, do not retry and do not look for another way - just report what happened.

1. Append this single line to the end of the file `{fname}`, keeping every other line
   of that file unchanged:
   <!-- agent-note: {note} -->
2. Run: git add -A && git commit -m "docs: agent note"
3. Run: git status --short

Then reply with exactly three lines and nothing else:
STEP1=<ok|failed>
STEP2=<the last line of the commit command output, or the error>
STEP3=<the full output of git status --short, or EMPTY>"""


# ---------------------------------------------------------------- 한 케이스
def run_case(runtime, layout, setting_sources, mode, rep, out_dir):
    run_id = f"{runtime}-{layout}-{mode}-{rep}-{uuid.uuid4().hex[:6]}"
    base = os.path.join(WORK_ROOT, run_id)
    shutil.rmtree(base, ignore_errors=True)
    repo = os.path.join(base, "repo")
    make_repo(repo)

    session, agent = uuid.uuid4().hex[:8], "cataloger"
    branch = f"colab/{session}/{agent}"
    if layout == "worktree":
        wt = os.path.join(base, "wt")
        git(repo, "worktree", "add", "-q", "-b", branch, wt)
        workdir = wt
    else:
        workdir = repo

    fname = INSTR_FILE[runtime]
    path = os.path.join(workdir, fname)
    code = "SIERRA-" + uuid.uuid4().hex[:4].upper()
    note = "NOTE-" + uuid.uuid4().hex[:4].upper()
    r = {"run_id": run_id, "runtime": runtime, "layout": layout, "mode": mode, "rep": rep,
         "setting_sources": setting_sources, "instruction_file": fname, "workdir": workdir,
         "branch": branch if layout == "worktree" else "main",
         "confirm_code": code, "agent_note": note, "model": MODEL, "ts": time.strftime("%FT%T%z")}

    r["status_before"] = gitout(workdir, "status", "--porcelain")

    # --- 데몬이 하는 일: 마커 append + 숨기기 -------------------------------
    brief = brief_text(code)
    fallback_path = None
    if mode == "marker":                       # PLAN 기본안
        append_block(path, brief)
        git(workdir, "update-index", "--skip-worktree", fname)
    elif mode == "import":                     # 우회 A: 별도 파일 + import 지시(마커는 1줄)
        side = "CLAUDE.local.md" if runtime == "claude_code" else "AGENTS.local.md"
        fallback_path = os.path.join(workdir, side)
        open(fallback_path, "w", encoding="utf-8").write(brief)
        gitdir = gitout(workdir, "rev-parse", "--git-common-dir")
        gitdir = gitdir if os.path.isabs(gitdir) else os.path.join(workdir, gitdir)
        with open(os.path.join(gitdir, "info", "exclude"), "a") as f:
            f.write(side + "\n")
        append_block(path, f"@{side}")
        git(workdir, "update-index", "--skip-worktree", fname)
    elif mode == "nohide":                     # 대조: 마커만 append 하고 숨기지 않는다
        append_block(path, brief)
    elif mode == "meta":                       # 대조군: v1 계약 경로(_meta.systemPrompt.append)
        pass                                   # 디스크에 아무것도 쓰지 않는다
    elif mode == "prompt_file":                # 우회 B: 추적 파일을 아예 건드리지 않는다
        side = "COLAB_BRIEF.md"
        fallback_path = os.path.join(workdir, side)
        open(fallback_path, "w", encoding="utf-8").write(brief)
        gitdir = gitout(workdir, "rev-parse", "--git-common-dir")
        gitdir = gitdir if os.path.isabs(gitdir) else os.path.join(workdir, gitdir)
        with open(os.path.join(gitdir, "info", "exclude"), "a") as f:
            f.write(side + "\n")
    else:
        raise SystemExit("unknown mode " + mode)

    r["marker_in_file"] = MARK_START in open(path, encoding="utf-8").read()
    r["ls_files_v"] = gitout(workdir, "ls-files", "-v", fname)
    r["skip_worktree_bit"] = r["ls_files_v"][:1]
    r["status_after_append"] = gitout(workdir, "status", "--porcelain")
    if layout == "worktree":
        r["main_ls_files_v"] = gitout(repo, "ls-files", "-v", fname)
        r["main_status"] = gitout(repo, "status", "--porcelain")

    # --- 런타임 ------------------------------------------------------------
    log = os.path.join(out_dir, run_id + ".stderr.log")
    meta = (acp.claude_meta(brief=(brief if mode == "meta" else None), setting_sources=setting_sources)
            if runtime == "claude_code" else None)
    rt = acp.Runtime(runtime, workdir, log, MODEL, meta=meta)
    try:
        r["start"] = {k: ("ok" if "result" in v else v) for k, v in rt.start().items()}
        if not getattr(rt, "session_id", None):
            r["error"] = "session/new failed"
            return r
        # 우회 B 는 브리프가 파일에 있으므로 "도구를 쓰지 마라" 를 뺀다(모순 방지, §0.4).
        p1 = (READ_PROMPT_FILE.replace("<BRIEF>", os.path.basename(fallback_path))
              if mode == "prompt_file" else READ_PROMPT)
        t1 = rt.prompt(p1)
        r["turn1_text"] = t1["text"].strip()
        r["turn1_tools"] = [t.get("title") for t in t1["tools"]]
        m = re.search(r"CODE=([^\s]+)", r["turn1_text"])
        r["read_code"] = bool(m and code in m.group(1))
        m2 = re.search(r"RULE=([^\s]+)", r["turn1_text"])
        r["read_orig_rule"] = bool(m2 and ORIG_CODE in m2.group(1))
        r["turn1_toolfree"] = len(t1["tools"]) == 0

        t2 = rt.prompt(edit_prompt(fname, note))
        r["turn2_text"] = t2["text"].strip()
        r["turn2_tools"] = [t.get("title") for t in t2["tools"]]
        r["permissions"] = rt.permissions
    finally:
        rt.close()

    # --- 편집 직후 관측 ------------------------------------------------------
    cur = open(path, encoding="utf-8").read() if os.path.exists(path) else ""
    r["file_has_note_after_edit"] = note in cur
    r["file_has_marker_after_edit"] = MARK_START in cur
    r["status_after_edit"] = gitout(workdir, "status", "--porcelain")
    r["log_after_edit"] = gitout(workdir, "log", "--oneline", "-3")
    r["committed_new"] = "docs: agent note" in r["log_after_edit"]
    head_file = gitout(workdir, "show", f"HEAD:{fname}")
    r["head_has_marker"] = MARK_START in head_file
    r["head_has_note"] = note in head_file
    r["committed_files"] = gitout(workdir, "show", "--stat", "--oneline", "HEAD")

    # --- 데몬 복원: --no-skip-worktree → 마커 구간만 제거 ---------------------
    if mode in ("marker", "import"):
        git(workdir, "update-index", "--no-skip-worktree", fname)
    if mode in ("marker", "import", "nohide"):
        cur2 = open(path, encoding="utf-8").read()
        open(path, "w", encoding="utf-8").write(strip_block(cur2))
    if fallback_path and os.path.exists(fallback_path):
        os.remove(fallback_path)
    final = open(path, encoding="utf-8").read()
    r["restore_status"] = gitout(workdir, "status", "--porcelain")
    r["restore_clean"] = r["restore_status"] == ""
    r["restore_no_marker"] = MARK_START not in final and MARK_END not in final
    orig_committed = ORIG_CLAUDE if fname == "CLAUDE.md" else ORIG_AGENTS
    r["restore_original_intact"] = all(l in final for l in orig_committed.strip().splitlines())
    r["restore_note_kept"] = note in final
    r["restore_diff"] = gitout(workdir, "diff", "--", fname)
    r["final_file"] = final
    return r


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--runtime", required=True, choices=["claude_code", "hermes"])
    ap.add_argument("--layout", default="plain", choices=["plain", "worktree"])
    ap.add_argument("--setting-sources", default="empty", choices=["empty", "project"])
    ap.add_argument("--mode", default="marker", choices=["marker", "import", "prompt_file", "meta", "nohide"])
    ap.add_argument("--rep", type=int, default=1)
    ap.add_argument("--out", default=os.path.join(HERE, "out"))
    a = ap.parse_args()
    os.makedirs(a.out, exist_ok=True)
    ss = [] if a.setting_sources == "empty" else ["project"]
    row = run_case(a.runtime, a.layout, ss, a.mode, a.rep, a.out)
    row["setting_sources_label"] = a.setting_sources
    with open(os.path.join(a.out, "runs.jsonl"), "a", encoding="utf-8") as f:
        f.write(json.dumps(row, ensure_ascii=False) + "\n")
    print(json.dumps({k: row[k] for k in row if k not in ("final_file", "turn1_text", "turn2_text")},
                     ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
