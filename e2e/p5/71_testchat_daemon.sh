#!/usr/bin/env bash
# e2e/p5/71_testchat_daemon.sh — T-D12 실기 스모크: 테스트 채팅(daemon-protocol v0.8 §4.5)의 **데몬 몫**.
# 70_ 이 데몬을 curl 로 흉내내 서버를 쟀다면, 이 판은 그 반대다 — 실서버(T-S12) + **실기 데몬 바이너리** +
# acpfake(= `hermes` 이름으로 PATH 앞에 놓은 테스트 바이너리, 모델 호출 없음).
#
# 재는 것 (판정 표 out/71-checks.tsv):
#   A. createTestChat → 턴 1: 데몬이 §4.5 번들을 "토큰 없는 attempt" 로 돌린다 —
#      <root>/.colab/testchat/<id> 를 만들고 그 안에서 런타임을 띄웠고(record 파일 위치), session/new 에
#      mcpServers 가 비었고, CLI 래퍼를 만들지 않았고(로그 `colab surface off`), finish.transport=acp,
#      getTestChat 이 agent 턴 본문·토큰·transport 를 돌려준다.
#   B. 턴 2 = resume: 같은 디렉터리에서 session/load(record) · 서버 저장 ref 로 이어짐.
#   C. close → 서버 gc {test_chat_id} → 데몬이 디렉터리를 지우고 §6 영수증 → 명령 소비 · workdir 행 0.
#   D. 같은 스택에서 세션 task 1회: 회귀 없음(claim kind=task · finish completed · §6 lane 보고 · colab 표면 켜짐).
#
# 스택(T-D12 배정, §0-13): server :8108 · pg :5452 · 컨테이너 colab-pg-d12 · out/ 는 e2e/p5/out/d12.
# 사용:
#   bash e2e/p5/71_testchat_daemon.sh          # 스택이 없으면 up.sh 를 같은 포트로 띄운다
#   E2E_OUT=e2e/p5/out/d12 SERVER_URL=http://localhost:8108 bash e2e/p5/down.sh
export SERVER_URL="${SERVER_URL:-http://localhost:8108}"
export WEB_URL="${WEB_URL:-http://localhost:3020}"
export PG_PORT="${PG_PORT:-5452}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-d12}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$HERE/out/d12}"
source "$HERE/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/71-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/71-cookies.txt"; rm -f "$COOKIE"
API="$SERVER_URL/api/v1"
DLOG="$OUT/71-daemon.log"; : > "$DLOG"
# 작업 폴더는 **이 저장소 밖**(P4_TASKS §0-18): 저장소 안에 두면 lane 폴더의 §6 git 측정이 이 저장소의
# 브랜치·커밋 수를 읽어 표가 오염된다(실측: commits_ahead=529).
WORK="${COLAB_D12_WORK:-/private/tmp/colab-d12}/$RUN"; mkdir -p "$WORK/bin"
ROOT="$WORK/root"; mkdir -p "$ROOT"
CFG="$WORK/daemon.json"

cleanup() {
  if [ -f "$OUT/daemon-71.pid" ]; then
    pid="$(cat "$OUT/daemon-71.pid")"; kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
    rm -f "$OUT/daemon-71.pid"
  fi
}
trap cleanup EXIT

curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 || bash "$HERE/up.sh" || die "stack did not come up"

wait_for() { # wait_for <seconds> <shell test>
  local n=0; while [ $n -lt $(( $1 * 10 )) ]; do if eval "$2"; then return 0; fi; sleep 0.1; n=$((n+1)); done; return 1
}
record_count() { # record_count FILE METHOD → 그 method 의 요청 수
  [ -f "$1" ] || { echo 0; return; }
  python3 - "$1" "$2" <<'PY'
import json, sys
n = 0
for l in open(sys.argv[1]):
    l = l.strip()
    if not l: continue
    r = json.loads(l)
    if r.get("method") == sys.argv[2]: n += 1
print(n)
PY
}
record_prompt_has() { # record_prompt_has FILE TEXT → session/prompt 본문에 TEXT 가 있는 요청 수
  python3 - "$1" "$2" <<'PY'
import json, sys
n = 0
for l in open(sys.argv[1]):
    l = l.strip()
    if not l: continue
    r = json.loads(l)
    if r.get("method") == "session/prompt":
        text = "".join(p.get("text", "") for p in (r.get("params") or {}).get("prompt") or [])
        if sys.argv[2] in text: n += 1
print(n)
PY
}
record_mcp() { # record_mcp FILE → session/new·load 의 mcpServers 길이들 (공백 구분)
  python3 - "$1" <<'PY'
import json, sys
out = []
for l in open(sys.argv[1]):
    l = l.strip()
    if not l: continue
    r = json.loads(l)
    if r.get("method") in ("session/new", "session/load"):
        out.append(str(len((r.get("params") or {}).get("mcpServers") or [])))
print(" ".join(out))
PY
}

