#!/usr/bin/env bash
# e2e/p5/75_scenario_d.sh — **시나리오 D — 프로파일 전환** (PRD §4 D · EVAL E16-D, E8-08·E8-09).
#
#   C  hermes 프로파일이 실패(모델 오타) → 같은 머신의 claude_code 대체 프로파일로 재큐잉, workdir 재사용,
#      runtime_session_ref 는 새 런타임 것(콜드 스타트), 폴백한 프로파일이 **아티팩트를 같은 workdir 에서** 제출
#   D  대체 프로파일이 없으면 → 재큐잉은 하되 다른 머신으로 넘기지 않고 Director 알림 1건
#
# 판정은 `e2e/p3/53_scenario_d.sh`(G6) 그대로. 페이크에서 "hermes 실패" 는 primary 프로파일 env 의 대본이
# 매 턴 JSON-RPC 오류로 답하는 것이다(모델 오타와 같은 재시도 가능 실패 = failure_kind other).
# 산출물: out/75-checks.tsv · out/75.json
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-75.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-75.json"; WORK="$P5_TMP_ROOT/75/work"; DLOG="$OUT/daemon-75.log"
MODEL="${LEAD_MODEL}"; BAD_MODEL="${BAD_MODEL:-claude-haiku-4-5-TYPO}"
g5_chk_init "$OUT/75-checks.tsv"
cleanup() { daemon_stop "$OUT/daemon-75.pid"; return 0; }
trap cleanup EXIT

FB_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 짧은 안내문을 쓰는 작성자다. 답은 한국어로 짧게.
지시를 받으면 a. 현재 작업 디렉토리에 guide-y.md 를 만들어 제품 Y 안내문을 여덟 줄로 쓴다. b. colab_artifact_submit 을 부른다. type "doc", file 은 그 파일의 절대 경로, name 은 "product-y-guide.md". c. colab_status_set 으로 status "done". 메시지는 게시하지 않는다. 웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라.'
GOAL='가상의 실내 화분 자동 급수기 제품 Y 의 짧은 안내문을 작업 디렉토리에 만든다'

step "1. 계정 · 페어링"
: > "$DLOG"
signup "i5d+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "G9 Scenario D $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 2
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-75.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME_ID="$(runtime_of_config "$CFG")"
KINDS="$(runtime_kinds "$RUNTIME_ID")"
chk R0 "이 머신이 두 런타임을 다 광고한다 (kinds=$KINDS)" "yes|yes" "$(in_set hermes $KINDS)|$(in_set claude_code $KINDS)"
T0="$(now_ms)"

step "2. C — hermes 실패 → 같은 머신 claude_code 로 전환 (E8-08)"
if [ "$RUNTIME" = fake ]; then
  FBA="$(create_agent_2profiles_env "$WS" Faller writer "$FB_INS" hermes "$BAD_MODEL" "$(fake_env_error Faller hermes "Internal error: model $BAD_MODEL is not available")" claude_code "$MODEL" "$(fake_env Faller claude)")"
else
  FBA="$(create_agent_2profiles "$WS" Faller writer "$FB_INS" hermes "$BAD_MODEL" '[]' claude_code "$MODEL")"
fi
link_fallback "$FBA" primary spare       # 우회(G5 S-24): 정식 경로 부재
P_PRIMARY="$(profile_of "$FBA" primary)"; P_SPARE="$(profile_of "$FBA" spare)"
chk C0 "폴백 연결이 섰다 (정식 경로 부재 — S-24 우회)" "$P_SPARE" "$(psqlq "select coalesce(fallback_profile_id::text,'-') from agent_profile where id='$P_PRIMARY'")"
FS="$(create_session_p3 "$WS" "제품 Y 안내문 (프로파일 전환)" "$GOAL" "$FBA" "$RUNTIME_ID" '{}' "$FBA")"
FT="$(session_initial_task "$FS")"
WD_BEFORE=""
DEADLINE=$(( $(date +%s) + ${FALLBACK_S:-$T_TURN} ))
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
  [ -z "$WD_BEFORE" ] && WD_BEFORE="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id=(select lane_id from task where id='$FT')")"
  case "$(task_field "$FT" status)" in completed|failed|cancelled) break;; esac
  sleep 2
