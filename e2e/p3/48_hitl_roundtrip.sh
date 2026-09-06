#!/usr/bin/env bash
# e2e/p3/48_hitl_roundtrip.sh — T-I3 (a): **HITL 왕복** 실기 1회씩.
#
#   E7-01·03  에이전트가 HITL 을 등록 → 턴 종료 → task `waiting_human`(프로세스 없음, 슬롯 미점유),
#             타임라인에 HITL 카드(message.kind='hitl'), Director 인박스 `action_required`, workdir 보존
#   E7-18     같은 세션의 **다른 lane 은 계속 돈다** — 데몬 `capacity=1` 로 두어 반증 가능하게 만든다:
#             waiting_human 이 슬롯을 잡고 있으면 Peer 의 task 는 영원히 queued 로 남는다
#   E8-01     웹 인박스(S8)에서 Director 가 답 → `answered` + 결정 기록 1건 + 재큐잉(새 attempt) →
#             **resume 우선**(`task_attempt.resumed=true`)이고 답변이 `<hitl_answer>` 로 프롬프트에
#   E8-02     같은 왕복을 **강제 콜드 스타트**로 한 번 더(Claude Code transcript 삭제) → 이어간다
#   E7-17     `approval` **거절** → 프롬프트에 `approved: false` + 사유, task 는 `queued`(failed 아님)
#   두 도구 표면  arm A1·A2·A4 는 Claude Code(`mcp`), arm A3 는 Hermes(`cli_wrapper` 래퍼)
#
# ── 차단 결함과 우회 (Lead 결정) ────────────────────────────────────────────
# **K-7**(계약 충돌) · **C-4**(CLI): `contracts/colab-cli.md` v0.5 §2.4 는 `colab hitl *` 의 경로를
# `POST /v1/tasks/{T}/hitl` 로 적고 CLI(PR #126)가 그대로 구현했는데, **openapi 에는 그 경로가 없다**
# (`createHitlRequest` = `POST /sessions/{S}/hitl-requests`) — 서버(PR #124)는 openapi 만 구현한다.
# 실서버 1회차 실측: MCP 툴 `colab_hitl_ask` 도 cli_wrapper 래퍼도 **404 Not Found** 로 거부되고,
# 에이전트는 30여 툴콜을 헤매다 저장소를 뒤졌다. Lead 판정: openapi 가 SSOT → 계약 PR + CLI 핫픽스.
#
# 그래서 이 스크립트는 두 가지를 **같은 턴 안에서** 한다:
#   (1) 도구 표면 프로브 — 에이전트가 정식 도구를 한 번 부른다. 그 결과가 (a) 의 "두 도구 표면" 칸이다.
#       지금은 404 이므로 **FAIL 로 남는다**(핫픽스 뒤 이 스크립트만 재실행하면 칸이 갱신된다).
#   (2) 우회 — 같은 턴에서 attempt 토큰으로 openapi 경로에 직접 등록한다. 서버가 보는 것은 정식 경로와
#       **완전히 같다**(source=agent · pending_hitl · 카드 · 인박스). 그래서 그 뒤의 모든 단계
#       (턴 종료 → waiting_human · 웹 답변 · 재큐잉 · `<hitl_answer>` · 거절)는 실기 그대로 잰다.
#
# 과제는 저장소 밖의 무해한 주제다(X-2). 수치는 전부 서버 DB 단일 클럭, 화면 판정만 DOM(§0-9).
# 산출물: out/48-checks.tsv · out/48.json · out/48-prompt-*.txt · web/__screenshots__/p3-48-*.png
source "$(dirname "$0")/lib.sh"
STAMP="$(date +%s)"
COOKIE="$OUT/cookies-48.txt"; rm -f "$COOKIE"
CFG="$OUT/daemon-48.json"; WORK="$OUT/work-48"; DLOG="$OUT/daemon-48.log"
TAP="$OUT/tap-48.jsonl"; TAP_PORT="${TAP_PORT_48:-8101}"
MODEL="${LEAD_MODEL}"
HERMES_MODEL="${HERMES_MODEL:-claude-haiku-4-5-20251001}"
EMAIL="g6a+$STAMP@example.com"; PASSWORD="password123"
export AGENT_BROWSER_SESSION="colab-g6-48-$STAMP"
mkdir -p "$E2E_ROOT/web/__screenshots__"
g5_chk_init "$OUT/48-checks.tsv"

