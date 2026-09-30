#!/usr/bin/env bash
# e2e/p5/96_message_parts.sh — T-PARTS: 부분 메시지(PRD FR-3.1.4 · openapi v0.3.6 postMessageGroup · colab-cli v0.9.5 --parts-file ·
# harness v0.9.11 <trigger> 부분).
#
# 비용 한 줄(I-3): 페이크 턴 3 · $0 · ≈ 20s. 실기 대조 없음(페이크 고정 — 대본이 --parts-file 을 부른다).
#
# 실사용 모양(Director 요청 2026-09-27): Lead 가 한 턴에 여러 명에게 서로 다른 말을 한다 — 보고는 Director 에게, 요청은 Writer·Researcher 에게.
#   P1  (페이크) Lead 가 Director 지시로 깨어난 턴에서 `colab message post --parts-file` → 행 셋 · 같은 group_id · group_index 0·1·2 · group_size 3
#   P2  행마다 판정: 보고 → Director(↩ 원래 지시) · 요청 → Writer · 요청 → Researcher (본문 속 @Writer 링크는 Researcher 부분의 받는 쪽이 아니다)
#   P3  두 에이전트가 **각자 제 부분**으로만 깨어났다 — task 하나씩, trigger = 제 부분 행. 본문 속 @Writer 는 Writer 를 두 번 깨우지 않았다
#   P4  사람 부분은 알림만 — Director 에게 mention 인박스 1건(그 부분 인용) · 그 부분이 만든 task 0
#   P5  Writer 번들: <trigger> 에 제 부분만 + group 속성 + 「Other parts of the same message: → Director (report) · → @Researcher (request)」,
#       남의 부분 본문 0 · 브리프 [2] 부분 메시지 줄(표면별 — claude_code 는 mcp 말)
#   P6  listMessages ?group= → 셋, group_index 순
#   (422 네 가지 · 원자성 · 멱등은 서버 테스트 message_parts_test.go 가 소유 — 데몬 밖에서는 TaskToken 을 쥘 수 없다)
# 산출물: out/96-checks.tsv · out/96-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-96.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-96.json"; WORK="$P5_TMP_ROOT/96/work"; DLOG="$OUT/daemon-96.log"
TAP="$OUT/tap-96.jsonl"; TAP_PORT="${TAP_PORT_96:-8196}"
g5_chk_init "$OUT/96-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-96.pid"; return 0; }
trap cleanup EXIT
[ "$RUNTIME" = fake ] || { echo "96_: 페이크 고정(RUNTIME=$RUNTIME) — 건너뜀"; exit 0; }

step "0. claim 탭"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 워크스페이스 · 페어링"
signup "parts+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Message parts $STAMP")"
DIRECTOR_ID="$(api_ok GET /me | jq -r .user.id)"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 2
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-96.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

step "2. Writer · Researcher(한 줄 답) · Lead(부분 셋) · 방(미션 시작 → Lead 첫 턴)"
FAKE_W='{"turns":[{"steps":[{"exec":"colab message post --body \"WRITER got my part\""}]}]}'
FAKE_R='{"turns":[{"steps":[{"exec":"colab message post --body \"RESEARCHER got my part\""}]}]}'
WRITER="$(create_agent_fake "$WS" Writer writer claude_code "$LEAD_MODEL" "받은 일에 한 문장으로 답하라." '초안을 쓴다' "$FAKE_W")"
RESEARCHER="$(create_agent_fake "$WS" Researcher researcher claude_code "$LEAD_MODEL" "받은 일에 한 문장으로 답하라." '자료를 찾는다' "$FAKE_R")"
PARTS_FILE="$OUT/96-parts.json"
BODY_D="v9 올렸습니다. 커브 원인은 횡가속도 상한이었습니다 (DIRECTOR-ONLY)"
BODY_W="스프라이트 24방향으로 맞춰 주세요 (WRITER-ONLY)"
BODY_R="FX 자료를 모아 주세요 — [@Writer](mention://agent/$WRITER) 가 쓸 겁니다 (RESEARCHER-ONLY)"
jq -nc --arg d "$BODY_D" --arg w "$BODY_W" --arg r "$BODY_R" \
  '[{to:["@Director"],body:$d},{to:["@Writer"],body:$w},{to:["@Researcher"],body:$r}]' > "$PARTS_FILE"