# ───────────────────────────── 0 ─────────────────────────────────────────────
step "0. 바이너리 · 계정 · hermes(=acpfake) 에이전트 · 실기 페어링 · 데몬 run"
mkdir -p "$BIN"
(cd "$E2E_ROOT/daemon" && go build -o "$BIN/daemon" ./cmd/daemon) || die "daemon build"
# acpfake 는 TestMain 에서 ACPFAKE=1 이면 스스로 ACP 서버가 된다(T-D10 레시피). `hermes --version` 은
# 플래그 오류로 끝나지만 probe 는 "설치됨·버전 미상"으로 읽어 hermes 능력을 광고한다.
(cd "$E2E_ROOT/daemon" && go test -c -o "$WORK/bin/hermes" ./internal/loop) || die "acpfake build"
export PATH="$WORK/bin:$PATH"

signup "d12-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "D12 $RUN")"
SCRIPT="$(jq -nc '{kind:"hermes",no_mcp_capabilities:true,known_sessions:["sess-1"],
  turns:[{steps:[{chunk:"나는 Guide 에이전트입니다."}],usage:{inputTokens:1200,outputTokens:300}}]}')"
# ACPFAKE_RECORD 는 **상대 경로**: 페이크가 자기 CWD 에 남기므로 그 파일이 생긴 자리가 곧 런타임의 실제 CWD 증거.
AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg s "$SCRIPT" '{name:"Guide",role:"researcher",role_description:"제품 사용법을 설명한다",
  instructions:"짧게, 한국어로 답한다.",budget_per_task:0.5,
  profiles:[{name:"default",runtime_kind:"hermes",model:"sonnet",is_default:true,
             env:{ACPFAKE:"1",ACPFAKE_SCRIPT:$s,ACPFAKE_RECORD:"acpfake-record.jsonl"}}]}')" | jq -r .id)"
IFS=$'\t' read -r PID_ PCODE <<<"$(create_pairing "$WS")"
daemon_pair "$PCODE" "$CFG" "$ROOT" --no-turn >/dev/null
RID="$(jq -r .runtime_id "$CFG")"
chk 0.1 online "$(psqlq "select status from runtime where id='$RID'")" "실기 pair + probe 뒤 컴퓨터 online"
chk 0.2 1 "$(psqlq "select count(*) from runtime where id='$RID' and capabilities @> '[{\"kind\":\"hermes\"}]'")" "probe 가 hermes(=acpfake) 능력을 광고"
chk 0.3 "$ROOT" "$(psqlq "select workdir_root from runtime where id='$RID'")" "workdir_root = 데몬 root (§4.5 경로 재료)"
# 24h 방어(§4.5 (g)): 시작 전에 낡은 테스트 채팅 폴더 하나를 심어 둔다.
STALE="$ROOT/.colab/testchat/stale-chat"; mkdir -p "$STALE"; echo x > "$STALE/f"
touch -t "$(date -v-25H +%Y%m%d%H%M 2>/dev/null || date -d '25 hours ago' +%Y%m%d%H%M)" "$STALE" "$STALE/f"
daemon_start "$CFG" "$DLOG" > "$OUT/daemon-71.pid"
wait_for 30 'grep -q "colab-daemon" "$DLOG"' || die "daemon did not start (see $DLOG)"
sleep 1
chk 0.4 no "$([ -d "$STALE" ] && echo yes || echo no)" "시작 시 24h 넘은 .colab/testchat/* 삭제 (§4.5 (g))"
chk 0.5 1 "$(grep -c "testchat sweep: removed $STALE" "$DLOG" || true)" "삭제를 로그에 남김"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. createTestChat → 턴 1 (실기 데몬이 §4.5 번들을 돌린다)"
CHAT_JSON="$(api_ok POST "/agents/$AG/test-chats" '{}')"
CHAT="$(jq -r .id <<<"$CHAT_JSON")"
chk A.1 "open/$RID" "$(jq -r '.status+"/"+.runtime_id' <<<"$CHAT_JSON")" "채팅 열림 · hermes 가 온라인인 이 컴퓨터로 고정"
DIR="$ROOT/.colab/testchat/$CHAT"
api_ok POST "/test-chats/$CHAT/turns" '{"content":"안녕, 너는 누구니?"}' -H "Idempotency-Key: $(uuid)" >/dev/null
wait_for 90 '[ "$(psqlq "select turn_status from test_chat where id='"'"'$CHAT'"'"'")" = idle ] && [ "$(psqlq "select turn_no from test_chat where id='"'"'$CHAT'"'"'")" = 1 ]' \
  || bad "turn 1 did not finish (see $DLOG)"