cleanup() {
  [ -n "${TAP_PID:-}" ] && kill "$TAP_PID" 2>/dev/null || true
  [ -f "$OUT/daemon-48.pid" ] && { kill -TERM -- "-$(cat "$OUT/daemon-48.pid")" 2>/dev/null || true; }
  agent-browser close >/dev/null 2>&1 || true
  return 0
}
trap cleanup EXIT

# ── 지시문 (전부 저장소 밖의 무해한 과제) ────────────────────────────────────
ASK_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 사용 설명 초안을 쓰는 작성자다. 답은 한국어로 짧게.

첫 턴에 할 일은 아래 세 가지뿐이다. **각각 한 번씩만** 하고, 실패해도 재시도하거나 다른 방법을 찾지 마라.
  a. colab_hitl_ask 를 한 번 호출한다. question 은 "설명 초안의 독자를 누구로 잡을까요?",
     default 는 "가정용 사용자", context 는 "브리프에 독자가 적혀 있지 않다".
     이 호출이 성공하면 **b 를 건너뛰고 곧장 c 로 간다.** 오류가 나면 아무것도 조사하지 말고 b 로 간다.
  b. (a 가 실패했을 때만) 셸에서 아래 명령을 그대로 한 번 실행한다.
     curl -sS -X POST "$COLAB_SERVER_URL/api/v1/sessions/$COLAB_SESSION_ID/hitl-requests" -H "Authorization: Bearer $COLAB_TASK_TOKEN" -H "Content-Type: application/json" -d '"'"'{"type":"question","question":"설명 초안의 독자를 누구로 잡을까요?","context":"브리프에 독자가 적혀 있지 않다","proposed_default":"가정용 사용자"}'"'"'
     (그 명령의 응답에 "turn_end_required":true 가 들어 있으면 등록된 것이다.)
  c. 메시지를 하나도 게시하지 말고 즉시 턴을 끝낸다. 다른 도구를 부르지 마라.

다음 턴에는 프롬프트에 <hitl_answer> 구간이 들어 있다.
  a. 그 구간의 answer 값을 읽는다.
  b. 현재 작업 디렉토리에 guide-draft.md 파일을 만들고 첫 줄에 `독자: <answer 값>` 을 적는다.
  c. colab_message_post 로 정확히 `ANSWERED: <answer 값>` 한 줄을 게시한다(mention 없이).
  d. colab_status_set 으로 status "done". 이것이 턴의 마지막 도구 호출이다.

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라.'

PEER_INS='너는 같은 팀의 검토자다. 답은 한국어로 짧게.

지시를 받으면:
  a. 현재 작업 디렉토리에 review-note.md 를 만들어 두 줄을 쓴다.
  b. colab_message_post 로 정확히 `PEER-DONE` 한 줄을 게시한다(mention 없이).
  c. colab_status_set 으로 status "done". 이것이 턴의 마지막 도구 호출이다.

웹 검색을 하지 마라. 파일 하나를 쓰는 것 말고는 colab_* 도구만 쓴다.'

REJ_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 설명 초안을 쓰는 작성자다. 답은 한국어로 짧게.

첫 턴에 할 일은 아래 세 가지뿐이다. **각각 한 번씩만** 하고, 실패해도 재시도하거나 다른 방법을 찾지 마라.
  a. colab_hitl_approve_request 를 한 번 호출한다. summary 는 "초안을 이대로 확정해도 될까요?".
     이 호출이 성공하면 **b 를 건너뛰고 곧장 c 로 간다.** 오류가 나면 b 로 간다.
  b. (a 가 실패했을 때만) 셸에서 아래 명령을 그대로 한 번 실행한다.
     curl -sS -X POST "$COLAB_SERVER_URL/api/v1/sessions/$COLAB_SESSION_ID/hitl-requests" -H "Authorization: Bearer $COLAB_TASK_TOKEN" -H "Content-Type: application/json" -d '"'"'{"type":"approval","summary":"초안을 이대로 확정해도 될까요?"}'"'"'
     (그 명령의 응답에 "turn_end_required":true 가 들어 있으면 등록된 것이다.)
  c. 메시지를 하나도 게시하지 말고 즉시 턴을 끝낸다.

다음 턴의 프롬프트에는 <hitl_answer> 구간이 있고 approved 와 reason 이 들어 있다.
  approved 가 false 면 colab_message_post 로 정확히 `REJECTED: <reason 값>` 한 줄을 게시하고,
  colab_status_set 으로 status "done" 을 부른 뒤 턴을 끝낸다.

