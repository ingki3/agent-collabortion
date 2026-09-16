#!/usr/bin/env python3
"""Assertions for 58_worktree_wiring_smoke.sh — daemon-protocol §6 (v0.7.3).

Four shapes, all read out of the mock server's JSONL:

    row <state> <path> <session> <agent>            the workdir report row
    receipt <state> <path> <session> <agent> <want> the same row's `gc` block
    idrow <state> <path> <id> <session> <agent> [<n>]   K-14 (v0.8.3): the row
        echoes the bundle's `workdir.id` (n-th row for the path, default last)
    idreceipt <state> <path> <id> <want>            the gc receipt by id

They live in a file instead of a heredoc because what they check is the point
of the whole script: a row the server cannot MATCH is skipped without a word,
and that silence is what made T-I4 차단 ② invisible until GC deleted work.
"""
import json
import sys


def all_rows(state, path, need_gc=False):
    out = []
    for line in open(state):
        if not line.strip():
            continue
        rec = json.loads(line)
        if rec["kind"] != "workdirs":
            continue
        for w in rec["body"].get("workdirs", []):
            if w.get("path") == path and (not need_gc or w.get("gc")):
                out.append(w)
    return out


def rows(state, path, need_gc=False):
    found = all_rows(state, path, need_gc)
    return found[-1] if found else None


def main():
    mode, state, path = sys.argv[1:4]
    if mode == "idrow":
        wid, session, agent = sys.argv[4:7]
        found = all_rows(state, path)
        n = int(sys.argv[7]) if len(sys.argv) > 7 else len(found)
        row = found[n - 1] if 0 < n <= len(found) else None
        print("§6 row #%d/%d =" % (n, len(found)), json.dumps(row, ensure_ascii=False))
        if not row:
            return 1
        bad = []
        if row.get("id") != wid:
            bad.append("id (번들 workdir.id 를 그대로 — §6 v0.8.3, got %r)" % row.get("id"))
        if row.get("session_id") != session:
            bad.append("session_id")
        if row.get("agent_id") != agent:
            bad.append("agent_id")
        if row.get("kind") != "worktree" or not row.get("bytes") or not row.get("git"):
            bad.append("kind/bytes/git")
        print("missing/wrong:", bad or "none")
        return 1 if bad else 0
    if mode == "idreceipt":
        wid, want = sys.argv[4:6]
        row = rows(state, path, need_gc=True)
        print("gc receipt =", json.dumps(row, ensure_ascii=False))
        if not row:
            return 1
        gc, bad = row["gc"], []
        if row.get("id") != wid:
            bad.append("id (행 id, got %r)" % row.get("id"))
        if gc.get("id") != wid:
            bad.append("gc.id")
        if gc.get("status") != want:
            bad.append("gc.status (want %s, got %s)" % (want, gc.get("status")))
        print("missing/wrong:", bad or "none")
        return 1 if bad else 0
    session, agent = sys.argv[4:6]
    if mode == "row":
        row = rows(state, path)
        print("§6 row =", json.dumps(row, ensure_ascii=False))
        if not row:
            return 1
        git, bad = row.get("git") or {}, []
        if row.get("session_id") != session:
            bad.append("session_id (세션 uuid 여야 한다)")
        if row.get("agent_id") != agent:
            bad.append("agent_id (worktree 격리는 필수)")
        if row.get("kind") != "worktree":
            bad.append("kind")
        if not row.get("bytes"):
            bad.append("bytes")
        for k in ("branch", "merged", "dirty", "commits_ahead"):
            if k not in git:
                bad.append("git." + k)
        if git.get("commits_ahead") != 1 or git.get("merged") or not git.get("dirty"):
            bad.append("git facts (want 1 ahead · unmerged · dirty)")
        print("missing/wrong:", bad or "none")
        return 1 if bad else 0

    want = sys.argv[6]
    row = rows(state, path, need_gc=True)
    print("gc receipt =", json.dumps(row, ensure_ascii=False))
    if not row:
        return 1
    gc, bad = row["gc"], []
    if gc.get("id") != "wd-58":
        bad.append("gc.id (서버가 물은 행 id 를 되돌려야 한다)")
    if gc.get("status") != want:
        bad.append("gc.status (want %s, got %s)" % (want, gc.get("status")))
    if want == "refused" and not gc.get("reason"):
        bad.append("gc.reason")
    if row.get("session_id") != session:
        bad.append("session_id")
    if row.get("agent_id") != agent:
        bad.append("agent_id")
    print("missing/wrong:", bad or "none")
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
