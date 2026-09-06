#!/usr/bin/env bash
# P4 계약 왕복 스모크 — **에이전트 턴 0**. 웹이 부르는 P4 operation 을 계약 모양대로 왕복시킨다.
#
# `e2e/p3-mock.sh` 와 같은 구조다: 목 API(COLAB_MOCK_API=1)로도, 실서버(:8080 프록시)로도 같은 스크립트가
# 돌아 통합(T-I4)이 BASE_URL 만 바꿔 대조할 수 있다. 목에만 있는 시드(`/__mock/...`)는 MOCK=1 일 때만 돈다.
#
# 재는 것(P4a 골든·계약 원문):
#   S11  오프라인 유예 6일 23시간 → active·알림 0 / 7일 → paused(runtime_offline) + 선택지 2   E14-01·02
#   S11  런타임 삭제 409 runtime_has_active_sessions + Problem.sessions[]                      E14-08
#   S17  후보는 remote URL 로 판정(경로 아님) · 후보 아님도 사유와 함께 돌려준다               E14-04·05
#   S17  worktree 재바인딩: acknowledge_loss 없으면 422, 후보 아니면 422, 성공하면 active      E14-06·03
#   S17  diff 아티팩트 목록 = 제출 순서                                                         E14-06
#   S13  workdir 목록(용량·보존·차단 사유) · 삭제 409 workdir_dirty → force 202 · 브랜치 보존   E13-12·13·10
#   S6   저장소 검증은 `ok:false` 여도 200                                                      E13-01
#   S8   인박스 카드가 purpose 를 싣는다(N+1 제거)                                              K-9
#
# 사용:
#   COLAB_MOCK_API=1 npx next dev -p 3117 &
#   BASE_URL=http://localhost:3117 MOCK=1 bash e2e/p4-mock.sh
set -uo pipefail
cd "$(dirname "$0")/.."
BASE_URL="${BASE_URL:-http://localhost:3117}"
MOCK="${MOCK:-1}"
B="$BASE_URL/api/v1"
J="$(mktemp -t colab-p4-cookies)"
trap 'rm -f "$J"' EXIT
fail=0
say() { printf '%-58s %s\n' "$1" "$2"; }
chk() { if [ "$2" = "$3" ]; then say "$1" "OK ($2)"; else say "$1" "FAIL got=$2 want=$3"; fail=1; fi; }
py() { python3 -c "$1"; }

EMAIL="${E2E_EMAIL:-demo@colab.dev}"
PASSWORD="${E2E_PASSWORD:-password123}"

[ "$MOCK" = "1" ] && curl -sS -X POST "$B/__mock/reset" -o /dev/null

