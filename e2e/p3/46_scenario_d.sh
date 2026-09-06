#!/usr/bin/env bash
# e2e/p3/46_scenario_d.sh — T-I3 (f): **시나리오 D 재확인 — 프로파일 전환** (EVAL E16-D, E8-08 · E8-09).
#
# G5 에서 `e2e/p2/30_scenario_a_hermes.sh` 의 arm C·D 로 이미 통과한 항목을 **P3 빌드에서 다시** 잰다.
# 30_ 의 판정을 그대로 옮기고 두 가지를 더한다:
#   - 폴백 뒤 **아티팩트**가 같은 workdir 에서 제출된다(E16-D "workdir·아티팩트 유지" 의 아티팩트 쪽)
#   - 폴백 attempt 의 프로파일·런타임이 바뀌었는데 workdir 행은 하나 그대로다
# 30_ 전체(시나리오 A 를 Hermes 로 끝까지)를 다시 돌리지 않는다 — G6 이 재확인하는 것은 **전환**이고,
# Hermes 로 도는 시나리오 A 자체는 G5 판정에서 닫혔다.
#
#   C  hermes 프로파일이 실패(모델 오타) → 같은 머신의 claude_code 대체 프로파일로 재큐잉,
#      workdir 재사용, `runtime_session_ref` 는 새 런타임 것(resume 비움 → 콜드 스타트)
#   D  대체 프로파일이 없으면 → 재큐잉은 하되 다른 머신으로 넘기지 않고 Director 알림 1건
#
# 산출물: out/46-checks.tsv · out/46.json
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-46.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-46.json"; WORK="$OUT/work-46"; DLOG="$OUT/daemon-46.log"
MODEL="${LEAD_MODEL}"
BAD_MODEL="${BAD_MODEL:-claude-haiku-4-5-TYPO}"     # 오타 → 재시도 가능한 실패
g5_chk_init "$OUT/46-checks.tsv"

cleanup() { [ -f "$OUT/daemon-46.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-46.pid")" 2>/dev/null || true; }; return 0; }
trap cleanup EXIT

# 폴백 뒤 실제로 일을 끝내는지 보려면 대체 프로파일이 뭔가를 남겨야 한다 — 파일 하나 + 아티팩트 제출.
FB_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 짧은 안내문을 쓰는 작성자다. 답은 한국어로 짧게.

지시를 받으면:
  a. 현재 작업 디렉토리에 guide-y.md 를 만들어 제품 Y 안내문을 여덟 줄로 쓴다.
  b. colab_artifact_submit 을 부른다. type "doc", file 은 그 파일의 **절대 경로**, name 은 "product-y-guide.md".
  c. colab_status_set 으로 status "done". 이것이 턴의 마지막 도구 호출이다.
메시지는 게시하지 않는다. 웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라.'
GOAL='가상의 실내 화분 자동 급수기 제품 Y 의 짧은 안내문을 작업 디렉토리에 만든다'

step "1. 계정 · 페어링"
: > "$DLOG"
signup "g6f+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G6 Scenario D $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
daemon_pair_cap "$PTOK" "$CFG" "$WORK" 2
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$OUT/daemon-46.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
RT_JSON="$(api_ok GET "/runtimes/$RUNTIME")"
chk R0  "이 머신이 두 런타임을 다 광고한다 (전환할 상대가 있다)" yes \
  "$( [ -n "$(jq -c '.capabilities[]?|select(.kind=="hermes")' <<<"$RT_JSON")" ] && \
       [ -n "$(jq -c '.capabilities[]?|select(.kind=="claude_code")' <<<"$RT_JSON")" ] && echo yes || echo no )"
ok "ws=$WS runtime=$RUNTIME"
T0="$(now_ms)"

step "2. C — hermes 실패 → 같은 머신 claude_code 로 전환 (E8-08)"
FBA="$(create_agent_2profiles "$WS" Faller writer "$FB_INS" hermes "$BAD_MODEL" '[]' claude_code "$MODEL")"
# **우회(G5 S-24)**: openapi 는 `AgentProfileCreate.fallback_profile(_id)` 를 P2 로 두지만 서버는 생성 시
# 두 필드를 조용히 버리고 `updateAgentProfile` 은 501 이다. 정식 경로가 없어 DB 에 직접 쓴다 —
# 재는 것은 "연결이 있을 때 전환하는가"이고, 연결을 만드는 경로 자체는 S-24 로 열려 있다.
link_fallback "$FBA" primary spare
P_PRIMARY="$(profile_of "$FBA" primary)"; P_SPARE="$(profile_of "$FBA" spare)"
chk C0 "폴백 연결이 섰다 (정식 경로 부재 — S-24 우회)" "$P_SPARE" \
  "$(psqlq "select coalesce(fallback_profile_id::text,'-') from agent_profile where id='$P_PRIMARY'")"
FS="$(create_session_p3 "$WS" "제품 Y 안내문 (프로파일 전환)" "$GOAL" "$FBA" "$RUNTIME" '{}' "$FBA")"
FT="$(session_initial_task "$FS")"
WD_BEFORE=""
DEADLINE=$(( $(date +%s) + ${FALLBACK_S:-600} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ -z "$WD_BEFORE" ] && WD_BEFORE="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id=(select lane_id from task where id='$FT')")"
  case "$(task_field "$FT" status)" in completed|failed|cancelled) break;; esac
  sleep 4
