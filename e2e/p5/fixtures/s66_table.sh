#!/usr/bin/env bash
# e2e/p5/fixtures/s66_table.sh SESSION DAEMON_LOG [LABEL] — S-66 관측표 한 세션분.
#   attempt 별: 에이전트 · outcome · failure_kind · 턴 길이(s) · 데몬 로그의 stall 줄(있으면) ·
#   그 attempt 가 마지막으로 낸 task_event(class/verb·시각) · 아티팩트 바이트.
# 실기 로그(D-24)의 줄: "<task>.<attempt> stall fired … counted=…" / "turn outcome=…".
source "$(dirname "${BASH_SOURCE[0]}")/../lib_i5.sh"
S="$1"; DLOG="$2"; LABEL="${3:-}"
printf 'label\tagent\ttask.attempt\toutcome\tfailure_kind\tturn_s\tlast_event\tlast_event_at\tstall_line\n'
psqlq "select a.name, t.id, ta.attempt, coalesce(ta.outcome,'-'), coalesce(ta.failure_kind::text,'-'),
              coalesce(round(extract(epoch from (ta.finished_at - ta.started_at))::numeric,1)::text,'-'),
              coalesce((select e.class||'/'||coalesce(e.verb,'-') from task_event e where e.task_id=t.id and e.attempt=ta.attempt and e.seq < 1073741824 order by e.seq desc limit 1),'-'),
              coalesce((select to_char(coalesce(e.ts,e.created_at),'HH24:MI:SS') from task_event e where e.task_id=t.id and e.attempt=ta.attempt and e.seq < 1073741824 order by e.seq desc limit 1),'-')
       from task_attempt ta join task t on t.id=ta.task_id join agent a on a.id=t.agent_id
       where t.session_id='$S' order by ta.started_at" | while IFS=$'\t' read -r agent task attempt outcome kind turn_s ev ev_at; do
  stall="$({ grep -F "$task.$attempt" "$DLOG" 2>/dev/null | grep -i "stall" | tail -1 | cut -c1-160 || true; } | tr '\t' ' ')"
  printf '%s\t%s\t%s.%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$LABEL" "$agent" "${task:0:8}" "$attempt" "$outcome" "$kind" "$turn_s" "$ev" "$ev_at" "${stall:--}"
done
