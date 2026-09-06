#!/usr/bin/env bash
# e2e/p4/62_double_write.sh — T-I4 (b): **실행 중 데몬 kill -9 → 재시작 → 이중 쓰기 0**
# (EVAL E11-05·06, E8-04 (4), FR-9.1, daemon-protocol §5).
#
# 이 판이 재는 것은 하나다: **한 git 체크아웃에 두 프로세스가 동시에 쓰지 않는다.**
# 중복 메시지는 PRD §11 이 1% 까지 허용하지만 워크트리의 두 번째 writer 는 중복이 아니라
# 저장소 손상이다. 그래서 판정은 절대 개수로 한다 — 델타가 아니라.
#
#   D1  실기 런타임이 워크트리에 쓰는 동안 데몬을 `kill -9` 한다
#   D2  재시작한 데몬이 **claim 보다 먼저** 고아 프로세스 그룹을 정리한다 (E11-05)
#   D3  고아가 남긴 편집은 지우지 않는다 (E11-06)
#   D4  같은 workdir 에 살아 있는 프로세스 그룹 = 1 이하 (이중 쓰기 0)
#   D5  두 번째 턴이 같은 워크트리에 들어와도 브리프 마커 블록은 **절대 개수 1** (§8.4)
#   D6  `daemon/worktreesim` 100 라운드 (overlaps·lateClaims·dupEdits 전부 0) — 로그 인용
#
# 실기 런타임은 **hermes** 다. claude_code 는 브리프를 `acp_meta_system_prompt` 로 실어
# 디스크에 아무것도 쓰지 않으므로 D5(마커 절대 개수)를 잴 수 없다.
#
# 실험 저장소는 이 저장소가 아니다(§0-18). 산출물: out/62-checks.tsv · out/62-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-62.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-62.json"; WORK="$P4_TMP_ROOT/62/work"; DLOG="$OUT/daemon-62.log"
REPO="$P4_TMP_ROOT/62/repo"
HERMES_MODEL="${HERMES_MODEL:-claude-haiku-4-5-20251001}"
EMAIL="i4d+$STAMP@example.com"; PASSWORD="password123"
g5_chk_init "$OUT/62-checks.tsv"
MARKER_START='<!-- colab:brief:start -->'
# cnt FILE PATTERN — 항상 숫자 한 개만 낸다(grep -c 는 무매치면 exit 1 이라 `|| echo 0` 이 줄을 두 개 만든다)
cnt() { local n; n="$({ grep -cF "$2" "$1" 2>/dev/null || true; } | head -1 | tr -d ' \n')"; printf '%s' "${n:-0}"; }

cleanup() {
  daemon_stop "$OUT/daemon-62.pid"
  [ -f "$OUT/62-pgid" ] && kill -9 -- "-$(cat "$OUT/62-pgid")" 2>/dev/null
  return 0
}
trap cleanup EXIT

