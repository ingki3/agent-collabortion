#!/usr/bin/env bash
# e2e/p4/63_offline_rebind.sh — T-I4 (c): **오프라인 유예 → paused → 재바인딩 → diff 재적용**
# (EVAL E14-02·03·04·05·06·08·10, FR-9.2, daemon-protocol §4.3, harness §10).
#
#   R1  런타임 B 준비 — **같은 remote URL 의 다른 경로**(경로 문자열 비교가 아니다, E14-04·05)
#   R2  A 가 사라진다(데몬 종료) → `offline_since` 를 8일 전으로(§0-13 클럭 우회, 아래 주석)
#   R3  스윕 2회 → 세션 `paused(runtime_offline)` + Director 인박스 **1건**(두 번째 스윕에도 1건, E14-10)
#   R4  후보 조회 — B 는 후보, **다른 remote 를 가진 C 는 제외**(E14-05)
#   R5  재바인딩 → `rebind_prepare` 큐잉 → 데몬 B 가 `<workdir_root>/.colab/rebind/<S>/` 로 내려받는다
#   R6  **NN2(#168 리뷰)**: `rebind_prepare` 가 첫 claim 보다 먼저 처리되는가 — 실측한다.
#       (같은 claim 응답에 명령과 task 가 함께 오고 데몬은 `go d.rebindPrepare(...)` 로 비동기다 →
#        어긋나면 `<rebind>` 프롬프트가 가리키는 디렉터리가 빈 채로 턴이 시작된다)
#   R7  첫 턴 프롬프트: `{{COLAB_REBIND_DIR}}/manifest.json` · 제출 순서 · 콜드 스타트
#   R8  에이전트가 diff 를 **순서대로** 적용해 이어간다 (E14-06)
#   R9  실서버 `listArtifacts` 의 순서가 **제출순(오름차순)** 인가 (T-W5 가 목에서 최신순을 고쳤다)
#   R10 활성 세션이 걸린 런타임 삭제 = 409 (E14-08)
#
# 실험 저장소는 이 저장소가 아니다(§0-18). 산출물: out/63-checks.tsv · out/63-*.txt
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-63.txt"; rm -f "$COOKIE"
BASE="$P4_TMP_ROOT/63"
REPO_A="$BASE/repo-a"; REPO_B="$BASE/repo-b"; REPO_C="$BASE/repo-c"
WORK_A="$BASE/work-a"; WORK_B="$BASE/work-b"; WORK_C="$BASE/work-c"
CFG_A="$OUT/daemon-63a.json"; CFG_B="$OUT/daemon-63b.json"; CFG_C="$OUT/daemon-63c.json"
LOG_A="$OUT/daemon-63a.log"; LOG_B="$OUT/daemon-63b.log"; LOG_C="$OUT/daemon-63c.log"
TAP="$OUT/tap-63.jsonl"; TAP_PORT="${TAP_PORT_63:-8116}"
MODEL="${LEAD_MODEL}"
REMOTE="git@example.invalid:planter-$STAMP.git"
EMAIL="i4r+$STAMP@example.com"; PASSWORD="password123"
g5_chk_init "$OUT/63-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  for f in "$OUT"/daemon-63a.pid "$OUT"/daemon-63b.pid "$OUT"/daemon-63c.pid; do daemon_stop "$f"; done
  return 0
}
trap cleanup EXIT

wait_until() { local dl=$(( $(date +%s) + $1 )); shift; while [ "$(date +%s)" -lt "$dl" ]; do eval "$1" && return 0; sleep 3; done; return 1; }
sess_status() { psqlq "select status::text from session where id='$1'"; }

