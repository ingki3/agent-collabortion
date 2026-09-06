#!/usr/bin/env bash
# plan/spikes/spike04c/run_batch.sh — 스파이크 4c 한 배치.
#
#   run_batch.sh <batch> <arm:kind:count> [<arm:kind:count> ...]
#     arm  a = resume 성공 경로 (lane ref 그대로)
#          b = 강제 콜드 스타트 (lane ref 의 session_id 를 없는 값으로 → E8-03 유실 감지)
#          c = a 와 같은 런타임에서 ref 만 훼손 (프롬프트 변수만 갈라 보는 대조군)
#          d = warm-up 없음 → lane ref 가 아예 없는 상태로 중단 (E8-04 의 실제 모양)
#
# 한 배치 = 세션 N 개를 한 데몬에 동시에 얹고, 두 번째 메시지가 게시된 순간 데몬을 **pid 로** 죽인 뒤
# (§0-10) 서버의 3분 heartbeat 만료 재큐잉을 기다렸다가 데몬을 다시 띄운다. 3분 창을 N 개가 공유한다.
source "$(dirname "$0")/lib.sh"
BATCH="$1"; shift
CFG="$OUT/daemon-$BATCH.json"; WORK="$OUT/work-$BATCH"; DLOG="$OUT/daemon-$BATCH.log"
PIDF="$OUT/daemon-$BATCH.pid"; RES="$OUT/$BATCH.jsonl"
TAP="$OUT/tap-$BATCH.jsonl"; TAP_PORT="${TAP_PORT:-8096}"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-$BATCH.txt"; rm -f "$COOKIE"; : > "$RES"

# ── 과제: 저장소 밖의 무해한 것 (X-2). 파일명·시나리오 이름이 이 저장소에 없어야 한다. ──
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

step "0. claim 탭 — 서버가 데몬에 주는 TaskBundle(턴 프롬프트)을 기록한다"
rm -f "$TAP"; : > "$TAP"
python3 "$ROOT/e2e/p2/fixtures/claimtap.py" "$TAP_PORT" "$SERVER_URL" "$TAP" & TAP_PID=$!
trap 'kill "$TAP_PID" 2>/dev/null || true' EXIT
for i in $(seq 1 30); do curl -fsS -o /dev/null "http://localhost:$TAP_PORT/healthz" 2>/dev/null && break; sleep 0.3; done
ok "tap :$TAP_PORT → $SERVER_URL (pid $TAP_PID)"

step "0b. 가입 · 워크스페이스 · 페어링 (batch $BATCH)"
signup "s4c-$BATCH+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Spike4c $BATCH $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"; mkdir -p "$WORK"; rm -f "$CFG"; : > "$DLOG"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PTOK" --server "http://localhost:$TAP_PORT" --workdir-root "$WORK" 2>&1 | tail -2 >&2
jq '.capacity=20' "$CFG" > "$CFG.tmp" && mv "$CFG.tmp" "$CFG"
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$PIDF"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
ok "workspace $WS runtime $RUNTIME daemon pid $(cat "$PIDF")"

# ── 세션 생성 ──────────────────────────────────────────────────────────────
RUNS=()   # arm<TAB>kind<TAB>session<TAB>agent<TAB>lane<TAB>warmup_task
for spec in "$@"; do
  IFS=: read -r arm kind count <<<"$spec"
  for ((i=1;i<=count;i++)); do
    name="W$arm$i"
    AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$name" --arg i "$INS" --arg k "$kind" --arg m "$MODEL" \
      '{name:$n,role:"writer",role_description:"카탈로그 초안을 쓴다",instructions:$i,
        profiles:[{name:"default",runtime_kind:$k,model:$m,is_default:true}]}')" | jq -r .id)"
    S="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg t "카탈로그 $arm$i" --arg g "$GOAL" --arg a "$AG" --arg rt "$RUNTIME" \
      '{title:$t,goal:$g,isolation:{kind:"none"},runtime_id:$rt,participants:[{agent_id:$a}],assignee_agent_id:$a,
        completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
    RUNS+=("$arm	$kind	$S	$AG	$name")
    log "run $arm$i ($kind) session $S agent $name"
  done
done

