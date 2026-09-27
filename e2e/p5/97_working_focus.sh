#!/usr/bin/env bash
# e2e/p5/97_working_focus.sh — T-FOCUS: 지금 하는 일(PRD FR-3.1.5 · openapi v0.3.8 Lane.focus · colab-cli v0.9.7 `status set working --note` ·
# harness v0.9.13 브리프 [2] 줄).
#
# 비용 한 줄(I-3): 페이크 턴 2 · $0 · ≈ 25s. 실기 대조 없음(페이크 고정 — 대본이 `status set working --note` 를 부른다).
#
# 실사용 모양(Director 요청 2026-09-27, 게임 제작 방): 에이전트가 턴을 시작하자마자 사람에게 「지금 무엇을 풀고 있는지」 한 문장을 말한다.
#   F1  (페이크) Writer 가 턴 시작에 `colab status set working --note "<한 문장>"` → 응답의 lane.focus = {text, source: agent}
#   F2  턴이 도는 동안 listLanes 의 그 lane focus = 에이전트 문장 · source agent (턴 중 폴링으로 잡는다)
#   F3  lane.updated 가 SSE 로 흘렀다(stream_event 에 focus.source=agent 인 lane.updated)
#   F4  턴이 끝나면 focus = null (listLanes · DB)
#   F5  선언 없는 턴(Researcher) 은 도는 동안 source = derived 대신 문장 「Director 의 「…」 요청을 처리하고 있습니다」, 끝나면 null
#   F6  브리프 [2] 에 「지금 하는 일」 줄(claude_code → mcp 말) — 탭으로 번들을 본다
#   F7  선언은 활동 피드에 남는다(task_event set_status working)
# 산출물: out/97-checks.tsv · out/97-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-97.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-97.json"; WORK="$P5_TMP_ROOT/97/work"; DLOG="$OUT/daemon-97.log"
TAP="$OUT/tap-97.jsonl"; TAP_PORT="${TAP_PORT_97:-8197}"
g5_chk_init "$OUT/97-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-97.pid"; return 0; }
trap cleanup EXIT
[ "$RUNTIME" = fake ] || { echo "97_: 페이크 고정(RUNTIME=$RUNTIME) — 건너뜀"; exit 0; }

step "0. claim 탭"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 워크스페이스 · 페어링"
signup "focus+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Working focus $STAMP")"
rm -rf "$WORK"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 2
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-97.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

step "2. Writer(턴 시작에 선언 · 8초 일함 · 게시) · Researcher(선언 없음 · 8초 · 게시) · Lead(방 시작 턴은 한 줄)"
NOTE="코너에서 차가 미끄러지는 원인을 찾고 있습니다 — 타이어 접지 한계 공식을 점검하는 중입니다"
FOCUS_OUT="$OUT/97-writer-status.json"
rm -f "$FOCUS_OUT"
FAKE_W="$(jq -nc --arg n "$NOTE" --arg o "$FOCUS_OUT" \
  '{turns:[{steps:[{exec:("colab status set working --note \"" + $n + "\" > " + $o + " && sleep 8 && colab message post --body \"WRITER done\""), exec_timeout_ms:60000}]}]}')"
FAKE_R='{"turns":[{"steps":[{"exec":"sleep 8 && colab message post --body \"RESEARCHER done\"","exec_timeout_ms":60000}]}]}'
FAKE_L='{"turns":[{"steps":[{"exec":"colab message post --body \"LEAD ready\""}]}]}'
WRITER="$(create_agent_fake "$WS" Writer writer claude_code "$LEAD_MODEL" "받은 일에 한 문장으로 답하라." '초안을 쓴다' "$FAKE_W")"
RESEARCHER="$(create_agent_fake "$WS" Researcher researcher claude_code "$LEAD_MODEL" "받은 일에 한 문장으로 답하라." '자료를 찾는다' "$FAKE_R")"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$LEAD_MODEL" "받은 일을 나눠라." '팀을 이끈다' "$FAKE_L")"
SESSION="$(create_session_p2 "$WS" "마리오 카트" "커브가 어색한 원인을 찾아 고친다" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$WRITER" "$RESEARCHER")"
echo "$WS $SESSION $LEAD $WRITER $RESEARCHER $RUNTIME_ID" > "$OUT/97-ids.txt"
wait_quiet "$SESSION" "$T_TURN" || true

