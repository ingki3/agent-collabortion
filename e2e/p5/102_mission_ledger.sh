#!/usr/bin/env bash
# e2e/p5/102_mission_ledger.sh — T-LEDGER: 미션 상태 원장 한 바퀴를 페이크 런타임(acpfake exec)으로 (PRD FR-4.6 ·
#   openapi v0.3.12 · colab-cli v0.9.12 · harness v0.9.18, 맥락 2단계). 대본: fixtures/ledger_flow.sh.
#
# 비용 한 줄(I-3): 페이크 턴 3(Lead 2 · R 1) · $0 · ≈ 40s. 실기 대조 없음(페이크 고정).
#
# 방 L: Lead(lead) · R(researcher). 긴 미션 — Lead 첫 턴 뒤 그 미션의 메시지 150건(셋 중 하나는 작업 내용 3,000자)을
#   DB 에 심어 ②가 크게 생기게 한다(0단계 기준선 04-baseline §4 의 「긴 미션」 모양).
#   L1  Lead 첫 턴: plan·fact·assignment·open_question note exit 0 · lesson 두 번(같은 작성자) → support 1 그대로 · get 5
#   L2  R: plan note → exit 3(역할 밖) · Lead 의 fact supersede exit 0 · 다시 supersede → exit 3 memory_not_active · 같은 lesson → support 2 · retire exit 0
#   L3  DB: 원장 행 5 + supersede 1 = 6 · lesson 행 1(support 2) · 옛 fact 의 content 그대로 + superseded · active plan 1
#   L4  Lead 둘째 턴의 <mission_ledger>: 새 값만(옛 값 없음) · lesson(support 2) · plan · 철회된 질문 없음 · ② 다음 ③ 앞
#   L5  ② 는 머리글 색인: 메시지마다 한 줄 · <detail 없음
#   L6  턴 프롬프트 크기(task_context_metric): ② 바이트 ≤ ②가 대신한 원문(내용+작업 내용) 바이트의 40% — 숫자는 out/102-metric.txt
# 산출물: out/102-checks.tsv · out/102-metric.txt · out/102-ledger.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-102.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-102.json"; WORK="$P5_TMP_ROOT/102/work"; DLOG="$OUT/daemon-102.log"
REC="$E2E_OUT/fake-records"
g5_chk_init "$OUT/102-checks.tsv"
cleanup() { daemon_stop "$OUT/daemon-102.pid"; return 0; }
trap cleanup EXIT
[ "$RUNTIME" = fake ] || { echo "102_: 페이크 고정(RUNTIME=$RUNTIME) — 건너뜀"; exit 0; }
mkdir -p "$REC"; rm -f "$REC"/102-* "$OUT"/102-*.txt

step "1. 계정 · 워크스페이스 · 페어링"
signup "ledger+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Ledger $STAMP")"
rm -rf "$WORK"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-102.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"
script_of() { jq -nc --arg fx "$FIX" --arg r "$1" '{turns:[{steps:[{exec:("bash "+$fx+"/ledger_flow.sh "+$r), exec_timeout_ms:60000}]}]}'; }
mk() { create_agent_fake "$WS" "$1" "$2" claude_code "$LEAD_MODEL" "$1 이다." "$1" "$(script_of "$1")"; }
recv() { cat "$REC/102-$1" 2>/dev/null | tr '\n' ' ' | sed 's/ $//'; }
q() { psqlq "$1" 2>/dev/null || echo -; }

step "2. 방 L — Lead 첫 턴이 원장을 쓴다"
LEAD="$(mk Lead lead)"; R="$(mk R researcher)"
ROOM="$(create_session_p2 "$WS" "원장" "분기 매출 표를 만든다" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$R")"
W="$(q "select legacy_work_id from room where id='$ROOM'")"
[ "$W" = "-" ] || [ -z "$W" ] && W="$(q "select id from work where room_id='$ROOM' order by created_at limit 1")"
wait_until "$T_TURN" "[ -s \"$REC/102-lead-get\" ]" || true
wait_quiet "$ROOM" "$T_TURN" || true
chk L1a "Lead plan·fact·assignment·open_question note → exit 0" "0/0/0/0" "$(recv lead-plan)/$(recv lead-fact)/$(recv lead-assign)/$(recv lead-question)"
chk L1b "같은 lesson 을 같은 작성자가 두 번 → support 1 그대로(v0.3.13)" "0 1/0 1" "$(recv lead-lesson1)/$(recv lead-lesson2)"
chk L1c "memory get → 5 항목(active — lesson 은 합쳐져 하나)" "0 5" "$(recv lead-get)"

step "3. 긴 미션 — 메시지 150건을 그 미션에 심는다"
q "insert into message (session_id, author_type, author_id, content, detail, created_at, work_id)
   select '$ROOM', 'agent', '$R',
          lpad(g::text, 3, '0') || ' ' || repeat('조사 결과를 정리했습니다 — 출처 세 곳을 대조했고 수치가 맞지 않는 부분은 표시했습니다. ', 6),
          case when g % 3 = 0 then repeat('| 항목 | 값 | 출처 |' || chr(10) || '| 매출 | 1,234억 | 공시 |' || chr(10), 75) end,
          now() + g * interval '1 millisecond', '$W'
   from generate_series(1, 150) g" >/dev/null
# 기준점(Lead 첫 턴) 뒤에 쓴 메시지라 재개 턴의 델타에도 실린다 — 심은 시각이 지나간 뒤에 다음 게시를 한다.
sleep 1

