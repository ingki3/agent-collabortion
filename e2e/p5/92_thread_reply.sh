#!/usr/bin/env bash
# e2e/p5/92_thread_reply.sh — T-THREAD: 스레드로 물은 질문에 에이전트가 **그 스레드에** 답한다
# (daemon-protocol v0.9.2 task.thread_root_id · harness v0.9.3 COLAB_THREAD_ID·<message thread> · colab-cli v0.9.1).
#
# 비용 한 줄(I-3): 페이크 턴 2 · $0 · ≈ 15s. 실기(RUNTIME=real): Lead haiku 2턴 ≈ $0.02 · ≈ 1분
#
# STO 방 실측(2026-09-25)의 모양 그대로: Director 가 요약 메시지 스레드에 「@Lead 종료 된거지?」 →
# 예전에는 Lead 답이 parent_id NULL 로 메인 타임라인에 올라갔다.
#   T1  미션 시작 턴(최상위 트리거)의 답은 메인 타임라인(parent_id NULL)
#   T2  스레드 질문 턴의 답은 그 스레드(parent_id = 루트)
#   T3  (페이크) 에이전트 프로세스가 본 COLAB_THREAD_ID — 최상위 턴은 키 없음, 스레드 턴은 루트
#   T4  claim 번들: 스레드 턴만 task.thread_root_id = 루트 · 프롬프트 <message … thread="루트">
#   T5  스레드 읽기(웹 S7 펼치기와 같은 호출 ?thread=)에 에이전트 답이 있고 루트 reply_count 가 센다
#
# 페이크 대본은 exec 한 줄 — `colab message post` 를 **--reply-to 없이** 부른다. 기본 답글 위치가 전부다.
# 실기 지시문도 reply_to·top_level 을 말하지 않는다 — 턴 프롬프트의 thread 속성·마지막 지시문·CLI 기본값이 일한다.
# 산출물: out/92-checks.tsv · out/92-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-92.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-92.json"; WORK="$P5_TMP_ROOT/92/work"; DLOG="$OUT/daemon-92.log"
TAP="$OUT/tap-92.jsonl"; TAP_PORT="${TAP_PORT_92:-8146}"
g5_chk_init "$OUT/92-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-92.pid"; return 0; }
trap cleanup EXIT

LEAD_INS_92="질문에 한국어 한 문장으로 답하라. 답은 colab_message_post 로 딱 한 번 게시하고, 본문은 'ANSWER' 로 시작한다. 다른 도구는 부르지 마라."
# 페이크: 에이전트가 본 env 를 본문에 싣는다(T3) — 키가 없으면 none.
FAKE_92='{"turns":[{"steps":[{"exec":"colab message post --body \"ANSWER thread=${COLAB_THREAD_ID:-none}\""}]}]}'

step "0. claim 탭 · RUNTIME=$RUNTIME"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 워크스페이스 · 페어링"
signup "thread+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Thread reply $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 2
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-92.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

step "2. Lead 하나 · 방(미션 시작 → Lead 첫 턴 = 최상위 트리거)"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$LEAD_MODEL" "$LEAD_INS_92" '팀을 이끈다' "$FAKE_92")"
SESSION="$(create_session_p2 "$WS" "STO 시장 리서치" "스레드 답글 확인" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD")"
echo "$WS $SESSION $LEAD $RUNTIME_ID" > "$OUT/92-ids.txt"
wait_quiet "$SESSION" "$T_TURN" || true
T_TOP="$(psqlq "select id from task where session_id='$SESSION' and agent_id='$LEAD' order by created_at limit 1")"
A_TOP="$(psqlq "select id || E'\t' || coalesce(parent_id::text,'NULL') || E'\t' || left(replace(content,E'\n',' '),120) from message where source_task_id='$T_TOP' order by created_at limit 1")"
IFS=$'\t' read -r A_TOP_ID A_TOP_PARENT A_TOP_BODY <<<"$A_TOP"
chk T1 "최상위 트리거 턴의 답은 메인 타임라인 (parent_id)" NULL "${A_TOP_PARENT:-none}"