done
echo "── task_attempt ──" >&2; task_attempts "$FT" | column -t -s $'\t' >&2
A1_KIND="$(psqlq "select coalesce(failure_kind::text,'-') from task_attempt where task_id='$FT' and attempt=1")"
chk C1  "attempt 1 이 실패했다 (hermes 모델 오타)"           yes "$( [ "$A1_KIND" != '-' ] && [ -n "$A1_KIND" ] && echo yes || echo no )"
A1_RETRYABLE=no; case "$A1_KIND" in other|network|stall|timeout) A1_RETRYABLE=yes;; esac
chk C1b "그 failure_kind 가 재시도 가능하다 (§8) — 관측 kind=$A1_KIND" yes "$A1_RETRYABLE"
chk C2  "재큐잉됐다 (attempt ≥ 2)"                           yes \
  "$( [ "$(task_field "$FT" attempt)" -ge 2 ] 2>/dev/null && echo yes || echo no )"
CUR_PROF="$(task_field "$FT" profile_id)"
chk C3  "task 프로파일이 spare(claude_code) 로 바뀌었다"      "$P_SPARE" "$CUR_PROF"
chk C3b "lane 프로파일도 같이 바뀌었다"                       "$P_SPARE" \
  "$(psqlq "select profile_id from lane where id='$(task_field "$FT" lane_id)'")"
chk C3c "최종 런타임 종류가 claude_code 다"                   claude_code \
  "$(psqlq "select runtime_kind from agent_profile where id='$CUR_PROF'")"
WD_AFTER="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id=(select lane_id from task where id='$FT')")"
chk C4  "**workdir 를 그대로 재사용한다** (§4.4 workdir.reuse)" "$WD_BEFORE" "$WD_AFTER"
chk C4b "workdir 행이 하나뿐이다 (새로 만들지 않았다)"         1 "$(psqlq "select count(*) from workdir where session_id='$FS'")"
FB_REF="$(lane_ref "$FS" Faller)"
chk C5  "폴백 뒤 runtime_session_ref 는 새 런타임 것이다 (resume 비움)" claude_code \
  "$(jq -r '.runtime_kind // "none"' <<<"$FB_REF")"
chk C6  "폴백 뒤 task 가 완료됐다 (전환이 실제로 일을 끝낸다)" completed "$(task_field "$FT" status)"
chk C6b "세션은 같은 머신에 남았다 (다른 머신으로 넘기지 않는다)" "$RUNTIME" \
  "$(psqlq "select runtime_id from session where id='$FS'")"

step "3. C 추가 — **아티팩트가 유지된 workdir 에서 제출된다** (E16-D \"workdir·아티팩트 유지\")"
ART="$(psqlq "select id from artifact where session_id='$FS' order by created_at desc limit 1")"
chk C7  "폴백한 프로파일이 아티팩트를 제출했다"               yes "$( [ -n "$ART" ] && echo yes || echo no )"
if [ -n "$ART" ]; then
chk C7b "아티팩트 이름이 지시한 그대로"                       product-y-guide.md \
  "$(psqlq "select name from artifact where id='$ART'")"
chk C7c "그 파일이 **같은 workdir** 안에 있다"                yes \
  "$( [ -f "$WD_AFTER/guide-y.md" ] && echo yes || echo no )"
fi
chk C7d "workdir 경로가 attempt 1 때와 같다 (한 번 더 확인)"  "$WD_BEFORE" "$WD_AFTER"

step "4. D — 대안이 없으면 queued 유지 + Director 알림 (E8-09)"
NFA="$(create_agent_kind "$WS" Lonely writer hermes "$BAD_MODEL" "$FB_INS" '대안 없는 프로파일')"
NS="$(create_session_p3 "$WS" "제품 Y 안내문 (대안 없음)" "$GOAL" "$NFA" "$RUNTIME" '{}' "$NFA")"
NT="$(session_initial_task "$NS")"
DEADLINE=$(( $(date +%s) + ${NOALT_S:-420} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ "$(psqlq "select count(*) from inbox_item where ref_id='$NT' and type='run_failed'")" -ge 1 ] && break
  sleep 3
done
echo "── task_attempt (대안 없음) ──" >&2; task_attempts "$NT" | column -t -s $'\t' >&2
chk D1  "Director 인박스에 run_failed 알림 1건 (E8-09)"       1 "$(psqlq "select count(*) from inbox_item where ref_id='$NT' and type='run_failed'")"
# E8-09 의 "queued 대기" 는 재시도 사이의 상태라 폴링으로는 놓친다(G5 실측). 재큐잉이 있었다는 것은
# attempt 가 둘 이상 기록된 것으로 센다 — 서버는 재시도 가능한 실패를 queued 로 되돌려야만 다음 attempt 를 만든다.
chk D2  "대안이 없어도 재큐잉했다 (attempt 2건 이상 기록)"     yes \
  "$( [ "$(psqlq "select count(*) from task_attempt where task_id='$NT'")" -ge 2 ] && echo yes || echo no )"
chk D2b "프로파일은 바뀌지 않았다 (전환할 대안이 없다)"        1 \
  "$(psqlq "select count(distinct profile_id) from task where id='$NT'")"
chk D3  "다른 머신으로 넘기지 않았다 (runtime 고정)"           1 \
  "$(psqlq "select count(distinct runtime_id) from task_attempt where task_id='$NT' and runtime_id is not null")"
chk D4  "세션도 같은 머신에 남았다"                            "$RUNTIME" \
  "$(psqlq "select runtime_id from session where id='$NS'")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg fs "$FS" --arg ft "$FT" --arg ns "$NS" --arg nt "$NT" \
  --arg kind "$A1_KIND" --arg wd_before "$WD_BEFORE" --arg wd_after "$WD_AFTER" --arg art "${ART:-}" \
  --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,fallback:{session:$fs,task:$ft,attempt1_failure_kind:$kind,
      workdir_before:$wd_before,workdir_after:$wd_after,artifact:$art},
    no_alternative:{session:$ns,task:$nt},
    elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/46.json"
[ "$fail" = 0 ]
