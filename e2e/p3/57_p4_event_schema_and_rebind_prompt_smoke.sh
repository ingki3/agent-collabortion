#!/usr/bin/env bash
# e2e/p3/57_p4_event_schema_and_rebind_prompt_smoke.sh — T-S9b 실서버 스모크.
#
# 세 가지를 실서버에서 잰다.
#   1. S-52 — 서버가 스스로 쓰는 `task_event` 도 닫힌 스키마를 지킨다
#      (PRD §7 v0.16). 취소·상태 변경·HITL·GC 거부를 실제로 일으킨 뒤, 서버가 쓴
#      행(seq ≥ 2^30) 전부를 SQL 로 스캔해 class 별 허용 키 밖의 payload 키·
#      enum 밖의 verb·outcome 이 0 인지 본다. 특정 행을 하나씩 세는 대신 전수로
#      재는 이유는 결함이 "13곳" 이었기 때문이다 — 하나 고치고 하나 놓치는 것을
#      막는 것은 전수 조건뿐이다.
#   2. S-53 · NN5 — 재바인딩 뒤 첫 턴 프롬프트가 에이전트에게 실제로 간다.
#      `{{COLAB_REBIND_DIR}}/manifest.json` 을 가리키고 `git apply` 를 시키며
#      `colab artifact get` 재다운로드는 없다. 재큐잉해도 남고, completed finish
#      뒤에는 사라진다.
#   3. E8-04 (4) — 재개 프롬프트의 `<resumed>` 가 "이미 게시한 메시지" 와
#      "workdir 을 먼저 보고 이미 있는 편집을 다시 하지 마라" 를 담는다.
#
# 전용 스택(§0-13, 56_ 와 같은 것을 재사용한다):
#   docker run -d --name colab-pg-s9 -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab \
#     -e POSTGRES_DB=colab -p 5448:5432 postgres:16-alpine
#   COLAB_DB_URL="postgres://colab:colab@localhost:5448/colab?sslmode=disable" \
#     COLAB_SERVER_ADDR=:8103 COLAB_SERVER_URL=http://127.0.0.1:8103 ./out/server-s9b
# 종료는 pid·포트로만 (§0-10).
#
# 사용: bash e2e/p3/57_p4_event_schema_and_rebind_prompt_smoke.sh
set -uo pipefail
RUNID=$(uuidgen | tr "[:upper:]" "[:lower:]" | cut -c1-8)
S=http://127.0.0.1:8103/api/v1
D=http://127.0.0.1:8103/v1/daemon
J="${TMPDIR:-/tmp}/s9b-jar-$RUNID.txt"; rm -f "$J"; trap 'rm -f "$J"' EXIT
OUT="${OUT:-out}"; mkdir -p "$OUT"
FAILED=0
ok(){ printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad(){ printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED+1)); }
step(){ printf '\n\033[1m== %s\033[0m\n' "$*"; }
api(){ curl -sS -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }
code(){ curl -sS -o /dev/null -w '%{http_code}' -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }
Q(){ docker exec colab-pg-s9 psql -U colab -d colab -tAc "$1" | tr -d ' '; }
Qraw(){ docker exec colab-pg-s9 psql -U colab -d colab -tAc "$1"; }

step "0. 계정·워크스페이스·에이전트·런타임 2대 (같은 저장소)"
api -X POST "$S/auth/signup" -d '{"display_name":"Dir","email":"s9b-'$RUNID'@example.com","password":"password123"}' >/dev/null
WS=$(api -X POST "$S/workspaces" -d '{"name":"S9b"}' | jq -r .id)
R=$(api -X POST "$S/workspaces/$WS/agents" -d '{"name":"R","role":"engineer","role_description":"d","instructions":"i","profiles":[{"name":"default","runtime_kind":"claude_code","model":"claude-sonnet-5"}]}' | jq -r .id)
pairup(){
  local code rt
  code=$(api -X POST "$S/workspaces/$WS/runtimes/pairings" -d "{\"name\":\"$1\"}" | jq -r .pairing_token)
  rt=$(curl -sS -X POST "$D/pair" -H 'Content-Type: application/json' \
        -d "{\"pairing_code\":\"$code\",\"hostname\":\"$1\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")
  echo "$(echo "$rt" | jq -r .runtime_id) $(echo "$rt" | jq -r .daemon_token)"
}
probe(){ curl -sS -X POST "$D/runtimes/$1/probe" -H "Authorization: Bearer $2" -H 'Content-Type: application/json' \
  -d "{\"daemon_version\":\"0.1.0\",\"hostname\":\"h\",\"capabilities\":[{\"runtime_kind\":\"claude_code\",\"present\":true,\"models\":[\"claude-sonnet-5\"],\"logged_in\":true}],\"repos\":[{\"path\":\"$3\",\"remote_url\":\"$4\",\"branch\":\"main\",\"clean\":true}],\"workdir_root\":\"/w\",\"disk\":{\"used_bytes\":1024},\"colab_cli\":{\"present\":true,\"version\":\"0.1.0\"}}" >/dev/null; }