DEV_RULES="$P4_RULES"
DEV1_INS="너는 dev1(engineer)이다. 한국어로 짧게 답한다. 작업 디렉토리는 작은 장난감 저장소의 git 워크트리다.
첫 턴부터 곧바로 아래를 순서대로 한다.
1. src/pump.py 의 water_seconds 를 고쳐 moisture 가 30 미만이면 5, 아니면 0 을 돌려주도록 구현한다.
2. colab_artifact_submit 을 type \"diff\", name \"step-1\" 로 부른다. **file 은 주지 않는다**.
3. colab_message_post 로 \"STEP1 <artifact id>\" 를 게시하고 colab_status_set 으로 status \"done\".
커밋하지 마라.
**턴 프롬프트에 재바인딩 안내(<rebind>)가 있으면 그 지시를 글자 그대로 먼저 수행한다** — manifest.json 을 읽고
거기 적힌 순서대로 diff 파일을 \`git apply\` 로 적용한 뒤, 무엇을 적용했는지 colab_message_post 로 한 줄 보고하고
status \"done\" 을 부른다. 아티팩트를 다시 내려받지 마라 — 이미 디스크에 있다.
$DEV_RULES"
DEV2_INS="너는 dev2(engineer)다. 한국어로 짧게 답한다. 작업 디렉토리는 작은 장난감 저장소의 git 워크트리다.
첫 턴부터 곧바로 아래를 순서대로 한다.
1. src/ui.py 의 render 를 고쳐 \"planter: \" 를 앞에 붙여 돌려주도록 한다.
2. colab_artifact_submit 을 type \"diff\", name \"step-2\" 로 부른다. **file 은 주지 않는다**.
3. colab_message_post 로 \"STEP2 <artifact id>\" 를 게시하고 colab_status_set 으로 status \"done\".
커밋하지 마라.
$DEV_RULES"

step "0. 실험 저장소 3개 (A·B 는 같은 remote, C 는 다른 remote) + claim 탭"
rm -rf "$BASE"; mkdir -p "$BASE"
make_repo "$REPO_A" "$REMOTE"
git clone -q "$REPO_A" "$REPO_B"
git -C "$REPO_B" remote set-url origin "$REMOTE"
git -C "$REPO_B" config user.email i4@test; git -C "$REPO_B" config user.name "i4 e2e"
make_repo "$REPO_C" "git@example.invalid:other-$STAMP.git"
ok "A=$REPO_A · B=$REPO_B (같은 remote $REMOTE) · C=$REPO_C (다른 remote)"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 런타임 3대 페어링"
: > "$LOG_A"; : > "$LOG_B"; : > "$LOG_C"
signup "$EMAIL" "$PASSWORD" Director >/dev/null
WS="$(create_workspace "G7 Rebind $STAMP")"
pair_one() { # NAME CFG WORKROOT REPO → runtime_id
  local pid ptok
  read -r pid ptok <<<"$(create_pairing "$WS" | tr '\t' ' ')"
  PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p4 "$ptok" "$2" "$3" 2 "$4"
  runtime_of_config "$2"
}
RA="$(pair_one A "$CFG_A" "$WORK_A" "$REPO_A")"
RB="$(pair_one B "$CFG_B" "$WORK_B" "$REPO_B")"
RC="$(pair_one C "$CFG_C" "$WORK_C" "$REPO_C")"
ok "A=$RA B=$RB C=$RC"
D1="$(create_agent_p2 "$WS" dev1 engineer "$MODEL" "$DEV1_INS" '급수 시간 계산')"
D2="$(create_agent_p2 "$WS" dev2 engineer "$MODEL" "$DEV2_INS" '패널 표시')"

step "2. 세션(A) — 우회: workdir 절대 경로 선행 삽입 (61_ X1 의 결함)"
TITLE="rebind-$STAMP"; SLUG="$TITLE"
S="$(create_session_p4 "$WS" "$TITLE" '급수 시간 계산과 상태 표시를 두 조각으로 구현한다' "$D1" "$RA" "$REPO_A" \
     "$(jq -nc '{op:"and",conditions:[{type:"manual"}]}')" '{}' "$D1" "$D2")"
seed_worktree_workdirs "$S" "$WORK_A" "$SLUG" "$D1:dev1" "$D2:dev2"
T1="$(session_initial_task "$S")"
ok "session $S · dev1 task $T1"

step "3. 데몬 A·B·C 기동 → dev1 · dev2 가 diff 를 순서대로 제출"
# **B 도 처음부터 띄운다**: 재바인딩 후보 판정은 온라인 런타임만 본다(`JudgeCandidate` —
# "오프라인 — 이 컴퓨터의 데몬이 연결돼 있지 않습니다"). 세션은 A 에 묶여 있으므로 B 는 논다.
daemon_run "$CFG_A" "$LOG_A" > "$OUT/daemon-63a.pid"
daemon_run "$CFG_B" "$LOG_B" > "$OUT/daemon-63b.pid"
daemon_run "$CFG_C" "$LOG_C" > "$OUT/daemon-63c.pid"
wait_until 1200 '[ "$(psqlq "select count(*) from artifact where session_id='"'$S'"' and name='"'step-1'"'")" -ge 1 ]' \
  || bad "step-1 아티팩트가 오지 않았다"
post_message "$S" "$(mention dev2 "$D2") 패널 표시를 맡아 주세요" >/dev/null
wait_until 1200 '[ "$(psqlq "select count(*) from artifact where session_id='"'$S'"' and name='"'step-2'"'")" -ge 1 ]' \
  || bad "step-2 아티팩트가 오지 않았다"
artifact_rows "$S" > "$OUT/63-artifacts.txt"
A1="$(psqlq "select id from artifact where session_id='$S' and name='step-1' limit 1")"
A2="$(psqlq "select id from artifact where session_id='$S' and name='step-2' limit 1")"
chk R0  "diff 아티팩트 2개 (step-1 → step-2)" 2 "$(psqlq "select count(*) from artifact where session_id='$S' and type='diff'")"

step "4. R9 — 실서버 listArtifacts 가 **제출순(오름차순)** 인가"
api_ok GET "/sessions/$S/artifacts" > "$OUT/63-list-artifacts.json"
FIRST="$(jq -r 'if type=="array" then .[0].name else .items[0].name end' "$OUT/63-list-artifacts.json" 2>/dev/null || echo '-')"
ORDER_LIST="$(jq -r 'if type=="array" then . else .items end | map(.name) | join(",")' "$OUT/63-list-artifacts.json" 2>/dev/null || echo '-')"
ok "listArtifacts 순서 = $ORDER_LIST"
chk R9  "listArtifacts 첫 항목 = step-1 (제출순, 재바인딩 재적용 순서 = 이것)" step-1 "$FIRST"

step "5. R2·R3 — A 가 사라진다 → 유예 7일 → paused(runtime_offline)"
daemon_stop "$OUT/daemon-63a.pid"
sleep 5
post_message "$S" "$(mention dev1 "$D1") 이어서 마무리해 주세요" >/dev/null
T_RESUME="$(latest_task "$S" dev1)"
# **클럭 우회(§0-13)**: 서버 바이너리에 클럭 주입 경로가 없다(`clock.Real{}` 고정). 7일을
# 기다릴 수 없으므로 런타임이 8일 전부터 오프라인이었던 것으로 만든다 — 56_ 와 같은 방법.
psqlq "update runtime set status='offline', offline_since = now() - interval '8 days',
       last_seen_at = now() - interval '8 days' where id='$RA'" >/dev/null
sleep 65
chk R3  "세션 = paused"                    paused          "$(sess_status "$S")"
chk R3b "paused_reason = runtime_offline"  runtime_offline "$(psqlq "select coalesce(paused_reason::text,'-') from session where id='$S'")"
chk R3c "Director 인박스 runtime_offline 1건" 1 "$(inbox_count "$S" runtime_offline)"
sleep 65
chk R3d "두 번째 스윕 뒤에도 1건 — 멱등 (E14-10)" 1 "$(inbox_count "$S" runtime_offline)"

step "6. R4 — 후보 조회: B 는 후보, C 는 제외 (E14-05)"
api_ok GET "/workspaces/$WS/runtime-candidates?isolation=worktree&session_id=$S" > "$OUT/63-candidates.json" || true
# jq 의 `//` 는 **false 도 비어 있는 것으로 본다** — `.eligible // "없음"` 은 후보가 아닌 런타임을
# 전부 "없음" 으로 만든다(실측). tostring 으로 받는다.
cand_eligible() { jq -r --arg r "$1" '([.candidates[] | select(.runtime.id==$r)] | .[0]) as $c | if $c == null then "없음" else ($c.eligible|tostring) end' "$OUT/63-candidates.json" 2>/dev/null || echo '없음'; }
CB="$(cand_eligible "$RB")"; CC="$(cand_eligible "$RC")"
ok "후보: B=$CB C=$CC ($(jq -r '[.candidates[] | .runtime.name + "=" + (.eligible|tostring)] | join(" ")' "$OUT/63-candidates.json" 2>/dev/null || echo '-'))"
chk R4  "B(같은 remote, 다른 경로) = 후보"  true  "$CB"
chk R4b "C(다른 remote) = 후보 아님 (E14-05)" false "$CC"