step "4. R 이 값을 고친다(일반 턴)"
post_message "$ROOM" "[@R](mention://agent/$R) 한도 값을 문서로 확인해서 원장을 고쳐 주세요" >/dev/null
wait_until "$T_TURN" "[ -s \"$REC/102-r-retire\" ]" || true
wait_quiet "$ROOM" "$T_TURN" || true
chk L2a "R plan note → exit 3(역할 밖, 서버에 보내기 전)" "3" "$(recv r-plan | awk '{print $1}')"
chk L2b "R supersede exit 0 · 다시 → exit 3 memory_not_active · retire exit 0" "0/3 memory_not_active/0" "$(recv r-supersede)/$(recv r-supersede-again)/$(recv r-retire)"
chk L2c "같은 lesson 을 다른 작성자(R)가 → support 2(병합, 새 행 없음)" "0 2" "$(recv r-lesson)"
FID="$(cat "$REC/102-fact-id" 2>/dev/null)"
chk L3a "원장 행 6(5 + supersede 1) · lesson 행 1(support 2) · active plan 1" "6/1 2/1" \
  "$(q "select count(*) from memory_item where work_id='$W'")/$(q "select count(*)||' '||max(support_count) from memory_item where work_id='$W' and kind='lesson'")/$(q "select count(*) from memory_item where work_id='$W' and kind='plan' and status='active'")"
chk L3b "옛 fact: content 그대로 · superseded · 새 항목이 supersedes 로 가리킨다" "API 한도는 분당 60회 superseded 1" \
  "$(q "select content||' '||status from memory_item where id='$FID'") $(q "select count(*) from memory_item where supersedes='$FID'")"
chk L3c "철회한 질문은 retired 로 남는다(삭제 없음) · 사유는 retire_reason" "retired Director 가 연결 기준으로 정했다" "$(q "select status||' '||retire_reason from memory_item where work_id='$W' and kind='open_question'")"

step "5. Lead 둘째 턴 — 원장과 머리글 색인을 읽는다"
post_message "$ROOM" "[@Lead](mention://agent/$LEAD) 지금 원장 기준으로 다음 할 일을 정리해 주세요" >/dev/null
wait_until "$T_TURN" "[ -s \"$REC/102-lead-ledger-seen\" ]" || true
wait_quiet "$ROOM" "$T_TURN" || true
chk L4a "<mission_ledger>: 새 값 1 · 옛 값 0 · lesson(support 2) 1 · plan 1 · 철회된 질문 0" "1/0/1/1/0" \
  "$(recv lead-ledger-fact-new)/$(recv lead-ledger-fact-old)/$(recv lead-ledger-lesson)/$(recv lead-ledger-plan)/$(recv lead-ledger-question)"
chk L4b "블록 순서: ② → <mission_ledger> → <roster_status>" "<mission_message|<mission_ledger |<roster_status>|" "$(recv lead-order)"
LT="$(q "select t.id from task t where t.session_id='$ROOM' and t.agent_id='$LEAD' order by t.created_at desc limit 1")"
MMN="$(q "select counts->>'mission_messages' from task_context_metric where task_id='$LT' order by attempt desc limit 1")"
chk L5 "② 머리글 색인: 줄 수 = counts.mission_messages · <detail 0" "$MMN/0" "$(recv lead-mm-lines)/$(recv lead-mm-detail)"
MMB="$(q "select sections->'prompt.mission_messages'->>'bytes' from task_context_metric where task_id='$LT' order by attempt desc limit 1")"
LDB="$(q "select coalesce(sections->'prompt.mission_ledger'->>'bytes','0') from task_context_metric where task_id='$LT' order by attempt desc limit 1")"
PB="$(q "select prompt_bytes from task_context_metric where task_id='$LT' order by attempt desc limit 1")"
# ②가 대신한 원문: ① 창(방의 최근 50)보다 오래된 그 미션 메시지의 내용 + 작업 내용(옛 ②는 내용 전문 + 작업 내용 앞 400자를 실었다).
RAW="$(q "select sum(octet_length(content) + coalesce(octet_length(detail),0)) from (select content, detail, work_id from message where session_id='$ROOM' order by created_at desc, id desc offset 50) m where work_id='$W'")"
OLD="$(q "select sum(octet_length(content) + coalesce(octet_length(left(detail, 400)),0) + 120) from (select content, detail, work_id from message where session_id='$ROOM' order by created_at desc, id desc offset 50) m where work_id='$W'")"
printf 'prompt_bytes=%s mission_messages_bytes=%s mission_ledger_bytes=%s mission_messages_lines=%s raw_bytes=%s legacy_estimate_bytes=%s\n' \
  "$PB" "$MMB" "$LDB" "$MMN" "$RAW" "$OLD" | tee "$OUT/102-metric.txt"
chk L6 "② + <mission_ledger> ≤ 40% of the raw mission text ② replaces" 1 \
  "$(awk -v a="$MMB" -v b="$LDB" -v r="$RAW" 'BEGIN{ if (a ~ /^[0-9]+$/ && r+0 > 0) print ((a+b) <= 0.4*r) ? 1 : 0; else print "-" }')"
q "select kind, status, support_count, content from memory_item where work_id='$W' order by created_at" > "$OUT/102-ledger.txt"
echo "$WS $ROOM $W $RUNTIME_ID" > "$OUT/102-ids.txt"

printf '\n102_mission_ledger: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
