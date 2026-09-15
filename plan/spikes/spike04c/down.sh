#!/usr/bin/env bash
# plan/spikes/spike04c/down.sh — pid 파일만으로 종료(§0-10). Postgres 컨테이너는 남긴다.
source "$(dirname "$0")/lib.sh"
for f in "$OUT"/daemon*.pid "$OUT"/server.pid; do
  [ -f "$f" ] || continue
  pid="$(cat "$f")"; kill -TERM -- "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true; rm -f "$f"
done
ok "stopped"
