#!/usr/bin/env bash
# e2e/p5/87_migrate_0025.sh — T-R1a 이관 검증: 옛 서버(0024)로 데이터를 만들고 → 0025 를 걸고 → 행 수 대조
# (server/migrations/verify/verify_0025*.sql) → 옛 `/sessions/*` 응답이 이관 전과 JSON 동일(키 순서 무시)인지 잰다(R4 뒤로는 옮겨진 op 만 — 아래 R4 문단).
#
# 비용 한 줄(I-3): 에이전트 턴 0(데몬 없이 curl) · $0 · ≈ 60s(옛 서버 빌드 포함)
#
# 재는 것 (판정 표 out/87-checks.tsv):
#   0. 옛 서버 = 0025 가 없는 커밋(BASE)에서 빌드한 바이너리, 새 서버 = HEAD. 한 DB(colab87)를 둘이 차례로 쓴다.
#   A. 옛 서버로 시드 — 세션 5개(active·draft·paused·completed·cancelled), 참여자 2·deputy·메시지·lane·task(claim)·
#      결정·HITL·아티팩트·인박스·workdir. 이관 전 응답 스냅숏(세션 목록·상세·참여자·메시지·lane·task·HITL·
#      아티팩트·결정·비용·워크스페이스 비용·인박스).
#   B. verify_0025_pre.sql → HEAD cmd/migrate(0025 한 개 적용) → verify_0025.sql 의 모든 검사 0.
#   C. 새 서버의 같은 응답이 이관 전과 JSON 동일(jq -S).
#   D. 이관 뒤 쓰기 경로: 새 서버로 세션 생성·메시지 게시가 되고, 새 세션도 방 1 + 미션 1 + 사람 참여자(방장)로 선다.
#
# R4(openapi v0.3.0 D22) — 새 서버에는 `/sessions/*` 가 없다. **옛 서버(BASE, 0024)에 던지는 호출은 그대로 `/sessions/*`**
#   (그 바이너리의 표면이다: mk · 시드 메시지 · pause · cancel · snap pre). 새 서버 쪽만 옮겼다:
#   · C: snap post 는 경로만 옮겨진 op(messages·lanes·hitl-requests·artifacts·decisions·cost — 같은 operationId)을
#     `/rooms/{id}/…` 로 읽어 옛 `/sessions/{id}/…` 스냅숏과 대조한다. 삭제된 op(listSessions·getSession·
#     listParticipants — 방·미션 모양으로 바뀜)는 대조에서 빠진다(44 → 33).
#   · D: 새 세션 = createRoom → updateRoom → addRoomParticipant → createWork(create_room_work), 읽기는 getWork.
#
# 스택(T-R1a 배정): 옛 서버 :8125 · 새 서버 :8126 · pg :5474 · 컨테이너 colab-pg-r1a. DB 는 colab87 을 새로 만든다.
# 사용: bash e2e/p5/87_migrate_0025.sh        (서버는 스크립트가 띄우고 끈다 — up.sh 불필요)
#       BASE=<commit> 로 옛 쪽을 고정할 수 있다(기본: 0025 를 처음 더한 커밋의 부모, 아직 커밋 전이면 HEAD).
export PG_PORT="${PG_PORT:-5474}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-r1a}"
OLD_PORT="${OLD_PORT:-8125}"; NEW_PORT="${NEW_PORT:-8126}"
export SERVER_URL="http://localhost:$OLD_PORT"
source "$(dirname "$0")/lib.sh"
cd "$E2E_ROOT"
DB=colab87
DBURL="postgres://colab:colab@localhost:$PG_PORT/$DB?sslmode=disable"
CHECKS="$OUT/87-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/87-cookies.txt"; rm -f "$COOKIE"
SNAP="$OUT/87-snap"; rm -rf "$SNAP"; mkdir -p "$SNAP/pre" "$SNAP/post"
RUN="$(date +%H%M%S)-$RANDOM"
# 이 스크립트의 psql 은 colab87 을 본다(lib 의 psqlq 는 colab 을 본다).
psqlq() { docker exec -i "$PG_CONTAINER" psql -U colab -d "$DB" -qtA -F $'\t' -v ON_ERROR_STOP=1 -c "$1"; }
psqlf() { docker exec -i "$PG_CONTAINER" psql -U colab -d "$DB" -qtA -F $'\t' -v ON_ERROR_STOP=1 -f - < "$1"; }
stop_pid() { [ -f "$1" ] || return 0; local p; p="$(cat "$1")"; kill -TERM -- "-$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null || true; rm -f "$1"; }
trap 'stop_pid "$OUT/87-old.pid"; stop_pid "$OUT/87-new.pid"' EXIT
start_server() { # BIN PORT PIDFILE LOG
  COLAB_DB_URL="$DBURL" COLAB_SERVER_URL="http://localhost:$2" COLAB_WEB_URL="http://localhost:3999" COLAB_SERVER_ADDR=":$2" \
    setsid_run "$4" "$1" > "$3"
  for i in $(seq 1 60); do curl -fsS "http://localhost:$2/healthz" >/dev/null 2>&1 && return 0; sleep 0.5; done
  die "server $1 did not start on :$2 (see $4)"
}

# ───────────────────────────── 0 ─────────────────────────────────────────────
step "0. 바이너리 — 옛(BASE) · 새(HEAD) · DB $DB"
if [ -z "${BASE:-}" ]; then
  ADD="$(git log --diff-filter=A --format=%H -1 -- server/migrations/0025_v19_rooms.sql)"
  BASE="${ADD:+$ADD^}"; BASE="${BASE:-HEAD}"
fi
git cat-file -e "$BASE:server/migrations/0025_v19_rooms.sql" 2>/dev/null && die "BASE $BASE already has 0025 — set BASE to a commit before it"
OLD="$OUT/87-old-src"; rm -rf "$OLD"; mkdir -p "$OLD"
git archive "$BASE" server contracts | tar -x -C "$OLD"
(cd "$OLD/server" && GOWORK=off go build -o "$OUT/87-server-old" ./cmd/server) || die "old server build failed"
(cd server && go build -o "$OUT/87-server-new" ./cmd/server && go build -o "$OUT/87-migrate-new" ./cmd/migrate) || die "new build failed"
chk 0.1 no "$( [ -f "$OLD/server/migrations/0025_v19_rooms.sql" ] && echo yes || echo no )" "옛 소스($(git rev-parse --short "$BASE"))에는 0025 가 없다"
docker exec -i "$PG_CONTAINER" psql -U colab -d colab -qc "DROP DATABASE IF EXISTS $DB WITH (FORCE)" >/dev/null
docker exec -i "$PG_CONTAINER" psql -U colab -d colab -qc "CREATE DATABASE $DB" >/dev/null
start_server "$OUT/87-server-old" "$OLD_PORT" "$OUT/87-old.pid" "$OUT/87-server-old.log"
chk 0.2 24 "$(psqlq "select max(version) from schema_migrations")" "옛 서버가 0024 까지 적용했다"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. 옛 서버로 시드"
signup "r1a-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "R1a $RUN")"
LEAD="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc '{name:"Lead",role:"lead",role_description:"이끈다",instructions:"짧게",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id)"
REV="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc '{name:"Rev",role:"reviewer",role_description:"검토한다",instructions:"짧게",
  profiles:[{name:"default",runtime_kind:"claude_code",model:"claude-sonnet-5",is_default:true}]}')" | jq -r .id)"
