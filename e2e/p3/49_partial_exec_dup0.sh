#!/usr/bin/env bash
# e2e/p3/49_partial_exec_dup0.sh — T-I3 (b): **부분 실행 → 재개, 중복 0** 실기 1회 (E8-04).
#
# 시뮬레이터 100회는 CI(서버 test/sim, T-P3a)가 돌린다. 여기서 재는 것은 **실기 한 번**이다:
#   파일 절반 편집 + 메시지 2개 게시 상태에서 데몬 **SIGKILL** → heartbeat 3분 만료 재큐잉(E5-03)
#   → attempt 2 재개 → 같은 메시지 재게시 0 · 같은 편집 중복 0 · 같은 workdir·같은 파일.
#
# 판정기는 스파이크 4c 의 것을 그대로 옮겨 쓴다(`fixtures/measure_dup0.py`) — 같은 자를 쓰면 스파이크
# 표와 이 실기 1회가 비교 가능하다(T-I3 지시 "재사용 가능"). 원본 대비 고친 것은 psql 마지막 줄의
# 빈 후행 칸이 잘려 죽던 곳 하나뿐이다(G6 1회차 실측 — 픽스처 헤더에 적었다).
#
# **재현 함정 두 가지(SPIKE_04c §0.2)** — 여기서도 그대로 적용된다:
#   1. kill 은 **SIGKILL** 이어야 한다. SIGTERM 은 데몬의 정상 종료 경로라 running task 를
#      finish(outcome=cancelled) 로 닫아 재큐잉이 아예 일어나지 않는다.
#   2. `lane.runtime_session_ref` 는 finish 에서만 저장된다. 그래서 **warm-up 턴**(계획 한 줄만
#      게시하고 끝내는 턴)이 먼저 있어야 attempt 2 가 resume 을 시도한다. warm-up 이 없으면
#      resume 필드가 비어 콜드 스타트가 되고 — 그것이 실사용의 기본 모양이라 (b2) 로 같이 잰다.
#
# 산출물: out/49-checks.tsv · out/49.jsonl(런별 판정치) · out/49-prompt-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-49.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-49.json"; WORK="$OUT/work-49"; DLOG="$OUT/daemon-49.log"; PIDF="$OUT/daemon-49.pid"
TAP="$OUT/tap-49.jsonl"; TAP_PORT="${TAP_PORT_49:-8102}"
RES="$OUT/49.jsonl"; : > "$RES"
MODEL="${LEAD_MODEL}"
MEASURE="$P3_DIR/fixtures/measure_dup0.py"
g5_chk_init "$OUT/49-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$PIDF" ] && { kill -TERM -- "-$(cat "$PIDF")" 2>/dev/null || true; }
  return 0
}
trap cleanup EXIT

# 과제는 저장소 밖의 무해한 것(X-2). 스파이크 4c 와 **같은 과제**를 쓴다 — 판정기를 공유하기 때문이다.
INS='너는 가상의 스마트 물병 제품 카탈로그 초안을 만드는 작성자다. 답은 한국어로 짧게.

세션이 시작되면 **계획 한 줄만** colab message post 로 게시하고 턴을 끝낸다. 실제 작성은 Director 가 "시작" 이라고 말한 뒤에 한다.

"시작" 지시를 받으면 현재 작업 디렉토리에서 아래 네 단계를 순서대로 수행한다.
- 단계 A1: 파일 part-one.md 에 `- [1] …` `- [2] …` `- [3] …` 세 줄을 쓴다. 끝나면 colab message post 로 `STAGE-A1 done` 을 게시한다.
- 단계 B1: 파일 part-two.md 에 `- [1] …` `- [2] …` `- [3] …` 세 줄을 쓴다. 끝나면 colab message post 로 `STAGE-B1 done` 을 게시한다.
- 단계 A2: part-one.md 에 `- [4] …` `- [5] …` `- [6] …` 세 줄을 덧붙인다. 끝나면 colab message post 로 `STAGE-A2 done` 을 게시한다.
- 단계 B2: part-two.md 에 `- [4] …` `- [5] …` `- [6] …` 세 줄을 덧붙인다. 끝나면 colab message post 로 `STAGE-B2 done` 을 게시한다.
네 단계가 모두 끝나면 마지막으로 `ALL-DONE` 을 게시하고 턴을 끝낸다.
각 항목은 물병 기능을 설명하는 짧은 한 줄이다. 파일은 각각 정확히 여섯 줄이면 끝난 것이다.'
GOAL='가상의 스마트 물병 제품 카탈로그 초안 두 조각을 작업 디렉토리에 만든다'

