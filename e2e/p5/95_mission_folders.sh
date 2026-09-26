#!/usr/bin/env bash
# e2e/p5/95_mission_folders.sh — T-FOLDERS 실서버 스모크: 같은 미션 두 에이전트가 폴더로 만난다
# (daemon-protocol v0.10.0 §4.1·§6.1, harness v0.9.7 `<folders>`, Director 판정 2026-09-26 D1~D8)
# — **데몬 없이**, 데몬 역할은 curl + 실제 파일 만들기로 흉내(86_ 의 레시피, fake 런타임).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 15s
#
# 재는 것 (판정 표 out/95-checks.tsv):
#   A. `none` 미션 턴 → 번들: 경로가 `rooms/<방>/<미션>/<에이전트>`(한글 보존) · 첫 attempt 부터 workdir.id ·
#      shared_path · 행(work_id·role=agent) · lane 바인딩.
#   B. 두 번째 에이전트의 `<folders>`: 동료 A 의 폴더가 read only 로 실려 있고, **B 가 그 경로로 A 의 파일을 읽는다**
#      (fake 런타임이 실제로 디스크에서 읽는다 — 경로가 맞아야만 통과).
#   C. `_shared` 쓰기: 둘 다 같은 `_shared` 를 받고, A 가 쓴 파일을 B 가 읽는다. §6 role=shared 보고가 그 행에 붙는다.
#   D. 미션 밖 턴 → `_room/<에이전트>`, shared 줄 없음.
#   E. 미션 닫기 → **gc 명령 0**(D8 B: 닫힘은 삭제 신호가 아니다) · 디스크의 폴더 그대로 · S13 `?work_id=` 가
#      그 미션의 행과 합계 용량을 준다(닫기 확인 문장의 입력).
#   F. 이름 바꾸기 뒤 다음 번들의 경로 불변(§6.1 만들 때 고정).
#
# 스택(T-FOLDERS 배정): server :8126 · pg :5470 · 컨테이너 colab-pg-folders-e2e.
# 사용: SERVER_URL=http://localhost:8126 PG_PORT=5470 PG_CONTAINER=colab-pg-folders-e2e bash e2e/p5/up.sh
#       bash e2e/p5/95_mission_folders.sh
#       SERVER_URL=http://localhost:8126 PG_PORT=5470 PG_CONTAINER=colab-pg-folders-e2e bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8126}"
export PG_PORT="${PG_PORT:-5470}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-folders-e2e}"
source "$(dirname "$0")/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/95-checks.tsv"; : > "$CHECKS"
curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server not up at $SERVER_URL — run e2e/p5/up.sh with the T-FOLDERS ports"

claim() { daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}'; }
report() { daemon_api_code "runtimes/$RID/workdirs" "$(jq -nc --argjson e "$1" '{workdirs:[$e]}')"; }
# bundle_for AGENT_NAME → 그 에이전트를 멘션해 얻은 번들 JSON (claim 한 번, 다른 task 는 완료 처리)
bundle_for() {
  local name="$1" agent="$2" cl b
  post_message "$S" "[@$name](mention://agent/$agent) 부탁합니다" >/dev/null
  cl="$(claim)"
  b="$(jq -c --arg a "$agent" '[.tasks[]|select(.task.agent_id==$a)][0]' <<<"$cl")"
  [ -n "$b" ] && [ "$b" != null ] || die "claim 에 $name 의 task 가 없다: $cl"
  # 나머지 번들은 다음 claim 을 막지 않게 끝낸다(한 에이전트당 running 1).
  for t in $(jq -r '.tasks[].task.id' <<<"$cl"); do
    psqlq "update task set status='completed' where id='$t'" >/dev/null
  done
  printf '%s' "$b"
}
# fake 런타임: 데몬이 할 일만 — 번들의 경로를 그대로 mkdir -p 한다(§4.1: 경로는 서버가 짓는다).
prepare() {
  local b="$1" p s
  p="$(jq -r '.workdir.path' <<<"$b")"; s="$(jq -r '.workdir.shared_path // ""' <<<"$b")"
  mkdir -p "$p"; [ -n "$s" ] && mkdir -p "$s"
  printf '%s' "$p"
}
folders_block() { jq -r '.prompt' <<<"$1" | awk '/^<folders>/{f=1} f{print} /^<\/folders>/{f=0}'; }

step "0. 계정 · 워크스페이스 · 에이전트 2 · 페어링(curl) · probe(workdir_root)"
signup "folders-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "FOLDERS $RUN")"
mkagent() {
  api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg n "$1" --arg r "$2" \
    '{name:$n,role:$r,role_description:"한다",instructions:"짧게, 한국어로.",
      profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id
}
A="$(mkagent Developer engineer)"; B="$(mkagent Writer writer)"; C="$(mkagent Reviewer reviewer)"
ROOT="/tmp/colab-folders-$RUN"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-folders" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "$ROOT")"
export DTOK RID
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "probe 뒤 컴퓨터 online"