G="$(api_ok GET "/test-chats/$CHAT")"; echo "$G" | jq . > "$OUT/71-testchat-1.json"
chk A.2 "user/agent" "$(jq -r '[.turns[].role]|join("/")' <<<"$G")" "턴 [user, agent]"
chk A.3 "나는 Guide 에이전트입니다." "$(jq -r '.turns[1].content' <<<"$G")" "agent 턴 본문 = 페이크의 message.say"
chk A.4 "acp/1200/300" "$(jq -r '.transport+"/"+(.input_tokens|tostring)+"/"+(.output_tokens|tostring)' <<<"$G")" "finish.transport=acp · usage 토큰 (§4.5 (d))"
chk A.5 yes "$([ -d "$DIR" ] && echo yes || echo no)" "<root>/.colab/testchat/<id> 가 생겼다 (§4.5 (b))"
chk A.6 yes "$([ -s "$DIR/acpfake-record.jsonl" ] && echo yes || echo no)" "런타임의 실제 CWD = 그 디렉터리 (record 파일 위치)"
# 브리프 파일은 finish 때 지워지므로(harness §10 "lane 종료 시 파일 삭제") 프롬프트 첫 줄의 포인터로 잰다.
chk A.7 1 "$(record_prompt_has "$DIR/acpfake-record.jsonl" "$DIR/COLAB_BRIEF.md")" "hermes 브리프 포인터가 그 디렉터리의 COLAB_BRIEF.md 를 가리킴 (harness §10)"
chk A.8 1 "$(record_count "$DIR/acpfake-record.jsonl" session/new)" "턴 1 = session/new"
chk A.9 0 "$(record_mcp "$DIR/acpfake-record.jsonl" | awk '{print $1}')" "session/new 의 mcpServers 0개 (§4.5 (a))"
chk A.10 no "$([ -e "$ROOT/.colab/bin/$CHAT.1" ] && echo yes || echo no)" "CLI 래퍼 디렉터리 없음 (harness §2.1 v0.8.8)"
chk A.11 1 "$(grep -c "$CHAT.1 colab surface off: no task_token (kind=test_chat)" "$DLOG" || true)" "데몬 로그: colab 표면 끔"
chk A.12 1 "$(grep -c "$CHAT.1 claim kind=test_chat" "$DLOG" || true)" "데몬 로그: claim kind=test_chat"
chk A.13 1 "$(grep -c "$CHAT.1 stall watch armed limit=3m0s counts=session/update,request_permission,raw:_claude/sdkMessage(off)" "$DLOG" || true)" "stall 워처가 세는 것을 로그에 (hermes 는 원시 스트림 없음)"
chk A.14 0 "$(psqlq "select count(*) from workdir where path_or_ref='$DIR'")" "테스트 채팅 폴더는 workdir 행이 아니다 (§4.5 (e))"
chk A.15 0 "$(psqlq "select count(*) from task_event where task_id='$CHAT'")" "task_event 저장 0 (§4.5 보고)"

# ───────────────────────────── B ─────────────────────────────────────────────
step "B. 턴 2 = resume (같은 디렉터리 · session/load)"
api_ok POST "/test-chats/$CHAT/turns" '{"content":"한 문장으로 다시"}' -H "Idempotency-Key: $(uuid)" >/dev/null
wait_for 90 '[ "$(psqlq "select turn_status from test_chat where id='"'"'$CHAT'"'"'")" = idle ] && [ "$(psqlq "select turn_no from test_chat where id='"'"'$CHAT'"'"'")" = 2 ]' \
  || bad "turn 2 did not finish (see $DLOG)"
