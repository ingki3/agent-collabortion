#!/usr/bin/env bash
# e2e/p4/60_cli_artifact_diff.sh — T-C6: `colab artifact submit --type diff` 를 **실서버**에 한 번 재는 스모크.
#
# 왜 실서버인가. 목이 CLI 와 같은 표에서 나오면 스모크는 오답을 확인해 줄 뿐이다(C-4, #134).
# 그래서 진짜 서버·진짜 라우터·진짜 TaskToken 으로 multipart 4필드가 그대로 통과하는지 본다 —
# 이 작업이 새 필드를 만들지 않고 description 첫 줄과 diff 본문 주석 한 줄에 메타를 넣은 이유가
# openapi `submitArtifact` 의 본문이 {name,type,file,description} 뿐이기 때문이므로, 그 통과 여부는
# 계약 대조표가 아니라 서버가 답해야 한다.
#
# 에이전트 런타임은 띄우지 않는다. 필요한 것은 데몬의 HTTP 왕복뿐이라 daemon-protocol.md §2·§4 를
# curl 로 직접 친다(pair → claim → phase → finish 없이 종료). 모델 호출 0회.
#
# 대상 저장소는 **이 저장소가 아니라 /tmp 에 만든 임시 git 저장소**다(P4_TASKS §0-18): 워크트리
# 격리 e2e 가 자기 저장소를 대상으로 삼으면 X-2 의 worktree 판이 된다.
#
#   1. 전용 스택(Postgres :5449 + server :8104) — 다른 워커 스택에 손대지 않는다(P3_TASKS §0-13).
#   2. 임시 저장소: main 1커밋 → colab/S/frontend 브랜치에 커밋·staged·unstaged·untracked 각 1개.
#   3. `colab artifact submit --type diff` (cwd = 임시 워크트리, --file 없음)
#      → 201, artifact.type=diff, name=frontend.diff, description 첫 줄 `diff <b>@<c> vs main`.
#   4. 내려받은 본문: 첫 줄 `# colab-diff: …`, 세 변경이 다 있고 untracked 는 없다.
#      base 만 있는 새 클론에 `git apply` → 그대로 적용된다(E14-06 재적용의 전제).
#   5. 같은 워크트리에서 두 번째 제출 → **같은 이름 version 2** (FR-4.3, E16-B 5→6단계).
#   6. 변경 없는 워크트리 → exit 2 `empty_diff`, 아티팩트 행 증가 0.
#   7. 워크트리 밖은 읽지 않는다 — 저장소가 아닌 cwd 는 위로 올라가지 않고 거절한다.
#
# 사용: bash e2e/p4/60_cli_artifact_diff.sh          (끝나면 스택은 down)
#       KEEP_STACK=1 bash e2e/p4/60_cli_artifact_diff.sh
# 산출물: e2e/p4/out/60_*.{json,log,diff}, e2e/p4/out/60_checks.tsv

export SERVER_URL="${SERVER_URL:-http://localhost:8104}"
export PG_PORT="${PG_PORT:-5449}"
export PG_CONTAINER="${PG_CONTAINER:-colab-pg-c6}"
P4_DIR="$(cd "$(dirname "$0")" && pwd)"
export E2E_OUT="${E2E_OUT:-$P4_DIR/out}"
# p2/lib.sh 는 p1/lib.sh 를 부르고 API·psqlq·setsid_run 헬퍼를 준다. 웹은 쓰지 않는다.
source "$P4_DIR/../p2/lib.sh"

STAMP="$(date +%s)"
CHK="$OUT/60_checks.tsv"; printf 'id\twhat\tverdict\tvalue\n' > "$CHK"
pass=0; fail=0
chk() { # id 설명 기대 실제
  if [ "$3" = "$4" ]; then pass=$((pass+1)); printf '  \033[32m✓\033[0m %-56s %s\n' "$2" "$4" >&2; printf '%s\t%s\tPASS\t%s\n' "$1" "$2" "$4" >> "$CHK"
  else fail=$((fail+1)); printf '  \033[31m✗\033[0m %-56s got=%s want=%s\n' "$2" "$4" "$3" >&2; printf '%s\t%s\tFAIL\tgot=%s want=%s\n' "$1" "$2" "$4" "$3" >> "$CHK"; fi
}
dcurl() { # METHOD PATH [JSON] — 데몬 토큰(/v1/daemon 은 openapi 밖)
  local m="$1" p="$2" b="${3:-}"
  if [ -n "$b" ]; then
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $DTOK" -H 'Content-Type: application/json' -X "$m" "${SERVER_URL%/}$p" --data "$b"
  else
    curl -sS -w '\n%{http_code}' -H "Authorization: Bearer $DTOK" -X "$m" "${SERVER_URL%/}$p"
  fi
}
# 임시 저장소 전용 git — 사용자 전역 설정을 읽지 않아 어느 머신에서나 같은 결과가 나온다.
tgit() { git -C "$1" -c user.name=colab -c user.email=colab@example.com "${@:2}"; }

