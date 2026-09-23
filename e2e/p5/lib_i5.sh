#!/usr/bin/env bash
# e2e/p5/lib.sh — P5 통합 E2E 공통 헬퍼 (T-I5, G9 판정 자료).
#
# `e2e/p4/lib.sh`(→ p3 → p2 → p1) 를 그대로 재사용하고 **포트·컨테이너·workdir 만 분리**한다
# (P3_TASKS §0-13). 스택: server :8109 · Postgres :5453(colab-pg-i5) · web :3021.
#
# 이 판이 P1~P4 와 다른 점 하나 — **런타임이 페이크다**(기본 `RUNTIME=fake`).
#   G9 조건은 "시나리오 A·B·C·D E2E 가 **CI 에서** 초록" 인데 CI 에는 모델도 로그인도 없다. 그래서
#   데몬 모듈의 acpfake(`daemon/acpfake`, 대본 응답 ACP 에이전트)를 `bin/acpfake` 로 빌드해
#   `hermes`·`npx`(claude_code 어댑터 자리) 이름으로 PATH 앞에 두면, 데몬은 **자기 코드 그대로**
#   런타임을 spawn 하고 프로토콜을 다 탄다(T-D10 실기 스모크 58_ 의 레시피). 모델 답은 대본이다:
#   `fixtures/agent.sh <role>` 이 acpfake 의 `exec` 스텝에서 턴 프롬프트(`ACPFAKE_PROMPT`)를 읽고
#   진짜 `colab` CLI 로 위임·게시·아티팩트·HITL 을 한다 — 서버·데몬·CLI 는 전부 실물이다.
#   `RUNTIME=real` 이면 P2~P4 처럼 실기(claude_code·hermes 로그인)로 같은 스크립트가 돈다.
#
# CI 에서는 docker 가 없다 — Postgres 는 service 컨테이너다. `PSQL_URL` 을 export 하면 `psqlq` 가
# `docker exec` 대신 `psql` 클라이언트로 간다(아래).
export SERVER_URL="${SERVER_URL:-http://localhost:8109}"
export WEB_URL="${WEB_URL:-http://localhost:3021}"
export PG_PORT="${PG_PORT:-5453}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-i5}"
P5_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P5_DIR/out}"
mkdir -p "$E2E_OUT"
source "$P5_DIR/../p4/lib.sh"

RUNTIME="${RUNTIME:-fake}"
export PG_URL="${PSQL_URL:-postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable}"
# CI: `psql` 클라이언트 (ubuntu-latest 에 있다). 로컬: docker exec (p1 lib).
if [ -n "${PSQL_URL:-}" ]; then
  psqlq() { psql "$PSQL_URL" -qtA -F $'\t' -v ON_ERROR_STOP=1 -c "$1"; }
fi

# 임시 루트: macOS 는 /private/tmp, Linux(CI) 는 /tmp.
if [ -d /private/tmp ]; then _p5_tmp=/private/tmp; else _p5_tmp="${TMPDIR:-/tmp}"; fi
P5_TMP_ROOT="${P5_TMP_ROOT:-$_p5_tmp/colab-p5-i5}"
P4_TMP_ROOT="$P5_TMP_ROOT"     # p4 lib 의 make_repo·daemon_run 이 이 아래를 쓴다
FIX="$P5_DIR/fixtures"

