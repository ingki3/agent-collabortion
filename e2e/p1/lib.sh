#!/usr/bin/env bash
# e2e/p1/lib.sh — P1 통합 E2E 공통 헬퍼 (T-I1). 실제 런타임(Claude Code + ACP 어댑터) 전제.
# 서버 :8080, 웹 :3000, Postgres = docker colab-pg (호스트 PG_PORT, 기본 5433).
# 모든 API 호출은 contracts/openapi.yaml 그대로 curl 로 한다. 시각 측정은 서버 DB 의 timestamptz 를 쓴다(단일 클럭).
set -euo pipefail

E2E_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SERVER_URL="${SERVER_URL:-http://localhost:8080}"
API="$SERVER_URL/api/v1"
WEB_URL="${WEB_URL:-http://localhost:3000}"
# E2E 전용 Postgres 컨테이너. 공용 colab-pg(5433) 는 다른 세션의 `go test`(db 패키지 DROP SCHEMA) 가 스키마를 날릴 수 있어
# (2026-09-06 06:32 실제로 20회차 도중 발생) 격리한다. 필요하면 PG_PORT/PG_CONTAINER 로 덮어쓴다.
PG_PORT="${PG_PORT:-5435}"
PG_CONTAINER="${PG_CONTAINER:-colab-pg-e2e}"
OUT="${E2E_OUT:-$E2E_ROOT/e2e/p1/out}"
mkdir -p "$OUT"
COOKIE="${COOKIE:-$OUT/cookies.txt}"
BIN="$E2E_ROOT/bin"
LEAD_MODEL="${LEAD_MODEL:-claude-haiku-4-5-20251001}"   # 비용: 에이전트 턴은 haiku

log()  { printf '%s %s\n' "$(date +%H:%M:%S)" "$*" >&2; }
step() { printf '\n▶ %s\n' "$*" >&2; }
ok()   { printf '  ✓ %s\n' "$*" >&2; }
bad()  { printf '  ✗ %s\n' "$*" >&2; }
die()  { bad "$*"; exit 1; }
now_ms() { python3 -c 'import time; print(int(time.time()*1000))'; }

# psqlq "<sql>" → 탭 구분 행
psqlq() { docker exec -i "$PG_CONTAINER" psql -U colab -d colab -tA -F $'\t' -c "$1"; }

# api METHOD PATH [JSON] [extra curl args...] → stdout: body, 마지막 줄에 HTTP 코드
api() {
  local method="$1" path="$2" body="${3:-}"; shift 2; [ $# -gt 0 ] && shift
  if [ -n "$body" ]; then
    curl -sS -w '\n%{http_code}' -b "$COOKIE" -c "$COOKIE" -H 'Content-Type: application/json' -X "$method" "$API$path" --data "$body" "$@"
  else
    curl -sS -w '\n%{http_code}' -b "$COOKIE" -c "$COOKIE" -X "$method" "$API$path" "$@"
  fi
}
# api_body → 마지막 줄(코드) 제거
api_body() { sed '$d'; }
api_code() { tail -n1; }
# api_ok METHOD PATH [JSON] — 2xx 가 아니면 실패
api_ok() {
  local out code; out="$(api "$@")"; code="$(api_code <<<"$out")"
  case "$code" in 2*) api_body <<<"$out";; *) bad "$1 $2 → HTTP $code: $(api_body <<<"$out")"; return 1;; esac
}
uuid() { python3 -c 'import uuid; print(uuid.uuid4())'; }

# ── 계정·워크스페이스 ──
signup() { # email password name → cookie 저장, user id 출력
  api_ok POST /auth/signup "$(jq -nc --arg e "$1" --arg p "$2" --arg n "$3" '{email:$e,password:$p,display_name:$n}')" | jq -r '.user.id // .id'
}
login() { api_ok POST /auth/login "$(jq -nc --arg e "$1" --arg p "$2" '{email:$e,password:$p}')" >/dev/null; }
create_workspace() { api_ok POST /workspaces "$(jq -nc --arg n "$1" '{name:$n}')" | jq -r .id; }

