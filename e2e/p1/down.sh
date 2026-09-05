#!/usr/bin/env bash
# e2e/p1/down.sh — up.sh 와 시나리오가 띄운 프로세스 종료(데몬·서버·웹). Postgres 컨테이너는 남긴다.
source "$(dirname "$0")/lib.sh"
for f in "$OUT"/daemon-*.pid "$OUT"/web.pid "$OUT"/server.pid; do
  [ -f "$f" ] || continue
  pid="$(cat "$f")"; kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true; rm -f "$f"
done
pkill -f 'claude-agent-acp' 2>/dev/null || true
ok "stopped"
