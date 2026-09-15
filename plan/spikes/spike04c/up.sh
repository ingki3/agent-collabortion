#!/usr/bin/env bash
# plan/spikes/spike04c/up.sh — 전용 Postgres + server. 웹은 없다.
source "$(dirname "$0")/lib.sh"
cd "$ROOT"
step "Postgres (docker $PG_CONTAINER :$PG_PORT) + migrate"
docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
  || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
for i in $(seq 1 40); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" go run ./server/cmd/migrate 2>&1 | tail -1 >&2
step "build bin/server bin/daemon bin/colab ($(git rev-parse --short HEAD))"
make build >/dev/null
if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up"; else
  step "server $SERVER_URL"
  COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" COLAB_SERVER_URL="$SERVER_URL" \
    COLAB_SERVER_ADDR=":${SERVER_URL##*:}" setsid_run "$OUT/server.log" "$BIN/server" > "$OUT/server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/server.log)"
  ok "server pid $(cat "$OUT/server.pid")"
fi
