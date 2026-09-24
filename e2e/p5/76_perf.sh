#!/usr/bin/env bash
# e2e/p5/76_perf.sh — **성능 (PRD §9)**: 지연 100회 + 동시 task 50 부하. 페이크 런타임(모델 0회).
#
# 비용 한 줄(I-3): 페이크 턴 ≈ 250(지연 100 · 첫 출력 100 · 동시 50) · $0 · ≈ 130s. 실기로 돌리지 마라 — haiku ≈ $1~2 · 20분+
#
#   (1) 지연 — 메시지 게시 → 데몬 claim **p50 < 2s**, 에이전트 첫 출력(격리 none) **p50 < 10s**. 100회.
#       한 세션·한 lane 에 메시지를 **하나씩**(이전 task 가 끝난 뒤) 게시한다 — 겹치면 lane 병합으로 한 task 가 되어
#       "게시→claim" 이 아니라 "게시→다음 턴" 을 재게 된다. 시각은 서버 DB 단일 클럭(p1 lib latency_row).
#   (2) 부하 — 워크스페이스 1 · 데몬 5대 × capacity 10 · 세션 50개 동시 생성(각 첫 task) → 동시 running 최대,
#       claim p50/p95 · API p50/p95(부하 중 GET /rooms/{id} — getRoom, 옛 getSession 자리) · DB 커넥션 최대 · 재큐잉 0 · 이중 게시 0.
#
# 수치는 out/76-latency.tsv · out/76-load.tsv · out/76.json 에 표로. 판정 상한은 §9 그대로.
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-76.txt"; rm -f "$COOKIE"
WORK="$P5_TMP_ROOT/76/work"; MODEL="${LEAD_MODEL}"
N_LAT="${N_LAT:-100}"; N_LOAD="${N_LOAD:-50}"; N_DAEMONS="${N_DAEMONS:-5}"; CAP="${CAP:-10}"
g5_chk_init "$OUT/76-checks.tsv"
cleanup() { local f; for f in "$OUT"/daemon-76-*.pid; do daemon_stop "$f"; done; return 0; }
trap cleanup EXIT
ECHO_INS='너는 Echo 다. 지시를 받으면 colab_message_post 로 "echo: <지시 첫 줄>" 한 줄을 게시하고 colab_status_set done. 다른 일은 하지 마라.'
pct() { # pct P  ← stdin 숫자들 → p-분위수 (nearest-rank)
  sort -n | awk -v p="$1" '{a[NR]=$1} END{if(NR==0){print "NA";exit} i=int((NR*p+99)/100); if(i<1)i=1; print a[i]}'; }
db_conns() { psqlq "select count(*) from pg_stat_activity where datname='colab' and backend_type='client backend'"; }

step "1. 계정 · 워크스페이스 · 데몬 $N_DAEMONS 대 (capacity $CAP)"
signup "i5p+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G9 Perf $STAMP")"
rm -rf "$WORK"
RTS=()
for i in $(seq 1 "$N_DAEMONS"); do
  read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
  CFG="$OUT/daemon-76-$i.json"; : > "$OUT/daemon-76-$i.log"
  daemon_pair_p5 "$PTOK" "$CFG" "$WORK/d$i" "$CAP"
  daemon_run_p5 "$CFG" "$OUT/daemon-76-$i.log" > "$OUT/daemon-76-$i.pid"
  wait_pairing "$WS" "$PID_" 300 || die "pairing $i not ready"
  RTS+=("$(runtime_of_config "$CFG")")
done
ok "runtimes: ${RTS[*]}"
ECHO="$(create_agent_fake "$WS" Echo researcher claude_code "$MODEL" "$ECHO_INS" '한 줄 답만 한다')"
# 부하용 에이전트: 턴이 LOAD_TURN_MS 만큼 걸린다(페이크 sleep) — 그래야 50 task 가 실제로 **겹친다**.
# 페이크 턴이 0.2s 면 슬롯 50개를 채우기 전에 끝나 동시성을 잴 수 없다(첫 실행 실측 max=3).
LOAD_TURN_MS="${LOAD_TURN_MS:-4000}"
# 같은 에이전트 하나가 50 세션을 맡으므로 에이전트 상한(FR-6.3, 기본 3)을 열어 둔다 — 재는 것은
# 워크스페이스·런타임 층이다(첫 실행 실측: 기본값 3 이 동시성을 3 으로 눌렀다).
LOAD="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg i "$ECHO_INS" --argjson env "$(fake_env Load claude \
    "$(jq -nc --argjson ms "$LOAD_TURN_MS" --arg fix "$FIX" '{turns:[{steps:[{sleep_ms:$ms},{exec:("bash "+$fix+"/agent.sh Echo")}]}]}')")" \
    --argjson n "$N_LOAD" \
  '{name:"Load",role:"researcher",role_description:"부하용",instructions:$i,max_concurrent_tasks:$n,
    profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-haiku-4-5-20251001",is_default:true,env:$env}]}')" | jq -r .id)"