웹 검색을 하지 마라. 파일을 쓰지 말고 저장소를 뒤지지 마라.'

# Hermes 는 MCP 를 존중하지 않는다(harness §10) — **셸에서 래퍼로** 부른다.
# 명령을 백틱 안 명령 위치에 두어야 데몬이 attempt 별 래퍼 절대 경로로 치환한다(§10 v0.8.1).
WRAP_INS='너는 가상의 실내 화분 자동 급수기 제품 Y 의 설명 초안을 쓰는 작성자다. 답은 한국어로 짧게.
플랫폼에는 오직 셸 명령으로만 말할 수 있다.

첫 턴에 할 일은 딱 하나다. 아래 명령을 **적힌 그대로 한 번만** 실행하고, 성공하든 실패하든
**재시도하지 말고 아무것도 조사하지 말고 즉시 턴을 끝낸다.**
  `colab hitl ask --question "설명 초안의 독자를 누구로 잡을까요?" --default "가정용 사용자"`

다음 턴의 프롬프트에 <hitl_answer> 구간이 있으면 그 answer 값을 읽고 다음을 실행한 뒤 턴을 끝낸다.
  `colab message post --body "ANSWERED: <answer 값>"`
  `colab status set --status done`

웹 검색을 하지 마라. 저장소나 다른 디렉토리를 뒤지지 마라.'

step "0. claim 탭 — 서버가 데몬에 주는 턴 프롬프트를 기록한다(디스크에 남지 않는다)"
TAP_PID="$(tap_start "$TAP_PORT" "$TAP")"
ok "tap :$TAP_PORT → $SERVER_URL (pid $TAP_PID)"

step "1. 계정 · 워크스페이스 · 페어링 (capacity=1)"
: > "$DLOG"
signup "$EMAIL" "$PASSWORD" "Director" >/dev/null
WS="$(create_workspace "G6 HITL $STAMP")"
read -r PID_ PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
rm -rf "$WORK"
PAIR_SERVER="http://localhost:$TAP_PORT" daemon_pair_cap "$PTOK" "$CFG" "$WORK" 1
COLAB_DAEMON_CONFIG="$CFG" setsid_run "$DLOG" "$BIN/daemon" run > "$OUT/daemon-48.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready"
RUNTIME="$(psqlq "select id from runtime where workspace_id='$WS' order by created_at desc limit 1")"
chk C0 "데몬 capacity=1 (슬롯 판정의 전제)" 1 "$(jq -r .capacity "$CFG")"
ok "ws=$WS runtime=$RUNTIME daemon pid $(cat "$OUT/daemon-48.pid")"

step "2. arm A1 — Claude Code(mcp) 가 colab_hitl_ask 로 질문한다"
ASKER="$(create_agent_p2 "$WS" Asker writer "$MODEL" "$ASK_INS" '설명 초안을 쓴다')"
PEER="$(create_agent_p2  "$WS" Peer  reviewer "$MODEL" "$PEER_INS" '초안을 검토한다')"
S1="$(create_session_p3 "$WS" "제품 Y 설명 초안 (질문)" "$P3_GOAL" "$ASKER" "$RUNTIME" '{}' "$ASKER" "$PEER")"
T_A1="$(session_initial_task "$S1")"
ok "S1=$S1 asker task=$T_A1"
T0="$(now_ms)"
ST="$(WAIT_S=${HITL_WAIT_S:-420} wait_task "$T_A1" waiting_human failed cancelled completed)"
chk A1 "E7-03 첫 턴이 task 를 waiting_human 으로 끝낸다" waiting_human "$ST"
LANE_A1="$(task_field "$T_A1" lane_id)"
chk A1b "그 lane 도 waiting_human 이다"                  waiting_human "$(lane_field "$LANE_A1" status)"
chk A1c "세션은 active 유지 (E7-18)"                     active "$(psqlq "select status from session where id='$S1'")"
chk A1d "attempt 1 의 프로세스가 남아 있지 않다"          0 "$(procs_of_attempt "$WORK" "$T_A1" 1)"
chk A1e "workdir 디렉토리가 보존된다"                     yes \
  "$( [ -d "$WORK/sessions/$S1/$LANE_A1" ] && echo yes || echo no )"
