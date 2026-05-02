APP_NAME=sekai-master-api
COMPOSE_FILE=deploy/compose/dev-compose.yaml
COMPOSE_FILE_ABS := $(abspath $(COMPOSE_FILE))
COMPOSE_PROJECT_ARG = -p "$${COMPOSE_PROJECT_NAME:-$(APP_NAME)}"
WORKSPACE_DIR := $(abspath .)
APP_PORT ?= 8080
KEYCLOAK_PORT ?= 18081
GRAFANA_PORT ?= 13000
LOKI_PORT ?= 3100
PROMETHEUS_PORT ?= 9090
TEMPO_PORT ?= 3200
OTEL_COLLECTOR_GRPC_PORT ?= 4317
OTEL_COLLECTOR_HTTP_PORT ?= 4318
LOKI_HOST ?= host.docker.internal
LOKI_PUSH_URL ?= http://$(LOKI_HOST):$(LOKI_PORT)/loki/api/v1/push
COMPOSE_HOST ?= host.docker.internal
OTEL_ENABLED ?= true
OTEL_SERVICE_NAME ?= $(APP_NAME)
OTEL_SERVICE_VERSION ?=
OTEL_EXPORTER_OTLP_ENDPOINT ?= http://$(COMPOSE_HOST):$(OTEL_COLLECTOR_HTTP_PORT)
OTEL_EXPORTER_OTLP_INSECURE ?= true
OTEL_METRIC_EXPORT_INTERVAL ?= 10000
DEV_APP_IMAGE ?= sekai/$(APP_NAME)-dev:local
DEV_APP_CONTAINER ?= $(APP_NAME)-dev
DEV_APP_VOLUME ?= $(APP_NAME)-dev-data
DEV_APP_NETWORK ?= sekai-dev
DEV_APP_INTERNAL_PORT ?= 8080
DEV_GRAFANA_URL ?= http://grafana.sekai-master-api.orb.local
DEV_MASTER_DATA_AUTO_SYNC ?= false
DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC ?= false
DOCKER ?= docker
COMPOSE_CMD ?= $(shell if $(DOCKER) compose version >/dev/null 2>&1; then echo "$(DOCKER) compose"; elif command -v docker-compose >/dev/null 2>&1; then echo docker-compose; else echo "$(DOCKER) compose"; fi)
APP_ENV ?= development
GO_DOCKER_IMAGE ?= golang:1.26.2-alpine3.23
GO_DOCKER_WORKDIR ?= /src
GO_DOCKER_MOD_CACHE_VOLUME ?= $(APP_NAME)-go-mod-cache
GO_DOCKER_BUILD_CACHE_VOLUME ?= $(APP_NAME)-go-build-cache
GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME ?= $(APP_NAME)-go-toolchain-cache

.PHONY: run dev dev-down dev-logs test test-docker tidy format lint swagger migrate-up migrate-down dev-env-up dev-env-down dev-env-down-purge dev-env-logs keycloak-up keycloak-down keycloak-logs keycloak-token smoke admin-open dev-logs-ui

run:
	go run -buildvcs=false ./cmd/api

dev:
	@\
	echo "[dev] ensuring dependency stack is running"; \
	$(MAKE) dev-env-up; \
	echo "[dev] building app image $(DEV_APP_IMAGE) with buildx"; \
	$(DOCKER) buildx build --load -f deploy/compose/app/Dockerfile -t "$(DEV_APP_IMAGE)" .; \
	echo "[dev] recreating container $(DEV_APP_CONTAINER) on network $(DEV_APP_NETWORK)"; \
	$(DOCKER) rm -f "$(DEV_APP_CONTAINER)" >/dev/null 2>&1 || true; \
	$(DOCKER) volume create "$(DEV_APP_VOLUME)" >/dev/null; \
	$(DOCKER) run -d \
		--name "$(DEV_APP_CONTAINER)" \
		--restart unless-stopped \
		--network "$(DEV_APP_NETWORK)" \
		-e DEV_HOST_APP_PORT="$(APP_PORT)" \
		-e DEV_MASTER_DATA_AUTO_SYNC="$(DEV_MASTER_DATA_AUTO_SYNC)" \
		-e DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC="$(DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC)" \
		-p "$(APP_PORT):$(DEV_APP_INTERNAL_PORT)" \
		-v "$(DEV_APP_VOLUME):/app/tmp" \
		"$(DEV_APP_IMAGE)" >/dev/null; \
	echo "[dev] app listening on http://localhost:$(APP_PORT)"; \
	echo "[dev] MASTER_DATA_AUTO_SYNC=$(DEV_MASTER_DATA_AUTO_SYNC)"; \
	echo "[dev] MASTER_DATA_RECOVER_INTERRUPTED_SYNC=$(DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC)"; \
	echo "[dev] logs: make dev-logs"

dev-down:
	@$(DOCKER) rm -f "$(DEV_APP_CONTAINER)" >/dev/null 2>&1 || true

dev-logs:
	@$(DOCKER) logs -f "$(DEV_APP_CONTAINER)"

test:
	@if command -v go >/dev/null 2>&1; then \
		go test -buildvcs=false ./...; \
	else \
		echo "[test] local go not found, using docker with cached go module/build volumes"; \
		$(MAKE) test-docker; \
	fi