DB0="$(db_conns)"

step "2. 지연 — $N_LAT 회 (게시 → claim · 첫 출력)"
S="$(create_session_p3 "$WS" "perf latency" "지연 측정" "$ECHO" "${RTS[0]}" '{}' "$ECHO")"
T_INIT="$(session_initial_task "$S")"; WAIT_S=120 wait_task "$T_INIT" completed failed >/dev/null
printf 'i\ttask\tclaim_s\tfirst_event_s\tfirst_out_s\tfirst_say_s\treply_s\n' > "$OUT/76-latency.tsv"
LAT_START="$(now_ms)"
for i in $(seq 1 "$N_LAT"); do
  R="$(post_message "$S" "$(mention Echo "$ECHO") ping $i")"
  T="$(jq -r '.triggers[0].task_id // empty' <<<"$R")"
  [ -n "$T" ] || { bad "post $i 가 task 를 만들지 않았다: $R"; continue; }
  WAIT_S=60 wait_task "$T" completed failed cancelled >/dev/null || true
  printf '%s\t%s\t%s\n' "$i" "$T" "$(latency_row "$T" 1)" >> "$OUT/76-latency.tsv"
done
LAT_ELAPSED=$(( ($(now_ms)-LAT_START)/1000 ))
CLAIM_P50="$(cut -f3 "$OUT/76-latency.tsv" | tail -n +2 | pct 50)"; CLAIM_P95="$(cut -f3 "$OUT/76-latency.tsv" | tail -n +2 | pct 95)"
OUT_P50="$(cut -f5 "$OUT/76-latency.tsv" | tail -n +2 | pct 50)";   OUT_P95="$(cut -f5 "$OUT/76-latency.tsv" | tail -n +2 | pct 95)"
REPLY_P50="$(cut -f7 "$OUT/76-latency.tsv" | tail -n +2 | pct 50)"
N_DONE="$(tail -n +2 "$OUT/76-latency.tsv" | awk -F'\t' '$3!=""' | wc -l | tr -d ' ')"
chk L0 "지연 표본 $N_LAT 건" "$N_LAT" "$N_DONE"
chk_lt L1 "게시 → claim p50 < 2s (§9)" 2 "$CLAIM_P50"
chk_lt L2 "게시 → 첫 출력 p50 < 10s (격리 none, §9)" 10 "$OUT_P50"
log "claim p50=$CLAIM_P50 p95=$CLAIM_P95 · 첫 출력 p50=$OUT_P50 p95=$OUT_P95 · 답글 p50=$REPLY_P50 · ${LAT_ELAPSED}s"

step "3. 부하 — 세션 $N_LOAD 개 동시 (데몬 $N_DAEMONS × cap $CAP = $((N_DAEMONS*CAP)) 슬롯)"
SESS=()
LOAD_START="$(now_ms)"
# v0.3.0(R4): 옛 createSession 한 번이 createRoom·updateRoom·addRoomParticipant·createWork 네 번이 됐다. 50 번을 차례로
# 한꺼번에 돌리면 생성에 ~7s 가 걸려 페이크 턴(~4s)이 먼저 끝나 동시 running 이 슬롯을 못 채운다(CI got=32) — 제품이 아니라
# 하네스가 재는 값이 바뀐 것. 병렬 생성은 쓸 수 없다(api() 가 쿠키 통을 -c 로 다시 써 동시 호출이 로그인을 깨뜨린다).
# 그래서 두 단계: 방·참여자를 먼저 다 만들고(task 없음), 미션을 한 호출씩 몰아 연다 — 옛 createSession 한 번 = 초기 task 한 개와 같은 밀도.
LOAD_ROOMS=()
for i in $(seq 1 "$N_LOAD"); do
  # runtime_id 를 비운다 → 온라인 런타임 아무거나(첫 claim 이 고정, E11-10) — 5대에 분산된다.
  r="$(api_ok POST "/workspaces/$WS/rooms" "$(jq -nc --arg n "perf load $i" '{name:$n,description:""}')" | jq -r .id)"
  api_ok PATCH "/rooms/$r" '{"isolation":{"kind":"none"}}' >/dev/null
  api_ok POST "/rooms/$r/participants" "$(jq -nc --arg a "$LOAD" '{agent_id:$a}')" >/dev/null
  LOAD_ROOMS+=("$r")