REPO=""
cleanup() {
  if [ -z "${KEEP_STACK:-}" ]; then
    [ -f "$OUT/60_server.pid" ] && { kill -TERM -- "-$(cat "$OUT/60_server.pid")" 2>/dev/null || true; }
    docker stop "$PG_CONTAINER" >/dev/null 2>&1 || true
  fi
  [ -n "$REPO" ] && rm -rf "$(dirname "$REPO")"
  return 0
}
trap cleanup EXIT

step "0. 전용 스택 — Postgres($PG_CONTAINER :$PG_PORT) + server $SERVER_URL (웹 없음)"
docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
  || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
for i in $(seq 1 30); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable"
(cd "$E2E_ROOT" && COLAB_DB_URL="$DB_URL" go run ./server/cmd/migrate 2>&1 | tail -1 >&2)
(cd "$E2E_ROOT" && make build >"$OUT/60_build.log" 2>&1) || die "make build 실패 (see $OUT/60_build.log)"
if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up"; else
  COLAB_DB_URL="$DB_URL" COLAB_SERVER_URL="$SERVER_URL" COLAB_SERVER_ADDR=":${SERVER_URL##*:}" \
    setsid_run "$OUT/60_server.log" "$BIN/server" > "$OUT/60_server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/60_server.log)"
  ok "server pid $(cat "$OUT/60_server.pid")"
fi

step "1. 임시 git 저장소 — 이 저장소가 아니다 (P4_TASKS §0-18)"
TMPROOT="$(mktemp -d "${TMPDIR:-/tmp}/colab-c6-XXXXXX")"
REPO="$TMPROOT/app"
mkdir -p "$REPO"
tgit "$REPO" init -q
tgit "$REPO" symbolic-ref HEAD refs/heads/main
printf 'one\n' > "$REPO/a.txt"
tgit "$REPO" add a.txt
tgit "$REPO" commit -qm base
BASE_COMMIT="$(tgit "$REPO" rev-parse HEAD)"
tgit "$REPO" checkout -q -b colab/S/frontend
printf 'one\ntwo committed\n' > "$REPO/a.txt"
tgit "$REPO" commit -qam "committed work"
printf 'staged work\n' > "$REPO/staged.txt"
tgit "$REPO" add staged.txt
printf 'one\ntwo committed\nthree unstaged\n' > "$REPO/a.txt"
printf 'build noise\n' > "$REPO/junk.log"
HEAD_SHORT="$(tgit "$REPO" rev-parse --short HEAD)"
ok "repo $REPO — branch colab/S/frontend @ $HEAD_SHORT (base main $BASE_COMMIT)"

step "2. 계정 · 워크스페이스 · 페어링 · 세션 · claim (진짜 TaskToken)"
signup "c6+$STAMP@example.com" "password123" "Director" >/dev/null
WS="$(create_workspace "C6 artifact diff $STAMP")"
read -r PAIRING PTOK <<<"$(create_pairing "$WS" | tr '\t' ' ')"
PAIR_OUT="$(curl -sS -H 'Content-Type: application/json' -X POST "${SERVER_URL%/}/v1/daemon/pair" \
  --data "$(jq -nc --arg c "$PTOK" '{pairing_code:$c,hostname:"e2e-c6",os:"darwin",daemon_version:"e2e"}')")"
RUNTIME="$(jq -r .runtime_id <<<"$PAIR_OUT")"; DTOK="$(jq -r .daemon_token <<<"$PAIR_OUT")"
[ -n "$RUNTIME" ] && [ "$RUNTIME" != null ] || die "pair 실패: $PAIR_OUT"
dcurl POST "/v1/daemon/runtimes/$RUNTIME/probe" "$(jq -nc \
  '{daemon_version:"e2e",hostname:"e2e-c6",capabilities:[{runtime_kind:"claude_code",present:true}],
    repos:[],workdir_root:"/tmp/c6",disk:{used_bytes:0},colab_cli:{present:true,version:"e2e"}}')" >/dev/null
AGENT="$(create_agent_p2 "$WS" Frontend engineer "$LEAD_MODEL" '화면을 구현한다' '프런트엔드')"
# 격리는 none 이다 — 이 스모크가 재는 것은 서버의 아티팩트 경로이고, 워크트리 준비는 T-D9(데몬)다.
# CLI 는 어차피 **자기 프로세스의 cwd** 만 본다(FR-6.1).
SESSION="$(create_session_p2 "$WS" "C6 diff 아티팩트" "$SCENARIO_GOAL" "$AGENT" "$RUNTIME" "$AGENT" "$AGENT")"
CLAIM=""
for i in $(seq 1 20); do
  CLAIM="$(dcurl POST "/v1/daemon/runtimes/$RUNTIME/claim" '{"capacity":1,"wait_ms":2000}' | sed '$d')"
  [ "$(jq -r '.tasks|length' <<<"$CLAIM")" -gt 0 ] && break
  sleep 1