step "3. Director 가 Writer · Researcher 에게 지시 → 두 턴이 동시에 돈다"
api_ok POST "/rooms/$SESSION/messages" "$(with_work "$SESSION" "$(jq -nc --arg c "[@Writer](mention://agent/$WRITER) [@Researcher](mention://agent/$RESEARCHER) 커브가 어색해. 원인 찾아서 고쳐 줘." '{content:$c}')")" \
  -H "Idempotency-Key: $(uuid)" > "$OUT/97-order.json"
L_W="$(jq -r --arg a "$WRITER" '.triggers[] | select(.agent_id==$a) | .lane_id' "$OUT/97-order.json")"
L_R="$(jq -r --arg a "$RESEARCHER" '.triggers[] | select(.agent_id==$a) | .lane_id' "$OUT/97-order.json")"
T_W="$(jq -r --arg a "$WRITER" '.triggers[] | select(.agent_id==$a) | .task_id' "$OUT/97-order.json")"
[ -n "$L_W" ] && [ -n "$L_R" ] || die "지시가 두 에이전트를 깨우지 않았다: $(cat "$OUT/97-order.json")"
lane_focus() { api_ok GET "/rooms/$SESSION/lanes" | jq -c --arg l "$1" '.[] | select(.id==$l) | .focus'; }

wait_step F2 "Writer 턴 중 listLanes focus = 에이전트 문장 · source agent" 60 \
  "[ \"\$(lane_focus $L_W | jq -r '(.source // \"\") + \"|\" + (.text // \"\")')\" = \"agent|$NOTE\" ]"
lane_focus "$L_W" > "$OUT/97-focus-writer-running.json"
wait_step F5a "Researcher 턴 중 focus = 대신 문장 · source derived" 60 \
  "[ \"\$(lane_focus $L_R | jq -r '.source // \"\"')\" = derived ]"
lane_focus "$L_R" > "$OUT/97-focus-researcher-running.json"
chk F5b "대신 문장 = 「Director 의 「…」 요청을 처리하고 있습니다」" \
  "Director 의 「커브가 어색해. 원인 찾아서 고쳐 줘.」 요청을 처리하고 있습니다" \
  "$(jq -r '.text' "$OUT/97-focus-researcher-running.json")"

step "4. 턴 끝"
wait_quiet "$SESSION" "$T_TURN" || true
chk F1 "status set working --note 응답의 lane.focus = 문장 · agent" "agent|$NOTE" \
  "$(jq -r '(.lane.focus.source // "") + "|" + (.lane.focus.text // "")' "$FOCUS_OUT" 2>/dev/null)"
chk F3 "lane.updated(SSE) 에 focus.source=agent 가 흘렀다" yes \
  "$([ "$(psqlq "select count(*) from stream_event where type='lane.updated' and payload->>'id'='$L_W' and payload->'focus'->>'source'='agent'")" -ge 1 ] && echo yes || echo no)"
chk F4a "턴 끝 — Writer focus null(listLanes)" null "$(lane_focus "$L_W")"
chk F4b "턴 끝 — Researcher focus null(listLanes)" null "$(lane_focus "$L_R")"
chk F4c "턴 끝 — DB focus 칸 비움" 0 "$(psqlq "select count(*) from lane where session_id='$SESSION' and focus_text is not null")"
chk F4d "턴 끝 frame 은 focus null 을 싣는다" yes \
  "$([ "$(psqlq "select count(*) from stream_event where type='lane.updated' and payload->>'id'='$L_W' and payload->'focus' = 'null'::jsonb and payload->>'status' = 'done'")" -ge 1 ] && echo yes || echo no)"

step "5. 브리프 [2] 줄 · 활동 피드"
python3 - "$TAP" "$T_W" > "$OUT/97-brief.txt" <<'PY'
import json, sys
tap, tid = sys.argv[1:]
want = "tell the people what you are doing now with the `colab_status_set` tool (status: working, note:"
for line in open(tap):
    try: body = json.loads(line)["body"]
    except Exception: continue
    for b in (body.get("tasks") or []):
        if b["task"]["id"] != tid: continue
        t = b["brief"]["text"]
        print("yes" if want in t and "Not after every tool call." in t and "`colab status set working" not in t else "no")
        sys.exit(0)
print("missing")
PY
chk F6 "Writer 브리프 [2] — 지금 하는 일 줄(mcp 말 · 셸 명령 없음)" yes "$(cat "$OUT/97-brief.txt")"
chk F7 "선언이 활동 피드에(set_status working)" 1 \
  "$(psqlq "select count(*) from task_event where task_id='$T_W' and verb='set_status' and object_ref = '\"working\"'::jsonb")"

printf '\n97_working_focus: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