read -r RID DTOK <<<"$(pairup mac-a-$RUNID)"
read -r RID2 DTOK2 <<<"$(pairup mac-c-$RUNID)"
# 같은 remote — mac-c 는 재바인딩 **후보**다(E14-05).
probe "$RID"  "$DTOK"  /Users/a/dev/app  git@x:app-$RUNID.git
probe "$RID2" "$DTOK2" /Users/c/work/app git@x:app-$RUNID.git
[ "$RID" != null ] && [ "$RID2" != null ] && ok "runtime 2대 페어링 ($RID / $RID2)" || { bad "pair"; exit 1; }

step "1. 서버가 쓰는 이벤트를 실제로 만들어 둔다 (취소·상태·HITL·GC 거부)"
SESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"ev\",\"goal\":\"g\",\"runtime_id\":\"$RID\",\"isolation\":{\"kind\":\"none\"},\"participants\":[{\"agent_id\":\"$R\"}],\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"manual\"}]},\"start\":true}")
ESID=$(echo "$SESS" | jq -r .id)
api -X POST "$S/sessions/$ESID/messages" -d "{\"content\":\"[@R](mention://agent/$R) 시작해라\"}" >/dev/null
sleep 1
ETASK=$(Q "SELECT id FROM task WHERE session_id='$ESID' ORDER BY created_at LIMIT 1")
ELANE=$(Q "SELECT lane_id FROM task WHERE id='$ETASK'")
# (a) 사람이 lane 을 중단한다 → status/cancel + args.note
code -X POST "$S/lanes/$ELANE/cancel" -d '{}' >/dev/null
# (b) 데몬이 gc 거부를 보고한다 → status/error gc.refused (command=gc + args.note)
GSESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"gc\",\"goal\":\"g\",\"runtime_id\":\"$RID\",\"isolation\":{\"kind\":\"worktree\",\"repo_path\":\"/Users/a/dev/app\"},\"participants\":[{\"agent_id\":\"$R\"}],\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"manual\"}]},\"start\":true}")
GSID=$(echo "$GSESS" | jq -r .id)
api -X POST "$S/sessions/$GSID/messages" -d "{\"content\":\"[@R](mention://agent/$R) 해라\"}" >/dev/null
sleep 1
# 먼저 깨끗한 workdir 을 보고하고(삭제 가능), Director 가 수동 GC 를 요청해
# 서버가 `gc` 명령을 큐잉하게 만든다 — 거부 영수증은 명령이 있어야 유효하다
# (workdirs.ApplyGCReports: "서버가 이 런타임에게 실제로 시킨 것만 움직인다").
curl -sS -X POST "$D/runtimes/$RID/workdirs" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d "{\"workdirs\":[{\"kind\":\"worktree\",\"path\":\"/w/gc57/r\",\"session_id\":\"$GSID\",\"agent_id\":\"$R\",\"bytes\":2048,\"git\":{\"branch\":\"colab/gc57/r\",\"merged\":true,\"dirty\":false,\"commits_ahead\":0}}]}" >/dev/null
WDID=$(Q "SELECT id FROM workdir WHERE session_id='$GSID' LIMIT 1")
code -X DELETE "$S/workdirs/$WDID?force=true" >/dev/null
GCMD=$(Q "SELECT count(*) FROM daemon_command WHERE type='gc' AND runtime_id='$RID'")
[ "${GCMD:-0}" -ge 1 ] && ok "수동 GC 요청이 gc 명령을 큐잉했다 ($GCMD 건)" || bad "gc 명령 $GCMD 건, want ≥1"
# 데몬이 거부를 돌려준다 → 서버가 피드에 "GC 거부: <reason>" 를 남긴다 (§6).
curl -sS -X POST "$D/runtimes/$RID/workdirs" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d "{\"workdirs\":[{\"kind\":\"worktree\",\"path\":\"/w/gc57/r\",\"session_id\":\"$GSID\",\"agent_id\":\"$R\",\"bytes\":2048,\"git\":{\"branch\":\"colab/gc57/r\",\"merged\":true,\"dirty\":false,\"commits_ahead\":0},\"gc\":{\"status\":\"refused\",\"reason\":\"isolation_worktree_57\"}}]}" >/dev/null
sleep 1
CANCEL_NOTE=$(Q "SELECT count(*) FROM task_event WHERE task_id='$ETASK' AND class='status' AND verb='cancel' AND payload->'args'->>'note' <> ''")
CANCEL_BAD=$(Q "SELECT count(*) FROM task_event WHERE task_id='$ETASK' AND class='status' AND payload ? 'note'")
[ "$CANCEL_NOTE" -ge 1 ] && ok "취소 행의 사람 문장이 payload.args.note 에 있다 ($CANCEL_NOTE 건)" || bad "args.note 취소 행 = $CANCEL_NOTE, want ≥1"
[ "$CANCEL_BAD" = 0 ] && ok "status payload 최상위 note 0 건 (닫힌 스키마)" || bad "최상위 note 가 남아 있다: $CANCEL_BAD 건"
GCREF=$(Q "SELECT count(*) FROM task_event WHERE object_ref=to_jsonb('gc.refused'::text) AND payload->>'command'='gc' AND payload->'args'->>'note' LIKE 'GC 거부:%'")
[ "$GCREF" -ge 1 ] && ok "gc 거부 행 = command:gc + args.note ($GCREF 건)" || bad "gc.refused 행 $GCREF 건, want ≥1"

