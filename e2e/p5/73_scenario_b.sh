#!/usr/bin/env bash
# e2e/p5/73_scenario_b.sh — **시나리오 B 전체** (PRD §4 B · EVAL E16-B, E13-01~08, FR-6.4·6.1·6.5, §8.4).
#
# 비용 한 줄(I-3): 페이크 턴 ≈ 8(PM 3 · Backend 1 · Frontend 2 · QA 2) · $0 · ≈ 10s. 실기: haiku ≈ $0.05 · ≈ 6분
#
#   B1 세션(worktree, `agent_approval(QA)` 단독) → PM 스펙 + @Backend @Frontend 병렬 위임
#   B2 워크트리 에이전트당 1개 · 브랜치 colab/<S>/<agent>   B3 각자 `artifact submit --type diff`
#   B4 QA 번들에 남의 workdir 경로 0(E13-08)   B5 QA 반려 → Frontend 기존 lane 재진입 · diff version 2
#   B6 서버가 QA 를 깨운다 → approve → 사람 승인 없이 completed   B7 워크트리 위생(§8.4)
#   B8 요약 1개 + generated_by   B9 활동 피드 편집·셸 카드
#
# 판정은 `e2e/p4/61_scenario_b.sh`(G7) 그대로. 기본은 페이크 런타임(QA 는 hermes kind = cli_wrapper 경로,
# 나머지는 claude_code kind). `RUNTIME=real` 이면 61_ 과 같은 실기다.
# 실험 저장소는 이 저장소가 아니다(§0-18). 산출물: out/73-checks.tsv · out/73-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-73.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-73.json"; WORK="$P5_TMP_ROOT/73/work"; DLOG="$OUT/daemon-73.log"
REPO="$P5_TMP_ROOT/73/repo"
TAP="$OUT/tap-73.jsonl"; TAP_PORT="${TAP_PORT_73:-8120}"
MODEL="${LEAD_MODEL}"; HERMES_MODEL="${HERMES_MODEL:-claude-haiku-4-5-20251001}"
g5_chk_init "$OUT/73-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-73.pid"; return 0; }
trap cleanup EXIT

RULES="$P5_RULES"
PM_INS="너는 PM(lead)이다. 한국어로 짧게 답한다. 첫 턴부터 곧바로: SPEC.md 를 세 줄로 쓰고, colab_lane_delegate 로 \"Backend\" 에 \"src/pump.py 의 water_seconds 를 구현하라\", \"Frontend\" 에 \"src/ui.py 의 render 를 고쳐라\" 를 위임한 뒤 status \"done\". 두 위임이 끝나 다시 깨어나면 세션 메시지의 마지막 \"FRONTEND-DIFF <id>\" 를 찾아 colab_message_post 로 \"[@QA](mention://agent/QA_ID) 리뷰 부탁합니다. FRONTEND-DIFF <id>\" 를 게시하고 status \"done\". QA 의 반려가 보이면 확인 한 줄만 게시하고 status \"done\". $RULES"
BE_INS="너는 Backend(engineer)다. src/pump.py 의 water_seconds 를 moisture<30 이면 5, 아니면 0 으로 고치고 \`git diff --stat\` 한 번, colab_artifact_submit type \"diff\" name \"backend\"(file 없이), \"BACKEND-DIFF <id>\" 게시, status \"done\". 커밋·add 금지. $RULES"
FE_INS="너는 Frontend(engineer)다. src/ui.py 의 render 가 \"planter: \" 를 앞에 붙이게 고치고 \`git diff --stat\` 한 번, colab_artifact_submit type \"diff\" name \"frontend\"(file 없이), \"FRONTEND-DIFF <id>\" 게시, status \"done\". 수정 요청을 받으면 src/ui.py 첫 줄에 \`# QA-FIX-9421\` 을 넣고 같은 절차(name 그대로). 커밋·add 금지. $RULES"
QA_INS="너는 QA(reviewer)다. 도구는 셸의 \`colab\` 명령이다. frontend 아티팩트만 본다. \`colab session messages --limit 50\` 에서 마지막 \"FRONTEND-DIFF <id>\" 를 찾아 \`colab artifact get <id> --out ./fe.diff\` 로 받는다(남의 작업 디렉토리는 열지 않는다). \`QA-FIX-9421\` 이 있으면 \`colab review approve --artifact <id> --note 승인\`, 없으면 \`colab review reject --artifact <id> --reason \"src/ui.py 맨 첫 줄에 주석 # QA-FIX-9421 한 줄을 추가해 주세요\"\`. 정확히 한 번. 그리고 \`colab status set done\`. $RULES"
GOAL='실내 화분 자동 급수기 패널의 급수 시간 계산과 상태 표시를 구현한다'
TITLE="scenario-b-$STAMP"; SLUG="$TITLE"