step "7. R5·R6 — 재바인딩 → rebind_prepare 와 첫 claim 의 순서"
# 우회 2 (61_ X1 의 결함과 같은 뿌리): 재바인딩은 옛 workdir 행을 `retained` 로만 바꾸는데
# `BundleWorkdirPaths` 는 그 행을 계속 돌려주므로 새 machine 의 번들이 사라진 컴퓨터의 경로를
# 가리킨다. 옛 행을 접고 B 의 경로를 시드한다.
RB_CODE="$(api POST "/sessions/$S/rebind" "$(jq -nc --arg r "$RB" '{runtime_id:$r,acknowledge_loss:true}')" | api_code)"
chk R5  "rebind = 200 (E14-03)" 200 "$RB_CODE"
chk R5b "rebind_prepare 명령 1건 큐잉 (§4.3)" yes \
  "$( [ "$(psqlq "select count(*) from daemon_command where type='rebind_prepare' and session_id='$S'")" -ge 1 ] && echo yes || echo no )"
chk R5c "lane 의 runtime_session_ref 가 비었다 → 콜드 스타트" 0 \
  "$(psqlq "select count(*) from lane where session_id='$S' and runtime_session_ref is not null")"
# **관측**: A 가 사라진 동안 그 런타임으로 dispatch 된 task 는 `failed(timeout)` 로 끝난다.
# `rebindSession` 은 `queued`·`deferred` 만 되살리므로(runtimes/offline.go) 그 task 는 되살아나지 않는다.
PREV_ST="$(task_field "$T_RESUME" status)"
PREV_OK=no
if [ "$PREV_ST" = queued ] || [ "$PREV_ST" = running ] || [ "$PREV_ST" = completed ]; then PREV_OK=yes; fi
chk R5g "오프라인 동안 dispatch 된 task 가 재바인딩으로 되살아난다 (실제: $PREV_ST)" yes "$PREV_OK"
retire_workdirs "$S"
seed_worktree_workdirs "$S" "$WORK_B" "$SLUG" "$D1:dev1" "$D2:dev2"
REBIND_DIR="$WORK_B/.colab/rebind/$S"
# 새 컴퓨터에서 실제로 한 턴을 돌린다 — `session.rebind_prompt` 는 completed finish 전까지 남으므로
# 이 task 의 번들에 `<rebind>` 구간이 실린다(S-53, 57_).
post_message "$S" "$(mention dev1 "$D1") 이어서 마무리해 주세요" >/dev/null
wait_until 300 '[ -n "$(latest_task "'"$S"'" dev1)" ]' || true
T_RESUME="$(latest_task "$S" dev1)"
ok "재바인딩 뒤 dev1 task = $T_RESUME"
wait_until 600 '[ -f "'"$REBIND_DIR"'/manifest.json" ]' || bad "rebind manifest 가 오지 않았다"
cp "$REBIND_DIR/manifest.json" "$OUT/63-manifest.json" 2>/dev/null || true
chk R5h "재바인딩이 세션의 저장소 경로를 **새 컴퓨터의 것**으로 옮긴다 (listRuntimeCandidates.matched_repo)" \
  "$REPO_B" "$(psqlq "select coalesce(isolation->>'repo_path','-') from session where id='$S'")"