IFS=$'\t' read -r RID DTOK <<<"$(pair_curl "$WS" "mac-r1a" '[{"kind":"claude_code","version":"1.0.0","logged_in":true,"models":["claude-sonnet-5"],"protocol_version":1,"resume":true,"usage":true,"tool_disallow":true,"brief_transport":"acp_meta_system_prompt","allow_once_missing":false}]' "/tmp/colab-r1a-$RUN")"
export DTOK RID
# deputy 로 쓸 두 번째 멤버(초대 흐름은 이 스크립트의 대상이 아니다 — 옛 스키마에 직접)
DEP="$(psqlq "insert into app_user (email, display_name) values ('dep-$RUN@example.com', 'Dep') returning id" | head -1)"
psqlq "insert into member (workspace_id, user_id, role) values ('$WS', '$DEP', 'member')" >/dev/null
mk() { api_ok POST "/workspaces/$WS/sessions" "$1" | jq -r .id; }   # 옛 서버(0024) 전용 — createSession 은 그 바이너리의 표면(R4 이관 대상 아님)
S1="$(mk "$(jq -nc --arg a "$LEAD" --arg r "$REV" --arg rt "$RID" --arg d "$DEP" \
  '{title:"R1a 진행",goal:"첫 줄 목표\n둘째 줄",acceptance_criteria:["하나","둘"],isolation:{kind:"none"},participants:[{agent_id:$a},{agent_id:$r}],
    assignee_agent_id:$a,runtime_id:$rt,deputy_director_user_id:$d,limits:{budget_usd:5,max_parallel_lanes:3}}')")"