done
echo "$CLAIM" > "$OUT/60_claim.json"
[ "$(jq -r '.tasks|length' <<<"$CLAIM")" -gt 0 ] || die "claim 이 task 를 주지 않았다: $CLAIM"
TASK="$(jq -r '.tasks[0].task.id' <<<"$CLAIM")"
ATTEMPT="$(jq -r '.tasks[0].task.attempt' <<<"$CLAIM")"
TOKEN="$(jq -r '.tasks[0].task_token' <<<"$CLAIM")"
LANE="$(jq -r '.tasks[0].task.lane_id' <<<"$CLAIM")"
dcurl POST "/v1/daemon/tasks/$TASK/attempts/$ATTEMPT/phase" "$(jq -nc --arg w "$REPO" '{phase:"preparing",pgid:0,workdir_path:$w}')" >/dev/null
dcurl POST "/v1/daemon/tasks/$TASK/attempts/$ATTEMPT/phase" "$(jq -nc --arg w "$REPO" '{phase:"running",pgid:0,workdir_path:$w}')"   >/dev/null
ok "session $SESSION · task $TASK attempt $ATTEMPT · token ${TOKEN:0:12}…"

# 데몬이 attempt 프로세스에 넣는 환경 (harness.md §2.1 / colab-cli.md §1).
# cwd 는 workdir — cli_wrapper(harness §10)도 같은 cwd 로 exec 하므로 위생화된 env 로도 같다.
colab_in_workdir() { (cd "$1" && env -i PATH="$PATH" HOME="$HOME" \
    COLAB_TASK_TOKEN="$TOKEN" COLAB_SERVER_URL="$SERVER_URL" \
    COLAB_TASK_ID="$TASK" COLAB_TASK_ATTEMPT="$ATTEMPT" COLAB_LANE_ID="$LANE" \
    COLAB_SESSION_ID="$SESSION" COLAB_AGENT_NAME=Frontend "$BIN/colab" "${@:2}"); }

step "3. colab artifact submit --type diff (cwd = 임시 워크트리, --file 없음)"
set +e
colab_in_workdir "$REPO" artifact submit --type diff --description '회원 탈퇴 화면' \
  > "$OUT/60_submit1.json" 2>"$OUT/60_submit1.err"
CODE=$?
set -e
chk C6-1 "submit 종료 코드" 0 "$CODE"
[ "$CODE" = 0 ] || { echo "--- stderr ---" >&2; cat "$OUT/60_submit1.err" >&2; }
ART="$(jq -r '.artifact_id // "-"' "$OUT/60_submit1.json" 2>/dev/null)"
chk C6-2 "artifact 행 1개 (type=diff, name, version)" "diff|frontend.diff|1" \
  "$(psqlq "select type || '|' || name || '|' || version from artifact where id='$ART'" 2>/dev/null)"
chk C6-3 "description 첫 줄이 계약 고정 문구" "diff colab/S/frontend@$HEAD_SHORT vs main" \
  "$(psqlq "select split_part(description, E'\n', 1) from artifact where id='$ART'" 2>/dev/null)"
chk C6-4 "사용자 --description 은 그 아래 줄" "회원 탈퇴 화면" \
  "$(psqlq "select split_part(description, E'\n', 2) from artifact where id='$ART'" 2>/dev/null)"
chk C6-5 "submitted_by_task_id = 이 task" "$TASK" \
  "$(psqlq "select coalesce(submitted_by_task_id::text,'-') from artifact where id='$ART'" 2>/dev/null)"

step "4. 내려받은 본문 — 주석 한 줄 + 세 변경, untracked 없음, git apply 통과"
DL="$OUT/60_downloaded.diff"; rm -f "$DL"
set +e
colab_in_workdir "$REPO" artifact get "$ART" --out "$DL" > "$OUT/60_get.json" 2>"$OUT/60_get.err"
GCODE=$?
set -e
chk C6-6 "artifact get 종료 코드" 0 "$GCODE"
chk C6-7 "본문 첫 줄 = # colab-diff: 메타" "# colab-diff: branch=colab/S/frontend base=main commit=$HEAD_SHORT" \
  "$(head -1 "$DL" 2>/dev/null)"
