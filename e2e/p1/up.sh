#!/usr/bin/env bash
# e2e/p1/up.sh — `make dev` 와 같은 구성(Postgres + server :8080 + web :3000)을 백그라운드로 띄운다.
# make dev 는 포그라운드(trap kill 0)라 스크립트에서 다루기 어려워 같은 명령을 개별로 실행한다. 데몬은 시나리오가 띄운다.
source "$(dirname "$0")/lib.sh"
cd "$E2E_ROOT"
step "Postgres (docker $PG_CONTAINER :$PG_PORT) + migrate"
# make db 와 같은 컨테이너 구성(postgres:16-alpine, colab/colab) 을 E2E 전용 이름으로
docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
  || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
for i in $(seq 1 30); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" go run ./server/cmd/migrate 2>&1 | tail -1 >&2
step "build bin/server bin/daemon bin/colab"
make build >/dev/null
if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up"; else
  step "server $SERVER_URL"
  # COLAB_WEB_URL: 초대 링크가 사람이 여는 웹 오리진(:3000)을 가리키게 한다(S-5, PR #33).
  # `make dev` 는 아직 이 값을 넘기지 않아 기본 폴백(서버 URL :8080)이 쓰인다 — Makefile 은 이 작업 범위 밖이라 여기서만 맞춘다.
  COLAB_DB_URL="postgres://colab:colab@localhost:$PG_PORT/colab?sslmode=disable" COLAB_SERVER_URL="$SERVER_URL" COLAB_WEB_URL="$WEB_URL" \
    setsid_run "$OUT/server.log" "$BIN/server" > "$OUT/server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/server.log)"
  ok "server pid $(cat "$OUT/server.pid")"
fi
if curl -fsS -o /dev/null "$WEB_URL/login" 2>/dev/null; then ok "web already up"; else
  step "web $WEB_URL (next dev, /api/v1 → $SERVER_URL)"
  [ -d web/node_modules ] || (cd web && npm install --no-audit --no-fund >/dev/null)
  (cd web && COLAB_SERVER_URL="$SERVER_URL" setsid_run "$OUT/web.log" npx next dev -p 3000) > "$OUT/web.pid"
  for i in $(seq 1 120); do curl -fsS -o /dev/null "$WEB_URL/login" 2>/dev/null && break; sleep 1; done
  curl -fsS -o /dev/null "$WEB_URL/login" || die "web did not start (see $OUT/web.log)"
  ok "web pid $(cat "$OUT/web.pid")"
fi
