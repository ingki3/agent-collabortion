#!/usr/bin/env bash
# e2e/p5/86_workdir_id.sh — T-S21 실서버 스모크: K-14 `workdir.id` 번들 → §6 id 회신 → S13 한 행 → gc
# (daemon-protocol v0.8.3 §4.1·§6) — **데몬 없이**, 데몬 역할은 curl 로 흉내(79_ 의 레시피).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 10s
#
# 재는 것 (판정 표 out/86-checks.tsv):
#   A. worktree 세션 → claim: 번들 `workdir.id` 가 uuid 이고 그 행이 이미 있다(경로·kind·agent 일치) · 행 1.
#   B. phase preparing(path) → 행 여전히 1(같은 id). §6 보고 **id 만**(session_id·agent_id 없음 — 재시작한
#      데몬이 표식 파일에서 읽은 모양) → 200 · 그 행의 bytes·git 갱신 · 피드에 드롭 노트 0.
#   C. S13 listRuntimeWorkdirs: 그 컴퓨터에 행 1(같은 id, 중복 0).
#   D. 옛 데몬 모양(id 없음, session_id·agent_id·path) 보고 → 같은 행 갱신 · 여전히 1.
#   E. 두 번째 턴(같은 세션·같은 에이전트): 번들 workdir.id 가 A 와 같다 · reuse=true.
#   F. finish(workdir.git) → 병합·클린 → deleteWorkdir 202 → claim 응답 gc {workdirs:[{id,path}]} 의 id = A
#      → §6 영수증(id 만 + gc.status=deleted) 200 → 행 status=deleted · 명령 consumed_by=workdir_report · S13 0.
#
# 스택(T-S21 배정): server :8124 · pg :5468 · 컨테이너 colab-pg-s21. 다른 워커 스택과 겹치지 않는다(§0-13).
# 사용: SERVER_URL=http://localhost:8124 PG_PORT=5468 PG_CONTAINER=colab-pg-s21 bash e2e/p5/up.sh
#       bash e2e/p5/86_workdir_id.sh
#       SERVER_URL=http://localhost:8124 PG_PORT=5468 PG_CONTAINER=colab-pg-s21 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8124}"
export PG_PORT="${PG_PORT:-5468}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-s21}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/86-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/86-cookies.txt"; rm -f "$COOKIE"
NIL="00000000-0000-0000-0000-000000000000"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-S21 ports"
claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
# report JSON_ENTRY → §6 보고 코드
report() { daemon_api_code "runtimes/$RID/workdirs" "$(jq -nc --argjson e "$1" '{workdirs:[$e]}')"; }
rows() { psqlq "select count(*) from workdir where session_id='$S'"; }

step "0. 계정 · 워크스페이스 · 에이전트 · 페어링(curl) · probe(workdir_root)"
signup "s21-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "S21 $RUN")"
AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc '{name:"Lead",role:"lead",role_description:"팀을 이끈다",
  instructions:"짧게, 한국어로 답한다.",profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id)"
ROOT="/tmp/colab-s21-$RUN"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-s21" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "$ROOT")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. worktree 세션 → claim: 번들 workdir.id 와 그 행"
S="$(create_room_work "$WS" "$(jq -nc --arg t "S21 워크트리 $RUN" --arg a "$AG" --arg rt "$RID" --arg r "$ROOT/repo" \
  '{title:$t,goal:"저장소 밖에서 짧은 인사말 한 줄을 쓴다",isolation:{kind:"worktree",repo_path:$r},participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt}')")"
