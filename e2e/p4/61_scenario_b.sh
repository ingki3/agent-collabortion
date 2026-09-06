#!/usr/bin/env bash
# e2e/p4/61_scenario_b.sh — T-I4 (a): **시나리오 B 전체** (EVAL E16-B, E13-01~08, FR-6.4·FR-6.1·FR-6.5, PRD §3·§8.4).
#
# 실기다. PM·Backend·Frontend 는 claude_code(어댑터 0.74.0), **QA 는 hermes**(0.20.6) 로 돌린다.
# QA 를 hermes 로 두는 이유는 두 가지다 — P4_TASKS §4 의 "Reviewer 를 hermes 프로파일로" 병행 항목이
# 그것이고, **브리프 전송이 런타임마다 다르기 때문**이다: claude_code 는 `acp_meta_system_prompt`(디스크에
# 아무것도 쓰지 않는다) 이고 hermes 만 `instruction_file` 이다. §8.4 v0.16 이 막는 오염
# (`COLAB_BRIEF.md` 잔존 · `.git/info/exclude` 미해제 · 추적 중 `CLAUDE.md`/`AGENTS.md` 변경)은
# **instruction_file 경로에서만 일어날 수 있으므로**, hermes 참가자가 없으면 E13-03~06 은 아무것도 재지 못한다.
#
#   B1  세션(worktree, `agent_approval(QA)` 단독) → PM 이 스펙을 쓰고 @Backend @Frontend 병렬 위임
#   B2  워크트리: 에이전트당 1개, 브랜치 `colab/<S>/<agent>` (E13-01·02)
#   B3  각자 `colab artifact submit --type diff` → 아티팩트 type=diff
#   B4  QA 는 **남의 워크트리에 접근하지 않는다** — 번들에 남의 workdir 경로 0 (E13-08)
#   B5  QA 수정 요청 → Frontend **기존 lane 재진입**(같은 워크트리, 새 lane 0) → 새 diff = version 2
#   B6  서버가 QA 를 깨운다(FR-6.5) → QA `review approve` → 사람 승인 없이 `completed`
#   B7  종료 뒤 각 워크트리 `git status` 클린 · 추적 파일 무변경 · `COLAB_BRIEF.md` 없음 · exclude 해제
#   B8  `session_summary` 정확히 1개 + `generated_by` 피드
#   B9  활동 피드에 파일 편집 카드 · 셸 카드
#
# 실험 저장소는 **이 저장소가 아니다**(P4_TASKS §0-18) — /private/tmp 아래 임시 git 저장소.
# 산출물: out/61-checks.tsv · out/61-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-61.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-61.json"; WORK="$P4_TMP_ROOT/61/work"; DLOG="$OUT/daemon-61.log"
REPO="$P4_TMP_ROOT/61/repo"
TAP="$OUT/tap-61.jsonl"; TAP_PORT="${TAP_PORT_61:-8115}"
MODEL="${LEAD_MODEL}"                       # claude-haiku-4-5-20251001
HERMES_MODEL="${HERMES_MODEL:-claude-haiku-4-5-20251001}"
EMAIL="i4b+$STAMP@example.com"; PASSWORD="password123"
g5_chk_init "$OUT/61-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  daemon_stop "$OUT/daemon-61.pid"
  return 0
}
trap cleanup EXIT

RULES="$P4_RULES"

PM_INS="너는 PM(lead)이다. 한국어로 짧게 답한다.
첫 턴부터 곧바로 아래를 순서대로 한다.
1. 네 작업 디렉토리에 SPEC.md 를 만들어 \"급수 시간 계산과 패널 표시를 각각 구현한다\" 를 세 줄로 쓴다.
2. colab_lane_delegate 로 agent \"Backend\" 에 brief \"src/pump.py 의 water_seconds 를 구현하라\" 를 위임한다.
3. colab_lane_delegate 로 agent \"Frontend\" 에 brief \"src/ui.py 의 render 를 고쳐라\" 를 위임한다.
4. colab_status_set 으로 status \"done\" 을 부르고 턴을 끝낸다.
두 위임이 모두 끝나 다시 깨어나면: 세션 메시지에서 \"FRONTEND-DIFF <id>\" 의 **마지막** 줄을 찾아
colab_message_post 로 \"[@QA](mention://agent/QA_ID) 리뷰 부탁합니다. FRONTEND-DIFF <id>\" 를 게시하고 status \"done\".
**QA 의 반려(수정 요청)가 보이면**: colab_lane_delegate 를 쓰지 말고 colab_message_post 로
\"[@Frontend](mention://agent/FE_ID) <QA 가 요구한 수정 내용 그대로>\" 를 게시하고 status \"done\".
$RULES"