test-docker:
	@WORKSPACE_DIR="$(WORKSPACE_DIR)" \
	DOCKER_BIN="$(DOCKER)" \
	GO_DOCKER_IMAGE="$(GO_DOCKER_IMAGE)" \
	GO_DOCKER_WORKDIR="$(GO_DOCKER_WORKDIR)" \
	GO_DOCKER_MOD_CACHE_VOLUME="$(GO_DOCKER_MOD_CACHE_VOLUME)" \
	GO_DOCKER_BUILD_CACHE_VOLUME="$(GO_DOCKER_BUILD_CACHE_VOLUME)" \
	GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME="$(GO_DOCKER_GOTOOLCHAIN_CACHE_VOLUME)" \
	sh ./scripts/docker-go.sh go test -buildvcs=false ./...

tidy:
	go mod tidy

format:
	gofmt -w $$(find . -type f -name '*.go' -not -path './vendor/*')

lint:
	@UNFORMATTED="$$(gofmt -l $$(find . -type f -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "The following files are not gofmt-formatted:"; \
		echo "$$UNFORMATTED"; \
		exit 1; \
	fi
	go vet ./...

swagger:
	go run -buildvcs=false github.com/swaggo/swag/cmd/swag@v1.8.12 init -g main.go -d cmd/api,internal/transport/http/handlers/admin,internal/transport/http/handlers/cards,internal/transport/http/handlers/events,internal/transport/http/handlers/musics,internal/transport/http/handlers/shared,internal/transport/http/handlers/system,internal/transport/http/handlers/virtuallives,internal/domain/masterdata -o docs

migrate-up:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.$(APP_ENV)" ]; then . "./.env.$(APP_ENV)"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.$(APP_ENV).local" ]; then . "./.env.$(APP_ENV).local"; fi; \
	set +a; \
	DRIVER="$${DATABASE_DRIVER}"; \
	if [ -z "$$DRIVER" ]; then \
		if [ "$(APP_ENV)" = "development" ] || [ "$(APP_ENV)" = "dev" ]; then \
			DRIVER="sqlite"; \
		else \
			DRIVER="pgx"; \
		fi; \
	fi; \
	if [ "$$DRIVER" = "sqlite" ]; then \
		DIALECT="sqlite3"; \
		DSN="$${SQLITE_PATH:-./tmp/dev.db}"; \
	else \
		DIALECT="postgres"; \
		DSN="$${DATABASE_URL:-postgres://sekai:sekai@$(COMPOSE_HOST):5432/sekai?sslmode=disable}"; \
	fi; \
	echo "[migrate-up] APP_ENV=$(APP_ENV) DRIVER=$$DRIVER DIALECT=$$DIALECT"; \
	go run -buildvcs=false github.com/pressly/goose/v3/cmd/goose@latest -dir internal/storage/migrations $$DIALECT "$$DSN" up

migrate-down:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.$(APP_ENV)" ]; then . "./.env.$(APP_ENV)"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.$(APP_ENV).local" ]; then . "./.env.$(APP_ENV).local"; fi; \
	set +a; \
	DRIVER="$${DATABASE_DRIVER}"; \
	if [ -z "$$DRIVER" ]; then \
		if [ "$(APP_ENV)" = "development" ] || [ "$(APP_ENV)" = "dev" ]; then \
			DRIVER="sqlite"; \
		else \
			DRIVER="pgx"; \
		fi; \
	fi; \
	if [ "$$DRIVER" = "sqlite" ]; then \
		DIALECT="sqlite3"; \
		DSN="$${SQLITE_PATH:-./tmp/dev.db}"; \
	else \
		DIALECT="postgres"; \
		DSN="$${DATABASE_URL:-postgres://sekai:sekai@$(COMPOSE_HOST):5432/sekai?sslmode=disable}"; \
	fi; \
	echo "[migrate-down] APP_ENV=$(APP_ENV) DRIVER=$$DRIVER DIALECT=$$DIALECT"; \
	go run -buildvcs=false github.com/pressly/goose/v3/cmd/goose@latest -dir internal/storage/migrations $$DIALECT "$$DSN" down

dev-env-up:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) up -d --build --remove-orphans

dev-env-down:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(MAKE) dev-down; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) down --remove-orphans

dev-env-down-purge:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(MAKE) dev-down; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) down -v --remove-orphans

dev-env-logs:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) logs -f

keycloak-up:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) up -d --build keycloak

keycloak-down:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) stop keycloak; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) rm -f keycloak

keycloak-logs:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	$(COMPOSE_CMD) $(COMPOSE_PROJECT_ARG) -f $(COMPOSE_FILE_ABS) logs -f keycloak

keycloak-token:
	@set -a; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
	if [ -f "./.env.development" ]; then . "./.env.development"; fi; \
	if [ -f "./.env.local" ]; then . "./.env.local"; fi; \
	if [ -f "./.env.development.local" ]; then . "./.env.development.local"; fi; \
	set +a; \
	KEYCLOAK_PORT="$(KEYCLOAK_PORT)" sh ./scripts/get-keycloak-token.sh

smoke:
	APP_PORT=$(APP_PORT) sh ./scripts/smoke.sh

admin-open:
	"$$BROWSER" http://localhost:$(APP_PORT)/admin/login

dev-logs-ui:
	"$$BROWSER" "$(DEV_GRAFANA_URL)"