done
mkdir -p "$ROOM_WORK_DIR"
LOAD_START="$(now_ms)"
for i in $(seq 1 "$N_LOAD"); do
  r="${LOAD_ROOMS[$((i-1))]}"
  w="$(api_ok POST "/rooms/$r/works" "$(jq -nc --arg t "perf load $i" --arg g "부하 $i" --arg a "$LOAD" \
        '{title:$t,goal:$g,assignee_agent_id:$a,completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
  [ -n "$w" ] && [ "$w" != null ] || bad "부하 미션 $i 생성 실패"
  printf '%s' "$w" > "$ROOM_WORK_DIR/$r"
  SESS+=("$r")
done
CREATE_MS=$(( $(now_ms)-LOAD_START ))
ok "세션 $N_LOAD 개 생성 ${CREATE_MS}ms"
# 부하 중 API 지연 + DB 커넥션 샘플링
: > "$OUT/76-api.txt"; DBMAX="$DB0"
IDS="$(printf '%s\n' "${SESS[@]}")"
for _ in $(seq 1 40); do
  sid="$(printf '%s\n' "$IDS" | awk -v n="$((RANDOM % N_LOAD + 1))" 'NR==n')"
  curl -sS -o /dev/null -w '%{time_total}\n' -b "$COOKIE" "$API/rooms/$sid" >> "$OUT/76-api.txt"
  c="$(db_conns)"; [ "$c" -gt "$DBMAX" ] && DBMAX="$c"
  ACT="$(psqlq "select count(*) from task t join room s on s.id=t.session_id join work wk on wk.room_id=s.id where s.workspace_id='$WS' and wk.title like 'perf load%' and t.status in ('queued','dispatched','preparing','running')")"
  [ "$ACT" = 0 ] && break
  sleep 0.5
done
wait_until 300 '[ "$(psqlq "select count(*) from task t join room s on s.id=t.session_id join work wk on wk.room_id=s.id where s.workspace_id='"'$WS'"' and wk.title like '"'perf load%'"' and t.status in ('"'queued'"','"'dispatched'"','"'preparing'"','"'running'"')")" = 0 ]' || bad "부하 task 가 다 끝나지 않았다"
LOAD_ELAPSED=$(( ($(now_ms)-LOAD_START)/1000 ))
psqlq "with t as (select t.id, t.attempt, t.status::text st, s.runtime_id, t.created_at, t.dispatched_at, t.started_at, t.finished_at
                  from task t join room s on s.id=t.session_id join work wk on wk.room_id=s.id where s.workspace_id='$WS' and wk.title like 'perf load%')
       select id, st, attempt, coalesce(runtime_id::text,'-'),
              round(extract(epoch from (dispatched_at - created_at))::numeric,3) claim_s,
              round(extract(epoch from (started_at - created_at))::numeric,3) start_s,
              round(extract(epoch from (finished_at - created_at))::numeric,3) finish_s
       from t order by created_at" > "$OUT/76-load.tsv"
LC_P50="$(cut -f5 "$OUT/76-load.tsv" | pct 50)"; LC_P95="$(cut -f5 "$OUT/76-load.tsv" | pct 95)"
LF_P50="$(cut -f7 "$OUT/76-load.tsv" | pct 50)"; LF_P95="$(cut -f7 "$OUT/76-load.tsv" | pct 95)"
API_P50="$(pct 50 < "$OUT/76-api.txt")"; API_P95="$(pct 95 < "$OUT/76-api.txt")"
MAX_RUN="$(psqlq "with iv as (select t.started_at s, coalesce(t.finished_at, now()) e from task t join room s on s.id=t.session_id join work wk on wk.room_id=s.id
                   where s.workspace_id='$WS' and wk.title like 'perf load%' and t.started_at is not null),
                  pts as (select s ts, 1 d from iv union all select e, -1 from iv)
                  select coalesce(max(run),0) from (select sum(d) over (order by ts, d desc rows unbounded preceding) run from pts) x")"