# 페이크 대본에 편집 카드를 붙인다(B9 파일 편집 카드) — 실기는 어댑터가 낸다.
edit_turn() { # edit_turn ROLE PATH → turns JSON: tool_call(edit) → exec(agent.sh ROLE) → tool_call_update
  jq -nc --arg role "$1" --arg p "$2" --arg fix "$FIX" \
    '{turns:[{steps:[{tool_call:{id:"e1",title:("edit "+$p),kind:"edit",path:$p}},
                     {exec:("bash "+$fix+"/agent.sh "+$role)},
                     {tool_update:{id:"e1",status:"completed",path:$p,old_text:"",new_text:"edited"}}]}]}'
}

step "0. 실험 저장소 (이 저장소가 아니다) + claim 탭"
mkdir -p "$P5_TMP_ROOT/73"
make_repo "$REPO" "git@example.invalid:planter-$STAMP.git"
REPO_HEAD_BEFORE="$(git -C "$REPO" rev-parse HEAD)"
CLAUDE_MD_BEFORE="$(shasum "$REPO/CLAUDE.md" | cut -d' ' -f1)"; AGENTS_MD_BEFORE="$(shasum "$REPO/AGENTS.md" | cut -d' ' -f1)"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "repo $REPO · tap :$TAP_PORT · RUNTIME=$RUNTIME"

step "1. 계정 · 페어링(capacity 4, repos=[repo]) · 에이전트 4 (QA 만 hermes kind)"
signup "i5b+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G9 Scenario B $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4 "$REPO"
RUNTIME_ID="$(runtime_of_config "$CFG")"
QA="$(create_agent_fake "$WS" QA reviewer hermes "$HERMES_MODEL" "$QA_INS" 'diff 아티팩트를 리뷰한다')"
BE="$(create_agent_fake "$WS" Backend engineer claude_code "$MODEL" "$BE_INS" '급수 시간 계산을 구현한다' "$(edit_turn Backend src/pump.py)")"
FE="$(create_agent_fake "$WS" Frontend engineer claude_code "$MODEL" "$FE_INS" '패널 표시를 구현한다' "$(edit_turn Frontend src/ui.py)")"
PM_TEXT="${PM_INS/QA_ID/$QA}"
PM="$(create_agent_fake "$WS" PM lead claude_code "$MODEL" "$PM_TEXT" '스펙을 쓰고 위임한다')"
ok "PM=$PM BE=$BE FE=$FE QA=$QA runtime=$RUNTIME_ID"

step "2. 세션 — 격리 worktree · 종료 조건 agent_approval(QA) 단독"
S="$(create_session_p4 "$WS" "$TITLE" "$GOAL" "$PM" "$RUNTIME_ID" "$REPO" "$(cond_agent_approval "$QA")" '{}' "$PM" "$BE" "$FE" "$QA")"
[ -n "$S" ] && [ "$S" != null ] || die "세션 생성 실패"
T_PM="$(session_initial_task "$S")"
chk B1  "worktree 격리 세션이 열린다 (repo_path 검증 통과)" yes "$( [ -n "$S" ] && echo yes || echo no )"
chk B1b "isolation.kind = worktree" worktree "$(psqlq "select isolation->>'kind' from session where id='$S'")"
chk B1c "종료 조건 = agent_approval 단독" agent_approval "$(psqlq "select completion_condition->'conditions'->0->>'type' from session where id='$S'")"
T0="$(now_ms)"

step "2b. 데몬 기동"
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-73.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
python3 - "$TAP" "$T_PM" > "$OUT/73-pm-bundle.json" <<'PY' &
import json, sys, time
tap, task = sys.argv[1], sys.argv[2]
for _ in range(600):
    for line in open(tap):
        try: rec = json.loads(line)
        except Exception: continue
        for b in ((rec.get("body") or {}).get("tasks") or []):
            if b.get("task", {}).get("id") == task:
                json.dump(b, sys.stdout, ensure_ascii=False, indent=1); sys.exit(0)
    time.sleep(1)