chk R5d "다운로드 위치 = <workdir_root>/.colab/rebind/<S> (체크아웃 밖, §4.3)" yes \
  "$( [ -f "$REBIND_DIR/manifest.json" ] && echo yes || echo no )"
chk R5e "manifest 에 아티팩트 2개가 제출 순서대로" "$A1 $A2" \
  "$(jq -r '[.artifacts[] | .id] | join(" ")' "$OUT/63-manifest.json" 2>/dev/null || echo '-')"
chk R5f "manifest 의 모든 아티팩트가 실제로 내려받아졌다 (error 0)" 0 \
  "$(jq -r '[.artifacts[] | select((.error // "") != "")] | length' "$OUT/63-manifest.json" 2>/dev/null || echo -1)"
# R6 — NN2(#168 리뷰) 순서 실측. **이 빌드에서는 잴 수 없다**: 아래 R5f 대로 다운로드가 전부
# 401 로 실패하고 `rebind_prepare` 는 30초마다 재발행돼 manifest 의 mtime 이 계속 갱신된다 —
# "명령이 첫 claim 보다 먼저 처리됐는가" 를 가릴 시각이 남지 않는다. 다운로드가 성공하게 되면
# 이 자리에서 다시 잰다.
wait_until 900 '[ "$(task_field "'"$T_RESUME"'" status)" = completed ] || [ "$(task_field "'"$T_RESUME"'" status)" = failed ]' || true
M_TS="$(python3 -c "import os,sys;print(int(os.path.getmtime(sys.argv[1])))" "$REBIND_DIR/manifest.json" 2>/dev/null || echo 0)"
D_TS="$(psqlq "select coalesce(extract(epoch from dispatched_at)::bigint::text,'0') from task where id='$T_RESUME'")"
P_TS="$(psqlq "select coalesce(extract(epoch from started_at)::bigint::text,'0') from task where id='$T_RESUME'")"
chk_na R6 "manifest 가 그 attempt 보다 먼저 존재한다 (NN2)" "manifest=$M_TS dispatch=$D_TS running=$P_TS" \
  "다운로드가 401 로 전부 실패해 명령이 소비되지 않고 30초마다 재발행된다 (R5f)"