step "2. S-52 전수 — 서버가 쓴 행(seq ≥ 2^30) 중 스키마 위반 0"
# task_event.schema.json 의 닫힌 집합을 SQL 로 옮긴다. 서버 코드의 표가 아니라
# 계약 원문을 보고 적었으므로, 구현이 표를 자기에게 맞게 낮추면 여기서 어긋난다.
VIOL_SQL="
WITH allowed(class, keys) AS (VALUES
  ('message', ARRAY['kind','text','chars']),
  ('tool',    ARRAY['tool_call_id','kind','title','path','lines_added','lines_removed','command','exit_code','summary','masked','duration_ms','policy','option_kind','options_offered','allow_once_missing']),
  ('usage',   ARRAY['input_tokens','output_tokens','cache_read_tokens','cache_write_tokens','cost_usd','estimated','cumulative','model','model_drift','rate_limit']),
  ('plan',    ARRAY['entries_total','entries_done','current']),
  ('runtime', ARRAY['runtime_kind','adapter_version','protocol_version','session_id','failure_kind','detail','not_before','stop_reason','resume_reason']),
  ('status',  ARRAY['command','args','result_ref','rejected_reason'])
)
SELECT count(*) FROM task_event e
  LEFT JOIN allowed a ON a.class = e.class::text
 WHERE e.seq >= 1073741824
   AND (
     e.class::text NOT IN ('message','tool','usage','plan','runtime','status')
     OR e.verb NOT IN ('say','think','edit_file','run_shell','read','search','use_tool','permission','report','update','start','resume','error','cancel','turn_end','post_message','delegate','set_status','submit_artifact','record_decision','hitl','review')
     OR e.outcome NOT IN ('started','ok','failed','allowed','rejected','cancelled','resumed','cold_start','report','update','info')
     OR (e.payload IS NOT NULL AND jsonb_typeof(e.payload) = 'object'
         AND EXISTS (SELECT 1 FROM jsonb_object_keys(e.payload) k WHERE NOT (k = ANY(a.keys))))
     OR (e.class::text = 'status' AND NOT (e.payload ? 'command'))
   )"
SERVER_ROWS=$(Q "SELECT count(*) FROM task_event WHERE seq >= 1073741824")
VIOL=$(Q "$VIOL_SQL")
[ "${SERVER_ROWS:-0}" -ge 3 ] && ok "서버가 쓴 행 $SERVER_ROWS 개를 검사했다" || bad "서버가 쓴 행이 $SERVER_ROWS 개뿐 — 스캔이 아무것도 재지 못한다"
if [ "$VIOL" = 0 ]; then ok "스키마 위반 0 (S-52)"; else
  bad "스키마 위반 $VIOL 건"
  Qraw "SELECT class||'/'||verb||' '||coalesce(object_ref::text,'')||' '||coalesce(payload::text,'') FROM task_event WHERE seq >= 1073741824" | sed 's/^/     /'
