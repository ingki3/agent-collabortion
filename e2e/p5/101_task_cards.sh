#!/usr/bin/env bash
# e2e/p5/101_task_cards.sh — T-CARD-S: 작업 카드의 한 바퀴를 페이크 런타임(acpfake exec)으로 (PRD FR-3.8 ·
#   openapi v0.3.10 · colab-cli v0.9.10 · harness v0.9.16). 대본: fixtures/cards_flow.sh(프롬프트 블록으로 고른다).
#
# 비용 한 줄(I-3): 페이크 턴 ≈ 10(Lead 3 · A 1 · B 3 · Asker 2 · Q 1) · $0 · ≈ 60s. 실기 대조 없음(페이크 고정).
#
# 방 C (카드): Lead 가 A·B 에게 카드 위임 — 첫 시도(기준 없음)는 검사에서 거절(exit 3 card_invalid), 고쳐서 통과.
#   A 는 결과 카드 + done. B 는 결과 없이 턴을 끝낸다 → 카드 게이트가 result_card_missing 후속 1 → 결과 카드.
#   합류 묶음(<result_cards>)으로 Lead 가 깨어 C-1 수락 · C-2 수정 요청 → B lane 재진입(v2) → 결과 → Lead 수락.
#   C1  card_invalid 거절 · 번호는 성공한 카드에만(C-1·C-2)
#   C2  B 의 후속 task 1(trigger_reason=result_card_missing) · 자동 결과 0
#   C3  합류 묶음 1 · Lead 가 <result_cards> 로 두 번 판정했다
#   C4  C-1 accepted v1 · C-2 accepted v2(수정 요청 사유 기록) · 위임 말풍선 3 · 결과 말풍선 3
#       C4d·C4e(T-CARD-COMMENT, colab-cli v0.9.11): accept 는 --comment 필수 — 없으면 exit 2 · 공백만 exit 3 judgement_comment_required · 코멘트는 judgement 에
#   C5  B 의 lane 은 하나(재진입이지 새 lane 이 아니다)
# 방 Q (질문): Asker 가 카드 없이 Q 를 멘션 → Q 의 task kind question.
#   Q1  question task 1 · speech question 1 · answer 1
#   Q2  질문 턴의 artifact submit → exit 3 command_not_allowed(문장 = 서버 질문 거부 문장)
#   Q3  질문은 카드를 만들지 않는다(방 Q task_card 0)
# 산출물: out/101-checks.tsv · out/101-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-101.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-101.json"; WORK="$P5_TMP_ROOT/101/work"; DLOG="$OUT/daemon-101.log"
REC="$E2E_OUT/fake-records"
g5_chk_init "$OUT/101-checks.tsv"
cleanup() { daemon_stop "$OUT/daemon-101.pid"; return 0; }
trap cleanup EXIT
[ "$RUNTIME" = fake ] || { echo "101_: 페이크 고정(RUNTIME=$RUNTIME) — 건너뜀"; exit 0; }
mkdir -p "$REC"; rm -f "$REC"/101-* "$OUT"/101-*.txt

step "1. 계정 · 워크스페이스 · 페어링"
signup "cards+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Task cards $STAMP")"
rm -rf "$WORK"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-101.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"
script_of() { jq -nc --arg fx "$FIX" --arg r "$1" '{turns:[{steps:[{exec:("bash "+$fx+"/cards_flow.sh "+$r), exec_timeout_ms:60000}]}]}'; }
mk() { create_agent_fake "$WS" "$1" "$2" claude_code "$LEAD_MODEL" "$1 이다." "$1" "$(script_of "$1")"; }
recv() { cat "$REC/101-$1" 2>/dev/null | tr '\n' ' ' | sed 's/ $//'; }
# q SQL → 값, 표·칸이 없으면(origin/dev 바이너리로 돌린 before) 「-」 — 판정 행이 모두 남게.
q() { psqlq "$1" 2>/dev/null || echo -; }