step "A. 미션 턴 → rooms/<방>/<미션>/<에이전트> · 첫 attempt id · shared_path"
S="$(create_room_work "$WS" "$(jq -nc --arg a "$A" --arg b "$B" --arg c "$C" --arg rt "$RID" \
  '{title:"게임 제작",goal:"스네이크를 만든다",isolation:{kind:"none"},
    participants:[{agent_id:$a},{agent_id:$b},{agent_id:$c}],assignee_agent_id:$a,runtime_id:$rt}')")"
[ -n "$S" ] && [ "$S" != null ] || die "방 생성 실패"
# create_room_work 는 방 + 미션을 만들고 미션 id 를 $ROOM_WORK_DIR/<방> 에 적어 둔다(방에는 legacy_work_id 가 없다).
WK="$(cat "$ROOM_WORK_DIR/$S")"
[ -n "$WK" ] || die "미션 id 를 찾지 못했다"
psqlq "update work set title='스네이크 v2' where id='$WK'" >/dev/null
BA="$(bundle_for Developer "$A")"; PA="$(prepare "$BA")"
WDA="$(jq -r '.workdir.id // ""' <<<"$BA")"; SH="$(jq -r '.workdir.shared_path // ""' <<<"$BA")"
chk A.1 yes "$( [[ "$WDA" =~ ^[0-9a-f-]{36}$ ]] && echo yes || echo "no($WDA)" )" "첫 attempt 번들에 workdir.id (dir 도, §4.1 v0.10.0)"
chk A.2 yes "$( [[ "$PA" == "$ROOT/rooms/게임-제작-"*"/스네이크-v2-"*"/developer-"* ]] && echo yes || echo "no($PA)" )" "경로 = rooms/<방>/<미션>/<에이전트>, 한글 보존(D1 A)"
chk A.3 yes "$( [[ "$SH" == "$ROOT/rooms/게임-제작-"*"/스네이크-v2-"*"/_shared" ]] && echo yes || echo "no($SH)" )" "shared_path = 그 미션의 _shared(D4 A)"
chk A.4 "$WK/agent" "$(psqlq "select coalesce(work_id::text,'-')||'/'||role from workdir where id='$WDA'")" "행의 work_id·role=agent"
chk A.5 "$WDA" "$(psqlq "select coalesce(workdir_id::text,'-') from lane where id='$(jq -r .task.lane_id <<<"$BA")'")" "lane 이 번들 시점에 그 행에 묶인다"
chk A.6 1 "$(psqlq "select count(*) from workdir where work_id='$WK' and role='shared'")" "_shared 행 1(에이전트·lane 없음)"

step "B. 동료 폴더 — B 의 <folders> 에 A 의 경로, 그 경로로 실제 파일을 읽는다"
echo "index.html by Developer" > "$PA/index.html"
BB="$(bundle_for Writer "$B")"; PB="$(prepare "$BB")"
FB="$(folders_block "$BB")"; printf '%s\n' "$FB" > "$OUT/95-folders-writer.txt"
chk B.1 yes "$(grep -qF "you: $PB  (your working folder — write here)" <<<"$FB" && echo yes || echo no)" "you: 자기 폴더"
chk B.2 yes "$(grep -qF -- "- Developer: $PA  (read only)" <<<"$FB" && echo yes || echo no)" "동료 Developer 의 폴더가 read only 로"
chk B.3 "index.html by Developer" "$(cat "$(grep -oE "^- Developer: [^ ]+" <<<"$FB" | cut -d' ' -f3)/index.html")" "B 가 <folders> 의 경로로 A 의 파일을 읽는다(실제 디스크)"
chk B.4 0 "$(grep -c '^- Writer:' <<<"$FB")" "자기 자신은 동료 줄에 없다"
chk B.5 yes "$(jq -r '.prompt' <<<"$BB" | grep -q '<roster_status>' && jq -r '.prompt' <<<"$BB" | awk '/<roster_status>/{r=NR} /<folders>/{f=NR} /<trigger>/{t=NR} END{exit !(r<f && f<t)}' && echo yes || echo no)" "<folders> 는 <roster_status> 뒤 <trigger> 앞"
chk B.6 yes "$(grep -qF 'colab_room_read' <<<"$FB" && ! grep -qF '`colab room read`' <<<"$FB" && echo yes || echo no)" "마지막 문장이 표면별(mcp = 툴 이름, 셸 명령 0)"

step "C. 미션 공용 _shared — 둘이 같은 폴더, A 가 쓰고 B 가 읽는다 · role=shared 보고"
chk C.1 "$SH" "$(jq -r '.workdir.shared_path' <<<"$BB")" "두 에이전트의 shared_path 가 같다"
echo "spec by Developer" > "$SH/spec.md"
chk C.2 "spec by Developer" "$(cat "$(jq -r '.workdir.shared_path' <<<"$BB")/spec.md")" "B 가 _shared 의 파일을 읽는다"
chk C.3 200 "$(report "$(jq -nc --arg p "$SH" '{kind:"dir",path:$p,role:"shared",bytes:2048}')")" "§6 role=shared 보고(표식 없는 폴더) → 200"
chk C.4 2048 "$(psqlq "select disk_bytes from workdir where path_or_ref='$SH'")" "그 보고가 _shared 행에 붙었다(경로로 찾는다)"

