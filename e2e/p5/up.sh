#!/usr/bin/env bash
# e2e/p5/up.sh — P5 스모크 스택(전용 Postgres + server)을 백그라운드로 띄운다.
# `e2e/p4/up.sh` 와 같은 구성이고 포트만 다르다(lib.sh 주석). 바이너리는 **매번 다시 빌드**한다.
# 웹은 기본으로 띄우지 않는다(WITH_WEB=1 이면 p4 와 같은 방식으로 next build).
source "$(dirname "$0")/lib.sh"
cd "$E2E_ROOT"
step "Postgres (docker $PG_CONTAINER :$PG_PORT) + migrate"
docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
  || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
for i in $(seq 1 40); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" go run ./server/cmd/migrate 2>&1 | tail -1 >&2

step "build bin/server (HEAD $(git rev-parse --short HEAD))"
mkdir -p "$BIN"
(cd server && go build -o "$BIN/server" ./cmd/server)
printf '  %-8s %s\n' server "$(date -r "$BIN/server" '+%Y-%m-%d %H:%M:%S')" >&2

if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up ($SERVER_URL)"; else
  step "server $SERVER_URL"
  COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" COLAB_SERVER_URL="$SERVER_URL" COLAB_WEB_URL="$WEB_URL" \
    COLAB_SERVER_ADDR=":${SERVER_URL##*:}" \
    setsid_run "$OUT/server.log" "$BIN/server" > "$OUT/server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/server.log)"
  ok "server pid $(cat "$OUT/server.pid")"
fi
[ "${WITH_WEB:-0}" = 1 ] || { ok "web 생략 (WITH_WEB=0 기본)"; exit 0; }
WEB_PORT="${WEB_URL##*:}"
step "web $WEB_URL (next build, /api/v1 → $SERVER_URL)"
[ -d web/node_modules ] || (cd web && npm install --no-audit --no-fund >/dev/null)
(cd web && COLAB_SERVER_URL="$SERVER_URL" npx next build >"$OUT/web-build.log" 2>&1) || die "next build 실패 (see $OUT/web-build.log)"
(cd web && COLAB_SERVER_URL="$SERVER_URL" setsid_run "$OUT/web.log" npx next start -p "$WEB_PORT") > "$OUT/web.pid"
for i in $(seq 1 240); do curl -fsS -o /dev/null "$WEB_URL/login" 2>/dev/null && break; sleep 1; done
curl -fsS -o /dev/null "$WEB_URL/login" || die "web did not start (see $OUT/web.log)"
ok "web pid $(cat "$OUT/web.pid")"