[ -n "$S" ] && [ "$S" != null ] || die "세션 생성 실패"
CL="$(claim)"; echo "$CL" | jq . > "$OUT/86-claim-1.json"
B="$(jq -c --arg s "$S" '.tasks[]|select(.task.session_id==$s)' <<<"$CL")"
[ -n "$B" ] || die "claim 에 세션 $S 의 task 가 없다: $CL"
T1="$(jq -r .task.id <<<"$B")"; WD="$(jq -r '.workdir.id // ""' <<<"$B")"; WT_PATH="$(jq -r .workdir.path <<<"$B")"
chk A.1 yes "$( [[ "$WD" =~ ^[0-9a-f-]{36}$ ]] && echo yes || echo "no($WD)" )" "번들 workdir.id 가 uuid (§4.1 v0.8.3)"
WD="${WD:-$NIL}"
chk A.2 "worktree/$WT_PATH/$AG/$S" "$(psqlq "select kind::text||'/'||path_or_ref||'/'||coalesce(agent_id::text,'-')||'/'||session_id from workdir where id='$WD'")" "그 id 의 행이 이미 있다(kind·path·agent·session 이 번들과 같다)"
chk A.3 1 "$(rows)" "세션의 workdir 행 1"
chk A.4 "$WD" "$(psqlq "select coalesce(workdir_id::text,'-') from lane where id='$(jq -r .task.lane_id <<<"$B")'")" "lane.workdir_id 가 번들 시점에 이미 그 행"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. phase preparing(path) → §6 보고 id 만 → 같은 행 갱신"
chk B.1 200 "$(daemon_api_code "tasks/$T1/attempts/1/phase" "$(jq -nc --arg p "$WT_PATH" '{phase:"preparing",pgid:4242,workdir_path:$p}')")" "phase preparing(workdir_path)"
chk B.2 1 "$(rows)" "preparing 뒤에도 행 1(갱신만)"
chk B.3 200 "$(report "$(jq -nc --arg id "$WD" --arg p "$WT_PATH" '{id:$id,kind:"worktree",path:$p,bytes:4096,git:{branch:"colab/s21/lead",merged:false,dirty:false,commits_ahead:2}}')")" "§6 보고: id 만(session_id·agent_id 없음) → 200"
chk B.4 "4096/f/2" "$(psqlq "select disk_bytes||'/'||case when merged then 't' else 'f' end||'/'||commits_ahead from workdir where id='$WD'")" "그 행의 bytes·merged·commits_ahead 갱신"
chk B.5 1 "$(rows)" "행 여전히 1"
chk B.6 0 "$(psqlq "select count(*) from task_event where task_id='$T1' and class='runtime' and verb='error'")" "피드에 드롭 노트 0"
daemon_api "tasks/$T1/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. S13 listRuntimeWorkdirs — 같은 행 1(중복 0)"
L="$(api_ok GET "/runtimes/$RID/workdirs")"; echo "$L" | jq . > "$OUT/86-s13-1.json"
chk C.1 1 "$(jq '.items|length' <<<"$L")" "S13 행 1"
chk C.2 "$WD/4096" "$(jq -r '.items[0].id+"/"+(.items[0].disk_bytes|tostring)' <<<"$L")" "S13 행 = 번들 id · 보고한 bytes"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 옛 데몬 모양(id 없음) 보고 → 폴백으로 같은 행"
chk D.1 200 "$(report "$(jq -nc --arg s "$S" --arg a "$AG" --arg p "$WT_PATH" '{kind:"worktree",path:$p,session_id:$s,agent_id:$a,bytes:8192,git:{branch:"colab/s21/lead",merged:false,dirty:true,commits_ahead:2}}')")" "§6 보고: session_id·agent_id·path(id 없음) → 200"
chk D.2 "8192/t" "$(psqlq "select disk_bytes||'/'||case when coalesce(tree_dirty,false) then 't' else 'f' end from workdir where id='$WD'")" "같은 행이 갱신됐다(bytes·tree_dirty)"
chk D.3 1 "$(rows)" "행 여전히 1(짝 폴백이 새 행을 만들지 않는다)"

