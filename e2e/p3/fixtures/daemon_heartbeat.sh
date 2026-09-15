#!/usr/bin/env bash
# e2e/p3/fixtures/daemon_heartbeat.sh — **데몬 대역 픽스처**: 데몬이 보내야 할 heartbeat 를 대신 보낸다.
#
# 사용: daemon_heartbeat.sh <daemon.json> <task_id> <attempt> <input_tokens> <output_tokens> <cost_usd> <estimated>
# 출력: HTTP 코드<TAB>응답 본문(commands 포함)
#
# **왜 필요한가**(G6 실측, 결함 — 보고서 §결함): 데몬의 `acp.Runner.recordUsage` 는 `session/prompt`
# 응답에서만 호출되므로 **턴 중** heartbeat 의 `usage` 는 언제나 0 이고, 서버 `daemonHeartbeat` 의
# 가드(`InputTokens>0||OutputTokens>0||CostUSD>0`)가 거짓이라 `enforceBudgetFor` 가 호출되지 않는다.
# `enforceBudgetFor` 는 `tasks.Finish` 에서도 호출되지 않아 사후 강제도 없다. 그래서 FR-7.3 의
# "턴 중 강제" 는 실기에서 한 번도 발동한 적이 없다.
#
# 이 픽스처는 **계약(daemon-protocol §4.2)의 와이어 그대로** usage 를 실어 보낸다 — 서버가 하는 일은
# 실제 데몬이 보냈을 때와 완전히 같다. 즉 재는 것은 서버의 강제 경로이고, 대역으로 채운 것은
# "데몬이 턴 중에 숫자를 올린다" 한 가지뿐이다. 구현 코드는 건드리지 않는다.
set -euo pipefail
CFG="$1"; TASK="$2"; ATTEMPT="$3"; IN="$4"; OUT_T="$5"; COST="$6"; EST="${7:-false}"
TOKEN="$(jq -r .daemon_token "$CFG")"
BASE="${SERVER_URL:-http://localhost:8100}"
BODY="$(jq -nc --argjson i "$IN" --argjson o "$OUT_T" --argjson c "$COST" --argjson e "$EST" \
  '{usage:{input_tokens:$i,output_tokens:$o,cache_read_tokens:0,cache_write_tokens:0,cost_usd:$c,estimated:$e},last_seq:0}')"
RES="$(curl -sS -w '\n%{http_code}' -X POST "$BASE/v1/daemon/tasks/$TASK/attempts/$ATTEMPT/heartbeat" \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$BODY")"
printf '%s\t%s' "$(tail -n1 <<<"$RES")" "$(sed '$d' <<<"$RES" | tr -d '\n')"
