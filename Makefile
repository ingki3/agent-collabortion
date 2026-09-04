# Colab monorepo — PLAN.md §3 P0-a "프로젝트 골격"
#   make dev    Postgres + server + daemon + web 한 번에
#   make test   Go 모듈 전부 + web typecheck
#   make build  바이너리 3개 (bin/)

GO_MODULES := contracts server daemon cli
PG_CONTAINER := colab-pg
PG_PORT ?= 5432
PG_URL ?= postgres://colab:colab@localhost:$(PG_PORT)/colab?sslmode=disable

.PHONY: dev db db-stop build test vet lint web-install web-dev clean

## dev: run everything for local development (Ctrl-C stops all)
dev: db build
	@echo ">> server :8080 | web :3000 | daemon (skeleton) | postgres :$(PG_PORT)"
	@trap 'kill 0' INT TERM; \
	  COLAB_DB_URL="$(PG_URL)" ./bin/server & \
	  ./bin/daemon version; \
	  (cd web && npm run dev) & \
	  wait

## db: start Postgres 16 in Docker (idempotent)
db:
	@docker inspect $(PG_CONTAINER) >/dev/null 2>&1 \
	  && docker start $(PG_CONTAINER) >/dev/null \
	  || docker run -d --name $(PG_CONTAINER) \
	       -e POSTGRES_USER=colab -e POSTGRES_PASSWORD=colab -e POSTGRES_DB=colab \
	       -p $(PG_PORT):5432 postgres:16-alpine >/dev/null
	@echo ">> postgres ready: $(PG_URL)"

db-stop:
	@docker stop $(PG_CONTAINER) >/dev/null 2>&1 || true

build:
	@mkdir -p bin
	go build -o bin/server ./server/cmd/server
	go build -o bin/daemon ./daemon/cmd/daemon
	go build -o bin/colab  ./cli/cmd/colab

test: vet
	@for m in $(GO_MODULES); do (cd $$m && go test ./...) || exit 1; done
	@cd web && npm run typecheck

vet:
	@for m in $(GO_MODULES); do (cd $$m && go vet ./...) || exit 1; done

lint: vet

web-install:
	cd web && npm install

web-dev:
	cd web && npm run dev

clean: db-stop
	rm -rf bin web/.next