# ── 1. warm-up 턴: 계획 한 줄 → finish 가 lane.runtime_session_ref 를 심는다 ─────
# arm d 는 warm-up 을 건너뛴다 — ref 가 없는 상태(E8-04 의 실제 모양)를 재현하는 것이 목적이다.
step "1. warm-up 턴 완료 대기 (lane ref 적재)"
DEADLINE=$(( $(date +%s) + 600 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for r in "${RUNS[@]}"; do
    IFS=$'\t' read -r arm kind S AG name <<<"$r"
    [ "$arm" = d ] && continue
    st="$(psqlq "select status from task where session_id='$S' order by created_at limit 1")"
    case "$st" in completed|failed|cancelled) ;; *) left=$((left+1));; esac
  done
  [ "$left" = 0 ] && break
  sleep 5
done
for r in "${RUNS[@]}"; do
  IFS=$'\t' read -r arm kind S AG name <<<"$r"
  ref="$(psqlq "select coalesce(runtime_session_ref::text,'null') from lane where session_id='$S' limit 1")"
  log "  $name warm-up ref: $(cut -c1-90 <<<"$ref")"
done

# ── 2. 본 작업 지시 → attempt 1 ────────────────────────────────────────────
step "2. '시작' 지시 → attempt 1"
for r in "${RUNS[@]}"; do
  IFS=$'\t' read -r arm kind S AG name <<<"$r"
  post_message "$S" "$(mention "$name" "$AG") 시작" >/dev/null
done
sleep 5
TASKS=()
for idx in "${!RUNS[@]}"; do
  IFS=$'\t' read -r arm kind S AG name <<<"${RUNS[$idx]}"
  t=""; for i in $(seq 1 60); do
    t="$(psqlq "select id from task where session_id='$S' order by created_at desc limit 1")"
    first="$(psqlq "select id from task where session_id='$S' order by created_at limit 1")"
    if [ -n "$t" ] && { [ "$t" != "$first" ] || [ "$arm" = d ]; }; then break; fi
    sleep 2
  done
  TASKS[$idx]="$t"; log "  $name work task $t"
done

# ── 3. 메시지 2개 게시 시점까지 대기 → 데몬 kill (pid) ──────────────────────
step "3. 각 런이 메시지 2개를 게시할 때까지 대기 (상한 ${KILL_DEADLINE_S:-420}s)"
DEADLINE=$(( $(date +%s) + ${KILL_DEADLINE_S:-420} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for idx in "${!RUNS[@]}"; do
    IFS=$'\t' read -r arm kind S AG name <<<"${RUNS[$idx]}"
    n="$(psqlq "select count(*) from message where source_task_id='${TASKS[$idx]}'")"
    st="$(psqlq "select status from task where id='${TASKS[$idx]}'")"
    case "$st" in completed|failed|cancelled) ;; *) [ "${n:-0}" -lt 2 ] && left=$((left+1));; esac
  done
  log "  아직 2개 미만: $left"
  [ "$left" = 0 ] && break
  sleep 3
done

step "4. 데몬 kill (pid $(cat "$PIDF")) + attempt 1 상태 스냅샷"
for idx in "${!RUNS[@]}"; do
  IFS=$'\t' read -r arm kind S AG name <<<"${RUNS[$idx]}"
  LANE="$(psqlq "select id from lane where session_id='$S' limit 1")"
  wd="$WORK/sessions/$S/$LANE"
  snap="$OUT/snap-$BATCH-$name"; mkdir -p "$snap"
  for f in part-one.md part-two.md; do [ -f "$wd/$f" ] && cp "$wd/$f" "$snap/$f" || : ; done
  psqlq "select id||E'\t'||replace(content,E'\n',' ') from message where source_task_id='${TASKS[$idx]}' order by created_at" > "$snap/posted.tsv" || true
  echo "$wd" > "$snap/workdir.txt"
done
# **SIGKILL** 이어야 한다. SIGTERM 은 데몬의 정상 종료 경로라 running task 를 취소로 finish 해
# 버려 재큐잉이 일어나지 않는다(스모크 실측: status=cancelled, attempt 1 에서 멈춤). 크래시를
# 재현하려면 finish 가 아예 가지 않아야 한다 → heartbeat 3분 만료 → E5-03 재큐잉.
kill -KILL -- "-$(cat "$PIDF")" 2>/dev/null || kill -KILL "$(cat "$PIDF")" 2>/dev/null || true
sleep 3
ok "데몬 SIGKILL"