# ── 페어링·데몬 ──
create_pairing() { # ws → "pairing_id<TAB>pairing_token"
  api_ok POST "/workspaces/$1/runtimes/pairings" '{"name":"e2e"}' | jq -r '[.id,.pairing_token]|@tsv'
}
pairing_status() { api GET "/workspaces/$1/runtimes/pairings/$2" | api_body | jq -r .status; }
# daemon_pair CODE CONFIG WORKROOT [--no-turn]
daemon_pair() {
  local code="$1" cfg="$2" root="$3"; shift 3
  COLAB_DAEMON_CONFIG="$cfg" "$BIN/daemon" pair "$code" --server "$SERVER_URL" --workdir-root "$root" "$@" 2>&1 | tee -a "$OUT/daemon-pair.log" >&2
}
# colab_tap CONFIG → daemon.json 의 colab_bin 을 토큰 기록 래퍼로 바꾼다(테스트 픽스처: E11-04 의 폐기 토큰을 얻기 위해)
colab_tap() {
  local cfg="$1" tap="$OUT/colab-tap.sh"
  cat > "$tap" <<EOS
#!/usr/bin/env bash
# 테스트 픽스처 — 어댑터가 띄우는 colab MCP 서버의 COLAB_* 를 기록한 뒤 진짜 colab 으로 exec
printf '%s\t%s\t%s\t%s\n' "\$(date +%s)" "\${COLAB_TASK_ID:-}" "\${COLAB_TASK_ATTEMPT:-}" "\${COLAB_TASK_TOKEN:-}" >> "$OUT/colab-tap.log"
exec "$BIN/colab" "\$@"
EOS
  chmod +x "$tap"
  jq --arg b "$tap" '.colab_bin=$b' "$cfg" > "$cfg.tmp" && mv "$cfg.tmp" "$cfg"
}
# daemon_start CONFIG LOGFILE → pid (bin/daemon run --no-turn, 자기 프로세스 그룹)
daemon_start() {
  local cfg="$1" logf="$2"
  COLAB_DAEMON_CONFIG="$cfg" setsid_run "$logf" "$BIN/daemon" run --no-turn
}
# setsid_run LOG CMD... → 새 세션(pgid) 으로 백그라운드 실행, pid 출력 (macOS 에 setsid 없음 → python)
setsid_run() {
  local logf="$1"; shift
  python3 - "$logf" "$@" <<'PY'
import os, sys, subprocess
logf, cmd = sys.argv[1], sys.argv[2:]
f = open(logf, "ab")
p = subprocess.Popen(cmd, stdout=f, stderr=subprocess.STDOUT, stdin=subprocess.DEVNULL, start_new_session=True, env=os.environ)
print(p.pid)
PY
}
wait_pairing() { # ws pairing_id timeout_s
  local i; for ((i=0;i<$3;i++)); do [ "$(pairing_status "$1" "$2")" = ready ] && return 0; sleep 1; done; return 1
}