BE_INS="너는 Backend(engineer)다. 한국어로 짧게 답한다. 작업 디렉토리는 작은 장난감 저장소의 git 워크트리다.
첫 턴부터 곧바로 아래를 순서대로 한다.
1. src/pump.py 의 water_seconds 를 고쳐 moisture 가 30 미만이면 5, 아니면 0 을 돌려주도록 구현한다.
2. 셸로 \`git diff --stat\` 을 한 번 실행한다.
3. colab_artifact_submit 을 type \"diff\", name \"backend\" 로 부른다. **file 은 주지 않는다** — CLI 가 네 워크트리의 diff 를 스스로 만든다.
4. colab_message_post 로 \"BACKEND-DIFF <방금 받은 artifact id>\" 한 줄을 게시한다.
5. colab_status_set 으로 status \"done\" 을 부르고 턴을 끝낸다.
커밋하지 마라. git add 도 하지 마라 — 변경은 작업 트리에 그대로 둔다(시나리오 B 의 정상 상태다).
$RULES"

FE_INS="너는 Frontend(engineer)다. 한국어로 짧게 답한다. 작업 디렉토리는 작은 장난감 저장소의 git 워크트리다.
첫 턴부터 곧바로 아래를 순서대로 한다.
1. src/ui.py 의 render 를 고쳐 \"planter: \" 를 앞에 붙여 돌려주도록 한다.
2. 셸로 \`git diff --stat\` 을 한 번 실행한다.
3. colab_artifact_submit 을 type \"diff\", name \"frontend\" 로 부른다. **file 은 주지 않는다**.
4. colab_message_post 로 \"FRONTEND-DIFF <방금 받은 artifact id>\" 한 줄을 게시한다.
5. colab_status_set 으로 status \"done\".
**수정 요청을 받으면**(두 번째 턴) src/ui.py 맨 첫 줄에 주석 \`# QA-FIX-9421\` 한 줄을 추가하고 2~5 를 그대로 다시 한다(name 은 그대로 \"frontend\").
커밋하지 마라. git add 도 하지 마라.
$RULES"

QA_INS="너는 QA(reviewer)다. 한국어로 짧게 답한다. 도구는 셸에서 부르는 \`colab\` 명령이다.
**이번 세션에서 네 판정 대상은 frontend 아티팩트 하나뿐이다. backend 아티팩트는 승인도 반려도 하지 마라.**
첫 턴부터 곧바로 아래를 순서대로 한다.
1. \`colab session messages --limit 50\` 로 메시지를 읽어 \"FRONTEND-DIFF <id>\" 의 아티팩트 id 를 찾는다. 여러 개면 **가장 마지막 줄**의 것을 쓴다.
2. \`colab artifact get <frontend id> --out ./fe.diff\` 로 내려받아 읽는다.
   **다른 사람의 작업 디렉토리는 절대 열지 마라** — 너에게는 아티팩트만 있다.
3. fe.diff 안에 \`QA-FIX-9421\` 이라는 문자열이 있으면 \`colab review approve --artifact <frontend id> --note 승인\` 을 실행한다.
   없으면 \`colab review reject --artifact <frontend id> --reason \"src/ui.py 맨 첫 줄에 주석 # QA-FIX-9421 한 줄을 추가해 주세요\"\` 를 실행한다.
   승인이든 반려든 **정확히 한 번만** 실행하고, 다른 아티팩트에는 손대지 마라.
4. \`colab status set done\` 을 실행하고 턴을 끝낸다.
$RULES"


GOAL='실내 화분 자동 급수기 패널의 급수 시간 계산과 상태 표시를 구현한다'
TITLE="scenario-b-$STAMP"
SLUG="$TITLE"

# ── 대기 헬퍼 ────────────────────────────────────────────────────────────────
wait_until() { # wait_until TIMEOUT "shell test"
  local dl=$(( $(date +%s) + $1 )); shift
  while [ "$(date +%s)" -lt "$dl" ]; do eval "$1" && return 0; sleep 3; done
  return 1
}
sess_status() { psqlq "select status::text from session where id='$1'"; }
# cnt FILE PATTERN... — 항상 숫자 한 개만 낸다(grep -c 는 무매치면 exit 1 이라 `|| echo 0` 이 줄을 두 개 만든다)
cnt() { local f="$1"; shift; local a=(); local x n; for x in "$@"; do a+=(-e "$x"); done
        n="$({ grep -c "${a[@]}" "$f" 2>/dev/null || true; } | head -1 | tr -d ' \n')"; printf '%s' "${n:-0}"; }
# 세션의 모든 task 가 멈출 때까지 (브리프 삭제·exclude 해제는 attempt 종료의 defer 다)
wait_quiet() {
  local dl=$(( $(date +%s) + ${2:-600} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select count(*) from task where session_id='$1' and status in ('queued','dispatched','preparing','running')")" = 0 ] && return 0
    sleep 3
  done
  return 1
}

step "0. 실험 저장소 (이 저장소가 아니다) + claim 탭"
mkdir -p "$P4_TMP_ROOT/61"
make_repo "$REPO" "git@example.invalid:planter-$STAMP.git"
ok "repo $REPO ($(git -C "$REPO" rev-parse --short HEAD)) — CLAUDE.md·AGENTS.md 추적 중"
REPO_HEAD_BEFORE="$(git -C "$REPO" rev-parse HEAD)"
CLAUDE_MD_BEFORE="$(shasum "$REPO/CLAUDE.md" | cut -d' ' -f1)"
AGENTS_MD_BEFORE="$(shasum "$REPO/AGENTS.md" | cut -d' ' -f1)"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT (pid $TAP_PID)"

step "1. 계정 · 페어링(capacity 4) · 에이전트 4 (QA 만 hermes)"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G7 Scenario B $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p4 "$PTOK" "$CFG" "$WORK" 4 "$REPO"
RUNTIME="$(runtime_of_config "$CFG")"
# **데몬은 아직 띄우지 않는다.** 아래 결함(상대 workdir 경로) 우회가 세션 생성과 첫 claim 사이에
# workdir 행을 넣어야 하기 때문이다 — 런타임이 없으면 task 는 queued 로 기다린다.
QA="$(create_agent_kind "$WS" QA reviewer hermes "$HERMES_MODEL" "$QA_INS" 'diff 아티팩트를 리뷰한다')"
BE="$(create_agent_p2 "$WS" Backend engineer "$MODEL" "$BE_INS" '급수 시간 계산을 구현한다')"
FE="$(create_agent_p2 "$WS" Frontend engineer "$MODEL" "$FE_INS" '패널 표시를 구현한다')"
# PM 의 지시문은 두 에이전트 id 를 멘션으로 담는다 — 그래서 마지막에 만든다.
PM_TEXT="${PM_INS/QA_ID/$QA}"; PM_TEXT="${PM_TEXT/FE_ID/$FE}"
PM="$(create_agent_p2 "$WS" PM lead "$MODEL" "$PM_TEXT" '스펙을 쓰고 위임한다')"
ok "PM=$PM BE=$BE FE=$FE QA=$QA(hermes) runtime=$RUNTIME"

step "2. **전제 확인** — worktree 격리 세션이 손대지 않은 채로 도는가 (신규 결함)"
# 아무 우회 없이 worktree 세션 하나를 그대로 띄워 본다. 이 세 줄이 FAIL 이면 그 아래 시나리오는
# 전부 우회 위에서 잰 것이다 — 그래서 판정 표의 맨 앞에 둔다.
PROBE_REPO="$P4_TMP_ROOT/61/repo-probe"
make_repo "$PROBE_REPO" "git@example.invalid:probe-$STAMP.git"
PTITLE="probe-worktree-$STAMP"
SP="$(create_session_p4 "$WS" "$PTITLE" "$GOAL" "$BE" "$RUNTIME" "$PROBE_REPO" "$(jq -nc '{op:"and",conditions:[{type:"manual"}]}')" '{}' "$BE")"
T_PROBE="$(session_initial_task "$SP")"
ok "probe session $SP · task $T_PROBE (아직 데몬 없음 — queued)"

step "2b. 세션 — 격리 worktree · 종료 조건 agent_approval(QA) 단독 (E16-B 1단계)"
S="$(create_session_p4 "$WS" "$TITLE" "$GOAL" "$PM" "$RUNTIME" "$REPO" "$(cond_agent_approval "$QA")" '{}' "$PM" "$BE" "$FE" "$QA")"
[ -n "$S" ] && [ "$S" != null ] || die "세션 생성 실패"
T_PM="$(session_initial_task "$S")"
chk B1  "worktree 격리 세션이 열린다 (repo_path 검증 통과)"      yes "$( [ -n "$S" ] && echo yes || echo no )"
chk B1b "isolation.kind = worktree"                              worktree "$(psqlq "select isolation->>'kind' from session where id='$S'")"
chk B1c "종료 조건 = agent_approval 단독"                        agent_approval "$(psqlq "select completion_condition->'conditions'->0->>'type' from session where id='$S'")"
# 우회(보고서 §대역/우회 표에 그대로 적는다): 서버가 번들에 **상대** workdir 경로를 실어
# worktree 세션이 첫 턴부터 죽는다. 서버는 이 에이전트의 workdir 행이 이미 있으면 그 경로를
# 대신 싣는다(`workdirs.BundleWorkdirPaths` → `ExistingForAgent`) — 그래서 **의도된 절대 경로**를
# 먼저 넣는다. probe 세션에는 넣지 않는다(위 X1 이 그것을 잰다).
seed_worktree_workdirs "$S" "$WORK" "$SLUG" "$PM:pm" "$BE:backend" "$FE:frontend" "$QA:qa"
ok "session $S · PM task $T_PM (workdir 행 4개 선행 삽입 — 우회)"
T0="$(now_ms)"

step "2c. 데몬 기동 — 두 세션이 동시에 claim 된다"
daemon_run "$CFG" "$DLOG" > "$OUT/daemon-61.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
wait_until 420 '[ "$(task_field "'"$T_PROBE"'" status)" = failed ] || [ "$(task_field "'"$T_PROBE"'" status)" = completed ]' || true
PROBE_ST="$(task_field "$T_PROBE" status)"
PROBE_DETAIL="$(psqlq "select coalesce(payload->>'detail','-') from task_event where task_id='$T_PROBE' and class='runtime' and verb='error' limit 1")"
python3 - "$TAP" "$T_PROBE" > "$OUT/61-probe-bundle.json" <<'PY' || true
import json, sys
tap, task = sys.argv[1], sys.argv[2]
for line in open(tap):
    try: rec = json.loads(line)
    except Exception: continue
    for b in ((rec.get("body") or {}).get("tasks") or []):
        if b.get("task", {}).get("id") == task:
            json.dump(b, sys.stdout, ensure_ascii=False, indent=1); sys.exit(0)
PY
PROBE_WD="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('workdir',{}).get('path',''))" "$OUT/61-probe-bundle.json" 2>/dev/null || echo '')"
PROBE_WD_KIND=없음
if [ -n "$PROBE_WD" ]; then
  if [ "${PROBE_WD#/}" != "$PROBE_WD" ]; then PROBE_WD_KIND=absolute; else PROBE_WD_KIND="relative:$PROBE_WD"; fi
fi
PROBE_DEAD=no; [ "$PROBE_ST" = failed ] && PROBE_DEAD=yes
chk X1  "TaskBundle 의 workdir.path 가 **절대 경로**다 (§4.1 — 서버는 데몬의 workdir_root 를 모른다)" \
  absolute "$PROBE_WD_KIND"
chk X1b "손대지 않은 worktree 세션의 첫 attempt 가 config 로 죽지 않는다" no "$PROBE_DEAD"
chk X1c "체크아웃이 사용자 저장소 안에 생기지 않는다 (worktree.go 주석·FR-6.4)" 1 \
  "$(git -C "$PROBE_REPO" worktree list | wc -l | tr -d ' ')"
git -C "$PROBE_REPO" worktree list > "$OUT/61-probe-worktrees.txt" 2>&1 || true
printf 'bundle workdir.path = %s\nattempt status = %s\ndetail = %s\n' "$PROBE_WD" "$PROBE_ST" "$PROBE_DETAIL" > "$OUT/61-probe.txt"
ok "probe: workdir.path=$PROBE_WD status=$PROBE_ST"
api_ok POST "/sessions/$SP/complete" '{"confirm":true}' >/dev/null 2>&1 || true

step "3. B1·B2 — PM 위임 → 워크트리 2개 · 브랜치 colab/<S>/<agent>"
wait_until 900 '[ "$(lanes_count "'"$S"'" Backend)" -ge 1 ] && [ "$(lanes_count "'"$S"'" Frontend)" -ge 1 ]' \
  || bad "PM 위임이 900초 안에 두 lane 을 만들지 않았다"
chk B2  "Backend lane 1개"  1 "$(lanes_count "$S" Backend)"
chk B2b "Frontend lane 1개" 1 "$(lanes_count "$S" Frontend)"
WT_BE="$WORK/worktrees/$SLUG/backend"
WT_FE="$WORK/worktrees/$SLUG/frontend"
wait_until 900 '[ -d "'"$WT_BE"'" ] && [ -d "'"$WT_FE"'" ]' || bad "워크트리 두 개가 준비되지 않았다"
chk B2c "Backend 워크트리 존재 ($WT_BE)"  yes "$( [ -d "$WT_BE" ] && echo yes || echo no )"
chk B2d "Frontend 워크트리 존재"          yes "$( [ -d "$WT_FE" ] && echo yes || echo no )"
chk B2e "Backend 브랜치 = colab/$SLUG/backend"   "colab/$SLUG/backend"  "$(git -C "$WT_BE" symbolic-ref --short HEAD 2>/dev/null || echo 없음)"
chk B2f "Frontend 브랜치 = colab/$SLUG/frontend" "colab/$SLUG/frontend" "$(git -C "$WT_FE" symbolic-ref --short HEAD 2>/dev/null || echo 없음)"

step "4. B3 — 각자 diff 아티팩트 제출"
wait_until 1500 '[ "$(psqlq "select count(*) from artifact where session_id='"'$S'"' and type='"'diff'"'")" -ge 2 ]' \
  || bad "diff 아티팩트 두 개가 1500초 안에 오지 않았다"
artifact_rows "$S" > "$OUT/61-artifacts-1.txt"
chk B3  "diff 아티팩트 2개"                 2 "$(psqlq "select count(distinct name) from artifact where session_id='$S' and type='diff'")"
chk B3b "전부 type=diff"                    0 "$(psqlq "select count(*) from artifact where session_id='$S' and type<>'diff'")"
A_BE="$(psqlq "select id from artifact where session_id='$S' and name like 'backend%' order by version desc limit 1")"
A_FE="$(psqlq "select id from artifact where session_id='$S' and name like 'frontend%' order by version desc limit 1")"
chk B3c "backend 아티팩트 id"  yes "$( [ -n "$A_BE" ] && echo yes || echo no )"
chk B3d "frontend 아티팩트 id" yes "$( [ -n "$A_FE" ] && echo yes || echo no )"

step "5. B4 — QA 가 깨어난다 · QA 번들에 남의 workdir 경로 0 (E13-08)"
wait_until 900 '[ -n "$(latest_task "'"$S"'" QA)" ]' || bad "QA task 가 생기지 않았다"
T_QA="$(latest_task "$S" QA)"
wait_until 600 '[ -n "$(tap_prompt "'"$TAP"'" "'"$T_QA"'" 1 2>/dev/null)" ]' || true
tap_prompt "$TAP" "$T_QA" 1 > "$OUT/61-qa-prompt.txt" 2>/dev/null || true
tap_brief  "$TAP" "$T_QA" 1 > "$OUT/61-qa-brief.txt"  2>/dev/null || true
python3 - "$TAP" "$T_QA" > "$OUT/61-qa-bundle.json" <<'PY' || true
import json, sys
tap, task = sys.argv[1], sys.argv[2]
for line in open(tap):
    try: rec = json.loads(line)
    except Exception: continue
    for b in ((rec.get("body") or {}).get("tasks") or []):
        if b.get("task", {}).get("id") == task:
            json.dump(b, sys.stdout, ensure_ascii=False, indent=1); sys.exit(0)
PY
QA_LEAK="$(cnt "$OUT/61-qa-bundle.json" "worktrees/$SLUG/backend" "worktrees/$SLUG/frontend")"
chk B4  "QA 번들에 Backend·Frontend 워크트리 경로 0건 (E13-08)" 0 "$QA_LEAK"
QA_WD="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('workdir',{}).get('path',''))" "$OUT/61-qa-bundle.json" 2>/dev/null || echo '')"
QA_WD_OK=no
[ "${QA_WD%/qa}" != "$QA_WD" ] && QA_WD_OK=yes
chk B4b "QA 번들 workdir 은 QA 자기 것 (${QA_WD:-없음})" yes "$QA_WD_OK"
chk B4c "QA 브리프 전송 = instruction_file (hermes)" instruction_file \
  "$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('brief',{}).get('transport',''))" "$OUT/61-qa-bundle.json" 2>/dev/null || echo '-')"

step "6. B5 — QA 수정 요청 → Frontend 기존 lane 재진입 (해소 규칙 1, 새 lane 0)"
LANE_FE="$(lane_of "$S" Frontend)"
wait_until 1200 '[ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'$S'"' and a.name='"'Frontend'"'")" -ge 2 ]' \
  || bad "Frontend 재진입 task 가 오지 않았다 (QA 반려가 없었나?)"
chk B5  "Frontend lane 은 여전히 1개 (새 lane 0)" 1 "$(lanes_count "$S" Frontend)"
chk B5b "Frontend task 2개 (재진입)"              2 "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Frontend'")"
chk B5c "재진입 task 의 lane 이 같다"             "$LANE_FE" "$(task_field "$(latest_task "$S" Frontend)" lane_id)"
chk B5d "Frontend 워크트리는 여전히 1개 (에이전트당 1개, C3)" 1 "$(worktrees_of "$S" Frontend)"
REJ="$(psqlq "select count(*) from message m where m.session_id='$S' and m.parent_id is not null and m.author_type='agent' and m.content like '%반려%'")"
chk B5f "QA 의 반려가 Frontend 스레드에 답글로 게시됐다 (openapi reviewArtifact)" yes \
  "$( [ "${REJ:-0}" -ge 1 ] && echo yes || echo no )"
REJ_MSG="$(psqlq "select id from message where session_id='$S' and author_type='agent' and parent_id is not null and content like '%반려%' order by created_at limit 1")"
REJ_TRIG=0
[ -n "$REJ_MSG" ] && REJ_TRIG="$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Frontend' and t.trigger_message_id='$REJ_MSG'")"
chk B5g "반려 답글이 **그 자체로** Frontend 를 재진입시킨다 (openapi reviewArtifact: '해소 규칙 1로 재진입')" yes \
  "$( [ "${REJ_TRIG:-0}" -ge 1 ] && echo yes || echo no )"
chk B5e "결정 기록에 리뷰가 남았다 (source=agent)" yes \
  "$( [ "$(psqlq "select count(*) from decision where session_id='$S' and source='agent'")" -ge 1 ] && echo yes || echo no )"

step "7. B6 — 새 diff = version 2 → 서버가 QA 를 깨운다 → approve → completed"
wait_until 1500 '[ "$(psqlq "select coalesce(max(version),0) from artifact where session_id='"'$S'"' and name like '"'frontend%'"'")" -ge 2 ]' \
  || bad "frontend 아티팩트 version 2 가 오지 않았다"
chk B6  "frontend 아티팩트 version 2 (같은 이름 재제출, FR-4.3)" 2 "$(psqlq "select max(version) from artifact where session_id='$S' and name like 'frontend%'")"
wait_until 900 '[ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'$S'"' and a.name='"'QA'"'")" -ge 2 ]' || true
chk B6b "QA 가 두 번째로 깨어났다 — QA task 2개 이상 (FR-6.5)" yes \
  "$( [ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='QA'")" -ge 2 ] && echo yes || echo no )"
wait_until 1500 '[ "$(sess_status "'"$S"'")" = completed ]' || bad "세션이 completed 로 가지 않았다 (현재: $(sess_status "$S"))"
chk B7  "세션 completed"                        completed "$(sess_status "$S")"
chk B7b "사람 승인 HITL 0건 (agent_approval 단독, E6-05)" 0 \
  "$(psqlq "select count(*) from hitl_request where session_id='$S' and purpose='user_approval'")"
chk B7c "승인은 QA 가 냈다 (decision source=agent)" yes \
  "$( [ "$(psqlq "select count(*) from decision where session_id='$S' and source='agent'")" -ge 1 ] && echo yes || echo no )"

step "8. B7 — 워크트리 위생 (E13-03~06, §8.4 v0.16)"
# 브리프 파일 삭제와 exclude 해제는 **attempt 종료의 defer** 다(daemon loop.go). 마지막 턴이
# 아직 돌고 있는 동안 재면 있지도 않은 오염을 본다 — 1차 실행 실측.
wait_quiet "$S" 600 || true
sleep 10
artifact_rows "$S" > "$OUT/61-artifacts-2.txt"
for pair in "Backend:$WT_BE" "Frontend:$WT_FE"; do
  who="${pair%%:*}"; wt="${pair#*:}"
  low="$(printf '%s' "$who" | tr 'A-Z' 'a-z')"
  git -C "$wt" status --porcelain > "$OUT/61-status-$low.txt" 2>&1 || true
  # 시나리오 B 는 커밋 없이 diff 만 낸다 — 소스 변경은 남아 있는 것이 정상이다.
  # 위생 판정은 "**우리가** 만든 잔여물이 없는가" 다.
  chk "B8-$low"  "$who: COLAB_BRIEF.md 없음"          0 "$(ls "$wt/COLAB_BRIEF.md" 2>/dev/null | wc -l | tr -d ' ')"
  chk "B8b-$low" "$who: 추적 중 CLAUDE.md 무변경"      "$CLAUDE_MD_BEFORE" "$(shasum "$wt/CLAUDE.md" 2>/dev/null | cut -d' ' -f1)"
  chk "B8c-$low" "$who: 추적 중 AGENTS.md 무변경"      "$AGENTS_MD_BEFORE" "$(shasum "$wt/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
  chk "B8d-$low" "$who: .git/info/exclude 에 COLAB_BRIEF 항목 0 (해제됨)" 0 \
    "$(cnt "$REPO/.git/info/exclude" 'COLAB_BRIEF')"
  chk "B8e-$low" "$who: git status 에 COLAB_BRIEF·wrapper 잔여물 0" 0 \
    "$(cnt "$OUT/61-status-$low.txt" 'COLAB_BRIEF' '\.colab')"
  chk "B8f-$low" "$who: 저장소 규칙 파일이 status 에 안 뜬다" 0 \
    "$(cnt "$OUT/61-status-$low.txt" 'CLAUDE\.md' 'AGENTS\.md')"
done
# QA 는 hermes = instruction_file 전송이다. 오염이 일어날 수 있는 유일한 경로이므로 따로 본다.
WT_QA="$WORK/worktrees/$SLUG/qa"
if [ -d "$WT_QA" ]; then
  git -C "$WT_QA" status --porcelain > "$OUT/61-status-qa.txt" 2>&1 || true
  chk B8-qa  "QA(hermes): COLAB_BRIEF.md 없음 (§8.4 세션 종료 뒤 삭제)" 0 "$(ls "$WT_QA/COLAB_BRIEF.md" 2>/dev/null | wc -l | tr -d ' ')"
  chk B8b-qa "QA(hermes): 추적 중 AGENTS.md 무변경 (M3 — 읽지도 쓰지도 않는다)" "$AGENTS_MD_BEFORE" "$(shasum "$WT_QA/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
  chk B8c-qa "QA(hermes): exclude 에 COLAB_BRIEF 항목 0" 0 \
    "$(cnt "$REPO/.git/info/exclude" 'COLAB_BRIEF')"
else
  chk_na B8-qa "QA 워크트리" 없음 "QA 턴이 워크트리를 만들지 않았다"
fi
chk B8g "원본 저장소 HEAD 무변경" "$REPO_HEAD_BEFORE" "$(git -C "$REPO" rev-parse HEAD)"
chk B8h "원본 저장소 git status 클린" yes "$(repo_status_clean "$REPO")"

step "9. B8 — 세션 요약 정확히 1개 + generated_by (FR-2.4, §8.5)"
summary_body "$S" > "$OUT/61-summary.txt" 2>/dev/null || true
summary_feed "$S" > "$OUT/61-summary-feed.txt" 2>/dev/null || true
chk B9  "session_summary 메시지 정확히 1개" 1 "$(summary_count "$S")"
chk B9b "요약에 FR-2.4 네 절"               4 \
  "$(psqlq "select (content like '%결정 기록%')::int + (content like '%아티팩트%')::int + (content like '%비용%')::int + (content like '%타임라인%')::int from message where session_id='$S' and kind='summary'")"
GB="$(psqlq "select coalesce(e.object_ref::text,'-') from task_event e join task t on t.id=e.task_id where t.session_id='$S' and e.object_ref::text like '%generated_by%' limit 1")"
GB_OK=no
printf '%s' "$GB" | grep -q generated_by && GB_OK=yes
chk B9c "generated_by 피드 항목이 있다 (키 없음 → fallback: $GB)" yes "$GB_OK"
ok "generated_by = $GB"

step "10. B9 — 활동 피드에 파일 편집 카드 · 셸 카드"
for t in "$(latest_task "$S" Backend)" "$(latest_task "$S" Frontend)"; do feed_kinds "$t"; done > "$OUT/61-feed.txt"
chk B10  "파일 편집 카드 ≥ 2 (tool/edit_file)" yes \
  "$( [ "$(feed_has "$S" tool edit_file)" -ge 2 ] && echo yes || echo no )"
chk B10b "셸 카드 ≥ 1 (tool/run_shell)"        yes \
  "$( [ "$(feed_has "$S" tool run_shell)" -ge 1 ] && echo yes || echo no )"

step "11. 비용 · 턴 수 (보고서용 실측)"
COST="$(psqlq "select coalesce(round(sum(cost_usd)::numeric,6)::text,'0') from task_usage u join task t on t.id=u.task_id where t.session_id='$S'")"
TURNS="$(psqlq "select count(*) from task_attempt ta join task t on t.id=ta.task_id where t.session_id='$S'")"
ELAPSED=$(( ($(now_ms) - T0) / 1000 ))
printf 'session=%s cost_usd=%s attempts=%s elapsed_s=%s\n' "$S" "$COST" "$TURNS" "$ELAPSED" | tee "$OUT/61-metrics.txt"

step "결과"
printf '  PASS %d · FAIL %d  (%s)\n' "$pass" "$fail" "$OUT/61-checks.tsv"
exit "$fail"