H1="$(hitl_of_task "$T_A1")"
chk A2 "HITL 요청이 1건 열렸다"                           1 "$(psqlq "select count(*) from hitl_request where task_id='$T_A1'")"
chk A2b "source=agent (에이전트 발행)"                    agent "$(hitl_field "$H1" source)"
chk A2c "type=question"                                   question "$(hitl_field "$H1" type)"
chk A2d "purpose=agent (0012 · 시스템 발행과 갈린다)"     agent "$(hitl_field "$H1" purpose)"
chk A2e "approver_spec=director"                          director "$(hitl_field "$H1" approver_spec)"
chk A2f "proposed_default 가 실려 있다 (FR-5.1)"          "가정용 사용자" "$(hitl_field "$H1" proposed_default)"
chk A2g "task_id 가 채워져 있다 (에이전트 발행)"          "$T_A1" "$(hitl_field "$H1" task_id)"
chk A3 "타임라인에 HITL 카드가 게시됐다 (message.kind=hitl)" 1 \
  "$(psqlq "select count(*) from message where session_id='$S1' and kind='hitl'")"
chk A3b "Director 인박스에 hitl_request 항목"             1 \
  "$(psqlq "select count(*) from inbox_item where session_id='$S1' and type='hitl_request' and ref_id='$H1'")"
chk A3c "그 인박스 항목의 severity 가 action_required"    action_required \
  "$(psqlq "select severity::text from inbox_item where ref_id='$H1' limit 1")"
chk A3d "이 턴에 게시된 일반 메시지 0건 (턴을 바로 끝냈다)" 0 \
  "$(psqlq "select count(*) from message where source_task_id='$T_A1' and kind<>'hitl'")"

# ── (a) "두 도구 표면" 칸 — 정식 경로 프로브 (K-7 · C-4) ────────────────────
# 같은 턴 안에서 에이전트가 `colab_hitl_ask` 를 한 번 불렀다. 그 호출이 HITL 을 등록했는가가
# 이 칸이다. 지금은 CLI 가 openapi 에 없는 경로(`POST /tasks/{T}/hitl`)를 부르므로 404 다.
MCP_TRIED="$(psqlq "select count(*) from task_event where task_id='$T_A1' and attempt=1
                    and payload::text like '%colab_hitl_ask%'")"
MCP_404="$(psqlq "select count(*) from task_event where task_id='$T_A1' and attempt=1
                  and payload::text like '%colab_hitl_ask%' and payload::text like '%404%'")"
psqlq "select left(replace(coalesce(payload->>'summary',''),E'\n','⏎'),300) from task_event
       where task_id='$T_A1' and attempt=1 and payload::text like '%colab_hitl_ask%'" > "$OUT/48-mcp-probe.txt"
chk_ge T0 "에이전트가 MCP 도구 colab_hitl_ask 를 실제로 불렀다" 1 "$MCP_TRIED"
chk T1 "**MCP 도구 표면이 HITL 을 등록한다** (K-7 · C-4 가 막았던 자리 — 404 가 0건이어야 한다)" 0 "$MCP_404"
# T1 이 FAIL 이면 이 HITL 은 우회(attempt 토큰 → openapi createHitlRequest)로 등록된 것이고,
# PASS 면 정식 도구가 등록한 것이다. 어느 쪽이든 뒷단계는 서버가 보기에 같은 상태에서 출발한다.
chk T2 "어느 경로로든 HITL 이 하나 열려 있다 (뒷단계의 전제)" yes \
  "$( [ -n "$H1" ] && echo yes || echo no )"

step "3. E7-18 — waiting_human 이 슬롯을 잡지 않는다 (capacity=1 에서 다른 lane 이 돈다)"
post_message "$S1" "$(mention Peer "$PEER") 검토 메모를 남겨라" >/dev/null
T_PEER=""
for i in $(seq 1 60); do
  T_PEER="$(psqlq "select t.id from task t join agent a on a.id=t.agent_id where t.session_id='$S1' and a.name='Peer' order by t.created_at desc limit 1")"
  [ -n "$T_PEER" ] && break; sleep 2
done
chk A4 "Peer 의 task 가 생겼다" yes "$( [ -n "$T_PEER" ] && echo yes || echo no )"
PST="$(WAIT_S=${PEER_WAIT_S:-420} wait_task "$T_PEER" completed failed cancelled)"
chk A4b "**capacity=1 인데** Peer task 가 끝까지 돌았다 (슬롯 미점유)" completed "$PST"
chk A4c "Peer 가 메시지를 게시했다"                        1 \
  "$(psqlq "select count(*) from message where source_task_id='$T_PEER' and content like 'PEER-DONE%'")"
