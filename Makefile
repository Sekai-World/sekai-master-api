APP_NAME=sekai-master-api
COMPOSE_FILE=deploy/compose/test-compose.yaml
COMPOSE_FILE_ABS := $(abspath $(COMPOSE_FILE))
WORKSPACE_DIR := $(abspath .)
APP_PORT ?= 18080
KEYCLOAK_PORT ?= 18081
GRAFANA_PORT ?= 3000
LOKI_PORT ?= 3100
LOKI_HOST ?= host.docker.internal
LOKI_PUSH_URL ?= http://$(LOKI_HOST):$(LOKI_PORT)/loki/api/v1/push
COMPOSE_HOST ?= host.docker.internal
DOCKER ?= docker
COMPOSE_CMD ?= $(shell if $(DOCKER) compose version >/dev/null 2>&1; then echo "$(DOCKER) compose"; elif command -v docker-compose >/dev/null 2>&1; then echo docker-compose; else echo "$(DOCKER) compose"; fi)
DEVCONTAINER ?= devcontainer
APP_ENV ?= development

.PHONY: run dev-watch test tidy format lint swagger migrate-up migrate-down dev-env-up dev-env-down dev-env-down-purge dev-env-logs test-env-up test-env-down test-env-down-purge test-env-logs keycloak-up keycloak-down keycloak-logs keycloak-token smoke admin-open dev-logs-ui devcontainer-up devcontainer-rebuild devcontainer-test

run:
	go run -buildvcs=false ./cmd/api

dev-watch:
	@AIR_CMD="air"; \
	if ! command -v air >/dev/null 2>&1; then \
		echo "[dev-watch] air not found, installing..."; \
		go install -buildvcs=false github.com/air-verse/air@latest; \
		AIR_CMD="$$(go env GOPATH)/bin/air"; \
	fi; \
	echo "[dev-watch] streaming logs to Loki at $(LOKI_PUSH_URL)"; \
	APP_ENV=development LOKI_PUSH_URL="$(LOKI_PUSH_URL)" $$AIR_CMD -c .air.toml

test:
	go test -buildvcs=false ./...

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
	go run -buildvcs=false github.com/swaggo/swag/cmd/swag@v1.8.12 init -g main.go -d cmd/api,internal/transport/http/handler,internal/domain/masterdata -o docs

migrate-up:
	@set -a; \
	if [ -f "./.env.$(APP_ENV)" ]; then . "./.env.$(APP_ENV)"; fi; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
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
	if [ -f "./.env.$(APP_ENV)" ]; then . "./.env.$(APP_ENV)"; fi; \
	if [ -f "./.env" ]; then . "./.env"; fi; \
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
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) up -d --build

dev-env-down:
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) down

dev-env-down-purge:
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) down -v

dev-env-logs:
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) logs -f

test-env-up: dev-env-up

test-env-down: dev-env-down

test-env-down-purge: dev-env-down-purge

test-env-logs: dev-env-logs

keycloak-up:
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) up -d keycloak

keycloak-down:
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) stop keycloak
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) rm -f keycloak

keycloak-logs:
	$(COMPOSE_CMD) -f $(COMPOSE_FILE_ABS) logs -f keycloak

keycloak-token:
	KEYCLOAK_PORT=$(KEYCLOAK_PORT) sh ./scripts/get-keycloak-token.sh

smoke:
	APP_PORT=$(APP_PORT) KEYCLOAK_PORT=$(KEYCLOAK_PORT) sh ./scripts/smoke.sh

admin-open:
	"$$BROWSER" http://localhost:$(APP_PORT)/admin/login

dev-logs-ui:
	"$$BROWSER" http://localhost:$(GRAFANA_PORT)

devcontainer-up:
	$(DEVCONTAINER) up --workspace-folder $(WORKSPACE_DIR)

devcontainer-rebuild:
	$(DEVCONTAINER) up --remove-existing-container --workspace-folder $(WORKSPACE_DIR)

devcontainer-test:
	$(DEVCONTAINER) exec --workspace-folder $(WORKSPACE_DIR) go test -buildvcs=false ./...
