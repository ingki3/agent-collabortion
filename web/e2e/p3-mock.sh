#!/usr/bin/env bash
# P3 계약 왕복 스모크 — **에이전트 턴 0**. 웹이 부르는 P3 operation 을 계약 모양대로 왕복시킨다.
#
# `e2e/p2-mock.sh` 와 같은 구조다: 목 API(COLAB_MOCK_API=1)로도, 실서버(:8080 프록시)로도 같은 스크립트가
# 돌아 통합(T-I3)이 BASE_URL 만 바꿔 대조할 수 있다. 목에만 있는 시드(`/__mock/...`)는 MOCK=1 일 때만 돈다.
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3113 &
#   BASE_URL=http://localhost:3113 MOCK=1 bash e2e/p3-mock.sh
set -uo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3113}"
MOCK="${MOCK:-1}"
B="$BASE_URL/api/v1"
J="$(mktemp -t colab-p3-cookies)"
trap 'rm -f "$J"' EXIT
fail=0
say() { printf '%-52s %s\n' "$1" "$2"; }
chk() { if [ "$2" = "$3" ]; then say "$1" "OK ($2)"; else say "$1" "FAIL got=$2 want=$3"; fail=1; fi; }
py() { python3 -c "$1"; }

EMAIL="${E2E_EMAIL:-demo@colab.dev}"
PASSWORD="${E2E_PASSWORD:-password123}"
IDEM() { printf 'idem-%s-%s' "$$" "$RANDOM"; }

[ "$MOCK" = "1" ] && curl -sS -X POST "$B/__mock/reset" -o /dev/null

LOGIN=$(curl -sS -c "$J" -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/auth/login" -H 'content-type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")
chk "로그인" "$LOGIN" "200"
WS=$(curl -sS -b "$J" "$B/me" | py 'import sys,json;print(json.load(sys.stdin)["workspaces"][0]["id"])')

