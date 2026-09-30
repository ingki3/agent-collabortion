#!/usr/bin/env bash
# e2e/p5/100_lane_end_join.sh — T-FIX-B: 턴 종료로 끝난 lane 도 합류(FR-6.5)를 부른다 · 실패한 형제도 합류에 든다.
# (PRD FR-6.5 「그룹의 모든 자식 lane이 종료 상태(done 또는 failed)가 되면 … 한 번」 ·
#  contracts/colab-cli.md §2 「lane 종료 판정은 서버가 turn_end 와 함께 한다」 · openapi setTaskStatus done.)
#
# 비용 한 줄(I-3): 페이크 턴 8(Lead 2 × 2방 · 자식 2 × 2방) · $0 · ≈ 60s. 실기 대조 없음(페이크 고정 — 순서를 대본이 정한다).
#
# 방 A (섞인 끝): Lead 가 Alpha·Beta 에게 위임. Alpha 는 `colab status set done` 하고 턴을 끝낸다(먼저),
#   Beta 는 6초 뒤 **status set 없이** 게시만 하고 턴을 끝낸다(나중 — 그룹의 마지막). T-FIX-B 전에는 여기서
#   합류가 영영 오지 않았다(Lead 가 다시 깨지 않는다).
#   A1  Beta lane done · Beta 의 status set 이벤트 0 (턴 종료로만 끝났다)
#   A2  합류 발화(위임 task 의 join_fired_at)
#   A3  합류 묶음 시스템 메시지 1건
#   A4  Lead 가 합류로 한 번 깨어 JOIN-SEEN 을 한 번 게시(Lead task 2 · JOIN-SEEN 1)
#   A5  「요청하신 작업이 끝났습니다」 0 (위임 자식은 합류로만 알린다 — 보고 중복 없음)
#   A6  Alpha 는 status set done 1회 → 그 턴 끝에서 합류·보고가 다시 나가지 않았다(묶음 여전히 1)
# 방 B (실패한 형제): Alpha2 는 status set done, Beta2 는 40초 잠든 사이 Director 가 「중단」 → 데몬이 cancelled
#   로 finish → lane failed.
#   B1  Beta2 lane failed
#   B2  합류 발화 · 묶음 1건
#   B3  묶음에 「Beta2: failed — 사유: cancelled」
#   B4  Lead2 가 합류로 한 번 깨어 JOIN-SEEN 1
# 산출물: out/100-checks.tsv · out/100-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-100.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-100.json"; WORK="$P5_TMP_ROOT/100/work"; DLOG="$OUT/daemon-100.log"
g5_chk_init "$OUT/100-checks.tsv"
cleanup() { daemon_stop "$OUT/daemon-100.pid"; return 0; }
trap cleanup EXIT
[ "$RUNTIME" = fake ] || { echo "100_: 페이크 고정(RUNTIME=$RUNTIME) — 건너뜀"; exit 0; }
rm -f "$OUT"/100-*.txt "$OUT"/100-*.json

step "1. 계정 · 워크스페이스 · 페어링"
signup "join+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Lane end join $STAMP")"
rm -rf "$WORK"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 4
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-100.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

# Lead 대본: 첫 턴은 두 자식에게 위임, 그 뒤의 턴(합류)은 JOIN-SEEN 한 줄. 턴 수는 파일로 센다.
lead_script() { # NAME CHILD1 CHILD2
  jq -nc --arg o "$OUT" --arg n "$1" --arg a "$2" --arg b "$3" --arg fx "$FIX" '{turns:[{steps:[{exec:(
    "c=$(cat " + $o + "/100-" + $n + "-turns.txt 2>/dev/null || echo 0); echo $((c+1)) > " + $o + "/100-" + $n + "-turns.txt; "
    + "if [ \"$c\" = 0 ]; then bash " + $fx + "/card.sh delegate " + $a + " \"A 조사\" >/dev/null && bash " + $fx + "/card.sh delegate " + $b + " \"B 조사\" >/dev/null; "
    + "else colab message post --body \"JOIN-SEEN " + $n + "\" >/dev/null; fi"), exec_timeout_ms:60000}]}]}'
}
# T-CARD-S: 카드로 받은 일은 결과 카드를 내고 끝낸다(Alpha 는 결과 카드 + status set done, Beta 는 결과 카드 + 턴 종료만).
DONE_SCRIPT="$(jq -nc --arg fx "$FIX" '{turns:[{steps:[{exec:("colab message post --body \"ALPHA 결과\" >/dev/null && bash " + $fx + "/card.sh report >/dev/null && colab status set done >/dev/null"), exec_timeout_ms:60000}]}]}')"
TURN_END_SCRIPT="$(jq -nc --arg fx "$FIX" '{turns:[{steps:[{exec:("sleep 6 && colab message post --body \"BETA 결과\" >/dev/null && bash " + $fx + "/card.sh report >/dev/null"), exec_timeout_ms:60000}]}]}')"
SLOW_SCRIPT="$(jq -nc '{turns:[{steps:[{sleep_ms:40000},{chunk:"late"}]}]}')"

bundles() { psqlq "select count(*) from message where session_id='$1' and author_type='system' and content like '%위임한 작업이 모두 끝났습니다%'"; }
join_task() { psqlq "select id from task where session_id='$1' and agent_id='$2' order by created_at limit 1"; }
lane_of_agent() { psqlq "select id from lane where session_id='$1' and agent_id='$2' order by created_at limit 1"; }
lane_status() { psqlq "select status::text from lane where id='$1'"; }