chk A4d "그 사이 Asker 는 여전히 waiting_human"             waiting_human "$(task_field "$T_A1" status)"
chk A4e "세션은 active 유지"                                active "$(psqlq "select status from session where id='$S1'")"

step "4. 웹 인박스(S8)에서 Director 가 답한다 — 화면 판정은 DOM"
ANSWER="관리사무소 담당자"
ab set viewport 1440 1000 >/dev/null 2>&1 || true
WEB_OK=no
if web_login "$EMAIL" "$PASSWORD"; then WEB_OK=yes; fi
chk W0 "웹 로그인" yes "$WEB_OK"
ab open "$WEB_URL/inbox" >/dev/null 2>&1 || true
abwait '[data-testid="inbox-page"]' 30 || true
shot "p3-48-01-inbox"
ITEMS="$(abcount '[data-testid="inbox-item"]')"
chk W1 "인박스에 항목이 보인다 (S8)"                       yes "$( [ "${ITEMS:-0}" -ge 1 ] && echo yes || echo no )"
INBOX_TXT="$(abget get text '[data-testid="inbox-list"]' | tr '\n' ' ')"
chk W1b "인박스 본문에 에이전트 질문이 실려 있다"          yes \
  "$(grep -q "독자" <<<"$INBOX_TXT" && echo yes || echo no)"
CARD_SEL='[data-testid="inbox-item"][data-type="hitl_request"]'
[ "$(abcount "$CARD_SEL")" -ge 1 ] || CARD_SEL='[data-testid="inbox-item"]'
ab fill "$CARD_SEL [data-testid=\"hitl-answer-input\"]" "$ANSWER" >/dev/null 2>&1 \
  || ab fill '[data-testid="hitl-answer-input"]' "$ANSWER" >/dev/null 2>&1 || true
shot "p3-48-02-inbox-answer"
ab click "$CARD_SEL [data-testid=\"hitl-answer\"]" >/dev/null 2>&1 \
  || ab click '[data-testid="hitl-answer"]' >/dev/null 2>&1 || true
sleep 4
chk W2 "**웹에서 낸 답이 서버에 도달했다** (status=answered)" answered "$(hitl_field "$H1" status)"
chk W2b "저장된 답이 화면에 입력한 값 그대로"               "$ANSWER" "$(hitl_field "$H1" answer)"
chk W2c "결정 기록 1건 (FR-4.2)"                            1 \
  "$(psqlq "select count(*) from decision where session_id='$S1'")"
chk W2d "그 결정의 source 가 hitl"                          hitl \
  "$(psqlq "select coalesce(source::text,'-') from decision where session_id='$S1' order by created_at desc limit 1")"
chk W2e "인박스 항목이 읽음/해소로 닫혔다"                  yes \
  "$(psqlq "select case when count(*)=0 then 'yes' else 'no' end from inbox_item where ref_id='$H1' and read_at is null")"

step "5. E8-01 — 재큐잉 → 새 attempt, resume 우선, 답변이 프롬프트에"
wait_task_attempt "$T_A1" 2 || bad "attempt 2 로 넘어가지 않았다"
chk A5 "같은 task 의 attempt 가 2 다 (재지시가 아니라 재개)" 2 "$(task_field "$T_A1" attempt)"
chk A5b "task 수는 그대로 (새 task 를 만들지 않았다)"         2 "$(task_count "$S1")"
A1ST="$(WAIT_S=${RESUME_WAIT_S:-600} wait_task "$T_A1" completed failed cancelled)"
chk A5c "재개한 턴이 끝났다"                                  completed "$A1ST"
tap_prompt "$TAP" "$T_A1" 2 > "$OUT/48-prompt-a1-attempt2.txt"
chk_has A6  "attempt 2 프롬프트에 <resumed> 구간"             "$OUT/48-prompt-a1-attempt2.txt" "<resumed attempt=2>"
chk_has A6b "그 안에 <hitl_answer> 구간 (PR #124 R1)"          "$OUT/48-prompt-a1-attempt2.txt" "<hitl_answer"
chk_has A6c "질문 원문이 실려 있다"                            "$OUT/48-prompt-a1-attempt2.txt" "question: 설명 초안의 독자를 누구로 잡을까요?"
chk_has A6d "**사람이 낸 답**이 실려 있다"                     "$OUT/48-prompt-a1-attempt2.txt" "answer: $ANSWER"
chk_has A6e "sections=question_answer"                          "$OUT/48-prompt-a1-attempt2.txt" 'sections="question_answer"'
RESUMED_A1="$(psqlq "select coalesce(resumed::text,'-') from task_attempt where task_id='$T_A1' and attempt=2")"
chk A7  "attempt 2 가 **resume** 으로 붙었다 (E8-01 resume 우선)" true "$RESUMED_A1"
chk A7b "에이전트가 그 답을 실제로 읽고 게시했다 (ANSWERED: …)" 1 \
  "$(psqlq "select count(*) from message where source_task_id='$T_A1' and content like 'ANSWERED:%$ANSWER%'")"