# ── 5. arm 별 ref 조작 ─────────────────────────────────────────────────────
step "5. arm 별 lane.runtime_session_ref 조작"
for r in "${RUNS[@]}"; do
  IFS=$'\t' read -r arm kind S AG name <<<"$r"
  case "$arm" in
    b|c)
      # **실제 유실을 만든다** — ref 를 훼손하는 대신 런타임 쪽 세션 기록을 지운다.
      #   hermes      : ~/.hermes 의 state.db 대신 해당 ACP 세션 행만 (사용자 실데이터 보호)
      #   claude_code : ~/.claude/projects/<cwd 인코딩>/<sessionId>.jsonl (이 스파이크가 만든 것만)
      sid="$(psqlq "select runtime_session_ref->>'session_id' from lane where session_id='$S' limit 1")"
      LANE="$(psqlq "select id from lane where session_id='$S' limit 1")"
      wd="$WORK/sessions/$S/$LANE"
      case "$kind" in
        claude_code)
          enc="$(printf '%s' "$wd" | tr '/._' '---')"
          tr_file="$HOME/.claude/projects/$enc/$sid.jsonl"
          if [ -f "$tr_file" ]; then rm -f "$tr_file"; log "  $name transcript 제거 → 강제 콜드 스타트 ($sid)";
          else bad "  $name transcript 없음: $tr_file"; fi;;
        hermes)
          # SPIKE_04a §(b) 와 같은 방법 — ~/.hermes/state.db 에서 **이 세션 행만** 지운다.
          # (1차 시도에서 ref session_id 를 없는 값으로 바꾸는 방식을 썼더니 Hermes 가
          #  provenance 없는 응답을 주고 데몬이 그것을 `resumed` 로 처리해 E8-03 경로에
          #  들어가지 않았다 — 그 자체가 결함이고 보고서 §2 에 남긴다.)
          python3 "$(dirname "$0")/hermes_forget.py" "$sid" >&2 || bad "  $name hermes 세션 제거 실패"
          log "  $name hermes state.db 세션 제거 → 유실 감지 경로 ($sid)";;
      esac;;
    *)   log "  $name ref 그대로";;
  esac
done

# ── 6. 재큐잉(heartbeat 3분 만료) 대기 → 데몬 재기동 ────────────────────────
step "6. 재큐잉 대기 (heartbeat 만료 3분)"
DEADLINE=$(( $(date +%s) + 420 ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for idx in "${!RUNS[@]}"; do
    IFS=$'\t' read -r arm kind S AG name <<<"${RUNS[$idx]}"
    a="$(psqlq "select attempt from task where id='${TASKS[$idx]}'")"
    [ "${a:-1}" -lt 2 ] && left=$((left+1))
  done
  log "  attempt<2: $left"
  [ "$left" = 0 ] && break
  sleep 10
done
step "7. 데몬 재기동 → attempt 2"
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$PIDF"
ok "daemon pid $(cat "$PIDF")"

DEADLINE=$(( $(date +%s) + ${FINISH_DEADLINE_S:-900} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  left=0
  for idx in "${!RUNS[@]}"; do
    IFS=$'\t' read -r arm kind S AG name <<<"${RUNS[$idx]}"
    st="$(psqlq "select status from task where id='${TASKS[$idx]}'")"
    case "$st" in completed|failed|cancelled) ;; *) left=$((left+1));; esac
  done
  log "  attempt2 미완: $left"
  [ "$left" = 0 ] && break
  sleep 10
done

# ── 8. 측정 ────────────────────────────────────────────────────────────────
step "8. 측정 → $RES"
for idx in "${!RUNS[@]}"; do
  IFS=$'\t' read -r arm kind S AG name <<<"${RUNS[$idx]}"
  T="${TASKS[$idx]}"; snap="$OUT/snap-$BATCH-$name"; wd="$(cat "$snap/workdir.txt" 2>/dev/null || echo)"
  python3 "$(dirname "$0")/measure.py" \
     --batch "$BATCH" --arm "$arm" --kind "$kind" --name "$name" --session "$S" --task "$T" \
     --snap "$snap" --workdir "$wd" --pg "$PG_CONTAINER" --tap "$TAP" >> "$RES"
done
ok "batch $BATCH 완료 — $RES"
