#!/bin/sh
set -eu

APP_DIR="/app"
OVERRIDE_FILE="${APP_DIR}/.env.development.local"
BAKED_FILE="${APP_DIR}/.env.development.local.baked"
HOST_APP_PORT="${DEV_HOST_APP_PORT:-8080}"

load_env_file() {
  file_path="$1"
  if [ -f "${file_path}" ]; then
    set -a
    # shellcheck disable=SC1090
    . "${file_path}"
    set +a
  fi
}

load_env_file "${APP_DIR}/.env"
load_env_file "${APP_DIR}/.env.development"
load_env_file "${APP_DIR}/.env.local"
load_env_file "${BAKED_FILE}"

issuer_base="${OIDC_ISSUER_URL:-${OIDC_INTERNAL_URL:-}}"
issuer_path=""
if [ -n "${issuer_base}" ]; then
  issuer_without_scheme="${issuer_base#*://}"
  if [ "${issuer_without_scheme}" != "${issuer_base}" ] && [ "${issuer_without_scheme#*/}" != "${issuer_without_scheme}" ]; then
    issuer_path="/${issuer_without_scheme#*/}"
  fi
fi

if [ -z "${issuer_path}" ]; then
  keycloak_realm="${KEYCLOAK_REALM:-sekai}"
  issuer_path="/realms/${keycloak_realm}"
fi

mkdir -p /app/tmp

{
  if [ -f "${BAKED_FILE}" ]; then
    cat "${BAKED_FILE}"
    printf '\n'
  fi
  printf 'APP_ENV=development\n'
  printf 'APP_PORT=8080\n'
  printf 'DATABASE_DRIVER=pgx\n'
  printf 'DATABASE_URL=postgres://postgres:postgres@postgres:5432/sekai?sslmode=disable\n'
  printf 'SQLITE_PATH=/app/tmp/dev.db\n'
  printf 'REDIS_ADDR=redis:6379\n'
  printf 'LOKI_PUSH_URL=http://loki:3100/loki/api/v1/push\n'
  printf 'OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318\n'
  printf 'OTEL_EXPORTER_OTLP_INSECURE=true\n'
  printf 'OIDC_INTERNAL_URL=http://keycloak:8080%s\n' "${issuer_path}"
  printf 'OIDC_REDIRECT_URL=http://localhost:%s/api/v1/admin/login/callback\n' "${HOST_APP_PORT}"
} > "${OVERRIDE_FILE}"

export APP_ENV=development
export APP_PORT=8080

exec /usr/local/bin/sekai-master-api
