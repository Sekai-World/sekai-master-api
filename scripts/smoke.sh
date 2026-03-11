#!/usr/bin/env sh

set -eu

COMPOSE_HOST="${COMPOSE_HOST:-localhost}"
in_container="0"
if [ -f "/.dockerenv" ] || [ -f "/run/.containerenv" ]; then
  in_container="1"
elif grep -Eq '(docker|containerd|kubepods)' /proc/1/cgroup 2>/dev/null; then
  in_container="1"
fi

if [ "${COMPOSE_HOST}" = "localhost" ] && [ "${in_container}" = "1" ]; then
  if getent hosts host.docker.internal >/dev/null 2>&1; then
    COMPOSE_HOST="host.docker.internal"
  elif getent hosts host.containers.internal >/dev/null 2>&1; then
    COMPOSE_HOST="host.containers.internal"
  fi
fi

APP_PORT="${APP_PORT:-8080}"
APP_ENV="${APP_ENV:-test}"
DATABASE_URL="${DATABASE_URL:-postgres://sekai:sekai@${COMPOSE_HOST}:5432/sekai?sslmode=disable}"
KEYCLOAK_PORT="${KEYCLOAK_PORT:-8081}"
KEYCLOAK_ISSUER_URL="${KEYCLOAK_ISSUER_URL:-http://${COMPOSE_HOST}:${KEYCLOAK_PORT}/realms/sekai}"
KEYCLOAK_AUDIENCE="${KEYCLOAK_AUDIENCE:-sekai-api}"
KEYCLOAK_BASE_URL="${KEYCLOAK_BASE_URL:-${KEYCLOAK_ISSUER_URL%/realms/*}}"

API_PID=""

COMPOSE_CMD="docker compose"
if command -v docker-compose >/dev/null 2>&1; then
  COMPOSE_CMD="docker-compose"
fi

cleanup() {
  if [ -n "${API_PID}" ]; then
    kill "${API_PID}" >/dev/null 2>&1 || true
    wait "${API_PID}" 2>/dev/null || true
  fi
  make test-env-down >/dev/null 2>&1 || true
}

trap cleanup EXIT INT TERM

echo "[smoke] starting dependencies"
make test-env-up >/dev/null

echo "[smoke] waiting keycloak ready by checking issuer endpoint: ${KEYCLOAK_ISSUER_URL}/.well-known/openid-configuration"
attempt=0
until curl -fsS "${KEYCLOAK_ISSUER_URL}/.well-known/openid-configuration" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 60 ]; then
    echo "[smoke] keycloak not ready in time"
    ${COMPOSE_CMD} -f deploy/compose/test-compose.yaml ps keycloak || true
    ${COMPOSE_CMD} -f deploy/compose/test-compose.yaml logs --tail=80 keycloak || true
    exit 1
  fi
  sleep 2
done

echo "[smoke] starting api"
if curl -fsS "http://localhost:${APP_PORT}/api/v1/health" >/dev/null 2>&1; then
  echo "[smoke] api port ${APP_PORT} is already in use, stop existing process or set APP_PORT"
  exit 1
fi

APP_ENV="${APP_ENV}" \
DATABASE_URL="${DATABASE_URL}" \
KEYCLOAK_ISSUER_URL="${KEYCLOAK_ISSUER_URL}" \
KEYCLOAK_AUDIENCE="${KEYCLOAK_AUDIENCE}" \
APP_PORT="${APP_PORT}" \
go run ./cmd/api >/tmp/sekai-smoke-api.log 2>&1 &
API_PID=$!

echo "[smoke] waiting api ready"
attempt=0
until curl -fsS "http://localhost:${APP_PORT}/api/v1/health" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "${attempt}" -ge 60 ]; then
    echo "[smoke] api not ready in time"
    cat /tmp/sekai-smoke-api.log || true
    exit 1
  fi
  sleep 1
done

echo "[smoke] requesting keycloak token"
token_json="$(KEYCLOAK_BASE_URL="${KEYCLOAK_BASE_URL}" sh ./scripts/get-keycloak-token.sh)"
access_token="$(printf '%s' "${token_json}" | sed -n 's/.*"access_token":"\([^"]*\)".*/\1/p')"

if [ -z "${access_token}" ]; then
  echo "[smoke] failed to parse access token"
  printf '%s\n' "${token_json}"
  exit 1
fi

echo "[smoke] calling protected endpoint"
profile_response="$(curl -fsS -H "Authorization: Bearer ${access_token}" "http://localhost:${APP_PORT}/api/v1/admin/profile")"

echo "[smoke] success"
printf '%s\n' "${profile_response}"