done
echo "── task_attempt ──" >&2; task_attempts "$FT" | column -t -s $'\t' >&2
A1_KIND="$(psqlq "select coalesce(failure_kind::text,'-') from task_attempt where task_id='$FT' and attempt=1")"
chk C1  "attempt 1 이 실패했다 (hermes 프로파일)" yes "$( [ "$A1_KIND" != '-' ] && [ -n "$A1_KIND" ] && echo yes || echo no )"
chk C1b "그 failure_kind 가 재시도 가능하다 (§8) — 관측 kind=$A1_KIND" yes "$(in_set "$A1_KIND" other network stall timeout | sed 's/^[^y].*/no/')"
chk C2  "재큐잉됐다 (attempt ≥ 2)" yes "$( [ "$(task_field "$FT" attempt)" -ge 2 ] 2>/dev/null && echo yes || echo no )"
CUR_PROF="$(task_field "$FT" profile_id)"
chk C3  "task 프로파일이 spare(claude_code) 로 바뀌었다" "$P_SPARE" "$CUR_PROF"
chk C3b "lane 프로파일도 같이 바뀌었다" "$P_SPARE" "$(psqlq "select profile_id from lane where id='$(task_field "$FT" lane_id)'")"
chk C3c "최종 런타임 종류가 claude_code 다" claude_code "$(psqlq "select runtime_kind from agent_profile where id='$CUR_PROF'")"
WD_AFTER="$(psqlq "select coalesce(w.path_or_ref,'') from lane l left join workdir w on w.id=l.workdir_id where l.id=(select lane_id from task where id='$FT')")"
chk C4  "**workdir 를 그대로 재사용한다** (§4.4 workdir.reuse)" "$WD_BEFORE" "$WD_AFTER"
chk C4b "workdir 행이 하나뿐이다" 1 "$(psqlq "select count(*) from workdir where session_id='$FS'")"
chk C5  "폴백 뒤 runtime_session_ref 는 새 런타임 것이다" claude_code "$(lane_ref "$FS" Faller | jq -r '.runtime_kind // "none"')"
chk C6  "폴백 뒤 task 가 완료됐다 (전환이 실제로 일을 끝낸다)" completed "$(task_field "$FT" status)"
chk C6b "세션은 같은 머신에 남았다" "$RUNTIME_ID" "$(psqlq "select runtime_id from session where id='$FS'")"

step "3. C 추가 — 아티팩트가 유지된 workdir 에서 제출된다 (E16-D)"
ART="$(psqlq "select id from artifact where session_id='$FS' order by created_at desc limit 1")"
chk C7  "폴백한 프로파일이 아티팩트를 제출했다" yes "$( [ -n "$ART" ] && echo yes || echo no )"
if [ -n "$ART" ]; then
chk C7b "아티팩트 이름이 지시한 그대로" product-y-guide.md "$(psqlq "select name from artifact where id='$ART'")"
chk C7c "그 파일이 **같은 workdir** 안에 있다" yes "$( [ -f "$WD_AFTER/guide-y.md" ] && echo yes || echo no )"
fi
chk C7d "workdir 경로가 attempt 1 때와 같다" "$WD_BEFORE" "$WD_AFTER"

step "4. D — 대안이 없으면 queued 유지 + Director 알림 (E8-09)"
if [ "$RUNTIME" = fake ]; then
  NFA="$(PROFILE_ENV="$(fake_env_error Lonely hermes "Internal error: model $BAD_MODEL is not available")" create_agent_kind "$WS" Lonely writer hermes "$BAD_MODEL" "$FB_INS" '대안 없는 프로파일')"
else
  NFA="$(create_agent_kind "$WS" Lonely writer hermes "$BAD_MODEL" "$FB_INS" '대안 없는 프로파일')"
fi
NS="$(create_session_p3 "$WS" "제품 Y 안내문 (대안 없음)" "$GOAL" "$NFA" "$RUNTIME_ID" '{}' "$NFA")"
NT="$(session_initial_task "$NS")"
wait_until ${NOALT_S:-$((T_TURN*2))} '[ "$(psqlq "select count(*) from inbox_item where ref_id='"'$NT'"' and type='"'run_failed'"'")" -ge 1 ]' || true
echo "── task_attempt (대안 없음) ──" >&2; task_attempts "$NT" | column -t -s $'\t' >&2
chk D1  "Director 인박스에 run_failed 알림 1건 (E8-09)" 1 "$(psqlq "select count(*) from inbox_item where ref_id='$NT' and type='run_failed'")"
chk D2  "대안이 없어도 재큐잉했다 (attempt 2건 이상)" yes "$( [ "$(psqlq "select count(*) from task_attempt where task_id='$NT'")" -ge 2 ] && echo yes || echo no )"
chk D2b "프로파일은 바뀌지 않았다" 1 "$(psqlq "select count(distinct profile_id) from task where id='$NT'")"
chk D3  "다른 머신으로 넘기지 않았다 (runtime 고정)" 1 "$(psqlq "select count(distinct runtime_id) from task_attempt where task_id='$NT' and runtime_id is not null")"
chk D4  "세션도 같은 머신에 남았다" "$RUNTIME_ID" "$(psqlq "select runtime_id from session where id='$NS'")"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
jq -n --arg ws "$WS" --arg fs "$FS" --arg ft "$FT" --arg ns "$NS" --arg nt "$NT" --arg kind "$A1_KIND" --arg mode "$RUNTIME" \
  --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  '{runtime_mode:$mode,workspace:$ws,fallback:{session:$fs,task:$ft,attempt1_failure_kind:$kind},no_alternative:{session:$ns,task:$nt},elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/75.json"
[ "$fail" = 0 ]
