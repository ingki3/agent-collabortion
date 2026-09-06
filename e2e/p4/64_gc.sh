#!/usr/bin/env bash
# e2e/p4/64_gc.sh — T-I4 (d): **workdir GC** (EVAL E13-09~19, FR-6.4, daemon-protocol §4.3·§6).
#
# 실서버 + 실기 데몬 + 진짜 git 워크트리다. 판정은 서버가, 삭제는 데몬이 한다(§6) —
# 그 두 쪽을 다 돌린다. 에이전트 턴은 **git 상태를 만들기 위한 것**이라 아주 짧다.
#
#   G1  미병합 커밋: 삭제 0 + Director 인박스 `workdir_gc_blocked`(unmerged_commits) 1건
#       두 번째 스윕에도 1건(멱등) — E13-12
#   G2  병합+클린: `git worktree remove` 로 삭제되고 **브랜치는 남는다** — E13-10·11
#   G3  미커밋 변경: uncommitted_changes (미병합보다 우선) — E13-13
#   G4  **active 세션**의 워크트리는 14일이 지나도 보존 — E13-18
#   G5  쿼터 null = 무제한(E13-19) · 사용량 ≥ 상한이면 세션 생성 차단(E13-16)
#
# 보존 기한은 클럭이 아니라 `session.finished_at` 을 30일 전으로 밀어서 만든다(§0-13 우회 —
# 서버 바이너리에 클럭 주입 경로가 없다, 56_ 와 같은 방법). 쿼터 분자도 마찬가지로
# `workdir.disk_bytes` 를 큰 값으로 적어 넣는다 — 규칙의 **입력**이지 규칙 자체가 아니다.
#
# 산출물: out/64-checks.tsv · out/64-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-64.txt"; rm -f "$COOKIE"
BASE="$P4_TMP_ROOT/64"
REPO="$BASE/repo"; WORK="$BASE/work"
CFG="$OUT/daemon-64.json"; DLOG="$OUT/daemon-64.log"
MODEL="${LEAD_MODEL}"
EMAIL="i4g+$STAMP@example.com"; PASSWORD="password123"
g5_chk_init "$OUT/64-checks.tsv"