S2="$(mk "$(jq -nc --arg a "$LEAD" '{title:"R1a 초안",goal:"초안 목표",isolation:{kind:"none"},participants:[{agent_id:$a}],draft:true}')")"
S3="$(mk "$(jq -nc --arg a "$LEAD" '{title:"R1a 멈춤",goal:"멈출 목표",isolation:{kind:"none"},participants:[{agent_id:$a}]}')")"
S4="$(mk "$(jq -nc --arg a "$LEAD" '{title:"R1a 끝남",goal:"끝낼 목표",isolation:{kind:"none"},participants:[{agent_id:$a}]}')")"
S5="$(mk "$(jq -nc --arg a "$LEAD" '{title:"R1a 취소",goal:"취소할 목표",isolation:{kind:"none"},participants:[{agent_id:$a}]}')")"
for s in "$S1" "$S2" "$S3" "$S4" "$S5"; do [[ "$s" =~ ^[0-9a-f-]{36}$ ]] || die "세션 생성 실패: $s"; done
# 옛 서버(0024) — /sessions 가 그 바이너리의 표면이다(R4 이관 대상 아님).
api_ok POST "/sessions/$S1/messages" "$(jq -nc --arg m "$(mention Rev "$REV") 확인 부탁" '{content:$m}')" -H "Idempotency-Key: $(uuid)" >/dev/null
CL="$(daemon_api "runtimes/$RID/claim" '{"capacity":5,"wait_ms":0}')"
T1="$(jq -r --arg s "$S1" '[.tasks[]|select(.task.session_id==$s)][0].task.id // ""' <<<"$CL")"
[ -n "$T1" ] && daemon_api_code "tasks/$T1/attempts/1/phase" '{"phase":"running","pgid":4242}' >/dev/null
api_ok POST "/sessions/$S3/pause" '{}' >/dev/null
api_ok POST "/sessions/$S5/cancel" '{}' >/dev/null || api_ok POST "/sessions/$S5/cancel" '{"confirm":true}' >/dev/null
# 완료는 요약 경로(모델)를 타므로 데몬 없는 이 스택에서는 상태만 옛 스키마에 직접 둔다 — 이관이 보는 것은 행이다.
psqlq "update session set status='completed', finished_at=now(), updated_at=now() where id='$S4'" >/dev/null
psqlq "insert into decision (session_id, summary, rationale, source) values ('$S1', '방식 A 로 간다', '빠르다', 'agent')" >/dev/null
psqlq "insert into hitl_request (session_id, source, type, question, due_at, purpose) values ('$S1', 'system', 'approval', '예산을 올릴까요?', now() + interval '1 day', 'user_approval')" >/dev/null
psqlq "insert into artifact (session_id, name, type, storage_ref, size_bytes) values ('$S1', 'notes.md', 'file', 'inline:r1a', 3)" >/dev/null
psqlq "insert into workdir (session_id, agent_id, kind, path_or_ref, disk_bytes) values ('$S1', '$LEAD', 'dir', '/tmp/colab-r1a-$RUN/s1/lead', 10)" >/dev/null
chk A.1 5 "$(psqlq "select count(*) from session where workspace_id='$WS'")" "세션 5개"
chk A.2 "active cancelled completed draft paused" "$(psqlq "select string_agg(status::text, ' ' order by status::text) from session where workspace_id='$WS'")" "상태 다섯 가지"
chk_ge A.3 1 "$(psqlq "select count(*) from task where session_id='$S1'")" "S1 에 task(메시지 트리거)"