PY
BUNDLE_PID=$!

step "3. B1·B2 — PM 위임 → 워크트리 2개 · 브랜치 colab/<S>/<agent>"
wait_until $T_TURN '[ "$(lanes_count "'"$S"'" Backend)" -ge 1 ] && [ "$(lanes_count "'"$S"'" Frontend)" -ge 1 ]' || bad "PM 위임이 두 lane 을 만들지 않았다"
chk B2  "Backend lane 1개"  1 "$(lanes_count "$S" Backend)"
chk B2b "Frontend lane 1개" 1 "$(lanes_count "$S" Frontend)"
WT_BE="$WORK/worktrees/$SLUG/backend"; WT_FE="$WORK/worktrees/$SLUG/frontend"
wait_until $T_TURN '[ -d "'"$WT_BE"'" ] && [ -d "'"$WT_FE"'" ]' || bad "워크트리 두 개가 준비되지 않았다"
chk B2c "Backend 워크트리 존재 ($WT_BE)"  yes "$( [ -d "$WT_BE" ] && echo yes || echo no )"
chk B2d "Frontend 워크트리 존재"          yes "$( [ -d "$WT_FE" ] && echo yes || echo no )"
chk B2e "Backend 브랜치 = colab/$SLUG/backend"   "colab/$SLUG/backend"  "$(git -C "$WT_BE" symbolic-ref --short HEAD 2>/dev/null || echo 없음)"
chk B2f "Frontend 브랜치 = colab/$SLUG/frontend" "colab/$SLUG/frontend" "$(git -C "$WT_FE" symbolic-ref --short HEAD 2>/dev/null || echo 없음)"
wait "$BUNDLE_PID" 2>/dev/null || true
PM_WD="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('workdir',{}).get('path',''))" "$OUT/73-pm-bundle.json" 2>/dev/null || echo '')"
chk X1  "TaskBundle 의 workdir.path 가 절대 경로다 (§4.1)" yes "$( [ -n "$PM_WD" ] && [ "${PM_WD#/}" != "$PM_WD" ] && echo yes || echo no )"
chk X1c "체크아웃이 사용자 저장소 안에 생기지 않는다 (FR-6.4)" 0 \
  "$(git -C "$REPO" worktree list --porcelain 2>/dev/null | awk -v r="$REPO/" '/^worktree /{p=substr($0,10); if (index(p,r)==1) n++} END{print n+0}')"

step "4. B3 — 각자 diff 아티팩트 제출"
wait_until $T_TURN '[ "$(psqlq "select count(*) from artifact where session_id='"'$S'"' and type='"'diff'"'")" -ge 2 ]' || bad "diff 아티팩트 두 개가 오지 않았다"
artifact_rows "$S" > "$OUT/73-artifacts-1.txt"
chk B3  "diff 아티팩트 2개" 2 "$(psqlq "select count(distinct name) from artifact where session_id='$S' and type='diff'")"
chk B3b "전부 type=diff"    0 "$(psqlq "select count(*) from artifact where session_id='$S' and type<>'diff'")"
A_FE="$(psqlq "select id from artifact where session_id='$S' and name like 'frontend%' order by version desc limit 1")"
chk B3d "frontend 아티팩트 id" yes "$( [ -n "$A_FE" ] && echo yes || echo no )"
if [ -n "$A_FE" ]; then
  curl -sS -o "$OUT/73-fe-v1.diff" -b "$COOKIE" "$API/artifacts/$A_FE/content"
  chk B3e "frontend diff 가 git apply --check 를 통과한다" yes "$(git -C "$REPO" apply --check "$OUT/73-fe-v1.diff" >/dev/null 2>&1 && echo yes || echo no)"
fi

step "5. B4 — QA 가 깨어난다 · QA 번들에 남의 workdir 경로 0 (E13-08)"
wait_until $T_TURN '[ -n "$(latest_task "'"$S"'" QA)" ]' || bad "QA task 가 생기지 않았다"
T_QA="$(latest_task "$S" QA)"
wait_until $T_TURN '[ -n "$(tap_prompt "'"$TAP"'" "'"$T_QA"'" 1 2>/dev/null)" ]' || true
python3 - "$TAP" "$T_QA" > "$OUT/73-qa-bundle.json" <<'PY' || true
import json, sys
tap, task = sys.argv[1], sys.argv[2]
for line in open(tap):
    try: rec = json.loads(line)
    except Exception: continue
    for b in ((rec.get("body") or {}).get("tasks") or []):
        if b.get("task", {}).get("id") == task:
            json.dump(b, sys.stdout, ensure_ascii=False, indent=1); sys.exit(0)
PY
chk B4  "QA 번들에 Backend·Frontend 워크트리 경로 0건 (E13-08)" 0 "$(cnt "$OUT/73-qa-bundle.json" "worktrees/$SLUG/backend" "worktrees/$SLUG/frontend")"
QA_WD="$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('workdir',{}).get('path',''))" "$OUT/73-qa-bundle.json" 2>/dev/null || echo '')"
chk B4b "QA 번들 workdir 은 QA 자기 것 (${QA_WD:-없음})" yes "$( [ "${QA_WD%/qa}" != "$QA_WD" ] && echo yes || echo no )"
chk B4c "QA 브리프 전송 = instruction_file (hermes)" instruction_file "$(python3 -c "import json,sys;print(json.load(open(sys.argv[1])).get('brief',{}).get('transport',''))" "$OUT/73-qa-bundle.json" 2>/dev/null || echo '-')"

