APP_NAME=sekai-master-api
COMPOSE_FILE=deploy/compose/test-compose.yaml
APP_PORT ?= 18080
KEYCLOAK_PORT ?= 18081
APP_ENV ?= development

.PHONY: run dev-watch test tidy format lint swagger migrate-up migrate-down dev-env-up dev-env-down dev-env-logs test-env-up test-env-down test-env-logs keycloak-up keycloak-down keycloak-logs keycloak-token smoke admin-open

run:
	go run ./cmd/api

dev-watch:
	@AIR_CMD="air"; \
	if ! command -v air >/dev/null 2>&1; then \
		echo "[dev-watch] air not found, installing..."; \
		go install github.com/air-verse/air@latest; \
		AIR_CMD="$$(go env GOPATH)/bin/air"; \
	fi; \
	APP_ENV=development $$AIR_CMD -c .air.toml

test:
	go test ./...

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
	go run github.com/swaggo/swag/cmd/swag@v1.8.12 init -g main.go -d cmd/api,internal/transport/http/handler,internal/domain/masterdata -o docs

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
		DSN="$${DATABASE_URL:-postgres://sekai:sekai@localhost:5432/sekai?sslmode=disable}"; \
	fi; \
	echo "[migrate-up] APP_ENV=$(APP_ENV) DRIVER=$$DRIVER DIALECT=$$DIALECT"; \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir internal/storage/migrations $$DIALECT "$$DSN" up

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
		DSN="$${DATABASE_URL:-postgres://sekai:sekai@localhost:5432/sekai?sslmode=disable}"; \
	fi; \
	echo "[migrate-down] APP_ENV=$(APP_ENV) DRIVER=$$DRIVER DIALECT=$$DIALECT"; \
	go run github.com/pressly/goose/v3/cmd/goose@latest -dir internal/storage/migrations $$DIALECT "$$DSN" down

dev-env-up:
	docker compose -f $(COMPOSE_FILE) up -d --build

dev-env-down:
	docker compose -f $(COMPOSE_FILE) down -v

dev-env-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

test-env-up: dev-env-up

test-env-down: dev-env-down

test-env-logs: dev-env-logs

keycloak-up:
	docker compose -f $(COMPOSE_FILE) up -d keycloak

keycloak-down:
	docker compose -f $(COMPOSE_FILE) stop keycloak
	docker compose -f $(COMPOSE_FILE) rm -f keycloak

keycloak-logs:
	docker compose -f $(COMPOSE_FILE) logs -f keycloak

keycloak-token:
	KEYCLOAK_PORT=$(KEYCLOAK_PORT) sh ./scripts/get-keycloak-token.sh

smoke:
	APP_PORT=$(APP_PORT) KEYCLOAK_PORT=$(KEYCLOAK_PORT) sh ./scripts/smoke.sh

admin-open:
	"$$BROWSER" http://localhost:$(APP_PORT)/admin/login