G="$(api_ok GET "/test-chats/$CHAT")"; echo "$G" | jq . > "$OUT/71-testchat-2.json"
chk B.1 "user/agent/user/agent" "$(jq -r '[.turns[].role]|join("/")' <<<"$G")" "턴 4개"
chk B.2 1 "$(record_count "$DIR/acpfake-record.jsonl" session/load)" "턴 2 = session/load (record, §4.5 (c))"
chk B.3 "0 0" "$(record_mcp "$DIR/acpfake-record.jsonl")" "new·load 둘 다 mcpServers 0개"
chk B.4 "sess-1" "$(psqlq "select runtime_session_ref->>'session_id' from test_chat where id='$CHAT'")" "서버가 저장한 ref = 페이크 세션"
chk B.5 1 "$(grep -c "$CHAT.2 claim kind=test_chat" "$DLOG" || true)" "데몬 로그: 턴 2 claim (attempt=턴 번호)"
chk B.6 "acp/2400/600" "$(jq -r '.transport+"/"+(.input_tokens|tostring)+"/"+(.output_tokens|tostring)' <<<"$G")" "토큰 누적 · transport 유지"
cp "$DIR/acpfake-record.jsonl" "$OUT/71-acpfake-record.jsonl" 2>/dev/null || true

# ───────────────────────────── C ─────────────────────────────────────────────
step "C. close → gc {test_chat_id} → 데몬 rm -rf + §6 영수증 → 명령 소비"
api_ok POST "/test-chats/$CHAT/close" '' >/dev/null
wait_for 60 '[ ! -d "$DIR" ]' || bad "directory still there after close (see $DLOG)"
chk C.1 no "$([ -d "$DIR" ] && echo yes || echo no)" "디렉터리 삭제 (§4.5 (f))"
chk C.2 1 "$(grep -c "gc testchat=$CHAT $DIR: deleted" "$DLOG" || true)" "데몬 로그: gc deleted"
wait_for 30 '[ "$(psqlq "select count(*) from daemon_command where runtime_id='"'"'$RID'"'"' and type='"'"'gc'"'"' and payload->>'"'"'test_chat_id'"'"'='"'"'$CHAT'"'"' and consumed_at is null")" = 0 ]' || true
chk C.3 0 "$(psqlq "select count(*) from daemon_command where runtime_id='$RID' and type='gc' and payload->>'test_chat_id'='$CHAT' and consumed_at is null")" "§6 영수증으로 gc 명령 소비"
chk C.4 0 "$(psqlq "select count(*) from workdir where path_or_ref like '%/.colab/testchat/%'")" "workdir 테이블에 test_chat 행 없음"
chk C.5 closed "$(api_ok GET "/test-chats/$CHAT" | jq -r .status)" "채팅 closed"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 같은 스택·같은 데몬에서 세션 task 1회 — 회귀 없음"
SID="$(create_session "$WS" "$AG" "D12 회귀 $RUN" "Reply with one short sentence. Do not look around the repository or any other directory; if a tool fails, do not retry or look for another way." "$RID")"
TASK="$(psqlq "select id from task where session_id='$SID' order by created_at limit 1")"
wait_for 120 'grep -q "$TASK.1 finish outcome=" "$DLOG"' || bad "session task did not finish (see $DLOG)"
chk D.1 1 "$(grep -c "$TASK.1 claim kind=task lane=" "$DLOG" || true)" "claim kind=task"
chk D.2 1 "$(grep -c "$TASK.1 finish outcome=completed" "$DLOG" || true)" "finish completed"
chk D.3 0 "$(grep -c "$TASK.1 colab surface off" "$DLOG" || true)" "세션 task 는 colab 표면 켜짐 (로그 없음)"
chk D.4 1 "$(grep -c "$TASK.1 workdir report kind=dir" "$DLOG" || true)" "§6 lane 종료 보고 (D-23 그대로)"
LANE_DIR="$(psqlq "select path_or_ref from workdir where session_id='$SID' limit 1")"
chk D.5 yes "$([ -n "$LANE_DIR" ] && echo yes || echo no)" "세션 workdir 행이 서버에 있다 (path=${LANE_DIR:-없음})"
chk D.6 yes "$([ -s "$LANE_DIR/acpfake-record.jsonl" ] && echo yes || echo no)" "세션 런타임의 CWD = lane 폴더"
chk D.7 1 "$(record_mcp "$LANE_DIR/acpfake-record.jsonl" | awk '{print NF}')" "세션 task 도 session/new 1회"
chk D.8 1 "$(grep -c "$TASK.1 stall watch armed" "$DLOG" || true)" "세션 task 도 stall 워처 로그 1줄"
chk D.9 0 "$(ls "$ROOT/.colab/testchat" 2>/dev/null | wc -l | tr -d ' ')" "gc 뒤 .colab/testchat 이 비었다"

step "요약"
printf '%s\n' "PASS $(( $(wc -l < "$CHECKS") - FAILS )) / FAIL $FAILS — $CHECKS"
cp "$DLOG" "$OUT/71-daemon.final.log" 2>/dev/null || true
[ "$FAILS" -eq 0 ]