step "0. claim 탭"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT (pid $TAP_PID)"

step "1. 계정 · 워크스페이스 · 페어링 (capacity=4 — 두 런이 같은 3분 창을 공유한다)"
: > "$DLOG"
signup "g6b+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G6 Dup0 $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_cap "$PTOK" "$CFG" "$WORK" 4
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$PIDF"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "ws=$WS runtime=$RUNTIME daemon pid $(cat "$PIDF")"

# 두 런: b1 = warm-up 있음(재개 경로) · b2 = warm-up 없음(E8-04 의 실제 모양 — resume 필드가 비어 있다)
NAMES=(Bwarm Bcold); WARM=(yes no)
SESS=(); AGS=(); TASKS=()
step "2. 세션 2개 (b1 warm-up 있음 · b2 없음)"
for i in 0 1; do
  n="${NAMES[$i]}"
  AG="$(create_agent_p2 "$WS" "$n" writer "$MODEL" "$INS" '카탈로그 초안을 쓴다')"
  S="$(create_session_p3 "$WS" "카탈로그 $n" "$GOAL" "$AG" "$RUNTIME" '{}' "$AG")"
  AGS+=("$AG"); SESS+=("$S")
  ok "$n session=$S"
done

step "3. warm-up 턴 완료 대기 — finish 가 lane.runtime_session_ref 를 심는다"
DEADLINE=$(( $(date +%s) + ${WARMUP_S:-600} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for i in 0 1; do
    st="$(psqlq "select status from task where session_id='${SESS[$i]}' order by created_at limit 1")"
    case "$st" in completed|failed|cancelled) ;; *) left=$((left+1));; esac
  done
  [ "$left" = 0 ] && break; sleep 5
done
for i in 0 1; do
  ref="$(psqlq "select coalesce(runtime_session_ref::text,'null') from lane where session_id='${SESS[$i]}' limit 1")"
  log "  ${NAMES[$i]} warm-up ref: $(cut -c1-80 <<<"$ref")"
done
chk P0 "b1(warm-up 있음) lane 에 runtime_session_ref 가 심겼다 (E8-13)" yes \
  "$( [ "$(psqlq "select coalesce(runtime_session_ref::text,'null') from lane where session_id='${SESS[0]}' limit 1")" != null ] && echo yes || echo no )"
# b2 는 warm-up ref 를 **일부러 지운다** — E8-04 는 "ref 가 없는 크래시"가 기본 모양이다(SPIKE_04c §0.2).
psqlq "update lane set runtime_session_ref=null where session_id='${SESS[1]}'" >/dev/null
ok "b2 의 lane ref 를 비웠다 (ref 없는 크래시 = E8-04 의 실제 모양)"

step "4. '시작' → attempt 1"
for i in 0 1; do post_message "${SESS[$i]}" "$(mention "${NAMES[$i]}" "${AGS[$i]}") 시작" >/dev/null; done
sleep 5
for i in 0 1; do
  t=""; first="$(psqlq "select id from task where session_id='${SESS[$i]}' order by created_at limit 1")"
  for _ in $(seq 1 60); do
    t="$(psqlq "select id from task where session_id='${SESS[$i]}' order by created_at desc limit 1")"
    [ -n "$t" ] && [ "$t" != "$first" ] && break; sleep 2
  done
  TASKS+=("$t"); log "  ${NAMES[$i]} work task $t"
done

step "5. 각 런이 메시지 2개를 게시할 때까지 대기 (상한 ${KILL_DEADLINE_S:-480}s)"
DEADLINE=$(( $(date +%s) + ${KILL_DEADLINE_S:-480} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for i in 0 1; do
    n="$(psqlq "select count(*) from message where source_task_id='${TASKS[$i]}'")"
    st="$(psqlq "select status from task where id='${TASKS[$i]}'")"
    case "$st" in completed|failed|cancelled) ;; *) [ "${n:-0}" -lt 2 ] && left=$((left+1));; esac
  done
  [ "$left" = 0 ] && break; sleep 3