RT_USED="$(psqlq "select count(distinct s.runtime_id) from room s join work wk on wk.room_id=s.id where s.workspace_id='$WS' and wk.title like 'perf load%' and s.runtime_id is not null")"
REQUEUE="$(psqlq "select count(*) from task_attempt ta join task t on t.id=ta.task_id join room s on s.id=t.session_id join work wk on wk.room_id=s.id where s.workspace_id='$WS' and wk.title like 'perf load%' and ta.attempt > 1")"
DUP_POST="$(psqlq "select count(*) from (select m.source_task_id, count(*) c from message m join room s on s.id=m.session_id join work wk on wk.room_id=s.id where s.workspace_id='$WS' and wk.title like 'perf load%' and m.author_type='agent' group by 1 having count(*) > 1) d")"
COMPLETED="$(psqlq "select count(*) from task t join room s on s.id=t.session_id join work wk on wk.room_id=s.id where s.workspace_id='$WS' and wk.title like 'perf load%' and t.status='completed'")"
chk P0 "부하 task $N_LOAD 건 전부 completed" "$N_LOAD" "$COMPLETED"
chk_ge P1 "동시 running 최대 ≥ 슬롯의 80% (워크스페이스당 50, §9)" "$(( (N_LOAD < N_DAEMONS*CAP ? N_LOAD : N_DAEMONS*CAP) * 8 / 10 ))" "$MAX_RUN"
chk_ge P1b "런타임 $N_DAEMONS 대에 분산됐다" "$(( N_DAEMONS > 1 ? 2 : 1 ))" "$RT_USED"
chk P2 "재큐잉 0 (attempt > 1 없음)" 0 "$REQUEUE"
chk P3 "이중 게시 0 (task 당 에이전트 메시지 1)" 0 "$DUP_POST"
chk_lt P4 "부하 중 claim p50 < 2s" 2 "$LC_P50"
log "부하: claim p50=$LC_P50 p95=$LC_P95 · 완료 p50=$LF_P50 p95=$LF_P95 · API GET p50=${API_P50}s p95=${API_P95}s · DB conns max=$DBMAX (idle $DB0) · 동시 running max=$MAX_RUN · ${LOAD_ELAPSED}s"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg mode "$RUNTIME" --argjson n_lat "$N_LAT" --argjson n_load "$N_LOAD" --argjson daemons "$N_DAEMONS" --argjson cap "$CAP" \
  --arg c50 "$CLAIM_P50" --arg c95 "$CLAIM_P95" --arg o50 "$OUT_P50" --arg o95 "$OUT_P95" --arg r50 "$REPLY_P50" \
  --arg lc50 "$LC_P50" --arg lc95 "$LC_P95" --arg lf50 "$LF_P50" --arg lf95 "$LF_P95" --arg a50 "$API_P50" --arg a95 "$API_P95" \
  --argjson dbmax "$DBMAX" --argjson db0 "$DB0" --argjson maxrun "$MAX_RUN" --argjson rt "$RT_USED" --argjson requeue "$REQUEUE" --argjson dup "$DUP_POST" \
  --argjson lat_s "$LAT_ELAPSED" --argjson load_s "$LOAD_ELAPSED" --argjson pass "$pass" --argjson fail "$fail" \
  '{runtime_mode:$mode,
    latency:{n:$n_lat,claim_p50_s:$c50,claim_p95_s:$c95,first_output_p50_s:$o50,first_output_p95_s:$o95,reply_p50_s:$r50,elapsed_s:$lat_s},
    load:{sessions:$n_load,daemons:$daemons,capacity:$cap,claim_p50_s:$lc50,claim_p95_s:$lc95,finish_p50_s:$lf50,finish_p95_s:$lf95,
          api_get_session_p50_s:$a50,api_get_session_p95_s:$a95,db_conns_max:$dbmax,db_conns_idle:$db0,max_concurrent_running:$maxrun,
          runtimes_used:$rt,requeues:$requeue,double_posts:$dup,elapsed_s:$load_s},
    pass:$pass,fail:$fail}' | tee "$OUT/76.json"
[ "$fail" = 0 ]