# 대본은 턴마다 같다(페이크는 턴마다 새 세션이라 늘 첫 턴을 쓴다 — 마지막 턴이 반복된다).
# 방 시작 턴도 부분을 올리지만(트리거가 시스템 → 보고가 아니다), 판정은 **Director 지시로 깨어난 턴**의 행만 본다:
# 그래야 첫 부분이 그 지시에 대한 보고(↩)가 된다(FR-3.1.3 표 7행). 멱등키는 task 마다 달라(UUIDv5 task:<id>:<seq>) 두 묶음이 따로 생긴다.
# T-FIX-B: 보고(「요청하신 작업이 끝났습니다」)로 깨어난 턴은 **메시지 없이** 끝낸다(빈 확인 메시지 규약). 옛 대본은 어느 턴에서나
# 부분 셋을 다시 올렸는데, Writer·Researcher 의 멘션 없는 답(규칙 4 — Lead 를 깨우지 않는다) 뒤에 턴 종료가 보고를 내지 않던
# 결함에 기대 멈추고 있었다. 턴 종료도 보고를 내자(FR-6.5 · colab-cli §2) 요청 → 보고 → 요청이 되어 방이 loop 상한으로 멈췄다.
FAKE_L="$(jq -nc --arg f "$PARTS_FILE" '{turns:[{steps:[{exec:("case \"$ACPFAKE_PROMPT\" in *요청하신\\ 작업이\\ 끝났습니다*) exit 0;; esac; colab message post --parts-file " + $f)}]}]}')"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$LEAD_MODEL" "받은 일을 나눠 보고·요청하라." '팀을 이끈다' "$FAKE_L")"
SESSION="$(create_session_p2 "$WS" "마리오 카트" "커브가 어색한 원인을 찾아 고친다" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$WRITER" "$RESEARCHER")"
echo "$WS $SESSION $LEAD $WRITER $RESEARCHER $RUNTIME_ID $DIRECTOR_ID" > "$OUT/96-ids.txt"
wait_quiet "$SESSION" "$T_TURN" || true

