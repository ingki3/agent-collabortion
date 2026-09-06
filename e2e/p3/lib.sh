#!/usr/bin/env bash
# e2e/p3/lib.sh — P3 통합 E2E 공통 헬퍼 (T-I3, G6 판정 자료).
#
# `e2e/p2/lib.sh`(→ `e2e/p1/lib.sh`) 를 그대로 재사용하고 **포트·컨테이너·workdir 만 분리**한다.
# 같은 머신에 P1(:8080/:3000/:5435) · P2(:8090/:3010/:5436) · G5(:5437) · 스파이크 4c(:8095/:5441)
# 스택이 동시에 떠 있을 수 있다(P3_TASKS §0-13). 덮어쓰려면 미리 export 한다.
export SERVER_URL="${SERVER_URL:-http://localhost:8100}"
export WEB_URL="${WEB_URL:-http://localhost:3020}"
export PG_PORT="${PG_PORT:-5442}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-g6}"
P3_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P3_DIR/out}"
mkdir -p "$E2E_OUT"
source "$P3_DIR/../p2/lib.sh"

# 과제는 **저장소 밖의 무해한 주제**로 한다(G3_DECISION §2 X-2 — goal 에 이 저장소의 파일·스크립트
# 이름을 쓰면 에이전트가 그것을 찾아 스스로 실행한다).
P3_GOAL="${P3_GOAL:-가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명 초안을 정리한다}"

# create_session_p3 WS TITLE GOAL ASSIGNEE RUNTIME EXTRA_JSON PARTICIPANT_IDS... → session id
# 종료 조건은 `manual` 이다 — T-I3 이 재는 것은 HITL·재개·예산·취소이지 종료 조건이 아니고,
# `artifact_submitted AND user_approval` 을 켜면 매 세션이 승인 HITL 을 하나 더 만들어
# "task 당 열린 HITL 1개"·인박스 수치가 흐려진다. (g) 만 33_ 로 그 경로를 따로 잰다.
# EXTRA_JSON 은 session create 본문에 병합된다(예: limits).
create_session_p3() {
  local ws="$1" title="$2" goal="$3" assignee="$4" rt="$5" extra="${6:-{\}}"; shift 6
  local parts; parts="$(printf '%s\n' "$@" | jq -R . | jq -sc 'map({agent_id:.})')"
  api_ok POST "/workspaces/$ws/sessions" "$(jq -nc --arg t "$title" --arg g "$goal" --arg a "$assignee" --arg rt "$rt" \
      --argjson p "$parts" --argjson x "$extra" \
    '{title:$t,goal:$g,isolation:{kind:"none"},participants:$p,assignee_agent_id:$a,
      completion_condition:{op:"and",conditions:[{type:"manual"}]}}
     + (if $rt=="" then {} else {runtime_id:$rt} end) + $x')" | jq -r .id
}
# daemon_pair_cap CODE CONFIG WORKROOT CAPACITY [--no-turn] — 페어링 뒤 capacity 를 박는다.
# capacity 는 §0 의 "동시 실행 슬롯"이다. 41_ 은 **1** 로 두어 "waiting_human 이 슬롯을 잡지 않는다"를
# 반증 가능하게 만든다 — 슬롯을 잡고 있으면 다른 lane 이 영원히 queued 로 남는다.
daemon_pair_cap() {
  local code="$1" cfg="$2" root="$3" cap="$4"; shift 4
  rm -f "$cfg"; mkdir -p "$root"
  COLAB_DAEMON_CONFIG="$cfg" "$BIN/daemon" pair "$code" --server "${PAIR_SERVER:-$SERVER_URL}" --workdir-root "$root" "$@" 2>&1 | tail -2 >&2
  jq --argjson c "$cap" '.capacity=$c' "$cfg" > "$cfg.tmp" && mv "$cfg.tmp" "$cfg"
}

