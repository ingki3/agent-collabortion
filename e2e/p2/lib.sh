#!/usr/bin/env bash
# e2e/p2/lib.sh — P2 통합 E2E 공통 헬퍼 (T-I2, G4 판정 자료).
#
# e2e/p1/lib.sh 를 그대로 재사용하되 **포트와 컨테이너를 분리**한다 — P1 스택(8080/3000/5435)이
# 다른 워크스페이스에서 돌고 있을 수 있고, 두 서버가 같은 포트를 잡을 수 없기 때문이다.
# 덮어쓰려면 SERVER_URL·WEB_URL·PG_PORT·PG_CONTAINER 를 미리 export 한다.
export SERVER_URL="${SERVER_URL:-http://localhost:8090}"
export WEB_URL="${WEB_URL:-http://localhost:3010}"
export PG_PORT="${PG_PORT:-5436}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-g4}"
P2_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P2_DIR/out}"
source "$P2_DIR/../p1/lib.sh"

# 시나리오 A 의 과제는 **저장소 밖의 무해한 주제**로 한다.
# (P1 실측: goal 에 이 저장소의 스크립트 이름을 쓰면 에이전트가 그것을 찾아 스스로 실행해
#  세션이 재귀 생성되고 데몬이 죽는다 — G3_DECISION §2 X-2.)
SCENARIO_GOAL="${SCENARIO_GOAL:-가상의 스마트 물병 제품 X 의 시장 조사 요약 3항목을 정리한다}"

# ── P2 에이전트·세션 ──
# PROFILE_ENV — 프로파일 env(jsonb, FR-1.6: 사용자가 명시한 것만 더해진다). 기본은 비어 있다.
# (역사) 2026-09-06 첫 실행에서 데몬 환경 허용 목록에 `USER` 가 없어 만료된 Claude Code OAuth 를 갱신하지
# 못했다(`failure_kind=auth`). 그때는 `PROFILE_ENV='{"USER":"..."}'` 로 우회해 측정했다. PR #71(dev 9bb4ce9)
# 이 허용 목록에 USER 를 넣어 우회는 걷어냈다 — 되살릴 일이 있으면 이 변수로 넣는다.
PROFILE_ENV="${PROFILE_ENV:-$(printf '{}')}"
# create_agent_p2 WS NAME ROLE MODEL INSTRUCTIONS [ROLE_DESC] → agent id
create_agent_p2() {
  api_ok POST "/workspaces/$1/agents" "$(jq -nc --arg n "$2" --arg r "$3" --arg m "$4" --arg i "$5" --arg rd "${6:-$3 역할}" --argjson env "$PROFILE_ENV" \
    '{name:$n,role:$r,role_description:$rd,instructions:$i,profiles:[{name:"default",runtime_kind:"claude_code",model:$m,is_default:true,env:$env}]}')" | jq -r .id
}
# create_session_p2 WS TITLE GOAL ASSIGNEE RUNTIME PARTICIPANT_IDS... → session id
# 종료 조건은 스키마 기본값과 같은 `artifact_submitted(Writer) AND user_approval` 을 명시한다.
create_session_p2() {
  local ws="$1" title="$2" goal="$3" assignee="$4" rt="$5" writer="$6"; shift 6
  local parts; parts="$(printf '%s\n' "$@" | jq -R . | jq -sc 'map({agent_id:.})')"
  api_ok POST "/workspaces/$ws/sessions" "$(jq -nc --arg t "$title" --arg g "$goal" --arg a "$assignee" --arg rt "$rt" --arg w "$writer" --argjson p "$parts" \
    '{title:$t,goal:$g,isolation:{kind:"none"},participants:$p,assignee_agent_id:$a,
      completion_condition:{op:"and",conditions:[{type:"artifact_submitted",agent_id:$w},{type:"user_approval"}]}}
     + (if $rt=="" then {} else {runtime_id:$rt} end)')" | jq -r .id
}

# ── 관측 질의 (전부 서버 DB 단일 클럭) ──
# lanes_of SESSION → lane_id  agent  status  delegated_from_task  created_at
lanes_of() { psqlq "select l.id, a.name, l.status, coalesce(l.delegated_from_task_id::text,'-'), l.created_at
                    from lane l join agent a on a.id=l.agent_id where l.session_id='$1' order by l.created_at"; }
# tasks_of_agent SESSION AGENT_NAME → task rows
tasks_of_agent() { psqlq "select t.id, t.status, coalesce(t.delegated_from_task_id::text,'-'), t.created_at
                          from task t join agent a on a.id=t.agent_id
                          where t.session_id='$1' and a.name='$2' order by t.created_at"; }
# count_tasks_of_agent SESSION AGENT_NAME
count_tasks_of_agent() { psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$1' and a.name='$2'"; }
# running_overlap SESSION AGENT_NAME → 그 에이전트의 lane 들이 **동시에** running 이었던 최대 겹침 수
# task 의 started_at..finished_at 구간을 훑어 최대 동시 실행 수를 센다(스윕 라인).
running_overlap() {
  psqlq "with iv as (
           select t.started_at s, coalesce(t.finished_at, now()) e
           from task t join agent a on a.id=t.agent_id
           where t.session_id='$1' and a.name='$2' and t.started_at is not null),
         pts as (select s ts, 1 d from iv union all select e, -1 from iv)
         select coalesce(max(run),0) from (select sum(d) over (order by ts, d desc rows unbounded preceding) run from pts) x"
}
# join_fired SESSION → 발화된 합류(위임 task) 목록: task_id  join_fired_at  자식 lane 수
join_fired() {
  psqlq "select t.id, t.join_fired_at, (select count(*) from lane l where l.delegated_from_task_id=t.id)
         from task t where t.session_id='$1' and t.join_fired_at is not null order by t.join_fired_at"
}
# system_messages SESSION → author=system 메시지 (합류·통보 확인용)
system_messages() { psqlq "select id, left(replace(content,E'\n','⏎'),200) from message where session_id='$1' and author_type='system' order by created_at"; }
# completion_progress SESSION → 서버가 계산한 진행률 JSON
completion_progress() { api_ok GET "/sessions/$1" | jq -c '.completion_progress'; }

# daemon_start_p2 CONFIG LOGFILE → pid. p1 의 daemon_start 와 달리 **PONG 턴을 돈다**
# (`--no-turn` 이면 재시작 때 runtime.capabilities.models 가 빈 배열로 덮여 S9·S11 표시가 비어 보인다 — G3_REPORT §2).
daemon_start_p2() {
  local cfg="$1" logf="$2"
  COLAB_DAEMON_CONFIG="$cfg" setsid_run "$logf" "$BIN/daemon" run
}
