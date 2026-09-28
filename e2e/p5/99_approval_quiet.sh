#!/usr/bin/env bash
# e2e/p5/99_approval_quiet.sh — T-QUIET: 승인만 남은 일은 에이전트끼리 새 일을 만들지 않는다
# (PRD v0.19.13 FR-2A.2.3 · openapi v0.3.9 QueuedReason.approval_pending · CompletionProgress.paused_agent_triggers ·
#  harness v0.9.15 브리프 [2] 한 줄·게시 결과 안내).
#
# 비용 한 줄(I-3): 페이크 턴 2(Lead · Writer) · $0 · ≈ 40s. 실기 대조 없음(페이크 고정 — 대본이 셋이 서로 멘션한다).
#
# 실사용 모양(2026-09-28 「게임 제작 방」): 게임(v39)이 완성된 뒤에도 Lead·Writer·Researcher 가 서로 멘션하며 가이드 각주를
# 고쳤고, 도는 할 일이 끊이지 않아 승인 요청이 한 번도 뜨지 않았다. 대본:
#   Lead(방 시작 턴) — @Writer 에게 각주 확인 요청 → game.html 제출(승인만 남음 = 승인 대기) → @Researcher 에게 각주 요청 → Director 에게 보고
#   Writer(Lead 가 깨움, 승인 대기 전) — 8초 뒤 @Researcher 에게 · @Lead 에게 각주 핑퐁
#   Researcher — 깨어나면 @Lead 에게 핑퐁(깨어나면 안 된다)
# 판정:
#   Q1  제출 뒤 미션 approval_quiet = quiet
#   Q2  Lead 의 @Researcher 게시 결과 notice = harness v0.9.15 문장(@Researcher)
#   Q3  Writer 의 @Researcher · @Lead 게시 결과 notice
#   Q4  도는 턴(Lead · Writer)이 끝나면 승인 요청 1건(user_approval open = 1)
#   Q5  보류 할 일 = queued · approval_pending ≥ 2, Researcher 는 한 번도 dispatch 되지 않았다
#   Q6  getWork completion_progress.paused_agent_triggers = 보류 할 일 수
#   Q7  listLanes 의 그 lane queued_reason = approval_pending
#   Q8  15초 더 — 여전히 아무것도 dispatch 되지 않고 승인 요청 1건
#   Q9  Director 승인 → 미션 completed, 보류 할 일 전부 cancelled · stop_reason approval_closed · 피드 「일이 승인되어 닫혔습니다」
#   Q10 Lead 브리프 [2] 에 승인 대기 줄
# 산출물: out/99-checks.tsv · out/99-*.json
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-99.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-99.json"; WORK="$P5_TMP_ROOT/99/work"; DLOG="$OUT/daemon-99.log"
TAP="$OUT/tap-99.jsonl"; TAP_PORT="${TAP_PORT_99:-8199}"
g5_chk_init "$OUT/99-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-99.pid"; return 0; }
trap cleanup EXIT
[ "$RUNTIME" = fake ] || { echo "99_: 페이크 고정(RUNTIME=$RUNTIME) — 건너뜀"; exit 0; }
NOTICE() { printf "This mission is waiting for the Director's approval, so @%s was not woken. If work remains after approval, tell the Director." "$1"; }

step "0. claim 탭"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 워크스페이스 · 페어링"
signup "quiet+$STAMP@example.com" password123 Simplist >/dev/null
WS="$(create_workspace "Approval quiet $STAMP")"
rm -rf "$WORK"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 3
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-99.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