C1="$(grep -c '^+two committed$' "$DL" 2>/dev/null || true)"
C2="$(grep -c '^+three unstaged$' "$DL" 2>/dev/null || true)"
C3="$(grep -c '^+staged work$' "$DL" 2>/dev/null || true)"
chk C6-8 "커밋·staged·unstaged 세 변경이 한 패치에" 3 "$((C1 + C2 + C3))"
chk C6-9 "untracked 는 본문에 없다" 0 "$(grep -c 'junk.log' "$DL" 2>/dev/null || true)"
chk C6-10 "untracked 는 결과에서 알려 준다" "junk.log" \
  "$(jq -r '.diff.untracked_not_included | join(",")' "$OUT/60_submit1.json" 2>/dev/null)"
# E14-06 재적용의 전제: base 만 있는 새 워크트리에 그대로 붙는다(`#` 줄은 git apply 가 무시한다).
FRESH="$TMPROOT/fresh"
tgit "$TMPROOT" clone -q "$REPO" fresh >/dev/null 2>&1
tgit "$FRESH" checkout -q main
set +e
tgit "$FRESH" apply "$DL" >"$OUT/60_apply.log" 2>&1
APPLY=$?
set -e
chk C6-11 "새 클론에 git apply 성공" 0 "$APPLY"
chk C6-12 "적용 결과가 원본 워크트리와 같다" "same" \
  "$(if diff -q "$REPO/a.txt" "$FRESH/a.txt" >/dev/null 2>&1 && diff -q "$REPO/staged.txt" "$FRESH/staged.txt" >/dev/null 2>&1; then echo same; else echo differs; fi)"

step "5. 재진입 후 두 번째 제출 → 같은 이름 version 2 (FR-4.3, E16-B 5→6)"
printf 'one\ntwo committed\nthree unstaged\nfour after review\n' > "$REPO/a.txt"
set +e
colab_in_workdir "$REPO" artifact submit --type diff --description '리뷰 반영' \
  > "$OUT/60_submit2.json" 2>"$OUT/60_submit2.err"
CODE2=$?
set -e
chk C6-13 "두 번째 submit 종료 코드" 0 "$CODE2"
chk C6-14 "같은 이름 version 2" "2" \
  "$(psqlq "select version from artifact where session_id='$SESSION' and name='frontend.diff' order by version desc limit 1" 2>/dev/null)"
chk C6-15 "아티팩트 이름은 하나뿐(새 아티팩트 아님)" "1" \
  "$(psqlq "select count(distinct name) from artifact where session_id='$SESSION'" 2>/dev/null)"

step "6. 변경 없는 워크트리 → exit 2 empty_diff, 서버 요청 0"
BEFORE="$(psqlq "select count(*) from artifact where session_id='$SESSION'")"
tgit "$REPO" add -A
tgit "$REPO" commit -qm "everything"
set +e
colab_in_workdir "$REPO" artifact submit --type diff --base HEAD \
  > "$OUT/60_submit3.json" 2>"$OUT/60_submit3.err"
CODE3=$?
set -e
chk C6-16 "빈 diff 종료 코드 2" 2 "$CODE3"
chk C6-17 "코드 empty_diff" empty_diff "$(jq -r '.error.code // "-"' "$OUT/60_submit3.json" 2>/dev/null)"
chk C6-18 "아티팩트 행 증가 0" "$BEFORE" "$(psqlq "select count(*) from artifact where session_id='$SESSION'")"

step "7. 워크트리 밖은 읽지 않는다 — git 은 cwd 안에서만 돈다"
# 저장소가 아닌 디렉토리에서는 위로 올라가 다른 저장소를 찾지 않는다: 거절이지 남의 diff 가 아니다.
set +e
colab_in_workdir "$TMPROOT" artifact submit --type diff --name outside-probe \
  > "$OUT/60_submit4.json" 2>"$OUT/60_submit4.err"
CODE4=$?
set -e
chk C6-19 "저장소 밖 cwd → exit 2 not_a_git_repo" "2|not_a_git_repo" \
  "$CODE4|$(jq -r '.error.code // "-"' "$OUT/60_submit4.json" 2>/dev/null)"
chk C6-20 "그 시도로 만들어진 아티팩트 0" "0" \
  "$(psqlq "select count(*) from artifact where session_id='$SESSION' and name='outside-probe'" 2>/dev/null)"
chk C6-21 "제출된 본문에 이 저장소의 경로 0" 0 "$(grep -c 'cli/internal' "$DL" 2>/dev/null || true)"

printf '\n'
column -t -s $'\t' "$CHK" >&2
if [ "$fail" = 0 ]; then printf '\033[32mREAL SMOKE PASS\033[0m — %d/%d (server %s)\n' "$pass" "$((pass+fail))" "$SERVER_URL"
else printf '\033[31mREAL SMOKE FAIL\033[0m — %d건 실패 (%d 통과)\n' "$fail" "$pass"; fi
exit "$fail"