step "2b. Director 지시 → Lead 두 번째 턴이 부분 셋을 올린다"
ORDER="$(api_ok POST "/rooms/$SESSION/messages" "$(with_work "$SESSION" "$(jq -nc --arg c "[@Lead](mention://agent/$LEAD) 커브가 어색해. 원인 찾아서 고쳐 줘." '{content:$c}')")" -H "Idempotency-Key: $(uuid)" | jq -r .message.id)"
wait_quiet "$SESSION" "$T_TURN" || true
T_L="$(psqlq "select id from task where session_id='$SESSION' and agent_id='$LEAD' and trigger_message_id='$ORDER' limit 1")"
[ -n "$T_L" ] || die "Lead 두 번째 턴이 없다 (see $DLOG)"

step "3. 부분 행"
psqlq "select group_index || E'\t' || id || E'\t' || coalesce(group_id::text,'NULL') || E'\t' || coalesce(group_size::text,'NULL') || E'\t' || speech || E'\t' || (addressees->0->>'name') || E'\t' || coalesce(responds_to_message_id::text,'-') from message where source_task_id='$T_L' order by created_at, group_index" > "$OUT/96-rows.txt"
cat "$OUT/96-rows.txt" >&2
N_ROWS="$(wc -l < "$OUT/96-rows.txt" | tr -d ' ')"
GID="$(awk -F'\t' 'NR==1{print $3}' "$OUT/96-rows.txt")"
chk P1a "Lead 부분 게시 = 행 셋" 3 "$N_ROWS"
chk P1b "같은 group_id · index 0,1,2 · size 3" "1|0,1,2|3,3,3" \
  "$(awk -F'\t' '{print $3}' "$OUT/96-rows.txt" | sort -u | wc -l | tr -d ' ')|$(awk -F'\t' '{print $1}' "$OUT/96-rows.txt" | paste -sd, -)|$(awk -F'\t' '{print $4}' "$OUT/96-rows.txt" | paste -sd, -)"
P0="$(awk -F'\t' '$1==0{print $2}' "$OUT/96-rows.txt")"; P1="$(awk -F'\t' '$1==1{print $2}' "$OUT/96-rows.txt")"; P2="$(awk -F'\t' '$1==2{print $2}' "$OUT/96-rows.txt")"
TRIG_L="$ORDER"
chk P2a "판정: 보고→Director · 요청→Writer · 요청→Researcher" "report:Director|request:Writer|request:Researcher" \
  "$(awk -F'\t' '{print $5":"$6}' "$OUT/96-rows.txt" | paste -sd'|' -)"
chk P2b "보고의 ↩ = Lead 를 깨운 지시" "$TRIG_L" "$(awk -F'\t' '$1==0{print $7}' "$OUT/96-rows.txt")"
chk P2c "Researcher 부분 addressees 는 Researcher 하나(본문 @Writer 제외)" 1 "$(psqlq "select jsonb_array_length(addressees) from message where id='$P2'")"

step "4. 받는 쪽마다 제 부분으로"
T_W="$(psqlq "select string_agg(trigger_message_id::text, ',') from task where session_id='$SESSION' and agent_id='$WRITER' and trigger_message_id in ('$P0','$P1','$P2')")"
T_R="$(psqlq "select string_agg(trigger_message_id::text, ',') from task where session_id='$SESSION' and agent_id='$RESEARCHER' and trigger_message_id in ('$P0','$P1','$P2')")"
chk P3a "Writer task 하나 · 트리거 = Writer 부분" "$P1" "${T_W:-none}"
chk P3b "Researcher task 하나 · 트리거 = Researcher 부분" "$P2" "${T_R:-none}"
chk P3c "두 에이전트가 제 부분으로 깨어나 답했다" "2" "$(psqlq "select count(distinct m.author_id) from message m join task t on t.id = m.source_task_id where m.content like '%got my part' and t.trigger_message_id in ('$P1','$P2')")"

step "5. 사람 부분 = 알림만"
chk P4a "Director mention 인박스 1건(그 부분 인용)" 1 "$(psqlq "select count(*) from inbox_item where type='mention' and quote_message_id='$P0'")"
chk P4b "Director 부분이 만든 task 0" 0 "$(psqlq "select count(*) from task where trigger_message_id='$P0'")"

step "6. Writer 번들(탭)"
WTASK="$(psqlq "select id from task where trigger_message_id='$P1' limit 1")"
python3 - "$TAP" "$WTASK" "$GID" > "$OUT/96-bundle.txt" <<'PY'
import json, sys
tap, tid, gid = sys.argv[1:]
for line in open(tap):
    try: body = json.loads(line)["body"]
    except Exception: continue
    for b in (body.get("tasks") or []):
        if b["task"]["id"] != tid: continue
        p = b["prompt"]; t = p[p.find("<trigger>\n"):p.find("</trigger>")]
        own = "WRITER-ONLY" in t
        others = ("DIRECTOR-ONLY" in t) or ("RESEARCHER-ONLY" in t)
        line_ok = "Other parts of the same message: → Director (report) · → @Researcher (request)." in t
        attr = ('group="%s"' % gid) in t
        brief = "send one message in parts: the `parts` argument of `colab_message_post` — one part per recipient." in b["brief"]["text"]
        print("|".join("yes" if x else "no" for x in (own, not others, attr, line_ok, brief)))
        sys.stderr.write(t + "\n")
        sys.exit(0)
print("missing")
PY
chk P5 "Writer <trigger>: 제 부분 · 남의 본문 0 · group 속성 · 다른 부분 줄 · 브리프 [2] 줄" "yes|yes|yes|yes|yes" "$(cat "$OUT/96-bundle.txt")"

step "7. listMessages ?group"
chk P6 "?group= → 셋, group_index 순" "0,1,2" "$(api_ok GET "/rooms/$SESSION/messages?group=$GID" | jq -r '[.items[].group_index]|map(tostring)|join(",")')"

printf '\n96_message_parts: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
