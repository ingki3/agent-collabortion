#!/usr/bin/env bash
# e2e/p4/up.sh — P4 통합 스택(전용 Postgres + server + web)을 백그라운드로 띄운다.
# `e2e/p3/up.sh` 와 같은 구성이고 포트만 다르다(lib.sh 주석). 바이너리는 **매번 다시 빌드**한다 —
# 오래된 bin/daemon 으로 돌리면 P3 명령(cancel·gc)이 조용히 무시되고 결함처럼 보인다.
source "$(dirname "$0")/lib.sh"
cd "$E2E_ROOT"
step "Postgres (docker $PG_CONTAINER :$PG_PORT) + migrate"
docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
  || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
for i in $(seq 1 40); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" go run ./server/cmd/migrate 2>&1 | tail -1 >&2

step "build bin/server bin/daemon bin/colab (dev HEAD $(git rev-parse --short HEAD))"
make build >/dev/null
for b in server daemon colab; do
  printf '  %-8s %s\n' "$b" "$(date -r "$BIN/$b" '+%Y-%m-%d %H:%M:%S')" >&2
done

WEB_PORT="${WEB_URL##*:}"
if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up ($SERVER_URL)"; else
  step "server $SERVER_URL"
  COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" COLAB_SERVER_URL="$SERVER_URL" COLAB_WEB_URL="$WEB_URL" \
    COLAB_SERVER_ADDR=":${SERVER_URL##*:}" \
    setsid_run "$OUT/server.log" "$BIN/server" > "$OUT/server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/server.log)"
  ok "server pid $(cat "$OUT/server.pid")"
fi
# WEB_MODE=build(기본) — `next dev` 는 요청 중에 .next 를 다시 쓰다가 라우트를 통째로 죽인다(P2 실측).
WEB_MODE="${WEB_MODE:-build}"
if [ "${WITH_WEB:-1}" = 0 ]; then ok "web 생략 (WITH_WEB=0)"; exit 0; fi
if curl -fsS -o /dev/null "$WEB_URL/login" 2>/dev/null; then ok "web already up ($WEB_URL)"; else
  step "web $WEB_URL (next $WEB_MODE, /api/v1 → $SERVER_URL)"
  [ -d web/node_modules ] || (cd web && npm install --no-audit --no-fund >/dev/null)
  if [ "$WEB_MODE" = build ]; then
    (cd web && COLAB_SERVER_URL="$SERVER_URL" npx next build >"$OUT/web-build.log" 2>&1) || die "next build 실패 (see $OUT/web-build.log)"
    (cd web && COLAB_SERVER_URL="$SERVER_URL" setsid_run "$OUT/web.log" npx next start -p "$WEB_PORT") > "$OUT/web.pid"
  else
    (cd web && COLAB_SERVER_URL="$SERVER_URL" setsid_run "$OUT/web.log" npx next dev -p "$WEB_PORT") > "$OUT/web.pid"
  fi
  for i in $(seq 1 240); do curl -fsS -o /dev/null "$WEB_URL/login" 2>/dev/null && break; sleep 1; done
  curl -fsS -o /dev/null "$WEB_URL/login" || die "web did not start (see $OUT/web.log)"
  ok "web pid $(cat "$OUT/web.pid") ($WEB_MODE)"
fi