printf 'manifest_mtime=%s dispatched_at=%s started_at=%s\n' "$M_TS" "$D_TS" "$P_TS" > "$OUT/63-order.txt"

step "8. R7·R8 — 첫 턴 프롬프트 · diff 재적용"
wait_until 600 '[ -n "$(tap_prompt "'"$TAP"'" "'"$T_RESUME"'" 2>/dev/null)" ]' || true
tap_prompt "$TAP" "$T_RESUME" > "$OUT/63-rebind-prompt.txt" 2>/dev/null || true
has() { grep -qF -- "$1" "$OUT/63-rebind-prompt.txt" 2>/dev/null; }
chk R7  "<rebind> 구간이 있다 (S-53)"                      yes "$( has '<rebind>' && echo yes || echo no )"
chk R7b "{{COLAB_REBIND_DIR}}/manifest.json 를 가리킨다"   yes "$( has '{{COLAB_REBIND_DIR}}/manifest.json' && echo yes || echo no )"
chk R7c "자리표시자 정확히 1회 (harness §10)"              1 \
  "$({ grep -oF '{{COLAB_REBIND_DIR}}' "$OUT/63-rebind-prompt.txt" 2>/dev/null || true; } | wc -l | tr -d ' ')"
chk R7d "git apply 로 적용하라고 지시한다"                  yes "$( has 'git apply' && echo yes || echo no )"
chk R7e "콜드 스타트 문장이 있다 (E14-06)"                  yes "$( has '콜드 스타트' && echo yes || echo no )"
# pipefail 이 켜져 있다 — 무매치 grep 이 파이프라인을 실패시켜 스크립트가 조용히 끝난다.
I1="$({ grep -nF "$A1" "$OUT/63-rebind-prompt.txt" 2>/dev/null || true; } | head -1 | cut -d: -f1)"
I2="$({ grep -nF "$A2" "$OUT/63-rebind-prompt.txt" 2>/dev/null || true; } | head -1 | cut -d: -f1)"
ORDER2=no; [ -n "$I1" ] && [ -n "$I2" ] && [ "$I1" -le "$I2" ] && ORDER2=yes
chk R7f "프롬프트가 아티팩트를 제출 순서로 나열한다 (step-1 먼저)" yes "$ORDER2"
chk R7g "자리표시자 치환 실패로 죽지 않았다 (failed(config) 0)" 0 \
  "$(psqlq "select count(*) from task_attempt where task_id='$T_RESUME' and failure_kind='config'")"