# ───────────────────────────── E ─────────────────────────────────────────────
step "E. 두 번째 턴 — 번들 workdir.id 가 같다"
daemon_api "tasks/$T1/attempts/1/finish" "$(jq -nc --arg p "$WT_PATH" '{outcome:"completed",stop_reason:"end_turn",transport:"acp",last_seq:0,
  usage:{input_tokens:10,output_tokens:5,cost_usd:0.001},workdir:{path:$p,git:{branch:"colab/s21/lead",merged:false,dirty:false,commits_ahead:2}}}')" >/dev/null
post_message "$S" "[@Lead](mention://agent/$AG) 하나 더" >/dev/null
CL2="$(claim)"; echo "$CL2" | jq . > "$OUT/86-claim-2.json"
B2="$(jq -c --arg s "$S" '.tasks[]|select(.task.session_id==$s)' <<<"$CL2")"
[ -n "$B2" ] || die "두 번째 claim 에 세션 $S 의 task 가 없다: $CL2"
T2="$(jq -r .task.id <<<"$B2")"
chk E.1 "$WD" "$(jq -r '.workdir.id // "-"' <<<"$B2")" "두 번째 번들 workdir.id = 첫 번들과 같다(C3: 에이전트당 1 워크트리)"
chk E.2 "$WT_PATH/true" "$(jq -r '.workdir.path+"/"+(.workdir.reuse|tostring)' <<<"$B2")" "경로 같고 reuse=true"
chk E.3 1 "$(rows)" "행 여전히 1"
daemon_api "tasks/$T2/attempts/1/phase" '{"phase":"running","pgid":4243}' >/dev/null
daemon_api "tasks/$T2/attempts/1/finish" "$(jq -nc --arg p "$WT_PATH" '{outcome:"completed",stop_reason:"end_turn",transport:"acp",last_seq:0,
  usage:{input_tokens:10,output_tokens:5,cost_usd:0.001},workdir:{path:$p,git:{branch:"colab/s21/lead",merged:true,dirty:false,commits_ahead:0}}}')" >/dev/null

# ───────────────────────────── F ─────────────────────────────────────────────
step "F. deleteWorkdir → gc 명령의 id → §6 영수증(id 만) → deleted"
chk F.1 "t/f/0" "$(psqlq "select case when merged then 't' else 'f' end||'/'||case when coalesce(tree_dirty,false) then 't' else 'f' end||'/'||commits_ahead from workdir where id='$WD'")" "finish 의 workdir.git 이 행에 반영(병합·클린)"
R="$(api DELETE "/workdirs/$WD")"
chk F.2 202 "$(api_code <<<"$R")" "deleteWorkdir → 202"
CL3="$(claim)"; echo "$CL3" | jq . > "$OUT/86-claim-gc.json"
GC="$(jq -c --arg s "$S" '[.commands[]|select(.type=="gc" and .session_id==$s)][0]' <<<"$CL3")"
chk F.3 "$WD/$WT_PATH" "$(jq -r '(.workdirs[0].id // "-")+"/"+(.workdirs[0].path // "-")' <<<"$GC")" "claim 응답의 gc {workdirs:[{id,path}]} 의 id = 번들 id"
chk F.4 200 "$(report "$(jq -nc --arg id "$WD" --arg p "$WT_PATH" '{id:$id,kind:"worktree",path:$p,gc:{status:"deleted"}}')")" "§6 영수증: id 만 + gc.status=deleted → 200"
chk F.5 deleted "$(psqlq "select status from workdir where id='$WD'")" "행 status=deleted"
chk F.6 workdir_report "$(psqlq "select coalesce(consumed_by,'-') from daemon_command where type='gc' and session_id='$S'")" "gc 명령 소비(consumed_by=workdir_report)"
chk F.7 0 "$(api_ok GET "/runtimes/$RID/workdirs" | jq '.items|length')" "S13 에서 사라졌다(deleted 는 기본 목록 밖)"
chk F.8 1 "$(rows)" "행은 1(삭제 표식, 중복 없음)"

step "결과: $CHECKS"
cat "$CHECKS" >&2
[ "$FAILS" = 0 ] && ok "86 all pass" || die "86: $FAILS fail"