# ── HITL 관측 (전부 서버 DB 단일 클럭) ────────────────────────────────────────
# hitl_rows SESSION → id  source  type  purpose  status  approver_spec  task_id  created_at  due_at
hitl_rows() { psqlq "select id, source::text, type::text, coalesce(purpose,'-'), status::text,
                            approver_spec, coalesce(task_id::text,'-'), created_at, due_at
                     from hitl_request where session_id='$1' order by created_at"; }
# hitl_open SESSION [PURPOSE] → 가장 최근 open HITL id
hitl_open() {
  local extra=""; [ -n "${2:-}" ] && extra=" and purpose='$2'"
  psqlq "select id from hitl_request where session_id='$1' and status='open'$extra order by created_at desc limit 1"
}
# hitl_of_task TASK → 그 task 의 가장 최근 HITL id
hitl_of_task() { psqlq "select id from hitl_request where task_id='$1' order by created_at desc limit 1"; }
# hitl_field ID COLUMN
hitl_field() { psqlq "select coalesce(($2)::text,'-') from hitl_request where id='$1'"; }
# wait_hitl SESSION [TIMEOUT_S] [PURPOSE] → open HITL id (없으면 빈 문자열)
wait_hitl() {
  local s="$1" dl=$(( $(date +%s) + ${2:-300} )) h=""
  while [ "$(date +%s)" -lt "$dl" ]; do h="$(hitl_open "$s" "${3:-}")"; [ -n "$h" ] && break; sleep 3; done
  printf '%s' "$h"
}
# respond_hitl ID JSON → "HTTP코드<TAB>본문"
respond_hitl() {
  local out; out="$(api POST "/hitl-requests/$1/response" "$2" -H "Idempotency-Key: $(uuid)")"
  printf '%s\t%s' "$(api_code <<<"$out")" "$(api_body <<<"$out" | tr -d '\n')"
}
# backdate_hitl ID SECONDS — created_at·due_at 를 같은 만큼 과거로 민다.
# **왜 이렇게 재나**: 서버 바이너리에 클럭 주입 경로가 없다(`clock.Real{}` 고정, server/cmd/server/main.go).
# `hitl.Authorize` 는 `Elapsed = now - created_at` 과 `DueIn = due_at - created_at` 만 본다 — 둘을 같이
# 밀면 기한 길이(24h)는 보존한 채 경과 시간만 바꿀 수 있다. 보고서에 방법을 명시한다(§0-11 우회 표기).
backdate_hitl() { psqlq "update hitl_request set created_at = created_at - interval '$2 seconds',
                                                 due_at    = due_at    - interval '$2 seconds'
                         where id='$1'" >/dev/null; }

# in_set VALUE ITEM... → 값이 목록에 있으면 yes, 아니면 **그 값 자체**(표에 무엇이었는지 남는다).
# `chk` 안에서 `case` 를 쓰면 bash 3.2 가 명령 치환 안의 `pattern)` 을 파싱하지 못한다(실측).
in_set() { local v="$1"; shift; local x; for x in "$@"; do [ "$v" = "$x" ] && { printf yes; return; }; done; printf '%s' "$v"; }

# ── task · lane · attempt ────────────────────────────────────────────────────
# task_row TASK → status  attempt  paused_reason  pending_hitl  budget_override  restarted_from  lane
task_row() { psqlq "select status::text, attempt, coalesce(paused_reason::text,'-'), pending_hitl::text,
                           coalesce(budget_override::text,'-'), coalesce(restarted_from_task_id::text,'-'), lane_id
                    from task where id='$1'"; }
task_field() { psqlq "select coalesce(($2)::text,'-') from task where id='$1'"; }
lane_field() { psqlq "select coalesce(($2)::text,'-') from lane where id='$1'"; }
# wait_task_status TASK STATUS... — p1 의 wait_task 와 같으나 조용하다
# latest_task SESSION [AGENT] → 가장 최근 task id
latest_task() {
  if [ -n "${2:-}" ]; then psqlq "select t.id from task t join agent a on a.id=t.agent_id
                                  where t.session_id='$1' and a.name='$2' order by t.created_at desc limit 1"
  else psqlq "select id from task where session_id='$1' order by created_at desc limit 1"; fi
}
# task_count SESSION → task 수
task_count() { psqlq "select count(*) from task where session_id='$1'"; }
# attempt_rows TASK → attempt  outcome  failure_kind  resumed  started_at  finished_at
attempt_rows() { psqlq "select attempt, coalesce(outcome,'-'), coalesce(failure_kind::text,'-'),
                               coalesce(resumed::text,'-'), started_at, coalesce(finished_at::text,'-')
                        from task_attempt where task_id='$1' order by attempt"; }
# events_of TASK [CLASS] → ts  attempt  class  verb  tool  payload
events_of() {
  local extra=""; [ -n "${2:-}" ] && extra=" and class='$2'"
  psqlq "select coalesce(ts,created_at), attempt, class::text, coalesce(verb,'-'), coalesce(tool,'-'),
                left(replace(coalesce(payload::text,''),E'\n','⏎'),160)
         from task_event where task_id='$1'$extra order by attempt, seq"
}
# procs_of_attempt WORKROOT TASK ATTEMPT → 그 attempt 의 pgid 로 남아 있는 프로세스 수 (E7-03 "프로세스 없음")
procs_of_attempt() {
  local g; g="$(pgid_of_attempt "$1" "$2" "$3")"
  { [ -n "$g" ] && [ "$g" != null ] && pg_procs "$g" | wc -l | tr -d ' '; } || echo 0
}

# ── 턴 프롬프트 (claim 탭) ───────────────────────────────────────────────────
# tap_start PORT TAPFILE → pid. 서버가 데몬에 주는 TaskBundle 을 기록한다 — 턴 프롬프트는
# 디스크에 남지 않으므로 이것 없이는 "답변이 프롬프트에 들어갔는가"를 증명할 수 없다(E1-21 과 같은 이유).
tap_start() {
  : > "$2"
  python3 "$P3_DIR/../p2/fixtures/claimtap.py" "$1" "$SERVER_URL" "$2" >/dev/null 2>&1 &
  local pid=$!
  local i; for i in $(seq 1 30); do curl -fsS -o /dev/null "http://localhost:$1/healthz" 2>/dev/null && break; sleep 0.3; done
  printf '%s' "$pid"
}
# tap_prompt TAPFILE TASK [ATTEMPT] → 그 task(·attempt)의 턴 프롬프트 전문
# (p2 의 prompt_of_task.py 는 attempt 를 가리지 않아 attempt 1·2 가 섞인다 — 재개를 재는 T-I3 에서는
#  그러면 "답변이 프롬프트에 들어갔다"가 항상 참이 된다. 그래서 attempt 를 받는 픽스처를 따로 둔다.)
tap_prompt() { python3 "$P3_DIR/fixtures/prompt_of.py" "$1" "$2" ${3:+"$3"}; }
# tap_brief TAPFILE TASK [ATTEMPT] → 브리프 [1]~[8] 전문 (결정 기록 [7] 을 잰다)
tap_brief() { python3 "$P3_DIR/fixtures/brief_of.py" "$1" "$2" ${3:+"$3"}; }

# ── 예산 ─────────────────────────────────────────────────────────────────────
# set_agent_budget AGENT USD — 에이전트 budget_per_task. openapi 에 P3 op 가 없어(계약에 없는 칸이 아니라
# **경로가 없다**) DB 로 넣는다. 판정 대상이 아니라 **자극**이므로 우회로 세지 않는다.
set_agent_budget() { psqlq "update agent set budget_per_task=$2 where id='$1'" >/dev/null; }
agent_budget() { psqlq "select coalesce(budget_per_task::text,'-') from agent where id='$1'"; }
# task_usage TASK → cost_usd  estimated
task_usage() { psqlq "select coalesce(cost_usd::text,'0'), coalesce(estimated::text,'-') from task_usage where task_id='$1'"; }
# session_limits SESSION → limits jsonb
session_limits() { psqlq "select coalesce(limits::text,'{}') from session where id='$1'"; }

# ── 인박스 ───────────────────────────────────────────────────────────────────
inbox_rows() { psqlq "select type::text, severity::text, coalesce(ref_id::text,'-'), coalesce(read_at::text,'-')
                      from inbox_item where session_id='$1' order by created_at"; }
# decisions_of SESSION → source  summary  rationale
decisions_of() { psqlq "select coalesce(source::text,'-'), left(replace(summary,E'\n','⏎'),80), left(replace(coalesce(rationale,''),E'\n','⏎'),80)
                        from decision where session_id='$1' order by created_at"; }

# ── 웹 (agent-browser) ───────────────────────────────────────────────────────
# §0-9: 화면 판정은 DOM 으로 한다. 아래 3개는 11_scenario_a_web.sh 의 관례를 그대로 옮긴 것이다.
ab()     { agent-browser "$@"; }
abget()  { agent-browser "$@" 2>/dev/null || true; }
abwait() { agent-browser wait "$1" --timeout "$(( ${2:-25} * 1000 ))" >/dev/null 2>&1; }
abcount(){ local n; n="$(abget get count "$1")"; echo "${n:-0}"; }
# web_login EMAIL PASSWORD — 로그인해서 /sessions 까지
web_login() {
  ab open "$WEB_URL/login" >/dev/null
  abwait '[data-testid="login-form"]' || return 1
  ab fill 'input[name=email]' "$1" >/dev/null
  ab fill 'input[name=password]' "$2" >/dev/null
  ab click 'button[type=submit]' >/dev/null
  agent-browser wait --url "**/sessions" --timeout 20000 >/dev/null 2>&1 || true
}
shot() { ab screenshot "$E2E_ROOT/web/__screenshots__/$1.png" >/dev/null 2>&1; log "📸 $1.png"; }