# ── 세션 하나 ──
AGS=$(curl -sS -b "$J" "$B/workspaces/$WS/agents" | py 'import sys,json;d=json.load(sys.stdin)["items"];print(",".join(a["id"] for a in d[:2]))')
A1=${AGS%%,*}; A2=${AGS##*,}
S=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' \
  -d "{\"title\":\"P3 스모크\",\"goal\":\"HITL·인박스 왕복\",\"isolation\":{\"kind\":\"none\"},\"participants\":[{\"agent_id\":\"$A1\"},{\"agent_id\":\"$A2\"}],\"assignee_agent_id\":\"$A1\"}")
SID=$(echo "$S" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
say "session" "$SID"
chk "my_role 은 director" "$(echo "$S" | py 'import sys,json;print(json.load(sys.stdin)["my_role"])')" "director"

# ── HITL 카드(S7) ──────────────────────────────────────────────────────────
H=$(curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-hitl" -H 'content-type: application/json' -d '{}')
HID=$(echo "$H" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "HITL 기한이 created_at + 24h (골든 dueIn)" \
  "$(echo "$H" | py 'import sys,json,datetime as dt;d=json.load(sys.stdin);f=lambda x:dt.datetime.fromisoformat(x.replace("Z","+00:00"));print(int((f(d["due_at"])-f(d["created_at"])).total_seconds()))')" "86400"
chk "question 은 제안 기본값이 있다(FR-5.1)" "$(echo "$H" | py 'import sys,json;print(bool(json.load(sys.stdin)["proposed_default"]))')" "True"
chk "타임라인에 hitl 카드 메시지가 붙는다" "$(echo "$H" | py 'import sys,json;print(bool(json.load(sys.stdin)["message_id"]))')" "True"

LH=$(curl -sS -b "$J" "$B/sessions/$SID/hitl-requests")
chk "listHitlRequests 는 페이지 모양" "$(echo "$LH" | py 'import sys,json;print("items" in json.load(sys.stdin))')" "True"
chk "Director 는 can_respond" "$(echo "$LH" | py 'import sys,json;print(json.load(sys.stdin)["items"][0]["can_respond"])')" "True"

# 멱등키는 계약 필수 헤더다
NOKEY=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/hitl-requests/$HID/response" -H 'content-type: application/json' -d '{"answer":"경영진"}')
chk "Idempotency-Key 없는 응답은 422" "$NOKEY" "422"

R1=$(curl -sS -b "$J" -X POST "$B/hitl-requests/$HID/response" -H 'content-type: application/json' -H "Idempotency-Key: $(IDEM)" -d '{"answer":"경영진"}')
chk "응답 → answered" "$(echo "$R1" | py 'import sys,json;print(json.load(sys.stdin)["hitl_request"]["status"])')" "answered"
chk "결정 기록 1건" "$(echo "$R1" | py 'import sys,json;print(bool(json.load(sys.stdin)["decision_id"]))')" "True"
R2=$(curl -sS -b "$J" -X POST "$B/hitl-requests/$HID/response" -H 'content-type: application/json' -H "Idempotency-Key: $(IDEM)" -d '{"answer":"실무자"}')
chk "두 번째 응답은 ignored (E7-08)" "$(echo "$R2" | py 'import sys,json;print(json.load(sys.stdin)["ignored"])')" "True"
chk "첫 답이 유지된다 (E7-08)" "$(echo "$R2" | py 'import sys,json;print(json.load(sys.stdin)["hitl_request"]["answer"])')" "경영진"

# ── 예산 HITL (E9-01·02) ───────────────────────────────────────────────────
HB=$(curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-hitl" -H 'content-type: application/json' \
  -d "{\"source\":\"system\",\"purpose\":\"budget\",\"type\":\"approval\",\"proposed_default\":null,\"agent_id\":\"$A1\",\"question\":\"예산 \$1 을 초과했습니다\"}")
HBID=$(echo "$HB" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "예산 HITL 은 system 발행이어도 task_id 를 채운다(s-13)" "$(echo "$HB" | py 'import sys,json;print(json.load(sys.stdin)["task_id"] is not None)')" "True"
chk "purpose 가 budget (0012 판정 기준)" "$(echo "$HB" | py 'import sys,json;print(json.load(sys.stdin)["purpose"])')" "budget"
# S-45 대비 — 시스템 발행 HITL 도 타임라인 카드(kind=hitl)를 만든다. 없으면 S7 에 답할 자리가 없다.
chk "시스템 발행 HITL 도 hitl 메시지를 만든다 (S-45 대비)" "$(echo "$HB" | py 'import sys,json;print(bool(json.load(sys.stdin)["message_id"]))')" "True"
HBMID=$(echo "$HB" | py 'import sys,json;print(json.load(sys.stdin)["message_id"])')
HBMSG=$(curl -sS -b "$J" "$B/sessions/$SID/messages")
chk "그 메시지의 kind 가 hitl 이다" "$(echo "$HBMSG" | py "import sys,json;d=json.load(sys.stdin)['items'];print([m['kind'] for m in d if m['id']=='$HBMID'][0])")" "hitl"
# W-6 — task 범위 초과는 세션을 멈추지 않는다. 인박스 항목은 hitl_request 로 오고 purpose 는 상세에만 있다.
chk "task 범위 예산 HITL 은 세션을 멈추지 않는다 (E9-01)" "$(curl -sS -b "$J" "$B/sessions/$SID" | py 'import sys,json;print(json.load(sys.stdin)["status"])')" "active"
IN0=$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS")
chk "예산 HITL 의 인박스 항목 타입은 hitl_request (W-6)" "$(echo "$IN0" | py "import sys,json;d=[x for x in json.load(sys.stdin)['items'] if x['ref_id']=='$HBID'];print(d[0]['type'])")" "hitl_request"
chk "항목 카드에는 purpose 가 없다 — 상세를 읽어야 안다 (W-6)" "$(echo "$IN0" | py "import sys,json;d=[x for x in json.load(sys.stdin)['items'] if x['ref_id']=='$HBID'];print('purpose' in d[0]['card'])")" "False"
chk "ref_id 로 읽은 상세에는 purpose=budget 이 있다 (W-6)" "$(curl -sS -b "$J" "$B/hitl-requests/$HBID" | py 'import sys,json;print(json.load(sys.stdin)["purpose"])')" "budget"
RB=$(curl -sS -b "$J" -X POST "$B/hitl-requests/$HBID/response" -H 'content-type: application/json' -H "Idempotency-Key: $(IDEM)" -d '{"approved":true,"budget_override_usd":3}')
chk "승인은 task.budget_override 로 간다 (E9-02)" "$(echo "$RB" | py 'import sys,json;print(json.load(sys.stdin)["task"]["budget_override"])')" "3"
chk "승인이 곧 트리거 — task queued (E9-02)" "$(echo "$RB" | py 'import sys,json;print(json.load(sys.stdin)["task"]["status"])')" "queued"

# ── 인박스(S8) ─────────────────────────────────────────────────────────────
[ "$MOCK" = "1" ] && curl -sS -b "$J" -X POST "$B/__mock/inbox/seed" -o /dev/null
curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-hitl" -H 'content-type: application/json' -d '{"age_ms":108000000}' -o /dev/null
IN=$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS")
chk "인박스 페이지 모양" "$(echo "$IN" | py 'import sys,json;print("items" in json.load(sys.stdin))')" "True"
chk "overdue 가 맨 위 (SCREEN §4.6 정렬)" "$(echo "$IN" | py 'import sys,json;print(json.load(sys.stdin)["items"][0]["overdue"])')" "True"
chk "심각도 3종만 쓴다" "$(echo "$IN" | py 'import sys,json;d=json.load(sys.stdin)["items"];print(set(x["severity"] for x in d) <= {"action_required","attention","info"})')" "True"
chk "hitl_request 카드에 제안 기본값이 있다(F2)" "$(echo "$IN" | py 'import sys,json;d=[x for x in json.load(sys.stdin)["items"] if x["type"]=="hitl_request"];print(bool(d[0]["card"]["proposed_default"]))')" "True"
SUM=$(curl -sS -b "$J" "$B/inbox/summary?workspace_id=$WS")
# 뱃지는 목록의 action_required 수와 같아야 한다 — info 까지 세면 뱃지가 영구히 켜져 의미가 없다.
AR_IN_LIST=$(echo "$IN" | py 'import sys,json;print(len([x for x in json.load(sys.stdin)["items"] if x["severity"]=="action_required"]))')
chk "뱃지 = 목록의 action_required 수" "$(echo "$SUM" | py 'import sys,json;print(json.load(sys.stdin)["action_required"])')" "$AR_IN_LIST"
FA=$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS&filter=action_required")
chk "액션 필요 필터" "$(echo "$FA" | py 'import sys,json;d=json.load(sys.stdin)["items"];print(all(x["severity"]=="action_required" for x in d))')" "True"
curl -sS -b "$J" -X POST "$B/inbox/read-all?workspace_id=$WS" -o /dev/null
AR=$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS")
chk "전부 읽음이 action_required 는 안 건드린다" "$(echo "$AR" | py 'import sys,json;d=json.load(sys.stdin)["items"];print(all(x["read_at"] is None for x in d if x["severity"]=="action_required"))')" "True"

# ── 상단 액션: 참여자 · Director 교체 · 취소 ────────────────────────────────
PARTS=$(curl -sS -b "$J" "$B/sessions/$SID/participants")
chk "참여자 2명" "$(echo "$PARTS" | py 'import sys,json;print(len(json.load(sys.stdin)))')" "2"
RMBUSY=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X DELETE "$B/sessions/$SID/participants/$A1")
chk "assignee 제거는 409" "$RMBUSY" "409"
UID2=$(curl -sS -b "$J" "$B/workspaces/$WS/members" | py 'import sys,json;d=json.load(sys.stdin)["items"];print([m["user"]["id"] for m in d][1])')
CD=$(curl -sS -b "$J" -X PUT "$B/sessions/$SID/director" -H 'content-type: application/json' -d "{\"director_user_id\":\"$UID2\"}")
chk "Director 교체 → my_role 이 member 로" "$(echo "$CD" | py 'import sys,json;print(json.load(sys.stdin)["my_role"])')" "member"
NOAUTH=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/sessions/$SID/pause")
chk "일반 멤버의 일시정지는 403 (S7-P)" "$NOAUTH" "403"
curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/role" -H 'content-type: application/json' -d '{"role":"director"}' -o /dev/null

# ── paused 배너 5종 ────────────────────────────────────────────────────────
for reason in budget time loop runtime_offline director; do
  P=$(curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/pause" -H 'content-type: application/json' -d "{\"reason\":\"$reason\"}")
  chk "paused($reason) detail.reason" "$(echo "$P" | py 'import sys,json;print(json.load(sys.stdin)["paused_detail"]["reason"])')" "$reason"
  if [ "$reason" = "runtime_offline" ]; then
    RS=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/sessions/$SID/resume" -H 'content-type: application/json' -d '{}')
    chk "runtime_offline 은 재개 불가 409" "$RS" "409"
  else
    curl -sS -b "$J" -X POST "$B/sessions/$SID/resume" -H 'content-type: application/json' -d '{}' -o /dev/null
  fi
done
curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/pause" -H 'content-type: application/json' -d '{"reason":"director"}' -o /dev/null
curl -sS -b "$J" -X POST "$B/sessions/$SID/resume" -H 'content-type: application/json' -d '{}' -o /dev/null

# ── 종료(manual) — 진행 중 lane 이 있으면 confirm 필요 ──────────────────────
[ "$MOCK" = "1" ] && curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-lanes" -H 'content-type: application/json' -d '{}' -o /dev/null
NOCONF=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/sessions/$SID/complete" -H 'content-type: application/json' -d '{}')
chk "진행 중 lane + confirm 없음 → 409 running_lanes" "$NOCONF" "409"
CP=$(curl -sS -b "$J" -X POST "$B/sessions/$SID/complete" -H 'content-type: application/json' -d '{"confirm":true}')
chk "confirm → completing" "$(echo "$CP" | py 'import sys,json;print(json.load(sys.stdin)["status"])')" "completing"

echo
if [ "$fail" = "0" ]; then echo "✅ P3 목 스모크 통과"; else echo "❌ 실패 있음"; fi
exit "$fail"