fi

step "3. 재바인딩 — 프롬프트가 번들에 실린다 (S-53 · NN5 · E14-06)"
RSESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"rebind\",\"goal\":\"g\",\"runtime_id\":\"$RID\",\"isolation\":{\"kind\":\"worktree\",\"repo_path\":\"/Users/a/dev/app\"},\"participants\":[{\"agent_id\":\"$R\"}],\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"manual\"}]},\"start\":true}")
RSID=$(echo "$RSESS" | jq -r .id)
# 세션이 diff 아티팩트 두 개를 제출한 상태로 만든다(제출 순서 = created_at).
Q "INSERT INTO artifact (session_id, name, type, version, storage_ref, created_at) VALUES
   ('$RSID','step-1','diff',1,'s1', now() - interval '2 minutes'),
   ('$RSID','step-2','diff',1,'s2', now() - interval '1 minutes')" >/dev/null
A1=$(Q "SELECT id FROM artifact WHERE session_id='$RSID' AND name='step-1'")
A2=$(Q "SELECT id FROM artifact WHERE session_id='$RSID' AND name='step-2'")
api -X POST "$S/sessions/$RSID/messages" -d "{\"content\":\"[@R](mention://agent/$R) 이어서 해라\"}" >/dev/null
sleep 1
RTASK=$(Q "SELECT id FROM task WHERE session_id='$RSID' ORDER BY created_at LIMIT 1")
# mac-a 가 8일째 사라졌다 → 스윕이 세션을 paused(runtime_offline) 로 옮긴다.
Q "UPDATE runtime SET status='offline', offline_since = now() - interval '8 days', last_seen_at = now() - interval '8 days' WHERE id='$RID'" >/dev/null
sleep 62
RST=$(Q "SELECT status FROM session WHERE id='$RSID'")
[ "$RST" = paused ] && ok "세션 = paused(runtime_offline) — 재바인딩 조건 성립" || bad "세션 = $RST, want paused"
RBC=$(code -X POST "$S/sessions/$RSID/rebind" -d "{\"runtime_id\":\"$RID2\",\"acknowledge_loss\":true}")
[ "$RBC" = 200 ] && ok "같은 remote 런타임으로 rebind = 200 (E14-03)" || bad "rebind = $RBC"
PREP=$(Q "SELECT count(*) FROM daemon_command WHERE type='rebind_prepare' AND session_id='$RSID'")
[ "$PREP" -ge 1 ] && ok "rebind_prepare 명령 $PREP 건 큐잉 (§4.3)" || bad "rebind_prepare = $PREP, want ≥1"
STORED=$(Q "SELECT length(coalesce(rebind_prompt,'')) FROM session WHERE id='$RSID'")
[ "${STORED:-0}" -gt 0 ] && ok "session.rebind_prompt 저장됨 ($STORED 자)" || bad "rebind_prompt 가 비어 있다 — 프롬프트가 갈 길이 없다 (S-53)"

# 새 머신의 데몬이 claim 한다 — 번들의 프롬프트가 실측 대상이다.
claim_prompt(){ curl -sS -X POST "$D/runtimes/$RID2/claim" -H "Authorization: Bearer $DTOK2" -H 'Content-Type: application/json' \
  -d '{"capacity":2,"wait_ms":0}' | jq -r --arg t "$1" '.tasks[] | select(.task.id==$t) | .prompt'; }