# ── 에이전트·세션 ──
mention() { printf '[@%s](mention://agent/%s)' "$1" "$2"; }
create_agent() { # ws name model → agent id
  api_ok POST "/workspaces/$1/agents" "$(jq -nc --arg n "$2" --arg m "$3" '{name:$n,role:"lead",role_description:"팀을 이끌고 위임·종합한다",instructions:"You are the lead. Reply briefly in Korean. Always post replies with the colab_message_post MCP tool.",profiles:[{name:"default",runtime_kind:"claude_code",model:$m,is_default:true}]}')" | jq -r .id
}
# ── 방·미션 (openapi v0.3.0 D22 — 옛 /sessions/* 삭제) ─────────────────────────
# 옛 createSession 한 번 = createRoom → updateRoom(격리·컴퓨터·한도·autonomy) → addRoomParticipant… → createWork.
# 옛 세션은 방에 legacy_work_id 가 있어 work_id 없는 사람 게시도 그 미션에 귀속됐다. createRoom 방은 "미션 없음"이라
# 여기서 만든 방의 미션 id 를 $ROOM_WORK_DIR/<room> 에 적어 두고 post_message 가 work_id 를 붙인다(옛 동작 흉내).
ROOM_WORK_DIR="$OUT/.room-work"
# work_of ROOM → 그 방의 미션 id (이 스크립트가 만든 방이면 그 미션, 아니면 가장 최근 미션)
work_of() {
  if [ -f "$ROOM_WORK_DIR/$1" ]; then cat "$ROOM_WORK_DIR/$1"
  else psqlq "select id from work where room_id='$1' order by created_at desc limit 1"; fi
}
# create_room_work_api WS OLD_SESSION_CREATE_JSON → api 모양(본문 + 마지막 줄 HTTP 코드).
#   성공: 201 과 {id: 방 id, room_id, work_id, work: Work}. 실패: 처음 실패한 단계의 Problem 과 코드.
#   옛 SessionCreate 칸 → 새 자리: title·goal·assignee_agent_id(비우면 첫 참여자 — createWork 는 기본값이 없다)·
#   completion_condition·acceptance_criteria·draft·director_user_id·deputy_director_user_id→deputy_user_id 는 미션,
#   isolation·runtime_id·autonomy·limits(budget_usd·time_limit·max_parallel_lanes·max_concurrent_works) 는 방,
#   limits(budget_tokens·max_tasks) 는 미션 한도. participants[] 는 addRoomParticipant 하나씩.
create_room_work_api() {
  local ws="$1" in="$2" out code room patch p work wid
  out="$(api POST "/workspaces/$ws/rooms" "$(jq -c '{name:(.title // (.goal|split("\n")[0]) // "room"), description:""}' <<<"$in")")"
  code="$(api_code <<<"$out")"; case "$code" in 2*) ;; *) printf '%s\n' "$out"; return 0;; esac
  room="$(api_body <<<"$out" | jq -r .id)"
  patch="$(jq -c '(if .isolation then {isolation:.isolation} else {} end)
     + (if (.runtime_id // "") != "" then {runtime_id:.runtime_id} else {} end)
     + (if .autonomy then {autonomy:.autonomy} else {} end)
     + (if .visibility then {visibility:.visibility} else {} end)
     + (if .limits then ((.limits|with_entries(select(.key|IN("budget_usd","time_limit","max_parallel_lanes","max_concurrent_works"))))
         | if length>0 then {limits:.} else {} end) else {} end)' <<<"$in")"
  if [ "$patch" != "{}" ]; then
    out="$(api PATCH "/rooms/$room" "$patch")"; code="$(api_code <<<"$out")"
    case "$code" in 2*) ;; *) printf '%s\n' "$out"; return 0;; esac
  fi
  while read -r p; do
    [ -n "$p" ] || continue
    out="$(api POST "/rooms/$room/participants" "$p")"; code="$(api_code <<<"$out")"
    case "$code" in 2*) ;; *) printf '%s\n' "$out"; return 0;; esac
  done <<<"$(jq -c '.participants[]? | {agent_id} + (if (.profile_id // "") != "" then {profile_id} else {} end)' <<<"$in")"
  work="$(jq -c '{goal, title, assignee_agent_id:(.assignee_agent_id // .participants[0].agent_id)}
     + (if .completion_condition then {completion_condition} else {} end)
     + (if .acceptance_criteria then {acceptance_criteria} else {} end)
     + (if .draft then {draft} else {} end)
     + (if .director_user_id then {director_user_id} else {} end)
     + (if .deputy_director_user_id then {deputy_user_id:.deputy_director_user_id} else {} end)
     + (if .limits then ((.limits|with_entries(select(.key|IN("budget_tokens","max_tasks"))))
         | if length>0 then {limits:.} else {} end) else {} end)
     | with_entries(select(.value != null))' <<<"$in")"
  out="$(api POST "/rooms/$room/works" "$work")"; code="$(api_code <<<"$out")"
  case "$code" in 2*) ;; *) printf '%s\n' "$out"; return 0;; esac
  wid="$(api_body <<<"$out" | jq -r .id)"
  mkdir -p "$ROOM_WORK_DIR"; printf '%s' "$wid" > "$ROOM_WORK_DIR/$room"
  jq -c --arg r "$room" '{id:$r, room_id:$r, work_id:.id, work:.}' <<<"$(api_body <<<"$out")"
  printf '%s\n' "$code"
}
# create_room_work WS OLD_SESSION_CREATE_JSON → 방 id (옛 createSession 의 세션 id 자리). 2xx 가 아니면 실패.
create_room_work() {
  local out code; out="$(create_room_work_api "$@")"; code="$(api_code <<<"$out")"
  case "$code" in 2*) api_body <<<"$out" | jq -r .id;; *) bad "createRoom/createWork → HTTP $code: $(api_body <<<"$out")"; return 1;; esac
}
create_session() { # ws agent title goal [runtime_id] → 방 id (초기 task 가 assignee 에게 생긴다)
  # runtime_id 를 주면 그 런타임에 고정. 비우면 "자동 선택(첫 claim 런타임 고정)" — 서버 결함(claim 이 워크스페이스를 보지 않음)으로
  # 다른 워크스페이스의 데몬이 가져갈 수 있어(2026-09-06 실측) 시나리오 b·c 는 명시한다.
  local rt="${5:-}"
  create_room_work "$1" "$(jq -nc --arg a "$2" --arg t "$3" --arg g "$4" --arg rt "$rt" '{title:$t,goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a} + (if $rt=="" then {} else {runtime_id:$rt} end)')"
}
# with_work ROOM JSON → JSON 에 work_id 를 붙인다(이 스크립트가 옛 세션 자리로 만든 방일 때만 — legacy_work_id 흉내)
with_work() {
  if [ -f "$ROOM_WORK_DIR/$1" ] && ! jq -e 'has("work_id")' >/dev/null <<<"$2"; then jq -c --arg w "$(cat "$ROOM_WORK_DIR/$1")" '. + {work_id:$w}' <<<"$2"; else printf '%s' "$2"; fi
}
# post_message ROOM CONTENT → 응답 JSON (MessagePostResult)
post_message() {
  api_ok POST "/rooms/$1/messages" "$(with_work "$1" "$(jq -nc --arg c "$2" '{content:$c}')")" -H "Idempotency-Key: $(uuid)"
}
task_status() { psqlq "select status from task where id='$1'"; }
task_attempt() { psqlq "select attempt from task where id='$1'"; }
# wait_task TASK STATUS... (timeout via WAIT_S, 기본 600)
wait_task() {
  local task="$1"; shift; local want=" $* " s i
  for ((i=0;i<${WAIT_S:-600};i++)); do s="$(task_status "$task")"; case "$want" in *" $s "*) echo "$s"; return 0;; esac; sleep 1; done
  echo "timeout($s)"; return 1
}
# wait_task_attempt TASK ATTEMPT
wait_task_attempt() {
  local i; for ((i=0;i<${WAIT_S:-600};i++)); do [ "$(task_attempt "$1")" -ge "$2" ] 2>/dev/null && return 0; sleep 1; done; return 1
}
# session_initial_task SESSION → 세션 생성 시 만들어진 첫 task id
session_initial_task() { psqlq "select id from task where session_id='$1' order by created_at limit 1"; }
# reply_count SESSION TASK → 그 task 가 게시한 메시지 수
reply_count() { psqlq "select count(*) from message where session_id='$1' and source_task_id='$2'"; }
# pgid_of_attempt WORKROOT TASK ATTEMPT → 데몬이 기록한 pgid (E11-01)
pgid_of_attempt() { jq -r .pgid "$1/.colab/attempts/$2.$3.json" 2>/dev/null || true; }
pg_alive() { kill -0 -- "-$1" 2>/dev/null; }
pg_procs() { ps -o pid=,pgid=,command= -ax | awk -v g="$1" '$2==g'; }
# latency_row TASK ATTEMPT → 게시(트리거 메시지)기준 지연(초): claim(dispatched_at), first_event(첫 task_event=runtime.start, 데몬 발행),
#   first_runtime_out(런타임이 낸 첫 tool/message/plan 이벤트 = '첫 출력'), first_say(턴 종료 시 합쳐지는 message.say), reply(첫 답글 메시지 도착)
latency_row() {
  psqlq "with t as (select t.id, t.attempt, m.created_at as posted, t.dispatched_at from task t join message m on m.id=t.trigger_message_id where t.id='$1')
  select round(extract(epoch from (t.dispatched_at - t.posted))::numeric,3) as claim_s,
         round(extract(epoch from ((select min(coalesce(e.ts,e.created_at)) from task_event e where e.task_id=t.id and e.attempt=$2 and e.seq < 1073741824) - t.posted))::numeric,3) as first_event_s,
         round(extract(epoch from ((select min(coalesce(e.ts,e.created_at)) from task_event e where e.task_id=t.id and e.attempt=$2 and e.seq < 1073741824 and e.class in ('tool','message','plan')) - t.posted))::numeric,3) as first_runtime_out_s,
         round(extract(epoch from ((select min(coalesce(e.ts,e.created_at)) from task_event e where e.task_id=t.id and e.attempt=$2 and e.class='message') - t.posted))::numeric,3) as first_say_s,
         round(extract(epoch from ((select min(r.created_at) from message r where r.source_task_id=t.id) - t.posted))::numeric,3) as reply_s
  from t"
}
median() { sort -n | awk '{a[NR]=$1} END{if(NR==0){print "NA";exit} if(NR%2){print a[(NR+1)/2]} else {printf "%.3f\n",(a[NR/2]+a[NR/2+1])/2}}'; }
