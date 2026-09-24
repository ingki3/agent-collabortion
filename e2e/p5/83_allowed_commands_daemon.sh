#!/usr/bin/env bash
# e2e/p5/83_allowed_commands_daemon.sh — T-D13 실기 스모크: 번들 `task.allowed_commands`(K-19) 의 **데몬 몫**
#   (harness §10 v0.8.10 · daemon-protocol §4.1 v0.8.2). 실기 claude_code 1턴, reviewer 역할.
#
# 비용 한 줄(I-3): 실기 고정 — claude_code haiku 1턴 ≈ $0.01 · ≈ 60s(빌드 포함) (CI 는 안 돈다)
#
# 재는 것 (판정 표 out/d13/83-checks.tsv):
#   D.1 데몬 로그 `allowed commands: <reviewer 10개> (denied: lane_delegate,artifact_submit,hitl_approve_request)`
#       — 서버(T-S19)가 번들에 실은 값을 데몬이 읽었다.
#   D.2 어댑터가 실제로 띄운 colab MCP 서버의 argv 에 `mcp serve --allow <같은 목록>` — colab_bin 자리에 둔
#       탭(tap) 스크립트가 "$@" 를 기록한 뒤 진짜 colab 으로 exec 한다(p1 lib colab_tap 레시피).
#   D.3 턴 뒤 데몬 로그 `colab tools registered: …` — raw system/init 의 콜랩 툴 목록(어댑터가 등록한 것).
#   D.4 그 목록에 `colab_lane_delegate` 가 **없다** — CLI 가 `--allow` 를 구현했을 때만(T-C7). 이 스크립트가
#       빌드한 CLI 가 아직 플래그를 모르면(`colab mcp serve --allow x --list` 가 없다) 판정 대신 관측만 적는다:
#       데몬 몫은 argv 까지(D.2)이고 툴 등록 필터는 CLI 몫이다.
#   D.5 에이전트가 게시한 메시지에 브리프 [2] 의 "이 역할은 위임 · 아티팩트 제출 · 완료 승인 요청 · 미션 제안을 쓰지 않는다."
#       가 글자 그대로 있다 — 실기 런타임이 받은 _meta.systemPrompt 를 에이전트 입으로 확인(데몬은 브리프를
#       로그에 남기지 않는다). 세션 goal 이 그 줄을 그대로 인용해 게시하라고 시킨다.
#   D.6 task 가 completed.
#
# 스택(T-D13 배정): server :8118 · pg :5462 · 컨테이너 colab-pg-d13. 기본 PG_PORT 는 5472 — T-D13 작성
# 시점(2026-09-16)에 :5462 를 **PR #250 리뷰어 자신의 잔여물(colab-pg-review182)** 이 점유하고 있어 우회했다
# (#250 리뷰 NN1: "다른 워커의" 가 아니었다 — 리뷰 중 정리됨). 컨테이너 colab-pg-d13 이 :5472 로 이미 있어
# 기본값은 그대로 둔다; 배정 포트로 돌리려면 PG_PORT=5462 로 새 컨테이너를 띄운다.
# 사용: bash e2e/p5/83_allowed_commands_daemon.sh          # 스택이 없으면 up.sh 를 같은 포트로 띄운다
#       SERVER_URL=http://localhost:8118 PG_PORT=5472 PG_CONTAINER=colab-pg-d13 bash e2e/p5/down.sh
# 비용: haiku 1턴(≈ 수 센트). 로그인된 claude_code 가 필요하다(RUNTIME=real 고정 — 페이크는 유닛이 잰다).
export SERVER_URL="${SERVER_URL:-http://localhost:8118}"
export WEB_URL="${WEB_URL:-http://localhost:3023}"
export PG_PORT="${PG_PORT:-5472}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-d13}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$HERE/out/d13}"
source "$HERE/lib.sh"
RUN="$(date +%H%M%S)-$RANDOM"
CHECKS="$OUT/83-checks.tsv"; : > "$CHECKS"
COOKIE="$OUT/83-cookies.txt"; rm -f "$COOKIE"
API="$SERVER_URL/api/v1"
DLOG="$OUT/83-daemon.log"; : > "$DLOG"
ARGV="$OUT/83-colab-argv.log"; : > "$ARGV"
MODEL="${LEAD_MODEL:-claude-haiku-4-5-20251001}"
# 작업 폴더는 이 저장소 밖(P4_TASKS §0-18).
WORK="${COLAB_D13_WORK:-/private/tmp/colab-d13}/$RUN"; mkdir -p "$WORK"
ROOT="$WORK/root"; mkdir -p "$ROOT"
CFG="$WORK/daemon.json"