chk A7c "재개 attempt 가 같은 workdir 를 썼다"                 yes \
  "$( [ -f "$WORK/sessions/$S1/$LANE_A1/guide-draft.md" ] && echo yes || echo no )"

step "6. arm A2 — 강제 콜드 스타트(transcript 삭제)로 같은 왕복 (E8-02)"
COLDER="$(create_agent_p2 "$WS" Colder writer "$MODEL" "$ASK_INS" '설명 초안을 쓴다')"
S2="$(create_session_p3 "$WS" "제품 Y 설명 초안 (콜드)" "$P3_GOAL" "$COLDER" "$RUNTIME" '{}' "$COLDER")"
T_A2="$(session_initial_task "$S2")"
ST2="$(WAIT_S=${HITL_WAIT_S:-420} wait_task "$T_A2" waiting_human failed cancelled completed)"
chk B1 "arm A2 도 waiting_human 으로 턴을 끝냈다" waiting_human "$ST2"
LANE_A2="$(task_field "$T_A2" lane_id)"
SID2="$(psqlq "select runtime_session_ref->>'session_id' from lane where id='$LANE_A2'")"
WD2="$WORK/sessions/$S2/$LANE_A2"
ENC2="$(printf '%s' "$WD2" | tr '/._' '---')"
TRANSCRIPT="$HOME/.claude/projects/$ENC2/$SID2.jsonl"
chk B1b "lane 에 runtime_session_ref 가 심겼다 (E8-13)" yes "$( [ -n "$SID2" ] && echo yes || echo no )"
if [ -f "$TRANSCRIPT" ]; then rm -f "$TRANSCRIPT"; ok "transcript 제거 → 강제 콜드 스타트 ($SID2)"; else bad "transcript 없음: $TRANSCRIPT"; fi
chk B1c "**유실 유도**: 런타임 transcript 를 지웠다 (E8-02 정식 유도)" no \
  "$( [ -f "$TRANSCRIPT" ] && echo yes || echo no )"
H2="$(hitl_of_task "$T_A2")"
ANSWER2="아파트 관리사무소"
read -r RC2 RB2 <<<"$(respond_hitl "$H2" "$(jq -nc --arg a "$ANSWER2" '{answer:$a}')")"
chk B2 "API 로 답변 (HTTP $RC2)" yes "$( [ "${RC2:0:1}" = 2 ] && echo yes || echo no )"
wait_task_attempt "$T_A2" 2 || bad "arm A2 attempt 2 로 넘어가지 않았다"
A2ST="$(WAIT_S=${RESUME_WAIT_S:-600} wait_task "$T_A2" completed failed cancelled)"
chk B3 "콜드 스타트 attempt 가 끝났다" completed "$A2ST"
RESUMED_A2="$(psqlq "select coalesce(resumed::text,'-') from task_attempt where task_id='$T_A2' and attempt=2")"
chk B3b "그 attempt 는 resume 이 아니다 (콜드 스타트)" false "$RESUMED_A2"
tap_prompt "$TAP" "$T_A2" 2 > "$OUT/48-prompt-a2-attempt2.txt"
chk_has B4  "콜드 스타트 프롬프트에도 <hitl_answer>"      "$OUT/48-prompt-a2-attempt2.txt" "<hitl_answer"
chk_has B4b "사람의 답이 실려 있다"                        "$OUT/48-prompt-a2-attempt2.txt" "answer: $ANSWER2"
chk_has B4c "\"workdir 를 먼저 확인하라\" 지시 (§8.4)"     "$OUT/48-prompt-a2-attempt2.txt" "inspect the current state of the workdir"
chk B5 "**콜드 스타트인데 이어갔다** (ANSWERED: … 게시)"   1 \
  "$(psqlq "select count(*) from message where source_task_id='$T_A2' and content like 'ANSWERED:%$ANSWER2%'")"
