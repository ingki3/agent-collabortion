#!/usr/bin/env bash
# P5 (T-W6) 계약 왕복 스모크 — **에이전트 턴 0**. S14 설정 8탭 + 대시보드 · S10 시험 대화가 부르는 operation 을 계약 모양대로
# 왕복시킨다. `e2e/p3-mock.sh`·`p4-mock.sh` 와 같은 구조: 목(COLAB_MOCK_API=1)으로도, 실서버(:8080 프록시)로도 같은 스크립트가
# 돌아 T-S12 머지 뒤 Lead 가 BASE_URL 만 바꿔 **목 ↔ 실서버 응답을 대조**할 수 있다. 목에만 있는 시드(`/__mock/...`)는 MOCK=1 일 때만.
#
# 재는 것(openapi 0.1.2):
#   S14  getWorkspaceSettings 200(멤버 읽기) · updateWorkspaceSettings 부분 갱신(다른 키 보존, S-26) · 422 errors[] · 멤버 403
#   S14  listMembers · updateMemberRole 200 · 마지막 owner 409 · createInvite 201 · owner 역할 422 · revokeInvite 204
#   S14  get/updateNotificationSettings(개인)
#   S14  getWorkspaceMetrics — 10개 · §11 열 순서 · unit/target_op enum · value null ⇒ n 0
#   S10  createTestChat 201(세션 0개) · postTestChatTurn 202 · 진행 중 409 · SSE test_chat.delta/turn · closeTestChat 200 · 닫힌 뒤 410
#   S5   deleteSession(T-W13, 계약 #218 · 서버 T-S17 대조) — active 409 session_active · member 403 · cancelled 204 · 두 번째 404 · SSE session.deleted
#        · (MOCK=1) 미병합 worktree 409 workdir_unmerged + Problem.workdirs[]
#   S6·S7 종료 조건(T-W15, 계약 #232 0.1.4 · 서버 T-S18 대조) — createSession 422 reviewer_required/reviewer_not_participant · 진행률 conditions[] 키
#        · updateSession completion_condition 은 active 에서도(Director) + SSE session.completion_progress · 403 · 끝난 세션 422 immutable · isolation 422 immutable
#        · (MOCK=1) 리뷰어 없는 옛 세션 시드 → blocked_reason reviewer_missing → 조건 고치기로 해소(met 유지)
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3117 &
#   BASE_URL=http://localhost:3117 MOCK=1 bash e2e/p5-mock.sh
set -uo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
MOCK="${MOCK:-1}"
B="$BASE_URL/api/v1"
J="$(mktemp -t colab-p5-cookies)"
trap 'rm -f "$J"' EXIT
fail=0
say() { printf '%-64s %s\n' "$1" "$2"; }
chk() { if [ "$2" = "$3" ]; then say "$1" "OK ($2)"; else say "$1" "FAIL got=$2 want=$3"; fail=1; fi; }
py() { python3 -c "$1"; }
code() { curl -sS -b "$J" -o /dev/null -w '%{http_code}' "$@"; }

EMAIL="${E2E_EMAIL:-demo@colab.dev}"
PASSWORD="${E2E_PASSWORD:-password123}"

[ "$MOCK" = "1" ] && curl -sS -X POST "$B/__mock/reset" -o /dev/null

LOGIN=$(curl -sS -c "$J" -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/auth/login" -H 'content-type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")
chk "로그인" "$LOGIN" "200"
WS=$(curl -sS -b "$J" "$B/me" | py 'import sys,json;print(json.load(sys.stdin)["workspaces"][0]["id"])')
ME=$(curl -sS -b "$J" "$B/me" | py 'import sys,json;print(json.load(sys.stdin)["user"]["id"])')

