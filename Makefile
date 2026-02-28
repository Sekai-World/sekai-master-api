APP_NAME=sekai-master-api
COMPOSE_FILE=deploy/compose/test-compose.yaml
APP_PORT ?= 18080
KEYCLOAK_PORT ?= 18081

.PHONY: run test tidy dev-env-up dev-env-down dev-env-logs test-env-up test-env-down test-env-logs keycloak-up keycloak-down keycloak-logs keycloak-token smoke admin-open

run:
	go run ./cmd/api

test:
	go test ./...

tidy:
	go mod tidy

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