LOGIN=$(curl -sS -c "$J" -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/auth/login" -H 'content-type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASSWORD\"}")
chk "로그인" "$LOGIN" "200"
WS=$(curl -sS -b "$J" "$B/me" | py 'import sys,json;print(json.load(sys.stdin)["workspaces"][0]["id"])')
RTS=$(curl -sS -b "$J" "$B/workspaces/$WS/runtimes")
RT=$(echo "$RTS" | py 'import sys,json;print(json.load(sys.stdin)[0]["id"])')
REMOTE=$(echo "$RTS" | py 'import sys,json;print(json.load(sys.stdin)[0]["repos"][0]["remote_url"])')
REPO=$(echo "$RTS" | py 'import sys,json;print(json.load(sys.stdin)[0]["repos"][0]["path"])')
AGS=$(curl -sS -b "$J" "$B/workspaces/$WS/agents" | py 'import sys,json;d=json.load(sys.stdin)["items"];print(",".join(a["id"] for a in d[:2]))')
A1=${AGS%%,*}; A2=${AGS##*,}

# ── S6 저장소 검증(E13-01) ────────────────────────────────────────────────
OKCHK=$(curl -sS -b "$J" -X POST "$B/runtimes/$RT/repo-checks" -H 'content-type: application/json' -d "{\"repo_path\":\"$REPO\"}")
chk "checkRepo ok" "$(echo "$OKCHK" | py 'import sys,json;print(json.load(sys.stdin)["ok"])')" "True"
chk "checkRepo 가 remote URL 을 준다(FR-9)" "$(echo "$OKCHK" | py 'import sys,json;print(bool(json.load(sys.stdin)["remote_url"]))')" "True"
BAD=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/runtimes/$RT/repo-checks" -H 'content-type: application/json' -d '{"repo_path":"/nope/dirty"}')
chk "ok:false 여도 200 이다 — 오류가 아니라 답 (E13-01)" "$BAD" "200"

# ── worktree 세션 하나 ────────────────────────────────────────────────────
S=$(curl -sS -b "$J" -X POST "$B/workspaces/$WS/sessions" -H 'content-type: application/json' \
  -d "{\"title\":\"P4 스모크\",\"goal\":\"worktree 격리와 재바인딩\",\"isolation\":{\"kind\":\"worktree\",\"repo_path\":\"$REPO\",\"remote_url\":\"$REMOTE\"},\"runtime_id\":\"$RT\",\"participants\":[{\"agent_id\":\"$A1\"},{\"agent_id\":\"$A2\"}],\"assignee_agent_id\":\"$A1\"}")
SID=$(echo "$S" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
say "session" "$SID"
chk "세션이 remote_url 을 보존한다 — 재바인딩 판정 키다" "$(echo "$S" | py 'import sys,json;print(bool(json.load(sys.stdin)["isolation"].get("remote_url")))')" "True"

# ── S13 workdir(E13-09~13) ────────────────────────────────────────────────
[ "$MOCK" = "1" ] && curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-workdirs" -H 'content-type: application/json' -d '{}' -o /dev/null
WD=$(curl -sS -b "$J" "$B/runtimes/$RT/workdirs")
chk "workdir 목록은 Page 봉투 + 용량 합계" "$(echo "$WD" | py 'import sys,json;d=json.load(sys.stdin);print("items" in d and "disk_bytes_total" in d)')" "True"
chk "차단 사유 둘이 구별된다 (E13-12·13)" "$(echo "$WD" | py 'import sys,json;d=json.load(sys.stdin)["items"];print(sorted({w["gc_blocked_reason"] for w in d if w["gc_blocked_reason"]}))')" "['uncommitted_changes', 'unmerged_commits']"
WID=$(echo "$WD" | py 'import sys,json;d=json.load(sys.stdin)["items"];print([w["id"] for w in d if w["gc_blocked_reason"]=="unmerged_commits"][0])')
WBR=$(echo "$WD" | py 'import sys,json;d=json.load(sys.stdin)["items"];print([w["branch"] for w in d if w["gc_blocked_reason"]=="unmerged_commits"][0])')
D409=$(curl -sS -b "$J" -X DELETE "$B/workdirs/$WID")
chk "미병합 커밋 삭제는 409 workdir_dirty (FR-6.4 M4)" "$(echo "$D409" | py 'import sys,json;print(json.load(sys.stdin)["code"])')" "workdir_dirty"
chk "409 detail 이 사유를 그대로 준다" "$(echo "$D409" | py 'import sys,json;print(json.load(sys.stdin)["detail"])')" "unmerged_commits"
D202=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X DELETE "$B/workdirs/$WID?force=true")
chk "force 확인 뒤 202 (삭제는 데몬이 한다)" "$D202" "202"
WD2=$(curl -sS -b "$J" "$B/runtimes/$RT/workdirs")
chk "상태가 deleted 로 바뀐다" "$(echo "$WD2" | py "import sys,json;print([w['status'] for w in json.load(sys.stdin)['items'] if w['id']=='$WID'][0])")" "deleted"
chk "**브랜치는 남는다** (FR-6.4 M4)" "$(echo "$WD2" | py "import sys,json;print([w['branch'] for w in json.load(sys.stdin)['items'] if w['id']=='$WID'][0])")" "$WBR"

# ── S17 재바인딩(E14) ─────────────────────────────────────────────────────
[ "$MOCK" = "1" ] && curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-artifacts" -H 'content-type: application/json' -d '{"count":3,"type":"diff"}' -o /dev/null
ART=$(curl -sS -b "$J" "$B/sessions/$SID/artifacts?type=diff")
chk "diff 아티팩트 목록은 제출 순서다 (E14-06)" "$(echo "$ART" | py 'import sys,json;d=json.load(sys.stdin);print([a["name"] for a in d]==sorted([a["name"] for a in d]) and [a["created_at"] for a in d]==sorted([a["created_at"] for a in d]))')" "True"

# 유예 직전 — 세션은 그대로 active, 알림 0 (E14-01)
if [ "$MOCK" = "1" ]; then
  IN_BEFORE=$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS" | py 'import sys,json;print(len([x for x in json.load(sys.stdin)["items"] if x["type"]=="runtime_offline"]))')
  curl -sS -b "$J" -X POST "$B/__mock/runtimes/$RT/offline" -H 'content-type: application/json' -d '{"days":6.958333}' -o /dev/null
  chk "E14-01 유예 직전은 active" "$(curl -sS -b "$J" "$B/sessions/$SID" | py 'import sys,json;print(json.load(sys.stdin)["status"])')" "active"
  chk "E14-01 알림 0" "$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS" | py 'import sys,json;print(len([x for x in json.load(sys.stdin)["items"] if x["type"]=="runtime_offline"]))')" "$IN_BEFORE"
  # 유예 도달 (E14-02)
  curl -sS -b "$J" -X POST "$B/__mock/runtimes/$RT/offline" -H 'content-type: application/json' -d '{"days":8}' -o /dev/null