chk B5b "같은 workdir 에 파일이 남았다"                    yes "$( [ -f "$WD2/guide-draft.md" ] && echo yes || echo no )"
{ grep -iE 'cold|resume' "$DLOG" | tail -20; } > "$OUT/48-daemon-resume.txt" 2>/dev/null || true

step "7. arm A3 — Hermes(cli_wrapper) 도구 표면 + 왕복 (두 번째 표면)"
# cli_wrapper 경로는 **우회할 수 없다**: 래퍼는 위생화된 env(`env -i`)에서 돌아 에이전트가
# COLAB_TASK_TOKEN 을 볼 수 없다 — 토큰을 나르는 것이 래퍼 파일 자신이기 때문이다(harness §10).
# 그래서 이 arm 은 정식 경로만 본다. K-7·C-4 로 막혀 있던 자리이고, 핫픽스(#133·#134) 뒤에는
# 여기서 왕복까지 이어진다.
WRAPA="$(create_agent_kind "$WS" Wrapper writer hermes "$HERMES_MODEL" "$WRAP_INS" '설명 초안을 쓴다')"
S3="$(create_session_p3 "$WS" "제품 Y 설명 초안 (래퍼)" "$P3_GOAL" "$WRAPA" "$RUNTIME" '{}' "$WRAPA")"
T_A3="$(session_initial_task "$S3")"
ST3="$(WAIT_S=${HITL_WAIT_S:-600} wait_task "$T_A3" waiting_human failed cancelled completed)"
H3="$(hitl_of_task "$T_A3")"
psqlq "select left(replace(coalesce(payload::text,''),E'\n','⏎'),300) from task_event
       where task_id='$T_A3' and attempt=1 and class='tool' order by seq" > "$OUT/48-wrapper-probe.txt"
chk H1  "**cli_wrapper 도구 표면이 HITL 을 등록한다** (K-7 · C-4 가 막았던 자리)" 1 \
  "$(psqlq "select count(*) from hitl_request where task_id='$T_A3'")"
chk H1b "그래서 Hermes 턴은 waiting_human 으로 끝나야 한다"  waiting_human "$ST3"
log "arm A3 최종 상태 = $ST3 · HITL = ${H3:-없음} (프로브 로그: out/48-wrapper-probe.txt)"
if [ -n "$H3" ]; then
  ANSWER3="사내 총무팀"
  read -r RC3 _ <<<"$(respond_hitl "$H3" "$(jq -nc --arg a "$ANSWER3" '{answer:$a}')")"
  chk H2 "답변 (HTTP $RC3)" yes "$( [ "${RC3:0:1}" = 2 ] && echo yes || echo no )"
  wait_task_attempt "$T_A3" 2 || bad "arm A3 attempt 2 로 넘어가지 않았다"
  A3ST="$(WAIT_S=${RESUME_WAIT_S:-600} wait_task "$T_A3" completed failed cancelled)"
  chk H2b "재개한 턴이 끝났다" completed "$A3ST"
  tap_prompt "$TAP" "$T_A3" 2 > "$OUT/48-prompt-a3-attempt2.txt"
  chk_has H2c "프롬프트에 답변" "$OUT/48-prompt-a3-attempt2.txt" "answer: $ANSWER3"
  chk H3 "Hermes 에이전트가 답을 읽고 게시했다" 1 \
    "$(psqlq "select count(*) from message where source_task_id='$T_A3' and content like 'ANSWERED:%'")"
else
  chk H2 "**Hermes(cli_wrapper) HITL 왕복** — 등록이 없어 서지 않는다 (K-7 · C-4)" yes no
fi

step "8. arm A4 — approval **거절** (E7-17): failed 가 아니라 재개한다"
REJA="$(create_agent_p2 "$WS" Rejected writer "$MODEL" "$REJ_INS" '설명 초안을 쓴다')"
S4="$(create_session_p3 "$WS" "제품 Y 설명 초안 (거절)" "$P3_GOAL" "$REJA" "$RUNTIME" '{}' "$REJA")"
T_A4="$(session_initial_task "$S4")"
ST4="$(WAIT_S=${HITL_WAIT_S:-420} wait_task "$T_A4" waiting_human failed cancelled completed)"
chk R1 "approval 요청도 턴을 waiting_human 으로 끝낸다" waiting_human "$ST4"
H4="$(hitl_of_task "$T_A4")"
chk R1b "type=approval"                     approval "$(hitl_field "$H4" type)"
chk R1c "approval 에는 proposed_default 가 없다 (E7-06 · 절대 자동 진행 없음)" - "$(hitl_field "$H4" proposed_default)"
REJ_REASON="출처가 없어 이대로는 못 쓴다"
read -r RC4 _ <<<"$(respond_hitl "$H4" "$(jq -nc --arg r "$REJ_REASON" '{approved:false,reason:$r}')")"
chk R2 "거절이 받아들여진다 (HTTP $RC4)" yes "$( [ "${RC4:0:1}" = 2 ] && echo yes || echo no )"
chk R2b "hitl 이 answered · approved=false"  "answered|false" \
  "$(psqlq "select status::text||'|'||coalesce(approved::text,'-') from hitl_request where id='$H4'")"
