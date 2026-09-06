#!/usr/bin/env bash
# P2 계약 왕복 스모크 — **에이전트 턴 0**. 웹이 부르는 P2 operation 을 계약 모양대로 왕복시킨다.
#
# 목 API(COLAB_MOCK_API=1)로도, 실서버(:8080 프록시)로도 같은 스크립트가 돈다 — 그래서 통합(T-I2)이 서버를
# 바꾸지 않고 이 파일의 BASE_URL 만 바꿔 대조할 수 있다. 목에만 있는 경로(`/__mock/...`)는 MOCK=1 일 때만 돈다.
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3111 &   # 또는 실서버
#   BASE_URL=http://localhost:3111 MOCK=1 bash e2e/p2-mock.sh
set -uo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3111}"
MOCK="${MOCK:-1}"
B="$BASE_URL/api/v1"
J="$(mktemp -t colab-p2-cookies)"
trap 'rm -f "$J"' EXIT
fail=0
say() { printf '%-46s %s\n' "$1" "$2"; }
chk() { if [ "$2" = "$3" ]; then say "$1" "OK ($2)"; else say "$1" "FAIL got=$2 want=$3"; fail=1; fi; }

EMAIL="${E2E_EMAIL:-demo@colab.dev}"
PASSWORD="${E2E_PASSWORD:-password123}"

# 목은 전역 저장소를 프로세스가 살아 있는 동안 들고 있다 — 이 스크립트가 킬 스위치를 켜므로 재실행 전에 되돌린다.
[ "$MOCK" = "1" ] && curl -sS -X POST "$B/__mock/reset" -o /dev/null

LOGIN=$(curl -sS -c "$J" -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/auth/login" -H 'content-type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")
chk "로그인" "$LOGIN" "200"
WS=$(curl -sS -b "$J" "$B/me" | python3 -c 'import sys,json;print(json.load(sys.stdin)["workspaces"][0]["id"])')
say "workspace" "$WS"

# 템플릿 — 매핑 결과가 채워지는지
T=$(curl -sS -b "$J" "$B/workspaces/$WS/agent-templates")
chk "agent-templates 3종" "$(echo "$T" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')" "3"
chk "매핑 status" "$(echo "$T" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d[0]["agents"][0]["mapping"]["status"])')" "mapped"

# 팀 생성
AP=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/agent-templates/dev_team/apply" -H 'content-type: application/json' -d '{}')
chk "dev_team 적용 3명" "$(echo "$AP" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["agents"]))')" "3"
AID=$(echo "$AP" | python3 -c 'import sys,json;print(json.load(sys.stdin)["agents"][0]["id"])')

# 프로파일 옵션 — 광고된 값은 통과, 광고 없는 키는 422
P1=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/agents/$AID/profiles" -H 'content-type: application/json' \
  -d '{"name":"fast","runtime_kind":"claude_code","model":"claude-opus-5","options":{"effort":"xhigh"}}')
chk "프로파일 추가(광고된 effort)" "$P1" "201"
P2=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/agents/$AID/profiles" -H 'content-type: application/json' \
  -d '{"name":"h","runtime_kind":"hermes","model":"hermes-4","options":{"effort":"xhigh"}}')
chk "광고 없는 옵션은 422" "$P2" "422"

# 런타임 후보
RC=$(curl -sS -b "$J" "$B/workspaces/$WS/runtime-candidates?isolation=none")
chk "none 은 자동 선택 허용" "$(echo "$RC" | python3 -c 'import sys,json;print(json.load(sys.stdin)["auto_select_allowed"])')" "True"
RC2=$(curl -sS -b "$J" "$B/workspaces/$WS/runtime-candidates?isolation=worktree&remote_url=git@github.com:ingki3/agent-collabortion.git")
chk "worktree 는 자동 선택 불가" "$(echo "$RC2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["auto_select_allowed"])')" "False"
chk "remote URL 일치 런타임 후보" "$(echo "$RC2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["candidates"][0]["eligible"])')" "True"