snap() { # DIR [new] — 옛·새 서버에 같은 GET 을 던져 jq -S 로 저장. new 면 R4 새 서버: 옮겨진 op 만 /rooms/{id}/… 로
  local d="$1" mode="${2:-old}" s p
  [ "$mode" = new ] || api_ok GET "/workspaces/$WS/sessions" | jq -S . > "$d/list.json"   # 옛 서버만(listSessions 는 R4 에서 삭제)
  api_ok GET "/workspaces/$WS/cost" | jq -S . > "$d/ws-cost.json"
  # v0.2.0 이 InboxItem 에 더한 칸(room_id · work_id · lane_id · recipient_basis, T-R1b3)은 옛 서버에 없다 — 옛 칸만 대조.
  api_ok GET "/inbox?workspace_id=$WS" | jq -S 'del(.items[].room_id, .items[].work_id, .items[].lane_id, .items[].recipient_basis)' > "$d/inbox.json"
  [ -z "$T1" ] || api_ok GET "/tasks/$T1" | jq -S . > "$d/task-t1.json"   # listSessionTasks 는 501 이라 claim 한 task 하나
  for s in "$S1" "$S2" "$S3" "$S4" "$S5"; do
    if [ "$mode" = new ]; then
      for p in /messages /lanes /hitl-requests /artifacts /decisions /cost; do
        api_ok GET "/rooms/$s$p" | jq -S . > "$d/${s:0:8}${p//\//_}.json"
      done
    else
      for p in "" /participants /messages /lanes /hitl-requests /artifacts /decisions /cost; do
        api_ok GET "/sessions/$s$p" | jq -S . > "$d/${s:0:8}${p//\//_}.json"   # 옛 서버(0024)
      done
    fi
  done
}
snap "$SNAP/pre"
chk A.4 44 "$(ls "$SNAP/pre" | wc -l | tr -d ' ')" "이관 전 응답 44개(목록·비용·인박스·task + 세션 5 × 8)"
stop_pid "$OUT/87-old.pid"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. verify_0025_pre → 0025 적용 → verify_0025"
psqlf server/migrations/verify/verify_0025_pre.sql >/dev/null || die "verify_0025_pre failed"
psqlq "select tbl||'='||n from verify_0025.counts order by tbl" | tr '\n' ' ' > "$OUT/87-pre-counts.txt"; echo >> "$OUT/87-pre-counts.txt"
COLAB_DB_URL="$DBURL" "$OUT/87-migrate-new" > "$OUT/87-migrate.log" 2>&1 || die "migrate failed (see $OUT/87-migrate.log)"
chk B.1 1 "$(psqlq "select count(*) from schema_migrations where version = 25")" "HEAD 가 0025 를 적용했다(뒤 번호도 함께 — R1b 가 더한다)"
psqlf server/migrations/verify/verify_0025.sql > "$OUT/87-verify.tsv" || die "verify_0025 failed"
chk_ge B.2 40 "$(wc -l < "$OUT/87-verify.tsv" | tr -d ' ')" "검사 행 수"
chk B.3 "" "$(awk -F'\t' '$2 != 0 {printf "%s=%s; ", $1, $2}' "$OUT/87-verify.tsv")" "verify_0025 의 모든 검사 0 (어긋난 것만 나열)"
chk B.4 "5/5/5" "$(psqlq "select (select count(*) from room where workspace_id='$WS')||'/'||(select count(*) from work w join room r on r.id=w.room_id where r.workspace_id='$WS')||'/'||(select count(*) from room_participant p join room r on r.id=p.room_id where r.workspace_id='$WS' and p.role='owner')")" "방 5 · 미션 5 · 방장 5"
chk B.5 "active:active cancelled:archived completed:archived draft:active paused:active" "$(psqlq "select string_agg(w.status::text||':'||r.status::text, ' ' order by w.status::text) from room r join work w on w.room_id=r.id where r.workspace_id='$WS'")" "완료·취소 → 보관, 나머지 active(§10)"
chk B.6 "첫 줄 목표" "$(psqlq "select description from room where id='$S1'")" "방 설명 = goal 첫 줄"
chk B.7 "$DEP" "$(psqlq "select user_id from room_participant where room_id='$S1' and user_id='$DEP' and role='member'")" "deputy 는 방 참여자(member)"

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. 새 서버 — 같은 GET 이 JSON 동일"
export SERVER_URL="http://localhost:$NEW_PORT"; API="$SERVER_URL/api/v1"
start_server "$OUT/87-server-new" "$NEW_PORT" "$OUT/87-new.pid" "$OUT/87-server-new.log"
snap "$SNAP/post" new
DIFFS=""
for f in "$SNAP"/post/*.json; do   # R4: 새 서버에 남은 op 만 — 그 파일들을 이관 전 스냅숏과 대조
  b="$(basename "$f")"
  cmp -s "$SNAP/pre/$b" "$f" || DIFFS="$DIFFS $b"
done
chk C.1 "" "${DIFFS# }" "옮겨진 op 응답 33개(새 /rooms/{id}/… · 비용·인박스·task)가 이관 전 옛 /sessions/{id}/… 과 JSON 동일(jq -S) — 다른 파일만 나열"
[ -z "$DIFFS" ] || for b in $DIFFS; do diff "$SNAP/pre/$b" "$SNAP/post/$b" | head -20 >&2; done
chk C.2 33 "$(ls "$SNAP/post" | wc -l | tr -d ' ')" "이관 뒤 응답 33개(워크스페이스 비용·인박스·task + 방 5 × 옮겨진 op 6)"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 이관 뒤 쓰기 경로"
chk D.1 201 "$(api POST "/rooms/$S1/messages" "$(jq -nc '{content:"이관 뒤 메시지"}')" -H "Idempotency-Key: $(uuid)" | api_code)" "옛 세션에 메시지 게시"
# R4: 새 서버에는 createSession 이 없다 — 방+미션(create_room_work). createRoom 의 설명은 helper 가 비워 둔다(옛 createSession 은 goal 첫 줄).
S6="$(create_room_work "$WS" "$(jq -nc --arg a "$LEAD" '{title:"R1a 새 세션",goal:"새 목표\n둘째",isolation:{kind:"none"},participants:[{agent_id:$a}]}')")"
chk D.2 "R1a 새 세션//1/1/owner" "$(psqlq "select r.name||'/'||r.description||'/'||(select count(*) from work where room_id=r.id)||'/'||(select count(*) from room_participant where room_id=r.id and agent_id is not null)||'/'||(select string_agg(role::text, ',') from room_participant where room_id=r.id and user_id is not null) from room r where r.id='$S6'")" "새 방+미션 = 방(이름·빈 설명) + 미션 1 + 에이전트 1 + 방장 1"
chk D.3 "active" "$(api_ok GET "/works/$(work_of "$S6")" | jq -r .status)" "새 미션을 getWork 로 읽는다(R4: getSession 삭제)"
chk D.4 "frozen" "$(psqlq "update session_participant set joined_at = now()" 2>&1 | grep -o frozen | head -1)" "옛 session_participant 는 쓰기 금지(정본은 room_participant)"

stop_pid "$OUT/87-new.pid"
step "결과"
awk -F'\t' '{print $2}' "$CHECKS" | sort | uniq -c >&2
[ "$FAILS" = 0 ] || die "87: $FAILS FAIL — $CHECKS"
ok "87 PASS ($(wc -l < "$CHECKS" | tr -d ' ') checks)"