step "2. 대본 — 셋이 서로 멘션한다"
FAKE_L="$(jq -nc --arg o "$OUT" '{turns:[{steps:[{exec:(
  "colab message post --body \"가이드 각주 ≈86% 확인 부탁합니다\" --mention @Writer > " + $o + "/99-lead-1.json"
  + " && sleep 3 && printf v39 > game.html && colab artifact submit --name game.html --type doc --file game.html > " + $o + "/99-lead-submit.json"
  + " && colab message post --body \"4.1σ → 3.3σ 맞는지 봐 주세요\" --mention @Researcher > " + $o + "/99-lead-2.json"
  + " && colab message post --body \"Simplist 님, 게임 v39 완성했습니다. 팀 쪽에 열린 일은 0 입니다.\" > " + $o + "/99-lead-3.json"),
  exec_timeout_ms:60000}]}]}')"
FAKE_W="$(jq -nc --arg o "$OUT" '{turns:[{steps:[{exec:(
  "sleep 8 && colab message post --body \"≈85% 가 맞나요?\" --mention @Researcher > " + $o + "/99-writer-1.json"
  + " && colab message post --body \"각주 고쳤습니다 — 다시 봐 주세요\" --mention @Lead > " + $o + "/99-writer-2.json"),
  exec_timeout_ms:60000}]}]}')"
FAKE_R="$(jq -nc --arg o "$OUT" '{turns:[{steps:[{exec:("colab message post --body \"≈86% 로 되돌렸습니다\" --mention @Lead > " + $o + "/99-researcher.json"), exec_timeout_ms:60000}]}]}')"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$LEAD_MODEL" "팀을 이끈다." '팀을 이끈다' "$FAKE_L")"
WRITER="$(create_agent_fake "$WS" Writer writer claude_code "$LEAD_MODEL" "가이드를 쓴다." '가이드를 쓴다' "$FAKE_W")"
RESEARCHER="$(create_agent_fake "$WS" Researcher researcher claude_code "$LEAD_MODEL" "근거를 찾는다." '근거를 찾는다' "$FAKE_R")"
rm -f "$OUT"/99-lead-*.json "$OUT"/99-writer-*.json "$OUT/99-researcher.json"
SESSION="$(create_session_p2 "$WS" "게임 제작 방" "게임을 만든다" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$WRITER" "$RESEARCHER")"
WORK_ID="$(psqlq "select id from work where room_id='$SESSION'")"
echo "$WS $SESSION $WORK_ID $LEAD $WRITER $RESEARCHER $RUNTIME_ID" > "$OUT/99-ids.txt"

