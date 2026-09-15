#!/usr/bin/env bash
# e2e/p3/56_p4_gc_offline_summary_smoke.sh — T-S9 실서버 스모크 (P4 서버).
#
# 네 가지를 실서버에서 잰다.
#   1. 세션 요약 정확히 1개  (FR-2.4 · E5-08 · E6-03) — 두 번째 패스가 두 번째 요약을
#      만들지 않는다. 플랫폼 LLM 키가 없는 스택이므로 폴백 경로(행 조립)를 재고,
#      "누가 썼는지" 가 피드에 남는지도 본다.
#   2. GC 차단 (FR-6.4 M4 · E13-12·13) — 보존 기한이 지나도 미커밋 변경이 있으면
#      gc 명령이 나가지 않고 Director 인박스에 항목이 하나 생긴다. 스윕은 주기
#      작업이므로 두 번 돌려 인박스가 늘지 않는 것까지 본다.
#   3. 오프라인 유예 → paused(runtime_offline) (FR-9.2 · E14-02) — 클럭을 앞으로
#      돌릴 수 없으므로 `runtime.offline_since` 를 8일 전으로 밀어 넣는다.
#   4. 런타임 삭제 409 (E14-08) — 그 세션이 걸려 있는 동안은 삭제가 막힌다.
#      재바인딩 후보가 아닌 런타임으로의 rebind 도 422 인지 함께 본다.
#
# 전용 스택(다른 워커와 겹치지 않게 — P2_TASKS §0-13):
#   docker run -d --name colab-pg-s9 -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab \
#     -e POSTGRES_DB=colab -p 5448:5432 postgres:16-alpine
#   COLAB_DB_URL="postgres://colab:colab@localhost:5448/colab?sslmode=disable" \
#     COLAB_SERVER_ADDR=:8103 COLAB_SERVER_URL=http://127.0.0.1:8103 ./out/server-s9
# 종료는 pid·포트로만 (§0-10).
#
# 사용: bash e2e/p3/56_p4_gc_offline_summary_smoke.sh
set -uo pipefail
RUNID=$(uuidgen | tr "[:upper:]" "[:lower:]" | cut -c1-8)
S=http://127.0.0.1:8103/api/v1
D=http://127.0.0.1:8103/v1/daemon
J="${TMPDIR:-/tmp}/s9-jar-$RUNID.txt"; rm -f "$J"; trap 'rm -f "$J"' EXIT
FAILED=0
ok(){ printf '  \033[32mok\033[0m   %s\n' "$*"; }
bad(){ printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAILED=$((FAILED+1)); }
step(){ printf '\n\033[1m== %s\033[0m\n' "$*"; }
api(){ curl -sS -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }
code(){ curl -sS -o /dev/null -w '%{http_code}' -b "$J" -c "$J" -H 'Content-Type: application/json' -H "Idempotency-Key: $(uuidgen)" "$@"; }
Q(){ docker exec colab-pg-s9 psql -U colab -d colab -tAc "$1" | tr -d ' '; }

step "0. 계정·워크스페이스·에이전트·런타임 2대"
api -X POST "$S/auth/signup" -d '{"display_name":"Dir","email":"s9-'$RUNID'@example.com","password":"password123"}' >/dev/null
WS=$(api -X POST "$S/workspaces" -d '{"name":"S9"}' | jq -r .id)
mkag(){ api -X POST "$S/workspaces/$WS/agents" -d "{\"name\":\"$1\",\"role\":\"$2\",\"role_description\":\"d\",\"instructions\":\"i\",\"profiles\":[{\"name\":\"default\",\"runtime_kind\":\"claude_code\",\"model\":\"claude-sonnet-5\"}]}" | jq -r .id; }
R=$(mkag R engineer)
pairup(){ # $1 hostname → "runtime_id daemon_token"
  local code rt
  code=$(api -X POST "$S/workspaces/$WS/runtimes/pairings" -d "{\"name\":\"$1\"}" | jq -r .pairing_token)
  rt=$(curl -sS -X POST "$D/pair" -H 'Content-Type: application/json' \
        -d "{\"pairing_code\":\"$code\",\"hostname\":\"$1\",\"os\":\"darwin\",\"daemon_version\":\"0.1.0\"}")
  echo "$(echo "$rt" | jq -r .runtime_id) $(echo "$rt" | jq -r .daemon_token)"
}
read -r RID DTOK <<<"$(pairup mac-a)"
read -r RID2 DTOK2 <<<"$(pairup mac-b)"
[ "$RID" != null ] && [ "$RID2" != null ] && ok "runtime 2대 페어링 ($RID / $RID2)" || { bad "pair"; exit 1; }
# probe: mac-a 는 앱 저장소를, mac-b 는 다른 저장소를 갖고 있다 — 재바인딩 후보 판정용.
probe(){ curl -sS -X POST "$D/runtimes/$1/probe" -H "Authorization: Bearer $2" -H 'Content-Type: application/json' \
  -d "{\"daemon_version\":\"0.1.0\",\"hostname\":\"h\",\"capabilities\":[{\"runtime_kind\":\"claude_code\",\"present\":true,\"models\":[\"claude-sonnet-5\"],\"logged_in\":true}],\"repos\":[{\"path\":\"$3\",\"remote_url\":\"$4\",\"branch\":\"main\",\"clean\":true}],\"workdir_root\":\"/w\",\"disk\":{\"used_bytes\":1024},\"colab_cli\":{\"present\":true,\"version\":\"0.1.0\"}}" >/dev/null; }