done
for i in 0 1; do
  log "  ${NAMES[$i]} 게시 $(psqlq "select count(*) from message where source_task_id='${TASKS[$i]}'")건 상태 $(psqlq "select status from task where id='${TASKS[$i]}'")"
done

step "6. 스냅샷 + 데몬 **SIGKILL** (pgid $(cat "$PIDF"))"
SNAPS=(); WDS=()
for i in 0 1; do
  LANE="$(psqlq "select id from lane where session_id='${SESS[$i]}' limit 1")"
  wd="$WORK/sessions/${SESS[$i]}/$LANE"; WDS+=("$wd")
  snap="$OUT/snap-49-${NAMES[$i]}"; rm -rf "$snap"; mkdir -p "$snap"; SNAPS+=("$snap")
  for f in part-one.md part-two.md; do [ -f "$wd/$f" ] && cp "$wd/$f" "$snap/$f" || :; done
  psqlq "select id||E'\t'||replace(content,E'\n',' ') from message where source_task_id='${TASKS[$i]}' order by created_at" > "$snap/posted.tsv" || true
  echo "$wd" > "$snap/workdir.txt"
done
# SIGTERM 은 정상 종료 경로다 — finish(cancelled) 가 가면 재큐잉이 없다. 크래시는 SIGKILL 이어야 한다.
kill -KILL -- "-$(cat "$PIDF")" 2>/dev/null || kill -KILL "$(cat "$PIDF")" 2>/dev/null || true
sleep 3
ok "데몬 SIGKILL — finish 가 아예 가지 않는다"
for i in 0 1; do
  chk "K$i" "${NAMES[$i]}: kill 시점에 메시지 2건 이상 게시돼 있다" yes \
    "$( [ "$(wc -l < "${SNAPS[$i]}/posted.tsv" | tr -d ' ')" -ge 2 ] && echo yes || echo no )"
  chk "K${i}b" "${NAMES[$i]}: kill 시점에 파일이 절반 상태다 (한 파일이라도 존재)" yes \
    "$( { [ -f "${SNAPS[$i]}/part-one.md" ] || [ -f "${SNAPS[$i]}/part-two.md" ]; } && echo yes || echo no )"
done