step "3. 도는 턴이 끝날 때까지(보류 할 일은 도는 것이 아니다)"
running() { psqlq "select count(*) from task where session_id='$SESSION' and status in ('dispatched','preparing','running')
                   or (session_id='$SESSION' and status='queued' and queued_reason is distinct from 'approval_pending')"; }
wait_step Q4 "Lead · Writer 턴이 끝나면 승인 요청 1건" 120 \
  "[ \"\$(running)\" = 0 ] && [ \"\$(psqlq \"select count(*) from hitl_request where work_id='$WORK_ID' and purpose='user_approval' and status='open'\")\" = 1 ]"
chk Q1 "제출 뒤 approval_quiet = quiet" quiet "$(psqlq "select coalesce(approval_quiet,'') from work where id='$WORK_ID'")"
chk Q2 "Lead 의 @Researcher 게시 결과 notice" "$(NOTICE Researcher)" "$(jq -r '.notice // ""' "$OUT/99-lead-2.json" 2>/dev/null)"
chk Q3a "Writer 의 @Researcher 게시 결과 notice" "$(NOTICE Researcher)" "$(jq -r '.notice // ""' "$OUT/99-writer-1.json" 2>/dev/null)"
chk Q3b "Writer 의 @Lead 게시 결과 notice" "$(NOTICE Lead)" "$(jq -r '.notice // ""' "$OUT/99-writer-2.json" 2>/dev/null)"
chk Q3c "Lead 의 @Writer(승인 대기 전) — notice 없음" "" "$(jq -r '.notice // ""' "$OUT/99-lead-1.json" 2>/dev/null)"
HELD="$(psqlq "select count(*) from task where work_id='$WORK_ID' and status='queued' and queued_reason='approval_pending'")"
chk_ge Q5a "보류 할 일(queued · approval_pending)" 2 "$HELD"
chk Q5b "Researcher 는 한 번도 dispatch 되지 않았다" 0 "$(psqlq "select count(*) from task where session_id='$SESSION' and agent_id='$RESEARCHER' and dispatched_at is not null")"
chk Q5c "Researcher 대본 게시 없음" no "$([ -f "$OUT/99-researcher.json" ] && echo yes || echo no)"
api_ok GET "/works/$WORK_ID" > "$OUT/99-work.json"
chk Q6 "getWork paused_agent_triggers = 보류 수" "$HELD" "$(jq -r '.completion_progress.paused_agent_triggers // 0' "$OUT/99-work.json")"
api_ok GET "/rooms/$SESSION/lanes" > "$OUT/99-lanes.json"
chk Q7 "Researcher lane queued_reason = approval_pending" approval_pending \
  "$(jq -r --arg a "$RESEARCHER" '[.[] | select(.agent_id==$a and .status=="queued")][0].queued_reason // ""' "$OUT/99-lanes.json")"

step "4. 15초 더 — claim 이 보류를 건너뛴다"
sleep 15
chk Q8a "여전히 dispatch 0(Researcher)" 0 "$(psqlq "select count(*) from task where session_id='$SESSION' and agent_id='$RESEARCHER' and dispatched_at is not null")"
chk Q8b "보류 수 그대로" "$HELD" "$(psqlq "select count(*) from task where work_id='$WORK_ID' and status='queued' and queued_reason='approval_pending'")"
chk Q8c "승인 요청 여전히 1건" 1 "$(psqlq "select count(*) from hitl_request where work_id='$WORK_ID' and purpose='user_approval'")"

step "5. Director 승인 → 보류 할 일 취소"
HITL="$(psqlq "select id from hitl_request where work_id='$WORK_ID' and purpose='user_approval' and status='open'")"
api_ok POST "/hitl-requests/$HITL/response" '{"approved":true}' -H "Idempotency-Key: $(uuid)" > "$OUT/99-approve.json"
wait_step Q9a "미션 completed" 30 "[ \"\$(psqlq \"select status from work where id='$WORK_ID'\")\" = completed ]"
chk Q9b "보류 할 일 전부 cancelled · approval_closed" "$HELD" \
  "$(psqlq "select count(*) from task where work_id='$WORK_ID' and status='cancelled' and stop_reason='approval_closed'")"
chk Q9c "queued 남은 것 0" 0 "$(psqlq "select count(*) from task where work_id='$WORK_ID' and status='queued'")"
chk Q9d "피드 문장 「일이 승인되어 닫혔습니다」" "$HELD" \
  "$(psqlq "select count(*) from task_event e join task t on t.id=e.task_id where t.work_id='$WORK_ID' and e.verb='cancel' and e.payload->'args'->>'note'='일이 승인되어 닫혔습니다'")"

step "6. 브리프 [2]"
T_L="$(psqlq "select id from task where session_id='$SESSION' and agent_id='$LEAD' order by created_at limit 1")"
python3 - "$TAP" "$T_L" > "$OUT/99-brief.txt" <<'PY'
import json, sys
tap, tid = sys.argv[1:]
want = "When your mission's only remaining completion condition is the Director's approval, do not wake teammates; report the result to the Director instead."
for line in open(tap):
    try: body = json.loads(line)["body"]
    except Exception: continue
    for b in (body.get("tasks") or []):
        if b["task"]["id"] != tid: continue
        print("yes" if want in b["brief"]["text"] else "no")
        sys.exit(0)
print("missing")
PY
chk Q10 "Lead 브리프 [2] 승인 대기 줄" yes "$(cat "$OUT/99-brief.txt")"

printf '\n99_approval_quiet: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