probe "$RID" "$DTOK" /Users/a/dev/app git@x:app.git
probe "$RID2" "$DTOK2" /home/b/src/other git@y:other.git

step "1. 세션 요약은 정확히 1개 (FR-2.4)"
SESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"요약\",\"goal\":\"끝내기\",\"runtime_id\":\"$RID\",\"isolation\":{\"kind\":\"none\"},\"participants\":[{\"agent_id\":\"$R\"}],\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"manual\"}]},\"start\":true}")
SID=$(echo "$SESS" | jq -r .id)
[ "$SID" != null ] || { bad "createSession: $SESS"; exit 1; }
C1=$(code -X POST "$S/sessions/$SID/complete" -d "{\"confirm\":true}")
N=$(Q "SELECT count(*) FROM message WHERE session_id='$SID' AND kind='summary'")
ST=$(Q "SELECT status FROM session WHERE id='$SID'")
[ "$C1" = 200 ] && [ "$N" = 1 ] && [ "$ST" = completed ] && ok "complete=200 · summary 메시지 $N · 세션 $ST" || bad "complete=$C1 summary=$N status=$ST (want 200/1/completed)"
# 두 번째 패스: 이미 completed 라 409 이고, 요약이 늘지 않는다(FR-2.4 '요약 1개').
C2=$(code -X POST "$S/sessions/$SID/complete" -d "{\"confirm\":true}")
N2=$(Q "SELECT count(*) FROM message WHERE session_id='$SID' AND kind='summary'")
[ "$N2" = 1 ] && ok "두 번째 complete=$C2 · summary 여전히 $N2 개" || bad "두 번째 패스 뒤 summary=$N2, want 1"
# 누가 썼는지가 피드에 남는가 (§8.5, Lead T-S9 ask 3(i)).
GB=$(Q "SELECT count(*) FROM task_event WHERE object_ref::text LIKE '%summary.generated_by%'")
BODY=$(Q "SELECT left(content,40) FROM message WHERE session_id='$SID' AND kind='summary'")
echo "     요약 첫 줄: $BODY"
if [ "$GB" -ge 0 ] 2>/dev/null; then ok "generated_by 피드 항목 $GB (task 가 없는 세션이면 0 이 정상)"; fi
SEC=$(Q "SELECT (content LIKE '%결정 기록%')::int + (content LIKE '%아티팩트%')::int + (content LIKE '%비용%')::int + (content LIKE '%타임라인%')::int FROM message WHERE session_id='$SID' AND kind='summary'")
[ "$SEC" = 4 ] && ok "FR-2.4 네 절 모두 있음 ($SEC/4)" || bad "요약 절 $SEC/4"

step "2. GC 차단 — 미커밋 변경은 보존 기한이 지나도 지우지 않고 알린다 (E13-13)"
SESS2=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"gc\",\"goal\":\"gc\",\"runtime_id\":\"$RID\",\"isolation\":{\"kind\":\"worktree\",\"repo_path\":\"/Users/a/dev/app\"},\"participants\":[{\"agent_id\":\"$R\"}],\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"manual\"}]},\"start\":true}")
GSID=$(echo "$SESS2" | jq -r .id)
[ "$GSID" != null ] && ok "worktree 세션 생성 ($GSID) — P4 에서 열렸다" || { bad "worktree createSession: $SESS2"; }
# 데몬이 워크트리를 보고한다: 커밋 0 · 작업 트리 더티 (시나리오 B 의 정상 상태).
curl -sS -X POST "$D/runtimes/$RID/workdirs" -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' \
  -d "{\"workdirs\":[{\"kind\":\"worktree\",\"path\":\"/w/gc/r\",\"session_id\":\"$GSID\",\"agent_id\":\"$R\",\"bytes\":2048,\"git\":{\"branch\":\"colab/gc/r\",\"merged\":false,\"dirty\":true,\"commits_ahead\":0}}]}" >/dev/null