# ── 페이크 런타임 배선 ──────────────────────────────────────────────────────
# fake_runtime_setup → FAKEBIN·FAKEHOME 을 만든다. 데몬에 넘기는 PATH 앞에 FAKEBIN 을 두면
#   claude_code: probe 가 `claude --version` + `npx` 를 찾는다 → 둘 다 여기 있다. 어댑터 spawn 은
#                `npx -y @agentclientprotocol/claude-agent-acp@<pin>` → 래퍼가 인자를 버리고 acpfake 로.
#   hermes:      `hermes --version` · `hermes acp` → acpfake.
# 로그인 판정(probe.claudeLoggedIn/hermesConfigured)은 HOME 의 파일 유무라 페이크 HOME 을 준다.
# 페이크의 기본 대본(프로파일 env 에 ACPFAKE_SCRIPT 가 없을 때 = probe PONG 턴)은 모델 목록만 광고한다.
FAKE_MODELS='["claude-haiku-4-5-20251001","claude-sonnet-4-5"]'
fake_runtime_setup() {
  FAKEBIN="$E2E_OUT/fakebin"; FAKEHOME="$E2E_OUT/fakehome"
  mkdir -p "$FAKEBIN" "$FAKEHOME/.claude" "$FAKEHOME/.hermes" "$FAKEHOME/.local/state"
  : > "$FAKEHOME/.claude/.credentials.json"
  cat > "$FAKEBIN/claude" <<'SH'
#!/bin/sh
# 테스트 픽스처 — probe 의 `claude --version` 자리
echo "2.0.0 (Claude Code fake)"
SH
  # 기본 대본은 파일로 둔다 — 셸 문자열 안의 JSON 따옴표 이스케이프를 피한다.
  printf '{"kind":"claude","models":%s}\n' "$FAKE_MODELS" > "$FAKEBIN/default-claude.json"
  printf '{"kind":"hermes","no_mcp_capabilities":true,"models":%s}\n' "$FAKE_MODELS" > "$FAKEBIN/default-hermes.json"
  cat > "$FAKEBIN/npx" <<SH
#!/bin/sh
# 테스트 픽스처 — claude_code 어댑터(npx -y @…/claude-agent-acp@pin) 자리. 인자는 버린다.
[ -n "\${ACPFAKE_SCRIPT:-}" ] || ACPFAKE_SCRIPT="\$(cat "$FAKEBIN/default-claude.json")"
export ACPFAKE_SCRIPT
exec "$BIN/acpfake"
SH
  cat > "$FAKEBIN/hermes" <<SH
#!/bin/sh
# 테스트 픽스처 — hermes 자리 (hermes --version · hermes acp). 이 heredoc 은 확장된다: 백틱 금지.
[ -n "\${ACPFAKE_SCRIPT:-}" ] || ACPFAKE_SCRIPT="\$(cat "$FAKEBIN/default-hermes.json")"
export ACPFAKE_SCRIPT
exec "$BIN/acpfake" "\$@"
SH
  chmod +x "$FAKEBIN"/*
  export FAKEBIN FAKEHOME
}
# fake_script ROLE [KIND] [EXTRA_JSON] → 그 역할의 ACPFAKE_SCRIPT(JSON 한 줄).
#   exec 스텝 하나가 fixtures/agent.sh ROLE 을 부른다. `known_sessions` 로 session/load 가 성공해
#   재개(resume)가 실물처럼 잡힌다. EXTRA_JSON 은 스크립트 최상위에 병합된다(steps 앞뒤 카드 등).
fake_script() {
  local role="$1" kind="${2:-claude}" extra="${3:-}"; [ -n "$extra" ] || extra='{}'
  local nomcp=false; [ "$kind" = hermes ] && nomcp=true
  jq -nc --arg k "$kind" --arg role "$role" --arg fix "$FIX" --argjson m "$FAKE_MODELS" --argjson nomcp "$nomcp" --argjson x "$extra" \
    '{kind:$k, models:$m, known_sessions:["sess-1"], no_mcp_capabilities:$nomcp,
      turns:[{steps:[{exec:("bash "+$fix+"/agent.sh "+$role)}]}]} + $x'
}
# fake_env ROLE [KIND] [EXTRA_JSON] → 프로파일 env JSON ({ACPFAKE_SCRIPT, ACPFAKE_RECORD, FAKE_*})
#   FAKE_OUT 은 agent.sh 가 자기 흔적(무엇을 했나)을 남기는 자리다.
fake_env() {
  local role="$1"
  mkdir -p "$E2E_OUT/fake-records"
  jq -nc --arg s "$(fake_script "$@")" --arg rec "$E2E_OUT/fake-records/$role.jsonl" --arg o "$E2E_OUT/fake-records" \
    '{ACPFAKE_SCRIPT:$s, ACPFAKE_RECORD:$rec, FAKE_OUT:$o}'
}
# create_agent_fake WS NAME ROLE KIND MODEL INSTRUCTIONS [ROLE_DESC] [SCRIPT_EXTRA_JSON] → agent id
#   RUNTIME=fake: 프로파일 env 에 대본을 싣는다. RUNTIME=real: p2 의 create_agent_kind 그대로.
create_agent_fake() {
  local ws="$1" name="$2" role="$3" kind="$4" model="$5" ins="$6" rd="${7:-$3 역할}" extra="${8:-}"; [ -n "$extra" ] || extra='{}'
  if [ "$RUNTIME" = fake ]; then
    local saved="$PROFILE_ENV"; PROFILE_ENV="$(fake_env "$name" "$kind" "$extra")"
    create_agent_kind "$ws" "$name" "$role" "$kind" "$model" "$ins" "$rd"; local rc=$?
    PROFILE_ENV="$saved"; return $rc
  else
    create_agent_kind "$ws" "$name" "$role" "$kind" "$model" "$ins" "$rd"
  fi
}
# daemon_run_p5 CONFIG LOG → pid. RUNTIME=fake 면 PATH 앞에 FAKEBIN, HOME 은 FAKEHOME.
# DAEMON_BIN 으로 다른 데몬 바이너리(예: #204 이전 아카이브 빌드)를 돌릴 수 있다 — S-66 전후 대조.
daemon_run_p5() {
  local cwd="$P5_TMP_ROOT/daemon-cwd"; mkdir -p "$cwd"
  local DAEMON="${DAEMON_BIN:-$BIN/daemon}"
  if [ "$RUNTIME" = fake ]; then
    [ -n "${FAKEBIN:-}" ] || fake_runtime_setup
    ( cd "$cwd" && export PATH="$FAKEBIN:$BIN:$(stable_path)" HOME="$FAKEHOME" COLAB_DAEMON_CONFIG="$1"; setsid_run "$2" "$DAEMON" run )
  else
    ( cd "$cwd" && export PATH="$BIN:$(stable_path)" COLAB_DAEMON_CONFIG="$1"; setsid_run "$2" "$DAEMON" run )
  fi
}
# daemon_pair_p5 CODE CONFIG WORKROOT CAPACITY [REPO...] — 페어링(HOME·PATH 는 daemon_run_p5 와 같게)
daemon_pair_p5() {
  if [ "$RUNTIME" = fake ]; then
    [ -n "${FAKEBIN:-}" ] || fake_runtime_setup
    # 서브셸 + export — 함수 호출 앞의 임시 대입은 bash 3.2 에서 자식 프로세스(데몬)까지 가지 않았다(실측).
    ( export PATH="$FAKEBIN:$BIN:$PATH" HOME="$FAKEHOME"; daemon_pair_p4 "$@" )
  else
    daemon_pair_p4 "$@"
  fi
}
# runtime_kinds RUNTIME_ID → 광고된 kind 목록(공백 구분)
runtime_kinds() { api_ok GET "/runtimes/$1" | jq -r '[.capabilities[]?.kind]|join(" ")'; }

# ── 공통 대기·판정 ───────────────────────────────────────────────────────────
wait_until() { # wait_until TIMEOUT "shell test"
  local dl=$(( $(date +%s) + $1 )); shift
  while [ "$(date +%s)" -lt "$dl" ]; do eval "$1" && return 0; sleep "${WAIT_TICK:-2}"; done
  return 1
}
# wait_step ID 설명 TIMEOUT "shell test" [TICK] — 단계별 대기를 **판정 행**으로(I-1, PR #206 리뷰 NN):
#   조건이 TIMEOUT 초 안에 참이 되면 PASS(걸린 초), 아니면 FAIL — 걸려 있는 대기가 "행이 아니라 단언" 이 된다.
#   72_ A2d 흔들림(CI, PR #249 attempt 1: got=2)의 자리: 3 lane 이 동시에 running 인 순간을 **폴링으로 잡고**
#   못 잡으면 여기서 FAIL 이다(대본 쪽 barrier 는 fixtures/agent.sh Researcher).
wait_step() {
  local id="$1" what="$2" timeout="$3" test="$4" tick="${5:-0.5}" t0 dl
  t0="$(date +%s)"; dl=$(( t0 + timeout ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    if eval "$test"; then chk "$id" "$what" yes "yes"; printf '    (%ss)\n' "$(( $(date +%s) - t0 ))" >&2; return 0; fi
    sleep "$tick"
  done
  chk "$id" "$what (timeout ${timeout}s)" yes "timeout"; return 1
}
sess_status() { psqlq "select status::text from work where room_id='$1'"; }
wait_quiet() { # 세션의 모든 task 가 멈출 때까지
  local dl=$(( $(date +%s) + ${2:-600} ))
  while [ "$(date +%s)" -lt "$dl" ]; do
    [ "$(psqlq "select count(*) from task where session_id='$1' and status in ('queued','dispatched','preparing','running')")" = 0 ] && return 0
    sleep 2
  done
  return 1
}
cnt() { local f="$1"; shift; local a=(); local x n; for x in "$@"; do a+=(-e "$x"); done
        n="$({ grep -c "${a[@]}" "$f" 2>/dev/null || true; } | head -1 | tr -d ' \n')"; printf '%s' "${n:-0}"; }
# chk_lt ID 설명 상한 실제(숫자, 소수 가능) — 실제 < 상한 이면 PASS
chk_lt() {
  if awk -v a="${4:-999999}" -v b="$3" 'BEGIN{exit !(a+0 < b+0)}'; then pass=$((pass+1)); printf '  ✓ %-56s %s (<%s)\n' "$2" "$4" "$3" >&2; printf '%s\t%s\tPASS\t%s\n' "$1" "$2" "$4" >> "$CHK"
  else fail=$((fail+1)); printf '  ✗ %-56s got=%s want<%s\n' "$2" "$4" "$3" >&2; printf '%s\t%s\tFAIL\tgot=%s want<%s\n' "$1" "$2" "$4" "$3" >> "$CHK"; fi
}
# 페이크 시나리오는 실기보다 훨씬 빠르다 — 대기 상한을 모드로 정한다.
if [ "$RUNTIME" = fake ]; then T_TURN="${T_TURN:-240}"; else T_TURN="${T_TURN:-900}"; fi

# 시나리오 A·C·D 의 지시문 — 페이크는 읽지 않지만(대본이 답한다) 실기 모드에서는 이것이 모델의 지시다.
# 저장소 밖의 무해한 주제(X-2) + §0-16 기본 문구.
P5_RULES="$P4_RULES"

# ── 시나리오 D (프로파일 2개, 프로파일별 env) ──────────────────────────────
# create_agent_2profiles_env WS NAME ROLE INS  K1 M1 ENV1_JSON  K2 M2 ENV2_JSON → agent id
# p2 의 create_agent_2profiles 와 같되 프로파일마다 env 가 다르다 — 페이크는 프로파일 env 로 대본을 받으므로
# "primary 는 실패하는 대본, spare 는 일하는 대본" 이 여기서 갈린다. 폴백 연결은 link_fallback(S-24 우회).
create_agent_2profiles_env() {
  local ws="$1" n="$2" r="$3" ins="$4" k1="$5" m1="$6" e1="$7" k2="$8" m2="$9" e2="${10}"
  api_ok POST "/workspaces/$ws/agents" "$(jq -nc --arg n "$n" --arg r "$r" --arg i "$ins" \
      --arg k1 "$k1" --arg m1 "$m1" --argjson e1 "$e1" --arg k2 "$k2" --arg m2 "$m2" --argjson e2 "$e2" \
    '{name:$n,role:$r,role_description:($r+" 역할"),instructions:$i,
      profiles:[{name:"primary",runtime_kind:$k1,model:$m1,is_default:true,env:$e1},
                {name:"spare",  runtime_kind:$k2,model:$m2,is_default:false,env:$e2}]}')" | jq -r .id
}
# fake_env_error ROLE KIND MESSAGE → 매 턴 JSON-RPC -32603 오류로 답하는 대본의 프로파일 env (재시도 가능한 실패 = other)
fake_env_error() {
  local role="$1" kind="$2" msg="$3"
  local extra; extra="$(jq -nc --arg m "$msg" '{turns:[{error:{code:-32603,message:$m}}]}')"
  fake_env "$role" "$kind" "$extra"
}

# Linux(CI) 에는 shasum 이 없을 수 있다(73_ 의 파일 지문).
command -v shasum >/dev/null 2>&1 || shasum() { sha1sum "$@"; }