step "3. Director: 요약(/note, 아무도 안 깨움) → 그 스레드에 「@Lead 종료 된거지?」"
ROOT="$(post_message "$SESSION" "/note 미션 요약: 경쟁사 3곳 정리 완료" | jq -r .message.id)"
Q="$(api_ok POST "/rooms/$SESSION/messages" "$(with_work "$SESSION" "$(jq -nc --arg c "[@Lead](mention://agent/$LEAD) 종료 된거지?" --arg p "$ROOT" '{content:$c,parent_id:$p}')")" -H "Idempotency-Key: $(uuid)")"
T_TH="$(jq -r --arg a "$LEAD" '.triggers[] | select(.agent_id==$a) | .task_id' <<<"$Q")"
chk T2a "스레드 질문이 Lead 를 깨웠다 (task)" yes "$( [ -n "$T_TH" ] && echo yes || echo no )"
WAIT_S=$T_TURN wait_task "$T_TH" completed failed cancelled >/dev/null || true
A_TH="$(psqlq "select id || E'\t' || coalesce(parent_id::text,'NULL') || E'\t' || left(replace(content,E'\n',' '),200) from message where source_task_id='$T_TH' order by created_at limit 1")"
IFS=$'\t' read -r A_TH_ID A_TH_PARENT A_TH_BODY <<<"$A_TH"
chk T2 "스레드 질문 턴의 답은 그 스레드 (parent_id = 루트 $ROOT)" "$ROOT" "${A_TH_PARENT:-none}"
printf 'root\t%s\nquestion_task\t%s\nanswer\t%s\t%s\t%s\ntop_answer\t%s\t%s\t%s\n' "$ROOT" "$T_TH" "$A_TH_ID" "$A_TH_PARENT" "$A_TH_BODY" "$A_TOP_ID" "$A_TOP_PARENT" "$A_TOP_BODY" > "$OUT/92-answers.txt"

if [ "$RUNTIME" = fake ]; then
  chk T3a "최상위 턴의 에이전트 env 에 COLAB_THREAD_ID 없음" "ANSWER thread=none" "$A_TOP_BODY"
  chk T3b "스레드 턴의 에이전트 env COLAB_THREAD_ID = 루트" "ANSWER thread=$ROOT" "$A_TH_BODY"
else
  chk_na T3 "에이전트 env 는 페이크만 본문에 싣는다" "-" "RUNTIME=real"
fi

step "4. claim 번들(탭)"
python3 - "$TAP" "$T_TOP" "$T_TH" "$ROOT" > "$OUT/92-bundle.txt" <<'PY'
import json, sys
tap, top, th, root = sys.argv[1:]
seen = {}
for line in open(tap):
    try: body = json.loads(line)["body"]
    except Exception: continue
    for b in (body.get("tasks") or []):
        seen[b["task"]["id"]] = b
def row(tid):
    b = seen.get(tid)
    if not b: return "missing\tmissing\tmissing"
    return "%s\t%s\t%s" % (b["task"].get("thread_root_id", "absent"),
                           ("yes" if ('thread="%s"' % root) in b["prompt"] else "no"),
                           ("yes" if "--top-level" in b["prompt"] else "no"))
print(row(top)); print(row(th))
PY
IFS=$'\t' read -r B_TOP_ID B_TOP_ATTR B_TOP_LINE <<<"$(sed -n 1p "$OUT/92-bundle.txt")"
IFS=$'\t' read -r B_TH_ID B_TH_ATTR B_TH_LINE <<<"$(sed -n 2p "$OUT/92-bundle.txt")"
chk T4a "최상위 턴 번들: thread_root_id 없음 · thread 속성 없음 · 지시문 없음" "absent|no|no" "$B_TOP_ID|$B_TOP_ATTR|$B_TOP_LINE"
chk T4b "스레드 턴 번들: thread_root_id = 루트 · thread 속성 · 지시문"      "$ROOT|yes|yes" "$B_TH_ID|$B_TH_ATTR|$B_TH_LINE"

step "5. 스레드 읽기(S7 펼치기와 같은 호출)"
THR="$(api_ok GET "/rooms/$SESSION/messages?thread=$ROOT&limit=200")"
chk T5a "?thread= 에 에이전트 답이 있다" yes "$(jq -r --arg id "$A_TH_ID" '[.items[] | select(.id==$id)] | if length==1 then "yes" else "no" end' <<<"$THR")"
RC="$(api_ok GET "/rooms/$SESSION/messages?limit=200" | jq -r --arg id "$ROOT" '.items[] | select(.id==$id) | .reply_count')"
chk T5b "루트 reply_count = 질문 + 답 = 2" 2 "${RC:-none}"

printf '\n92_thread_reply: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
