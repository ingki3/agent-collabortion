#!/usr/bin/env bash
# e2e/p5/up.sh — P5 통합 스택(전용 Postgres + server [+ web])을 백그라운드로 띄운다.
# `e2e/p4/up.sh` 와 같은 구성이고 포트만 다르다. 두 가지가 더 있다:
#   - **`bin/acpfake`** 도 빌드한다(RUNTIME=fake 의 런타임 자리, lib.sh 머리 주석).
#   - **CI(PG_EXTERNAL=1)** 에서는 docker 를 부르지 않는다 — Postgres 는 service 컨테이너이고
#     `PSQL_URL` 로 닿는다. 마이그레이션은 같은 URL 로 돌린다.
source "$(dirname "$0")/lib_i5.sh"
cd "$E2E_ROOT"
if [ "${PG_EXTERNAL:-0}" = 1 ]; then
  step "Postgres (external $PG_URL) + migrate"
  for i in $(seq 1 40); do psql "$PG_URL" -qtA -c 'select 1' >/dev/null 2>&1 && break; sleep 1; done
else
  step "Postgres (docker $PG_CONTAINER :$PG_PORT) + migrate"
  docker inspect "$PG_CONTAINER" >/dev/null 2>&1 && docker start "$PG_CONTAINER" >/dev/null \
    || docker run -d --name "$PG_CONTAINER" -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab -p "$PG_PORT":5432 postgres:16-alpine >/dev/null
  for i in $(seq 1 40); do docker exec "$PG_CONTAINER" pg_isready -U colab >/dev/null 2>&1 && break; sleep 1; done
fi
COLAB_DB_URL="$PG_URL" go run ./server/cmd/migrate 2>&1 | tail -1 >&2

step "build bin/server bin/daemon bin/colab bin/acpfake (HEAD $(git rev-parse --short HEAD))"
make build >/dev/null
go build -o bin/acpfake ./daemon/cmd/acpfake
for b in server daemon colab acpfake; do
  printf '  %-8s %s\n' "$b" "$(date -r "$BIN/$b" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || stat -c %y "$BIN/$b")" >&2
done

WEB_PORT="${WEB_URL##*:}"
if curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1; then ok "server already up ($SERVER_URL)"; else
  step "server $SERVER_URL"
  # 요약은 키 없음 폴백(행 조립)으로 간다 — CI 에 ANTHROPIC_API_KEY 가 없고, 있어도 여기서는 쓰지 않는다.
  unset ANTHROPIC_API_KEY
  COLAB_DB_URL="$PG_URL" COLAB_SERVER_URL="$SERVER_URL" COLAB_WEB_URL="$WEB_URL" \
    COLAB_SERVER_ADDR=":${SERVER_URL##*:}" \
    setsid_run "$OUT/server.log" "$BIN/server" > "$OUT/server.pid"
  for i in $(seq 1 60); do curl -fsS "$SERVER_URL/healthz" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "$SERVER_URL/healthz" >/dev/null || die "server did not start (see $OUT/server.log)"
  ok "server pid $(cat "$OUT/server.pid")"
fi
if [ "${WITH_WEB:-1}" = 0 ]; then ok "web 생략 (WITH_WEB=0)"; exit 0; fi
WEB_MODE="${WEB_MODE:-build}"
if curl -fsS -o /dev/null "$WEB_URL/login" 2>/dev/null; then ok "web already up ($WEB_URL)"; else
  step "web $WEB_URL (next $WEB_MODE, /api/v1 → $SERVER_URL)"
  [ -d web/node_modules ] || (cd web && npm ci --no-audit --no-fund >/dev/null)
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