step "2. 방 C — Lead·A·B 카드 한 바퀴"
LEAD="$(mk Lead lead)"; A="$(mk A researcher)"; B="$(mk B researcher)"
ROOM_C="$(create_session_p2 "$WS" "카드" "시장 조사" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$A" "$B")"
cards_n() { q "select count(*) from task_card where room_id='$1'"; }
card_row() { q "select status||' v'||version from task_card where room_id='$ROOM_C' and number=$1"; }
# 끝 = C-2 가 v2 로 수락됐다, 또는(before: 카드가 없는 바이너리) 위임 시도가 끝나고 방이 조용하다.
wait_until "$T_TURN" "[ \"\$(card_row 2)\" = 'accepted v2' ] || { [ -s \"$REC/101-lead-delegated\" ] && [ \"\$(q \"select count(*) from task where session_id='$ROOM_C' and status in ('queued','dispatched','preparing','running')\")\" = 0 ]; }" || true
wait_quiet "$ROOM_C" "$T_TURN" || true
chk C1a "첫 위임(기준 없음) → exit 3 card_invalid" "3 card_invalid" "$(recv lead-invalid)"
chk C1b "고친 위임 둘 → exit 0 · 카드 번호 1·2 만" "0/0/1,2" "$(recv lead-deleg-a)/$(recv lead-deleg-b)/$(q "select string_agg(number::text, ',' order by number) from task_card where room_id='$ROOM_C'")"
LANE_B="$(q "select lane_id from task_card where room_id='$ROOM_C' and number=2")"
chk C2a "B 의 결과 없는 턴 → result_card_missing 후속 task 1" 1 "$(q "select count(*) from task where lane_id='$LANE_B' and trigger_reason='result_card_missing'")"
chk C2b "후속 턴이 결과 카드를 냈다(자동 결과 아님)" "ok/0" "$(recv b-followup-report)/$(q "select count(*) from message where session_id='$ROOM_C' and card_role='result' and content like '%(자동)%'")"
chk C3a "합류 묶음 1" 1 "$(q "select count(*) from message where session_id='$ROOM_C' and author_type='system' and content like '%위임한 작업이 모두 끝났습니다%'")"
chk C3b "Lead 가 <result_cards> 로 두 번 판정 · accept/revise/accept exit 0" "2 0/0/0" "$(recv lead-judge | awk '{print $NF}') $(recv lead-accept1)/$(recv lead-revise)/$(recv lead-accept2)"
chk C4a "C-1 accepted v1 · C-2 accepted v2" "accepted v1/accepted v2" "$(card_row 1)/$(card_row 2)"
# T-CARD-COMMENT (colab-cli v0.9.11 · openapi v0.3.11): 수락에는 코멘트가 필수 — 빈 수락은 거절, 코멘트는 판정에 남는다.
chk C4d "accept --comment 없음 → exit 2 · 공백만 → exit 3 judgement_comment_required" "2/3 judgement_comment_required" "$(recv lead-accept-nocomment)/$(recv lead-accept-blank)"
chk C4e "C-1·C-2 판정 코멘트가 남았다" "규모 숫자를 출처와 함께 확인했습니다/경쟁 제품 셋과 출처를 확인했습니다" "$(q "select judgement->>'comment' from task_card where room_id='$ROOM_C' and number=1")/$(q "select judgement->>'comment' from task_card where room_id='$ROOM_C' and number=2")"
chk C4b "C-2 수정 요청 사유가 지난 판에 남았다" 1 "$(q "select count(*) from task_card where room_id='$ROOM_C' and number=2 and jsonb_array_length(versions) >= 1")"
chk C4c "위임 말풍선 3(C-1 · C-2 v1 · C-2 v2) · 결과 말풍선 3" "3/3" "$(q "select count(*) from message where session_id='$ROOM_C' and card_role='delegation'")/$(q "select count(*) from message where session_id='$ROOM_C' and card_role='result'")"
chk C5 "B 의 lane 은 하나(수정 요청은 같은 lane 재진입)" 1 "$(q "select count(*) from lane where session_id='$ROOM_C' and agent_id='$B'")"
q "select number, version, status, follow_ups from task_card where room_id='$ROOM_C' order by number" > "$OUT/101-cards.txt"
q "select a.name, t.kind, coalesce(t.trigger_reason,'-'), t.status from task t join agent a on a.id=t.agent_id where t.session_id='$ROOM_C' order by t.created_at" > "$OUT/101-tasks-C.txt"

step "3. 방 Q — 에이전트 간 멘션은 질문"
Q="$(mk Q researcher)"; printf '%s' "$Q" > "$REC/101-q-id"
ASKER="$(mk Asker researcher)"
ROOM_Q="$(create_session_p2 "$WS" "질문" "가격대를 묻는다" "$ASKER" "$RUNTIME_ID" "$ASKER" "$ASKER" "$Q")"
wait_until "$T_TURN" "[ -s \"$REC/101-q-answered\" ] || [ \"\$(q \"select count(*) from task where session_id='$ROOM_Q' and status='completed'\")\" -ge 2 ]" || true
wait_quiet "$ROOM_Q" "$T_TURN" || true
chk Q1a "Q 의 task kind question 1" 1 "$(q "select count(*) from task where session_id='$ROOM_Q' and agent_id='$Q' and kind='question'")"
chk Q1b "speech question 1 · answer 1" "1/1" "$(q "select count(*) from message where session_id='$ROOM_Q' and speech='question'")/$(q "select count(*) from message where session_id='$ROOM_Q' and speech='answer'")"
chk Q2a "질문 턴의 artifact submit → exit 3 command_not_allowed" "3 command_not_allowed" "$(recv q-submit)"
chk Q2b "거부 문장 = 질문 거부 문장" "이 턴은 질문에 답하는 턴입니다 — artifact submit 를 쓸 수 없습니다. 일을 맡기려면 카드로 위임하세요" "$(cat "$REC/101-q-submit-detail" 2>/dev/null)"
chk Q3 "질문은 카드를 만들지 않는다" 0 "$(cards_n "$ROOM_Q")"
q "select a.name, t.kind, t.status from task t join agent a on a.id=t.agent_id where t.session_id='$ROOM_Q' order by t.created_at" > "$OUT/101-tasks-Q.txt"
echo "$WS $ROOM_C $ROOM_Q $RUNTIME_ID" > "$OUT/101-ids.txt"

printf '\n101_task_cards: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