# 세션
AGS=$(curl -sS -b "$J" "$B/workspaces/$WS/agents" | python3 -c 'import sys,json;d=json.load(sys.stdin)["items"];print(",".join(a["id"] for a in d[:2]))')
A1=${AGS%%,*}; A2=${AGS##*,}
S=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' \
  -d "{\"title\":\"스모크\",\"goal\":\"목 P2 확인\",\"isolation\":{\"kind\":\"none\"},\"participants\":[{\"agent_id\":\"$A1\"},{\"agent_id\":\"$A2\"}],\"assignee_agent_id\":\"$A1\"}")
SID=$(echo "$S" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
say "session" "$SID"
sleep 3

# lane 보드
L=$(curl -sS -b "$J" "$B/sessions/$SID/lanes")
chk "lane 생성됨" "$(echo "$L" | python3 -c 'import sys,json;print(len(json.load(sys.stdin))>0)')" "True"
LID=$(echo "$L" | python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["id"])')
chk "lane 상태 done" "$(echo "$L" | python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["status"])')" "done"
chk "lane tasks 정보 5종" "$(curl -sS -b "$J" "$B/lanes/$LID/tasks" | python3 -c 'import sys,json;t=json.load(sys.stdin)[0];print(all(k in t for k in ("attempt","attempts","usage","trigger_message_id","failure_kind")))')" "True"

# 트리거 미리보기
MENT="[@x](mention://agent/$A2)"
PV=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/messages/preview" -H 'content-type: application/json' -d "{\"content\":\"$MENT 도와줘\"}")
chk "preview 규칙 2" "$(echo "$PV" | python3 -c 'import sys,json;print(json.load(sys.stdin)["triggers"][0]["rule"])')" "2"
PV2=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/messages/preview" -H 'content-type: application/json' -d '{"content":"/note 참고만"}')
chk "preview note_only" "$(echo "$PV2" | python3 -c 'import sys,json;print(json.load(sys.stdin)["note_only"])')" "True"
PV3=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/messages/preview" -H 'content-type: application/json' -d '{"content":"[@all](mention://all) 공지"}')
chk "preview 규칙 3" "$(echo "$PV3" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["implicit_routing_suppressed"] and not d["triggers"])')" "True"
PV4=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/messages/preview" -H 'content-type: application/json' -d "{\"content\":\"$MENT 별도로\",\"new_lane\":true}")
chk "new_lane 은 항상 새 lane" "$(echo "$PV4" | python3 -c 'import sys,json;t=json.load(sys.stdin)["triggers"][0];print(t["lane"]["lane_id"] is None and t["lane"]["resolution"]==1)')" "True"
PV5=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/messages/preview" -H 'content-type: application/json' -d "{\"content\":\"$MENT 억제\",\"suppress_agent_ids\":[\"$A2\"]}")
chk "억제하면 트리거 0" "$(echo "$PV5" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)["triggers"]))')" "0"

# 재지시 · 중단
curl -sS -b "$J" -X POST "$B/sessions/$SID/messages" -H 'content-type: application/json' -H "idempotency-key: $(uuidgen)" -d "{\"content\":\"$MENT 시작\"}" -o /dev/null
sleep 1
RL=$(curl -sS -b "$J" "$B/sessions/$SID/lanes" | python3 -c 'import sys,json;d=[l for l in json.load(sys.stdin) if l["status"] in ("running","queued")];print(d[0]["id"] if d else "")')
if [ -n "$RL" ]; then
  R=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/lanes/$RL/restart" -H 'content-type: application/json' -H "idempotency-key: $(uuidgen)" -d '{"content":"한국 시장으로 좁혀줘"}')
  chk "lane restart 202" "$R" "202"
fi
sleep 3
CL=$(curl -sS -b "$J" "$B/sessions/$SID/lanes" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d[0]["id"])')
chk "lane cancel(종료된 lane) 409" "$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/lanes/$CL/cancel")" "409"

# 일시정지 · 재개
chk "pause 200" "$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/sessions/$SID/pause")" "200"
PD=$(curl -sS -b "$J" "$B/sessions/$SID" | python3 -c 'import sys,json;d=json.load(sys.stdin);print(d["paused_detail"]["reason"])')
chk "paused_detail 객체" "$PD" "director"
chk "resume 200" "$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/sessions/$SID/resume" -H 'content-type: application/json' -d '{}')" "200"

# lane 7상태 시드 — 목에만 있는 경로다(화면의 7상태를 눈으로 볼 때 쓴다)
if [ "$MOCK" = "1" ]; then
  chk "seed-lanes 7개" "$(curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-lanes" | python3 -c 'import sys,json;print(len(json.load(sys.stdin)))')" "7"
fi

# 아티팩트·결정·비용
chk "artifacts 200" "$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' "$B/sessions/$SID/artifacts")" "200"
chk "decisions 200" "$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' "$B/sessions/$SID/decisions")" "200"
chk "cost 200" "$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' "$B/sessions/$SID/cost")" "200"

# 킬 스위치 — 실행 중 턴 취소, 참여자 disabled
curl -sS -b "$J" -X PATCH "$B/agents/$A2" -H 'content-type: application/json' -d '{"respond_to":"nobody"}' -o /dev/null
chk "킬 스위치 후 참여자 disabled" "$(curl -sS -b "$J" "$B/sessions/$SID" | python3 -c "import sys,json;d=json.load(sys.stdin);print([p['status'] for p in d['participants'] if p['agent_id']=='$A2'][0])")" "disabled"
PVK=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/messages/preview" -H 'content-type: application/json' -d "{\"content\":\"$MENT 다시\"}")
chk "정지된 에이전트는 경고" "$(echo "$PVK" | python3 -c 'import sys,json;print(json.load(sys.stdin)["warnings"][0]["code"])')" "agent_disabled"

# 저장소 검증(E13-01)
RID=$(curl -sS -b "$J" "$B/workspaces/$WS/runtimes" | python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["id"])')
chk "dirty 저장소는 ok:false" "$(curl -sS -b "$J" -X POST "$B/runtimes/$RID/repo-checks" -H 'content-type: application/json' -d '{"repo_path":"~/dev/dirty-repo"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["ok"])')" "False"

echo "---"
[ "$fail" = 0 ] && echo "SMOKE PASS" || echo "SMOKE FAIL"
exit $fail