step "6. B5 — QA 수정 요청 → Frontend 기존 lane 재진입 (규칙 1, 새 lane 0)"
LANE_FE="$(lane_of "$S" Frontend)"
wait_until $T_TURN '[ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'$S'"' and a.name='"'Frontend'"'")" -ge 2 ]' || bad "Frontend 재진입 task 가 오지 않았다"
chk B5  "Frontend lane 은 여전히 1개 (새 lane 0)" 1 "$(lanes_count "$S" Frontend)"
chk B5b "Frontend task 2개 (재진입)" 2 "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Frontend'")"
chk B5c "재진입 task 의 lane 이 같다" "$LANE_FE" "$(task_field "$(latest_task "$S" Frontend)" lane_id)"
chk B5d "Frontend 워크트리는 여전히 1개" 1 "$(worktrees_of "$S" Frontend)"
REJ_MSG="$(psqlq "select id from message where session_id='$S' and author_type='agent' and parent_id is not null and content like '%반려%' order by created_at limit 1")"
chk B5f "QA 의 반려가 Frontend 스레드에 답글로 게시됐다" yes "$( [ -n "$REJ_MSG" ] && echo yes || echo no )"
REJ_TRIG=0; [ -n "$REJ_MSG" ] && REJ_TRIG="$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='Frontend' and t.trigger_message_id='$REJ_MSG'")"
chk B5g "반려 답글이 그 자체로 Frontend 를 재진입시킨다" yes "$( [ "${REJ_TRIG:-0}" -ge 1 ] && echo yes || echo no )"
chk B5e "결정 기록에 리뷰가 남았다 (source=agent)" yes "$( [ "$(psqlq "select count(*) from decision where session_id='$S' and source='agent'")" -ge 1 ] && echo yes || echo no )"
T_FE2="$(latest_task "$S" Frontend)"
wait_until $T_TURN '[ "$(task_field "'"$T_FE2"'" status)" = completed ]' || true
chk B5h "재진입 attempt 가 lane 의 런타임 세션을 resume 했다 (PRD B 5단계)" true "$(psqlq "select coalesce(resumed::text,'-') from task_attempt where task_id='$T_FE2' and attempt=1")"

step "7. B6 — 새 diff = version 2 → 서버가 QA 를 깨운다 → approve → completed"
wait_until $T_TURN '[ "$(psqlq "select coalesce(max(version),0) from artifact where session_id='"'$S'"' and name like '"'frontend%'"'")" -ge 2 ]' || bad "frontend version 2 가 오지 않았다"
chk B6  "frontend 아티팩트 version 2 (FR-4.3)" 2 "$(psqlq "select max(version) from artifact where session_id='$S' and name like 'frontend%'")"
wait_until $T_TURN '[ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='"'$S'"' and a.name='"'QA'"'")" -ge 2 ]' || true
chk B6b "QA 가 두 번째로 깨어났다 (FR-6.5)" yes "$( [ "$(psqlq "select count(*) from task t join agent a on a.id=t.agent_id where t.session_id='$S' and a.name='QA'")" -ge 2 ] && echo yes || echo no )"
wait_until $T_TURN '[ "$(sess_status "'"$S"'")" = completed ]' || bad "세션이 completed 로 가지 않았다 (현재: $(sess_status "$S"))"
chk B7  "세션 completed" completed "$(sess_status "$S")"
chk B7b "사람 승인 HITL 0건 (agent_approval 단독, E6-05)" 0 "$(psqlq "select count(*) from hitl_request where session_id='$S' and purpose='user_approval'")"