step "D. 미션 밖 턴 → _room/<에이전트>, shared 줄 없음"
# 미션 밖 턴: 방에 열린 미션이 있으면 서버가 자동 귀속하므로(FR-3.1) 할 일의 work_id 를 비워
# "미션이 지워진 뒤 큐에 남은 할 일"과 같은 모양으로 만든다.
# 아직 폴더가 없는 에이전트로 — 이미 행이 있는 lane 은 그 경로를 그대로 쓴다(D6 A).
api_ok POST "/rooms/$S/messages" "$(jq -nc --arg c "[@Reviewer](mention://agent/$C) 미션 밖 부탁" '{content:$c}')" -H "Idempotency-Key: $(uuid)" >/dev/null
psqlq "update task set work_id=null where session_id='$S' and status='queued'" >/dev/null
psqlq "update lane set work_id=null where session_id='$S' and id in (select lane_id from task where session_id='$S' and status='queued')" >/dev/null
CLO="$(claim)"
BO="$(jq -c --arg a "$C" '[.tasks[]|select(.task.agent_id==$a)][0]' <<<"$CLO")"
[ -n "$BO" ] && [ "$BO" != null ] || die "미션 밖 claim 실패: $CLO"
for t in $(jq -r '.tasks[].task.id' <<<"$CLO"); do psqlq "update task set status='completed' where id='$t'" >/dev/null; done
PO="$(jq -r '.workdir.path' <<<"$BO")"
chk D.1 yes "$( [[ "$PO" == "$ROOT/rooms/게임-제작-"*"/_room/reviewer-"* ]] && echo yes || echo "no($PO)" )" "미션 밖 = _room/<에이전트>"
chk D.2 "" "$(jq -r '.workdir.shared_path // ""' <<<"$BO")" "미션 밖 턴에는 shared_path 없음"
chk D.3 0 "$(folders_block "$BO" | grep -c '^shared:')" "<folders> 에 shared 줄 없음"

step "E. 미션 닫기 — gc 0(D8 B) · 폴더 그대로 · S13 ?work_id="
psqlq "update task set status='completed' where session_id='$S' and status<>'completed'" >/dev/null
api_ok POST "/works/$WK/complete" '{"confirm":true}' >/dev/null
chk E.1 completed "$(psqlq "select status from work where id='$WK'")" "미션이 닫혔다"
chk E.2 0 "$(psqlq "select count(*) from daemon_command c where c.type='gc' and c.payload::text like '%'||'$WDA'||'%'")" "닫힘 직후 gc 명령 0 — 닫힘은 삭제 신호가 아니다(D8 B)"
chk E.3 0 "$(psqlq "select count(*) from daemon_command c where c.type='gc' and c.payload::text like '%'||'$SH'||'%'")" "_shared 에도 gc 0"
chk E.4 yes "$( [ -f "$PA/index.html" ] && [ -f "$SH/spec.md" ] && echo yes || echo no )" "디스크의 미션 폴더가 그대로 있다"
chk E.5 active "$(psqlq "select distinct status from workdir where work_id='$WK'")" "행도 active(보존 기한을 기다린다)"
L="$(api_ok GET "/runtimes/$RID/workdirs?work_id=$WK")"; echo "$L" | jq . > "$OUT/95-s13-work.json"
chk E.6 yes "$( [ "$(jq '[.items[]|select(.role=="shared")]|length' <<<"$L")" = 1 ] && [ "$(jq '[.items[]|select(.role=="agent")]|length' <<<"$L")" -ge 1 ] && echo yes || echo no )" "S13 ?work_id= 가 그 미션의 에이전트 행 + _shared 를 준다(닫기 확인의 N개)"
chk E.7 2048 "$(jq -r '.disk_bytes_total' <<<"$L")" "합계 용량도 그 미션 것만(닫기 확인의 〈용량〉)"
chk E.8 yes "$(jq -e --arg w "$WK" 'all(.items[]; .work_id == $w and .work.title == "스네이크 v2")' <<<"$L" >/dev/null && echo yes || echo no)" "행마다 현재 미션 이름(work) — 경로가 아니라 이름을 화면이 쓴다"

step "F. 이름을 바꿔도 경로는 그대로(§6.1 만들 때 고정)"
psqlq "update room set name='완전히 다른 방' where id='$S'" >/dev/null
psqlq "update agent set name='Dev2' where id='$A'" >/dev/null
psqlq "update work set status='active', finished_at=null where id='$WK'" >/dev/null
BA2="$(bundle_for Dev2 "$A")"
chk F.1 "$PA" "$(jq -r '.workdir.path' <<<"$BA2")" "이름을 바꾼 뒤에도 번들의 경로가 같다"
chk F.2 "$WDA/true" "$(jq -r '.workdir.id+"/"+(.workdir.reuse|tostring)' <<<"$BA2")" "같은 행 · reuse=true"

step "결과: $CHECKS"
cat "$CHECKS" >&2
[ "$FAILS" = 0 ] && ok "95 all pass" || die "95: $FAILS fail"