cleanup() { daemon_stop "$OUT/daemon-83.pid"; return 0; }
trap cleanup EXIT

curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 || bash "$HERE/up.sh" || die "stack did not come up"
wait_for() { local n=0; while [ $n -lt $(( $1 * 10 )) ]; do if eval "$2"; then return 0; fi; sleep 0.1; n=$((n+1)); done; return 1; }
# in_list NEEDLE CSV → yes|no · has_str HAYSTACK NEEDLE → yes|no  (bash 3.2: `$( case … )` 안의 `)` 가 깨진다)
in_list() { case ",$2," in *",$1,"*) echo yes;; *) echo no;; esac; }
has_str() { case "$1" in *"$2"*) echo yes;; *) echo no;; esac; }

# ───────────────────────────── 0 ─────────────────────────────────────────────
step "0. 바이너리(daemon · colab @ HEAD $(git rev-parse --short HEAD)) · colab 탭 · 계정 · 실기 페어링 · 데몬 run"
mkdir -p "$BIN"
(cd "$E2E_ROOT/daemon" && go build -o "$BIN/daemon" ./cmd/daemon) || die "daemon build"
(cd "$E2E_ROOT/cli" && go build -o "$BIN/colab" ./cmd/colab) || die "colab build"
# CLI 가 --allow 를 아는가(T-C7). 모르면 D.4 는 관측만.
CLI_ALLOW=no
if "$BIN/colab" mcp serve --allow session_get --list </dev/null 2>/dev/null | grep -q colab_session_get; then CLI_ALLOW=yes; fi
ok "CLI --allow: $CLI_ALLOW"
TAP="$OUT/83-colab-tap.sh"
cat > "$TAP" <<EOS
#!/usr/bin/env bash
# 테스트 픽스처 — 어댑터가 띄우는 colab MCP 서버의 argv 를 기록한 뒤 진짜 colab 으로 exec
printf '%s\t%s\n' "\$(date +%s)" "\$*" >> "$ARGV"
exec "$BIN/colab" "\$@"
EOS
chmod +x "$TAP"

signup "d13-$RUN@example.com" password123 "Dir" >/dev/null
WS="$(create_workspace "D13 $RUN")"
IFS=$'\t' read -r PID_ PCODE <<<"$(create_pairing "$WS")"
rm -f "$CFG"
COLAB_DAEMON_CONFIG="$CFG" "$BIN/daemon" pair "$PCODE" --server "$SERVER_URL" --workdir-root "$ROOT" 2>&1 | tail -2 >&2
jq --arg b "$TAP" '.capacity=1 | .colab_bin=$b' "$CFG" > "$CFG.tmp" && mv "$CFG.tmp" "$CFG"
RID="$(jq -r .runtime_id "$CFG")"
( cd "$WORK" && export PATH="$BIN:$(stable_path)" COLAB_DAEMON_CONFIG="$CFG"; setsid_run "$DLOG" "$BIN/daemon" run ) > "$OUT/daemon-83.pid"
wait_pairing "$WS" "$PID_" 300 || die "pairing not ready (see $DLOG)"
KINDS="$(api_ok GET "/runtimes/$RID" | jq -r '[.capabilities[]?.kind]|join(" ")')"
chk 0.1 yes "$(in_list claude_code "$(tr ' ' , <<<"$KINDS")")" "컴퓨터가 claude_code 를 광고 (kinds=$KINDS)"

# ───────────────────────────── A ─────────────────────────────────────────────
step "A. reviewer 에이전트(claude_code $MODEL) · 세션(assignee=Rev) → 실기 1턴"
INS="You are a reviewer. When the session goal asks you to quote lines from your brief, copy them character for character. $P4_RULES"
AG="$(api_ok POST "/workspaces/$WS/agents" "$(jq -nc --arg m "$MODEL" --arg i "$INS" '{name:"Rev",role:"reviewer",role_description:"산출물을 검토한다",
  instructions:$i,profiles:[{name:"default",runtime_kind:"claude_code",model:$m,is_default:true}]}')" | jq -r .id)"