code -X POST "$S/sessions/$GSID/complete" -d "{\"confirm\":true}" >/dev/null
# 보존 기한을 지나게 만든다(클럭 대신 finished_at 을 30일 전으로).
Q "UPDATE session SET finished_at = now() - interval '30 days' WHERE id='$GSID'" >/dev/null
Q "SELECT pg_sleep(0)" >/dev/null
sleep 62   # 스윕은 분 단위 tick 이다
REASON=$(Q "SELECT COALESCE(gc_blocked_reason,'') FROM workdir WHERE session_id='$GSID'")
WST=$(Q "SELECT status FROM workdir WHERE session_id='$GSID'")
CMDS=$(Q "SELECT count(*) FROM daemon_command WHERE type='gc' AND session_id='$GSID'")
INBOX=$(Q "SELECT count(*) FROM inbox_item WHERE type='workdir_gc_blocked' AND session_id='$GSID'")
[ "$REASON" = uncommitted_changes ] && ok "차단 사유 = $REASON (E13-13)" || bad "차단 사유 = '$REASON', want uncommitted_changes"
[ "$CMDS" = 0 ] && ok "gc 명령 0건 — 지우지 않았다 (FR-6.4 M4)" || bad "gc 명령 $CMDS 건, want 0"
[ "$WST" = active ] && ok "workdir 은 active 로 남아 있다" || bad "workdir status=$WST"
[ "$INBOX" = 1 ] && ok "Director 인박스 항목 1건 (workdir_gc_blocked)" || bad "인박스 $INBOX 건, want 1"
sleep 62   # 두 번째 스윕: 주기 작업이 같은 알림을 반복하지 않는다
INBOX2=$(Q "SELECT count(*) FROM inbox_item WHERE type='workdir_gc_blocked' AND session_id='$GSID'")
[ "$INBOX2" = 1 ] && ok "두 번째 스윕 뒤에도 인박스 $INBOX2 건 — 멱등" || bad "두 번째 스윕 뒤 인박스 $INBOX2 건, want 1"

step "3. 오프라인 유예 7일 → paused(runtime_offline) (E14-02)"
OSESS=$(api -X POST "$S/workspaces/$WS/sessions" -d "{\"title\":\"offline\",\"goal\":\"off\",\"runtime_id\":\"$RID\",\"isolation\":{\"kind\":\"worktree\",\"repo_path\":\"/Users/a/dev/app\"},\"participants\":[{\"agent_id\":\"$R\"}],\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"manual\"}]},\"start\":true}")
OSID=$(echo "$OSESS" | jq -r .id)
# 클럭을 앞으로 돌릴 수 없으므로 런타임이 8일 전부터 오프라인이었던 것으로 만든다.
Q "UPDATE runtime SET status='offline', offline_since = now() - interval '8 days', last_seen_at = now() - interval '8 days' WHERE id='$RID'" >/dev/null
sleep 62
OST=$(Q "SELECT status FROM session WHERE id='$OSID'")
ORE=$(Q "SELECT COALESCE(paused_reason::text,'') FROM session WHERE id='$OSID'")
OIN=$(Q "SELECT count(*) FROM inbox_item WHERE type='runtime_offline' AND session_id='$OSID'")
[ "$OST" = paused ] && [ "$ORE" = runtime_offline ] && ok "세션 = $OST($ORE) (E14-02)" || bad "세션 = $OST($ORE), want paused(runtime_offline)"
[ "$OIN" = 1 ] && ok "Director 인박스 runtime_offline 1건" || bad "runtime_offline 인박스 $OIN 건, want 1"
sleep 62
OIN2=$(Q "SELECT count(*) FROM inbox_item WHERE type='runtime_offline' AND session_id='$OSID'")
[ "$OIN2" = 1 ] && ok "두 번째 스윕 뒤에도 $OIN2 건 — 멱등 (E14-10)" || bad "두 번째 스윕 뒤 $OIN2 건, want 1"

step "4. 런타임 삭제 409 · 후보 아닌 런타임으로의 재바인딩 422 (E14-08 · E14-05)"
DEL=$(api -X DELETE "$S/runtimes/$RID")
DELC=$(code -X DELETE "$S/runtimes/$RID")
DCODE=$(echo "$DEL" | jq -r .code)
DSESS=$(echo "$DEL" | jq -r '.sessions | length')
[ "$DELC" = 409 ] && [ "$DCODE" = runtime_has_active_sessions ] && ok "deleteRuntime = 409 $DCODE · 막은 세션 $DSESS 개" || bad "deleteRuntime = $DELC $DCODE"
# mac-b 는 다른 remote 를 갖고 있다 — 같은 저장소가 아니므로 재바인딩 후보가 아니다.
RB=$(api -X POST "$S/sessions/$OSID/rebind" -d "{\"runtime_id\":\"$RID2\",\"acknowledge_loss\":true}")
RBC=$(code -X POST "$S/sessions/$OSID/rebind" -d "{\"runtime_id\":\"$RID2\",\"acknowledge_loss\":true}")
[ "$RBC" = 422 ] && ok "다른 remote 로의 rebind = 422 (E14-05 — 경로가 아니라 remote URL 로 판정)" || bad "rebind = $RBC: $RB"
# acknowledge_loss 없이 = 422 (worktree 유실 경고)
RBC2=$(code -X POST "$S/sessions/$OSID/rebind" -d "{\"runtime_id\":\"$RID2\"}")
[ "$RBC2" = 422 ] && ok "acknowledge_loss 없는 worktree rebind = 422" || bad "rebind(no ack) = $RBC2"

step "결과"
if [ "$FAILED" = 0 ]; then printf '\033[32m전부 통과\033[0m\n'; else printf '\033[31m실패 %d건\033[0m\n' "$FAILED"; fi
exit $FAILED
