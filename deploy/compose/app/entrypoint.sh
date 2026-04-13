#!/bin/sh
set -eu

APP_DIR="/app"
OVERRIDE_FILE="${APP_DIR}/.env.development.local"
BAKED_FILE="${APP_DIR}/.env.development.local.baked"
HOST_APP_PORT="${DEV_HOST_APP_PORT:-8080}"
MASTER_DATA_AUTO_SYNC_OVERRIDE="${DEV_MASTER_DATA_AUTO_SYNC:-false}"
MASTER_DATA_RECOVER_INTERRUPTED_SYNC_OVERRIDE="${DEV_MASTER_DATA_RECOVER_INTERRUPTED_SYNC:-false}"

read_dotenv_value() {
  file_path="$1"
  key="$2"

  if [ ! -f "${file_path}" ]; then
    return 1
  fi

  awk -F= -v key="${key}" '
    $0 ~ "^[[:space:]]*" key "=" {
      sub(/^[[:space:]]*[^=]+=/, "", $0)
      gsub(/\r$/, "", $0)
      print $0
      exit
    }
  ' "${file_path}"
}

lookup_baked_value() {
  key="$1"

  for file_path in "${BAKED_FILE}" "${APP_DIR}/.env.local" "${APP_DIR}/.env.development" "${APP_DIR}/.env"; do
    value="$(read_dotenv_value "${file_path}" "${key}" || true)"
    if [ -n "${value}" ]; then
      printf '%s\n' "${value}"
      return 0
    fi
  done

  return 1
}

issuer_base="$(lookup_baked_value OIDC_ISSUER_URL || lookup_baked_value OIDC_INTERNAL_URL || true)"
issuer_path=""
if [ -n "${issuer_base}" ]; then
  issuer_without_scheme="${issuer_base#*://}"
  if [ "${issuer_without_scheme}" != "${issuer_base}" ] && [ "${issuer_without_scheme#*/}" != "${issuer_without_scheme}" ]; then
    issuer_path="/${issuer_without_scheme#*/}"
  fi
fi

if [ -z "${issuer_path}" ]; then
  keycloak_realm="$(lookup_baked_value KEYCLOAK_REALM || true)"
  if [ -z "${keycloak_realm}" ]; then
    keycloak_realm="sekai"
  fi
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
  printf 'MASTER_DATA_AUTO_SYNC=%s\n' "${MASTER_DATA_AUTO_SYNC_OVERRIDE}"
  printf 'MASTER_DATA_RECOVER_INTERRUPTED_SYNC=%s\n' "${MASTER_DATA_RECOVER_INTERRUPTED_SYNC_OVERRIDE}"
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
