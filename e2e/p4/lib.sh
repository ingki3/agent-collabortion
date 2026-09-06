#!/usr/bin/env bash
# e2e/p4/lib.sh — P4 통합 E2E 공통 헬퍼 (T-I4, G7 판정 자료).
#
# `e2e/p3/lib.sh`(→ p2 → p1) 를 그대로 재사용하고 **포트·컨테이너·workdir 만 분리**한다
# (P3_TASKS §0-13). 같은 머신에 P1(:8080/:5435)·P2(:8090/:5436)·G5(:5437)·스파이크
# 4c(:5441)·G6(:8100/:3020/:5442)·P4a(:5446)·T-S9(:8103/:5448)·T-C6(:8104/:5449)
# 스택이 동시에 떠 있을 수 있다. 덮어쓰려면 미리 export 한다.
export SERVER_URL="${SERVER_URL:-http://localhost:8105}"
export WEB_URL="${WEB_URL:-http://localhost:3018}"
export PG_PORT="${PG_PORT:-5450}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-i4}"
P4_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P4_DIR/out}"
mkdir -p "$E2E_OUT"
source "$P4_DIR/../p3/lib.sh"

# 실험 저장소는 **이 저장소가 아니다**(P4_TASKS §0-18). 워크트리를 다루는 판마다
# 저장소 밖에 임시 git 저장소를 만들고, 끝나면 지운다.
P4_TMP_ROOT="${P4_TMP_ROOT:-/private/tmp/colab-p4-i4}"

# make_repo DIR REMOTE_URL — 실험 저장소 하나. `CLAUDE.md`·`AGENTS.md` 를 **추적 상태로**
# 커밋해 둔다(E13-03~06 이 재는 것이 바로 그 상태다 — 추적 중인 규칙 파일에 브리프를
# 덧쓰면 세션이 끝난 뒤 `git status` 가 더러워진다).
make_repo() {
  local dir="$1" remote="${2:-}"
  rm -rf "$dir"; mkdir -p "$dir"
  git -C "$dir" init -q -b main
  git -C "$dir" config user.email i4@test
  git -C "$dir" config user.name "i4 e2e"
  git -C "$dir" config commit.gpgsign false
  cat > "$dir/CLAUDE.md" <<'MD'
# House rules (tracked)

PROJECT_RULE_CODE is ORIG-RULE-8813. Never change this file.
MD
  cat > "$dir/AGENTS.md" <<'MD'
# House rules (tracked)

PROJECT_RULE_CODE is ORIG-RULE-8813. Never change this file.
MD
  mkdir -p "$dir/src"
  cat > "$dir/src/pump.py" <<'PY'
"""Indoor planter pump controller (toy)."""

def water_seconds(moisture: int) -> int:
    """Return how many seconds to run the pump."""
    return 0
PY
  cat > "$dir/src/ui.py" <<'PY'
"""Indoor planter panel (toy)."""

def render(status: str) -> str:
    return "status: " + status
PY
  printf 'Toy repo for colab e2e. Not the colab repository.\n' > "$dir/README.md"
  git -C "$dir" add -A >/dev/null
  git -C "$dir" commit -qm "seed"
  [ -n "$remote" ] && git -C "$dir" remote add origin "$remote"
  return 0
}

# repo_status_clean DIR → yes|<git status --porcelain 첫 줄들>
repo_status_clean() {
  local out; out="$(git -C "$1" status --porcelain 2>&1)"
  if [ -z "$out" ]; then printf yes; else printf '%s' "$(printf '%s' "$out" | tr '\n' ';')"; fi
}

# ── 워크트리 · workdir ───────────────────────────────────────────────────────
# workdir_rows SESSION → path  branch  status  merged  commits_ahead  gc_blocked_reason  tree_dirty
workdir_rows() {
  psqlq "select path_or_ref, coalesce(branch,'-'), status::text, coalesce(merged::text,'-'),
                coalesce(commits_ahead::text,'-'), coalesce(gc_blocked_reason,'-'), coalesce(tree_dirty::text,'-')
         from workdir where session_id='$1' order by path_or_ref"
}
workdir_field() { psqlq "select coalesce(($2)::text,'-') from workdir where id='$1'"; }
# gc_commands SESSION → 그 세션으로 큐잉된 gc 명령 수
gc_commands() { psqlq "select count(*) from daemon_command where type='gc' and session_id='$1'"; }
# inbox_count SESSION TYPE
inbox_count() { psqlq "select count(*) from inbox_item where session_id='$1' and type='$2'"; }

# ── 아티팩트 ────────────────────────────────────────────────────────────────
# artifact_rows SESSION → name  type  version  created_at (제출 순서)
artifact_rows() { psqlq "select name, type::text, version::text, created_at from artifact where session_id='$1' order by created_at, version"; }
artifact_ids_in_order() { psqlq "select id from artifact where session_id='$1' and type='diff' order by created_at, version"; }