P1=$(claim_prompt "$RTASK")
printf '%s\n' "$P1" > "$OUT/57-rebind-prompt.txt"
has(){ printf '%s' "$2" | grep -qF -- "$1"; }
has '<rebind>' "$P1"                        && ok "번들 프롬프트에 <rebind> 구간이 있다 (S-53)"        || bad "<rebind> 없음 — $OUT/57-rebind-prompt.txt"
has '{{COLAB_REBIND_DIR}}/manifest.json' "$P1" && ok "자리표시자 + manifest.json 을 가리킨다 (§4.3 v0.7.2)" || bad "{{COLAB_REBIND_DIR}}/manifest.json 없음"
has 'git apply' "$P1"                       && ok "git apply 로 적용하라고 지시한다 (NN5)"            || bad "git apply 지시 없음"
if has 'colab artifact get' "$P1"; then bad "아직 colab artifact get 재다운로드를 시킨다 (NN5)"; else ok "colab artifact get 재다운로드 없음 (NN5)"; fi
NPH=$(printf '%s' "$P1" | grep -oF '{{COLAB_REBIND_DIR}}' | wc -l | tr -d ' ')
[ "$NPH" = 1 ] && ok "자리표시자 정확히 1회 (harness §10 — 미치환은 failed(config))" || bad "자리표시자 $NPH 회, want 1"
I1=$(printf '%s' "$P1" | grep -nF "$A1" | head -1 | cut -d: -f1)
I2=$(printf '%s' "$P1" | grep -nF "$A2" | head -1 | cut -d: -f1)
if [ -n "$I1" ] && [ -n "$I2" ] && [ "$I1" -lt "$I2" ]; then ok "아티팩트가 제출 순서대로 나열됐다 (E14-06)"; else bad "순서 = $I1/$I2 (want step-1 먼저)"; fi
has '콜드 스타트' "$P1" && ok "콜드 스타트 문장이 있다 (E14-06)" || bad "콜드 스타트 문장 없음"

step "4. 재큐잉해도 남고, completed 뒤에는 사라진다 (S-53)"
# 새 머신도 조용해진다 → attempt 가 재큐잉된다. 지시가 사라지면 diff 는 영영 적용되지 않는다.
Q "UPDATE task SET status='queued', attempt = attempt + 1, runtime_id=NULL, dispatched_at=NULL, started_at=NULL, heartbeat_at=NULL WHERE id='$RTASK'" >/dev/null
P2=$(claim_prompt "$RTASK")
printf '%s\n' "$P2" > "$OUT/57-rebind-prompt-attempt2.txt"
has '<rebind>' "$P2" && ok "재큐잉된 attempt 2 번들에도 <rebind> 가 있다" || bad "재큐잉 뒤 <rebind> 가 사라졌다 — $OUT/57-rebind-prompt-attempt2.txt"
has '<resumed' "$P2" && ok "attempt 2 프롬프트에 <resumed> 구간이 있다 (§8.4)" || bad "<resumed> 없음"
has 'do NOT make an edit again' "$P2" && ok "workdir 을 먼저 보고 이미 있는 편집을 다시 하지 말라고 지시한다 (E8-04 (4))" || bad "workdir 중복 편집 금지 문장 없음"
has 'git status' "$P2" && ok "workdir 점검 지시에 git status 가 있다 (PRD §8.4)" || bad "git status 지시 없음"

ATT=$(Q "SELECT attempt FROM task WHERE id='$RTASK'")
curl -sS -X POST "$D/tasks/$RTASK/attempts/$ATT/phase" -H "Authorization: Bearer $DTOK2" -H 'Content-Type: application/json' \
  -d '{"phase":"running","pgid":4242,"workdir_path":"/w/rebind/r"}' >/dev/null
FINC=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$D/tasks/$RTASK/attempts/$ATT/finish" -H "Authorization: Bearer $DTOK2" -H 'Content-Type: application/json' \
  -d '{"outcome":"completed","stop_reason":"end_turn"}')
STORED2=$(Q "SELECT length(coalesce(rebind_prompt,'')) FROM session WHERE id='$RSID'")
[ "$FINC" = 200 ] && ok "finish(completed) = 200" || bad "finish = $FINC"
[ "${STORED2:-1}" = 0 ] && ok "completed 뒤 rebind_prompt 가 비었다 — 다음 턴은 diff 를 다시 적용하지 않는다" || bad "rebind_prompt 가 $STORED2 자로 남아 있다"

step "5. 두 번째 전수 검사 — 위의 조작이 만든 행까지 포함해 위반 0"
VIOL2=$(Q "$VIOL_SQL")
SERVER_ROWS2=$(Q "SELECT count(*) FROM task_event WHERE seq >= 1073741824")
if [ "$VIOL2" = 0 ]; then ok "서버가 쓴 행 $SERVER_ROWS2 개 전부 스키마 준수 (S-52)"; else
  bad "스키마 위반 $VIOL2 건"
  Qraw "SELECT class||'/'||verb||' '||coalesce(object_ref::text,'')||' '||coalesce(payload::text,'') FROM task_event WHERE seq >= 1073741824" | sed 's/^/     /'
fi

step "결과"
if [ "$FAILED" = 0 ]; then printf '\033[32m전부 통과\033[0m\n'; else printf '\033[31m실패 %d건\033[0m\n' "$FAILED"; fi
exit $FAILED