# ── S14 워크스페이스 설정 ──────────────────────────────────────────────────
S=$(curl -sS -b "$J" "$B/workspaces/$WS/settings")
chk "getWorkspaceSettings 기본값 loop 8/60/5 (PRD §7)" "$(echo "$S" | py 'import sys,json;l=json.load(sys.stdin)["loop_limits"];print(l["max_chain_depth"],l["max_hops_per_hour"],l["max_pair_roundtrips"])')" "8 60 5"
chk "  retention 14 · grace P7D · masking false" "$(echo "$S" | py 'import sys,json;s=json.load(sys.stdin);print(s["workdir_retention_days"],s["runtime_offline_grace"],s["task_event_masking"])')" "14 P7D False"
P=$(curl -sS -b "$J" -X PATCH "$B/workspaces/$WS/settings" -H 'content-type: application/json' -d '{"loop_limits":{"max_pair_roundtrips":2}}')
chk "updateWorkspaceSettings 부분 갱신 — 다른 키 보존 (S-26)" "$(echo "$P" | py 'import sys,json;l=json.load(sys.stdin)["loop_limits"];print(l["max_chain_depth"],l["max_hops_per_hour"],l["max_pair_roundtrips"])')" "8 60 2"
V=$(curl -sS -b "$J" -X PATCH "$B/workspaces/$WS/settings" -H 'content-type: application/json' -d '{"loop_limits":{"max_chain_depth":0}}')
chk "  0 은 422 + errors[].field" "$(echo "$V" | py 'import sys,json;d=json.load(sys.stdin);print(d["status"],d["errors"][0]["field"])')" "422 loop_limits.max_chain_depth"
chk "  workdir_disk_quota_gb null 로 지움" "$(curl -sS -b "$J" -X PATCH "$B/workspaces/$WS/settings" -H 'content-type: application/json' -d '{"workdir_disk_quota_gb":null}' | py 'import sys,json;print(json.load(sys.stdin)["workdir_disk_quota_gb"])')" "None"
curl -sS -b "$J" -X PATCH "$B/workspaces/$WS/settings" -H 'content-type: application/json' -d '{"loop_limits":{"max_pair_roundtrips":5}}' -o /dev/null

# ── S14 멤버 · 초대 ───────────────────────────────────────────────────────
MEMS=$(curl -sS -b "$J" "$B/workspaces/$WS/members")
MYM=$(echo "$MEMS" | py "import sys,json;print(next(m['id'] for m in json.load(sys.stdin)['items'] if m['user']['id']=='$ME'))")
OTHER=$(echo "$MEMS" | py "import sys,json;print(next((m['id'] for m in json.load(sys.stdin)['items'] if m['user']['id']!='$ME'),''))")
chk "listMembers 에 나(owner)가 있다" "$(echo "$MEMS" | py "import sys,json;print(next(m['role'] for m in json.load(sys.stdin)['items'] if m['user']['id']=='$ME'))")" "owner"
chk "updateMemberRole 마지막 owner 강등은 409" "$(code -X PATCH "$B/workspaces/$WS/members/$MYM" -H 'content-type: application/json' -d '{"role":"member"}')" "409"
if [ -n "$OTHER" ]; then
  chk "updateMemberRole member → admin 200" "$(curl -sS -b "$J" -X PATCH "$B/workspaces/$WS/members/$OTHER" -H 'content-type: application/json' -d '{"role":"admin"}' | py 'import sys,json;print(json.load(sys.stdin).get("role"))')" "admin"
  curl -sS -b "$J" -X PATCH "$B/workspaces/$WS/members/$OTHER" -H 'content-type: application/json' -d '{"role":"member"}' -o /dev/null
