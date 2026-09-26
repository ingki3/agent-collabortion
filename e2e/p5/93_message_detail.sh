#!/usr/bin/env bash
# e2e/p5/93_message_detail.sh — T-DETAIL: 에이전트 메시지의 대화(content)와 작업 내용(detail)
# (openapi v0.3.1 D23 · colab-cli v0.9.2 --detail-file · harness v0.9.4 브리프 [2]·<trigger> <detail>).
#
# 비용 한 줄(I-3): 페이크 턴 2 · $0 · ≈ 15s. 실기(RUNTIME=real): Lead haiku 1턴 ≈ $0.02 · ≈ 1분
#
# STO 방 실측(2026-09-24)의 모양: Researcher 조사 결과 1.5만 자가 「@Lead 조사 끝났습니다」 한 줄과 한 본문에 섞였다.
#   D1  (페이크) `colab message post --body … --detail-file` → content 는 대화만, detail 은 파일 그대로(바이트 수 동일)
#   D2  대화의 멘션이 Writer 를 깨웠다 — detail 안의 멘션 링크는 멘션으로 저장되지 않는다(아무도 안 깨운다)
#   D3  Writer 턴 번들: <trigger> 에 <detail> 전문 · 브리프 [2] 에 작업 내용 규칙
#   D4  listMessages 에 detail (S7 이 읽는 호출)
#   R1  (실기) 지시문이 detail 을 말하지 않을 때 에이전트가 긴 조사 결과를 --detail 로 나누는지 — 판정 아닌 관측(N/A + 값)
# 산출물: out/93-checks.tsv · out/93-*.txt
source "$(dirname "$0")/lib_i5.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-93.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-93.json"; WORK="$P5_TMP_ROOT/93/work"; DLOG="$OUT/daemon-93.log"
TAP="$OUT/tap-93.jsonl"; TAP_PORT="${TAP_PORT_93:-8147}"
g5_chk_init "$OUT/93-checks.tsv"
cleanup() { [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true; daemon_stop "$OUT/daemon-93.pid"; return 0; }
trap cleanup EXIT

# 실기 지시문은 detail 을 말하지 않는다 — 브리프 [2] 가 가르치는지가 관측 대상이다.
LEAD_INS_93="요청받은 주제를 자세히 조사해 보고하라: 표 2개 이상, 항목 20개 이상, 각 항목에 근거 한 문장. 보고는 colab_message_post 로 한 번만 게시한다. 파일을 만들거나 다른 도구를 부르지 마라."
DETAIL_FILE="$OUT/93-detail.md"
{
  printf '## A. 정렬 알고리즘 비교\n\n| 알고리즘 | 평균 | 최악 | 안정 |\n|---|---|---|---|\n'
  for i in $(seq 1 40); do printf '| 알고리즘 %d | O(n log n) | O(n^2) | 예 |\n' "$i"; done
  printf '\n원문에 있던 링크: [@Lead](mention://agent/LEAD_ID) 에게 넘기라는 문장.\n\n```\ncode block\n```\n'
} > "$DETAIL_FILE"

step "0. claim 탭 · RUNTIME=$RUNTIME"
rm -f "$TAP"; : > "$TAP"; : > "$DLOG"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"

step "1. 계정 · 워크스페이스 · 페어링"
signup "detail+$STAMP@example.com" password123 Director >/dev/null
WS="$(create_workspace "Message detail $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_p5 "$PTOK" "$CFG" "$WORK" 2
daemon_run_p5 "$CFG" "$DLOG" > "$OUT/daemon-93.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
RUNTIME_ID="$(runtime_of_config "$CFG")"

step "2. Writer · Lead · 방(미션 시작 → Lead 첫 턴)"
FAKE_W='{"turns":[{"steps":[{"exec":"colab message post --body \"WRITER got it\""}]}]}'
WRITER="$(create_agent_fake "$WS" Writer writer claude_code "$LEAD_MODEL" "받은 일에 한 문장으로 답하라." '초안을 쓴다' "$FAKE_W")"
# Lead 페이크: 대화에 Writer 멘션, 작업 내용은 파일에서(detail 안의 Lead 링크는 아무도 안 깨워야 한다).
sed -i.bak "s/LEAD_ID/00000000-0000-0000-0000-000000000000/" "$DETAIL_FILE" && rm -f "$DETAIL_FILE.bak"
BODY_93="[@Writer](mention://agent/$WRITER) 조사 끝났습니다. 표 1개, 40항목입니다. 표로 옮겨 주세요."
FAKE_L="$(jq -nc --arg b "$BODY_93" --arg f "$DETAIL_FILE" --arg q "'" '{turns:[{steps:[{exec:("colab message post --body " + $q + $b + $q + " --detail-file " + $q + $f + $q)}]}]}')"
LEAD="$(create_agent_fake "$WS" Lead lead claude_code "$LEAD_MODEL" "$LEAD_INS_93" '팀을 이끈다' "$FAKE_L")"
SESSION="$(create_session_p2 "$WS" "정렬 알고리즘 조사" "주요 정렬 알고리즘 8종을 시간·공간 복잡도·안정성·실사용 예로 비교 조사해 보고한다" "$LEAD" "$RUNTIME_ID" "$LEAD" "$LEAD" "$WRITER")"
echo "$WS $SESSION $LEAD $WRITER $RUNTIME_ID" > "$OUT/93-ids.txt"
wait_quiet "$SESSION" "$T_TURN" || true
T_L="$(psqlq "select id from task where session_id='$SESSION' and agent_id='$LEAD' order by created_at limit 1")"
M_L="$(psqlq "select id || E'\t' || char_length(content) || E'\t' || coalesce(char_length(detail)::text,'NULL') || E'\t' || coalesce(octet_length(detail)::text,'NULL') from message where source_task_id='$T_L' order by created_at limit 1")"
IFS=$'\t' read -r M_L_ID M_L_CLEN M_L_DLEN M_L_DBYTES <<<"$M_L"
psqlq "select content, E'\n---- detail ----\n', coalesce(detail,'(없음)') from message where id='$M_L_ID'" > "$OUT/93-lead-message.txt" 2>/dev/null || true

if [ "$RUNTIME" = fake ]; then
  chk D1a "Lead 메시지 content 는 대화만 (글자 수)" "$(python3 -c 'import sys; print(len(sys.argv[1]))' "$BODY_93")" "${M_L_CLEN:-none}"
  chk D1b "detail = --detail-file 바이트 그대로" "$(wc -c < "$DETAIL_FILE" | tr -d ' ')" "${M_L_DBYTES:-none}"
  T_W="$(psqlq "select id from task where session_id='$SESSION' and agent_id='$WRITER' order by created_at limit 1")"
  chk D2a "대화의 멘션이 Writer 를 깨웠다" yes "$( [ -n "$T_W" ] && echo yes || echo no )"
  N_M="$(psqlq "select jsonb_array_length(mentions) || ':' || (mentions->0->>'id') from message where id='$M_L_ID'")"
  chk D2b "detail 안의 링크는 멘션이 아니다 (mentions = 대화의 Writer 하나)" "1:$WRITER" "$N_M"
  WAIT_S=$T_TURN wait_task "$T_W" completed failed cancelled >/dev/null || true
  step "3. Writer 번들(탭)"
  python3 - "$TAP" "$T_W" "$DETAIL_FILE" > "$OUT/93-bundle.txt" <<'PY'
import json, sys
tap, tid, df = sys.argv[1:]
detail = open(df, encoding="utf-8").read().rstrip("\n")
for line in open(tap):
    try: body = json.loads(line)["body"]
    except Exception: continue
    for b in (body.get("tasks") or []):
        if b["task"]["id"] != tid: continue
        p = b["prompt"]; t = p[p.find("<trigger>\n"):]
        # Writer is claude_code (tool_surface mcp, harness v0.9.6): [2] names the
        # tool's detail argument and no shell command anywhere in the bundle.
        text = b["brief"]["text"] + p
        print("yes" if ("<detail>\n" + detail + "\n</detail>") in t else "no",
              "yes" if ("`detail_file`" in b["brief"]["text"] and "`colab " not in text) else "no", sep="\t")
        sys.exit(0)
print("missing\tmissing")
PY
  IFS=$'\t' read -r B_DET B_RULE <<<"$(cat "$OUT/93-bundle.txt")"
  chk D3 "Writer 번들: <trigger> 에 <detail> 전문 · 브리프 [2] 규칙(툴 말 · 셸 명령 0, harness v0.9.6)" "yes|yes" "$B_DET|$B_RULE"
else
  chk_na D1 "페이크만(대본이 --detail-file 을 부른다)" "-" "RUNTIME=real"
  chk_na D2 "페이크만" "-" "RUNTIME=real"
  chk_na D3 "페이크만" "-" "RUNTIME=real"
  # 턴이 여럿이다(Lead 가 위임하면 Writer·Lead 재진입) — 방의 에이전트 메시지 전부를 한 줄씩.
  psqlq "select a.name || ':' || char_length(m.content) || '/' || coalesce(char_length(m.detail)::text,'-') from message m join agent a on a.id=m.author_id where m.session_id='$SESSION' order by m.created_at" > "$OUT/93-real-split.txt"
  psqlq "select a.name || ':' || m.content || E'\n---- detail ----\n' || coalesce(m.detail,'(없음)') || E'\n====' from message m join agent a on a.id=m.author_id where m.session_id='$SESSION' order by m.created_at" > "$OUT/93-lead-message.txt"
  chk_na R1 "실기 관측: 에이전트 메시지별 content 글자/detail 글자" "-" "$(tr '\n' ' ' < "$OUT/93-real-split.txt")"
fi

step "4. listMessages (S7 이 읽는 호출)"
LM="$(api_ok GET "/rooms/$SESSION/messages?limit=200" | jq -r --arg id "$M_L_ID" '.items[] | select(.id==$id) | (.detail // "NULL" | length | tostring)')"
if [ "${M_L_DLEN:-NULL}" = NULL ]; then WANT_LM=4; else WANT_LM="$M_L_DLEN"; fi   # "NULL" 은 4글자
chk D4 "listMessages 의 detail 글자 수 = DB" "$WANT_LM" "${LM:-none}"

printf '\n93_message_detail: pass=%s fail=%s (RUNTIME=%s)\n' "$pass" "$fail" "$RUNTIME"
[ "$fail" = 0 ]