# E14-06 실기 판정: 새 워크트리에 **두 diff 가 다 적용돼** 있는가.
WT_NEW="$WORK_B/worktrees/$SLUG/dev1"
wait_until 1500 '[ "$(task_field "'"$T_RESUME"'" status)" = completed ] || [ "$(task_field "'"$T_RESUME"'" status)" = failed ]' || true
git -C "$WT_NEW" status --porcelain > "$OUT/63-new-worktree-status.txt" 2>&1 || true
git -C "$WT_NEW" diff > "$OUT/63-new-worktree-diff.txt" 2>&1 || true
chk R8  "새 워크트리에 step-1 이 적용됐다 (src/pump.py)" yes \
  "$( grep -q 'pump.py' "$OUT/63-new-worktree-diff.txt" 2>/dev/null && echo yes || echo no )"
chk R8b "새 워크트리에 step-2 가 적용됐다 (src/ui.py)"   yes \
  "$( grep -q 'ui.py' "$OUT/63-new-worktree-diff.txt" 2>/dev/null && echo yes || echo no )"
chk R8c "재적용은 새 machine 의 워크트리에서 일어났다"    yes "$( [ -d "$WT_NEW" ] && echo yes || echo no )"

step "9. R10 — 활성 세션이 걸린 런타임 삭제 = 409 (E14-08)"
DEL="$(api DELETE "/runtimes/$RB" '')"
chk R10  "deleteRuntime = 409"                     409 "$(api_code <<<"$DEL")"
chk R10b "code = runtime_has_active_sessions"      runtime_has_active_sessions "$(api_body <<<"$DEL" | jq -r '.code // "-"')"

step "10. 비용 · 턴 수"
COST="$(psqlq "select coalesce(round(sum(cost_usd)::numeric,6)::text,'0') from task_usage u join task t on t.id=u.task_id where t.session_id='$S'")"
TURNS="$(psqlq "select count(*) from task_attempt ta join task t on t.id=ta.task_id where t.session_id='$S'")"
printf 'session=%s cost_usd=%s attempts=%s\n' "$S" "$COST" "$TURNS" | tee "$OUT/63-metrics.txt"

step "결과"
printf '  PASS %d · FAIL %d  (%s)\n' "$pass" "$fail" "$OUT/63-checks.tsv"
exit "$fail"