step "7. 재큐잉 대기 (heartbeat 3분 만료, E5-03)"
DEADLINE=$(( $(date +%s) + 420 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for i in 0 1; do a="$(psqlq "select attempt from task where id='${TASKS[$i]}'")"; [ "${a:-1}" -lt 2 ] && left=$((left+1)); done
  log "  attempt<2: $left"
  [ "$left" = 0 ] && break; sleep 10
done
for i in 0 1; do
  chk "Q$i" "${NAMES[$i]}: 죽은 attempt 가 재큐잉돼 attempt 2 가 됐다" 2 "$(psqlq "select attempt from task where id='${TASKS[$i]}'")"
  chk "Q${i}b" "${NAMES[$i]}: 같은 task 다 (재지시가 아니다)" 1 "$(psqlq "select count(*) from task where id='${TASKS[$i]}' and restarted_from_task_id is null")"
done

step "8. 데몬 재기동 → attempt 2 완료 대기"
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$PIDF"
ok "daemon pid $(cat "$PIDF")"
DEADLINE=$(( $(date +%s) + ${FINISH_S:-1200} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for i in 0 1; do
    st="$(psqlq "select status from task where id='${TASKS[$i]}'")"
    case "$st" in completed|failed|cancelled) ;; *) left=$((left+1));; esac
  done
  log "  attempt2 미완: $left"
  [ "$left" = 0 ] && break; sleep 10
done

step "9. 측정 (스파이크 4c 판정기 그대로)"
for i in 0 1; do
  python3 "$MEASURE" --batch g6 --arm "${WARM[$i]}" --kind claude_code --name "${NAMES[$i]}" \
    --session "${SESS[$i]}" --task "${TASKS[$i]}" --snap "${SNAPS[$i]}" --workdir "${WDS[$i]}" \
    --pg "$PG_CONTAINER" --tap "$TAP" >> "$RES"
done
python3 - "$RES" <<'PY' > "$OUT/49-summary.tsv"
import json,sys
rows=[json.loads(l) for l in open(sys.argv[1]) if l.strip()]
print("name\twarmup\tstatus\tattempt\tresumed\tcontinued\tsame_files\tdup_messages\tdup_edits\tworkdir_first")
for r in rows:
    a2=r.get("attempt2_resume") or {}
    print("\t".join(str(x) for x in [r["name"],r["arm"],r["status"],r["attempt"],a2.get("outcome","-"),
        r["continued"],r["same_files"],r["dup_messages"],r["dup_edits"],r["workdir_first"]]))
PY
column -t -s $'\t' "$OUT/49-summary.tsv" >&2

for i in 0 1; do
  J="$(python3 -c "import json,sys;print(json.dumps([json.loads(l) for l in open(sys.argv[1]) if l.strip()][$i],ensure_ascii=False))" "$RES")"
  g() { python3 -c "import json,sys;d=json.loads(sys.argv[1]);print(d.get(sys.argv[2]))" "$J" "$1"; }
  N="${NAMES[$i]}"
  chk "D$i"  "$N: **같은 메시지 재게시 0** (E8-04 (3))"           0 "$(g dup_messages)"
  chk "D${i}b" "$N: **같은 편집 중복 0** (E8-04 (4))"             0 "$(g dup_edits)"
  chk "D${i}c" "$N: 남은 작업을 끝냈다 (두 파일 6항목 + ALL-DONE)" True "$(g continued)"
  chk "D${i}d" "$N: attempt 1 이 만든 **같은 파일**을 이어서 편집했다 (E8-04 (1))" True "$(g same_files)"
  chk "D${i}e" "$N: attempt 2 가 workdir 를 **먼저 확인**했다"     True "$(g workdir_first)"
  chk "D${i}f" "$N: task 가 completed 로 닫혔다"                  completed "$(g status)"
  P="$OUT/49-prompt-${N}.txt"
  python3 -c "import json,sys;d=json.loads(sys.argv[1]);sys.stdout.write(d.get('prompt2') or '')" "$J" > "$P"
  chk_has "D${i}g" "$N: attempt 2 프롬프트에 <resumed> 구간"        "$P" "<resumed attempt=2>"
  chk_has "D${i}h" "$N: **이미 게시한 메시지 목록**이 실려 있다 (E8-04 (2))" "$P" "Messages you already posted"
  # 스파이크 4c §5-1 은 "목록이 UUID 뿐이라 무엇을 게시했는지 알려주지 않는다"를 부족 항목으로 적었다.
  # PR #124 뒤에는 본문 한 줄이 같이 실린다 — 그것이 실려 있는지 여기서 확인한다.
  chk_has "D${i}k" "$N: 그 목록에 **메시지 본문**도 실린다 (SPIKE_04c §5-1 이 지적한 빈칸)" "$P" "STAGE-A1"
  chk_has "D${i}i" "$N: \"workdir 를 먼저 확인하라\" 지시"          "$P" "inspect the current state of the workdir"
  PMIDS="$(python3 -c "import json,sys;print(len(json.loads(sys.argv[1]).get('posted_message_ids') or []))" "$J")"
  chk_ge "D${i}j" "$N: posted_message_ids 가 kill 전 게시분을 담는다" 2 "$PMIDS"
done
# b1 은 warm-up ref 가 있으므로 resume 을, b2 는 없으므로 콜드 스타트를 기대한다.
R1="$(python3 -c "import json,sys;d=[json.loads(l) for l in open(sys.argv[1]) if l.strip()][0];print((d.get('attempt2_resume') or {}).get('outcome','-'))" "$RES")"
R2="$(python3 -c "import json,sys;d=[json.loads(l) for l in open(sys.argv[1]) if l.strip()][1];print((d.get('attempt2_resume') or {}).get('outcome','-'))" "$RES")"
chk E1 "b1(warm-up 있음)은 **resume** 으로 붙었다"        resumed "$R1"
chk E2 "b2(ref 없음)는 resume 이 아니다 — 그래도 중복 0 이다" no \
  "$( [ "$R2" = resumed ] && echo yes || echo no )"
log "resume 판정: b1=$R1 b2=$R2"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
[ "$fail" = 0 ]