# ── 요약 ────────────────────────────────────────────────────────────────────
summary_count() { psqlq "select count(*) from message where session_id='$1' and kind='summary'"; }
summary_body()  { psqlq "select coalesce(content,'') from message where session_id='$1' and kind='summary' limit 1"; }
# summary_feed SESSION → object_ref  detail (요약이 남긴 runtime 이벤트)
summary_feed() {
  psqlq "select coalesce(e.object_ref::text,'-'), coalesce(e.payload->>'detail','-')
         from task_event e join task t on t.id=e.task_id
         where t.session_id='$1' and e.object_ref::text like '%summary.%' order by e.created_at"
}

# ── 데몬 (실기 2종) ─────────────────────────────────────────────────────────
# daemon_pair_p4 CODE CONFIG WORKROOT CAPACITY REPO... — 페어링 + capacity + repos[]
# repos[] 는 §3 probe 의 저장소 목록이다. 재바인딩 후보 판정(E14-05)이 이것을 본다.
daemon_pair_p4() {
  local code="$1" cfg="$2" root="$3" cap="$4"; shift 4
  rm -f "$cfg"; mkdir -p "$root"
  COLAB_DAEMON_CONFIG="$cfg" "$BIN/daemon" pair "$code" --server "${PAIR_SERVER:-$SERVER_URL}" --workdir-root "$root" 2>&1 | tail -2 >&2
  local repos; repos="$(printf '%s\n' "$@" | jq -R . | jq -sc .)"
  jq --argjson c "$cap" --argjson r "$repos" '.capacity=$c | .repos=$r' "$cfg" > "$cfg.tmp" && mv "$cfg.tmp" "$cfg"
}
# runtime_of_config CONFIG → runtime_id
runtime_of_config() { jq -r .runtime_id "$1"; }

# ── PATH 안정화 (실측 2026-09-07) ───────────────────────────────────────────
# fnm 은 셸마다 `~/.local/state/fnm_multishells/<pid>_<ts>/bin` 을 만들고 **그 셸이 끝나면
# 지운다**. 데몬은 자기를 띄운 셸보다 오래 살고, claude_code 어댑터는 `npx` 를 PATH 로 찾는다 —
# 그래서 데몬이 그 경로를 물려받으면 셸이 끝난 뒤 모든 attempt 가
# `spawn: fork/exec …/npx: no such file or directory` · `failure_kind=config` 로 죽는다.
# 데몬에 넘기는 PATH 에서만 그 항목을 안정 경로(`~/.fnm/aliases/default/bin`)로 바꾼다.
stable_path() {
  python3 - "$PATH" <<'PYEOF'
import os, sys
alias = os.path.expanduser("~/.fnm/aliases/default/bin")
out, seen = [], set()
for p in sys.argv[1].split(":"):
    if "/fnm_multishells/" in p:
        p = alias
    if p and p not in seen:
        seen.add(p); out.append(p)
if alias not in seen and os.path.isdir(alias):
    out.append(alias)
print(":".join(out))
PYEOF
}

# daemon_run CONFIG LOG → pid (probe 턴을 돈다 — 능력 광고가 비면 S9·S11 이 빈다)
# 데몬의 CWD 는 **이 저장소 밖의 빈 디렉토리**로 둔다. 아래 결함(상대 workdir 경로)이
# 있는 동안 `os.MkdirAll(filepath.Dir(<상대경로>))` 가 데몬의 CWD 에 디렉토리를 만든다 —
# 그대로 두면 이 저장소가 미추적 파일로 더러워진다(§0-18 의 취지).
daemon_run() {
  local cwd="${DAEMON_CWD:-$P4_TMP_ROOT/daemon-cwd}"
  mkdir -p "$cwd"
  ( cd "$cwd" && PATH="$(stable_path)" COLAB_DAEMON_CONFIG="$1" setsid_run "$2" "$BIN/daemon" run )
}
# daemon_stop PIDFILE — pgid → pid 순서로만 (§0-10, 경로 기반 pkill 금지)
daemon_stop() {
  [ -f "$1" ] || return 0
  local pid; pid="$(cat "$1")"
  kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
  rm -f "$1"
}

# ── 실기 지시문 기본 문구 (§0-16) ───────────────────────────────────────────
# 에이전트가 저장소를 헤집거나 실패한 도구를 재시도하지 않게 한다.
P4_RULES="Work only inside your own working directory. Do not look at any other directory and do not search the wider filesystem. If a tool fails, stop and say so — do not retry it and do not look for another way."