fi
PS=$(curl -sS -b "$J" "$B/sessions/$SID")
chk "E14-02 세션이 paused(runtime_offline)" "$(echo "$PS" | py 'import sys,json;d=json.load(sys.stdin);print(d["status"]+"/"+str(d["paused_reason"]))')" "paused/runtime_offline"
chk "E14-02 선택지는 정확히 둘(재바인딩·종료)" "$(echo "$PS" | py 'import sys,json;print(json.load(sys.stdin)["paused_detail"]["resolve_actions"])')" "['rebind', 'cancel']"
chk "E14-10 스윕은 멱등 — 알림 1건" "$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS" | py 'import sys,json;print(len([x for x in json.load(sys.stdin)["items"] if x["type"]=="runtime_offline"]))')" "1"

# 삭제 차단 (E14-08)
DEL=$(curl -sS -b "$J" -X DELETE "$B/runtimes/$RT")
chk "E14-08 활성/정지 세션이 걸린 런타임 삭제는 409" "$(echo "$DEL" | py 'import sys,json;print(json.load(sys.stdin)["code"])')" "runtime_has_active_sessions"
chk "E14-08 Problem.sessions[] 가 대상 세션을 준다" "$(echo "$DEL" | py "import sys,json;print([x['id'] for x in json.load(sys.stdin)['sessions']]==['$SID'])")" "True"

# 후보 조회 — remote URL 판정 (E14-04·05). 목에는 런타임 생성 op 이 없으므로 두 번째 머신은 MOCK 에서만 잰다.
CAND=$(curl -sS -b "$J" "$B/workspaces/$WS/runtime-candidates?isolation=worktree&session_id=$SID")
chk "worktree 는 자동 선택 불가 (E13-17)" "$(echo "$CAND" | py 'import sys,json;print(json.load(sys.stdin)["auto_select_allowed"])')" "False"
chk "오프라인 런타임은 후보가 아니고 사유가 붙는다" "$(echo "$CAND" | py "import sys,json;c=[x for x in json.load(sys.stdin)['candidates'] if x['runtime']['id']=='$RT'][0];print(c['eligible']==False and bool(c['reason']))")" "True"

# 재바인딩 거절 경로 — 후보가 아니면 422 (E14-05)
RB422=$(curl -sS -b "$J" -o /dev/null -w '%{http_code}' -X POST "$B/sessions/$SID/rebind" -H 'content-type: application/json' -d "{\"runtime_id\":\"$RT\",\"acknowledge_loss\":true}")
chk "E14-05 후보가 아닌 런타임은 422" "$RB422" "422"

# ── S8 인박스 카드 purpose (K-9) ──────────────────────────────────────────
HB=$(curl -sS -b "$J" -X POST "$B/__mock/sessions/$SID/seed-hitl" -H 'content-type: application/json' \
  -d "{\"source\":\"system\",\"purpose\":\"budget\",\"type\":\"approval\",\"proposed_default\":null,\"agent_id\":\"$A1\",\"question\":\"예산을 초과했습니다\"}")
HBID=$(echo "$HB" | py 'import sys,json;print(json.load(sys.stdin)["id"])')
chk "K-9 인박스 카드가 purpose 를 싣는다(상세 왕복 없음)" \
  "$(curl -sS -b "$J" "$B/inbox?workspace_id=$WS" | py "import sys,json;d=[x for x in json.load(sys.stdin)['items'] if x['ref_id']=='$HBID'];print(d[0]['card'].get('purpose'))")" "budget"

echo
if [ "$fail" = "0" ]; then echo "✅ P4 목 스모크 통과"; else echo "❌ 실패 있음"; fi
exit "$fail"