wait_task_attempt "$T_A4" 2 || bad "arm A4 attempt 2 로 넘어가지 않았다"
chk R3 "**거절도 재개다** — task 가 failed 가 아니다" no \
  "$( [ "$(task_field "$T_A4" status)" = failed ] && echo yes || echo no )"
A4ST="$(WAIT_S=${RESUME_WAIT_S:-600} wait_task "$T_A4" completed failed cancelled)"
chk R3b "거절 뒤 턴이 정상 종료" completed "$A4ST"
tap_prompt "$TAP" "$T_A4" 2 > "$OUT/48-prompt-a4-attempt2.txt"
chk_has R4  "프롬프트에 approved: false"   "$OUT/48-prompt-a4-attempt2.txt" "approved: false"
chk_has R4b "프롬프트에 거절 사유"          "$OUT/48-prompt-a4-attempt2.txt" "reason: $REJ_REASON"
chk_has R4c "sections=approval_result"      "$OUT/48-prompt-a4-attempt2.txt" 'sections="approval_result"'
chk R5 "에이전트가 거절 사유를 읽고 게시했다" 1 \
  "$(psqlq "select count(*) from message where source_task_id='$T_A4' and content like 'REJECTED:%'")"
chk R6 "거절도 결정 기록 1건" 1 "$(psqlq "select count(*) from decision where session_id='$S4'")"

step "9. 타임라인의 HITL 카드가 웹 S7 에도 정식 카드로 보이는가 (§0-9 DOM)"
ab open "$WEB_URL/sessions/$S1" >/dev/null 2>&1 || true
abwait '[data-testid="hitl-card"]' 30 || true
shot "p3-48-03-session-hitl-card"
HC="$(abcount '[data-testid="hitl-card"]')"
chk W3 "세션 타임라인에 HITL 카드가 렌더된다" yes "$( [ "${HC:-0}" -ge 1 ] && echo yes || echo no )"
HC_SRC="$(abget get attr '[data-testid="hitl-card"]' data-source)"
chk W3b "그 카드의 data-source=agent"        agent "${HC_SRC:-none}"
HC_STAT="$(abget get attr '[data-testid="hitl-card"]' data-status)"
chk W3c "카드 상태가 answered"               answered "${HC_STAT:-none}"

step "결과"
printf '판정: PASS %d · FAIL %d\n' "$pass" "$fail" >&2
hitl_rows "$S1" | column -t -s $'\t' >&2
jq -n --arg ws "$WS" --arg s1 "$S1" --arg s2 "$S2" --arg s3 "${S3:-}" --arg s4 "$S4" \
  --arg t_a1 "$T_A1" --arg t_a2 "$T_A2" --arg t_a3 "${T_A3:-}" --arg t_a4 "$T_A4" \
  --arg h1 "$H1" --arg h2 "$H2" --arg h3 "${H3:-}" --arg h4 "$H4" \
  --arg resumed_a1 "$RESUMED_A1" --arg resumed_a2 "$RESUMED_A2" --arg peer "$PST" \
  --argjson elapsed_s "$(( ($(now_ms)-T0)/1000 ))" --argjson pass "$pass" --argjson fail "$fail" \
  '{workspace:$ws,sessions:{a1:$s1,cold:$s2,hermes:$s3,reject:$s4},
    tasks:{a1:$t_a1,cold:$t_a2,hermes:$t_a3,reject:$t_a4},
    hitl:{a1:$h1,cold:$h2,hermes:$h3,reject:$h4},
    resumed:{a1:$resumed_a1,cold:$resumed_a2},peer_task:$peer,
    elapsed_s:$elapsed_s,pass:$pass,fail:$fail}' | tee "$OUT/48.json"
[ "$fail" = 0 ]