# ── 세션 (worktree 격리) ────────────────────────────────────────────────────
# create_session_p4 WS TITLE GOAL ASSIGNEE RUNTIME REPO_PATH COND_JSON EXTRA_JSON PARTICIPANTS... → session id
# `isolation.kind=worktree` 는 P4 에서 처음 열린 경로다(P2·P3 는 전부 `none`).
create_session_p4() {
  local ws="$1" title="$2" goal="$3" assignee="$4" rt="$5" repo="$6" cond="$7" extra="${8:-{\}}"; shift 8
  local parts; parts="$(printf '%s\n' "$@" | jq -R . | jq -sc 'map({agent_id:.})')"
  api_ok POST "/workspaces/$ws/sessions" "$(jq -nc --arg t "$title" --arg g "$goal" --arg a "$assignee" --arg rt "$rt" \
      --arg repo "$repo" --argjson p "$parts" --argjson c "$cond" --argjson x "$extra" \
    '{title:$t,goal:$g,isolation:{kind:"worktree",repo_path:$repo},participants:$p,assignee_agent_id:$a,
      completion_condition:$c, runtime_id:$rt} + $x')" | jq -r .id
}
# cond_agent_approval AGENT → 종료 조건 JSON (E16-B 1단계: QA 승인 단독)
cond_agent_approval() { jq -nc --arg a "$1" '{op:"and",conditions:[{type:"agent_approval",agent_id:$a}]}'; }

# lane_of SESSION AGENT_NAME → lane id
lane_of() { psqlq "select l.id from lane l join agent a on a.id=l.agent_id where l.session_id='$1' and a.name='$2' order by l.created_at limit 1"; }
# lanes_count SESSION AGENT_NAME
lanes_count() { psqlq "select count(*) from lane l join agent a on a.id=l.agent_id where l.session_id='$1' and a.name='$2'"; }
# worktrees_of SESSION AGENT_NAME → 그 에이전트의 workdir 행 수
worktrees_of() { psqlq "select count(*) from workdir w join agent a on a.id=w.agent_id where w.session_id='$1' and a.name='$2'"; }
# workdir_path_of SESSION AGENT_NAME
workdir_path_of() { psqlq "select w.path_or_ref from workdir w join agent a on a.id=w.agent_id where w.session_id='$1' and a.name='$2' order by w.created_at limit 1"; }
# feed_classes TASK → class/verb 별 개수
feed_kinds() { psqlq "select class::text||'/'||coalesce(verb,'-'), count(*) from task_event where task_id='$1' group by 1 order by 1"; }
# feed_has SESSION CLASS VERB → 세션 전체에서 그 카드 수
feed_has() { psqlq "select count(*) from task_event e join task t on t.id=e.task_id where t.session_id='$1' and e.class='$2' and e.verb='$3'"; }

# ── 신규 결함 우회: 서버가 번들에 **상대** workdir 경로를 싣는다 ─────────────
# 실측(2026-09-07, dev c375b33): `queue.buildBundle` 은 `workdirs.PlanWorktree` 를 `Root` 없이
# 부르므로 TaskBundle 의 `workdir.path` 가 `"<session-slug>/<agent-slug>"` (상대)다. 데몬
# `PrepareWorktree` 는 그 값을 그대로 쓰고 `filepath.Abs` 로 **자기 CWD 기준** 절대화하는데,
# `git -C <repo> worktree add <상대경로>` 는 **저장소 안**에 체크아웃을 만든다 — 둘이 어긋나
# 어댑터에 없는 디렉토리가 cmd.Dir 로 가고 모든 attempt 가
# `failed(config) · spawn: fork/exec …/npx: no such file or directory` 로 죽는다.
# → **worktree 격리 세션은 첫 턴부터 하나도 돌지 않는다.**
#
# 측정을 이어가기 위한 우회다(보고서에 명시). 서버는 이 에이전트의 workdir 행이 이미 있으면
# 그 경로를 번들에 싣는다(`workdirs.BundleWorkdirPaths` → `ExistingForAgent`). 그래서 세션을
# `draft` 로 만들고 **의도된 절대 경로**(`<workdir_root>/worktrees/<slug>/<agent>`, 데몬
# `workdir.WorktreePath` 와 같은 규칙)를 workdir 행으로 미리 넣은 뒤 시작한다.
# 데몬의 워크트리 준비·브랜치·브리프·정리는 전부 실기 그대로 돈다(`reuse` 는 데몬이 읽지 않는다).
#
# seed_worktree_workdirs SESSION WORKROOT SLUG AGENT_ID:AGENT_SLUG...
seed_worktree_workdirs() {
  local sess="$1" root="$2" slug="$3"; shift 3
  local a id ag
  for a in "$@"; do
    id="${a%%:*}"; ag="${a#*:}"
    psqlq "insert into workdir (session_id, agent_id, kind, path_or_ref, branch, status, disk_bytes,
                                last_used_at, dirty, merged, commits_ahead, tree_dirty, created_at, updated_at)
           values ('$sess','$id','worktree','$root/worktrees/$slug/$ag','colab/$slug/$ag','active',0,
                   now(), false, false, 0, false, now(), now())
           on conflict (session_id, path_or_ref) do nothing" >/dev/null
  done
}

# retire_workdirs SESSION — 위 우회의 짝. 재바인딩은 옛 machine 의 workdir 행을
# `retained` 로만 바꾸는데 `BundleWorkdirPaths` 는 `status <> 'deleted'` 만 보므로 새 machine 의
# 번들이 **사라진 컴퓨터의 경로**를 그대로 가리킨다. 새 경로를 시드하기 전에 옛 행을 접는다.
retire_workdirs() { psqlq "update workdir set status='deleted' where session_id='$1'" >/dev/null; }