step "2. 방 A — Alpha(status set done, 먼저) · Beta(턴 종료만, 나중)"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$LEAD_MODEL" "팀을 이끈다." '팀을 이끈다' "$(lead_script Lead Alpha Beta)")"
ALPHA="$(create_agent_fake "$WS" Alpha researcher claude_code "$LEAD_MODEL" "조사한다." '조사한다' "$DONE_SCRIPT")"
BETA="$(create_agent_fake "$WS" Beta researcher claude_code "$LEAD_MODEL" "조사한다." '조사한다' "$TURN_END_SCRIPT")"
ROOM_A="$(create_session_p2 "$WS" "합류 A" "섞인 끝" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$ALPHA" "$BETA")"
wait_until "$T_TURN" "[ -n \"\$(lane_of_agent $ROOM_A $BETA)\" ] && [ \"\$(lane_status \$(lane_of_agent $ROOM_A $BETA))\" = done ]" || true
wait_quiet "$ROOM_A" "$T_TURN" || true
LANE_B="$(lane_of_agent "$ROOM_A" "$BETA")"
chk A1a "Beta lane done" done "$(lane_status "$LANE_B")"
chk A1b "Beta 의 status set 이벤트 0(턴 종료로만)" 0 \
  "$(psqlq "select count(*) from task_event e join task t on t.id=e.task_id where t.lane_id='$LANE_B' and e.verb='set_status'")"
DT_A="$(join_task "$ROOM_A" "$LEAD")"
chk A2 "합류 발화(join_fired_at)" yes "$(psqlq "select case when join_fired_at is null then 'no' else 'yes' end from task where id='$DT_A'")"
chk A3 "합류 묶음 1건" 1 "$(bundles "$ROOM_A")"
chk A4a "Lead task 2(시작 + 합류)" 2 "$(psqlq "select count(*) from task where session_id='$ROOM_A' and agent_id='$LEAD'")"
chk A4b "JOIN-SEEN 1회" 1 "$(psqlq "select count(*) from message where session_id='$ROOM_A' and content='JOIN-SEEN Lead'")"
chk A5 "「요청하신 작업이 끝났습니다」 0" 0 "$(psqlq "select count(*) from message where session_id='$ROOM_A' and content='요청하신 작업이 끝났습니다.'")"
LANE_A="$(lane_of_agent "$ROOM_A" "$ALPHA")"
chk A6a "Alpha status set done 1회" 1 \
  "$(psqlq "select count(*) from task_event e join task t on t.id=e.task_id where t.lane_id='$LANE_A' and e.verb='set_status' and e.object_ref #>> '{}' = 'done'")"
chk A6b "Alpha lane done · task completed" "done completed" \
  "$(lane_status "$LANE_A") $(psqlq "select status from task where lane_id='$LANE_A' order by created_at desc limit 1")"
psqlq "select a.name, l.status, coalesce(l.delegated_from_task_id::text,'-') from lane l join agent a on a.id=l.agent_id where l.session_id='$ROOM_A' order by l.created_at" > "$OUT/100-lanes-A.txt"

step "3. 방 B — Alpha2(status set done) · Beta2(잠든 사이 Director 가 중단 → failed)"
LEAD2="$(create_agent_fake "$WS" Lead2 lead claude_code "$LEAD_MODEL" "팀을 이끈다." '팀을 이끈다' "$(lead_script Lead2 Alpha2 Beta2)")"
ALPHA2="$(create_agent_fake "$WS" Alpha2 researcher claude_code "$LEAD_MODEL" "조사한다." '조사한다' "$DONE_SCRIPT")"
BETA2="$(create_agent_fake "$WS" Beta2 researcher claude_code "$LEAD_MODEL" "조사한다." '조사한다' "$SLOW_SCRIPT")"
ROOM_B="$(create_session_p2 "$WS" "합류 B" "실패한 형제" "$LEAD2" "$RUNTIME_ID" "$LEAD2" "$LEAD2" "$ALPHA2" "$BETA2")"
wait_until "$T_TURN" "[ -n \"\$(lane_of_agent $ROOM_B $BETA2)\" ] && [ \"\$(lane_status \$(lane_of_agent $ROOM_B $BETA2))\" = running ] && [ -n \"\$(lane_of_agent $ROOM_B $ALPHA2)\" ] && [ \"\$(lane_status \$(lane_of_agent $ROOM_B $ALPHA2))\" = done ]" || true
LANE_B2="$(lane_of_agent "$ROOM_B" "$BETA2")"
api POST "/lanes/$LANE_B2/cancel" '' > "$OUT/100-cancel.json" || true
wait_until "$T_TURN" "[ \"\$(lane_status $LANE_B2)\" = failed ]" || true
wait_quiet "$ROOM_B" "$T_TURN" || true
chk B1 "Beta2 lane failed" failed "$(lane_status "$LANE_B2")"
DT_B="$(join_task "$ROOM_B" "$LEAD2")"
chk B2a "합류 발화" yes "$(psqlq "select case when join_fired_at is null then 'no' else 'yes' end from task where id='$DT_B'")"
chk B2b "합류 묶음 1건" 1 "$(bundles "$ROOM_B")"
chk B3 "묶음에 실패 사유" 1 "$(psqlq "select count(*) from message where session_id='$ROOM_B' and author_type='system' and content like '%Beta2: failed — 사유: cancelled%'")"
chk B4 "JOIN-SEEN 1회" 1 "$(psqlq "select count(*) from message where session_id='$ROOM_B' and content='JOIN-SEEN Lead2'")"
psqlq "select a.name, l.status, coalesce(l.delegated_from_task_id::text,'-') from lane l join agent a on a.id=l.agent_id where l.session_id='$ROOM_B' order by l.created_at" > "$OUT/100-lanes-B.txt"
echo "$WS $ROOM_A $ROOM_B $RUNTIME_ID" > "$OUT/100-ids.txt"

printf '\n100_lane_end_join: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
