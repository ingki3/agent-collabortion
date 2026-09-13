#!/usr/bin/env bash
# e2e/p5/down.sh — up.sh 와 스크립트가 띄운 프로세스 종료(pid·pgid 만, §0-10). Postgres 컨테이너는 남긴다.
source "$(dirname "$0")/lib.sh"
for f in "$OUT"/daemon-*.pid "$OUT"/web.pid "$OUT"/server.pid "$OUT"/*-sse.pid; do
  [ -f "$f" ] || continue
  pid="$(cat "$f")"; kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true; rm -f "$f"
done
ok "stopped"