wait_until() { local dl=$(( $(date +%s) + $1 )); shift; while [ "$(date +%s)" -lt "$dl" ]; do eval "$1" && return 0; sleep 2; done; return 1; }
# 세션의 모든 task 가 멈출 때까지 (브리프 삭제·exclude 해제는 attempt 종료의 defer 다)
wait_quiet() {
  local dl=$(( $(date +%s) + ${2:-600} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select count(*) from task where session_id='$1' and status in ('queued','dispatched','preparing','running')")" = 0 ] && return 0
    sleep 3
  done; return 1
}

# 240회 × 0.5s = 120초. kill -9 를 턴 한복판에 넣기엔 넉넉하고, **재큐잉된 attempt 2 가 같은
# 반복문을 다시 도는 시간**(capacity 1 이라 두 번째 task 가 그 뒤에 온다)을 짧게 유지한다 —
# 900회(450초)로 두면 D5 의 관측 창이 두 번째 턴보다 먼저 닫힌다(실측).
LOOP_CMD='for i in $(seq 1 240); do echo "<orphan-$i>" >> AGENT_WORK.md; sleep 0.5; done'
RUN_INS="너는 Runner 다. 도구는 셸이다. 한국어로 짧게 답한다.
첫 턴부터 곧바로 네 작업 디렉토리에서 아래 셸 명령을 **정확히 그대로 한 번** 실행하고, 그다음 \"STARTED\" 라는 낱말만 답한다.
$LOOP_CMD
$P4_RULES"

step "0. 실험 저장소 (이 저장소가 아니다)"
mkdir -p "$P4_TMP_ROOT/62"
make_repo "$REPO" "git@example.invalid:runner-$STAMP.git"
AGENTS_MD_BEFORE="$(shasum "$REPO/AGENTS.md" | cut -d' ' -f1)"
ok "repo $REPO"

step "1. 계정 · 페어링(capacity 1) · Runner(hermes)"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G7 DoubleWrite $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
daemon_pair_p4 "$PTOK" "$CFG" "$WORK" 1 "$REPO"
RUNTIME="$(runtime_of_config "$CFG")"
RUN="$(create_agent_kind "$WS" Runner engineer hermes "$HERMES_MODEL" "$RUN_INS" '워크트리에 계속 쓴다')"
TITLE="double-write-$STAMP"; SLUG="$TITLE"
WT="$WORK/worktrees/$SLUG/runner"

step "2. D1 — 실기 턴이 워크트리에 쓰기 시작할 때까지"
# 데몬은 세션을 만들고 workdir 절대 경로를 시드한 **뒤에** 띄운다 — 61_ X1 의 결함 우회
# (서버가 번들에 상대 workdir 경로를 실어 worktree 세션이 첫 턴부터 죽는다).
S="$(create_session_p4 "$WS" "$TITLE" '워크트리에 계속 쓰는 긴 셸 작업을 돌린다' "$RUN" "$RUNTIME" "$REPO" \
     "$(jq -nc '{op:"and",conditions:[{type:"manual"}]}')" '{}' "$RUN")"
seed_worktree_workdirs "$S" "$WORK" "$SLUG" "$RUN:runner"
T1="$(session_initial_task "$S")"
daemon_run "$CFG" "$DLOG" > "$OUT/daemon-62.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
ok "session $S · task $T1"
wait_until 600 '[ -d "'"$WT"'" ]' || bad "워크트리가 준비되지 않았다"
chk D0  "워크트리 준비 ($WT)"                       yes "$( [ -d "$WT" ] && echo yes || echo no )"
chk D0b "브랜치 colab/$SLUG/runner"                 "colab/$SLUG/runner" "$(git -C "$WT" symbolic-ref --short HEAD 2>/dev/null || echo 없음)"
wait_until 600 '[ -f "'"$WORK"'/.colab/attempts/'"$T1"'.1.json" ]' || bad "pgid 기록이 없다"
PGID="$(jq -r .pgid "$WORK/.colab/attempts/$T1.1.json" 2>/dev/null || echo '')"
echo "${PGID:-0}" > "$OUT/62-pgid"
chk D0c "attempt pgid 가 체크아웃 밖(<workdir_root>/.colab)에 기록됐다" yes \
  "$( [ -n "$PGID" ] && [ "$PGID" != null ] && echo yes || echo "${PGID:-없음}" )"
# 브리프는 hermes 이므로 워크트리 안의 COLAB_BRIEF.md 다.
chk D0d "COLAB_BRIEF.md 마커 블록 1개 (턴 1)" 1 "$(cnt "$WT/COLAB_BRIEF.md" "$MARKER_START")"
chk D0e "COLAB_BRIEF.md 가 .git/info/exclude 에 등록돼 있다 (턴 중)" yes \
  "$( grep -qF 'COLAB_BRIEF' "$REPO/.git/info/exclude" 2>/dev/null && echo yes || echo no )"
chk D0f "추적 중 AGENTS.md 무변경 (M3)" "$AGENTS_MD_BEFORE" "$(shasum "$WT/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
wait_until 900 '[ -s "'"$WT"'/AGENT_WORK.md" ]' || bad "런타임이 워크트리에 쓰기 시작하지 않았다"
BEFORE="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
ok "런타임이 쓰기 시작했다 (<orphan-> $BEFORE 줄, pgid $PGID)"

step "3. 데몬 kill -9 (pid 로만, §0-10)"
DPID="$(cat "$OUT/daemon-62.pid")"
LOG_MARK="$(wc -l < "$DLOG" | tr -d ' ')"
kill -9 "$DPID" 2>/dev/null || true
wait_until 30 '! kill -0 "'"$DPID"'" 2>/dev/null' || bad "데몬이 죽지 않았다"
chk D1  "데몬 kill -9" yes "$( kill -0 "$DPID" 2>/dev/null && echo no || echo yes )"
ALIVE_AFTER_KILL=no; kill -0 -- "-$PGID" 2>/dev/null && ALIVE_AFTER_KILL=yes
# **관측이지 단정이 아니다**(55_ 와 같은 이유): 프로세스 그룹은 살아남지만 어댑터의 stdio 가
# 데몬과 함께 끊기면 그 셸 도구의 쓰기는 멈춘다. FR-9.1 이 요구하는 것은 아래 D2 다.
DURING="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
ok "관측: kill -9 직후 고아 그룹 생존=$ALIVE_AFTER_KILL · <orphan-> $BEFORE → $DURING"

step "4. D2·D3·D4 — 재시작이 claim 보다 먼저 고아를 정리한다"
daemon_run "$CFG" "$DLOG" > "$OUT/daemon-62.pid"
wait_until 120 '! kill -0 -- "-'"$PGID"'" 2>/dev/null' || bad "고아 프로세스 그룹이 정리되지 않았다"
sleep 3
chk D2  "재시작 뒤 고아 프로세스 그룹 = 0 (E11-05)" 0 \
  "$( kill -0 -- "-$PGID" 2>/dev/null && echo 1 || echo 0 )"
# 순서: 스윕 로그가 claim 로그보다 앞에 있어야 한다(worktreesim 의 lateClaims 와 같은 판정).
# 재시작 배너 줄부터 자른다(LOG_MARK 로 자르면 스윕 줄이 아직 안 써진 순간을 볼 수 있다 — 실측).
BANNER_LN="$({ grep -n 'colab-daemon' "$DLOG" || true; } | tail -1 | cut -d: -f1)"
tail -n "+${BANNER_LN:-1}" "$DLOG" > "$OUT/62-daemon-restart.log"
# pipefail 이 켜져 있다 — 무매치 grep 이 파이프라인을 실패시켜 스크립트가 조용히 끝난다(실측).
SWEEP_LN="$({ grep -n -m1 -e 'sweep' -e 'orphan' "$OUT/62-daemon-restart.log" || true; } | cut -d: -f1)"
CLAIM_LN="$({ grep -n -m1 -e 'claim' "$OUT/62-daemon-restart.log" || true; } | cut -d: -f1)"
chk D2b "고아 정리가 claim 보다 앞이다 (sweep@${SWEEP_LN:-없음} < claim@${CLAIM_LN:-없음})" yes \
  "$( if [ -n "$SWEEP_LN" ] && { [ -z "$CLAIM_LN" ] || [ "$SWEEP_LN" -lt "$CLAIM_LN" ]; }; then echo yes; else echo no; fi )"
AFTER="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
chk D3  "고아가 남긴 편집을 지우지 않았다 ($DURING → $AFTER, E11-06)" yes \
  "$( [ "$AFTER" -ge "$DURING" ] && echo yes || echo no )"
LIVE="$({ ps -o pgid= -ax 2>/dev/null || true; } | awk -v g="$PGID" '$1==g' | wc -l | tr -d ' ')"
chk D4  "같은 workdir 에 살아 있는 프로세스 그룹 0 (이중 쓰기 0)" 0 "$LIVE"

step "5. D5 — 두 번째 턴이 같은 워크트리에 들어와도 마커 절대 개수 1"
post_message "$S" "$(mention Runner "$RUN") AGENT_WORK.md 의 줄 수만 세어 알려주고 끝내라. 새 셸 반복문은 돌리지 마라." >/dev/null
# **여기서 턴이 끝나기를 기다리면 안 된다** — 브리프 파일은 턴이 도는 동안에만 있다(1차 실행에서
# `wait_quiet` 를 여기 두는 바람에 관측 창이 이미 닫힌 뒤에 셌다).
wait_tasks() { # SESSION N [TIMEOUT] — task 수가 N 이상이 될 때까지
  local dl=$(( $(date +%s) + ${3:-300} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select count(*) from task where session_id='$1'")" -ge "$2" ] && return 0
    sleep 2
  done; return 1
}
wait_tasks "$S" 2 300 || bad "두 번째 task 가 생기지 않았다"
T2="$(latest_task "$S")"
# 브리프 파일은 **턴이 도는 동안에만** 디스크에 있다(attempt 종료의 defer 가 지운다). 그래서
# 두 번째 턴이 도는 동안 계속 세어 **관측된 최대치**를 판정한다 — 1차 실행은 턴이 끝난 뒤에 재서
# 0 을 보았다. 재는 것은 "덧붙이지 않는가" 이므로 최대치가 곧 판정이다.
MAXMARK=0
DL=$(( $(date +%s) + 1500 ))
I=0
while [ "$(date +%s)" -lt "$DL" ]; do
  # **0.2초 간격**으로 본다. 브리프 파일은 턴이 도는 동안에만 있고 짧은 턴은 20초면 끝난다 —
  # 폴링 안에서 psqlq(docker exec) 를 부르면 한 바퀴가 2초씩 걸려 그 창을 통째로 놓친다(1차 실행 실측).
  n="$(cnt "$WT/COLAB_BRIEF.md" "$MARKER_START")"
  if [ "$n" -gt "$MAXMARK" ]; then MAXMARK="$n"; fi
  I=$((I+1))
  if [ $((I % 50)) = 0 ]; then
    ST2="$(psqlq "select count(*) from task where session_id='$S' and status in ('queued','dispatched','preparing','running')")"
    if [ "$ST2" = 0 ] && [ "$MAXMARK" -gt 0 ]; then break; fi
  fi
  sleep 0.2
done
chk D5  "두 번째 턴 중 COLAB_BRIEF.md 마커 블록 **절대 개수** 1 (덧붙이지 않는다)" 1 "$MAXMARK"
chk D5b "워크트리는 여전히 1개 (에이전트당 1개)" 1 "$(worktrees_of "$S" Runner)"
chk D5c "두 번째 턴도 같은 워크트리" "$WT" "$(workdir_path_of "$S" Runner)"
AFTER2="$(grep -c '<orphan-' "$WT/AGENT_WORK.md" 2>/dev/null || echo 0)"
chk D5d "두 번째 턴이 고아 편집을 덮어쓰지 않았다 ($AFTER → $AFTER2)" yes \
  "$( [ "$AFTER2" -ge "$AFTER" ] && echo yes || echo no )"

step "6. 세션 종료 뒤 위생 (§8.4 v0.16)"
# 브리프 삭제와 exclude 해제는 **attempt 종료의 defer** 다 — 마지막 턴이 돌고 있는 동안 재면
# 있지도 않은 오염을 본다(61_ 1차 실행 실측).
wait_quiet "$S" 900 || true
sleep 5
api_ok POST "/sessions/$S/complete" '{"confirm":true}' >/dev/null || true
wait_until 180 '[ "$(psqlq "select status::text from session where id='"'$S'"'")" = completed ]' || true
git -C "$WT" status --porcelain > "$OUT/62-status.txt" 2>&1 || true
chk D6  "세션 종료 뒤 COLAB_BRIEF.md 없음 (E13-05)" 0 "$(ls "$WT/COLAB_BRIEF.md" 2>/dev/null | wc -l | tr -d ' ')"
chk D6b "exclude 항목 해제 (E13-06)" 0 "$(cnt "$REPO/.git/info/exclude" 'COLAB_BRIEF')"
chk D6c "추적 중 AGENTS.md 무변경" "$AGENTS_MD_BEFORE" "$(shasum "$WT/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
chk D6d "git status 에 COLAB_BRIEF 잔여물 0" 0 "$(cnt "$OUT/62-status.txt" 'COLAB_BRIEF')"

step "7. D6 — daemon/worktreesim 100 라운드 (미러, 절대 개수)"
SIM_RC=0
( cd "$E2E_ROOT/daemon" && go test -tags p4golden -run 'TestWorktree(CrashRecoverySingleRound|DoubleWriteHundredRounds)GoldenMirror|TestClaimBeforeSweepIsCaught|TestOverlappingWritersObservation' -v ./worktreesim/ ) > "$OUT/62-worktreesim.log" 2>&1 || SIM_RC=$?
chk D7  "worktreesim 100 라운드 전부 통과" 0 "$SIM_RC"
SIM_LINE="$({ grep -o '100 rounds: overlaps=[0-9]* lateClaims=[0-9]* dupEdits=[0-9]*' "$OUT/62-worktreesim.log" || true; } | tail -1)"
chk D7b "100 라운드 수치 = overlaps 0 · lateClaims 0 · dupEdits 0" \
  "100 rounds: overlaps=0 lateClaims=0 dupEdits=0" "${SIM_LINE:-없음}"
ok "$SIM_LINE  ($OUT/62-worktreesim.log)"

step "결과"
printf '  PASS %d · FAIL %d  (%s)\n' "$pass" "$fail" "$OUT/62-checks.tsv"
exit "$fail"