step "8. B7 — 워크트리 위생 (E13-03~06, §8.4)"
wait_quiet "$S" 300 || true; sleep 3
for pair in "Backend:$WT_BE" "Frontend:$WT_FE"; do
  who="${pair%%:*}"; wt="${pair#*:}"; low="$(printf '%s' "$who" | tr 'A-Z' 'a-z')"
  git -C "$wt" status --porcelain > "$OUT/73-status-$low.txt" 2>&1 || true
  chk "B8-$low"  "$who: COLAB_BRIEF.md 없음"     0 "$(ls "$wt/COLAB_BRIEF.md" 2>/dev/null | wc -l | tr -d ' ')"
  chk "B8b-$low" "$who: 추적 중 CLAUDE.md 무변경" "$CLAUDE_MD_BEFORE" "$(shasum "$wt/CLAUDE.md" 2>/dev/null | cut -d' ' -f1)"
  chk "B8c-$low" "$who: 추적 중 AGENTS.md 무변경" "$AGENTS_MD_BEFORE" "$(shasum "$wt/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
  chk "B8d-$low" "$who: exclude 에 COLAB_BRIEF 0" 0 "$(cnt "$REPO/.git/info/exclude" 'COLAB_BRIEF')"
  chk "B8e-$low" "$who: status 에 COLAB_BRIEF·wrapper 잔여물 0" 0 "$(cnt "$OUT/73-status-$low.txt" 'COLAB_BRIEF' '\.colab')"
done
WT_QA="$WORK/worktrees/$SLUG/qa"
if [ -d "$WT_QA" ]; then
  chk B8-qa  "QA(hermes): COLAB_BRIEF.md 없음 (§8.4)" 0 "$(ls "$WT_QA/COLAB_BRIEF.md" 2>/dev/null | wc -l | tr -d ' ')"
  chk B8b-qa "QA(hermes): AGENTS.md 무변경 (M3)" "$AGENTS_MD_BEFORE" "$(shasum "$WT_QA/AGENTS.md" 2>/dev/null | cut -d' ' -f1)"
else chk_na B8-qa "QA 워크트리" 없음 "QA 턴이 워크트리를 만들지 않았다"; fi
chk B8g "원본 저장소 HEAD 무변경" "$REPO_HEAD_BEFORE" "$(git -C "$REPO" rev-parse HEAD)"
chk B8h "원본 저장소 git status 클린" yes "$(repo_status_clean "$REPO")"

step "9. B8 — 요약 1개 + generated_by · B9 피드 카드"
chk B9  "session_summary 메시지 정확히 1개" 1 "$(summary_count "$S")"
GB="$(psqlq "select coalesce(e.object_ref::text,'-') from task_event e join task t on t.id=e.task_id where t.session_id='$S' and e.object_ref::text like '%generated_by%' limit 1")"
chk B9c "generated_by 피드 항목 ($GB)" yes "$(printf '%s' "$GB" | grep -q generated_by && echo yes || echo no)"
chk B10  "파일 편집 카드 ≥ 2 (tool/edit_file)" yes "$( [ "$(feed_has "$S" tool edit_file)" -ge 2 ] && echo yes || echo no )"
chk B10b "셸 카드 ≥ 1 (tool/run_shell)"        yes "$( [ "$(feed_has "$S" tool run_shell)" -ge 1 ] && echo yes || echo no )"
ELAPSED=$(( ($(now_ms) - T0) / 1000 ))

step "결과"
printf '판정: PASS %d · FAIL %d · %ds\n' "$pass" "$fail" "$ELAPSED" >&2
jq -n --arg ws "$WS" --arg s "$S" --arg mode "$RUNTIME" --argjson elapsed_s "$ELAPSED" --argjson pass "$pass" --argjson fail "$fail" \
  '{runtime_mode:$mode,workspace:$ws,session:$s,elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/73.json"
[ "$fail" = 0 ]