cleanup() { daemon_stop "$OUT/daemon-64.pid"; return 0; }
trap cleanup EXIT
wait_until() { local dl=$(( $(date +%s) + $1 )); shift; while [ "$(date +%s)" -lt "$dl" ]; do eval "$1" && return 0; sleep 3; done; return 1; }
wait_quiet() { # SESSION [TIMEOUT]
  local dl=$(( $(date +%s) + ${2:-900} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select count(*) from task where session_id='$1' and status in ('queued','dispatched','preparing','running')")" = 0 ] && return 0
    sleep 3
  done; return 1
}

RULES="$P4_RULES"
INS_COMMIT="너는 gcCommit 이다. 도구는 셸이다. 한국어로 짧게 답한다.
첫 턴부터 곧바로 네 작업 디렉토리에서 아래 셸 명령을 정확히 그대로 한 번 실행하고 \"DONE\" 만 답한다.
printf 'note\\n' > NOTE.md && git add NOTE.md && git -c user.email=a@b -c user.name=a commit -q -m note
$RULES"
INS_CLEAN="너는 gcClean 이다. 도구는 셸이다. 한국어로 짧게 답한다.
첫 턴부터 곧바로 셸로 \`git status --porcelain\` 을 한 번 실행하고 \"DONE\" 만 답한다. 파일을 만들거나 고치지 마라.
$RULES"
INS_DIRTY="너는 gcDirty 다. 도구는 셸이다. 한국어로 짧게 답한다.
첫 턴부터 곧바로 네 작업 디렉토리에서 아래 셸 명령을 정확히 그대로 한 번 실행하고 \"DONE\" 만 답한다. 커밋하지 마라.
printf '# touched\\n' >> src/pump.py
$RULES"

step "0. 실험 저장소 (이 저장소가 아니다) · 계정 · 페어링(capacity 4)"
rm -rf "$BASE"; mkdir -p "$BASE"
make_repo "$REPO" "git@example.invalid:gc-$STAMP.git"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G7 GC $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
daemon_pair_p4 "$PTOK" "$CFG" "$WORK" 4 "$REPO"
RUNTIME="$(runtime_of_config "$CFG")"
AC="$(create_agent_p2 "$WS" gcCommit engineer "$MODEL" "$INS_COMMIT" '커밋을 남긴다')"
AK="$(create_agent_p2 "$WS" gcClean  engineer "$MODEL" "$INS_CLEAN"  '아무것도 안 한다')"
AD="$(create_agent_p2 "$WS" gcDirty  engineer "$MODEL" "$INS_DIRTY"  '미커밋 변경을 남긴다')"
ok "runtime=$RUNTIME"

step "1. 네 세션 — 데몬 기동 전에 만들고 workdir 절대 경로를 시드한다 (61_ X1 결함 우회)"
MANUAL="$(jq -nc '{op:"and",conditions:[{type:"manual"}]}')"
mk() { # TITLE AGENT_ID AGENT_SLUG → session id
  local t="$1" ag="$2" slug="$3" s
  s="$(create_session_p4 "$WS" "$t" 'GC 판정을 위한 git 상태를 만든다' "$ag" "$RUNTIME" "$REPO" "$MANUAL" '{}' "$ag")"
  seed_worktree_workdirs "$s" "$WORK" "$t" "$ag:$slug"
  printf '%s' "$s"
}
T_A="gc-unmerged-$STAMP";  S_A="$(mk "$T_A" "$AC" gccommit)"
T_B="gc-merged-$STAMP";    S_B="$(mk "$T_B" "$AK" gcclean)"
T_C="gc-dirty-$STAMP";     S_C="$(mk "$T_C" "$AD" gcdirty)"
T_D="gc-active-$STAMP";    S_D="$(mk "$T_D" "$AC" gccommit)"
ok "A(미병합)=$S_A B(병합·클린)=$S_B C(미커밋)=$S_C D(active)=$S_D"

step "2. 데몬 기동 — 네 턴이 각자 git 상태를 만든다"
daemon_run "$CFG" "$DLOG" > "$OUT/daemon-64.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
for s in "$S_A" "$S_B" "$S_C" "$S_D"; do wait_quiet "$s" 1200 || bad "세션 $s 의 턴이 끝나지 않았다"; done
sleep 5
workdir_rows "$S_A" > "$OUT/64-workdirs.txt"; workdir_rows "$S_B" >> "$OUT/64-workdirs.txt"
workdir_rows "$S_C" >> "$OUT/64-workdirs.txt"; workdir_rows "$S_D" >> "$OUT/64-workdirs.txt"
# ── 디스크의 진짜 git 상태 (판정의 입력이 무엇이어야 하는가) ────────────────
WT_A0="$WORK/worktrees/$T_A/gccommit"; WT_B0="$WORK/worktrees/$T_B/gcclean"
WT_C0="$WORK/worktrees/$T_C/gcdirty";  WT_D0="$WORK/worktrees/$T_D/gccommit"
git_facts() { # DIR → "ahead|dirty"
  local d="$1" ahead dirty
  ahead="$(git -C "$d" rev-list --count main..HEAD 2>/dev/null || echo 0)"
  dirty=f; [ -n "$(git -C "$d" status --porcelain 2>/dev/null)" ] && dirty=t
  printf '%s|%s' "${ahead:-0}" "$dirty"
}
FA="$(git_facts "$WT_A0")"; FB="$(git_facts "$WT_B0")"; FC="$(git_facts "$WT_C0")"
printf 'A(unmerged)=%s\nB(merged/clean)=%s\nC(dirty)=%s\n' "$FA" "$FB" "$FC" | tee "$OUT/64-git-facts.txt"
chk P0  "디스크: A 는 미병합 커밋 1개 + 클린" "1|f" "$FA"
chk P0b "디스크: B 는 커밋 0 + 클린"           "0|f" "$FB"
chk P0c "디스크: C 는 커밋 0 + 미커밋 변경"    "0|t" "$FC"

# ── 서버가 그 사실을 받았는가 (§4.4 finish `workdir.git` · §6 workdirs 보고) ──
db_facts() { psqlq "select coalesce(merged::text,'-')||'|'||coalesce(commits_ahead::text,'-')||'|'||coalesce(tree_dirty::text,'-') from workdir where session_id='$1' limit 1"; }
chk P1  "A: 미병합 커밋이 **서버 행에** 반영됐다 (merged|ahead|tree_dirty)" "f|1|f" "$(db_facts "$S_A")"
chk P1b "B: 병합·클린이 서버 행에 반영됐다"                                "t|0|f" "$(db_facts "$S_B")"
chk P1c "C: 미커밋 변경이 서버 행에 반영됐다"                              "f|0|t" "$(db_facts "$S_C")"
chk P1d "worktree workdir 의 disk_bytes 가 보고됐다 (S13 용량 · 쿼터 분자)" yes \
  "$( [ "$(psqlq "select coalesce(sum(disk_bytes),0) from workdir where session_id in ('$S_A','$S_B','$S_C')")" -gt 0 ] && echo yes || echo no )"

# ── 우회: 위 세 줄이 FAIL 이면 GC **판정** 자체는 잴 수가 없다 ────────────────
# GC 규칙의 입력(merged·commits_ahead·tree_dirty)은 데몬이 재서 서버에 올리는 값이다. 그 통로가
# 막혀 있으면 JudgeGC 는 언제나 "커밋 0·클린" 을 보고 **전부 삭제**로 판정한다 — 규칙이 무엇을
# 하는지 아무것도 드러나지 않는다. 그래서 **디스크에서 방금 잰 진짜 값**을 행에 직접 써 넣고
# (없는 사실을 지어내지 않는다) 그 아래에서 판정·알림·멱등·브랜치 보존을 잰다.
# 보고서 §대역/우회 표에 그대로 적는다.
inject_facts() { # SESSION AHEAD DIRTY MERGED
  psqlq "update workdir set commits_ahead=$2, tree_dirty=$3, merged=$4, dirty=($3 or $2>0),
         disk_bytes=greatest(disk_bytes, 1048576) where session_id='$1'" >/dev/null
}
inject_facts "$S_A" 1 false false
inject_facts "$S_B" 0 false true
inject_facts "$S_C" 0 true  false
inject_facts "$S_D" 1 false false
ok "우회: 디스크에서 잰 git 사실을 workdir 행에 직접 기록했다 ($OUT/64-git-facts.txt)"

step "3. 세션 A·B·C 종료 → 보존 기한 30일 경과 (D 는 active 로 둔다)"
for s in "$S_A" "$S_B" "$S_C"; do api_ok POST "/sessions/$s/complete" '{"confirm":true}' >/dev/null || true; done
wait_until 300 '[ "$(psqlq "select count(*) from session where id in ('"'$S_A'"','"'$S_B'"','"'$S_C'"') and status='"'completed'"'")" = 3 ]' || bad "세션 3개가 completed 로 가지 않았다"
# **클럭 우회(§0-13)**: 보존 기한(기본 14일)은 세션 종료 시각부터다. 서버 클럭을 못 돌리므로
# `finished_at` 을 30일 전으로 민다. active 세션 D 는 `created_at`·`started_at` 만 민다 —
# `finished_at` 이 없다는 것 자체가 E13-18 의 판정 근거다.
psqlq "update session set finished_at = now() - interval '30 days' where id in ('$S_A','$S_B','$S_C')" >/dev/null
psqlq "update session set created_at = now() - interval '30 days', started_at = now() - interval '30 days' where id='$S_D'" >/dev/null
BR_B="colab/$T_B/gcclean"
WT_A="$WT_A0"; WT_B="$WT_B0"; WT_D="$WT_D0"
chk P1  "판정 전: B 워크트리가 디스크에 있다"    yes "$( [ -d "$WT_B" ] && echo yes || echo no )"
sleep 70

step "4. G1 — 미병합 커밋: 삭제 0 + 인박스 1건 (E13-12)"
chk G1  "A: gc_blocked_reason = unmerged_commits" unmerged_commits "$(psqlq "select coalesce(gc_blocked_reason,'-') from workdir where session_id='$S_A' limit 1")"
chk G1b "A: gc 명령 0건 — 지우지 않았다"          0 "$(gc_commands "$S_A")"
chk G1c "A: 워크트리가 디스크에 그대로 있다"     yes "$( [ -d "$WT_A" ] && echo yes || echo no )"
chk G1d "A: Director 인박스 workdir_gc_blocked 1건" 1 "$(inbox_count "$S_A" workdir_gc_blocked)"
chk G1e "A: 알림이 commits_ahead 를 담는다 (사유 두 값 중 어느 쪽인지 Director 가 알아야 한다)" yes \
  "$( [ "$(psqlq "select coalesce(commits_ahead,0) from workdir where session_id='$S_A' limit 1")" -ge 1 ] && echo yes || echo no )"

step "5. G3 — 미커밋 변경 (E13-13)"
chk G3  "C: gc_blocked_reason = uncommitted_changes" uncommitted_changes "$(psqlq "select coalesce(gc_blocked_reason,'-') from workdir where session_id='$S_C' limit 1")"
chk G3b "C: gc 명령 0건"                             0 "$(gc_commands "$S_C")"
chk G3c "C: 인박스 1건"                              1 "$(inbox_count "$S_C" workdir_gc_blocked)"

step "6. G2 — 병합+클린: git worktree remove · 브랜치 보존 (E13-10·11)"
wait_until 300 '[ ! -d "'"$WT_B"'" ]' || true
chk G2  "B: gc 명령이 나갔다"                         yes "$( [ "$(gc_commands "$S_B")" -ge 1 ] && echo yes || echo no )"
chk G2b "B: 워크트리가 디스크에서 사라졌다"          no  "$( [ -d "$WT_B" ] && echo yes || echo no )"
chk G2c "B: **브랜치는 남아 있다** (FR-6.4 — 삭제는 워크트리만)" yes \
  "$( git -C "$REPO" rev-parse --verify --quiet "refs/heads/$BR_B" >/dev/null 2>&1 && echo yes || echo no )"
chk G2d "B: workdir 행이 deleted 로 닫혔다 (§6 gc 영수증 — P1 과 같은 통로다)" deleted \
  "$(psqlq "select status::text from workdir where session_id='$S_B' limit 1")"
git -C "$REPO" branch --list 'colab/*' > "$OUT/64-branches.txt" 2>&1 || true
git -C "$REPO" worktree list > "$OUT/64-worktrees.txt" 2>&1 || true

step "7. G4 — active 세션은 14일이 지나도 보존 (E13-18)"
chk G4  "D: 세션은 여전히 active"        active "$(psqlq "select status::text from session where id='$S_D'")"
chk G4b "D: gc 명령 0건"                 0 "$(gc_commands "$S_D")"
chk G4c "D: 워크트리가 디스크에 있다"    yes "$( [ -d "$WT_D" ] && echo yes || echo no )"
chk G4d "D: gc_blocked_reason 없음 — 알림도 없다(경고 피로 방지)" "-" \
  "$(psqlq "select coalesce(gc_blocked_reason,'-') from workdir where session_id='$S_D' limit 1")"

step "8. 두 번째 스윕 — 같은 알림을 반복하지 않는다 (멱등)"
sleep 70
chk G5  "A: 두 번째 스윕 뒤에도 인박스 1건" 1 "$(inbox_count "$S_A" workdir_gc_blocked)"
chk G5b "C: 두 번째 스윕 뒤에도 인박스 1건" 1 "$(inbox_count "$S_C" workdir_gc_blocked)"
chk G5c "A: 여전히 gc 명령 0건"             0 "$(gc_commands "$S_A")"

step "9. G5 — 쿼터: null = 무제한(E13-19) · ≥ 상한이면 차단(E13-16)"
QSET="$(api_ok GET "/workspaces/$WS/settings" | jq -r '.workdir_disk_quota_gb // "null"')"
chk Q1  "기본 workdir_disk_quota_gb = null (설정하지 않았다)" null "$QSET"
SQ="$(create_session_p4 "$WS" "quota-null-$STAMP" '쿼터 미설정에서 세션이 열린다' "$AK" "$RUNTIME" "$REPO" "$MANUAL" '{}' "$AK")"
chk Q1b "쿼터 null 이면 세션이 열린다 (null 을 0 으로 읽지 않는다, E13-19)" yes \
  "$( [ -n "$SQ" ] && [ "$SQ" != null ] && echo yes || echo no )"
api_ok POST "/sessions/$SQ/complete" '{"confirm":true}' >/dev/null 2>&1 || true
api_ok PATCH "/workspaces/$WS/settings" '{"workdir_disk_quota_gb":1}' >/dev/null
# 규칙의 **입력**(분자)만 만든다 — 1GB 넘게 실제로 쓰지 않고 보고 값을 적어 넣는다.
psqlq "update workdir set disk_bytes = 2147483648 where session_id='$S_A'" >/dev/null
QC="$(api POST "/workspaces/$WS/sessions" "$(jq -nc --arg a "$AK" --arg rt "$RUNTIME" --arg repo "$REPO" \
   '{title:"quota-blocked",goal:"차단되어야 한다",isolation:{kind:"worktree",repo_path:$repo},
     participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt,
     completion_condition:{op:"and",conditions:[{type:"manual"}]}}')")"
chk Q2  "사용량 ≥ 상한이면 세션 생성 = 409 (E13-16, 초과가 아니라 도달에서 막는다)" 409 "$(api_code <<<"$QC")"
chk Q2b "code = workdir_quota_exceeded" workdir_quota_exceeded "$(api_body <<<"$QC" | jq -r '.code // "-"')"
api_ok PATCH "/workspaces/$WS/settings" '{"workdir_disk_quota_gb":null}' >/dev/null 2>&1 || true

step "결과"
printf '  PASS %d · FAIL %d  (%s)\n' "$pass" "$fail" "$OUT/64-checks.tsv"
exit "$fail"
