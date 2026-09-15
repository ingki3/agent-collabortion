#!/usr/bin/env bash
# e2e/p1/down.sh — up.sh 와 시나리오가 띄운 프로세스 종료(데몬·서버·웹). Postgres 컨테이너는 남긴다.
source "$(dirname "$0")/lib.sh"
for f in "$OUT"/daemon-*.pid "$OUT"/web.pid "$OUT"/server.pid; do
  [ -f "$f" ] || continue
  pid="$(cat "$f")"; kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true; rm -f "$f"
done
# 남은 어댑터는 **이 실행이 기록한 pgid** 로만 정리한다. `pkill -f claude-agent-acp` 는 경로를 보지 않아
# 같은 머신의 다른 워크스페이스가 돌리는 런타임까지 죽인다(2026-09-06 다른 워커의 `pkill -f bin/server` 로
# 이 스택의 서버가 죽는 사고가 났다 — 종료는 pid 파일·pgid·포트로만).
for f in "$OUT"/work-*/.colab/attempts/*.json; do
  [ -f "$f" ] || continue
  pgid="$(jq -r '.pgid // empty' "$f" 2>/dev/null)"
  [ -n "$pgid" ] && kill -TERM -- "-$pgid" 2>/dev/null || true
done
ok "stopped"