fi
INV=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/invites" -H 'content-type: application/json' -d '{"email":null,"role":"member","expires_in_hours":168}')
IID=$(echo "$INV" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "createInvite 201 → status pending · url 에 token" "$(echo "$INV" | py 'import sys,json;d=json.load(sys.stdin);print(d["status"], d["token"] in d["url"])')" "pending True"
chk "  owner 역할은 422" "$(code -X POST "$B/workspaces/$WS/invites" -H 'content-type: application/json' -d '{"role":"owner"}')" "422"
chk "revokeInvite 204" "$(code -X DELETE "$B/workspaces/$WS/invites/$IID")" "204"
chk "  목록에서 revoked" "$(curl -sS -b "$J" "$B/workspaces/$WS/invites" | py "import sys,json;print(next(i['status'] for i in json.load(sys.stdin) if i['id']=='$IID'))")" "revoked"

# ── S14 알림(개인) ────────────────────────────────────────────────────────
chk "getNotificationSettings 기본값" "$(curl -sS -b "$J" "$B/me/notification-settings" | py 'import sys,json;d=json.load(sys.stdin);print(d["email"],d["push"],d["default_subscription"])')" "True False all"
chk "updateNotificationSettings hitl_only" "$(curl -sS -b "$J" -X PATCH "$B/me/notification-settings" -H 'content-type: application/json' -d '{"email":true,"push":false,"default_subscription":"hitl_only"}' | py 'import sys,json;print(json.load(sys.stdin)["default_subscription"])')" "hitl_only"

# ── S14 대시보드 ──────────────────────────────────────────────────────────
M=$(curl -sS -b "$J" "$B/workspaces/$WS/metrics")
chk "getWorkspaceMetrics 10개 · §11 열 순서" "$(echo "$M" | py 'import sys,json;m=json.load(sys.stdin)["metrics"];print(len(m), [x["key"] for x in m]==["f1_minutes","auto_complete_rate","hitl_response_minutes","delegation_autonomous_rate","parallel_wallclock_reduction","task_success_rate_by_runtime","duplicate_after_resume_rate","resume_success_rate","blocked_response_minutes","weekly_active_sessions"])')" "10 True"
chk "  unit·target_op enum · value null ⇒ n 0" "$(echo "$M" | py 'import sys,json;m=json.load(sys.stdin)["metrics"];print(all(x["unit"] in ("minutes","ratio","count") and x["target_op"] in ("lt","gt") for x in m), all(x["n"]==0 for x in m if x["value"] is None))')" "True True"
chk "  breakdown 은 task_success_rate_by_runtime 에만" "$(echo "$M" | py 'import sys,json;m=json.load(sys.stdin)["metrics"];print([x["key"] for x in m if x.get("breakdown")])')" "['task_success_rate_by_runtime']"

# ── S10 시험 대화 ─────────────────────────────────────────────────────────
AG=$(curl -sS -b "$J" "$B/workspaces/$WS/agents" | py 'import sys,json;print(json.load(sys.stdin)["items"][0]["id"])')
BEFORE=$(curl -sS -b "$J" "$B/workspaces/$WS/sessions" | py 'import sys,json;print(len(json.load(sys.stdin)["items"]))')
TC=$(curl -sS -b "$J" -X POST "$B/agents/$AG/test-chats" -H 'content-type: application/json' -d '{"profile_id":null,"runtime_id":null}')
TCID=$(echo "$TC" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "createTestChat 201 → open · 턴 0 · transport null" "$(echo "$TC" | py 'import sys,json;d=json.load(sys.stdin);print(d["status"],len(d["turns"]),d.get("transport"))')" "open 0 None"
chk "  세션이 아니다 — 세션 수 그대로" "$(curl -sS -b "$J" "$B/workspaces/$WS/sessions" | py 'import sys,json;print(len(json.load(sys.stdin)["items"]))')" "$BEFORE"
# SSE 를 먼저 열어 두고 턴을 보낸다 — delta(ephemeral)는 백필되지 않는다.
SSE="$(mktemp -t colab-p5-sse)"
curl -sS -N -b "$J" --max-time 8 "$B/workspaces/$WS/stream" > "$SSE" 2>/dev/null &
SSEPID=$!
sleep 1
chk "postTestChatTurn 202" "$(code -X POST "$B/test-chats/$TCID/turns" -H 'content-type: application/json' -d '{"content":"안녕"}')" "202"
chk "  진행 중이면 409" "$(code -X POST "$B/test-chats/$TCID/turns" -H 'content-type: application/json' -d '{"content":"또"}')" "409"
wait $SSEPID 2>/dev/null || true
chk "  SSE test_chat.delta ≥ 1 · test_chat.turn = 1" "$(python3 - "$SSE" "$TCID" <<'PY'
import sys, json
raw = open(sys.argv[1]).read()
d = t = 0
for frame in raw.split("\n\n"):
    for line in frame.splitlines():
        if line.startswith("data: "):
            ev = json.loads(line[6:])
            if ev.get("payload", {}).get("test_chat_id") != sys.argv[2]: continue
            if ev["type"] == "test_chat.delta" and ev.get("ephemeral"): d += 1
            if ev["type"] == "test_chat.turn": t += 1
print(d >= 1, t)
PY
)" "True 1"
rm -f "$SSE"
G=$(curl -sS -b "$J" "$B/test-chats/$TCID")
chk "getTestChat — 턴 2 · transport acp|cli · 토큰 > 0" "$(echo "$G" | py 'import sys,json;d=json.load(sys.stdin);print(len(d["turns"]), d["transport"] in ("acp","cli"), d["input_tokens"]>0 and d["output_tokens"]>0)')" "2 True True"
chk "closeTestChat 200 → closed" "$(curl -sS -b "$J" -X POST "$B/test-chats/$TCID/close" | py 'import sys,json;print(json.load(sys.stdin)["status"])')" "closed"
chk "  닫힌 뒤 턴은 410" "$(code -X POST "$B/test-chats/$TCID/turns" -H 'content-type: application/json' -d '{"content":"x"}')" "410"
chk "  닫기는 멱등 200" "$(code -X POST "$B/test-chats/$TCID/close")" "200"

# ── S5 세션 삭제 (T-W13 · 계약 #218 deleteSession · 서버 T-S17 대조용) ─────────
# 목이 흉내 낸 서버 응답: 진행 중 409 session_active · 권한 없는 member 403 · cancelled 204 · 두 번째 404 · SSE session.deleted {session_id}.
# 미병합 worktree 409 workdir_unmerged + Problem.workdirs[] 는 시드(`/__mock/sessions/{id}/seed-workdirs`)가 목에만 있어 MOCK=1 일 때만.
RT=$(curl -sS -b "$J" "$B/workspaces/$WS/runtimes" | py 'import sys,json;print(json.load(sys.stdin)[0]["id"])')
DS=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' \
  -d "{\"title\":\"지울 세션\",\"goal\":\"삭제 왕복\",\"isolation\":{\"kind\":\"none\"},\"runtime_id\":\"$RT\",\"participants\":[{\"agent_id\":\"$AG\"}],\"assignee_agent_id\":\"$AG\"}" \
  | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "deleteSession 진행 중(active) → 409 session_active" "$(curl -sS -b "$J" -X DELETE "$B/sessions/$DS" | py 'import sys,json;d=json.load(sys.stdin);print(d["status"],d["code"],d["detail"])')" "409 session_active 진행 중인 세션은 먼저 종료하세요"
curl -sS -b "$J" -X POST "$B/sessions/$DS/cancel" -H 'content-type: application/json' -d '{}' -o /dev/null
chk "  cancelled 뒤 목록에 status=cancelled" "$(curl -sS -b "$J" "$B/workspaces/$WS/sessions" | py "import sys,json;print(next(s['status'] for s in json.load(sys.stdin)['items'] if s['id']=='$DS'))")" "cancelled"
# 권한 — Director 도 owner·admin 도 아닌 member(서연) 는 403.
J2="$(mktemp -t colab-p5-cookies2)"
curl -sS -c "$J2" -o /dev/null -X POST "$B/auth/login" -H 'content-type: application/json' -d '{"email":"seoyeon@colab.dev","password":"password123"}'
chk "  Director 아닌 member → 403" "$(curl -sS -b "$J2" -o /dev/null -w '%{http_code}' -X DELETE "$B/sessions/$DS")" "403"
rm -f "$J2"
SSE="$(mktemp -t colab-p5-sse2)"
curl -sS -N -b "$J" --max-time 4 "$B/workspaces/$WS/stream" > "$SSE" 2>/dev/null &
SSEPID=$!
sleep 1
chk "  owner(Director) → 204" "$(code -X DELETE "$B/sessions/$DS")" "204"
chk "  두 번째 DELETE 는 404(멱등 아님)" "$(code -X DELETE "$B/sessions/$DS")" "404"
chk "  GET 도 404 · 목록에서 빠짐" "$(code "$B/sessions/$DS") $(curl -sS -b "$J" "$B/workspaces/$WS/sessions" | py "import sys,json;print(any(s['id']=='$DS' for s in json.load(sys.stdin)['items']))")" "404 False"
wait $SSEPID 2>/dev/null || true
chk "  SSE session.deleted {session_id}" "$(python3 - "$SSE" "$DS" <<'PY'
import sys, json
raw = open(sys.argv[1]).read()
hits = 0
for frame in raw.split("\n\n"):
    for line in frame.splitlines():
        if line.startswith("data: "):
            ev = json.loads(line[6:])
            if ev["type"] == "session.deleted" and ev.get("payload") == {"session_id": sys.argv[2]}: hits += 1
print(hits)
PY
)" "1"
rm -f "$SSE"
if [ "$MOCK" = "1" ]; then
  DS2=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' \
    -d "{\"title\":\"작업 폴더 남은 세션\",\"goal\":\"409 workdir_unmerged\",\"isolation\":{\"kind\":\"worktree\",\"repo_path\":\"/work/colab\"},\"runtime_id\":\"$RT\",\"participants\":[{\"agent_id\":\"$AG\"}],\"assignee_agent_id\":\"$AG\"}" \
    | py 'import sys,json;print(json.load(sys.stdin)["id"])')
  curl -sS -b "$J" -X POST "$B/sessions/$DS2/cancel" -H 'content-type: application/json' -d '{}' -o /dev/null
  curl -sS -b "$J" -X POST "$B/__mock/sessions/$DS2/seed-workdirs" -H 'content-type: application/json' -d '{}' -o /dev/null
  R=$(curl -sS -b "$J" -X DELETE "$B/sessions/$DS2")
  chk "  미병합·미커밋 worktree → 409 workdir_unmerged + workdirs[2] (경로·브랜치·사유)" "$(echo "$R" | py 'import sys,json;d=json.load(sys.stdin);w=d.get("workdirs",[]);print(d["status"],d["code"],len(w),sorted(x["gc_blocked_reason"] for x in w),all("path_or_ref" in x and "branch" in x for x in w))')" "409 workdir_unmerged 2 ['uncommitted_changes', 'unmerged_commits'] True"
  for WID in $(echo "$R" | py 'import sys,json;print(" ".join(x["id"] for x in json.load(sys.stdin)["workdirs"]))'); do curl -sS -b "$J" -X DELETE "$B/workdirs/$WID?force=true" -o /dev/null; done
  chk "  작업 폴더 정리 뒤 → 204" "$(code -X DELETE "$B/sessions/$DS2")" "204"