chk A.1 "lane_delegate,artifact_submit,hitl_approve_request,work_propose" \
  "$(api_ok GET "/agents/$AG" | jq -r '.allowed_commands as $a | ["session_get","session_messages","message_post","status_set","decision_record","lane_delegate","artifact_submit","artifact_get","review_approve","review_reject","hitl_ask","hitl_approve_request","hitl_request_info","room_list","room_read","work_propose"] - $a | join(",")')" \
  "서버 Agent.allowed_commands — reviewer 가 못 쓰는 4개(colab-cli §2.5 v0.8)"
GOAL="Your system prompt (the brief) has a section [2] Workspace rules and colab CLI. Post ONE message whose body is exactly the line of that section that starts with \"- 이 역할은\" — copy it character for character, nothing else. Then end your turn. $P4_RULES"
SID="$(api_ok POST "/workspaces/$WS/sessions" "$(jq -nc --arg g "$GOAL" --arg a "$AG" --arg rt "$RID" \
  '{title:"D13 allowed commands",goal:$g,isolation:{kind:"none"},participants:[{agent_id:$a}],assignee_agent_id:$a,runtime_id:$rt,
    completion_condition:{op:"and",conditions:[{type:"manual"}]}}')" | jq -r .id)"
ok "session $SID"
TASK="$(psqlq "select id from task where session_id='$SID' order by created_at limit 1")"
[ -n "$TASK" ] || die "no task queued for the session"
wait_for "${T_TURN:-600}" '[ -n "$(psqlq "select outcome from task_attempt where task_id='"'"'$TASK'"'"' and attempt=1 and outcome is not null")" ]' \
  || bad "turn did not finish in time (see $DLOG)"
OUTCOME="$(psqlq "select coalesce(outcome,'-') from task_attempt where task_id='$TASK' and attempt=1")"
chk D.6 completed "$OUTCOME" "task attempt 1 outcome"

# ───────────────────────────── D ─────────────────────────────────────────────
step "D. 데몬 로그 · MCP argv · 툴 목록 · 에이전트가 인용한 브리프 줄"
REVIEWER="session_get,session_messages,artifact_get,message_post,status_set,decision_record,review_approve,review_reject,hitl_ask,hitl_request_info,room_list,room_read"
chk D.1 1 "$(grep -c "allowed commands: $REVIEWER (denied: lane_delegate,artifact_submit,hitl_approve_request,work_propose)" "$DLOG" || true)" "데몬이 번들 allowed_commands 를 읽음(로그)"
chk D.2 1 "$(grep -c "^[0-9]*	mcp serve --allow $REVIEWER\$" "$ARGV" || true)" "colab MCP 서버 argv = mcp serve --allow <reviewer 12개> (탭 기록)"
TOOLS="$(grep -o "colab tools registered: .*" "$DLOG" | tail -1 | sed 's/colab tools registered: //')"
chk D.3 yes "$([ -n "$TOOLS" ] && echo yes || echo no)" "raw system/init 의 콜랩 툴 목록을 로그에 남김: $TOOLS"
HAS_DELEGATE="$(in_list colab_lane_delegate "$TOOLS")"
if [ "$CLI_ALLOW" = yes ]; then
  chk D.4 no "$HAS_DELEGATE" "툴 목록에 colab_lane_delegate 없음 (CLI --allow 구현됨)"
else
  printf 'D.4\tNOTE\t-\t%s\t%s\n' "$HAS_DELEGATE" "colab_lane_delegate 가 툴 목록에 있는가 — CLI 가 --allow 를 아직 모른다(T-C7 전), 데몬은 argv 까지(D.2)" >> "$CHECKS"
  ok "D.4  (관측) colab_lane_delegate in tools: $HAS_DELEGATE — CLI --allow 는 T-C7"
fi
MSG="$(psqlq "select content from message where session_id='$SID' and author_type='agent' order by created_at limit 1")"
printf '%s\n' "$MSG" > "$OUT/83-agent-message.txt"
chk D.5 yes "$(has_str "$MSG" "이 역할은 위임 · 아티팩트 제출 · 완료 승인 요청 · 미션 제안을 쓰지 않는다.")" "에이전트가 인용한 [2] 의 줄 = 데몬이 쓴 문장 (msg: $(printf '%s' "$MSG" | head -c 120))"

cp "$DLOG" "$OUT/83-daemon.final.log" 2>/dev/null || true
step "결과: $CHECKS"
cat "$CHECKS" >&2
[ "$FAILS" = 0 ] && ok "83 all pass" || die "83: $FAILS fail"