fi

# ── S6·S7 종료 조건 (T-W15 · 계약 #232 openapi 0.1.4 · 서버 T-S18 대조용) ─────────────────────
# 목이 흉내 낸 서버 응답: createSession/updateSession 의 리뷰어 검사(422 errors[].code reviewer_required · reviewer_not_participant,
# field completion_condition/conditions/<i>/agent_id) · 진행률 conditions[] 의 agent_id·agent_name·blocked_reason·hitl_request_id ·
# updateSession completion_condition 은 active 에서도(Director) + SSE session.completion_progress · 끝난 세션 422 immutable · 시작 뒤 isolation 422 immutable.
AG2=$(curl -sS -b "$J" "$B/workspaces/$WS/agents" | py 'import sys,json;print(json.load(sys.stdin)["items"][1]["id"])')
BODY() { echo "{\"title\":\"$1\",\"goal\":\"종료 조건\",\"isolation\":{\"kind\":\"none\"},\"runtime_id\":\"$RT\",\"participants\":[{\"agent_id\":\"$AG\"},{\"agent_id\":\"$AG2\"}],\"assignee_agent_id\":\"$AG2\",\"completion_condition\":$2}"; }
V=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' -d "$(BODY 'x' '{"op":"and","conditions":[{"type":"artifact_submitted","who":"assignee"},{"type":"agent_approval"}]}')")
chk "createSession agent_approval 에 agent_id 없음 → 422 reviewer_required · field 경로" "$(echo "$V" | py 'import sys,json;d=json.load(sys.stdin);e=d["errors"][0];print(d["status"],d["code"],e["code"],e["field"])')" "422 validation_failed reviewer_required completion_condition/conditions/1/agent_id"
V=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' -d "$(BODY 'x' '{"op":"and","conditions":[{"type":"agent_approval","agent_id":"00000000-0000-0000-0000-000000000000"}]}')")
chk "  참여자 아닌 리뷰어 → 422 reviewer_not_participant" "$(echo "$V" | py 'import sys,json;print(json.load(sys.stdin)["errors"][0]["code"])')" "reviewer_not_participant"
# bash 3.2 — 겹친 따옴표를 $( ) 안에 넣지 않는다(PR #212 함정): 조건 JSON 을 먼저 변수로.
CC="{\"op\":\"and\",\"conditions\":[{\"type\":\"artifact_submitted\",\"who\":\"assignee\"},{\"type\":\"agent_approval\",\"agent_id\":\"$AG\"},{\"type\":\"user_approval\"}]}"
CSBODY=$(BODY '종료 조건 세션' "$CC")
CS=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' -d "$CSBODY")
CSID=$(echo "$CS" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "  리뷰어가 참여자 → 201 · 진행률 3행 · 키 집합(계약 CompletionProgress.conditions[])" "$(echo "$CS" | py 'import sys,json;p=json.load(sys.stdin)["completion_progress"];print(p["total"],p["human_gate"],all(sorted(c)==["agent_id","agent_name","blocked_reason","hitl_request_id","met","met_at","met_by","next_actor","path","type"] for c in p["conditions"]))')" "3 True True"
chk "  agent_approval 행 — agent_id·agent_name(Lead)·blocked_reason null" "$(echo "$CS" | py "import sys,json;c=json.load(sys.stdin)['completion_progress']['conditions'][1];print(c['type'],c['agent_id']=='$AG',c['agent_name'],c['blocked_reason'])")" "agent_approval True Lead None"
chk "  artifact_submitted(who assignee) 행 — 담당 에이전트가 agent_id·next_actor" "$(echo "$CS" | py "import sys,json;c=json.load(sys.stdin)['completion_progress']['conditions'][0];print(c['agent_id']=='$AG2',c['next_actor']==c['agent_name'])")" "True True"
chk "updateSession 시작 뒤 isolation → 422 immutable(서버 문장)" "$(curl -sS -b "$J" -X PATCH "$B/sessions/$CSID" -H 'content-type: application/json' -d '{"isolation":{"kind":"none"}}' | py 'import sys,json;d=json.load(sys.stdin);print(d["status"],d["errors"][0]["field"],d["errors"][0]["code"])')" "422 isolation immutable"
chk "  completion_condition 리뷰어 없음 → 422 reviewer_required" "$(curl -sS -b "$J" -X PATCH "$B/sessions/$CSID" -H 'content-type: application/json' -d '{"completion_condition":{"op":"and","conditions":[{"type":"agent_approval"}]}}' | py 'import sys,json;print(json.load(sys.stdin)["errors"][0]["code"])')" "reviewer_required"
J3="$(mktemp -t colab-p5-cookies3)"
curl -sS -c "$J3" -o /dev/null -X POST "$B/auth/login" -H 'content-type: application/json' -d '{"email":"seoyeon@colab.dev","password":"password123"}'
chk "  Director 아닌 member → 403 director_required" "$(curl -sS -b "$J3" -X PATCH "$B/sessions/$CSID" -H 'content-type: application/json' -d '{"completion_condition":{"op":"and","conditions":[{"type":"manual"}]}}' | py 'import sys,json;d=json.load(sys.stdin);print(d["status"],d["code"])')" "403 director_required"
rm -f "$J3"
SSE="$(mktemp -t colab-p5-sse3)"
curl -sS -N -b "$J" --max-time 4 "$B/workspaces/$WS/stream" > "$SSE" 2>/dev/null &
SSEPID=$!
sleep 1
U=$(curl -sS -b "$J" -X PATCH "$B/sessions/$CSID" -H 'content-type: application/json' -d "{\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"artifact_submitted\",\"who\":\"assignee\"},{\"type\":\"agent_approval\",\"agent_id\":\"$AG2\"}]}}")
chk "  active 에서 Director 가 리뷰어 교체 → 200 · 진행률 재계산(2행 · 리뷰어 이름 바뀜)" "$(echo "$U" | py 'import sys,json;d=json.load(sys.stdin);p=d["completion_progress"];print(d["status"],p["total"],p["conditions"][1]["agent_name"],p["human_gate"])')" "active 2 Researcher False"
wait $SSEPID 2>/dev/null || true
chk "  SSE session.completion_progress {session_id, completion_progress} 1건" "$(python3 - "$SSE" "$CSID" <<'PY'
import sys, json
raw = open(sys.argv[1]).read()
hits = 0
for frame in raw.split("\n\n"):
    for line in frame.splitlines():
        if line.startswith("data: "):
            ev = json.loads(line[6:])
            if ev["type"] == "session.completion_progress" and sorted(ev.get("payload", {})) == ["completion_progress", "session_id"] and ev["payload"]["session_id"] == sys.argv[2]: hits += 1
print(hits)
PY
)" "1"
rm -f "$SSE"
if [ "$MOCK" = "1" ]; then
  L=$(curl -sS -b "$J" -X POST "$B/__mock/sessions/$CSID/seed-legacy-condition" -H 'content-type: application/json' -d '{"met_artifact":true}')
  chk "  (MOCK) 리뷰어 없는 옛 세션 시드 → blocked_reason reviewer_missing · 보고서 제출은 met" "$(echo "$L" | py 'import sys,json;c=json.load(sys.stdin)["completion_progress"]["conditions"];print(c[0]["met"],c[1]["blocked_reason"],c[1]["next_actor"])')" "True reviewer_missing None"
  F=$(curl -sS -b "$J" -X PATCH "$B/sessions/$CSID" -H 'content-type: application/json' -d "{\"completion_condition\":{\"op\":\"and\",\"conditions\":[{\"type\":\"artifact_submitted\",\"who\":\"assignee\"},{\"type\":\"agent_approval\",\"agent_id\":\"$AG\"}]}}")
  chk "  조건 고치기 → 막힘 해소 · 이미 충족된 원자(met) 유지" "$(echo "$F" | py 'import sys,json;p=json.load(sys.stdin)["completion_progress"];print(p["met"],p["conditions"][0]["met"],p["conditions"][1]["blocked_reason"],p["conditions"][1]["agent_name"])')" "1 True None Lead"
fi
curl -sS -b "$J" -X POST "$B/sessions/$CSID/cancel" -H 'content-type: application/json' -d '{}' -o /dev/null
chk "  끝난 세션의 completion_condition → 422 immutable" "$(curl -sS -b "$J" -X PATCH "$B/sessions/$CSID" -H 'content-type: application/json' -d '{"completion_condition":{"op":"and","conditions":[{"type":"manual"}]}}' | py 'import sys,json;d=json.load(sys.stdin);print(d["status"],d["errors"][0]["field"],d["errors"][0]["code"])')" "422 completion_condition immutable"

echo
if [ "$fail" = "0" ]; then echo "✅ P5 목 스모크 통과"; else echo "❌ P5 목 스모크 실패"; exit 1; fi
